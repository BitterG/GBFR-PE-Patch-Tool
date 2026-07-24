package backend

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

const wrightstoneMemoryRecordSize = 0x18

var wrightstoneMemoryWriteMu sync.Mutex

type WrightstoneMemoryUpdate struct {
	ExpectedSelectedAddr uint64 `json:"expectedSelectedAddr"`
	FirstHash            uint32 `json:"firstHash"`
	FirstLevel           uint32 `json:"firstLevel"`
	SecondHash           uint32 `json:"secondHash"`
	SecondLevel          uint32 `json:"secondLevel"`
	ThirdHash            uint32 `json:"thirdHash"`
	ThirdLevel           uint32 `json:"thirdLevel"`
}

func isEmptyWrightstoneMemoryTrait(hash uint32) bool {
	return hash == 0 || hash == EmptyHash
}

func validateWrightstoneMemorySlot(catalog *WrightstoneCatalog, hash, level uint32, slot, naturalCap int, required bool) error {
	if isEmptyWrightstoneMemoryTrait(hash) {
		if required {
			return fmt.Errorf("祝福词条 %d 不能为空", slot)
		}
		if level != 0 {
			return fmt.Errorf("祝福词条 %d 为空时等级必须为 0", slot)
		}
		return nil
	}
	if level == 0 {
		return fmt.Errorf("祝福词条 %d 非空时等级不能为 0", slot)
	}
	trait := catalog.LookupTraitByHash(hash)
	if trait == nil {
		return fmt.Errorf("未知祝福词条 %d 哈希 0x%08X", slot, hash)
	}
	levels, err := requireWrightstoneTraitLevels(trait)
	if err != nil {
		return err
	}
	maxLevel := effectCurveMax(levels, naturalCap)
	if level > uint32(maxLevel) {
		return fmt.Errorf("祝福词条 %d 等级 %d 超过技能效果曲线上限 %d", slot, level, maxLevel)
	}
	return nil
}

func validateWrightstoneMemoryUpdate(catalog *WrightstoneCatalog, update WrightstoneMemoryUpdate) error {
	if catalog == nil {
		return fmt.Errorf("祝福词条目录为空")
	}
	slots := []struct {
		hash     uint32
		level    uint32
		cap      int
		required bool
	}{
		{update.FirstHash, update.FirstLevel, 20, true},
		{update.SecondHash, update.SecondLevel, 15, false},
		{update.ThirdHash, update.ThirdLevel, 10, false},
	}
	seen := make(map[uint32]int, len(slots))
	for index, slot := range slots {
		position := index + 1
		if err := validateWrightstoneMemorySlot(catalog, slot.hash, slot.level, position, slot.cap, slot.required); err != nil {
			return err
		}
		if isEmptyWrightstoneMemoryTrait(slot.hash) {
			continue
		}
		if previous, ok := seen[slot.hash]; ok {
			return fmt.Errorf("祝福词条 %d 与词条 %d 重复", position, previous)
		}
		seen[slot.hash] = position
	}
	return nil
}

func validateWrightstoneMemoryWriteRequest(catalog *WrightstoneCatalog, update WrightstoneMemoryUpdate) error {
	if isEmptyWrightstoneMemoryTrait(update.FirstHash) {
		return fmt.Errorf("祝福第一槽 Hash 必须是可编码的非空值")
	}
	slots := []struct {
		hash       uint32
		level      uint32
		naturalCap int
		required   bool
	}{
		{update.FirstHash, update.FirstLevel, 20, true},
		{update.SecondHash, update.SecondLevel, 15, false},
		{update.ThirdHash, update.ThirdLevel, 10, false},
	}
	for index, slot := range slots {
		if err := validateWrightstoneMemorySlot(catalog, slot.hash, slot.level, index+1, slot.naturalCap, slot.required); err != nil {
			return err
		}
	}
	return nil
}

func validateWrightstoneMemorySelection(expected, statusSelected, caveSelected uintptr) (uintptr, error) {
	if expected == 0 {
		return 0, fmt.Errorf("缺少写入前捕获的祝福记录地址")
	}
	if statusSelected != expected {
		return 0, fmt.Errorf("祝福记录已从 0x%X 切换到 0x%X，请重新确认", expected, statusSelected)
	}
	if caveSelected == 0 || caveSelected != expected {
		return 0, fmt.Errorf("写入前祝福记录地址已变化，请重新选择目标记录")
	}
	return expected, nil
}

func validateWrightstoneMemorySnapshot(expected, statusSelected, caveSelected uintptr, original, current []byte) error {
	if _, err := validateWrightstoneMemorySelection(expected, statusSelected, caveSelected); err != nil {
		return err
	}
	if len(original) != wrightstoneMemoryRecordSize || len(current) != wrightstoneMemoryRecordSize {
		return fmt.Errorf("备份后祝福记录长度异常")
	}
	if !bytes.Equal(original, current) {
		return fmt.Errorf("自动备份期间目标祝福记录已变化，请重新选择后确认")
	}
	return nil
}

func encodeWrightstoneMemoryRecord(original []byte, update WrightstoneMemoryUpdate) ([]byte, error) {
	if len(original) != wrightstoneMemoryRecordSize {
		return nil, fmt.Errorf("祝福记录长度 %d，预期 %d", len(original), wrightstoneMemoryRecordSize)
	}
	normalize := func(hash uint32) uint32 {
		if isEmptyWrightstoneMemoryTrait(hash) {
			return EmptyHash
		}
		return hash
	}
	encoded := append([]byte(nil), original...)
	binary.LittleEndian.PutUint32(encoded[0x00:0x04], normalize(update.FirstHash))
	binary.LittleEndian.PutUint32(encoded[0x04:0x08], update.FirstLevel)
	binary.LittleEndian.PutUint32(encoded[0x08:0x0C], normalize(update.SecondHash))
	binary.LittleEndian.PutUint32(encoded[0x0C:0x10], update.SecondLevel)
	binary.LittleEndian.PutUint32(encoded[0x10:0x14], normalize(update.ThirdHash))
	binary.LittleEndian.PutUint32(encoded[0x14:0x18], update.ThirdLevel)
	return encoded, nil
}

type wrightstoneMemoryRecordWriter func([]byte) error
type wrightstoneMemoryRecordCommitter func() error
type wrightstoneMemoryRecordReader func() ([]byte, error)

func verifyWrightstoneMemoryRecord(want []byte, reader wrightstoneMemoryRecordReader) error {
	got, err := reader()
	if err != nil {
		return fmt.Errorf("祝福记录回读失败: %w", err)
	}
	if len(got) != wrightstoneMemoryRecordSize {
		return fmt.Errorf("祝福记录回读长度 %d，预期 %d", len(got), wrightstoneMemoryRecordSize)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("祝福记录回读不一致")
	}
	return nil
}

func rollbackWrightstoneMemoryRecord(original []byte, persist bool, writer wrightstoneMemoryRecordWriter, committer wrightstoneMemoryRecordCommitter, reader wrightstoneMemoryRecordReader) error {
	if err := writer(original); err != nil {
		return fmt.Errorf("恢复原祝福记录失败: %w", err)
	}
	if persist {
		if err := committer(); err != nil {
			return fmt.Errorf("重新保存原祝福记录失败: %w", err)
		}
	}
	if err := verifyWrightstoneMemoryRecord(original, reader); err != nil {
		return fmt.Errorf("恢复原祝福记录后验证失败: %w", err)
	}
	return nil
}

func wrightstoneMemoryTransactionError(cause, rollback error) error {
	if rollback == nil {
		return cause
	}
	return errors.Join(cause, errLiveMemoryRollbackUnproven, fmt.Errorf("祝福回滚失败: %w", rollback))
}

func writeWrightstoneMemoryRecordAtomic(original, desired []byte, writer wrightstoneMemoryRecordWriter, committer wrightstoneMemoryRecordCommitter, reader wrightstoneMemoryRecordReader) error {
	if len(original) != wrightstoneMemoryRecordSize || len(desired) != wrightstoneMemoryRecordSize || writer == nil || committer == nil || reader == nil {
		return fmt.Errorf("祝福原子写入参数无效")
	}
	if err := writer(desired); err != nil {
		return wrightstoneMemoryTransactionError(err, rollbackWrightstoneMemoryRecord(original, false, writer, committer, reader))
	}
	if err := verifyWrightstoneMemoryRecord(desired, reader); err != nil {
		return wrightstoneMemoryTransactionError(err, rollbackWrightstoneMemoryRecord(original, false, writer, committer, reader))
	}
	if err := committer(); err != nil {
		if isRemoteCallIndeterminate(err) {
			// The remote thread may still be using the desired record. Rolling
			// back concurrently would create a mixed persisted state.
			return err
		}
		return wrightstoneMemoryTransactionError(err, rollbackWrightstoneMemoryRecord(original, true, writer, committer, reader))
	}
	if err := verifyWrightstoneMemoryRecord(desired, reader); err != nil {
		return wrightstoneMemoryTransactionError(err, rollbackWrightstoneMemoryRecord(original, true, writer, committer, reader))
	}
	return nil
}
