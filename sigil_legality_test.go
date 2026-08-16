package main

import "testing"

func loadTestLegality(t *testing.T) *SigilLegalityData {
	t.Helper()
	d, err := LoadSigilLegality()
	if err != nil {
		t.Fatalf("LoadSigilLegality: %v", err)
	}
	return d
}

func hasRule(issues []SigilLegalityIssue, rule string) bool {
	for _, i := range issues {
		if i.Rule == rule {
			return true
		}
	}
	return false
}

func TestSigilLegality_LockedPairSilentOnExactPair(t *testing.T) {
	d := loadTestLegality(t)
	// Thunderwolf's Awakening+ locks (Recharge, Acuity).
	issues := d.CheckSigilLegality(0x23953FD4, 0x7D75D904, 0xBE3404B9)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
}

func TestSigilLegality_LockedPairAccusesWrongTraits(t *testing.T) {
	d := loadTestLegality(t)
	// Wrong secondary.
	sec := d.CheckSigilLegality(0x23953FD4, 0x7D75D904, 0x0053599E)
	if len(sec) != 1 || sec[0].Rule != "lockedPairSecondary" {
		t.Fatalf("expected lockedPairSecondary, got %+v", sec)
	}
	// Wrong primary.
	pri := d.CheckSigilLegality(0x23953FD4, 0xDC584F60, 0xBE3404B9)
	if len(pri) != 1 || pri[0].Rule != "lockedPairPrimary" {
		t.Fatalf("expected lockedPairPrimary, got %+v", pri)
	}
}

func TestSigilLegality_LockedPairEmptySlotIsSilent(t *testing.T) {
	d := loadTestLegality(t)
	issues := d.CheckSigilLegality(0x23953FD4, 0x7D75D904, 0) // empty secondary
	if len(issues) != 0 {
		t.Fatalf("empty slot should be silent, got %+v", issues)
	}
}

func TestSigilLegality_SingleTraitSigilRejectsSecondTrait(t *testing.T) {
	d := loadTestLegality(t)
	issues := d.CheckSigilLegality(0xCB5F29C1, 0xA1A8E39D, 0xDC584F60) // Stout Heart + DMG Cap
	if len(issues) != 1 || issues[0].Rule != "singleTrait" {
		t.Fatalf("expected singleTrait, got %+v", issues)
	}
	// No second trait is fine.
	if got := d.CheckSigilLegality(0xCB5F29C1, 0xA1A8E39D, 0); len(got) != 0 {
		t.Fatalf("expected silent, got %+v", got)
	}
}

func TestSigilLegality_QuestLockedTraitMustStayHome(t *testing.T) {
	d := loadTestLegality(t)
	// Crabby Resonance on an unrelated sigil (its primary also mismatches the
	// sigil's intrinsic trait, but the quest-locked rule must still fire).
	issues := d.CheckSigilLegality(0x2D7F2E70, 0x082033CB, 0)
	if !hasRule(issues, "questLockedTrait") {
		t.Fatalf("expected questLockedTrait, got %+v", issues)
	}
	// On its own home sigil is silent.
	home := d.CheckSigilLegality(0x1C4D37E4, 0x082033CB, 0)
	if len(home) != 0 {
		t.Fatalf("home sigil should be silent, got %+v", home)
	}
}

func TestSigilLegality_PrimaryTraitMismatch(t *testing.T) {
	d := loadTestLegality(t)
	// Attack Power V+ (intrinsic ATK 0x50079A1C) with a wrong primary trait.
	issues := d.CheckSigilLegality(0x2D7F2E70, 0xDC584F60, 0)
	if len(issues) != 1 || issues[0].Rule != "primaryTraitMismatch" {
		t.Fatalf("expected primaryTraitMismatch, got %+v", issues)
	}
	// The intrinsic primary trait is silent.
	if got := d.CheckSigilLegality(0x2D7F2E70, 0x50079A1C, 0); len(got) != 0 {
		t.Fatalf("correct primary should be silent, got %+v", got)
	}
}

func TestSigilLegality_OrdinaryVPlusSecondTraitIsNeverAccused(t *testing.T) {
	d := loadTestLegality(t)
	// A plain V+ with an arbitrary (non-crab, non-locked) second trait.
	for _, second := range []uint32{0xDC584F60, 0x0053599E, 0xDEADBEEF} {
		issues := d.CheckSigilLegality(0x2D7F2E70, 0x50079A1C, second)
		if len(issues) != 0 {
			t.Fatalf("ordinary V+ second trait %08X must not be accused, got %+v", second, issues)
		}
	}
}
