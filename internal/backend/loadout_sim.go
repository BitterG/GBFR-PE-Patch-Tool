package backend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// ── 配装模拟器：把一套配装的因子换算成「词条加成汇总」──────────────────
//
// 规则（来自游戏原表 skill_status，并由回归测试覆盖）：
//  1. 逐颗因子取出主词条(hash,等级)+副词条(hash,等级)。
//  2. 按词条 internalId 分组，把同名词条所有出现的等级**相加**，封顶到该词条 maxLevel。
//  3. 用合并后等级查 trait_values.json，按格式串占位符 {N}→LevelValue(N+1) 取数值与单位（带%为百分比）。
//
// 该层负责可审计的因子→词条聚合；更上层再将武器、召唤石、专精与角色固定基准分轨汇总。
// 离线最终值在百分比乘区与中间 float32 量化顺序尚未闭环时必须保留估算标记。

//go:embed data/trait_values.json
var traitValuesJSON []byte

//go:embed data/trait_values_202.json
var traitValues202JSON []byte

//go:embed data/summon_main_trait_values_202.json
var summonMainTraitValues202JSON []byte

type traitPlaceholder struct {
	Ph     int       `json:"ph"`
	Col    int       `json:"col"`
	Unit   string    `json:"unit"` // "pct" | "flat"
	Values []float64 `json:"values"`
}

type traitValueDef struct {
	Name              string             `json:"name"`
	Cat               *int               `json:"cat"`
	CatLabel          string             `json:"catLabel"`
	MaxLevel          int                `json:"maxLevel"`
	AggregationPolicy string             `json:"aggregationPolicy,omitempty"` // empty=syntax-derived; detail_only=never enter static totals
	Format            string             `json:"format"`
	Placeholders      []traitPlaceholder `json:"placeholders"`
}

const traitAggregationDetailOnly = "detail_only"

var (
	traitValuesOnce sync.Once
	traitValuesByID map[string]*traitValueDef

	summonMainSkillNamesOnce sync.Once
	summonMainSkillNames     map[uint32]string
)

func loadSummonMainSkillNames() map[uint32]string {
	summonMainSkillNamesOnce.Do(func() {
		summonMainSkillNames = map[uint32]string{}
		var payload summonSkillFile
		if err := json.Unmarshal(summonSkillsJSON, &payload); err != nil {
			return
		}
		for _, skill := range payload.Skills {
			hash, err := ParseHashHex(skill.Hash)
			if err == nil {
				summonMainSkillNames[hash] = strings.TrimSpace(skill.DisplayName)
			}
		}
	})
	return summonMainSkillNames
}

func loadTraitValues() map[string]*traitValueDef {
	traitValuesOnce.Do(func() {
		var payload struct {
			Traits map[string]*traitValueDef `json:"traits"`
		}
		traitValuesByID = map[string]*traitValueDef{}
		if err := json.Unmarshal(traitValuesJSON, &payload); err != nil {
			return
		}
		traitValuesByID = payload.Traits
		for _, data := range [][]byte{traitValues202JSON, summonMainTraitValues202JSON} {
			var supplement struct {
				Traits      map[string]*traitValueDef `json:"traits"`
				HashAliases map[string]string         `json:"hashAliases"`
			}
			if err := json.Unmarshal(data, &supplement); err == nil {
				for id, definition := range supplement.Traits {
					traitValuesByID[id] = definition
				}
				for hash, id := range supplement.HashAliases {
					if definition := traitValuesByID[id]; definition != nil {
						traitValuesByID[hash] = definition
					}
				}
			}
		}
		// DLC 2.0 gives the rebuilt terminus skill its own skill_status row.
		// It is not the old SKILL_143_00 curve. The non-zero Lv25-Lv35 tail is
		// copied directly from the local 2.0 skill_status.tbl; at Lv30 the five
		// values are 60, 430, 430, 430 and 150000 respectively.
		levelValues := func(tail ...float64) []float64 {
			values := make([]float64, 35)
			copy(values[24:], tail)
			return values
		}
		attackCategory := 1
		traitValuesByID["SKILL_143_10"] = &traitValueDef{
			Name: "浩劫", Cat: &attackCategory, CatLabel: "攻击类", MaxLevel: 35,
			Format: "最大HP不超过{4:.1f}时\n攻击力+{0:.1f}%\n普通攻击伤害上限+{1:.1f}% / 能力伤害上限+{2:.1f}% / 奥义伤害上限+{3:.1f}%",
			Placeholders: []traitPlaceholder{
				{Ph: 0, Col: 1, Unit: "pct", Values: levelValues(50, 52, 54, 56, 58, 60, 62, 64, 66, 68, 70)},
				{Ph: 1, Col: 2, Unit: "pct", Values: levelValues(100, 250, 400, 410, 420, 430, 440, 450, 460, 470, 500)},
				{Ph: 2, Col: 3, Unit: "pct", Values: levelValues(100, 250, 400, 410, 420, 430, 440, 450, 460, 470, 500)},
				{Ph: 3, Col: 4, Unit: "pct", Values: levelValues(100, 250, 400, 410, 420, 430, 440, 450, 460, 470, 500)},
				{Ph: 4, Col: 5, Unit: "flat", Values: levelValues(45000, 110000, 120000, 130000, 140000, 150000, 160000, 170000, 180000, 190000, 200000)},
			},
		}
		// The local trait/summon catalogs spell this verified Io trait 伶俐;
		// retain its skill_status values while fixing the older text typo.
		if mageSavvy := traitValuesByID["SKILL_117_01"]; mageSavvy != nil {
			mageSavvy.Name = "魔法师的伶俐"
		}
		// LevelValue2 is an internal trigger flag, not the cooldown. The 2.0.2
		// skill_status row stores the cooldown in LevelValue3.
		if guts := traitValuesByID["SKILL_045_00"]; guts != nil {
			guts.Name = "豪胆"
			guts.Format = "受到致命伤害时HP保持1\n再次触发等待{2}秒"
			guts.Placeholders = []traitPlaceholder{{
				Ph: 2, Col: 3, Unit: "flat",
				Values: []float64{230, 225, 220, 215, 210, 205, 200, 195, 190, 185, 180, 175, 170, 165, 160, 155, 150, 140, 130, 120},
			}}
		}
		if autorevive := traitValuesByID["SKILL_068_00"]; autorevive != nil {
			autorevive.Name = "自动复活"
			autorevive.Format = "HP归零时自动复活\n再次触发等待{2}秒"
			autorevive.Placeholders = []traitPlaceholder{{
				Ph: 2, Col: 3, Unit: "flat",
				Values: []float64{230, 225, 220, 215, 210, 205, 200, 195, 190, 185, 180, 175, 170, 165, 160, 155, 150, 140, 130, 120},
			}}
		}
		if factorBooster := traitValuesByID["SKILL_113_00"]; factorBooster != nil {
			factorBooster.Name = "因子强化"
			factorBooster.AggregationPolicy = traitAggregationDetailOnly
			factorBooster.Format = "技能等级+{0}"
			factorBooster.Placeholders = []traitPlaceholder{{
				Ph: 0, Col: 1, Unit: "flat", Values: []float64{1, 2},
			}}
		}
	})
	return traitValuesByID
}

// TraitBonus 是模拟器输出的一行：某词条的合并加成。
type TraitBonus struct {
	TraitID    string           `json:"traitId"`
	Name       string           `json:"name"`
	CatLabel   string           `json:"catLabel"`
	Level      int              `json:"level"`    // 合并后（封顶后）等级
	RawLevel   int              `json:"rawLevel"` // 封顶前的等级之和
	MaxLevel   int              `json:"maxLevel"`
	Capped     bool             `json:"capped"`
	Effect     string           `json:"effect"`     // 渲染好的中文效果串（数值已代入）
	Components []BonusComponent `json:"components"` // 结构化数值，供聚合（如总HP）
	Sources    []string         `json:"sources,omitempty"`
	Warning    string           `json:"warning,omitempty"`
	// MaxHPCondition is non-zero only when the current panel can determine an
	// HP-gated skill. It is deliberately structured instead of inferred from
	// localized effect text.
	MaxHPCondition float64 `json:"maxHpCondition,omitempty"`
}

// BonusComponent 是一个词条效果里的一个数值分量。
type BonusComponent struct {
	Label             string  `json:"label"`
	Unit              string  `json:"unit"` // pct | flat
	Value             float64 `json:"value"`
	Additive          bool    `json:"additive"`
	AggregationPolicy string  `json:"aggregationPolicy,omitempty"`
}

// EffectTotal 是右侧“总计加成”的一行。只聚合同一语义、格式串明确使用 +/-，
// 且定义策略允许静态聚合的数值；触发阈值、持续时间和未知战斗条件不会被误算。
type EffectTotal struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Unit     string   `json:"unit"`
	Value    float64  `json:"value"`
	CatLabel string   `json:"catLabel"`
	Sources  []string `json:"sources"`
}

// LoadoutSimulation 同时保留合并后的总计与逐词条明细。
type LoadoutSimulation struct {
	Totals        []EffectTotal         `json:"totals"`
	DynamicTotals []EffectTotal         `json:"dynamicTotals"`
	Bonuses       []TraitBonus          `json:"bonuses"`
	FinalStats    *LoadoutFinalStats    `json:"finalStats,omitempty"`
	Weapon        *LoadoutWeaponContext `json:"weapon,omitempty"`
	WeaponSkills  []LoadoutWeaponSkill  `json:"weaponSkills"`
}

// SimSigilInput 是模拟器的单颗因子输入（主/副词条 hash + 等级）。
type SimSigilInput struct {
	PrimaryHash    uint32
	PrimaryLevel   int
	SecondaryHash  uint32
	SecondaryLevel int
}

// simulateTraits 把若干 (词条hash,等级) 聚合成加成汇总。
// hashToID：词条 hash → internalId（来自 catalog.traitByHash）。
func simulateTraits(pairs []struct {
	hash  uint32
	level int
}, hashToID map[uint32]string) []TraitBonus {
	tv := loadTraitValues()
	summonNames := loadSummonMainSkillNames()

	// 1. 按 internalId 分组累加等级
	sumByID := map[string]int{}
	hashByID := map[string]uint32{}
	order := []string{}
	for _, p := range pairs {
		if p.hash == EmptyHash || p.hash == 0 || p.level <= 0 {
			continue
		}
		id := resolveTraitValueID(p.hash, hashToID)
		if id == "" {
			continue // 未知词条，跳过
		}
		if _, seen := sumByID[id]; !seen {
			order = append(order, id)
			hashByID[id] = p.hash
		}
		sumByID[id] += p.level
	}

	var out []TraitBonus
	for _, id := range order {
		def := tv[id]
		hash := hashByID[id]
		summonName, isSummonMain := summonNames[hash]
		if def == nil {
			if isSummonMain {
				out = append(out, TraitBonus{
					TraitID:  id,
					Name:     summonName,
					Level:    sumByID[id],
					RawLevel: sumByID[id],
					Warning:  fmt.Sprintf("召唤石主技能 %s（0x%08X）缺少经过本地 2.0.2 表验证的效果定义，未计入模拟。", summonName, hash),
				})
			}
			continue
		}
		raw := sumByID[id]
		lv := raw
		capped := false
		if def.MaxLevel > 0 && lv > def.MaxLevel {
			lv = def.MaxLevel
			capped = true
		}
		name := strings.TrimSpace(def.Name)
		if name == "" {
			name = strings.TrimSpace(localizedRuntimeName(hash))
		}
		if isSummonMain && strings.TrimSpace(summonName) != "" {
			name = summonName
		}
		b := TraitBonus{
			TraitID: id, Name: name, CatLabel: def.CatLabel,
			Level: lv, RawLevel: raw, MaxLevel: def.MaxLevel, Capped: capped,
		}
		if isSummonMain && (name == "" || strings.TrimSpace(def.Format) == "") {
			b.Warning = fmt.Sprintf("召唤石主技能 %s（0x%08X）的本地 2.0.2 效果定义缺少名称或格式，未计入模拟。", summonName, hash)
			out = append(out, b)
			continue
		}
		if unverifiedTraitFormat(def.Format) {
			b.Warning = fmt.Sprintf("%s（0x%08X）的效果定义尚未闭环：缺少本地 2.0.2 文本表证据，暂不计入模拟。", name, hash)
			out = append(out, b)
			continue
		}
		b.Effect, b.Components = renderTraitEffect(def, lv)
		if id == "SKILL_143_10" {
			for _, placeholder := range def.Placeholders {
				if placeholder.Ph == 4 && lv > 0 && lv <= len(placeholder.Values) {
					b.MaxHPCondition = placeholder.Values[lv-1]
				}
			}
		}
		out = append(out, b)
	}
	// 攻击类靠前、按分类再按等级排
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := catRank(out[i].CatLabel), catRank(out[j].CatLabel)
		if ci != cj {
			return ci < cj
		}
		return out[i].Level > out[j].Level
	})
	return out
}

func unverifiedTraitFormat(format string) bool {
	format = strings.TrimSpace(format)
	return format == "" || strings.Contains(format, "亚亚亚亚")
}

func resolveTraitValueID(hash uint32, hashToID map[uint32]string) string {
	id := hashToID[hash]
	rawID := fmt.Sprintf("%08X", hash)
	values := loadTraitValues()
	// DLC hashes can have a save-catalog ID and a newer audited value-table
	// alias. Resolve by the exact hash join used by simulateTraits so source
	// attribution and level aggregation always land on the same canonical row.
	if (id == "" || values[id] == nil) && values[rawID] != nil {
		id = rawID
	}
	// A summon catalog entry with no value row must still survive the join so
	// the final panel can explain that it was deliberately left out.
	if id == "" {
		if _, isSummonMain := loadSummonMainSkillNames()[hash]; isSummonMain {
			id = rawID
		}
	}
	return canonicalTraitValueID(id)
}

func canonicalTraitValueID(id string) string {
	if strings.HasPrefix(id, "SKILL_") {
		return id
	}
	definition := loadTraitValues()[id]
	if definition == nil {
		return id
	}
	canonical := ""
	for candidate, candidateDefinition := range loadTraitValues() {
		if candidateDefinition != definition || !strings.HasPrefix(candidate, "SKILL_") {
			continue
		}
		if canonical == "" || candidate < canonical {
			canonical = candidate
		}
	}
	if canonical != "" {
		return canonical
	}
	return id
}

func catRank(label string) int {
	switch label {
	case "攻击类":
		return 0
	case "基础能力":
		return 1
	case "防御类":
		return 2
	case "支援类":
		return 3
	default:
		return 4
	}
}

// renderTraitEffect 把格式串在指定等级代入数值，返回中文效果串 + 结构化分量。
func renderTraitEffect(def *traitValueDef, level int) (string, []BonusComponent) {
	if level < 1 {
		return "", nil
	}
	idx := level - 1
	// 占位符 ph -> 该级数值
	valByPh := map[int]float64{}
	var comps []BonusComponent
	for _, p := range def.Placeholders {
		v := 0.0
		if idx >= 0 && idx < len(p.Values) {
			v = p.Values[idx]
		}
		v *= placeholderFormatScale(def.Format, p.Ph)
		valByPh[p.Ph] = v
		label, sign, additive := additivePlaceholderMeta(def.Format, p.Ph)
		if sign < 0 {
			v = -v
		}
		if def.AggregationPolicy == traitAggregationDetailOnly {
			additive = false
		}
		comps = append(comps, BonusComponent{
			Label: label, Unit: p.Unit, Value: v, Additive: additive,
			AggregationPolicy: def.AggregationPolicy,
		})
	}
	return stripMarkup(substituteFormat(def.Format, valByPh)), comps
}

// placeholderFormatScale handles the numeric scale syntax emitted by the
// game's text table. It is distinct from precision syntax such as :.1f:
// `昏厥值+{0:10}` means that the stored 0.5..10 curve is displayed and
// aggregated as 5..100. Unknown format specs deliberately keep scale 1.
func placeholderFormatScale(format string, ph int) float64 {
	marker := fmt.Sprintf("{%d:", ph)
	start := strings.Index(format, marker)
	if start < 0 {
		return 1
	}
	specStart := start + len(marker)
	relEnd := strings.IndexByte(format[specStart:], '}')
	if relEnd < 0 {
		return 1
	}
	spec := strings.TrimSpace(format[specStart : specStart+relEnd])
	if spec == "" || strings.ContainsAny(spec, ".fFeEgG") {
		return 1
	}
	scale, err := strconv.ParseFloat(spec, 64)
	if err != nil || scale <= 0 {
		return 1
	}
	return scale
}

// additivePlaceholderMeta 从“冷却时间-{0:.1f}%”中提取“冷却时间”和负号。
// 只有占位符前紧邻明确的 + / - 才可参加总计，避免把“HP 高于 {0} 时”之类阈值相加。
func additivePlaceholderMeta(format string, ph int) (label string, sign float64, additive bool) {
	plain := stripMarkup(format)
	marker := fmt.Sprintf("{%d", ph)
	start := strings.Index(plain, marker)
	if start < 0 {
		return "", 1, false
	}
	prefix := []rune(plain[:start])
	i := len(prefix) - 1
	for i >= 0 && unicode.IsSpace(prefix[i]) {
		i--
	}
	if i < 0 || (prefix[i] != '+' && prefix[i] != '-') {
		return "", 1, false
	}
	sign = 1
	if prefix[i] == '-' {
		sign = -1
	}
	prefix = prefix[:i]
	cut := -1
	for j := len(prefix) - 1; j >= 0; j-- {
		switch prefix[j] {
		case '\n', '\r', '/', '；', ';', '。', '，', ',', '}':
			cut = j
			j = -1
		}
	}
	label = strings.TrimSpace(string(prefix[cut+1:]))
	label = strings.TrimLeftFunc(label, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("·•:：()（）[]【】", r)
	})
	if label == "" {
		return "", sign, false
	}
	return label, sign, true
}

// aggregateTraitEffects 合并不同词条带来的同类可加效果，供右侧总计展示。
func aggregateTraitEffects(bonuses []TraitBonus) []EffectTotal {
	byKey := map[string]*EffectTotal{}
	order := make([]string, 0)
	for _, bonus := range bonuses {
		for _, component := range bonus.Components {
			if !component.Additive || component.AggregationPolicy == traitAggregationDetailOnly || component.Label == "" {
				continue
			}
			for _, label := range canonicalEffectLabels(component.Label) {
				key := component.Unit + "|" + label
				total := byKey[key]
				if total == nil {
					total = &EffectTotal{
						Key:      key,
						Label:    label,
						Unit:     component.Unit,
						CatLabel: bonus.CatLabel,
					}
					byKey[key] = total
					order = append(order, key)
				}
				total.Value += component.Value
				sources := bonus.Sources
				if len(sources) == 0 && bonus.Name != "" {
					sources = []string{bonus.Name}
				}
				for _, source := range sources {
					seen := false
					for _, existing := range total.Sources {
						if existing == source {
							seen = true
							break
						}
					}
					if !seen && source != "" {
						total.Sources = append(total.Sources, source)
					}
				}
			}
		}
	}
	out := make([]EffectTotal, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := catRank(out[i].CatLabel), catRank(out[j].CatLabel)
		if ci != cj {
			return ci < cj
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func canonicalEffectLabels(label string) []string {
	switch strings.TrimSpace(label) {
	case "伤害上限":
		// 游戏里的无前缀上限同时作用于普通攻击、能力和奥义；展开后才能和
		// α/β/γ秘纹以及“伤害上限”词条得到真正有用的三项总数。
		return []string{"普通攻击伤害上限", "能力伤害上限", "奥义伤害上限"}
	case "攻击和攻击的伤害上限", "攻击的伤害上限", "普通攻击的伤害上限", "普通攻击伤害上限":
		return []string{"普通攻击伤害上限"}
	case "能力的伤害上限", "能力伤害上限":
		return []string{"能力伤害上限"}
	case "奥义的伤害上限", "奥义伤害上限":
		return []string{"奥义伤害上限"}
	case "奥义连锁伤害上限", "连锁伤害上限":
		return []string{"奥义连锁伤害上限"}
	default:
		return []string{strings.TrimSpace(label)}
	}
}

// stripMarkup 去掉游戏富文本标记（如 <d>…</d> 的属性色标签），只留纯文本。
func stripMarkup(s string) string {
	out := make([]rune, 0, len(s))
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			out = append(out, r)
		}
	}
	return string(out)
}

// substituteFormat 把 "攻击力+{0}" / "攻击力+{1:.1f}%" 里的 {N}[:spec] 替换成数值。
func substituteFormat(format string, valByPh map[int]float64) string {
	out := make([]rune, 0, len(format))
	runes := []rune(format)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '{' {
			out = append(out, runes[i])
			continue
		}
		// 找到 '}'
		j := i + 1
		for j < len(runes) && runes[j] != '}' {
			j++
		}
		if j >= len(runes) {
			out = append(out, runes[i])
			continue
		}
		inner := string(runes[i+1 : j]) // 如 "0" 或 "1:.1f"
		ph := 0
		oneDecimal := false
		// 解析 ph 与是否 .1f
		n := 0
		k := 0
		for k < len(inner) && inner[k] >= '0' && inner[k] <= '9' {
			n = n*10 + int(inner[k]-'0')
			k++
		}
		ph = n
		if k < len(inner) && inner[k] == ':' {
			if containsSub(inner[k:], ".1f") {
				oneDecimal = true
			}
		}
		v := valByPh[ph]
		out = append(out, []rune(formatNum(v, oneDecimal))...)
		i = j
	}
	return string(out)
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func formatNum(v float64, oneDecimal bool) string {
	if oneDecimal {
		return fmt.Sprintf("%.1f", v)
	}
	// 整数则不带小数
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

// buildTraitHashToID 从 catalog 建「词条 hash -> internalId」映射（供模拟器 join trait_values）。
func buildTraitHashToID(cat *Catalog) map[uint32]string {
	m := map[uint32]string{}
	if cat == nil {
		return m
	}
	for i := range cat.Traits {
		if h, err := ParseHashHex(cat.Traits[i].Hash); err == nil {
			m[h] = cat.Traits[i].InternalID
		}
	}
	return m
}

// readSigilTraits 读一颗因子（因子槽 UnitID）的主/副词条 (hash, level)。
func readSigilTraits(save *SaveData, gemUnitID uint32) (primHash uint32, primLv int, secHash uint32, secLv int) {
	gemIndex := int(gemUnitID) - GemSlotBaseID
	if gemIndex < 0 {
		return
	}
	primUnit := uint32(TraitSlotBase + gemIndex*100)
	secUnit := primUnit + 1
	primHash, primLv = readSigilTraitUnit(save, primUnit)
	secHash, secLv = readSigilTraitUnit(save, secUnit)
	return
}

// readSigilTraitUnit joins the hash and level records for one trait UnitID.
// Some saves retain duplicate records after character/loadout migration; the
// first matching record can be stale and may contain a negative sentinel level.
// Prefer the last valid pair, which is the record written by the current save.
func readSigilTraitUnit(save *SaveData, unitID uint32) (uint32, int) {
	if save == nil {
		return 0, 0
	}
	var hashes, levels []*unitEntry
	for _, candidate := range save.findAllUnitsByType(TraitHashIDType) {
		if candidate.UnitID != unitID || candidate.ValueCnt != 1 {
			continue
		}
		hashes = append(hashes, candidate)
	}
	for _, levelEntry := range save.findAllUnitsByType(TraitLevelIDType) {
		if levelEntry.UnitID != unitID || levelEntry.ValueCnt != 1 {
			continue
		}
		levels = append(levels, levelEntry)
	}
	limit := len(hashes)
	if len(levels) < limit {
		limit = len(levels)
	}
	for index := limit - 1; index >= 0; index-- {
		candidateHash := hashes[index].Uint32()
		candidateLevel := int(levels[index].Int32())
		if candidateHash == 0 || candidateLevel < 0 {
			continue
		}
		return candidateHash, candidateLevel
	}
	// If an old save has unpaired duplicate records, retain the last valid
	// hash/level independently rather than exposing the stale negative value.
	var hash uint32
	for index := len(hashes) - 1; index >= 0; index-- {
		if candidateHash := hashes[index].Uint32(); candidateHash != 0 {
			hash = candidateHash
			break
		}
	}
	for index := len(levels) - 1; index >= 0; index-- {
		if candidateLevel := int(levels[index].Int32()); candidateLevel >= 0 && hash != 0 {
			return hash, candidateLevel
		}
	}
	return 0, 0
}

// LoadoutSimulate 对一组因子 SlotID 计算「词条加成总和」。
// sigilSlotIDs 是 1403 里存的因子 SlotID（前端编辑器当前勾选的因子）。
func (a *App) LoadoutSimulate(path string, sigilSlotIDs []uint32) ([]TraitBonus, error) {
	cat, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	ix := buildLoadoutIndex(save)
	hashToID := buildTraitHashToID(cat)

	pairs := collectStoredSigilPairs(save, ix, sigilSlotIDs)
	return simulateTraits(pairs, hashToID), nil
}

func collectStoredSigilPairs(save *SaveData, ix *loadoutIndex, sigilSlotIDs []uint32) []struct {
	hash  uint32
	level int
} {
	var pairs []struct {
		hash  uint32
		level int
	}
	for _, sid := range sigilSlotIDs {
		gu, ok := ix.gemBySlotID[sid]
		if !ok {
			continue
		}
		ph, pl, sh, sl := readSigilTraits(save, gu)
		if ph != 0 && ph != EmptyHash && pl > 0 {
			pairs = append(pairs, struct {
				hash  uint32
				level int
			}{ph, pl})
		}
		if sh != 0 && sh != EmptyHash && sl > 0 {
			pairs = append(pairs, struct {
				hash  uint32
				level int
			}{sh, sl})
		}
	}
	return pairs
}

// LoadoutSimulateDraft 在不写档的前提下，把背包因子和当前构造草稿一起模拟。
func (a *App) LoadoutSimulateDraft(path string, sigilSlotIDs []uint32, constructed []LoadoutConstructedSigil) (*LoadoutSimulation, error) {
	cat, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	ix := buildLoadoutIndex(save)
	pairs := collectStoredSigilPairs(save, ix, sigilSlotIDs)
	for _, draft := range constructed {
		prepared, err := prepareLoadoutSigil(cat, draft)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个构造草稿无法模拟: %w", draft.Index+1, err)
		}
		pairs = append(pairs, struct {
			hash  uint32
			level int
		}{prepared.primaryHash, prepared.item.PrimaryLevel})
		if prepared.secondaryHash != 0 && prepared.secondaryHash != EmptyHash && prepared.secondaryLevel > 0 {
			pairs = append(pairs, struct {
				hash  uint32
				level int
			}{prepared.secondaryHash, prepared.secondaryLevel})
		}
	}
	bonuses := simulateTraits(pairs, buildTraitHashToID(cat))
	return &LoadoutSimulation{Totals: aggregateTraitEffects(bonuses), Bonuses: bonuses}, nil
}
