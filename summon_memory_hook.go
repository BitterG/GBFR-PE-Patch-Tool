package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unsafe"
)

const (
	summonMemoryHookSize       = uintptr(5)
	summonMemoryCaveDataOffset = uintptr(0x40)
	summonMemoryOriginalOffset = uintptr(17)
)

var summonMemoryHookOriginal = []byte{0x48, 0x8D, 0x54, 0x24, 0x28} // lea rdx,[rsp+0x28]
// 前五字节设为通配符，使扫描既可定位原始指令，也可识别已由 CT 或本工具
// Hook 的指令入口；其后指令序列仍足以在当前模块中唯一定位。
var summonMemorySelectedPattern = []byte{0, 0, 0, 0, 0, 0x48, 0x89, 0, 0xE8, 0, 0, 0, 0, 0x48, 0x83, 0, 0, 0, 0, 0x0F, 0x84}
var summonMemorySelectedMask = []bool{false, false, false, false, false, true, true, false, true, false, false, false, false, true, true, false, false, false, false, true, true}

func (a *App) summonSelectedAddress() (uintptr, error) {
	if err := a.ensureGameProcess(); err != nil {
		return 0, err
	}
	if a.summonMemoryHookAddr == 0 {
		// 纯 AOB 定位（模式前 5 字节通配，兼容已 hook 状态），游戏版本更新后自动适配。
		// 历史已知位置：2.0.2 = 0x3F1FB1B，2.0.4 = 0x3F20ABB。
		x, e := a.scanPatternUnique(summonMemorySelectedPattern, summonMemorySelectedMask, "当前选中召唤石特征")
		if e != nil {
			return 0, e
		}
		a.summonMemoryHookAddr = x
	}
	cur := make([]byte, summonMemoryHookSize)
	if e := readProcessMemory(a.hProcess, a.summonMemoryHookAddr, unsafe.Pointer(&cur[0]), uintptr(len(cur))); e != nil {
		return 0, e
	}
	if cur[0] == 0xE9 {
		// 即使工具上次异常退出，也可识别它自己的代码洞并恢复捕获槽；只有
		// 代码洞布局不符合本工具签名时才将其视为外部 Hook。
		cave := relJumpTarget(a.summonMemoryHookAddr, cur)
		prologue := make([]byte, 24)
		if e := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&prologue[0]), uintptr(len(prologue))); e != nil {
			return 0, fmt.Errorf("读取现有召唤石 Hook 失败: %w", e)
		}
		if bytes.Equal(prologue[19:24], summonMemoryHookOriginal) && prologue[0] == 0x50 && prologue[1] == 0x41 && prologue[2] == 0x52 && prologue[3] == 0x49 && prologue[4] == 0xBA && prologue[13] == 0x49 && prologue[14] == 0x89 && prologue[15] == 0x02 && prologue[16] == 0x41 && prologue[17] == 0x5A && prologue[18] == 0x58 {
			a.summonMemoryCaveAddr = cave
			a.summonMemoryPointerAddr = uintptr(binary.LittleEndian.Uint64(prologue[5:13]))
			a.summonMemoryOriginal = append([]byte(nil), summonMemoryHookOriginal...)
		} else if a.summonMemoryCaveAddr == 0 || len(a.summonMemoryOriginal) != int(summonMemoryHookSize) || cave != a.summonMemoryCaveAddr {
			return 0, fmt.Errorf("检测到其他程序已安装召唤石 Hook，请先关闭或禁用 Cheat Engine 的“当前选中的召唤石”后重试")
		}
	} else {
		if string(cur) != string(summonMemoryHookOriginal) {
			return 0, fmt.Errorf("召唤石 Hook 指令异常: %s", bytesToHex(cur))
		}
		cave, e := virtualAllocRemoteNear(a.hProcess, a.summonMemoryHookAddr, 0x1000)
		if e != nil {
			return 0, e
		}
		code := []byte{0x50, 0x41, 0x52, 0x49, 0xBA}
		code = binary.LittleEndian.AppendUint64(code, uint64(cave+summonMemoryCaveDataOffset))
		code = append(code, 0x49, 0x89, 0x02, 0x41, 0x5A, 0x58) // preserve rax and r10
		code = append(code, cur...)
		j, e := makeRelJump(cave+uintptr(len(code)), a.summonMemoryHookAddr+summonMemoryHookSize, 5)
		if e != nil {
			return 0, e
		}
		code = append(code, j...)
		for len(code) < int(summonMemoryCaveDataOffset)+8 {
			code = append(code, 0)
		}
		if e = writeCodeMemory(a.hProcess, cave, code); e != nil {
			return 0, e
		}
		patch, e := makeRelJump(a.summonMemoryHookAddr, cave, 5)
		if e != nil {
			return 0, e
		}
		if e = writeCodeMemory(a.hProcess, a.summonMemoryHookAddr, patch); e != nil {
			return 0, e
		}
		a.summonMemoryCaveAddr = cave
		a.summonMemoryPointerAddr = cave + summonMemoryCaveDataOffset
		a.summonMemoryOriginal = append([]byte(nil), cur...)
	}
	if a.summonMemoryPointerAddr == 0 {
		return 0, fmt.Errorf("未初始化召唤石指针槽")
	}
	var p uintptr
	if e := readProcessMemory(a.hProcess, a.summonMemoryPointerAddr, unsafe.Pointer(&p), unsafe.Sizeof(p)); e != nil {
		return 0, e
	}
	if p == 0 {
		return 0, fmt.Errorf("请在游戏内重新选中一颗召唤石后刷新")
	}
	return p, nil
}
func (a *App) releaseSummonMemoryHook() error {
	if a.hProcess == 0 || a.summonMemoryHookAddr == 0 || len(a.summonMemoryOriginal) != int(summonMemoryHookSize) {
		return nil
	}
	if err := writeCodeMemory(a.hProcess, a.summonMemoryHookAddr, a.summonMemoryOriginal); err != nil {
		return fmt.Errorf("恢复召唤石 Hook 原始指令失败: %w", err)
	}
	a.summonMemoryHookAddr = 0
	a.summonMemoryCaveAddr = 0
	a.summonMemoryPointerAddr = 0
	a.summonMemoryOriginal = nil
	return nil
}
