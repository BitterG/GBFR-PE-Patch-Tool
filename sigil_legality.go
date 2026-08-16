package main

import (
	"encoding/json"
	"fmt"
)

// SigilLegalityData carries the four user-confirmed sigil legality rules,
// ported from relink-logs src-tauri/src/legality/sigils.rs. Only these
// "definitely illegal" cases are judged; an ordinary V+ second trait is never
// accused, and a single-trait sigil's level is never judged.
type SigilLegalityData struct {
	// LockedPairs maps a character sigil hash (hex8) to its fixed
	// [primaryTraitHash, secondaryTraitHash] pair. Any deviation is impossible.
	LockedPairs map[string][]string `json:"lockedPairs"`
	// SingleTraitSigils are sigil hashes that can never carry a second trait.
	SingleTraitSigils []string `json:"singleTraitSigils"`
	// QuestLockedTraits are the crab trait hashes that exist only on their own
	// quest sigils.
	QuestLockedTraits []string `json:"questLockedTraits"`
	// QuestLockedHomes maps each quest-locked trait hash to the sigil hashes
	// that may carry it.
	QuestLockedHomes map[string][]string `json:"questLockedHomes"`
	// IntrinsicPrimaryTraits maps a sigil hash to its intrinsic first-trait
	// hash (from the local sigils.json primaryTraitId). Used for the generator
	// only — a stricter check than relink-logs, which deliberately does not
	// accuse on the trait1 column.
	IntrinsicPrimaryTraits map[string]string `json:"intrinsicPrimaryTraits"`
}

// SigilLegalityIssue is one rule violation, kept numeric/hash-only so the
// frontend can translate ids to display names.
type SigilLegalityIssue struct {
	Rule          string   `json:"rule"` // lockedPairPrimary | lockedPairSecondary | singleTrait | questLockedTrait
	ObservedHash  string   `json:"observedHash"`
	AllowedHashes []string `json:"allowedHashes,omitempty"`
}

var cachedLegality *SigilLegalityData

// LoadSigilLegality reads and caches the baked legality rules.
func LoadSigilLegality() (*SigilLegalityData, error) {
	if cachedLegality != nil {
		return cachedLegality, nil
	}
	raw, err := dataFiles.ReadFile("data/sigil-legality.json")
	if err != nil {
		return nil, fmt.Errorf("读取因子合法性数据失败: %w", err)
	}
	var d SigilLegalityData
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("解析因子合法性数据失败: %w", err)
	}
	cachedLegality = &d
	return cachedLegality, nil
}

// GetSigilLegalityData exposes the rules to the frontend.
func (a *App) GetSigilLegalityData() (SigilLegalityData, error) {
	d, err := LoadSigilLegality()
	if err != nil {
		return SigilLegalityData{}, err
	}
	return *d, nil
}

// CheckSigilLegality judges one sigil's trait pair against the four relink-logs
// rules plus one generator-only rule (intrinsic primary-trait match). A zero or
// empty hash is missing data, not a violation; it returns issues only for the
// cases the rules can prove.
func (d *SigilLegalityData) CheckSigilLegality(sigilHash, primaryTraitHash, secondaryTraitHash uint32) []SigilLegalityIssue {
	var issues []SigilLegalityIssue
	sh := hex8(sigilHash)
	isEmpty := func(v uint32) bool { return v == 0 || v == EmptyHash }

	// Locked pairs: a character sigil fixes both traits.
	if pair, ok := d.LockedPairs[sh]; ok && len(pair) == 2 {
		if !isEmpty(primaryTraitHash) && hex8(primaryTraitHash) != pair[0] {
			issues = append(issues, SigilLegalityIssue{
				Rule:          "lockedPairPrimary",
				ObservedHash:  hex8(primaryTraitHash),
				AllowedHashes: []string{pair[0]},
			})
		}
		if !isEmpty(secondaryTraitHash) && hex8(secondaryTraitHash) != pair[1] {
			issues = append(issues, SigilLegalityIssue{
				Rule:          "lockedPairSecondary",
				ObservedHash:  hex8(secondaryTraitHash),
				AllowedHashes: []string{pair[1]},
			})
		}
	} else if intrinsic, ok := d.IntrinsicPrimaryTraits[sh]; ok {
		// Intrinsic primary-trait match (generator-only): a non-locked sigil
		// must keep its intrinsic first trait. Locked pairs already cover
		// their own primary trait, so this skips them.
		if !isEmpty(primaryTraitHash) && hex8(primaryTraitHash) != intrinsic {
			issues = append(issues, SigilLegalityIssue{
				Rule:          "primaryTraitMismatch",
				ObservedHash:  hex8(primaryTraitHash),
				AllowedHashes: []string{intrinsic},
			})
		}
	}

	// Single-trait sigils: Stout Heart / Stout Heart+ / Immortal Shell.
	if !isEmpty(secondaryTraitHash) && containsStr(d.SingleTraitSigils, sh) {
		issues = append(issues, SigilLegalityIssue{
			Rule:         "singleTrait",
			ObservedHash: hex8(secondaryTraitHash),
		})
	}

	// Quest-locked (crab) traits must stay on their own sigils.
	for _, th := range []uint32{primaryTraitHash, secondaryTraitHash} {
		if isEmpty(th) {
			continue
		}
		homes, ok := d.QuestLockedHomes[hex8(th)]
		if !ok || containsStr(homes, sh) {
			continue
		}
		issues = append(issues, SigilLegalityIssue{
			Rule:          "questLockedTrait",
			ObservedHash:  hex8(th),
			AllowedHashes: homes,
		})
	}

	return issues
}

// hex8 formats a uint32 as an uppercase 0x-prefixed hex string, matching the
// keys baked into data/sigil-legality.json.
func hex8(v uint32) string {
	return fmt.Sprintf("0x%08X", v)
}

func containsStr(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}
