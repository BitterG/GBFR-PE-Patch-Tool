package backend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed data/logs_skillboard_layout.json
var logsSkillboardLayoutJSON []byte

//go:embed data/logs_skillboard_zh_cn.json
var logsSkillboardChineseJSON []byte

type logsSkillboardText struct {
	Key  string `json:"key"`
	Text string `json:"text"`
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

func logsMasteryTextKey(ownerCode string, id uint32) string { return strings.ToLower(ownerCode) + fmt.Sprintf("_%04x", id) }

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
