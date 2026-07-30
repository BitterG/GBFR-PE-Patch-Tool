package main

import (
	"strings"
	"testing"
)

func TestSigilMemoryOptionsTranslateSupplementalNames(t *testing.T) {
	setCurrentLanguage("zh")
	defer setCurrentLanguage("en")

	want := map[string]string{
		"DMG Cap Ecru": "伤害上限·轰天", "DMG Cap Sage": "伤害上限·疾天",
		"DMG Cap Cobalt": "伤害上限·苍天", "DMG Cap Cardinal": "伤害上限·红天",
		"Supernova": "超新星", "Unbound Strike": "超凡强击",
		"Unbound Technique": "超凡技艺", "Unbound Exertion": "超凡奥秘",
		"Unbound Master": "超凡破限", "Catastrophe Nova": "浩劫新星",
	}
	for english, chinese := range want {
		if got := cnTrait(english); got != chinese {
			t.Fatalf("cnTrait(%q) = %q, want %q", english, got, chinese)
		}
	}
}

func TestCatalogIntegratesRuntimeSigilsWithVerifiedPrimaryTraits(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		hash      uint32
		primaryID string
		maxLevel  int
		display   string
	}{
		{0xD8C61507, "SKILL_176_02", 15, "Thunderwolf's Warpath+"},
		{0x23953FD4, "SKILL_176_00", 15, "Thunderwolf's Awakening+"},
		{0x5A360EA8, "SKILL_177_00", 15, "Enchantress's Awakening+"},
		{0x49434696, "SKILL_301_00", 20, "Dread Black Pincer Crab Sigil"},
		{0xB289A9AD, "SKILL_303_00", 5, "Sumo Power"},
	}
	for _, check := range checks {
		sigil := catalog.LookupSigilByHash(check.hash)
		if sigil == nil {
			t.Fatalf("runtime sigil 0x%08X is missing", check.hash)
		}
		if sigil.PrimaryTraitID != check.primaryID || sigil.MaxSigilLevel == nil || *sigil.MaxSigilLevel != check.maxLevel || sigil.DisplayName != check.display {
			t.Fatalf("runtime sigil 0x%08X = %+v, want primary=%s level=%d display=%q", check.hash, sigil, check.primaryID, check.maxLevel, check.display)
		}
	}
}

func TestSigilMemoryOptionsExcludeSupplementDuplicatesAndShortenVPlus(t *testing.T) {
	setCurrentLanguage("zh")
	defer setCurrentLanguage("en")

	options, err := NewApp().SigilMemoryGetOptions()
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[uint32]int, len(options.Sigils))
	var thunderwolf *SigilMemoryOption
	for i := range options.Sigils {
		option := &options.Sigils[i]
		seen[option.Hash]++
		if option.Source != "catalog" {
			t.Fatalf("option %q has source %q, want catalog", option.DisplayName, option.Source)
		}
		if strings.Contains(option.DisplayName, "[补]") {
			t.Fatalf("option %q must not have a supplement label", option.DisplayName)
		}
		if option.Hash == 0xD8C61507 {
			thunderwolf = option
		}
	}
	if seen[0xD8C61507] != 1 {
		t.Fatalf("Thunderwolf's Warpath appears %d times, want once", seen[0xD8C61507])
	}
	if thunderwolf == nil {
		t.Fatal("Thunderwolf's Warpath is missing")
	}
	if thunderwolf.DisplayName != "雷狼的战气+" {
		t.Fatalf("DisplayName = %q, hash = 0x%08X, want %q", thunderwolf.DisplayName, thunderwolf.Hash, "雷狼的战气+")
	}
}

func TestCatalogIncludesPreciseWrathVPlus(t *testing.T) {
	setCurrentLanguage("zh")
	defer setCurrentLanguage("en")

	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for _, candidate := range catalog.Sigils {
		if candidate.DisplayName == "Precise Wrath V+" || candidate.Hash == "0x46ABA3C0" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("found %d Precise Wrath V+ candidates, want exactly 1", seen)
	}

	sigil := catalog.LookupSigilByHash(0xCE6C62CF)
	if sigil == nil {
		t.Fatal("Precise Wrath V+ (0xCE6C62CF) is missing from the catalog")
	}
	if got := displaySigilName(sigil); got != "怒发冲冠V+" {
		t.Fatalf("displaySigilName() = %q, want %q", got, "怒发冲冠V+")
	}
	if !supportsGeneratedPlusSigil(sigil) {
		t.Fatal("Precise Wrath V+ must support a secondary trait")
	}
}

func TestSigilMemoryOptionsIncludePreciseWrathVPlus(t *testing.T) {
	setCurrentLanguage("zh")
	defer setCurrentLanguage("en")

	options, err := NewApp().SigilMemoryGetOptions()
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for _, sigil := range options.Sigils {
		if sigil.Hash == 0x46ABA3C0 {
			t.Fatal("legacy Precise Wrath hash 0x46ABA3C0 must not be offered")
		}
		if sigil.Hash == 0xCE6C62CF {
			seen++
			if sigil.DisplayName != "怒发冲冠V+" {
				t.Fatalf("DisplayName = %q, want %q", sigil.DisplayName, "怒发冲冠V+")
			}
			if sigil.SupportsSecondaryTrait == nil || !*sigil.SupportsSecondaryTrait {
				t.Fatal("Precise Wrath V+ must support a secondary trait")
			}
		}
	}
	if seen != 1 {
		t.Fatalf("found %d Precise Wrath V+ options, want exactly 1", seen)
	}
}
