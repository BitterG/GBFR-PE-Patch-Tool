package backend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

//go:embed data/weapon_stats.json
var loadoutWeaponStatsJSON []byte

// WeaponStatLine is a four-dimensional weapon stat contribution. The JSON
// names match the labels used by the final character panel.
type WeaponStatLine struct {
	ATK      float64 `json:"attack"`
	HP       float64 `json:"hp"`
	Stun     float64 `json:"stun"`
	CritRate float64 `json:"critRate"`
}

type LoadoutWeaponSkill struct {
	Slot            int    `json:"slot"`
	TraitHash       string `json:"traitHash"`
	TraitID         string `json:"traitId"`
	Name            string `json:"name"`
	Level           int    `json:"level"`
	StaticLevel     int    `json:"staticLevel,omitempty"`
	RuntimeObserved bool   `json:"runtimeObserved,omitempty"`
	StableReads     int    `json:"stableReads,omitempty"`
	Effect          string `json:"effect"`
	Source          string `json:"source"`
	SourceWeapon    string `json:"sourceWeapon"`
	LevelTableHash  string `json:"levelTableHash"`
	UnlockCondition string `json:"unlockCondition"`
}

func applyRuntimeWeaponSkillLevels(skills []LoadoutWeaponSkill, observed []runtimeWeaponTrait) {
	byHash := make(map[uint32]int, len(observed))
	for _, skill := range observed {
		byHash[skill.Hash] = skill.Level
	}
	values := loadTraitValues()
	for index := range skills {
		hash, err := ParseHashHex(skills[index].TraitHash)
		if err != nil {
			continue
		}
		level, ok := byHash[hash]
		if !ok {
			continue
		}
		skills[index].StaticLevel = skills[index].Level
		skills[index].Level = level
		skills[index].RuntimeObserved = true
		skills[index].StableReads = 3
		definition := values[skills[index].TraitID]
		if definition == nil {
			definition = values[skills[index].TraitHash]
		}
		if definition != nil {
			presentationLevel := level
			if definition.MaxLevel > 0 && presentationLevel > definition.MaxLevel {
				presentationLevel = definition.MaxLevel
			}
			skills[index].Effect, _ = renderTraitEffect(definition, presentationLevel)
		}
	}
}

// SKILL_319_00 is stored in weapon_skill_level_rebuild as a one-level
// activation flag, while its 2.0.2 skill_status curve has 55 effect levels.
// Those 55 rows align with chara_master_exp progress indices 1..55, including
// the five post-50 star levels. Runtime observations remain authoritative;
// offline previews derive the effective level from the selected character.
func applyMasterProgressWeaponSkillLevels(skills []LoadoutWeaponSkill, growth LoadoutPermanentGrowth) {
	if !growth.MasterSystemAvailable || growth.MasterProgressIndex <= 0 {
		return
	}
	values := loadTraitValues()
	for index := range skills {
		skill := &skills[index]
		if skill.TraitID != "SKILL_319_00" || skill.Level <= 0 || skill.RuntimeObserved {
			continue
		}
		definition := values[skill.TraitID]
		if definition == nil {
			continue
		}
		level := min(growth.MasterProgressIndex, definition.MaxLevel)
		if level <= 0 {
			continue
		}
		skill.StaticLevel = skill.Level
		skill.Level = level
		skill.Effect, _ = renderTraitEffect(definition, level)
		skill.UnlockCondition += fmt.Sprintf(" · 按角色专精进度 Lv%d 生效", level)
	}
}

// LoadoutWeaponSkillOption is one legal trait for a single 2818 vector slot at
// the weapon's current transcendence stage. Options are deliberately scoped to
// the weapon table group instead of exposing a global trait list.
type LoadoutWeaponSkillOption struct {
	Hash  string `json:"hash"`
	Name  string `json:"name"`
	Level int    `json:"level"`
}

type LoadoutWeaponSkillSlot struct {
	Index        int                        `json:"index"`
	Label        string                     `json:"label"`
	GroupHash    string                     `json:"groupHash"`
	CurrentHash  string                     `json:"currentHash"`
	CurrentName  string                     `json:"currentName"`
	CurrentLevel int                        `json:"currentLevel"`
	Editable     bool                       `json:"editable"`
	Options      []LoadoutWeaponSkillOption `json:"options"`
}

// LoadoutWeaponWrightstoneTrait is one of the three traits copied onto a
// weapon instance when a Wrightstone is imbued.  These live in the weapon's
// own 130000000-range effective trait slots; they are not inventory sigils and must not
// receive the 因子强化 level boost.
type LoadoutWeaponWrightstoneTrait struct {
	Index   int    `json:"index"`
	Hash    string `json:"hash"`
	TraitID string `json:"traitId"`
	Name    string `json:"name"`
	Level   int    `json:"level"`
}

type LoadoutWeaponWrightstone struct {
	Hash            string                          `json:"hash"`
	InternalID      string                          `json:"internalId"`
	Name            string                          `json:"name"`
	Traits          []LoadoutWeaponWrightstoneTrait `json:"traits"`
	Evidence        string                          `json:"evidence"`
	RuntimeObserved bool                            `json:"runtimeObserved"`
	StableReads     int                             `json:"stableReads"`
}

type LoadoutWeaponContext struct {
	UnitID                 uint32                    `json:"unitId"`
	SlotID                 uint32                    `json:"slotId"`
	StoredHash             string                    `json:"storedHash"`
	BaseHash               string                    `json:"baseHash"`
	Name                   string                    `json:"name"`
	InternalID             string                    `json:"internalId"`
	WeaponType             string                    `json:"weaponType"`
	Level                  int                       `json:"level"`
	XP                     uint32                    `json:"xp"`
	Uncap                  int                       `json:"uncap"`
	Mirage                 int                       `json:"mirage"`
	Awakening              int                       `json:"awakening"`
	Transcendence          int                       `json:"transcendence"`
	TranscendenceSkill     string                    `json:"transcendenceSkill"`
	TranscendenceSkillName string                    `json:"transcendenceSkillName"`
	Base                   WeaponStatLine            `json:"base"`
	AwakeningBonus         WeaponStatLine            `json:"awakeningBonus"`
	MirageBonus            WeaponStatLine            `json:"mirageBonus"`
	RebuildBonus           WeaponStatLine            `json:"rebuildBonus"`
	Total                  WeaponStatLine            `json:"total"`
	Skills                 []LoadoutWeaponSkill      `json:"skills"`
	SkillSlots             []LoadoutWeaponSkillSlot  `json:"skillSlots"`
	Wrightstone            *LoadoutWeaponWrightstone `json:"wrightstone,omitempty"`
	Warnings               []string                  `json:"warnings"`
	FormulaVerified        bool                      `json:"formulaVerified"`
}

type weaponStatKeyframe struct {
	Level    int     `json:"level"`
	Attack   float64 `json:"attack"`
	HP       float64 `json:"hp"`
	Stun     float64 `json:"stun"`
	CritRate float64 `json:"critRate"`
}

type loadoutWeaponTableRow struct {
	Key                     string    `json:"key"`
	WeaponID                string    `json:"weaponId"`
	WeaponID2               string    `json:"weaponId2"`
	PlusKey                 string    `json:"plusKey"`
	RebuildStatusKey        string    `json:"rebuildStatusKey"`
	AwakeningStatusKey      string    `json:"awakeningStatusKey"`
	RebuildSkillLevelKeys   [5]string `json:"rebuildSkillLevelKeys"`
	SkillLevelKeys          [4]string `json:"skillLevelKeys"`
	AwakeningSkillLevelKeys [4]string `json:"awakeningSkillLevelKeys"`
	SkillHashes             [4]string `json:"skillHashes"`
	AwakeningSkillHashes    [4]string `json:"awakeningSkillHashes"`
}

type loadoutWeaponSkillLevelRow struct {
	Uncap     [7]int `json:"uncap"`
	Awakening [4]int `json:"awakening"`
}

type loadoutWeaponRebuildSkillRow struct {
	Group  string `json:"group"`
	Trait  string `json:"trait"`
	Levels [7]int `json:"levels"`
}

type loadoutWeaponStatsFile struct {
	Version             string                                `json:"version"`
	Source              string                                `json:"source"`
	Weapons             map[string]loadoutWeaponTableRow      `json:"weapons"`
	Status              map[string][]weaponStatKeyframe       `json:"status"`
	AwakeningStatus     map[string][]weaponStatKeyframe       `json:"awakeningStatus"`
	PlusStatus          map[string][]weaponStatKeyframe       `json:"plusStatus"`
	RebuildStatus       map[string][]weaponStatKeyframe       `json:"rebuildStatus"`
	SkillLevels         map[string]loadoutWeaponSkillLevelRow `json:"skillLevels"`
	RebuildSkillLevels  []loadoutWeaponRebuildSkillRow        `json:"rebuildSkillLevels"`
	TraitIDs            map[string]string                     `json:"traitIds"`
	rebuildByGroupTrait map[string]loadoutWeaponRebuildSkillRow
}

var (
	loadoutWeaponStatsOnce sync.Once
	loadoutWeaponStatsData *loadoutWeaponStatsFile
	loadoutWeaponStatsErr  error
)

func loadLoadoutWeaponStats() (*loadoutWeaponStatsFile, error) {
	loadoutWeaponStatsOnce.Do(func() {
		var data loadoutWeaponStatsFile
		if err := json.Unmarshal(loadoutWeaponStatsJSON, &data); err != nil {
			loadoutWeaponStatsErr = fmt.Errorf("解析内置武器数值目录失败: %w", err)
			return
		}
		if data.Version != "2.0.0-20260716" || len(data.Weapons) == 0 {
			loadoutWeaponStatsErr = fmt.Errorf("内置武器数值目录版本或内容无效: %q", data.Version)
			return
		}
		data.rebuildByGroupTrait = make(map[string]loadoutWeaponRebuildSkillRow, len(data.RebuildSkillLevels))
		for _, row := range data.RebuildSkillLevels {
			data.rebuildByGroupTrait[row.Group+"|"+row.Trait] = row
		}
		loadoutWeaponStatsData = &data
	})
	return loadoutWeaponStatsData, loadoutWeaponStatsErr
}

func readLoadoutWeaponContext(save *SaveData, slotID uint32) (*LoadoutWeaponContext, error) {
	if save == nil {
		return nil, fmt.Errorf("存档不能为空")
	}
	if slotID == 0 || slotID == EmptyHash {
		return nil, fmt.Errorf("武器 SlotID %d 无效", slotID)
	}
	data, err := loadLoadoutWeaponStats()
	if err != nil {
		return nil, err
	}
	if _, err := loadProgressionCatalog(); err != nil {
		return nil, err
	}

	var unitID uint32
	for _, entry := range save.findAllUnitsByType(weaponSlotIDType) {
		if entry.ValueCnt != 1 || entry.Uint32() != slotID {
			continue
		}
		if unitID != 0 {
			return nil, fmt.Errorf("武器 SlotID %d 被多个实例引用", slotID)
		}
		unitID = entry.UnitID
	}
	if unitID == 0 {
		return nil, fmt.Errorf("存档里找不到武器 SlotID %d", slotID)
	}

	weaponID, ok := save.findUnitExact(weaponIDType, unitID)
	if !ok || weaponID.ValueCnt != 1 {
		return nil, fmt.Errorf("武器 SlotID %d 缺少 2803 标量", slotID)
	}
	storedHash := weaponID.Uint32()
	if storedHash == 0 || storedHash == EmptyHash {
		return nil, fmt.Errorf("武器 SlotID %d 没有有效武器", slotID)
	}
	storedText := hashText(storedHash)
	row, rowOK := resolveLoadoutWeaponTableRow(data, storedHash)
	if !rowOK {
		return nil, fmt.Errorf("武器 %s 不在内置官方数值目录中", storedText)
	}

	baseHash := row.WeaponID
	if baseHash == "" || baseHash == "00000000" || baseHash == hashText(EmptyHash) {
		baseHash = row.WeaponID2
	}
	if baseHash == "" || baseHash == "00000000" || baseHash == hashText(EmptyHash) {
		baseHash = row.Key
	}
	context := &LoadoutWeaponContext{UnitID: unitID, SlotID: slotID, StoredHash: storedText, BaseHash: baseHash, Level: 1, FormulaVerified: true}
	if def, resolved := progressionWeaponDefForHash(storedHash); resolved {
		context.Name = progressionWeaponName(def)
		context.InternalID = def.InternalID
		context.WeaponType = def.WeaponType
		if def.AliasOf != "" {
			context.BaseHash = def.AliasOf
		}
	}
	if context.Name == "" {
		context.Name = storedText
	}

	context.XP = readWeaponUintScalar(save, weaponXPIDType, unitID, &context.Warnings)
	context.Level = weaponLevelForXP(context.XP)
	context.Uncap = readWeaponIntScalar(save, weaponUncapIDType, unitID, &context.Warnings)
	context.Mirage = readWeaponIntScalar(save, weaponMirageIDType, unitID, &context.Warnings)
	context.Awakening = readWeaponIntScalar(save, weaponAwakeIDType, unitID, &context.Warnings)
	context.Transcendence = readWeaponIntScalar(save, weaponTranscendenceIDType, unitID, &context.Warnings)
	if extra, ok := save.findUnitExact(weaponExtraIDType, unitID); ok && extra.ValueCnt >= 5 {
		if hash, readErr := extra.Uint32At(4); readErr == nil {
			if name, verified := weaponTranscendenceSkills[hash]; verified {
				context.TranscendenceSkill = hashText(hash)
				context.TranscendenceSkillName = name
			}
		}
	}

	context.Base = interpolateWeaponStat(data.Status[baseHash], context.Level)
	// weapon_status_awake rows are per-level increments.  Summing through the
	// current awakening level reproduces the game's max-awakened weapon totals;
	// treating the final row as a replacement drops the Lv1-Lv9 gains.
	context.AwakeningBonus = truncateRuntimeWeaponEnhancement(cumulativeWeaponStat(data.AwakeningStatus[row.AwakeningStatusKey], context.Awakening))
	context.MirageBonus = cumulativeRuntimeWeaponPlus(data.PlusStatus[row.PlusKey], context.Mirage)
	context.RebuildBonus = truncateRuntimeWeaponEnhancement(cumulativeWeaponStat(data.RebuildStatus[row.RebuildStatusKey], context.Transcendence))
	context.Total = addWeaponStatLines(context.Base, context.AwakeningBonus, context.MirageBonus, context.RebuildBonus)

	catalog, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	context.Skills = readLoadoutWeaponSkills(save, data, catalog, row, context)
	context.SkillSlots = buildLoadoutWeaponSkillSlots(save, data, catalog, row, context)
	context.Wrightstone = readLoadoutWeaponWrightstone(save, context.UnitID, &context.Warnings)
	markUnresolvedWeaponSkills(context)
	return context, nil
}

func readLoadoutWeaponWrightstone(save *SaveData, weaponUnitID uint32, warnings *[]string) *LoadoutWeaponWrightstone {
	if save == nil || weaponUnitID < weaponSlotBase {
		return nil
	}
	stoneEntry, ok := save.findUnitExact(weaponStoneSubType, weaponUnitID)
	if !ok || stoneEntry.ValueCnt != 1 {
		if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("武器实例 %d 缺少武炼结晶类型字段 2816", weaponUnitID))
		}
		return nil
	}
	stoneHash := stoneEntry.Uint32()
	if stoneHash == 0 || stoneHash == EmptyHash {
		return nil
	}
	catalog, err := LoadWrightstoneCatalog()
	if err != nil {
		if warnings != nil {
			*warnings = append(*warnings, err.Error())
		}
		return nil
	}
	definition := catalog.LookupWrightstoneByHash(stoneHash)
	if definition == nil {
		if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("武器实例 %d 的武炼结晶 %08X 未收录", weaponUnitID, stoneHash))
		}
		return nil
	}
	result := &LoadoutWeaponWrightstone{
		Hash: hashText(stoneHash), InternalID: definition.InternalID,
		Name: cnWrightstone(definition.DisplayName), Evidence: "save:2816+130000000-weapon-effective-traits",
	}
	traitBase, traitBaseErr := weaponImbuedTraitUnitBase(weaponUnitID)
	if traitBaseErr != nil {
		if warnings != nil {
			*warnings = append(*warnings, traitBaseErr.Error())
		}
		return result
	}
	for index := 0; index < 3; index++ {
		hashEntry, hashOK := save.findUnitExact(TraitHashIDType, traitBase+uint32(index))
		levelEntry, levelOK := save.findUnitExact(TraitLevelIDType, traitBase+uint32(index))
		if !hashOK || !levelOK || hashEntry.ValueCnt != 1 || levelEntry.ValueCnt != 1 {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("武器实例 %d 的武炼结晶词条槽 %d 损坏", weaponUnitID, index+1))
			}
			continue
		}
		traitHash, level := hashEntry.Uint32(), int(levelEntry.Int32())
		if traitHash == 0 || traitHash == EmptyHash || level <= 0 {
			continue
		}
		trait := catalog.LookupTraitByHash(traitHash)
		if trait == nil {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("武器实例 %d 的武炼结晶词条 %08X 未收录", weaponUnitID, traitHash))
			}
			continue
		}
		result.Traits = append(result.Traits, LoadoutWeaponWrightstoneTrait{
			Index: index, Hash: hashText(traitHash), TraitID: trait.InternalID,
			Name: cnWrightstoneTrait(trait.DisplayName), Level: level,
		})
	}
	return result
}

func resolveLoadoutWeaponTableRow(data *loadoutWeaponStatsFile, storedHash uint32) (loadoutWeaponTableRow, bool) {
	if data == nil {
		return loadoutWeaponTableRow{}, false
	}
	if row, ok := data.Weapons[hashText(storedHash)]; ok {
		return row, true
	}
	return firstWeaponTableRowForBase(data.Weapons, hashText(weaponBaseHash(storedHash)))
}

func loadoutWeaponSkillName(data *loadoutWeaponStatsFile, catalog *Catalog, hash uint32, level int) string {
	skill := newLoadoutWeaponSkill(data, catalog, "", 0, hash, level, "weapon-rebuild", "", "")
	if strings.TrimSpace(skill.Name) != "" {
		return skill.Name
	}
	return hashText(hash)
}

func rebuildSkillOptionsForSlot(data *loadoutWeaponStatsFile, catalog *Catalog, group string, stage int) []LoadoutWeaponSkillOption {
	if data == nil || group == "" || stage <= 0 || stage > 7 {
		return nil
	}
	seen := make(map[uint32]bool)
	options := make([]LoadoutWeaponSkillOption, 0)
	for _, row := range data.RebuildSkillLevels {
		if row.Group != group || row.Levels[stage-1] <= 0 {
			continue
		}
		hash, ok := parseWeaponSkillHash(row.Trait)
		if !ok || seen[hash] {
			continue
		}
		seen[hash] = true
		level := row.Levels[stage-1]
		options = append(options, LoadoutWeaponSkillOption{
			Hash: hashText(hash), Name: loadoutWeaponSkillName(data, catalog, hash, level), Level: level,
		})
	}
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].Name != options[j].Name {
			return options[i].Name < options[j].Name
		}
		return options[i].Hash < options[j].Hash
	})
	return options
}

func buildLoadoutWeaponSkillSlots(save *SaveData, data *loadoutWeaponStatsFile, catalog *Catalog, row loadoutWeaponTableRow, context *LoadoutWeaponContext) []LoadoutWeaponSkillSlot {
	if save == nil || context == nil || context.Transcendence <= 0 {
		return nil
	}
	extra, ok := save.findUnitExact(weaponExtraIDType, context.UnitID)
	if !ok || extra.ValueCnt < 5 {
		return nil
	}
	labels := [...]string{"I", "II", "III", "IV", "EX"}
	result := make([]LoadoutWeaponSkillSlot, 0, len(labels))
	for index, label := range labels {
		current, err := extra.Uint32At(index)
		if err != nil {
			continue
		}
		options := rebuildSkillOptionsForSlot(data, catalog, row.RebuildSkillLevelKeys[index], context.Transcendence)
		slot := LoadoutWeaponSkillSlot{
			Index: index, Label: label, GroupHash: row.RebuildSkillLevelKeys[index],
			CurrentHash: hashText(current), Options: options,
		}
		currentIsLegal := false
		for _, option := range options {
			if option.Hash == slot.CurrentHash {
				slot.CurrentName = option.Name
				slot.CurrentLevel = option.Level
				currentIsLegal = true
				break
			}
		}
		if slot.CurrentName == "" && current != 0 && current != EmptyHash {
			slot.CurrentName = loadoutWeaponSkillName(data, catalog, current, 0)
		}
		slot.Editable = currentIsLegal && len(options) > 1
		if !currentIsLegal && current != 0 && current != EmptyHash {
			context.FormulaVerified = false
			context.Warnings = append(context.Warnings, fmt.Sprintf("武器技能槽 %d 的 %s 不属于当前武器与超凡阶段的技能组，已禁止编辑", index+1, slot.CurrentHash))
		}
		result = append(result, slot)
	}
	return result
}

func markUnresolvedWeaponSkills(context *LoadoutWeaponContext) {
	if context == nil {
		return
	}
	for _, skill := range context.Skills {
		if strings.TrimSpace(skill.Name) != "" && strings.TrimSpace(skill.Effect) != "" {
			continue
		}
		context.FormulaVerified = false
		context.Warnings = append(context.Warnings, fmt.Sprintf("武器技能槽 %d（%s）缺少完整名称或效果，最终属性未标记为完全验证", skill.Slot+1, skill.TraitHash))
	}
}

func firstWeaponTableRowForBase(rows map[string]loadoutWeaponTableRow, base string) (loadoutWeaponTableRow, bool) {
	keys := make([]string, 0)
	for key, row := range rows {
		if row.WeaponID == base {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return loadoutWeaponTableRow{}, false
	}
	sort.Slice(keys, func(i, j int) bool {
		count := func(key string) int {
			result := 0
			for _, hash := range rows[key].AwakeningSkillHashes {
				if _, ok := parseWeaponSkillHash(hash); ok {
					result++
				}
			}
			return result
		}
		left, right := count(keys[i]), count(keys[j])
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	return rows[keys[0]], true
}

func readWeaponUintScalar(save *SaveData, idType, unitID uint32, warnings *[]string) uint32 {
	entry, ok := save.findUnitExact(idType, unitID)
	if !ok || entry.ValueCnt != 1 {
		*warnings = append(*warnings, fmt.Sprintf("武器实例 %d 缺少 %d 标量", unitID, idType))
		return 0
	}
	return entry.Uint32()
}

func readWeaponIntScalar(save *SaveData, idType, unitID uint32, warnings *[]string) int {
	entry, ok := save.findUnitExact(idType, unitID)
	if !ok || entry.ValueCnt != 1 {
		*warnings = append(*warnings, fmt.Sprintf("武器实例 %d 缺少 %d 标量", unitID, idType))
		return 0
	}
	return int(entry.Int32())
}

func keyframeLine(row weaponStatKeyframe) WeaponStatLine {
	return WeaponStatLine{ATK: row.Attack, HP: row.HP, Stun: row.Stun, CritRate: row.CritRate}
}

// The 2.0.2 weapon-status writer interpolates all four values as floats, then
// uses vcvttss2si for HP, attack and critical rate before storing them on the
// weapon object. Stun remains a float. Truncation toward zero therefore belongs
// at the base-table boundary, before awakening/plus/rebuild increments are
// aggregated.
func truncateRuntimeWeaponBase(line WeaponStatLine) WeaponStatLine {
	line.ATK = math.Trunc(line.ATK)
	line.HP = math.Trunc(line.HP)
	line.CritRate = math.Trunc(line.CritRate)
	return line
}

func interpolateWeaponStat(rows []weaponStatKeyframe, level int) WeaponStatLine {
	if len(rows) == 0 || level <= 0 {
		return WeaponStatLine{}
	}
	if level <= rows[0].Level {
		return truncateRuntimeWeaponBase(keyframeLine(rows[0]))
	}
	for index := 1; index < len(rows); index++ {
		hi := rows[index]
		if level == hi.Level {
			return truncateRuntimeWeaponBase(keyframeLine(hi))
		}
		if level < hi.Level {
			lo := rows[index-1]
			ratio := float64(level-lo.Level) / float64(hi.Level-lo.Level)
			return truncateRuntimeWeaponBase(WeaponStatLine{
				ATK:      lo.Attack + (hi.Attack-lo.Attack)*ratio,
				HP:       lo.HP + (hi.HP-lo.HP)*ratio,
				Stun:     lo.Stun + (hi.Stun-lo.Stun)*ratio,
				CritRate: lo.CritRate + (hi.CritRate-lo.CritRate)*ratio,
			})
		}
	}
	return truncateRuntimeWeaponBase(keyframeLine(rows[len(rows)-1]))
}

func cumulativeWeaponStat(rows []weaponStatKeyframe, level int) WeaponStatLine {
	var result WeaponStatLine
	for _, row := range rows {
		if row.Level > level {
			break
		}
		result = addWeaponStatLines(result, keyframeLine(row))
	}
	return result
}

// Awakening and rebuild aggregate every stage as floats, then convert HP/ATK
// with vcvttss2si and Stun with vroundss (toward zero) before storing their
// component fields. Critical rate is not part of either enhancement record.
func truncateRuntimeWeaponEnhancement(line WeaponStatLine) WeaponStatLine {
	line.ATK = math.Trunc(line.ATK)
	line.HP = math.Trunc(line.HP)
	line.Stun = math.Trunc(line.Stun)
	line.CritRate = 0
	return line
}

// Mirage (+) applies its toward-zero conversion after each individual stage,
// then uses the converted i32 as the accumulator for the next stage.
func cumulativeRuntimeWeaponPlus(rows []weaponStatKeyframe, level int) WeaponStatLine {
	var result WeaponStatLine
	for _, row := range rows {
		if row.Level > level {
			break
		}
		result.ATK = math.Trunc(result.ATK + row.Attack)
		result.HP = math.Trunc(result.HP + row.HP)
	}
	return result
}

func addWeaponStatLines(lines ...WeaponStatLine) WeaponStatLine {
	var result WeaponStatLine
	for _, line := range lines {
		result.ATK += line.ATK
		result.HP += line.HP
		result.Stun += line.Stun
		result.CritRate += line.CritRate
	}
	return result
}

func readLoadoutWeaponSkills(save *SaveData, data *loadoutWeaponStatsFile, catalog *Catalog, row loadoutWeaponTableRow, context *LoadoutWeaponContext) []LoadoutWeaponSkill {
	if context.Transcendence > 0 {
		if extra, ok := save.findUnitExact(weaponExtraIDType, context.UnitID); ok && extra.ValueCnt >= 5 {
			result := make([]LoadoutWeaponSkill, 0, 5)
			for slot := 0; slot < 5; slot++ {
				hash, err := extra.Uint32At(slot)
				if err != nil || hash == 0 || hash == EmptyHash {
					continue
				}
				traitText := hashText(hash)
				levelRow, exists := data.rebuildByGroupTrait[row.RebuildSkillLevelKeys[slot]+"|"+traitText]
				if !exists || context.Transcendence > len(levelRow.Levels) {
					context.Warnings = append(context.Warnings, fmt.Sprintf("武器技能槽 %d 的 %s 无法在超越等级表中解析", slot+1, traitText))
					continue
				}
				level := levelRow.Levels[context.Transcendence-1]
				if level <= 0 {
					continue
				}
				unlock := fmt.Sprintf("超凡 %d/7 · 当前阶段技能表", context.Transcendence)
				result = append(result, newLoadoutWeaponSkill(data, catalog, context.Name, slot, hash, level, "weapon-rebuild", levelRow.Group, unlock))
			}
			return result
		}
		context.Warnings = append(context.Warnings, "超越武器缺少完整的 2818 五技能向量，已回退到武器表基础技能")
	}

	result := make([]LoadoutWeaponSkill, 0, 8)
	for slot, traitText := range row.SkillHashes {
		level := weaponSkillLevel(data.SkillLevels[row.SkillLevelKeys[slot]], context.Uncap, context.Awakening)
		if hash, ok := parseWeaponSkillHash(traitText); ok && level > 0 {
			unlock := fmt.Sprintf("上限突破 %d/6 · 觉醒 %d/10", context.Uncap, context.Awakening)
			result = append(result, newLoadoutWeaponSkill(data, catalog, context.Name, slot, hash, level, "weapon-base", row.SkillLevelKeys[slot], unlock))
		}
	}
	awakeningThresholds := [...]int{3, 10, 10, 10}
	for slot, traitText := range row.AwakeningSkillHashes {
		if context.Awakening < awakeningThresholds[slot] {
			continue
		}
		level := weaponSkillLevel(data.SkillLevels[row.AwakeningSkillLevelKeys[slot]], context.Uncap, context.Awakening)
		if hash, ok := parseWeaponSkillHash(traitText); ok && level > 0 {
			unlock := fmt.Sprintf("觉醒 %d/10 · 阶段 %d 解锁", context.Awakening, awakeningThresholds[slot])
			result = append(result, newLoadoutWeaponSkill(data, catalog, context.Name, slot+4, hash, level, "weapon-awakening", row.AwakeningSkillLevelKeys[slot], unlock))
		}
	}
	return result
}

func weaponSkillLevel(row loadoutWeaponSkillLevelRow, uncap, awakening int) int {
	if uncap < 0 {
		uncap = 0
	}
	if uncap >= len(row.Uncap) {
		uncap = len(row.Uncap) - 1
	}
	awakeningIndex := 0
	for index, threshold := range []int{0, 3, 6, 10} {
		if awakening >= threshold {
			awakeningIndex = index
		}
	}
	level := row.Uncap[uncap]
	if row.Awakening[awakeningIndex] > level {
		level = row.Awakening[awakeningIndex]
	}
	return level
}

func parseWeaponSkillHash(text string) (uint32, bool) {
	if text == "" || text == "00000000" || text == hashText(EmptyHash) {
		return 0, false
	}
	hash, err := ParseHashHex(text)
	return hash, err == nil
}

func newLoadoutWeaponSkill(data *loadoutWeaponStatsFile, catalog *Catalog, weaponName string, slot int, hash uint32, level int, source, levelTableHash, unlockCondition string) LoadoutWeaponSkill {
	hashString := hashText(hash)
	traitID := data.TraitIDs[hashString]
	name := ""
	if trait := catalog.LookupTraitByHash(hash); trait != nil {
		if traitID == "" {
			traitID = trait.InternalID
		}
		name = loadoutTraitDisplayName(catalog, hash)
	}
	values := loadTraitValues()
	definition := values[traitID]
	if definition == nil {
		definition = values[hashString]
	}
	effect := ""
	if definition != nil {
		if strings.TrimSpace(name) == "" {
			name = definition.Name
		}
		presentationLevel := level
		if definition.MaxLevel > 0 && presentationLevel > definition.MaxLevel {
			presentationLevel = definition.MaxLevel
		}
		effect, _ = renderTraitEffect(definition, presentationLevel)
	}
	if strings.TrimSpace(name) == "" {
		if verifiedName, ok := weaponTranscendenceSkills[hash]; ok {
			name = verifiedName
		}
	}
	return LoadoutWeaponSkill{Slot: slot, TraitHash: hashString, TraitID: traitID, Name: name, Level: level, Effect: effect, Source: source, SourceWeapon: weaponName, LevelTableHash: levelTableHash, UnlockCondition: unlockCondition}
}
