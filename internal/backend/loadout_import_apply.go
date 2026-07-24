package backend

import (
	"fmt"
	"math"
	"strings"
)

type preparedCharacterBase struct {
	level        int
	baseHP       int
	baseATK      int
	baseStunBits uint32
	baseCritRate int
}

type preparedLoadoutImport struct {
	characterUnitID                    uint32
	characterBase                      *preparedCharacterBase
	masterTotalMSP                     *int
	legacyProgress                     *int
	enhancementPanel                   []int
	enhancementNodes                   []LoadoutShareEnhancementNode
	weaponUnitID                       uint32
	weapon                             *LoadoutShareWeaponState
	applyWeaponEnhancement             bool
	applyWeaponWrightstone             bool
	weaponWrightstone                  preparedWeaponWrightstone
	characterWeaponChanges             []ProgressionWeaponChange
	characterWeaponWrightstones        []preparedWeaponWrightstone
	characterWeaponExpected            []progressionWeaponExpected
	characterWeaponWrightstoneExpected []expectedWeaponWrightstone
	overLimit                          []LoadoutShareOverLimit
}

type preparedWeaponWrightstone struct {
	source *LoadoutWeaponWrightstone
	hash   uint32
	traits [3]struct {
		hash  uint32
		level int
	}
}

type expectedWeaponWrightstone struct {
	unitID   uint32
	snapshot preparedWeaponWrightstone
}

func prepareWeaponWrightstone(source *LoadoutWeaponWrightstone) (preparedWeaponWrightstone, error) {
	prepared := preparedWeaponWrightstone{source: source}
	if source == nil {
		return prepared, nil
	}
	hash, err := ParseHashHex(source.Hash)
	if err != nil {
		return prepared, fmt.Errorf("导入武器祝福哈希无效: %w", err)
	}
	prepared.hash = hash
	seen := make(map[int]bool, 3)
	for _, trait := range source.Traits {
		if trait.Index < 0 || trait.Index >= 3 || seen[trait.Index] || trait.Level < 0 {
			return prepared, fmt.Errorf("导入武器祝福词条槽位无效")
		}
		traitHash, traitErr := ParseHashHex(trait.Hash)
		if traitErr != nil {
			return prepared, fmt.Errorf("导入武器祝福词条 %d 无效: %w", trait.Index+1, traitErr)
		}
		seen[trait.Index] = true
		prepared.traits[trait.Index].hash = traitHash
		prepared.traits[trait.Index].level = trait.Level
	}
	return prepared, nil
}

func applyPreparedWeaponWrightstone(save *SaveData, unitID uint32, prepared preparedWeaponWrightstone) error {
	traitBase, err := weaponImbuedTraitUnitBase(unitID)
	if err != nil {
		return err
	}
	stoneHash := prepared.hash
	if prepared.source == nil {
		stoneHash = EmptyHash
	}
	if err := save.patchUint(weaponStoneSubType, unitID, stoneHash); err != nil {
		return err
	}
	for index := 0; index < 3; index++ {
		hash := prepared.traits[index].hash
		level := prepared.traits[index].level
		if prepared.source == nil || hash == 0 {
			hash = EmptyHash
			level = 0
		}
		if err := save.patchUint(TraitHashIDType, traitBase+uint32(index), hash); err != nil {
			return err
		}
		if err := save.patchInt(TraitLevelIDType, traitBase+uint32(index), level); err != nil {
			return err
		}
	}
	return nil
}

func verifyPreparedWeaponWrightstone(save *SaveData, expected expectedWeaponWrightstone) error {
	stone, ok := save.findUnitExact(weaponStoneSubType, expected.unitID)
	wantStone := expected.snapshot.hash
	if expected.snapshot.source == nil {
		wantStone = EmptyHash
	}
	if !ok || stone.ValueCnt != 1 || stone.Uint32() != wantStone {
		return fmt.Errorf("武器 %d 的祝福类型回读不一致", expected.unitID)
	}
	traitBase, err := weaponImbuedTraitUnitBase(expected.unitID)
	if err != nil {
		return err
	}
	for index := 0; index < 3; index++ {
		wantHash := expected.snapshot.traits[index].hash
		wantLevel := expected.snapshot.traits[index].level
		if expected.snapshot.source == nil || wantHash == 0 {
			wantHash = EmptyHash
			wantLevel = 0
		}
		hash, hashOK := save.findUnitExact(TraitHashIDType, traitBase+uint32(index))
		level, levelOK := save.findUnitExact(TraitLevelIDType, traitBase+uint32(index))
		if !hashOK || !levelOK || hash.ValueCnt != 1 || level.ValueCnt != 1 || hash.Uint32() != wantHash || int(level.Int32()) != wantLevel {
			return fmt.Errorf("武器 %d 的祝福第 %d 条生效词条回读不一致", expected.unitID, index+1)
		}
	}
	return nil
}

func masterTotalMSPForProgress(sourceTotal, progressIndex int) (int, error) {
	if progressIndex < 1 || progressIndex >= len(characterMasterExpThresholds) {
		return 0, fmt.Errorf("专精进度必须为 1..%d，收到 %d", len(characterMasterExpThresholds)-1, progressIndex)
	}
	if deriveMasterGrowth(sourceTotal).ProgressIndex == progressIndex {
		return sourceTotal, nil
	}
	return characterMasterExpThresholds[progressIndex], nil
}

func validateImportedMasteryCapacity(save *SaveData, prepared *preparedLoadoutImport, resolved []*resolvedWrite, enabled bool) error {
	if !enabled {
		return nil
	}
	if prepared == nil || prepared.characterUnitID == 0 {
		return fmt.Errorf("导入专精配置缺少目标角色状态")
	}
	level, ok := save.findUnitExact(1308, prepared.characterUnitID)
	if !ok || level.ValueCnt != 1 {
		return fmt.Errorf("目标角色缺少等级字段，无法导入专精")
	}
	effectiveLevel := int(level.Int32())
	if prepared.characterBase != nil {
		effectiveLevel = prepared.characterBase.level
	}
	if effectiveLevel < 100 {
		return fmt.Errorf("导入后的角色等级为 %d；达到 Lv100 后才能导入专精配置", effectiveLevel)
	}
	totalMSPEntry, ok := save.findUnitExact(1323, prepared.characterUnitID)
	if !ok || totalMSPEntry.ValueCnt != 1 {
		return fmt.Errorf("目标角色尚未建立专精等级字段")
	}
	totalMSP := int(totalMSPEntry.Int32())
	if prepared.masterTotalMSP != nil {
		totalMSP = *prepared.masterTotalMSP
	}
	caps := deriveMasterGrowth(totalMSP).MasteryRankCaps
	for _, write := range resolved {
		counts := make(map[string]int, len(caps))
		for _, hash := range write.mastery {
			if hash == 0 || hash == EmptyHash {
				continue
			}
			node, exists := skillboardNodeForHash(hash)
			if !exists {
				return fmt.Errorf("专精节点 %08X 未收录", hash)
			}
			rank, _, exists := masteryRankOfGrp(node.Grp)
			if exists {
				counts[rank]++
			}
		}
		for rank, count := range counts {
			if count > caps[rank] {
				growth := deriveMasterGrowth(totalMSP)
				return fmt.Errorf("目标专精进度 %d 的%s容量为 %d，但导入配置需要 %d；请同时同步足够的专精等级", growth.ProgressIndex, masteryRankLabel(rank), caps[rank], count)
			}
		}
	}
	return nil
}

func loadoutCharacterUnitForHash(save *SaveData, charaHash uint32) (uint32, error) {
	var result uint32
	for _, entry := range save.findAllUnitsByType(1301) {
		if entry.ValueCnt != 1 || entry.Uint32() != charaHash {
			continue
		}
		if result != 0 && result != entry.UnitID {
			return 0, fmt.Errorf("角色 %08X 对应多个存档实例", charaHash)
		}
		result = entry.UnitID
	}
	if result == 0 {
		return 0, fmt.Errorf("存档里找不到角色 %08X", charaHash)
	}
	return result, nil
}

func materializeLoadoutImportWeapon(save *SaveData, changes []LoadoutWrite, payload *LoadoutImportApplyPayload) (bool, error) {
	if payload == nil || payload.ConstructedWeapon == nil {
		return false, nil
	}
	if len(changes) != 1 || changes[0].Op != "write" {
		return false, fmt.Errorf("缺失装备武器只能随一套配装原子补建")
	}
	if changes[0].WeaponSlotID != 0 {
		return false, fmt.Errorf("待补建装备武器与目标存档现有武器冲突")
	}
	if payload.Weapon == nil {
		return false, fmt.Errorf("缺失装备武器没有完整的源武器状态")
	}
	source := payload.ConstructedWeapon
	if source.InternalID == "" || source.Level < 1 || source.Level > 150 ||
		source.Uncap < 0 || source.Uncap > 6 || source.Mirage < 0 || source.Mirage > 99 ||
		source.Awakening < 0 || source.Awakening > 10 || source.Transcendence < 0 || source.Transcendence > 7 {
		return false, fmt.Errorf("待补建装备武器的目录或强化字段无效")
	}
	baseHashText := strings.TrimSpace(source.BaseHash)
	if baseHashText == "" {
		baseHashText = strings.TrimSpace(source.Hash)
	}
	baseHash, err := ParseHashHex(baseHashText)
	if err != nil {
		return false, fmt.Errorf("待补建装备武器哈希无效: %w", err)
	}
	definition, known := progressionWeaponDefForHash(baseHash)
	if !known || definition.InternalID != source.InternalID {
		return false, fmt.Errorf("待补建装备武器 %s 不在当前 2.0.2 目录", source.InternalID)
	}
	added, expected, err := applyProgressionWeaponChange(save, ProgressionWeaponChange{
		Action: "add", Hash: baseHashText, Level: source.Level, Uncap: source.Uncap, Mirage: source.Mirage,
		Awakening: source.Awakening, Transcendence: source.Transcendence, TranscendenceSkill: source.TranscendenceSkill,
	})
	if err != nil {
		return false, fmt.Errorf("补建装备武器失败: %w", err)
	}
	if !added || expected.UnitID == 0 {
		return false, fmt.Errorf("补建装备武器没有生成新实例")
	}
	slot, ok := save.findUnitExact(weaponSlotIDType, expected.UnitID)
	if !ok || slot.ValueCnt != 1 || slot.Uint32() == 0 || slot.Uint32() == EmptyHash {
		return false, fmt.Errorf("补建装备武器没有生成有效 SlotID")
	}
	changes[0].WeaponSlotID = slot.Uint32()
	// 新实例没有可保留的目标强化状态；用分享文件的精确 2813/2815/2818
	// 快照完成初始化，并由统一的武器回读验证覆盖。
	payload.ApplyWeaponEnhancement = true
	return true, nil
}

func prepareLoadoutImport(save *SaveData, changes []LoadoutWrite, payload *LoadoutImportApplyPayload) (*preparedLoadoutImport, error) {
	type requiredField struct {
		id   uint32
		name string
	}
	if payload == nil {
		return nil, nil
	}
	if len(changes) != 1 || changes[0].Op != "write" {
		return nil, fmt.Errorf("完整配装导入一次只能写入一个目标槽")
	}
	if payload.ApplyOverLimit && len(payload.OverLimit) != 4 {
		return nil, fmt.Errorf("上限突破必须提供完整 4 槽快照")
	}
	charaHash, err := ParseHashHex(changes[0].ExpectCharaHash)
	if err != nil {
		return nil, fmt.Errorf("导入目标角色无效: %w", err)
	}
	prepared := &preparedLoadoutImport{}
	needsCharacter := payload.ApplyCharacterLevel || payload.ApplyMasteryConfiguration || payload.ApplyMasterProgress || payload.ApplyCharacterGrowth || payload.ApplyCharacterWeaponCollection || payload.ApplyCharacterWeaponWrightstones || payload.ApplyOverLimit
	if (payload.ApplyMasterProgress || payload.ApplyCharacterGrowth || payload.ApplyCharacterWeaponCollection || payload.ApplyCharacterWeaponWrightstones) && payload.Character == nil {
		return nil, fmt.Errorf("所选导入范围缺少角色养成源数据")
	}
	if payload.ApplyCharacterWeaponWrightstones && !payload.ApplyCharacterWeaponCollection {
		return nil, fmt.Errorf("同步全部武器祝福必须同时选择整组角色武器收藏")
	}
	if needsCharacter {
		if payload.Character != nil && (payload.Character.MasterTotalMSP < 0 || payload.Character.MasterTotalMSP > 0x7FFFFFFF ||
			payload.Character.LegacyProgress < 0 || uint64(payload.Character.LegacyProgress) > uint64(^uint32(0))) {
			return nil, fmt.Errorf("角色强化进度超出存档字段范围")
		}
		unitID, unitErr := loadoutCharacterUnitForHash(save, charaHash)
		if unitErr != nil {
			return nil, unitErr
		}
		currentLevelEntry, levelExists := save.findUnitExact(1308, unitID)
		if !levelExists || currentLevelEntry.ValueCnt != 1 {
			return nil, fmt.Errorf("目标角色缺少等级字段")
		}
		requiresLevel100 := payload.ApplyMasteryConfiguration || payload.ApplyMasterProgress || payload.ApplyCharacterGrowth
		applyCharacterLevel := payload.ApplyCharacterLevel || (requiresLevel100 && currentLevelEntry.Int32() < 100)
		if applyCharacterLevel {
			if payload.Character == nil || !payload.Character.CharacterBaseCaptured {
				return nil, fmt.Errorf("配装没有角色等级基础快照，请用新版从源存档重新导出")
			}
			if requiresLevel100 && payload.Character.CharacterLevel != 100 {
				return nil, fmt.Errorf("专精或角色强化需要 Lv100 来源快照，当前来源为 Lv%d", payload.Character.CharacterLevel)
			}
			stun := math.Float32frombits(payload.Character.BaseStunBits)
			if payload.Character.CharacterLevel < 1 || payload.Character.CharacterLevel > 100 ||
				payload.Character.BaseHP < 0 || payload.Character.BaseHP > 10_000_000 ||
				payload.Character.BaseATK < 0 || payload.Character.BaseATK > 10_000_000 ||
				math.IsNaN(float64(stun)) || math.IsInf(float64(stun), 0) || stun < 0 || stun > 1_000_000 ||
				payload.Character.BaseCritRate < 0 || payload.Character.BaseCritRate > 10_000 {
				return nil, fmt.Errorf("角色等级基础快照超出存档字段范围")
			}
			prepared.characterBase = &preparedCharacterBase{
				level: payload.Character.CharacterLevel, baseHP: payload.Character.BaseHP,
				baseATK: payload.Character.BaseATK, baseStunBits: payload.Character.BaseStunBits,
				baseCritRate: payload.Character.BaseCritRate,
			}
		}
		fields := make([]requiredField, 0, 7)
		if applyCharacterLevel {
			fields = append(fields,
				requiredField{1308, "角色等级"},
				requiredField{1309, "基础 HP"},
				requiredField{1310, "基础攻击"},
				requiredField{1312, "基础昏厥"},
				requiredField{1313, "基础暴击"},
			)
		}
		if payload.ApplyMasterProgress {
			fields = append(fields, requiredField{1323, "专精总 MSP"})
		}
		if payload.ApplyCharacterGrowth {
			fields = append(fields, requiredField{1321, "角色强化进度"}, requiredField{1503, "角色强化面板"})
		}
		for _, field := range fields {
			entry, ok := save.findUnitExact(field.id, unitID)
			wantCount := 1
			if field.id == 1503 {
				wantCount = 2
			}
			if !ok || entry.ValueCnt != wantCount {
				return nil, fmt.Errorf("目标角色尚未建立%s字段，无法安全补写系统结构", field.name)
			}
		}
		prepared.characterUnitID = unitID
		if payload.ApplyMasterProgress {
			total, totalErr := masterTotalMSPForProgress(payload.Character.MasterTotalMSP, payload.MasterProgressIndex)
			if totalErr != nil {
				return nil, totalErr
			}
			prepared.masterTotalMSP = &total
		}
		if payload.ApplyCharacterGrowth {
			if len(payload.Character.EnhancementPanel) != 2 {
				return nil, fmt.Errorf("角色强化导入缺少 1503 双槽快照，请重新导出配装")
			}
			if len(payload.Character.EnhancementNodes) == 0 {
				return nil, fmt.Errorf("角色强化导入缺少 1602 节点快照，请用新版重新导出配装")
			}
			for index, value := range payload.Character.EnhancementPanel {
				if value < 0 {
					return nil, fmt.Errorf("角色强化面板槽 %d 尚未初始化，不能覆盖目标角色", index+1)
				}
			}
			legacy := payload.Character.LegacyProgress
			prepared.legacyProgress = &legacy
			prepared.enhancementPanel = append([]int(nil), payload.Character.EnhancementPanel...)
			nodeBase := uint32(10000000) + (unitID-10000)*1000
			seenNodes := make(map[int]bool, len(payload.Character.EnhancementNodes))
			for _, node := range payload.Character.EnhancementNodes {
				if node.Index < 0 || node.Index >= 1000 || seenNodes[node.Index] {
					return nil, fmt.Errorf("角色强化节点 %d 越界或重复", node.Index)
				}
				entry, exists := save.findUnitExact(1602, nodeBase+uint32(node.Index))
				if !exists || entry.ValueCnt != 1 {
					return nil, fmt.Errorf("目标角色缺少 1602 强化节点 %d，无法安全复制", node.Index)
				}
				seenNodes[node.Index] = true
				prepared.enhancementNodes = append(prepared.enhancementNodes, node)
			}
		}
	}
	if payload.ApplyCharacterWeaponCollection {
		if payload.ApplyCharacterWeaponWrightstones && !payload.Character.WeaponWrightstonesCaptured {
			return nil, fmt.Errorf("配装文件没有整组武器祝福快照，请重新导出")
		}
		inventory := progressionInventory(save, "")
		byInternalID := make(map[string]OwnedProgressionWeapon, len(inventory.Weapons))
		for _, weapon := range inventory.Weapons {
			if weapon.InternalID != "" {
				if _, exists := byInternalID[weapon.InternalID]; !exists {
					byInternalID[weapon.InternalID] = weapon
				}
			}
		}
		seenWeapons := make(map[string]bool, len(payload.Character.Weapons))
		missingWeapons := 0
		for _, weapon := range payload.Character.Weapons {
			if weapon.InternalID == "" || seenWeapons[weapon.InternalID] {
				return nil, fmt.Errorf("角色强化武器目录含空值或重复项 %q", weapon.InternalID)
			}
			seenWeapons[weapon.InternalID] = true
			if weapon.Level < 1 || weapon.Level > 150 || weapon.Uncap < 0 || weapon.Uncap > 6 || weapon.Mirage < 0 || weapon.Mirage > 99 ||
				weapon.Awakening < 0 || weapon.Awakening > 10 || weapon.Transcendence < 0 || weapon.Transcendence > 7 {
				return nil, fmt.Errorf("角色强化武器 %s 的等级字段越界", weapon.InternalID)
			}
			baseHash := strings.TrimSpace(weapon.BaseHash)
			if baseHash == "" {
				baseHash = strings.TrimSpace(weapon.Hash)
			}
			hash, hashErr := ParseHashHex(baseHash)
			if hashErr != nil {
				return nil, fmt.Errorf("角色强化武器 %s 哈希无效: %w", weapon.InternalID, hashErr)
			}
			definition, known := progressionWeaponDefForHash(hash)
			if !known || definition.InternalID != weapon.InternalID {
				return nil, fmt.Errorf("角色强化武器 %s 不在当前 2.0.2 目录", weapon.InternalID)
			}
			change := ProgressionWeaponChange{
				Action: "add", Hash: baseHash, Level: weapon.Level, Uncap: weapon.Uncap, Mirage: weapon.Mirage,
				Awakening: weapon.Awakening, Transcendence: weapon.Transcendence, TranscendenceSkill: weapon.TranscendenceSkill,
			}
			if existing, ok := byInternalID[weapon.InternalID]; ok {
				change.Action = "update"
				change.UnitID = existing.UnitID
			} else {
				missingWeapons++
			}
			prepared.characterWeaponChanges = append(prepared.characterWeaponChanges, change)
			if payload.ApplyCharacterWeaponWrightstones {
				wrightstone, prepareErr := prepareWeaponWrightstone(weapon.Wrightstone)
				if prepareErr != nil {
					return nil, fmt.Errorf("角色武器 %s 的祝福快照无效: %w", weapon.InternalID, prepareErr)
				}
				prepared.characterWeaponWrightstones = append(prepared.characterWeaponWrightstones, wrightstone)
			}
		}
		if missingWeapons > inventory.EmptyWeapons {
			return nil, fmt.Errorf("复制武器收集强化需要 %d 个空武器槽，目标存档只有 %d 个", missingWeapons, inventory.EmptyWeapons)
		}
	}
	if payload.ApplyWeaponEnhancement || payload.ApplyWeaponWrightstone {
		if payload.Weapon == nil {
			return nil, fmt.Errorf("所选武器导入范围缺少源武器数据")
		}
		if changes[0].WeaponSlotID == 0 {
			return nil, fmt.Errorf("导入包含武器强化数据，但目标配装没有武器")
		}
		unitID, unitErr := exactWeaponUnitForSlot(save, changes[0].WeaponSlotID)
		if unitErr != nil {
			return nil, unitErr
		}
		if payload.Weapon.Uncap < 0 || payload.Weapon.Uncap > 6 || payload.Weapon.Mirage < 0 || payload.Weapon.Mirage > 99 ||
			payload.Weapon.Awakening < 0 || payload.Weapon.Awakening > 10 || payload.Weapon.Transcendence < 0 || payload.Weapon.Transcendence > 7 {
			return nil, fmt.Errorf("武器强化等级超出游戏字段范围")
		}
		_, parseErr := ParseHashHex(payload.Weapon.StoredHash)
		if parseErr != nil {
			return nil, fmt.Errorf("导入武器哈希无效: %w", parseErr)
		}
		currentHash, ok := save.findUnitExact(weaponIDType, unitID)
		if !ok || currentHash.ValueCnt != 1 {
			return nil, fmt.Errorf("目标武器缺少 2803 类型字段")
		}
		fields := make([]requiredField, 0, 8)
		if payload.ApplyWeaponEnhancement {
			fields = append(fields,
				requiredField{weaponXPIDType, "经验"},
				requiredField{weaponUncapIDType, "上限突破"},
				requiredField{weaponMirageIDType, "幻晶"},
				requiredField{weaponAwakeIDType, "觉醒"},
				requiredField{weaponTranscendenceIDType, "超凡"},
				requiredField{weaponExtraIDType, "超凡技能"},
			)
			if payload.Weapon.ExactState {
				fields = append(fields, requiredField{weaponFlagsIDType, "武器状态标志"}, requiredField{weaponStateIDType, "武器状态"})
			}
		}
		if payload.ApplyWeaponWrightstone {
			fields = append(fields,
				requiredField{weaponStoneSubType, "祝福类型"},
			)
		}
		for _, field := range fields {
			entry, exists := save.findUnitExact(field.id, unitID)
			if !exists || (field.id == weaponExtraIDType && entry.ValueCnt < 5) || (field.id != weaponExtraIDType && entry.ValueCnt != 1) {
				return nil, fmt.Errorf("目标武器缺少完整的%s字段", field.name)
			}
		}
		if payload.ApplyWeaponWrightstone {
			traitBase, traitBaseErr := weaponImbuedTraitUnitBase(unitID)
			if traitBaseErr != nil {
				return nil, traitBaseErr
			}
			for index := uint32(0); index < 3; index++ {
				for _, field := range []struct {
					id   uint32
					name string
				}{{TraitHashIDType, "哈希"}, {TraitLevelIDType, "等级"}} {
					entry, exists := save.findUnitExact(field.id, traitBase+index)
					if !exists || entry.ValueCnt != 1 {
						return nil, fmt.Errorf("目标武器缺少第 %d 条祝福生效%s字段", index+1, field.name)
					}
				}
			}
		}
		if payload.ApplyWeaponEnhancement && len(payload.Weapon.SkillHashes) != 5 {
			return nil, fmt.Errorf("导入武器必须携带完整的五技能向量")
		}
		if payload.ApplyWeaponEnhancement {
			for index, value := range payload.Weapon.SkillHashes {
				if _, parseErr := ParseHashHex(value); parseErr != nil {
					return nil, fmt.Errorf("导入武器技能槽 %d 无效: %w", index+1, parseErr)
				}
			}
		}
		prepared.weaponUnitID = unitID
		copyValue := *payload.Weapon
		copyValue.SkillHashes = append([]string(nil), payload.Weapon.SkillHashes...)
		prepared.weapon = &copyValue
		prepared.applyWeaponEnhancement = payload.ApplyWeaponEnhancement
		prepared.applyWeaponWrightstone = payload.ApplyWeaponWrightstone
		if payload.ApplyWeaponWrightstone {
			wrightstone, prepareErr := prepareWeaponWrightstone(payload.Weapon.Wrightstone)
			if prepareErr != nil {
				return nil, prepareErr
			}
			prepared.weaponWrightstone = wrightstone
		}
	}
	if payload.ApplyOverLimit {
		seen := make(map[int]bool, 4)
		for _, slot := range payload.OverLimit {
			if slot.Index < 0 || slot.Index >= 4 || seen[slot.Index] {
				return nil, fmt.Errorf("上限突破槽位 %d 无效或重复", slot.Index+1)
			}
			seen[slot.Index] = true
			unitID := uint32(10000000) + (prepared.characterUnitID-10000)*1000 + uint32(slot.Index)
			for _, field := range []uint32{1606, 1607} {
				entry, ok := save.findUnitExact(field, unitID)
				if !ok || entry.ValueCnt != 1 {
					return nil, fmt.Errorf("目标角色上限突破槽 %d 缺少字段 %d", slot.Index+1, field)
				}
			}
			if slot.AttributeHash == "" && slot.Level == 0 {
				continue
			}
			hash, hashErr := ParseHashHex(slot.AttributeHash)
			if hashErr != nil {
				return nil, fmt.Errorf("上限突破槽 %d 属性无效: %w", slot.Index+1, hashErr)
			}
			if _, ok := overLimitCatalog[hash]; !ok || slot.Level < 1 || slot.Level > 10 {
				return nil, fmt.Errorf("上限突破槽 %d 不在已验证属性/等级目录", slot.Index+1)
			}
		}
		prepared.overLimit = append([]LoadoutShareOverLimit(nil), payload.OverLimit...)
	}
	return prepared, nil
}

func materializeLoadoutImportSummons(save *SaveData, changes []LoadoutWrite, payload *LoadoutImportApplyPayload) ([]SummonSaveRecord, error) {
	if payload == nil || len(payload.ConstructedSummons) == 0 {
		return nil, nil
	}
	if len(changes) != 1 || len(changes[0].SummonSlotIDs) != 4 {
		return nil, fmt.Errorf("缺失召唤石只能随一套完整四槽配装生成")
	}
	seen := map[int]bool{}
	for _, draft := range payload.ConstructedSummons {
		if draft.Index < 0 || draft.Index >= 4 || seen[draft.Index] || changes[0].SummonSlotIDs[draft.Index] != 0 {
			return nil, fmt.Errorf("待生成召唤石槽位 %d 无效或已被占用", draft.Index+1)
		}
		if _, err := save.summonRegistrationFlags(draft.State.TypeHash); err != nil {
			return nil, err
		}
		seen[draft.Index] = true
	}
	inventory, err := save.InspectSummonInventory()
	if err != nil {
		return nil, err
	}
	if inventory.Occupied+len(payload.ConstructedSummons) > inventory.Capacity {
		return nil, fmt.Errorf("召唤石空槽不足：需要 %d，剩余 %d", len(payload.ConstructedSummons), inventory.Capacity-inventory.Occupied)
	}
	created := make([]SummonSaveRecord, 0, len(payload.ConstructedSummons))
	for _, draft := range payload.ConstructedSummons {
		record, createErr := save.CreateSummonRecord(draft.State)
		if createErr != nil {
			return nil, fmt.Errorf("生成第 %d 槽召唤石失败: %w", draft.Index+1, createErr)
		}
		changes[0].SummonSlotIDs[draft.Index] = record.SlotID
		created = append(created, record)
	}
	return created, nil
}

func applyPreparedLoadoutImport(save *SaveData, prepared *preparedLoadoutImport) (int, error) {
	if prepared == nil {
		return 0, nil
	}
	verified := 0
	if prepared.characterBase != nil {
		for _, field := range []struct {
			idType uint32
			value  uint32
		}{
			{1308, uint32(int32(prepared.characterBase.level))},
			{1309, uint32(int32(prepared.characterBase.baseHP))},
			{1310, uint32(int32(prepared.characterBase.baseATK))},
			{1312, prepared.characterBase.baseStunBits},
			{1313, uint32(int32(prepared.characterBase.baseCritRate))},
		} {
			if err := save.patchUintExact(field.idType, prepared.characterUnitID, field.value); err != nil {
				return verified, err
			}
		}
		verified++
	}
	for index, change := range prepared.characterWeaponChanges {
		_, expected, err := applyProgressionWeaponChange(save, change)
		if err != nil {
			return verified, fmt.Errorf("同步角色强化武器失败: %w", err)
		}
		prepared.characterWeaponExpected = append(prepared.characterWeaponExpected, expected)
		if len(prepared.characterWeaponWrightstones) > 0 {
			if index >= len(prepared.characterWeaponWrightstones) {
				return verified, fmt.Errorf("整组武器祝福快照数量与武器数量不一致")
			}
			snapshot := prepared.characterWeaponWrightstones[index]
			if err := applyPreparedWeaponWrightstone(save, expected.UnitID, snapshot); err != nil {
				return verified, fmt.Errorf("同步角色武器 %d 的祝福失败: %w", expected.UnitID, err)
			}
			prepared.characterWeaponWrightstoneExpected = append(prepared.characterWeaponWrightstoneExpected, expectedWeaponWrightstone{unitID: expected.UnitID, snapshot: snapshot})
		}
	}
	if prepared.masterTotalMSP != nil {
		if err := save.patchInt(1323, prepared.characterUnitID, *prepared.masterTotalMSP); err != nil {
			return verified, err
		}
		verified++
	}
	if prepared.legacyProgress != nil {
		if err := save.patchUint(1321, prepared.characterUnitID, uint32(*prepared.legacyProgress)); err != nil {
			return verified, err
		}
		verified++
	}
	if len(prepared.enhancementPanel) == 2 {
		panel, ok := save.findUnitExact(1503, prepared.characterUnitID)
		if !ok || panel.ValueCnt != 2 {
			return verified, fmt.Errorf("角色强化面板字段缺失")
		}
		for index, value := range prepared.enhancementPanel {
			if err := panel.SetInt32At(index, int32(value)); err != nil {
				return verified, err
			}
		}
		verified++
	}
	if len(prepared.enhancementNodes) > 0 {
		nodeBase := uint32(10000000) + (prepared.characterUnitID-10000)*1000
		for _, node := range prepared.enhancementNodes {
			if err := save.patchInt(1602, nodeBase+uint32(node.Index), node.Value); err != nil {
				return verified, err
			}
		}
		verified++
	}
	if prepared.weapon != nil {
		if prepared.applyWeaponEnhancement {
			storedHash, _ := ParseHashHex(prepared.weapon.StoredHash)
			if err := save.patchUint(weaponIDType, prepared.weaponUnitID, storedHash); err != nil {
				return verified, err
			}
			if err := save.patchUint(weaponXPIDType, prepared.weaponUnitID, prepared.weapon.XP); err != nil {
				return verified, err
			}
			for _, field := range []struct {
				id    uint32
				value int
			}{
				{weaponUncapIDType, prepared.weapon.Uncap}, {weaponMirageIDType, prepared.weapon.Mirage},
				{weaponAwakeIDType, prepared.weapon.Awakening}, {weaponTranscendenceIDType, prepared.weapon.Transcendence},
			} {
				if err := save.patchInt(field.id, prepared.weaponUnitID, field.value); err != nil {
					return verified, err
				}
			}
			if prepared.weapon.ExactState {
				if err := save.patchUint(weaponFlagsIDType, prepared.weaponUnitID, prepared.weapon.Flags); err != nil {
					return verified, err
				}
				if err := save.patchInt(weaponStateIDType, prepared.weaponUnitID, prepared.weapon.State); err != nil {
					return verified, err
				}
			}
			extra, _ := save.findUnitExact(weaponExtraIDType, prepared.weaponUnitID)
			for index, value := range prepared.weapon.SkillHashes {
				hash, _ := ParseHashHex(strings.TrimSpace(value))
				if err := extra.SetUint32At(index, hash); err != nil {
					return verified, err
				}
			}
		}
		if prepared.applyWeaponWrightstone {
			if err := applyPreparedWeaponWrightstone(save, prepared.weaponUnitID, prepared.weaponWrightstone); err != nil {
				return verified, err
			}
		}
		verified++
	}
	if len(prepared.overLimit) > 0 {
		byIndex := make(map[int]LoadoutShareOverLimit, len(prepared.overLimit))
		for _, slot := range prepared.overLimit {
			byIndex[slot.Index] = slot
		}
		base := uint32(10000000) + (prepared.characterUnitID-10000)*1000
		for index := 0; index < 4; index++ {
			slot := byIndex[index]
			hash := EmptyHash
			levelBit := 0
			if slot.AttributeHash != "" && slot.Level > 0 {
				hash, _ = ParseHashHex(slot.AttributeHash)
				levelBit = 1 << (slot.Level - 1)
			}
			if err := save.patchUint(1606, base+uint32(index), hash); err != nil {
				return verified, err
			}
			if err := save.patchInt(1607, base+uint32(index), levelBit); err != nil {
				return verified, err
			}
		}
		verified++
	}
	return verified, nil
}

func verifyPreparedLoadoutImport(save *SaveData, prepared *preparedLoadoutImport) (int, error) {
	if prepared == nil {
		return 0, nil
	}
	verified := 0
	if prepared.characterBase != nil {
		for _, field := range []struct {
			idType uint32
			name   string
			value  uint32
		}{
			{1308, "角色等级", uint32(int32(prepared.characterBase.level))},
			{1309, "基础 HP", uint32(int32(prepared.characterBase.baseHP))},
			{1310, "基础攻击", uint32(int32(prepared.characterBase.baseATK))},
			{1312, "基础昏厥", prepared.characterBase.baseStunBits},
			{1313, "基础暴击", uint32(int32(prepared.characterBase.baseCritRate))},
		} {
			entry, ok := save.findUnitExact(field.idType, prepared.characterUnitID)
			if !ok || entry.ValueCnt != 1 || entry.Uint32() != field.value {
				return verified, fmt.Errorf("%s回读不一致", field.name)
			}
		}
		verified++
	}
	if len(prepared.characterWeaponExpected) > 0 {
		n, err := verifyProgressionChanges(save, nil, nil, prepared.characterWeaponExpected)
		if err != nil {
			return verified, err
		}
		verified += n
	}
	for _, expected := range prepared.characterWeaponWrightstoneExpected {
		if err := verifyPreparedWeaponWrightstone(save, expected); err != nil {
			return verified, err
		}
		verified++
	}
	if prepared.masterTotalMSP != nil {
		msp, mok := save.findUnitExact(1323, prepared.characterUnitID)
		if !mok || int(msp.Int32()) != *prepared.masterTotalMSP {
			return verified, fmt.Errorf("专精等级回读不一致")
		}
		verified++
	}
	if prepared.legacyProgress != nil {
		legacy, ok := save.findUnitExact(1321, prepared.characterUnitID)
		if !ok || int(legacy.Uint32()) != *prepared.legacyProgress {
			return verified, fmt.Errorf("角色强化进度回读不一致")
		}
		verified++
	}
	if len(prepared.enhancementPanel) == 2 {
		panel, ok := save.findUnitExact(1503, prepared.characterUnitID)
		if !ok || panel.ValueCnt != 2 {
			return verified, fmt.Errorf("角色强化面板回读字段缺失")
		}
		for index, want := range prepared.enhancementPanel {
			got, readErr := panel.Uint32At(index)
			if readErr != nil || int(int32(got)) != want {
				return verified, fmt.Errorf("角色强化面板槽 %d 回读不一致", index+1)
			}
		}
		verified++
	}
	if len(prepared.enhancementNodes) > 0 {
		nodeBase := uint32(10000000) + (prepared.characterUnitID-10000)*1000
		for _, want := range prepared.enhancementNodes {
			entry, ok := save.findUnitExact(1602, nodeBase+uint32(want.Index))
			if !ok || entry.ValueCnt != 1 || int(entry.Int32()) != want.Value {
				return verified, fmt.Errorf("角色强化节点 %d 回读不一致", want.Index)
			}
		}
		verified++
	}
	if prepared.weapon != nil {
		context, err := readLoadoutWeaponContext(save, func() uint32 {
			slot, _ := save.findUnitExact(weaponSlotIDType, prepared.weaponUnitID)
			if slot == nil {
				return 0
			}
			return slot.Uint32()
		}())
		if err != nil {
			return verified, err
		}
		if prepared.applyWeaponEnhancement && !strings.EqualFold(context.StoredHash, prepared.weapon.StoredHash) {
			return verified, fmt.Errorf("武器本体哈希回读不一致")
		}
		if prepared.applyWeaponEnhancement && (context.XP != prepared.weapon.XP || context.Uncap != prepared.weapon.Uncap || context.Mirage != prepared.weapon.Mirage ||
			context.Awakening != prepared.weapon.Awakening || context.Transcendence != prepared.weapon.Transcendence) {
			return verified, fmt.Errorf("武器强化回读不一致")
		}
		if prepared.weapon.ExactState && prepared.applyWeaponEnhancement {
			flags, fok := save.findUnitExact(weaponFlagsIDType, prepared.weaponUnitID)
			state, sok := save.findUnitExact(weaponStateIDType, prepared.weaponUnitID)
			if !fok || !sok || flags.Uint32() != prepared.weapon.Flags || int(state.Int32()) != prepared.weapon.State {
				return verified, fmt.Errorf("武器状态字段回读不一致")
			}
		}
		if prepared.applyWeaponEnhancement {
			extra := readFixedVec(save, weaponExtraIDType, prepared.weaponUnitID, 5)
			if len(extra) != 5 {
				return verified, fmt.Errorf("武器五技能向量回读缺失")
			}
			for index, value := range prepared.weapon.SkillHashes {
				want, _ := ParseHashHex(value)
				if extra[index] != want {
					return verified, fmt.Errorf("武器技能槽 %d 回读不一致", index+1)
				}
			}
		}
		if prepared.applyWeaponWrightstone {
			if err := verifyPreparedWeaponWrightstone(save, expectedWeaponWrightstone{unitID: prepared.weaponUnitID, snapshot: prepared.weaponWrightstone}); err != nil {
				return verified, err
			}
		}
		verified++
	}
	if len(prepared.overLimit) > 0 {
		wantByIndex := make(map[int]LoadoutShareOverLimit, len(prepared.overLimit))
		for _, want := range prepared.overLimit {
			wantByIndex[want.Index] = want
		}
		base := uint32(10000000) + (prepared.characterUnitID-10000)*1000
		for index := 0; index < 4; index++ {
			want := wantByIndex[index]
			attribute, aok := save.findUnitExact(1606, base+uint32(index))
			level, lok := save.findUnitExact(1607, base+uint32(index))
			if !aok || !lok {
				return verified, fmt.Errorf("上限突破槽 %d 回读字段缺失", index+1)
			}
			if want.AttributeHash == "" && want.Level == 0 {
				if attribute.Uint32() != EmptyHash || level.Int32() != 0 {
					return verified, fmt.Errorf("上限突破槽 %d 清空回读不一致", index+1)
				}
				continue
			}
			wantHash, _ := ParseHashHex(want.AttributeHash)
			wantLevel := 1 << (want.Level - 1)
			if attribute.Uint32() != wantHash || int(level.Int32()) != wantLevel {
				return verified, fmt.Errorf("上限突破槽 %d 回读不一致", index+1)
			}
		}
		verified++
	}
	return verified, nil
}
