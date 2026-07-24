package backend

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed data/quest_names_i18n.csv
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
	Index       int    `json:"index"`
	QuestID     uint32 `json:"questId"`
	QuestCode   string `json:"questCode"`
	StoredID    uint32 `json:"storedId"`
	QuestName   string `json:"questName"`
	QuestNameCN string `json:"questNameCn"`
	Clears      uint32 `json:"clears"`
}

type CharacterStat struct {
	Slot  uint32 `json:"slot"`
	Name  string `json:"name"`
	Count int32  `json:"count"`
}

type CharacterCountChange struct {
	Slot  uint32 `json:"slot"`
	Count int32  `json:"count"`
}

type QuestCountChange struct {
	Index    int    `json:"index"`
	QuestID  uint32 `json:"questId"`
	StoredID uint32 `json:"storedId"`
	Count    uint32 `json:"count"`
}

type SaveEditResult struct {
	OutputPath string `json:"outputPath"`
	BackupPath string `json:"backupPath"`
	Changed    int    `json:"changed"`
	Verified   int    `json:"verified"`
}

type SaveSlot struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
	Name  string `json:"name"`
}

// ── Quest name mapping ──

var questNames map[string]string
var questNamesCN map[string]string

func init() {
	questNames = make(map[string]string)
	questNamesCN = make(map[string]string)
	r := csv.NewReader(strings.NewReader(string(questCSVData)))
	records, err := r.ReadAll()
	if err != nil {
		return
	}
	for _, row := range records[1:] { // skip header
		if len(row) >= 2 {
			code := strings.ToUpper(strings.TrimSpace(row[0]))
			questNames[code] = row[1]
			if len(row) >= 3 && row[2] != "" {
				questNamesCN[code] = row[2]
			}
		}
	}
}

func storedToQuestCode(stored uint32) string {
	return fmt.Sprintf("%06X", stored)
}

func questIDToName(stored uint32) string {
	code := storedToQuestCode(stored)
	if name, ok := questNames[code]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return "Quest " + code
}

func questIDToNameCN(stored uint32) string {
	code := storedToQuestCode(stored)
	if name, ok := questNamesCN[code]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return "未收录任务 · " + code
}

func storedToQuestID(stored uint32) uint32 {
	code := storedToQuestCode(stored)
	if qid, err := strconv.ParseUint(code, 10, 32); err == nil {
		return uint32(qid)
	}
	return stored
}

// ── App save methods (bound to Wails) ──

// FindSaveFiles scans the default GBFR save directory
func (a *App) FindSaveFiles() []SaveSlot {
	gbfrFolder := defaultSaveGamesDir()
	var slots []SaveSlot
	for index := 1; index <= 3; index++ {
		name := fmt.Sprintf("SaveData%d.dat", index)
		path := filepath.Join(gbfrFolder, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		slots = append(slots, SaveSlot{Index: index, Path: path, Name: name})
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

func characterStatsForSave(data *SaveDataBinary, newSave bool) []CharacterStat {
	const firstCharacterSlot uint32 = 10000

	counts := make(map[uint32]int32, 41)
	identities := make(map[uint32]uint32, 41)
	for _, unit := range data.UIntTable {
		if unit.IDType == SaveID_CharacterQuestUse && len(unit.ValueData) > 0 && unit.UnitID >= firstCharacterSlot && unit.UnitID < firstCharacterSlot+41 {
			counts[unit.UnitID-firstCharacterSlot] = int32(unit.ValueData[0])
		} else if unit.IDType == SaveID_CharacterID && len(unit.ValueData) > 0 && unit.UnitID >= firstCharacterSlot && unit.UnitID < firstCharacterSlot+41 {
			identities[unit.UnitID-firstCharacterSlot] = unit.ValueData[0]
		}
	}

	stats := make([]CharacterStat, 0, len(characterNameByHash))
	if len(identities) == 0 {
		names := convertedCharacterNames[:]
		if newSave {
			names = dlcCharacterNames[:]
		}
		for slot, name := range names {
			if name != "" {
				stats = append(stats, CharacterStat{Slot: uint32(slot), Name: name, Count: counts[uint32(slot)]})
			}
		}
		return stats
	}
	for slot := uint32(0); slot < 41; slot++ {
		name, ok := characterNameByHash[identities[slot]]
		if !ok {
			continue
		}
		stats = append(stats, CharacterStat{Slot: slot, Name: name, Count: counts[slot]})
	}
	return stats
}

var convertedCharacterNames = [...]string{
	"古兰", "姬塔", "卡塔莉娜", "拉卡姆", "伊欧", "欧根", "", "萝赛塔", "冈达葛萨", "菲莉",
	"兰斯洛特", "巴恩", "珀西瓦尔", "", "齐格飞", "夏洛特", "索恩", "尤达拉哈", "娜露梅", "伽兰查",
	"塞达", "伊德", "巴萨拉卡", "", "卡莉奥丝特罗", "", "", "圣德芬", "希耶提", "",
	"", "", "", "", "", "", "菲迪埃尔", "贝阿朵丽丝", "玛琪拉菲菈", "尤斯提斯", "芙劳",
}

var dlcCharacterNames = [...]string{
	"古兰", "姬塔", "菲迪埃尔", "卡塔莉娜", "拉卡姆", "伊欧", "欧根", "", "萝赛塔", "冈达葛萨",
	"菲莉", "兰斯洛特", "贝阿朵丽丝", "巴恩", "珀西瓦尔", "", "齐格飞", "夏洛特", "索恩", "尤达拉哈",
	"娜露梅", "伽兰查", "塞达", "伊德", "巴萨拉卡", "", "卡莉奥丝特罗", "", "", "圣德芬",
	"希耶提", "玛琪拉菲菈", "尤斯提斯", "", "芙劳", "", "", "", "", "", "",
}

// Character identities are stored in IDType 1301. DLC-created and converted
// saves place these hashes in different slots, so names must follow the hash,
// not a global old/new positional table (GitHub issue #18).
var characterNameByHash = map[uint32]string{
	0x2A26B1B2: "古兰", 0xA4ACBA76: "姬塔", 0x18E2F9F9: "卡塔莉娜", 0x079DF0CC: "拉卡姆",
	0x4D0A60C3: "伊欧", 0xDD7A151E: "欧根", 0xC8616284: "萝赛塔", 0x978E4B18: "冈达葛萨",
	0xC3FFD418: "菲莉", 0x22E437E5: "兰斯洛特", 0x2EBE91D5: "巴恩", 0xBDEF7181: "珀西瓦尔",
	0x627BCB0D: "齐格飞", 0xFD3BE362: "夏洛特", 0xBAD16E3B: "索恩", 0xFC6CDF7B: "尤达拉哈",
	0xE7053919: "娜露梅", 0x1BB37EF0: "伽兰查", 0x0D21B430: "塞达", 0xA3A3CB2F: "伊德",
	0xF0EB77EF: "巴萨拉卡", 0xAA66178A: "卡莉奥丝特罗", 0x718E1A14: "圣德芬", 0x296471BE: "希耶提",
	0x74DD4C79: "菲迪埃尔", 0x9A8AF295: "贝阿朵丽丝", 0x25D46F4B: "玛琪拉菲菈", 0x9B15CFB1: "尤斯提斯",
	0x646C3168: "芙劳",
}

// UpdateCharacterStats updates arbitrary character slots. Slot IDs are kept in
// the UI model so sorting/filtering can never redirect a value to another row.
func (a *App) UpdateCharacterStats(path string, changes []CharacterCountChange) (*SaveEditResult, error) {
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()

	if len(changes) == 0 {
		return nil, fmt.Errorf("没有选择要修改的角色")
	}
	if _, err := findProcessByName(charaProcessName); err == nil {
		return nil, fmt.Errorf("写入存档前请先完全退出游戏，避免游戏把旧数据写回")
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	seen := make(map[uint32]bool, len(changes))
	for _, change := range changes {
		if change.Slot >= 41 {
			return nil, fmt.Errorf("角色槽位 %d 超出 DLC 2.0.2 范围", change.Slot)
		}
		if change.Count < 0 || change.Count > 99999999 {
			return nil, fmt.Errorf("角色次数必须在 0 到 99999999 之间")
		}
		if seen[change.Slot] {
			return nil, fmt.Errorf("角色槽位 %d 被重复提交", change.Slot)
		}
		seen[change.Slot] = true
		if err := save.patchUint(SaveID_CharacterQuestUse, 10000+change.Slot, uint32(change.Count)); err != nil {
			return nil, fmt.Errorf("修改角色槽位 %d 失败: %w", change.Slot, err)
		}
	}
	if err := save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("修复存档校验失败: %w", err)
	}
	if err := save.Write(path); err != nil {
		return nil, err
	}
	result := &SaveEditResult{OutputPath: path, BackupPath: save.LastBackupPath(), Changed: len(changes)}
	verify, err := LoadSave(path)
	if err != nil {
		return nil, fmt.Errorf("修改已写入，但重新读取失败: %w", err)
	}
	for _, change := range changes {
		entry, ok := verify.findUnit(SaveID_CharacterQuestUse, 10000+change.Slot)
		if !ok || entry.Uint32() != uint32(change.Count) {
			return nil, fmt.Errorf("修改已写入，但角色槽位 %d 验证失败；请用备份恢复", change.Slot)
		}
		result.Verified++
	}
	return result, nil
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
		code := storedToQuestCode(qIDs.ValueData[i])
		if strings.HasPrefix(code, "4F") {
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
			Index:       i,
			QuestID:     qid,
			QuestCode:   code,
			StoredID:    qIDs.ValueData[i],
			QuestName:   name,
			QuestNameCN: nameCN,
			Clears:      count,
		})
	}
	return quests, nil
}

// UpdateQuestCounts modifies selected quest rows in the save vectors. Both the
// vector index and quest ID are checked to prevent a sorted UI from editing the
// wrong quest.
func (a *App) UpdateQuestCounts(path string, changes []QuestCountChange) (*SaveEditResult, error) {
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()

	if len(changes) == 0 {
		return nil, fmt.Errorf("没有选择要修改的任务")
	}
	if _, err := findProcessByName(charaProcessName); err == nil {
		return nil, fmt.Errorf("写入存档前请先完全退出游戏，避免游戏把旧数据写回")
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	ids, ok := save.findUnit(SaveID_QuestIDs, 0)
	if !ok {
		return nil, fmt.Errorf("存档缺少任务 ID 列表")
	}
	counts, ok := save.findUnit(SaveID_QuestCompleteCount, 0)
	if !ok {
		return nil, fmt.Errorf("存档缺少任务次数列表")
	}
	seen := make(map[int]bool, len(changes))
	for _, change := range changes {
		if change.Index < 0 || change.Index >= ids.ValueCnt || change.Index >= counts.ValueCnt {
			return nil, fmt.Errorf("任务索引 %d 超出范围", change.Index)
		}
		if change.Count > 99999999 {
			return nil, fmt.Errorf("任务次数必须在 0 到 99999999 之间")
		}
		if seen[change.Index] {
			return nil, fmt.Errorf("任务索引 %d 被重复提交", change.Index)
		}
		seen[change.Index] = true
		storedID, err := ids.Uint32At(change.Index)
		if err != nil || (change.StoredID != 0 && storedID != change.StoredID) || (change.StoredID == 0 && storedToQuestID(storedID) != change.QuestID) {
			return nil, fmt.Errorf("任务 %d 的索引已变化，请刷新后重试", change.QuestID)
		}
		if err := counts.SetUint32At(change.Index, change.Count); err != nil {
			return nil, err
		}
	}
	if err := save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("修复存档校验失败: %w", err)
	}
	if err := save.Write(path); err != nil {
		return nil, err
	}
	result := &SaveEditResult{OutputPath: path, BackupPath: save.LastBackupPath(), Changed: len(changes)}
	verify, err := LoadSave(path)
	if err != nil {
		return nil, fmt.Errorf("修改已写入，但重新读取失败: %w", err)
	}
	verifiedCounts, ok := verify.findUnit(SaveID_QuestCompleteCount, 0)
	if !ok {
		return nil, fmt.Errorf("修改已写入，但任务列表重新读取失败；请用备份恢复")
	}
	for _, change := range changes {
		value, err := verifiedCounts.Uint32At(change.Index)
		if err != nil || value != change.Count {
			return nil, fmt.Errorf("修改已写入，但任务 %d 验证失败；请用备份恢复", change.QuestID)
		}
		result.Verified++
	}
	return result, nil
}
