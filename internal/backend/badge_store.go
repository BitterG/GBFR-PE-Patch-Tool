package backend

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	badgeVectorLength  = 1700
	badgeCatalogCount  = 1616
	badgeCatalogSource = "title-catalog-202"
	badgeCatalogSHA256 = "2aab04739254bebb26d48db9ec5f9cd804cda6a37a09654de2344cd41f4d833f"
)

//go:embed data/badges.json
var badgeCatalogJSON []byte

type badgeCatalogName struct {
	ZH string `json:"zh"`
	EN string `json:"en"`
}

type BadgeEntry struct {
	ID            int    `json:"id"`
	NameZH        string `json:"nameZh"`
	NameEN        string `json:"nameEn"`
	Unlocked      bool   `json:"unlocked"`
	RewardClaimed bool   `json:"rewardClaimed"`
	Viewed        bool   `json:"viewed"`
}

type BadgeState struct {
	Path               string       `json:"path"`
	Total              int          `json:"total"`
	UnlockedCount      int          `json:"unlockedCount"`
	RewardClaimedCount int          `json:"rewardClaimedCount"`
	ViewedCount        int          `json:"viewedCount"`
	CatalogSource      string       `json:"catalogSource"`
	CatalogSHA256      string       `json:"catalogSha256"`
	Badges             []BadgeEntry `json:"badges"`
}

type BadgeWriteResult struct {
	OutputPath string `json:"outputPath"`
	BackupPath string `json:"backupPath,omitempty"`
	Changed    int    `json:"changed"`
	Verified   int    `json:"verified"`
}

var (
	badgeCatalogOnce       sync.Once
	badgeCatalog           []BadgeEntry
	badgeCatalogErr        error
	badgeWriteMu           sync.Mutex
	badgeFindProcessByName = findProcessByName
)

func loadBadgeCatalog() ([]BadgeEntry, error) {
	badgeCatalogOnce.Do(func() {
		var raw map[string]badgeCatalogName
		if err := json.Unmarshal(badgeCatalogJSON, &raw); err != nil {
			badgeCatalogErr = fmt.Errorf("解析称号目录失败: %w", err)
			return
		}
		rows := make([]BadgeEntry, 0, len(raw))
		for key, name := range raw {
			id, err := strconv.Atoi(key)
			if err != nil || id < 0 || id >= badgeVectorLength {
				badgeCatalogErr = fmt.Errorf("称号目录 ID %q 超出 0..%d", key, badgeVectorLength-1)
				return
			}
			if strings.TrimSpace(name.ZH) == "" || strings.TrimSpace(name.EN) == "" {
				badgeCatalogErr = fmt.Errorf("称号 %d 缺少中英文名称", id)
				return
			}
			rows = append(rows, BadgeEntry{ID: id, NameZH: name.ZH, NameEN: name.EN})
		}
		if len(rows) != badgeCatalogCount {
			badgeCatalogErr = fmt.Errorf("称号目录数量 %d，期望 %d", len(rows), badgeCatalogCount)
			return
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		badgeCatalog = rows
	})
	if badgeCatalogErr != nil {
		return nil, badgeCatalogErr
	}
	return badgeCatalog, nil
}

func requireBadgeVector(save *SaveData, idType uint32, label string) (*unitEntry, error) {
	matches := make([]*unitEntry, 0, 1)
	for _, entry := range save.findAllUnitsByType(idType) {
		if entry.UnitID == 0 {
			matches = append(matches, entry)
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("存档%s向量 IDType=%d/UnitID=0 数量为 %d，期望 1", label, idType, len(matches))
	}
	entry := matches[0]
	values := entry.Bytes()
	if entry.ValueCnt != badgeVectorLength || len(values) != badgeVectorLength {
		return nil, fmt.Errorf("存档%s向量长度为 %d，期望 %d", label, entry.ValueCnt, badgeVectorLength)
	}
	for index, value := range values {
		if value != 0 && value != 1 {
			return nil, fmt.Errorf("存档%s向量第 %d 项值为 %d，不是布尔值", label, index, value)
		}
	}
	return entry, nil
}

func readBadgeVectors(save *SaveData) (unlocked, rewards, viewed *unitEntry, err error) {
	if unlocked, err = requireBadgeVector(save, SaveID_BadgeUnlocked, "称号解锁"); err != nil {
		return nil, nil, nil, err
	}
	if rewards, err = requireBadgeVector(save, SaveID_BadgeRewardClaimed, "称号奖励"); err != nil {
		return nil, nil, nil, err
	}
	if viewed, err = requireBadgeVector(save, SaveID_BadgeViewed, "称号已查看"); err != nil {
		return nil, nil, nil, err
	}
	return unlocked, rewards, viewed, nil
}

func (a *App) LoadBadgeState(path string) (*BadgeState, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("存档路径不能为空")
	}
	catalog, err := loadBadgeCatalog()
	if err != nil {
		return nil, err
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	unlocked, rewards, viewed, err := readBadgeVectors(save)
	if err != nil {
		return nil, err
	}
	unlockValues, rewardValues, viewedValues := unlocked.Bytes(), rewards.Bytes(), viewed.Bytes()
	state := &BadgeState{
		Path: path, Total: len(catalog), CatalogSource: badgeCatalogSource,
		CatalogSHA256: badgeCatalogSHA256, Badges: make([]BadgeEntry, len(catalog)),
	}
	for index, row := range catalog {
		row.Unlocked = unlockValues[row.ID] == 1
		row.RewardClaimed = rewardValues[row.ID] == 1
		row.Viewed = viewedValues[row.ID] == 1
		if row.Unlocked {
			state.UnlockedCount++
		}
		if row.RewardClaimed {
			state.RewardClaimedCount++
		}
		if row.Viewed {
			state.ViewedCount++
		}
		state.Badges[index] = row
	}
	return state, nil
}

func (a *App) SetBadgeState(path string, id int, unlocked, markViewed bool) (*BadgeWriteResult, error) {
	return a.writeBadgeStates(path, map[int]bool{id: unlocked}, markViewed)
}

func (a *App) SetAllBadgeStates(path string, unlocked, markViewed bool) (*BadgeWriteResult, error) {
	catalog, err := loadBadgeCatalog()
	if err != nil {
		return nil, err
	}
	changes := make(map[int]bool, len(catalog))
	for _, badge := range catalog {
		changes[badge.ID] = unlocked
	}
	return a.writeBadgeStates(path, changes, markViewed)
}

func (a *App) writeBadgeStates(path string, changes map[int]bool, markViewed bool) (*BadgeWriteResult, error) {
	badgeWriteMu.Lock()
	defer badgeWriteMu.Unlock()
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()

	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("存档路径不能为空")
	}
	if _, err := badgeFindProcessByName(charaProcessName); err == nil {
		return nil, fmt.Errorf("写入存档前请先完全退出游戏，避免游戏把旧数据写回")
	}
	catalog, err := loadBadgeCatalog()
	if err != nil {
		return nil, err
	}
	known := make(map[int]bool, len(catalog))
	for _, badge := range catalog {
		known[badge.ID] = true
	}
	for id := range changes {
		if !known[id] {
			return nil, fmt.Errorf("称号 ID %d 不在锁定目录中", id)
		}
	}

	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	originalFile := append([]byte(nil), save.data...)
	unlockedEntry, rewardEntry, viewedEntry, err := readBadgeVectors(save)
	if err != nil {
		return nil, err
	}
	expectedUnlocked := append([]byte(nil), unlockedEntry.Bytes()...)
	expectedViewed := append([]byte(nil), viewedEntry.Bytes()...)
	rewardBefore := append([]byte(nil), rewardEntry.Bytes()...)
	for id, value := range changes {
		if value {
			expectedUnlocked[id] = 1
		} else {
			expectedUnlocked[id] = 0
		}
		if markViewed {
			expectedViewed[id] = 1
		}
	}
	copy(unlockedEntry.Bytes(), expectedUnlocked)
	copy(viewedEntry.Bytes(), expectedViewed)
	if err := save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("修复存档校验和失败: %w", err)
	}
	currentFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("写入前重读存档失败: %w", err)
	}
	if !bytes.Equal(originalFile, currentFile) {
		return nil, fmt.Errorf("存档在称号编辑期间已被其他程序修改，请刷新后重试")
	}
	if err := save.Write(path); err != nil {
		return nil, fmt.Errorf("写入称号记录失败: %w", err)
	}
	result := &BadgeWriteResult{OutputPath: path, BackupPath: save.LastBackupPath(), Changed: len(changes)}

	verifiedSave, err := LoadSave(path)
	if err != nil {
		return result, fmt.Errorf("称号写后回读失败（备份：%s）: %w", result.BackupPath, err)
	}
	verifiedUnlocked, verifiedRewards, verifiedViewed, err := readBadgeVectors(verifiedSave)
	if err != nil {
		return result, fmt.Errorf("称号写后结构验证失败（备份：%s）: %w", result.BackupPath, err)
	}
	if !bytes.Equal(verifiedUnlocked.Bytes(), expectedUnlocked) {
		return result, fmt.Errorf("称号解锁向量回读与请求不符（备份：%s）", result.BackupPath)
	}
	if !bytes.Equal(verifiedViewed.Bytes(), expectedViewed) {
		return result, fmt.Errorf("称号已查看向量回读与请求不符（备份：%s）", result.BackupPath)
	}
	if !bytes.Equal(verifiedRewards.Bytes(), rewardBefore) {
		return result, fmt.Errorf("称号奖励领取向量发生非预期变化（备份：%s）", result.BackupPath)
	}
	result.Verified = len(changes)
	return result, nil
}
