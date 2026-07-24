package backend

import (
	"fmt"
	"sort"
)

const (
	SummonEquippedIDType   uint32 = 1451
	SummonCatalogIDType    uint32 = 1452
	SummonRegisteredIDType uint32 = 1453
	SummonMaxSlotIDType    uint32 = 1454
	SummonUnlockedIDType   uint32 = 1455
	SummonSlotIDType       uint32 = 1456
	SummonTypeIDType       uint32 = 1457
	SummonTraitsIDType     uint32 = 1458
	SummonLevelsIDType     uint32 = 1459
	SummonRankIDType       uint32 = 1460
	SummonSaveCapacity            = 1000
)

type SummonSaveRecord struct {
	UnitID uint32           `json:"unitId"`
	SlotID uint32           `json:"slotId"`
	State  SummonTraitState `json:"state"`
}

type SummonSaveInventory struct {
	Unlocked  bool               `json:"unlocked"`
	Capacity  int                `json:"capacity"`
	Occupied  int                `json:"occupied"`
	MaxSlotID uint32             `json:"maxSlotId"`
	Records   []SummonSaveRecord `json:"records"`
}

func (s *SaveData) strictSummonUnit(idType, unitID uint32, valueCount int) (*unitEntry, bool) {
	var result *unitEntry
	for _, entry := range s.findAllUnitsByType(idType) {
		if entry.UnitID != unitID || entry.ValueCnt != valueCount || entry.ValueOff < 0 || entry.ValueOff+valueCount*4 > len(s.data) {
			continue
		}
		if result != nil {
			return nil, false
		}
		result = entry
	}
	return result, result != nil
}

func (s *SaveData) requireStrictSummonUnit(idType, unitID uint32, valueCount int) (*unitEntry, error) {
	entry, ok := s.strictSummonUnit(idType, unitID, valueCount)
	if !ok {
		return nil, fmt.Errorf("召唤石存档字段不唯一或缺失: IDType=%d UnitID=%d ValueCount=%d", idType, unitID, valueCount)
	}
	return entry, nil
}

func (s *SaveData) summonSystemUnlocked() (bool, error) {
	entry, err := s.requireStrictSummonUnit(SummonUnlockedIDType, 0, 1)
	if err != nil {
		return false, err
	}
	switch entry.Uint32() {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("召唤系统开放标记 1455=%d，不是已验证的 0/1", entry.Uint32())
	}
}

func (s *SaveData) readSummonSaveRecord(unitID uint32) (SummonSaveRecord, error) {
	slot, err := s.requireStrictSummonUnit(SummonSlotIDType, unitID, 1)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	typeEntry, err := s.requireStrictSummonUnit(SummonTypeIDType, unitID, 1)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	traits, err := s.requireStrictSummonUnit(SummonTraitsIDType, unitID, 2)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	levels, err := s.requireStrictSummonUnit(SummonLevelsIDType, unitID, 2)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	rank, err := s.requireStrictSummonUnit(SummonRankIDType, unitID, 1)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	mainHash, _ := traits.Uint32At(0)
	subHash, _ := traits.Uint32At(1)
	mainLevel, _ := levels.Uint32At(0)
	subLevel, _ := levels.Uint32At(1)
	return SummonSaveRecord{
		UnitID: unitID,
		SlotID: slot.Uint32(),
		State: SummonTraitState{
			TypeHash: typeEntry.Uint32(), MainTraitHash: mainHash, SubParamHash: subHash,
			MainTraitLevel: mainLevel, SubParamLevel: subLevel, Rank: rank.Uint32(),
		},
	}, nil
}

func isEmptySummonSaveRecord(record SummonSaveRecord) bool {
	return record.SlotID == 0 && record.State == (SummonTraitState{
		TypeHash: EmptyHash, MainTraitHash: EmptyHash, SubParamHash: EmptyHash,
		MainTraitLevel: ^uint32(0), SubParamLevel: ^uint32(0), Rank: 0,
	})
}

func (s *SaveData) InspectSummonInventory() (SummonSaveInventory, error) {
	unlocked, err := s.summonSystemUnlocked()
	if err != nil {
		return SummonSaveInventory{}, err
	}
	maxEntry, err := s.requireStrictSummonUnit(SummonMaxSlotIDType, 0, 1)
	if err != nil {
		return SummonSaveInventory{}, err
	}
	result := SummonSaveInventory{
		Unlocked: unlocked, Capacity: SummonSaveCapacity, MaxSlotID: maxEntry.Uint32(),
		Records: make([]SummonSaveRecord, 0, SummonSaveCapacity),
	}
	seenSlots := make(map[uint32]uint32)
	for unitID := uint32(0); unitID < SummonSaveCapacity; unitID++ {
		record, err := s.readSummonSaveRecord(unitID)
		if err != nil {
			return SummonSaveInventory{}, err
		}
		if isEmptySummonSaveRecord(record) {
			continue
		}
		if record.SlotID == 0 || record.State.TypeHash == EmptyHash {
			return SummonSaveInventory{}, fmt.Errorf("召唤石 UnitID %d 是不完整的半空记录", unitID)
		}
		if previous, duplicate := seenSlots[record.SlotID]; duplicate {
			return SummonSaveInventory{}, fmt.Errorf("召唤石 SlotID %d 同时属于 UnitID %d 和 %d", record.SlotID, previous, unitID)
		}
		seenSlots[record.SlotID] = unitID
		result.Records = append(result.Records, record)
	}
	sort.Slice(result.Records, func(i, j int) bool { return result.Records[i].SlotID < result.Records[j].SlotID })
	result.Occupied = len(result.Records)
	if result.MaxSlotID < uint32(result.Occupied) {
		return SummonSaveInventory{}, fmt.Errorf("召唤石 1454=%d 小于已占记录数 %d", result.MaxSlotID, result.Occupied)
	}
	return result, nil
}

func (s *SaveData) summonRegistrationFlags(typeHash uint32) ([]*unitEntry, error) {
	flags := make([]*unitEntry, 0, 1)
	for _, entry := range s.findAllUnitsByType(SummonCatalogIDType) {
		if entry.ValueCnt != 1 || entry.Uint32() != typeHash {
			continue
		}
		flag, err := s.requireStrictSummonUnit(SummonRegisteredIDType, entry.UnitID, 1)
		if err != nil {
			return nil, err
		}
		if flag.Uint32() > 1 {
			return nil, fmt.Errorf("召唤石类型 0x%08X 的 1453 标记=%d，不是 0/1", typeHash, flag.Uint32())
		}
		flags = append(flags, flag)
	}
	if len(flags) == 0 {
		return nil, fmt.Errorf("召唤石类型 0x%08X 不在存档 1452 登记表中", typeHash)
	}
	return flags, nil
}

func registerSummonType(flags []*unitEntry) {
	for _, flag := range flags {
		flag.SetUint32(1)
	}
}

func validateSummonSaveDraft(draft, existing SummonTraitState) error {
	catalog, err := loadSummonStatCatalog()
	if err != nil {
		return err
	}
	if err := validateSummonTraitChange(catalog, draft, existing); err != nil {
		return fmt.Errorf("召唤石字段不在已审计目录/等级范围: %w", err)
	}
	return nil
}

func (s *SaveData) writeSummonSaveState(unitID uint32, draft SummonTraitState) error {
	typeEntry, _ := s.strictSummonUnit(SummonTypeIDType, unitID, 1)
	traits, _ := s.strictSummonUnit(SummonTraitsIDType, unitID, 2)
	levels, _ := s.strictSummonUnit(SummonLevelsIDType, unitID, 2)
	rank, _ := s.strictSummonUnit(SummonRankIDType, unitID, 1)
	if typeEntry == nil || traits == nil || levels == nil || rank == nil {
		return fmt.Errorf("召唤石 UnitID %d 的写入字段在校验后丢失", unitID)
	}
	typeEntry.SetUint32(draft.TypeHash)
	_ = traits.SetUint32At(0, draft.MainTraitHash)
	_ = traits.SetUint32At(1, draft.SubParamHash)
	_ = levels.SetUint32At(0, draft.MainTraitLevel)
	_ = levels.SetUint32At(1, draft.SubParamLevel)
	rank.SetUint32(draft.Rank)
	return nil
}

func (s *SaveData) CreateSummonRecord(draft SummonTraitState) (SummonSaveRecord, error) {
	return s.CreateSummonRecordWithPolicy(draft, false)
}

// CreateSummonRecordWithPolicy optionally enforces catalog/natural rules for
// diagnostics. Normal writes keep those rules advisory while container shape,
// capacity, checksum and readback requirements remain mandatory.
func (s *SaveData) CreateSummonRecordWithPolicy(draft SummonTraitState, enforceNaturalRules bool) (SummonSaveRecord, error) {
	inventory, err := s.InspectSummonInventory()
	if err != nil {
		return SummonSaveRecord{}, err
	}
	if !inventory.Unlocked && enforceNaturalRules {
		return SummonSaveRecord{}, fmt.Errorf("召唤系统尚未由游戏开放；不会通过存档写入强行解锁 DLC 系统")
	}
	if inventory.MaxSlotID == ^uint32(0) {
		return SummonSaveRecord{}, fmt.Errorf("召唤石 MaxSlotID 已溢出")
	}
	emptyState := SummonTraitState{
		TypeHash: EmptyHash, MainTraitHash: EmptyHash, SubParamHash: EmptyHash,
		MainTraitLevel: ^uint32(0), SubParamLevel: ^uint32(0), Rank: 0,
	}
	if enforceNaturalRules {
		if err := validateSummonSaveDraft(draft, emptyState); err != nil {
			return SummonSaveRecord{}, err
		}
	}
	registration, err := s.summonRegistrationFlags(draft.TypeHash)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	var target SummonSaveRecord
	found := false
	for unitID := uint32(0); unitID < SummonSaveCapacity; unitID++ {
		record, readErr := s.readSummonSaveRecord(unitID)
		if readErr != nil {
			return SummonSaveRecord{}, readErr
		}
		if isEmptySummonSaveRecord(record) {
			target = record
			found = true
			break
		}
	}
	if !found {
		return SummonSaveRecord{}, fmt.Errorf("召唤石空槽不足（容量 %d）", SummonSaveCapacity)
	}
	maxEntry, _ := s.strictSummonUnit(SummonMaxSlotIDType, 0, 1)
	slotEntry, _ := s.strictSummonUnit(SummonSlotIDType, target.UnitID, 1)
	if maxEntry == nil || slotEntry == nil {
		return SummonSaveRecord{}, fmt.Errorf("召唤石新增元数据在校验后丢失")
	}
	newSlotID := inventory.MaxSlotID + 1
	if err := s.writeSummonSaveState(target.UnitID, draft); err != nil {
		return SummonSaveRecord{}, err
	}
	slotEntry.SetUint32(newSlotID)
	maxEntry.SetUint32(newSlotID)
	registerSummonType(registration)
	return SummonSaveRecord{UnitID: target.UnitID, SlotID: newSlotID, State: draft}, nil
}

func (s *SaveData) UpdateSummonRecord(expected SummonSaveRecord, draft SummonTraitState) (SummonSaveRecord, error) {
	return s.UpdateSummonRecordWithPolicy(expected, draft, false)
}

func (s *SaveData) UpdateSummonRecordWithPolicy(expected SummonSaveRecord, draft SummonTraitState, enforceNaturalRules bool) (SummonSaveRecord, error) {
	unlocked, err := s.summonSystemUnlocked()
	if err != nil {
		return SummonSaveRecord{}, err
	}
	if !unlocked && enforceNaturalRules {
		return SummonSaveRecord{}, fmt.Errorf("召唤系统尚未由游戏开放；不会修改未开放存档")
	}
	if expected.UnitID >= SummonSaveCapacity || expected.SlotID == 0 {
		return SummonSaveRecord{}, fmt.Errorf("召唤石目标标识无效")
	}
	current, err := s.readSummonSaveRecord(expected.UnitID)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	if current != expected {
		return SummonSaveRecord{}, fmt.Errorf("召唤石记录已变化，请重新加载存档")
	}
	if draft.TypeHash != current.State.TypeHash {
		return s.replaceSummonRecordType(current, draft, enforceNaturalRules)
	}
	if enforceNaturalRules {
		if err := validateSummonSaveDraft(draft, current.State); err != nil {
			return SummonSaveRecord{}, err
		}
	}
	registration, err := s.summonRegistrationFlags(draft.TypeHash)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	if err := s.writeSummonSaveState(expected.UnitID, draft); err != nil {
		return SummonSaveRecord{}, err
	}
	registerSummonType(registration)
	return SummonSaveRecord{UnitID: expected.UnitID, SlotID: expected.SlotID, State: draft}, nil
}

// 换种类按物品新增语义重建 SlotID，并在同一事务迁移四槽装备引用。
func (s *SaveData) replaceSummonRecordType(current SummonSaveRecord, draft SummonTraitState, enforceNaturalRules bool) (SummonSaveRecord, error) {
	inventory, err := s.InspectSummonInventory()
	if err != nil {
		return SummonSaveRecord{}, err
	}
	foundCurrent := false
	for _, record := range inventory.Records {
		if record == current {
			foundCurrent = true
			break
		}
	}
	if !foundCurrent {
		return SummonSaveRecord{}, fmt.Errorf("召唤石目标记录不在完整背包快照中")
	}
	if enforceNaturalRules {
		if err := validateSummonSaveDraft(draft, current.State); err != nil {
			return SummonSaveRecord{}, err
		}
	}
	registration, err := s.summonRegistrationFlags(draft.TypeHash)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	maxEntry, err := s.requireStrictSummonUnit(SummonMaxSlotIDType, 0, 1)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	if maxEntry.Uint32() != inventory.MaxSlotID {
		return SummonSaveRecord{}, fmt.Errorf("召唤石 MaxSlotID 在写入前已变化，请重新加载存档")
	}
	if inventory.MaxSlotID == ^uint32(0) {
		return SummonSaveRecord{}, fmt.Errorf("召唤石 MaxSlotID 已溢出")
	}
	slotEntry, err := s.requireStrictSummonUnit(SummonSlotIDType, current.UnitID, 1)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	equipped, err := s.requireStrictSummonUnit(SummonEquippedIDType, 0, 4)
	if err != nil {
		return SummonSaveRecord{}, err
	}
	newSlotID := inventory.MaxSlotID + 1
	for _, record := range inventory.Records {
		if record.SlotID == newSlotID {
			return SummonSaveRecord{}, fmt.Errorf("召唤石新 SlotID %d 已被 UnitID %d 占用", newSlotID, record.UnitID)
		}
	}
	equippedIndexes := make([]int, 0, equipped.ValueCnt)
	for index := 0; index < equipped.ValueCnt; index++ {
		value, readErr := equipped.Uint32At(index)
		if readErr != nil {
			return SummonSaveRecord{}, readErr
		}
		if value == current.SlotID {
			equippedIndexes = append(equippedIndexes, index)
		}
	}
	if err := s.writeSummonSaveState(current.UnitID, draft); err != nil {
		return SummonSaveRecord{}, err
	}
	slotEntry.SetUint32(newSlotID)
	maxEntry.SetUint32(newSlotID)
	registerSummonType(registration)
	for _, index := range equippedIndexes {
		if err := equipped.SetUint32At(index, newSlotID); err != nil {
			return SummonSaveRecord{}, err
		}
	}
	return SummonSaveRecord{UnitID: current.UnitID, SlotID: newSlotID, State: draft}, nil
}

func (s *SaveData) VerifySummonRecord(expected SummonSaveRecord) error {
	actual, err := s.readSummonSaveRecord(expected.UnitID)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("召唤石回读不一致: got=%+v want=%+v", actual, expected)
	}
	flags, err := s.summonRegistrationFlags(expected.State.TypeHash)
	if err != nil {
		return err
	}
	for _, flag := range flags {
		if flag.Uint32() != 1 {
			return fmt.Errorf("召唤石类型 0x%08X 写入后未完整登记", expected.State.TypeHash)
		}
	}
	return nil
}
