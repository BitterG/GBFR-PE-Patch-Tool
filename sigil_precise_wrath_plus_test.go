package main

import "testing"

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
