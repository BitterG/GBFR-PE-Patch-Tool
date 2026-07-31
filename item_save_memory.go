package main

import (
	"fmt"
	"unsafe"
)

// Prologue plus the following test distinguishes the inventory field-save helper
// from other functions that share the same stack frame setup.
var (
	itemSaveFunctionPattern = []byte{
		0x55, 0x48, 0x83, 0xEC, 0x60, 0x48, 0x8D, 0x6C, 0x24, 0x60,
		0x48, 0xC7, 0x45, 0xF8, 0xFE, 0xFF, 0xFF, 0xFF,
		0x48, 0x8B, 0x05, 0, 0, 0, 0,
		0x48, 0x85, 0xC0,
	}
	itemSaveFunctionMask = []bool{
		true, true, true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true,
		true, true, true, false, false, false, false,
		true, true, true,
	}
)

func (a *App) resolveItemSaveFunction() (uintptr, error) {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return 0, fmt.Errorf("未连接游戏进程")
	}
	if a.itemSaveFunctionAddr != 0 {
		if err := a.validateItemSaveFunction(a.itemSaveFunctionAddr); err == nil {
			return a.itemSaveFunctionAddr, nil
		}
		a.itemSaveFunctionAddr = 0
	}

	addr, err := a.scanPatternUnique(itemSaveFunctionPattern, itemSaveFunctionMask, "物品保存函数特征")
	if err != nil {
		return 0, err
	}
	if err := a.validateItemSaveFunction(addr); err != nil {
		return 0, err
	}
	a.itemSaveFunctionAddr = addr
	return addr, nil
}

func (a *App) validateItemSaveFunction(addr uintptr) error {
	buf := make([]byte, len(itemSaveFunctionPattern))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return fmt.Errorf("读取物品保存函数失败: %w", err)
	}
	if !matchPattern(buf, itemSaveFunctionPattern, itemSaveFunctionMask) {
		return fmt.Errorf("物品保存函数特征不匹配: %s", bytesToHex(buf[:18]))
	}
	return nil
}
