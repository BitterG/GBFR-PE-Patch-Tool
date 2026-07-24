package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Two independent highlighted-item paths feed the address capture. This
// implementation never writes the
// record at +0/+4/+8 and never invokes a game save function.
const (
	runtimePatchSelectedMaterialAOB = "488Bxxxx4885xx74xx448Bxxxx4889xxE8xxxxxxxx498B"
	runtimePatchSelectedKeyItemAOB  = "448Bxxxx4889xx89xxE8xxxxxxxx488Bxxxxxxxxxx488Bxxxxxxxxxx4839"

	runtimePatchSelectedMaterialRVA = uintptr(0x3F4BAC3)
	runtimePatchSelectedKeyItemRVA  = uintptr(0x3F2061C)
	runtimePatchSelectedHookSize    = 7

	runtimePatchSelectedItemRecordSize    = 0x0C
	runtimePatchSelectedCaveMarkerOffset  = uintptr(0x30)
	runtimePatchSelectedCaveKindOffset    = uintptr(0x38)
	runtimePatchSelectedCavePIDOffset     = uintptr(0x3C)
	runtimePatchSelectedCaveCreatedOffset = uintptr(0x40)
	runtimePatchSelectedCaveOwnerOffset   = uintptr(0x48)
	runtimePatchSelectedCaveDataOffset    = uintptr(0x50)
)

var (
	runtimePatchSelectedMaterialOriginal = []byte{0x48, 0x8B, 0x41, 0x18, 0x48, 0x85, 0xC0}
	runtimePatchSelectedKeyItemOriginal  = []byte{0x44, 0x8B, 0x40, 0x04, 0x48, 0x89, 0xD9}
	runtimePatchSelectedCaveMarker       = [...]byte{'G', 'B', 'F', 'R', 'I', 'T', 'M', '1'}
	runtimePatchSelectedInstallHook      = installRemoteCodeHook
)

type RuntimePatchSelectedItemKind string

const (
	RuntimePatchSelectedItemMaterial RuntimePatchSelectedItemKind = "material"
	RuntimePatchSelectedItemKeyItem  RuntimePatchSelectedItemKind = "keyItem"
)

type RuntimePatchSelectedItemCapture struct {
	Kind         RuntimePatchSelectedItemKind `json:"kind"`
	DisplayName  string                       `json:"displayName"`
	Found        bool                         `json:"found"`
	Hooked       bool                         `json:"hooked"`
	Address      uint64                       `json:"address"`
	RVA          uint64                       `json:"rva"`
	SelectedAddr uint64                       `json:"selectedAddr"`
	Captured     bool                         `json:"captured"`
}

type RuntimePatchSelectedItemsStatus struct {
	OwnerToken     string                          `json:"ownerToken,omitempty"`
	PID            uint32                          `json:"pid"`
	ProcessCreated uint64                          `json:"processCreated"`
	Enabled        bool                            `json:"enabled"`
	ReadOnly       bool                            `json:"readOnly"`
	GameVersion    string                          `json:"gameVersion"`
	Source         string                          `json:"source"`
	Material       RuntimePatchSelectedItemCapture `json:"material"`
	KeyItem        RuntimePatchSelectedItemCapture `json:"keyItem"`
}

type RuntimePatchSelectedItemReadRequest struct {
	Kind                 RuntimePatchSelectedItemKind `json:"kind"`
	ExpectedSelectedAddr uint64                       `json:"expectedSelectedAddr"`
}

type RuntimePatchSelectedItemRecord struct {
	Kind         RuntimePatchSelectedItemKind `json:"kind"`
	DisplayName  string                       `json:"displayName"`
	SelectedAddr uint64                       `json:"selectedAddr"`
	Hash         uint32                       `json:"hash"`
	HashHex      string                       `json:"hashHex"`
	Name         string                       `json:"name"`
	Category     string                       `json:"category,omitempty"`
	Quantity     uint32                       `json:"quantity"`
	Flags        uint32                       `json:"flags"`
	FlagsHex     string                       `json:"flagsHex"`
	ReadOnly     bool                         `json:"readOnly"`
	GameVersion  string                       `json:"gameVersion"`
}

type runtimePatchSelectedCaptureLease struct {
	Kind       RuntimePatchSelectedItemKind
	HookAddr   uintptr
	CaveAddr   uintptr
	Original   []byte
	Process    processInstanceID
	OwnerToken string
}

func (lease runtimePatchSelectedCaptureLease) active() bool {
	return lease.Kind != "" || lease.HookAddr != 0 || lease.CaveAddr != 0 || len(lease.Original) != 0 || lease.Process.PID != 0 || lease.OwnerToken != ""
}

type runtimePatchSelectedItemMemory interface {
	ReadAt(address uintptr, destination []byte) error
	WriteAt(address uintptr, source []byte) error
}

type remoteRuntimePatchSelectedItemMemory struct {
	handle windows.Handle
}

func (memory remoteRuntimePatchSelectedItemMemory) ReadAt(address uintptr, destination []byte) error {
	if memory.handle == 0 {
		return fmt.Errorf("game process handle is empty")
	}
	if len(destination) == 0 {
		return nil
	}
	return readProcessMemory(memory.handle, address, unsafe.Pointer(&destination[0]), uintptr(len(destination)))
}

func (memory remoteRuntimePatchSelectedItemMemory) WriteAt(address uintptr, source []byte) error {
	if memory.handle == 0 {
		return fmt.Errorf("game process handle is empty")
	}
	if len(source) == 0 {
		return nil
	}
	return writeProcessMemory(memory.handle, address, unsafe.Pointer(&source[0]), uintptr(len(source)))
}

func (a *App) RuntimePatchSelectedItemsEnableOwned(token string) (RuntimePatchSelectedItemsStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.enableRuntimePatchSelectedItemsLocked(token)
}

func (a *App) RuntimePatchSelectedItemsStatusOwned(token string) (RuntimePatchSelectedItemsStatus, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.readRuntimePatchSelectedItemsStatusLocked(token)
}

func (a *App) RuntimePatchSelectedItemReadOwned(token string, request RuntimePatchSelectedItemReadRequest) (RuntimePatchSelectedItemRecord, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return RuntimePatchSelectedItemRecord{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()

	lease, err := a.runtimePatchSelectedLeaseForRequestLocked(token, request.Kind)
	if err != nil {
		return RuntimePatchSelectedItemRecord{}, err
	}
	memory := remoteRuntimePatchSelectedItemMemory{handle: a.hProcess}
	record, err := consumeRuntimePatchSelectedItemRecord(memory, lease.CaveAddr, request)
	if err != nil {
		return RuntimePatchSelectedItemRecord{}, err
	}
	decorateRuntimePatchSelectedItemRecord(&record)
	return record, nil
}

func (a *App) RuntimePatchSelectedItemsDisableOwned(token string) (RuntimePatchSelectedItemsStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	if err := a.releaseRuntimePatchSelectedCaptureHooksLocked(token, false); err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	return a.readRuntimePatchSelectedItemsStatusLocked(token)
}

func (a *App) enableRuntimePatchSelectedItemsLocked(token string) (RuntimePatchSelectedItemsStatus, error) {
	if a.runtimePatchSelectedMaterialHook.active() || a.runtimePatchSelectedKeyItemHook.active() {
		status, err := a.readRuntimePatchSelectedItemsStatusLocked(token)
		if err == nil && status.Enabled {
			return status, nil
		}
		if releaseErr := a.releaseRuntimePatchSelectedCaptureHooksLocked(token, false); releaseErr != nil {
			return RuntimePatchSelectedItemsStatus{}, errors.Join(err, fmt.Errorf("clear previous selected-item recovery lease: %w", releaseErr))
		}
	}

	materialAddress, materialOriginal, err := a.locateRuntimePatchSelectedHookLocked(RuntimePatchSelectedItemMaterial)
	if err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	keyAddress, keyOriginal, err := a.locateRuntimePatchSelectedHookLocked(RuntimePatchSelectedItemKeyItem)
	if err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	process := a.currentProcessInstance()
	materialLease, err := a.prepareRuntimePatchSelectedCaptureLease(RuntimePatchSelectedItemMaterial, materialAddress, materialOriginal, process, token)
	if err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	keyLease, err := a.prepareRuntimePatchSelectedCaptureLease(RuntimePatchSelectedItemKeyItem, keyAddress, keyOriginal, process, token)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, materialLease.CaveAddr)
		return RuntimePatchSelectedItemsStatus{}, err
	}

	materialPatch, err := makeRelJump(materialLease.HookAddr, materialLease.CaveAddr, runtimePatchSelectedHookSize)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, materialLease.CaveAddr)
		_ = virtualFreeRemote(a.hProcess, keyLease.CaveAddr)
		return RuntimePatchSelectedItemsStatus{}, err
	}
	installResult, err := runtimePatchSelectedInstallHook(a.hProcess, materialLease.HookAddr, materialLease.Original, materialPatch)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, keyLease.CaveAddr)
		return RuntimePatchSelectedItemsStatus{}, runtimeHookInstallFailure(
			runtimePatchMonitorText("选中素材捕获 Hook", "Selected-material capture hook"), installResult, err,
			func() { _ = virtualFreeRemote(a.hProcess, materialLease.CaveAddr) },
			func() {
				a.retireRuntimeCaveLocked(materialLease.CaveAddr, "RuntimePatch selected material install rollback")
			},
			func() { a.runtimePatchSelectedMaterialHook = materialLease },
			a.poisonCurrentLiveMemoryWrites,
		)
	}
	a.runtimePatchSelectedMaterialHook = materialLease

	installResult, err = installPreparedRuntimePatchSelectedHook(a.hProcess, keyLease, makeRelJump, runtimePatchSelectedInstallHook)
	if err != nil {
		installErr := runtimeHookInstallFailure(
			runtimePatchMonitorText("选中关键物品捕获 Hook", "Selected-key-item capture hook"), installResult, err,
			func() { _ = virtualFreeRemote(a.hProcess, keyLease.CaveAddr) },
			func() {
				a.retireRuntimeCaveLocked(keyLease.CaveAddr, "RuntimePatch selected key item install rollback")
			},
			func() { a.runtimePatchSelectedKeyItemHook = keyLease },
			a.poisonCurrentLiveMemoryWrites,
		)
		rollbackErr := a.releaseRuntimePatchSelectedCaptureHookLocked(&a.runtimePatchSelectedMaterialHook, token, false)
		if rollbackErr != nil {
			a.poisonCurrentLiveMemoryWrites()
		}
		return RuntimePatchSelectedItemsStatus{}, errors.Join(installErr, rollbackErr)
	}
	a.runtimePatchSelectedKeyItemHook = keyLease

	status, verifyErr := a.readRuntimePatchSelectedItemsStatusLocked(token)
	if verifyErr == nil && status.Enabled {
		return status, nil
	}
	rollbackErr := a.releaseRuntimePatchSelectedCaptureHooksLocked(token, false)
	if rollbackErr != nil {
		a.poisonCurrentLiveMemoryWrites()
		return RuntimePatchSelectedItemsStatus{}, errors.Join(verifyErr, errRuntimeHookRollbackUnproven, rollbackErr)
	}
	if verifyErr == nil {
		verifyErr = fmt.Errorf("selected-item hook pair did not become enabled")
	}
	return RuntimePatchSelectedItemsStatus{}, fmt.Errorf("selected-item hook verification failed; both entries restored: %w", verifyErr)
}

func installPreparedRuntimePatchSelectedHook(
	handle windows.Handle,
	lease runtimePatchSelectedCaptureLease,
	buildJump func(uintptr, uintptr, int) ([]byte, error),
	install func(windows.Handle, uintptr, []byte, []byte) (codeHookInstallResult, error),
) (codeHookInstallResult, error) {
	if buildJump == nil || install == nil {
		return codeHookInstallResult{State: codeHookEntryNeverPublished}, fmt.Errorf("selected-item hook installer is incomplete")
	}
	patch, err := buildJump(lease.HookAddr, lease.CaveAddr, runtimePatchSelectedHookSize)
	if err != nil {
		// No entry write was attempted, so the prepared but unreachable cave is
		// always safe for the caller to free.
		return codeHookInstallResult{State: codeHookEntryNeverPublished}, err
	}
	return install(handle, lease.HookAddr, lease.Original, patch)
}

func (a *App) prepareRuntimePatchSelectedCaptureLease(kind RuntimePatchSelectedItemKind, hook uintptr, original []byte, process processInstanceID, token string) (runtimePatchSelectedCaptureLease, error) {
	cave, err := virtualAllocRemoteNear(a.hProcess, hook, 0x1000)
	if err != nil {
		return runtimePatchSelectedCaptureLease{}, fmt.Errorf("%s: %w", runtimePatchMonitorText("分配选中物品捕获代码洞失败", "Allocate selected-item capture cave"), err)
	}
	code, err := buildRuntimePatchSelectedCaptureCave(kind, cave, hook+runtimePatchSelectedHookSize, original, process, token)
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return runtimePatchSelectedCaptureLease{}, err
	}
	if err := writeCodeMemory(a.hProcess, cave, code); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return runtimePatchSelectedCaptureLease{}, fmt.Errorf("%s: %w", runtimePatchMonitorText("写入选中物品捕获代码洞失败", "Write selected-item capture cave"), err)
	}
	written := make([]byte, len(code))
	if err := readProcessMemory(a.hProcess, cave, unsafe.Pointer(&written[0]), uintptr(len(written))); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return runtimePatchSelectedCaptureLease{}, fmt.Errorf("%s: %w", runtimePatchMonitorText("回读选中物品捕获代码洞失败", "Read back selected-item capture cave"), err)
	}
	if !bytes.Equal(written, code) {
		_ = virtualFreeRemote(a.hProcess, cave)
		return runtimePatchSelectedCaptureLease{}, fmt.Errorf("%s", runtimePatchMonitorText("选中物品捕获代码洞写后回读不一致", "Selected-item capture cave readback mismatch"))
	}
	if err := validateRuntimePatchSelectedCaptureCaveBytes(kind, cave, hook+runtimePatchSelectedHookSize, original, process, token, written); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return runtimePatchSelectedCaptureLease{}, err
	}
	return runtimePatchSelectedCaptureLease{Kind: kind, HookAddr: hook, CaveAddr: cave, Original: append([]byte(nil), original...), Process: process, OwnerToken: token}, nil
}

func (a *App) locateRuntimePatchSelectedHookLocked(kind RuntimePatchSelectedItemKind) (uintptr, []byte, error) {
	rawPattern, rva, expected, err := runtimePatchSelectedHookDefinition(kind)
	if err != nil {
		return 0, nil, err
	}
	pattern, err := parseRuntimePatchPattern(rawPattern)
	if err != nil {
		return 0, nil, err
	}
	address, ok := checkedRuntimePatchMonitorAddress(a.moduleBase, rva)
	if !ok {
		return 0, nil, fmt.Errorf("selected-item hook address overflow")
	}
	actualPattern := make([]byte, len(pattern.Values))
	if err := readProcessMemory(a.hProcess, address, unsafe.Pointer(&actualPattern[0]), uintptr(len(actualPattern))); err != nil {
		return 0, nil, fmt.Errorf("%s: %w", runtimePatchSelectedKindName(kind), err)
	}
	if !matchRuntimePatchPattern(actualPattern, pattern) {
		return 0, nil, fmt.Errorf("%s: %s RVA 0x%X", runtimePatchSelectedKindName(kind), runtimePatchMonitorText("签名与游戏 2.0.2 不匹配", "signature does not match game 2.0.2"), rva)
	}
	if !bytes.Equal(actualPattern[:runtimePatchSelectedHookSize], expected) {
		return 0, nil, fmt.Errorf("%s: %s %s", runtimePatchSelectedKindName(kind), runtimePatchMonitorText("入口原始字节不匹配", "entry bytes mismatch"), bytesToHex(actualPattern[:runtimePatchSelectedHookSize]))
	}
	return address, append([]byte(nil), expected...), nil
}

func (a *App) readRuntimePatchSelectedItemsStatusLocked(token string) (RuntimePatchSelectedItemsStatus, error) {
	result := RuntimePatchSelectedItemsStatus{
		OwnerToken: token, PID: a.charaPID, ProcessCreated: a.charaCreated,
		ReadOnly: true, GameVersion: "2.0.2", Source: "game_selected_item_read_only_2.0.2",
	}
	material, err := a.readRuntimePatchSelectedCaptureStatusLocked(token, RuntimePatchSelectedItemMaterial, &a.runtimePatchSelectedMaterialHook)
	if err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	keyItem, err := a.readRuntimePatchSelectedCaptureStatusLocked(token, RuntimePatchSelectedItemKeyItem, &a.runtimePatchSelectedKeyItemHook)
	if err != nil {
		return RuntimePatchSelectedItemsStatus{}, err
	}
	result.Material = material
	result.KeyItem = keyItem
	result.Enabled = material.Hooked && keyItem.Hooked
	return result, nil
}

func (a *App) readRuntimePatchSelectedCaptureStatusLocked(token string, kind RuntimePatchSelectedItemKind, lease *runtimePatchSelectedCaptureLease) (RuntimePatchSelectedItemCapture, error) {
	result := RuntimePatchSelectedItemCapture{Kind: kind, DisplayName: runtimePatchSelectedKindName(kind)}
	_, rva, _, definitionErr := runtimePatchSelectedHookDefinition(kind)
	if definitionErr != nil {
		return result, definitionErr
	}
	result.RVA = uint64(rva)
	if lease == nil || !lease.active() {
		address, _, err := a.locateRuntimePatchSelectedHookLocked(kind)
		if err != nil {
			return result, err
		}
		result.Found = true
		result.Address = uint64(address)
		return result, nil
	}
	if lease.Kind != kind || !sameProcessInstance(lease.Process, a.currentProcessInstance()) || lease.OwnerToken != token || !runtimeOwnerTokenMatches(a.charaOwnerToken, token) {
		return result, fmt.Errorf("%s", runtimePatchMonitorText("选中物品捕获租约的所有者或进程实例已失效", "Selected-item capture lease owner or process instance is stale"))
	}
	entry := make([]byte, runtimePatchSelectedHookSize)
	if err := readProcessMemory(a.hProcess, lease.HookAddr, unsafe.Pointer(&entry[0]), uintptr(len(entry))); err != nil {
		return result, err
	}
	result.Found = true
	result.Address = uint64(lease.HookAddr)
	if bytes.Equal(entry, lease.Original) {
		return result, nil
	}
	if !isRuntimePatchSelectedJump(entry) || relJumpTarget(lease.HookAddr, entry) != lease.CaveAddr {
		return result, fmt.Errorf("%s", runtimePatchMonitorText("选中物品 Hook 入口已被外部修改", "Selected-item hook entry was replaced externally"))
	}
	if err := a.validateRuntimePatchSelectedCaptureCaveLocked(*lease); err != nil {
		return result, err
	}
	selected, err := readRuntimePatchSelectedPointer(remoteRuntimePatchSelectedItemMemory{handle: a.hProcess}, lease.CaveAddr+runtimePatchSelectedCaveDataOffset)
	if err != nil {
		return result, err
	}
	result.Hooked = true
	result.SelectedAddr = uint64(selected)
	result.Captured = selected != 0
	return result, nil
}

func (a *App) runtimePatchSelectedLeaseForRequestLocked(token string, kind RuntimePatchSelectedItemKind) (*runtimePatchSelectedCaptureLease, error) {
	var lease *runtimePatchSelectedCaptureLease
	switch kind {
	case RuntimePatchSelectedItemMaterial:
		lease = &a.runtimePatchSelectedMaterialHook
	case RuntimePatchSelectedItemKeyItem:
		lease = &a.runtimePatchSelectedKeyItemHook
	default:
		return nil, fmt.Errorf("%s: %q", runtimePatchMonitorText("未知选中物品类型", "Unknown selected-item kind"), kind)
	}
	status, err := a.readRuntimePatchSelectedCaptureStatusLocked(token, kind, lease)
	if err != nil {
		return nil, err
	}
	if !status.Hooked || !lease.active() {
		return nil, fmt.Errorf("%s", runtimePatchMonitorText("选中物品只读捕获尚未启用", "Selected-item read-only capture is not enabled"))
	}
	return lease, nil
}

func consumeRuntimePatchSelectedItemRecord(memory runtimePatchSelectedItemMemory, cave uintptr, request RuntimePatchSelectedItemReadRequest) (RuntimePatchSelectedItemRecord, error) {
	if memory == nil || cave == 0 || (request.Kind != RuntimePatchSelectedItemMaterial && request.Kind != RuntimePatchSelectedItemKeyItem) || request.ExpectedSelectedAddr == 0 {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("invalid selected-item read request")
	}
	expected := uintptr(request.ExpectedSelectedAddr)
	if uint64(expected) != request.ExpectedSelectedAddr || expected > ^uintptr(0)-(runtimePatchSelectedItemRecordSize-1) {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("ExpectedSelectedAddr is outside the local address range")
	}
	captured, err := readRuntimePatchSelectedPointer(memory, cave+runtimePatchSelectedCaveDataOffset)
	if err != nil {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("read selected-item capture: %w", err)
	}
	if captured == 0 || captured != expected {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("ExpectedSelectedAddr mismatch: captured=0x%X expected=0x%X; select the item again", captured, expected)
	}
	first := make([]byte, runtimePatchSelectedItemRecordSize)
	if err := memory.ReadAt(expected, first); err != nil {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("read selected-item record: %w", err)
	}
	confirmedPointer, err := readRuntimePatchSelectedPointer(memory, cave+runtimePatchSelectedCaveDataOffset)
	if err != nil {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("revalidate selected-item capture: %w", err)
	}
	if confirmedPointer != expected {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("selected-item pointer changed during read: got=0x%X want=0x%X", confirmedPointer, expected)
	}
	confirmed := make([]byte, runtimePatchSelectedItemRecordSize)
	if err := memory.ReadAt(expected, confirmed); err != nil {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("revalidate selected-item record: %w", err)
	}
	if !bytes.Equal(first, confirmed) {
		return RuntimePatchSelectedItemRecord{}, fmt.Errorf("selected-item record changed during full 0x0C revalidation")
	}
	if err := clearRuntimePatchSelectedPointer(memory, cave+runtimePatchSelectedCaveDataOffset); err != nil {
		return RuntimePatchSelectedItemRecord{}, err
	}
	return RuntimePatchSelectedItemRecord{
		Kind: request.Kind, DisplayName: runtimePatchSelectedKindName(request.Kind), SelectedAddr: uint64(expected),
		Hash: binary.LittleEndian.Uint32(confirmed[0:4]), Quantity: binary.LittleEndian.Uint32(confirmed[4:8]), Flags: binary.LittleEndian.Uint32(confirmed[8:12]),
		ReadOnly: true, GameVersion: "2.0.2",
	}, nil
}

func readRuntimePatchSelectedPointer(memory interface{ ReadAt(uintptr, []byte) error }, address uintptr) (uintptr, error) {
	encoded := make([]byte, 8)
	if err := memory.ReadAt(address, encoded); err != nil {
		return 0, err
	}
	value := binary.LittleEndian.Uint64(encoded)
	if uint64(uintptr(value)) != value {
		return 0, fmt.Errorf("selected-item pointer is outside the local address width: 0x%X", value)
	}
	return uintptr(value), nil
}

func clearRuntimePatchSelectedPointer(memory runtimePatchSelectedItemMemory, address uintptr) error {
	zero := make([]byte, 8)
	if err := memory.WriteAt(address, zero); err != nil {
		return fmt.Errorf("clear selected-item capture: %w", err)
	}
	verified := make([]byte, 8)
	if err := memory.ReadAt(address, verified); err != nil {
		return fmt.Errorf("verify cleared selected-item capture: %w", err)
	}
	if !bytes.Equal(verified, zero) {
		return fmt.Errorf("selected-item capture clear readback mismatch: %s", bytesToHex(verified))
	}
	return nil
}

func decorateRuntimePatchSelectedItemRecord(record *RuntimePatchSelectedItemRecord) {
	if record == nil {
		return
	}
	record.HashHex = fmt.Sprintf("%08X", record.Hash)
	record.FlagsHex = fmt.Sprintf("%08X", record.Flags)
	record.Name = fmt.Sprintf("0x%08X", record.Hash)
	if _, err := loadProgressionCatalog(); err == nil {
		if definition, ok := progressionItemByHash[record.Hash]; ok {
			record.Name = progressionItemName(definition)
			record.Category = definition.Category
		}
	}
}

func buildRuntimePatchSelectedCaptureCave(kind RuntimePatchSelectedItemKind, cave, returnAddr uintptr, original []byte, process processInstanceID, ownerToken string) ([]byte, error) {
	_, _, expected, err := runtimePatchSelectedHookDefinition(kind)
	if err != nil {
		return nil, err
	}
	if len(original) != runtimePatchSelectedHookSize || !bytes.Equal(original, expected) || cave == 0 || returnAddr == 0 || process.PID == 0 || process.Created == 0 || strings.TrimSpace(ownerToken) == "" {
		return nil, fmt.Errorf("invalid selected-item capture cave parameters")
	}
	code := make([]byte, 0, runtimePatchSelectedCaveDataOffset+8)
	code = append(code, original...)
	// RAX is the selected record pointer for both monitored paths. MOV moffs64,RAX is
	// register- and flag-preserving. On the material path it executes after
	// test rax,rax and unconditionally writes zero before the displaced JE.
	code = append(code, 0x48, 0xA3)
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+runtimePatchSelectedCaveDataOffset))
	jump, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jump...)
	for len(code) < int(runtimePatchSelectedCaveDataOffset)+8 {
		code = append(code, 0)
	}
	copy(code[runtimePatchSelectedCaveMarkerOffset:], runtimePatchSelectedCaveMarker[:])
	code[runtimePatchSelectedCaveKindOffset] = runtimePatchSelectedKindByte(kind)
	binary.LittleEndian.PutUint32(code[runtimePatchSelectedCavePIDOffset:runtimePatchSelectedCavePIDOffset+4], process.PID)
	binary.LittleEndian.PutUint64(code[runtimePatchSelectedCaveCreatedOffset:runtimePatchSelectedCaveCreatedOffset+8], process.Created)
	binary.LittleEndian.PutUint64(code[runtimePatchSelectedCaveOwnerOffset:runtimePatchSelectedCaveOwnerOffset+8], runtimePatchSelectedOwnerFingerprint(ownerToken))
	return code, nil
}

func validateRuntimePatchSelectedCaptureCaveBytes(kind RuntimePatchSelectedItemKind, cave, returnAddr uintptr, original []byte, process processInstanceID, ownerToken string, code []byte) error {
	minimum := int(runtimePatchSelectedCaveDataOffset) + 8
	if len(code) < minimum || len(original) != runtimePatchSelectedHookSize {
		return fmt.Errorf("selected-item cave or original bytes are too short")
	}
	if !bytes.Equal(code[:runtimePatchSelectedHookSize], original) {
		return fmt.Errorf("selected-item cave displaced instructions mismatch")
	}
	captureOffset := runtimePatchSelectedHookSize
	if !bytes.Equal(code[captureOffset:captureOffset+2], []byte{0x48, 0xA3}) || binary.LittleEndian.Uint64(code[captureOffset+2:captureOffset+10]) != uint64(cave+runtimePatchSelectedCaveDataOffset) {
		return fmt.Errorf("selected-item cave capture instruction mismatch")
	}
	jumpOffset := captureOffset + 10
	if relJumpTarget(cave+uintptr(jumpOffset), code[jumpOffset:jumpOffset+5]) != returnAddr {
		return fmt.Errorf("selected-item cave return target mismatch")
	}
	if !bytes.Equal(code[runtimePatchSelectedCaveMarkerOffset:runtimePatchSelectedCaveMarkerOffset+uintptr(len(runtimePatchSelectedCaveMarker))], runtimePatchSelectedCaveMarker[:]) ||
		code[runtimePatchSelectedCaveKindOffset] != runtimePatchSelectedKindByte(kind) {
		return fmt.Errorf("selected-item cave ownership marker mismatch")
	}
	if binary.LittleEndian.Uint32(code[runtimePatchSelectedCavePIDOffset:runtimePatchSelectedCavePIDOffset+4]) != process.PID ||
		binary.LittleEndian.Uint64(code[runtimePatchSelectedCaveCreatedOffset:runtimePatchSelectedCaveCreatedOffset+8]) != process.Created {
		return fmt.Errorf("selected-item cave process identity mismatch")
	}
	if binary.LittleEndian.Uint64(code[runtimePatchSelectedCaveOwnerOffset:runtimePatchSelectedCaveOwnerOffset+8]) != runtimePatchSelectedOwnerFingerprint(ownerToken) {
		return fmt.Errorf("selected-item cave owner fingerprint mismatch")
	}
	return nil
}

func (a *App) validateRuntimePatchSelectedCaptureCaveLocked(lease runtimePatchSelectedCaptureLease) error {
	code := make([]byte, int(runtimePatchSelectedCaveDataOffset)+8)
	if err := readProcessMemory(a.hProcess, lease.CaveAddr, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		return fmt.Errorf("read selected-item capture cave: %w", err)
	}
	return validateRuntimePatchSelectedCaptureCaveBytes(lease.Kind, lease.CaveAddr, lease.HookAddr+runtimePatchSelectedHookSize, lease.Original, lease.Process, lease.OwnerToken, code)
}

func (a *App) releaseRuntimePatchSelectedCaptureHooksLocked(ownerToken string, force bool) error {
	var result error
	if err := a.releaseRuntimePatchSelectedCaptureHookLocked(&a.runtimePatchSelectedKeyItemHook, ownerToken, force); err != nil {
		result = errors.Join(result, fmt.Errorf("key item: %w", err))
	}
	if err := a.releaseRuntimePatchSelectedCaptureHookLocked(&a.runtimePatchSelectedMaterialHook, ownerToken, force); err != nil {
		result = errors.Join(result, fmt.Errorf("material: %w", err))
	}
	return result
}

func (a *App) releaseRuntimePatchSelectedCaptureHookLocked(lease *runtimePatchSelectedCaptureLease, ownerToken string, force bool) error {
	if lease == nil || !lease.active() {
		return nil
	}
	if !sameProcessInstance(lease.Process, a.currentProcessInstance()) {
		return fmt.Errorf("selected-item recovery lease belongs to another process instance")
	}
	if !force && (ownerToken == "" || lease.OwnerToken != ownerToken || !runtimeOwnerTokenMatches(a.charaOwnerToken, ownerToken)) {
		return errRuntimeOwnerLeaseStale
	}
	if len(lease.Original) != runtimePatchSelectedHookSize || lease.HookAddr == 0 || lease.CaveAddr == 0 {
		return fmt.Errorf("selected-item recovery lease is incomplete")
	}
	entry := make([]byte, runtimePatchSelectedHookSize)
	if err := readProcessMemory(a.hProcess, lease.HookAddr, unsafe.Pointer(&entry[0]), uintptr(len(entry))); err != nil {
		return err
	}
	originalEntry := bytes.Equal(entry, lease.Original)
	if !originalEntry {
		if !isRuntimePatchSelectedJump(entry) || relJumpTarget(lease.HookAddr, entry) != lease.CaveAddr {
			return fmt.Errorf("selected-item hook entry is neither the owned jump nor exact original bytes: %s", bytesToHex(entry))
		}
	}
	if err := a.validateRuntimePatchSelectedCaptureCaveLocked(*lease); err != nil {
		return fmt.Errorf("selected-item cave ownership validation failed: %w", err)
	}
	memory := remoteRuntimePatchSelectedItemMemory{handle: a.hProcess}
	preClearErr := clearRuntimePatchSelectedPointer(memory, lease.CaveAddr+runtimePatchSelectedCaveDataOffset)
	if !originalEntry {
		if err := writeCodeMemory(a.hProcess, lease.HookAddr, lease.Original); err != nil {
			return errors.Join(preClearErr, fmt.Errorf("restore selected-item hook entry: %w", err))
		}
		restored := make([]byte, runtimePatchSelectedHookSize)
		if err := readProcessMemory(a.hProcess, lease.HookAddr, unsafe.Pointer(&restored[0]), uintptr(len(restored))); err != nil {
			return errors.Join(preClearErr, fmt.Errorf("read back restored selected-item hook entry: %w", err))
		}
		if !bytes.Equal(restored, lease.Original) {
			return errors.Join(preClearErr, fmt.Errorf("restored selected-item hook entry mismatch: %s", bytesToHex(restored)))
		}
	}
	postClearErr := clearRuntimePatchSelectedPointer(memory, lease.CaveAddr+runtimePatchSelectedCaveDataOffset)
	if err := errors.Join(preClearErr, postClearErr); err != nil {
		return err
	}
	a.retireRuntimeCaveLocked(lease.CaveAddr, "RuntimePatch selected-item capture release")
	*lease = runtimePatchSelectedCaptureLease{}
	return nil
}

func (a *App) dropRuntimePatchSelectedCaptureHooksLocked(ownerToken string, force bool) {
	for _, lease := range []*runtimePatchSelectedCaptureLease{&a.runtimePatchSelectedMaterialHook, &a.runtimePatchSelectedKeyItemHook} {
		if !lease.active() || force || (ownerToken != "" && lease.OwnerToken == ownerToken) {
			*lease = runtimePatchSelectedCaptureLease{}
		}
	}
}

func (a *App) hasRuntimePatchSelectedCaptureLeaseLocked() bool {
	return a.runtimePatchSelectedMaterialHook.active() || a.runtimePatchSelectedKeyItemHook.active()
}

func isRuntimePatchSelectedJump(entry []byte) bool {
	return len(entry) == runtimePatchSelectedHookSize && entry[0] == 0xE9 && entry[5] == 0x90 && entry[6] == 0x90
}

func runtimePatchSelectedHookDefinition(kind RuntimePatchSelectedItemKind) (string, uintptr, []byte, error) {
	switch kind {
	case RuntimePatchSelectedItemMaterial:
		return runtimePatchSelectedMaterialAOB, runtimePatchSelectedMaterialRVA, runtimePatchSelectedMaterialOriginal, nil
	case RuntimePatchSelectedItemKeyItem:
		return runtimePatchSelectedKeyItemAOB, runtimePatchSelectedKeyItemRVA, runtimePatchSelectedKeyItemOriginal, nil
	default:
		return "", 0, nil, fmt.Errorf("unknown selected-item kind %q", kind)
	}
}

func runtimePatchSelectedKindByte(kind RuntimePatchSelectedItemKind) byte {
	if kind == RuntimePatchSelectedItemMaterial {
		return 1
	}
	if kind == RuntimePatchSelectedItemKeyItem {
		return 2
	}
	return 0
}

func runtimePatchSelectedOwnerFingerprint(token string) uint64 {
	sum := sha256.Sum256([]byte(token))
	return binary.LittleEndian.Uint64(sum[:8])
}

func runtimePatchSelectedKindName(kind RuntimePatchSelectedItemKind) string {
	if useChinese() {
		if kind == RuntimePatchSelectedItemMaterial {
			return "当前选中素材"
		}
		if kind == RuntimePatchSelectedItemKeyItem {
			return "当前选中关键物品"
		}
	}
	if kind == RuntimePatchSelectedItemMaterial {
		return "Selected Material"
	}
	if kind == RuntimePatchSelectedItemKeyItem {
		return "Selected Key Item"
	}
	return string(kind)
}
