package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	saveSnapshotMetadata  = "snapshot.json"
	saveSnapshotRetention = 10
)

var errSaveRestoreGameRunning = errors.New("恢复存档前请先完全退出游戏")

var saveSnapshotMu sync.Mutex

// SaveBackupSlot describes one real GBFR save contained in a group snapshot.
// Only SaveData1.dat through SaveData3.dat are accepted.
type SaveBackupSlot struct {
	Slot     int    `json:"slot"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// SaveSnapshot is a point-in-time backup of every existing GBFR save slot.
type SaveSnapshot struct {
	ID          string           `json:"id"`
	CreatedAt   string           `json:"createdAt"`
	DisplayTime string           `json:"displayTime"`
	Reason      string           `json:"reason"`
	SaveDir     string           `json:"saveDir"`
	TotalSize   int64            `json:"totalSize"`
	Slots       []SaveBackupSlot `json:"slots"`
}

type SaveRestoreResult struct {
	Snapshot         SaveSnapshot `json:"snapshot"`
	SafetySnapshotID string       `json:"safetySnapshotId"`
	Restored         int          `json:"restored"`
}

func defaultSaveGamesDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "GBFR", "Saved", "SaveGames")
}

func saveSnapshotRoot() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "gbfr-player-info-edit", "save-backups"), nil
}

func managedSaveSlot(path string) (int, bool) {
	name := strings.ToLower(filepath.Base(path))
	for slot := 1; slot <= 3; slot++ {
		if name == strings.ToLower(fmt.Sprintf("SaveData%d.dat", slot)) {
			return slot, true
		}
	}
	return 0, false
}

func existingManagedSaveSlots(saveDir string) ([]SaveBackupSlot, error) {
	slots := make([]SaveBackupSlot, 0, 3)
	for slot := 1; slot <= 3; slot++ {
		name := fmt.Sprintf("SaveData%d.dat", slot)
		path := filepath.Join(saveDir, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("检查%s失败: %w", name, err)
		}
		if info.IsDir() {
			continue
		}
		slots = append(slots, SaveBackupSlot{Slot: slot, FileName: name, Size: info.Size()})
	}
	return slots, nil
}

func sanitiseSnapshotReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "手动备份"
	}
	runes := []rune(reason)
	if len(runes) > 80 {
		reason = string(runes[:80])
	}
	return reason
}

func copyFileWithSHA256(source, destination string) (int64, string, error) {
	in, err := os.Open(source)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, "", err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hash), in)
	if err != nil {
		return 0, "", err
	}
	if err := out.Sync(); err != nil {
		return 0, "", err
	}
	if err := out.Close(); err != nil {
		return 0, "", err
	}
	ok = true
	return size, strings.ToUpper(hex.EncodeToString(hash.Sum(nil))), nil
}

func createSaveSnapshotLocked(saveDir, reason string, allowEmpty bool) (SaveSnapshot, error) {
	absDir, err := filepath.Abs(saveDir)
	if err != nil {
		return SaveSnapshot{}, err
	}
	absDir = filepath.Clean(absDir)
	slots, err := existingManagedSaveSlots(absDir)
	if err != nil {
		return SaveSnapshot{}, err
	}
	if len(slots) == 0 {
		if allowEmpty {
			return SaveSnapshot{}, nil
		}
		return SaveSnapshot{}, fmt.Errorf("未找到SaveData1.dat、SaveData2.dat或SaveData3.dat")
	}
	root, err := saveSnapshotRoot()
	if err != nil {
		return SaveSnapshot{}, fmt.Errorf("定位备份目录失败: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SaveSnapshot{}, fmt.Errorf("创建备份目录失败: %w", err)
	}

	now := time.Now()
	baseID := now.Format("20060102-150405.000000000")
	id := baseID
	for attempt := 1; ; attempt++ {
		if _, err := os.Stat(filepath.Join(root, id)); os.IsNotExist(err) {
			break
		}
		id = baseID + "-" + strconv.Itoa(attempt)
	}
	tmpDir, err := os.MkdirTemp(root, ".snapshot-*")
	if err != nil {
		return SaveSnapshot{}, fmt.Errorf("创建临时备份目录失败: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	snapshot := SaveSnapshot{
		ID:          id,
		CreatedAt:   now.Format(time.RFC3339Nano),
		DisplayTime: now.Format("2006-01-02 15:04:05"),
		Reason:      sanitiseSnapshotReason(reason),
		SaveDir:     absDir,
		Slots:       slots,
	}
	for index := range snapshot.Slots {
		slot := &snapshot.Slots[index]
		size, checksum, err := copyFileWithSHA256(filepath.Join(absDir, slot.FileName), filepath.Join(tmpDir, slot.FileName))
		if err != nil {
			return SaveSnapshot{}, fmt.Errorf("备份%s失败: %w", slot.FileName, err)
		}
		slot.Size = size
		slot.SHA256 = checksum
		snapshot.TotalSize += size
	}
	metadata, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return SaveSnapshot{}, err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, saveSnapshotMetadata), metadata, 0o644); err != nil {
		return SaveSnapshot{}, fmt.Errorf("写入备份索引失败: %w", err)
	}
	if err := os.Rename(tmpDir, filepath.Join(root, id)); err != nil {
		return SaveSnapshot{}, fmt.Errorf("提交备份失败: %w", err)
	}
	committed = true
	if err := pruneSaveSnapshotsLocked(root, saveSnapshotRetention); err != nil {
		return SaveSnapshot{}, fmt.Errorf("备份已创建，但清理旧备份失败: %w", err)
	}
	return snapshot, nil
}

func pruneSaveSnapshotsLocked(root string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("备份保留数量必须至少为 1")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	type managedSnapshot struct {
		id        string
		createdAt string
	}
	managed := make([]managedSnapshot, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		snapshot, _, loadErr := loadSaveSnapshot(entry.Name())
		if loadErr == nil {
			managed = append(managed, managedSnapshot{id: entry.Name(), createdAt: snapshot.CreatedAt})
		}
	}
	sort.Slice(managed, func(i, j int) bool {
		if managed[i].createdAt == managed[j].createdAt {
			return managed[i].id > managed[j].id
		}
		return managed[i].createdAt > managed[j].createdAt
	})
	if len(managed) <= keep {
		return nil
	}
	for _, snapshot := range managed[keep:] {
		target := filepath.Join(root, snapshot.id)
		if filepath.Dir(target) != filepath.Clean(root) {
			return fmt.Errorf("拒绝清理备份根目录之外的路径")
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	return nil
}

func loadSaveSnapshot(id string) (SaveSnapshot, string, error) {
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return SaveSnapshot{}, "", fmt.Errorf("备份ID无效")
	}
	root, err := saveSnapshotRoot()
	if err != nil {
		return SaveSnapshot{}, "", err
	}
	dir := filepath.Join(root, id)
	data, err := os.ReadFile(filepath.Join(dir, saveSnapshotMetadata))
	if err != nil {
		return SaveSnapshot{}, "", fmt.Errorf("读取备份索引失败: %w", err)
	}
	var snapshot SaveSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return SaveSnapshot{}, "", fmt.Errorf("解析备份索引失败: %w", err)
	}
	if snapshot.ID != id || len(snapshot.Slots) == 0 {
		return SaveSnapshot{}, "", fmt.Errorf("备份索引不完整")
	}
	return snapshot, dir, nil
}

func verifySnapshotFiles(snapshot SaveSnapshot, snapshotDir string) error {
	seen := map[int]bool{}
	for _, slot := range snapshot.Slots {
		expectedName := fmt.Sprintf("SaveData%d.dat", slot.Slot)
		if slot.Slot < 1 || slot.Slot > 3 || !strings.EqualFold(slot.FileName, expectedName) || seen[slot.Slot] {
			return fmt.Errorf("备份槽位索引无效")
		}
		seen[slot.Slot] = true
		path := filepath.Join(snapshotDir, slot.FileName)
		info, checksum, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("校验%s失败: %w", slot.FileName, err)
		}
		if info != slot.Size || !strings.EqualFold(checksum, slot.SHA256) {
			return fmt.Errorf("%s校验不一致，已拒绝恢复", slot.FileName)
		}
	}
	return nil
}

func hashFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, strings.ToUpper(hex.EncodeToString(hash.Sum(nil))), nil
}

func restoreSnapshotFilesLocked(snapshot SaveSnapshot, snapshotDir string, beforeReplace ...func() error) error {
	if err := os.MkdirAll(snapshot.SaveDir, 0o755); err != nil {
		return fmt.Errorf("创建存档目录失败: %w", err)
	}
	type stagedFile struct {
		tmp  string
		dest string
	}
	staged := make([]stagedFile, 0, len(snapshot.Slots))
	defer func() {
		for _, file := range staged {
			_ = os.Remove(file.tmp)
		}
	}()
	for _, slot := range snapshot.Slots {
		source := filepath.Join(snapshotDir, slot.FileName)
		tmp, err := os.CreateTemp(snapshot.SaveDir, ".restore-*.tmp")
		if err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return err
		}
		_, copyErr := io.Copy(tmp, input)
		closeInputErr := input.Close()
		syncErr := tmp.Sync()
		closeErr := tmp.Close()
		if copyErr != nil || closeInputErr != nil || syncErr != nil || closeErr != nil {
			_ = os.Remove(tmp.Name())
			return fmt.Errorf("暂存%s失败", slot.FileName)
		}
		staged = append(staged, stagedFile{tmp: tmp.Name(), dest: filepath.Join(snapshot.SaveDir, slot.FileName)})
	}
	for _, check := range beforeReplace {
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
	}
	for _, file := range staged {
		if err := replaceFileAtomic(file.tmp, file.dest); err != nil {
			return err
		}
	}
	return nil
}

// CreateSaveSnapshot manually snapshots all currently existing SaveData1-3 files.
func (a *App) CreateSaveSnapshot(reason string) (SaveSnapshot, error) {
	saveSnapshotMu.Lock()
	defer saveSnapshotMu.Unlock()
	return createSaveSnapshotLocked(defaultSaveGamesDir(), reason, false)
}

// ListSaveSnapshots returns the recovery timeline, newest first.
func (a *App) ListSaveSnapshots() ([]SaveSnapshot, error) {
	saveSnapshotMu.Lock()
	defer saveSnapshotMu.Unlock()
	root, err := saveSnapshotRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []SaveSnapshot{}, nil
		}
		return nil, err
	}
	snapshots := make([]SaveSnapshot, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		snapshot, _, err := loadSaveSnapshot(entry.Name())
		if err == nil {
			snapshots = append(snapshots, snapshot)
		}
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].CreatedAt > snapshots[j].CreatedAt })
	return snapshots, nil
}

// RestoreSaveSnapshot verifies a snapshot, saves the current slots as a new
// safety point, then restores every slot contained in the selected snapshot.
var saveRestoreFindProcessByName = findProcessByName

func (a *App) RestoreSaveSnapshot(id string) (SaveRestoreResult, error) {
	if _, err := saveRestoreFindProcessByName(charaProcessName); err == nil {
		return SaveRestoreResult{}, errSaveRestoreGameRunning
	}
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()
	saveSnapshotMu.Lock()
	defer saveSnapshotMu.Unlock()
	snapshot, snapshotDir, err := loadSaveSnapshot(id)
	if err != nil {
		return SaveRestoreResult{}, err
	}
	if err := verifySnapshotFiles(snapshot, snapshotDir); err != nil {
		return SaveRestoreResult{}, err
	}
	safety, err := createSaveSnapshotLocked(snapshot.SaveDir, "恢复前自动备份", true)
	if err != nil {
		return SaveRestoreResult{}, fmt.Errorf("恢复前安全备份失败: %w", err)
	}
	recheckGame := func() error {
		if _, findErr := saveRestoreFindProcessByName(charaProcessName); findErr == nil {
			return errSaveRestoreGameRunning
		}
		return nil
	}
	if err := restoreSnapshotFilesLocked(snapshot, snapshotDir, recheckGame); err != nil {
		if safety.ID != "" && !errors.Is(err, errSaveRestoreGameRunning) {
			if rollback, rollbackDir, loadErr := loadSaveSnapshot(safety.ID); loadErr == nil {
				_ = restoreSnapshotFilesLocked(rollback, rollbackDir)
			}
		}
		return SaveRestoreResult{}, fmt.Errorf("恢复存档失败，已尝试回滚: %w", err)
	}
	return SaveRestoreResult{Snapshot: snapshot, SafetySnapshotID: safety.ID, Restored: len(snapshot.Slots)}, nil
}

// autoSnapshotBeforeSaveWrite is the non-bypassable gate used by SaveData.Write.
func autoSnapshotBeforeSaveWrite(path string) (SaveSnapshot, error) {
	if _, ok := managedSaveSlot(path); !ok {
		return SaveSnapshot{}, nil
	}
	saveSnapshotMu.Lock()
	defer saveSnapshotMu.Unlock()
	return createSaveSnapshotLocked(filepath.Dir(path), "写入前自动备份 · "+filepath.Base(path), true)
}

// snapshotBeforeLiveSaveChange protects the last durable on-disk saves before
// a live-memory action that the game may persist later.
func snapshotBeforeLiveSaveChange(reason string) error {
	saveSnapshotMu.Lock()
	defer saveSnapshotMu.Unlock()
	_, err := createSaveSnapshotLocked(defaultSaveGamesDir(), reason, false)
	return err
}
