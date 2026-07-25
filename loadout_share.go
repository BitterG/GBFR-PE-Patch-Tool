package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	loadoutShareFormat        = "gbfr-loadout"
	loadoutShareVersion       = 10
	loadoutShareLegacyVersion = 1
	loadoutShareLogsVersion   = 3
	loadoutShareMaxSize       = 1024 * 1024
	loadoutShareMaxSigils     = 12
	loadoutShareMaxSkills     = 4
	loadoutShareMaxMastery    = 50
)

type LoadoutShare struct {
	Format            string                            `json:"format"`
	Version           int                               `json:"version"`
	CharaHash         string                            `json:"charaHash"`
	CharaName         string                            `json:"charaName"`
	OwnerCode         string                            `json:"ownerCode"`
	Name              string                            `json:"name"`
	WeaponHash        string                            `json:"weaponHash,omitempty"`
	WeaponName        string                            `json:"weaponName,omitempty"`
	Sigils            []LoadoutShareSigil               `json:"sigils"`
	Summons           []LoadoutShareSummon              `json:"summons,omitempty"`
	Skills            []LoadoutShareSkill               `json:"skills"`
	WeaponSkillHashes []string                          `json:"weaponSkillHashes,omitempty"`
	MasteryHashes     []string                          `json:"masteryHashes"`
	Character         *LoadoutShareCharacterProgression `json:"character,omitempty"`
	Weapon            *LoadoutShareWeaponState          `json:"weapon,omitempty"`
	OverLimit         []LoadoutShareOverLimit           `json:"overLimit,omitempty"`
	LogsSnapshot      *LoadoutLogsSnapshot              `json:"logsSnapshot,omitempty"`
}
type LoadoutShareSigil struct {
	Index               *int   `json:"index,omitempty"`
	Hash                string `json:"hash"`
	Name                string `json:"name"`
	Level               int    `json:"level"`
	PrimaryTraitHash    string `json:"primaryTraitHash"`
	PrimaryTraitName    string `json:"primaryTraitName,omitempty"`
	PrimaryTraitLevel   int    `json:"primaryTraitLevel"`
	SecondaryTraitHash  string `json:"secondaryTraitHash,omitempty"`
	SecondaryTraitName  string `json:"secondaryTraitName,omitempty"`
	SecondaryTraitLevel int    `json:"secondaryTraitLevel,omitempty"`
}
type LoadoutShareSummon struct {
	TypeHash       string `json:"typeHash"`
	Name           string `json:"name"`
	MainTraitHash  string `json:"mainTraitHash"`
	MainTraitName  string `json:"mainTraitName,omitempty"`
	MainTraitLevel int    `json:"mainTraitLevel"`
	SubParamHash   string `json:"subParamHash"`
	SubParamName   string `json:"subParamName,omitempty"`
	SubParamLevel  int    `json:"subParamLevel"`
	Rank           int    `json:"rank"`
}
type LoadoutShareSkill struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}
type LoadoutShareEnhancementNode struct {
	Index int `json:"index"`
	Value int `json:"value"`
}
type LoadoutShareOverLimit struct {
	Index         int     `json:"index"`
	AttributeHash string  `json:"attributeHash,omitempty"`
	Name          string  `json:"name,omitempty"`
	Level         int     `json:"level,omitempty"`
	Value         float64 `json:"value,omitempty"`
	Unit          string  `json:"unit,omitempty"`
}
type LoadoutShareWeaponWrightstoneTrait struct {
	Index   int    `json:"index"`
	Hash    string `json:"hash"`
	TraitID string `json:"traitId"`
	Name    string `json:"name"`
	Level   int    `json:"level"`
}
type LoadoutShareWeaponWrightstone struct {
	Hash            string                               `json:"hash"`
	InternalID      string                               `json:"internalId"`
	Name            string                               `json:"name"`
	Traits          []LoadoutShareWeaponWrightstoneTrait `json:"traits"`
	Evidence        string                               `json:"evidence"`
	RuntimeObserved bool                                 `json:"runtimeObserved"`
	StableReads     int                                  `json:"stableReads"`
}
type LoadoutShareProgressionWeapon struct {
	Hash               string                         `json:"hash"`
	BaseHash           string                         `json:"baseHash,omitempty"`
	InternalID         string                         `json:"internalId"`
	Level              int                            `json:"level"`
	Uncap              int                            `json:"uncap"`
	Mirage             int                            `json:"mirage"`
	Awakening          int                            `json:"awakening"`
	Transcendence      int                            `json:"transcendence"`
	TranscendenceSkill string                         `json:"transcendenceSkill,omitempty"`
	Wrightstone        *LoadoutShareWeaponWrightstone `json:"wrightstone,omitempty"`
}
type LoadoutShareWeaponState struct {
	StoredHash           string                         `json:"storedHash"`
	XP                   uint32                         `json:"xp"`
	Uncap                int                            `json:"uncap"`
	Mirage               int                            `json:"mirage"`
	Awakening            int                            `json:"awakening"`
	Transcendence        int                            `json:"transcendence"`
	ExactState           bool                           `json:"exactState,omitempty"`
	Flags                uint32                         `json:"flags,omitempty"`
	WrightstoneReference string                         `json:"wrightstoneReference,omitempty"`
	State                int                            `json:"state,omitempty"`
	SkillHashes          []string                       `json:"skillHashes"`
	SkillNames           []string                       `json:"skillNames,omitempty"`
	SkillLevels          []int                          `json:"skillLevels,omitempty"`
	Wrightstone          *LoadoutShareWeaponWrightstone `json:"wrightstone,omitempty"`
}
type LoadoutShareCharacterProgression struct {
	CharacterLevel             int                             `json:"characterLevel,omitempty"`
	BaseHP                     int                             `json:"baseHp,omitempty"`
	BaseATK                    int                             `json:"baseAtk,omitempty"`
	BaseStunBits               uint32                          `json:"baseStunBits,omitempty"`
	BaseCritRate               int                             `json:"baseCritRate,omitempty"`
	CharacterBaseCaptured      bool                            `json:"characterBaseCaptured,omitempty"`
	MasterTotalMSP             int                             `json:"masterTotalMsp"`
	LegacyProgress             int                             `json:"legacyProgress"`
	EnhancementPanel           []int                           `json:"enhancementPanel,omitempty"`
	EnhancementNodes           []LoadoutShareEnhancementNode   `json:"enhancementNodes,omitempty"`
	EnhancementNodeValues      []int                           `json:"enhancementNodeValues,omitempty"`
	Weapons                    []LoadoutShareProgressionWeapon `json:"weapons,omitempty"`
	WeaponWrightstonesCaptured bool                            `json:"weaponWrightstonesCaptured,omitempty"`
}
type LoadoutLogsSnapshot struct {
	Overmasteries []LoadoutLogsOvermastery `json:"overmasteries,omitempty"`
	IsOnline      bool                     `json:"isOnline,omitempty"`
}
type LoadoutLogsOvermastery struct {
	ID    uint32  `json:"id"`
	Flags uint32  `json:"flags"`
	Value float32 `json:"value"`
}

func ParseLoadoutShare(data []byte) (LoadoutShare, error) {
	if len(data) == 0 || len(data) > loadoutShareMaxSize {
		return LoadoutShare{}, fmt.Errorf("配装文件大小无效")
	}
	var s LoadoutShare
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.Format != loadoutShareFormat || s.Version < loadoutShareLegacyVersion || s.Version > loadoutShareVersion {
		return s, fmt.Errorf("不支持的配装格式或版本")
	}
	if len(s.Sigils) > loadoutShareMaxSigils || len(s.Skills) > loadoutShareMaxSkills || len(s.MasteryHashes) > loadoutShareMaxMastery {
		return s, fmt.Errorf("配装条目数量超过限制")
	}
	seen := map[int]bool{}
	for _, x := range s.Sigils {
		if !validLoadoutHash(x.Hash) || !validLoadoutHash(x.PrimaryTraitHash) || (x.SecondaryTraitHash != "" && !validLoadoutHash(x.SecondaryTraitHash)) {
			return s, fmt.Errorf("因子 hash 无效")
		}
		if s.Version >= 2 && (x.Index == nil || *x.Index < 0 || *x.Index >= loadoutShareMaxSigils || seen[*x.Index]) {
			return s, fmt.Errorf("因子索引无效")
		}
		if x.Index != nil {
			seen[*x.Index] = true
		}
	}
	if len(s.Summons) != 0 && len(s.Summons) != 4 {
		return s, fmt.Errorf("召唤石必须为 4 个")
	}
	return s, nil
}
func validLoadoutHash(x string) bool {
	_, e := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(x, "0x"), "0X"), 16, 32)
	return e == nil
}
func loadoutHex(x uint32) string     { return fmt.Sprintf("%08X", x) }
func loadoutName(x ...string) string { return strings.TrimSpace(strings.Join(x, " ")) }
