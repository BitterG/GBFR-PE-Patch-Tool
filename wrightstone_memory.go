package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

const (
	wrightstoneMemoryHookRVA        = uintptr(0x31B59F) // 2.0.3; was 0x3222CF on 2.0.2
	wrightstoneMemorySaveRVA        = uintptr(0x796E60) // 2.0.3; was 0x79D820 on 2.0.2
	wrightstoneMemoryHookSize       = 8
	wrightstoneMemoryCaveDataOffset = uintptr(0x40)
)

type WrightstoneMemoryOption struct {
	Hash        uint32 `json:"hash"`
	DisplayName string `json:"displayName"`
	MaxLevel    *int   `json:"maxLevel,omitempty"`
}
type WrightstoneMemoryOptions struct {
	Traits []WrightstoneMemoryOption `json:"traits"`
}
type WrightstoneMemoryStatus struct {
	Found        bool   `json:"found"`
	Hooked       bool   `json:"hooked"`
	SelectedAddr uint64 `json:"selectedAddr"`
	FirstHash    uint32 `json:"firstHash"`
	FirstName    string `json:"firstName"`
	FirstLevel   uint32 `json:"firstLevel"`
	SecondHash   uint32 `json:"secondHash"`
	SecondName   string `json:"secondName"`
	SecondLevel  uint32 `json:"secondLevel"`
	ThirdHash    uint32 `json:"thirdHash"`
	ThirdName    string `json:"thirdName"`
	ThirdLevel   uint32 `json:"thirdLevel"`
}
type WrightstoneMemoryUpdate struct {
	FirstHash   uint32 `json:"firstHash"`
	FirstLevel  uint32 `json:"firstLevel"`
	SecondHash  uint32 `json:"secondHash"`
	SecondLevel uint32 `json:"secondLevel"`
	ThirdHash   uint32 `json:"thirdHash"`
	ThirdLevel  uint32 `json:"thirdLevel"`
}

func (a *App) WrightstoneMemoryGetOptions() (WrightstoneMemoryOptions, error) {
	catalog, err := LoadWrightstoneCatalog()
	if err != nil {
		return WrightstoneMemoryOptions{}, err
	}
	result := WrightstoneMemoryOptions{Traits: make([]WrightstoneMemoryOption, 0, len(catalog.Traits))}
	for _, trait := range catalog.GetTraitSortedList() {
		hash, err := ParseHashHex(trait.Hash)
		if err == nil {
			result.Traits = append(result.Traits, WrightstoneMemoryOption{hash, cnWrightstoneTrait(trait.DisplayName), trait.MaxLevel})
		}
	}
	return result, nil
}

func (a *App) WrightstoneMemoryGetStatus() (WrightstoneMemoryStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	if a.wrightstoneMemoryHookAddr == 0 {
		a.wrightstoneMemoryHookAddr = a.moduleBase + wrightstoneMemoryHookRVA
		original := make([]byte, wrightstoneMemoryHookSize)
		if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryHookAddr, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
			return WrightstoneMemoryStatus{}, fmt.Errorf("读取祝福焦点指令失败: %w", err)
		}
		if !isWrightstoneMemoryOriginal(original) && !isWrightstoneMemoryJump(original) {
			return WrightstoneMemoryStatus{}, fmt.Errorf("祝福焦点指令字节异常: %s", bytesToHex(original))
		}
		if isWrightstoneMemoryOriginal(original) {
			a.wrightstoneMemoryOriginal = original
		} else {
			cave := relJumpTarget(a.wrightstoneMemoryHookAddr, original)
			recovered, err := a.recoverWrightstoneMemoryHook(cave)
			if err != nil {
				return WrightstoneMemoryStatus{}, fmt.Errorf("祝福 Hook 无法接管: %w", err)
			}
			a.wrightstoneMemoryCaveAddr = cave
			a.wrightstoneMemoryOriginal = recovered
		}
	}
	return a.readWrightstoneMemoryStatus()
}

func (a *App) WrightstoneMemoryDisable() (WrightstoneMemoryStatus, error) {
	if err := a.releaseWrightstoneMemoryHook(); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("关闭祝福读取失败: %w", err)
	}
	return WrightstoneMemoryStatus{}, nil
}

func (a *App) WrightstoneMemoryEnable() (WrightstoneMemoryStatus, error) {
	status, err := a.WrightstoneMemoryGetStatus()
	if err != nil || status.Hooked {
		return status, err
	}
	cave, err := virtualAllocRemoteNear(a.hProcess, a.wrightstoneMemoryHookAddr, 0x1000)
	if err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("分配祝福读取代码洞失败: %w", err)
	}
	code, err := buildWrightstoneMemoryCave(cave, a.wrightstoneMemoryHookAddr+wrightstoneMemoryHookSize, a.wrightstoneMemoryOriginal)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return WrightstoneMemoryStatus{}, err
	}
	if err := writeProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return WrightstoneMemoryStatus{}, fmt.Errorf("写入祝福读取代码洞失败: %w", err)
	}
	patch, err := makeRelJump(a.wrightstoneMemoryHookAddr, cave, wrightstoneMemoryHookSize)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return WrightstoneMemoryStatus{}, err
	}
	if err := writeCodeMemory(a.hProcess, a.wrightstoneMemoryHookAddr, patch); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return WrightstoneMemoryStatus{}, fmt.Errorf("写入祝福读取 Hook 失败: %w", err)
	}
	a.wrightstoneMemoryCaveAddr = cave
	return a.readWrightstoneMemoryStatus()
}

func (a *App) WrightstoneMemoryUpdate(update WrightstoneMemoryUpdate) (WrightstoneMemoryStatus, error) {
	status, err := a.WrightstoneMemoryGetStatus()
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	if !status.Hooked || status.SelectedAddr == 0 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("请先开启读取，并在游戏内选中一个祝福石")
	}
	if update.FirstHash == 0x887AE0B0 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福词条 1 不能选择不选择")
	}
	if update.FirstLevel > 999 || update.SecondLevel > 999 || update.ThirdLevel > 999 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福词条等级不能超过 999")
	}
	base := uintptr(status.SelectedAddr)
	writes := []struct {
		offset uintptr
		value  uint32
	}{{0, update.FirstHash}, {4, update.FirstLevel}, {8, update.SecondHash}, {0x0C, update.SecondLevel}, {0x10, update.ThirdHash}, {0x14, update.ThirdLevel}}
	for _, write := range writes {
		if err := writeUint32Remote(a.hProcess, base+write.offset, write.value); err != nil {
			return WrightstoneMemoryStatus{}, fmt.Errorf("写入祝福词条失败: %w", err)
		}
	}
	for _, offset := range []uintptr{0, 4, 8, 0x0C, 0x10, 0x14} {
		if err := a.callRemoteOneArg(a.moduleBase+wrightstoneMemorySaveRVA, base+offset); err != nil {
			return WrightstoneMemoryStatus{}, fmt.Errorf("保存祝福字段 +0x%02X 失败: %w", offset, err)
		}
	}
	result, err := a.readWrightstoneMemoryStatus()
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	if err := a.clearWrightstoneMemorySelection(); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	result.SelectedAddr = 0
	return result, nil
}

func (a *App) readWrightstoneMemoryStatus() (WrightstoneMemoryStatus, error) {
	status := WrightstoneMemoryStatus{Found: a.wrightstoneMemoryHookAddr != 0}
	if !status.Found {
		return status, nil
	}
	current := make([]byte, wrightstoneMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return status, err
	}
	status.Hooked = isWrightstoneMemoryJump(current)
	if !status.Hooked {
		return status, nil
	}
	var selected uintptr
	if a.wrightstoneMemoryCaveAddr == 0 {
		cave := relJumpTarget(a.wrightstoneMemoryHookAddr, current)
		recovered, err := a.recoverWrightstoneMemoryHook(cave)
		if err != nil {
			return status, err
		}
		a.wrightstoneMemoryCaveAddr = cave
		a.wrightstoneMemoryOriginal = recovered
	}
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryCaveAddr+wrightstoneMemoryCaveDataOffset, unsafe.Pointer(&selected), unsafe.Sizeof(selected)); err != nil {
		return status, err
	}
	status.SelectedAddr = uint64(selected)
	if selected == 0 {
		return status, nil
	}
	values := make([]byte, 0x18)
	if err := readProcessMemory(a.hProcess, selected, unsafe.Pointer(&values[0]), uintptr(len(values))); err != nil {
		return status, err
	}
	status.FirstHash, status.FirstLevel = binary.LittleEndian.Uint32(values[0:4]), binary.LittleEndian.Uint32(values[4:8])
	status.SecondHash, status.SecondLevel = binary.LittleEndian.Uint32(values[0x08:0x0C]), binary.LittleEndian.Uint32(values[0x0C:0x10])
	status.ThirdHash, status.ThirdLevel = binary.LittleEndian.Uint32(values[0x10:0x14]), binary.LittleEndian.Uint32(values[0x14:0x18])
	if catalog, err := LoadWrightstoneCatalog(); err == nil {
		if trait := catalog.LookupTraitByHash(status.FirstHash); trait != nil {
			status.FirstName = cnWrightstoneTrait(trait.DisplayName)
		}
		if trait := catalog.LookupTraitByHash(status.SecondHash); trait != nil {
			status.SecondName = cnWrightstoneTrait(trait.DisplayName)
		}
		if trait := catalog.LookupTraitByHash(status.ThirdHash); trait != nil {
			status.ThirdName = cnWrightstoneTrait(trait.DisplayName)
		}
	}
	if status.FirstName == "" {
		if status.FirstHash == 0x887AE0B0 {
			status.FirstName = "不选择"
		} else {
			status.FirstName = fmt.Sprintf("0x%08X", status.FirstHash)
		}
	}
	if status.SecondName == "" {
		if status.SecondHash == 0x887AE0B0 {
			status.SecondName = "不选择"
		} else {
			status.SecondName = fmt.Sprintf("0x%08X", status.SecondHash)
		}
	}
	if status.ThirdName == "" {
		if status.ThirdHash == 0x887AE0B0 {
			status.ThirdName = "不选择"
		} else {
			status.ThirdName = fmt.Sprintf("0x%08X", status.ThirdHash)
		}
	}
	return status, nil
}

func isWrightstoneMemoryOriginal(b []byte) bool {
	return len(b) == 8 && b[0] == 0x8B && b[1] == 0x02 && b[2] == 0x39 && b[3] == 0x06
}
func isWrightstoneMemoryJump(b []byte) bool {
	return len(b) == 8 && b[0] == 0xE9 && b[5] == 0x90 && b[6] == 0x90 && b[7] == 0x90
}
func buildWrightstoneMemoryCave(cave, returnAddr uintptr, original []byte) ([]byte, error) {
	if !isWrightstoneMemoryOriginal(original) {
		return nil, fmt.Errorf("祝福原始指令签名异常")
	}
	code := []byte{0x41, 0x52, 0x49, 0xBA} // push r10; mov r10, cave data address
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+wrightstoneMemoryCaveDataOffset))
	code = append(code, 0x49, 0x89, 0x12, 0x41, 0x5A) // mov [r10], rdx; pop r10
	code = append(code, original...)
	jump, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jump...)
	for len(code) < int(wrightstoneMemoryCaveDataOffset)+8 {
		code = append(code, 0)
	}
	return code, nil
}
func (a *App) recoverWrightstoneMemoryHook(cave uintptr) ([]byte, error) {
	if cave == 0 {
		return nil, fmt.Errorf("祝福代码洞地址为空")
	}
	prologue := make([]byte, 25)
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&prologue[0]), uintptr(len(prologue))); err != nil {
		return nil, fmt.Errorf("读取祝福代码洞失败: %w", err)
	}
	if prologue[0] != 0x41 || prologue[1] != 0x52 || prologue[2] != 0x49 || prologue[3] != 0xBA || prologue[12] != 0x49 || prologue[13] != 0x89 || prologue[14] != 0x12 || prologue[15] != 0x41 || prologue[16] != 0x5A {
		return nil, fmt.Errorf("祝福代码洞签名不匹配")
	}
	dataAddr := uintptr(binary.LittleEndian.Uint64(prologue[4:12]))
	if dataAddr != cave+wrightstoneMemoryCaveDataOffset {
		return nil, fmt.Errorf("祝福代码洞数据地址不匹配")
	}
	original := append([]byte{}, prologue[17:25]...)
	if !isWrightstoneMemoryOriginal(original) {
		return nil, fmt.Errorf("祝福原始指令签名不匹配: %s", bytesToHex(original))
	}
	return original, nil
}

func (a *App) clearWrightstoneMemorySelection() error {
	if a.hProcess == 0 || a.wrightstoneMemoryCaveAddr == 0 {
		return nil
	}
	var zero uintptr
	if err := writeProcessMemory(a.hProcess, a.wrightstoneMemoryCaveAddr+wrightstoneMemoryCaveDataOffset, unsafe.Pointer(&zero), unsafe.Sizeof(zero)); err != nil {
		return fmt.Errorf("清空旧的选中祝福石指针失败: %w", err)
	}
	return nil
}

func (a *App) releaseWrightstoneMemoryHook() error {
	if a.hProcess == 0 || a.wrightstoneMemoryHookAddr == 0 {
		return nil
	}
	current := make([]byte, wrightstoneMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return err
	}
	if !isWrightstoneMemoryJump(current) {
		return nil
	}
	cave := relJumpTarget(a.wrightstoneMemoryHookAddr, current)
	original := a.wrightstoneMemoryOriginal
	if len(original) != wrightstoneMemoryHookSize {
		var err error
		original, err = a.recoverWrightstoneMemoryHook(cave)
		if err != nil {
			return err
		}
	}
	if err := writeCodeMemory(a.hProcess, a.wrightstoneMemoryHookAddr, original); err != nil {
		return fmt.Errorf("恢复祝福原始指令失败: %w", err)
	}
	// Do not free remote page: a game thread may already be executing in it.
	a.wrightstoneMemoryHookAddr = 0
	a.wrightstoneMemoryCaveAddr = 0
	a.wrightstoneMemoryOriginal = nil
	return nil
}
