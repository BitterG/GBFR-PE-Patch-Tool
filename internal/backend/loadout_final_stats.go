package backend

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const loadoutFinalStatsScope = "人物属性以存档中的角色基础值、命运篇章与角色强化为固定基准；加成明细默认只汇总可随时更换的武器（含武器技能）、因子、专精、角色上限突破与召唤石，不含任务、队伍、临时状态及战斗内条件加成。"

const loadoutFinalStatsFormulaWarning = "角色基础值、命运篇章与角色强化固定基准优先按 2.0.2 运行时状态对象读取，离线时回退存档及游戏表；HP/攻击的任意草稿百分比乘区尚未覆盖全部组合，因此未实读草稿仍标记为估算。已确认普通面板聚合使用 float32 后四舍五入，重制终末武器独立末乘区再向零截断。"

// LoadoutPanelBonus is an unconditional value that is safe to apply to the
// compact character-panel stats. Conditional skillboard text is kept in
// the detailed mastery list, but deliberately does not enter this structure.
type LoadoutPanelBonus struct {
	Label        string  `json:"label"`
	Unit         string  `json:"unit"` // flat | pct
	Value        float64 `json:"value"`
	RawValue     float64 `json:"rawValue"`
	RawType      string  `json:"rawType,omitempty"`
	DisplayScale float64 `json:"displayScale"`
	Evidence     string  `json:"evidence,omitempty"`
	Source       string  `json:"source"`
}

type LoadoutDefenseZone struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Reduction float64 `json:"reduction"`
	Included  bool    `json:"included"`
	Condition string  `json:"condition,omitempty"`
	Evidence  string  `json:"evidence"`
}

type LoadoutDefenseModel struct {
	AssumedHPPercent float64              `json:"assumedHpPercent"`
	IncomingRate     float64              `json:"incomingRate"`
	Formula          string               `json:"formula"`
	Zones            []LoadoutDefenseZone `json:"zones"`
}

// LoadoutFinalStats mirrors the values shown by the in-game-style panel and
// exposes the separately audited, loadout-only defense percentage. GBFR does
// not expose an absolute defense number on that panel, so DefenseBonus must
// not be presented as a fabricated final defense stat.
// The three damage-cap fields remain available so the single compact value can
// use the conservative common increase while the detailed totals stay honest.
type LoadoutFinalStats struct {
	HP                int                  `json:"hp"`
	Attack            int                  `json:"attack"`
	CritRate          float64              `json:"critRate"`
	StunPower         float64              `json:"stunPower"`
	DefenseBonus      float64              `json:"defenseBonus"`
	DamageTakenRate   float64              `json:"damageTakenRate"`
	DefenseModel      *LoadoutDefenseModel `json:"defenseModel,omitempty"`
	DamageCap         float64              `json:"damageCap"`
	NormalDamageCap   float64              `json:"normalDamageCap"`
	AbilityDamageCap  float64              `json:"abilityDamageCap"`
	SkyboundDamageCap float64              `json:"skyboundDamageCap"`
	ChainDamageCap    float64              `json:"chainDamageCap"`
	Mode              string               `json:"mode"`
	Scope             string               `json:"scope"`
	FormulaVerified   bool                 `json:"formulaVerified"`
	Warnings          []string             `json:"warnings,omitempty"`
}

func defenseTraitComponent(bonus TraitBonus, match func(BonusComponent) bool) float64 {
	for _, component := range bonus.Components {
		if match(component) {
			return math.Abs(component.Value)
		}
	}
	return 0
}

func calculateLoadoutDefenseModel(bonuses []TraitBonus, unconditionalDefense float64) (*LoadoutDefenseModel, bool) {
	common := unconditionalDefense
	stoutHeart := float64(0)
	stronghold := float64(0)
	hasGarrison := false
	hasReferenceZone := false
	for _, bonus := range bonuses {
		switch bonus.TraitID {
		case "SKILL_096_00": // 坚持：受到的伤害-X%
			common += defenseTraitComponent(bonus, func(component BonusComponent) bool {
				return component.Unit == "pct" && strings.Contains(component.Label, "受到的伤害")
			})
		case "SKILL_141_00": // 钳蟹的报恩：无标签的百分比是减伤，最大HP另行计算
			common += defenseTraitComponent(bonus, func(component BonusComponent) bool {
				return component.Unit == "pct" && component.Label == ""
			})
		case "SKILL_044_00":
			// skill_status only records the persistent stout-heart state. The 25%
			// reduction comes from the supplied repeated-hit reference and remains
			// explicitly labelled as a candidate until a local A/B/A/B closes it.
			stoutHeart = 25
			hasReferenceZone = true
		case "SKILL_144_00": // 刚健：满血取表中的“效果最大时防御力”
			stronghold = defenseTraitComponent(bonus, func(component BonusComponent) bool {
				return component.Unit == "pct" && strings.Contains(component.Label, "效果最大")
			})
			hasReferenceZone = hasReferenceZone || stronghold > 0
		case "SKILL_036_00":
			hasGarrison = true
			hasReferenceZone = true
		}
	}

	zones := []LoadoutDefenseZone{
		{Key: "common", Label: "通用加算区", Reduction: common, Included: common != 0, Condition: "无条件防御、坚持与钳蟹减伤同区相加", Evidence: "2.0.2-table + Io runtime +5%"},
		{Key: "stout-heart", Label: "霸体乘区", Reduction: stoutHeart, Included: stoutHeart != 0, Condition: "装备霸体时", Evidence: "reference-candidate"},
		{Key: "stronghold", Label: "刚健乘区", Reduction: stronghold, Included: stronghold != 0, Condition: "静态草稿按 HP 100%", Evidence: "2.0.2-table + reference-zone"},
		{Key: "garrison", Label: "坚守乘区", Reduction: 0, Included: false, Condition: "随失去 HP 变化；满血静态草稿不计", Evidence: "2.0.2-table; runtime-curve-open"},
		{Key: "attack-down", Label: "攻击 DOWN 乘区", Reduction: 0, Included: false, Condition: "取决于受击敌人的当前弱化", Evidence: "battle-state-unavailable"},
		{Key: "independent", Label: "独立减伤区", Reduction: 0, Included: false, Condition: "伤害减免 Buff、祝福等需战斗状态", Evidence: "battle-state-unavailable"},
		{Key: "defense-up", Label: "防御 UP 乘区", Reduction: 0, Included: false, Condition: "当前 Buff 未由离线配装提供", Evidence: "battle-state-unavailable"},
	}
	if hasGarrison {
		zones[3].Condition = "已装备坚守；曲线未闭环，满血参考值暂不计入"
	}
	incomingRate := float64(1)
	for _, zone := range zones {
		if !zone.Included {
			continue
		}
		reduction := math.Max(0, math.Min(100, zone.Reduction))
		incomingRate *= 1 - reduction/100
	}
	return &LoadoutDefenseModel{
		AssumedHPPercent: 100,
		IncomingRate:     incomingRate * 100,
		Formula:          "同区相加，跨区相乘",
		Zones:            zones,
	}, hasReferenceZone
}

type loadoutPanelInputs struct {
	OwnerCode          string
	CharacterHP        float64
	CharacterATK       float64
	CharacterCritRate  float64
	CharacterStun      float64
	CharacterDamageCap float64
	WeaponHP           float64
	WeaponATK          float64
	WeaponCritRate     float64
	WeaponStun         float64
	Bonuses            []TraitBonus
	Mastery            []LoadoutPanelBonus
	OverLimit          []LoadoutOverLimitBonus
	Warnings           []string
}

// loadoutFactorCategoryCounts counts only each equipped sigil's primary
// trait. The four mastery synergies explicitly say “因子装备数量”; the
// reference calculator and the unpacked node layout both treat the primary
// factor slot as the counted category, not every secondary trait.
type loadoutFactorCategoryCounts struct {
	Basic          int
	Attack         int
	DefenseSupport int
}

func loadoutPrimaryFactorCategoryCounts(cat *Catalog, primaryHashes []uint32) loadoutFactorCategoryCounts {
	var counts loadoutFactorCategoryCounts
	if cat == nil {
		return counts
	}
	values := loadTraitValues()
	for _, hash := range primaryHashes {
		trait := cat.LookupTraitByHash(hash)
		if trait == nil {
			continue
		}
		definition := values[trait.InternalID]
		if definition == nil {
			continue
		}
		switch definition.CatLabel {
		case "基础能力":
			counts.Basic++
		case "攻击类":
			counts.Attack++
		case "防御类", "支援类":
			counts.DefenseSupport++
		}
	}
	return counts
}

var masteryPanelBonusPattern = regexp.MustCompile(`^(最大HP|攻击力|暴击率|昏厥值|防御力|伤害上限|攻击的伤害上限|普通攻击的伤害上限|普通攻击伤害上限|能力的伤害上限|能力伤害上限|奥义的伤害上限|奥义伤害上限|奥义连锁伤害上限|连锁伤害上限)([+-])([0-9]+(?:\.[0-9]+)?)(%)?$`)

// parseMasteryPanelBonus accepts only a complete, unconditional panel line.
// Text such as “花耀七闪的伤害上限…” or “中毒状态期间…” intentionally fails
// this anchored expression and remains visible only in the mastery details.
func parseMasteryPanelBonus(desc, source string) (LoadoutPanelBonus, bool) {
	match := masteryPanelBonusPattern.FindStringSubmatch(strings.TrimSpace(desc))
	if match == nil {
		return LoadoutPanelBonus{}, false
	}
	value, err := strconv.ParseFloat(match[3], 64)
	if err != nil {
		return LoadoutPanelBonus{}, false
	}
	if match[2] == "-" {
		value = -value
	}
	unit := "flat"
	if match[4] == "%" {
		unit = "pct"
	}
	panel := LoadoutPanelBonus{
		Label: match[1], Unit: unit, Value: value, RawValue: value,
		RawType: "table-number", DisplayScale: 1, Evidence: "2.0.2-table", Source: source,
	}
	if panel.Label == "昏厥值" && panel.Unit == "flat" {
		panel.RawType = "f32"
		panel.DisplayScale = legacyMasteryStunPanelScale
		panel.Value = panel.RawValue * panel.DisplayScale
		panel.Evidence = "2.0.2-table+runtime-display-scale"
	}
	return panel, true
}

func masteryCalculationRankCaps(growth LoadoutPermanentGrowth) (map[string]int, bool) {
	if growth.MasterTotalMSP == 0 {
		return map[string]int{"R1": 0, "R2": 0, "R3": 0, "EX": 0}, true
	}
	caps := make(map[string]int, len(growth.MasteryRankCaps))
	for rank, cap := range growth.MasteryRankCaps {
		caps[rank] = cap
	}
	return caps, false
}

// effectiveMasteryHexesForRankCaps separates the writable 50-slot draft from
// the nodes currently covered by the character's save-backed Master level.
// It deliberately does not mutate or reject overflow draft nodes.
func effectiveMasteryHexesForRankCaps(ownerCode string, hexes []string, caps map[string]int) ([]string, int, error) {
	used := map[string]int{"R1": 0, "R2": 0, "R3": 0, "EX": 0}
	effective := make([]string, 0, len(hexes))
	ignored := 0
	seen := make(map[uint32]bool, len(hexes))
	for _, value := range hexes {
		hash, err := ParseHashHex(value)
		if err != nil {
			return nil, 0, fmt.Errorf("专精节点 hash %q 无效: %w", value, err)
		}
		if hash == 0 || hash == EmptyHash {
			continue
		}
		if seen[hash] {
			return nil, 0, fmt.Errorf("专精节点 %08X 被重复配置", hash)
		}
		seen[hash] = true
		node, ok := skillboardNodeForHash(hash)
		if !ok {
			return nil, 0, fmt.Errorf("专精节点 %08X 未收录", hash)
		}
		if ownerCode != "" && node.Char != "" && node.Char != ownerCode {
			return nil, 0, fmt.Errorf("专精节点 %08X 属于 %s，不属于 %s", hash, node.Char, ownerCode)
		}
		rank, _, ok := masteryRankOfGrp(node.Grp)
		if !ok {
			continue
		}
		cap := max(0, caps[rank])
		if used[rank] >= cap {
			ignored++
			continue
		}
		used[rank]++
		effective = append(effective, value)
	}
	return effective, ignored, nil
}

func loadoutMasteryPanelBonuses(ownerCode string, hexes []string, factorCounts loadoutFactorCategoryCounts) ([]LoadoutPanelBonus, error) {
	bonuses := make([]LoadoutPanelBonus, 0)
	seen := make(map[uint32]bool, len(hexes))
	for _, value := range hexes {
		hash, err := ParseHashHex(value)
		if err != nil {
			return nil, fmt.Errorf("专精节点 hash %q 无效: %w", value, err)
		}
		if hash == 0 || hash == EmptyHash {
			continue
		}
		if seen[hash] {
			return nil, fmt.Errorf("专精节点 %08X 被重复配置", hash)
		}
		seen[hash] = true
		node, ok := skillboardNodeForHash(hash)
		if !ok {
			return nil, fmt.Errorf("专精节点 %08X 未收录", hash)
		}
		if ownerCode != "" && node.Char != "" && node.Char != ownerCode {
			return nil, fmt.Errorf("专精节点 %08X 属于 %s，不属于 %s", hash, node.Char, ownerCode)
		}
		rank, _, ok := masteryRankOfGrp(node.Grp)
		if !ok {
			continue
		}
		source := "专精 · " + masteryRankLabel(rank)
		if panel, ok := parseMasteryPanelBonus(node.Desc, source); ok {
			bonuses = append(bonuses, panel)
			continue
		}
		// These four strings are shared by every character's audited R3/EX
		// tables. Whitespace is layout-only, so normalize it before matching.
		// The HP line says “+10000%” in the Chinese table, but both its integer
		// parameter and the in-game calculator semantics are a flat +10000 HP.
		normalized := strings.Join(strings.Fields(node.Desc), "")
		synergySource := source + " · 因子联动"
		switch normalized {
		case "攻击力随攻击类因子装备数量增加而提升每装备1个相应因子+10%（5个因子时效果最大）":
			bonuses = append(bonuses, LoadoutPanelBonus{Label: "攻击力", Unit: "pct", Value: float64(min(factorCounts.Attack, 5) * 10), Source: synergySource})
		case "伤害上限随基础能力类因子装备数量增加而提升每装备1个相应因子+20%（5个因子时效果最大）":
			bonuses = append(bonuses, LoadoutPanelBonus{Label: "伤害上限", Unit: "pct", Value: float64(min(factorCounts.Basic, 5) * 20), Source: synergySource})
		case "最大HP随防御类因子和支援类因子装备数量增加而提升每装备1个相应因子+10000%（4个因子时效果最大）":
			bonuses = append(bonuses, LoadoutPanelBonus{Label: "最大HP", Unit: "flat", Value: float64(min(factorCounts.DefenseSupport, 4) * 10000), Source: synergySource})
		case "防御力随防御类因子和支援类因子装备数量增加而提升每装备1个相应因子+6%（5个因子时效果最大）":
			bonuses = append(bonuses, LoadoutPanelBonus{Label: "防御力", Unit: "pct", Value: float64(min(factorCounts.DefenseSupport, 5) * 6), Source: synergySource})
		}
	}
	return bonuses, nil
}

func addPanelBonusesToTotals(totals *[]EffectTotal, bonuses []LoadoutPanelBonus) {
	for _, bonus := range bonuses {
		catLabel := "专精"
		if bonus.Label == "攻击力" || strings.Contains(bonus.Label, "伤害上限") {
			catLabel = "攻击类"
		} else if bonus.Label == "最大HP" || bonus.Label == "暴击率" || bonus.Label == "昏厥值" {
			catLabel = "基础能力"
		} else if bonus.Label == "防御力" {
			catLabel = "防御类"
		}
		labels := []string{bonus.Label}
		if strings.Contains(bonus.Label, "伤害上限") {
			labels = canonicalEffectLabels(bonus.Label)
		}
		for _, label := range labels {
			addTotal(totals, label, bonus.Unit, bonus.Value, catLabel, bonus.Source)
		}
	}
}

func panelTraitAllowed(bonus TraitBonus) bool {
	switch bonus.TraitID {
	case "SKILL_313_00", "SKILL_316_00", "SKILL_317_00", "SKILL_318_00", "SKILL_319_00":
		return true
	}
	name := strings.TrimSpace(bonus.Name)
	switch strings.TrimSpace(name) {
	case "体力", "攻击力", "暴击率", "昏厥", "伤害上限",
		"守护", "金刚", "暴君", "刀上舞", "穷寇心",
		"终极钳蟹因子", "钳蟹的共鸣", "钳蟹的报恩",
		"天司长的灵威", "天星之界":
		return true
	default:
		return false
	}
}

func panelTraitPercentMultiplier(bonus TraitBonus, label string) bool {
	name := strings.TrimSpace(bonus.Name)
	if label == "最大HP" {
		switch name {
		case "守护", "金刚", "暴君", "钳蟹的报恩", "天星之界":
			return true
		}
	}
	if label == "攻击力" {
		if bonus.TraitID == "SKILL_313_00" {
			return true
		}
		// 暴君 is an unconditional trade-off. Conditional attack traits such as
		// Quick Charge, Stamina and Enmity never reach this branch.
		switch name {
		case "暴君", "刀上舞", "穷寇心":
			return true
		}
	}
	return false
}

func panelTraitFlatHP(name string) bool {
	return name == "体力" || name == "终极钳蟹因子"
}

func panelTraitFlatAttack(name string) bool {
	return name == "攻击力" || name == "钳蟹的共鸣"
}

func panelTraitDamageCap(bonus TraitBonus) bool {
	switch bonus.TraitID {
	case "SKILL_313_00", "SKILL_316_00", "SKILL_317_00", "SKILL_318_00", "SKILL_319_00":
		return true
	}
	name := strings.TrimSpace(bonus.Name)
	switch name {
	case "伤害上限", "刀上舞", "天司长的灵威", "天星之界":
		return true
	default:
		return false
	}
}

func addPanelCap(label string, value float64, normal, ability, skybound, chain *float64) {
	for _, canonical := range canonicalEffectLabels(label) {
		switch canonical {
		case "普通攻击伤害上限":
			*normal += value
		case "能力伤害上限":
			*ability += value
		case "奥义伤害上限":
			*skybound += value
		case "奥义连锁伤害上限":
			*chain += value
		}
	}
}

func calculateLoadoutFinalStats(input loadoutPanelInputs) LoadoutFinalStats {
	// The 2.0.2 aggregator uses scalar SS instructions throughout
	// (VCVTSI2SS/VADDSS/VMULSS) before VCVTTSS2SI, so preserve binary32
	// rounding at every arithmetic step instead of calculating in float64.
	hpFlat := float32(input.CharacterHP) + float32(input.WeaponHP)
	atkFlat := float32(input.CharacterATK) + float32(input.WeaponATK)
	crit := input.CharacterCritRate + input.WeaponCritRate
	stun := input.CharacterStun + input.WeaponStun
	defenseBonus := float64(0)
	hpMultiplier := float32(1)
	atkMultiplier := float32(1)
	masteryHPPct := float32(0)
	masteryATKPct := float32(0)
	normalCap, abilityCap, skyboundCap, chainCap := input.CharacterDamageCap, input.CharacterDamageCap, input.CharacterDamageCap, input.CharacterDamageCap
	var hpGatedTerminus *TraitBonus

	for index := range input.Bonuses {
		bonus := input.Bonuses[index]
		if bonus.TraitID == "SKILL_143_10" {
			hpGatedTerminus = &input.Bonuses[index]
			continue
		}
		// 剑圣的闪刃仅在剑神/分身召唤中生效，属于战斗内状态；
		// 效果仍保留在明细，但默认静态人物面板必须排除。
		if bonus.TraitID == "SKILL_170_01" {
			continue
		}
		name := strings.TrimSpace(bonus.Name)
		if bonus.TraitID != "SKILL_117_01" && !panelTraitAllowed(bonus) {
			continue
		}
		for _, component := range bonus.Components {
			if !component.Additive || component.Label == "" {
				continue
			}
			label := strings.TrimSpace(component.Label)
			switch {
			case bonus.TraitID == "SKILL_117_01" && component.Unit == "pct" && strings.Contains(label, "伤害上限"):
				addPanelCap(label, component.Value, &normalCap, &abilityCap, &skyboundCap, &chainCap)
			case panelTraitFlatHP(name) && label == "最大HP" && component.Unit == "flat":
				hpFlat += float32(component.Value)
			case panelTraitFlatAttack(name) && label == "攻击力" && component.Unit == "flat":
				atkFlat += float32(component.Value)
			case name == "暴击率" && label == "暴击率" && component.Unit == "pct":
				crit += component.Value
			case name == "昏厥" && label == "昏厥值" && component.Unit == "flat":
				stun += component.Value
			case panelTraitDamageCap(bonus) && component.Unit == "pct" && strings.Contains(label, "伤害上限"):
				addPanelCap(label, component.Value, &normalCap, &abilityCap, &skyboundCap, &chainCap)
			case component.Unit == "pct" && panelTraitPercentMultiplier(bonus, label):
				if label == "最大HP" {
					hpMultiplier *= 1 + float32(component.Value)/100
				} else if label == "攻击力" {
					atkMultiplier *= 1 + float32(component.Value)/100
				}
			}
		}
	}

	for _, bonus := range input.Mastery {
		switch {
		case bonus.Label == "最大HP" && bonus.Unit == "flat":
			hpFlat += float32(bonus.Value)
		case bonus.Label == "最大HP" && bonus.Unit == "pct":
			masteryHPPct += float32(bonus.Value)
		case bonus.Label == "攻击力" && bonus.Unit == "flat":
			atkFlat += float32(bonus.Value)
		case bonus.Label == "攻击力" && bonus.Unit == "pct":
			masteryATKPct += float32(bonus.Value)
		case bonus.Label == "暴击率" && bonus.Unit == "pct":
			crit += bonus.Value
		case bonus.Label == "昏厥值" && bonus.Unit == "flat":
			stun += bonus.Value
		case bonus.Label == "防御力" && bonus.Unit == "pct":
			defenseBonus += bonus.Value
		case bonus.Unit == "pct" && strings.Contains(bonus.Label, "伤害上限"):
			addPanelCap(bonus.Label, bonus.Value, &normalCap, &abilityCap, &skyboundCap, &chainCap)
		}
	}

	for _, bonus := range input.OverLimit {
		switch {
		case bonus.Name == "最大HP" && bonus.Unit == "flat":
			hpFlat += float32(bonus.Value)
		case bonus.Name == "攻击力" && bonus.Unit == "flat":
			atkFlat += float32(bonus.Value)
		case bonus.Name == "暴击率" && bonus.Unit == "pct":
			crit += bonus.Value
		case bonus.Name == "昏厥值" && bonus.Unit == "flat":
			stun += bonus.Value
		case bonus.Name == "防御力" && bonus.Unit == "pct":
			defenseBonus += bonus.Value
		case bonus.Unit == "pct" && strings.Contains(bonus.Name, "伤害上限"):
			addPanelCap(bonus.Name, bonus.Value, &normalCap, &abilityCap, &skyboundCap, &chainCap)
		}
	}

	hpMultiplier *= 1 + masteryHPPct/100
	atkMultiplier *= 1 + masteryATKPct/100
	finalHP := int(math.Round(float64(hpFlat * hpMultiplier)))
	// The ordinary attack producer rounds to its integer panel subtotal before
	// the HP-gated rebuilt Terminus multiplier is applied. Io's observed 2.0.2
	// path is round(29496*2.3)=67841, then trunc(67841*1.64)=111259.
	attackSubtotal := atkFlat * atkMultiplier
	finalAttack := int(math.Round(float64(attackSubtotal)))
	if hpGatedTerminus != nil && hpGatedTerminus.MaxHPCondition > 0 && float64(finalHP) <= hpGatedTerminus.MaxHPCondition {
		finalAttack = int(math.Round(float64(attackSubtotal)))
		for _, component := range hpGatedTerminus.Components {
			if !component.Additive || component.Unit != "pct" {
				continue
			}
			switch component.Label {
			case "攻击力":
				finalAttack = int(float32(finalAttack) * (1 + float32(component.Value)/100))
			case "普通攻击伤害上限", "能力伤害上限", "奥义伤害上限", "伤害上限":
				addPanelCap(component.Label, component.Value, &normalCap, &abilityCap, &skyboundCap, &chainCap)
			}
		}
	}
	// Keep the legacy compact value as the common increase shared by normal,
	// ability and SBA. Chain Burst has separate effects in the 2.0.2 tables and
	// is therefore shown only in its dedicated field until generic-cap sharing
	// is proven by a runtime sample.
	commonCap := math.Min(normalCap, math.Min(abilityCap, skyboundCap))
	warnings := append([]string(nil), input.Warnings...)
	for _, bonus := range input.Bonuses {
		warning := strings.TrimSpace(bonus.Warning)
		if warning == "" {
			continue
		}
		seen := false
		for _, existing := range warnings {
			if existing == warning {
				seen = true
				break
			}
		}
		if !seen {
			warnings = append(warnings, warning)
		}
	}
	defenseModel, defenseReferenceCandidate := calculateLoadoutDefenseModel(input.Bonuses, defenseBonus)
	if defenseReferenceCandidate {
		warnings = append(warnings, "防御分区按同区相加、跨区相乘生成满血参考值；霸体 25% 与刚健/坚守乘区仍需本机重复受击样本闭环。")
	}
	warnings = append(warnings, loadoutFinalStatsFormulaWarning)
	return LoadoutFinalStats{
		HP:                finalHP,
		Attack:            finalAttack,
		CritRate:          crit,
		StunPower:         stun,
		DefenseBonus:      defenseBonus,
		DamageTakenRate:   defenseModel.IncomingRate,
		DefenseModel:      defenseModel,
		DamageCap:         commonCap,
		NormalDamageCap:   normalCap,
		AbilityDamageCap:  abilityCap,
		SkyboundDamageCap: skyboundCap,
		ChainDamageCap:    chainCap,
		Mode:              "permanent-baseline+changeable-loadout",
		Scope:             loadoutFinalStatsScope,
		// The final scalar-float32 aggregation and toward-zero conversions are
		// audited. The producer that turns percentage effects into the absolute
		// contributions consumed by that aggregator is not yet closed, so this
		// loadout-only comparison must remain explicitly estimated.
		FormulaVerified: false,
		Warnings:        warnings,
	}
}
