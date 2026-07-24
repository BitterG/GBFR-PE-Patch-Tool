package backend

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	runtimePatchRuntimeOverrideSchemaVersion = 1
	runtimePatchRuntimeOverrideGameVersion   = "2.0.2"
)

//go:embed data/runtime_patch_overrides_2.0.2.json
var runtimePatchRuntimeOverridesJSON []byte

type runtimePatchRuntimeOverrideManifest struct {
	SchemaVersion        int                           `json:"schemaVersion"`
	GameVersion          string                        `json:"gameVersion"`
	GameExecutableSHA256 string                        `json:"gameExecutableSha256"`
	Overrides            []runtimePatchRuntimeOverride `json:"overrides"`
}

type runtimePatchRuntimeOverride struct {
	FeatureID     string             `json:"featureId"`
	EvidenceLevel string             `json:"evidenceLevel"`
	EvidenceNote  string             `json:"evidenceNote"`
	AppendSites   []RuntimePatchSite `json:"appendSites"`
}

func decodeRuntimePatchRuntimeOverrides(raw []byte) (*runtimePatchRuntimeOverrideManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var manifest runtimePatchRuntimeOverrideManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode RuntimePatch runtime overrides: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode RuntimePatch runtime overrides: unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("decode RuntimePatch runtime overrides trailing data: %w", err)
	}
	if manifest.SchemaVersion != runtimePatchRuntimeOverrideSchemaVersion {
		return nil, fmt.Errorf("RuntimePatch runtime override schemaVersion=%d, want %d", manifest.SchemaVersion, runtimePatchRuntimeOverrideSchemaVersion)
	}
	if manifest.GameVersion != runtimePatchRuntimeOverrideGameVersion {
		return nil, fmt.Errorf("RuntimePatch runtime override gameVersion=%q, want %q", manifest.GameVersion, runtimePatchRuntimeOverrideGameVersion)
	}
	if !strings.EqualFold(manifest.GameExecutableSHA256, runtimeCharacterPanelGameEXESHA256) {
		return nil, fmt.Errorf("RuntimePatch runtime override executable identity mismatch")
	}
	if len(manifest.Overrides) == 0 {
		return nil, fmt.Errorf("RuntimePatch runtime overrides are empty")
	}
	return &manifest, nil
}

func applyRuntimePatchRuntimeOverrides(catalog *RuntimePatchCatalog, raw []byte) error {
	if catalog == nil {
		return fmt.Errorf("RuntimePatch runtime override target is nil")
	}
	manifest, err := decodeRuntimePatchRuntimeOverrides(raw)
	if err != nil {
		return err
	}

	byID := make(map[string]*RuntimePatchFeature, len(catalog.Features))
	for index := range catalog.Features {
		byID[catalog.Features[index].ID] = &catalog.Features[index]
	}
	seen := make(map[string]struct{}, len(manifest.Overrides))
	for index, override := range manifest.Overrides {
		if _, duplicate := seen[override.FeatureID]; duplicate {
			return fmt.Errorf("RuntimePatch runtime override[%d] duplicates feature %q", index, override.FeatureID)
		}
		seen[override.FeatureID] = struct{}{}
		feature, exists := byID[override.FeatureID]
		if !exists {
			return fmt.Errorf("RuntimePatch runtime override[%d] references unknown feature %q", index, override.FeatureID)
		}
		if strings.TrimSpace(override.EvidenceLevel) == "" || strings.TrimSpace(override.EvidenceNote) == "" {
			return fmt.Errorf("RuntimePatch runtime override[%d] has incomplete evidence metadata", index)
		}
		if len(override.AppendSites) == 0 {
			return fmt.Errorf("RuntimePatch runtime override[%d] has no appended sites", index)
		}
		feature.EvidenceLevel = override.EvidenceLevel
		feature.EvidenceNote = override.EvidenceNote
		feature.Sites = append(feature.Sites, override.AppendSites...)
	}
	return validateRuntimePatchCatalog(catalog)
}
