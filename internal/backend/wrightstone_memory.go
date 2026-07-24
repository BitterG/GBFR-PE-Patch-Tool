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
	// This DLC 2.0.2 instruction captures RDX while the game opens or refreshes
	// the currently viewed wrightstone record. The RVA and full instruction
	// window below were independently matched in the local 2.0.2 executable.
	wrightstoneMemoryHookRVA        = uintptr(0x361CB4)
	wrightstoneMemorySaveRVA        = uintptr(0x79D820)
	wrightstoneMemoryHookSize       = uintptr(6)
	wrightstoneMemoryCaveDataOffset = uintptr(0x40)
	wrightstoneMemoryOriginalOffset = uintptr(17)
	wrightstoneMemoryMarkerOffset   = uintptr(0x30)
)

var (
	// The six displaced instructions are insufficient as a version guard on
	// their own. Keep the complete DLC 2.0.2 instruction window, including its
	// local CALL and following record-ID read, so a coincidental MOV pair cannot pass.
	wrightstoneMemoryOriginalBytes = []byte{0x48, 0x89, 0xD7, 0x48, 0x89, 0xCE}
	wrightstoneMemoryGuardBytes    = []byte{
		0x48, 0x89, 0xD7, 0x48, 0x89, 0xCE, 0xE8, 0xF1, 0x05, 0xFC, 0xFF,
		0x48, 0x39, 0xFE, 0x74, 0x4C, 0x48, 0x8D, 0x4E, 0x18, 0x8B, 0x47, 0x18,
	}
	wrightstoneMemoryMarker      = []byte("GBFRWTM2")
	wrightstoneMemoryLifecycleMu sync.Mutex
)

type WrightstoneMemoryOption struct {
	Hash          uint32 `json:"hash"`
	DisplayName   string `json:"displayName"`
	MaxLevel      *int   `json:"maxLevel,omitempty"`
	AllowedLevels []int  `json:"allowedLevels,omitempty"`
}

type WrightstoneMemoryOptions struct {
	Traits []WrightstoneMemoryOption `json:"traits"`
}

type WrightstoneMemoryStatus struct {
	OwnerToken    string `json:"ownerToken,omitempty"`
	Found         bool   `json:"found"`
	Hooked        bool   `json:"hooked"`
	Address       uint64 `json:"address"`
	RVA           uint64 `json:"rva"`
	SelectedAddr  uint64 `json:"selectedAddr"`
	SaveRVA       uint64 `json:"saveRva"`
	CurrentBytes  string `json:"currentBytes"`
	CaptureSource string `json:"captureSource"`
	SourceVersion string `json:"sourceVersion"`
	FirstHash     uint32 `json:"firstHash"`
	FirstName     string `json:"firstName"`
	FirstLevel    uint32 `json:"firstLevel"`
	SecondHash    uint32 `json:"secondHash"`
	SecondName    string `json:"secondName"`
	SecondLevel   uint32 `json:"secondLevel"`
	ThirdHash     uint32 `json:"thirdHash"`
	ThirdName     string `json:"thirdName"`
	ThirdLevel    uint32 `json:"thirdLevel"`
}

func (a *App) WrightstoneMemoryGetOptions() (WrightstoneMemoryOptions, error) {
	catalog, err := LoadWrightstoneCatalog()
	if err != nil {
		return WrightstoneMemoryOptions{}, err
	}
	result := WrightstoneMemoryOptions{Traits: make([]WrightstoneMemoryOption, 0, len(catalog.Traits))}
	seen := make(map[uint32]struct{}, len(catalog.Traits))
	for _, trait := range catalog.GetTraitSortedList() {
		hash, err := ParseHashHex(trait.Hash)
		if err != nil {
			continue
		}
		if _, exists := seen[hash]; exists {
			continue
		}
		seen[hash] = struct{}{}
		levels, _ := requireWrightstoneTraitLevels(trait)
		result.Traits = append(result.Traits, WrightstoneMemoryOption{
			Hash:          hash,
			DisplayName:   cnWrightstoneTrait(trait.DisplayName),
			MaxLevel:      trait.MaxLevel,
			AllowedLevels: levels,
		})
	}
	return result, nil
}

func (a *App) WrightstoneMemoryScan() (WrightstoneMemoryStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()
	return a.scanWrightstoneMemoryLocked()
}

func (a *App) scanWrightstoneMemoryLocked() (WrightstoneMemoryStatus, error) {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("未连接游戏进程")
	}
	addr := a.moduleBase + wrightstoneMemoryHookRVA
	guard := make([]byte, len(wrightstoneMemoryGuardBytes))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&guard[0]), uintptr(len(guard))); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("读取祝福焦点指令失败: %w", err)
	}
	first := guard[:wrightstoneMemoryHookSize]
	original := isWrightstoneMemoryGuard(guard, false)
	hooked := isWrightstoneMemoryGuard(guard, true)
	if !original && !hooked {
		// The save function uses a version-specific fixed RVA too. Relocating only
		// this hook from an eight-byte match would create a false sense of version
		// compatibility and could later start a thread at the wrong function.
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福焦点指令字节异常 (%s)：当前游戏版本未通过 2.0.2 / DLC 2.0.2 runtime catalog 完整守卫", bytesToHex(guard))
	}

	a.wrightstoneMemoryHookAddr = addr
	if original {
		a.wrightstoneMemoryOriginal = append(a.wrightstoneMemoryOriginal[:0], first...)
		a.wrightstoneMemoryCaveAddr = 0
	} else {
		cave := relJumpTarget(addr, first)
		original, err := a.recoverWrightstoneMemoryHookLocked(cave)
		if err != nil {
			a.wrightstoneMemoryHookAddr = 0
			return WrightstoneMemoryStatus{}, fmt.Errorf("祝福读取 Hook 无法接管: %w", err)
		}
		a.wrightstoneMemoryCaveAddr = cave
		a.wrightstoneMemoryOriginal = original
	}
	return a.readWrightstoneMemoryStatusLocked()
}

func (a *App) WrightstoneMemoryGetStatus() (WrightstoneMemoryStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()
	if a.wrightstoneMemoryHookAddr == 0 {
		return a.scanWrightstoneMemoryLocked()
	}
	return a.readWrightstoneMemoryStatusLocked()
}

func (a *App) WrightstoneMemoryEnable() (WrightstoneMemoryStatus, error) {
	if err := a.acquireLegacyRuntimeMutationLease(runtimeOwnerWrightstone); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()
	status, err := a.wrightstoneMemoryEnableLocked()
	if err == nil {
		// Compatibility callers deliberately take an unowned hook lease.
		a.wrightstoneMemoryOwnerToken = ""
	}
	return status, err
}

func (a *App) WrightstoneMemoryAcquire(requestID uint64) (WrightstoneMemoryStatus, error) {
	if err := a.acquireOwnedGameProcessLease(requestID); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()
	status, err := a.wrightstoneMemoryEnableLocked()
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	return a.grantWrightstoneMemoryOwner(status), nil
}

func (a *App) grantWrightstoneMemoryOwner(status WrightstoneMemoryStatus) WrightstoneMemoryStatus {
	token := a.nextRuntimeOwnerToken("wrightstone")
	a.charaOwnerToken = ""
	a.wrightstoneMemoryOwnerToken = token
	status.OwnerToken = token
	return status
}

func (a *App) wrightstoneMemoryEnableLocked() (WrightstoneMemoryStatus, error) {
	var status WrightstoneMemoryStatus
	var err error
	if a.wrightstoneMemoryHookAddr == 0 {
		status, err = a.scanWrightstoneMemoryLocked()
	} else {
		status, err = a.readWrightstoneMemoryStatusLocked()
	}
	if err != nil || status.Hooked {
		return status, err
	}
	if err := a.validateRemoteFunctionStart(a.moduleBase+wrightstoneMemorySaveRVA, "游戏内祝福保存函数"); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	original := make([]byte, wrightstoneMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryHookAddr, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("读取祝福焦点原始指令失败: %w", err)
	}
	if !isWrightstoneMemoryOriginal(original) {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福焦点原始指令已变化: %s", bytesToHex(original))
	}
	cave, err := virtualAllocRemoteNear(a.hProcess, a.wrightstoneMemoryHookAddr, 0x1000)
	if err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("分配祝福读取代码洞失败: %w", err)
	}
	code, err := buildWrightstoneMemoryCave(cave, a.wrightstoneMemoryHookAddr+wrightstoneMemoryHookSize, original)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return WrightstoneMemoryStatus{}, err
	}
	if err := writeCodeMemory(a.hProcess, cave, code); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return WrightstoneMemoryStatus{}, fmt.Errorf("写入祝福读取代码洞失败: %w", err)
	}
	patch, err := makeRelJump(a.wrightstoneMemoryHookAddr, cave, int(wrightstoneMemoryHookSize))
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return WrightstoneMemoryStatus{}, err
	}
	installResult, err := installRemoteCodeHook(a.hProcess, a.wrightstoneMemoryHookAddr, original, patch)
	if err != nil {
		return WrightstoneMemoryStatus{}, runtimeHookInstallFailure(
			"祝福读取 Hook", installResult, err,
			func() { _ = virtualFreeRemote(a.hProcess, cave) },
			func() { a.retireRuntimeCaveLocked(cave, "wrightstone-memory install rollback") },
			func() {
				a.wrightstoneMemoryCaveAddr = cave
				a.wrightstoneMemoryOriginal = append(a.wrightstoneMemoryOriginal[:0], original...)
			},
			a.poisonCurrentLiveMemoryWrites,
		)
	}
	a.wrightstoneMemoryCaveAddr = cave
	a.wrightstoneMemoryOriginal = append(a.wrightstoneMemoryOriginal[:0], original...)
	return finalizeRuntimeHookEnable(
		"祝福读取 Hook",
		a.readWrightstoneMemoryStatusLocked,
		a.releaseWrightstoneMemoryHookLocked,
		a.poisonCurrentLiveMemoryWrites,
	)
}

func (a *App) WrightstoneMemoryUpdate(update WrightstoneMemoryUpdate) (WrightstoneMemoryStatus, error) {
	return a.wrightstoneMemoryUpdate("", false, update)
}

func (a *App) WrightstoneMemoryUpdateOwned(token string, update WrightstoneMemoryUpdate) (WrightstoneMemoryStatus, error) {
	return a.wrightstoneMemoryUpdate(token, true, update)
}

func (a *App) wrightstoneMemoryUpdate(token string, owned bool, update WrightstoneMemoryUpdate) (WrightstoneMemoryStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	wrightstoneMemoryWriteMu.Lock()
	defer wrightstoneMemoryWriteMu.Unlock()
	var leaseErr error
	if owned {
		leaseErr = a.acquireOwnedRuntimeWriteLease(runtimeOwnerWrightstone, token)
	} else {
		leaseErr = a.acquireLegacyRuntimeMutationLease(runtimeOwnerWrightstone)
	}
	if leaseErr != nil {
		return WrightstoneMemoryStatus{}, leaseErr
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()

	var status WrightstoneMemoryStatus
	var err error
	if a.wrightstoneMemoryHookAddr == 0 {
		status, err = a.scanWrightstoneMemoryLocked()
	} else {
		status, err = a.readWrightstoneMemoryStatusLocked()
	}
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	if !status.Hooked || status.SelectedAddr == 0 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("请先开启读取，并在游戏内背包中选中一个祝福")
	}
	catalog, err := LoadWrightstoneCatalog()
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	if err := validateWrightstoneMemoryWriteRequest(catalog, update); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福写入参数无效: %w", err)
	}
	var selected uintptr
	if a.wrightstoneMemoryCaveAddr == 0 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福读取代码洞尚未就绪")
	}
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryCaveAddr+wrightstoneMemoryCaveDataOffset, unsafe.Pointer(&selected), unsafe.Sizeof(selected)); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("写入前复核选中祝福指针失败: %w", err)
	}
	base, err := validateWrightstoneMemorySelection(uintptr(update.ExpectedSelectedAddr), uintptr(status.SelectedAddr), selected)
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	original := make([]byte, wrightstoneMemoryRecordSize)
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("读取祝福原记录失败: %w", err)
	}
	desired, err := encodeWrightstoneMemoryRecord(original, update)
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	if err := snapshotBeforeLiveSaveChange("游戏内祝福写入前自动备份"); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}
	// Backup and its filesystem work are intentionally outside the target
	// record. Revalidate both the hook-owned pointer and the record bytes after
	// that potentially slow step so inventory rebuilds cannot stale the write.
	confirmedStatus, err := a.readWrightstoneMemoryStatusLocked()
	if err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("自动备份后复核祝福状态失败: %w", err)
	}
	var confirmedSelected uintptr
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryCaveAddr+wrightstoneMemoryCaveDataOffset, unsafe.Pointer(&confirmedSelected), unsafe.Sizeof(confirmedSelected)); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("自动备份后复核祝福指针失败: %w", err)
	}
	confirmedRecord := make([]byte, wrightstoneMemoryRecordSize)
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&confirmedRecord[0]), uintptr(len(confirmedRecord))); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("自动备份后复核祝福记录失败: %w", err)
	}
	if err := validateWrightstoneMemorySnapshot(base, uintptr(confirmedStatus.SelectedAddr), confirmedSelected, original, confirmedRecord); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	writer := func(record []byte) error {
		if len(record) != wrightstoneMemoryRecordSize {
			return fmt.Errorf("祝福记录长度异常: %d", len(record))
		}
		return writeProcessMemory(a.hProcess, base, unsafe.Pointer(&record[0]), uintptr(len(record)))
	}
	reader := func() ([]byte, error) {
		record := make([]byte, wrightstoneMemoryRecordSize)
		if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&record[0]), uintptr(len(record))); err != nil {
			return nil, err
		}
		return record, nil
	}
	if err := writeWrightstoneMemoryRecordAtomic(original, desired, writer, func() error { return a.saveWrightstoneMemory(base) }, reader); err != nil {
		if isRemoteCallIndeterminate(err) || errors.Is(err, errLiveMemoryRollbackUnproven) {
			a.poisonCurrentLiveMemoryWrites()
			_ = a.clearWrightstoneMemorySelectionLocked()
		}
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福原子写入失败: %w", err)
	}
	result, err := a.readWrightstoneMemoryStatusLocked()
	if err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	if err := a.clearWrightstoneMemorySelectionLocked(); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	result.SelectedAddr = 0
	return result, nil
}

// WrightstoneMemoryRelease tears down only the hook lease identified by token.
func (a *App) WrightstoneMemoryRelease(token string) (WrightstoneMemoryStatus, error) {
	a.procMu.Lock()
	if !runtimeOwnerTokenMatches(a.wrightstoneMemoryOwnerToken, token) {
		a.procMu.Unlock()
		return WrightstoneMemoryStatus{}, nil
	}
	idle := a.wrightstoneMemoryHookAddr == 0 && a.wrightstoneMemoryCaveAddr == 0 && len(a.wrightstoneMemoryOriginal) == 0
	if idle {
		a.wrightstoneMemoryOwnerToken = ""
		a.procMu.Unlock()
		return WrightstoneMemoryStatus{}, nil
	}
	a.procMu.Unlock()

	if err := a.acquireGameProcessLease(); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()
	if !runtimeOwnerTokenMatches(a.wrightstoneMemoryOwnerToken, token) {
		return WrightstoneMemoryStatus{}, nil
	}
	if err := a.releaseWrightstoneMemoryHookLocked(); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("关闭祝福读取失败: %w", err)
	}
	a.wrightstoneMemoryOwnerToken = ""
	return WrightstoneMemoryStatus{}, nil
}

func (a *App) WrightstoneMemoryDisable() (WrightstoneMemoryStatus, error) {
	// Preserve idempotence when this page was never enabled, while ensuring any
	// actual hook teardown pins the process until the lifecycle lock is released.
	a.procMu.Lock()
	if a.wrightstoneMemoryOwnerToken != "" {
		a.procMu.Unlock()
		return WrightstoneMemoryStatus{}, errRuntimeOwnerLeaseStale
	}
	idle := a.wrightstoneMemoryHookAddr == 0 && a.wrightstoneMemoryCaveAddr == 0 && len(a.wrightstoneMemoryOriginal) == 0
	a.procMu.Unlock()
	if idle {
		return WrightstoneMemoryStatus{}, nil
	}
	if err := a.acquireLegacyRuntimeMutationLease(runtimeOwnerWrightstone); err != nil {
		return WrightstoneMemoryStatus{}, err
	}
	defer a.procMu.Unlock()
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()
	if err := a.releaseWrightstoneMemoryHookLocked(); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("关闭祝福读取失败: %w", err)
	}
	a.wrightstoneMemoryOwnerToken = ""
	return WrightstoneMemoryStatus{}, nil
}

func (a *App) saveWrightstoneMemory(base uintptr) error {
	fn := a.moduleBase + wrightstoneMemorySaveRVA
	for offset := uintptr(0); offset < wrightstoneMemoryRecordSize; offset += 4 {
		if err := a.callRemoteOneArg(fn, base+offset); err != nil {
			return fmt.Errorf("保存祝福字段 +0x%02X 失败: %w", offset, err)
		}
	}
	return nil
}

func (a *App) readWrightstoneMemoryStatusLocked() (WrightstoneMemoryStatus, error) {
	if a.hProcess == 0 || a.wrightstoneMemoryHookAddr == 0 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("未定位祝福焦点指令")
	}
	current := make([]byte, wrightstoneMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("读取祝福 Hook 指令失败: %w", err)
	}
	hooked := isWrightstoneMemoryJump(current)
	if !hooked && !isWrightstoneMemoryOriginal(current) {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福焦点指令字节异常: %s", bytesToHex(current))
	}
	status := newWrightstoneMemoryStatus(true, hooked, a.wrightstoneMemoryHookAddr, a.moduleBase, current)
	if !hooked {
		return status, nil
	}
	cave := relJumpTarget(a.wrightstoneMemoryHookAddr, current)
	if cave == 0 {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福读取 Hook 跳转目标为空")
	}
	if a.wrightstoneMemoryCaveAddr != 0 && a.wrightstoneMemoryCaveAddr != cave {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福读取 Hook 跳转目标已从 0x%X 变为 0x%X", a.wrightstoneMemoryCaveAddr, cave)
	}
	original, err := a.recoverWrightstoneMemoryHookLocked(cave)
	if err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("校验祝福读取 Hook 失败: %w", err)
	}
	if len(a.wrightstoneMemoryOriginal) == int(wrightstoneMemoryHookSize) && !bytes.Equal(a.wrightstoneMemoryOriginal, original) {
		return WrightstoneMemoryStatus{}, fmt.Errorf("祝福读取 Hook 保存的原始指令已变化")
	}
	a.wrightstoneMemoryCaveAddr = cave
	a.wrightstoneMemoryOriginal = original
	var selected uintptr
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryCaveAddr+wrightstoneMemoryCaveDataOffset, unsafe.Pointer(&selected), unsafe.Sizeof(selected)); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("读取选中祝福指针失败: %w", err)
	}
	status.SelectedAddr = uint64(selected)
	if selected == 0 {
		return status, nil
	}
	values := make([]byte, wrightstoneMemoryRecordSize)
	if err := readProcessMemory(a.hProcess, selected, unsafe.Pointer(&values[0]), uintptr(len(values))); err != nil {
		return WrightstoneMemoryStatus{}, fmt.Errorf("读取选中祝福数据失败: %w", err)
	}
	status.FirstHash = binary.LittleEndian.Uint32(values[0x00:0x04])
	status.FirstLevel = binary.LittleEndian.Uint32(values[0x04:0x08])
	status.SecondHash = binary.LittleEndian.Uint32(values[0x08:0x0C])
	status.SecondLevel = binary.LittleEndian.Uint32(values[0x0C:0x10])
	status.ThirdHash = binary.LittleEndian.Uint32(values[0x10:0x14])
	status.ThirdLevel = binary.LittleEndian.Uint32(values[0x14:0x18])
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
	status.FirstName = wrightstoneMemoryDisplayName(status.FirstHash, status.FirstName)
	status.SecondName = wrightstoneMemoryDisplayName(status.SecondHash, status.SecondName)
	status.ThirdName = wrightstoneMemoryDisplayName(status.ThirdHash, status.ThirdName)
	return status, nil
}

func wrightstoneMemoryDisplayName(hash uint32, current string) string {
	if current != "" {
		return current
	}
	if isEmptyWrightstoneMemoryTrait(hash) {
		if useChinese() {
			return "不选择"
		}
		return "None"
	}
	if name := localizedRuntimeName(hash); name != "" {
		return name
	}
	return fmt.Sprintf("0x%08X", hash)
}

func isWrightstoneMemoryOriginal(buf []byte) bool {
	return len(buf) >= int(wrightstoneMemoryHookSize) && bytes.Equal(buf[:wrightstoneMemoryHookSize], wrightstoneMemoryOriginalBytes)
}

func isWrightstoneMemoryGuard(buf []byte, hooked bool) bool {
	if len(buf) < len(wrightstoneMemoryGuardBytes) {
		return false
	}
	if !hooked {
		return bytes.Equal(buf[:len(wrightstoneMemoryGuardBytes)], wrightstoneMemoryGuardBytes)
	}
	return isWrightstoneMemoryJump(buf[:wrightstoneMemoryHookSize]) &&
		bytes.Equal(buf[wrightstoneMemoryHookSize:len(wrightstoneMemoryGuardBytes)], wrightstoneMemoryGuardBytes[wrightstoneMemoryHookSize:])
}

func isWrightstoneMemoryJump(buf []byte) bool {
	if len(buf) < int(wrightstoneMemoryHookSize) || buf[0] != 0xE9 {
		return false
	}
	for index := 5; index < int(wrightstoneMemoryHookSize); index++ {
		if buf[index] != 0x90 {
			return false
		}
	}
	return true
}

func newWrightstoneMemoryStatus(found, hooked bool, hookAddr, moduleBase uintptr, current []byte) WrightstoneMemoryStatus {
	return WrightstoneMemoryStatus{
		Found:         found,
		Hooked:        hooked,
		Address:       uint64(hookAddr),
		RVA:           uint64(hookAddr - moduleBase),
		SaveRVA:       uint64(wrightstoneMemorySaveRVA),
		CurrentBytes:  bytesToHex(current),
		CaptureSource: "dlcSupplement-current-view",
		SourceVersion: "2.0.2",
	}
}

func buildWrightstoneMemoryCave(cave, returnAddr uintptr, original []byte) ([]byte, error) {
	if len(original) != int(wrightstoneMemoryHookSize) || !isWrightstoneMemoryOriginal(original) {
		return nil, fmt.Errorf("祝福焦点原始指令长度或签名异常")
	}
	code := make([]byte, 0, wrightstoneMemoryCaveDataOffset+8)
	code = append(code, 0x41, 0x52) // push r10
	code = append(code, 0x49, 0xBA) // mov r10, cave data address
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+wrightstoneMemoryCaveDataOffset))
	code = append(code, 0x49, 0x89, 0x12) // mov [r10], rdx
	code = append(code, 0x41, 0x5A)       // pop r10
	code = append(code, original...)
	jmp, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jmp...)
	for len(code) < int(wrightstoneMemoryMarkerOffset) {
		code = append(code, 0)
	}
	code = append(code, wrightstoneMemoryMarker...)
	for len(code) < int(wrightstoneMemoryCaveDataOffset)+8 {
		code = append(code, 0)
	}
	return code, nil
}

func decodeWrightstoneMemoryCave(cave uintptr, code []byte) ([]byte, error) {
	minimum := int(wrightstoneMemoryMarkerOffset) + len(wrightstoneMemoryMarker)
	if cave == 0 || len(code) < minimum {
		return nil, fmt.Errorf("祝福代码洞长度不足")
	}
	if !bytes.Equal(code[0:4], []byte{0x41, 0x52, 0x49, 0xBA}) ||
		!bytes.Equal(code[12:17], []byte{0x49, 0x89, 0x12, 0x41, 0x5A}) {
		return nil, fmt.Errorf("祝福代码洞寄存器保护签名不匹配")
	}
	dataAddr := uintptr(binary.LittleEndian.Uint64(code[4:12]))
	if dataAddr != cave+wrightstoneMemoryCaveDataOffset {
		return nil, fmt.Errorf("祝福代码洞数据地址不匹配")
	}
	if !bytes.Equal(code[wrightstoneMemoryMarkerOffset:wrightstoneMemoryMarkerOffset+uintptr(len(wrightstoneMemoryMarker))], wrightstoneMemoryMarker) {
		return nil, fmt.Errorf("祝福代码洞拥有权标记不匹配")
	}
	original := append([]byte(nil), code[wrightstoneMemoryOriginalOffset:wrightstoneMemoryOriginalOffset+wrightstoneMemoryHookSize]...)
	if !isWrightstoneMemoryOriginal(original) {
		return nil, fmt.Errorf("祝福代码洞中的原始指令不匹配: %s", bytesToHex(original))
	}
	jumpOffset := wrightstoneMemoryOriginalOffset + wrightstoneMemoryHookSize
	if len(code) < int(jumpOffset)+5 || code[jumpOffset] != 0xE9 {
		return nil, fmt.Errorf("祝福代码洞回跳签名不匹配")
	}
	return original, nil
}

func (a *App) recoverWrightstoneMemoryHookLocked(cave uintptr) ([]byte, error) {
	if cave == 0 {
		return nil, fmt.Errorf("祝福代码洞地址为空")
	}
	code := make([]byte, wrightstoneMemoryCaveDataOffset)
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		return nil, fmt.Errorf("读取祝福代码洞失败: %w", err)
	}
	original, err := decodeWrightstoneMemoryCave(cave, code)
	if err != nil {
		return nil, err
	}
	jumpOffset := wrightstoneMemoryOriginalOffset + wrightstoneMemoryHookSize
	if target := relJumpTarget(cave+jumpOffset, code[jumpOffset:jumpOffset+5]); target != a.wrightstoneMemoryHookAddr+wrightstoneMemoryHookSize {
		return nil, fmt.Errorf("祝福代码洞回跳地址不匹配")
	}
	return original, nil
}

func (a *App) clearWrightstoneMemorySelectionLocked() error {
	if a.hProcess == 0 || a.wrightstoneMemoryCaveAddr == 0 {
		return nil
	}
	var zero uintptr
	if err := writeProcessMemory(a.hProcess, a.wrightstoneMemoryCaveAddr+wrightstoneMemoryCaveDataOffset, unsafe.Pointer(&zero), unsafe.Sizeof(zero)); err != nil {
		return fmt.Errorf("清空旧的选中祝福指针失败: %w", err)
	}
	return nil
}

func (a *App) releaseWrightstoneMemoryHook() error {
	wrightstoneMemoryLifecycleMu.Lock()
	defer wrightstoneMemoryLifecycleMu.Unlock()
	return a.releaseWrightstoneMemoryHookLocked()
}

func (a *App) releaseWrightstoneMemoryHookLocked() error {
	if a.wrightstoneMemoryHookAddr == 0 {
		if a.wrightstoneMemoryCaveAddr != 0 || len(a.wrightstoneMemoryOriginal) != 0 {
			return fmt.Errorf("祝福 Hook 入口未知，但仍保留代码洞或原始指令恢复状态")
		}
		return nil
	}
	if a.hProcess == 0 {
		return fmt.Errorf("缺少游戏进程句柄，无法恢复祝福 Hook")
	}
	current := make([]byte, wrightstoneMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return err
	}
	if isWrightstoneMemoryOriginal(current) {
		a.wrightstoneMemoryHookAddr = 0
		a.wrightstoneMemoryCaveAddr = 0
		a.wrightstoneMemoryOriginal = nil
		return nil
	}
	if !isWrightstoneMemoryJump(current) {
		return fmt.Errorf("祝福 Hook 入口已被其他代码修改: %s", bytesToHex(current))
	}
	cave := relJumpTarget(a.wrightstoneMemoryHookAddr, current)
	if a.wrightstoneMemoryCaveAddr != 0 && cave != a.wrightstoneMemoryCaveAddr {
		return fmt.Errorf("祝福 Hook 跳转目标已被替换，拒绝覆盖外部 Hook")
	}
	original, err := a.recoverWrightstoneMemoryHookLocked(cave)
	if err != nil {
		return err
	}
	if len(a.wrightstoneMemoryOriginal) == int(wrightstoneMemoryHookSize) && !bytes.Equal(a.wrightstoneMemoryOriginal, original) {
		return fmt.Errorf("祝福 Hook 原始指令缓存与代码洞不一致，拒绝恢复")
	}
	_ = a.clearWrightstoneMemorySelectionLocked()
	if err := writeCodeMemory(a.hProcess, a.wrightstoneMemoryHookAddr, original); err != nil {
		return fmt.Errorf("恢复祝福焦点原始指令失败: %w", err)
	}
	restored := make([]byte, wrightstoneMemoryHookSize)
	if err := readProcessMemory(a.hProcess, a.wrightstoneMemoryHookAddr, unsafe.Pointer(&restored[0]), uintptr(len(restored))); err != nil {
		return fmt.Errorf("恢复祝福焦点原始指令后回读失败: %w", err)
	}
	if !bytes.Equal(restored, original) {
		return fmt.Errorf("恢复祝福焦点原始指令后回读不一致: %s", bytesToHex(restored))
	}
	// The game may still have a thread inside the cave. Leave the page mapped;
	// the operating system reclaims it with the process.
	a.wrightstoneMemoryHookAddr = 0
	a.wrightstoneMemoryCaveAddr = 0
	a.wrightstoneMemoryOriginal = nil
	return nil
}
