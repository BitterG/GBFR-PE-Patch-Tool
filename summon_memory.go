package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"unsafe"
)

const (
	summonInventoryPtrRVA = 0x7C23F48
	summonRecordsOffset   = 0xC40
	summonRecordSize      = 0x1C
	summonMaxRecords      = 1000
	summonInvalidTypeHash = 0x887AE0B0
	// CT 的 NBLib.SSS 在 v2.0.2 中对选中记录 +0x00..+0x18 的 7 个
	// DWORD 逐一调用该保存函数；运行中的 CT 已跟踪验证其地址为此 RVA。
	summonSaveFunctionRVA = 0x796E60
)

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
	NameEn    string    `json:"nameEn"`
	MaxLevel  int       `json:"maxLevel"`
	Cost      int       `json:"cost"`
	TypeName  string    `json:"typeName"`
	IsPercent bool      `json:"isPercent"`
	Values    []float64 `json:"values"`
}

type SummonOptions struct {
	Types     []SummonOption `json:"types"`
	Traits    []SummonOption `json:"traits"`
	SubParams []SummonOption `json:"subParams"`
}

type summonTypeFile struct {
	Summons []struct {
		Hash          string `json:"hash"`
		DisplayName   string `json:"displayName"`
		DisplayNameEn string `json:"displayNameEn"`
		Cost          int    `json:"cost"`
		TypeName      string `json:"typeName"`
	} `json:"summons"`
}

type summonSkillFile struct {
	Skills []struct {
		Hash          string `json:"hash"`
		DisplayName   string `json:"displayName"`
		DisplayNameEn string `json:"displayNameEn"`
		MaxLevel      int    `json:"maxLevel"`
	} `json:"skills"`
}

type summonSubParamFile struct {
	SubParams []struct {
		Hash          string    `json:"hash"`
		DisplayName   string    `json:"displayName"`
		DisplayNameEn string    `json:"displayNameEn"`
		MaxLevel      int       `json:"maxLevel"`
		IsPercent     bool      `json:"isPercent"`
		Values        []float64 `json:"values"`
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
	options := SummonOptions{
		Types:     make([]SummonOption, 0, len(types.Summons)),
		Traits:    make([]SummonOption, 0, len(skills.Skills)),
		SubParams: make([]SummonOption, 0, len(subParams.SubParams)),
	}
	for _, item := range types.Summons {
		hash, err := ParseHashHex(item.Hash)
		if err == nil {
			options.Types = append(options.Types, SummonOption{Hash: hash, Name: item.DisplayName, NameEn: item.DisplayNameEn, Cost: item.Cost, TypeName: item.TypeName})
		}
	}
	for _, item := range skills.Skills {
		hash, err := ParseHashHex(item.Hash)
		if err == nil {
			options.Traits = append(options.Traits, SummonOption{Hash: hash, Name: item.DisplayName, NameEn: item.DisplayNameEn, MaxLevel: item.MaxLevel})
		}
	}
	for _, item := range subParams.SubParams {
		hash, err := ParseHashHex(item.Hash)
		if err == nil {
			options.SubParams = append(options.SubParams, SummonOption{
				Hash:      hash,
				Name:      item.DisplayName,
				NameEn:    item.DisplayNameEn,
				MaxLevel:  item.MaxLevel,
				IsPercent: item.IsPercent,
				Values:    item.Values,
			})
		}
	}
	return options, nil
}

func (a *App) summonSubParamMaxLevel(hash uint32) (int, bool) {
	var subParams summonSubParamFile
	if err := json.Unmarshal(summonSubParamsJSON, &subParams); err != nil {
		return 0, false
	}
	for _, item := range subParams.SubParams {
		h, err := ParseHashHex(item.Hash)
		if err == nil && h == hash {
			return item.MaxLevel, true
		}
	}
	return 0, false
}

func (a *App) summonInventoryAddress() (uintptr, error) {
	selected, err := a.summonSelectedAddress()
	if err != nil {
		return 0, err
	}
	// 保持原有调用链不变：将 CT 捕获的当前记录映射为索引 0 的虚拟背包根。
	return selected - summonRecordsOffset, nil
}

func (a *App) readSummonRecords(inventory uintptr) ([]SummonInfo, error) {
	start := inventory + summonRecordsOffset
	buf := make([]byte, summonRecordSize)
	if err := readProcessMemory(a.hProcess, start, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return nil, fmt.Errorf("读取当前选中召唤石失败: %w", err)
	}
	item := SummonInfo{
		Index: 0, Address: uint64(start), TypeHash: readUint32LE(buf[0x00:]), Slot: readUint32LE(buf[0x04:]),
		MainTraitHash: readUint32LE(buf[0x08:]), SubParamHash: readUint32LE(buf[0x0C:]),
		MainTraitLevel: readUint32LE(buf[0x10:]), SubParamLevel: readUint32LE(buf[0x14:]), Rank: readUint32LE(buf[0x18:]),
	}
	if item.TypeHash == 0 || item.TypeHash == summonInvalidTypeHash {
		return nil, fmt.Errorf("当前未选中有效召唤石，请在游戏内重新选中后刷新")
	}
	return []SummonInfo{item}, nil
}

func (a *App) SummonGetAll() ([]SummonInfo, error) {
	inventory, err := a.summonInventoryAddress()
	if err != nil {
		return nil, err
	}
	return a.readSummonRecords(inventory)
}

func (a *App) SummonUpdate(item SummonUpdate) (SummonInfo, error) {
	if item.Index != 0 {
		return SummonInfo{}, fmt.Errorf("当前 CT 工作流只编辑游戏内选中的召唤石")
	}
	if item.TypeHash == 0 {
		return SummonInfo{}, fmt.Errorf("召唤石种类不能为空")
	}
	// 游戏记录存在阶级 0 的合法召唤石。
	if item.Rank > 3 {
		return SummonInfo{}, fmt.Errorf("阶级必须为 0 到 3")
	}
	if item.MainTraitLevel > math.MaxInt32 || item.SubParamLevel > math.MaxInt32 {
		return SummonInfo{}, fmt.Errorf("召唤石等级或副参数等级超出范围")
	}
	// 副参数等级是档位索引(0~maxLevel), 超出会越界读到相邻档位表导致数值溢出, 按该副参数上限钳制。
	if item.SubParamHash != 0 {
		if max, ok := a.summonSubParamMaxLevel(item.SubParamHash); ok && item.SubParamLevel > uint32(max) {
			return SummonInfo{}, fmt.Errorf("副参数等级超出上限，应为 0 到 %d", max)
		}
	}

	inventory, err := a.summonInventoryAddress()
	if err != nil {
		return SummonInfo{}, err
	}
	items, err := a.readSummonRecords(inventory)
	if err != nil {
		return SummonInfo{}, err
	}
	if len(items) != 1 || items[0].Index != item.Index {
		return SummonInfo{}, fmt.Errorf("召唤石索引不存在于当前背包: %d", item.Index)
	}

	address := inventory + summonRecordsOffset
	values := []struct {
		offset uintptr
		value  uint32
	}{
		{0x00, item.TypeHash},
		{0x08, item.MainTraitHash},
		{0x0C, item.SubParamHash},
		{0x10, item.MainTraitLevel},
		{0x14, item.SubParamLevel},
		{0x18, item.Rank},
	}
	for _, field := range values {
		if err := writeUint32Remote(a.hProcess, address+field.offset, field.value); err != nil {
			return SummonInfo{}, fmt.Errorf("写入召唤石字段 +0x%02X 失败: %w", field.offset, err)
		}
	}

	saveFn := a.moduleBase + summonSaveFunctionRVA
	for _, offset := range []uintptr{0x00, 0x04, 0x08, 0x0C, 0x10, 0x14, 0x18} {
		if err := a.callRemoteOneArg(saveFn, address+offset); err != nil {
			return SummonInfo{}, fmt.Errorf("调用召唤石保存函数失败: %w", err)
		}
	}

	items, err = a.SummonGetAll()
	if err != nil {
		return SummonInfo{}, err
	}
	for _, updated := range items {
		if updated.Index == item.Index {
			return updated, nil
		}
	}
	return SummonInfo{}, fmt.Errorf("召唤石写入后未找到索引 %d", item.Index)
}

func readUint32LE(data []byte) uint32 {
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
}
