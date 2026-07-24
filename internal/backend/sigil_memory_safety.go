package backend

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

const sigilMemoryLoadoutMaxEntries = 12
const sigilMemoryRecordSize = 0x1C

var sigilMemoryWriteMu sync.Mutex

type SigilMemoryBatchValidation struct {
	Valid bool `json:"valid"`
	Count int  `json:"count"`
}

func validateSigilMemorySelection(expected, statusSelected, caveSelected uintptr) (uintptr, error) {
	if expected == 0 {
		return 0, fmt.Errorf("缺少写入前捕获的因子记录地址")
	}
	if statusSelected != expected {
		return 0, fmt.Errorf("因子记录已从 0x%X 切换到 0x%X，请重新确认", expected, statusSelected)
	}
	if caveSelected == 0 || caveSelected != expected {
		return 0, fmt.Errorf("写入前因子记录地址已变化，请重新选择目标记录")
	}
	return expected, nil
}

func validateSigilMemorySnapshot(expected, statusSelected, caveSelected uintptr, original, current []byte) error {
	if _, err := validateSigilMemorySelection(expected, statusSelected, caveSelected); err != nil {
		return err
	}
	if len(original) != sigilMemoryRecordSize || len(current) != sigilMemoryRecordSize {
		return fmt.Errorf("备份后因子记录长度异常")
	}
	if !bytes.Equal(original, current) {
		return fmt.Errorf("自动备份期间目标因子记录已变化，请重新选择后确认")
	}
	return nil
}

func sigilMemoryLevelAllowed(level uint32, allowed []int) bool {
	if level == 0 {
		return false
	}
	max := 0
	for _, candidate := range allowed {
		if candidate > max {
			max = candidate
		}
	}
	return max > 0 && level <= uint32(max)
}

func validateSigilMemoryTraitLevel(catalog *Catalog, hash, level uint32, label string) error {
	if hash == 0 || hash == EmptyHash {
		return fmt.Errorf("%s哈希为空", label)
	}
	if trait := catalog.LookupTraitByHash(hash); trait != nil {
		levels, err := requireTraitLevels(trait, "运行时因子")
		if err != nil {
			return err
		}
		if !sigilMemoryLevelAllowed(level, levels) {
			return fmt.Errorf("%s等级 %d 不在已验证范围内", label, level)
		}
		return nil
	}
	return fmt.Errorf("未知%s哈希 0x%08X；四个编辑入口只接受统一目录", label, hash)
}

func validateSigilMemoryUpdate(catalog *Catalog, update SigilMemoryUpdate) error {
	if catalog == nil {
		return fmt.Errorf("因子目录为空")
	}
	if update.SigilHash == 0 || update.SigilHash == EmptyHash {
		return fmt.Errorf("因子哈希为空")
	}

	sigil := catalog.LookupSigilByHash(update.SigilHash)
	if sigil == nil {
		return fmt.Errorf("未知因子哈希 0x%08X；四个编辑入口只接受统一目录", update.SigilHash)
	}

	if update.SigilLevel == 0 || update.SigilLevel > sigilWritableLevelMax {
		return fmt.Errorf("因子等级 %d 超过修改上限 %d", update.SigilLevel, sigilWritableLevelMax)
	}

	primary, err := catalog.RequireTrait(sigil.PrimaryTraitID)
	if err != nil {
		return err
	}
	primaryHash, err := ParseHashHex(primary.Hash)
	if err != nil {
		return err
	}
	if update.PrimaryTraitHash != primaryHash {
		return fmt.Errorf("主词条 0x%08X 与因子 %s 的固定主词条不匹配", update.PrimaryTraitHash, displaySigilName(sigil))
	}
	primaryLevels, err := catalog.RequirePrimaryTraitLevels(sigil)
	if err != nil {
		return err
	}
	if !sigilMemoryLevelAllowed(update.PrimaryTraitLevel, primaryLevels) {
		return fmt.Errorf("主词条等级 %d 超过技能效果曲线上限", update.PrimaryTraitLevel)
	}
	return validateSigilMemorySecondary(catalog, sigil, update)
}

func validateSigilMemoryWriteRequest(catalog *Catalog, update SigilMemoryUpdate) error {
	if update.SigilHash == 0 || update.PrimaryTraitHash == 0 {
		return fmt.Errorf("因子和主词条 Hash 必须是可编码的非零值")
	}
	if catalog == nil || catalog.LookupSigilByHash(update.SigilHash) == nil {
		return fmt.Errorf("未知因子哈希 0x%08X；运行时写入只接受统一目录", update.SigilHash)
	}
	if update.SigilLevel == 0 || update.SigilLevel > sigilWritableLevelMax {
		return fmt.Errorf("因子等级 %d 超过修改上限 %d", update.SigilLevel, sigilWritableLevelMax)
	}
	if err := validateSigilMemoryTraitLevel(catalog, update.PrimaryTraitHash, update.PrimaryTraitLevel, "主词条"); err != nil {
		return err
	}
	if isEmptySigilMemoryTrait(update.SecondaryTraitHash) {
		if update.SecondaryTraitLevel != 0 {
			return fmt.Errorf("副词条为空时等级必须为 0")
		}
		return nil
	}
	if err := validateSigilMemoryTraitLevel(catalog, update.SecondaryTraitHash, update.SecondaryTraitLevel, "副词条"); err != nil {
		return err
	}
	return nil
}

func isEmptySigilMemoryTrait(hash uint32) bool {
	return hash == 0 || hash == EmptyHash
}

func validateSigilMemorySecondary(catalog *Catalog, sigil *SigilDef, update SigilMemoryUpdate) error {
	empty := isEmptySigilMemoryTrait(update.SecondaryTraitHash)
	if empty {
		if requiresCharacterSigilSecondary(sigil) {
			return fmt.Errorf("角色因子 %s 必须使用本地 2.0.2 gem/lot 白名单中的副词条，不能留空", displaySigilName(sigil))
		}
		if update.SecondaryTraitLevel != 0 {
			return fmt.Errorf("副词条为空时等级必须为 0")
		}
		return nil
	}
	if update.SecondaryTraitLevel == 0 {
		return fmt.Errorf("副词条非空时等级不能为 0")
	}
	if sigil == nil {
		return validateSigilMemoryTraitLevel(catalog, update.SecondaryTraitHash, update.SecondaryTraitLevel, "副词条")
	}
	if !supportsGeneratedPlusSigil(sigil) {
		return fmt.Errorf("因子 %s 没有副词条槽", displaySigilName(sigil))
	}
	secondary := catalog.LookupTraitByHash(update.SecondaryTraitHash)
	if secondary == nil {
		return fmt.Errorf("未知副词条哈希 0x%08X", update.SecondaryTraitHash)
	}
	if secondary.InternalID == sigil.PrimaryTraitID {
		return fmt.Errorf("主词条与副词条重复: %s", cnTrait(secondary.DisplayName))
	}
	allowed, err := catalog.GetAllowedSecondaryTraits(sigil)
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range allowed {
		if candidate.InternalID == secondary.InternalID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("副词条 %s 不能用于因子 %s", cnTrait(secondary.DisplayName), displaySigilName(sigil))
	}
	levels, err := catalog.RequireSecondaryTraitLevels(sigil, secondary)
	if err != nil {
		return err
	}
	if !sigilMemoryLevelAllowed(update.SecondaryTraitLevel, levels) {
		return fmt.Errorf("副词条等级 %d 不在已验证范围内", update.SecondaryTraitLevel)
	}
	return nil
}

func validateSigilMemoryUpdates(catalog *Catalog, updates []SigilMemoryUpdate) error {
	if len(updates) == 0 || len(updates) > sigilMemoryLoadoutMaxEntries {
		return fmt.Errorf("因子配装必须包含 1 到 %d 条记录", sigilMemoryLoadoutMaxEntries)
	}
	for index, update := range updates {
		if err := validateSigilMemoryUpdate(catalog, update); err != nil {
			return fmt.Errorf("第 %d 条因子无效: %w", index+1, err)
		}
	}
	return nil
}

func preflightSigilMemoryLoadout(catalog *Catalog, updates []SigilMemoryUpdate) (SigilMemoryBatchValidation, error) {
	result := SigilMemoryBatchValidation{Count: len(updates)}
	if err := validateSigilMemoryUpdates(catalog, updates); err != nil {
		return result, err
	}
	result.Valid = true
	return result, nil
}

// SigilMemoryValidateLoadout is the non-mutating gate for the interactive
// 1..12-item restore workflow. The caller must invoke it once before the first
// selection is written; SigilMemoryUpdate repeats per-item validation so a
// changed request cannot bypass this batch check.
func (a *App) SigilMemoryValidateLoadout(updates []SigilMemoryUpdate) (SigilMemoryBatchValidation, error) {
	catalog, err := LoadCatalog()
	if err != nil {
		return SigilMemoryBatchValidation{Count: len(updates)}, err
	}
	result, err := preflightSigilMemoryLoadout(catalog, updates)
	if err != nil {
		return result, err
	}
	if err := a.ensureGameProcess(); err != nil {
		return SigilMemoryBatchValidation{Count: len(updates)}, fmt.Errorf("游戏进程状态检查失败: %w", err)
	}
	return result, nil
}

func encodeSigilMemoryRecord(original []byte, update SigilMemoryUpdate) ([]byte, error) {
	if len(original) != sigilMemoryRecordSize {
		return nil, fmt.Errorf("因子记录长度 %d，预期 %d", len(original), sigilMemoryRecordSize)
	}
	secondaryHash := update.SecondaryTraitHash
	secondaryLevel := update.SecondaryTraitLevel
	if secondaryHash == 0 || secondaryHash == EmptyHash {
		secondaryHash = EmptyHash
		secondaryLevel = 0
	}
	encoded := append([]byte(nil), original...)
	binary.LittleEndian.PutUint32(encoded[0x00:0x04], update.PrimaryTraitHash)
	binary.LittleEndian.PutUint32(encoded[0x04:0x08], update.PrimaryTraitLevel)
	binary.LittleEndian.PutUint32(encoded[0x08:0x0C], secondaryHash)
	binary.LittleEndian.PutUint32(encoded[0x0C:0x10], secondaryLevel)
	binary.LittleEndian.PutUint32(encoded[0x10:0x14], update.SigilHash)
	// +0x14 belongs to the game's record and is deliberately preserved.
	binary.LittleEndian.PutUint32(encoded[0x18:0x1C], update.SigilLevel)
	return encoded, nil
}

type sigilMemoryRecordWriter func([]byte) error
type sigilMemoryRecordCommitter func() error
type sigilMemoryRecordReader func() ([]byte, error)

func verifySigilMemoryRecord(want []byte, reader sigilMemoryRecordReader) error {
	got, err := reader()
	if err != nil {
		return fmt.Errorf("因子记录回读失败: %w", err)
	}
	if len(got) != sigilMemoryRecordSize {
		return fmt.Errorf("因子记录回读长度 %d，预期 %d", len(got), sigilMemoryRecordSize)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("因子记录回读不一致")
	}
	return nil
}

func rollbackSigilMemoryRecord(original []byte, persist bool, writer sigilMemoryRecordWriter, committer sigilMemoryRecordCommitter, reader sigilMemoryRecordReader) error {
	if err := writer(original); err != nil {
		return fmt.Errorf("恢复原记录内存失败: %w", err)
	}
	if persist {
		if err := committer(); err != nil {
			return fmt.Errorf("重新保存原记录失败: %w", err)
		}
	}
	if err := verifySigilMemoryRecord(original, reader); err != nil {
		return fmt.Errorf("恢复原记录后验证失败: %w", err)
	}
	return nil
}

func sigilMemoryTransactionError(cause, rollback error) error {
	if rollback == nil {
		return cause
	}
	return errors.Join(cause, errLiveMemoryRollbackUnproven, fmt.Errorf("因子回滚失败: %w", rollback))
}

func writeSigilMemoryRecordAtomic(original, desired []byte, writer sigilMemoryRecordWriter, committer sigilMemoryRecordCommitter, reader sigilMemoryRecordReader) error {
	if len(original) != sigilMemoryRecordSize || len(desired) != sigilMemoryRecordSize || writer == nil || committer == nil || reader == nil {
		return fmt.Errorf("因子原子写入参数无效")
	}
	if err := writer(desired); err != nil {
		return sigilMemoryTransactionError(err, rollbackSigilMemoryRecord(original, false, writer, committer, reader))
	}
	if err := verifySigilMemoryRecord(desired, reader); err != nil {
		return sigilMemoryTransactionError(err, rollbackSigilMemoryRecord(original, false, writer, committer, reader))
	}
	if err := committer(); err != nil {
		if isRemoteCallIndeterminate(err) {
			// A timed-out remote thread may still be saving this record. Do not
			// race it with a rollback; require the user to reconnect and re-read.
			return err
		}
		return sigilMemoryTransactionError(err, rollbackSigilMemoryRecord(original, true, writer, committer, reader))
	}
	if err := verifySigilMemoryRecord(desired, reader); err != nil {
		return sigilMemoryTransactionError(err, rollbackSigilMemoryRecord(original, true, writer, committer, reader))
	}
	return nil
}
