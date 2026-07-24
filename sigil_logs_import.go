package main

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	_ "modernc.org/sqlite"
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
	LogTime  int64              `json:"logTime"`
	Loadouts []LogsSigilLoadout `json:"loadouts"`
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
	PlayerData [4]*logsPlayer `cbor:"playerData"`
}
type logsPlayer struct {
	ActorIndex      uint32               `cbor:"actorIndex"`
	DisplayName     string               `cbor:"displayName"`
	CharacterName   string               `cbor:"characterName"`
	CharacterType   string               `cbor:"characterType"`
	Sigils          []logsSigil          `cbor:"sigils"`
	Summons         []logsSummon         `cbor:"summons"`
	Abilities       []uint32             `cbor:"abilities"`
	WeaponKey       string               `cbor:"weaponKey"`
	MasterLevel     uint32               `cbor:"masterLevel"`
	Skillboard      []uint32             `cbor:"skillboard"`
	Stats           *logsRecordStats     `cbor:"stats"`
	WeaponState     *logsWeaponState     `cbor:"weaponState"`
	IsOnline        bool                 `cbor:"isOnline"`
	OvermasteryInfo *logsOvermasteryInfo `cbor:"overmasteryInfo"`
	PlayerStats     *logsPlayerStats     `cbor:"playerStats"`
}
type logsSigil struct {
	FirstTraitID     uint32 `cbor:"firstTraitId"`
	FirstTraitLevel  uint32 `cbor:"firstTraitLevel"`
	SecondTraitID    uint32 `cbor:"secondTraitId"`
	SecondTraitLevel uint32 `cbor:"secondTraitLevel"`
	SigilID          uint32 `cbor:"sigilId"`
	SigilLevel       uint32 `cbor:"sigilLevel"`
}
type logsSummon struct {
	SummonID       uint32 `cbor:"summonId"`
	MainTraitID    uint32 `cbor:"mainTraitId"`
	MainTraitLevel uint32 `cbor:"mainTraitLevel"`
	BonusID        uint32 `cbor:"bonusId"`
	BonusLevel     uint32 `cbor:"bonusLevel"`
}
type logsTraitPair struct {
	ID    uint32 `cbor:"id"`
	Level uint32 `cbor:"level"`
}
type logsWeaponState struct {
	WeaponID          uint32          `cbor:"weaponId"`
	Exp               uint32          `cbor:"exp"`
	StarLevel         uint32          `cbor:"starLevel"`
	PlusMarks         uint32          `cbor:"plusMarks"`
	AwakeningLevel    uint32          `cbor:"awakeningLevel"`
	WrightstoneID     uint32          `cbor:"wrightstoneId"`
	WrightstoneTraits []logsTraitPair `cbor:"wrightstoneTraits"`
	InnateTraits      []logsTraitPair `cbor:"innateTraits"`
}
type logsOvermasteryInfo struct {
	Overmasteries []logsOvermastery `cbor:"overmasteries"`
}
type logsOvermastery struct {
	ID    uint32  `cbor:"id"`
	Flags uint32  `cbor:"flags"`
	Value float32 `cbor:"value"`
}
type logsRecordStats struct {
	Level     uint32  `cbor:"level"`
	HP        uint32  `cbor:"hp"`
	Attack    uint32  `cbor:"attack"`
	StunPower float32 `cbor:"stunPower"`
	Power     uint32  `cbor:"power"`
}
type logsPlayerStats struct {
	Level        uint32  `cbor:"level"`
	TotalHP      uint32  `cbor:"totalHp"`
	TotalAttack  uint32  `cbor:"totalAttack"`
	StunPower    float32 `cbor:"stunPower"`
	CriticalRate float32 `cbor:"criticalRate"`
	TotalPower   uint32  `cbor:"totalPower"`
}

// SelectLogsSigilLoadouts reads v1 GBFR Logs records through SQLite read-only mode.
func (a *App) SelectLogsSigilLoadouts() ([]LogsSigilLoadoutImport, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("Wails 上下文未初始化")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 GBFR Logs SQLite 文件", Filters: []runtime.FileFilter{{DisplayName: "SQLite 数据库 (*.db, *.sqlite, *.sqlite3)", Pattern: "*.db;*.sqlite;*.sqlite3"}, {DisplayName: "所有文件 (*.*)", Pattern: "*.*"}}})
	if err != nil || path == "" {
		return nil, err
	}
	return readLogsSigilLoadouts(path)
}

func readLogsSigilLoadouts(path string) ([]LogsSigilLoadoutImport, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("打开日志数据库失败: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT time, data FROM logs WHERE version = 1 ORDER BY time DESC`)
	if err != nil {
		return nil, fmt.Errorf("读取 logs 表失败: %w", err)
	}
	defer rows.Close()
	var imports []LogsSigilLoadoutImport
	for rows.Next() {
		var time int64
		var blob []byte
		if err := rows.Scan(&time, &blob); err != nil {
			return nil, fmt.Errorf("读取日志记录失败: %w", err)
		}
		encounter, err := decodeLogsEncounter(blob)
		if err != nil {
			continue
		}
		if loadouts := logsPlayerLoadouts(encounter); len(loadouts) != 0 {
			imports = append(imports, LogsSigilLoadoutImport{LogTime: time, Loadouts: loadouts})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历日志记录失败: %w", err)
	}
	if len(imports) == 0 {
		return nil, fmt.Errorf("未找到包含玩家因子配装的 v1 日志")
	}
	return imports, nil
}
func decodeLogsEncounter(blob []byte) (logsEncounter, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return logsEncounter{}, err
	}
	defer decoder.Close()
	raw, err := decoder.DecodeAll(blob, nil)
	if err != nil {
		return logsEncounter{}, err
	}
	var encounter logsEncounter
	if err = cbor.Unmarshal(raw, &encounter); err != nil {
		return logsEncounter{}, err
	}
	return encounter, nil
}
func logsPlayerLoadouts(encounter logsEncounter) []LogsSigilLoadout {
	result := make([]LogsSigilLoadout, 0, 4)
	for _, p := range encounter.PlayerData {
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
		draft, warnings := logsPlayerLoadoutDraft(p, entries)
		result = append(result, LogsSigilLoadout{PlayerName: p.DisplayName, CharacterName: p.CharacterName, CharacterType: p.CharacterType, Entries: entries, Loadout: draft, Warnings: warnings})
	}
	return result
}

// logsPlayerLoadoutDraft is intentionally a read-only conversion. Fields whose
// save layout is not validated in this project remain in the portable draft.
func logsPlayerLoadoutDraft(p *logsPlayer, entries []SigilLoadoutEntry) (*LoadoutShare, []string) {
	share := &LoadoutShare{Format: loadoutShareFormat, Version: loadoutShareLogsVersion, CharaName: p.CharacterName, OwnerCode: p.CharacterType, Name: loadoutName(p.DisplayName, p.CharacterName), WeaponName: p.WeaponKey, MasteryHashes: make([]string, 0, len(p.Skillboard))}
	warnings := []string{"来源为 GBFR Logs 战斗快照；远程玩家的武器、祝福和数值可能不完整。当前项目仅支持将其作为草稿和因子写入来源。"}
	for i, e := range entries {
		index := i
		share.Sigils = append(share.Sigils, LoadoutShareSigil{Index: &index, Hash: loadoutHex(e.SigilHash), Level: int(e.SigilLevel), PrimaryTraitHash: loadoutHex(e.PrimaryTraitHash), PrimaryTraitLevel: int(e.PrimaryTraitLevel), SecondaryTraitHash: loadoutHex(e.SecondaryTraitHash), SecondaryTraitLevel: int(e.SecondaryTraitLevel)})
	}
	if len(p.Abilities) > loadoutShareMaxSkills || len(p.Skillboard) > loadoutShareMaxMastery {
		warnings = append(warnings, "日志中的技能或专精数量超过完整分享格式限制，已截断为可导出的数量。")
	}
	for _, id := range p.Abilities[:min(len(p.Abilities), loadoutShareMaxSkills)] {
		share.Skills = append(share.Skills, LoadoutShareSkill{Hash: loadoutHex(id)})
	}
	for _, id := range p.Skillboard[:min(len(p.Skillboard), loadoutShareMaxMastery)] {
		share.MasteryHashes = append(share.MasteryHashes, loadoutHex(id))
	}
	if len(p.Summons) == 4 {
		for _, s := range p.Summons {
			share.Summons = append(share.Summons, LoadoutShareSummon{TypeHash: loadoutHex(s.SummonID), MainTraitHash: loadoutHex(s.MainTraitID), MainTraitLevel: int(s.MainTraitLevel), SubParamHash: loadoutHex(s.BonusID), SubParamLevel: int(s.BonusLevel)})
		}
	} else if len(p.Summons) != 0 {
		warnings = append(warnings, "日志中的召唤石数量不是 4，未写入完整分享草稿。")
	}
	if p.WeaponState != nil {
		w := p.WeaponState
		share.WeaponHash = loadoutHex(w.WeaponID)
		stone := &LoadoutShareWeaponWrightstone{Hash: loadoutHex(w.WrightstoneID)}
		for i, trait := range w.WrightstoneTraits {
			stone.Traits = append(stone.Traits, LoadoutShareWeaponWrightstoneTrait{Index: i, Hash: loadoutHex(trait.ID), Level: int(trait.Level)})
		}
		share.Weapon = &LoadoutShareWeaponState{StoredHash: loadoutHex(w.WeaponID), XP: w.Exp, Uncap: int(w.StarLevel), Mirage: int(w.PlusMarks), Awakening: int(w.AwakeningLevel), Wrightstone: stone}
		for _, trait := range w.InnateTraits {
			share.Weapon.SkillHashes = append(share.Weapon.SkillHashes, loadoutHex(trait.ID))
		}
	}
	if p.Stats != nil {
		share.Character = &LoadoutShareCharacterProgression{CharacterLevel: int(p.Stats.Level), BaseHP: int(p.Stats.HP), BaseATK: int(p.Stats.Attack), CharacterBaseCaptured: true, MasterTotalMSP: int(p.MasterLevel)}
	} else if p.PlayerStats != nil {
		s := p.PlayerStats
		share.Character = &LoadoutShareCharacterProgression{CharacterLevel: int(s.Level), BaseHP: int(s.TotalHP), BaseATK: int(s.TotalAttack), CharacterBaseCaptured: true, MasterTotalMSP: int(p.MasterLevel)}
	}
	if p.OvermasteryInfo != nil {
		share.LogsSnapshot = &LoadoutLogsSnapshot{IsOnline: p.IsOnline, Overmasteries: make([]LoadoutLogsOvermastery, 0, len(p.OvermasteryInfo.Overmasteries))}
		for _, item := range p.OvermasteryInfo.Overmasteries {
			share.LogsSnapshot.Overmasteries = append(share.LogsSnapshot.Overmasteries, LoadoutLogsOvermastery{ID: item.ID, Flags: item.Flags, Value: item.Value})
		}
	}
	return share, warnings
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
