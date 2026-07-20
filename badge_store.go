package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// 称号解锁：直接改存档 SlotData 的三个 Bool 向量，靠动态解析的 FlatBuffer 偏移定位，
// 不依赖易失效的 EXE AOB。称号 ID 即向量下标。仅写 5801/5816，绝不动 5814(奖励领取)。

//go:embed data/badges.json
var badgeNamesRaw []byte

const badgeVectorSize = 1700

type badgeName struct {
	ZH string `json:"zh"`
	EN string `json:"en"`
}

var badgeNames = loadBadgeNames()

// 有效称号 ID：badges.json 中存在名称的即有效（已排除游戏内无记录的空洞 ID）。
var badgeIDs = buildBadgeIDs()

func loadBadgeNames() map[int]badgeName {
	raw := map[string]badgeName{}
	if err := json.Unmarshal(badgeNamesRaw, &raw); err != nil {
		return nil
	}
	out := make(map[int]badgeName, len(raw))
	for k, v := range raw {
		var id int
		if _, err := fmt.Sscanf(k, "%d", &id); err == nil {
			out[id] = v
		}
	}
	return out
}

func buildBadgeIDs() []int {
	ids := make([]int, 0, len(badgeNames))
	for id := range badgeNames {
		ids = append(ids, id)
	}
	// 升序，便于列表稳定
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

// ── 对外类型 ──

type BadgeItem struct {
	ID       int    `json:"id"`
	NameZH   string `json:"nameZh"`
	NameEN   string `json:"nameEn"`
	Unlocked bool   `json:"unlocked"`
	Viewed   bool   `json:"viewed"`
}

type BadgeUnlockStatus struct {
	Total       int  `json:"total"`
	Unlocked    int  `json:"unlocked"`
	Viewed      int  `json:"viewed"`
	AllUnlocked bool `json:"allUnlocked"`
	AllViewed   bool `json:"allViewed"`
}

type BadgeUnlockResult struct {
	Status     BadgeUnlockStatus `json:"status"`
	Changed    int               `json:"changed"`
	BackupPath string            `json:"backupPath"`
}

type badgeSaveContext struct {
	save     *SaveData
	unlocked *unitEntry
	viewed   *unitEntry
	reward   *unitEntry
	mode     os.FileMode
}

// ── App 方法 ──

func (a *App) GetBadgeUnlockStatus(path string) (BadgeUnlockStatus, error) {
	ctx, err := loadBadgeSave(path)
	if err != nil {
		return BadgeUnlockStatus{}, err
	}
	return ctx.status(), nil
}

func (a *App) GetBadgeList(path string) ([]BadgeItem, error) {
	ctx, err := loadBadgeSave(path)
	if err != nil {
		return nil, err
	}
	items := make([]BadgeItem, 0, len(badgeIDs))
	for _, id := range badgeIDs {
		name := badgeNames[id]
		items = append(items, BadgeItem{
			ID:       id,
			NameZH:   name.ZH,
			NameEN:   name.EN,
			Unlocked: ctx.getBool(ctx.unlocked, id),
			Viewed:   ctx.getBool(ctx.viewed, id),
		})
	}
	return items, nil
}

// SetBadge 解锁或取消单个称号。unlocked=true 解锁，false 取消。
// markViewed 只在解锁时生效(同步标记已查看)；取消时不改已查看，避免误伤。
func (a *App) SetBadge(path string, id int, unlocked bool, markViewed bool) (BadgeUnlockResult, error) {
	if _, ok := badgeNames[id]; !ok {
		return BadgeUnlockResult{}, fmt.Errorf("无效的称号 ID: %d", id)
	}
	return runBadgeWrite(path, func(ctx *badgeSaveContext) int {
		changed := 0
		if ctx.setBool(ctx.unlocked, id, unlocked) {
			changed++
		}
		if unlocked && markViewed {
			if ctx.setBool(ctx.viewed, id, true) {
				changed++
			}
		}
		return changed
	})
}

// UnlockAllBadges 一键解锁全部有效称号。
func (a *App) UnlockAllBadges(path string, markViewed bool) (BadgeUnlockResult, error) {
	return runBadgeWrite(path, func(ctx *badgeSaveContext) int {
		changed := 0
		for _, id := range badgeIDs {
			if ctx.setBool(ctx.unlocked, id, true) {
				changed++
			}
			if markViewed && ctx.setBool(ctx.viewed, id, true) {
				changed++
			}
		}
		return changed
	})
}

// ── 写入流程（备份 + 原子写 + 写后验证 + 失败回滚 + 校验和重算）──

func runBadgeWrite(path string, mutate func(*badgeSaveContext) int) (BadgeUnlockResult, error) {
	ctx, err := loadBadgeSave(path)
	if err != nil {
		return BadgeUnlockResult{}, err
	}

	original := append([]byte(nil), ctx.save.data...)
	originalReward := ctx.vectorBytes(ctx.reward)

	changed := mutate(ctx)
	if changed == 0 {
		return BadgeUnlockResult{Status: ctx.status()}, nil
	}

	// 奖励领取向量必须原样不动
	if !bytes.Equal(ctx.vectorBytes(ctx.reward), originalReward) {
		return BadgeUnlockResult{}, fmt.Errorf("内部错误：奖励领取状态被意外修改，已中止")
	}
	if err := ctx.save.FixChecksums(); err != nil {
		return BadgeUnlockResult{}, fmt.Errorf("更新存档校验失败: %w", err)
	}
	if err := ensureFileUnchanged(path, original); err != nil {
		return BadgeUnlockResult{}, err
	}

	backupPath, err := createBadgeBackup(path, original, ctx.mode)
	if err != nil {
		return BadgeUnlockResult{}, err
	}
	if err := ensureFileUnchanged(path, original); err != nil {
		return BadgeUnlockResult{}, err
	}
	if err := replaceFileAtomically(path, ctx.save.data, ctx.mode); err != nil {
		return BadgeUnlockResult{}, fmt.Errorf("写入存档失败，原存档未改动，备份位于 %s: %w", backupPath, err)
	}

	// 写后重解析验证
	verified, err := loadBadgeSave(path)
	if err == nil {
		err = verifyBadgeWrite(verified, originalReward)
	}
	if err != nil {
		if restoreErr := replaceFileAtomically(path, original, ctx.mode); restoreErr != nil {
			return BadgeUnlockResult{}, fmt.Errorf("写后验证失败且自动恢复失败，请使用备份 %s: %v；恢复错误: %w", backupPath, err, restoreErr)
		}
		return BadgeUnlockResult{}, fmt.Errorf("写后验证失败，已自动恢复原存档，备份位于 %s: %w", backupPath, err)
	}

	return BadgeUnlockResult{Status: verified.status(), Changed: changed, BackupPath: backupPath}, nil
}

func loadBadgeSave(path string) (*badgeSaveContext, error) {
	if len(badgeNames) == 0 {
		return nil, fmt.Errorf("称号名称数据未加载")
	}
	if !strings.EqualFold(filepath.Ext(path), ".dat") {
		return nil, fmt.Errorf("请选择 .dat 存档文件")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("读取存档信息失败: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("存档路径必须是普通文件")
	}

	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}

	ctx := &badgeSaveContext{save: save, mode: info.Mode().Perm()}
	if ctx.unlocked, err = requireBadgeUnit(save, SaveID_BadgeUnlocked); err != nil {
		return nil, err
	}
	if ctx.viewed, err = requireBadgeUnit(save, SaveID_BadgeViewed); err != nil {
		return nil, err
	}
	if ctx.reward, err = requireBadgeUnit(save, SaveID_BadgeRewardClaimed); err != nil {
		return nil, err
	}
	for _, unit := range []*unitEntry{ctx.unlocked, ctx.viewed, ctx.reward} {
		if err := ctx.validateUnit(unit); err != nil {
			return nil, err
		}
	}
	return ctx, nil
}

func requireBadgeUnit(save *SaveData, idType uint32) (*unitEntry, error) {
	entry, ok := save.findUnit(idType, 0)
	if !ok {
		return nil, fmt.Errorf("存档缺少称号状态 %d，可能不是受支持的 2.0.2 存档", idType)
	}
	return entry, nil
}

func (ctx *badgeSaveContext) validateUnit(unit *unitEntry) error {
	if unit.ValueCnt != badgeVectorSize {
		return fmt.Errorf("称号状态 %d 长度异常: 期望 %d，实际 %d", unit.IDType, badgeVectorSize, unit.ValueCnt)
	}
	start, end, err := ctx.vectorRange(unit)
	if err != nil {
		return err
	}
	for i, value := range ctx.save.data[start:end] {
		if value > 1 {
			return fmt.Errorf("称号状态 %d 在索引 %d 存在非法布尔值 %d", unit.IDType, i, value)
		}
	}
	return nil
}

func (ctx *badgeSaveContext) vectorRange(unit *unitEntry) (int, int, error) {
	start := unit.ValueOff
	end := start + unit.ValueCnt
	slotStart := int(ctx.save.slotOff)
	slotEnd := int(ctx.save.slotOff + ctx.save.slotLen)
	if start < slotStart || end > slotEnd || end > len(ctx.save.data) {
		return 0, 0, fmt.Errorf("称号状态 %d 的数据偏移越界", unit.IDType)
	}
	return start, end, nil
}

func (ctx *badgeSaveContext) vectorBytes(unit *unitEntry) []byte {
	start, end, err := ctx.vectorRange(unit)
	if err != nil {
		return nil
	}
	return append([]byte(nil), ctx.save.data[start:end]...)
}

func (ctx *badgeSaveContext) getBool(unit *unitEntry, id int) bool {
	if id < 0 || id >= unit.ValueCnt {
		return false
	}
	return ctx.save.data[unit.ValueOff+id] == 1
}

// setBool 将称号 id 的标志设为 value，返回是否发生变化。
func (ctx *badgeSaveContext) setBool(unit *unitEntry, id int, value bool) bool {
	if id < 0 || id >= unit.ValueCnt {
		return false
	}
	want := byte(0)
	if value {
		want = 1
	}
	pos := unit.ValueOff + id
	if ctx.save.data[pos] == want {
		return false
	}
	ctx.save.data[pos] = want
	return true
}

func (ctx *badgeSaveContext) status() BadgeUnlockStatus {
	status := BadgeUnlockStatus{Total: len(badgeIDs)}
	for _, id := range badgeIDs {
		if ctx.getBool(ctx.unlocked, id) {
			status.Unlocked++
		}
		if ctx.getBool(ctx.viewed, id) {
			status.Viewed++
		}
	}
	status.AllUnlocked = status.Unlocked == status.Total
	status.AllViewed = status.Viewed == status.Total
	return status
}

// verifyBadgeWrite 只校验：奖励领取向量未变、所有布尔值合法。
// 不校验"全解锁"，因为单个取消解锁是合法结果。
func verifyBadgeWrite(ctx *badgeSaveContext, originalReward []byte) error {
	if !bytes.Equal(ctx.vectorBytes(ctx.reward), originalReward) {
		return fmt.Errorf("称号奖励领取状态发生了意外变化")
	}
	for _, unit := range []*unitEntry{ctx.unlocked, ctx.viewed} {
		start, end, err := ctx.vectorRange(unit)
		if err != nil {
			return err
		}
		for i, value := range ctx.save.data[start:end] {
			if value > 1 {
				return fmt.Errorf("称号状态 %d 在索引 %d 写后出现非法值 %d", unit.IDType, i, value)
			}
		}
	}
	return nil
}

// ── 文件安全操作 ──

func ensureFileUnchanged(path string, expected []byte) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("写入前重新读取存档失败: %w", err)
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("存档在操作期间被游戏或其他程序修改，已取消写入")
	}
	return nil
}

func createBadgeBackup(path string, data []byte, mode os.FileMode) (string, error) {
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(filepath.Base(path), ext)
	timestamp := time.Now().Format("20060102_150405")
	for sequence := 0; sequence < 1000; sequence++ {
		suffix := ""
		if sequence > 0 {
			suffix = fmt.Sprintf("_%03d", sequence)
		}
		// _BackUp 命名让备份不被存档槽扫描当成正式存档
		backupPath := filepath.Join(dir, fmt.Sprintf("%s_BackUp_Badges_%s%s%s", stem, timestamp, suffix, ext))
		file, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("创建存档备份失败: %w", err)
		}
		if _, err = file.Write(data); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(backupPath)
			return "", fmt.Errorf("写入存档备份失败: %w", err)
		}
		written, err := os.ReadFile(backupPath)
		if err != nil || !bytes.Equal(written, data) {
			return "", fmt.Errorf("存档备份验证失败: %s", backupPath)
		}
		return backupPath, nil
	}
	return "", fmt.Errorf("无法生成不重复的存档备份文件名")
}

func replaceFileAtomically(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".gbfr-save-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return windows.Rename(tempPath, path)
}
