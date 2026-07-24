package backend

import "fmt"

const (
	WrightstoneMaxSlotIDType uint32 = 2101
	WrightstoneItemIDType    uint32 = 2102
	WrightstoneSlotIDType    uint32 = 2103
	WrightstoneBoolIDType    uint32 = 2104
	WrightstoneFlagsIDType   uint32 = 2105
	WrightstoneSlotBaseID           = 50000
	WrightstoneTraitSlotBase        = 140000000
	NormalWrightstoneFlags   uint32 = 2
)

func (s *SaveData) GetMaxWrightstoneSlotID() (int, error) {
	entry, ok := s.findUnit(WrightstoneMaxSlotIDType, 0)
	if !ok {
		return 0, fmt.Errorf("找不到 ITEM_UNK_MAX_SLOT_ID (2101)")
	}
	return int(entry.Uint32()), nil
}

func (s *SaveData) SetMaxWrightstoneSlotID(id int) error {
	entry, ok := s.findUnit(WrightstoneMaxSlotIDType, 0)
	if !ok {
		return fmt.Errorf("找不到 ITEM_UNK_MAX_SLOT_ID (2101)")
	}
	entry.SetUint32(uint32(id))
	return nil
}

func (s *SaveData) FindEmptyWrightstoneSlots(count int) ([]int, error) {
	allItemUnits := s.findAllUnitsByType(WrightstoneItemIDType)
	var emptyIDs []int
	for _, u := range allItemUnits {
		if int(u.UnitID) >= WrightstoneSlotBaseID && u.Uint32() == EmptyHash {
			emptyIDs = append(emptyIDs, int(u.UnitID))
			if len(emptyIDs) >= count {
				break
			}
		}
	}
	if len(emptyIDs) < count {
		return nil, fmt.Errorf("空祝福槽不足 (需要 %d, 找到 %d)", count, len(emptyIDs))
	}
	return emptyIDs, nil
}

func (s *SaveData) GetOccupiedWrightstoneCount() int {
	allItemUnits := s.findAllUnitsByType(WrightstoneItemIDType)
	count := 0
	for _, u := range allItemUnits {
		if int(u.UnitID) >= WrightstoneSlotBaseID && u.Uint32() != EmptyHash {
			count++
		}
	}
	return count
}

func (s *SaveData) PatchWrightstone(itemUnitID, newSlotID int, wrightstoneHash uint32,
	firstTraitHash uint32, firstLevel int,
	secondTraitHash uint32, secondLevel int,
	thirdTraitHash uint32, thirdLevel int) error {

	traitBase := getWrightstoneTraitBase(itemUnitID)

	if err := s.patchUint(WrightstoneItemIDType, uint32(itemUnitID), wrightstoneHash); err != nil {
		return err
	}
	if err := s.patchUint(WrightstoneSlotIDType, uint32(itemUnitID), uint32(newSlotID)); err != nil {
		return err
	}
	if err := s.patchWrightstoneBool(WrightstoneBoolIDType, uint32(itemUnitID), false); err != nil {
		return err
	}
	if err := s.patchUint(WrightstoneFlagsIDType, uint32(itemUnitID), NormalWrightstoneFlags); err != nil {
		return err
	}

	if err := s.patchUint(TraitHashIDType, uint32(traitBase), firstTraitHash); err != nil {
		return err
	}
	if err := s.patchInt(TraitLevelIDType, uint32(traitBase), firstLevel); err != nil {
		return err
	}
	if err := s.patchUint(TraitHashIDType, uint32(traitBase+1), secondTraitHash); err != nil {
		return err
	}
	if err := s.patchInt(TraitLevelIDType, uint32(traitBase+1), secondLevel); err != nil {
		return err
	}
	if err := s.patchUint(TraitHashIDType, uint32(traitBase+2), thirdTraitHash); err != nil {
		return err
	}
	if err := s.patchInt(TraitLevelIDType, uint32(traitBase+2), thirdLevel); err != nil {
		return err
	}
	return nil
}

func (s *SaveData) VerifyWrightstone(itemUnitID int, newSlotID int, wrightstoneHash uint32,
	firstTraitHash uint32, firstLevel int,
	secondTraitHash uint32, secondLevel int,
	thirdTraitHash uint32, thirdLevel int) error {

	traitBase := getWrightstoneTraitBase(itemUnitID)

	check := func(idType, unitID, expected uint32, label string) error {
		entry, ok := s.findUnit(idType, unitID)
		if !ok {
			return fmt.Errorf("验证失败: 找不到 %s", label)
		}
		actual := entry.Uint32()
		if actual != expected {
			return fmt.Errorf("验证失败 %s: 期望 0x%08X, 实际 0x%08X", label, expected, actual)
		}
		return nil
	}
	checkInt := func(idType, unitID uint32, expected int, label string) error {
		entry, ok := s.findUnit(idType, unitID)
		if !ok {
			return fmt.Errorf("验证失败: 找不到 %s", label)
		}
		actual := entry.Int32()
		if int(actual) != expected {
			return fmt.Errorf("验证失败 %s: 期望 %d, 实际 %d", label, expected, actual)
		}
		return nil
	}
	checkBool := func(idType, unitID uint32, expected bool, label string) error {
		entry, ok := s.findUnit(idType, unitID)
		if !ok {
			return fmt.Errorf("验证失败: 找不到 %s", label)
		}
		actual := entry.Bool()
		if actual != expected {
			return fmt.Errorf("验证失败 %s: 期望 %v, 实际 %v", label, expected, actual)
		}
		return nil
	}

	if err := check(WrightstoneItemIDType, uint32(itemUnitID), wrightstoneHash, "祝福哈希"); err != nil {
		return err
	}
	if err := check(WrightstoneSlotIDType, uint32(itemUnitID), uint32(newSlotID), "祝福槽位 ID"); err != nil {
		return err
	}
	if err := checkBool(WrightstoneBoolIDType, uint32(itemUnitID), false, "祝福布尔字段"); err != nil {
		return err
	}
	if err := check(WrightstoneFlagsIDType, uint32(itemUnitID), NormalWrightstoneFlags, "祝福标记"); err != nil {
		return err
	}
	if err := check(TraitHashIDType, uint32(traitBase), firstTraitHash, "第一特性哈希"); err != nil {
		return err
	}
	if err := checkInt(TraitLevelIDType, uint32(traitBase), firstLevel, "第一特性等级"); err != nil {
		return err
	}
	if err := check(TraitHashIDType, uint32(traitBase+1), secondTraitHash, "第二特性哈希"); err != nil {
		return err
	}
	if err := checkInt(TraitLevelIDType, uint32(traitBase+1), secondLevel, "第二特性等级"); err != nil {
		return err
	}
	if err := check(TraitHashIDType, uint32(traitBase+2), thirdTraitHash, "第三特性哈希"); err != nil {
		return err
	}
	if err := checkInt(TraitLevelIDType, uint32(traitBase+2), thirdLevel, "第三特性等级"); err != nil {
		return err
	}
	return nil
}

func (s *SaveData) patchWrightstoneBool(idType, unitID uint32, value bool) error {
	entry, ok := s.findUnit(idType, unitID)
	if !ok {
		return fmt.Errorf("找不到 save unit: IDType=%d, UnitID=%d", idType, unitID)
	}
	entry.SetBool(value)
	return nil
}

func getWrightstoneTraitBase(itemUnitID int) int {
	return WrightstoneTraitSlotBase + ((itemUnitID - WrightstoneSlotBaseID) * 100)
}
