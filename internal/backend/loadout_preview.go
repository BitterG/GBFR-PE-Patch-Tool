package backend

import "fmt"

type LoadoutPreviewStats struct {
	UnitID           uint32             `json:"unitId"`
	Slot             int                `json:"slot"`
	IsParty          bool               `json:"isParty"`
	FinalStats       *LoadoutFinalStats `json:"finalStats,omitempty"`
	RuntimeBaseline  bool               `json:"runtimeBaseline"`
	BaselineEvidence string             `json:"baselineEvidence,omitempty"`
	Error            string             `json:"error,omitempty"`
}

// LoadoutPreviewList calculates every saved preset for one character using
// the same simulation path as the editor. The active party loadout is kept
// first. When the target character is available in the running game, its
// three-read-stable fixed growth replaces the save-derived fallback for all
// presets; the current weapon and factors remain specific to each preset.
func (a *App) LoadoutPreviewList(path, charaHex string) ([]LoadoutPreviewStats, error) {
	groups, err := a.LoadoutList(path)
	if err != nil {
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
	parsed, err := LoadSaveFile(path)
	if err != nil {
		return nil, err
	}
	context, err := a.loadoutStatContextFromLoaded(path, charaHex, parsed, save, true)
	if err != nil {
		return nil, err
	}
	var loadouts []LoadoutEntry
	for _, group := range groups {
		if normalizedHashText(group.CharaHash) == normalizedHashText(charaHex) {
			loadouts = group.Loadouts
			break
		}
	}
	if loadouts == nil {
		return nil, fmt.Errorf("存档里找不到角色 %s 的配装", charaHex)
	}
	result := make([]LoadoutPreviewStats, 0, len(loadouts))
	for _, loadout := range loadouts {
		entry := LoadoutPreviewStats{
			UnitID: loadout.UnitID, Slot: loadout.Slot, IsParty: loadout.IsParty,
			RuntimeBaseline:  context.PermanentGrowth.RuntimeObserved,
			BaselineEvidence: context.PermanentGrowth.Evidence,
		}
		sigilSlots := make([]uint32, 0, len(loadout.Sigils))
		for _, sigil := range loadout.Sigils {
			if !sigil.Missing && sigil.SlotID != 0 && sigil.SlotID != EmptyHash {
				sigilSlots = append(sigilSlots, sigil.SlotID)
			}
		}
		mastery := make([]string, 0, len(loadout.Mastery))
		for _, node := range loadout.Mastery {
			mastery = append(mastery, node.Hash)
		}
		simulation, simulationErr := a.loadoutSimulateBuildFromLoaded(
			path, charaHex, loadout.WeaponSlotID, sigilSlots, nil, mastery, context.EquippedSummonSlotIDs,
			cat, save, context, false,
		)
		if simulationErr != nil {
			entry.Error = simulationErr.Error()
		} else {
			entry.FinalStats = simulation.FinalStats
		}
		result = append(result, entry)
	}
	return result, nil
}

func normalizedHashText(value string) string {
	parsed, err := ParseHashHex(value)
	if err != nil {
		return ""
	}
	return hashText(parsed)
}
