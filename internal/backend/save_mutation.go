package backend

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
)

// offlineSaveMutationMu covers the full read/validate/modify/write/readback
// lifecycle. SaveData.Write is atomic on disk, but locking only the final
// replace would still allow two editors to derive changes from the same stale
// snapshot and let the later replace discard the earlier one.
var offlineSaveMutationMu sync.Mutex

type failedGeneratorCommit struct {
	outputPath string
	written    []byte
}

func ensureOfflineSaveSnapshotCurrent(path string, expected []byte) error {
	path = strings.TrimSpace(path)
	if path == "" || len(expected) == 0 {
		return nil
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("refresh save snapshot before write: %w", err)
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("save changed after it was loaded; refresh this page and retry")
	}
	return nil
}

func allowExactFailedCommitRetry(sourcePath, outputPath string, retry *failedGeneratorCommit) (bool, error) {
	if retry == nil || !samePath(strings.TrimSpace(outputPath), strings.TrimSpace(retry.outputPath)) {
		return false, nil
	}
	current, err := os.ReadFile(strings.TrimSpace(outputPath))
	if err != nil {
		return false, fmt.Errorf("read failed-commit target before retry: %w", err)
	}
	if !bytes.Equal(current, retry.written) {
		return false, fmt.Errorf("save changed after the failed readback; refresh before retrying")
	}
	// Only an in-place retry may skip the source snapshot check. A separate
	// output has now been proven unchanged, but the source must still match the
	// generator's loaded snapshot before deriving the next write.
	return samePath(strings.TrimSpace(sourcePath), strings.TrimSpace(outputPath)), nil
}
