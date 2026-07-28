package main

import "testing"

func TestTraitChineseTranslationsCoverWrightstoneOnlyNames(t *testing.T) {
	setCurrentLanguage("zh")
	t.Cleanup(func() { setCurrentLanguage("en") })

	translations := map[string]string{
		"Anomaly Resistance":            "异能耐受",
		"DEF Down Resistance":           "防御DOWN抗性",
		"Frozen Resistance":             "冰冻抗性",
		"Healing Cap":                   "回复性能",
		"Skill Seal Resistance":         "能力封印抗性",
		"Skybound Arts Seal Resistance": "奥义封印抗性",
		"Stun Resistance":               "昏迷抗性",
		"Waterprison Resistance":        "水牢抗性",
	}
	for english, want := range translations {
		if got := cnTrait(english); got != want {
			t.Errorf("cnTrait(%q) = %q, want %q", english, got, want)
		}
	}
}
