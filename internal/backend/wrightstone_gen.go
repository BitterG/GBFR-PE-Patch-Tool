package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type WrightstoneInfo struct {
	InternalID       string `json:"internalId"`
	Hash             string `json:"hash"`
	DisplayName      string `json:"displayName"`
	DefaultTraitID   string `json:"defaultTraitId"`
	DefaultTraitName string `json:"defaultTraitName"`
}

type WrightstoneTraitInfo struct {
	InternalID    string `json:"internalId"`
	Hash          string `json:"hash"`
	DisplayName   string `json:"displayName"`
	MaxLevel      int    `json:"maxLevel"`
	AllowedLevels []int  `json:"allowedLevels"`
}

type WrightstoneSaveInfo struct {
	Path                 string `json:"path"`
	OccupiedWrightstones int    `json:"occupiedWrightstones"`
	MaxSlotID            int    `json:"maxSlotId"`
}

type WrightstoneQueueItem struct {
	WrightstoneID   string `json:"wrightstoneId"`
	WrightstoneName string `json:"wrightstoneName"`
	FirstTraitID    string `json:"firstTraitId"`
	FirstTraitName  string `json:"firstTraitName"`
	FirstLevel      int    `json:"firstLevel"`
	SecondTraitID   string `json:"secondTraitId"`
	SecondTraitName string `json:"secondTraitName"`
	SecondLevel     int    `json:"secondLevel"`
	ThirdTraitID    string `json:"thirdTraitId"`
	ThirdTraitName  string `json:"thirdTraitName"`
	ThirdLevel      int    `json:"thirdLevel"`
	Quantity        int    `json:"quantity"`
	LegalityStatus  string `json:"legalityStatus"`
	LegalityMessage string `json:"legalityMessage"`
}

type WrightstoneApplyResult struct {
	CreatedCount  int    `json:"createdCount"`
	VerifiedCount int    `json:"verifiedCount"`
	OutputPath    string `json:"outputPath"`
}

type WrightstoneGen struct {
	mu                      sync.Mutex
	ctx                     context.Context
	catalog                 *WrightstoneCatalog
	save                    *SaveData
	savePath                string
	queue                   []WrightstoneQueueItem
	loadSaveForVerification func(string) (*SaveData, error)
	retryAfterFailedCommit  *failedGeneratorCommit
}

func NewWrightstoneGen() *WrightstoneGen {
	return &WrightstoneGen{loadSaveForVerification: LoadSave}
}

type wrightstoneWriteExpectation struct {
	ItemUnitID      int
	SlotID          int
	WrightstoneHash uint32
	FirstHash       uint32
	FirstLevel      int
	SecondHash      uint32
	SecondLevel     int
	ThirdHash       uint32
	ThirdLevel      int
}

type wrightstoneRecordVerifier func(*SaveData, wrightstoneWriteExpectation) error

func (wg *WrightstoneGen) verifyWrittenWrightstones(outputPath string, created int,
	expected []wrightstoneWriteExpectation, verifyRecord wrightstoneRecordVerifier) (int, error) {
	loader := wg.loadSaveForVerification
	if loader == nil {
		loader = LoadSave
	}
	verifySave, err := loader(outputPath)
	if err != nil {
		return 0, fmt.Errorf("祝福已写入，但重新读取失败: %w", err)
	}
	verified := 0
	for i, record := range expected {
		if err := verifyRecord(verifySave, record); err != nil {
			return verified, fmt.Errorf("祝福已写入，但第 %d 个祝福回读验证失败: %w", i+1, err)
		}
		verified++
	}
	if verified != created {
		return verified, fmt.Errorf("祝福已写入，但回读验证数量不符: 已创建 %d，已验证 %d", created, verified)
	}
	return verified, nil
}

func verifyWrightstoneRecord(save *SaveData, record wrightstoneWriteExpectation) error {
	return save.VerifyWrightstone(record.ItemUnitID, record.SlotID, record.WrightstoneHash,
		record.FirstHash, record.FirstLevel,
		record.SecondHash, record.SecondLevel,
		record.ThirdHash, record.ThirdLevel)
}

func (wg *WrightstoneGen) startup(ctx context.Context) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	wg.ctx = ctx
}

func (wg *WrightstoneGen) LoadCatalog() error {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	return wg.loadCatalogLocked()
}

func (wg *WrightstoneGen) loadCatalogLocked() error {
	c, err := LoadWrightstoneCatalog()
	if err != nil {
		return err
	}
	wg.catalog = c
	return nil
}

func (wg *WrightstoneGen) ensureCatalogLocked() error {
	if wg.catalog == nil {
		return wg.loadCatalogLocked()
	}
	return nil
}

func (wg *WrightstoneGen) GetWrightstoneList() ([]WrightstoneInfo, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if err := wg.ensureCatalogLocked(); err != nil {
		return nil, err
	}
	sorted := wg.catalog.GetWrightstoneSortedList()
	result := make([]WrightstoneInfo, len(sorted))
	for i, w := range sorted {
		defaultName := ""
		if t, err := wg.catalog.RequireTrait(w.DefaultTraitID); err == nil {
			defaultName = cnWrightstoneTrait(t.DisplayName)
		}
		result[i] = WrightstoneInfo{
			InternalID:       w.InternalID,
			Hash:             w.Hash,
			DisplayName:      cnWrightstone(w.DisplayName),
			DefaultTraitID:   w.DefaultTraitID,
			DefaultTraitName: defaultName,
		}
	}
	return result, nil
}

func (wg *WrightstoneGen) GetTraitList() ([]WrightstoneTraitInfo, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if err := wg.ensureCatalogLocked(); err != nil {
		return nil, err
	}
	sorted := wg.catalog.GetTraitSortedList()
	result := make([]WrightstoneTraitInfo, len(sorted))
	for i, t := range sorted {
		levels, _ := requireWrightstoneTraitLevels(t)
		result[i] = WrightstoneTraitInfo{
			InternalID:    t.InternalID,
			Hash:          t.Hash,
			DisplayName:   cnWrightstoneTrait(t.DisplayName),
			MaxLevel:      derefInt(t.MaxLevel),
			AllowedLevels: levels,
		}
	}
	return result, nil
}

func (wg *WrightstoneGen) GetTraitLevels(traitID string) ([]int, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if err := wg.ensureCatalogLocked(); err != nil {
		return nil, err
	}
	trait, err := wg.catalog.RequireTrait(traitID)
	if err != nil {
		return nil, err
	}
	return requireWrightstoneTraitLevels(trait)
}

func (wg *WrightstoneGen) GetDefaultTrait(wrightstoneID string) (*WrightstoneTraitInfo, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if err := wg.ensureCatalogLocked(); err != nil {
		return nil, err
	}
	w, err := wg.catalog.RequireWrightstone(wrightstoneID)
	if err != nil {
		return nil, err
	}
	t, err := wg.catalog.RequireTrait(w.DefaultTraitID)
	if err != nil {
		return nil, err
	}
	levels, _ := requireWrightstoneTraitLevels(t)
	return &WrightstoneTraitInfo{
		InternalID:    t.InternalID,
		Hash:          t.Hash,
		DisplayName:   cnWrightstoneTrait(t.DisplayName),
		MaxLevel:      derefInt(t.MaxLevel),
		AllowedLevels: levels,
	}, nil
}

func (wg *WrightstoneGen) LoadSaveFile(path string) (*WrightstoneSaveInfo, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	s, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	wg.save = s
	wg.savePath = path
	wg.retryAfterFailedCommit = nil

	info := &WrightstoneSaveInfo{Path: path, OccupiedWrightstones: s.GetOccupiedWrightstoneCount()}
	if maxID, err := s.GetMaxWrightstoneSlotID(); err == nil {
		info.MaxSlotID = maxID
	}
	return info, nil
}

func (wg *WrightstoneGen) GetLoadedSaveInfo() (*WrightstoneSaveInfo, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if wg.save == nil {
		return nil, fmt.Errorf("未加载存档")
	}
	info := &WrightstoneSaveInfo{Path: wg.savePath, OccupiedWrightstones: wg.save.GetOccupiedWrightstoneCount()}
	if maxID, err := wg.save.GetMaxWrightstoneSlotID(); err == nil {
		info.MaxSlotID = maxID
	}
	return info, nil
}

func (wg *WrightstoneGen) FileExists(path string) (bool, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (wg *WrightstoneGen) SelectWrightstoneInputSave() (string, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if wg.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	return runtime.OpenFileDialog(wg.ctx, runtime.OpenDialogOptions{
		Title: "选择 GBFR 存档文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "GBFR 存档 (*.dat)", Pattern: "*.dat"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
}

func (wg *WrightstoneGen) SelectWrightstoneOutputSave(defaultPath string) (string, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if wg.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	defaultDir := ""
	defaultName := ""
	if defaultPath != "" {
		defaultDir = filepath.Dir(defaultPath)
		defaultName = filepath.Base(defaultPath)
	}
	return runtime.SaveFileDialog(wg.ctx, runtime.SaveDialogOptions{
		Title:            "选择输出存档文件",
		DefaultDirectory: defaultDir,
		DefaultFilename:  defaultName,
		Filters: []runtime.FileFilter{
			{DisplayName: "GBFR 存档 (*.dat)", Pattern: "*.dat"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
}

func (wg *WrightstoneGen) GetQueue() []WrightstoneQueueItem {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if wg.queue == nil {
		return []WrightstoneQueueItem{}
	}
	return append([]WrightstoneQueueItem(nil), wg.queue...)
}

func (wg *WrightstoneGen) AddToQueue(item WrightstoneQueueItem) error {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	normalized, report, err := wg.normalizeWrightstoneQueueItem(item)
	if err != nil {
		return err
	}
	if !report.Writable {
		return fmt.Errorf("%s", report.Message)
	}
	wg.queue = append(wg.queue, normalized)
	return nil
}

func (wg *WrightstoneGen) CheckLegality(item WrightstoneQueueItem) (LegalityReport, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	_, report, err := wg.normalizeWrightstoneQueueItem(item)
	return report, err
}

func (wg *WrightstoneGen) normalizeWrightstoneQueueItem(item WrightstoneQueueItem) (WrightstoneQueueItem, LegalityReport, error) {
	if err := wg.ensureCatalogLocked(); err != nil {
		return item, LegalityReport{}, err
	}
	if item.Quantity <= 0 {
		report := newLegalityReport(LegalityImpossible, false, "数量至少为 1")
		return item, report, nil
	}
	if item.Quantity > generatorQuantityMax {
		report := newLegalityReport(LegalityImpossible, false, fmt.Sprintf("数量不能超过 %d", generatorQuantityMax))
		return item, report, nil
	}
	wrightstone, err := wg.catalog.RequireWrightstone(item.WrightstoneID)
	if err != nil {
		report := newLegalityReport(LegalityImpossible, false, err.Error())
		return item, report, nil
	}
	item.WrightstoneName = cnWrightstone(wrightstone.DisplayName)
	reasons := make([]string, 0, 5)
	for _, level := range []struct {
		value int
		label string
	}{
		{item.FirstLevel, "第一特性等级"},
		{item.SecondLevel, "第二特性等级"},
		{item.ThirdLevel, "第三特性等级"},
	} {
		if err := validateStoredLevel(level.value, level.label); err != nil {
			return item, newLegalityReport(LegalityImpossible, false, err.Error()), nil
		}
	}

	firstTrait, err := wg.catalog.RequireTrait(item.FirstTraitID)
	if err != nil {
		report := newLegalityReport(LegalityImpossible, false, "缺少可写入的第一特性")
		return item, report, nil
	}
	if err := validateWrightstoneSlotLevel(firstTrait, item.FirstLevel, "第一特性", 20); err != nil {
		reasons = append(reasons, err.Error())
	}
	item.FirstTraitName = cnWrightstoneTrait(firstTrait.DisplayName)
	firstLevels, err := requireWrightstoneTraitLevels(firstTrait)
	if err != nil {
		return item, LegalityReport{}, err
	}
	firstWritableMax := effectCurveMax(firstLevels, 20)
	if item.FirstLevel > firstWritableMax {
		return item, newLegalityReport(LegalityImpossible, false, fmt.Sprintf("第一特性 %s 的等级 %d 超过技能效果曲线上限 %d", item.FirstTraitName, item.FirstLevel, firstWritableMax)), nil
	}
	if firstTrait.InternalID != wrightstone.DefaultTraitID {
		reasons = append(reasons, "第一特性与该祝福的固有特性不一致")
	}

	secondTrait, err := wg.catalog.RequireTrait(item.SecondTraitID)
	if err != nil {
		report := newLegalityReport(LegalityImpossible, false, "缺少可写入的第二特性")
		return item, report, nil
	}
	if err := validateWrightstoneSlotLevel(secondTrait, item.SecondLevel, "第二特性", 15); err != nil {
		reasons = append(reasons, err.Error())
	}
	item.SecondTraitName = cnWrightstoneTrait(secondTrait.DisplayName)
	secondLevels, err := requireWrightstoneTraitLevels(secondTrait)
	if err != nil {
		return item, LegalityReport{}, err
	}
	secondWritableMax := effectCurveMax(secondLevels, 15)
	if item.SecondLevel > secondWritableMax {
		return item, newLegalityReport(LegalityImpossible, false, fmt.Sprintf("第二特性 %s 的等级 %d 超过技能效果曲线上限 %d", item.SecondTraitName, item.SecondLevel, secondWritableMax)), nil
	}

	thirdTrait, err := wg.catalog.RequireTrait(item.ThirdTraitID)
	if err != nil {
		report := newLegalityReport(LegalityImpossible, false, "缺少可写入的第三特性")
		return item, report, nil
	}
	if err := validateWrightstoneSlotLevel(thirdTrait, item.ThirdLevel, "第三特性", 10); err != nil {
		reasons = append(reasons, err.Error())
	}
	item.ThirdTraitName = cnWrightstoneTrait(thirdTrait.DisplayName)
	thirdLevels, err := requireWrightstoneTraitLevels(thirdTrait)
	if err != nil {
		return item, LegalityReport{}, err
	}
	thirdWritableMax := effectCurveMax(thirdLevels, 10)
	if item.ThirdLevel > thirdWritableMax {
		return item, newLegalityReport(LegalityImpossible, false, fmt.Sprintf("第三特性 %s 的等级 %d 超过技能效果曲线上限 %d", item.ThirdTraitName, item.ThirdLevel, thirdWritableMax)), nil
	}

	if item.FirstTraitID == item.SecondTraitID {
		reasons = append(reasons, fmt.Sprintf("第一特性「%s」与第二特性「%s」重复冲突", item.FirstTraitName, item.SecondTraitName))
	}
	if item.FirstTraitID == item.ThirdTraitID {
		reasons = append(reasons, fmt.Sprintf("第一特性「%s」与第三特性「%s」重复冲突", item.FirstTraitName, item.ThirdTraitName))
	}
	if item.SecondTraitID == item.ThirdTraitID {
		reasons = append(reasons, fmt.Sprintf("第二特性「%s」与第三特性「%s」重复冲突", item.SecondTraitName, item.ThirdTraitName))
	}
	status := LegalityUnknown
	if len(reasons) > 0 {
		status = LegalityForced
		reasons = append(reasons, "合规检测仅作提示；确认强制写入后会保留所选值")
	} else {
		reasons = append(reasons, "已验证固有特性和等级；第二、第三特性的完整天然词池仍缺少可靠数据")
	}
	report := newLegalityReport(status, true, reasons...)
	item.LegalityStatus = report.Status
	item.LegalityMessage = report.Message
	return item, report, nil
}

func validateWrightstoneSlotLevel(trait *WrightstoneTraitDef, level int, label string, naturalMax int) error {
	if trait == nil {
		return fmt.Errorf("%s缺少特性数据", label)
	}
	if level < 1 || level > naturalMax {
		return fmt.Errorf("%s %s 等级 %d 超出自然范围 1 到 %d", label, trait.DisplayName, level, naturalMax)
	}
	return nil
}

func (wg *WrightstoneGen) RemoveFromQueue(index int) error {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if index < 0 || index >= len(wg.queue) {
		return fmt.Errorf("无效的队列索引: %d", index)
	}
	wg.queue = append(wg.queue[:index], wg.queue[index+1:]...)
	return nil
}

func (wg *WrightstoneGen) ClearQueue() {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	wg.queue = nil
}

func (wg *WrightstoneGen) ApplyQueue(outputPath string) (*WrightstoneApplyResult, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()
	items := append([]WrightstoneQueueItem(nil), wg.queue...)
	result, err := wg.applyItemsLocked(items, outputPath)
	if err != nil {
		return nil, err
	}
	wg.queue = nil
	return result, nil
}

func (wg *WrightstoneGen) ApplyItems(items []WrightstoneQueueItem, outputPath string) (*WrightstoneApplyResult, error) {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()
	return wg.applyItemsLocked(append([]WrightstoneQueueItem(nil), items...), outputPath)
}

func (wg *WrightstoneGen) applyItemsLocked(items []WrightstoneQueueItem, outputPath string) (*WrightstoneApplyResult, error) {
	if err := ensureGeneratorWriteAllowed(outputPath); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("没有要写入的祝福")
	}
	if wg.save == nil {
		return nil, fmt.Errorf("请先加载存档")
	}
	outputPath = strings.TrimSpace(outputPath)
	retryAllowed, err := allowExactFailedCommitRetry(wg.savePath, outputPath, wg.retryAfterFailedCommit)
	if err != nil {
		return nil, err
	}
	if !retryAllowed {
		if err := ensureOfflineSaveSnapshotCurrent(wg.savePath, wg.save.data); err != nil {
			return nil, err
		}
	}
	wg.retryAfterFailedCommit = nil
	if outputPath == "" {
		return nil, fmt.Errorf("请输入输出路径")
	}
	if err := wg.ensureCatalogLocked(); err != nil {
		return nil, err
	}

	normalized := make([]WrightstoneQueueItem, len(items))
	for i, item := range items {
		n, report, err := wg.normalizeWrightstoneQueueItem(item)
		if err != nil {
			return nil, err
		}
		if !report.Writable {
			return nil, fmt.Errorf("%s", report.Message)
		}
		normalized[i] = n
	}

	var expanded []WrightstoneQueueItem
	for _, item := range normalized {
		for i := 0; i < item.Quantity; i++ {
			expanded = append(expanded, item)
		}
	}

	emptySlots, err := wg.save.FindEmptyWrightstoneSlots(len(expanded))
	if err != nil {
		return nil, err
	}

	maxSlotID, err := wg.save.GetMaxWrightstoneSlotID()
	if err != nil {
		return nil, err
	}
	firstNewSlotID, newMaxSlotID, err := allocateSequentialSlotIDs(maxSlotID, len(expanded))
	if err != nil {
		return nil, fmt.Errorf("无法分配祝福 SlotID: %w", err)
	}

	for i := range expanded {
		itemUnitID := emptySlots[i]
		traitBase := getWrightstoneTraitBase(itemUnitID)
		if _, ok := wg.save.findUnit(WrightstoneItemIDType, uint32(itemUnitID)); !ok {
			return nil, fmt.Errorf("祝福槽 %d 缺少 ITEM_ID", itemUnitID)
		}
		if _, ok := wg.save.findUnit(WrightstoneSlotIDType, uint32(itemUnitID)); !ok {
			return nil, fmt.Errorf("祝福槽 %d 缺少 SLOT_ID", itemUnitID)
		}
		if _, ok := wg.save.findUnit(WrightstoneBoolIDType, uint32(itemUnitID)); !ok {
			return nil, fmt.Errorf("祝福槽 %d 缺少 BOOL 字段", itemUnitID)
		}
		if _, ok := wg.save.findUnit(WrightstoneFlagsIDType, uint32(itemUnitID)); !ok {
			return nil, fmt.Errorf("祝福槽 %d 缺少 FLAGS", itemUnitID)
		}
		for j := 0; j < 3; j++ {
			unit := uint32(traitBase + j)
			if _, ok := wg.save.findUnit(TraitHashIDType, unit); !ok {
				return nil, fmt.Errorf("祝福槽 %d 缺少第 %d 个特性哈希", itemUnitID, j+1)
			}
			if _, ok := wg.save.findUnit(TraitLevelIDType, unit); !ok {
				return nil, fmt.Errorf("祝福槽 %d 缺少第 %d 个特性等级", itemUnitID, j+1)
			}
		}
	}

	// Treat the in-memory patch, disk replacement and strict readback as one
	// transaction.  A failed attempt must leave both the save buffer and queue
	// exactly as they were so a retry rewrites the same slots instead of appending.
	originalData := append([]byte(nil), wg.save.data...)
	originalQueue := append([]WrightstoneQueueItem(nil), wg.queue...)
	originalBackupPath := wg.save.lastBackupPath
	committed := false
	diskWritten := false
	var writtenData []byte
	defer func() {
		if committed {
			return
		}
		wg.save.data = originalData
		wg.save.lastBackupPath = originalBackupPath
		wg.queue = originalQueue
		if diskWritten {
			wg.retryAfterFailedCommit = &failedGeneratorCommit{outputPath: outputPath, written: writtenData}
		}
	}()

	if err := wg.save.SetMaxWrightstoneSlotID(newMaxSlotID); err != nil {
		return nil, err
	}

	created := 0
	expectedWrites := make([]wrightstoneWriteExpectation, 0, len(expanded))
	for i, item := range expanded {
		itemUnitID := emptySlots[i]
		newSlotID := firstNewSlotID + i

		wrightstone, _ := wg.catalog.RequireWrightstone(item.WrightstoneID)
		wrightstoneHash, err := ParseHashHex(wrightstone.Hash)
		if err != nil {
			return nil, fmt.Errorf("%s 哈希无效: %s", wrightstone.DisplayName, wrightstone.Hash)
		}
		firstTrait, _ := wg.catalog.RequireTrait(item.FirstTraitID)
		firstHash, err := ParseHashHex(firstTrait.Hash)
		if err != nil {
			return nil, fmt.Errorf("%s 哈希无效", firstTrait.DisplayName)
		}
		secondTrait, _ := wg.catalog.RequireTrait(item.SecondTraitID)
		secondHash, err := ParseHashHex(secondTrait.Hash)
		if err != nil {
			return nil, fmt.Errorf("%s 哈希无效", secondTrait.DisplayName)
		}
		thirdTrait, _ := wg.catalog.RequireTrait(item.ThirdTraitID)
		thirdHash, err := ParseHashHex(thirdTrait.Hash)
		if err != nil {
			return nil, fmt.Errorf("%s 哈希无效", thirdTrait.DisplayName)
		}

		if err := wg.save.PatchWrightstone(itemUnitID, newSlotID, wrightstoneHash,
			firstHash, item.FirstLevel,
			secondHash, item.SecondLevel,
			thirdHash, item.ThirdLevel); err != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", item.WrightstoneName, err)
		}
		expectedWrites = append(expectedWrites, wrightstoneWriteExpectation{
			ItemUnitID:      itemUnitID,
			SlotID:          newSlotID,
			WrightstoneHash: wrightstoneHash,
			FirstHash:       firstHash,
			FirstLevel:      item.FirstLevel,
			SecondHash:      secondHash,
			SecondLevel:     item.SecondLevel,
			ThirdHash:       thirdHash,
			ThirdLevel:      item.ThirdLevel,
		})
		created++
	}

	if err := wg.save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("校验和修复失败: %w", err)
	}
	if err := wg.save.Write(outputPath); err != nil {
		return nil, fmt.Errorf("写入输出文件失败: %w", err)
	}
	diskWritten = true
	writtenData = append([]byte(nil), wg.save.data...)

	verified, err := wg.verifyWrittenWrightstones(outputPath, created, expectedWrites, verifyWrightstoneRecord)
	if err != nil {
		return nil, err
	}

	absPath, _ := filepath.Abs(outputPath)
	committed = true
	wg.retryAfterFailedCommit = nil
	return &WrightstoneApplyResult{CreatedCount: created, VerifiedCount: verified, OutputPath: absPath}, nil
}
