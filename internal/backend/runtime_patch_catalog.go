package backend

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	runtimePatchCatalogSchemaVersion  = 3
	runtimePatchCatalogGameVersion    = "2.0.2"
	runtimePatchCatalogGameSHA256     = "63340832BCF731FBC97796F686B05C988418E83D451D4A49B2244A85D00E297F"
	runtimePatchCatalogFeatureCount   = 58
	runtimePatchDamageCapConflictName = "damage-cap-display"
)

//go:embed data/runtime_patch_catalog.json
var runtimePatchCatalogJSON []byte

type RuntimePatchCatalog struct {
	SchemaVersion        int                   `json:"schemaVersion"`
	GameVersion          string                `json:"gameVersion"`
	GameExecutableSHA256 string                `json:"gameExecutableSha256"`
	Features             []RuntimePatchFeature `json:"features"`
}

type RuntimePatchFeature struct {
	ID            string             `json:"id"`
	CatalogID     int                `json:"catalogId"`
	Name          string             `json:"name"`
	DisplayName   string             `json:"displayName"`
	Mode          string             `json:"mode"`
	Category      string             `json:"category"`
	Group         string             `json:"group"`
	GroupPath     []string           `json:"groupPath"`
	Character     string             `json:"character"`
	Conflicts     []string           `json:"conflicts"`
	ConflictGroup string             `json:"conflictGroup"`
	EvidenceLevel string             `json:"evidenceLevel,omitempty"`
	EvidenceNote  string             `json:"evidenceNote,omitempty"`
	Sites         []RuntimePatchSite `json:"sites"`
}

// RuntimePatchSite contains the signature and bytes for one direct patch.
type RuntimePatchSite struct {
	Symbol                 string `json:"symbol"`
	Module                 string `json:"module"`
	AOB                    string `json:"aob"`
	Offset                 int    `json:"offset"`
	PatternValues          []byte `json:"patternValues"`
	PatternMasks           []byte `json:"patternMasks"`
	EnableBytes            []byte `json:"enableBytes"`
	ExpectedOriginalBytes  []byte `json:"expectedOriginalBytes"`
	DisableBytes           []byte `json:"disableBytes"`
	RequiresRuntimeCapture bool   `json:"requiresRuntimeCapture"`
}

type runtimePatchCatalogPatchRange struct {
	start        int
	end          int
	featureID    string
	featureIndex int
	siteIndex    int
}

var (
	runtimePatchCatalogOnce sync.Once
	runtimePatchCatalogData *RuntimePatchCatalog
	runtimePatchCatalogErr  error
)

func decodeRuntimePatchCatalog(raw []byte) (*RuntimePatchCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var catalog RuntimePatchCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode RuntimePatch catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode RuntimePatch catalog: unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("decode RuntimePatch catalog trailing data: %w", err)
	}
	if err := validateRuntimePatchCatalog(&catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func loadRuntimePatchCatalog() (*RuntimePatchCatalog, error) {
	runtimePatchCatalogOnce.Do(func() {
		runtimePatchCatalogData, runtimePatchCatalogErr = decodeRuntimePatchCatalog(runtimePatchCatalogJSON)
		if runtimePatchCatalogErr == nil {
			runtimePatchCatalogErr = applyRuntimePatchRuntimeOverrides(runtimePatchCatalogData, runtimePatchRuntimeOverridesJSON)
		}
	})
	if runtimePatchCatalogErr != nil {
		return nil, runtimePatchCatalogErr
	}
	return cloneRuntimePatchCatalog(runtimePatchCatalogData), nil
}

// RuntimePatchGetCatalog returns an independently mutable copy for the Wails caller.
func (a *App) RuntimePatchGetCatalog() ([]RuntimePatchFeature, error) {
	catalog, err := loadRuntimePatchCatalog()
	if err != nil {
		return nil, err
	}
	return catalog.Features, nil
}

func validateRuntimePatchCatalog(catalog *RuntimePatchCatalog) error {
	if catalog == nil {
		return fmt.Errorf("RuntimePatch catalog is nil")
	}
	if catalog.SchemaVersion != runtimePatchCatalogSchemaVersion {
		return fmt.Errorf("RuntimePatch catalog schemaVersion=%d, want %d", catalog.SchemaVersion, runtimePatchCatalogSchemaVersion)
	}
	if catalog.GameVersion != runtimePatchCatalogGameVersion {
		return fmt.Errorf("runtime patch catalog gameVersion=%q, want %q", catalog.GameVersion, runtimePatchCatalogGameVersion)
	}
	if !strings.EqualFold(catalog.GameExecutableSHA256, runtimePatchCatalogGameSHA256) {
		return fmt.Errorf("runtime patch catalog executable identity mismatch")
	}
	if len(catalog.Features) != runtimePatchCatalogFeatureCount {
		return fmt.Errorf("RuntimePatch catalog has %d features, want %d", len(catalog.Features), runtimePatchCatalogFeatureCount)
	}

	byID := make(map[string]*RuntimePatchFeature, len(catalog.Features))
	seenCatalogIDs := make(map[int]string, len(catalog.Features))
	patchRanges := make(map[[3]string][]runtimePatchCatalogPatchRange)
	for featureIndex := range catalog.Features {
		feature := &catalog.Features[featureIndex]
		label := fmt.Sprintf("feature[%d] %q", featureIndex, feature.ID)
		wantID := fmt.Sprintf("runtime-patch-%03d", feature.CatalogID)
		if feature.ID != wantID {
			return fmt.Errorf("%s: id must be %q", label, wantID)
		}
		if previous, exists := byID[feature.ID]; exists {
			return fmt.Errorf("%s: duplicate id also used by catalog entry %d", label, previous.CatalogID)
		}
		if previousID, exists := seenCatalogIDs[feature.CatalogID]; exists {
			return fmt.Errorf("%s: duplicate catalogId also used by %q", label, previousID)
		}
		byID[feature.ID] = feature
		seenCatalogIDs[feature.CatalogID] = feature.ID

		if strings.TrimSpace(feature.Name) == "" {
			return fmt.Errorf("%s: name is empty", label)
		}
		if feature.DisplayName != feature.Name {
			return fmt.Errorf("%s: displayName must match name", label)
		}
		switch feature.Mode {
		case "combat", "characters", "quest":
		default:
			return fmt.Errorf("%s: invalid mode %q", label, feature.Mode)
		}
		if feature.Category != feature.Mode {
			return fmt.Errorf("%s: category must match mode", label)
		}
		if strings.TrimSpace(feature.Group) == "" {
			return fmt.Errorf("%s: group is empty", label)
		}
		if (strings.TrimSpace(feature.EvidenceLevel) == "") != (strings.TrimSpace(feature.EvidenceNote) == "") {
			return fmt.Errorf("%s: evidenceLevel and evidenceNote must be provided together", label)
		}
		if len(feature.Sites) == 0 {
			return fmt.Errorf("%s: patch sites are empty", label)
		}
		if err := validateRuntimePatchSites(feature, featureIndex, label, patchRanges); err != nil {
			return err
		}
	}

	for featureIndex := range catalog.Features {
		feature := &catalog.Features[featureIndex]
		label := fmt.Sprintf("feature[%d] %q", featureIndex, feature.ID)
		seen := make(map[string]struct{}, len(feature.Conflicts))
		for _, targetID := range feature.Conflicts {
			if targetID == feature.ID {
				return fmt.Errorf("%s: conflict target cannot reference itself", label)
			}
			if _, duplicate := seen[targetID]; duplicate {
				return fmt.Errorf("%s: duplicate conflict target %q", label, targetID)
			}
			seen[targetID] = struct{}{}
			target, exists := byID[targetID]
			if !exists {
				return fmt.Errorf("%s: conflict target %q does not exist", label, targetID)
			}
			if !containsRuntimePatchString(target.Conflicts, feature.ID) {
				return fmt.Errorf("%s: conflict with %q is not symmetric", label, targetID)
			}
		}
	}

	return validateRuntimePatchDamageCapConflicts(byID)
}

func validateRuntimePatchSites(feature *RuntimePatchFeature, featureIndex int, featureLabel string, ranges map[[3]string][]runtimePatchCatalogPatchRange) error {
	for siteIndex := range feature.Sites {
		site := &feature.Sites[siteIndex]
		label := fmt.Sprintf("%s site[%d]", featureLabel, siteIndex)
		if strings.TrimSpace(site.Symbol) == "" {
			return fmt.Errorf("%s: symbol is empty", label)
		}
		if strings.TrimSpace(site.Module) == "" {
			return fmt.Errorf("%s: module is empty", label)
		}
		if strings.TrimSpace(site.AOB) == "" {
			return fmt.Errorf("%s: AOB is empty", label)
		}

		pattern, err := parseRuntimePatchPattern(site.AOB)
		if err != nil {
			return fmt.Errorf("%s: invalid AOB: %w", label, err)
		}
		if err := validateRuntimePatchPattern(pattern); err != nil {
			return fmt.Errorf("%s: invalid AOB: %w", label, err)
		}
		if canonicalRuntimePatchAOB(pattern) != site.AOB {
			return fmt.Errorf("%s: AOB is not canonical", label)
		}
		if !bytes.Equal(site.PatternValues, pattern.Values) {
			return fmt.Errorf("%s: patternValues do not match AOB", label)
		}
		if !bytes.Equal(site.PatternMasks, pattern.Mask) {
			return fmt.Errorf("%s: patternMasks do not match AOB", label)
		}
		if err := validateRuntimePatchPattern(runtimePatchPattern{Values: site.PatternValues, Mask: site.PatternMasks}); err != nil {
			return fmt.Errorf("%s: noncanonical pattern arrays: %w", label, err)
		}
		if site.Offset < 0 {
			return fmt.Errorf("%s: offset=%d is negative", label, site.Offset)
		}
		if len(site.EnableBytes) == 0 {
			return fmt.Errorf("%s: enableBytes are empty", label)
		}
		if len(site.ExpectedOriginalBytes) != len(site.EnableBytes) {
			return fmt.Errorf("%s: expectedOriginalBytes length=%d, want %d", label, len(site.ExpectedOriginalBytes), len(site.EnableBytes))
		}
		if bytes.Equal(site.ExpectedOriginalBytes, site.EnableBytes) {
			return fmt.Errorf("%s: expectedOriginalBytes already equal enableBytes", label)
		}
		if site.Offset > len(pattern.Values) || len(site.EnableBytes) > len(pattern.Values)-site.Offset {
			return fmt.Errorf("%s: patch range [%d,%d) exceeds AOB length %d", label, site.Offset, site.Offset+len(site.EnableBytes), len(pattern.Values))
		}
		for byteIndex, expected := range site.ExpectedOriginalBytes {
			patternIndex := site.Offset + byteIndex
			if expected&pattern.Mask[patternIndex] != pattern.Values[patternIndex] {
				return fmt.Errorf("%s: expectedOriginalBytes[%d] contradicts exact AOB bits", label, byteIndex)
			}
		}
		if site.RequiresRuntimeCapture {
			if len(site.DisableBytes) != 0 {
				return fmt.Errorf("%s: runtime-capture site must not contain disableBytes", label)
			}
		} else if len(site.DisableBytes) != len(site.EnableBytes) {
			return fmt.Errorf("%s: disableBytes length=%d, want %d", label, len(site.DisableBytes), len(site.EnableBytes))
		} else if !bytes.Equal(site.DisableBytes, site.ExpectedOriginalBytes) {
			return fmt.Errorf("%s: disableBytes do not match expectedOriginalBytes", label)
		}

		key := [3]string{site.Module, site.Symbol, site.AOB}
		current := runtimePatchCatalogPatchRange{
			start:        site.Offset,
			end:          site.Offset + len(site.EnableBytes),
			featureID:    feature.ID,
			featureIndex: featureIndex,
			siteIndex:    siteIndex,
		}
		for _, previous := range ranges[key] {
			if current.start < previous.end && previous.start < current.end {
				return fmt.Errorf(
					"%s: patch range overlaps feature[%d] %q site[%d] for the same module/symbol/AOB",
					label, previous.featureIndex, previous.featureID, previous.siteIndex,
				)
			}
		}
		ranges[key] = append(ranges[key], current)
	}
	return nil
}

func validateRuntimePatchDamageCapConflicts(byID map[string]*RuntimePatchFeature) error {
	ids := []string{"runtime-patch-015", "runtime-patch-021", "runtime-patch-029"}
	for _, id := range ids {
		feature, exists := byID[id]
		if !exists {
			return fmt.Errorf("required damage-cap feature %q is missing", id)
		}
		if feature.ConflictGroup != runtimePatchDamageCapConflictName {
			return fmt.Errorf("feature %q: conflictGroup=%q, want %q", id, feature.ConflictGroup, runtimePatchDamageCapConflictName)
		}
		for _, otherID := range ids {
			if id != otherID && !containsRuntimePatchString(feature.Conflicts, otherID) {
				return fmt.Errorf("feature %q: missing required conflict with %q", id, otherID)
			}
		}
	}
	return nil
}

func canonicalRuntimePatchAOB(pattern runtimePatchPattern) string {
	tokens := make([]string, len(pattern.Values))
	const hex = "0123456789ABCDEF"
	for index, value := range pattern.Values {
		switch pattern.Mask[index] {
		case 0xff:
			tokens[index] = string([]byte{hex[value>>4], hex[value&0x0f]})
		case 0xf0:
			tokens[index] = string([]byte{hex[value>>4], '?'})
		case 0x0f:
			tokens[index] = string([]byte{'?', hex[value&0x0f]})
		case 0x00:
			tokens[index] = "??"
		}
	}
	return strings.Join(tokens, " ")
}

func cloneRuntimePatchCatalog(source *RuntimePatchCatalog) *RuntimePatchCatalog {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Features = make([]RuntimePatchFeature, len(source.Features))
	for featureIndex := range source.Features {
		cloned.Features[featureIndex] = source.Features[featureIndex]
		feature := &cloned.Features[featureIndex]
		feature.GroupPath = make([]string, len(source.Features[featureIndex].GroupPath))
		copy(feature.GroupPath, source.Features[featureIndex].GroupPath)
		feature.Conflicts = make([]string, len(source.Features[featureIndex].Conflicts))
		copy(feature.Conflicts, source.Features[featureIndex].Conflicts)
		feature.Sites = make([]RuntimePatchSite, len(source.Features[featureIndex].Sites))
		for siteIndex := range source.Features[featureIndex].Sites {
			feature.Sites[siteIndex] = source.Features[featureIndex].Sites[siteIndex]
			site := &feature.Sites[siteIndex]
			sourceSite := &source.Features[featureIndex].Sites[siteIndex]
			site.PatternValues = make([]byte, len(sourceSite.PatternValues))
			copy(site.PatternValues, sourceSite.PatternValues)
			site.PatternMasks = make([]byte, len(sourceSite.PatternMasks))
			copy(site.PatternMasks, sourceSite.PatternMasks)
			site.EnableBytes = make([]byte, len(sourceSite.EnableBytes))
			copy(site.EnableBytes, sourceSite.EnableBytes)
			site.ExpectedOriginalBytes = make([]byte, len(sourceSite.ExpectedOriginalBytes))
			copy(site.ExpectedOriginalBytes, sourceSite.ExpectedOriginalBytes)
			site.DisableBytes = make([]byte, len(sourceSite.DisableBytes))
			copy(site.DisableBytes, sourceSite.DisableBytes)
		}
	}
	return &cloned
}

func containsRuntimePatchString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
