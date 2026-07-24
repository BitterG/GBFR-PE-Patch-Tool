package backend

import (
	"bytes"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type codeHookWriter func([]byte) error
type codeHookReader func() ([]byte, error)

var errRuntimeHookRollbackUnproven = errors.New("运行时 Hook 原始入口未能确认恢复")

type codeHookEntryState uint8

const (
	// codeHookEntryRecoveryRequired is deliberately the zero value. An omitted
	// or partially constructed result must fail closed and retain the cave.
	codeHookEntryRecoveryRequired codeHookEntryState = iota
	// codeHookEntryNeverPublished means the patch writer was never called. A
	// prepared cave was therefore never reachable from the entry and may be
	// released immediately.
	codeHookEntryNeverPublished
	// codeHookEntryInstalled means the complete patch was read back and remains
	// active at the entry.
	codeHookEntryInstalled
	// codeHookEntryRestoredAfterPublishAttempt means the patch writer was called
	// (and a game thread may have entered the cave), but exact original bytes are
	// now proven at the entry. The cave must be retired, never freed in-process.
	codeHookEntryRestoredAfterPublishAttempt
)

type codeHookInstallResult struct {
	State codeHookEntryState
}

type retiredRuntimeCave struct {
	Address uintptr
	Process processInstanceID
	Label   string
}

func (result codeHookInstallResult) CanFreePreparedCave() bool {
	return result.State == codeHookEntryNeverPublished
}

func (result codeHookInstallResult) OriginalEntryProven() bool {
	return result.State == codeHookEntryRestoredAfterPublishAttempt
}

func (result codeHookInstallResult) RequiresRecoveryLease() bool {
	return result.State == codeHookEntryRecoveryRequired
}

// retireRuntimeCaveLocked records an unreachable cave without freeing it. The
// caller holds procMu, which keeps the process identity stable until detach.
func (a *App) retireRuntimeCaveLocked(address uintptr, label string) {
	if a == nil || address == 0 {
		return
	}
	process := a.currentProcessInstance()
	for _, cave := range a.retiredRuntimeCaves {
		if cave.Address == address && sameProcessInstance(cave.Process, process) {
			return
		}
	}
	a.retiredRuntimeCaves = append(a.retiredRuntimeCaves, retiredRuntimeCave{
		Address: address,
		Process: process,
		Label:   label,
	})
}

func runtimeHookInstallFailure(label string, result codeHookInstallResult, cause error, free, retire, retain, poison func()) error {
	installErr := fmt.Errorf("%s安装失败: %w", label, cause)
	if result.CanFreePreparedCave() {
		if free != nil {
			free()
		}
		return installErr
	}
	if result.OriginalEntryProven() {
		if retire != nil {
			retire()
		}
		return installErr
	}
	if retain != nil {
		retain()
	}
	if poison != nil {
		poison()
	}
	return errors.Join(installErr, errRuntimeHookRollbackUnproven)
}

func finalizeRuntimeHookEnable[T any](label string, read func() (T, error), rollback func() error, poison func()) (T, error) {
	var zero T
	if read == nil || rollback == nil {
		if poison != nil {
			poison()
		}
		return zero, errors.Join(fmt.Errorf("%s安装后验证参数无效", label), errRuntimeHookRollbackUnproven)
	}
	status, readErr := read()
	if readErr == nil {
		return status, nil
	}
	verificationErr := fmt.Errorf("%s安装后验证失败: %w", label, readErr)
	if rollbackErr := rollback(); rollbackErr != nil {
		if poison != nil {
			poison()
		}
		return zero, errors.Join(
			verificationErr,
			errRuntimeHookRollbackUnproven,
			fmt.Errorf("%s验证失败后的恢复也失败，已保留恢复租约: %w", label, rollbackErr),
		)
	}
	return zero, fmt.Errorf("%s安装后验证失败，原始入口已恢复: %w", label, readErr)
}

// installCodeHookAtomic distinguishes a prepared cave that was never published
// from an entry that may have reached the patch. Restoring exact original bytes
// makes the entry safe, but cannot prove that every remote thread has already
// left the cave, so callers must retire rather than free that cave.
func installCodeHookAtomic(original, patch []byte, writer codeHookWriter, reader codeHookReader) (codeHookInstallResult, error) {
	neverPublished := codeHookInstallResult{State: codeHookEntryNeverPublished}
	if len(original) == 0 || len(original) != len(patch) || writer == nil || reader == nil {
		return neverPublished, fmt.Errorf("Hook 安装参数无效")
	}
	current, err := reader()
	if err != nil {
		return neverPublished, fmt.Errorf("读取 Hook 写入前指令失败: %w", err)
	}
	if !bytes.Equal(current, original) {
		return neverPublished, fmt.Errorf("Hook 写入前指令已变化: %s", bytesToHex(current))
	}

	installErr := writer(patch)
	if installErr == nil {
		current, installErr = reader()
		if installErr == nil && bytes.Equal(current, patch) {
			return codeHookInstallResult{State: codeHookEntryInstalled}, nil
		}
		if installErr == nil {
			installErr = fmt.Errorf("Hook 写后回读不一致: %s", bytesToHex(current))
		} else {
			installErr = fmt.Errorf("Hook 写后回读失败: %w", installErr)
		}
	}

	restoreWriteErr := writer(original)
	restored, restoreReadErr := reader()
	if restoreReadErr == nil && bytes.Equal(restored, original) {
		if restoreWriteErr != nil {
			installErr = errors.Join(installErr, fmt.Errorf("Hook 恢复写入报告失败但原始入口已回读确认: %w", restoreWriteErr))
		}
		return codeHookInstallResult{State: codeHookEntryRestoredAfterPublishAttempt}, installErr
	}
	restoreErr := errors.Join(restoreWriteErr, restoreReadErr)
	if restoreReadErr == nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("Hook 恢复后回读不一致: %s", bytesToHex(restored)))
	}
	return codeHookInstallResult{State: codeHookEntryRecoveryRequired}, errors.Join(installErr, fmt.Errorf("Hook 原始指令无法确认恢复，代码洞必须保留: %w", restoreErr))
}

func installRemoteCodeHook(h windows.Handle, addr uintptr, original, patch []byte) (codeHookInstallResult, error) {
	writer := func(data []byte) error {
		return writeCodeMemory(h, addr, data)
	}
	reader := func() ([]byte, error) {
		data := make([]byte, len(original))
		if err := readProcessMemory(h, addr, unsafe.Pointer(&data[0]), uintptr(len(data))); err != nil {
			return nil, err
		}
		return data, nil
	}
	return installCodeHookAtomic(original, patch, writer, reader)
}
