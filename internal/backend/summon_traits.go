package backend

import "fmt"

// SummonTraitState is the save/runtime-neutral summon trait payload. Offline
// loadout edits deliberately keep TypeHash unchanged and only persist
// 1458/1459/1460.
type SummonTraitState struct {
	TypeHash       uint32 `json:"typeHash"`
	MainTraitHash  uint32 `json:"mainTraitHash"`
	SubParamHash   uint32 `json:"subParamHash"`
	MainTraitLevel uint32 `json:"mainTraitLevel"`
	SubParamLevel  uint32 `json:"subParamLevel"`
	Rank           uint32 `json:"rank"`
}

func validateSummonTraitChange(catalog *summonStatCatalog, draft, existing SummonTraitState) error {
	if catalog == nil {
		return fmt.Errorf("summon catalog is nil")
	}
	if _, ok := catalog.types[draft.TypeHash]; !ok {
		return fmt.Errorf("unknown summon type 0x%08X", draft.TypeHash)
	}
	if sub, ok := catalog.sub[draft.SubParamHash]; !ok {
		return fmt.Errorf("unknown summon sub parameter 0x%08X", draft.SubParamHash)
	} else {
		if sub.MaxLevel < 0 || len(sub.Values) == 0 || sub.MaxLevel >= len(sub.Values) {
			return fmt.Errorf("summon sub parameter 0x%08X has an invalid level table", draft.SubParamHash)
		}
		limit := uint32(sub.MaxLevel)
		if limit > summonSubParamSafetyMaxLevel {
			limit = summonSubParamSafetyMaxLevel
		}
		if draft.SubParamLevel > limit {
			return fmt.Errorf("summon sub parameter level %d exceeds natural/safety limit %d", draft.SubParamLevel, limit)
		}
	}
	if draft.Rank > 3 {
		return fmt.Errorf("summon rank must be between 0 and 3")
	}
	if existing.TypeHash != EmptyHash {
		if _, knownExistingMain := catalog.main[existing.MainTraitHash]; !knownExistingMain &&
			(draft.MainTraitHash != existing.MainTraitHash || draft.MainTraitLevel != existing.MainTraitLevel) {
			return fmt.Errorf("unknown existing summon main trait 0x%08X level %d must remain unchanged", existing.MainTraitHash, existing.MainTraitLevel)
		}
	}

	if main, ok := catalog.main[draft.MainTraitHash]; ok && main.MaxLevel > 0 {
		limit := uint32(main.MaxLevel)
		if limit > summonMainTraitSafetyMaxLevel {
			limit = summonMainTraitSafetyMaxLevel
		}
		if draft.MainTraitLevel > limit {
			return fmt.Errorf("summon main trait 0x%08X level %d exceeds natural/safety limit %d", draft.MainTraitHash, draft.MainTraitLevel, limit)
		}
	} else if draft.MainTraitHash != existing.MainTraitHash || draft.MainTraitLevel != existing.MainTraitLevel {
		return fmt.Errorf("summon main trait 0x%08X level %d is not an audited natural value", draft.MainTraitHash, draft.MainTraitLevel)
	}
	return validateSummonNaturalChange(draft, existing)
}
