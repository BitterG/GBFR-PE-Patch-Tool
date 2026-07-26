package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gbfrPlayerInfoEdit/internal/backend"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxSigilLoadoutEntries = 12

// LogsSigilLoadout retains the existing sigil-restoration payload and additionally
// exposes a portable complete loadout draft when the log contains those fields.
type LogsSigilLoadout struct {
	PlayerName    string              `json:"playerName"`
	CharacterName string              `json:"characterName"`
	CharacterType string              `json:"characterType"`
	Entries       []SigilLoadoutEntry `json:"entries"`
	Loadout       *LoadoutShare       `json:"loadout,omitempty"`
	Warnings      []string            `json:"warnings,omitempty"`
}

type LogsSigilLoadoutImport struct {
	LogTime   int64              `json:"logTime"`
	QuestID   uint32             `json:"questId,omitempty"`
	QuestName string             `json:"questName,omitempty"`
	Loadouts  []LogsSigilLoadout `json:"loadouts"`
}
type SigilLoadoutEntry struct {
	SigilHash           uint32 `json:"sigilHash"`
	SigilLevel          uint32 `json:"sigilLevel"`
	PrimaryTraitHash    uint32 `json:"primaryTraitHash"`
	PrimaryTraitLevel   uint32 `json:"primaryTraitLevel"`
	SecondaryTraitHash  uint32 `json:"secondaryTraitHash"`
	SecondaryTraitLevel uint32 `json:"secondaryTraitLevel"`
}

type logsEncounter struct {
	QuestID    uint32         `cbor:"questId" json:"questId"`
	PlayerData [4]*logsPlayer `cbor:"playerData" json:"playerData"`
}
type logsPlayer struct {
	ActorIndex      uint32               `cbor:"actorIndex" json:"actorIndex"`
	DisplayName     string               `cbor:"displayName" json:"displayName"`
	CharacterName   string               `cbor:"characterName" json:"characterName"`
	CharacterType   string               `cbor:"characterType" json:"characterType"`
	Sigils          []logsSigil          `cbor:"sigils" json:"sigils"`
	Summons         []logsSummon         `cbor:"summons" json:"summons"`
	Abilities       []uint32             `cbor:"abilities" json:"abilities"`
	WeaponKey       string               `cbor:"weaponKey" json:"weaponKey"`
	MasterLevel     uint32               `cbor:"masterLevel" json:"masterLevel"`
	Skillboard      []uint32             `cbor:"skillboard" json:"skillboard"`
	Stats           *logsRecordStats     `cbor:"stats" json:"stats"`
	WeaponState     *logsWeaponState     `cbor:"weaponState" json:"weaponState"`
	IsOnline        bool                 `cbor:"isOnline" json:"isOnline"`
	OvermasteryInfo *logsOvermasteryInfo `cbor:"overmasteryInfo" json:"overmasteryInfo"`
	PlayerStats     *logsPlayerStats     `cbor:"playerStats" json:"playerStats"`
}
type logsSigil struct {
	FirstTraitID     uint32 `cbor:"firstTraitId" json:"firstTraitId"`
	FirstTraitLevel  uint32 `cbor:"firstTraitLevel" json:"firstTraitLevel"`
	SecondTraitID    uint32 `cbor:"secondTraitId" json:"secondTraitId"`
	SecondTraitLevel uint32 `cbor:"secondTraitLevel" json:"secondTraitLevel"`
	SigilID          uint32 `cbor:"sigilId" json:"sigilId"`
	SigilLevel       uint32 `cbor:"sigilLevel" json:"sigilLevel"`
}
type logsSummon struct {
	SummonID       uint32 `cbor:"summonId" json:"summonId"`
	MainTraitID    uint32 `cbor:"mainTraitId" json:"mainTraitId"`
	MainTraitLevel uint32 `cbor:"mainTraitLevel" json:"mainTraitLevel"`
	BonusID        uint32 `cbor:"bonusId" json:"bonusId"`
	BonusLevel     uint32 `cbor:"bonusLevel" json:"bonusLevel"`
}
type logsTraitPair struct {
	ID    uint32 `cbor:"id" json:"id"`
	Level uint32 `cbor:"level" json:"level"`
}
type logsWeaponState struct {
	WeaponID          uint32          `cbor:"weaponId" json:"weaponId"`
	Exp               uint32          `cbor:"exp" json:"exp"`
	StarLevel         uint32          `cbor:"starLevel" json:"starLevel"`
	PlusMarks         *uint32         `cbor:"plusMarks" json:"plusMarks"`
	AwakeningLevel    uint32          `cbor:"awakeningLevel" json:"awakeningLevel"`
	Transcendence      *uint32         `cbor:"transcendence" json:"transcendence"`
	WrightstoneID     uint32          `cbor:"wrightstoneId" json:"wrightstoneId"`
	WrightstoneTraits []logsTraitPair `cbor:"wrightstoneTraits" json:"wrightstoneTraits"`
	InnateTraits      []logsTraitPair `cbor:"innateTraits" json:"innateTraits"`
}
type logsOvermasteryInfo struct {
	Overmasteries []logsOvermastery `cbor:"overmasteries" json:"overmasteries"`
}
type logsOvermastery struct {
	ID    uint32  `cbor:"id" json:"id"`
	Flags uint32  `cbor:"flags" json:"flags"`
	Value float32 `cbor:"value" json:"value"`
}
type logsRecordStats struct {
	Level     uint32  `cbor:"level" json:"level"`
	HP        uint32  `cbor:"hp" json:"hp"`
	Attack    uint32  `cbor:"attack" json:"attack"`
	StunPower float32 `cbor:"stunPower" json:"stunPower"`
	Power     uint32  `cbor:"power" json:"power"`
}
type logsPlayerStats struct {
	Level        uint32  `cbor:"level" json:"level"`
	TotalHP      uint32  `cbor:"totalHp" json:"totalHp"`
	TotalAttack  uint32  `cbor:"totalAttack" json:"totalAttack"`
	StunPower    float32 `cbor:"stunPower" json:"stunPower"`
	CriticalRate float32 `cbor:"criticalRate" json:"criticalRate"`
	TotalPower   uint32  `cbor:"totalPower" json:"totalPower"`
}

// SelectLogsSigilLoadouts reads character snapshots exported as JSON by GBFR Logs.
func (a *App) SelectLogsSigilLoadouts() ([]LogsSigilLoadoutImport, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("Wails 上下文未初始化")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 GBFR Logs 导出 JSON", Filters: []runtime.FileFilter{{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"}, {DisplayName: "所有文件 (*.*)", Pattern: "*.*"}}})
	if err != nil || path == "" {
		return nil, err
	}
	return readLogsSigilLoadoutsJSON(path)
}

// readLogsSigilLoadoutsJSON accepts either one character object or an array of
// character objects from a GBFR Logs JSON export and wraps them in one record.
func readLogsSigilLoadoutsJSON(path string) ([]LogsSigilLoadoutImport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 Logs 导出 JSON 失败: %w", err)
	}
	var players []logsPlayer
	if err := json.Unmarshal(raw, &players); err != nil {
		var player logsPlayer
		if objectErr := json.Unmarshal(raw, &player); objectErr != nil {
			return nil, fmt.Errorf("解析 Logs 导出 JSON 失败: %w", err)
		}
	players = []logsPlayer{player}
	}
	playerPointers := make([]*logsPlayer, len(players))
	for index := range players {
		playerPointers[index] = &players[index]
	}
	loadouts := logsPlayerLoadouts(playerPointers)
	if len(loadouts) == 0 {
		return nil, fmt.Errorf("Logs 导出 JSON 中未找到包含玩家因子配装的角色")
	}
	return []LogsSigilLoadoutImport{{QuestName: "Logs 导出 JSON", Loadouts: loadouts}}, nil
}
func normalizedLogsCharacter(value string) string {
	return strings.TrimSpace(strings.TrimRight(value, "\x00"))
}

func normalizedLogsOwnerCode(value string) string {
	value = strings.ToUpper(value)
	for index := 0; index+6 <= len(value); index++ {
		candidate := value[index : index+6]
		if candidate[0] != 'P' || candidate[1] != 'L' {
			continue
		}
		if candidate[2] >= '0' && candidate[2] <= '9' && candidate[3] >= '0' && candidate[3] <= '9' && candidate[4] >= '0' && candidate[4] <= '9' && candidate[5] >= '0' && candidate[5] <= '9' {
			return candidate
		}
	}
	return ""
}

func logsPlayerOwnerCode(p *logsPlayer) string {
	if ownerCode := normalizedLogsOwnerCode(p.WeaponKey); ownerCode != "" {
		return ownerCode
	}
	return normalizedLogsOwnerCode(p.CharacterType)
}

func logsCharacterName(ownerCode string) string {
	known := map[string]string{
		"PL0000": "古兰", "PL0100": "卡塔莉娜", "PL0200": "拉卡姆", "PL0300": "尤金", "PL0400": "伊欧", "PL0500": "萝赛塔",
		"PL0700": "兰斯洛特", "PL0800": "巴恩", "PL0900": "珀西瓦尔", "PL1000": "菲莉", "PL1100": "齐格飞", "PL1200": "夏洛特",
		"PL1300": "索恩", "PL1400": "尤达拉哈", "PL1500": "娜露梅", "PL1600": "冈达葛萨", "PL1700": "卡莉奥斯特萝", "PL1800": "巴萨拉卡",
		"PL2000": "泽塔", "PL2100": "伊德", "PL2400": "伽兰查", "PL2700": "圣德芬", "PL2800": "希耶提", "PL2900": "贝阿朵丽丝",
	}
	return known[ownerCode]
}

func logsPlayerLoadouts(players []*logsPlayer) []LogsSigilLoadout {
	result := make([]LogsSigilLoadout, 0, len(players))
	for _, p := range players {
		if p == nil || len(p.Sigils) == 0 {
			continue
		}
		entries := make([]SigilLoadoutEntry, 0, len(p.Sigils))
		for _, s := range p.Sigils {
			if s.SigilID != 0 {
				entries = append(entries, SigilLoadoutEntry{SigilHash: s.SigilID, SigilLevel: s.SigilLevel, PrimaryTraitHash: s.FirstTraitID, PrimaryTraitLevel: s.FirstTraitLevel, SecondaryTraitHash: s.SecondTraitID, SecondaryTraitLevel: s.SecondTraitLevel})
			}
		}
		if len(entries) == 0 || len(entries) > maxSigilLoadoutEntries {
			continue
		}
		characterType := logsPlayerOwnerCode(p)
		characterName := logsCharacterName(characterType)
		draft, warnings := logsPlayerLoadoutDraft(p, entries)
		result = append(result, LogsSigilLoadout{PlayerName: p.DisplayName, CharacterName: characterName, CharacterType: characterType, Entries: entries, Loadout: draft, Warnings: warnings})
	}
	return result
}

// logsPlayerLoadoutDraft writes only fields whose Logs values are validated against
// the save layout; all remaining captured data stays in the portable draft snapshot.
func logsPlayerLoadoutDraft(p *logsPlayer, entries []SigilLoadoutEntry) (*LoadoutShare, []string) {
	characterType := logsPlayerOwnerCode(p)
	share := &LoadoutShare{Format: loadoutShareFormat, Version: loadoutShareLogsVersion, LogsImport: true, OwnerCode: characterType, Name: loadoutName(p.DisplayName, normalizedLogsCharacter(p.CharacterName)), WeaponName: p.WeaponKey, MasteryHashes: []string{}}
	warnings := []string{"来源为 GBFR Logs 战斗快照；远程玩家的武器、祝福和数值可能不完整。当前项目仅支持将其作为草稿和因子写入来源。"}
	for i, e := range entries {
		index := i
		share.Sigils = append(share.Sigils, LoadoutShareSigil{Index: &index, Hash: loadoutHex(e.SigilHash), Name: backend.ResolveLogsSigilName(e.SigilHash), Level: int(e.SigilLevel), PrimaryTraitHash: loadoutHex(e.PrimaryTraitHash), PrimaryTraitName: backend.ResolveLogsTraitName(e.PrimaryTraitHash), PrimaryTraitLevel: int(e.PrimaryTraitLevel), SecondaryTraitHash: loadoutHex(e.SecondaryTraitHash), SecondaryTraitName: backend.ResolveLogsTraitName(e.SecondaryTraitHash), SecondaryTraitLevel: int(e.SecondaryTraitLevel)})
	}
	if len(p.Abilities) > loadoutShareMaxSkills {
		warnings = append(warnings, "日志中的技能数量超过完整分享格式限制，已截断为可导出的数量。")
	}
	if len(p.Skillboard) > 0 {
		if len(p.Skillboard) > loadoutShareMaxMastery {
			warnings = append(warnings, fmt.Sprintf("Logs 专精节点数量超过 %d，已截断为只读展示。", loadoutShareMaxMastery))
		}
		checked := min(len(p.Skillboard), loadoutShareMaxMastery)
		share.LogsSkillboardEffectUIIDs = append([]uint32(nil), p.Skillboard[:checked]...)
		mapping, err := backend.NewApp().MapLogsMasteryEffectUIIDs(characterType, share.LogsSkillboardEffectUIIDs)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Logs 专精安全映射不可用，全部仅展示不会写入：%v", err))
		} else {
			share.MasteryHashes = append(share.MasteryHashes, mapping.Hashes...)
			warnings = append(warnings, fmt.Sprintf("Logs 专精已安全映射 %d 个并会写入；%d 个因同类同阶重复文本随机分配不足、文本缺失或布局未收录仅展示不会写入。", len(mapping.Hashes), len(mapping.Unmapped)))
		}
	}
	for _, id := range p.Abilities[:min(len(p.Abilities), loadoutShareMaxSkills)] {
		share.Skills = append(share.Skills, LoadoutShareSkill{Hash: loadoutHex(id), Name: backend.ResolveLogsSkillName(id)})
	}

	if len(p.Summons) == 4 {
		for _, s := range p.Summons {
			typeName, mainName, subName := backend.ResolveLogsSummonNames(s.SummonID, s.MainTraitID, s.BonusID)
			share.Summons = append(share.Summons, LoadoutShareSummon{TypeHash: loadoutHex(s.SummonID), Name: typeName, MainTraitHash: loadoutHex(s.MainTraitID), MainTraitName: mainName, MainTraitLevel: int(s.MainTraitLevel), SubParamHash: loadoutHex(s.BonusID), SubParamName: subName, SubParamLevel: int(s.BonusLevel)})
		}
	} else if len(p.Summons) != 0 {
		warnings = append(warnings, "日志中的召唤石数量不是 4，未写入完整分享草稿。")
	}
	if p.WeaponState != nil {
		w := p.WeaponState
		share.WeaponHash = loadoutHex(w.WeaponID)
		traitHashes := make([]uint32, len(w.WrightstoneTraits))
		for index, trait := range w.WrightstoneTraits {
			traitHashes[index] = trait.ID
		}
		stoneName, traitNames := backend.ResolveLogsWrightstoneNames(w.WrightstoneID, traitHashes)
		stone := &LoadoutShareWeaponWrightstone{Hash: loadoutHex(w.WrightstoneID), Name: stoneName}
		for i, trait := range w.WrightstoneTraits {
			stone.Traits = append(stone.Traits, LoadoutShareWeaponWrightstoneTrait{Index: i, Hash: loadoutHex(trait.ID), Name: traitNames[i], Level: int(trait.Level)})
		}
		share.Weapon = &LoadoutShareWeaponState{StoredHash: loadoutHex(w.WeaponID), XP: w.Exp, Uncap: int(w.StarLevel), Awakening: int(w.AwakeningLevel), EnhancementCaptured: w.Transcendence != nil, MirageCaptured: w.PlusMarks != nil, Wrightstone: stone}
		if w.PlusMarks != nil {
			share.Weapon.Mirage = int(*w.PlusMarks)
		}
		if w.Transcendence != nil {
			share.Weapon.Transcendence = int(*w.Transcendence)
		} else {
			warnings = append(warnings, "Logs 武器快照缺少 transcendence；仍可导入装备和祝福，但不会覆盖武器强化字段。")
		}
		for _, trait := range w.InnateTraits {
			share.Weapon.SkillHashes = append(share.Weapon.SkillHashes, loadoutHex(trait.ID))
			share.Weapon.SkillNames = append(share.Weapon.SkillNames, backend.ResolveLogsWeaponSkillName(trait.ID))
			share.Weapon.SkillLevels = append(share.Weapon.SkillLevels, int(trait.Level))
		}
	}
	if p.Stats != nil {
		share.Character = &LoadoutShareCharacterProgression{CharacterLevel: int(p.Stats.Level), BaseHP: int(p.Stats.HP), BaseATK: int(p.Stats.Attack), CharacterBaseCaptured: true}
	} else if p.PlayerStats != nil {
		s := p.PlayerStats
		share.Character = &LoadoutShareCharacterProgression{CharacterLevel: int(s.Level), BaseHP: int(s.TotalHP), BaseATK: int(s.TotalAttack), CharacterBaseCaptured: true}
	}
	if p.MasterLevel < 1 || p.MasterLevel > 55 {
		warnings = append(warnings, fmt.Sprintf("Logs 专精等级 %d 不在支持范围 1..55，未写入专精进度。", p.MasterLevel))
	} else if share.Character == nil {
		warnings = append(warnings, "Logs 专精等级缺少角色快照，未写入专精进度。")
	} else if total, err := backend.MasterTotalMSPForProgress(0, int(p.MasterLevel)); err != nil {
		warnings = append(warnings, fmt.Sprintf("Logs 专精等级无法转换为 MSP，未写入：%v", err))
	} else {
		share.Character.MasterTotalMSP = total
		share.Character.MasterProgressIndex = int(p.MasterLevel)
		share.Character.MasterProgressCaptured = true
	}
	if p.OvermasteryInfo != nil {
		overmasteries := p.OvermasteryInfo.Overmasteries
		share.LogsSnapshot = &LoadoutLogsSnapshot{IsOnline: p.IsOnline, Overmasteries: make([]LoadoutLogsOvermastery, 0, len(overmasteries))}
		for _, item := range overmasteries {
			share.LogsSnapshot.Overmasteries = append(share.LogsSnapshot.Overmasteries, LoadoutLogsOvermastery{ID: item.ID, Flags: item.Flags, Value: item.Value})
		}

		if len(overmasteries) != 4 {
			warnings = append(warnings, fmt.Sprintf("Logs 上限突破数量为 %d（需要恰好 4 项），仅展示未写入。", len(overmasteries)))
		} else {
			resolved := make([]LoadoutShareOverLimit, 0, 4)
			seenAttributes := make(map[string]struct{}, 4)
			hasUnknown, hasInvalidOrDuplicate := false, false
			for index, item := range overmasteries {
				name, attributeHash, level, value, unit, ok := backend.ResolveLogsOvermastery(item.ID, item.Value)
				if !ok {
					hasUnknown = true
					continue
				}
				if attributeHash == "" || level < 1 || level > 10 {
					hasInvalidOrDuplicate = true
					continue
				}
				if _, duplicate := seenAttributes[attributeHash]; duplicate {
					hasInvalidOrDuplicate = true
					continue
				}
				seenAttributes[attributeHash] = struct{}{}
				resolved = append(resolved, LoadoutShareOverLimit{Index: index, AttributeHash: attributeHash, Name: name, Level: level, Value: value, Unit: unit})
			}
			if hasUnknown {
				warnings = append(warnings, "Logs 上限突破包含未知项，仅展示未写入。")
			}
			if hasInvalidOrDuplicate {
				warnings = append(warnings, "Logs 上限突破存在无效或重复映射，仅展示未写入。")
			}
			if !hasUnknown && !hasInvalidOrDuplicate && len(resolved) == 4 {
				share.OverLimit = resolved
				share.Version = 4
			}
		}
	}
	return share, warnings
}

