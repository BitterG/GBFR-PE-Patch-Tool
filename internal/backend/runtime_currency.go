package backend

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unsafe"
)

const (
	currencyHookSize         = 5
	currencyCaveMarkerOffset = 0x30
	currencyCaveDataOffset   = uintptr(0x40)
)

var currencyCaveMarker = [...]byte{'G', 'B', 'F', 'R', 'C', 'U', 'R', '1'}

var currencyInstallRemoteCodeHook = installRemoteCodeHook

// This DLC 2.0.2 instruction captures RCX and reads the resource fields at
// +30/+34/+98/+9C. Wildcards cover relocation- and register-dependent bytes.
var currencyCapturePattern = []byte{
	0xBA, 0, 0, 0, 0,
	0x41, 0x89, 0,
	0xE8, 0, 0, 0, 0,
	0x48, 0x8B, 0, 0, 0, 0, 0,
	0x8B, 0, 0, 0, 0, 0,
	0x83,
}

var currencyCaptureMask = []bool{
	true, false, false, false, false,
	true, true, false,
	true, false, false, false, false,
	true, true, false, false, false, false, false,
	true, false, false, false, false, false,
	true,
}

func (a *App) currencyRoot() (uintptr, error) {
	if err := a.ensureCurrencyHook(); err != nil {
		return 0, err
	}
	var root uintptr
	if err := readProcessMemory(a.hProcess, a.currencyCaveAddr+currencyCaveDataOffset, unsafe.Pointer(&root), unsafe.Sizeof(root)); err != nil {
		return 0, fmt.Errorf("读取实时资源指针失败: %w", err)
	}
	if root == 0 {
		return 0, fmt.Errorf("实时资源定位已启用；请在游戏内打开主菜单或让金币/MSP发生一次刷新，然后点击“刷新货币”")
	}
	if err := a.validateCurrencyRoot(root); err != nil {
		return 0, err
	}
	return root, nil
}

func (a *App) validateCurrencyRoot(root uintptr) error {
	for _, def := range currencyDefs {
		var value int32
		if err := readProcessMemory(a.hProcess, root+def.Offset, unsafe.Pointer(&value), unsafe.Sizeof(value)); err != nil {
			return fmt.Errorf("实时资源结构校验失败（%s）: %w", def.Name, err)
		}
		if value < 0 {
			return fmt.Errorf("实时资源结构校验失败：%s 读取到异常负值 %d，已拒绝写入", def.Name, value)
		}
	}
	return nil
}

func (a *App) ensureCurrencyHook() error {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return fmt.Errorf("未连接游戏进程")
	}
	if a.currencyHookAddr == 0 {
		addr, err := a.scanPatternUnique(currencyCapturePattern, currencyCaptureMask, "DLC 2.0.2 玩家资源结构")
		if err != nil {
			return err
		}
		a.currencyHookAddr = addr
	}
	current := make([]byte, currencyHookSize)
	if err := readProcessMemory(a.hProcess, a.currencyHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return fmt.Errorf("读取实时资源定位指令失败: %w", err)
	}
	if isCurrencyCaptureJump(current) {
		if a.currencyCaveAddr == 0 {
			return fmt.Errorf("检测到未知来源的资源定位 Hook；请完全退出游戏后重试")
		}
		if len(a.currencyOriginal) != currencyHookSize || !isCurrencyCaptureOriginal(a.currencyOriginal) {
			return fmt.Errorf("缺少可验证的资源定位原始指令，拒绝接管现有 Hook")
		}
		cave := relJumpTarget(a.currencyHookAddr, current)
		if cave != a.currencyCaveAddr {
			return fmt.Errorf("资源定位入口已被其他 Hook 替换：目标 0x%X，当前租约 0x%X", cave, a.currencyCaveAddr)
		}
		if err := a.validateCurrencyCaptureCave(cave, a.currencyOriginal); err != nil {
			return fmt.Errorf("资源定位代码洞所有权校验失败: %w", err)
		}
		return nil
	}
	if !isCurrencyCaptureOriginal(current) {
		return fmt.Errorf("实时资源定位指令不匹配 DLC 2.0.2: %s", bytesToHex(current))
	}
	return a.installCurrencyHook(current)
}

func (a *App) installCurrencyHook(original []byte) error {
	cave, err := virtualAllocRemoteNear(a.hProcess, a.currencyHookAddr, 0x1000)
	if err != nil {
		return fmt.Errorf("分配实时资源定位代码洞失败: %w", err)
	}
	code, err := buildCurrencyCaptureCave(cave, a.currencyHookAddr+currencyHookSize, original)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return err
	}
	if err := writeCodeMemory(a.hProcess, cave, code); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return fmt.Errorf("写入实时资源定位代码洞失败: %w", err)
	}
	if err := a.validateCurrencyCaptureCave(cave, original); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return fmt.Errorf("实时资源定位代码洞写后校验失败: %w", err)
	}
	patch, err := makeRelJump(a.currencyHookAddr, cave, currencyHookSize)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return err
	}
	installResult, err := currencyInstallRemoteCodeHook(a.hProcess, a.currencyHookAddr, original, patch)
	if err != nil {
		if installResult.CanFreePreparedCave() {
			_ = virtualFreeRemote(a.hProcess, cave)
		} else if installResult.OriginalEntryProven() {
			a.retireRuntimeCaveLocked(cave, "currency install rollback")
		} else {
			// The entry write may have reached the process while its rollback
			// could not be proven. Keep the cave and ownership evidence so a
			// later detach can inspect and recover it fail-closed.
			a.currencyCaveAddr = cave
			a.currencyOriginal = append(a.currencyOriginal[:0], original...)
		}
		return fmt.Errorf("写入实时资源定位 Hook 失败: %w", err)
	}
	a.currencyCaveAddr = cave
	a.currencyOriginal = append(a.currencyOriginal[:0], original...)
	return nil
}

func (a *App) releaseCurrencyHook() error {
	if a.hProcess == 0 || a.currencyHookAddr == 0 {
		return nil
	}
	current := make([]byte, currencyHookSize)
	if err := readProcessMemory(a.hProcess, a.currencyHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return err
	}
	if len(a.currencyOriginal) != currencyHookSize || !isCurrencyCaptureOriginal(a.currencyOriginal) {
		return fmt.Errorf("缺少可验证的资源定位原始指令，拒绝恢复")
	}
	if bytes.Equal(current, a.currencyOriginal) {
		a.currencyHookAddr = 0
		a.currencyCaveAddr = 0
		a.currencyOriginal = nil
		return nil
	}
	if !isCurrencyCaptureJump(current) {
		return fmt.Errorf("资源定位入口既不是自有跳转也不是原始指令: %s", bytesToHex(current))
	}
	if a.currencyCaveAddr == 0 {
		return fmt.Errorf("资源定位代码洞地址为空，拒绝恢复未知跳转")
	}
	cave := relJumpTarget(a.currencyHookAddr, current)
	if cave != a.currencyCaveAddr {
		return fmt.Errorf("资源定位入口属于其他 Hook：目标 0x%X，当前租约 0x%X", cave, a.currencyCaveAddr)
	}
	if err := a.validateCurrencyCaptureCave(cave, a.currencyOriginal); err != nil {
		return fmt.Errorf("资源定位代码洞所有权校验失败: %w", err)
	}
	if err := writeCodeMemory(a.hProcess, a.currencyHookAddr, a.currencyOriginal); err != nil {
		return fmt.Errorf("恢复实时资源定位指令失败: %w", err)
	}
	restored := make([]byte, currencyHookSize)
	if err := readProcessMemory(a.hProcess, a.currencyHookAddr, unsafe.Pointer(&restored[0]), uintptr(len(restored))); err != nil {
		return fmt.Errorf("恢复实时资源定位指令后回读失败: %w", err)
	}
	if !bytes.Equal(restored, a.currencyOriginal) {
		return fmt.Errorf("恢复实时资源定位指令后回读不一致: %s", bytesToHex(restored))
	}
	a.currencyHookAddr = 0
	a.currencyCaveAddr = 0
	a.currencyOriginal = nil
	return nil
}

func isCurrencyCaptureOriginal(buf []byte) bool {
	return len(buf) >= currencyHookSize && buf[0] == 0xBA
}

func isCurrencyCaptureJump(buf []byte) bool {
	return len(buf) >= currencyHookSize && buf[0] == 0xE9
}

func buildCurrencyCaptureCave(cave, returnAddr uintptr, original []byte) ([]byte, error) {
	if len(original) != currencyHookSize || !isCurrencyCaptureOriginal(original) {
		return nil, fmt.Errorf("实时资源定位原始指令长度或签名异常")
	}
	code := make([]byte, 0, currencyCaveDataOffset+8)
	// The displaced instruction is `mov edx, imm32`, so RDX is about to be
	// overwritten by the original code. Use RDX as scratch and avoid clobbering
	// another volatile register that the surrounding function may still need.
	code = append(code, 0x48, 0xBA) // mov rdx, data address
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+currencyCaveDataOffset))
	code = append(code, 0x48, 0x89, 0x0A) // mov [rdx], rcx
	code = append(code, original...)
	jmp, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jmp...)
	for len(code) < int(currencyCaveDataOffset)+8 {
		code = append(code, 0)
	}
	copy(code[currencyCaveMarkerOffset:], currencyCaveMarker[:])
	return code, nil
}

func validateCurrencyCaptureCaveBytes(cave, hookAddr uintptr, original, code []byte) error {
	const (
		dataAddressOffset = 2
		captureOffset     = 10
		originalOffset    = 13
		jumpOffset        = originalOffset + currencyHookSize
		instructionSize   = jumpOffset + 5
		minimumSize       = currencyCaveMarkerOffset + len(currencyCaveMarker)
	)
	if len(original) != currencyHookSize || !isCurrencyCaptureOriginal(original) {
		return fmt.Errorf("原始指令长度或签名异常")
	}
	if len(code) < minimumSize {
		return fmt.Errorf("代码洞过短: %d", len(code))
	}
	if code[0] != 0x48 || code[1] != 0xBA || binary.LittleEndian.Uint64(code[dataAddressOffset:captureOffset]) != uint64(cave+currencyCaveDataOffset) {
		return fmt.Errorf("数据地址指令不匹配")
	}
	if !bytes.Equal(code[captureOffset:originalOffset], []byte{0x48, 0x89, 0x0A}) {
		return fmt.Errorf("RCX 捕获指令不匹配")
	}
	if !bytes.Equal(code[originalOffset:jumpOffset], original) {
		return fmt.Errorf("代码洞内原始指令不匹配")
	}
	if code[jumpOffset] != 0xE9 || relJumpTarget(cave+jumpOffset, code[jumpOffset:instructionSize]) != hookAddr+currencyHookSize {
		return fmt.Errorf("代码洞回跳地址不匹配")
	}
	if !bytes.Equal(code[currencyCaveMarkerOffset:minimumSize], currencyCaveMarker[:]) {
		return fmt.Errorf("代码洞所有权标记不匹配")
	}
	return nil
}

func (a *App) validateCurrencyCaptureCave(cave uintptr, original []byte) error {
	const codeSize = currencyCaveMarkerOffset + len(currencyCaveMarker)
	code := make([]byte, codeSize)
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		return fmt.Errorf("读取代码洞失败: %w", err)
	}
	return validateCurrencyCaptureCaveBytes(cave, a.currencyHookAddr, original, code)
}
