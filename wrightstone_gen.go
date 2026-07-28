package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
}

type WrightstoneApplyResult struct {
	CreatedCount  int    `json:"createdCount"`
	VerifiedCount int    `json:"verifiedCount"`
	OutputPath    string `json:"outputPath"`
}

type WrightstoneGen struct {
	ctx      context.Context
	catalog  *WrightstoneCatalog
	save     *SaveData
	savePath string
	queue    []WrightstoneQueueItem
}

func NewWrightstoneGen() *WrightstoneGen {
	return &WrightstoneGen{}
}

func (wg *WrightstoneGen) startup(ctx context.Context) { wg.ctx = ctx }

func (wg *WrightstoneGen) LoadCatalog() error {
	c, err := LoadWrightstoneCatalog()
	if err != nil {
		return err
	}
	wg.catalog = c
	return nil
}

func (wg *WrightstoneGen) ensureCatalog() error {
	if wg.catalog == nil {
		return wg.LoadCatalog()
	}
	return nil
}

func (wg *WrightstoneGen) GetWrightstoneList() ([]WrightstoneInfo, error) {
	if err := wg.ensureCatalog(); err != nil {
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
	if err := wg.ensureCatalog(); err != nil {
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
	if err := wg.ensureCatalog(); err != nil {
		return nil, err
	}
	trait, err := wg.catalog.RequireTrait(traitID)
	if err != nil {
		return nil, err
	}
	return requireWrightstoneTraitLevels(trait)
}

func (wg *WrightstoneGen) GetDefaultTrait(wrightstoneID string) (*WrightstoneTraitInfo, error) {
	if err := wg.ensureCatalog(); err != nil {
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
	s, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	wg.save = s
	wg.savePath = path

	info := &WrightstoneSaveInfo{Path: path, OccupiedWrightstones: s.GetOccupiedWrightstoneCount()}
	if maxID, err := s.GetMaxWrightstoneSlotID(); err == nil {
		info.MaxSlotID = maxID
	}
	return info, nil
}

func (wg *WrightstoneGen) GetLoadedSaveInfo() (*WrightstoneSaveInfo, error) {
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
	if wg.queue == nil {
		return []WrightstoneQueueItem{}
	}
	return wg.queue
}

func (wg *WrightstoneGen) AddToQueue(item WrightstoneQueueItem) error {
	normalized, err := wg.normalizeWrightstoneQueueItem(item)
	if err != nil {
		return err
	}
	wg.queue = append(wg.queue, normalized)
	return nil
}

func (wg *WrightstoneGen) normalizeWrightstoneQueueItem(item WrightstoneQueueItem) (WrightstoneQueueItem, error) {
	if err := wg.ensureCatalog(); err != nil {
		return item, err
	}
	if item.Quantity <= 0 {
		return item, fmt.Errorf("数量至少为 1")
	}
	wrightstone, err := wg.catalog.RequireWrightstone(item.WrightstoneID)
	if err != nil {
		return item, err
	}
	item.WrightstoneName = cnWrightstone(wrightstone.DisplayName)

	firstTrait, err := wg.catalog.RequireTrait(item.FirstTraitID)
	if err != nil {
		return item, err
	}
	if err := validateWrightstoneTraitLevel(firstTrait, item.FirstLevel, "第一特性"); err != nil {
		return item, err
	}
	item.FirstTraitName = cnTrait(firstTrait.DisplayName)

	secondTrait, err := wg.catalog.RequireTrait(item.SecondTraitID)
	if err != nil {
		return item, err
	}
	if err := validateWrightstoneTraitLevel(secondTrait, item.SecondLevel, "第二特性"); err != nil {
		return item, err
	}
	item.SecondTraitName = cnTrait(secondTrait.DisplayName)

	thirdTrait, err := wg.catalog.RequireTrait(item.ThirdTraitID)
	if err != nil {
		return item, err
	}
	if err := validateWrightstoneTraitLevel(thirdTrait, item.ThirdLevel, "第三特性"); err != nil {
		return item, err
	}
	item.ThirdTraitName = cnTrait(thirdTrait.DisplayName)
	return item, nil
}

func validateWrightstoneTraitLevel(trait *WrightstoneTraitDef, level int, label string) error {
	levels, err := requireWrightstoneTraitLevels(trait)
	if err != nil {
		return err
	}
	if !containsInt(levels, level) {
		return fmt.Errorf("%s %s 不允许等级 %d", label, trait.DisplayName, level)
	}
	return nil
}

func (wg *WrightstoneGen) RemoveFromQueue(index int) error {
	if index < 0 || index >= len(wg.queue) {
		return fmt.Errorf("无效的队列索引: %d", index)
	}
	wg.queue = append(wg.queue[:index], wg.queue[index+1:]...)
	return nil
}

func (wg *WrightstoneGen) ClearQueue() {
	wg.queue = nil
}

func (wg *WrightstoneGen) ApplyQueue(outputPath string) (*WrightstoneApplyResult, error) {
	result, err := wg.applyItems(wg.queue, outputPath)
	if err != nil {
		return nil, err
	}
	wg.queue = nil
	return result, nil
}

func (wg *WrightstoneGen) ApplyItems(items []WrightstoneQueueItem, outputPath string) (*WrightstoneApplyResult, error) {
	return wg.applyItems(items, outputPath)
}

func (wg *WrightstoneGen) applyItems(items []WrightstoneQueueItem, outputPath string) (*WrightstoneApplyResult, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("没有要写入的祝福")
	}
	if wg.save == nil {
		return nil, fmt.Errorf("请先加载存档")
	}
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return nil, fmt.Errorf("请输入输出路径")
	}
	if err := wg.ensureCatalog(); err != nil {
		return nil, err
	}

	normalized := make([]WrightstoneQueueItem, len(items))
	for i, item := range items {
		n, err := wg.normalizeWrightstoneQueueItem(item)
		if err != nil {
			return nil, err
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
	firstNewSlotID := maxSlotID + 1

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

	newMaxSlotID := firstNewSlotID + len(expanded) - 1
	if err := wg.save.SetMaxWrightstoneSlotID(newMaxSlotID); err != nil {
		return nil, err
	}

	created := 0
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
		created++
	}

	if err := wg.save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("校验和修复失败: %w", err)
	}
	if err := wg.save.Write(outputPath); err != nil {
		return nil, fmt.Errorf("写入输出文件失败: %w", err)
	}

	verified := 0
	verifySave, err := LoadSave(outputPath)
	if err == nil {
		for i, item := range expanded {
			itemUnitID := emptySlots[i]
			newSlotID := firstNewSlotID + i
			wrightstone, _ := wg.catalog.RequireWrightstone(item.WrightstoneID)
			wrightstoneHash, _ := ParseHashHex(wrightstone.Hash)
			firstTrait, _ := wg.catalog.RequireTrait(item.FirstTraitID)
			firstHash, _ := ParseHashHex(firstTrait.Hash)
			secondTrait, _ := wg.catalog.RequireTrait(item.SecondTraitID)
			secondHash, _ := ParseHashHex(secondTrait.Hash)
			thirdTrait, _ := wg.catalog.RequireTrait(item.ThirdTraitID)
			thirdHash, _ := ParseHashHex(thirdTrait.Hash)

			if verifySave.VerifyWrightstone(itemUnitID, newSlotID, wrightstoneHash,
				firstHash, item.FirstLevel,
				secondHash, item.SecondLevel,
				thirdHash, item.ThirdLevel) == nil {
				verified++
			}
		}
	}

	absPath, _ := filepath.Abs(outputPath)
	return &WrightstoneApplyResult{CreatedCount: created, VerifiedCount: verified, OutputPath: absPath}, nil
}

func defaultWrightstoneOutputPath(inputPath string) string {
	dir := filepath.Dir(inputPath)
	ext := filepath.Ext(inputPath)
	base := strings.TrimSuffix(filepath.Base(inputPath), ext)
	if ext == "" {
		ext = ".dat"
	}
	return filepath.Join(dir, base+"_wrightstones"+ext)
}
