package backend

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ── 配装写入（LoadoutApply）────────────────────────────────────────────
//
// 安全优先的预设槽写入：把用户从「该存档已有资源」里拼出的一套配装原地写入
// 指定预设槽（UnitID 20000..20614）。构造模式会在同一次事务中生成并绑定目标
// 因子；目录与天然生成规则只作提示，不会替用户否决可编码的选择。资源归属、
// 目标快照、容器边界和事务完整性仍严格验证；普通编辑不会生成角色成长，
// v3 单套导入则会显式携带并回读角色成长与同角色武器收集状态。
//
// 字段填充语义（实测，务必区分）：
//   3002 名称  Byte 表，64 字节，UTF-8 + NUL 填充（走 SetBytes，绝不能按 uint32 步长写）
//   1402 武器  1 值 = 武器 SlotID（2802）
//   1403 因子  13 值，前≤12 = 因子 SlotID（2702），其余填 0（**不是** EmptyHash），第 13 位恒 0
//   1404 技能  4 值，≤4 技能 hash，其余填 EmptyHash
//   3005 武器技能 5 值，严格保留位置；通常取所选武器 2818 的五槽快照
//   3007 专精  50 值，≤50 节点 hash，其余填 EmptyHash
//   3003 角色  1 值 = 角色 hash

const (
	loadoutNameMaxBytes = 63 // 3002 缓冲 64 字节，留 1 字节 NUL
	loadoutMaxSigils    = 12
	loadoutMaxSkills    = 4
	loadoutMaxMastery   = 50
)

// LoadoutWrite 是一次预设槽写入请求。
type LoadoutWrite struct {
	UnitID            uint32                    `json:"unitId"`                      // 目标预设槽 20000..20614
	ExpectCharaHash   string                    `json:"expectCharaHash"`             // 前端认定的槽位归属角色（8 位 hex），写前与块内实测比对
	Op                string                    `json:"op"`                          // "write" | "clone" | "clear"
	Name              string                    `json:"name"`                        // 配装名称 UTF-8，≤63 字节
	WeaponSlotID      uint32                    `json:"weaponSlotId"`                // 1402
	SigilSlotIDs      []uint32                  `json:"sigilSlotIds"`                // 1403，≤12
	SkillHashes       []string                  `json:"skillHashes"`                 // 1404，≤4，8 位 hex
	WeaponSkillHashes []string                  `json:"weaponSkillHashes,omitempty"` // 3005，恰好 5 个位置敏感值
	MasteryHashes     []string                  `json:"masteryHashes"`               // 3007，≤50，8 位 hex
	SummonSlotIDs     []uint32                  `json:"summonSlotIds,omitempty"`     // 可选的全局 1451 四召唤石配置；仅 Op=="write" 生效
	ConstructedSigils []LoadoutConstructedSigil `json:"constructedSigils,omitempty"` // 写入时原子创建并替换 0 基因子槽
	CloneFromUnitID   uint32                    `json:"cloneFromUnitId"`             // Op=="clone" 时的源槽
}

// LoadoutConstructedSigil describes one factor draft that must be created and
// bound as part of the same LoadoutApply transaction. Index is 0 based.
type LoadoutConstructedSigil struct {
	Index                   int       `json:"index"`
	TemplateSlotID          uint32    `json:"templateSlotId,omitempty"`
	ExactSigilHash          string    `json:"exactSigilHash,omitempty"`
	ExactPrimaryTraitHash   string    `json:"exactPrimaryTraitHash,omitempty"`
	ExactSecondaryTraitHash string    `json:"exactSecondaryTraitHash,omitempty"`
	Item                    QueueItem `json:"item"`
}

// LoadoutApplyResult 汇报写入结果。
type LoadoutApplyResult struct {
	OutputPath         string   `json:"outputPath"`
	BackupPath         string   `json:"backupPath"`
	SlotsWritten       int      `json:"slotsWritten"`
	SlotsCleared       int      `json:"slotsCleared"`
	VerifiedFields     int      `json:"verifiedFields"` // 回读后逐字段命中的数量
	CreatedCount       int      `json:"createdCount"`
	CreatedWeaponCount int      `json:"createdWeaponCount"`
	CreatedSummonCount int      `json:"createdSummonCount"`
	VerifiedCount      int      `json:"verifiedCount"`
	SlotIDs            []uint32 `json:"slotIds,omitempty"`
}

// ── 只读编辑上下文：给前端一份「该角色可安全引用的资源池」──────────────

type LoadoutSlotInfo struct {
	UnitID   uint32 `json:"unitId"`
	Slot     int    `json:"slot"`
	Occupied bool   `json:"occupied"`
	Name     string `json:"name"`
}

type LoadoutPickWeapon struct {
	SlotID    uint32 `json:"slotId"`
	Hash      string `json:"hash"`
	Name      string `json:"name"`
	OwnerCode string `json:"ownerCode"` // 空 = 通用武器
}

type LoadoutPickSigil struct {
	SlotID              uint32 `json:"slotId"`
	Hash                string `json:"hash"`
	Name                string `json:"name"`
	Level               int    `json:"level"`
	PrimaryTraitHash    string `json:"primaryTraitHash"`
	PrimaryTraitName    string `json:"primaryTraitName"`
	PrimaryTraitLevel   int    `json:"primaryTraitLevel"`
	SecondaryTraitHash  string `json:"secondaryTraitHash"`
	SecondaryTraitName  string `json:"secondaryTraitName"`
	SecondaryTraitLevel int    `json:"secondaryTraitLevel"`
	Generic             bool   `json:"generic"` // true = 非角色因子（任意角色可装）
}

type LoadoutPickSkill struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}

type LoadoutMasterySource struct {
	UnitID     uint32   `json:"unitId"`
	Slot       int      `json:"slot"`
	Name       string   `json:"name"`
	NodeCount  int      `json:"nodeCount"`
	NodeHashes []string `json:"nodeHashes"`
}

type LoadoutEditContext struct {
	CharaHash      string                 `json:"charaHash"`
	CharaName      string                 `json:"charaName"`
	OwnerCode      string                 `json:"ownerCode"` // 推导出的 PLxxxx，空=无法确定
	BlockBase      uint32                 `json:"blockBase"`
	Slots          []LoadoutSlotInfo      `json:"slots"`
	Weapons        []LoadoutPickWeapon    `json:"weapons"`
	Sigils         []LoadoutPickSigil     `json:"sigils"`
	Skills         []LoadoutPickSkill     `json:"skills"`
	MasterySources []LoadoutMasterySource `json:"masterySources"`
}

var loadoutFindProcessByName = findProcessByName

// LoadoutConstructSigil creates one catalog-validated sigil in the save that is
// currently open in the loadout editor. A fresh generator is used deliberately:
// the editor must never consume or mutate the queue owned by the standalone
// sigil generator screen. SaveData.Write performs an atomic in-place write,
// creates a timestamped backup, fixes checksums and is verified by ApplyQueue.
func (a *App) LoadoutConstructSigil(path string, item QueueItem) (*ApplyResult, error) {
	if path == "" {
		return nil, fmt.Errorf("存档路径不能为空")
	}
	if _, err := loadoutFindProcessByName(charaProcessName); err == nil {
		return nil, fmt.Errorf("写入存档前请先完全退出游戏，避免游戏把旧数据写回")
	}
	item.Quantity = 1
	gen := NewSigilGen()
	if _, err := gen.LoadSaveFile(path); err != nil {
		return nil, fmt.Errorf("读取配装存档失败: %w", err)
	}
	if err := gen.AddToQueue(item); err != nil {
		return nil, fmt.Errorf("因子配置不合法: %w", err)
	}
	result, err := gen.ApplyQueue(path)
	if err != nil {
		return nil, fmt.Errorf("创建配装因子失败: %w", err)
	}
	return result, nil
}

// ── 存档索引：一次解析、读写共用（"所见即所写"）────────────────────────

type loadoutIndex struct {
	gemBySlotID map[uint32]uint32     // 因子 SlotID -> 因子槽 UnitID（仅非空因子）
	gemHash     map[uint32]*unitEntry // 因子槽 UnitID -> 2703
	gemLevel    map[uint32]*unitEntry // 因子槽 UnitID -> 2704
	wepBySlotID map[uint32]uint32     // 武器 SlotID -> 武器槽 UnitID
	wepHash     map[uint32]*unitEntry // 武器槽 UnitID -> 2803
	charName    map[uint32]string     // 角色 hash -> 名字
}

func buildLoadoutIndex(save *SaveData) *loadoutIndex {
	ix := &loadoutIndex{
		gemBySlotID: map[uint32]uint32{},
		wepBySlotID: map[uint32]uint32{},
		charName:    map[uint32]string{},
	}
	for hash, name := range characterNameByHash {
		ix.charName[hash] = name
	}
	ix.gemHash = entriesByUnitID(save.findAllUnitsByType(GemIDType))
	ix.gemLevel = entriesByUnitID(save.findAllUnitsByType(GemLevelIDType))
	for _, e := range save.findAllUnitsByType(gemSlotIDType) {
		if h := ix.gemHash[e.UnitID]; h == nil || h.Uint32() == EmptyHash || h.Uint32() == 0 {
			continue // 跳过已清空的因子记录，避免悬空引用命中空记录
		}
		ix.gemBySlotID[e.Uint32()] = e.UnitID
	}
	ix.wepHash = entriesByUnitID(save.findAllUnitsByType(weaponIDType))
	for _, e := range save.findAllUnitsByType(weaponSlotIDType) {
		ix.wepBySlotID[e.Uint32()] = e.UnitID
	}
	return ix
}

// resolveBlockChara 由目标槽所在的 15 槽块推断归属角色（读块内占用槽的 3003）。
// 块全空（无法确定角色）或多个非空 3003 互相冲突 → ok=false。
func resolveBlockChara(save *SaveData, unitID uint32) (charaHash uint32, ok bool) {
	if unitID < loadoutBase || unitID >= partyLoadoutBase {
		return 0, false
	}
	blockBase := loadoutBase + ((unitID-loadoutBase)/loadoutSlotsPerChara)*loadoutSlotsPerChara
	var found uint32
	for u := blockBase; u < blockBase+loadoutSlotsPerChara; u++ {
		e, exists := save.findUnitExact(loadoutCharIDType, u)
		if !exists {
			continue
		}
		v := e.Uint32()
		if v == EmptyHash || v == 0 {
			continue
		}
		if found == 0 {
			found = v
		} else if found != v {
			return 0, false // 同块出现两个不同角色 = 数据异常，拒绝
		}
	}
	if found == 0 {
		return 0, false
	}
	return found, true
}

// deriveOwnerCode 推导角色的 PLxxxx：从该角色现有配装的专精节点 char（权威）
// 与武器 ownerCode 双源交叉。两源须一致；无法确定时返回空串（=只放行通用武器）。
func (ix *loadoutIndex) deriveOwnerCode(save *SaveData, charaHash uint32) string {
	fromMastery := ""
	fromWeapon := ""
	for _, ce := range save.findAllUnitsByType(loadoutCharIDType) {
		if ce.Uint32() != charaHash || ce.UnitID >= partyLoadoutBase {
			continue
		}
		u := ce.UnitID
		// 专精节点 -> char
		if fromMastery == "" {
			if me, ok := save.findUnitExact(loadoutMasteryIDType, u); ok {
				for i, n := 0, vecLen(me); i < n; i++ {
					v, err := me.Uint32At(i)
					if err != nil {
						break
					}
					if v == EmptyHash || v == 0 {
						continue
					}
					if node, ok := skillboardNodeForHash(v); ok && node.Char != "" {
						fromMastery = node.Char
						break
					}
				}
			}
		}
		// 武器 ownerCode
		if fromWeapon == "" {
			if we, ok := save.findUnitExact(loadoutWeaponIDType, u); ok {
				if wu, ok := ix.wepBySlotID[we.Uint32()]; ok {
					if h := ix.wepHash[wu]; h != nil {
						if def, ok := progressionWeaponDefForHash(h.Uint32()); ok && def.OwnerCode != "" {
							fromWeapon = def.OwnerCode
						}
					}
				}
			}
		}
		if fromMastery != "" && fromWeapon != "" {
			break
		}
	}
	switch {
	case fromMastery != "" && fromWeapon != "":
		if fromMastery == fromWeapon {
			return fromMastery
		}
		return "" // 两源冲突，保守起见不确定
	case fromMastery != "":
		return fromMastery
	case fromWeapon != "":
		return fromWeapon
	default:
		return ""
	}
}

// charaPrecedent 收集该角色「游戏已亲自接受过」的因子/技能 hash 白名单：
// 凡出现在该角色任一现有配装（20000+）或实时装备（10000+，同 charaHash）中的，
// 都视为合法可再引用。用于对角色专属因子/技能做安全校验（无 hash→角色映射时的替代）。
func (ix *loadoutIndex) charaPrecedent(save *SaveData, charaHash uint32) (sigils, skills map[uint32]bool) {
	sigils = map[uint32]bool{}
	skills = map[uint32]bool{}
	for _, ce := range save.findAllUnitsByType(loadoutCharIDType) {
		if ce.Uint32() != charaHash {
			continue
		}
		u := ce.UnitID
		if se, ok := save.findUnitExact(loadoutSigilsIDType, u); ok {
			for i, n := 0, vecLen(se); i < n; i++ {
				sid, err := se.Uint32At(i)
				if err != nil {
					break
				}
				if gu, ok := ix.gemBySlotID[sid]; ok {
					if h := ix.gemHash[gu]; h != nil {
						sigils[h.Uint32()] = true
					}
				}
			}
		}
		if ke, ok := save.findUnitExact(loadoutSkillsIDType, u); ok {
			for i, n := 0, vecLen(ke); i < n; i++ {
				v, err := ke.Uint32At(i)
				if err != nil {
					break
				}
				if v != EmptyHash && v != 0 {
					skills[v] = true
				}
			}
		}
	}
	return sigils, skills
}

// ── 字段写入原语 ──────────────────────────────────────────────────────

// writeLoadoutName 写 3002 名称：断言 ValueCnt==64，UTF-8 编码 + NUL 填满 64 字节。
func (s *SaveData) writeLoadoutName(unitID uint32, name string) error {
	e, ok := s.findUnitExact(loadoutNameIDType, unitID)
	if !ok {
		return fmt.Errorf("找不到配装名称字段 3002 @%d", unitID)
	}
	if e.ValueCnt != 64 {
		return fmt.Errorf("配装名称字段 ValueCnt=%d，期望 64", e.ValueCnt)
	}
	buf := []byte(name)
	if len(buf) > loadoutNameMaxBytes {
		return fmt.Errorf("配装名称 %d 字节超过上限 %d（约 21 个汉字）", len(buf), loadoutNameMaxBytes)
	}
	return e.SetBytes(buf) // SetBytes 会先清零整块 64 字节
}

// writeLoadoutVector 写定长 uint32 向量：断言 ValueCnt==wantCnt，前 len(vals) 写值、其余补 pad。
func (s *SaveData) writeLoadoutVector(idType, unitID uint32, vals []uint32, wantCnt int, pad uint32) error {
	e, ok := s.findUnitExact(idType, unitID)
	if !ok {
		return fmt.Errorf("找不到字段 IDType=%d @%d", idType, unitID)
	}
	if e.ValueCnt != wantCnt {
		return fmt.Errorf("字段 IDType=%d @%d ValueCnt=%d，期望 %d", idType, unitID, e.ValueCnt, wantCnt)
	}
	if len(vals) > wantCnt {
		return fmt.Errorf("字段 IDType=%d 写入 %d 个值超过容量 %d", idType, len(vals), wantCnt)
	}
	for i := 0; i < wantCnt; i++ {
		v := pad
		if i < len(vals) {
			v = vals[i]
		}
		if err := e.SetUint32At(i, v); err != nil {
			return err
		}
	}
	return nil
}

// ── 校验 + 落盘编排 ──────────────────────────────────────────────────

// resolvedWrite 是一条 LoadoutWrite 经完整预校验后的、可直接落盘的结果。
type resolvedWrite struct {
	unitID       uint32
	op           string
	charaHash    uint32   // 写入 3003 的实测推导值
	name         string   // Op=="write"
	weaponSID    uint32   // 1402
	sigilSIDs    []uint32 // 1403（≤12）
	skills       []uint32 // 1404（≤4）
	weaponSkills []uint32 // 3005（严格 5 槽）
	mastery      []uint32 // 3007（≤50）
	keepWeapon   bool     // clear 时保持 1402 原值
	constructed  []*preparedLoadoutSigil
}

type preparedLoadoutSigil struct {
	index          int
	item           QueueItem
	owner          *resolvedWrite
	gemUnitID      int
	newSlotID      uint32
	sigilHash      uint32
	primaryHash    uint32
	secondaryHash  uint32
	secondaryLevel int
	hasSecondary   bool
	flags          uint32
}

func containsNaturalLevel(levels []int, selected int) bool {
	for _, level := range levels {
		if level == selected {
			return true
		}
	}
	return false
}

func hasExactLoadoutSigilSource(draft LoadoutConstructedSigil) bool {
	return strings.TrimSpace(draft.ExactSigilHash) != "" ||
		strings.TrimSpace(draft.ExactPrimaryTraitHash) != "" ||
		strings.TrimSpace(draft.ExactSecondaryTraitHash) != ""
}

func parseExactLoadoutHash(value, field string) (uint32, error) {
	hash, err := ParseHashHex(value)
	if err != nil {
		return 0, fmt.Errorf("%s无效: %w", field, err)
	}
	if hash == 0 || hash == EmptyHash {
		return 0, fmt.Errorf("%s不能是空值", field)
	}
	return hash, nil
}

func exactLoadoutTransportHashID(value string) (string, bool) {
	sigilID := strings.TrimSpace(value)
	if len(sigilID) != 13 || !strings.EqualFold(sigilID[:5], "HASH_") {
		return "", false
	}
	for _, char := range sigilID[5:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return "", false
		}
	}
	return strings.ToUpper(sigilID[5:]), true
}

func recoverTransportExactLoadoutSigil(cat *Catalog, draft LoadoutConstructedSigil) (LoadoutConstructedSigil, bool, error) {
	sigilID := strings.TrimSpace(draft.Item.SigilID)
	if len(sigilID) < 5 || !strings.EqualFold(sigilID[:5], "HASH_") {
		return draft, false, nil
	}
	sigilHash, valid := exactLoadoutTransportHashID(sigilID)
	if !valid {
		return draft, false, fmt.Errorf("精确因子 ID 格式无效: %s", sigilID)
	}

	item := draft.Item
	item.SigilID = sigilID
	item.PrimaryTraitID = strings.TrimSpace(item.PrimaryTraitID)
	item.SecondaryTraitID = strings.TrimSpace(item.SecondaryTraitID)
	primary, err := cat.RequireTrait(item.PrimaryTraitID)
	if err != nil {
		return draft, false, fmt.Errorf("恢复精确因子 %s 的主词条失败: %w", sigilID, err)
	}
	primaryHash, err := ParseHashHex(primary.Hash)
	if err != nil {
		return draft, false, fmt.Errorf("恢复精确因子 %s 的主词条哈希失败: %w", sigilID, err)
	}

	draft.Item = item
	draft.ExactSigilHash = sigilHash
	draft.ExactPrimaryTraitHash = fmt.Sprintf("%08X", primaryHash)
	if item.SecondaryTraitID == "" {
		return draft, true, nil
	}
	secondary, err := cat.RequireTrait(item.SecondaryTraitID)
	if err != nil {
		return draft, false, fmt.Errorf("恢复精确因子 %s 的副词条失败: %w", sigilID, err)
	}
	secondaryHash, err := ParseHashHex(secondary.Hash)
	if err != nil {
		return draft, false, fmt.Errorf("恢复精确因子 %s 的副词条哈希失败: %w", sigilID, err)
	}
	draft.ExactSecondaryTraitHash = fmt.Sprintf("%08X", secondaryHash)
	return draft, true, nil
}

func prepareExactLoadoutSigil(cat *Catalog, draft LoadoutConstructedSigil) (*preparedLoadoutSigil, error) {
	if draft.Index < 0 || draft.Index >= loadoutMaxSigils {
		return nil, fmt.Errorf("构造因子槽位索引 %d 越界（应为 0..%d）", draft.Index, loadoutMaxSigils-1)
	}
	sigilHash, err := parseExactLoadoutHash(draft.ExactSigilHash, "精确因子哈希")
	if err != nil {
		return nil, err
	}
	primaryHash, err := parseExactLoadoutHash(draft.ExactPrimaryTraitHash, "精确主词条哈希")
	if err != nil {
		return nil, err
	}
	item := draft.Item
	item.Quantity = 1
	if item.Level <= 0 || item.PrimaryLevel <= 0 {
		return nil, fmt.Errorf("精确因子和主词条等级必须大于 0")
	}
	if item.SigilID == "" {
		item.SigilID = fmt.Sprintf("HASH_%08X", sigilHash)
	}
	if item.SigilName == "" {
		item.SigilName = sigilDisplayNameOr(sigilHash)
	}
	flags := uint32(NormalSigilFlags)
	if sigil := cat.LookupSigilByHash(sigilHash); sigil != nil && strings.EqualFold(sigil.InternalID, "GEEN_142_02") {
		flags = 22
	}
	prepared := &preparedLoadoutSigil{
		index: draft.Index, item: item, sigilHash: sigilHash,
		primaryHash: primaryHash, secondaryHash: EmptyHash, flags: flags,
	}
	secondaryText := strings.TrimSpace(draft.ExactSecondaryTraitHash)
	if secondaryText == "" {
		if item.SecondaryLevel != 0 {
			return nil, fmt.Errorf("精确副词条为空时副词条等级必须为 0")
		}
		prepared.item.SecondaryLevel = 0
		return prepared, nil
	}
	secondaryHash, err := parseExactLoadoutHash(secondaryText, "精确副词条哈希")
	if err != nil {
		return nil, err
	}
	if item.SecondaryLevel <= 0 {
		return nil, fmt.Errorf("精确副词条等级必须大于 0")
	}
	prepared.secondaryHash = secondaryHash
	prepared.secondaryLevel = item.SecondaryLevel
	prepared.hasSecondary = true
	return prepared, nil
}

func prepareLoadoutSigil(cat *Catalog, draft LoadoutConstructedSigil) (*preparedLoadoutSigil, error) {
	if hasExactLoadoutSigilSource(draft) {
		return prepareExactLoadoutSigil(cat, draft)
	}
	recovered, ok, err := recoverTransportExactLoadoutSigil(cat, draft)
	if err != nil {
		return nil, err
	}
	if ok {
		return prepareExactLoadoutSigil(cat, recovered)
	}
	if draft.Index < 0 || draft.Index >= loadoutMaxSigils {
		return nil, fmt.Errorf("构造因子槽位索引 %d 越界（应为 0..%d）", draft.Index, loadoutMaxSigils-1)
	}
	item := draft.Item
	item.Quantity = 1
	normalized, report, err := (&SigilGen{catalog: cat}).normalizeQueueItem(item)
	if err != nil {
		return nil, err
	}
	if !report.Writable {
		return nil, fmt.Errorf("%s", report.Message)
	}
	sigil, err := cat.RequireSigil(normalized.SigilID)
	if err != nil {
		return nil, err
	}
	primary, err := cat.RequireTrait(normalized.PrimaryTraitID)
	if err != nil {
		return nil, err
	}
	sigilHash, err := ParseHashHex(sigil.Hash)
	if err != nil {
		return nil, fmt.Errorf("因子「%s」哈希无效: %w", displaySigilName(sigil), err)
	}
	primaryHash, err := ParseHashHex(primary.Hash)
	if err != nil {
		return nil, fmt.Errorf("主词条「%s」哈希无效: %w", cnTrait(primary.DisplayName), err)
	}
	flags := uint32(NormalSigilFlags)
	if strings.EqualFold(normalized.SigilID, "GEEN_142_02") {
		flags = 22
	}
	prepared := &preparedLoadoutSigil{
		index: draft.Index, item: normalized, sigilHash: sigilHash,
		primaryHash: primaryHash, secondaryHash: EmptyHash,
		hasSecondary: normalized.SecondaryTraitID != "", flags: flags,
	}
	if !prepared.hasSecondary {
		prepared.item.SecondaryLevel = 0
		return prepared, nil
	}
	secondary, err := cat.RequireTrait(normalized.SecondaryTraitID)
	if err != nil {
		return nil, err
	}
	secondaryHash, err := ParseHashHex(secondary.Hash)
	if err != nil {
		return nil, fmt.Errorf("副词条「%s」哈希无效: %w", cnTrait(secondary.DisplayName), err)
	}
	prepared.secondaryHash = secondaryHash
	prepared.secondaryLevel = normalized.SecondaryLevel
	return prepared, nil
}

// prepareLoadoutSigilNatural 保留自然目录判定，仅用于生成可读警告；
// 是否能够编码和写入由 prepareLoadoutSigil 单独决定。
func prepareLoadoutSigilNatural(cat *Catalog, draft LoadoutConstructedSigil) (*preparedLoadoutSigil, error) {
	if draft.Index < 0 || draft.Index >= loadoutMaxSigils {
		return nil, fmt.Errorf("构造因子槽位索引 %d 越界（应为 0..%d）", draft.Index, loadoutMaxSigils-1)
	}
	item := draft.Item
	if strings.EqualFold(item.SigilID, "GEEN_142_02") {
		return nil, fmt.Errorf("因子 GEEN_142_02 是已验证的 Seven Net 商店特典，真实记录使用特殊 flags=22；普通构造器只写 flags=2，拒绝伪造")
	}
	sigil, err := cat.RequireSigil(item.SigilID)
	if err != nil {
		return nil, err
	}
	if !cat.IsSigilConstructible(sigil) {
		return nil, fmt.Errorf("因子 %s 缺少可信的自然生成与兼容性依据，拒绝构造", sigil.InternalID)
	}
	sigilLevels, err := cat.RequireSigilLevels(sigil)
	if err != nil {
		return nil, err
	}
	if item.Level < 1 || item.Level > 15 {
		return nil, fmt.Errorf("因子「%s」等级 %d 超出自然范围 1..15", displaySigilName(sigil), item.Level)
	}
	if !containsNaturalLevel(sigilLevels, item.Level) {
		return nil, fmt.Errorf("因子「%s」等级 %d 不在自然目录等级中", displaySigilName(sigil), item.Level)
	}
	primary, err := cat.RequireTrait(sigil.PrimaryTraitID)
	if err != nil {
		return nil, err
	}
	primaryLevels, err := cat.RequirePrimaryTraitLevels(sigil)
	if err != nil {
		return nil, err
	}
	if item.PrimaryLevel < 1 || item.PrimaryLevel > 15 {
		return nil, fmt.Errorf("因子「%s」主词条等级 %d 超出自然范围 1..15", displaySigilName(sigil), item.PrimaryLevel)
	}
	if !containsNaturalLevel(primaryLevels, item.PrimaryLevel) {
		return nil, fmt.Errorf("因子「%s」主词条等级 %d 不在自然目录等级中", displaySigilName(sigil), item.PrimaryLevel)
	}
	sigilHash, err := ParseHashHex(sigil.Hash)
	if err != nil {
		return nil, fmt.Errorf("因子「%s」哈希无效: %w", displaySigilName(sigil), err)
	}
	primaryHash, err := ParseHashHex(primary.Hash)
	if err != nil {
		return nil, fmt.Errorf("主词条「%s」哈希无效: %w", cnTrait(primary.DisplayName), err)
	}

	item.Quantity = 1
	item.SigilName = displaySigilName(sigil)
	item.PrimaryTraitID = primary.InternalID
	item.PrimaryTraitName = cnTrait(primary.DisplayName)
	prepared := &preparedLoadoutSigil{
		index: draft.Index, item: item, sigilHash: sigilHash,
		primaryHash: primaryHash, secondaryHash: EmptyHash,
		hasSecondary: supportsGeneratedPlusSigil(sigil), flags: NormalSigilFlags,
	}
	if item.SecondaryTraitID == "" {
		if requiresCharacterSigilSecondary(sigil) {
			return nil, fmt.Errorf("角色因子「%s」必须使用本地 2.0.2 gem/lot 白名单中的副词条，不能留空", item.SigilName)
		}
		if item.SecondaryLevel != 0 {
			return nil, fmt.Errorf("未选择副词条时副词条等级必须为 0")
		}
		prepared.item.SecondaryLevel = 0
		return prepared, nil
	}
	if !prepared.hasSecondary {
		return nil, fmt.Errorf("因子「%s」没有副词条槽", item.SigilName)
	}
	secondary, err := cat.RequireTrait(item.SecondaryTraitID)
	if err != nil {
		return nil, err
	}
	if secondary.InternalID == primary.InternalID {
		return nil, fmt.Errorf("因子主副词条不能同为「%s」", cnTrait(primary.DisplayName))
	}
	explicitlyCompatible := false
	for _, traitID := range sigil.AllowedSecondaryTraitIDs {
		if traitID == secondary.InternalID {
			explicitlyCompatible = true
			break
		}
	}
	if !explicitlyCompatible {
		return nil, fmt.Errorf("因子「%s」没有把副词条「%s」列入已验证的兼容白名单", item.SigilName, cnTrait(secondary.DisplayName))
	}
	allowed, err := cat.GetAllowedSecondaryTraits(sigil)
	if err != nil {
		return nil, err
	}
	compatible := false
	for _, candidate := range allowed {
		if candidate.InternalID == secondary.InternalID {
			compatible = true
			break
		}
	}
	if !compatible {
		return nil, fmt.Errorf("副词条「%s」不是因子「%s」的自然兼容词条", cnTrait(secondary.DisplayName), item.SigilName)
	}
	secondaryLevels, err := cat.RequireSecondaryTraitLevels(sigil, secondary)
	if err != nil {
		return nil, err
	}
	if item.SecondaryLevel < 1 || item.SecondaryLevel > 15 {
		return nil, fmt.Errorf("副词条「%s」等级 %d 超出自然范围 1..15", cnTrait(secondary.DisplayName), item.SecondaryLevel)
	}
	if !containsNaturalLevel(secondaryLevels, item.SecondaryLevel) {
		return nil, fmt.Errorf("副词条「%s」等级 %d 不在自然目录等级中", cnTrait(secondary.DisplayName), item.SecondaryLevel)
	}
	secondaryHash, err := ParseHashHex(secondary.Hash)
	if err != nil {
		return nil, fmt.Errorf("副词条「%s」哈希无效: %w", cnTrait(secondary.DisplayName), err)
	}
	prepared.secondaryHash = secondaryHash
	prepared.secondaryLevel = item.SecondaryLevel
	prepared.item.SecondaryTraitName = cnTrait(secondary.DisplayName)
	return prepared, nil
}

func prepareLoadoutSigilForSave(save *SaveData, ix *loadoutIndex, cat *Catalog, draft LoadoutConstructedSigil) (*preparedLoadoutSigil, error) {
	if hasExactLoadoutSigilSource(draft) {
		return prepareExactLoadoutSigil(cat, draft)
	}
	recovered, ok, err := recoverTransportExactLoadoutSigil(cat, draft)
	if err != nil {
		return nil, err
	}
	if ok {
		return prepareExactLoadoutSigil(cat, recovered)
	}
	if draft.TemplateSlotID == 0 {
		return prepareLoadoutSigil(cat, draft)
	}
	if draft.Index < 0 || draft.Index >= loadoutMaxSigils {
		return nil, fmt.Errorf("构造因子槽位索引 %d 越界（应为 0..%d）", draft.Index, loadoutMaxSigils-1)
	}
	sourceUnit, ok := ix.gemBySlotID[draft.TemplateSlotID]
	if !ok {
		return nil, fmt.Errorf("真实存档模板 SlotID %d 不存在或已经为空", draft.TemplateSlotID)
	}
	readUint := func(idType uint32, unitID uint32, name string) (uint32, error) {
		entry, exists := save.findUnitExact(idType, unitID)
		if !exists || entry.ValueCnt != 1 {
			return 0, fmt.Errorf("真实存档模板 SlotID %d 缺少%s标量", draft.TemplateSlotID, name)
		}
		return entry.Uint32(), nil
	}
	sigilHash, err := readUint(GemIDType, sourceUnit, "因子哈希")
	if err != nil || sigilHash == 0 || sigilHash == EmptyHash {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("真实存档模板 SlotID %d 指向空因子", draft.TemplateSlotID)
	}
	levelRaw, err := readUint(GemLevelIDType, sourceUnit, "因子等级")
	if err != nil {
		return nil, err
	}
	flags, err := readUint(GemFlagsIDType, sourceUnit, "因子标记")
	if err != nil {
		return nil, err
	}
	traitBase := uint32(TraitSlotBase + (int(sourceUnit)-GemSlotBaseID)*100)
	primaryHash, err := readUint(TraitHashIDType, traitBase, "主词条哈希")
	if err != nil || primaryHash == 0 || primaryHash == EmptyHash {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("真实存档模板 SlotID %d 缺少主词条", draft.TemplateSlotID)
	}
	primaryLevelRaw, err := readUint(TraitLevelIDType, traitBase, "主词条等级")
	if err != nil {
		return nil, err
	}
	secondaryHash, err := readUint(TraitHashIDType, traitBase+1, "副词条哈希")
	if err != nil {
		return nil, err
	}
	secondaryLevelRaw, err := readUint(TraitLevelIDType, traitBase+1, "副词条等级")
	if err != nil {
		return nil, err
	}
	hasSecondary := secondaryHash != 0 && secondaryHash != EmptyHash
	if !hasSecondary {
		secondaryHash = EmptyHash
		secondaryLevelRaw = 0
	}
	item := draft.Item
	item.Quantity = 1
	item.Level = int(int32(levelRaw))
	item.PrimaryLevel = int(int32(primaryLevelRaw))
	item.SecondaryLevel = int(int32(secondaryLevelRaw))
	if item.SigilName == "" {
		item.SigilName = sigilDisplayNameOr(sigilHash)
	}
	return &preparedLoadoutSigil{
		index: draft.Index, item: item, sigilHash: sigilHash, primaryHash: primaryHash,
		secondaryHash: secondaryHash, secondaryLevel: item.SecondaryLevel,
		hasSecondary: hasSecondary, flags: flags,
	}, nil
}

func validateLoadoutSigilDestination(save *SaveData, prepared *preparedLoadoutSigil) error {
	gemUnitID := prepared.gemUnitID
	gemIndex := gemUnitID - GemSlotBaseID
	primaryTraitUnit := TraitSlotBase + gemIndex*100
	secondaryTraitUnit := primaryTraitUnit + 1
	for _, field := range []struct {
		idType uint32
		unitID int
		name   string
	}{
		{GemSlotIDType, gemUnitID, "因子 SlotID"},
		{GemIDType, gemUnitID, "因子哈希"},
		{GemWornByIDType, gemUnitID, "装备角色"},
		{GemFlagsIDType, gemUnitID, "因子标记"},
		{GemLevelIDType, gemUnitID, "因子等级"},
		{TraitHashIDType, primaryTraitUnit, "主词条哈希"},
		{TraitLevelIDType, primaryTraitUnit, "主词条等级"},
	} {
		if _, ok := save.findUnit(field.idType, uint32(field.unitID)); !ok {
			return fmt.Errorf("因子空槽 %d 缺少%s字段", gemUnitID, field.name)
		}
	}
	if prepared.hasSecondary {
		if _, ok := save.findUnit(TraitHashIDType, uint32(secondaryTraitUnit)); !ok {
			return fmt.Errorf("因子空槽 %d 缺少副词条哈希字段", gemUnitID)
		}
		if _, ok := save.findUnit(TraitLevelIDType, uint32(secondaryTraitUnit)); !ok {
			return fmt.Errorf("因子空槽 %d 缺少副词条等级字段", gemUnitID)
		}
	}
	return nil
}

func validateLoadoutMasteryForCharacter(save *SaveData, charaHash uint32, ownerCode string, hashes []uint32) error {
	_, _ = save, charaHash
	_, err := validateMasteryQuota(hashes, ownerCode, false)
	return err
}

// validateLoadoutWrite 对一条写入请求做完整预校验，返回可落盘的 resolvedWrite。
// 任一不合法即返回 error（此时缓冲区尚未被触碰）。
func validateLoadoutWrite(save *SaveData, ix *loadoutIndex, cat *Catalog, w LoadoutWrite) (*resolvedWrite, error) {
	// 槽位：必须是玩家预设槽（非队伍、非实时装备）
	slot, isParty := loadoutSlotOf(w.UnitID)
	if isParty || slot < 1 || w.UnitID < loadoutBase || w.UnitID >= partyLoadoutBase {
		return nil, fmt.Errorf("UnitID %d 不是可写的预设槽（20000..20614）", w.UnitID)
	}
	// 六字段必须都存在且 ValueCnt 恰为设计前提值
	for _, f := range []struct {
		id   uint32
		want int
		name string
	}{
		{loadoutCharIDType, 1, "角色"}, {loadoutNameIDType, 64, "名称"},
		{loadoutWeaponIDType, 1, "武器"}, {loadoutSigilsIDType, 13, "因子"},
		{loadoutSkillsIDType, 4, "技能"}, {loadoutWeaponSkillsIDType, 5, "武器技能"}, {loadoutMasteryIDType, 50, "专精"},
	} {
		e, ok := save.findUnitExact(f.id, w.UnitID)
		if !ok {
			return nil, fmt.Errorf("槽 %d 缺少%s字段（IDType %d）", w.UnitID, f.name, f.id)
		}
		if e.ValueCnt != f.want {
			return nil, fmt.Errorf("槽 %d 的%s字段 ValueCnt=%d，期望 %d（存档结构异常，拒绝写入）", w.UnitID, f.name, e.ValueCnt, f.want)
		}
	}
	// 归属角色：块内实测推导，必须与前端认定一致
	blockChara, ok := resolveBlockChara(save, w.UnitID)
	if !ok {
		return nil, fmt.Errorf("槽 %d 所在角色块为空或角色冲突，无法确定归属角色（请选择已有≥1 套配装的角色）", w.UnitID)
	}
	expect, err := ParseHashHex(w.ExpectCharaHash)
	if err != nil {
		return nil, fmt.Errorf("ExpectCharaHash 无效: %v", err)
	}
	if expect != blockChara {
		return nil, fmt.Errorf("槽 %d 归属角色不符（存档实测 %08X，前端认定 %08X）——存档可能已变，请刷新", w.UnitID, blockChara, expect)
	}

	rw := &resolvedWrite{unitID: w.UnitID, op: w.Op, charaHash: blockChara}

	if w.Op == "clear" {
		if len(w.ConstructedSigils) != 0 {
			return nil, fmt.Errorf("clear 操作不能携带构造因子草稿")
		}
		rw.keepWeapon = true
		return rw, nil
	}

	if w.Op == "clone" {
		if len(w.ConstructedSigils) != 0 {
			return nil, fmt.Errorf("clone 操作不能携带构造因子草稿")
		}
		src := w.CloneFromUnitID
		sc, ok := resolveBlockChara(save, src)
		if !ok || sc != blockChara {
			return nil, fmt.Errorf("克隆源槽 %d 与目标不属于同一角色", src)
		}
		if e, ok := save.findUnitExact(loadoutWeaponIDType, src); ok {
			rw.weaponSID = e.Uint32()
		}
		rw.sigilSIDs = readLoadoutSigilVector(save, src)
		rw.skills = readVec(save, loadoutSkillsIDType, src, loadoutMaxSkills)
		rw.weaponSkills = readFixedVec(save, loadoutWeaponSkillsIDType, src, 5)
		rw.mastery = readVec(save, loadoutMasteryIDType, src, loadoutMaxMastery)
		rw.name = entryTextAt(save, src)
		if len(rw.mastery) > 0 {
			ownerCode := ix.deriveOwnerCode(save, blockChara)
			if err := validateLoadoutMasteryForCharacter(save, blockChara, ownerCode, rw.mastery); err != nil {
				return nil, err
			}
		}
		return rw, nil
	}

	if w.Op != "write" {
		return nil, fmt.Errorf("未知操作 %q（应为 write/clone/clear）", w.Op)
	}

	// ── Op == "write"：逐字段校验用户自定义内容 ──
	if !utf8.ValidString(w.Name) {
		return nil, fmt.Errorf("配装名称不是合法 UTF-8")
	}
	if len(w.Name) > loadoutNameMaxBytes {
		return nil, fmt.Errorf("配装名称 %d 字节超过上限 %d", len(w.Name), loadoutNameMaxBytes)
	}
	rw.name = w.Name

	ownerCode := ix.deriveOwnerCode(save, blockChara)

	// 武器（1402）：SlotID -> 现存武器，hash 非空，ownerCode 须匹配该角色或通用
	if w.WeaponSlotID != 0 {
		wu, ok := ix.wepBySlotID[w.WeaponSlotID]
		if !ok {
			return nil, fmt.Errorf("武器 SlotID %d 在存档里找不到对应武器", w.WeaponSlotID)
		}
		h := ix.wepHash[wu]
		if h == nil || h.Uint32() == EmptyHash || h.Uint32() == 0 {
			return nil, fmt.Errorf("武器 SlotID %d 指向空武器槽", w.WeaponSlotID)
		}
		if _, err := validateLoadoutWeaponDefinition(h.Uint32(), ownerCode); err != nil {
			return nil, fmt.Errorf("武器 SlotID %d 写入校验失败: %w", w.WeaponSlotID, err)
		}
		rw.weaponSID = w.WeaponSlotID
		if len(w.WeaponSkillHashes) == 0 {
			extra, exists := save.findUnitExact(weaponExtraIDType, wu)
			if !exists || extra.ValueCnt < 5 {
				return nil, fmt.Errorf("武器 SlotID %d 缺少 2818 五技能向量", w.WeaponSlotID)
			}
			rw.weaponSkills = readFixedVec(save, weaponExtraIDType, wu, 5)
		}
	}
	if len(w.WeaponSkillHashes) > 0 {
		if len(w.WeaponSkillHashes) != 5 {
			return nil, fmt.Errorf("配装武器技能 3005 必须恰好有 5 槽")
		}
		for index, value := range w.WeaponSkillHashes {
			hash, parseErr := ParseHashHex(value)
			if parseErr != nil {
				return nil, fmt.Errorf("配装武器技能槽 %d 无效: %w", index+1, parseErr)
			}
			rw.weaponSkills = append(rw.weaponSkills, hash)
		}
	}

	// 因子（1403）：≤12。已有 SlotID 与按 0 基索引提交的构造草稿先共同
	// 形成最终向量；草稿占用的位置不再要求旧 SlotID 仍有效。
	if len(w.SigilSlotIDs) > loadoutMaxSigils {
		return nil, fmt.Errorf("因子最多 %d 个，收到 %d", loadoutMaxSigils, len(w.SigilSlotIDs))
	}
	_, precSkills := ix.charaPrecedent(save, blockChara)
	constructedIndexes := make(map[int]bool, len(w.ConstructedSigils))
	for _, draft := range w.ConstructedSigils {
		if constructedIndexes[draft.Index] {
			return nil, fmt.Errorf("因子槽位索引 %d 被重复提交构造草稿", draft.Index)
		}
		prepared, err := prepareLoadoutSigilForSave(save, ix, cat, draft)
		if err != nil {
			return nil, fmt.Errorf("构造第 %d 个因子失败: %w", draft.Index+1, err)
		}
		constructedIndexes[draft.Index] = true
		rw.constructed = append(rw.constructed, prepared)
	}
	finalLen := len(w.SigilSlotIDs)
	for index := range constructedIndexes {
		if index+1 > finalLen {
			finalLen = index + 1
		}
	}
	rw.sigilSIDs = make([]uint32, finalLen)
	copy(rw.sigilSIDs, w.SigilSlotIDs)
	seenSID := map[uint32]bool{}
	for index, sid := range rw.sigilSIDs {
		if constructedIndexes[index] {
			rw.sigilSIDs[index] = 0 // 分配空槽后替换为新 SlotID
			continue
		}
		if sid == 0 {
			continue
		}
		if seenSID[sid] {
			return nil, fmt.Errorf("因子 SlotID %d 被重复装备", sid)
		}
		seenSID[sid] = true
		gu, ok := ix.gemBySlotID[sid]
		if !ok {
			return nil, fmt.Errorf("因子 SlotID %d 在存档里找不到对应因子（拒绝写悬空引用）", sid)
		}
		h := ix.gemHash[gu]
		if h == nil {
			return nil, fmt.Errorf("因子 SlotID %d 无 hash", sid)
		}
	}

	// 技能（1404）：≤4，优先按解包 skill_names 的角色归属校验；旧档未知项再回退 precedent。
	if len(w.SkillHashes) > loadoutMaxSkills {
		return nil, fmt.Errorf("技能最多 %d 个，收到 %d", loadoutMaxSkills, len(w.SkillHashes))
	}
	seenSkill := map[uint32]bool{}
	for _, hx := range w.SkillHashes {
		v, err := ParseHashHex(hx)
		if err != nil {
			return nil, fmt.Errorf("技能 hash 无效: %v", err)
		}
		if v == EmptyHash || v == 0 {
			continue
		}
		if seenSkill[v] {
			return nil, fmt.Errorf("技能 %08X 被重复配置", v)
		}
		seenSkill[v] = true
		if !skillBelongsToOwner(v, ownerCode) && !precSkills[v] {
			return nil, fmt.Errorf("技能 %08X 不属于该角色（%s）", v, ownerCode)
		}
		rw.skills = append(rw.skills, v)
	}

	// 专精（3007）：≤50，逐节点须属于该角色 PLxxxx（skillboard_nodes.json 的 char）
	if len(w.MasteryHashes) > loadoutMaxMastery {
		return nil, fmt.Errorf("专精节点最多 %d 个，收到 %d", loadoutMaxMastery, len(w.MasteryHashes))
	}
	exactMasterySlots := len(w.MasteryHashes) == loadoutMaxMastery
	activeMastery := make([]uint32, 0, len(w.MasteryHashes))
	for _, hx := range w.MasteryHashes {
		v, err := ParseHashHex(hx)
		if err != nil {
			return nil, fmt.Errorf("专精 hash 无效: %v", err)
		}
		if v == EmptyHash || v == 0 {
			if exactMasterySlots {
				rw.mastery = append(rw.mastery, EmptyHash)
			}
			continue
		}
		node, ok := skillboardNodeForHash(v)
		if !ok {
			return nil, fmt.Errorf("专精节点 %08X 未收录", v)
		}
		if ownerCode != "" && node.Char != "" && node.Char != ownerCode {
			return nil, fmt.Errorf("专精节点 %08X 属于 %s，不属于该角色（%s）", v, node.Char, ownerCode)
		}
		rw.mastery = append(rw.mastery, v)
		activeMastery = append(activeMastery, v)
	}
	// 配额校验先保护专精盘固有的分档与方向规则，再按目标角色自身的
	// 1323 Master 总 MSP 派生等级容量；低级角色不能借前端或克隆写入未解锁档位。
	if len(activeMastery) > 0 {
		if err := validateLoadoutMasteryForCharacter(save, blockChara, ownerCode, activeMastery); err != nil {
			return nil, err
		}
	}

	return rw, nil
}

// readVec 读一个 uint32 向量的前 maxN 个非填充值（跳过 0 与 EmptyHash）。
func readVec(save *SaveData, idType, unitID uint32, maxN int) []uint32 {
	e, ok := save.findUnitExact(idType, unitID)
	if !ok {
		return nil
	}
	var out []uint32
	for i, n := 0, vecLen(e); i < n && len(out) < maxN; i++ {
		v, err := e.Uint32At(i)
		if err != nil {
			break
		}
		if v == EmptyHash || v == 0 {
			continue
		}
		out = append(out, v)
	}
	return out
}

func readFixedVec(save *SaveData, idType, unitID uint32, count int) []uint32 {
	e, ok := save.findUnitExact(idType, unitID)
	if !ok || e.ValueCnt < count {
		return nil
	}
	out := make([]uint32, 0, count)
	for i := 0; i < count; i++ {
		value, err := e.Uint32At(i)
		if err != nil {
			return nil
		}
		out = append(out, value)
	}
	return out
}

func readLoadoutSigilVector(save *SaveData, unitID uint32) []uint32 {
	result := make([]uint32, loadoutMaxSigils)
	e, ok := save.findUnitExact(loadoutSigilsIDType, unitID)
	if !ok {
		return result
	}
	for _, sigil := range readLoadoutSigilSlots(e) {
		result[sigil.Index] = sigil.SlotID
	}
	return result
}

func entryTextAt(save *SaveData, unitID uint32) string {
	if e, ok := save.findUnitExact(loadoutNameIDType, unitID); ok {
		return entryText(e)
	}
	return ""
}

func sigilDisplayNameOr(hash uint32) string {
	if n := sigilDisplayName(hash); n != "" {
		return n
	}
	if useChinese() {
		return "未收录因子"
	}
	return "Uncatalogued Sigil"
}

// applyResolvedWrite 把一条已校验的写入落到缓冲区（内存态）。
func applyResolvedWrite(save *SaveData, rw *resolvedWrite) error {
	if rw.op == "clear" {
		if err := save.patchUintExact(loadoutCharIDType, rw.unitID, EmptyHash); err != nil {
			return err
		}
		if err := save.writeLoadoutName(rw.unitID, ""); err != nil {
			return err
		}
		if err := save.writeLoadoutVector(loadoutSigilsIDType, rw.unitID, nil, 13, 0); err != nil {
			return err
		}
		if err := save.writeLoadoutVector(loadoutSkillsIDType, rw.unitID, nil, 4, EmptyHash); err != nil {
			return err
		}
		if err := save.writeLoadoutVector(loadoutWeaponSkillsIDType, rw.unitID, nil, 5, EmptyHash); err != nil {
			return err
		}
		if err := save.writeLoadoutVector(loadoutMasteryIDType, rw.unitID, nil, 50, EmptyHash); err != nil {
			return err
		}
		return nil // 1402 保持原值
	}
	// write / clone
	if err := save.patchUintExact(loadoutCharIDType, rw.unitID, rw.charaHash); err != nil {
		return err
	}
	if err := save.writeLoadoutName(rw.unitID, rw.name); err != nil {
		return err
	}
	if err := save.patchUintExact(loadoutWeaponIDType, rw.unitID, rw.weaponSID); err != nil {
		return err
	}
	if err := save.writeLoadoutVector(loadoutSigilsIDType, rw.unitID, rw.sigilSIDs, 13, 0); err != nil {
		return err
	}
	if err := save.writeLoadoutVector(loadoutSkillsIDType, rw.unitID, rw.skills, 4, EmptyHash); err != nil {
		return err
	}
	if err := save.writeLoadoutVector(loadoutWeaponSkillsIDType, rw.unitID, rw.weaponSkills, 5, EmptyHash); err != nil {
		return err
	}
	if err := save.writeLoadoutVector(loadoutMasteryIDType, rw.unitID, rw.mastery, 50, EmptyHash); err != nil {
		return err
	}
	return nil
}

// verifyResolvedWrite 回读逐字段比对，返回命中的字段数。
func verifyResolvedWrite(save *SaveData, rw *resolvedWrite) (int, error) {
	hit := 0
	e, ok := save.findUnitExact(loadoutCharIDType, rw.unitID)
	if !ok {
		return hit, fmt.Errorf("回读缺少 3003 角色字段")
	}
	wantChara := rw.charaHash
	if rw.op == "clear" {
		wantChara = EmptyHash
	}
	if e.Uint32() != wantChara {
		return hit, fmt.Errorf("回读 3003 不符")
	}
	hit++
	if got := entryTextAt(save, rw.unitID); got == rw.name || (rw.op == "clear" && got == "") {
		hit++
	} else {
		return hit, fmt.Errorf("回读名称不符：%q != %q", got, rw.name)
	}
	if !vecMatches(save, loadoutSigilsIDType, rw.unitID, rw.sigilSIDs, 13, 0) {
		return hit, fmt.Errorf("回读因子向量不符")
	}
	hit++
	if !vecMatches(save, loadoutSkillsIDType, rw.unitID, rw.skills, 4, EmptyHash) {
		return hit, fmt.Errorf("回读技能向量不符")
	}
	hit++
	if !vecMatches(save, loadoutWeaponSkillsIDType, rw.unitID, rw.weaponSkills, 5, EmptyHash) {
		return hit, fmt.Errorf("回读武器技能向量不符")
	}
	hit++
	if !vecMatches(save, loadoutMasteryIDType, rw.unitID, rw.mastery, 50, EmptyHash) {
		return hit, fmt.Errorf("回读专精向量不符")
	}
	hit++
	if rw.op != "clear" {
		if e, ok := save.findUnitExact(loadoutWeaponIDType, rw.unitID); ok && e.Uint32() == rw.weaponSID {
			hit++
		} else {
			return hit, fmt.Errorf("回读武器 SlotID 不符")
		}
	}
	return hit, nil
}

func vecMatches(save *SaveData, idType, unitID uint32, vals []uint32, wantCnt int, pad uint32) bool {
	e, ok := save.findUnitExact(idType, unitID)
	if !ok || e.ValueCnt != wantCnt {
		return false
	}
	for i := 0; i < wantCnt; i++ {
		want := pad
		if i < len(vals) {
			want = vals[i]
		}
		got, err := e.Uint32At(i)
		if err != nil || got != want {
			return false
		}
	}
	return true
}

// sharedLoadoutSummonSlotIDs resolves the optional global summon configuration
// for one atomic apply. 1451 is global rather than per preset, so two write
// operations cannot safely request different orders in the same transaction.
// clone/clear operations intentionally never change it.
func sharedLoadoutSummonSlotIDs(changes []LoadoutWrite) ([]uint32, error) {
	var shared []uint32
	found := false
	for _, change := range changes {
		if !strings.EqualFold(strings.TrimSpace(change.Op), "write") || change.SummonSlotIDs == nil {
			continue
		}
		if !found {
			shared = make([]uint32, len(change.SummonSlotIDs))
			copy(shared, change.SummonSlotIDs)
			found = true
			continue
		}
		if len(shared) != len(change.SummonSlotIDs) {
			return nil, fmt.Errorf("同一批 write 请求了不同的召唤石配置")
		}
		for index := range shared {
			if shared[index] != change.SummonSlotIDs[index] {
				return nil, fmt.Errorf("同一批 write 请求了不同的召唤石配置")
			}
		}
	}
	if !found {
		return nil, nil
	}
	return shared, nil
}

func typedSlotDataFromSave(save *SaveData) (*SaveDataBinary, error) {
	if save == nil {
		return nil, fmt.Errorf("存档为空")
	}
	parsed, err := ParseSaveData(save.data)
	if err != nil {
		return nil, err
	}
	if parsed.SlotData == nil {
		return nil, fmt.Errorf("存档没有 SlotData")
	}
	return parsed.SlotData, nil
}

func validateLoadoutEquippedSummonTyped(data *SaveDataBinary) error {
	if data == nil {
		return fmt.Errorf("存档没有 SlotData")
	}
	matches := 0
	for _, unit := range data.UIntTable {
		if unit.IDType != 1451 || unit.UnitID != 0 {
			continue
		}
		matches++
		if len(unit.ValueData) != 4 {
			return fmt.Errorf("1451 UInt UnitID 0 的值数量为 %d，期望 4", len(unit.ValueData))
		}
	}
	if matches != 1 {
		return fmt.Errorf("1451 必须恰好有一条 UInt UnitID 0 vec4，实际 %d 条", matches)
	}
	return nil
}

// validateLoadoutSummonSlotIDs validates references only. It never edits the
// 1456..1460 summon instances themselves.
func validateLoadoutSummonSlotIDs(save *SaveData, slotIDs []uint32) ([]uint32, error) {
	if len(slotIDs) != 4 {
		return nil, fmt.Errorf("召唤石配置必须恰好包含 4 个 SlotID")
	}
	data, err := typedSlotDataFromSave(save)
	if err != nil {
		return nil, err
	}
	if err := validateLoadoutEquippedSummonTyped(data); err != nil {
		return nil, err
	}
	types := uintUnitMap(data, 1457)
	realSlots := map[uint32]int{}
	for _, unit := range uintUnitsByType(data, 1456) {
		if len(unit.ValueData) != 1 {
			continue
		}
		slotID := unit.ValueData[0]
		typeUnit, ok := types[unit.UnitID]
		if !ok || len(typeUnit.ValueData) != 1 {
			continue
		}
		typeHash := typeUnit.ValueData[0]
		if slotID != 0 && slotID != EmptyHash && typeHash != 0 && typeHash != EmptyHash && typeHash != summonInvalidTypeHash {
			realSlots[slotID]++
		}
	}
	seen := map[uint32]bool{}
	for _, slotID := range slotIDs {
		if slotID == 0 || slotID == EmptyHash {
			return nil, fmt.Errorf("召唤石 SlotID 必须为非零有效值")
		}
		if seen[slotID] {
			return nil, fmt.Errorf("召唤石 SlotID %d 重复", slotID)
		}
		seen[slotID] = true
		if realSlots[slotID] == 0 {
			return nil, fmt.Errorf("召唤石 SlotID %d 不存在", slotID)
		}
		if realSlots[slotID] > 1 {
			return nil, fmt.Errorf("召唤石 SlotID %d 对应多个实例，拒绝歧义写入", slotID)
		}
	}
	return append([]uint32(nil), slotIDs...), nil
}

func readLoadoutEquippedSummonSlotIDsStrict(save *SaveData) ([]uint32, error) {
	data, err := typedSlotDataFromSave(save)
	if err != nil {
		return nil, err
	}
	unit, ok := uintUnitExact(data, 1451, 0)
	if !ok {
		return nil, fmt.Errorf("回读缺少 1451 UnitID 0")
	}
	if len(unit.ValueData) != 4 {
		return nil, fmt.Errorf("回读 1451 值数量为 %d，期望 4", len(unit.ValueData))
	}
	return append([]uint32(nil), unit.ValueData...), nil
}

// readLoadoutEquippedSummonSlotIDs is kept small for tests and callers that
// only need a best-effort snapshot; the write path uses the strict variant.
func readLoadoutEquippedSummonSlotIDs(save *SaveData) []uint32 {
	values, _ := readLoadoutEquippedSummonSlotIDsStrict(save)
	return values
}

func writeLoadoutEquippedSummonSlotIDs(save *SaveData, slotIDs []uint32) error {
	if len(slotIDs) != 4 {
		return fmt.Errorf("写入 1451 需要恰好 4 个 SlotID")
	}
	var target *unitEntry
	for _, entry := range save.findAllUnitsByType(1451) {
		if entry.UnitID != 0 || entry.ValueCnt != 4 {
			continue
		}
		if target != nil && target.ValueOff != entry.ValueOff {
			return fmt.Errorf("1451 UnitID 0 存在多个候选记录，拒绝歧义写入")
		}
		target = entry
	}
	if target == nil {
		return fmt.Errorf("找不到四值的 1451 UnitID 0")
	}
	for index, slotID := range slotIDs {
		if err := target.SetUint32At(index, slotID); err != nil {
			return err
		}
	}
	return nil
}

func equalUint32Slice(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// LoadoutApply 是配装写入闸门：全量预校验通过后统一落盘，任一失败即在触碰磁盘前返回。
func (a *App) LoadoutApply(inputPath, outputPath string, changes []LoadoutWrite) (*LoadoutApplyResult, error) {
	return a.LoadoutApplyWithResources(inputPath, outputPath, LoadoutApplyRequest{Changes: changes})
}

// LoadoutApplyWithResources is the single transaction boundary for preset,
// constructed sigil, audited weapon, and audited summon edits.
func (a *App) LoadoutApplyWithResources(inputPath, outputPath string, request LoadoutApplyRequest) (*LoadoutApplyResult, error) {
	changes := request.Changes
	// Serialise all in-process save transactions so two editor actions cannot
	// allocate the same empty gem slot/max SlotID from the same stale snapshot.
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()

	if len(changes) == 0 {
		return nil, fmt.Errorf("没有要写入的配装")
	}
	if outputPath == "" {
		outputPath = inputPath
	}
	if samePath(inputPath, outputPath) {
		if _, err := loadoutFindProcessByName(charaProcessName); err == nil {
			return nil, fmt.Errorf("写入存档前请先完全退出游戏，避免游戏把旧数据写回")
		}
	}
	if _, err := loadProgressionCatalog(); err != nil {
		return nil, err
	}
	cat, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	save, err := LoadSave(inputPath)
	if err != nil {
		return nil, err
	}
	if request.ImportPayload != nil && (len(request.WeaponEdits) > 0 || len(request.SummonEdits) > 0) {
		return nil, fmt.Errorf("导入完整配装时不能同时提交手动武器/召唤石实例编辑")
	}
	createdWeapon, err := materializeLoadoutImportWeapon(save, changes, request.ImportPayload)
	if err != nil {
		return nil, err
	}
	preparedImport, err := prepareLoadoutImport(save, changes, request.ImportPayload)
	if err != nil {
		return nil, err
	}
	createdSummons, err := materializeLoadoutImportSummons(save, changes, request.ImportPayload)
	if err != nil {
		return nil, err
	}
	summonSlotIDs, err := sharedLoadoutSummonSlotIDs(changes)
	if err != nil {
		return nil, err
	}
	ix := buildLoadoutIndex(save)
	if summonSlotIDs != nil {
		summonSlotIDs, err = validateLoadoutSummonSlotIDs(save, summonSlotIDs)
		if err != nil {
			return nil, err
		}
	}
	inlineSummonSlotIDs := summonSlotIDs
	if len(request.SummonEdits) > 0 && inlineSummonSlotIDs == nil {
		inlineSummonSlotIDs, err = readLoadoutEquippedSummonSlotIDsStrict(save)
		if err != nil {
			return nil, err
		}
		inlineSummonSlotIDs, err = validateLoadoutSummonSlotIDs(save, inlineSummonSlotIDs)
		if err != nil {
			return nil, err
		}
	}

	// 第一段：全量预校验（不触碰缓冲区）
	seen := map[uint32]bool{}
	resolved := make([]*resolvedWrite, 0, len(changes))
	for _, w := range changes {
		if seen[w.UnitID] {
			return nil, fmt.Errorf("槽 %d 被重复提交", w.UnitID)
		}
		seen[w.UnitID] = true
		rw, err := validateLoadoutWrite(save, ix, cat, w)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, rw)
	}
	if request.ImportPayload != nil {
		if err := validateImportedMasteryCapacity(save, preparedImport, resolved, request.ImportPayload.ApplyMasteryConfiguration); err != nil {
			return nil, err
		}
	}
	inlineResources, err := prepareLoadoutInlineResources(save, request, resolved, inlineSummonSlotIDs)
	if err != nil {
		return nil, err
	}

	// 第二段：仍在触碰缓冲区之前，为全部构造草稿一次性分配空因子槽，
	// 并验证 PatchSigil 将访问的每个字段都存在。
	constructed := make([]*preparedLoadoutSigil, 0)
	for _, rw := range resolved {
		for _, prepared := range rw.constructed {
			prepared.owner = rw
			constructed = append(constructed, prepared)
		}
	}
	oldMaxSlotID := 0
	newMaxSlotID := 0
	firstNewSlotID := 0
	if len(constructed) > 0 {
		emptySlots, err := save.FindEmptyGemSlots(len(constructed))
		if err != nil {
			return nil, err
		}
		oldMaxSlotID, err = save.GetMaxSlotID()
		if err != nil {
			return nil, err
		}
		firstNewSlotID, newMaxSlotID, err = allocateSequentialSlotIDs(oldMaxSlotID, len(constructed))
		if err != nil {
			return nil, fmt.Errorf("无法为配装构造因子分配 SlotID: %w", err)
		}
		for i, prepared := range constructed {
			prepared.gemUnitID = emptySlots[i]
			prepared.newSlotID = uint32(firstNewSlotID + i)
			if err := validateLoadoutSigilDestination(save, prepared); err != nil {
				return nil, err
			}
		}
	}

	// 第三段：在同一个 SaveData 缓冲里先创建因子，再把新 SlotID 放入
	// 对应 1403 位置，最后只做一次校验和修复和磁盘写入。
	result := &LoadoutApplyResult{CreatedSummonCount: len(createdSummons)}
	if createdWeapon {
		result.CreatedWeaponCount = 1
	}
	if len(constructed) > 0 {
		if err := save.SetMaxSlotID(newMaxSlotID); err != nil {
			return nil, err
		}
		for _, prepared := range constructed {
			if err := save.PatchSigilWithFlags(
				prepared.gemUnitID, int(prepared.newSlotID), prepared.sigilHash, prepared.item.Level,
				prepared.primaryHash, prepared.item.PrimaryLevel,
				prepared.secondaryHash, prepared.secondaryLevel, prepared.hasSecondary, prepared.flags,
			); err != nil {
				return nil, fmt.Errorf("写入构造因子「%s」失败: %w", prepared.item.SigilName, err)
			}
			prepared.owner.sigilSIDs[prepared.index] = prepared.newSlotID
			result.CreatedCount++
			result.SlotIDs = append(result.SlotIDs, prepared.newSlotID)
		}
	}
	for _, rw := range resolved {
		if err := applyResolvedWrite(save, rw); err != nil {
			return nil, fmt.Errorf("写入槽 %d 失败: %w", rw.unitID, err)
		}
		if rw.op == "clear" {
			result.SlotsCleared++
		} else {
			result.SlotsWritten++
		}
	}
	if summonSlotIDs != nil {
		if err := writeLoadoutEquippedSummonSlotIDs(save, summonSlotIDs); err != nil {
			return nil, fmt.Errorf("写入召唤石配置失败: %w", err)
		}
	}
	if err := applyPreparedLoadoutInlineResources(save, inlineResources); err != nil {
		return nil, fmt.Errorf("apply inline loadout resources: %w", err)
	}
	_, err = applyPreparedLoadoutImport(save, preparedImport)
	if err != nil {
		return nil, fmt.Errorf("写入导入的角色/武器状态失败: %w", err)
	}
	if err := save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("修复存档校验失败: %w", err)
	}
	if err := save.Write(outputPath); err != nil {
		return nil, err
	}
	result.OutputPath = outputPath
	result.BackupPath = save.LastBackupPath()

	verify, err := LoadSave(outputPath)
	if err != nil {
		return nil, fmt.Errorf("配装已写入，但重新读取失败: %w", err)
	}
	for _, rw := range resolved {
		n, err := verifyResolvedWrite(verify, rw)
		if err != nil {
			return nil, fmt.Errorf("配装已写入，但槽 %d 回读验证失败（%v）；请用备份恢复", rw.unitID, err)
		}
		result.VerifiedFields += n
	}
	if summonSlotIDs != nil {
		got, err := readLoadoutEquippedSummonSlotIDsStrict(verify)
		if err != nil {
			return nil, fmt.Errorf("配装已写入，但召唤石配置回读失败（%v）；请用备份恢复", err)
		}
		if !equalUint32Slice(got, summonSlotIDs) {
			return nil, fmt.Errorf("配装已写入，但召唤石配置回读为 %v，期望 %v；请用备份恢复", got, summonSlotIDs)
		}
		result.VerifiedFields++
	}
	inlineVerified, err := verifyPreparedLoadoutInlineResources(verify, inlineResources)
	if err != nil {
		return nil, fmt.Errorf("loadout was written but inline resource readback failed: %v; restore from backup", err)
	}
	result.VerifiedFields += inlineVerified
	verifiedImport, err := verifyPreparedLoadoutImport(verify, preparedImport)
	if err != nil {
		return nil, fmt.Errorf("配装已写入，但角色/武器状态回读失败（%v）；请用备份恢复", err)
	}
	result.VerifiedFields += verifiedImport
	for index, record := range createdSummons {
		if err := verify.VerifySummonRecord(record); err != nil {
			return nil, fmt.Errorf("配装已写入，但第 %d 个新召唤石回读失败: %w；请用备份恢复", index+1, err)
		}
	}
	if len(constructed) > 0 {
		gotMaxSlotID, err := verify.GetMaxSlotID()
		if err != nil {
			return nil, fmt.Errorf("配装已写入，但因子最大 SlotID 回读失败: %w；请用备份恢复", err)
		}
		wantMaxSlotID := oldMaxSlotID + len(constructed)
		if gotMaxSlotID != wantMaxSlotID {
			return nil, fmt.Errorf("配装已写入，但因子最大 SlotID 回读为 %d，期望 %d；请用备份恢复", gotMaxSlotID, wantMaxSlotID)
		}
	}
	for i, prepared := range constructed {
		if err := verify.VerifySigilWithFlags(
			prepared.gemUnitID, prepared.newSlotID, prepared.sigilHash, prepared.item.Level,
			prepared.primaryHash, prepared.item.PrimaryLevel,
			prepared.secondaryHash, prepared.secondaryLevel, prepared.hasSecondary, prepared.flags,
		); err != nil {
			return nil, fmt.Errorf("配装已写入，但第 %d 个新因子回读验证失败: %w；请用备份恢复", i+1, err)
		}
		result.VerifiedCount++
	}
	if result.VerifiedCount != result.CreatedCount {
		return nil, fmt.Errorf("配装已写入，但新因子回读数量不符: 已创建 %d，已验证 %d；请用备份恢复", result.CreatedCount, result.VerifiedCount)
	}
	return result, nil
}

// LoadoutEditContext 给前端一份「该角色可安全引用的资源池」，杜绝前端瞎猜引用。
// charaHex 是 8 位十六进制的角色 hash（= LoadoutList 返回的 CharaHash）。
func (a *App) LoadoutEditContext(path, charaHex string) (*LoadoutEditContext, error) {
	if _, err := loadProgressionCatalog(); err != nil {
		return nil, err
	}
	cat, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	charaHash, err := ParseHashHex(charaHex)
	if err != nil {
		return nil, fmt.Errorf("角色 hash 无效: %v", err)
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	ix := buildLoadoutIndex(save)

	name := ix.charName[charaHash]
	if name == "" {
		return nil, fmt.Errorf("存档里找不到角色 %s", charaHex)
	}

	// 找该角色的块基址（任一占用槽）
	var blockBase uint32
	found := false
	for _, ce := range save.findAllUnitsByType(loadoutCharIDType) {
		if ce.Uint32() == charaHash && ce.UnitID >= loadoutBase && ce.UnitID < partyLoadoutBase {
			blockBase = loadoutBase + ((ce.UnitID-loadoutBase)/loadoutSlotsPerChara)*loadoutSlotsPerChara
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("角色「%s」在该存档里没有任何已保存的配装，无法确定预设槽块（请先在游戏里保存至少一套配装）", name)
	}

	ownerCode := ix.deriveOwnerCode(save, charaHash)
	precSigils, precSkills := ix.charaPrecedent(save, charaHash)

	ctx := &LoadoutEditContext{
		CharaHash: fmt.Sprintf("%08X", charaHash),
		CharaName: name,
		OwnerCode: ownerCode,
		BlockBase: blockBase,
	}

	// 15 个槽的占用/名称
	for u := blockBase; u < blockBase+loadoutSlotsPerChara; u++ {
		info := LoadoutSlotInfo{UnitID: u, Slot: int(u-blockBase) + 1}
		if ce, ok := save.findUnitExact(loadoutCharIDType, u); ok {
			v := ce.Uint32()
			if v != EmptyHash && v != 0 {
				info.Occupied = true
				info.Name = entryTextAt(save, u)
			}
		}
		ctx.Slots = append(ctx.Slots, info)
	}

	// 武器池：该角色专属 + 通用（去重，按 SlotID）
	seenWep := map[uint32]bool{}
	for sid, wu := range ix.wepBySlotID {
		h := ix.wepHash[wu]
		if h == nil {
			continue
		}
		hv := h.Uint32()
		if hv == EmptyHash || hv == 0 {
			continue
		}
		def, ok := progressionWeaponDefForLoadout(hv)
		if !ok {
			continue
		}
		oc := def.OwnerCode
		nm := progressionWeaponName(def)
		if oc != "" && oc != ownerCode {
			continue
		}
		if seenWep[sid] {
			continue
		}
		seenWep[sid] = true
		ctx.Weapons = append(ctx.Weapons, LoadoutPickWeapon{
			SlotID: sid, Hash: fmt.Sprintf("%08X", hv), Name: nm, OwnerCode: oc,
		})
	}

	// 因子池：通用因子 + 该角色 precedent 里出现过的角色因子（按 SlotID）。
	// 词条表先按 UnitID 建索引，避免对背包每颗因子反复全表扫描。
	traitHashByUnit := entriesByUnitID(save.findAllUnitsByType(TraitHashIDType))
	traitLevelByUnit := entriesByUnitID(save.findAllUnitsByType(TraitLevelIDType))
	for sid, gu := range ix.gemBySlotID {
		h := ix.gemHash[gu]
		if h == nil {
			continue
		}
		hv := h.Uint32()
		generic, allowed := loadoutSigilAccess(cat, hv, precSigils)
		if !allowed {
			continue
		}
		lvl := 0
		if l := ix.gemLevel[gu]; l != nil {
			lvl = int(l.Int32())
		}
		primaryHash, primaryLevel, secondaryHash, secondaryLevel := indexedSigilTraits(traitHashByUnit, traitLevelByUnit, gu)
		primaryName := loadoutTraitDisplayName(cat, primaryHash)
		secondaryName := loadoutTraitDisplayName(cat, secondaryHash)
		ctx.Sigils = append(ctx.Sigils, LoadoutPickSigil{
			SlotID: sid, Hash: fmt.Sprintf("%08X", hv), Name: loadoutSigilDisplayNameFromTraits(hv, primaryName, secondaryName),
			Level: lvl, PrimaryTraitHash: loadoutOptionalHash(primaryHash), PrimaryTraitName: primaryName, PrimaryTraitLevel: primaryLevel,
			SecondaryTraitHash: loadoutOptionalHash(secondaryHash), SecondaryTraitName: secondaryName, SecondaryTraitLevel: secondaryLevel, Generic: generic,
		})
	}
	sort.Slice(ctx.Weapons, func(i, j int) bool {
		if ctx.Weapons[i].Name != ctx.Weapons[j].Name {
			return ctx.Weapons[i].Name < ctx.Weapons[j].Name
		}
		return ctx.Weapons[i].SlotID < ctx.Weapons[j].SlotID
	})
	sort.Slice(ctx.Sigils, func(i, j int) bool {
		if ctx.Sigils[i].Name != ctx.Sigils[j].Name {
			return ctx.Sigils[i].Name < ctx.Sigils[j].Name
		}
		return ctx.Sigils[i].SlotID < ctx.Sigils[j].SlotID
	})

	// 技能池：使用解包数据中的完整角色技能表；仅在未收录角色上回退已有配装 precedent。
	ctx.Skills = skillPoolForOwnerCode(ownerCode)
	if len(ctx.Skills) == 0 {
		for h := range precSkills {
			ctx.Skills = append(ctx.Skills, LoadoutPickSkill{Hash: fmt.Sprintf("%08X", h), Name: skillNameForHash(h)})
		}
	}

	// 专精来源：该角色每套已存配装（可整段复制）
	for _, ce := range save.findAllUnitsByType(loadoutCharIDType) {
		if ce.Uint32() != charaHash || ce.UnitID < loadoutBase || ce.UnitID >= partyLoadoutBase {
			continue
		}
		nodes := readVec(save, loadoutMasteryIDType, ce.UnitID, loadoutMaxMastery)
		if len(nodes) == 0 {
			continue
		}
		hexes := make([]string, len(nodes))
		for i, n := range nodes {
			hexes[i] = fmt.Sprintf("%08X", n)
		}
		slot, _ := loadoutSlotOf(ce.UnitID)
		ctx.MasterySources = append(ctx.MasterySources, LoadoutMasterySource{
			UnitID: ce.UnitID, Slot: slot, Name: entryTextAt(save, ce.UnitID),
			NodeCount: len(nodes), NodeHashes: hexes,
		})
	}

	return ctx, nil
}
