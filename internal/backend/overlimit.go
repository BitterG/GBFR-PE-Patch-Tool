package backend

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// gameSaveFunctionPrologue is the exact prologue at RVA 0x79D820 in the
// locally supplied Granblue Fantasy: Relink v2.0.2 executable. Every runtime
// item editor that enters this function must fail closed when it differs.
var gameSaveFunctionPrologue = []byte{
	0x55, 0x48, 0x83, 0xEC, 0x60, 0x48, 0x8D, 0x6C, 0x24, 0x60,
	0x48, 0xC7, 0x45, 0xF8, 0xFE, 0xFF, 0xFF, 0xFF,
}

type remoteCallIndeterminateError struct {
	reason string
}

func (e *remoteCallIndeterminateError) Error() string {
	return "远程保存线程状态不确定：" + e.reason
}

func newRemoteCallIndeterminateError(reason string) error {
	return &remoteCallIndeterminateError{reason: reason}
}

func isRemoteCallIndeterminate(err error) bool {
	var target *remoteCallIndeterminateError
	return errors.As(err, &target)
}

func classifyRemoteCallWait(wait uint32, waitErr error) error {
	return classifyRemoteCallWaitWithTimeout(wait, waitErr, "等待 5 秒后仍未结束")
}

func classifyRemoteCallWaitWithTimeout(wait uint32, waitErr error, timeoutReason string) error {
	if waitErr != nil {
		return newRemoteCallIndeterminateError(fmt.Sprintf("等待失败（%v）", waitErr))
	}
	switch wait {
	case uint32(windows.WAIT_OBJECT_0):
		return nil
	case uint32(windows.WAIT_TIMEOUT):
		return newRemoteCallIndeterminateError(timeoutReason)
	default:
		return newRemoteCallIndeterminateError(fmt.Sprintf("未知等待结果 0x%08X", wait))
	}
}

type OverLimitOption struct {
	ID       uint32  `json:"id"`
	Hex      string  `json:"hex"`
	Name     string  `json:"name"`
	EffectID uint32  `json:"effectId"`
	MaxValue float32 `json:"maxValue"`
	Scale    float32 `json:"scale"`
}

type OverLimitSlot struct {
	Index     int     `json:"index"`
	Attribute uint32  `json:"attribute"`
	Level     uint32  `json:"level"`
	Value     float32 `json:"value"`
}

type OverLimitStatus struct {
	OwnerToken   string          `json:"ownerToken,omitempty"`
	Found        bool            `json:"found"`
	Hooked       bool            `json:"hooked"`
	Address      uint64          `json:"address"`
	RVA          uint64          `json:"rva"`
	SelectedAddr uint64          `json:"selectedAddr"`
	CurrentBytes string          `json:"currentBytes"`
	Slots        []OverLimitSlot `json:"slots"`
}

type OverLimitUpdate struct {
	Index                int     `json:"index"`
	ExpectedSelectedAddr uint64  `json:"expectedSelectedAddr"`
	Attribute            uint32  `json:"attribute"`
	Level                uint32  `json:"level"`
	Value                float32 `json:"value"`
}

// overLimitCatalogEntry is the single source of truth for both live-memory
// editing and save-file stat calculation. The runtime fields describe the
// result-screen representation; unit/values describe the audited save curve.
type overLimitCatalogEntry struct {
	name      string
	effectID  uint32
	maxValue  float32
	scale     float32
	unit      string
	values    [10]float64
	canonical uint32
}

var overLimitCatalogOrder = []uint32{
	0x52A207B5, 0x54929589, 0x6CB38EF3, 0xC4925BD7, 0x45C65767, 0x43B7581D,
	0x9A97C049, 0x9C555433, 0x4E42646B, 0x4A4C093D, 0x68B39018,
	0xCB63BE55, 0xDCBD8423, 0x59DCE1E8, 0xF203BB15,
	0x57BBC478, 0x5A51F0CB, 0x9C6375CF, 0xF004E9F2,
	0xC4B86ED7, 0xCEB0DBD2, 0xA3545CA1, 0x59FBB7D8,
}

var overLimitCatalog = func() map[uint32]overLimitCatalogEntry {
	pctCurve := [10]float64{1, 1, 2, 4, 6, 8, 10, 12, 16, 20}
	entries := map[uint32]overLimitCatalogEntry{
		0xC4925BD7: {name: "攻击力", effectID: 0, maxValue: 1000, scale: 1, unit: "flat", values: [10]float64{100, 100, 200, 300, 400, 500, 600, 700, 800, 1000}, canonical: 0xC4925BD7},
		0x52A207B5: {name: "最大HP", effectID: 1, maxValue: 2000, scale: 1, unit: "flat", values: [10]float64{100, 200, 400, 500, 600, 800, 1000, 1200, 1600, 2000}, canonical: 0x52A207B5},
		0x45C65767: {name: "暴击率", effectID: 2, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x45C65767},
		0x6CB38EF3: {name: "昏厥值", effectID: 3, maxValue: 20, scale: 10, unit: "flat", values: [10]float64{0.1, 0.1, 0.2, 0.4, 0.6, 0.8, 1, 1.2, 1.6, 2}, canonical: 0x6CB38EF3},
		0x9A97C049: {name: "能力伤害", effectID: 100, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x9A97C049},
		0x4E42646B: {name: "奥义伤害", effectID: 101, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x4E42646B},
		0x68B39018: {name: "奥义连锁伤害", effectID: 102, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x68B39018},
		0x43B7581D: {name: "普通攻击伤害上限", effectID: 103, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x43B7581D},
		0x9C555433: {name: "能力伤害上限", effectID: 104, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x9C555433},
		0x4A4C093D: {name: "奥义伤害上限", effectID: 105, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x4A4C093D},
		0x54929589: {name: "HP回复上限", effectID: 107, maxValue: 20, scale: 1, unit: "pct", values: pctCurve, canonical: 0x54929589},
	}
	aliases := map[uint32]uint32{
		0xCB63BE55: 0xC4925BD7, 0xDCBD8423: 0xC4925BD7, 0x59DCE1E8: 0xC4925BD7, 0xF203BB15: 0xC4925BD7,
		0x57BBC478: 0x52A207B5, 0x5A51F0CB: 0x52A207B5, 0x9C6375CF: 0x52A207B5, 0xF004E9F2: 0x52A207B5,
		0xC4B86ED7: 0x45C65767, 0xCEB0DBD2: 0x45C65767,
		0xA3545CA1: 0x6CB38EF3, 0x59FBB7D8: 0x6CB38EF3,
	}
	for alias, canonical := range aliases {
		entry := entries[canonical]
		entry.canonical = canonical
		entries[alias] = entry
	}
	return entries
}()

var overLimitAttributeOptions = func() []OverLimitOption {
	options := make([]OverLimitOption, 0, len(overLimitCatalogOrder))
	for _, hash := range overLimitCatalogOrder {
		entry := overLimitCatalog[hash]
		options = append(options, OverLimitOption{
			ID: hash, Hex: fmt.Sprintf("%08X", hash), Name: entry.name,
			EffectID: entry.effectID, MaxValue: entry.maxValue, Scale: entry.scale,
		})
	}
	return options
}()

var overLimitLevelOptions = []OverLimitOption{
	{0x00000001, "00000001", "LV1", 0, 0, 0},
	{0x00000002, "00000002", "LV2", 0, 0, 0},
	{0x00000004, "00000004", "LV3", 0, 0, 0},
	{0x00000008, "00000008", "LV4", 0, 0, 0},
	{0x00000010, "00000010", "LV5", 0, 0, 0},
	{0x00000020, "00000020", "LV6", 0, 0, 0},
	{0x00000040, "00000040", "LV7", 0, 0, 0},
	{0x00000080, "00000080", "LV8", 0, 0, 0},
	{0x00000100, "00000100", "LV9", 0, 0, 0},
	{0x00000200, "00000200", "LV10", 0, 0, 0},
}

var (
	overLimitSelectedPattern = []byte{
		0x8B, 0x03, 0x89, 0x02, 0x8B, 0x43, 0x04, 0x89, 0x42, 0x04,
		0x48, 0x8B, 0x43, 0x08, 0x48, 0x89, 0x42, 0x08,
		0x49, 0x83, 0x86, 0xD8, 0x0A, 0x00, 0x00, 0x10,
		0xEB, 0, 0x48, 0x85, 0xF6,
	}
	overLimitSelectedMask = []bool{
		true, true, true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true,
		true, false, true, true, true,
	}
	overLimitSelectedOrig = []byte{0x8B, 0x03, 0x89, 0x02, 0x8B, 0x43, 0x04}
)

const (
	overLimitCaveMarkerOffset = uintptr(0x30)
	overLimitCaveDataOffset   = uintptr(0x40)
	overLimitSlotStride       = uintptr(0x10)
	overLimitSlotCount        = 4
)

// overLimitCaveMarker distinguishes the register-preserving capture cave from
// both arbitrary E9 hooks and the exact clobbering layout emitted previously.
// Old M2 and markerless caves are restore-only and are never adopted.
const (
	overLimitCaveMarker       = "GBFROLM3"
	overLimitLegacyCaveMarker = "GBFROLM2"
)

var overLimitLifecycleMu sync.Mutex

func overLimitSelectedHookedMask() []bool {
	mask := append([]bool{}, overLimitSelectedMask...)
	for i := 0; i < len(overLimitSelectedOrig) && i < len(mask); i++ {
		mask[i] = false
	}
	return mask
}

func isOverLimitSelectedJump(buf []byte) bool {
	return len(buf) == len(overLimitSelectedOrig) && buf[0] == 0xE9 && buf[5] == 0x90 && buf[6] == 0x90
}

func validateOverLimitSelectedCave(cave, hook uintptr, code []byte) error {
	if cave == 0 || hook == 0 || len(code) != int(overLimitCaveDataOffset) {
		return fmt.Errorf("OverLimit cave ownership length is invalid")
	}
	expected, err := buildOverLimitSelectedCave(cave, hook+uintptr(len(overLimitSelectedOrig)))
	if err != nil {
		return fmt.Errorf("rebuild OverLimit cave ownership signature: %w", err)
	}
	if !bytes.Equal(code, expected[:overLimitCaveDataOffset]) {
		return fmt.Errorf("OverLimit cave ownership signature does not match")
	}
	return nil
}

func validateLegacyOverLimitSelectedCave(cave, hook uintptr, code []byte) error {
	if cave == 0 || hook == 0 || len(code) != int(overLimitCaveDataOffset) {
		return fmt.Errorf("legacy OverLimit cave length is invalid")
	}
	expected, err := buildLegacyOverLimitSelectedCave(cave, hook+uintptr(len(overLimitSelectedOrig)))
	if err != nil {
		return fmt.Errorf("rebuild legacy OverLimit cave signature: %w", err)
	}
	if bytes.Equal(code, expected[:overLimitCaveDataOffset]) {
		return nil
	}
	for index := range overLimitLegacyCaveMarker {
		expected[int(overLimitCaveMarkerOffset)+index] = 0
	}
	if bytes.Equal(code, expected[:overLimitCaveDataOffset]) {
		return nil
	}
	return fmt.Errorf("legacy OverLimit cave signature does not match")
}

// inspectOverLimitJumpLocked validates the complete cave before its mutable
// selected-address field is ever read. The bool result is true only for the
// exact register-clobbering M2/markerless layout emitted by older versions.
func (a *App) inspectOverLimitJumpLocked(entry []byte) (uintptr, bool, error) {
	if !isOverLimitSelectedJump(entry) {
		return 0, false, fmt.Errorf("OverLimit hook entry is not a supported jump")
	}
	cave := relJumpTarget(a.overLimitHookAddr, entry)
	if a.overLimitCaveAddr != 0 && cave != a.overLimitCaveAddr {
		return 0, false, fmt.Errorf("OverLimit hook jump target changed from 0x%X to 0x%X", a.overLimitCaveAddr, cave)
	}
	code := make([]byte, overLimitCaveDataOffset)
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		return 0, false, fmt.Errorf("read OverLimit cave for ownership validation: %w", err)
	}
	if err := validateOverLimitSelectedCave(cave, a.overLimitHookAddr, code); err == nil {
		return cave, false, nil
	} else if legacyErr := validateLegacyOverLimitSelectedCave(cave, a.overLimitHookAddr, code); legacyErr == nil {
		return cave, true, nil
	} else {
		return 0, false, fmt.Errorf("OverLimit cave ownership validation failed: current %v; legacy %v", err, legacyErr)
	}
}

func (a *App) restoreOverLimitEntryLocked() error {
	if err := writeCodeMemory(a.hProcess, a.overLimitHookAddr, overLimitSelectedOrig); err != nil {
		return fmt.Errorf("restore OverLimit hook entry: %w", err)
	}
	restored := make([]byte, len(overLimitSelectedOrig))
	if err := readProcessMemory(a.hProcess, a.overLimitHookAddr, unsafe.Pointer(&restored[0]), uintptr(len(restored))); err != nil {
		return fmt.Errorf("read back restored OverLimit hook entry: %w", err)
	}
	if !bytes.Equal(restored, overLimitSelectedOrig) {
		return fmt.Errorf("OverLimit hook restore readback mismatch: %s", bytesToHex(restored))
	}
	return nil
}

// releaseOverLimitHook restores the selected-character capture entry while the
// caller still owns a live process handle. The lifecycle lock is always taken
// after procMu throughout this file, including from charaDetachLocked.
func (a *App) releaseOverLimitHook() error {
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	return a.releaseOverLimitHookLocked()
}

func (a *App) releaseOverLimitHookLocked() error {
	if a.overLimitHookAddr == 0 {
		if a.overLimitCaveAddr != 0 {
			return fmt.Errorf("OverLimit hook entry is unknown while cave 0x%X is still cached", a.overLimitCaveAddr)
		}
		return nil
	}
	if a.hProcess == 0 {
		return fmt.Errorf("OverLimit hook cannot be restored without a process handle")
	}

	current := make([]byte, len(overLimitSelectedOrig))
	if err := readProcessMemory(a.hProcess, a.overLimitHookAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return fmt.Errorf("read OverLimit hook entry before restore: %w", err)
	}
	if bytes.Equal(current, overLimitSelectedOrig) {
		a.overLimitHookAddr = 0
		a.overLimitCaveAddr = 0
		return nil
	}
	if !isOverLimitSelectedJump(current) {
		return fmt.Errorf("OverLimit hook entry is neither the original nor an owned jump: %s", bytesToHex(current))
	}

	// M2/markerless legacy caves can be recognised exactly enough to restore the
	// original entry, but they are never adopted or used to read selectedAddr.
	if _, _, err := a.inspectOverLimitJumpLocked(current); err != nil {
		return err
	}
	if err := a.restoreOverLimitEntryLocked(); err != nil {
		return err
	}

	// Do not free the cave: a game thread may already be executing inside it.
	// Clear recovery state only after the original entry has been read back.
	a.overLimitHookAddr = 0
	a.overLimitCaveAddr = 0
	return nil
}

func (a *App) OverLimitGetOptions() map[string][]OverLimitOption {
	return map[string][]OverLimitOption{
		"attributes": overLimitAttributeOptions,
		"levels":     overLimitLevelOptions,
	}
}

func (a *App) OverLimitScan() (OverLimitStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	return a.scanOverLimitLocked()
}

func (a *App) scanOverLimitLocked() (OverLimitStatus, error) {
	addr, err := a.scanPatternUnique(overLimitSelectedPattern, overLimitSelectedMask, "上限突破角色指针特征")
	if err != nil {
		addr, err = a.scanPatternUnique(overLimitSelectedPattern, overLimitSelectedHookedMask(), "上限突破角色指针特征")
		if err != nil {
			return OverLimitStatus{}, err
		}
	}
	a.overLimitHookAddr = addr
	return a.readOverLimitStatusLocked()
}

func (a *App) OverLimitGetStatus() (OverLimitStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	return a.getOverLimitStatusLocked()
}

func (a *App) getOverLimitStatusLocked() (OverLimitStatus, error) {
	if a.overLimitHookAddr == 0 {
		return a.scanOverLimitLocked()
	}
	status, err := a.readOverLimitStatusLocked()
	if err != nil {
		// Once a cave has been installed/adopted, the entry address is recovery
		// state. Never discard it merely because a status read failed.
		if a.overLimitCaveAddr != 0 {
			return OverLimitStatus{}, err
		}
		a.overLimitHookAddr = 0
		return a.scanOverLimitLocked()
	}
	return status, nil
}

func (a *App) OverLimitEnable() (OverLimitStatus, error) {
	if err := a.acquireLegacyRuntimeMutationLease(runtimeOwnerOverLimit); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	status, err := a.overLimitEnableLocked()
	if err == nil {
		// Compatibility callers deliberately take an unowned hook lease.
		a.overLimitOwnerToken = ""
	}
	return status, err
}

func (a *App) OverLimitAcquire(requestID uint64) (OverLimitStatus, error) {
	if err := a.acquireOwnedGameProcessLease(requestID); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	status, err := a.overLimitEnableLocked()
	if err != nil {
		return OverLimitStatus{}, err
	}
	return a.grantOverLimitOwner(status), nil
}

func (a *App) grantOverLimitOwner(status OverLimitStatus) OverLimitStatus {
	token := a.nextRuntimeOwnerToken("overlimit")
	a.charaOwnerToken = ""
	a.overLimitOwnerToken = token
	status.OwnerToken = token
	return status
}

func (a *App) overLimitEnableLocked() (OverLimitStatus, error) {
	status, err := a.getOverLimitStatusLocked()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if status.Hooked {
		return status, nil
	}
	if a.overLimitHookAddr == 0 {
		return OverLimitStatus{}, fmt.Errorf("未定位上限突破角色指针特征")
	}

	cave, err := virtualAllocRemoteNear(a.hProcess, a.overLimitHookAddr, 0x1000)
	if err != nil {
		return OverLimitStatus{}, fmt.Errorf("分配上限突破代码洞失败: %w", err)
	}
	code, err := buildOverLimitSelectedCave(cave, a.overLimitHookAddr+uintptr(len(overLimitSelectedOrig)))
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return OverLimitStatus{}, err
	}
	if err := writeCodeMemory(a.hProcess, cave, code); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return OverLimitStatus{}, fmt.Errorf("写入上限突破代码洞失败: %w", err)
	}
	patch, err := makeRelJump(a.overLimitHookAddr, cave, len(overLimitSelectedOrig))
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return OverLimitStatus{}, err
	}
	installResult, err := installRemoteCodeHook(a.hProcess, a.overLimitHookAddr, overLimitSelectedOrig, patch)
	if err != nil {
		return OverLimitStatus{}, runtimeHookInstallFailure(
			"上限突破读取 Hook", installResult, err,
			func() { _ = virtualFreeRemote(a.hProcess, cave) },
			func() { a.retireRuntimeCaveLocked(cave, "over-limit install rollback") },
			func() { a.overLimitCaveAddr = cave },
			a.poisonCurrentLiveMemoryWrites,
		)
	}
	a.overLimitCaveAddr = cave
	return finalizeRuntimeHookEnable(
		"上限突破读取 Hook",
		a.readOverLimitStatusLocked,
		a.releaseOverLimitHookLocked,
		a.poisonCurrentLiveMemoryWrites,
	)
}

// OverLimitRelease explicitly removes this tool's selected-character capture
// hook while holding the same stable process and lifecycle leases as enable.
// CharaDetach calls the same restore path as a final fallback.
func (a *App) OverLimitRelease(token string) (OverLimitStatus, error) {
	a.procMu.Lock()
	if !runtimeOwnerTokenMatches(a.overLimitOwnerToken, token) {
		a.procMu.Unlock()
		return OverLimitStatus{}, nil
	}
	idle := a.overLimitHookAddr == 0 && a.overLimitCaveAddr == 0
	if idle {
		a.overLimitOwnerToken = ""
		a.procMu.Unlock()
		return OverLimitStatus{}, nil
	}
	a.procMu.Unlock()

	if err := a.acquireGameProcessLease(); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	if !runtimeOwnerTokenMatches(a.overLimitOwnerToken, token) {
		return OverLimitStatus{}, nil
	}
	if err := a.releaseOverLimitHookLocked(); err != nil {
		return OverLimitStatus{}, fmt.Errorf("关闭上限突破读取失败: %w", err)
	}
	a.overLimitOwnerToken = ""
	return OverLimitStatus{}, nil
}

func (a *App) OverLimitDisable() (OverLimitStatus, error) {
	// Avoid opening a game-process connection just because a never-enabled page
	// is being unmounted. Any cached hook state still takes the full lease below.
	a.procMu.Lock()
	if a.overLimitOwnerToken != "" {
		a.procMu.Unlock()
		return OverLimitStatus{}, errRuntimeOwnerLeaseStale
	}
	idle := a.overLimitHookAddr == 0 && a.overLimitCaveAddr == 0
	a.procMu.Unlock()
	if idle {
		return OverLimitStatus{}, nil
	}
	if err := a.acquireLegacyRuntimeMutationLease(runtimeOwnerOverLimit); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	if err := a.releaseOverLimitHookLocked(); err != nil {
		return OverLimitStatus{}, fmt.Errorf("关闭上限突破读取失败: %w", err)
	}
	a.overLimitOwnerToken = ""
	return OverLimitStatus{}, nil
}

func (a *App) OverLimitSetSlot(update OverLimitUpdate) (OverLimitStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if update.Index < 0 || update.Index >= overLimitSlotCount {
		return OverLimitStatus{}, fmt.Errorf("无效的上限突破槽位: %d", update.Index+1)
	}
	expected, err := expectedOverLimitSelection([]OverLimitUpdate{update})
	if err != nil {
		return OverLimitStatus{}, err
	}
	if err := a.acquireLegacyRuntimeMutationLease(runtimeOwnerOverLimit); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return OverLimitStatus{}, err
	}
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()

	status, err := a.getOverLimitStatusLocked()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if err := validateOverLimitBatchTarget(status); err != nil {
		return OverLimitStatus{}, err
	}
	if _, err := validateOverLimitSelection(expected, uintptr(status.SelectedAddr)); err != nil {
		return OverLimitStatus{}, err
	}
	updates := make([]OverLimitUpdate, overLimitSlotCount)
	for _, slot := range status.Slots {
		updates[slot.Index] = OverLimitUpdate{
			Index: slot.Index, ExpectedSelectedAddr: uint64(expected), Attribute: slot.Attribute, Level: slot.Level, Value: slot.Value,
		}
	}
	updates[update.Index] = update
	encoded, err := prepareOverLimitBatch(updates)
	if err != nil {
		return OverLimitStatus{}, err
	}
	return a.writeOverLimitBatchLocked(expected, status, encoded)
}

// OverLimitSetAll validates and writes the complete result-screen state as one
// transaction. Keeping this boundary at four slots prevents a half-updated
// character when a later WriteProcessMemory call fails.
func (a *App) OverLimitSetAll(updates []OverLimitUpdate) (OverLimitStatus, error) {
	return a.overLimitSetAll("", false, updates)
}

func (a *App) OverLimitSetAllOwned(token string, updates []OverLimitUpdate) (OverLimitStatus, error) {
	return a.overLimitSetAll(token, true, updates)
}

func (a *App) overLimitSetAll(token string, owned bool, updates []OverLimitUpdate) (OverLimitStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	encoded, err := prepareOverLimitBatch(updates)
	if err != nil {
		return OverLimitStatus{}, err
	}
	expected, err := expectedOverLimitSelection(updates)
	if err != nil {
		return OverLimitStatus{}, err
	}
	var leaseErr error
	if owned {
		leaseErr = a.acquireOwnedRuntimeWriteLease(runtimeOwnerOverLimit, token)
	} else {
		leaseErr = a.acquireLegacyRuntimeMutationLease(runtimeOwnerOverLimit)
	}
	if leaseErr != nil {
		return OverLimitStatus{}, leaseErr
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return OverLimitStatus{}, err
	}
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()

	status, err := a.getOverLimitStatusLocked()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if err := validateOverLimitBatchTarget(status); err != nil {
		return OverLimitStatus{}, err
	}
	if _, err := validateOverLimitSelection(expected, uintptr(status.SelectedAddr)); err != nil {
		return OverLimitStatus{}, err
	}
	return a.writeOverLimitBatchLocked(expected, status, encoded)
}

type overLimitEncodedBatch [overLimitSlotCount][0x10]byte
type overLimitBatchWriter func(addr uintptr, data []byte) error
type overLimitBatchReader func(addr uintptr, size int) ([]byte, error)

func prepareOverLimitBatch(updates []OverLimitUpdate) (overLimitEncodedBatch, error) {
	var encoded overLimitEncodedBatch
	if len(updates) != overLimitSlotCount {
		return encoded, fmt.Errorf("上限突破批量写入必须恰好包含 %d 个槽位，实际 %d 个", overLimitSlotCount, len(updates))
	}
	seen := [overLimitSlotCount]bool{}
	for _, update := range updates {
		if update.Index < 0 || update.Index >= overLimitSlotCount {
			return encoded, fmt.Errorf("无效的上限突破槽位: %d", update.Index+1)
		}
		if seen[update.Index] {
			return encoded, fmt.Errorf("上限突破槽位 %d 重复", update.Index+1)
		}
		seen[update.Index] = true
		entry, ok := overLimitCatalog[update.Attribute]
		if !ok {
			return encoded, fmt.Errorf("上限突破槽位 %d 的属性 0x%08X 不在已审计目录，已拒绝写入", update.Index+1, update.Attribute)
		}
		if !validOverLimitLevel(update.Level) {
			return encoded, fmt.Errorf("上限突破槽位 %d 的等级 0x%08X 无效", update.Index+1, update.Level)
		}
		value := float64(update.Value)
		if math.IsNaN(value) || math.IsInf(value, 0) || update.Value <= 0 || update.Value > entry.maxValue {
			return encoded, fmt.Errorf("上限突破槽位 %d 的%s数值必须在 (0, %.4g] 范围内", update.Index+1, entry.name, entry.maxValue)
		}
		if entry.scale <= 0 {
			return encoded, fmt.Errorf("上限突破属性 0x%08X 的缩放配置无效", update.Attribute)
		}
		buf := encoded[update.Index][:]
		binary.LittleEndian.PutUint32(buf[0:4], update.Attribute)
		binary.LittleEndian.PutUint32(buf[4:8], update.Level)
		binary.LittleEndian.PutUint32(buf[8:12], entry.effectID)
		binary.LittleEndian.PutUint32(buf[12:16], math.Float32bits(update.Value/entry.scale))
	}
	return encoded, nil
}

func validateOverLimitBatchTarget(status OverLimitStatus) error {
	if !status.Found || !status.Hooked || status.SelectedAddr == 0 {
		return fmt.Errorf("请先开启上限突破读取，并停留在显示四项结果的突破界面")
	}
	if len(status.Slots) != overLimitSlotCount {
		return fmt.Errorf("当前突破结果不完整：读取到 %d/%d 个槽位，已拒绝写入", len(status.Slots), overLimitSlotCount)
	}
	for index, slot := range status.Slots {
		if slot.Index != index {
			return fmt.Errorf("当前突破结果槽位顺序异常，已拒绝写入")
		}
	}
	return nil
}

func expectedOverLimitSelection(updates []OverLimitUpdate) (uintptr, error) {
	if len(updates) == 0 || updates[0].ExpectedSelectedAddr == 0 {
		return 0, fmt.Errorf("缺少写入前捕获的上限突破结果地址")
	}
	expected := updates[0].ExpectedSelectedAddr
	for _, update := range updates[1:] {
		if update.ExpectedSelectedAddr == 0 || update.ExpectedSelectedAddr != expected {
			return 0, fmt.Errorf("上限突破批量写入包含不一致的目标地址")
		}
	}
	return uintptr(expected), nil
}

func validateOverLimitSelection(expected, selected uintptr) (uintptr, error) {
	if expected == 0 {
		return 0, fmt.Errorf("缺少写入前捕获的上限突破结果地址")
	}
	if selected == 0 || selected != expected {
		return 0, fmt.Errorf("上限突破结果已从 0x%X 切换到 0x%X，请刷新后重新确认", expected, selected)
	}
	return expected, nil
}

func (a *App) writeOverLimitBatchLocked(expected uintptr, status OverLimitStatus, encoded overLimitEncodedBatch) (OverLimitStatus, error) {
	base, err := validateOverLimitSelection(expected, uintptr(status.SelectedAddr))
	if err != nil {
		return OverLimitStatus{}, err
	}
	snapshot := make([]byte, overLimitSlotCount*int(overLimitSlotStride))
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&snapshot[0]), uintptr(len(snapshot))); err != nil {
		return OverLimitStatus{}, fmt.Errorf("读取上限突破四槽旧快照失败: %w", err)
	}

	// Re-read the hook-owned pointer after taking the snapshot. If the player
	// changed character or left the result screen, no byte is written.
	confirmed, err := a.readOverLimitStatusLocked()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if err := validateOverLimitBatchTarget(confirmed); err != nil {
		return OverLimitStatus{}, err
	}
	if _, err := validateOverLimitSelection(expected, uintptr(confirmed.SelectedAddr)); err != nil {
		return OverLimitStatus{}, err
	}
	if err := snapshotBeforeLiveSaveChange("角色上限突破四槽原子写入"); err != nil {
		return OverLimitStatus{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}
	finalTarget, err := a.readOverLimitStatusLocked()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if err := validateOverLimitBatchTarget(finalTarget); err != nil {
		return OverLimitStatus{}, err
	}
	if _, err := validateOverLimitSelection(expected, uintptr(finalTarget.SelectedAddr)); err != nil {
		return OverLimitStatus{}, fmt.Errorf("自动备份期间%w", err)
	}
	confirmedSnapshot := make([]byte, len(snapshot))
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&confirmedSnapshot[0]), uintptr(len(confirmedSnapshot))); err != nil {
		return OverLimitStatus{}, fmt.Errorf("自动备份后重新读取上限突破四槽失败: %w", err)
	}
	if !bytes.Equal(confirmedSnapshot, snapshot) {
		return OverLimitStatus{}, fmt.Errorf("自动备份期间上限突破四槽内容已经变化，请刷新后重新确认")
	}
	writer := func(addr uintptr, data []byte) error {
		return writeProcessMemory(a.hProcess, addr, unsafe.Pointer(&data[0]), uintptr(len(data)))
	}
	reader := func(addr uintptr, size int) ([]byte, error) {
		if size <= 0 {
			return nil, fmt.Errorf("上限突破回读长度无效: %d", size)
		}
		data := make([]byte, size)
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&data[0]), uintptr(len(data))); err != nil {
			return nil, err
		}
		return data, nil
	}
	if err := writeOverLimitBatchAtomic(base, snapshot, encoded, writer, reader); err != nil {
		if errors.Is(err, errLiveMemoryRollbackUnproven) {
			a.poisonCurrentLiveMemoryWrites()
		}
		return OverLimitStatus{}, err
	}
	return a.readOverLimitStatusLocked()
}

func writeOverLimitBatchAtomic(base uintptr, snapshot []byte, encoded overLimitEncodedBatch, writer overLimitBatchWriter, reader overLimitBatchReader) error {
	wantSnapshotSize := overLimitSlotCount * int(overLimitSlotStride)
	if base == 0 || len(snapshot) != wantSnapshotSize || writer == nil || reader == nil {
		return fmt.Errorf("上限突破原子写入参数无效")
	}
	rollback := func(lastIndex int, cause error) error {
		var rollbackErr error
		for rollbackIndex := lastIndex; rollbackIndex >= 0; rollbackIndex-- {
			offset := rollbackIndex * int(overLimitSlotStride)
			oldSlot := snapshot[offset : offset+int(overLimitSlotStride)]
			rollbackAddr := base + uintptr(rollbackIndex)*overLimitSlotStride
			if err := writer(rollbackAddr, oldSlot); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("回滚槽位 %d 失败: %w", rollbackIndex+1, err))
				continue
			}
			restored, err := reader(rollbackAddr, len(oldSlot))
			if err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("回读回滚槽位 %d 失败: %w", rollbackIndex+1, err))
				continue
			}
			if !bytes.Equal(restored, oldSlot) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("回滚槽位 %d 后回读不一致", rollbackIndex+1))
			}
		}
		if rollbackErr != nil {
			return errors.Join(cause, errLiveMemoryRollbackUnproven, rollbackErr)
		}
		return cause
	}
	for index := 0; index < overLimitSlotCount; index++ {
		addr := base + uintptr(index)*overLimitSlotStride
		if err := writer(addr, encoded[index][:]); err != nil {
			writeErr := fmt.Errorf("写入上限突破槽位 %d 失败: %w", index+1, err)
			return rollback(index, writeErr)
		}
		actual, err := reader(addr, len(encoded[index]))
		if err != nil {
			return rollback(index, fmt.Errorf("写入上限突破槽位 %d 后回读失败: %w", index+1, err))
		}
		if !bytes.Equal(actual, encoded[index][:]) {
			return rollback(index, fmt.Errorf("上限突破槽位 %d 写后回读不一致", index+1))
		}
	}
	return nil
}

func (a *App) OverLimitCommit() (OverLimitStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return OverLimitStatus{}, err
	}
	defer a.procMu.Unlock()
	overLimitLifecycleMu.Lock()
	defer overLimitLifecycleMu.Unlock()
	status, err := a.getOverLimitStatusLocked()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if status.SelectedAddr == 0 {
		return OverLimitStatus{}, fmt.Errorf("请先在游戏突破界面加载角色")
	}
	return status, nil
}

func (a *App) readOverLimitStatusLocked() (OverLimitStatus, error) {
	if a.overLimitHookAddr == 0 {
		return OverLimitStatus{}, fmt.Errorf("未定位上限突破角色指针特征")
	}
	buf := make([]byte, len(overLimitSelectedOrig))
	if err := readProcessMemory(a.hProcess, a.overLimitHookAddr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return OverLimitStatus{}, fmt.Errorf("读取上限突破 hook 指令失败: %w", err)
	}
	orig := bytesEqual(buf, overLimitSelectedOrig)
	hooked := isOverLimitSelectedJump(buf)
	if !orig && !hooked {
		return OverLimitStatus{}, fmt.Errorf("上限突破指令字节异常: %s", bytesToHex(buf))
	}

	selected := uintptr(0)
	if hooked {
		cave, legacy, err := a.inspectOverLimitJumpLocked(buf)
		if err != nil {
			// A freshly discovered E9 without a verified owner marker is external
			// state, not recovery state. Forget its entry so detach never tries to
			// restore or retain the process lease for a hook we do not own.
			if a.overLimitCaveAddr == 0 {
				a.overLimitHookAddr = 0
			}
			return OverLimitStatus{}, err
		}
		if legacy {
			// The old markerless layout cannot prove unique ownership. Restore it
			// for compatibility, but never adopt it or touch its selected pointer.
			if err := a.restoreOverLimitEntryLocked(); err != nil {
				return OverLimitStatus{}, fmt.Errorf("restore legacy OverLimit hook: %w", err)
			}
			a.overLimitCaveAddr = 0
			hooked = false
			buf = append(buf[:0], overLimitSelectedOrig...)
		} else {
			// Cache only after instructions, data address, return jump, padding
			// and the owner marker have all been verified.
			a.overLimitCaveAddr = cave
		}
	}
	if hooked {
		if err := readProcessMemory(a.hProcess, a.overLimitCaveAddr+overLimitCaveDataOffset, unsafe.Pointer(&selected), unsafe.Sizeof(selected)); err != nil {
			return OverLimitStatus{}, fmt.Errorf("读取上限突破角色指针失败: %w", err)
		}
	}

	status := OverLimitStatus{
		Found:        true,
		Hooked:       hooked,
		Address:      uint64(a.overLimitHookAddr),
		RVA:          uint64(a.overLimitHookAddr - a.moduleBase),
		SelectedAddr: uint64(selected),
		CurrentBytes: bytesToHex(buf),
	}
	if selected != 0 {
		slots, err := readOverLimitSlots(a.hProcess, selected)
		if err != nil {
			return OverLimitStatus{}, err
		}
		status.Slots = slots
	}
	return status, nil
}

func buildOverLimitSelectedCave(cave uintptr, returnAddr uintptr) ([]byte, error) {
	code := make([]byte, 0, 0x80)
	code = append(code, 0x9C)       // pushfq
	code = append(code, 0x41, 0x52) // push r10
	code = append(code, 0x41, 0x53) // push r11
	code = append(code, 0x49, 0xBA) // mov r10,data
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+overLimitCaveDataOffset))
	code = append(code, 0x4C, 0x8B, 0xDA) // mov r11,rdx
	code = append(code, 0x49, 0x29, 0xDB) // sub r11,rbx
	code = append(code, 0x49, 0x01, 0xF3) // add r11,rsi
	code = append(code, 0x4D, 0x89, 0x1A) // mov [r10],r11
	code = append(code, 0x41, 0x5B)       // pop r11
	code = append(code, 0x41, 0x5A)       // pop r10
	code = append(code, 0x9D)             // popfq
	code = append(code, overLimitSelectedOrig...)
	jmp, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jmp...)
	for len(code) < int(overLimitCaveMarkerOffset) {
		code = append(code, 0)
	}
	code = append(code, overLimitCaveMarker...)
	for len(code) < int(overLimitCaveDataOffset)+8 {
		code = append(code, 0)
	}
	return code, nil
}

func buildLegacyOverLimitSelectedCave(cave uintptr, returnAddr uintptr) ([]byte, error) {
	code := make([]byte, 0, 0x80)
	code = append(code, 0x49, 0xBA) // mov r10,data (legacy clobbering layout)
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+overLimitCaveDataOffset))
	code = append(code, 0x4C, 0x8B, 0xDA) // mov r11,rdx
	code = append(code, 0x49, 0x29, 0xDB) // sub r11,rbx
	code = append(code, 0x49, 0x01, 0xF3) // add r11,rsi
	code = append(code, 0x4D, 0x89, 0x1A) // mov [r10],r11
	code = append(code, overLimitSelectedOrig...)
	jmp, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jmp...)
	for len(code) < int(overLimitCaveMarkerOffset) {
		code = append(code, 0)
	}
	code = append(code, overLimitLegacyCaveMarker...)
	for len(code) < int(overLimitCaveDataOffset)+8 {
		code = append(code, 0)
	}
	return code, nil
}

func makeRelJump(src uintptr, dst uintptr, size int) ([]byte, error) {
	if size < 5 {
		return nil, fmt.Errorf("跳转覆盖长度不足")
	}
	delta := int64(dst) - int64(src) - 5
	if delta < math.MinInt32 || delta > math.MaxInt32 {
		return nil, fmt.Errorf("跳转距离超过 rel32 范围")
	}
	patch := make([]byte, size)
	patch[0] = 0xE9
	binary.LittleEndian.PutUint32(patch[1:5], uint32(int32(delta)))
	for i := 5; i < len(patch); i++ {
		patch[i] = 0x90
	}
	return patch, nil
}

func relJumpTarget(src uintptr, buf []byte) uintptr {
	return uintptr(int64(src+5) + int64(int32(binary.LittleEndian.Uint32(buf[1:5]))))
}

func readOverLimitSlots(h windows.Handle, base uintptr) ([]OverLimitSlot, error) {
	slots := make([]OverLimitSlot, 0, overLimitSlotCount)
	for i := 0; i < overLimitSlotCount; i++ {
		addr := base + uintptr(i)*overLimitSlotStride
		buf := make([]byte, 0x10)
		if err := readProcessMemory(h, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
			return nil, fmt.Errorf("读取上限突破槽位 %d 失败: %w", i+1, err)
		}
		value := math.Float32frombits(binary.LittleEndian.Uint32(buf[0xC:0x10]))
		if _, scale, ok := overLimitValueSpec(binary.LittleEndian.Uint32(buf[0:4])); ok {
			value *= scale
		}
		slots = append(slots, OverLimitSlot{
			Index:     i,
			Attribute: binary.LittleEndian.Uint32(buf[0:4]),
			Level:     binary.LittleEndian.Uint32(buf[4:8]),
			Value:     value,
		})
	}
	return slots, nil
}

// validateRemoteFunctionStart uses an exact, locally verified signature. A
// permissive "not 00/CC" check can land in the middle of an unrelated function
// after a game update and is not safe enough for CreateRemoteThread.
func (a *App) validateRemoteFunctionStart(fn uintptr, label string) error {
	if fn == 0 {
		return fmt.Errorf("%s 地址无效(0)，已中止调用", label)
	}
	buf := make([]byte, len(gameSaveFunctionPrologue))
	if err := readProcessMemory(a.hProcess, fn, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return fmt.Errorf("读取 %s 函数序言失败（游戏版本可能不受支持）: %w", label, err)
	}
	if !bytes.Equal(buf, gameSaveFunctionPrologue) {
		return fmt.Errorf("%s 精确签名不匹配，当前游戏版本未通过运行时写入校验：%s", label, bytesToHex(buf))
	}
	return nil
}

func (a *App) callRemoteOneArg(fn uintptr, arg uintptr) error {
	if err := a.validateRemoteFunctionStart(fn, "游戏内保存函数"); err != nil {
		return err
	}
	code := make([]byte, 0, 32)
	code = append(code, 0x48, 0xB9)
	code = binary.LittleEndian.AppendUint64(code, uint64(arg))
	code = append(code, 0x48, 0xB8)
	code = binary.LittleEndian.AppendUint64(code, uint64(fn))
	code = append(code, 0x48, 0x83, 0xEC, 0x28)
	code = append(code, 0xFF, 0xD0)
	code = append(code, 0x48, 0x83, 0xC4, 0x28)
	code = append(code, 0xC3)

	remote, err := virtualAllocRemote(a.hProcess, uintptr(len(code)), windows.PAGE_EXECUTE_READWRITE)
	if err != nil {
		return err
	}
	freeRemote := true
	defer func() {
		if freeRemote {
			_ = virtualFreeRemote(a.hProcess, remote)
		}
	}()
	if err := writeCodeMemory(a.hProcess, remote, code); err != nil {
		return err
	}
	thread, err := createRemoteThread(a.hProcess, remote, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	wait, waitErr := windows.WaitForSingleObject(thread, 5000)
	if err := classifyRemoteCallWait(wait, waitErr); err != nil {
		// Once CreateRemoteThread succeeds, a timeout or failed wait gives us no
		// proof that the thread stopped. Keep the thunk mapped until process exit
		// so a late-starting/late-returning thread can never execute freed memory.
		freeRemote = false
		a.poisonCurrentLiveMemoryWrites()
		return err
	}
	return nil
}

func validOverLimitAttribute(v uint32) bool {
	_, ok := overLimitCatalog[v]
	return ok
}

func overLimitValueSpec(v uint32) (float32, float32, bool) {
	entry, ok := overLimitCatalog[v]
	if !ok {
		return 0, 1, false
	}
	return entry.maxValue, entry.scale, true
}

func validOverLimitLevel(v uint32) bool {
	for _, opt := range overLimitLevelOptions {
		if opt.ID == v {
			return true
		}
	}
	return false
}
