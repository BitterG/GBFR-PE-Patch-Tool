package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	loadoutShareFormat        = "gbfr-loadout"
	loadoutShareLegacyVersion = 1
	loadoutShareVersion       = 10
	loadoutShareMaxSize       = 1024 * 1024
)

// LoadoutShareSigil 使用“因子本体 + 等级 + 主副词条 + 词条等级”指纹。
// 分享文件不保存 SlotID；导入时转为原子构造草稿，避免复用已被其他配装占用的物品实例。
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

// LoadoutShareSummon deliberately excludes UnitID and SlotID: those values are
// local to one save. Import resolves this stable fingerprint against the target
// save's real summon inventory.
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

type LoadoutShareCharacterProgression struct {
	CharacterLevel             int                             `json:"characterLevel,omitempty"`
	BaseHP                     int                             `json:"baseHp,omitempty"`
	BaseATK                    int                             `json:"baseAtk,omitempty"`
	BaseStunBits               uint32                          `json:"baseStunBits,omitempty"`
	BaseCritRate               int                             `json:"baseCritRate,omitempty"`
	CharacterBaseCaptured      bool                            `json:"characterBaseCaptured,omitempty"`
	MasterTotalMSP             int                             `json:"masterTotalMsp"`
	// MasterProgressCaptured marks a validated Logs masterLevel observation.
	// It remains false for ordinary loadout JSON, including zero-MSP states.
	MasterProgressIndex        int                             `json:"masterProgressIndex,omitempty"`
	MasterProgressCaptured     bool                            `json:"masterProgressCaptured,omitempty"`
	LegacyProgress             int                             `json:"legacyProgress"`
	EnhancementPanel           []int                           `json:"enhancementPanel,omitempty"`
	EnhancementNodes           []LoadoutShareEnhancementNode   `json:"enhancementNodes,omitempty"`
	EnhancementNodeValues      []int                           `json:"enhancementNodeValues,omitempty"`
	Weapons                    []LoadoutShareProgressionWeapon `json:"weapons,omitempty"`
	WeaponWrightstonesCaptured bool                            `json:"weaponWrightstonesCaptured,omitempty"`
}

func compactEnhancementNodeValues(nodes []LoadoutShareEnhancementNode) ([]int, bool) {
	if len(nodes) == 0 {
		return nil, false
	}
	values := make([]int, len(nodes))
	for index, node := range nodes {
		if node.Index != index {
			return nil, false
		}
		values[index] = node.Value
	}
	return values, true
}

func normalizeEnhancementNodeValues(character *LoadoutShareCharacterProgression) error {
	if character == nil || len(character.EnhancementNodeValues) == 0 {
		return nil
	}
	if len(character.EnhancementNodeValues) > 1000 {
		return fmt.Errorf("enhancementNodeValues 超过 1000 个节点")
	}
	character.EnhancementNodes = make([]LoadoutShareEnhancementNode, len(character.EnhancementNodeValues))
	for index, value := range character.EnhancementNodeValues {
		character.EnhancementNodes[index] = LoadoutShareEnhancementNode{Index: index, Value: value}
	}
	character.EnhancementNodeValues = nil
	return nil
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

type LoadoutShareProgressionWeapon struct {
	Hash               string                    `json:"hash"`
	BaseHash           string                    `json:"baseHash,omitempty"`
	InternalID         string                    `json:"internalId"`
	Level              int                       `json:"level"`
	Uncap              int                       `json:"uncap"`
	Mirage             int                       `json:"mirage"`
	Awakening          int                       `json:"awakening"`
	Transcendence      int                       `json:"transcendence"`
	TranscendenceSkill string                    `json:"transcendenceSkill,omitempty"`
	Wrightstone        *LoadoutWeaponWrightstone `json:"wrightstone,omitempty"`
}

type LoadoutShareWeaponState struct {
	StoredHash           string                    `json:"storedHash"`
	XP                   uint32                    `json:"xp"`
	Level                int                       `json:"level,omitempty"`
	Uncap                int                       `json:"uncap"`
	Mirage               int                       `json:"mirage"`
	Awakening            int                       `json:"awakening"`
	Transcendence        int                       `json:"transcendence"`
	// EnhancementCaptured marks a Logs snapshot with a usable complete enhancement state.
	// Logs snapshots lacking transcendence use the user-selected full-transcendence default.
	EnhancementCaptured  bool                      `json:"enhancementCaptured,omitempty"`
	// MirageCaptured marks a Logs snapshot where plusMarks was present, including zero.
	MirageCaptured       bool                      `json:"mirageCaptured,omitempty"`
	ExactState           bool                      `json:"exactState,omitempty"`
	Flags                uint32                    `json:"flags,omitempty"`
	WrightstoneReference string                    `json:"wrightstoneReference,omitempty"`
	State                int                       `json:"state,omitempty"`
	SkillHashes          []string                  `json:"skillHashes"`
	Skills               []LoadoutWeaponSkill      `json:"skills,omitempty"`
	Wrightstone          *LoadoutWeaponWrightstone `json:"wrightstone,omitempty"`
}

type LoadoutConstructedSummon struct {
	Index int              `json:"index"`
	Name  string           `json:"name"`
	State SummonTraitState `json:"state"`
}

type LoadoutImportApplyPayload struct {
	Character                        *LoadoutShareCharacterProgression `json:"character,omitempty"`
	Weapon                           *LoadoutShareWeaponState          `json:"weapon,omitempty"`
	ConstructedWeapon                *LoadoutShareProgressionWeapon    `json:"constructedWeapon,omitempty"`
	ConstructedSummons               []LoadoutConstructedSummon        `json:"constructedSummons,omitempty"`
	OverLimit                        []LoadoutShareOverLimit           `json:"overLimit,omitempty"`
	ApplyMasteryConfiguration        bool                              `json:"applyMasteryConfiguration,omitempty"`
	ApplyMasterProgress              bool                              `json:"applyMasterProgress,omitempty"`
	MasterProgressIndex              int                               `json:"masterProgressIndex,omitempty"`
	ApplyCharacterLevel              bool                              `json:"applyCharacterLevel,omitempty"`
	ApplyCharacterGrowth             bool                              `json:"applyCharacterGrowth,omitempty"`
	ApplyCharacterWeaponCollection   bool                              `json:"applyCharacterWeaponCollection,omitempty"`
	ApplyCharacterWeaponWrightstones bool                              `json:"applyCharacterWeaponWrightstones,omitempty"`
	ApplyWeaponEnhancement           bool                              `json:"applyWeaponEnhancement,omitempty"`
	ApplyWeaponMirage                bool                              `json:"applyWeaponMirage,omitempty"`
	ApplyWeaponWrightstone           bool                              `json:"applyWeaponWrightstone,omitempty"`
	ApplyOverLimit                   bool                              `json:"applyOverLimit,omitempty"`
}

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
	Skills            []LoadoutSkill                    `json:"skills"`
	WeaponSkillHashes []string                          `json:"weaponSkillHashes,omitempty"`
	MasteryHashes     []string                          `json:"masteryHashes"`
	LogsImport        bool                              `json:"logsImport,omitempty"`
	Character         *LoadoutShareCharacterProgression `json:"character,omitempty"`
	Weapon            *LoadoutShareWeaponState          `json:"weapon,omitempty"`
	OverLimit         []LoadoutShareOverLimit           `json:"overLimit,omitempty"`
	Warnings          []string                          `json:"warnings,omitempty"`
}

type LoadoutImportCapabilities struct {
	TargetCharacterLevel        int  `json:"targetCharacterLevel"`
	TargetFateDataAvailable     bool `json:"targetFateDataAvailable"`
	TargetFateEpisodeCount      int  `json:"targetFateEpisodeCount"`
	TargetMasterProgressIndex   int  `json:"targetMasterProgressIndex"`
	TargetMasterLevel           int  `json:"targetMasterLevel"`
	TargetMasterSystem          bool `json:"targetMasterSystem"`
	TargetSummonSystem          bool `json:"targetSummonSystem"`
	SourceCharacterLevel        int  `json:"sourceCharacterLevel"`
	SourceCharacterBaseCaptured bool `json:"sourceCharacterBaseCaptured"`
	SourceMasterProgressIndex   int  `json:"sourceMasterProgressIndex"`
	SourceMasterLevel           int  `json:"sourceMasterLevel"`
}

// LoadoutImportDraft 是导入文件在当前存档中解析后的“草稿”，不会自动写档。
type LoadoutImportDraft struct {
	Name              string                     `json:"name"`
	WeaponSlotID      uint32                     `json:"weaponSlotId"`
	SigilSlotIDs      []uint32                   `json:"sigilSlotIds"`
	ConstructedSigils []LoadoutConstructedSigil  `json:"constructedSigils,omitempty"`
	SummonSlotIDs     []uint32                   `json:"summonSlotIds,omitempty"`
	SkillHashes       []string                   `json:"skillHashes"`
	WeaponSkillHashes []string                   `json:"weaponSkillHashes,omitempty"`
	MasteryHashes     []string                   `json:"masteryHashes"`
	ApplyPayload      *LoadoutImportApplyPayload `json:"applyPayload,omitempty"`
	Missing           []string                   `json:"missing"`
	MissingByScope    map[string][]string        `json:"missingByScope,omitempty"`
	Capabilities      LoadoutImportCapabilities  `json:"capabilities"`
}

func (draft *LoadoutImportDraft) addMissing(scope, message string) {
	draft.Missing = append(draft.Missing, message)
	if draft.MissingByScope == nil {
		draft.MissingByScope = make(map[string][]string)
	}
	draft.MissingByScope[scope] = append(draft.MissingByScope[scope], message)
}

func loadoutShareEquippedWeaponConstruction(share *LoadoutShare) (*LoadoutShareProgressionWeapon, error) {
	if share == nil || strings.TrimSpace(share.WeaponHash) == "" || share.Weapon == nil {
		return nil, fmt.Errorf("分享文件没有完整的装备武器快照")
	}
	wantHash, err := ParseHashHex(share.WeaponHash)
	if err != nil || wantHash == 0 || wantHash == EmptyHash {
		return nil, fmt.Errorf("分享文件的装备武器哈希无效")
	}
	if share.Character != nil {
		for _, weapon := range share.Character.Weapons {
			hash, parseErr := ParseHashHex(weapon.Hash)
			if parseErr != nil || hash != wantHash {
				continue
			}
			copyValue := weapon
			return &copyValue, nil
		}
	}
	definition, known := progressionWeaponDefForLoadout(wantHash)
	if !known || strings.TrimSpace(definition.InternalID) == "" {
		return nil, fmt.Errorf("装备武器 %08X 不在当前 2.0.2 武器目录", wantHash)
	}
	baseHash := strings.TrimSpace(definition.Hash)
	if strings.TrimSpace(definition.AliasOf) != "" {
		baseHash = strings.TrimSpace(definition.AliasOf)
	}
	return &LoadoutShareProgressionWeapon{
		Hash: share.WeaponHash, BaseHash: baseHash, InternalID: definition.InternalID,
		Level: weaponLevelForXP(share.Weapon.XP), Uncap: share.Weapon.Uncap, Mirage: share.Weapon.Mirage,
		Awakening: share.Weapon.Awakening, Transcendence: share.Weapon.Transcendence,
		Wrightstone: share.Weapon.Wrightstone,
	}, nil
}

func sameSharedWeaponIdentity(currentHash, wantedHash string) bool {
	current, currentErr := ParseHashHex(currentHash)
	wanted, wantedErr := ParseHashHex(wantedHash)
	if currentErr != nil || wantedErr != nil || current == 0 || wanted == 0 || current == EmptyHash || wanted == EmptyHash {
		return false
	}
	return weaponBaseHash(current) == weaponBaseHash(wanted)
}

func isOpaqueLoadoutShareName(name string, expectedHash uint32) bool {
	text := strings.TrimSpace(name)
	for _, prefix := range []string{"因子 ", "Sigil "} {
		if strings.HasPrefix(text, prefix) {
			text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
			break
		}
	}
	value, err := ParseHashHex(text)
	return err == nil && value == expectedHash
}

func shareHex(value uint32) string {
	if value == 0 || value == EmptyHash {
		return ""
	}
	return fmt.Sprintf("%08X", value)
}

func buildLoadoutShareSummons(context *LoadoutStatContext) ([]LoadoutShareSummon, error) {
	if context == nil || len(context.EquippedSummonSlotIDs) != 4 {
		count := 0
		if context != nil {
			count = len(context.EquippedSummonSlotIDs)
		}
		return nil, fmt.Errorf("当前召唤石配置不完整：需要 4 个槽位，得到 %d 个", count)
	}
	bySlot := make(map[uint32]LoadoutSummon, len(context.Summons))
	for _, summon := range context.Summons {
		bySlot[summon.SlotID] = summon
	}
	seen := make(map[uint32]bool, 4)
	result := make([]LoadoutShareSummon, 0, 4)
	for index, slotID := range context.EquippedSummonSlotIDs {
		if slotID == 0 || slotID == EmptyHash {
			return nil, fmt.Errorf("当前召唤石配置不完整：第 %d 槽为空", index+1)
		}
		if seen[slotID] {
			return nil, fmt.Errorf("当前召唤石配置重复引用 SlotID %d", slotID)
		}
		seen[slotID] = true
		summon, ok := bySlot[slotID]
		if !ok {
			return nil, fmt.Errorf("当前召唤石配置不完整：第 %d 槽 SlotID %d 没有真实实例", index+1, slotID)
		}
		result = append(result, LoadoutShareSummon{
			TypeHash: summon.TypeHash, Name: summon.Name,
			MainTraitHash: summon.MainTraitHash, MainTraitName: summon.MainTraitName, MainTraitLevel: summon.MainTraitLevel,
			SubParamHash: summon.SubParamHash, SubParamName: summon.SubParamName, SubParamLevel: summon.SubParamLevel,
			Rank: summon.Rank,
		})
	}
	return result, nil
}

func buildLoadoutShare(path string, unitID uint32) (*LoadoutShare, error) {
	app := &App{}
	groups, err := app.LoadoutList(path)
	if err != nil {
		return nil, err
	}
	var source *LoadoutEntry
	for gi := range groups {
		for li := range groups[gi].Loadouts {
			item := &groups[gi].Loadouts[li]
			if item.UnitID == unitID && !item.IsParty {
				source = item
				break
			}
		}
	}
	if source == nil {
		return nil, fmt.Errorf("没有找到可导出的配装槽 UnitID=%d", unitID)
	}

	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	ix := buildLoadoutIndex(save)
	catalog, err := LoadCatalog()
	if err != nil {
		return nil, fmt.Errorf("加载因子中文目录失败: %w", err)
	}
	charaHash, err := ParseHashHex(source.CharaHash)
	if err != nil {
		return nil, err
	}
	share := &LoadoutShare{
		Format: loadoutShareFormat, Version: loadoutShareVersion,
		CharaHash: source.CharaHash, CharaName: source.CharaName,
		OwnerCode: ix.deriveOwnerCode(save, charaHash), Name: source.Name,
		WeaponHash: source.WeaponHash, WeaponName: source.WeaponName,
		Skills:            append([]LoadoutSkill(nil), source.Skills...),
		WeaponSkillHashes: append([]string(nil), source.WeaponSkillHashes...),
	}
	if weaponHash, parseErr := ParseHashHex(source.WeaponHash); parseErr == nil {
		if definition, known := progressionWeaponDefForHash(weaponHash); known {
			share.WeaponName = progressionWeaponNameCN(definition)
		}
	}
	for _, hash := range readFixedVec(save, loadoutMasteryIDType, source.UnitID, loadoutMaxMastery) {
		share.MasteryHashes = append(share.MasteryHashes, hashText(hash))
	}
	for _, sigil := range source.Sigils {
		if sigil.Missing {
			share.Warnings = append(share.Warnings, fmt.Sprintf("因子槽位 %d 引用的 SlotID %d 已失效，未导出该因子", sigil.Index+1, sigil.SlotID))
			continue
		}
		gemUnitID, ok := ix.gemBySlotID[sigil.SlotID]
		if !ok {
			share.Warnings = append(share.Warnings, fmt.Sprintf("因子槽位 %d 引用的 SlotID %d 已失效，未导出该因子", sigil.Index+1, sigil.SlotID))
			continue
		}
		primaryHash, primaryLevel, secondaryHash, secondaryLevel := readSigilTraits(save, gemUnitID)
		sigilHash, parseErr := ParseHashHex(sigil.Hash)
		if parseErr != nil {
			return nil, fmt.Errorf("因子 hash 无效 %q: %w", sigil.Hash, parseErr)
		}
		index := sigil.Index
		share.Sigils = append(share.Sigils, LoadoutShareSigil{
			Index: &index, Hash: sigil.Hash, Name: loadoutShareChineseSigilName(catalog, sigilHash, sigil.Name), Level: sigil.Level,
			PrimaryTraitHash: shareHex(primaryHash), PrimaryTraitName: loadoutShareChineseTraitName(catalog, primaryHash, sigil.PrimaryTraitName), PrimaryTraitLevel: primaryLevel,
			SecondaryTraitHash: shareHex(secondaryHash), SecondaryTraitName: loadoutShareChineseTraitName(catalog, secondaryHash, sigil.SecondaryTraitName), SecondaryTraitLevel: secondaryLevel,
		})
	}
	statContext, err := app.LoadoutStatContext(path, source.CharaHash)
	if err != nil {
		return nil, fmt.Errorf("读取召唤石配置失败: %w", err)
	}
	share.Summons, err = buildLoadoutShareSummons(statContext)
	if err != nil {
		return nil, err
	}
	share.Character = &LoadoutShareCharacterProgression{
		CharacterLevel:             statContext.Level,
		MasterTotalMSP:             statContext.PermanentGrowth.MasterTotalMSP,
		LegacyProgress:             statContext.PermanentGrowth.LegacyProgress,
		WeaponWrightstonesCaptured: true,
	}
	characterUnitID, characterErr := loadoutCharacterUnitForHash(save, charaHash)
	if characterErr != nil {
		return nil, characterErr
	}
	characterBaseFields := []struct {
		idType uint32
		name   string
	}{
		{1308, "角色等级"},
		{1309, "基础 HP"},
		{1310, "基础攻击"},
		{1312, "基础昏厥"},
		{1313, "基础暴击"},
	}
	characterBase := make(map[uint32]uint32, len(characterBaseFields))
	for _, field := range characterBaseFields {
		entry, ok := save.findUnitExact(field.idType, characterUnitID)
		if !ok || entry.ValueCnt != 1 {
			return nil, fmt.Errorf("角色缺少完整的%s字段", field.name)
		}
		characterBase[field.idType] = entry.Uint32()
	}
	share.Character.CharacterLevel = int(int32(characterBase[1308]))
	share.Character.BaseHP = int(int32(characterBase[1309]))
	share.Character.BaseATK = int(int32(characterBase[1310]))
	share.Character.BaseStunBits = characterBase[1312]
	share.Character.BaseCritRate = int(int32(characterBase[1313]))
	share.Character.CharacterBaseCaptured = true
	if panel, ok := save.findUnitExact(1503, characterUnitID); ok && panel.ValueCnt == 2 {
		share.Character.EnhancementPanel = make([]int, 0, 2)
		for index := 0; index < 2; index++ {
			value, readErr := panel.Uint32At(index)
			if readErr != nil {
				return nil, fmt.Errorf("读取角色强化面板槽 %d 失败: %w", index+1, readErr)
			}
			share.Character.EnhancementPanel = append(share.Character.EnhancementPanel, int(int32(value)))
		}
	} else {
		return nil, fmt.Errorf("角色缺少完整的 1503 双槽强化面板")
	}
	nodeBase := uint32(10000000) + (characterUnitID-10000)*1000
	for _, entry := range save.findAllUnitsByType(1602) {
		if entry.UnitID < nodeBase || entry.UnitID >= nodeBase+1000 || entry.ValueCnt != 1 {
			continue
		}
		share.Character.EnhancementNodes = append(share.Character.EnhancementNodes, LoadoutShareEnhancementNode{
			Index: int(entry.UnitID - nodeBase), Value: int(entry.Int32()),
		})
	}
	sort.Slice(share.Character.EnhancementNodes, func(i, j int) bool {
		return share.Character.EnhancementNodes[i].Index < share.Character.EnhancementNodes[j].Index
	})
	if len(share.Character.EnhancementNodes) == 0 {
		return nil, fmt.Errorf("角色缺少 1602 强化节点快照")
	}
	overLimitByIndex := make(map[int]LoadoutOverLimitBonus, len(statContext.OverLimit))
	for _, bonus := range statContext.OverLimit {
		overLimitByIndex[bonus.Index] = bonus
	}
	share.OverLimit = make([]LoadoutShareOverLimit, 4)
	for index := 0; index < 4; index++ {
		share.OverLimit[index].Index = index
		if bonus, ok := overLimitByIndex[index]; ok {
			share.OverLimit[index].AttributeHash = bonus.AttributeHash
			share.OverLimit[index].Name = bonus.Name
			share.OverLimit[index].Level = bonus.Level
			share.OverLimit[index].Value = bonus.Value
			share.OverLimit[index].Unit = bonus.Unit
		}
	}
	for _, weapon := range progressionInventory(save, path).Weapons {
		if weapon.OwnerCode != statContext.OwnerCode || weapon.InternalID == "" {
			continue
		}
		share.Character.Weapons = append(share.Character.Weapons, LoadoutShareProgressionWeapon{
			Hash: weapon.Hash, BaseHash: weapon.BaseHash, InternalID: weapon.InternalID,
			Level: weapon.Level, Uncap: weapon.Uncap, Mirage: weapon.Mirage,
			Awakening: weapon.Awakening, Transcendence: weapon.Transcendence,
			TranscendenceSkill: weapon.TranscendenceSkill,
			Wrightstone:        readLoadoutWeaponWrightstone(save, weapon.UnitID, nil),
		})
	}
	if source.WeaponSlotID != 0 {
		weapon, weaponErr := readLoadoutWeaponContext(save, source.WeaponSlotID)
		if weaponErr != nil {
			return nil, fmt.Errorf("读取武器强化与祝福失败: %w", weaponErr)
		}
		applyMasterProgressWeaponSkillLevels(weapon.Skills, statContext.PermanentGrowth)
		state := &LoadoutShareWeaponState{
			StoredHash: weapon.StoredHash, XP: weapon.XP, Level: weapon.Level, Uncap: weapon.Uncap,
			Mirage: weapon.Mirage, Awakening: weapon.Awakening, Transcendence: weapon.Transcendence,
			ExactState: true, Skills: weapon.Skills, Wrightstone: weapon.Wrightstone,
		}
		if entry, ok := save.findUnitExact(weaponFlagsIDType, weapon.UnitID); ok && entry.ValueCnt == 1 {
			state.Flags = entry.Uint32()
		} else {
			return nil, fmt.Errorf("武器缺少 2813 状态标志")
		}
		if entry, ok := save.findUnitExact(weaponVariantIDType, weapon.UnitID); ok && entry.ValueCnt == 1 {
			// 仅保留给 v5 文件兼容；2.0.2 中 2814 属于武器自身状态，不是祝福定位键。
			state.WrightstoneReference = hashText(entry.Uint32())
		}
		if entry, ok := save.findUnitExact(weaponStateIDType, weapon.UnitID); ok && entry.ValueCnt == 1 {
			state.State = int(entry.Int32())
		} else {
			return nil, fmt.Errorf("武器缺少 2815 状态字段")
		}
		if extra, ok := save.findUnitExact(weaponExtraIDType, weapon.UnitID); ok && extra.ValueCnt >= 5 {
			for index := 0; index < 5; index++ {
				hash, readErr := extra.Uint32At(index)
				if readErr != nil {
					return nil, fmt.Errorf("读取武器技能槽 %d 失败: %w", index+1, readErr)
				}
				state.SkillHashes = append(state.SkillHashes, hashText(hash))
			}
		} else {
			return nil, fmt.Errorf("武器缺少完整的 2818 五技能向量")
		}
		share.Weapon = state
	}
	return share, nil
}

func loadoutShareChineseSigilName(catalog *Catalog, hash uint32, fallback string) string {
	if sigil := catalog.LookupSigilByHash(hash); sigil != nil {
		if name, ok := sigilCN[sigil.DisplayName]; ok {
			return name
		}
	}
	if name, ok := runtimeNameCN[hash]; ok {
		return name
	}
	return fallback
}

func loadoutShareChineseTraitName(catalog *Catalog, hash uint32, fallback string) string {
	if trait := catalog.LookupTraitByHash(hash); trait != nil {
		if name, ok := traitCN[trait.DisplayName]; ok {
			return name
		}
	}
	if name, ok := runtimeNameCN[hash]; ok {
		return name
	}
	return fallback
}

func sameSharedSummon(pick LoadoutSummon, want LoadoutShareSummon) bool {
	return strings.EqualFold(pick.TypeHash, want.TypeHash) &&
		strings.EqualFold(pick.MainTraitHash, want.MainTraitHash) && pick.MainTraitLevel == want.MainTraitLevel &&
		strings.EqualFold(pick.SubParamHash, want.SubParamHash) && pick.SubParamLevel == want.SubParamLevel &&
		pick.Rank == want.Rank
}

func loadoutShareConstructedSigil(cat *Catalog, want LoadoutShareSigil, index int) (LoadoutConstructedSigil, error) {
	sigilHash, err := ParseHashHex(want.Hash)
	if err != nil {
		return LoadoutConstructedSigil{}, fmt.Errorf("因子 %q 的哈希无效: %w", want.Name, err)
	}
	if sigilHash == 0 || sigilHash == EmptyHash {
		return LoadoutConstructedSigil{}, fmt.Errorf("因子 %q 的哈希不能为空", want.Name)
	}
	primaryHash, err := ParseHashHex(want.PrimaryTraitHash)
	if err != nil {
		return LoadoutConstructedSigil{}, fmt.Errorf("因子 %s 的主词条哈希无效: %w", want.Name, err)
	}
	if primaryHash == 0 || primaryHash == EmptyHash {
		return LoadoutConstructedSigil{}, fmt.Errorf("因子 %s 的主词条哈希不能为空", want.Name)
	}
	primary := cat.LookupTraitByHash(primaryHash)
	if primary == nil {
		return LoadoutConstructedSigil{}, fmt.Errorf("因子 %s 的主词条 0x%08X 不在当前游戏目录中", want.Name, primaryHash)
	}
	sigil := cat.LookupSigilByHash(sigilHash)
	sigilID := fmt.Sprintf("HASH_%08X", sigilHash)
	sigilName := strings.TrimSpace(want.Name)
	opaqueName := isOpaqueLoadoutShareName(sigilName, sigilHash)
	if verifiedName := strings.TrimSpace(sigilDisplayName(sigilHash)); verifiedName != "" {
		sigilName = verifiedName
		opaqueName = false
	}
	if sigil != nil {
		sigilID = sigil.InternalID
		if sigilName == "" || opaqueName {
			sigilName = displaySigilName(sigil)
			opaqueName = false
		}
	}
	item := QueueItem{
		SigilID: sigilID, SigilName: sigilName, Level: want.Level,
		PrimaryTraitID: primary.InternalID, PrimaryTraitName: cnTrait(primary.DisplayName), PrimaryLevel: want.PrimaryTraitLevel,
		Quantity: 1,
	}
	exactSecondaryHash := ""
	if strings.TrimSpace(want.SecondaryTraitHash) != "" {
		secondaryHash, parseErr := ParseHashHex(want.SecondaryTraitHash)
		if parseErr != nil {
			return LoadoutConstructedSigil{}, fmt.Errorf("因子 %s 的副词条哈希无效: %w", want.Name, parseErr)
		}
		if secondaryHash == 0 || secondaryHash == EmptyHash {
			return LoadoutConstructedSigil{}, fmt.Errorf("因子 %s 的副词条哈希不能为空", want.Name)
		}
		secondary := cat.LookupTraitByHash(secondaryHash)
		if secondary == nil {
			return LoadoutConstructedSigil{}, fmt.Errorf("因子 %s 的副词条 0x%08X 不在当前游戏目录中", want.Name, secondaryHash)
		}
		item.SecondaryTraitID = secondary.InternalID
		item.SecondaryTraitName = cnTrait(secondary.DisplayName)
		item.SecondaryLevel = want.SecondaryTraitLevel
		exactSecondaryHash = hashText(secondaryHash)
	}
	if item.SigilName == "" || opaqueName {
		item.SigilName = loadoutSigilDisplayNameFromTraits(sigilHash, item.PrimaryTraitName, item.SecondaryTraitName)
	}
	draft := LoadoutConstructedSigil{
		Index: index, Item: item,
		ExactSigilHash: hashText(sigilHash), ExactPrimaryTraitHash: hashText(primaryHash),
		ExactSecondaryTraitHash: exactSecondaryHash,
	}
	if _, err := prepareLoadoutSigil(cat, draft); err != nil {
		return LoadoutConstructedSigil{}, fmt.Errorf("因子第 %d 格无法构造: %w", index+1, err)
	}
	return draft, nil
}

func resolveLoadoutShare(path, expectCharaHash string, share *LoadoutShare) (*LoadoutImportDraft, error) {
	if share == nil || share.Format != loadoutShareFormat || share.Version < loadoutShareLegacyVersion || share.Version > loadoutShareVersion {
		return nil, fmt.Errorf("不是受支持的单套配装文件（需要 %s v%d..v%d）", loadoutShareFormat, loadoutShareLegacyVersion, loadoutShareVersion)
	}
	if err := normalizeEnhancementNodeValues(share.Character); err != nil {
		return nil, err
	}
	if len(share.Summons) > 0 && len(share.Summons) != 4 {
		return nil, fmt.Errorf("分享文件的召唤石指纹需要恰好 4 个，得到 %d 个", len(share.Summons))
	}
	if share.Version >= 4 && len(share.OverLimit) != 4 {
		return nil, fmt.Errorf("v4 配装的上限突破配置需要恰好 4 个槽，得到 %d 个", len(share.OverLimit))
	}
	if share.Version >= 5 {
		if len(share.WeaponSkillHashes) != 5 {
			return nil, fmt.Errorf("v%d 配装的武器技能快照需要恰好 5 槽", share.Version)
		}
		for index, value := range share.WeaponSkillHashes {
			if _, err := ParseHashHex(value); err != nil {
				return nil, fmt.Errorf("v%d 配装的武器技能槽 %d 无效: %w", share.Version, index+1, err)
			}
		}
		if share.Character == nil || len(share.Character.EnhancementPanel) != 2 {
			return nil, fmt.Errorf("v%d 配装缺少角色强化双槽快照", share.Version)
		}
	}
	if share.Version == 5 {
		if share.Weapon != nil && share.Weapon.Wrightstone != nil {
			_, err := ParseHashHex(share.Weapon.WrightstoneReference)
			if err != nil {
				return nil, fmt.Errorf("v5 配装缺少有效的 2814 兼容字段")
			}
		}
		if share.Weapon != nil && !share.Weapon.ExactState {
			return nil, fmt.Errorf("v5 配装缺少完整的 2813/2815 武器状态标记")
		}
	}
	if share.Version >= 6 {
		if share.Character == nil || len(share.Character.EnhancementNodes) == 0 {
			return nil, fmt.Errorf("v%d 配装缺少 1602 角色强化节点快照", share.Version)
		}
		seenNodes := make(map[int]bool, len(share.Character.EnhancementNodes))
		for _, node := range share.Character.EnhancementNodes {
			if node.Index < 0 || node.Index >= 1000 || seenNodes[node.Index] {
				return nil, fmt.Errorf("v%d 配装的角色强化节点 %d 越界或重复", share.Version, node.Index)
			}
			seenNodes[node.Index] = true
		}
	}
	if share.Version >= 8 && (share.Character == nil || !share.Character.WeaponWrightstonesCaptured) {
		return nil, fmt.Errorf("v%d 配装缺少整组武器祝福快照", share.Version)
	}
	if share.Version >= 9 && (share.Character == nil || !share.Character.CharacterBaseCaptured) {
		return nil, fmt.Errorf("v%d 配装缺少角色等级基础快照", share.Version)
	}
	if share.Version >= 4 {
		seen := make(map[int]bool, 4)
		for _, slot := range share.OverLimit {
			if slot.Index < 0 || slot.Index >= 4 || seen[slot.Index] {
				return nil, fmt.Errorf("v4 配装的上限突破槽位 %d 无效或重复", slot.Index+1)
			}
			seen[slot.Index] = true
		}
	}
	indexedSigils := share.Version >= 2
	if indexedSigils {
		if len(share.Sigils) > loadoutMaxSigils {
			return nil, fmt.Errorf("v%d 配装含 %d 个因子，超过 %d 格上限", share.Version, len(share.Sigils), loadoutMaxSigils)
		}
		seenIndices := make(map[int]bool, len(share.Sigils))
		for i, sigil := range share.Sigils {
			if sigil.Index == nil {
				return nil, fmt.Errorf("v%d 配装的第 %d 个因子缺少原始槽位索引", share.Version, i+1)
			}
			index := *sigil.Index
			if index < 0 || index >= loadoutMaxSigils {
				return nil, fmt.Errorf("v%d 配装的因子槽位索引 %d 越界（应为 0..%d）", share.Version, index, loadoutMaxSigils-1)
			}
			if seenIndices[index] {
				return nil, fmt.Errorf("v%d 配装的因子槽位索引 %d 重复", share.Version, index)
			}
			seenIndices[index] = true
		}
	}
	app := &App{}
	ctx, err := app.LoadoutEditContext(path, expectCharaHash)
	if err != nil {
		return nil, err
	}
	if share.OwnerCode != "" && ctx.OwnerCode != "" && share.OwnerCode != ctx.OwnerCode {
		return nil, fmt.Errorf("配装属于 %s（%s），当前角色是 %s（%s）", share.CharaName, share.OwnerCode, ctx.CharaName, ctx.OwnerCode)
	}
	if share.OwnerCode == "" && !strings.EqualFold(share.CharaHash, expectCharaHash) {
		return nil, fmt.Errorf("配装角色 %s 与当前角色 %s 不一致", share.CharaName, ctx.CharaName)
	}

	statContext, err := app.LoadoutStatContext(path, expectCharaHash)
	if err != nil {
		return nil, fmt.Errorf("读取目标角色养成状态失败: %w", err)
	}
	sourceMaster := masterGrowth{}
	if share.Character != nil {
		sourceMaster = deriveMasterGrowth(share.Character.MasterTotalMSP)
	}
	draft := &LoadoutImportDraft{
		Name:              share.Name,
		WeaponSkillHashes: append([]string(nil), share.WeaponSkillHashes...),
		Capabilities: LoadoutImportCapabilities{
			TargetCharacterLevel: statContext.Level, TargetFateDataAvailable: statContext.PermanentGrowth.FateDataAvailable,
			TargetFateEpisodeCount: statContext.PermanentGrowth.FateEpisodeCount, TargetMasterProgressIndex: statContext.PermanentGrowth.MasterProgressIndex,
			TargetMasterLevel: statContext.PermanentGrowth.MasterLevel, TargetMasterSystem: statContext.PermanentGrowth.MasterSystemAvailable,
			TargetSummonSystem: statContext.SummonSystemAvailable, SourceMasterProgressIndex: sourceMaster.ProgressIndex,
			SourceMasterLevel: sourceMaster.MasterLevel,
		},
	}
	if len(draft.WeaponSkillHashes) == 0 && share.Weapon != nil && len(share.Weapon.SkillHashes) == 5 {
		// v3/v4 没有独立导出 3005；旧文件以源武器 2818 作为兼容快照。
		draft.WeaponSkillHashes = append([]string(nil), share.Weapon.SkillHashes...)
	}
	if share.Character != nil {
		draft.Capabilities.SourceCharacterLevel = share.Character.CharacterLevel
		draft.Capabilities.SourceCharacterBaseCaptured = share.Character.CharacterBaseCaptured
	}
	if share.Version >= 3 {
		draft.ApplyPayload = &LoadoutImportApplyPayload{Character: share.Character, Weapon: share.Weapon}
		configureLogsImportApplyPayload(share, draft.ApplyPayload)
		if shouldApplyOrdinaryWeaponEnhancement(share) {
			draft.ApplyPayload.ApplyWeaponEnhancement = true
		}
		if share.Weapon != nil && share.Weapon.Wrightstone != nil {
			draft.ApplyPayload.ApplyWeaponWrightstone = true
		}
		if share.Version >= 4 {
			draft.ApplyPayload.OverLimit = append([]LoadoutShareOverLimit(nil), share.OverLimit...)
			draft.ApplyPayload.ApplyOverLimit = true
		}
	}
	if share.Version < 7 && share.Weapon != nil && share.Weapon.Wrightstone != nil {
		draft.addMissing("wrightstone", "旧版配装文件没有保存武器实际生效的祝福词条；请用新版从源存档重新导出")
	}
	if share.Version < 6 && share.Character != nil {
		draft.addMissing("characterGrowth", "旧版配装文件没有保存 1602 角色强化节点；请用新版从源存档重新导出")
	}
	if len(share.Summons) > 0 {
		draft.SummonSlotIDs = make([]uint32, len(share.Summons))
		usedSummons := make(map[uint32]bool, len(share.Summons))
		for index, want := range share.Summons {
			matches := make([]LoadoutSummon, 0, 2)
			for _, summon := range statContext.Summons {
				if usedSummons[summon.SlotID] || !sameSharedSummon(summon, want) {
					continue
				}
				matches = append(matches, summon)
			}
			if len(matches) >= 1 {
				draft.SummonSlotIDs[index] = matches[0].SlotID
				usedSummons[matches[0].SlotID] = true
				continue
			}
			label := strings.TrimSpace(want.Name)
			if label == "" {
				label = want.TypeHash
			}
			if share.Version >= 3 && draft.ApplyPayload != nil {
				typeHash, typeErr := ParseHashHex(want.TypeHash)
				mainHash, mainErr := ParseHashHex(want.MainTraitHash)
				subHash, subErr := ParseHashHex(want.SubParamHash)
				if typeErr != nil || mainErr != nil || subErr != nil || want.MainTraitLevel < 0 || want.SubParamLevel < 0 || want.Rank < 0 {
					return nil, fmt.Errorf("召唤石第 %d 槽的分享字段无效", index+1)
				}
				draft.ApplyPayload.ConstructedSummons = append(draft.ApplyPayload.ConstructedSummons, LoadoutConstructedSummon{
					Index: index, Name: label, State: SummonTraitState{TypeHash: typeHash, MainTraitHash: mainHash, SubParamHash: subHash,
						MainTraitLevel: uint32(want.MainTraitLevel), SubParamLevel: uint32(want.SubParamLevel), Rank: uint32(want.Rank)},
				})
			} else {
				draft.addMissing("summons", fmt.Sprintf("召唤石第 %d 槽：%s", index+1, label))
			}
		}
	}
	if indexedSigils {
		draft.SigilSlotIDs = make([]uint32, loadoutMaxSigils)
	} else {
		draft.SigilSlotIDs = make([]uint32, len(share.Sigils))
	}
	if share.WeaponHash != "" {
		for _, weapon := range ctx.Weapons {
			if sameSharedWeaponIdentity(weapon.Hash, share.WeaponHash) {
				draft.WeaponSlotID = weapon.SlotID
				break
			}
		}
		if draft.WeaponSlotID == 0 {
			constructed, constructErr := loadoutShareEquippedWeaponConstruction(share)
			if constructErr != nil || draft.ApplyPayload == nil {
				draft.addMissing("weapon", "武器："+share.WeaponName)
			} else {
				draft.ApplyPayload.ConstructedWeapon = constructed
			}
		}
	}

	cat, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	for fallbackIndex, want := range share.Sigils {
		index := fallbackIndex
		if indexedSigils {
			index = *want.Index
		}
		constructed, err := loadoutShareConstructedSigil(cat, want, index)
		if err != nil {
			return nil, err
		}
		draft.ConstructedSigils = append(draft.ConstructedSigils, constructed)
	}

	for _, skill := range share.Skills {
		hash, parseErr := ParseHashHex(skill.Hash)
		if parseErr != nil || !skillBelongsToOwner(hash, ctx.OwnerCode) {
			draft.addMissing("skills", "技能："+skill.Name)
			continue
		}
		draft.SkillHashes = append(draft.SkillHashes, strings.ToUpper(skill.Hash))
	}
	mastery := make([]uint32, 0, len(share.MasteryHashes))
	activeMastery := make([]uint32, 0, len(share.MasteryHashes))
	for _, value := range share.MasteryHashes {
		hash, parseErr := ParseHashHex(value)
		if parseErr != nil {
			return nil, fmt.Errorf("分享文件含无效专精节点 %q", value)
		}
		mastery = append(mastery, hash)
		if hash != 0 && hash != EmptyHash {
			activeMastery = append(activeMastery, hash)
		}
	}
	if _, err := validateMasteryQuota(activeMastery, ctx.OwnerCode, len(activeMastery) > 0); err != nil {
		return nil, fmt.Errorf("分享文件的专精配置无效: %w", err)
	}
	for _, hash := range mastery {
		draft.MasteryHashes = append(draft.MasteryHashes, fmt.Sprintf("%08X", hash))
	}
	return draft, nil
}

func shouldApplyOrdinaryWeaponEnhancement(share *LoadoutShare) bool {
	return share != nil && !share.LogsImport && share.Version >= 5 && share.Weapon != nil && share.Weapon.ExactState && len(share.Weapon.SkillHashes) == 5
}

func configureLogsImportApplyPayload(share *LoadoutShare, payload *LoadoutImportApplyPayload) {
	if share == nil || payload == nil || !share.LogsImport {
		return
	}
	if character := share.Character; character != nil && character.MasterProgressCaptured && character.MasterProgressIndex >= 1 && character.MasterProgressIndex <= 55 {
		payload.ApplyMasterProgress = true
		payload.MasterProgressIndex = character.MasterProgressIndex
	}
	if share.Weapon != nil && share.Weapon.EnhancementCaptured {
		payload.ApplyWeaponEnhancement = true
	} else if share.Weapon != nil && share.Weapon.MirageCaptured {
		payload.ApplyWeaponMirage = true
	}
}

func safeLoadoutFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	if name == "" {
		return "GBFR配装"
	}
	return name
}

func marshalLoadoutShare(share *LoadoutShare) ([]byte, error) {
	if share == nil {
		return json.Marshal(share)
	}
	// Keep Wails/API JSON on the verbose in-memory shape. Only the file export
	// uses the fixed-position array so the generated frontend model continues
	// to receive enhancementNodes for the import apply round-trip.
	copyValue := *share
	if share.Character != nil {
		character := *share.Character
		character.EnhancementNodeValues = nil
		if values, compact := compactEnhancementNodeValues(character.EnhancementNodes); compact {
			character.EnhancementNodes = nil
			character.EnhancementNodeValues = values
		}
		copyValue.Character = &character
	}
	return json.MarshalIndent(&copyValue, "", "  ")
}

func unmarshalLoadoutShare(payload []byte) (*LoadoutShare, error) {
	var share LoadoutShare
	if err := json.Unmarshal(payload, &share); err != nil {
		return nil, err
	}
	if err := normalizeEnhancementNodeValues(share.Character); err != nil {
		return nil, err
	}
	return &share, nil
}

// LoadoutExport 只导出当前角色的一个预设槽，绝不包含批量或整份存档数据。
func (a *App) LoadoutExport(savePath string, unitID uint32) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	share, err := buildLoadoutShare(savePath, unitID)
	if err != nil {
		return "", err
	}
	outputPath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "导出当前单套配装", DefaultDirectory: filepath.Dir(savePath),
		DefaultFilename: safeLoadoutFilename(share.CharaName+"_"+share.Name) + ".gbfr-loadout.json",
		Filters:         []runtime.FileFilter{{DisplayName: "GBFR 单套配装 (*.gbfr-loadout.json)", Pattern: "*.gbfr-loadout.json"}},
	})
	if err != nil || outputPath == "" {
		return outputPath, err
	}
	payload, err := marshalLoadoutShare(share)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(outputPath, payload, 0600); err != nil {
		return "", err
	}
	return outputPath, nil
}

// LoadoutImport 读取一份单套配装；因子转为独立构造草稿，其他资源映射到当前存档。
// 本方法只返回草稿，不自动写档。
func (a *App) LoadoutImport(savePath, expectCharaHash string) (*LoadoutImportDraft, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("Wails 上下文未初始化")
	}
	inputPath, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "导入单套配装", Filters: []runtime.FileFilter{{DisplayName: "GBFR 单套配装 (*.gbfr-loadout.json)", Pattern: "*.gbfr-loadout.json"}},
	})
	if err != nil || inputPath == "" {
		return nil, err
	}
	info, err := os.Stat(inputPath)
	if err != nil {
		return nil, err
	}
	if info.Size() > loadoutShareMaxSize {
		return nil, fmt.Errorf("配装文件超过 1 MiB，拒绝读取")
	}
	payload, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	share, err := unmarshalLoadoutShare(payload)
	if err != nil {
		return nil, fmt.Errorf("配装 JSON 无效: %w", err)
	}
	return resolveLoadoutShare(savePath, expectCharaHash, share)
}
