package backend

import (
	"fmt"
	"sort"
	"strings"
)

// ── 专精配置器：自由配置一套合法专精盘 ────────────────────────────────
//
// 规则（来自 skillboard_unlock/group/rank_adjust，并由回归测试覆盖）：
// 一套满级(50级)合法盘 = 精确 10/10/10/20 = 50 个节点，四档由 grp 区分：
//   68DE92AC=Rank1(上限10) A96D9EBC=Rank2(10) 4A5DDC7B=Rank3(10) 3B99904D=RankEX(20)
// 节点间无连通性要求，但 2 阶必须有一个方向达到 6 项，3 阶继续沿用该方向。
// 主方向之外仍可混搭普通子词条；若解包数据出现 2/3 阶具名专精技能，则只能属于主方向。

const (
	masteryGrpR1 = "68DE92AC"
	masteryGrpR2 = "A96D9EBC"
	masteryGrpR3 = "4A5DDC7B"
	masteryGrpEX = "3B99904D"
)

// masteryRanks 定义四档的顺序、标签与配额（满级）。
var masteryRanks = []struct {
	Grp   string
	Rank  string
	Label string
	Cap   int
}{
	{masteryGrpR1, "R1", "1阶专精技能", 10},
	{masteryGrpR2, "R2", "2阶专精技能", 10},
	{masteryGrpR3, "R3", "3阶专精技能", 10},
	{masteryGrpEX, "EX", "EX阶专精技能", 20},
}

func masteryRankOfGrp(grp string) (rank string, cap int, ok bool) {
	for _, r := range masteryRanks {
		if r.Grp == grp {
			return r.Rank, r.Cap, true
		}
	}
	return "", 0, false
}

// MasteryNode 是配置器里一个可选节点。
type MasteryNode struct {
	Hash           string `json:"hash"`
	Cat            string `json:"cat"`            // SB_ATK/SB_DEF/SB_LIMIT
	CatLabel       string `json:"catLabel"`       // 真谛（攻击盘）等
	Name           string `json:"name"`           // 1阶具名节点名（可空）
	Desc           string `json:"desc"`           // 效果说明（数值已填充）
	Specialization bool   `json:"specialization"` // 保留给前端兼容；可选3007节点始终为false
}

// MasteryRankPool 是某一档的可选节点池 + 配额。
type MasteryRankPool struct {
	Rank  string        `json:"rank"`
	Label string        `json:"label"`
	Cap   int           `json:"cap"`
	Nodes []MasteryNode `json:"nodes"`
}

// MasteryCategorySummary 是某一阶、某一专精类型的实际分配与生效状态。
// 1阶门槛为 3 项；2/3阶门槛为 6 项，且3阶必须沿用2阶方向。
type MasteryCategorySummary struct {
	Cat            string `json:"cat"`
	Label          string `json:"label"`
	Specialization string `json:"specialization"`
	Effect         string `json:"effect"`
	Count          int    `json:"count"`
	Threshold      int    `json:"threshold"`
	Active         bool   `json:"active"`
	Reason         string `json:"reason"`
}

type MasteryRankSummary struct {
	Rank       string                   `json:"rank"`
	Label      string                   `json:"label"`
	Count      int                      `json:"count"`
	Cap        int                      `json:"cap"`
	Categories []MasteryCategorySummary `json:"categories"`
}

type MasteryBuildSummary struct {
	Total        int                  `json:"total"`
	PrimaryCat   string               `json:"primaryCat"`
	PrimaryLabel string               `json:"primaryLabel"`
	Ranks        []MasteryRankSummary `json:"ranks"`
}

func (s *MasteryBuildSummary) rank(rank string) *MasteryRankSummary {
	for i := range s.Ranks {
		if s.Ranks[i].Rank == rank {
			return &s.Ranks[i]
		}
	}
	return nil
}

func (r *MasteryRankSummary) category(cat string) *MasteryCategorySummary {
	for i := range r.Categories {
		if r.Categories[i].Cat == cat {
			return &r.Categories[i]
		}
	}
	return nil
}

func (r *MasteryRankSummary) activeCategoryCount() int {
	n := 0
	for i := range r.Categories {
		if r.Categories[i].Active {
			n++
		}
	}
	return n
}

var masteryCatLabel = map[string]string{
	"SB_ATK":   "真谛（攻击盘）",
	"SB_DEF":   "觉醒（防御盘）",
	"SB_LIMIT": "秘义（界限盘）",
}

// MasteryNodePool 返回某角色（PLxxxx）可选专精节点，按四档分组。
// ownerCode 由前端从 LoadoutEditContext.ownerCode 取得。
func (a *App) MasteryNodePool(ownerCode string) ([]MasteryRankPool, error) {
	if ownerCode == "" {
		return nil, fmt.Errorf("无法确定该角色的专精盘归属码（PLxxxx）")
	}
	loadSkillboard()
	byRank := map[string][]MasteryNode{}
	for _, n := range skillboardAllNodes {
		if n.Char != ownerCode {
			continue
		}
		// A selectable node must have a verified, user-readable effect. The
		// unpacked table contains at least one placeholder row (F2D81718) with
		// no effect text; exposing it would let users write an unexplained node.
		if strings.TrimSpace(n.Desc) == "" {
			continue
		}
		rank, _, ok := masteryRankOfGrp(n.Grp)
		if !ok {
			continue
		}
		hash, _ := ParseHashHex(n.Hash)
		// Stage effect rows belong to the unlock table, not the save's 50-slot
		// 3007 vector. The game activates their effects from the selected-node
		// counts (3/6/6), so they must not consume an editable node slot.
		if (rank == "R1" && strings.TrimSpace(n.Name) != "") || isMasterySpecializationHash(hash) {
			continue
		}
		byRank[rank] = append(byRank[rank], MasteryNode{
			Hash: n.Hash, Cat: n.Cat, CatLabel: catLabelOf(n.Cat), Name: n.Name, Desc: n.Desc,
			Specialization: false,
		})
	}
	var out []MasteryRankPool
	for _, r := range masteryRanks {
		nodes := byRank[r.Rank]
		// 攻/防/界 分类内稳定排序，具名节点靠前
		sort.SliceStable(nodes, func(i, j int) bool {
			if nodes[i].Cat != nodes[j].Cat {
				return nodes[i].Cat < nodes[j].Cat
			}
			si, sj := nodes[i].Specialization, nodes[j].Specialization
			if si != sj {
				return si
			}
			ni, nj := nodes[i].Name != "", nodes[j].Name != ""
			if ni != nj {
				return ni
			}
			return nodes[i].Hash < nodes[j].Hash
		})
		out = append(out, MasteryRankPool{Rank: r.Rank, Label: r.Label, Cap: r.Cap, Nodes: nodes})
	}
	if len(byRank) == 0 {
		return nil, fmt.Errorf("角色 %s 没有专精盘数据（如伊德以特殊机制无技能盘）", ownerCode)
	}
	return out, nil
}

func catLabelOf(cat string) string {
	if l, ok := masteryCatLabel[cat]; ok {
		return l
	}
	return "基础盘"
}

func masterySpecializationName(ownerCode, cat string) string {
	loadSkillboard()
	for _, node := range skillboardAllNodes {
		if node.Char == ownerCode && node.Grp == masteryGrpR1 && node.Cat == cat && node.Name != "" {
			return node.Name
		}
	}
	return catLabelOf(cat)
}

func masterySpecializationEffect(ownerCode, rank, cat string) string {
	loadSkillboard()
	for _, node := range skillboardAllNodes {
		if node.Char != ownerCode || node.Cat != cat || strings.TrimSpace(node.Desc) == "" {
			continue
		}
		nodeRank, _, ok := masteryRankOfGrp(node.Grp)
		if !ok || nodeRank != rank {
			continue
		}
		hash, _ := ParseHashHex(node.Hash)
		if (rank == "R1" && strings.TrimSpace(node.Name) != "") || isMasterySpecializationHash(hash) {
			return node.Desc
		}
	}
	return ""
}

func summarizeMasteryHashes(ownerCode string, hashes []uint32) (*MasteryBuildSummary, error) {
	if ownerCode == "" {
		return nil, fmt.Errorf("无法确定专精盘归属码")
	}
	catOrder := []string{"SB_ATK", "SB_DEF", "SB_LIMIT"}
	counts := map[string]map[string]int{}
	for _, rank := range masteryRanks {
		counts[rank.Rank] = map[string]int{}
	}

	result := &MasteryBuildSummary{}
	for _, hash := range hashes {
		if hash == 0 || hash == EmptyHash {
			continue
		}
		node, ok := skillboardNodeForHash(hash)
		if !ok {
			return nil, fmt.Errorf("专精节点 %08X 未收录", hash)
		}
		if node.Char != "" && node.Char != ownerCode {
			return nil, fmt.Errorf("专精节点 %08X 属于 %s，不属于 %s", hash, node.Char, ownerCode)
		}
		rank, _, ok := masteryRankOfGrp(node.Grp)
		if !ok {
			return nil, fmt.Errorf("专精节点 %08X 阶级未知", hash)
		}
		counts[rank][node.Cat]++
		result.Total++
	}

	r2Primary := ""
	for _, cat := range catOrder {
		if counts["R2"][cat] >= 6 {
			r2Primary = cat
			break
		}
	}
	for _, rank := range masteryRanks {
		rs := MasteryRankSummary{Rank: rank.Rank, Label: rank.Label, Cap: rank.Cap}
		threshold := 0
		if rank.Rank == "R1" {
			threshold = 3
		}
		if rank.Rank == "R2" || rank.Rank == "R3" {
			threshold = 6
		}
		for _, cat := range catOrder {
			count := counts[rank.Rank][cat]
			cs := MasteryCategorySummary{
				Cat: cat, Label: catLabelOf(cat), Specialization: masterySpecializationName(ownerCode, cat),
				Effect: masterySpecializationEffect(ownerCode, rank.Rank, cat), Count: count, Threshold: threshold,
			}
			switch rank.Rank {
			case "R1":
				cs.Active = count >= threshold
				if !cs.Active {
					cs.Reason = fmt.Sprintf("需 %d 项，当前 %d 项", threshold, count)
				}
			case "R2":
				cs.Active = r2Primary == cat && count >= threshold
				if !cs.Active {
					cs.Reason = fmt.Sprintf("未达到2阶方向门槛（%d/%d）", count, threshold)
				}
			case "R3":
				cs.Active = r2Primary == cat && count >= threshold
				if r2Primary != cat {
					cs.Reason = "未沿用2阶主方向"
				} else if !cs.Active {
					cs.Reason = fmt.Sprintf("需 %d 项，当前 %d 项", threshold, count)
				}
			case "EX":
				cs.Reason = "EX阶技能；不构成第四种专精类型"
			}
			rs.Count += count
			rs.Categories = append(rs.Categories, cs)
		}
		result.Ranks = append(result.Ranks, rs)
	}
	if r3 := result.rank("R3"); r3 != nil {
		for _, category := range r3.Categories {
			if category.Active {
				result.PrimaryCat, result.PrimaryLabel = category.Cat, category.Specialization
				break
			}
		}
	}
	if result.PrimaryCat == "" && r2Primary != "" {
		result.PrimaryCat, result.PrimaryLabel = r2Primary, masterySpecializationName(ownerCode, r2Primary)
	}
	return result, nil
}

// MasterySummarize 给前端返回真实分配下的三方向生效状态与官方 EX 阶信息。
func (a *App) MasterySummarize(ownerCode string, hexes []string) (*MasteryBuildSummary, error) {
	hashes := make([]uint32, 0, len(hexes))
	for _, value := range hexes {
		hash, err := ParseHashHex(value)
		if err != nil {
			return nil, fmt.Errorf("专精节点 hash %q 无效: %w", value, err)
		}
		hashes = append(hashes, hash)
	}
	return summarizeMasteryHashes(ownerCode, hashes)
}

// validateMasteryQuota 校验一组专精节点 hash 是否满足配额（每档 ≤ 上限），
// 并可选地要求正好点满 50（10/10/10/20）。返回每档实际计数。
// 用于 loadout_write 写入前的合法性校验。
func validateMasteryQuota(hashes []uint32, ownerCode string, requireFull bool) (map[string]int, error) {
	_ = requireFull
	loadSkillboard()
	counts := map[string]int{"R1": 0, "R2": 0, "R3": 0, "EX": 0}
	seen := map[uint32]bool{}
	for _, h := range hashes {
		if h == EmptyHash || h == 0 {
			continue
		}
		if seen[h] {
			return counts, fmt.Errorf("专精节点 %08X 被重复配置", h)
		}
		seen[h] = true
		n, ok := skillboardNodeForHash(h)
		if !ok {
			return counts, fmt.Errorf("专精节点 %08X 未收录", h)
		}
		if ownerCode != "" && n.Char != "" && n.Char != ownerCode {
			return counts, fmt.Errorf("专精节点 %08X 属于 %s，不属于该角色（%s）", h, n.Char, ownerCode)
		}
		rank, cap, ok := masteryRankOfGrp(n.Grp)
		if !ok {
			return counts, fmt.Errorf("专精节点 %08X 档位未知", h)
		}
		counts[rank]++
		if counts[rank] > cap {
			return counts, fmt.Errorf("%s 档专精超过上限 %d 个", masteryRankLabel(rank), cap)
		}
	}
	return counts, nil
}

func masteryRankLabel(rank string) string {
	for _, r := range masteryRanks {
		if r.Rank == rank {
			return r.Label
		}
	}
	return rank
}
