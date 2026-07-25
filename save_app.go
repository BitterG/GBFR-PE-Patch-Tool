package main

import (
	_ "embed"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed internal/backend/data/quest_names_i18n.csv
var questCSVData []byte

// ── Exported types for Wails binding ──

type SaveSummary struct {
	FilePath         string  `json:"filePath"`
	FileName         string  `json:"fileName"`
	Rupees           int32   `json:"rupees"`
	MasteryPoints    int32   `json:"masteryPoints"`
	Commendations    int32   `json:"commendations"`
	StageID          uint32  `json:"stageId"`
	PartyHealth      []int32 `json:"partyHealth"`
	FavoriteChara    uint32  `json:"favoriteChara"`
	ItemCount        int     `json:"itemCount"`
	WeaponCount      int     `json:"weaponCount"`
	GemCount         int     `json:"gemCount"`
	QuestClears      int     `json:"questClears"`
	QuestTotalClears uint32  `json:"questTotalClears"`
	Unlocks          int     `json:"unlocks"`
}

type QuestEntry struct {
	QuestID     uint32 `json:"questId"`
	QuestName   string `json:"questName"`
	QuestNameCN string `json:"questNameCn"`
	Clears      uint32 `json:"clears"`
}

type CharacterStat struct {
	Slot  uint32 `json:"slot"`
	Name  string `json:"name"`
	Count int32  `json:"count"`
}

type SaveSlot struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
	Name  string `json:"name"`
}

type SaveCounters struct {
	Likes      uint32 `json:"likes"`
	Challenges uint32 `json:"challenges"`
}

// ── Quest name mapping ──

var questNames map[int]string
var questNamesCN map[int]string

func init() {
	questNames = make(map[int]string)
	questNamesCN = make(map[int]string)
	r := csv.NewReader(strings.NewReader(string(questCSVData)))
	records, err := r.ReadAll()
	if err != nil {
		return
	}
	for _, row := range records[1:] { // skip header
		if len(row) >= 2 {
			id, err := strconv.Atoi(row[0])
			if err != nil {
				parsed, parseErr := strconv.ParseUint(row[0], 16, 32)
				if parseErr != nil {
					continue
				}
				id = int(parsed)
			}
			questNames[id] = row[1]
			if len(row) >= 3 && row[2] != "" {
				questNamesCN[id] = row[2]
			}
		}
	}
}

func questIDToName(stored uint32) string {
	if name, ok := questNames[int(stored)]; ok {
		return name
	}
	return fmt.Sprintf("Unknown_%06X", stored)
}

func questIDToNameCN(stored uint32) string {
	return questNamesCN[int(stored)]
}

func storedToQuestID(stored uint32) uint32 {
	return stored
}

// ── App save methods (bound to Wails) ──

// FindSaveFiles scans the default GBFR save directory
func (a *App) FindSaveFiles() []SaveSlot {
	gbfrFolder := filepath.Join(os.Getenv("LOCALAPPDATA"), "GBFR", "Saved", "SaveGames")
	entries, err := os.ReadDir(gbfrFolder)
	if err != nil {
		return nil
	}

	var slots []SaveSlot
	idx := 1
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "SaveData") && strings.HasSuffix(name, ".dat") && !strings.Contains(name, "_BackUp") {
			slots = append(slots, SaveSlot{
				Index: idx,
				Path:  filepath.Join(gbfrFolder, name),
				Name:  name,
			})
			idx++
		}
	}
	return slots
}

// LoadSave loads and parses a save file, returning a summary
func (a *App) LoadSave(path string) (*SaveSummary, error) {
	save, err := LoadSaveFile(path)
	if err != nil {
		return nil, fmt.Errorf("解析存档失败: %w", err)
	}
	if save.SlotData == nil {
		return nil, fmt.Errorf("存档SlotData为空")
	}

	s := &SaveSummary{
		FilePath: path,
		FileName: filepath.Base(path),
	}

	// Rupees (int)
	if unit := save.SlotData.GetIntUnit(SaveID_Rupees); unit != nil && len(unit.ValueData) > 0 {
		s.Rupees = unit.ValueData[0]
	}
	// Mastery Points (int)
	if unit := save.SlotData.GetIntUnit(SaveID_MasteryPoints); unit != nil && len(unit.ValueData) > 0 {
		s.MasteryPoints = unit.ValueData[0]
	}
	// Commendations (int)
	if unit := save.SlotData.GetIntUnit(SaveID_Commendations); unit != nil && len(unit.ValueData) > 0 {
		s.Commendations = unit.ValueData[0]
	}
	// Stage ID (uint)
	if unit := save.SlotData.GetUIntUnit(SaveID_CurrentStageID); unit != nil && len(unit.ValueData) > 0 {
		s.StageID = unit.ValueData[0]
	}
	// Party Health (int)
	if unit := save.SlotData.GetIntUnit(SaveID_PartyHealth); unit != nil {
		s.PartyHealth = unit.ValueData
	}
	// Favorite Character (int)
	if unit := save.SlotData.GetIntUnit(SaveID_FavoriteChara); unit != nil && len(unit.ValueData) > 0 {
		s.FavoriteChara = uint32(unit.ValueData[0])
	}

	// Count items
	for _, u := range save.SlotData.UIntTable {
		switch u.IDType {
		case SaveID_ItemID:
			s.ItemCount += len(u.ValueData)
		case SaveID_WeaponID:
			s.WeaponCount += len(u.ValueData)
		case SaveID_GemID:
			s.GemCount += len(u.ValueData)
		}
	}

	// Quest stats
	qIDs := save.SlotData.GetUIntUnit(SaveID_QuestIDs)
	qCounts := save.SlotData.GetUIntUnit(SaveID_QuestCompleteCount)
	if qIDs != nil && qCounts != nil {
		for i := 0; i < len(qIDs.ValueData) && i < len(qCounts.ValueData); i++ {
			if qCounts.ValueData[i] > 0 {
				s.QuestClears++
				s.QuestTotalClears += qCounts.ValueData[i]
			}
		}
	}

	// Unlocks
	if unit := save.SlotData.GetBoolUnit(SaveID_IsUnlocked); unit != nil {
		for _, v := range unit.ValueData {
			if v {
				s.Unlocks++
			}
		}
	}

	return s, nil
}

// GetSaveCounters returns counters stored in the save file.
func (a *App) GetSaveCounters(path string) (*SaveCounters, error) {
	save, err := LoadSaveFile(path)
	if err != nil {
		return nil, err
	}
	if save.SlotData == nil {
		return nil, fmt.Errorf("存档SlotData为空")
	}

	counters := &SaveCounters{}
	if unit := save.SlotData.GetIntUnit(SaveID_Commendations); unit != nil && len(unit.ValueData) > 0 && unit.ValueData[0] >= 0 {
		counters.Likes = uint32(unit.ValueData[0])
	}
	if unit := save.SlotData.GetUIntUnit(SaveID_QuestCompleteCount); unit != nil {
		for _, count := range unit.ValueData {
			counters.Challenges += count
		}
	}
	return counters, nil
}

// UpdateSaveCounters writes counters in place, preserving FlatBuffers offsets.
func (a *App) UpdateSaveCounters(path string, likes, challenges uint32) (*SaveCounters, error) {
	if likes > uint32(^uint32(0)>>1) {
		return nil, fmt.Errorf("点赞数不能超过 %d", uint32(^uint32(0)>>1))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取存档失败: %w", err)
	}
	original := append([]byte(nil), raw...)
	if len(raw) < 52 {
		return nil, fmt.Errorf("文件太小，不是有效的存档")
	}
	slotOffset := int(binary.LittleEndian.Uint64(raw[28:36]))
	slotSize := int(binary.LittleEndian.Uint64(raw[44:52]))
	if slotOffset < 0 || slotSize <= 0 || slotOffset+slotSize > len(raw) {
		return nil, fmt.Errorf("存档SlotData范围无效")
	}
	slot := raw[slotOffset : slotOffset+slotSize]

	likesOffsets, err := flatBufferUIntValueOffsets(slot, 6, SaveID_Commendations, 0)
	if err != nil {
		return nil, err
	}
	profileLikesOffsets, err := flatBufferUIntValueOffsets(slot, 6, 4703, 10600)
	if err != nil {
		return nil, err
	}
	questOffsets, err := flatBufferUIntValueOffsets(slot, 7, SaveID_QuestCompleteCount, 0)
	if err != nil {
		return nil, err
	}
	targetQuestOffset, err := questCounterOffset(slot, 401303)
	if err != nil {
		return nil, err
	}
	profileChallengeOffsets, err := flatBufferUIntValueOffsets(slot, 6, 4901, 10600)
	if err != nil {
		return nil, err
	}
	if len(likesOffsets) == 0 || len(profileLikesOffsets) == 0 || len(questOffsets) == 0 || len(profileChallengeOffsets) == 0 {
		return nil, fmt.Errorf("存档计数字段为空")
	}

	binary.LittleEndian.PutUint32(slot[likesOffsets[0]:], likes)
	binary.LittleEndian.PutUint32(slot[profileLikesOffsets[0]:], likes)
	currentChallenges := uint64(0)
	for _, offset := range questOffsets {
		currentChallenges += uint64(binary.LittleEndian.Uint32(slot[offset:]))
	}
	if uint64(challenges) >= currentChallenges {
		increase := uint64(challenges) - currentChallenges
		current := binary.LittleEndian.Uint32(slot[targetQuestOffset:])
		if uint64(^uint32(0)-current) < increase {
			return nil, fmt.Errorf("目标挑战次数过大")
		}
		binary.LittleEndian.PutUint32(slot[targetQuestOffset:], current+uint32(increase))
	} else {
		if challenges == 0 {
			return nil, fmt.Errorf("挑战次数不能低于 1：担心爸爸至少保留 1 次")
		}
		decrease := currentChallenges - uint64(challenges)
		targetCount := binary.LittleEndian.Uint32(slot[targetQuestOffset:])
		if targetCount > 1 && uint64(targetCount-1) >= decrease {
			binary.LittleEndian.PutUint32(slot[targetQuestOffset:], targetCount-uint32(decrease))
		} else {
			binary.LittleEndian.PutUint32(slot[targetQuestOffset:], 1)
			if err := proportionallyReduceQuestCounts(slot, questOffsets, targetQuestOffset, challenges-1); err != nil {
				return nil, err
			}
		}
	}
	binary.LittleEndian.PutUint32(slot[profileChallengeOffsets[0]:], challenges)
	if err := fixSlotDataHash(slot); err != nil {
		return nil, err
	}

	backupPath := path + ".counters." + time.Now().Format("20060102_150405") + ".bak"
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		return nil, fmt.Errorf("创建备份失败: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, fmt.Errorf("写入存档失败: %w", err)
	}
	return a.GetSaveCounters(path)
}

func proportionallyReduceQuestCounts(slot []byte, questOffsets []int, targetQuestOffset int, targetTotal uint32) error {
	type questCount struct {
		offset int
		count  uint64
	}

	active := make([]questCount, 0, len(questOffsets))
	excessTotal := uint64(0)
	for _, offset := range questOffsets {
		if offset == targetQuestOffset {
			continue
		}
		count := uint64(binary.LittleEndian.Uint32(slot[offset:]))
		if count == 0 {
			continue
		}
		active = append(active, questCount{offset: offset, count: count})
		excessTotal += count - 1
	}
	if uint64(targetTotal) < uint64(len(active)) {
		return fmt.Errorf("目标挑战次数不能低于已有通关副本数量 %d", len(active))
	}
	if len(active) == 0 {
		if targetTotal != 0 {
			return fmt.Errorf("没有可扣减的副本次数")
		}
		return nil
	}

	type remainder struct {
		offset int
		value  uint64
	}
	remaining := uint64(targetTotal) - uint64(len(active))
	remainders := make([]remainder, 0, len(active))
	assigned := uint64(0)
	for _, entry := range active {
		value := uint64(0)
		fraction := uint64(0)
		if excessTotal > 0 {
			scaled := (entry.count - 1) * remaining
			value = scaled / excessTotal
			fraction = scaled % excessTotal
		}
		binary.LittleEndian.PutUint32(slot[entry.offset:], uint32(value+1))
		assigned += value
		remainders = append(remainders, remainder{offset: entry.offset, value: fraction})
	}

	// Largest remainders receive rounding units first; vector order breaks ties.
	sort.SliceStable(remainders, func(i, j int) bool { return remainders[i].value > remainders[j].value })
	for i := uint64(0); i < remaining-assigned; i++ {
		entry := remainders[i%uint64(len(remainders))]
		current := binary.LittleEndian.Uint32(slot[entry.offset:])
		binary.LittleEndian.PutUint32(slot[entry.offset:], current+1)
	}
	return nil
}

func questCounterOffset(slot []byte, questID uint32) (int, error) {
	questIDs, err := flatBufferUIntValueOffsets(slot, 7, SaveID_QuestIDs, 0)
	if err != nil {
		return 0, err
	}
	questCounts, err := flatBufferUIntValueOffsets(slot, 7, SaveID_QuestCompleteCount, 0)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(questIDs) && i < len(questCounts); i++ {
		if storedToQuestID(binary.LittleEndian.Uint32(slot[questIDs[i]:])) == questID {
			return questCounts[i], nil
		}
	}
	return 0, fmt.Errorf("存档中未找到任务 %d", questID)
}

func fixSlotDataHash(slot []byte) error {
	if len(slot) < 0x14 {
		return fmt.Errorf("SlotData太小")
	}
	hashesOffset := int(binary.LittleEndian.Uint32(slot[len(slot)-0x14:]))
	if hashesOffset < 0 || hashesOffset+len(hashSectionInfos)*8 > len(slot) {
		return fmt.Errorf("存档哈希区无效")
	}
	seedOffsets, err := flatBufferUIntValueOffsets(slot, 7, SaveID_HashSeed, 0)
	if err != nil || len(seedOffsets) == 0 {
		return fmt.Errorf("读取存档哈希种子失败: %w", err)
	}
	index := int(binary.LittleEndian.Uint32(slot[seedOffsets[0]:]) % uint32(len(hashSectionInfos)))
	section := hashSectionInfos[index]
	end := hashesOffset - section.SubSize
	if end < section.StartOffset {
		return fmt.Errorf("存档哈希范围无效")
	}
	binary.LittleEndian.PutUint64(slot[hashesOffset+index*8:], xxHash64(slot[section.StartOffset:end], XXHash64SaveSeed))
	return nil
}

// GetCharacterStats reads character-use counters from save character slots.
func (a *App) GetCharacterStats(path string, newSave bool) ([]CharacterStat, error) {
	save, err := LoadSaveFile(path)
	if err != nil {
		return nil, err
	}
	if save.SlotData == nil {
		return nil, fmt.Errorf("存档SlotData为空")
	}
	return characterStatsForSave(save.SlotData, newSave), nil
}

func (a *App) UpdateCharacterStat(path string, newSave bool, slot, count uint32) error {
	if count > uint32(^uint32(0)>>1) {
		return fmt.Errorf("角色次数不能超过 %d", uint32(^uint32(0)>>1))
	}
	characterNames := characterNamesForSave(newSave)
	if int(slot) >= len(characterNames) || characterNames[slot] == "" {
		return fmt.Errorf("角色槽位与当前存档版本不匹配")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取存档失败: %w", err)
	}
	original := append([]byte(nil), raw...)
	if len(raw) < 52 {
		return fmt.Errorf("文件太小，不是有效的存档")
	}
	slotOffset := int(binary.LittleEndian.Uint64(raw[28:36]))
	slotSize := int(binary.LittleEndian.Uint64(raw[44:52]))
	if slotOffset < 0 || slotSize <= 0 || slotOffset+slotSize > len(raw) {
		return fmt.Errorf("存档SlotData范围无效")
	}
	slotData := raw[slotOffset : slotOffset+slotSize]
	valueOffsets, err := flatBufferUIntValueOffsets(slotData, 7, SaveID_CharacterQuestUse, 10000+slot)
	if err != nil || len(valueOffsets) == 0 {
		return fmt.Errorf("读取角色次数失败: %w", err)
	}
	binary.LittleEndian.PutUint32(slotData[valueOffsets[0]:], count)
	if err := fixSlotDataHash(slotData); err != nil {
		return err
	}
	backupPath := path + ".character." + time.Now().Format("20060102_150405") + ".bak"
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		return fmt.Errorf("创建备份失败: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("写入存档失败: %w", err)
	}
	return nil
}

func characterNamesForSave(newSave bool) []string {
	oldCharacterNames := []string{
		"古兰", "姬塔", "卡塔莉娜", "拉卡姆", "伊欧", "欧根", "", "萝赛塔", "冈达葛萨", "菲莉",
		"兰斯洛特", "巴恩", "珀西瓦尔", "", "齐格飞", "夏洛特", "索恩", "尤达拉哈", "娜露梅", "伽兰查",
		"塞达", "伊德", "巴萨拉卡", "", "卡莉奥丝特罗", "", "", "圣德芬", "希耶提", "",
		"", "", "", "", "", "", "菲迪埃尔", "贝阿朵丽丝", "玛琪拉菲菈", "尤斯提斯", "芙劳",
	}
	if !newSave {
		return oldCharacterNames
	}
	return []string{
		"古兰", "姬塔", "菲迪埃尔", "卡塔莉娜", "拉卡姆", "伊欧", "欧根", "", "萝赛塔", "冈达葛萨",
		"菲莉", "兰斯洛特", "贝阿朵丽丝", "巴恩", "珀西瓦尔", "", "齐格飞", "夏洛特", "索恩", "尤达拉哈",
		"娜露梅", "伽兰查", "塞达", "伊德", "巴萨拉卡", "", "卡莉奥丝特罗", "", "", "圣德芬",
		"希耶提", "玛琪拉菲菈", "尤斯提斯", "", "芙劳",
	}
}

func characterStatsForSave(data *SaveDataBinary, newSave bool) []CharacterStat {
	const firstCharacterSlot uint32 = 10000
	characterNames := characterNamesForSave(newSave)
	counts := make(map[uint32]int32, len(characterNames))
	for _, unit := range data.UIntTable {
		if unit.IDType == SaveID_CharacterQuestUse && len(unit.ValueData) > 0 && unit.UnitID >= firstCharacterSlot && unit.UnitID < firstCharacterSlot+uint32(len(characterNames)) {
			counts[unit.UnitID-firstCharacterSlot] = int32(unit.ValueData[0])
		}
	}

	stats := make([]CharacterStat, 0, len(characterNames))
	for slot, name := range characterNames {
		if name != "" {
			stats = append(stats, CharacterStat{Slot: uint32(slot), Name: name, Count: counts[uint32(slot)]})
		}
	}
	return stats
}

// GetQuests returns the full quest list with names and clear counts
func (a *App) GetQuests(path string) ([]QuestEntry, error) {
	save, err := LoadSaveFile(path)
	if err != nil {
		return nil, err
	}
	if save.SlotData == nil {
		return nil, fmt.Errorf("存档SlotData为空")
	}

	qIDs := save.SlotData.GetUIntUnit(SaveID_QuestIDs)
	qCounts := save.SlotData.GetUIntUnit(SaveID_QuestCompleteCount)
	if qIDs == nil || qCounts == nil {
		return nil, nil
	}

	var quests []QuestEntry
	for i := 0; i < len(qIDs.ValueData); i++ {
		if qIDs.ValueData[i] == 0 {
			continue
		}
		count := uint32(0)
		if i < len(qCounts.ValueData) {
			count = qCounts.ValueData[i]
		}
		qid := storedToQuestID(qIDs.ValueData[i])
		name := questIDToName(qIDs.ValueData[i])
		nameCN := questIDToNameCN(qIDs.ValueData[i])
		quests = append(quests, QuestEntry{
			QuestID:     qid,
			QuestName:   name,
			QuestNameCN: nameCN,
			Clears:      count,
		})
	}
	return quests, nil
}
