package backend

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

const (
	summonInventoryPtrRVA = 0x7C23F48
	summonRecordsOffset   = 0xC40
	summonRecordSize      = 0x1C
	summonMaxRecords      = 1000
	summonInvalidTypeHash = 0x887AE0B0
	summonSaveFunctionRVA = 0x79D820

	// Local 2.0.2 summon_curve and summon_preset rows cap one summon's main
	// trait at 15, while 9 is the highest sub-parameter table index. Keep
	// independent safety ceilings so malformed or replaced catalog data cannot
	// expand the writable runtime range.
	summonMainTraitSafetyMaxLevel uint32 = 15
	summonSubParamSafetyMaxLevel  uint32 = 9
)

var errSummonMemoryRollbackUnproven = errLiveMemoryRollbackUnproven

type SummonInfo struct {
	Index          int    `json:"index"`
	Address        uint64 `json:"address"`
	TypeHash       uint32 `json:"typeHash"`
	Slot           uint32 `json:"slot"`
	MainTraitHash  uint32 `json:"mainTraitHash"`
	SubParamHash   uint32 `json:"subParamHash"`
	MainTraitLevel uint32 `json:"mainTraitLevel"`
	SubParamLevel  uint32 `json:"subParamLevel"`
	Rank           uint32 `json:"rank"`
}

type SummonUpdate struct {
	Index          int    `json:"index"`
	TypeHash       uint32 `json:"typeHash"`
	MainTraitHash  uint32 `json:"mainTraitHash"`
	SubParamHash   uint32 `json:"subParamHash"`
	MainTraitLevel uint32 `json:"mainTraitLevel"`
	SubParamLevel  uint32 `json:"subParamLevel"`
	Rank           uint32 `json:"rank"`
}

//go:embed data/summons.json
var summonTypesJSON []byte

//go:embed data/summon_skills.json
var summonSkillsJSON []byte

//go:embed data/summon_sub_params.json
var summonSubParamsJSON []byte

type SummonOption struct {
	Hash      uint32    `json:"hash"`
	Name      string    `json:"name"`
	MaxLevel  int       `json:"maxLevel"`
	Cost      int       `json:"cost"`
	EquipCost int       `json:"equipCost"`
	Tier      string    `json:"tier"`
	Mode      string    `json:"mode"`
	TypeName  string    `json:"typeName"`
	IsPercent bool      `json:"isPercent"`
	Values    []float64 `json:"values"`
}

type SummonOptions struct {
	Types     []SummonOption      `json:"types"`
	Traits    []SummonOption      `json:"traits"`
	SubParams []SummonOption      `json:"subParams"`
	Rules     []SummonNaturalRule `json:"rules"`
}

type summonTypeFile struct {
	Summons []struct {
		Hash        string `json:"hash"`
		DisplayName string `json:"displayName"`
		Cost        int    `json:"cost"`
		TypeName    string `json:"typeName"`
	} `json:"summons"`
}

type summonSkillFile struct {
	Skills []struct {
		Hash        string `json:"hash"`
		DisplayName string `json:"displayName"`
		MaxLevel    int    `json:"maxLevel"`
	} `json:"skills"`
}

type summonSubParamFile struct {
	SubParams []struct {
		Hash        string    `json:"hash"`
		DisplayName string    `json:"displayName"`
		MaxLevel    int       `json:"maxLevel"`
		IsPercent   bool      `json:"isPercent"`
		Values      []float64 `json:"values"`
	} `json:"subParams"`
}

func (a *App) SummonGetOptions() (SummonOptions, error) {
	var types summonTypeFile
	var skills summonSkillFile
	var subParams summonSubParamFile
	if err := json.Unmarshal(summonTypesJSON, &types); err != nil {
		return SummonOptions{}, fmt.Errorf("解析召唤石种类映射失败: %w", err)
	}
	if err := json.Unmarshal(summonSkillsJSON, &skills); err != nil {
		return SummonOptions{}, fmt.Errorf("解析召唤石因子映射失败: %w", err)
	}
	if err := json.Unmarshal(summonSubParamsJSON, &subParams); err != nil {
		return SummonOptions{}, fmt.Errorf("解析召唤石副参数映射失败: %w", err)
	}
	rules, err := loadSummonNaturalRules()
	if err != nil {
		return SummonOptions{}, err
	}
	rulesByHash, err := summonNaturalRuleByHash(rules)
	if err != nil {
		return SummonOptions{}, err
	}
	options := SummonOptions{
		Types:     make([]SummonOption, 0, len(types.Summons)),
		Traits:    make([]SummonOption, 0, len(skills.Skills)),
		SubParams: make([]SummonOption, 0, len(subParams.SubParams)),
		Rules:     rules,
	}
	for _, item := range types.Summons {
		hash, err := ParseHashHex(item.Hash)
		if err == nil {
			rule, ok := rulesByHash[hash]
			if !ok {
				return SummonOptions{}, fmt.Errorf("召唤石 0x%08X 缺少天然规则", hash)
			}
			options.Types = append(options.Types, SummonOption{
				Hash: hash, Name: item.DisplayName, Cost: item.Cost, EquipCost: rule.EquipCost,
				Tier: rule.Tier, Mode: rule.Mode, TypeName: item.TypeName,
			})
		}
	}
	for _, item := range skills.Skills {
		hash, err := ParseHashHex(item.Hash)
		if err == nil {
			options.Traits = append(options.Traits, SummonOption{Hash: hash, Name: item.DisplayName, MaxLevel: item.MaxLevel})
		}
	}
	for _, item := range subParams.SubParams {
		hash, err := ParseHashHex(item.Hash)
		if err == nil {
			options.SubParams = append(options.SubParams, SummonOption{
				Hash:      hash,
				Name:      item.DisplayName,
				MaxLevel:  item.MaxLevel,
				IsPercent: item.IsPercent,
				Values:    item.Values,
			})
		}
	}
	return options, nil
}

func validateSummonMemoryUpdate(catalog *summonStatCatalog, item SummonUpdate) error {
	if err := validateSummonMemoryUpdateNonMainFields(catalog, item); err != nil {
		return err
	}
	return validateSummonTraitChange(catalog, SummonTraitState{
		TypeHash: item.TypeHash, MainTraitHash: item.MainTraitHash, SubParamHash: item.SubParamHash,
		MainTraitLevel: item.MainTraitLevel, SubParamLevel: item.SubParamLevel, Rank: item.Rank,
	}, SummonTraitState{TypeHash: EmptyHash, MainTraitHash: EmptyHash, SubParamHash: EmptyHash, MainTraitLevel: ^uint32(0), SubParamLevel: ^uint32(0)})
}

func validateSummonMemoryUpdateNonMainFields(catalog *summonStatCatalog, item SummonUpdate) error {
	if catalog == nil {
		return fmt.Errorf("召唤石目录为空")
	}
	if item.Index < 0 || item.Index >= summonMaxRecords {
		return fmt.Errorf("无效召唤石索引: %d", item.Index)
	}
	if _, ok := catalog.types[item.TypeHash]; !ok {
		return fmt.Errorf("未知召唤石种类哈希 0x%08X", item.TypeHash)
	}
	sub, ok := catalog.sub[item.SubParamHash]
	if !ok {
		return fmt.Errorf("未知召唤石副参数哈希 0x%08X", item.SubParamHash)
	}
	if sub.MaxLevel < 0 || len(sub.Values) == 0 || sub.MaxLevel >= len(sub.Values) {
		return fmt.Errorf("召唤石副参数 0x%08X 的目录等级表无效", item.SubParamHash)
	}
	limit := uint32(sub.MaxLevel)
	if limit > summonSubParamSafetyMaxLevel {
		limit = summonSubParamSafetyMaxLevel
	}
	if item.SubParamLevel > limit {
		return fmt.Errorf("召唤石副参数等级 %d 超出自然/安全上限 %d", item.SubParamLevel, limit)
	}
	if item.Rank > 3 {
		return fmt.Errorf("召唤石阶级必须为 0 到 3")
	}
	return nil
}

func validateSummonMemoryUpdateAgainstExisting(catalog *summonStatCatalog, item SummonUpdate, existing SummonInfo) error {
	if err := validateSummonMemoryUpdateNonMainFields(catalog, item); err != nil {
		return err
	}
	return validateSummonTraitChange(catalog, SummonTraitState{
		TypeHash: item.TypeHash, MainTraitHash: item.MainTraitHash, SubParamHash: item.SubParamHash,
		MainTraitLevel: item.MainTraitLevel, SubParamLevel: item.SubParamLevel, Rank: item.Rank,
	}, SummonTraitState{
		TypeHash: existing.TypeHash, MainTraitHash: existing.MainTraitHash, SubParamHash: existing.SubParamHash,
		MainTraitLevel: existing.MainTraitLevel, SubParamLevel: existing.SubParamLevel, Rank: existing.Rank,
	})
}

func validateSummonMemoryWriteRequest(catalog *summonStatCatalog, item SummonUpdate) error {
	if item.Index < 0 || item.Index >= summonMaxRecords {
		return fmt.Errorf("无效召唤石索引: %d", item.Index)
	}
	_ = catalog // natural pools and observed caps are advisory for writes.
	return nil
}

func validateSummonMemoryWriteAgainstExisting(catalog *summonStatCatalog, item SummonUpdate, existing SummonInfo) error {
	_, _, _ = catalog, item, existing
	return nil
}

func encodeSummonMemoryRecord(original []byte, item SummonUpdate) ([]byte, error) {
	if len(original) != summonRecordSize {
		return nil, fmt.Errorf("召唤石记录长度 %d，预期 %d", len(original), summonRecordSize)
	}
	encoded := append([]byte(nil), original...)
	binary.LittleEndian.PutUint32(encoded[0x00:0x04], item.TypeHash)
	// +0x04 is the stable inventory Slot ID and is not user-editable.
	binary.LittleEndian.PutUint32(encoded[0x08:0x0C], item.MainTraitHash)
	binary.LittleEndian.PutUint32(encoded[0x0C:0x10], item.SubParamHash)
	binary.LittleEndian.PutUint32(encoded[0x10:0x14], item.MainTraitLevel)
	binary.LittleEndian.PutUint32(encoded[0x14:0x18], item.SubParamLevel)
	binary.LittleEndian.PutUint32(encoded[0x18:0x1C], item.Rank)
	return encoded, nil
}

func summonMemoryCommitOffsets(original, desired []byte) ([]uintptr, error) {
	if len(original) != summonRecordSize || len(desired) != summonRecordSize {
		return nil, fmt.Errorf("召唤石提交记录长度异常: %d/%d", len(original), len(desired))
	}
	// +0x04 is the inventory Slot ID. It identifies the owned instance and is
	// deliberately preserved by encodeSummonMemoryRecord, so it must never be
	// sent through the field-save function as part of an editor update.
	persisted := [...]uintptr{0x00, 0x08, 0x0C, 0x10, 0x14, 0x18}
	offsets := make([]uintptr, 0, len(persisted))
	for _, offset := range persisted {
		start := int(offset)
		if !bytes.Equal(original[start:start+4], desired[start:start+4]) {
			offsets = append(offsets, offset)
		}
	}
	return offsets, nil
}

func decodeSummonMemoryRecord(index int, address uintptr, record []byte) (SummonInfo, error) {
	if len(record) != summonRecordSize {
		return SummonInfo{}, fmt.Errorf("召唤石记录长度 %d，预期 %d", len(record), summonRecordSize)
	}
	return SummonInfo{
		Index:          index,
		Address:        uint64(address),
		TypeHash:       binary.LittleEndian.Uint32(record[0x00:0x04]),
		Slot:           binary.LittleEndian.Uint32(record[0x04:0x08]),
		MainTraitHash:  binary.LittleEndian.Uint32(record[0x08:0x0C]),
		SubParamHash:   binary.LittleEndian.Uint32(record[0x0C:0x10]),
		MainTraitLevel: binary.LittleEndian.Uint32(record[0x10:0x14]),
		SubParamLevel:  binary.LittleEndian.Uint32(record[0x14:0x18]),
		Rank:           binary.LittleEndian.Uint32(record[0x18:0x1C]),
	}, nil
}

func validateSummonMemorySnapshot(expectedInventory, currentInventory uintptr, expectedType uint32, original, current []byte) error {
	if expectedInventory == 0 || currentInventory == 0 || currentInventory != expectedInventory {
		return fmt.Errorf("自动备份期间召唤石背包根指针已变化，请刷新后重试")
	}
	if len(original) != summonRecordSize || len(current) != summonRecordSize {
		return fmt.Errorf("自动备份后召唤石记录长度异常")
	}
	if binary.LittleEndian.Uint32(original[0:4]) != expectedType {
		return fmt.Errorf("备份前目标索引的召唤石种类已不匹配 0x%08X", expectedType)
	}
	if binary.LittleEndian.Uint32(current[0:4]) != expectedType {
		return fmt.Errorf("自动备份期间目标索引的召唤石种类已变化")
	}
	if !bytes.Equal(original, current) {
		return fmt.Errorf("自动备份期间目标召唤石的完整记录已变化，请刷新后重试")
	}
	return nil
}

type summonMemoryRecordWriter func([]byte) error
type summonMemoryRecordCommitter func() error
type summonMemoryRecordReader func() ([]byte, error)

func verifySummonMemoryRecord(want []byte, reader summonMemoryRecordReader) error {
	got, err := reader()
	if err != nil {
		return fmt.Errorf("召唤石记录回读失败: %w", err)
	}
	if len(got) != summonRecordSize {
		return fmt.Errorf("召唤石记录回读长度 %d，预期 %d", len(got), summonRecordSize)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("召唤石完整记录回读不一致")
	}
	return nil
}

func rollbackSummonMemoryRecord(original []byte, persist bool, writer summonMemoryRecordWriter, committer summonMemoryRecordCommitter, reader summonMemoryRecordReader) error {
	if err := writer(original); err != nil {
		return fmt.Errorf("恢复原召唤石记录内存失败: %w", err)
	}
	if persist {
		if err := committer(); err != nil {
			return fmt.Errorf("重新保存原召唤石记录失败: %w", err)
		}
	}
	if err := verifySummonMemoryRecord(original, reader); err != nil {
		return fmt.Errorf("恢复原召唤石记录后验证失败: %w", err)
	}
	return nil
}

func summonMemoryTransactionError(cause, rollback error) error {
	if rollback == nil {
		return cause
	}
	return errors.Join(cause, errSummonMemoryRollbackUnproven, fmt.Errorf("召唤石回滚失败: %w", rollback))
}

func writeSummonMemoryRecordAtomic(original, desired []byte, writer summonMemoryRecordWriter, committer summonMemoryRecordCommitter, reader summonMemoryRecordReader) error {
	if len(original) != summonRecordSize || len(desired) != summonRecordSize || writer == nil || committer == nil || reader == nil {
		return fmt.Errorf("召唤石事务写入参数无效")
	}
	if err := writer(desired); err != nil {
		return summonMemoryTransactionError(err, rollbackSummonMemoryRecord(original, false, writer, committer, reader))
	}
	if err := verifySummonMemoryRecord(desired, reader); err != nil {
		return summonMemoryTransactionError(err, rollbackSummonMemoryRecord(original, false, writer, committer, reader))
	}
	if err := committer(); err != nil {
		if isRemoteCallIndeterminate(err) {
			// The remote thread may still be reading the desired record. A rollback
			// here could race that thread and persist a mixed state. callRemoteOneArg
			// has already poisoned this process instance, so fail closed until restart.
			return err
		}
		return summonMemoryTransactionError(err, rollbackSummonMemoryRecord(original, true, writer, committer, reader))
	}
	if err := verifySummonMemoryRecord(desired, reader); err != nil {
		return summonMemoryTransactionError(err, rollbackSummonMemoryRecord(original, true, writer, committer, reader))
	}
	return nil
}

func (a *App) summonInventoryAddressLocked() (uintptr, error) {
	var inventory uintptr
	root := a.moduleBase + summonInventoryPtrRVA
	if err := readProcessMemory(a.hProcess, root, unsafe.Pointer(&inventory), unsafe.Sizeof(inventory)); err != nil {
		return 0, fmt.Errorf("读取召唤石背包指针失败: %w", err)
	}
	if inventory == 0 {
		return 0, fmt.Errorf("召唤石背包未加载，请进入游戏存档并打开召唤石背包")
	}
	return inventory, nil
}

func (a *App) readSummonRecords(inventory uintptr) ([]SummonInfo, error) {
	buf := make([]byte, summonMaxRecords*summonRecordSize)
	start := inventory + summonRecordsOffset
	if err := readProcessMemory(a.hProcess, start, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return nil, fmt.Errorf("读取召唤石背包失败: %w", err)
	}

	result := make([]SummonInfo, 0, summonMaxRecords)
	for i := 0; i < summonMaxRecords; i++ {
		base := i * summonRecordSize
		item, err := decodeSummonMemoryRecord(i, start+uintptr(base), buf[base:base+summonRecordSize])
		if err != nil {
			return nil, err
		}
		if item.TypeHash != 0 && item.TypeHash != summonInvalidTypeHash {
			result = append(result, item)
		}
	}
	return result, nil
}

func (a *App) readSummonRecord(address uintptr) ([]byte, error) {
	record := make([]byte, summonRecordSize)
	if err := readProcessMemory(a.hProcess, address, unsafe.Pointer(&record[0]), uintptr(len(record))); err != nil {
		return nil, err
	}
	return record, nil
}

func (a *App) SummonGetAll() ([]SummonInfo, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return nil, err
	}
	defer a.procMu.Unlock()
	inventory, err := a.summonInventoryAddressLocked()
	if err != nil {
		return nil, err
	}
	return a.readSummonRecords(inventory)
}

func (a *App) SummonUpdate(item SummonUpdate) (SummonInfo, error) {
	return a.summonUpdate("", false, item)
}

func (a *App) SummonUpdateOwned(token string, item SummonUpdate) (SummonInfo, error) {
	return a.summonUpdate(token, true, item)
}

func (a *App) summonUpdate(token string, owned bool, item SummonUpdate) (SummonInfo, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if owned {
		if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
			return SummonInfo{}, err
		}
		defer a.procMu.Unlock()
	}
	catalog, err := loadSummonStatCatalog()
	if err != nil {
		return SummonInfo{}, fmt.Errorf("加载召唤石写入目录失败: %w", err)
	}
	if err := validateSummonMemoryWriteRequest(catalog, item); err != nil {
		return SummonInfo{}, fmt.Errorf("召唤石写入参数无效: %w", err)
	}

	if !owned {
		if err := a.acquireGameProcessLease(); err != nil {
			return SummonInfo{}, err
		}
		defer a.procMu.Unlock()
	}
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return SummonInfo{}, err
	}
	saveFn := a.moduleBase + summonSaveFunctionRVA
	if err := a.validateRemoteFunctionStart(saveFn, "游戏内召唤石保存函数"); err != nil {
		return SummonInfo{}, err
	}
	inventory, err := a.summonInventoryAddressLocked()
	if err != nil {
		return SummonInfo{}, err
	}
	address := inventory + summonRecordsOffset + uintptr(item.Index*summonRecordSize)
	original, err := a.readSummonRecord(address)
	if err != nil {
		return SummonInfo{}, fmt.Errorf("读取目标召唤石原记录失败: %w", err)
	}
	existing, err := decodeSummonMemoryRecord(item.Index, address, original)
	if err != nil {
		return SummonInfo{}, err
	}
	if existing.TypeHash == 0 || existing.TypeHash == summonInvalidTypeHash {
		return SummonInfo{}, fmt.Errorf("召唤石索引不存在于当前背包: %d", item.Index)
	}
	if err := validateSummonMemoryWriteAgainstExisting(catalog, item, existing); err != nil {
		return SummonInfo{}, fmt.Errorf("召唤石写入参数无效: %w", err)
	}
	desired, err := encodeSummonMemoryRecord(original, item)
	if err != nil {
		return SummonInfo{}, err
	}
	commitOffsets, err := summonMemoryCommitOffsets(original, desired)
	if err != nil {
		return SummonInfo{}, err
	}
	if err := snapshotBeforeLiveSaveChange("召唤石写入前自动备份"); err != nil {
		return SummonInfo{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}

	// The filesystem backup can be slow enough for the game to rebuild its
	// inventory. Re-read the root, the target type and every byte in the 0x1C
	// record before using the captured address.
	confirmedInventory, err := a.summonInventoryAddressLocked()
	if err != nil {
		return SummonInfo{}, fmt.Errorf("自动备份后复核召唤石背包失败: %w", err)
	}
	confirmed, err := a.readSummonRecord(address)
	if err != nil {
		return SummonInfo{}, fmt.Errorf("自动备份后复核召唤石记录失败: %w", err)
	}
	if err := validateSummonMemorySnapshot(inventory, confirmedInventory, existing.TypeHash, original, confirmed); err != nil {
		return SummonInfo{}, err
	}

	writer := func(record []byte) error {
		if len(record) != summonRecordSize {
			return fmt.Errorf("召唤石记录长度异常: %d", len(record))
		}
		return writeProcessMemory(a.hProcess, address, unsafe.Pointer(&record[0]), uintptr(len(record)))
	}
	reader := func() ([]byte, error) {
		return a.readSummonRecord(address)
	}
	committer := func() error {
		for _, offset := range commitOffsets {
			if err := a.callRemoteOneArg(saveFn, address+offset); err != nil {
				return fmt.Errorf("保存召唤石字段 +0x%02X 失败: %w", offset, err)
			}
		}
		return nil
	}
	if err := writeSummonMemoryRecordAtomic(original, desired, writer, committer, reader); err != nil {
		if isRemoteCallIndeterminate(err) || errors.Is(err, errSummonMemoryRollbackUnproven) {
			// Either a save thread may still be running or the original record could
			// not be restored and proven. Block every further live item write for
			// this process instance; only a full game-process restart clears it.
			a.poisonCurrentLiveMemoryWrites()
		}
		return SummonInfo{}, fmt.Errorf("召唤石事务写入失败: %w", err)
	}

	updated, err := decodeSummonMemoryRecord(item.Index, address, desired)
	if err != nil {
		return SummonInfo{}, err
	}
	return updated, nil
}
