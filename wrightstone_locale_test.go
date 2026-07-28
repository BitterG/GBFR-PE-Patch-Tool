package main

import "testing"

func TestWrightstoneTraitListUsesChineseBlessingTranslations(t *testing.T) {
	setCurrentLanguage("zh")
	t.Cleanup(func() { setCurrentLanguage("en") })

	traits, err := NewWrightstoneGen().GetTraitList()
	if err != nil {
		t.Fatalf("GetTraitList() error = %v", err)
	}

	want := map[string]string{
		"WRIGHTSTONE_EXTRA_ANOMALY_RESISTANCE":            "异能耐受",
		"WRIGHTSTONE_EXTRA_DEF_DOWN_RESISTANCE":           "防御DOWN抗性",
		"WRIGHTSTONE_EXTRA_FROZEN_RESISTANCE":             "冰冻抗性",
		"WRIGHTSTONE_EXTRA_HEALING_CAP":                   "回复性能",
		"WRIGHTSTONE_EXTRA_SKILL_SEAL_RESISTANCE":         "能力封印抗性",
		"WRIGHTSTONE_EXTRA_SKYBOUND_ARTS_SEAL_RESISTANCE": "奥义封印抗性",
		"WRIGHTSTONE_EXTRA_STUN_RESISTANCE":               "昏迷抗性",
		"WRIGHTSTONE_EXTRA_WATERPRISON_RESISTANCE":        "水牢抗性",
	}
	got := make(map[string]string, len(want))
	for _, trait := range traits {
		if displayName, ok := want[trait.InternalID]; ok {
			got[trait.InternalID] = trait.DisplayName
			if trait.DisplayName != displayName {
				t.Errorf("trait %s display name = %q, want %q", trait.InternalID, trait.DisplayName, displayName)
			}
		}
	}

	for internalID := range want {
		if _, ok := got[internalID]; !ok {
			t.Errorf("trait %s is missing from GetTraitList()", internalID)
		}
	}
}
