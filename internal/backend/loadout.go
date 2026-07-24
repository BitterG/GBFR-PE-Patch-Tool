package backend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"unicode/utf8"
)

// ── 配装预设（游戏内 Loadout / 装备方案）──────────────────────────────
//
// 结构通过存档差分实测确认（改一项 → 存档只变对应字段）：
//
//	UnitID = 20000 + 角色块*15 + (槽位-1)           每角色 15 个槽（游戏 UI 的 Loadout 01..15）
//	  3002 (vec)  配装名称，UTF-8 C 字符串
//	  3003 (1)    角色 hash（= SaveID_CharacterID 的值）；EmptyHash 表示该槽未保存
//	  1402 (1)    武器 —— 存的是武器的 SlotID（weaponSlotIDType 2802 的值），不是武器 hash
//	  1403 (13)   因子 ×12(+1 填充) —— 存的是因子的 SlotID（gemSlotIDType 2702 的值）
//	  1404 (4)    技能 ×4（技能 hash）
//	  3005 (5)    当前配装武器技能 ×5（位置敏感；与所选武器 2818 快照一致）
//	  3007 (50)   专精 —— 技能盘节点 hash（对应 DB skillboard_layout.Key）
//
// 关键点：1402/1403 是「SlotID 引用」，只能指向存档里真实存在的武器/因子，
// 这与游戏内手动保存的配装完全同构。
//
// 「角色块」不能由角色序号推算：实测 SaveData1 的伊欧块基址是 20060，
// 而 SaveData2 的 20060 属于欧根（存档有转换存档 / DLC 两套角色布局，
// 见 save_app.go）。块基址恒对齐 15 边界，故槽位号取模即可，归属角色一律读 3003。
//
// 写入前提（实测 SaveData2(3).dat 与 SaveData1.dat 一致）：
// 615 个预设槽（UnitID 20000..20614 = 41 个角色块 × 15 槽）在存档里**全部预先分配**，
// 空槽的六个字段同样存在，只是 3003=EmptyHash。各字段 ValueCnt 恒定：
// 3003=1 / 3002=64 / 1402=1 / 1403=13 / 1404=4 / 3007=50。
// 因此写入配装只需原地改值（sigil_store.go 的 patchUint 系列），无需插入 FlatBuffer 条目。
const (
	loadoutNameIDType         uint32 = 3002
	loadoutCharIDType         uint32 = 3003
	loadoutWeaponIDType       uint32 = 1402
	loadoutSigilsIDType       uint32 = 1403
	loadoutSkillsIDType       uint32 = 1404
	loadoutWeaponSkillsIDType uint32 = 3005
	loadoutMasteryIDType      uint32 = 3007

	gemSlotIDType uint32 = 2702 // 因子的 SlotID（与武器的 2802 对称）

	loadoutBase          = 20000  // 保存的预设：UnitID = 20000 + 角色块*15 + (槽位-1)
	loadoutSlotsPerChara = 15     // 每角色 15 个预设槽（游戏 UI 的 Loadout 01..15）
	partyLoadoutBase     = 104000 // 当前队伍 4 名成员的实时配装（非玩家保存的预设）
)

// ── 技能盘节点表（专精翻译）────────────────────────────────────────────
// data/skillboard_nodes.json 由游戏表提取：skillboard_layout/effect/action_parts
// + 简中文本；说明里的 {n} 占位符已按 Value(n+1) 顺序映射填入实际数值。

//go:embed data/skillboard_nodes.json
var skillboardNodesJSON []byte

type SkillboardNode struct {
	Hash string `json:"hash"`
	Char string `json:"char"` // PL0400 等
	Cat  string `json:"cat"`  // SB_ATK / SB_DEF / SB_LIMIT / 基础盘
	Grp  string `json:"grp"`
	Name string `json:"name"` // 具名大节点（真谛/觉醒/秘义），多数节点为空
	Desc string `json:"desc"` // 效果说明（数值已填充）
}

var (
	skillboardOnce     sync.Once
	skillboardByHash   map[uint32]SkillboardNode
	skillboardAllNodes []SkillboardNode // 全量节点（专精配置器按 char/grp 过滤用）
)

// loadSkillboard 确保技能盘节点表已解析（幂等）。
func loadSkillboard() {
	skillboardOnce.Do(func() {
		var payload struct {
			Nodes []SkillboardNode `json:"nodes"`
		}
		skillboardByHash = map[uint32]SkillboardNode{}
		if err := json.Unmarshal(skillboardNodesJSON, &payload); err != nil {
			return
		}
		skillboardAllNodes = payload.Nodes
		for _, n := range payload.Nodes {
			if h, err := ParseHashHex(n.Hash); err == nil {
				skillboardByHash[h] = n
			}
		}
	})
}

func skillboardNodeForHash(hash uint32) (SkillboardNode, bool) {
	loadSkillboard()
	n, ok := skillboardByHash[hash]
	return n, ok
}

type LoadoutMasteryNode struct {
	Hash         string  `json:"hash"`
	Cat          string  `json:"cat"`
	Grp          string  `json:"grp"`
	Rank         string  `json:"rank"`
	RankLabel    string  `json:"rankLabel"`
	Name         string  `json:"name"` // 具名节点名（可为空）
	Desc         string  `json:"desc"` // 按游戏面板尺度显示的效果说明
	RawDesc      string  `json:"rawDesc,omitempty"`
	DisplayScale float64 `json:"displayScale,omitempty"`
	Evidence     string  `json:"evidence,omitempty"`
}

func loadoutMasteryNodeForHash(hash uint32) (LoadoutMasteryNode, bool) {
	n, ok := skillboardNodeForHash(hash)
	if !ok {
		return LoadoutMasteryNode{}, false
	}
	rank, _, rankOK := masteryRankOfGrp(n.Grp)
	if !rankOK {
		return LoadoutMasteryNode{}, false
	}
	desc, rawDesc, displayScale, evidence := n.Desc, "", float64(0), "2.0.2-table"
	if panel, parsed := parseMasteryPanelBonus(n.Desc, ""); parsed && panel.Label == "昏厥值" && panel.Unit == "flat" {
		rawDesc = n.Desc
		desc = fmt.Sprintf("昏厥值%+.10g（原始 f32 %g ×%g 面板）", panel.Value, panel.RawValue, panel.DisplayScale)
		displayScale = panel.DisplayScale
		evidence = panel.Evidence
	}
	return LoadoutMasteryNode{
		Hash: fmt.Sprintf("%08X", hash), Cat: n.Cat, Grp: n.Grp,
		Rank: rank, RankLabel: masteryRankLabel(rank), Name: n.Name, Desc: desc,
		RawDesc: rawDesc, DisplayScale: displayScale, Evidence: evidence,
	}, true
}

// ── 技能名表 ──────────────────────────────────────────────────────────
// data/skill_names.json：ability.Key 经 XXHash32Custom 求 hash + 简中名。

//go:embed data/skill_names.json
var skillNamesJSON []byte

type LoadoutSkill struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}

var (
	skillNamesOnce   sync.Once
	skillNameByHash  map[uint32]string
	skillKeyByHash   map[uint32]string
	skillOwnerByHash map[uint32]string
	skillPoolByOwner map[string][]LoadoutPickSkill
)

func loadSkillNameCatalog() {
	skillNamesOnce.Do(func() {
		var payload struct {
			Skills map[string]struct {
				Key  string `json:"key"`
				Char string `json:"char"`
				Name string `json:"name"`
			} `json:"skills"`
		}
		skillNameByHash = map[uint32]string{}
		skillKeyByHash = map[uint32]string{}
		skillOwnerByHash = map[uint32]string{}
		skillPoolByOwner = map[string][]LoadoutPickSkill{}
		if err := json.Unmarshal(skillNamesJSON, &payload); err != nil {
			return
		}
		for hex, s := range payload.Skills {
			if h, err := ParseHashHex(hex); err == nil {
				skillNameByHash[h] = s.Name
				skillKeyByHash[h] = s.Key
				skillOwnerByHash[h] = s.Char
				if s.Char != "" {
					skillPoolByOwner[s.Char] = append(skillPoolByOwner[s.Char], LoadoutPickSkill{Hash: fmt.Sprintf("%08X", h), Name: s.Name, Key: s.Key})
				}
			}
		}
		for owner := range skillPoolByOwner {
			sort.Slice(skillPoolByOwner[owner], func(i, j int) bool { return skillPoolByOwner[owner][i].Key < skillPoolByOwner[owner][j].Key })
		}
	})
}

func skillNameForHash(hash uint32) string {
	loadSkillNameCatalog()
	return skillNameByHash[hash]
}

func skillKeyForHash(hash uint32) string {
	loadSkillNameCatalog()
	return skillKeyByHash[hash]
}

func skillPoolForOwnerCode(ownerCode string) []LoadoutPickSkill {
	loadSkillNameCatalog()
	source := skillPoolByOwner[ownerCode]
	out := make([]LoadoutPickSkill, len(source))
	copy(out, source)
	return out
}

func skillBelongsToOwner(hash uint32, ownerCode string) bool {
	loadSkillNameCatalog()
	return ownerCode != "" && skillOwnerByHash[hash] == ownerCode
}

type LoadoutSigil struct {
	Index               int    `json:"index"`  // 1403 原始 0 基位置；中间空槽不能压缩
	SlotID              uint32 `json:"slotId"` // 1403 里存的值
	UnitID              uint32 `json:"unitId"` // 对应的因子槽 UnitID（30000+）
	Hash                string `json:"hash"`   // 因子 hash
	Name                string `json:"name"`   // 中文名
	Level               int    `json:"level"`  // 因子等级
	PrimaryTraitHash    string `json:"primaryTraitHash"`
	PrimaryTraitName    string `json:"primaryTraitName"`
	PrimaryTraitLevel   int    `json:"primaryTraitLevel"`
	SecondaryTraitHash  string `json:"secondaryTraitHash"`
	SecondaryTraitName  string `json:"secondaryTraitName"`
	SecondaryTraitLevel int    `json:"secondaryTraitLevel"`
	Missing             bool   `json:"missing"` // SlotID 在存档里找不到对应因子
}

type LoadoutEntry struct {
	UnitID            uint32                `json:"unitId"`
	Slot              int                   `json:"slot"`    // 该角色下的第几个槽（1..15），无法推断时为 0
	IsParty           bool                  `json:"isParty"` // true = 当前队伍成员的实时配装（UnitID 104000+），不是玩家保存的预设
	Name              string                `json:"name"`
	CharaHash         string                `json:"charaHash"`
	CharaName         string                `json:"charaName"`
	WeaponSlotID      uint32                `json:"weaponSlotId"`
	WeaponHash        string                `json:"weaponHash"`
	WeaponName        string                `json:"weaponName"`
	Weapon            *LoadoutWeaponContext `json:"weapon,omitempty"`
	Sigils            []LoadoutSigil        `json:"sigils"`
	Skills            []LoadoutSkill        `json:"skills"`            // 4 个技能（含中文名）
	WeaponSkillHashes []string              `json:"weaponSkillHashes"` // 3005 的五槽原始顺序
	Mastery           []LoadoutMasteryNode  `json:"mastery"`           // 专精（技能盘）节点，含中文效果
}

type CharacterLoadouts struct {
	CharaName string         `json:"charaName"`
	CharaHash string         `json:"charaHash"`
	Loadouts  []LoadoutEntry `json:"loadouts"`
}

func sortLoadoutsCurrentFirst(loadouts []LoadoutEntry) {
	sort.SliceStable(loadouts, func(i, j int) bool {
		if loadouts[i].IsParty != loadouts[j].IsParty {
			return loadouts[i].IsParty
		}
		return loadouts[i].UnitID < loadouts[j].UnitID
	})
}

// maxLoadoutVec 限制单个向量字段的读取长度。tryReadUnitEntry 只校验
// ValueCnt>0、不校验它与剩余字节的关系，损坏/伪造存档可给出高达 2^31 的
// ValueCnt——若照此预分配会直接 OOM 崩溃。配装各字段实际最长为 3007 的 50。
const maxLoadoutVec = 256

func vecLen(e *unitEntry) int {
	if e == nil || e.ValueCnt <= 0 {
		return 0
	}
	if e.ValueCnt > maxLoadoutVec {
		return maxLoadoutVec
	}
	return e.ValueCnt
}

// entryText 把配装名称字段（3002，Byte 表 ValueCnt=字节数）当 UTF-8 C 字符串读。
// 名称是**字节向量**：直接按字节读 ValueCnt 个字节、遇首个 NUL 截断。
// （早先误按 uint32 步长读会越过 64 字节向量末尾多读 192 字节相邻记录，靠 NUL
// 截断侥幸不出错；现按字节读，语义正确、不越界。）
func entryText(e *unitEntry) string {
	raw := e.Bytes()
	if len(raw) == 0 {
		return ""
	}
	if i := indexZero(raw); i >= 0 {
		raw = raw[:i]
	}
	if !utf8.Valid(raw) {
		return ""
	}
	return string(raw)
}

func indexZero(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}

// loadoutSlotOf 由 UnitID 推断槽位号，以及它是否是队伍实时配装。
//
// UnitID 104000+ 是「当前队伍 4 名成员」的实时配装，不是玩家保存的预设槽。
// 预设槽：每个角色占连续 15 个槽，块基址恒对齐 15 的边界（实测 27 条预设的
// UnitID-20000 全为 15 的倍数），故槽位号只取块内偏移。
//
// 注意：绝不可由角色序号反推块基址。不同存档（转换存档 / DLC 存档，见
// save_app.go 的两张槽位表）角色→块的映射并不一致——实测 SaveData1 的伊欧
// 块基址是 20060，而 SaveData2 的 20060 属于欧根。取模也天然覆盖古兰（序号 0）。
// 归属角色一律读 3003，不做推算。
func loadoutSlotOf(u uint32) (slot int, isParty bool) {
	if u >= partyLoadoutBase {
		return int(u-partyLoadoutBase) + 1, true
	}
	if u >= loadoutBase {
		return int((u-loadoutBase)%loadoutSlotsPerChara) + 1, false
	}
	return 0, false
}

// LoadoutList 读出存档里全部已保存的配装预设，按角色分组。
func (a *App) LoadoutList(path string) ([]CharacterLoadouts, error) {
	if _, err := loadProgressionCatalog(); err != nil {
		return nil, err
	}
	cat, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}

	// 角色块在转换存档和 DLC 存档中的位置不同，名称只能按角色 hash 解析。
	charName := characterNameByHash

	gemHash := entriesByUnitID(save.findAllUnitsByType(GemIDType))
	gemLevel := entriesByUnitID(save.findAllUnitsByType(GemLevelIDType))
	traitHashByUnit := entriesByUnitID(save.findAllUnitsByType(TraitHashIDType))
	traitLevelByUnit := entriesByUnitID(save.findAllUnitsByType(TraitLevelIDType))

	// 因子 SlotID -> 因子槽 UnitID。
	// 跳过已清空的因子记录（2703==EmptyHash 但 2702 仍残留旧 SlotID，
	// 本工具的 RemoveAllSigils/DeleteSelectedSigils 就会留下这种记录）：
	// 否则悬空引用会「命中」空记录，显示成无名幽灵因子而非 Missing。
	gemBySlotID := map[uint32]uint32{}
	for _, e := range save.findAllUnitsByType(gemSlotIDType) {
		if h := gemHash[e.UnitID]; h == nil || h.Uint32() == EmptyHash || h.Uint32() == 0 {
			continue
		}
		gemBySlotID[e.Uint32()] = e.UnitID
	}

	// 武器 SlotID -> 武器槽 UnitID
	wepBySlotID := map[uint32]uint32{}
	for _, e := range save.findAllUnitsByType(weaponSlotIDType) {
		wepBySlotID[e.Uint32()] = e.UnitID
	}
	wepHash := entriesByUnitID(save.findAllUnitsByType(weaponIDType))

	names := entriesByUnitID(save.findAllUnitsByType(loadoutNameIDType))
	weapons := entriesByUnitID(save.findAllUnitsByType(loadoutWeaponIDType))
	sigils := entriesByUnitID(save.findAllUnitsByType(loadoutSigilsIDType))
	skills := entriesByUnitID(save.findAllUnitsByType(loadoutSkillsIDType))
	weaponSkills := entriesByUnitID(save.findAllUnitsByType(loadoutWeaponSkillsIDType))
	mastery := entriesByUnitID(save.findAllUnitsByType(loadoutMasteryIDType))
	weaponContexts := make(map[uint32]*LoadoutWeaponContext)

	byChara := map[uint32]*CharacterLoadouts{}
	for _, ce := range save.findAllUnitsByType(loadoutCharIDType) {
		ch := ce.Uint32()
		name, ok := charName[ch]
		if !ok || ch == EmptyHash || ch == 0 {
			continue // 空槽
		}
		u := ce.UnitID

		lo := LoadoutEntry{
			UnitID:    u,
			Name:      entryText(names[u]),
			CharaHash: fmt.Sprintf("%08X", ch),
			CharaName: name,
		}
		lo.Slot, lo.IsParty = loadoutSlotOf(u)

		// 武器：1402 = 武器 SlotID
		if e := weapons[u]; e != nil {
			sid := e.Uint32()
			lo.WeaponSlotID = sid
			if wu, ok := wepBySlotID[sid]; ok {
				if h := wepHash[wu]; h != nil {
					hv := h.Uint32()
					lo.WeaponHash = fmt.Sprintf("%08X", hv)
					if d, ok := progressionWeaponDefForHash(hv); ok {
						lo.WeaponName = progressionWeaponName(d)
					}
				}
			}
			if cached, exists := weaponContexts[sid]; exists {
				lo.Weapon = cached
			} else if context, contextErr := readLoadoutWeaponContext(save, sid); contextErr == nil {
				weaponContexts[sid] = context
				lo.Weapon = context
			}
		}

		// 因子：1403 = 因子 SlotID ×12(+1 填充)。保留每颗因子的原始
		// 0 基位置；不能把第 1、4 格压缩成前两格，否则编辑器回写会换槽。
		if e := sigils[u]; e != nil {
			for _, s := range readLoadoutSigilSlots(e) {
				sid := s.SlotID
				gu, ok := gemBySlotID[sid]
				if !ok {
					s.Missing = true
					lo.Sigils = append(lo.Sigils, s)
					continue
				}
				s.UnitID = gu
				var sigilHash uint32
				if h := gemHash[gu]; h != nil {
					sigilHash = h.Uint32()
					s.Hash = fmt.Sprintf("%08X", sigilHash)
				}
				if l := gemLevel[gu]; l != nil {
					s.Level = int(l.Int32())
				}
				primaryHash, primaryLevel, secondaryHash, secondaryLevel := indexedSigilTraits(traitHashByUnit, traitLevelByUnit, gu)
				if primaryHash != 0 && primaryHash != EmptyHash {
					s.PrimaryTraitHash = fmt.Sprintf("%08X", primaryHash)
				}
				s.PrimaryTraitName = loadoutTraitDisplayName(cat, primaryHash)
				s.PrimaryTraitLevel = primaryLevel
				if secondaryHash != 0 && secondaryHash != EmptyHash {
					s.SecondaryTraitHash = fmt.Sprintf("%08X", secondaryHash)
				}
				s.SecondaryTraitName = loadoutTraitDisplayName(cat, secondaryHash)
				s.SecondaryTraitLevel = secondaryLevel
				s.Name = loadoutSigilDisplayNameFromTraits(sigilHash, s.PrimaryTraitName, s.SecondaryTraitName)
				lo.Sigils = append(lo.Sigils, s)
			}
		}

		// 技能：1404 = 4 个技能 hash（经 skill_names.json 翻译）
		if e := skills[u]; e != nil {
			for i, n := 0, vecLen(e); i < n; i++ {
				v, err := e.Uint32At(i)
				if err != nil {
					break
				}
				if v == EmptyHash || v == 0 {
					continue
				}
				lo.Skills = append(lo.Skills, LoadoutSkill{
					Hash: fmt.Sprintf("%08X", v),
					Name: skillNameForHash(v),
					Key:  skillKeyForHash(v),
				})
			}
		}

		// 武器技能：3005 是配装自身保存的五槽快照，必须保留位置。
		if e := weaponSkills[u]; e != nil {
			for i := 0; i < vecLen(e) && i < 5; i++ {
				v, readErr := e.Uint32At(i)
				if readErr != nil {
					break
				}
				lo.WeaponSkillHashes = append(lo.WeaponSkillHashes, fmt.Sprintf("%08X", v))
			}
		}

		// 专精：3007 = 技能盘节点 hash（经 skillboard_nodes.json 翻译成中文效果）
		if e := mastery[u]; e != nil {
			for i, n := 0, vecLen(e); i < n; i++ {
				v, err := e.Uint32At(i)
				if err != nil {
					break
				}
				if v == EmptyHash || v == 0 {
					continue
				}
				node := LoadoutMasteryNode{Hash: fmt.Sprintf("%08X", v)}
				if resolved, ok := loadoutMasteryNodeForHash(v); ok {
					node = resolved
				}
				lo.Mastery = append(lo.Mastery, node)
			}
		}

		g := byChara[ch]
		if g == nil {
			g = &CharacterLoadouts{CharaName: name, CharaHash: fmt.Sprintf("%08X", ch)}
			byChara[ch] = g
		}
		g.Loadouts = append(g.Loadouts, lo)
	}

	result := make([]CharacterLoadouts, 0, len(byChara))
	for _, g := range byChara {
		sortLoadoutsCurrentFirst(g.Loadouts)
		result = append(result, *g)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CharaName < result[j].CharaName })
	return result, nil
}

// readLoadoutSigilSlots 只解析 1403 的 12 个可装备槽，跳过空值但保留
// 原始 Index。第 13 项是游戏固定填充位，绝不能暴露成可编辑槽。
func readLoadoutSigilSlots(e *unitEntry) []LoadoutSigil {
	if e == nil {
		return nil
	}
	n := vecLen(e)
	if n > loadoutMaxSigils {
		n = loadoutMaxSigils
	}
	result := make([]LoadoutSigil, 0, n)
	for i := 0; i < n; i++ {
		sid, err := e.Uint32At(i)
		if err != nil {
			break
		}
		if sid == 0 || sid == EmptyHash {
			continue
		}
		result = append(result, LoadoutSigil{Index: i, SlotID: sid})
	}
	return result
}

// sigilDisplayName returns the localized name when the runtime catalog proves one.
func sigilDisplayName(hash uint32) string {
	if hash == EmptyHash {
		return ""
	}
	return localizedRuntimeName(hash)
}

func indexedSigilTraits(hashByUnit, levelByUnit map[uint32]*unitEntry, gemUnitID uint32) (primaryHash uint32, primaryLevel int, secondaryHash uint32, secondaryLevel int) {
	gemIndex := int(gemUnitID) - GemSlotBaseID
	if gemIndex < 0 {
		return
	}
	primaryUnit := uint32(TraitSlotBase + gemIndex*100)
	secondaryUnit := primaryUnit + 1
	if entry := hashByUnit[primaryUnit]; entry != nil {
		primaryHash = entry.Uint32()
	}
	if entry := levelByUnit[primaryUnit]; entry != nil {
		primaryLevel = int(entry.Int32())
	}
	if entry := hashByUnit[secondaryUnit]; entry != nil {
		secondaryHash = entry.Uint32()
	}
	if entry := levelByUnit[secondaryUnit]; entry != nil {
		secondaryLevel = int(entry.Int32())
	}
	return
}

func loadoutTraitDisplayName(cat *Catalog, hash uint32) string {
	if hash == 0 || hash == EmptyHash {
		return ""
	}
	if trait := cat.LookupTraitByHash(hash); trait != nil {
		return cnTrait(trait.DisplayName)
	}
	if name := localizedRuntimeName(hash); name != "" {
		return name
	}
	if useChinese() {
		return "未收录词条"
	}
	return "Uncatalogued Trait"
}

func loadoutOptionalHash(hash uint32) string {
	if hash == 0 || hash == EmptyHash {
		return ""
	}
	return fmt.Sprintf("%08X", hash)
}
