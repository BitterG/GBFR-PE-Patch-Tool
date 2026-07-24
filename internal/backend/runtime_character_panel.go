package backend

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The character-panel reader is intentionally independent of charaManager.
// That legacy scanner targets the 0x5B70 character-use counter array, while
// this reader follows the 2.0.2 runtime status manager and never writes or
// injects code into the game process.
const (
	runtimeCharacterPanelManagerRVA = uintptr(0x7C24980)

	runtimeCharacterPanelVectorBeginOffset = uintptr(0x08)
	runtimeCharacterPanelVectorEndOffset   = uintptr(0x10)
	runtimeCharacterPanelSentinelOffset    = uintptr(0xA30)
	runtimeCharacterPanelBucketTableOffset = uintptr(0xA40)
	runtimeCharacterPanelBucketMaskOffset  = uintptr(0xA58)

	runtimeCharacterPanelBucketStride     = uintptr(0x10)
	runtimeCharacterPanelBucketLastOffset = uintptr(0x00)
	runtimeCharacterPanelBucketHeadOffset = uintptr(0x08)
	runtimeCharacterPanelNodeNextOffset   = uintptr(0x08)
	runtimeCharacterPanelNodeKeyOffset    = uintptr(0x10)
	runtimeCharacterPanelNodeStatusOffset = uintptr(0x30)

	runtimeCharacterPanelHPOffset              = uintptr(0x04)
	runtimeCharacterPanelAttackOffset          = uintptr(0x08)
	runtimeCharacterPanelStunOffset            = uintptr(0x10)
	runtimeCharacterPanelCritOffset            = uintptr(0x14)
	runtimeCharacterPanelReadyOffset           = uintptr(0x5EBC)
	runtimeCharacterPanelEligibilityOffset     = uintptr(0x5EBE)
	runtimeCharacterPanelCharacterHashOffset   = uintptr(0x59F0)
	runtimeCharacterPanelStunDisplayScale      = float32(10)
	runtimeCharacterPermanentAttackOffset      = uintptr(0x58F8)
	runtimeCharacterPermanentHPOffset          = uintptr(0x58FC)
	runtimeCharacterPermanentCritOffset        = uintptr(0x5900)
	runtimeCharacterPermanentStunOffset        = uintptr(0x5904)
	runtimeCharacterBaseLevelOffset            = uintptr(0x5B44)
	runtimeCharacterBaseHPOffset               = uintptr(0x5B48)
	runtimeCharacterBaseAttackOffset           = uintptr(0x5B4C)
	runtimeCharacterBaseStunOffset             = uintptr(0x5B54)
	runtimeCharacterBaseCritOffset             = uintptr(0x5B58)
	runtimeCharacterMasterHPOffset             = uintptr(0x5B64)
	runtimeCharacterMasterAttackOffset         = uintptr(0x5B68)
	runtimeCharacterFateHPOffset               = uintptr(0x5B70)
	runtimeCharacterFateAttackOffset           = uintptr(0x5B74)
	runtimeCharacterWeaponSlotOffset           = uintptr(0x50)
	runtimeCharacterWeaponHashOffset           = uintptr(0x54)
	runtimeCharacterWrightstoneTraitOffset     = uintptr(0x70)
	runtimeCharacterWrightstoneHashOffset      = uintptr(0x88)
	runtimeCharacterEffectiveWeaponSkillOffset = uintptr(0xF4)
	runtimeCharacterEffectiveWeaponSkillCount  = 4
	runtimeCharacterFactorArrayPointerOffset   = uintptr(0x5E60)
	runtimeCharacterFactorRecordStride         = uintptr(0x24)
	runtimeCharacterFactorRecordCount          = 12
	runtimeCharacterFactorPrimaryHashOffset    = uintptr(0x00)
	runtimeCharacterFactorPrimaryLevelOffset   = uintptr(0x04)
	runtimeCharacterFactorSecondaryHashOffset  = uintptr(0x08)
	runtimeCharacterFactorSecondaryLevelOffset = uintptr(0x0C)
	runtimeCharacterFactorItemHashOffset       = uintptr(0x10)
	runtimeCharacterFactorCharacterHashOffset  = uintptr(0x14)
	runtimeCharacterFactorLevelOffset          = uintptr(0x18)
	runtimeCharacterFactorRuntimeSlotOffset    = uintptr(0x1C)

	runtimeCharacterPanelMaxIDs        = 256
	runtimeCharacterPanelMaxChainNodes = 256
	runtimeCharacterPanelMaxHPAttack   = int32(999999)
	runtimeCharacterPanelMaxStun       = float32(999.9000244140625)
	runtimeCharacterPanelMaxCrit       = float32(999)

	runtimeCharacterPanelSource       = "game_runtime_2.0.2"
	runtimeCharacterPanelVerification = "游戏真实回读"

	// The handle has exactly the two rights required by NtQueryInformationProcess
	// and ReadProcessMemory. It deliberately omits PROCESS_VM_WRITE,
	// PROCESS_VM_OPERATION and every injection-capable access right.
	runtimeCharacterPanelProcessAccess = windows.PROCESS_QUERY_INFORMATION | windows.PROCESS_VM_READ
)

type runtimeWeaponTrait struct {
	Hash  uint32
	Level int
}

type runtimeWeaponWrightstoneSnapshot struct {
	WeaponSlotID    uint32
	WeaponHash      uint32
	WrightstoneHash uint32
	Traits          []runtimeWeaponTrait
	Skills          []runtimeWeaponTrait
}

// RuntimeCharacterFactorReading is one occupied factor record copied from the
// character status object's 12-entry runtime loadout array. RuntimeSlotID is
// the game's live inventory identity; it is evidence only and is never used as
// a writable save slot without a separate save-backed resolution.
type RuntimeCharacterFactorReading struct {
	Index               int    `json:"index"`
	ItemHash            string `json:"itemHash"`
	PrimaryTraitHash    string `json:"primaryTraitHash"`
	PrimaryTraitLevel   int    `json:"primaryTraitLevel"`
	SecondaryTraitHash  string `json:"secondaryTraitHash,omitempty"`
	SecondaryTraitLevel int    `json:"secondaryTraitLevel,omitempty"`
	Level               int    `json:"level"`
	RuntimeSlotID       uint32 `json:"runtimeSlotId"`
}

type RuntimeCharacterWeaponTraitReading struct {
	Index int    `json:"index"`
	Hash  string `json:"hash"`
	Level int    `json:"level"`
}

type RuntimeCharacterGrowthReading struct {
	Level        int                        `json:"level"`
	BaseHP       int                        `json:"baseHp"`
	BaseATK      int                        `json:"baseAtk"`
	BaseStun     float64                    `json:"baseStun"`
	BaseCritRate float64                    `json:"baseCritRate"`
	MasterHP     int                        `json:"masterHp"`
	MasterATK    int                        `json:"masterAtk"`
	FateHP       int                        `json:"fateHp"`
	FateATK      int                        `json:"fateAtk"`
	Permanent    LoadoutPermanentPanelStats `json:"permanent"`
	StableReads  int                        `json:"stableReads"`
}

type RuntimeCharacterSnapshotCoverage struct {
	Panel   bool     `json:"panel"`
	Weapon  bool     `json:"weapon"`
	Factors bool     `json:"factors"`
	Growth  bool     `json:"growth"`
	Notes   []string `json:"notes"`
}

func exportRuntimeWeaponTraits(values []runtimeWeaponTrait) []RuntimeCharacterWeaponTraitReading {
	result := make([]RuntimeCharacterWeaponTraitReading, len(values))
	for index, value := range values {
		result[index] = RuntimeCharacterWeaponTraitReading{Index: index, Hash: hashText(value.Hash), Level: value.Level}
	}
	return result
}

func readRuntimeCharacterFactors(memory runtimeCharacterPanelMemory, status uintptr, characterHash uint32) ([]RuntimeCharacterFactorReading, error) {
	array, err := readRuntimePanelPointerOffset(memory, status, runtimeCharacterFactorArrayPointerOffset)
	if err != nil {
		return nil, fmt.Errorf("读取运行时因子数组指针失败: %w", err)
	}
	if array == 0 {
		return nil, fmt.Errorf("运行时因子数组为空")
	}
	buffer := make([]byte, int(runtimeCharacterFactorRecordStride*runtimeCharacterFactorRecordCount))
	if err := memory.ReadAt(array, buffer); err != nil {
		return nil, fmt.Errorf("读取运行时因子数组失败: %w", err)
	}
	readU32 := func(base uintptr, offset uintptr) uint32 {
		return binary.LittleEndian.Uint32(buffer[int(base+offset):])
	}
	result := make([]RuntimeCharacterFactorReading, 0, runtimeCharacterFactorRecordCount)
	for index := 0; index < runtimeCharacterFactorRecordCount; index++ {
		base := uintptr(index) * runtimeCharacterFactorRecordStride
		primaryHash := readU32(base, runtimeCharacterFactorPrimaryHashOffset)
		secondaryHash := readU32(base, runtimeCharacterFactorSecondaryHashOffset)
		itemHash := readU32(base, runtimeCharacterFactorItemHashOffset)
		if (itemHash == 0 || itemHash == EmptyHash) && (primaryHash == 0 || primaryHash == EmptyHash) {
			continue
		}
		if itemHash == 0 || itemHash == EmptyHash || primaryHash == 0 || primaryHash == EmptyHash {
			return nil, fmt.Errorf("运行时因子槽 %d 的物品/主词条不完整", index+1)
		}
		ownerHash := readU32(base, runtimeCharacterFactorCharacterHashOffset)
		if characterHash != 0 && ownerHash != characterHash {
			return nil, fmt.Errorf("运行时因子槽 %d 的角色归属 %08X 与目标 %08X 不一致", index+1, ownerHash, characterHash)
		}
		primaryLevel := int(readU32(base, runtimeCharacterFactorPrimaryLevelOffset))
		secondaryLevel := int(readU32(base, runtimeCharacterFactorSecondaryLevelOffset))
		level := int(readU32(base, runtimeCharacterFactorLevelOffset))
		if primaryLevel <= 0 || primaryLevel > 100 || level <= 0 || level > 100 {
			return nil, fmt.Errorf("运行时因子槽 %d 的等级异常", index+1)
		}
		if secondaryHash == 0 || secondaryHash == EmptyHash {
			secondaryHash, secondaryLevel = 0, 0
		} else if secondaryLevel <= 0 || secondaryLevel > 100 {
			return nil, fmt.Errorf("运行时因子槽 %d 的副词条等级异常", index+1)
		}
		result = append(result, RuntimeCharacterFactorReading{
			Index: index, ItemHash: hashText(itemHash), PrimaryTraitHash: hashText(primaryHash), PrimaryTraitLevel: primaryLevel,
			SecondaryTraitHash: hashText(secondaryHash), SecondaryTraitLevel: secondaryLevel, Level: level,
			RuntimeSlotID: readU32(base, runtimeCharacterFactorRuntimeSlotOffset),
		})
	}
	return result, nil
}

func readStableRuntimeCharacterFactors(readSnapshot func() ([]RuntimeCharacterFactorReading, error)) ([]RuntimeCharacterFactorReading, error) {
	var first []RuntimeCharacterFactorReading
	for index := 0; index < 3; index++ {
		current, err := readSnapshot()
		if err != nil {
			return nil, err
		}
		if index == 0 {
			first = append([]RuntimeCharacterFactorReading(nil), current...)
			continue
		}
		if len(current) != len(first) {
			return nil, fmt.Errorf("运行时因子在连续三次读取间变化")
		}
		for factorIndex := range current {
			if current[factorIndex] != first[factorIndex] {
				return nil, fmt.Errorf("运行时因子在连续三次读取间变化")
			}
		}
	}
	return first, nil
}

func readRuntimeWeaponWrightstoneSnapshot(memory runtimeCharacterPanelMemory, status uintptr) (runtimeWeaponWrightstoneSnapshot, error) {
	buffer := make([]byte, int(runtimeCharacterEffectiveWeaponSkillOffset+runtimeCharacterEffectiveWeaponSkillCount*8))
	if err := memory.ReadAt(status, buffer); err != nil {
		return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("读取运行时武器快照失败: %w", err)
	}
	readU32 := func(offset uintptr) uint32 {
		return binary.LittleEndian.Uint32(buffer[int(offset):])
	}
	snapshot := runtimeWeaponWrightstoneSnapshot{
		WeaponSlotID:    readU32(runtimeCharacterWeaponSlotOffset),
		WeaponHash:      readU32(runtimeCharacterWeaponHashOffset),
		WrightstoneHash: readU32(runtimeCharacterWrightstoneHashOffset),
	}
	if snapshot.WeaponSlotID == 0 || snapshot.WeaponSlotID == EmptyHash || snapshot.WeaponHash == 0 || snapshot.WeaponHash == EmptyHash {
		return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("运行时武器快照没有有效武器")
	}
	for index := 0; index < 3; index++ {
		offset := runtimeCharacterWrightstoneTraitOffset + uintptr(index*8)
		hash, level := readU32(offset), int(readU32(offset+4))
		if hash == 0 || hash == EmptyHash || level <= 0 {
			continue
		}
		if level > 100 {
			return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("运行时武炼结晶词条等级异常: %d", level)
		}
		snapshot.Traits = append(snapshot.Traits, runtimeWeaponTrait{Hash: hash, Level: level})
	}
	for index := 0; index < runtimeCharacterEffectiveWeaponSkillCount; index++ {
		offset := runtimeCharacterEffectiveWeaponSkillOffset + uintptr(index*8)
		hash, level := readU32(offset), int(readU32(offset+4))
		if hash == 0 || hash == EmptyHash || level <= 0 {
			continue
		}
		if level > 100 {
			return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("运行时武器技能等级异常: %d", level)
		}
		snapshot.Skills = append(snapshot.Skills, runtimeWeaponTrait{Hash: hash, Level: level})
	}
	return snapshot, nil
}

func readStableRuntimeWeaponWrightstoneSnapshot(readSnapshot func() (runtimeWeaponWrightstoneSnapshot, error)) (runtimeWeaponWrightstoneSnapshot, error) {
	var first runtimeWeaponWrightstoneSnapshot
	for index := 0; index < 3; index++ {
		current, err := readSnapshot()
		if err != nil {
			return runtimeWeaponWrightstoneSnapshot{}, err
		}
		if index == 0 {
			first = current
			continue
		}
		if current.WeaponSlotID != first.WeaponSlotID || current.WeaponHash != first.WeaponHash || current.WrightstoneHash != first.WrightstoneHash || len(current.Traits) != len(first.Traits) {
			return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("运行时武器快照在连续三次读取间变化")
		}
		for traitIndex := range current.Traits {
			if current.Traits[traitIndex] != first.Traits[traitIndex] {
				return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("运行时武炼结晶词条在连续三次读取间变化")
			}
		}
		if len(current.Skills) != len(first.Skills) {
			return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("运行时武器技能在连续三次读取间变化")
		}
		for skillIndex := range current.Skills {
			if current.Skills[skillIndex] != first.Skills[skillIndex] {
				return runtimeWeaponWrightstoneSnapshot{}, fmt.Errorf("运行时武器技能在连续三次读取间变化")
			}
		}
	}
	return first, nil
}

func loadoutRuntimeWeaponObservation(charaHash, expectedWeaponSlotID uint32) (*LoadoutWeaponWrightstone, []runtimeWeaponTrait, error) {
	process, err := openReadOnlyGameProcess(windowsReadOnlyProcessBackend{}, charaProcessName, runtimeCharacterPanelVersionGuards)
	if err != nil {
		return nil, nil, err
	}
	defer process.Close()
	object, err := locateRuntimeCharacterPanelObject(process, process.moduleBase, charaHash)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := readStableRuntimeWeaponWrightstoneSnapshot(func() (runtimeWeaponWrightstoneSnapshot, error) {
		return readRuntimeWeaponWrightstoneSnapshot(process, object.Status)
	})
	if err != nil {
		return nil, nil, err
	}
	if snapshot.WeaponSlotID != expectedWeaponSlotID {
		return nil, nil, fmt.Errorf("运行时武器槽 %d 与草稿武器槽 %d 不同", snapshot.WeaponSlotID, expectedWeaponSlotID)
	}
	catalog, err := LoadWrightstoneCatalog()
	if err != nil {
		return nil, nil, err
	}
	result := &LoadoutWeaponWrightstone{
		Hash: hashText(snapshot.WrightstoneHash), Evidence: "runtime-2.0.2-three-stable-reads",
		RuntimeObserved: true, StableReads: 3,
	}
	if definition := catalog.LookupWrightstoneByHash(snapshot.WrightstoneHash); definition != nil {
		result.InternalID = definition.InternalID
		result.Name = cnWrightstone(definition.DisplayName)
	}
	for index, observed := range snapshot.Traits {
		trait := catalog.LookupTraitByHash(observed.Hash)
		entry := LoadoutWeaponWrightstoneTrait{Index: index, Hash: hashText(observed.Hash), Level: observed.Level}
		if trait != nil {
			entry.TraitID = trait.InternalID
			entry.Name = cnWrightstoneTrait(trait.DisplayName)
		}
		result.Traits = append(result.Traits, entry)
	}
	return result, append([]runtimeWeaponTrait(nil), snapshot.Skills...), nil
}

type runtimeCharacterPanelVersionGuard struct {
	RVA   uintptr
	Bytes []byte
}

// runtimeCharacterGrowthSnapshot is the character-specific producer input
// immediately next to the final status object. Unlike the on-disk save, it
// reflects the character data currently loaded by the game (including Fate
// episode growth after a save was reloaded or replaced).
type runtimeCharacterGrowthSnapshot struct {
	Level        int
	BaseHP       int
	BaseATK      int
	BaseStun     float64
	BaseCritRate float64
	MasterHP     int
	MasterATK    int
	FateHP       int
	FateATK      int
	Permanent    LoadoutPermanentPanelStats
}

func readRuntimeCharacterGrowthSnapshot(memory runtimeCharacterPanelMemory, status uintptr) (runtimeCharacterGrowthSnapshot, error) {
	readI32 := func(offset uintptr) (int, error) {
		value, err := readRuntimePanelI32Offset(memory, status, offset)
		return int(value), err
	}
	level, err := readI32(runtimeCharacterBaseLevelOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	baseHP, err := readI32(runtimeCharacterBaseHPOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	baseATK, err := readI32(runtimeCharacterBaseAttackOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	baseStun, err := readRuntimePanelF32Offset(memory, status, runtimeCharacterBaseStunOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	baseCrit, err := readI32(runtimeCharacterBaseCritOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	masterHP, err := readI32(runtimeCharacterMasterHPOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	masterATK, err := readI32(runtimeCharacterMasterAttackOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	fateHP, err := readI32(runtimeCharacterFateHPOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	fateATK, err := readI32(runtimeCharacterFateAttackOffset)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	permanent, err := readRuntimePermanentPanelStats(memory, status)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	result := runtimeCharacterGrowthSnapshot{
		Level: level, BaseHP: baseHP, BaseATK: baseATK, BaseStun: float64(baseStun), BaseCritRate: float64(baseCrit),
		MasterHP: masterHP, MasterATK: masterATK, FateHP: fateHP, FateATK: fateATK, Permanent: permanent,
	}
	if result.Level < 1 || result.Level > 200 || result.BaseHP < 0 || result.BaseHP > 999999 || result.BaseATK < 0 || result.BaseATK > 999999 ||
		math.IsNaN(result.BaseStun) || math.IsInf(result.BaseStun, 0) || result.BaseStun < 0 || result.BaseStun > 1000 ||
		result.BaseCritRate < 0 || result.BaseCritRate > 1000 || result.MasterHP < 0 || result.MasterHP > 999999 || result.MasterATK < 0 || result.MasterATK > 999999 ||
		result.FateHP < 0 || result.FateHP > 999999 || result.FateATK < 0 || result.FateATK > 999999 {
		return runtimeCharacterGrowthSnapshot{}, fmt.Errorf("运行时角色成长快照数值异常: %+v", result)
	}
	return result, nil
}

func readStableRuntimeCharacterGrowthSnapshots(readSnapshot func() (runtimeCharacterGrowthSnapshot, error)) (runtimeCharacterGrowthSnapshot, error) {
	var first runtimeCharacterGrowthSnapshot
	for index := 0; index < 3; index++ {
		current, err := readSnapshot()
		if err != nil {
			return runtimeCharacterGrowthSnapshot{}, err
		}
		if index == 0 {
			first = current
			continue
		}
		if current != first {
			return runtimeCharacterGrowthSnapshot{}, fmt.Errorf("运行时角色成长快照在连续三次读取间变化")
		}
	}
	return first, nil
}

func loadoutRuntimeCharacterGrowth(charaHash uint32) (runtimeCharacterGrowthSnapshot, error) {
	process, err := openReadOnlyGameProcess(windowsReadOnlyProcessBackend{}, charaProcessName, runtimeCharacterPanelVersionGuards)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	defer process.Close()
	object, err := locateRuntimeCharacterPanelObject(process, process.moduleBase, charaHash)
	if err != nil {
		return runtimeCharacterGrowthSnapshot{}, err
	}
	return readStableRuntimeCharacterGrowthSnapshots(func() (runtimeCharacterGrowthSnapshot, error) {
		return readRuntimeCharacterGrowthSnapshot(process, object.Status)
	})
}

func readRuntimePermanentPanelStats(memory runtimeCharacterPanelMemory, status uintptr) (LoadoutPermanentPanelStats, error) {
	attack, err := readRuntimePanelF32Offset(memory, status, runtimeCharacterPermanentAttackOffset)
	if err != nil {
		return LoadoutPermanentPanelStats{}, err
	}
	hp, err := readRuntimePanelF32Offset(memory, status, runtimeCharacterPermanentHPOffset)
	if err != nil {
		return LoadoutPermanentPanelStats{}, err
	}
	crit, err := readRuntimePanelF32Offset(memory, status, runtimeCharacterPermanentCritOffset)
	if err != nil {
		return LoadoutPermanentPanelStats{}, err
	}
	stun, err := readRuntimePanelF32Offset(memory, status, runtimeCharacterPermanentStunOffset)
	if err != nil {
		return LoadoutPermanentPanelStats{}, err
	}
	for _, value := range []float32{attack, hp, crit, stun} {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value < 0 || value > 9999999 {
			return LoadoutPermanentPanelStats{}, fmt.Errorf("运行时角色强化聚合值异常: %v/%v/%v/%v", attack, hp, crit, stun)
		}
	}
	return finalizePermanentPanelStats(LoadoutPermanentPanelStats{
		Attack: float64(attack), HP: float64(hp), CritRate: float64(crit), StunRaw: float64(stun),
	}), nil
}

// These anchors were checked byte-for-byte against the shipped 2.0.2 image.
// Guarding the manager lookup, hash-map lookup, ready flag and final-stat
// aggregator prevents an updated executable from being interpreted with stale
// offsets.
var runtimeCharacterPanelVersionGuards = []runtimeCharacterPanelVersionGuard{
	{RVA: 0xD4321, Bytes: []byte{0x48, 0x8B, 0x0D, 0x58, 0x06, 0xB5, 0x07, 0xE8, 0x93, 0x76, 0x20, 0x00}},
	{RVA: 0x2DC081, Bytes: []byte{0x41, 0x8B, 0x55, 0x00, 0x45, 0x8B, 0x84, 0x24, 0x58, 0x0A, 0x00, 0x00, 0x41, 0x21, 0xD0, 0x49, 0x8B, 0x84, 0x24, 0x30, 0x0A, 0x00, 0x00, 0x4D, 0x8B, 0x8C, 0x24, 0x40, 0x0A, 0x00, 0x00, 0x4C, 0x89, 0xC1, 0x48, 0xC1, 0xE1, 0x04, 0x49, 0x8B, 0x4C, 0x09, 0x08}},
	{RVA: 0x2DC11E, Bytes: []byte{0xC6, 0x44, 0x24, 0x38, 0x00, 0x4C, 0x89, 0xE1, 0x4C, 0x89, 0xE2, 0xE8, 0x52, 0x9E, 0x74, 0x00, 0x41, 0xC6, 0x84, 0x24, 0xBC, 0x5E, 0x00, 0x00, 0x01}},
	{RVA: 0xA296F3, Bytes: []byte{0xC5, 0xFA, 0x7E, 0x4B, 0x04, 0xC5, 0xE8, 0x57, 0xD2, 0xC4, 0xE2, 0x71, 0x3D, 0xCA, 0xC4, 0xE2, 0x71, 0x3B, 0x0D, 0xA6, 0xDB, 0xA7, 0x04, 0xC5, 0xF9, 0xD6, 0x4B, 0x04, 0xC5, 0xFB, 0x10, 0x5B, 0x10, 0xC5, 0xE8, 0x5F, 0xD3, 0xC5, 0xFB, 0x12, 0x1D, 0xB0, 0xDB, 0xA7, 0x04, 0xC5, 0xE0, 0x5D, 0xD2, 0xC5, 0xF8, 0x13, 0x53, 0x10}},
}

// RuntimeCharacterPanelStats contains values produced by the game's own 2.0.2
// panel aggregator. Unlike the offline loadout estimate, these fields are not
// recalculated by this application.
type RuntimeCharacterPanelStats struct {
	CharacterHash            string                               `json:"characterHash"`
	RuntimeID                string                               `json:"runtimeId"`
	CandidateObjectHash      string                               `json:"candidateObjectHash"`
	IdentitySource           string                               `json:"identitySource"`
	HP                       int32                                `json:"hp"`
	Attack                   int32                                `json:"attack"`
	StunPower                float32                              `json:"stunPower"`
	RawStunPower             float32                              `json:"rawStunPower"`
	CritRate                 float32                              `json:"critRate"`
	CurrentWeaponSlotID      uint32                               `json:"currentWeaponSlotId,omitempty"`
	CurrentWeaponHash        string                               `json:"currentWeaponHash,omitempty"`
	CurrentWrightstoneHash   string                               `json:"currentWrightstoneHash,omitempty"`
	CurrentWeaponStableReads int                                  `json:"currentWeaponStableReads,omitempty"`
	CurrentWrightstoneTraits []RuntimeCharacterWeaponTraitReading `json:"currentWrightstoneTraits,omitempty"`
	CurrentWeaponSkills      []RuntimeCharacterWeaponTraitReading `json:"currentWeaponSkills,omitempty"`
	CurrentFactors           []RuntimeCharacterFactorReading      `json:"currentFactors,omitempty"`
	CurrentFactorStableReads int                                  `json:"currentFactorStableReads,omitempty"`
	Growth                   *RuntimeCharacterGrowthReading       `json:"growth,omitempty"`
	Coverage                 RuntimeCharacterSnapshotCoverage     `json:"coverage"`
	HPField                  RuntimeCharacterPanelFieldReading    `json:"hpField"`
	AttackField              RuntimeCharacterPanelFieldReading    `json:"attackField"`
	StunField                RuntimeCharacterPanelFieldReading    `json:"stunField"`
	CritField                RuntimeCharacterPanelFieldReading    `json:"critField"`
	Source                   string                               `json:"source"`
	Verification             string                               `json:"verification"`
	GameVersion              string                               `json:"gameVersion"`
	RuntimeVerified          bool                                 `json:"runtimeVerified"`
}

type RuntimeCharacterPanelFieldReading struct {
	RawType        string  `json:"rawType"`
	RelativeOffset uint32  `json:"relativeOffset"`
	RawBits        string  `json:"rawBits"`
	DisplayScale   float32 `json:"displayScale"`
	StableReads    int     `json:"stableReads"`
}

type runtimeCharacterPanelMemory interface {
	ReadAt(address uintptr, destination []byte) error
}

type runtimeCharacterPanelObject struct {
	RuntimeID              uint32
	MapKey                 uint32
	Status                 uintptr
	InIDVector             bool
	Ready                  byte
	Eligibility            byte
	CandidateCharacterHash uint32
}

type runtimeCharacterPanelEnumeration struct {
	VectorIDs []uint32
	Objects   []runtimeCharacterPanelObject
}

type remoteRuntimeCharacterPanelMemory struct {
	handle windows.Handle
}

func (memory remoteRuntimeCharacterPanelMemory) ReadAt(address uintptr, destination []byte) error {
	if len(destination) == 0 {
		return nil
	}
	return readProcessMemory(memory.handle, address, unsafe.Pointer(&destination[0]), uintptr(len(destination)))
}

// LoadoutRuntimePanelStats opens a short-lived, read-only handle to the game,
// reads one requested character's computed panel values and closes the handle.
// It does not reuse or mutate App.hProcess/moduleBase and therefore cannot
// disturb the lifecycle of any memory editor page.
func (a *App) LoadoutRuntimePanelStats(charaHex string) (*RuntimeCharacterPanelStats, error) {
	targetHash, err := ParseHashHex(charaHex)
	if err != nil || targetHash == 0 {
		return nil, fmt.Errorf("角色 hash %q 无效", charaHex)
	}
	pid, err := findProcessByName(charaProcessName)
	if err != nil {
		return nil, fmt.Errorf("未找到游戏进程，请先启动游戏")
	}
	handle, err := windows.OpenProcess(runtimeCharacterPanelProcessAccess, false, pid)
	if err != nil {
		return nil, fmt.Errorf("无法以只读方式打开游戏进程: %w", err)
	}
	defer windows.CloseHandle(handle)

	moduleBase, err := getModuleBase(handle)
	if err != nil {
		return nil, fmt.Errorf("无法读取游戏模块基址: %w", err)
	}
	memory := remoteRuntimeCharacterPanelMemory{handle: handle}
	stats, err := readStableRuntimeCharacterPanelSnapshots(func() (RuntimeCharacterPanelStats, error) {
		return readRuntimeCharacterPanel(memory, moduleBase, targetHash)
	})
	if err != nil {
		return nil, err
	}
	// Equipment identity is optional evidence. A character panel can remain
	// readable while no valid weapon object is loaded, so failure here must not
	// turn an otherwise valid final-panel read into an error.
	if object, locateErr := locateRuntimeCharacterPanelObject(memory, moduleBase, targetHash); locateErr == nil {
		stats.Coverage.Panel = true
		if snapshot, snapshotErr := readStableRuntimeWeaponWrightstoneSnapshot(func() (runtimeWeaponWrightstoneSnapshot, error) {
			return readRuntimeWeaponWrightstoneSnapshot(memory, object.Status)
		}); snapshotErr == nil {
			stats.CurrentWeaponSlotID = snapshot.WeaponSlotID
			stats.CurrentWeaponHash = hashText(snapshot.WeaponHash)
			stats.CurrentWrightstoneHash = hashText(snapshot.WrightstoneHash)
			stats.CurrentWeaponStableReads = 3
			stats.CurrentWrightstoneTraits = exportRuntimeWeaponTraits(snapshot.Traits)
			stats.CurrentWeaponSkills = exportRuntimeWeaponTraits(snapshot.Skills)
			stats.Coverage.Weapon = true
		} else {
			stats.Coverage.Notes = append(stats.Coverage.Notes, "当前武器对象未稳定，未输出武器技能与祝福词条")
		}
		if factors, factorErr := readStableRuntimeCharacterFactors(func() ([]RuntimeCharacterFactorReading, error) {
			return readRuntimeCharacterFactors(memory, object.Status, targetHash)
		}); factorErr == nil {
			stats.CurrentFactors = factors
			stats.CurrentFactorStableReads = 3
			stats.Coverage.Factors = true
		} else {
			stats.Coverage.Notes = append(stats.Coverage.Notes, "当前因子数组未稳定，未输出因子快照")
		}
		if growth, growthErr := readStableRuntimeCharacterGrowthSnapshots(func() (runtimeCharacterGrowthSnapshot, error) {
			return readRuntimeCharacterGrowthSnapshot(memory, object.Status)
		}); growthErr == nil {
			stats.Growth = &RuntimeCharacterGrowthReading{
				Level: growth.Level, BaseHP: growth.BaseHP, BaseATK: growth.BaseATK,
				BaseStun: growth.BaseStun, BaseCritRate: growth.BaseCritRate,
				MasterHP: growth.MasterHP, MasterATK: growth.MasterATK,
				FateHP: growth.FateHP, FateATK: growth.FateATK,
				Permanent: growth.Permanent, StableReads: 3,
			}
			stats.Coverage.Growth = true
		} else {
			stats.Coverage.Notes = append(stats.Coverage.Notes, "角色基础、命运与强化聚合未稳定，未输出成长快照")
		}
	}
	stats.Coverage.Notes = append(stats.Coverage.Notes, "奥义、连锁与战斗条件状态尚无已验证访问链，不以静态推断冒充运行时数据")
	return &stats, nil
}

func readStableRuntimeCharacterPanelSnapshots(readSnapshot func() (RuntimeCharacterPanelStats, error)) (RuntimeCharacterPanelStats, error) {
	if readSnapshot == nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("游戏面板快照读取器为空")
	}
	var snapshots [3]RuntimeCharacterPanelStats
	for index := range snapshots {
		current, err := readSnapshot()
		if err != nil {
			return RuntimeCharacterPanelStats{}, fmt.Errorf("第 %d 次游戏面板快照读取失败: %w", index+1, err)
		}
		snapshots[index] = current
	}
	if !reflect.DeepEqual(snapshots[0], snapshots[1]) || !reflect.DeepEqual(snapshots[0], snapshots[2]) {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("游戏面板在连续 3 次快照间发生变化，请等待数值稳定后重试")
	}
	snapshots[0] = markRuntimeCharacterPanelStable(snapshots[0], len(snapshots))
	return snapshots[0], nil
}

func readRuntimeCharacterPanel(memory runtimeCharacterPanelMemory, moduleBase uintptr, targetHash uint32) (RuntimeCharacterPanelStats, error) {
	object, err := locateRuntimeCharacterPanelObject(memory, moduleBase, targetHash)
	if err != nil {
		return RuntimeCharacterPanelStats{}, err
	}
	stats, err := readRuntimeCharacterPanelValues(memory, object.Status, targetHash)
	if err != nil {
		return RuntimeCharacterPanelStats{}, err
	}
	stats.RuntimeID = hashText(object.RuntimeID)
	stats.CandidateObjectHash = hashText(object.CandidateCharacterHash)
	if object.MapKey == targetHash {
		stats.IdentitySource = "map_key"
	} else {
		stats.IdentitySource = "candidate_object_hash"
	}
	return stats, nil
}

func locateRuntimeCharacterPanelStatus(memory runtimeCharacterPanelMemory, moduleBase uintptr, targetHash uint32) (uintptr, error) {
	object, err := locateRuntimeCharacterPanelObject(memory, moduleBase, targetHash)
	if err != nil {
		return 0, err
	}
	return object.Status, nil
}

func locateRuntimeCharacterPanelObject(memory runtimeCharacterPanelMemory, moduleBase uintptr, targetHash uint32) (runtimeCharacterPanelObject, error) {
	if memory == nil || moduleBase == 0 || targetHash == 0 {
		return runtimeCharacterPanelObject{}, fmt.Errorf("游戏真实面板读取参数无效")
	}
	enumeration, err := enumerateRuntimeCharacterPanelStatuses(memory, moduleBase)
	if err != nil {
		return runtimeCharacterPanelObject{}, err
	}

	// The shipped 2.0.2 manager uses the map key (and matching ID-vector
	// value) as the directory character hash. The previously assumed +0x59F0
	// field is zero in every live object, so it is diagnostic-only and must not
	// veto an exact map-key match. Ready/eligibility are likewise reported but
	// are not hard filters for an exact identity.
	var exact []runtimeCharacterPanelObject
	for _, object := range enumeration.Objects {
		if object.MapKey == targetHash && object.Status != 0 {
			exact = append(exact, object)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return runtimeCharacterPanelObject{}, fmt.Errorf("角色 %08X 的运行时 map key 出现多个状态对象，已拒绝猜测", targetHash)
	}

	// Retain a guarded fallback for layouts where a runtime ID differs from
	// the directory hash but the candidate object hash is populated. Flags are
	// used only to disambiguate this weaker identity source.
	var fallback []runtimeCharacterPanelObject
	for _, object := range enumeration.Objects {
		if object.CandidateCharacterHash == targetHash && object.Status != 0 && object.Ready == 1 && object.Eligibility != 0 {
			fallback = append(fallback, object)
		}
	}
	if len(fallback) == 1 {
		return fallback[0], nil
	}
	if len(fallback) > 1 {
		return runtimeCharacterPanelObject{}, fmt.Errorf("角色 %08X 的候选对象 hash 对应多个状态对象，已拒绝猜测", targetHash)
	}
	return runtimeCharacterPanelObject{}, fmt.Errorf("游戏内尚无角色 %08X 的可用面板结果，请打开角色/装备面板后重试", targetHash)
}

func enumerateRuntimeCharacterPanelStatuses(memory runtimeCharacterPanelMemory, moduleBase uintptr) (runtimeCharacterPanelEnumeration, error) {
	if memory == nil || moduleBase == 0 {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("运行时角色对象枚举参数无效")
	}
	if err := verifyRuntimeCharacterPanelVersion(memory, moduleBase); err != nil {
		return runtimeCharacterPanelEnumeration{}, err
	}
	managerAddress, ok := checkedRuntimePanelAddress(moduleBase, runtimeCharacterPanelManagerRVA)
	if !ok {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态 manager 地址溢出")
	}
	manager, err := readRuntimePanelPointer(memory, managerAddress)
	if err != nil || manager == 0 {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 manager 失败: %w", normalizeRuntimePanelReadError(err))
	}
	begin, err := readRuntimePanelPointerOffset(memory, manager, runtimeCharacterPanelVectorBeginOffset)
	if err != nil {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色 ID 向量起点失败: %w", err)
	}
	end, err := readRuntimePanelPointerOffset(memory, manager, runtimeCharacterPanelVectorEndOffset)
	if err != nil {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色 ID 向量终点失败: %w", err)
	}
	if begin == 0 || end < begin || (end-begin)%4 != 0 || (end-begin)/4 > runtimeCharacterPanelMaxIDs {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色 ID 向量范围异常: begin=0x%X end=0x%X", begin, end)
	}
	result := runtimeCharacterPanelEnumeration{VectorIDs: make([]uint32, 0, (end-begin)/4)}
	vectorSet := make(map[uint32]struct{}, cap(result.VectorIDs))
	for cursor := begin; cursor < end; cursor += 4 {
		id, readErr := readRuntimePanelU32(memory, cursor)
		if readErr != nil {
			return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色 ID 失败: %w", readErr)
		}
		result.VectorIDs = append(result.VectorIDs, id)
		vectorSet[id] = struct{}{}
	}

	sentinel, err := readRuntimePanelPointerOffset(memory, manager, runtimeCharacterPanelSentinelOffset)
	if err != nil || sentinel == 0 {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 map 哨兵失败: %w", normalizeRuntimePanelReadError(err))
	}
	table, err := readRuntimePanelPointerOffset(memory, manager, runtimeCharacterPanelBucketTableOffset)
	if err != nil || table == 0 {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 bucket 表失败: %w", normalizeRuntimePanelReadError(err))
	}
	mask, err := readRuntimePanelU32Offset(memory, manager, runtimeCharacterPanelBucketMaskOffset)
	if err != nil {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 bucket mask 失败: %w", err)
	}
	if mask > 0xFFFF || ((uint64(mask)+1)&uint64(mask)) != 0 {
		return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态 bucket mask 异常: 0x%X", mask)
	}

	seenNodes := make(map[uintptr]struct{})
	seenKeys := make(map[uint32]struct{})
	for bucketIndex := uint32(0); bucketIndex <= mask; bucketIndex++ {
		bucketOffset := uintptr(bucketIndex) * runtimeCharacterPanelBucketStride
		bucket, addressOK := checkedRuntimePanelAddress(table, bucketOffset)
		if !addressOK {
			return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态 bucket 地址溢出")
		}
		last, readErr := readRuntimePanelPointerOffset(memory, bucket, runtimeCharacterPanelBucketLastOffset)
		if readErr != nil {
			return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 bucket 尾节点失败: %w", readErr)
		}
		node, readErr := readRuntimePanelPointerOffset(memory, bucket, runtimeCharacterPanelBucketHeadOffset)
		if readErr != nil {
			return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 bucket 头节点失败: %w", readErr)
		}
		if node == 0 || node == sentinel {
			continue
		}
		finished := false
		for step := 0; step < runtimeCharacterPanelMaxChainNodes; step++ {
			if node == 0 || node == sentinel {
				return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态 bucket 链在尾节点前结束（bucket=0x%X）", bucketIndex)
			}
			if _, duplicate := seenNodes[node]; duplicate {
				return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态 bucket 链出现循环或跨 bucket 重复（bucket=0x%X）", bucketIndex)
			}
			seenNodes[node] = struct{}{}
			key, keyErr := readRuntimePanelU32Offset(memory, node, runtimeCharacterPanelNodeKeyOffset)
			if keyErr != nil {
				return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态节点 key 失败: %w", keyErr)
			}
			if key&mask != bucketIndex {
				return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态节点 key %08X 位于错误 bucket 0x%X", key, bucketIndex)
			}
			if _, duplicate := seenKeys[key]; duplicate {
				return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态 map 出现重复 key %08X", key)
			}
			seenKeys[key] = struct{}{}
			status, statusErr := readRuntimePanelPointerOffset(memory, node, runtimeCharacterPanelNodeStatusOffset)
			if statusErr != nil {
				return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态指针失败: %w", statusErr)
			}
			object := runtimeCharacterPanelObject{RuntimeID: key, MapKey: key, Status: status}
			_, object.InIDVector = vectorSet[key]
			if status != 0 {
				object.Ready, readErr = readRuntimePanelU8Offset(memory, status, runtimeCharacterPanelReadyOffset)
				if readErr != nil {
					return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 ready 标记失败: %w", readErr)
				}
				object.Eligibility, readErr = readRuntimePanelU8Offset(memory, status, runtimeCharacterPanelEligibilityOffset)
				if readErr != nil {
					return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 eligibility 标记失败: %w", readErr)
				}
				object.CandidateCharacterHash, readErr = readRuntimePanelU32Offset(memory, status, runtimeCharacterPanelCharacterHashOffset)
				if readErr != nil {
					return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色候选 hash 失败: %w", readErr)
				}
			}
			result.Objects = append(result.Objects, object)
			if node == last {
				finished = true
				break
			}
			node, readErr = readRuntimePanelPointerOffset(memory, node, runtimeCharacterPanelNodeNextOffset)
			if readErr != nil {
				return runtimeCharacterPanelEnumeration{}, fmt.Errorf("读取角色状态 bucket 下一节点失败: %w", readErr)
			}
		}
		if !finished {
			return runtimeCharacterPanelEnumeration{}, fmt.Errorf("角色状态 bucket 链超过安全上限（bucket=0x%X）", bucketIndex)
		}
	}
	return result, nil
}

func verifyRuntimeCharacterPanelVersion(memory runtimeCharacterPanelMemory, moduleBase uintptr) error {
	for _, guard := range runtimeCharacterPanelVersionGuards {
		address, ok := checkedRuntimePanelAddress(moduleBase, guard.RVA)
		if !ok {
			return fmt.Errorf("2.0.2 版本守卫地址溢出: RVA 0x%X", guard.RVA)
		}
		actual := make([]byte, len(guard.Bytes))
		if err := memory.ReadAt(address, actual); err != nil {
			return fmt.Errorf("读取 2.0.2 版本守卫 RVA 0x%X 失败: %w", guard.RVA, err)
		}
		if !bytes.Equal(actual, guard.Bytes) {
			return fmt.Errorf("2.0.2 版本守卫不匹配（RVA 0x%X），已拒绝按旧布局读取", guard.RVA)
		}
	}
	return nil
}

func readRuntimeCharacterPanelValues(memory runtimeCharacterPanelMemory, status uintptr, characterHash uint32) (RuntimeCharacterPanelStats, error) {
	hp, err := readRuntimePanelI32Offset(memory, status, runtimeCharacterPanelHPOffset)
	if err != nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("读取最终 HP 失败: %w", err)
	}
	attack, err := readRuntimePanelI32Offset(memory, status, runtimeCharacterPanelAttackOffset)
	if err != nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("读取最终攻击力失败: %w", err)
	}
	rawStun, err := readRuntimePanelF32Offset(memory, status, runtimeCharacterPanelStunOffset)
	if err != nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("读取最终昏厥值失败: %w", err)
	}
	crit, err := readRuntimePanelF32Offset(memory, status, runtimeCharacterPanelCritOffset)
	if err != nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("读取最终暴击率失败: %w", err)
	}
	if hp < 1 || hp > runtimeCharacterPanelMaxHPAttack || attack < 1 || attack > runtimeCharacterPanelMaxHPAttack ||
		math.IsNaN(float64(rawStun)) || math.IsInf(float64(rawStun), 0) || rawStun < 0 || rawStun > runtimeCharacterPanelMaxStun ||
		math.IsNaN(float64(crit)) || math.IsInf(float64(crit), 0) || crit < 0 || crit > runtimeCharacterPanelMaxCrit {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("游戏面板数值异常：HP=%d 攻击力=%d 昏厥原始值=%v 暴击率=%v", hp, attack, rawStun, crit)
	}
	return RuntimeCharacterPanelStats{
		CharacterHash:       hashText(characterHash),
		RuntimeID:           hashText(characterHash),
		CandidateObjectHash: hashText(characterHash),
		IdentitySource:      "caller",
		HP:                  hp,
		Attack:              attack,
		StunPower:           runtimeCharacterPanelDisplayStun(rawStun),
		RawStunPower:        rawStun,
		CritRate:            crit,
		HPField:             runtimeCharacterPanelField("i32", runtimeCharacterPanelHPOffset, uint32(hp), 1),
		AttackField:         runtimeCharacterPanelField("i32", runtimeCharacterPanelAttackOffset, uint32(attack), 1),
		StunField:           runtimeCharacterPanelField("f32", runtimeCharacterPanelStunOffset, math.Float32bits(rawStun), runtimeCharacterPanelStunDisplayScale),
		CritField:           runtimeCharacterPanelField("f32", runtimeCharacterPanelCritOffset, math.Float32bits(crit), 1),
		Source:              runtimeCharacterPanelSource,
		Verification:        runtimeCharacterPanelVerification,
		GameVersion:         "2.0.2",
	}, nil
}

func runtimeCharacterPanelField(rawType string, offset uintptr, rawBits uint32, displayScale float32) RuntimeCharacterPanelFieldReading {
	return RuntimeCharacterPanelFieldReading{RawType: rawType, RelativeOffset: uint32(offset), RawBits: fmt.Sprintf("0x%08X", rawBits), DisplayScale: displayScale, StableReads: 1}
}

func runtimeCharacterPanelDisplayStun(raw float32) float32 {
	return float32(math.Round(float64(raw*runtimeCharacterPanelStunDisplayScale)*1000) / 1000)
}

func markRuntimeCharacterPanelStable(stats RuntimeCharacterPanelStats, reads int) RuntimeCharacterPanelStats {
	stats.RuntimeVerified = true
	stats.HPField.StableReads = reads
	stats.AttackField.StableReads = reads
	stats.StunField.StableReads = reads
	stats.CritField.StableReads = reads
	return stats
}

func checkedRuntimePanelAddress(base, offset uintptr) (uintptr, bool) {
	if ^uintptr(0)-base < offset {
		return 0, false
	}
	return base + offset, true
}

func normalizeRuntimePanelReadError(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("指针为空")
}

func readRuntimePanelPointer(memory runtimeCharacterPanelMemory, address uintptr) (uintptr, error) {
	encoded := make([]byte, 8)
	if err := memory.ReadAt(address, encoded); err != nil {
		return 0, err
	}
	value := binary.LittleEndian.Uint64(encoded)
	if uint64(uintptr(value)) != value {
		return 0, fmt.Errorf("64 位指针超出本机地址范围: 0x%X", value)
	}
	return uintptr(value), nil
}

func readRuntimePanelPointerOffset(memory runtimeCharacterPanelMemory, base, offset uintptr) (uintptr, error) {
	address, ok := checkedRuntimePanelAddress(base, offset)
	if !ok {
		return 0, fmt.Errorf("指针地址溢出")
	}
	return readRuntimePanelPointer(memory, address)
}

func readRuntimePanelU8Offset(memory runtimeCharacterPanelMemory, base, offset uintptr) (byte, error) {
	address, ok := checkedRuntimePanelAddress(base, offset)
	if !ok {
		return 0, fmt.Errorf("byte 地址溢出")
	}
	encoded := make([]byte, 1)
	if err := memory.ReadAt(address, encoded); err != nil {
		return 0, err
	}
	return encoded[0], nil
}

func readRuntimePanelU32(memory runtimeCharacterPanelMemory, address uintptr) (uint32, error) {
	encoded := make([]byte, 4)
	if err := memory.ReadAt(address, encoded); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(encoded), nil
}

func readRuntimePanelU32Offset(memory runtimeCharacterPanelMemory, base, offset uintptr) (uint32, error) {
	address, ok := checkedRuntimePanelAddress(base, offset)
	if !ok {
		return 0, fmt.Errorf("uint32 地址溢出")
	}
	return readRuntimePanelU32(memory, address)
}

func readRuntimePanelI32Offset(memory runtimeCharacterPanelMemory, base, offset uintptr) (int32, error) {
	value, err := readRuntimePanelU32Offset(memory, base, offset)
	return int32(value), err
}

func readRuntimePanelF32Offset(memory runtimeCharacterPanelMemory, base, offset uintptr) (float32, error) {
	value, err := readRuntimePanelU32Offset(memory, base, offset)
	return math.Float32frombits(value), err
}
