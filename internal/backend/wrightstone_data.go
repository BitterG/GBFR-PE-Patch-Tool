package backend

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

//go:embed data/wrightstones.json data/wrightstone_traits.json
var wrightstoneDataFiles embed.FS

type WrightstoneDef struct {
	InternalID     string `json:"internalId"`
	Hash           string `json:"hash"`
	DisplayName    string `json:"displayName"`
	DefaultTraitID string `json:"defaultTraitId"`
}

type WrightstoneTraitDef struct {
	InternalID     string  `json:"internalId"`
	Hash           string  `json:"hash"`
	DisplayName    string  `json:"displayName"`
	Category       *string `json:"category"`
	MaxLevel       *int    `json:"maxLevel"`
	AllowedLevels  []int   `json:"allowedLevels"`
	ObservedLevels []int   `json:"observedLevels"`
}

type WrightstoneCatalog struct {
	Wrightstones      []WrightstoneDef
	Traits            []WrightstoneTraitDef
	wrightstoneByID   map[string]*WrightstoneDef
	wrightstoneByHash map[uint32]*WrightstoneDef
	traitByID         map[string]*WrightstoneTraitDef
	traitByHash       map[uint32]*WrightstoneTraitDef
}

func LoadWrightstoneCatalog() (*WrightstoneCatalog, error) {
	c := &WrightstoneCatalog{}

	wrightstones, err := loadWrightstoneJSON[struct {
		Wrightstones []WrightstoneDef `json:"wrightstones"`
	}]("data/wrightstones.json", "data/wrightstones/wrightstones.json")
	if err != nil {
		return nil, fmt.Errorf("加载祝福数据失败: %w", err)
	}
	c.Wrightstones = wrightstones.Wrightstones

	traits, err := loadWrightstoneJSON[struct {
		Traits []WrightstoneTraitDef `json:"traits"`
	}]("data/wrightstone_traits.json", "data/traits.json", "data/traits/traits.json")
	if err != nil {
		return nil, fmt.Errorf("加载祝福特性数据失败: %w", err)
	}
	for _, trait := range traits.Traits {
		if trait.Hash == "" || trait.MaxLevel == nil {
			continue
		}
		c.Traits = append(c.Traits, trait)
	}
	appendDLCSupplementWrightstoneTraits(c)

	c.wrightstoneByID = make(map[string]*WrightstoneDef, len(c.Wrightstones))
	c.wrightstoneByHash = make(map[uint32]*WrightstoneDef, len(c.Wrightstones))
	for i := range c.Wrightstones {
		w := &c.Wrightstones[i]
		if w.InternalID == "" || w.Hash == "" {
			return nil, fmt.Errorf("祝福数据缺少 ID 或哈希")
		}
		if w.DefaultTraitID == "" {
			return nil, fmt.Errorf("%s 缺少默认特性", w.DisplayName)
		}
		c.wrightstoneByID[w.InternalID] = w
		if h, err := ParseHashHex(w.Hash); err == nil {
			c.wrightstoneByHash[h] = w
		}
	}

	c.traitByID = make(map[string]*WrightstoneTraitDef, len(c.Traits))
	c.traitByHash = make(map[uint32]*WrightstoneTraitDef, len(c.Traits))
	for i := range c.Traits {
		t := &c.Traits[i]
		c.traitByID[t.InternalID] = t
		if h, err := ParseHashHex(t.Hash); err == nil {
			c.traitByHash[h] = t
		}
	}

	for _, w := range c.Wrightstones {
		if _, ok := c.traitByID[w.DefaultTraitID]; !ok {
			return nil, fmt.Errorf("%s 的默认特性不存在: %s", w.DisplayName, w.DefaultTraitID)
		}
	}

	return c, nil
}

func loadWrightstoneJSON[T any](paths ...string) (T, error) {
	var result T
	var lastErr error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			cleaned := cleanJSON(string(data))
			if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
				return result, fmt.Errorf("解析 %s 失败: %w", path, err)
			}
			return result, nil
		}
		if !os.IsNotExist(err) {
			return result, fmt.Errorf("读取 %s 失败: %w", path, err)
		}
		lastErr = err
	}

	for _, path := range paths {
		data, err := wrightstoneDataFiles.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}
		cleaned := cleanJSON(string(data))
		if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
			return result, fmt.Errorf("解析 %s 失败: %w", path, err)
		}
		return result, nil
	}
	return result, fmt.Errorf("读取 %v 失败: %w", paths, lastErr)
}

func (c *WrightstoneCatalog) RequireWrightstone(id string) (*WrightstoneDef, error) {
	if w, ok := c.wrightstoneByID[id]; ok {
		return w, nil
	}
	return nil, fmt.Errorf("未知祝福 ID: %s", id)
}

func (c *WrightstoneCatalog) RequireTrait(id string) (*WrightstoneTraitDef, error) {
	if id == "" {
		return nil, fmt.Errorf("请选择特性")
	}
	if t, ok := c.traitByID[id]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("未知特性 ID: %s", id)
}

func (c *WrightstoneCatalog) LookupWrightstoneByHash(hash uint32) *WrightstoneDef {
	return c.wrightstoneByHash[hash]
}

func (c *WrightstoneCatalog) LookupTraitByHash(hash uint32) *WrightstoneTraitDef {
	return c.traitByHash[hash]
}

func (c *WrightstoneCatalog) GetWrightstoneSortedList() []*WrightstoneDef {
	sorted := make([]*WrightstoneDef, len(c.Wrightstones))
	for i := range c.Wrightstones {
		sorted[i] = &c.Wrightstones[i]
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].DisplayName < sorted[j].DisplayName
	})
	return sorted
}

func (c *WrightstoneCatalog) GetTraitSortedList() []*WrightstoneTraitDef {
	sorted := make([]*WrightstoneTraitDef, len(c.Traits))
	for i := range c.Traits {
		sorted[i] = &c.Traits[i]
	}
	sort.Slice(sorted, func(i, j int) bool {
		return cnTrait(sorted[i].DisplayName) < cnTrait(sorted[j].DisplayName)
	})
	return sorted
}

func requireWrightstoneTraitLevels(trait *WrightstoneTraitDef) ([]int, error) {
	if len(trait.AllowedLevels) > 0 {
		return trait.AllowedLevels, nil
	}
	if trait.MaxLevel != nil {
		levels := make([]int, *trait.MaxLevel)
		for i := range levels {
			levels[i] = i + 1
		}
		return levels, nil
	}
	return nil, fmt.Errorf("特性 %s 缺少已验证的等级范围", trait.DisplayName)
}
