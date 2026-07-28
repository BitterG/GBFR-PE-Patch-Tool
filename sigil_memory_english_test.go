package main

import "testing"

func TestSigilGeneratorUsesEnglishMemoryCatalogNames(t *testing.T) {
	setCurrentLanguage("en")
	t.Cleanup(func() { setCurrentLanguage("en") })

	generator := NewSigilGen()
	sigils, err := generator.GetSigilList()
	if err != nil {
		t.Fatalf("GetSigilList() error = %v", err)
	}

	want := map[string]string{
		"MEMORY_SIGIL_EB766D87": "Bladequeen's Warpath V+",
		"MEMORY_SIGIL_7B4AAB30": "Divergence V+",
	}
	for id, displayName := range want {
		found := false
		for _, sigil := range sigils {
			if sigil.InternalID == id {
				found = true
				if sigil.DisplayName != displayName {
					t.Errorf("sigil %s display name = %q, want %q", id, sigil.DisplayName, displayName)
				}
			}
		}
		if !found {
			t.Errorf("sigil %s is missing", id)
		}
	}
}
