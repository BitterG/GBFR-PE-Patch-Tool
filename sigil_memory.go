package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

const (
	sigilMemoryHookSize       = 8
	sigilMemoryCaveDataOffset = uintptr(0x40)
	sigilMemoryOriginalOffset = uintptr(13)
)

var (
	// Selected-sigil gate: compare primary/secondary trait slots against the
	// empty hash 0x887AE0B0, then add+xor+cmp edx,2. Multiple near-duplicates
	// exist; take the last module hit (verified to receive RAX = selected record).
	sigilMemorySelectedPattern = []byte{
		0x31, 0xC9, 0x81, 0x38, 0xB0, 0xE0, 0x7A, 0x88, 0x0F, 0x95, 0xC1,
		0x31, 0xD2, 0x81, 0x78, 0x08, 0xB0, 0xE0, 0x7A, 0x88, 0x0F, 0x95, 0xC2,
		0x01, 0, 0x31, 0, 0x83,
	}
	sigilMemorySelectedMask = []bool{
		true, true, true, true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true, true, true, true, true,
		true, false, true, false, true,
	}
)

type SigilMemoryOption struct {
	Hash        uint32 `json:"hash"`
	DisplayName string `json:"displayName"`

	// Level metadata — populated for catalog entries, nil for memory-only.
	MaxLevel                    *int     `json:"maxLevel,omitempty"`
	AllowedLevels               []int    `json:"allowedLevels,omitempty"`
	FirstTraitMaxLevel          *int     `json:"firstTraitMaxLevel,omitempty"`          // sigils only
	AllowedSecondaryTraitHashes []uint32 `json:"allowedSecondaryTraitHashes,omitempty"` // sigils only
	SupportsSecondaryTrait      *bool    `json:"supportsSecondaryTrait,omitempty"`      // sigils only
	Source                      string   `json:"source"`                                // "catalog" | "memory-only"
}

type SigilMemoryOptions struct {
	Sigils []SigilMemoryOption `json:"sigils"`
	Traits []SigilMemoryOption `json:"traits"`
}

type SigilMemoryStatus struct {
	Found               bool   `json:"found"`
	Hooked              bool   `json:"hooked"`
	Address             uint64 `json:"address"`
	RVA                 uint64 `json:"rva"`
	SelectedAddr        uint64 `json:"selectedAddr"`
	SaveRVA             uint64 `json:"saveRva"`
	CurrentBytes        string `json:"currentBytes"`
	SigilHash           uint32 `json:"sigilHash"`
	SigilName           string `json:"sigilName"`
	SigilLevel          uint32 `json:"sigilLevel"`
	PrimaryTraitHash    uint32 `json:"primaryTraitHash"`
	PrimaryTraitName    string `json:"primaryTraitName"`
	PrimaryTraitLevel   uint32 `json:"primaryTraitLevel"`
	SecondaryTraitHash  uint32 `json:"secondaryTraitHash"`
	SecondaryTraitName  string `json:"secondaryTraitName"`
	SecondaryTraitLevel uint32 `json:"secondaryTraitLevel"`
}

type SigilMemoryUpdate struct {
	SigilHash           uint32 `json:"sigilHash"`
	SigilLevel          uint32 `json:"sigilLevel"`
	PrimaryTraitHash    uint32 `json:"primaryTraitHash"`
	PrimaryTraitLevel   uint32 `json:"primaryTraitLevel"`
	SecondaryTraitHash  uint32 `json:"secondaryTraitHash"`
	SecondaryTraitLevel uint32 `json:"secondaryTraitLevel"`
}

func sigilMemoryHookedMask() []bool {
	mask := append([]bool{}, sigilMemorySelectedMask...)
	for i := 0; i < sigilMemoryHookSize && i < len(mask); i++ {
		mask[i] = false
	}
	return mask
}

func (a *App) SigilMemoryGetOptions() (SigilMemoryOptions, error) {
	catalog, err := LoadCatalog()
	if err != nil {
		return SigilMemoryOptions{}, err
	}

	// Build traitID → hash map once for allowedSecondaryTraitIds translation.
	traitHashByID := make(map[string]uint32, len(catalog.Traits))
	for i := range catalog.Traits {
		t := &catalog.Traits[i]
		if h, err := ParseHashHex(t.Hash); err == nil {
			traitHashByID[t.InternalID] = h
		}
	}

	result := SigilMemoryOptions{
		Sigils: make([]SigilMemoryOption, 0, len(catalog.Sigils)),
		Traits: make([]SigilMemoryOption, 0, len(catalog.Traits)),
	}

	for _, sigil := range catalog.GetSigilSortedList() {
		hash, err := ParseHashHex(sigil.Hash)
		if err != nil {
			continue
		}
		var allowedSecHashes []uint32
		if len(sigil.AllowedSecondaryTraitIDs) > 0 {
			allowedSecHashes = make([]uint32, 0, len(sigil.AllowedSecondaryTraitIDs))
			for _, id := range sigil.AllowedSecondaryTraitIDs {
				if h, ok := traitHashByID[id]; ok {
					allowedSecHashes = append(allowedSecHashes, h)
				}
			}
		}
		result.Sigils = append(result.Sigils, SigilMemoryOption{
			Hash:                        hash,
			DisplayName:                 displaySigilName(sigil),
			MaxLevel:                    sigil.MaxSigilLevel,
			AllowedLevels:               sigil.AllowedSigilLevels,
			FirstTraitMaxLevel:          sigil.FirstTraitMaxLevel,
			AllowedSecondaryTraitHashes: allowedSecHashes,
			SupportsSecondaryTrait:      sigil.SupportsSecondaryTrait,
			Source:                      "catalog",
		})
	}

	for i := range catalog.Traits {
		trait := &catalog.Traits[i]
		if !isSelectableTrait(trait) {
			continue
		}
		hash, err := ParseHashHex(trait.Hash)
		if err != nil {
			continue
		}
		result.Traits = append(result.Traits, SigilMemoryOption{
			Hash:          hash,
			DisplayName:   cnTrait(trait.DisplayName),
			MaxLevel:      trait.MaxLevel,
			AllowedLevels: trait.AllowedLevels,
			Source:        "catalog",
		})
	}

	return result, nil
}

func (a *App) SigilMemoryScan() (SigilMemoryStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return SigilMemoryStatus{}, err
	}
	addr, err := a.resolveSigilMemoryHook()
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	first := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&first[0]), uintptr(len(first))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取选中因子指令失败: %w", err)
	}
	if isSigilMemoryOriginal(first) {
		a.sigilMemoryOriginal = append(a.sigilMemoryOriginal[:0], first...)
		a.sigilMemoryCaveAddr = 0
	} else if isSigilMemoryJump(first) {
		// A previous tool instance may have exited while the game stayed open.
		// Adopt only a hook whose code cave has our exact prologue, then recover
		// the displaced instructions so it can be safely removed on shutdown.
		cave := relJumpTarget(addr, first)
		original, err := a.recoverSigilMemoryHook(cave)
		if err != nil {
			return SigilMemoryStatus{}, fmt.Errorf("选中因子 Hook 无法接管: %w", err)
		}
		a.sigilMemoryCaveAddr = cave
		a.sigilMemoryOriginal = original
	} else {
		return SigilMemoryStatus{}, fmt.Errorf("选中因子指令字节异常: %s", bytesToHex(first))
	}
	a.sigilMemoryHookAddr = addr
	return a.readSigilMemoryStatus()
}

func (a *App) resolveSigilMemoryHook() (uintptr, error) {
	return a.scanSigilMemoryPattern()
}

func (a *App) scanSigilMemoryPattern() (uintptr, error) {
	// Wildcard the first hook-sized bytes so both pristine and already-hooked
	// sites still match the rest of the uniqueness window.
	matches, err := a.scanPatternAll(sigilMemorySelectedPattern, sigilMemoryHookedMask())
	if err != nil {
		return 0, err
	}
	for i := len(matches) - 1; i >= 0; i-- {
		addr := matches[i]
		probe := make([]byte, sigilMemoryHookSize)
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&probe[0]), uintptr(len(probe))); err != nil {
			continue
		}
		if isSigilMemoryOriginal(probe) || isSigilMemoryJump(probe) {
			return addr, nil
		}
	}
	return 0, fmt.Errorf("未找到选中因子特征码；当前游戏版本与内置特征不匹配")
}

func (a *App) SigilMemoryGetStatus() (SigilMemoryStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return SigilMemoryStatus{}, err
	}
	if a.sigilMemoryHookAddr == 0 {
		return a.SigilMemoryScan()
	}
	status, err := a.readSigilMemoryStatus()
	if err != nil {
		a.sigilMemoryHookAddr = 0
		return a.SigilMemoryScan()
	}
	return status, nil
}

func (a *App) SigilMemoryEnable() (SigilMemoryStatus, error) {
	status, err := a.SigilMemoryGetStatus()
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	if status.Hooked {
		return status, nil
	}

	original := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.sigilMemoryHookAddr, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取选中因子原始指令失败: %w", err)
	}
	cave, err := virtualAllocRemoteNear(a.hProcess, a.sigilMemoryHookAddr, 0x1000)
	if err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("分配因子读取代码洞失败: %w", err)
	}
	code, err := buildSigilMemoryCave(cave, a.sigilMemoryHookAddr+sigilMemoryHookSize, original)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return SigilMemoryStatus{}, err
	}
	if err := writeProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return SigilMemoryStatus{}, fmt.Errorf("写入因子读取代码洞失败: %w", err)
	}
	patch, err := makeRelJump(a.sigilMemoryHookAddr, cave, sigilMemoryHookSize)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return SigilMemoryStatus{}, err
	}
	if err := writeCodeMemory(a.hProcess, a.sigilMemoryHookAddr, patch); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return SigilMemoryStatus{}, fmt.Errorf("写入因子读取 Hook 失败: %w", err)
	}
	a.sigilMemoryCaveAddr = cave
	a.sigilMemoryOriginal = append(a.sigilMemoryOriginal[:0], original...)
	return a.readSigilMemoryStatus()
}

func (a *App) SigilMemoryUpdate(update SigilMemoryUpdate) (SigilMemoryStatus, error) {
	status, err := a.SigilMemoryGetStatus()
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	if !status.Hooked || status.SelectedAddr == 0 {
		return SigilMemoryStatus{}, fmt.Errorf("请先开启读取，并在游戏内因子列表选中一个因子")
	}
	if update.SigilLevel > 999 || update.PrimaryTraitLevel > 999 || update.SecondaryTraitLevel > 999 {
		return SigilMemoryStatus{}, fmt.Errorf("因子和词条等级不能超过 999")
	}

	base := uintptr(status.SelectedAddr)
	writes := []struct {
		offset uintptr
		value  uint32
		name   string
	}{
		{0x00, update.PrimaryTraitHash, "主词条"},
		{0x04, update.PrimaryTraitLevel, "主词条等级"},
		{0x08, update.SecondaryTraitHash, "副词条"},
		{0x0C, update.SecondaryTraitLevel, "副词条等级"},
		{0x10, update.SigilHash, "因子"},
		{0x18, update.SigilLevel, "因子等级"},
	}
	for _, write := range writes {
		if err := writeUint32Remote(a.hProcess, base+write.offset, write.value); err != nil {
			return SigilMemoryStatus{}, fmt.Errorf("写入%s失败: %w", write.name, err)
		}
	}
	if err := a.saveSigilMemory(base); err != nil {
		return SigilMemoryStatus{}, err
	}
	result, err := a.readSigilMemoryStatus()
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	// Inventory storage can be rebuilt after rewards, sorting or scene changes.
	// Never allow a later write to silently reuse this raw pointer.
	if err := a.clearSigilMemorySelection(); err != nil {
		return SigilMemoryStatus{}, err
	}
	result.SelectedAddr = 0
	return result, nil
}

func (a *App) saveSigilMemory(base uintptr) error {
	fn, err := a.resolveItemSaveFunction()
	if err != nil {
		return err
	}
	for offset := uintptr(0); offset <= 0x20; offset += 4 {
		if err := a.callRemoteOneArg(fn, base+offset); err != nil {
			return fmt.Errorf("保存因子字段 +0x%02X 失败: %w", offset, err)
		}
	}
	return nil
}

func (a *App) readSigilMemoryStatus() (SigilMemoryStatus, error) {
	if a.sigilMemoryHookAddr == 0 {
		return SigilMemoryStatus{}, fmt.Errorf("未定位选中因子特征")
	}
	buf := make([]byte, len(sigilMemorySelectedPattern))
	if err := readProcessMemory(a.hProcess, a.sigilMemoryHookAddr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取选中因子 Hook 指令失败: %w", err)
	}
	hooked := isSigilMemoryJump(buf)
	if !hooked && !isSigilMemoryOriginal(buf) {
		return SigilMemoryStatus{}, fmt.Errorf("选中因子指令字节异常: %s", bytesToHex(buf))
	}

	status := SigilMemoryStatus{
		Found:        true,
		Hooked:       hooked,
		Address:      uint64(a.sigilMemoryHookAddr),
		RVA:          uint64(a.sigilMemoryHookAddr - a.moduleBase),
		CurrentBytes: bytesToHex(buf),
	}
	if saveFn, err := a.resolveItemSaveFunction(); err == nil {
		status.SaveRVA = uint64(saveFn - a.moduleBase)
	}
	if !hooked {
		return status, nil
	}
	if a.sigilMemoryCaveAddr == 0 {
		cave := relJumpTarget(a.sigilMemoryHookAddr, buf)
		original, err := a.recoverSigilMemoryHook(cave)
		if err != nil {
			return SigilMemoryStatus{}, fmt.Errorf("校验选中因子 Hook 失败: %w", err)
		}
		a.sigilMemoryCaveAddr = cave
		a.sigilMemoryOriginal = original
	}
	var selected uintptr
	if err := readProcessMemory(a.hProcess, a.sigilMemoryCaveAddr+sigilMemoryCaveDataOffset, unsafe.Pointer(&selected), unsafe.Sizeof(selected)); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取选中因子指针失败: %w", err)
	}
	status.SelectedAddr = uint64(selected)
	if selected == 0 {
		return status, nil
	}

	values := make([]byte, 0x1C)
	if err := readProcessMemory(a.hProcess, selected, unsafe.Pointer(&values[0]), uintptr(len(values))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取选中因子数据失败: %w", err)
	}
	status.PrimaryTraitHash = binary.LittleEndian.Uint32(values[0x00:0x04])
	status.PrimaryTraitLevel = binary.LittleEndian.Uint32(values[0x04:0x08])
	status.SecondaryTraitHash = binary.LittleEndian.Uint32(values[0x08:0x0C])
	status.SecondaryTraitLevel = binary.LittleEndian.Uint32(values[0x0C:0x10])
	status.SigilHash = binary.LittleEndian.Uint32(values[0x10:0x14])
	status.SigilLevel = binary.LittleEndian.Uint32(values[0x18:0x1C])

	catalog, err := LoadCatalog()
	if err == nil {
		if sigil := catalog.LookupSigilByHash(status.SigilHash); sigil != nil {
			status.SigilName = displaySigilName(sigil)
		}
		if trait := catalog.LookupTraitByHash(status.PrimaryTraitHash); trait != nil {
			status.PrimaryTraitName = cnTrait(trait.DisplayName)
		}
		if trait := catalog.LookupTraitByHash(status.SecondaryTraitHash); trait != nil {
			status.SecondaryTraitName = cnTrait(trait.DisplayName)
		}
	}
	if status.SigilName == "" {
		status.SigilName = ctName(status.SigilHash)
	}
	if status.SigilName == "" {
		status.SigilName = fmt.Sprintf("0x%08X", status.SigilHash)
	}
	if status.PrimaryTraitName == "" {
		status.PrimaryTraitName = ctName(status.PrimaryTraitHash)
	}
	if status.PrimaryTraitName == "" {
		status.PrimaryTraitName = fmt.Sprintf("0x%08X", status.PrimaryTraitHash)
	}
	if status.SecondaryTraitName == "" {
		status.SecondaryTraitName = ctName(status.SecondaryTraitHash)
	}
	if status.SecondaryTraitName == "" {
		status.SecondaryTraitName = fmt.Sprintf("0x%08X", status.SecondaryTraitHash)
	}
	return status, nil
}

func isSigilMemoryOriginal(buf []byte) bool {
	return len(buf) >= sigilMemoryHookSize && buf[0] == 0x31 && buf[2] == 0x81
}

func isSigilMemoryJump(buf []byte) bool {
	return len(buf) >= sigilMemoryHookSize && buf[0] == 0xE9 && buf[5] == 0x90 && buf[6] == 0x90 && buf[7] == 0x90
}

func (a *App) recoverSigilMemoryHook(cave uintptr) ([]byte, error) {
	if cave == 0 {
		return nil, fmt.Errorf("代码洞地址为空")
	}
	prologue := make([]byte, sigilMemoryOriginalOffset)
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&prologue[0]), uintptr(len(prologue))); err != nil {
		return nil, fmt.Errorf("读取代码洞失败: %w", err)
	}
	if prologue[0] != 0x49 || prologue[1] != 0xBA || prologue[10] != 0x49 || prologue[11] != 0x89 || prologue[12] != 0x02 {
		return nil, fmt.Errorf("代码洞签名不匹配")
	}
	dataAddr := uintptr(binary.LittleEndian.Uint64(prologue[2:10]))
	if dataAddr != cave+sigilMemoryCaveDataOffset {
		return nil, fmt.Errorf("代码洞数据地址不匹配")
	}
	original := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, cave+sigilMemoryOriginalOffset, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
		return nil, fmt.Errorf("读取原始指令失败: %w", err)
	}
	if !isSigilMemoryOriginal(original) {
		return nil, fmt.Errorf("原始指令签名不匹配: %s", bytesToHex(original))
	}
	return original, nil
}

func (a *App) clearSigilMemorySelection() error {
	if a.hProcess == 0 || a.sigilMemoryCaveAddr == 0 {
		return nil
	}
	var zero uintptr
	if err := writeProcessMemory(a.hProcess, a.sigilMemoryCaveAddr+sigilMemoryCaveDataOffset, unsafe.Pointer(&zero), unsafe.Sizeof(zero)); err != nil {
		return fmt.Errorf("清空旧的选中因子指针失败: %w", err)
	}
	return nil
}

func (a *App) releaseSigilMemoryHook() error {
	if a.hProcess == 0 || a.sigilMemoryHookAddr == 0 {
		return nil
	}
	current := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.sigilMemoryHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return err
	}
	if !isSigilMemoryJump(current) {
		return nil
	}
	cave := relJumpTarget(a.sigilMemoryHookAddr, current)
	original := a.sigilMemoryOriginal
	if len(original) != sigilMemoryHookSize {
		var err error
		original, err = a.recoverSigilMemoryHook(cave)
		if err != nil {
			return err
		}
	}
	if err := writeCodeMemory(a.hProcess, a.sigilMemoryHookAddr, original); err != nil {
		return fmt.Errorf("恢复选中因子原始指令失败: %w", err)
	}
	// Do not free the remote page here: a game thread may already be inside the
	// cave. The OS reclaims this single page when the game exits.
	a.sigilMemoryHookAddr = 0
	a.sigilMemoryCaveAddr = 0
	a.sigilMemoryOriginal = nil
	return nil
}

func buildSigilMemoryCave(cave, returnAddr uintptr, original []byte) ([]byte, error) {
	if len(original) != sigilMemoryHookSize {
		return nil, fmt.Errorf("选中因子原始指令长度异常")
	}
	code := make([]byte, 0, sigilMemoryCaveDataOffset+8)
	code = append(code, 0x49, 0xBA) // mov r10, cave data address
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+sigilMemoryCaveDataOffset))
	code = append(code, 0x49, 0x89, 0x02) // mov [r10], rax
	code = append(code, original...)
	jmp, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jmp...)
	for len(code) < int(sigilMemoryCaveDataOffset)+8 {
		code = append(code, 0)
	}
	return code, nil
}
