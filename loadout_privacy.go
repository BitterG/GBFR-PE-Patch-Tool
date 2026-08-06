//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

// ── 联机配装隐私：阻止 relink-logs 类工具采集我方配装 ──
//
// 原理（2026-08 逆向）：
//   relink-logs 采集的是"对方进程内存中我的 player record"，数据源头是
//   我方向网络发送的 compact profile blob。发送路径：
//     loadout → sub_14079B400(序列化器, RVA 0x79B400) → 0x2FC 字节 blob
//     → sub_1403C6DC0 成员列表打包 → 网络 → 对方 record（NETWORK mirror）
//   本 hook 在序列化器返回后把输出 blob 的"配装区"清零，只保留身份区
//   （角色 id / 等级 / 名字），使对方解包后 sigil/summon/weapon/觉醒/
//   skillboard 全为空，relink-logs 的 read_snapshot_sigils /
//   read_record_summons 会丢弃 0 与 0x887AE0B0 哨兵槽位。
//
// 序列化器反编译（v2.0.3, 0x14079B400）写入的 blob 字段布局：
//   a1+0x000..0x006  : 记录头（保留）
//   a1+0x006..0x1A8  : skillboard 节点等级（hashmap 查找写入）→ 配装
//   a1+0x1A8..0x25C  : 武器状态/觉醒/技能区（← loadout+0x3228..0x3429）→ 配装
//   a1+0x25C..0x2A4  : 召唤石区（← loadout+0x33DC..0x3429）→ 配装
//   a1+0x2A4..0x2FF  : party/charid/等级/名字 strcpy（身份，保留）
// 清空范围 = [0x06, 0x2A4)，见 profileBlobClearStart/End。

const (
	// loadoutPrivacyRVA 是 loadout→profile 序列化器 sub_14079B400 在 v2.0.3 的 RVA。
	loadoutPrivacyRVA = uintptr(0x79B400)

	// 序列化器入口前 5 字节：push r14; push rsi; push rdi; push rbx。
	loadoutPrivacyOrig = "\x41\x56\x56\x57\x53"

	// 输出 blob 中配装区（清零）与身份区（保留）的边界。实测校准用。
	profileBlobClearStart = 0x06
	profileBlobClearEnd   = 0x2A4

	// cave 布局偏移。
	loadoutPrivacyCaveDataOffset = uintptr(0x100) // a1 指针数据槽
	loadoutPrivacyTrampolineOff  = uintptr(0x200) // trampoline（重放 5 字节 + jmp 回原函数+5）
)

// loadoutPrivacyIsHook 判断目标地址当前是否已被本工具安装跳转。
func loadoutPrivacyIsHook(buf []byte) bool {
	return len(buf) == len(loadoutPrivacyOrig) && buf[0] == 0xE9
}

func (a *App) installLoadoutPrivacyHook() error {
	addr := a.moduleBase + loadoutPrivacyRVA
	cur := make([]byte, len(loadoutPrivacyOrig))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&cur[0]), uintptr(len(cur))); err != nil {
		return fmt.Errorf("读取配装序列化器入口失败: %w", err)
	}
	if loadoutPrivacyIsHook(cur) {
		if a.loadoutPrivacyCaveAddr != 0 {
			return nil // 已安装
		}
		return fmt.Errorf("检测到其他程序已在此地址安装跳转，请先恢复")
	}
	if string(cur) != loadoutPrivacyOrig {
		return fmt.Errorf("配装序列化器入口字节异常(可能版本不匹配): %s", bytesToHex(cur))
	}

	const caveSize = 0x300
	caveAddr, err := virtualAllocRemoteNear(a.hProcess, addr, caveSize)
	if err != nil {
		return fmt.Errorf("分配配装序列化器代码洞失败: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = virtualFreeRemote(a.hProcess, caveAddr)
		}
	}()

	trampolineAddr := caveAddr + loadoutPrivacyTrampolineOff
	dataSlot := caveAddr + loadoutPrivacyCaveDataOffset

	// cave 主逻辑（逐段构造，RIP 相对位移基于每条指令的实际地址）：
	//   sub rsp, 8                      ; 栈对齐，使 trampoline 入口 rsp≡8(mod16)
	//   mov [dataSlot], rcx             ; 保存 a1（48 89 0D rel32）
	//   mov rax, trampolineAddr         ; 48 B8 imm64
	//   call rax                        ; 完整执行原序列化器（经 trampoline）
	//   mov rcx, [dataSlot]             ; 取回 a1（48 8B 0D rel32）
	//   lea rdx, [rcx+clearStart]       ; 配装区起点
	//   mov r8d, clearLen               ; 长度
	//   xor r9d, r9d
	// loop:
	//   mov [rdx], r9b
	//   inc rdx
	//   dec r8d
	//   jnz loop
	//   add rsp, 8
	//   ret
	clearLen := profileBlobClearEnd - profileBlobClearStart
	var cave []byte

	// sub rsp, 8
	cave = append(cave, 0x48, 0x83, 0xEC, 0x08)
	// mov [rip+disp], rcx —— 指令起始 = caveAddr+4（sub rsp 之后），长度 7，disp 相对下一条
	storeStart := uintptr(len(cave))
	cave = append(cave, 0x48, 0x89, 0x0D)
	disp := int64(dataSlot) - int64(caveAddr+storeStart+7)
	cave = binary.LittleEndian.AppendUint32(cave, uint32(int32(disp)))
	// mov rax, imm64
	cave = append(cave, 0x48, 0xB8)
	cave = binary.LittleEndian.AppendUint64(cave, uint64(trampolineAddr))
	// call rax
	cave = append(cave, 0xFF, 0xD0)
	// mov rcx, [rip+disp] —— 指令起始 = 当前 len(cave)
	loadStart := uintptr(len(cave))
	cave = append(cave, 0x48, 0x8B, 0x0D)
	disp = int64(dataSlot) - int64(caveAddr+loadStart+7)
	cave = binary.LittleEndian.AppendUint32(cave, uint32(int32(disp)))
	// lea rdx, [rcx+clearStart]
	cave = append(cave, 0x48, 0x8D, 0x51, byte(profileBlobClearStart))
	// mov r8d, clearLen
	cave = append(cave, 0x41, 0xB8)
	cave = binary.LittleEndian.AppendUint32(cave, uint32(clearLen))
	// xor r9d, r9d
	cave = append(cave, 0x45, 0x31, 0xC9)
	// loop: mov [rdx], r9b
	loopStart := len(cave)
	cave = append(cave, 0x44, 0x88, 0x0A)
	// inc rdx
	cave = append(cave, 0x48, 0xFF, 0xC2)
	// dec r8d
	cave = append(cave, 0x41, 0xFF, 0xC8)
	// jnz loop —— rel8 = loopStart - (len(cave)+2)
	jnzRel := loopStart - (len(cave) + 2)
	cave = append(cave, 0x75, byte(int8(jnzRel)))
	// add rsp, 8
	cave = append(cave, 0x48, 0x83, 0xC4, 0x08)
	// ret
	cave = append(cave, 0xC3)
	// padding 到 trampoline
	for len(cave) < int(loadoutPrivacyTrampolineOff) {
		cave = append(cave, 0x90)
	}
	// trampoline：重放被覆盖的 5 字节 + jmp 回原函数+5
	tramp := []byte(loadoutPrivacyOrig)
	jmp, err := makeRelJump(caveAddr+loadoutPrivacyTrampolineOff+uintptr(len(tramp)), addr+uintptr(len(loadoutPrivacyOrig)), 5)
	if err != nil {
		return fmt.Errorf("生成配装序列化器 trampoline 跳转失败: %w", err)
	}
	tramp = append(tramp, jmp...)
	cave = append(cave, tramp...)
	for len(cave) < int(loadoutPrivacyCaveDataOffset)+8 {
		cave = append(cave, 0x90)
	}
	// 数据槽初始 0（a1 指针由 cave 内 mov [dataSlot] 写入）
	cave = append(cave, make([]byte, 8)...)

	if err := writeCodeMemory(a.hProcess, caveAddr, cave); err != nil {
		return fmt.Errorf("写入配装序列化器代码洞失败: %w", err)
	}
	patch, err := makeRelJump(addr, caveAddr, len(loadoutPrivacyOrig))
	if err != nil {
		return fmt.Errorf("生成配装序列化器入口跳转失败: %w", err)
	}
	if err := writeCodeMemory(a.hProcess, addr, patch); err != nil {
		return fmt.Errorf("写入配装序列化器入口跳转失败: %w", err)
	}
	a.loadoutPrivacyCaveAddr = caveAddr
	cleanup = false
	return nil
}

func (a *App) releaseLoadoutPrivacyHook() error {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return nil
	}
	addr := a.moduleBase + loadoutPrivacyRVA
	cur := make([]byte, len(loadoutPrivacyOrig))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&cur[0]), uintptr(len(cur))); err != nil {
		return fmt.Errorf("读取配装序列化器入口失败: %w", err)
	}
	if string(cur) == loadoutPrivacyOrig {
		return nil
	}
	if !loadoutPrivacyIsHook(cur) {
		return fmt.Errorf("配装序列化器入口字节异常: %s", bytesToHex(cur))
	}
	if err := writeCodeMemory(a.hProcess, addr, []byte(loadoutPrivacyOrig)); err != nil {
		return fmt.Errorf("恢复配装序列化器入口失败: %w", err)
	}
	if a.loadoutPrivacyCaveAddr != 0 {
		if err := virtualFreeRemote(a.hProcess, a.loadoutPrivacyCaveAddr); err != nil {
			return fmt.Errorf("释放配装序列化器代码洞失败: %w", err)
		}
		a.loadoutPrivacyCaveAddr = 0
	}
	return nil
}

func (a *App) readLoadoutPrivacyStatus() (LoadoutPrivacyStatus, error) {
	addr := a.moduleBase + loadoutPrivacyRVA
	buf := make([]byte, len(loadoutPrivacyOrig))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return LoadoutPrivacyStatus{}, fmt.Errorf("读取配装序列化器入口失败: %w", err)
	}
	enabled := loadoutPrivacyIsHook(buf) && a.loadoutPrivacyCaveAddr != 0
	if loadoutPrivacyIsHook(buf) && a.loadoutPrivacyCaveAddr == 0 {
		return LoadoutPrivacyStatus{}, fmt.Errorf("检测到其他程序已在此地址安装跳转，请先恢复")
	}
	return LoadoutPrivacyStatus{
		RVA:          uint64(loadoutPrivacyRVA),
		Enabled:      enabled,
		CurrentBytes: bytesToHex(buf),
		ClearRange:   fmt.Sprintf("0x%X..0x%X", profileBlobClearStart, profileBlobClearEnd),
	}, nil
}

// LoadoutPrivacyGetStatus 查询配装隐私 hook 状态。
func (a *App) LoadoutPrivacyGetStatus() (LoadoutPrivacyStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return LoadoutPrivacyStatus{}, err
	}
	return a.readLoadoutPrivacyStatus()
}

// LoadoutPrivacySetEnabled 开启/关闭配装隐私 hook。
func (a *App) LoadoutPrivacySetEnabled(enabled bool) (LoadoutPrivacyStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return LoadoutPrivacyStatus{}, err
	}
	if enabled {
		if err := a.installLoadoutPrivacyHook(); err != nil {
			return LoadoutPrivacyStatus{}, err
		}
	} else if err := a.releaseLoadoutPrivacyHook(); err != nil {
		return LoadoutPrivacyStatus{}, err
	}
	return a.readLoadoutPrivacyStatus()
}
