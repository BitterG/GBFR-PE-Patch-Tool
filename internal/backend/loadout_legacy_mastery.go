package backend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed data/ap_tree_panel_growth.json
var legacyMasteryPanelGrowthJSON []byte

const legacyMasteryStunPanelScale = 10.0

// LoadoutPermanentPanelStats keeps the game's raw stun unit separate from the
// number rendered by the character panel. Other three fields already use
// panel units in the 2.0.2 tables.
type LoadoutPermanentPanelStats struct {
	Attack    float64 `json:"attack"`
	HP        float64 `json:"hp"`
	CritRate  float64 `json:"critRate"`
	StunRaw   float64 `json:"stunRaw"`
	StunPanel float64 `json:"stunPanel"`
}

// LoadoutLegacyMasteryGrowth is the permanent four-tab 角色强化 layer. It is
// deliberately distinct from the 50 selectable R1/R2/R3/EX loadout nodes.
type LoadoutLegacyMasteryGrowth struct {
	OwnerCode        string                     `json:"ownerCode"`
	SaveProgress     int                        `json:"saveProgress"`
	RequiredProgress int                        `json:"requiredProgress"`
	Complete         bool                       `json:"complete"`
	Evidence         string                     `json:"evidence"`
	RuntimeObserved  bool                       `json:"runtimeObserved"`
	StableReads      int                        `json:"stableReads"`
	AttackTab        LoadoutPermanentPanelStats `json:"attackTab"`
	DefenseTab       LoadoutPermanentPanelStats `json:"defenseTab"`
	CollectionTab    LoadoutPermanentPanelStats `json:"collectionTab"`
	TranscendenceTab LoadoutPermanentPanelStats `json:"transcendenceTab"`
	Total            LoadoutPermanentPanelStats `json:"total"`
}

func legacyMasteryFromRuntimeAggregate(observed LoadoutPermanentPanelStats, overLimit []LoadoutOverLimitBonus) LoadoutPermanentPanelStats {
	result := observed
	for _, bonus := range overLimit {
		switch bonus.Name {
		case "最大HP":
			result.HP -= bonus.RawValue
		case "攻击力":
			result.Attack -= bonus.RawValue
		case "暴击率":
			result.CritRate -= bonus.RawValue
		case "昏厥值":
			result.StunRaw -= bonus.RawValue
		}
	}
	return finalizePermanentPanelStats(result)
}

type legacyMasteryWeaponState struct {
	InternalID    string
	Level         int
	Uncap         int
	Awakening     int
	Transcendence int
}

type legacyMasteryFixedTable struct {
	FullMSP  int     `json:"fullMsp"`
	Attack   float64 `json:"attack"`
	HP       float64 `json:"hp"`
	CritRate float64 `json:"critRate"`
	StunRaw  float64 `json:"stunRaw"`
}

type legacyMasteryWeaponNode struct {
	Source        string  `json:"source"`
	WeaponID      string  `json:"weaponId"`
	Effect        string  `json:"effect"`
	Value         float64 `json:"value"`
	Level         int     `json:"level"`
	Uncap         int     `json:"uncap"`
	Awakening     int     `json:"awakening"`
	Transcendence int     `json:"transcendence"`
	MSPCost       int     `json:"mspCost"`
	ParamKey      string  `json:"paramKey"`
}

type legacyMasteryCharacterTable struct {
	Attack      legacyMasteryFixedTable   `json:"attack"`
	Defense     legacyMasteryFixedTable   `json:"defense"`
	WeaponNodes []legacyMasteryWeaponNode `json:"weaponNodes"`
}

type legacyMasteryCatalog struct {
	Version     string                                 `json:"version"`
	GameVersion string                                 `json:"gameVersion"`
	Characters  map[string]legacyMasteryCharacterTable `json:"characters"`
}

var (
	legacyMasteryCatalogOnce sync.Once
	legacyMasteryCatalogData *legacyMasteryCatalog
	legacyMasteryCatalogErr  error
)

func loadLegacyMasteryCatalog() (*legacyMasteryCatalog, error) {
	legacyMasteryCatalogOnce.Do(func() {
		var catalog legacyMasteryCatalog
		if err := json.Unmarshal(legacyMasteryPanelGrowthJSON, &catalog); err != nil {
			legacyMasteryCatalogErr = fmt.Errorf("解析 2.0.2 角色强化四页目录失败: %w", err)
			return
		}
		if catalog.Version != "2.0.2-field-20260721" || catalog.GameVersion != "2.0.2" || len(catalog.Characters) == 0 {
			legacyMasteryCatalogErr = fmt.Errorf("角色强化四页目录版本或内容无效: %q/%q", catalog.Version, catalog.GameVersion)
			return
		}
		legacyMasteryCatalogData = &catalog
	})
	return legacyMasteryCatalogData, legacyMasteryCatalogErr
}

func fixedLegacyMasteryStats(row legacyMasteryFixedTable) LoadoutPermanentPanelStats {
	return finalizePermanentPanelStats(LoadoutPermanentPanelStats{
		Attack: row.Attack, HP: row.HP, CritRate: row.CritRate, StunRaw: row.StunRaw,
	})
}

func finalizePermanentPanelStats(stats LoadoutPermanentPanelStats) LoadoutPermanentPanelStats {
	stats.StunPanel = stats.StunRaw * legacyMasteryStunPanelScale
	return stats
}

func addPermanentPanelStats(left, right LoadoutPermanentPanelStats) LoadoutPermanentPanelStats {
	return finalizePermanentPanelStats(LoadoutPermanentPanelStats{
		Attack:   left.Attack + right.Attack,
		HP:       left.HP + right.HP,
		CritRate: left.CritRate + right.CritRate,
		StunRaw:  left.StunRaw + right.StunRaw,
	})
}

func legacyMasteryWeaponEligible(node legacyMasteryWeaponNode, weapon legacyMasteryWeaponState) bool {
	return strings.EqualFold(node.WeaponID, weapon.InternalID) &&
		weapon.Level >= node.Level && weapon.Uncap >= node.Uncap &&
		weapon.Awakening >= node.Awakening && weapon.Transcendence >= node.Transcendence
}

func addLegacyMasteryNode(stats *LoadoutPermanentPanelStats, node legacyMasteryWeaponNode) {
	switch node.Effect {
	case "attack":
		stats.Attack += node.Value
	case "hp":
		stats.HP += node.Value
	case "critRate":
		stats.CritRate += node.Value
	case "stunRaw":
		stats.StunRaw += node.Value
	}
	*stats = finalizePermanentPanelStats(*stats)
}

func deriveLegacyMasteryGrowth(ownerCode string, saveProgress int, weapons []legacyMasteryWeaponState) (LoadoutLegacyMasteryGrowth, error) {
	catalog, err := loadLegacyMasteryCatalog()
	if err != nil {
		return LoadoutLegacyMasteryGrowth{}, err
	}
	result := LoadoutLegacyMasteryGrowth{OwnerCode: ownerCode, SaveProgress: saveProgress}
	character, ok := catalog.Characters[ownerCode]
	if !ok {
		result.Evidence = "character-table-unavailable"
		return result, nil
	}

	result.RequiredProgress = character.Attack.FullMSP + character.Defense.FullMSP
	eligible := make([]legacyMasteryWeaponNode, 0, len(character.WeaponNodes))
	for _, node := range character.WeaponNodes {
		for _, weapon := range weapons {
			if legacyMasteryWeaponEligible(node, weapon) {
				eligible = append(eligible, node)
				result.RequiredProgress += node.MSPCost
				break
			}
		}
	}
	if saveProgress < result.RequiredProgress {
		result.Evidence = "partial-save-progress-unresolved"
		return result, nil
	}

	result.Complete = true
	result.Evidence = "extracted-2.0.2-tables+save-completion"
	result.AttackTab = fixedLegacyMasteryStats(character.Attack)
	result.DefenseTab = fixedLegacyMasteryStats(character.Defense)
	for _, node := range eligible {
		if node.Source == "collection" {
			addLegacyMasteryNode(&result.CollectionTab, node)
		} else if node.Source == "transcendence" {
			addLegacyMasteryNode(&result.TranscendenceTab, node)
		}
	}
	result.Total = addPermanentPanelStats(result.AttackTab, result.DefenseTab)
	result.Total = addPermanentPanelStats(result.Total, result.CollectionTab)
	result.Total = addPermanentPanelStats(result.Total, result.TranscendenceTab)
	return result, nil
}

func legacyMasteryWeaponStates(inventory *ProgressionInventory, ownerCode string) []legacyMasteryWeaponState {
	if inventory == nil {
		return nil
	}
	result := make([]legacyMasteryWeaponState, 0)
	for _, weapon := range inventory.Weapons {
		if weapon.OwnerCode != ownerCode || weapon.InternalID == "" {
			continue
		}
		result = append(result, legacyMasteryWeaponState{
			InternalID: weapon.InternalID, Level: weapon.Level, Uncap: weapon.Uncap,
			Awakening: weapon.Awakening, Transcendence: weapon.Transcendence,
		})
	}
	return result
}
