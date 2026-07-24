package backend

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

const (
	sigilMemoryHookRVA              = uintptr(0x345157)
	sigilMemorySaveRVA              = uintptr(0x79D820)
	sigilMemoryHookSize             = 8
	sigilMemoryCaveDataOffset       = uintptr(0x40)
	sigilMemoryOriginalOffset       = uintptr(17)
	sigilMemoryLegacyOriginalOffset = uintptr(13)
	sigilMemoryMarkerOffset         = uintptr(0x30)
)

var (
	sigilMemoryOriginalBytes = []byte{0x31, 0xC9, 0x81, 0x38, 0xB0, 0xE0, 0x7A, 0x88}
	sigilMemoryMarker        = []byte("GBFRSGM1")
	sigilMemoryLifecycleMu   sync.Mutex
)

type SigilMemoryOption struct {
	Hash        uint32 `json:"hash"`
	DisplayName string `json:"displayName"`

	// Level metadata comes from the same catalog used by save construction.
	MaxLevel                    *int     `json:"maxLevel,omitempty"`
	AllowedLevels               []int    `json:"allowedLevels,omitempty"`
	FirstTraitMaxLevel          *int     `json:"firstTraitMaxLevel,omitempty"`          // sigils only
	PrimaryTraitHash            uint32   `json:"primaryTraitHash,omitempty"`            // catalog sigils only
	AllowedPrimaryTraitLevels   []int    `json:"allowedPrimaryTraitLevels,omitempty"`   // catalog sigils only
	AllowedSecondaryTraitHashes []uint32 `json:"allowedSecondaryTraitHashes,omitempty"` // sigils only
	SupportsSecondaryTrait      *bool    `json:"supportsSecondaryTrait,omitempty"`      // sigils only
	Source                      string   `json:"source"`                                // "catalog"
}

type SigilMemoryOptions struct {
	Sigils []SigilMemoryOption `json:"sigils"`
	Traits []SigilMemoryOption `json:"traits"`
}

type SigilMemoryStatus struct {
	OwnerToken          string `json:"ownerToken,omitempty"`
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
	ExpectedSelectedAddr uint64 `json:"expectedSelectedAddr"`
	SigilHash            uint32 `json:"sigilHash"`
	SigilLevel           uint32 `json:"sigilLevel"`
	PrimaryTraitHash     uint32 `json:"primaryTraitHash"`
	PrimaryTraitLevel    uint32 `json:"primaryTraitLevel"`
	SecondaryTraitHash   uint32 `json:"secondaryTraitHash"`
	SecondaryTraitLevel  uint32 `json:"secondaryTraitLevel"`
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
		primaryTrait, err := catalog.RequireTrait(sigil.PrimaryTraitID)
		if err != nil {
			return SigilMemoryOptions{}, fmt.Errorf("因子 %s 的固定主词条无效: %w", sigil.DisplayName, err)
		}
		primaryTraitHash, err := ParseHashHex(primaryTrait.Hash)
		if err != nil {
			return SigilMemoryOptions{}, fmt.Errorf("因子 %s 的固定主词条哈希无效: %w", sigil.DisplayName, err)
		}
		allowedPrimaryLevels, err := catalog.RequirePrimaryTraitLevels(sigil)
		if err != nil {
			return SigilMemoryOptions{}, fmt.Errorf("因子 %s 的固定主词条等级无效: %w", sigil.DisplayName, err)
		}
		allowedSigilLevels, err := catalog.RequireSigilLevels(sigil)
		if err != nil {
			return SigilMemoryOptions{}, fmt.Errorf("因子 %s 的等级范围无效: %w", sigil.DisplayName, err)
		}
		naturalSigilLevelSet := naturalSigilLevelsForDefinition(sigil, allowedSigilLevels)
		naturalPrimaryLevels := naturalSigilLevelsForDefinition(sigil, allowedPrimaryLevels)
		sigilMaxLevel := sigilWritableLevelMax
		primaryCurve, err := requireTraitLevels(primaryTrait, "运行时主词条")
		if err != nil {
			return SigilMemoryOptions{}, err
		}
		primaryMaxLevel := effectCurveMax(primaryCurve, 15)
		// Keep the memory editor's legality hints in sync with the save
		// generator. The explicit IDs are the exact local gem/lot join; the
		// shared resolver applies the same trait eligibility and duplicate-primary
		// exclusions as offline construction.
		var allowedSecHashes []uint32
		if allowedTraits, err := catalog.GetAllowedSecondaryTraits(sigil); err == nil {
			allowedSecHashes = make([]uint32, 0, len(allowedTraits))
			for _, trait := range allowedTraits {
				if trait.InternalID == sigil.PrimaryTraitID {
					continue
				}
				if h, ok := traitHashByID[trait.InternalID]; ok {
					allowedSecHashes = append(allowedSecHashes, h)
				}
			}
		}
		result.Sigils = append(result.Sigils, SigilMemoryOption{
			Hash:                        hash,
			DisplayName:                 displaySigilName(sigil),
			MaxLevel:                    &sigilMaxLevel,
			AllowedLevels:               naturalSigilLevelSet,
			FirstTraitMaxLevel:          &primaryMaxLevel,
			PrimaryTraitHash:            primaryTraitHash,
			AllowedPrimaryTraitLevels:   naturalPrimaryLevels,
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
		curve, curveErr := requireTraitLevels(trait, "运行时词条")
		if curveErr != nil {
			return SigilMemoryOptions{}, curveErr
		}
		curveMax := effectCurveMax(curve, 15)
		result.Traits = append(result.Traits, SigilMemoryOption{
			Hash:          hash,
			DisplayName:   cnTrait(trait.DisplayName),
			MaxLevel:      &curveMax,
			AllowedLevels: trait.AllowedLevels,
			Source:        "catalog",
		})
	}
	return result, nil
}

func (a *App) SigilMemoryScan() (SigilMemoryStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return SigilMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	return a.scanSigilMemoryLocked()
}

func (a *App) scanSigilMemoryLocked() (SigilMemoryStatus, error) {
	// Current game build verified at granblue_fantasy_relink.exe+345157.
	// Its first 8 bytes are safe to validate and hook; later bytes vary by build.
	addr := a.moduleBase + sigilMemoryHookRVA
	first := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&first[0]), uintptr(len(first))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取选中因子指令失败: %w", err)
	}
	if isSigilMemoryOriginal(first) {
		a.sigilMemoryOriginal = append(a.sigilMemoryOriginal[:0], first...)
		a.sigilMemoryCaveAddr = 0
	} else if isSigilMemoryJump(first) {
		// readSigilMemoryStatus adopts only the preserving format. A legacy
		// self-owned cave is recognised solely so its hook can be removed.
		a.sigilMemoryCaveAddr = 0
		a.sigilMemoryOriginal = nil
	} else {
		return SigilMemoryStatus{}, fmt.Errorf("选中因子指令字节异常: %s", bytesToHex(first))
	}
	a.sigilMemoryHookAddr = addr
	return a.readSigilMemoryStatus()
}

func (a *App) SigilMemoryGetStatus() (SigilMemoryStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return SigilMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	if a.sigilMemoryHookAddr == 0 {
		return a.scanSigilMemoryLocked()
	}
	return a.readSigilMemoryStatus()
}

func (a *App) grantSigilMemoryOwner(status SigilMemoryStatus) SigilMemoryStatus {
	token := a.nextRuntimeOwnerToken("sigil")
	// The hook page now owns the shared process lifetime. Invalidate any older
	// character-page cleanup before exposing the new hook token.
	a.charaOwnerToken = ""
	a.sigilMemoryOwnerToken = token
	status.OwnerToken = token
	return status
}

func (a *App) SigilMemoryDisable() (SigilMemoryStatus, error) {
	// A never-enabled page can unmount after the game has already exited.
	// Avoid opening a connection solely for that idempotent no-op.
	a.procMu.Lock()
	if a.sigilMemoryOwnerToken != "" {
		a.procMu.Unlock()
		return SigilMemoryStatus{}, errRuntimeOwnerLeaseStale
	}
	idle := a.sigilMemoryHookAddr == 0 && a.sigilMemoryCaveAddr == 0 && len(a.sigilMemoryOriginal) == 0
	a.procMu.Unlock()
	if idle {
		return SigilMemoryStatus{}, nil
	}
	if err := a.acquireLegacyRuntimeMutationLease(runtimeOwnerSigil); err != nil {
		return SigilMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	if err := a.releaseSigilMemoryHookLocked(); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("关闭因子读取失败: %w", err)
	}
	a.sigilMemoryOwnerToken = ""
	return SigilMemoryStatus{}, nil
}

// SigilMemoryRelease tears down the hook only while token is still the latest
// owner. A delayed cleanup from a previous page instance is a no-op.
func (a *App) SigilMemoryRelease(token string) (SigilMemoryStatus, error) {
	a.procMu.Lock()
	if !runtimeOwnerTokenMatches(a.sigilMemoryOwnerToken, token) {
		a.procMu.Unlock()
		return SigilMemoryStatus{}, nil
	}
	idle := a.sigilMemoryHookAddr == 0 && a.sigilMemoryCaveAddr == 0 && len(a.sigilMemoryOriginal) == 0
	if idle {
		a.sigilMemoryOwnerToken = ""
		a.procMu.Unlock()
		return SigilMemoryStatus{}, nil
	}
	a.procMu.Unlock()

	if err := a.acquireGameProcessLease(); err != nil {
		return SigilMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	if !runtimeOwnerTokenMatches(a.sigilMemoryOwnerToken, token) {
		return SigilMemoryStatus{}, nil
	}
	if err := a.releaseSigilMemoryHookLocked(); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("关闭因子读取失败: %w", err)
	}
	a.sigilMemoryOwnerToken = ""
	return SigilMemoryStatus{}, nil
}

func (a *App) SigilMemoryEnable() (SigilMemoryStatus, error) {
	if err := a.acquireLegacyRuntimeMutationLease(runtimeOwnerSigil); err != nil {
		return SigilMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	status, err := a.sigilMemoryEnableLocked()
	if err == nil {
		// Compatibility callers deliberately take an unowned hook lease.
		a.sigilMemoryOwnerToken = ""
	}
	return status, err
}

func (a *App) SigilMemoryAcquire(requestID uint64) (SigilMemoryStatus, error) {
	if err := a.acquireOwnedGameProcessLease(requestID); err != nil {
		return SigilMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	status, err := a.sigilMemoryEnableLocked()
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	return a.grantSigilMemoryOwner(status), nil
}

func (a *App) sigilMemoryEnableLocked() (SigilMemoryStatus, error) {
	var status SigilMemoryStatus
	var err error
	if a.sigilMemoryHookAddr == 0 {
		status, err = a.scanSigilMemoryLocked()
	} else {
		status, err = a.readSigilMemoryStatus()
	}
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	if status.Hooked {
		return status, nil
	}
	if err := a.validateRemoteFunctionStart(a.moduleBase+sigilMemorySaveRVA, "游戏内因子保存函数"); err != nil {
		return SigilMemoryStatus{}, err
	}

	original := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.sigilMemoryHookAddr, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取选中因子原始指令失败: %w", err)
	}
	if !isSigilMemoryOriginal(original) {
		return SigilMemoryStatus{}, fmt.Errorf("选中因子精确指令签名已变化: %s", bytesToHex(original))
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
	if err := writeCodeMemory(a.hProcess, cave, code); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return SigilMemoryStatus{}, fmt.Errorf("写入因子读取代码洞失败: %w", err)
	}
	patch, err := makeRelJump(a.sigilMemoryHookAddr, cave, sigilMemoryHookSize)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return SigilMemoryStatus{}, err
	}
	installResult, err := installRemoteCodeHook(a.hProcess, a.sigilMemoryHookAddr, original, patch)
	if err != nil {
		return SigilMemoryStatus{}, runtimeHookInstallFailure(
			"因子读取 Hook", installResult, err,
			func() { _ = virtualFreeRemote(a.hProcess, cave) },
			func() { a.retireRuntimeCaveLocked(cave, "sigil-memory install rollback") },
			func() {
				a.sigilMemoryCaveAddr = cave
				a.sigilMemoryOriginal = append(a.sigilMemoryOriginal[:0], original...)
			},
			a.poisonCurrentLiveMemoryWrites,
		)
	}
	a.sigilMemoryCaveAddr = cave
	a.sigilMemoryOriginal = append(a.sigilMemoryOriginal[:0], original...)
	return finalizeRuntimeHookEnable(
		"因子读取 Hook",
		a.readSigilMemoryStatus,
		a.releaseSigilMemoryHookLocked,
		a.poisonCurrentLiveMemoryWrites,
	)
}

func (a *App) SigilMemoryUpdate(update SigilMemoryUpdate) (SigilMemoryStatus, error) {
	return a.sigilMemoryUpdate("", false, update)
}

func (a *App) SigilMemoryUpdateOwned(token string, update SigilMemoryUpdate) (SigilMemoryStatus, error) {
	return a.sigilMemoryUpdate(token, true, update)
}

func (a *App) sigilMemoryUpdate(token string, owned bool, update SigilMemoryUpdate) (SigilMemoryStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	sigilMemoryWriteMu.Lock()
	defer sigilMemoryWriteMu.Unlock()
	var leaseErr error
	if owned {
		leaseErr = a.acquireOwnedRuntimeWriteLease(runtimeOwnerSigil, token)
	} else {
		leaseErr = a.acquireLegacyRuntimeMutationLease(runtimeOwnerSigil)
	}
	if leaseErr != nil {
		return SigilMemoryStatus{}, leaseErr
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return SigilMemoryStatus{}, err
	}
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	var status SigilMemoryStatus
	var err error
	if a.sigilMemoryHookAddr == 0 {
		status, err = a.scanSigilMemoryLocked()
	} else {
		status, err = a.readSigilMemoryStatus()
	}
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	if !status.Hooked || status.SelectedAddr == 0 {
		return SigilMemoryStatus{}, fmt.Errorf("请先开启读取，并在游戏内因子列表选中一个因子")
	}
	catalog, err := LoadCatalog()
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	if err := validateSigilMemoryWriteRequest(catalog, update); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("因子写入参数无效: %w", err)
	}

	var selected uintptr
	if a.sigilMemoryCaveAddr == 0 {
		return SigilMemoryStatus{}, fmt.Errorf("因子读取代码洞尚未就绪")
	}
	if err := readProcessMemory(a.hProcess, a.sigilMemoryCaveAddr+sigilMemoryCaveDataOffset, unsafe.Pointer(&selected), unsafe.Sizeof(selected)); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("写入前复核选中因子指针失败: %w", err)
	}
	base, err := validateSigilMemorySelection(uintptr(update.ExpectedSelectedAddr), uintptr(status.SelectedAddr), selected)
	if err != nil {
		return SigilMemoryStatus{}, err
	}

	original := make([]byte, sigilMemoryRecordSize)
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("读取因子原记录失败: %w", err)
	}
	desired, err := encodeSigilMemoryRecord(original, update)
	if err != nil {
		return SigilMemoryStatus{}, err
	}
	if err := snapshotBeforeLiveSaveChange("游戏内因子写入前自动备份"); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}
	confirmedStatus, err := a.readSigilMemoryStatus()
	if err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("自动备份后复核因子状态失败: %w", err)
	}
	var confirmedSelected uintptr
	if err := readProcessMemory(a.hProcess, a.sigilMemoryCaveAddr+sigilMemoryCaveDataOffset, unsafe.Pointer(&confirmedSelected), unsafe.Sizeof(confirmedSelected)); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("自动备份后复核因子指针失败: %w", err)
	}
	confirmedRecord := make([]byte, sigilMemoryRecordSize)
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&confirmedRecord[0]), uintptr(len(confirmedRecord))); err != nil {
		return SigilMemoryStatus{}, fmt.Errorf("自动备份后复核因子记录失败: %w", err)
	}
	if err := validateSigilMemorySnapshot(base, uintptr(confirmedStatus.SelectedAddr), confirmedSelected, original, confirmedRecord); err != nil {
		return SigilMemoryStatus{}, err
	}

	writer := func(record []byte) error {
		if len(record) != sigilMemoryRecordSize {
			return fmt.Errorf("因子记录长度异常: %d", len(record))
		}
		return writeProcessMemory(a.hProcess, base, unsafe.Pointer(&record[0]), uintptr(len(record)))
	}
	reader := func() ([]byte, error) {
		record := make([]byte, sigilMemoryRecordSize)
		if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&record[0]), uintptr(len(record))); err != nil {
			return nil, err
		}
		return record, nil
	}
	if err := writeSigilMemoryRecordAtomic(original, desired, writer, func() error { return a.saveSigilMemory(base) }, reader); err != nil {
		if isRemoteCallIndeterminate(err) || errors.Is(err, errLiveMemoryRollbackUnproven) {
			a.poisonCurrentLiveMemoryWrites()
			_ = a.clearSigilMemorySelection()
		}
		return SigilMemoryStatus{}, fmt.Errorf("因子原子写入失败: %w", err)
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
	fn := a.moduleBase + sigilMemorySaveRVA
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
	buf := make([]byte, sigilMemoryHookSize)
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
		SaveRVA:      uint64(sigilMemorySaveRVA),
		CurrentBytes: bytesToHex(buf),
	}
	if !hooked {
		return status, nil
	}
	cave := relJumpTarget(a.sigilMemoryHookAddr, buf)
	if a.sigilMemoryCaveAddr != 0 && cave != a.sigilMemoryCaveAddr {
		return SigilMemoryStatus{}, fmt.Errorf("因子读取 Hook 跳转目标已被替换")
	}
	original, err := a.recoverSigilMemoryHook(cave)
	if err != nil {
		legacyOriginal, legacyErr := a.recoverLegacySigilMemoryHook(cave)
		if legacyErr != nil {
			return SigilMemoryStatus{}, fmt.Errorf("校验选中因子 Hook 所有权失败: 新洞 %v；旧洞 %w", err, legacyErr)
		}
		if err := writeCodeMemory(a.hProcess, a.sigilMemoryHookAddr, legacyOriginal); err != nil {
			return SigilMemoryStatus{}, fmt.Errorf("卸载旧版因子 Hook 失败: %w", err)
		}
		restored := make([]byte, sigilMemoryHookSize)
		if err := readProcessMemory(a.hProcess, a.sigilMemoryHookAddr, unsafe.Pointer(&restored[0]), uintptr(len(restored))); err != nil || !bytes.Equal(restored, legacyOriginal) {
			return SigilMemoryStatus{}, fmt.Errorf("卸载旧版因子 Hook 后无法确认原始指令已恢复")
		}
		// The exact legacy cave clobbers R10. It is recognised only so the
		// entry point can be restored; it is never adopted as a writable cave.
		a.sigilMemoryCaveAddr = 0
		a.sigilMemoryOriginal = append(a.sigilMemoryOriginal[:0], legacyOriginal...)
		status.Hooked = false
		status.CurrentBytes = bytesToHex(legacyOriginal)
		return status, nil
	}
	if len(a.sigilMemoryOriginal) == sigilMemoryHookSize && !bytes.Equal(a.sigilMemoryOriginal, original) {
		return SigilMemoryStatus{}, fmt.Errorf("因子读取 Hook 原始指令缓存与代码洞不一致")
	}
	a.sigilMemoryCaveAddr = cave
	a.sigilMemoryOriginal = original
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
		status.SigilName = sigilMemoryNameByHash(sigilMemorySigils, status.SigilHash)
	}
	if status.SigilName == "" {
		status.SigilName = localizedRuntimeName(status.SigilHash)
	}
	if status.SigilName == "" {
		status.SigilName = fmt.Sprintf("0x%08X", status.SigilHash)
	}
	if status.PrimaryTraitName == "" {
		status.PrimaryTraitName = sigilMemoryNameByHash(sigilMemoryTraits, status.PrimaryTraitHash)
	}
	if status.PrimaryTraitName == "" {
		status.PrimaryTraitName = localizedRuntimeName(status.PrimaryTraitHash)
	}
	if status.PrimaryTraitName == "" {
		status.PrimaryTraitName = fmt.Sprintf("0x%08X", status.PrimaryTraitHash)
	}
	if status.SecondaryTraitName == "" {
		status.SecondaryTraitName = sigilMemoryNameByHash(sigilMemoryTraits, status.SecondaryTraitHash)
	}
	if status.SecondaryTraitName == "" {
		status.SecondaryTraitName = localizedRuntimeName(status.SecondaryTraitHash)
	}
	if status.SecondaryTraitName == "" {
		status.SecondaryTraitName = fmt.Sprintf("0x%08X", status.SecondaryTraitHash)
	}
	return status, nil
}

func isSigilMemoryOriginal(buf []byte) bool {
	return len(buf) >= sigilMemoryHookSize && bytes.Equal(buf[:sigilMemoryHookSize], sigilMemoryOriginalBytes)
}

func isSigilMemoryJump(buf []byte) bool {
	return len(buf) >= sigilMemoryHookSize && buf[0] == 0xE9 && buf[5] == 0x90 && buf[6] == 0x90 && buf[7] == 0x90
}

func (a *App) recoverSigilMemoryHook(cave uintptr) ([]byte, error) {
	if cave == 0 {
		return nil, fmt.Errorf("代码洞地址为空")
	}
	code := make([]byte, sigilMemoryCaveDataOffset)
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		return nil, fmt.Errorf("读取代码洞失败: %w", err)
	}
	if code[0] != 0x41 || code[1] != 0x52 || code[2] != 0x49 || code[3] != 0xBA ||
		code[12] != 0x49 || code[13] != 0x89 || code[14] != 0x02 || code[15] != 0x41 || code[16] != 0x5A {
		return nil, fmt.Errorf("代码洞签名不匹配")
	}
	dataAddr := uintptr(binary.LittleEndian.Uint64(code[4:12]))
	if dataAddr != cave+sigilMemoryCaveDataOffset {
		return nil, fmt.Errorf("代码洞数据地址不匹配")
	}
	if !bytes.Equal(code[sigilMemoryMarkerOffset:sigilMemoryMarkerOffset+uintptr(len(sigilMemoryMarker))], sigilMemoryMarker) {
		return nil, fmt.Errorf("代码洞所有权标记不匹配")
	}
	original := append([]byte(nil), code[sigilMemoryOriginalOffset:sigilMemoryOriginalOffset+sigilMemoryHookSize]...)
	if !isSigilMemoryOriginal(original) {
		return nil, fmt.Errorf("原始指令签名不匹配: %s", bytesToHex(original))
	}
	jumpOffset := sigilMemoryOriginalOffset + sigilMemoryHookSize
	if target := relJumpTarget(cave+jumpOffset, code[jumpOffset:jumpOffset+5]); target != a.sigilMemoryHookAddr+sigilMemoryHookSize {
		return nil, fmt.Errorf("代码洞回跳地址不匹配")
	}
	return original, nil
}

func (a *App) recoverLegacySigilMemoryHook(cave uintptr) ([]byte, error) {
	if cave == 0 {
		return nil, fmt.Errorf("旧版代码洞地址为空")
	}
	codeLen := sigilMemoryLegacyOriginalOffset + sigilMemoryHookSize + 5
	code := make([]byte, codeLen)
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		return nil, fmt.Errorf("读取旧版代码洞失败: %w", err)
	}
	if code[0] != 0x49 || code[1] != 0xBA || code[10] != 0x49 || code[11] != 0x89 || code[12] != 0x02 {
		return nil, fmt.Errorf("旧版代码洞签名不匹配")
	}
	dataAddr := uintptr(binary.LittleEndian.Uint64(code[2:10]))
	if dataAddr != cave+sigilMemoryCaveDataOffset {
		return nil, fmt.Errorf("旧版代码洞数据地址不匹配")
	}
	original := append([]byte(nil), code[sigilMemoryLegacyOriginalOffset:sigilMemoryLegacyOriginalOffset+sigilMemoryHookSize]...)
	if !isSigilMemoryOriginal(original) {
		return nil, fmt.Errorf("旧版洞原始指令签名不匹配: %s", bytesToHex(original))
	}
	jumpOffset := sigilMemoryLegacyOriginalOffset + sigilMemoryHookSize
	if target := relJumpTarget(cave+jumpOffset, code[jumpOffset:jumpOffset+5]); target != a.sigilMemoryHookAddr+sigilMemoryHookSize {
		return nil, fmt.Errorf("旧版代码洞回跳地址不匹配")
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
	sigilMemoryLifecycleMu.Lock()
	defer sigilMemoryLifecycleMu.Unlock()
	return a.releaseSigilMemoryHookLocked()
}

func (a *App) releaseSigilMemoryHookLocked() error {
	if a.hProcess == 0 || a.sigilMemoryHookAddr == 0 {
		return nil
	}
	current := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.sigilMemoryHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return err
	}
	if isSigilMemoryOriginal(current) {
		a.sigilMemoryHookAddr = 0
		a.sigilMemoryCaveAddr = 0
		a.sigilMemoryOriginal = nil
		return nil
	}
	if !isSigilMemoryJump(current) {
		return fmt.Errorf("因子 Hook 入口既不是完整自有跳转也不是原始指令: %s", bytesToHex(current))
	}
	cave := relJumpTarget(a.sigilMemoryHookAddr, current)
	if a.sigilMemoryCaveAddr != 0 && cave != a.sigilMemoryCaveAddr {
		return fmt.Errorf("因子 Hook 跳转目标已被替换，拒绝覆盖外部 Hook")
	}
	original, err := a.recoverSigilMemoryHook(cave)
	if err != nil {
		legacyOriginal, legacyErr := a.recoverLegacySigilMemoryHook(cave)
		if legacyErr != nil {
			return fmt.Errorf("恢复因子 Hook 前所有权校验失败: 新洞 %v；旧洞 %w", err, legacyErr)
		}
		original = legacyOriginal
	}
	if len(a.sigilMemoryOriginal) == sigilMemoryHookSize && !bytes.Equal(a.sigilMemoryOriginal, original) {
		return fmt.Errorf("因子 Hook 原始指令缓存与代码洞不一致，拒绝恢复")
	}
	if err := writeCodeMemory(a.hProcess, a.sigilMemoryHookAddr, original); err != nil {
		return fmt.Errorf("恢复选中因子原始指令失败: %w", err)
	}
	restored := make([]byte, sigilMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.sigilMemoryHookAddr, unsafe.Pointer(&restored[0]), uintptr(len(restored))); err != nil {
		return fmt.Errorf("恢复选中因子原始指令后回读失败: %w", err)
	}
	if !bytes.Equal(restored, original) {
		return fmt.Errorf("恢复选中因子原始指令后回读不一致: %s", bytesToHex(restored))
	}
	// Do not free the remote page here: a game thread may already be inside the
	// cave. The OS reclaims this single page when the game exits.
	a.sigilMemoryHookAddr = 0
	a.sigilMemoryCaveAddr = 0
	a.sigilMemoryOriginal = nil
	return nil
}

func buildSigilMemoryCave(cave, returnAddr uintptr, original []byte) ([]byte, error) {
	if len(original) != sigilMemoryHookSize || !isSigilMemoryOriginal(original) {
		return nil, fmt.Errorf("选中因子原始指令长度异常")
	}
	code := make([]byte, 0, sigilMemoryCaveDataOffset+8)
	code = append(code, 0x41, 0x52, 0x49, 0xBA) // push r10; mov r10, cave data address
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+sigilMemoryCaveDataOffset))
	code = append(code, 0x49, 0x89, 0x02, 0x41, 0x5A) // mov [r10], rax; pop r10
	code = append(code, original...)
	jmp, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jmp...)
	for len(code) < int(sigilMemoryMarkerOffset) {
		code = append(code, 0)
	}
	code = append(code, sigilMemoryMarker...)
	for len(code) < int(sigilMemoryCaveDataOffset)+8 {
		code = append(code, 0)
	}
	return code, nil
}
