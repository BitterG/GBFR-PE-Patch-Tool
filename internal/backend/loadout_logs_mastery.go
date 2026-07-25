package backend

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

//go:embed data/logs_skillboard_layout.json
var logsSkillboardLayoutJSON []byte

//go:embed data/logs_skillboard_zh_cn.json
var logsSkillboardChineseJSON []byte

type logsSkillboardText struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// LogsMasteryMapping is the context-aware result of converting Logs EffectUiIds
// to save-file node hashes. Every ID that cannot be assigned a distinct eligible
// node within its category, rank, and normalized-text context is kept in Unmapped.
type LogsMasteryMapping struct {
	Hashes   []string          `json:"hashes"`
	Unmapped map[uint32]string `json:"unmapped"`
}

type LogsMasteryRankPool struct {
	Rank  string                 `json:"rank"`
	Label string                 `json:"label"`
	Nodes []LogsMasteryNode      `json:"nodes"`
}

// LogsMasteryNode is a read-only GBFR Logs EffectUiId node. EffectUIId is not a save hash.
type LogsMasteryNode struct {
	EffectUIId uint32 `json:"effectUIId"`
	Cat        string `json:"cat"`
	CatLabel   string `json:"catLabel"`
	Rank       string `json:"rank"`
	Text       string `json:"text"`
	Active     bool   `json:"active"`
	Unknown    bool   `json:"unknown"`
}

var (
	logsSkillboardOnce sync.Once
	logsSkillboardLayout map[string]map[string][]uint32
	logsSkillboardTexts map[string]logsSkillboardText
	logsSkillboardErr error
)

func loadLogsSkillboard() error {
	logsSkillboardOnce.Do(func() {
		if err := json.Unmarshal(logsSkillboardLayoutJSON, &logsSkillboardLayout); err != nil { logsSkillboardErr = fmt.Errorf("解析内嵌 Logs 专精布局失败: %w", err); return }
		if err := json.Unmarshal(logsSkillboardChineseJSON, &logsSkillboardTexts); err != nil { logsSkillboardErr = fmt.Errorf("解析内嵌 Logs 专精中文文本失败: %w", err) }
	})
	return logsSkillboardErr
}

func logsMasteryCategory(id uint32) (string, string) {
	switch {
	case id >= 200: return "SB_LIMIT", "秘义"
	case id >= 100: return "SB_ATK", "真谛"
	default: return "SB_DEF", "觉醒"
	}
}

func logsMasteryTextKey(ownerCode string, id uint32) string {
	return strings.ToLower(ownerCode) + fmt.Sprintf("_%04x", id)
}

// normalizeLogsMasteryText only smooths typography and whitespace differences.
// It deliberately retains digits and all semantic text, so distinct values cannot merge.
func normalizeLogsMasteryText(text string) string {
	text = norm.NFKC.String(text)
	var out strings.Builder
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			continue
		case unicode.IsPunct(r) && r != '%' && r != '+' && r != '-':
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func logsLayoutIDs(ownerCode string) map[uint32]struct{} {
	layout := logsSkillboardLayout[ownerCode]
	if layout == nil {
		return nil
	}
	ids := make(map[uint32]struct{})
	for _, rankIDs := range layout {
		for _, id := range rankIDs {
			ids[id] = struct{}{}
		}
	}
	return ids
}

type logsMasteryContext struct {
	owner, category, rank, text string
}

type logsMasteryCandidate struct {
	hash uint32
	text string
}

func logsMasteryRankForLayoutKey(key string) (string, bool) {
	switch key {
	case "1":
		return "R1", true
	case "2":
		return "R2", true
	case "3":
		return "R3", true
	case "ex":
		return "EX", true
	default:
		return "", false
	}
}

// MapLogsMasteryEffectUIIDs maps layout-backed Effects to distinct local save hashes.
// Equal Logs text is resolved only within the same owner, category, rank, and
// normalized text context; surplus eligible local nodes are randomly assigned
// without replacement.
func (a *App) MapLogsMasteryEffectUIIDs(ownerCode string, effectUIIDs []uint32) (*LogsMasteryMapping, error) {
	if err := loadLogsSkillboard(); err != nil {
		return nil, err
	}
	ownerCode = strings.ToUpper(strings.TrimSpace(ownerCode))
	mapping := &LogsMasteryMapping{Unmapped: make(map[uint32]string)}
	layout := logsSkillboardLayout[strings.ToLower(ownerCode)]
	if layout == nil {
		for _, id := range effectUIIDs {
			mapping.Unmapped[id] = "角色没有收录 Logs 专精布局"
		}
		return mapping, nil
	}

	layoutRanks := make(map[uint32]string)
	for key, ids := range layout {
		rank, valid := logsMasteryRankForLayoutKey(key)
		if !valid {
			continue
		}
		for _, id := range ids {
			layoutRanks[id] = rank
		}
	}
	loadSkillboard()
	candidatesByContext := make(map[logsMasteryContext][]logsMasteryCandidate)
	for _, node := range skillboardAllNodes {
		if node.Char != ownerCode {
			continue
		}
		// Only hashes accepted by the save's editable 3007 vector are candidates.
		rank, _, valid := masteryRankOfGrp(node.Grp)
		hash, hashErr := ParseHashHex(node.Hash)
		if !valid || hashErr != nil || (rank == "R1" && strings.TrimSpace(node.Name) != "") || isMasterySpecializationHash(hash) {
			continue
		}
		text := normalizeLogsMasteryText(node.Desc)
		if text == "" {
			continue
		}
		context := logsMasteryContext{owner: ownerCode, category: node.Cat, rank: rank, text: text}
		candidatesByContext[context] = append(candidatesByContext[context], logsMasteryCandidate{hash: hash, text: node.Hash})
	}

	seenIDs := make(map[uint32]struct{})
	uniqueIDs := make([]uint32, 0, len(effectUIIDs))
	groups := make(map[logsMasteryContext][]uint32)
	groupOrder := make([]logsMasteryContext, 0)
	for _, id := range effectUIIDs {
		if _, duplicate := seenIDs[id]; duplicate {
			continue
		}
		seenIDs[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
		rank, exists := layoutRanks[id]
		if !exists {
			mapping.Unmapped[id] = "EffectUiId 未收录在该角色的 Logs 专精布局"
			continue
		}
		text := normalizeLogsMasteryText(logsSkillboardTexts[logsMasteryTextKey(ownerCode, id)].Text)
		if text == "" {
			mapping.Unmapped[id] = "Logs 中文效果文本缺失"
			continue
		}
		category, _ := logsMasteryCategory(id)
		context := logsMasteryContext{owner: ownerCode, category: category, rank: rank, text: text}
		if _, exists := groups[context]; !exists {
			groupOrder = append(groupOrder, context)
		}
		groups[context] = append(groups[context], id)
	}

	assigned := make(map[uint32]string)
	usedHashes := make(map[uint32]struct{})
	for _, context := range groupOrder {
		ids := groups[context]
		available := make([]logsMasteryCandidate, 0, len(candidatesByContext[context]))
		seenHashes := make(map[uint32]struct{})
		for _, candidate := range candidatesByContext[context] {
			if _, duplicate := seenHashes[candidate.hash]; duplicate {
				continue
			}
			seenHashes[candidate.hash] = struct{}{}
			if _, occupied := usedHashes[candidate.hash]; !occupied {
				available = append(available, candidate)
			}
		}
		if len(available) < len(ids) {
			reason := fmt.Sprintf("同类同阶同文本本地节点不足：需%d/可用%d", len(ids), len(available))
			for _, id := range ids {
				mapping.Unmapped[id] = reason
			}
			continue
		}
		if len(available) > len(ids) {
			for index := range ids {
				n, err := rand.Int(rand.Reader, big.NewInt(int64(len(available)-index)))
				if err != nil {
					for _, unmappedID := range ids {
						mapping.Unmapped[unmappedID] = "同类同阶同文本随机分配失败"
					}
					available = nil
					break
				}
				pick := index + int(n.Int64())
				available[index], available[pick] = available[pick], available[index]
			}
		}
		if available == nil {
			continue
		}
		for index, id := range ids {
			assigned[id] = available[index].text
			usedHashes[available[index].hash] = struct{}{}
		}
	}
	for _, id := range uniqueIDs {
		if hash, ok := assigned[id]; ok {
			mapping.Hashes = append(mapping.Hashes, hash)
		}
	}
	return mapping, nil
}

// LogsMasteryNodePool returns the authoritative Logs EffectUiId layout for display only.
// It intentionally has no relationship to save-file MasteryHashes and never writes them.
func (a *App) LogsMasteryNodePool(ownerCode string, effectUIIDs []uint32) ([]LogsMasteryRankPool, error) {
	if err := loadLogsSkillboard(); err != nil { return nil, err }
	ownerCode = strings.ToLower(strings.TrimSpace(ownerCode))
	layout := logsSkillboardLayout[ownerCode]
	if layout == nil { layout = logsSkillboardLayout["default"] }
	if layout == nil { return nil, fmt.Errorf("未收录 Logs 专精布局：%s", ownerCode) }
	active := make(map[uint32]bool, len(effectUIIDs))
	for _, id := range effectUIIDs { active[id] = true }
	seen := make(map[uint32]bool)
	ranks := []struct { key, rank, label string }{{"1", "R1", "1阶段"}, {"2", "R2", "2阶段"}, {"3", "R3", "3阶段"}, {"ex", "EX", "EX阶段"}}
	out := make([]LogsMasteryRankPool, 0, len(ranks))
	for _, r := range ranks {
		pool := LogsMasteryRankPool{Rank: r.rank, Label: r.label}
		for _, id := range layout[r.key] {
			seen[id] = true
			cat, catLabel := logsMasteryCategory(id)
			text := logsSkillboardTexts[logsMasteryTextKey(ownerCode, id)].Text
			unknown := strings.TrimSpace(text) == ""
			if unknown { text = "未收录中文效果（仅展示，不会写入存档）" }
			pool.Nodes = append(pool.Nodes, LogsMasteryNode{EffectUIId: id, Cat: cat, CatLabel: catLabel, Rank: r.rank, Text: text, Active: active[id], Unknown: unknown})
		}
		out = append(out, pool)
	}
	// Preserve unknown IDs so callers can visibly distinguish unavailable layout entries.
	for _, id := range effectUIIDs { if !seen[id] { out = append(out, LogsMasteryRankPool{Rank: "UNKNOWN", Label: "未收录布局", Nodes: []LogsMasteryNode{{EffectUIId:id, Text:"未收录 Logs 专精布局（仅展示，不会写入存档）", Active:true, Unknown:true}}}) } }
	return out, nil
}
