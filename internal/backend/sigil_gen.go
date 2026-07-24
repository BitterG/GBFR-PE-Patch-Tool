package backend

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ── 前端交互数据结构 ──

type SigilInfo struct {
	InternalID              string `json:"internalId"`
	Hash                    string `json:"hash"`
	DisplayName             string `json:"displayName"`
	Category                string `json:"category"`
	Verified                bool   `json:"verified"`
	Constructible           bool   `json:"constructible"`
	SupportsSecondaryTrait  bool   `json:"supportsSecondaryTrait"`
	AllowedSigilLevels      []int  `json:"allowedSigilLevels"`
	DefaultSigilLevel       int    `json:"defaultSigilLevel"`
	PrimaryTraitID          string `json:"primaryTraitId"`
	PrimaryTraitName        string `json:"primaryTraitName"`
	AllowedFirstTraitLevels []int  `json:"allowedFirstTraitLevels"`
	FirstTraitMaxLevel      int    `json:"firstTraitMaxLevel"`
}

type TraitInfo struct {
	InternalID    string `json:"internalId"`
	Hash          string `json:"hash"`
	DisplayName   string `json:"displayName"`
	MaxLevel      int    `json:"maxLevel"`
	AllowedLevels []int  `json:"allowedLevels"`
}

type SaveInfo struct {
	Path           string `json:"path"`
	OccupiedSigils int    `json:"occupiedSigils"`
	MaxSlotID      int    `json:"maxSlotId"`
}

type QueueItem struct {
	SigilID            string `json:"sigilId"`
	SigilName          string `json:"sigilName"`
	Level              int    `json:"level"`
	PrimaryTraitID     string `json:"primaryTraitId"`
	PrimaryTraitName   string `json:"primaryTraitName"`
	PrimaryLevel       int    `json:"primaryLevel"`
	SecondaryTraitID   string `json:"secondaryTraitId"`
	SecondaryTraitName string `json:"secondaryTraitName"`
	SecondaryLevel     int    `json:"secondaryLevel"`
	Quantity           int    `json:"quantity"`
	LegalityStatus     string `json:"legalityStatus"`
	LegalityMessage    string `json:"legalityMessage"`
}

type ApplyResult struct {
	CreatedCount  int      `json:"createdCount"`
	VerifiedCount int      `json:"verifiedCount"`
	OutputPath    string   `json:"outputPath"`
	BackupPath    string   `json:"backupPath,omitempty"`
	SlotIDs       []uint32 `json:"slotIds,omitempty"`
}

// ── SigilGen 主体 ──

type SigilGen struct {
	mu                      sync.Mutex
	ctx                     context.Context
	catalog                 *Catalog
	save                    *SaveData
	savePath                string
	queue                   []QueueItem
	loadSaveForVerification func(string) (*SaveData, error)
	retryAfterFailedCommit  *failedGeneratorCommit
}

var generatorFindProcessByName = findProcessByName

func isDefaultManagedSavePath(path string) bool {
	path = strings.TrimSpace(path)
	slot, ok := managedSaveSlot(path)
	if !ok {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	expected, err := filepath.Abs(filepath.Join(defaultSaveGamesDir(), fmt.Sprintf("SaveData%d.dat", slot)))
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(target), filepath.Clean(expected))
}

func ensureGeneratorWriteAllowed(outputPath string) error {
	if !isDefaultManagedSavePath(outputPath) {
		return nil
	}
	if _, err := generatorFindProcessByName(charaProcessName); err == nil {
		return fmt.Errorf("写入默认存档前请先完全退出游戏，避免游戏把旧数据写回")
	}
	return nil
}

const (
	sigilWritableLevelMax = 50
	generatorQuantityMax  = 999
)

func highestLevel(levels []int, fallback int) int {
	max := fallback
	for _, level := range levels {
		if level > max {
			max = level
		}
	}
	return max
}

func effectCurveMax(levels []int, fallback int) int {
	max := 0
	for _, level := range levels {
		if level > max {
			max = level
		}
	}
	if max > 0 {
		return max
	}
	return fallback
}

func NewSigilGen() *SigilGen {
	return &SigilGen{loadSaveForVerification: LoadSave}
}

func (sg *SigilGen) startup(ctx context.Context) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	sg.ctx = ctx
}

// LoadCatalog 加载数据目录（从嵌入的 JSON 文件）
func (sg *SigilGen) LoadCatalog() error {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	return sg.loadCatalogLocked()
}

func (sg *SigilGen) loadCatalogLocked() error {
	c, err := LoadCatalog()
	if err != nil {
		return err
	}
	sg.catalog = c
	return nil
}

// GetSigilList 返回排序后的因子列表
func (sg *SigilGen) GetSigilList() ([]SigilInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	sorted := sg.catalog.GetSigilSortedList()
	result := make([]SigilInfo, 0, len(sorted))
	for _, s := range sorted {
		sigilLevels, _ := sg.catalog.RequireSigilLevels(s)
		primaryLevels, _ := sg.catalog.RequirePrimaryTraitLevels(s)
		naturalSigil := naturalSigilLevelsForDefinition(s, sigilLevels)
		naturalPrimary := naturalSigilLevelsForDefinition(s, primaryLevels)
		defaultLevel := derefInt(s.DefaultSigilLevel)
		if defaultLevel < 1 || defaultLevel > 15 {
			defaultLevel = maxNaturalSigilLevel(naturalSigil)
		}
		result = append(result, SigilInfo{
			InternalID:              s.InternalID,
			Hash:                    s.Hash,
			DisplayName:             displaySigilName(s),
			Category:                derefStr(s.Category),
			Verified:                isVerifiedSigilDefinition(s),
			Constructible:           sg.catalog.IsSigilConstructible(s),
			SupportsSecondaryTrait:  supportsGeneratedPlusSigil(s),
			AllowedSigilLevels:      naturalSigil,
			DefaultSigilLevel:       defaultLevel,
			PrimaryTraitID:          s.PrimaryTraitID,
			PrimaryTraitName:        cnTrait(derefStr(s.PrimaryTraitName)),
			AllowedFirstTraitLevels: naturalPrimary,
			FirstTraitMaxLevel:      maxNaturalSigilLevel(naturalPrimary),
		})
	}
	return result, nil
}

// GetTraitList 返回所有特性
func (sg *SigilGen) GetTraitList() ([]TraitInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	result := make([]TraitInfo, 0, len(sg.catalog.Traits))
	for i, t := range sg.catalog.Traits {
		if !isSelectableTrait(&sg.catalog.Traits[i]) {
			continue
		}
		result = append(result, TraitInfo{
			InternalID:    t.InternalID,
			Hash:          t.Hash,
			DisplayName:   cnTrait(t.DisplayName),
			MaxLevel:      derefInt(t.MaxLevel),
			AllowedLevels: t.AllowedLevels,
		})
	}
	return result, nil
}

// GetCompatibleSecondaryTraits 返回可选副特性列表
func (sg *SigilGen) GetCompatibleSecondaryTraits(sigilID string) ([]TraitInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	sigil, err := sg.catalog.RequireSigil(sigilID)
	if err != nil {
		return nil, err
	}
	if !sg.catalog.IsSigilConstructible(sigil) || len(sigil.AllowedSecondaryTraitIDs) == 0 {
		return []TraitInfo{}, nil
	}

	secondaries, err := sg.catalog.GetAllowedSecondaryTraits(sigil)
	if err != nil {
		return nil, err
	}

	explicit := make(map[string]bool, len(sigil.AllowedSecondaryTraitIDs))
	for _, id := range sigil.AllowedSecondaryTraitIDs {
		explicit[id] = true
	}
	result := make([]TraitInfo, 0, len(secondaries))
	for _, t := range secondaries {
		if !explicit[t.InternalID] || t.InternalID == sigil.PrimaryTraitID {
			continue
		}
		levels, err := sg.catalog.RequireSecondaryTraitLevels(sigil, t)
		if err != nil {
			continue
		}
		naturalLevels := naturalSigilLevels(levels)
		if len(naturalLevels) == 0 {
			continue
		}
		result = append(result, TraitInfo{
			InternalID:    t.InternalID,
			Hash:          t.Hash,
			DisplayName:   cnTrait(t.DisplayName),
			MaxLevel:      maxNaturalSigilLevel(naturalLevels),
			AllowedLevels: naturalLevels,
		})
	}
	return result, nil
}

// GetAllowedLevels 返回因子可选等级
func (sg *SigilGen) GetAllowedLevels(sigilID string) ([]int, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	sigil, err := sg.catalog.RequireSigil(sigilID)
	if err != nil {
		return nil, err
	}
	return sg.catalog.RequireSigilLevels(sigil)
}

// GetPrimaryTraitLevels 返回主特性可选等级
func (sg *SigilGen) GetPrimaryTraitLevels(sigilID string) ([]int, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	sigil, err := sg.catalog.RequireSigil(sigilID)
	if err != nil {
		return nil, err
	}
	return sg.catalog.RequirePrimaryTraitLevels(sigil)
}

// GetSecondaryTraitLevels 返回副特性可选等级
func (sg *SigilGen) GetSecondaryTraitLevels(sigilID, traitID string) ([]int, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	sigil, err := sg.catalog.RequireSigil(sigilID)
	if err != nil {
		return nil, err
	}
	trait, err := sg.catalog.RequireTrait(traitID)
	if err != nil {
		return nil, err
	}
	return sg.catalog.RequireSecondaryTraitLevels(sigil, trait)
}

// GetDefaultSecondaryTrait 返回因子的默认副特性
func (sg *SigilGen) GetDefaultSecondaryTrait(sigilID string) (*TraitInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	sigil, err := sg.catalog.RequireSigil(sigilID)
	if err != nil {
		return nil, err
	}
	t := sg.catalog.GetDefaultSecondaryTrait(sigil)
	if t == nil {
		return nil, nil
	}
	return &TraitInfo{
		InternalID:    t.InternalID,
		Hash:          t.Hash,
		DisplayName:   cnTrait(t.DisplayName),
		MaxLevel:      derefInt(t.MaxLevel),
		AllowedLevels: t.AllowedLevels,
	}, nil
}

// GetPrimaryTrait 返回因子的主特性
func (sg *SigilGen) GetPrimaryTrait(sigilID string) (*TraitInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}
	sigil, err := sg.catalog.RequireSigil(sigilID)
	if err != nil {
		return nil, err
	}
	trait, err := sg.catalog.RequireTrait(sigil.PrimaryTraitID)
	if err != nil {
		return nil, err
	}
	return &TraitInfo{
		InternalID:    trait.InternalID,
		Hash:          trait.Hash,
		DisplayName:   cnTrait(trait.DisplayName),
		MaxLevel:      derefInt(trait.MaxLevel),
		AllowedLevels: trait.AllowedLevels,
	}, nil
}

func (sg *SigilGen) SelectSigilInputSave() (string, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	return runtime.OpenFileDialog(sg.ctx, runtime.OpenDialogOptions{
		Title: "选择 GBFR 存档文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "GBFR 存档 (*.dat)", Pattern: "*.dat"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
}

func (sg *SigilGen) SelectSigilOutputSave(defaultPath string) (string, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	defaultDir := ""
	defaultName := ""
	if defaultPath != "" {
		defaultDir = filepath.Dir(defaultPath)
		defaultName = filepath.Base(defaultPath)
	}
	return runtime.SaveFileDialog(sg.ctx, runtime.SaveDialogOptions{
		Title:            "选择输出存档文件",
		DefaultDirectory: defaultDir,
		DefaultFilename:  defaultName,
		Filters: []runtime.FileFilter{
			{DisplayName: "GBFR 存档 (*.dat)", Pattern: "*.dat"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
}

// ── 存档操作 ──

func (sg *SigilGen) LoadSaveFile(path string) (*SaveInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	s, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	sg.save = s
	sg.savePath = path
	sg.retryAfterFailedCommit = nil

	info := &SaveInfo{Path: path, OccupiedSigils: s.GetOccupiedGemCount()}
	if maxID, err := s.GetMaxSlotID(); err == nil {
		info.MaxSlotID = maxID
	}
	return info, nil
}

func (sg *SigilGen) GetLoadedSaveInfo() (*SaveInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.save == nil {
		return nil, fmt.Errorf("未加载存档")
	}
	info := &SaveInfo{Path: sg.savePath, OccupiedSigils: sg.save.GetOccupiedGemCount()}
	if maxID, err := sg.save.GetMaxSlotID(); err == nil {
		info.MaxSlotID = maxID
	}
	return info, nil
}

// ── 队列操作 ──

func (sg *SigilGen) GetQueue() []QueueItem {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.queue == nil {
		return []QueueItem{}
	}
	return append([]QueueItem(nil), sg.queue...)
}

func (sg *SigilGen) AddToQueue(item QueueItem) error {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	normalized, report, err := sg.normalizeQueueItem(item)
	if err != nil {
		return err
	}
	if !report.Writable {
		return fmt.Errorf("%s", report.Message)
	}
	if sg.queue == nil {
		sg.queue = []QueueItem{}
	}
	sg.queue = append(sg.queue, normalized)
	return nil
}

// CheckLegality reports natural-game compatibility without changing or
// suppressing any writable value selected by the user.
func (sg *SigilGen) CheckLegality(item QueueItem) (LegalityReport, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	_, report, err := sg.normalizeQueueItem(item)
	return report, err
}

func (sg *SigilGen) normalizeQueueItem(item QueueItem) (QueueItem, LegalityReport, error) {
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return item, LegalityReport{}, err
		}
	}
	if item.Quantity <= 0 {
		report := newLegalityReport(LegalityImpossible, false, "数量至少为 1")
		return item, report, nil
	}
	if item.Quantity > generatorQuantityMax {
		report := newLegalityReport(LegalityImpossible, false, fmt.Sprintf("数量不能超过 %d", generatorQuantityMax))
		return item, report, nil
	}
	sigil, err := sg.catalog.RequireSigil(item.SigilID)
	if err != nil {
		report := newLegalityReport(LegalityImpossible, false, err.Error())
		return item, report, nil
	}
	item.SigilName = displaySigilName(sigil)
	reasons := make([]string, 0, 8)
	if strings.EqualFold(item.SigilID, "GEEN_142_02") {
		reasons = append(reasons, "Seven Net 使用特殊记录标记，将按已验证的 flags=22 写入")
	}

	if err := validateStoredLevel(item.Level, "因子等级"); err != nil {
		return item, newLegalityReport(LegalityImpossible, false, err.Error()), nil
	}
	sigilLevels, err := sg.catalog.RequireSigilLevels(sigil)
	if err != nil {
		return item, LegalityReport{}, err
	}
	if item.Level < 1 || item.Level > highestLevel(sigilLevels, 1) {
		reasons = append(reasons, fmt.Sprintf("因子等级 %d 偏离目录自然范围 1 到 %d", item.Level, highestLevel(sigilLevels, 1)))
	}
	if item.Level > sigilWritableLevelMax {
		return item, newLegalityReport(LegalityImpossible, false, fmt.Sprintf("因子等级 %d 超过修改上限 %d", item.Level, sigilWritableLevelMax)), nil
	}

	primaryID := item.PrimaryTraitID
	if primaryID == "" {
		primaryID = sigil.PrimaryTraitID
	}
	primaryTrait, err := sg.catalog.RequireTrait(primaryID)
	if err != nil {
		return item, LegalityReport{}, err
	}
	item.PrimaryTraitID = primaryTrait.InternalID
	item.PrimaryTraitName = cnTrait(primaryTrait.DisplayName)
	if _, err := requireTraitLevels(primaryTrait, "主特性"); err != nil {
		return item, LegalityReport{}, err
	}
	if err := validateStoredLevel(item.PrimaryLevel, "主特性等级"); err != nil {
		return item, newLegalityReport(LegalityImpossible, false, err.Error()), nil
	}
	primaryLevels, err := requireTraitLevels(primaryTrait, "主特性")
	if err != nil {
		return item, LegalityReport{}, err
	}
	primaryWritableMax := effectCurveMax(primaryLevels, 15)
	if item.PrimaryLevel > primaryWritableMax {
		return item, newLegalityReport(LegalityImpossible, false, fmt.Sprintf("主特性 %s 的等级 %d 超过技能效果曲线上限 %d", item.PrimaryTraitName, item.PrimaryLevel, primaryWritableMax)), nil
	}
	if primaryTrait.InternalID != sigil.PrimaryTraitID {
		reasons = append(reasons, fmt.Sprintf("主特性「%s」不是因子「%s」的自然主特性", item.PrimaryTraitName, item.SigilName))
	}
	primaryNaturalMax := 15
	if primaryTrait.InternalID == sigil.PrimaryTraitID {
		if levels, levelErr := sg.catalog.RequirePrimaryTraitLevels(sigil); levelErr == nil {
			primaryNaturalMax = highestLevel(naturalSigilLevelsForDefinition(sigil, levels), primaryNaturalMax)
		}
	}
	if item.PrimaryLevel < 1 || item.PrimaryLevel > primaryNaturalMax {
		reasons = append(reasons, fmt.Sprintf("主特性等级 %d 偏离目录自然范围 1 到 %d", item.PrimaryLevel, primaryNaturalMax))
	}

	if item.SecondaryTraitID == "" {
		item.SecondaryTraitName = ""
		item.SecondaryLevel = 0
		if requiresCharacterSigilSecondary(sigil) {
			reasons = append(reasons, "角色因子自然配置通常包含副特性")
		}
	} else {
		secondaryTrait, err := sg.catalog.RequireTrait(item.SecondaryTraitID)
		if err != nil {
			report := newLegalityReport(LegalityImpossible, false, err.Error())
			return item, report, nil
		}
		item.SecondaryTraitName = cnTrait(secondaryTrait.DisplayName)
		if _, err := requireTraitLevels(secondaryTrait, "副特性"); err != nil {
			return item, LegalityReport{}, err
		}
		if err := validateStoredLevel(item.SecondaryLevel, "副特性等级"); err != nil {
			return item, newLegalityReport(LegalityImpossible, false, err.Error()), nil
		}
		secondaryLevels, err := requireTraitLevels(secondaryTrait, "副特性")
		if err != nil {
			return item, LegalityReport{}, err
		}
		secondaryWritableMax := effectCurveMax(secondaryLevels, 15)
		if item.SecondaryLevel > secondaryWritableMax {
			return item, newLegalityReport(LegalityImpossible, false, fmt.Sprintf("副特性 %s 的等级 %d 超过技能效果曲线上限 %d", item.SecondaryTraitName, item.SecondaryLevel, secondaryWritableMax)), nil
		}
		if !supportsGeneratedPlusSigil(sigil) {
			reasons = append(reasons, fmt.Sprintf("因子「%s」自然记录不含副特性", item.SigilName))
		}
		if secondaryTrait.InternalID == primaryTrait.InternalID {
			reasons = append(reasons, fmt.Sprintf("主特性「%s」与副特性「%s」重复，游戏不会自然生成同名双词条", item.PrimaryTraitName, item.SecondaryTraitName))
		}
		allowed, _ := sg.catalog.GetAllowedSecondaryTraits(sigil)
		found := false
		for _, candidate := range allowed {
			if candidate.InternalID == secondaryTrait.InternalID {
				found = true
				break
			}
		}
		if !found {
			reasons = append(reasons, fmt.Sprintf("副特性「%s」不属于因子「%s」的自然组合", item.SecondaryTraitName, item.SigilName))
		}
		if item.SecondaryLevel < 1 || item.SecondaryLevel > 15 {
			reasons = append(reasons, fmt.Sprintf("副特性等级 %d 偏离目录自然范围 1 到 15", item.SecondaryLevel))
		}
	}

	status := LegalityLegal
	if len(reasons) > 0 {
		status = LegalityForced
		reasons = append(reasons, "合规检测仅作提示；确认强制写入后会保留所选值")
	}
	report := newLegalityReport(status, true, reasons...)
	item.LegalityStatus = report.Status
	item.LegalityMessage = report.Message
	return item, report, nil
}

func validateStoredLevel(level int, label string) error {
	if level < 0 || int64(level) > math.MaxInt32 {
		return fmt.Errorf("%s必须在 0 到 %d 之间", label, math.MaxInt32)
	}
	return nil
}

func (sg *SigilGen) RemoveFromQueue(index int) error {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if index < 0 || index >= len(sg.queue) {
		return fmt.Errorf("无效的队列索引: %d", index)
	}
	sg.queue = append(sg.queue[:index], sg.queue[index+1:]...)
	return nil
}

func (sg *SigilGen) ClearQueue() {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	sg.queue = nil
}

// ── 写入 ──

func (sg *SigilGen) ApplyQueue(outputPath string) (*ApplyResult, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()
	if err := ensureGeneratorWriteAllowed(outputPath); err != nil {
		return nil, err
	}
	if len(sg.queue) == 0 {
		return nil, fmt.Errorf("队列为空，请先添加因子")
	}
	if sg.save == nil {
		return nil, fmt.Errorf("请先加载存档")
	}
	retryAllowed, err := allowExactFailedCommitRetry(sg.savePath, outputPath, sg.retryAfterFailedCommit)
	if err != nil {
		return nil, err
	}
	if !retryAllowed {
		if err := ensureOfflineSaveSnapshotCurrent(sg.savePath, sg.save.data); err != nil {
			return nil, err
		}
	}
	sg.retryAfterFailedCommit = nil

	// 展开队列（按 quantity 展开）
	var expanded []QueueItem
	for _, item := range sg.queue {
		for i := 0; i < item.Quantity; i++ {
			expanded = append(expanded, item)
		}
	}

	// 找空槽
	emptySlots, err := sg.save.FindEmptyGemSlots(len(expanded))
	if err != nil {
		return nil, err
	}

	maxSlotID, err := sg.save.GetMaxSlotID()
	if err != nil {
		return nil, err
	}
	firstNewSlotID, newMaxSlotID, err := allocateSequentialSlotIDs(maxSlotID, len(expanded))
	if err != nil {
		return nil, fmt.Errorf("无法分配因子 SlotID: %w", err)
	}

	// 验证所有槽可写（需要找的 entry 必须存在）
	for i, item := range expanded {
		gemUnitID := emptySlots[i]
		gemIndex := gemUnitID - GemSlotBaseID
		primaryTraitUnit := TraitSlotBase + (gemIndex * 100)
		secondaryTraitUnit := primaryTraitUnit + 1

		// 验证必需的 entry 存在
		if _, ok := sg.save.findUnit(GemSlotIDType, uint32(gemUnitID)); !ok {
			return nil, fmt.Errorf("因子槽 %d 缺少 GEMDATA_SLOT_IDS", gemUnitID)
		}
		if _, ok := sg.save.findUnit(GemWornByIDType, uint32(gemUnitID)); !ok {
			return nil, fmt.Errorf("因子槽 %d 缺少 GEMDATA_WORN_BY", gemUnitID)
		}
		if _, ok := sg.save.findUnit(GemFlagsIDType, uint32(gemUnitID)); !ok {
			return nil, fmt.Errorf("因子槽 %d 缺少 GEMDATA_FLAGS", gemUnitID)
		}
		if _, ok := sg.save.findUnit(GemLevelIDType, uint32(gemUnitID)); !ok {
			return nil, fmt.Errorf("因子槽 %d 缺少 GEMDATA_SKILL_1_LEVEL", gemUnitID)
		}
		if _, ok := sg.save.findUnit(TraitHashIDType, uint32(primaryTraitUnit)); !ok {
			return nil, fmt.Errorf("因子槽 %d 缺少主特性哈希", gemUnitID)
		}
		if _, ok := sg.save.findUnit(TraitLevelIDType, uint32(primaryTraitUnit)); !ok {
			return nil, fmt.Errorf("因子槽 %d 缺少主特性等级", gemUnitID)
		}

		// PatchSigil writes the secondary trait unit whenever the sigil supports
		// a generated "+" secondary (regardless of whether one was chosen), so it
		// must be prechecked here too — otherwise a missing secondary unit only
		// surfaces mid-write, after the primary fields were already patched.
		if sigil, err := sg.catalog.RequireSigil(item.SigilID); err == nil && supportsGeneratedPlusSigil(sigil) {
			if _, ok := sg.save.findUnit(TraitHashIDType, uint32(secondaryTraitUnit)); !ok {
				return nil, fmt.Errorf("因子槽 %d 缺少副特性哈希", gemUnitID)
			}
			if _, ok := sg.save.findUnit(TraitLevelIDType, uint32(secondaryTraitUnit)); !ok {
				return nil, fmt.Errorf("因子槽 %d 缺少副特性等级", gemUnitID)
			}
		}
	}

	// 更新 max slot ID
	// Mutating SaveData is the beginning of the transaction, not its commit.
	// Keep an exact in-memory snapshot so a failed write or strict readback can
	// be retried without advancing to another empty slot and creating a duplicate.
	originalData := append([]byte(nil), sg.save.data...)
	originalQueue := append([]QueueItem(nil), sg.queue...)
	originalBackupPath := sg.save.lastBackupPath
	committed := false
	diskWritten := false
	var writtenData []byte
	defer func() {
		if committed {
			return
		}
		sg.save.data = originalData
		sg.save.lastBackupPath = originalBackupPath
		sg.queue = originalQueue
		if diskWritten {
			sg.retryAfterFailedCommit = &failedGeneratorCommit{outputPath: outputPath, written: writtenData}
		}
	}()

	if err := sg.save.SetMaxSlotID(newMaxSlotID); err != nil {
		return nil, err
	}

	// 写入每个因子
	created := 0
	for i, item := range expanded {
		gemUnitID := emptySlots[i]
		newSlotID := firstNewSlotID + i

		sigil, _ := sg.catalog.RequireSigil(item.SigilID)
		sigilHash, err := ParseHashHex(sigil.Hash)
		if err != nil {
			return nil, fmt.Errorf("%s 哈希无效: %s", sigil.DisplayName, sigil.Hash)
		}

		primaryTrait, _ := sg.catalog.RequireTrait(item.PrimaryTraitID)
		primaryHash, err := ParseHashHex(primaryTrait.Hash)
		if err != nil {
			return nil, fmt.Errorf("%s 哈希无效", primaryTrait.DisplayName)
		}

		secondaryHash := EmptyHash
		var secondaryLevel int
		hasSecondary := item.SecondaryTraitID != ""
		if hasSecondary {
			secondaryTrait, _ := sg.catalog.RequireTrait(item.SecondaryTraitID)
			secondaryHash, err = ParseHashHex(secondaryTrait.Hash)
			if err != nil {
				return nil, fmt.Errorf("%s 哈希无效", secondaryTrait.DisplayName)
			}
			secondaryLevel = item.SecondaryLevel
		}

		flags := uint32(NormalSigilFlags)
		if strings.EqualFold(item.SigilID, "GEEN_142_02") {
			flags = 22
		}
		if err := sg.save.PatchSigilWithFlags(gemUnitID, newSlotID, sigilHash, item.Level,
			primaryHash, item.PrimaryLevel,
			secondaryHash, secondaryLevel, hasSecondary, flags); err != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", item.SigilName, err)
		}
		created++
	}

	// 修复校验和
	if err := sg.save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("校验和修复失败: %w", err)
	}

	// 写入输出文件
	if err := sg.save.Write(outputPath); err != nil {
		return nil, fmt.Errorf("写入输出文件失败: %w", err)
	}
	diskWritten = true
	writtenData = append([]byte(nil), sg.save.data...)

	// 严格回读验证：加载失败或任一字段不符都必须向调用方返回错误。
	verifyLoader := sg.loadSaveForVerification
	if verifyLoader == nil {
		verifyLoader = LoadSave
	}
	verifySave, err := verifyLoader(outputPath)
	if err != nil {
		return nil, fmt.Errorf("因子已写入，但重新读取失败: %w", err)
	}
	verified := 0
	for i, item := range expanded {
		gemUnitID := emptySlots[i]
		expectedSlotID := uint32(firstNewSlotID + i)
		sigil, _ := sg.catalog.RequireSigil(item.SigilID)
		sigilHash, _ := ParseHashHex(sigil.Hash)
		primaryTrait, _ := sg.catalog.RequireTrait(item.PrimaryTraitID)
		primaryHash, _ := ParseHashHex(primaryTrait.Hash)

		secondaryHash := EmptyHash
		var secondaryLevel int
		hasSecondary := item.SecondaryTraitID != ""
		if hasSecondary {
			secondaryTrait, _ := sg.catalog.RequireTrait(item.SecondaryTraitID)
			secondaryHash, _ = ParseHashHex(secondaryTrait.Hash)
			secondaryLevel = item.SecondaryLevel
		}

		flags := uint32(NormalSigilFlags)
		if strings.EqualFold(item.SigilID, "GEEN_142_02") {
			flags = 22
		}
		if err := verifySave.VerifySigilWithFlags(gemUnitID, expectedSlotID, sigilHash, item.Level,
			primaryHash, item.PrimaryLevel,
			secondaryHash, secondaryLevel, hasSecondary, flags); err != nil {
			return nil, fmt.Errorf("因子已写入，但第 %d 个因子回读验证失败: %w", i+1, err)
		}
		verified++
	}
	if verified != created {
		return nil, fmt.Errorf("因子已写入，但回读验证数量不符: 已创建 %d，已验证 %d", created, verified)
	}

	absPath, _ := filepath.Abs(outputPath)
	createdSlotIDs := make([]uint32, created)
	for i := range createdSlotIDs {
		createdSlotIDs[i] = uint32(firstNewSlotID + i)
	}
	sg.queue = nil
	committed = true
	sg.retryAfterFailedCommit = nil
	return &ApplyResult{
		CreatedCount:  created,
		VerifiedCount: verified,
		OutputPath:    absPath,
		BackupPath:    sg.save.LastBackupPath(),
		SlotIDs:       createdSlotIDs,
	}, nil
}

// RemoveAllSigils 清除输出的存档中所有因子
func (sg *SigilGen) RemoveAllSigils(inputPath, outputPath string) (*ApplyResult, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()
	if err := ensureGeneratorWriteAllowed(outputPath); err != nil {
		return nil, err
	}
	s, err := LoadSave(inputPath)
	if err != nil {
		return nil, err
	}

	allGemUnits := s.findAllUnitsByType(GemIDType)
	removed := 0
	for _, u := range allGemUnits {
		if int(u.UnitID) >= GemSlotBaseID && u.Uint32() != EmptyHash {
			gemIndex := int(u.UnitID) - GemSlotBaseID
			primaryTraitUnit := uint32(TraitSlotBase + (gemIndex * 100))
			secondaryTraitUnit := primaryTraitUnit + 1

			s.tryPatchUint(GemIDType, u.UnitID, EmptyHash)
			s.tryPatchInt(GemLevelIDType, u.UnitID, 0)
			s.tryPatchUint(GemWornByIDType, u.UnitID, EmptyHash)
			s.tryPatchUint(GemFlagsIDType, u.UnitID, 0)
			s.tryPatchUint(TraitHashIDType, primaryTraitUnit, EmptyHash)
			s.tryPatchInt(TraitLevelIDType, primaryTraitUnit, 0)
			s.tryPatchUint(TraitHashIDType, secondaryTraitUnit, EmptyHash)
			s.tryPatchInt(TraitLevelIDType, secondaryTraitUnit, 0)
			removed++
		}
	}

	if err := s.FixChecksums(); err != nil {
		return nil, fmt.Errorf("校验和修复失败: %w", err)
	}
	if err := s.Write(outputPath); err != nil {
		return nil, fmt.Errorf("写入输出文件失败: %w", err)
	}

	verifySave, _ := LoadSave(outputPath)
	remaining := 0
	if verifySave != nil {
		remaining = verifySave.GetOccupiedGemCount()
	}

	absPath, _ := filepath.Abs(outputPath)
	return &ApplyResult{
		CreatedCount:  removed,
		VerifiedCount: remaining,
		OutputPath:    absPath,
	}, nil
}

// ── 已有因子查看/删除 ──

type ExistingSigil struct {
	GemUnitID          int    `json:"gemUnitId"`
	SigilName          string `json:"sigilName"`
	Level              int    `json:"level"`
	PrimaryTraitName   string `json:"primaryTraitName"`
	PrimaryLevel       int    `json:"primaryLevel"`
	SecondaryTraitName string `json:"secondaryTraitName"`
	SecondaryLevel     int    `json:"secondaryLevel"`
}

func (sg *SigilGen) DebugSave() (string, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.save == nil {
		return "no save loaded", nil
	}
	s := sg.save
	slot := s.slotSpan()

	// Count how many times the IDType 2703 (= 0xA8F = little endian 8F 0A 00 00) appears
	count2703 := 0
	for i := 0; i < len(slot)-4; i++ {
		if binary.LittleEndian.Uint32(slot[i:]) == GemIDType {
			count2703++
		}
	}

	info := fmt.Sprintf(
		"File: %d bytes | Slot off=%d size=%d (%d bytes)\n"+
			"First 40 slot bytes: %X\n"+
			"Last 40 slot bytes: %X\n"+
			"Occurrences of IDType 2703: %d\n"+
			"findAllUnitsByType(2703) returns: %d entries",
		len(s.data), s.slotOff, s.slotLen, len(slot),
		slot[:min(40, len(slot))],
		slot[max(0, len(slot)-40):],
		count2703,
		len(s.findAllUnitsByType(GemIDType)),
	)
	return info, nil
}

func (sg *SigilGen) GetExistingSigils() ([]ExistingSigil, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.save == nil {
		return nil, fmt.Errorf("请先加载存档")
	}
	if sg.catalog == nil {
		if err := sg.loadCatalogLocked(); err != nil {
			return nil, err
		}
	}

	allGemUnits := sg.save.findAllUnitsByType(GemIDType)
	totalScanned := len(allGemUnits)
	totalOccupied := 0
	var result []ExistingSigil
	for _, u := range allGemUnits {
		if int(u.UnitID) < GemSlotBaseID {
			continue
		}
		totalOccupied++
		if u.Uint32() == EmptyHash {
			continue
		}

		// 限制返回数量，避免渲染卡死
		if len(result) >= 500 {
			continue
		}

		gemUnitID := int(u.UnitID)
		gemIndex := gemUnitID - GemSlotBaseID
		primaryTraitUnit := uint32(TraitSlotBase + (gemIndex * 100))
		secondaryTraitUnit := primaryTraitUnit + 1

		es := ExistingSigil{GemUnitID: gemUnitID, SigilName: fmt.Sprintf("0x%08X", u.Uint32())}

		hash := u.Uint32()
		if sigil := sg.catalog.LookupSigilByHash(hash); sigil != nil {
			es.SigilName = cnName(sigil.DisplayName)
		} else if name := localizedRuntimeName(hash); name != "" {
			es.SigilName = name
		}

		if lv, ok := sg.save.findUnitExact(GemLevelIDType, u.UnitID); ok {
			es.Level = int(lv.Int32())
		}

		if ph, primaryLevel := readSigilTraitUnit(sg.save, primaryTraitUnit); ph != 0 {
			if trait := sg.catalog.LookupTraitByHash(ph); trait != nil {
				es.PrimaryTraitName = cnTrait(trait.DisplayName)
			} else if name := localizedRuntimeName(ph); name != "" {
				es.PrimaryTraitName = name
			}
			es.PrimaryLevel = primaryLevel
		}

		if sh, secondaryLevel := readSigilTraitUnit(sg.save, secondaryTraitUnit); sh != 0 {
			if sh != EmptyHash {
				if trait := sg.catalog.LookupTraitByHash(sh); trait != nil {
					es.SecondaryTraitName = cnTrait(trait.DisplayName)
				} else if name := localizedRuntimeName(sh); name != "" {
					es.SecondaryTraitName = name
				} else {
					es.SecondaryTraitName = fmt.Sprintf("0x%08X", sh)
				}
				es.SecondaryLevel = secondaryLevel
			}
		}

		result = append(result, es)
	}

	// 如果没有可识别的数据，返回诊断信息
	if len(result) == 0 && totalScanned == 0 {
		return nil, fmt.Errorf("存档扫描未发现任何因子数据 (扫描了 %d 条 Entry)", totalScanned)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("槽位 %d 个，全部为空", totalOccupied)
	}

	// 如果数量超过限制，在前面插入提示
	if len(result) >= 500 {
		result = append([]ExistingSigil{{
			GemUnitID: -1,
			SigilName: fmt.Sprintf("[共 %d 个因子，仅显示前 500 个]", len(result)),
		}}, result...)
	}
	return result, nil
}

// DeleteSelectedSigils 删除选中的因子并写入输出文件
func (sg *SigilGen) DeleteSelectedSigils(gemUnitIDs []int, outputPath string) (*ApplyResult, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()
	if err := ensureGeneratorWriteAllowed(outputPath); err != nil {
		return nil, err
	}
	if sg.save == nil {
		return nil, fmt.Errorf("请先加载存档")
	}
	if len(gemUnitIDs) == 0 {
		return nil, fmt.Errorf("未选择要删除的因子")
	}

	// 重新加载存档（避免影响之前的修改）
	s, err := LoadSave(sg.savePath)
	if err != nil {
		return nil, err
	}

	removed := 0
	for _, gemUnitID := range gemUnitIDs {
		entry, ok := s.findUnit(GemIDType, uint32(gemUnitID))
		if !ok || entry.Uint32() == EmptyHash {
			continue
		}

		gemIndex := gemUnitID - GemSlotBaseID
		primaryTraitUnit := uint32(TraitSlotBase + (gemIndex * 100))
		secondaryTraitUnit := primaryTraitUnit + 1

		s.tryPatchUint(GemIDType, uint32(gemUnitID), EmptyHash)
		s.tryPatchInt(GemLevelIDType, uint32(gemUnitID), 0)
		s.tryPatchUint(GemWornByIDType, uint32(gemUnitID), EmptyHash)
		s.tryPatchUint(GemFlagsIDType, uint32(gemUnitID), 0)
		s.tryPatchUint(TraitHashIDType, primaryTraitUnit, EmptyHash)
		s.tryPatchInt(TraitLevelIDType, primaryTraitUnit, 0)
		s.tryPatchUint(TraitHashIDType, secondaryTraitUnit, EmptyHash)
		s.tryPatchInt(TraitLevelIDType, secondaryTraitUnit, 0)
		removed++
	}

	if err := s.FixChecksums(); err != nil {
		return nil, fmt.Errorf("校验和修复失败: %w", err)
	}
	if err := s.Write(outputPath); err != nil {
		return nil, fmt.Errorf("写入输出文件失败: %w", err)
	}

	absPath, _ := filepath.Abs(outputPath)
	return &ApplyResult{
		CreatedCount:  removed,
		VerifiedCount: 0,
		OutputPath:    absPath,
	}, nil
}

// ── 辅助函数 ──

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}
