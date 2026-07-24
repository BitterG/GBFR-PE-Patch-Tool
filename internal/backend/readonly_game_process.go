package backend

import (
	"bytes"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// readonlyGameProcessAccess is deliberately exact. Formula sampling must never
// inherit the broader access mask used by runtime editing pages.
const readonlyGameProcessAccess = uint32(windows.PROCESS_QUERY_INFORMATION | windows.PROCESS_VM_READ)

type readOnlyProcessBackend interface {
	FindProcess(name string) (uint32, error)
	OpenProcess(access uint32, pid uint32) (readOnlyProcessHandle, error)
}

type readOnlyProcessHandle interface {
	runtimeCharacterPanelMemory
	ModuleBase() (uintptr, error)
	CreationTime() (uint64, error)
	IsMappedAddress(uint64) (bool, error)
	Alive() bool
	Close() error
}

type readOnlyGameProcess struct {
	handle     readOnlyProcessHandle
	pid        uint32
	created    uint64
	moduleBase uintptr
	guards     []runtimeCharacterPanelVersionGuard
}

func openReadOnlyGameProcess(backend readOnlyProcessBackend, name string, guards []runtimeCharacterPanelVersionGuard) (*readOnlyGameProcess, error) {
	if backend == nil {
		return nil, fmt.Errorf("read-only process backend is nil")
	}
	if len(guards) == 0 {
		return nil, fmt.Errorf("read-only process version guard set is empty")
	}
	pid, err := backend.FindProcess(name)
	if err != nil || pid == 0 {
		return nil, fmt.Errorf("find game process: %w", normalizeRuntimePanelReadError(err))
	}
	handle, err := backend.OpenProcess(readonlyGameProcessAccess, pid)
	if err != nil {
		return nil, fmt.Errorf("open game process read-only: %w", err)
	}
	if handle == nil {
		return nil, fmt.Errorf("open game process read-only returned nil handle")
	}
	closeOnFailure := true
	defer func() {
		if closeOnFailure {
			_ = handle.Close()
		}
	}()
	moduleBase, err := handle.ModuleBase()
	if err != nil || moduleBase == 0 {
		return nil, fmt.Errorf("read game module base: %w", normalizeRuntimePanelReadError(err))
	}
	created, err := handle.CreationTime()
	if err != nil || created == 0 {
		return nil, fmt.Errorf("read game process identity: %w", normalizeRuntimePanelReadError(err))
	}
	if !handle.Alive() {
		return nil, fmt.Errorf("game process exited during read-only attach")
	}
	if err := verifyReadOnlyGameProcessGuards(handle, moduleBase, guards); err != nil {
		return nil, err
	}
	closeOnFailure = false
	return &readOnlyGameProcess{
		handle: handle, pid: pid, created: created, moduleBase: moduleBase,
		guards: cloneRuntimePanelGuards(guards),
	}, nil
}

func verifyReadOnlyGameProcessGuards(memory runtimeCharacterPanelMemory, moduleBase uintptr, guards []runtimeCharacterPanelVersionGuard) error {
	for _, guard := range guards {
		address, ok := checkedRuntimePanelAddress(moduleBase, guard.RVA)
		if !ok || len(guard.Bytes) == 0 {
			return fmt.Errorf("invalid read-only version guard RVA 0x%X", guard.RVA)
		}
		actual := make([]byte, len(guard.Bytes))
		if err := memory.ReadAt(address, actual); err != nil {
			return fmt.Errorf("read version guard RVA 0x%X: %w", guard.RVA, err)
		}
		if !bytes.Equal(actual, guard.Bytes) {
			return fmt.Errorf("game version guard mismatch at RVA 0x%X", guard.RVA)
		}
	}
	return nil
}

func cloneRuntimePanelGuards(guards []runtimeCharacterPanelVersionGuard) []runtimeCharacterPanelVersionGuard {
	cloned := make([]runtimeCharacterPanelVersionGuard, len(guards))
	for index, guard := range guards {
		cloned[index] = runtimeCharacterPanelVersionGuard{RVA: guard.RVA, Bytes: append([]byte(nil), guard.Bytes...)}
	}
	return cloned
}

func (process *readOnlyGameProcess) ReadAt(address uintptr, destination []byte) error {
	if process == nil || process.handle == nil {
		return fmt.Errorf("read-only game process is closed")
	}
	return process.handle.ReadAt(address, destination)
}

func (process *readOnlyGameProcess) Validate() error {
	if process == nil || process.handle == nil {
		return fmt.Errorf("read-only game process is closed")
	}
	if !process.handle.Alive() {
		return fmt.Errorf("game process is no longer alive")
	}
	created, err := process.handle.CreationTime()
	if err != nil {
		return fmt.Errorf("re-read game process identity: %w", err)
	}
	if created == 0 || created != process.created {
		return fmt.Errorf("game process identity changed")
	}
	if err := verifyReadOnlyGameProcessGuards(process.handle, process.moduleBase, process.guards); err != nil {
		return fmt.Errorf("game version changed: %w", err)
	}
	return nil
}

func (process *readOnlyGameProcess) IsMappedAddress(value uint64) (bool, error) {
	if process == nil || process.handle == nil {
		return false, fmt.Errorf("read-only game process is closed")
	}
	return process.handle.IsMappedAddress(value)
}

func (process *readOnlyGameProcess) Close() error {
	if process == nil || process.handle == nil {
		return nil
	}
	handle := process.handle
	process.handle = nil
	return handle.Close()
}

type windowsReadOnlyProcessBackend struct{}

func (windowsReadOnlyProcessBackend) FindProcess(name string) (uint32, error) {
	return findProcessByName(name)
}

func (windowsReadOnlyProcessBackend) OpenProcess(access uint32, pid uint32) (readOnlyProcessHandle, error) {
	handle, err := windows.OpenProcess(access, false, pid)
	if err != nil {
		return nil, err
	}
	return &windowsReadOnlyProcessHandle{handle: handle}, nil
}

type windowsReadOnlyProcessHandle struct {
	handle windows.Handle
}

func (handle *windowsReadOnlyProcessHandle) ReadAt(address uintptr, destination []byte) error {
	if len(destination) == 0 {
		return nil
	}
	return readProcessMemory(handle.handle, address, unsafe.Pointer(&destination[0]), uintptr(len(destination)))
}

func (handle *windowsReadOnlyProcessHandle) ModuleBase() (uintptr, error) {
	return getModuleBase(handle.handle)
}

func (handle *windowsReadOnlyProcessHandle) CreationTime() (uint64, error) {
	return processCreationTime(handle.handle)
}

func (handle *windowsReadOnlyProcessHandle) IsMappedAddress(value uint64) (bool, error) {
	if handle == nil || handle.handle == 0 {
		return false, fmt.Errorf("read-only game process handle is closed")
	}
	if value < 0x10000 || value > 0x00007FFFFFFFFFFF || value > uint64(^uintptr(0)) {
		return false, nil
	}
	address := uintptr(value)
	var mbi memoryBasicInformation
	ret, _, _ := procVirtualQueryEx.Call(uintptr(handle.handle), address, uintptr(unsafe.Pointer(&mbi)), unsafe.Sizeof(mbi))
	if ret == 0 {
		return false, fmt.Errorf("VirtualQueryEx failed while redacting formula evidence")
	}
	if mbi.State != 0x1000 || mbi.RegionSize == 0 {
		return false, nil
	}
	end := mbi.BaseAddress + mbi.RegionSize
	return end > mbi.BaseAddress && address >= mbi.BaseAddress && address < end, nil
}

func (handle *windowsReadOnlyProcessHandle) Alive() bool { return processHandleAlive(handle.handle) }

func (handle *windowsReadOnlyProcessHandle) Close() error {
	if handle.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(handle.handle)
	handle.handle = 0
	return err
}
