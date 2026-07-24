package backend

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed data/sigils.json data/traits.json data/secondary-trait-rules.json
var dataFiles embed.FS

type SigilDef struct {
	InternalID                   string                        `json:"internalId"`
	Hash                         string                        `json:"hash"`
	DisplayName                  string                        `json:"displayName"`
	Notes                        string                        `json:"notes"`
	Source                       string                        `json:"source"`
	Confidence                   string                        `json:"confidence"`
	Category                     *string                       `json:"category"`
	IsPlusSigil                  *bool                         `json:"isPlusSigil"`
	SupportsSecondaryTrait       *bool                         `json:"supportsSecondaryTrait"`
	AllowedSigilLevels           []int                         `json:"allowedSigilLevels"`
	DefaultSigilLevel            *int                          `json:"defaultSigilLevel"`
	MaxSigilLevel                *int                          `json:"maxSigilLevel"`
	PrimaryTraitID               string                        `json:"primaryTraitId"`
	PrimaryTraitName             *string                       `json:"primaryTraitName"`
	FirstTraitMaxLevel           *int                          `json:"firstTraitMaxLevel"`
	AllowedFirstTraitLevels      []int                         `json:"allowedFirstTraitLevels"`
	AllowedSecondaryTraitIDs     []string                      `json:"allowedSecondaryTraitIds"`
	DisallowedSecondaryTraitIDs  []string                      `json:"disallowedSecondaryTraitIds"`
	DefaultSecondaryTraitID      *string                       `json:"defaultSecondaryTraitId"`
	DefaultSecondaryTraitName    *string                       `json:"defaultSecondaryTraitName"`
	SecondaryTraitLevelOverrides map[string]TraitLevelOverride `json:"secondaryTraitLevelOverrides"`
}

type TraitDef struct {
	InternalID                    string  `json:"internalId"`
	Hash                          string  `json:"hash"`
	DisplayName                   string  `json:"displayName"`
	Category                      *string `json:"category"`
	MaxLevel                      *int    `json:"maxLevel"`
	AllowedLevels                 []int   `json:"allowedLevels"`
	ObservedLevels                []int   `json:"observedLevels"`
	CanAppearAsPrimary            *bool   `json:"canAppearAsPrimary"`
	CanAppearAsSecondary          *bool   `json:"canAppearAsSecondary"`
	BannedAsSecondaryOnPlusSigils *bool   `json:"bannedAsSecondaryOnPlusSigils"`
}

type TraitLevelOverride struct {
	MaxLevel       *int  `json:"maxLevel"`
	AllowedLevels  []int `json:"allowedLevels"`
	ObservedLevels []int `json:"observedLevels"`
}

type CompatibilityRule struct {
	ID                     string  `json:"id"`
	Type                   string  `json:"type"`
	SigilID                *string `json:"sigilId"`
	PrimaryTraitID         *string `json:"primaryTraitId"`
	SecondaryTraitID       *string `json:"secondaryTraitId"`
	AllowedSecondaryLevels []int   `json:"allowedSecondaryLevels"`
}

type RuleFile struct {
	Rules []CompatibilityRule `json:"rules"`
}

type Catalog struct {
	Sigils      []SigilDef
	Traits      []TraitDef
	Rules       []CompatibilityRule
	sigilByID   map[string]*SigilDef
	traitByID   map[string]*TraitDef
	sigilByHash map[uint32]*SigilDef
	traitByHash map[uint32]*TraitDef
}

func LoadCatalog() (*Catalog, error) {
	c := &Catalog{}

	sigils, err := loadJSON[struct {
		Sigils []SigilDef `json:"sigils"`
	}]("data/sigils.json")
	if err != nil {
		return nil, fmt.Errorf("加载因子数据失败: %w", err)
	}
	c.Sigils = sigils.Sigils

	traits, err := loadJSON[struct {
		Traits []TraitDef `json:"traits"`
	}]("data/traits.json")
	if err != nil {
		return nil, fmt.Errorf("加载特性数据失败: %w", err)
	}
	c.Traits = traits.Traits
	appendDLCSupplementCatalog(c)

	rules, err := loadJSON[RuleFile]("data/secondary-trait-rules.json")
	if err != nil {
		return nil, fmt.Errorf("加载规则数据失败: %w", err)
	}
	c.Rules = rules.Rules

	c.sigilByID = make(map[string]*SigilDef, len(c.Sigils))
	c.sigilByHash = make(map[uint32]*SigilDef, len(c.Sigils))
	for i := range c.Sigils {
		c.sigilByID[c.Sigils[i].InternalID] = &c.Sigils[i]
		if h, err := ParseHashHex(c.Sigils[i].Hash); err == nil {
			c.sigilByHash[h] = &c.Sigils[i]
		}
	}
	c.traitByID = make(map[string]*TraitDef, len(c.Traits))
	c.traitByHash = make(map[uint32]*TraitDef, len(c.Traits))
	for i := range c.Traits {
		c.traitByID[c.Traits[i].InternalID] = &c.Traits[i]
		if h, err := ParseHashHex(c.Traits[i].Hash); err == nil {
			c.traitByHash[h] = &c.Traits[i]
		}
	}

	return c, nil
}

func (c *Catalog) LookupSigilByHash(hash uint32) *SigilDef {
	return c.sigilByHash[hash]
}

func (c *Catalog) LookupTraitByHash(hash uint32) *TraitDef {
	return c.traitByHash[hash]
}

func loadJSON[T any](path string) (T, error) {
	var result T
	data, err := dataFiles.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	// 预处理: 去掉 JS 风格的 // 和 /* */ 注释，以及尾部逗号
	cleaned := cleanJSON(string(data))
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return result, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return result, nil
}

func cleanJSON(s string) string {
	// 移除 UTF-8 BOM (EF BB BF)
	s = strings.TrimPrefix(s, "\xEF\xBB\xBF")
	// 移除 /* */ 注释
	for {
		start := strings.Index(s, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+2:], "*/")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+2+end+2:]
	}
	// 移除 // 行注释（仅当 // 前是空白或行首时）
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue // 整行注释，跳过
		}
		// 内联注释: 查找不在字符串内的 //
		inString := false
		commentStart := -1
		for i := 0; i < len(line)-1; i++ {
			if line[i] == '"' && (i == 0 || line[i-1] != '\\') {
				inString = !inString
			}
			if !inString && line[i] == '/' && line[i+1] == '/' {
				commentStart = i
				break
			}
		}
		if commentStart >= 0 {
			// 保留注释前的内容，但去掉尾部空白
			line = strings.TrimRight(line[:commentStart], " \t")
		}
		out = append(out, line)
	}
	s = strings.Join(out, "\n")
	// 移除尾部逗号 (在 ] 或 } 之前的逗号)
	s = strings.ReplaceAll(s, ",\n]", "\n]")
	s = strings.ReplaceAll(s, ",\n}", "\n}")
	s = strings.ReplaceAll(s, ", ]", " ]")
	s = strings.ReplaceAll(s, ", }", " }")
	s = strings.ReplaceAll(s, ",\t]", "\t]")
	s = strings.ReplaceAll(s, ",\t}", "\t}")
	s = strings.ReplaceAll(s, ",  ]", "  ]")
	s = strings.ReplaceAll(s, ",  }", "  }")
	return s
}

func (c *Catalog) RequireSigil(id string) (*SigilDef, error) {
	if s, ok := c.sigilByID[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("未知因子 ID: %s", id)
}

func (c *Catalog) RequireTrait(id string) (*TraitDef, error) {
	if id == "" {
		return nil, fmt.Errorf("缺少已验证的特性 ID")
	}
	if t, ok := c.traitByID[id]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("未知特性 ID: %s", id)
}

func isDraftSigilID(id string) bool {
	parts := strings.Split(id, "_")
	if len(parts) != 3 || strings.ToUpper(parts[0]) != "GEEN" {
		return false
	}
	family, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	if family == 10 {
		return true
	}
	if family >= 114 && family <= 132 {
		suffix := parts[2]
		return suffix == "04" || suffix == "54" || suffix == "64"
	}
	return false
}

func isDraftTraitID(id string) bool {
	return strings.EqualFold(id, "SKILL_010_00")
}

func isSelectableTrait(trait *TraitDef) bool {
	return trait != nil && !isDraftTraitID(trait.InternalID)
}

func supportsGeneratedPlusSigil(sigil *SigilDef) bool {
	return sigil != nil && sigil.SupportsSecondaryTrait != nil && *sigil.SupportsSecondaryTrait
}

func requiresCharacterSigilSecondary(sigil *SigilDef) bool {
	return sigil != nil &&
		strings.EqualFold(derefStr(sigil.Category), "character_sigil") &&
		len(sigil.AllowedSecondaryTraitIDs) > 0
}

func hasUnverifiedSigilNotes(notes string) bool {
	normalized := strings.ToLower(strings.TrimSpace(notes))
	for _, marker := range []string{
		"not verified",
		"unverified",
		"temporary data pass",
		"secondary pool includes every trait",
		"未验证",
		"未经验证",
		"临时全部副词条",
		"临时全词条",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func isVerifiedSigilDefinition(sigil *SigilDef) bool {
	return sigil != nil &&
		strings.EqualFold(strings.TrimSpace(sigil.Confidence), "high") &&
		!hasUnverifiedSigilNotes(sigil.Notes)
}

func naturalSigilLevels(levels []int) []int {
	result := make([]int, 0, len(levels))
	seen := make(map[int]bool, len(levels))
	for _, level := range levels {
		if level < 1 || level > 15 || seen[level] {
			continue
		}
		seen[level] = true
		result = append(result, level)
	}
	sort.Ints(result)
	return result
}

func naturalSigilLevelsForDefinition(sigil *SigilDef, levels []int) []int {
	if sigil != nil && strings.EqualFold(derefStr(sigil.Category), "special_sigil") && isVerifiedSigilDefinition(sigil) {
		return append([]int(nil), levels...)
	}
	return naturalSigilLevels(levels)
}

func maxNaturalSigilLevel(levels []int) int {
	if len(levels) == 0 {
		return 0
	}
	return levels[len(levels)-1]
}

func (c *Catalog) IsSigilConstructible(sigil *SigilDef) bool {
	// Seven Net is a verified retail-DLC record, but its real save flags are
	// 22 while the ordinary constructor deliberately emits normal flags 2.
	// Keep record authenticity separate from safe natural construction.
	if !isVerifiedSigilDefinition(sigil) || strings.EqualFold(sigil.InternalID, "GEEN_142_02") {
		return false
	}
	sigilLevels, err := c.RequireSigilLevels(sigil)
	if err != nil || len(naturalSigilLevelsForDefinition(sigil, sigilLevels)) == 0 {
		return false
	}
	primaryLevels, err := c.RequirePrimaryTraitLevels(sigil)
	if err != nil || len(naturalSigilLevelsForDefinition(sigil, primaryLevels)) == 0 {
		return false
	}
	if !supportsGeneratedPlusSigil(sigil) {
		return true
	}
	if len(sigil.AllowedSecondaryTraitIDs) == 0 {
		return false
	}
	for _, traitID := range sigil.AllowedSecondaryTraitIDs {
		if traitID == sigil.PrimaryTraitID {
			continue
		}
		trait, err := c.RequireTrait(traitID)
		if err != nil || !isSelectableTrait(trait) {
			continue
		}
		levels, err := c.RequireSecondaryTraitLevels(sigil, trait)
		if err == nil && len(naturalSigilLevels(levels)) > 0 {
			return true
		}
	}
	return false
}

func displaySigilName(sigil *SigilDef) string {
	if sigil == nil {
		return ""
	}
	if name := supplementalSigilDisplayName(sigil); name != "" {
		return name
	}
	if supportsGeneratedPlusSigil(sigil) {
		return cnName(sigil.DisplayName)
	}
	return cnName(sigil.DisplayName)
}

func (c *Catalog) GetAllowedSecondaryTraits(sigil *SigilDef) ([]*TraitDef, error) {
	if !supportsGeneratedPlusSigil(sigil) {
		return nil, nil
	}

	disallowed := make(map[string]bool)
	for _, id := range sigil.DisallowedSecondaryTraitIDs {
		disallowed[id] = true
	}

	seen := make(map[string]bool)
	appendIfAllowed := func(result []*TraitDef, trait *TraitDef) []*TraitDef {
		if trait == nil {
			return result
		}
		if seen[trait.InternalID] {
			return result
		}
		if !isSelectableTrait(trait) {
			return result
		}
		if disallowed[trait.InternalID] {
			return result
		}
		if trait.CanAppearAsSecondary != nil && !*trait.CanAppearAsSecondary {
			return result
		}
		if trait.BannedAsSecondaryOnPlusSigils != nil && *trait.BannedAsSecondaryOnPlusSigils {
			return result
		}
		seen[trait.InternalID] = true
		return append(result, trait)
	}

	if len(sigil.AllowedSecondaryTraitIDs) > 0 {
		result := make([]*TraitDef, 0, len(sigil.AllowedSecondaryTraitIDs))
		for _, id := range sigil.AllowedSecondaryTraitIDs {
			trait, err := c.RequireTrait(id)
			if err != nil {
				continue
			}
			result = appendIfAllowed(result, trait)
		}
		return result, nil
	}

	result := make([]*TraitDef, 0, len(c.Traits))
	for i := range c.Traits {
		result = appendIfAllowed(result, &c.Traits[i])
	}
	return result, nil
}

func (c *Catalog) GetDefaultSecondaryTrait(sigil *SigilDef) *TraitDef {
	if sigil.DefaultSecondaryTraitID == nil || *sigil.DefaultSecondaryTraitID == "" {
		return nil
	}
	trait, err := c.RequireTrait(*sigil.DefaultSecondaryTraitID)
	if err != nil {
		return nil
	}
	return trait
}

func (c *Catalog) RequireSigilLevels(sigil *SigilDef) ([]int, error) {
	if len(sigil.AllowedSigilLevels) > 0 {
		return sigil.AllowedSigilLevels, nil
	}
	if sigil.MaxSigilLevel != nil {
		levels := make([]int, *sigil.MaxSigilLevel)
		for i := range levels {
			levels[i] = i + 1
		}
		return levels, nil
	}
	return nil, fmt.Errorf("因子 %s 缺少已验证的等级范围", sigil.DisplayName)
}

func (c *Catalog) RequirePrimaryTraitLevels(sigil *SigilDef) ([]int, error) {
	if len(sigil.AllowedFirstTraitLevels) > 0 {
		return sigil.AllowedFirstTraitLevels, nil
	}
	if sigil.FirstTraitMaxLevel != nil {
		levels := make([]int, *sigil.FirstTraitMaxLevel)
		for i := range levels {
			levels[i] = i + 1
		}
		return levels, nil
	}
	trait, err := c.RequireTrait(sigil.PrimaryTraitID)
	if err != nil {
		return nil, err
	}
	return requireTraitLevels(trait, "主特性")
}

func (c *Catalog) RequireSecondaryTraitLevels(sigil *SigilDef, trait *TraitDef) ([]int, error) {
	if sigil.SecondaryTraitLevelOverrides != nil {
		if override, ok := sigil.SecondaryTraitLevelOverrides[trait.InternalID]; ok {
			if len(override.AllowedLevels) > 0 {
				return override.AllowedLevels, nil
			}
			if override.MaxLevel != nil {
				levels := make([]int, *override.MaxLevel)
				for i := range levels {
					levels[i] = i + 1
				}
				return levels, nil
			}
		}
	}
	return requireTraitLevels(trait, "副特性")
}

func requireTraitLevels(trait *TraitDef, label string) ([]int, error) {
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
	return nil, fmt.Errorf("特性 %s 缺少已验证的等级范围 (%s)", trait.DisplayName, label)
}

func ParseHashHex(s string) (uint32, error) {
	text := strings.TrimPrefix(s, "0x")
	text = strings.TrimPrefix(text, "0X")
	v, err := strconv.ParseUint(text, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("无效的哈希值: %s", s)
	}
	return uint32(v), nil
}

// GetSigilSortedList returns sigils sorted by group then display name (matching C# sort order).
func (c *Catalog) GetSigilSortedList() []*SigilDef {
	sorted := make([]*SigilDef, 0, len(c.Sigils))
	for i := range c.Sigils {
		if isDraftSigilID(c.Sigils[i].InternalID) {
			continue
		}
		sorted = append(sorted, &c.Sigils[i])
	}
	sort.Slice(sorted, func(i, j int) bool {
		gi := sigilSortKey(sorted[i].InternalID)
		gj := sigilSortKey(sorted[j].InternalID)
		if gi != gj {
			return gi < gj
		}
		return sorted[i].DisplayName < sorted[j].DisplayName
	})
	return sorted
}

func sigilSortKey(id string) int {
	if len(id) >= 8 && strings.HasPrefix(strings.ToUpper(id), "GEEN_") {
		// id format: GEEN_XXX_YY
		parts := strings.Split(id, "_")
		if len(parts) >= 3 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				return n
			}
		}
	}
	return 1 << 30 // max int-ish sentinel
}
