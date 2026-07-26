package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gbfrPlayerInfoEdit/internal/backend"
)

func TestReadLogsSigilLoadoutsJSONWithMasteryIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs-export.json")
	json := `{
		"displayName":"Player", "characterType":"Pl0400", "weaponKey":"WEP_PL0400_02_01",
		"sigils":[{"firstTraitId":1,"firstTraitLevel":15,"secondTraitId":2,"secondTraitLevel":15,"sigilId":3,"sigilLevel":15}],
		"summons":[{"summonId":31},{"summonId":32},{"summonId":33},{"summonId":34}],
		"abilities":[11,12], "masterLevel":55, "skillboard":[10,11],
		"stats":{"level":100,"hp":1000,"attack":2000},
		"weaponState":{"weaponId":41,"exp":42,"transcendence":7}, "overmasteryInfo":{"overmasteries":[]}
	}`
	if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := readLogsSigilLoadoutsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].LogTime != 0 || result[0].QuestName != "Logs 导出 JSON" || len(result[0].Loadouts) != 1 {
		t.Fatalf("unexpected import: %#v", result)
	}
	player := result[0].Loadouts[0]
	if player.CharacterType != "PL0400" || len(player.Entries) != 1 {
		t.Fatalf("unexpected player: %#v", player)
	}
	entry := player.Entries[0]
	if entry.SigilHash != 3 || entry.PrimaryTraitHash != 1 || entry.SecondaryTraitHash != 2 {
		t.Fatalf("unexpected entry: %#v", entry)
	}
	loadout := player.Loadout
	if loadout == nil || loadout.WeaponHash != "00000029" || len(loadout.Summons) != 4 || len(loadout.Skills) != 2 || len(loadout.LogsSkillboardEffectUIIDs) != 2 || loadout.LogsSkillboardEffectUIIDs[0] != 10 || loadout.LogsSkillboardEffectUIIDs[1] != 11 {
		t.Fatalf("unexpected complete loadout draft: %#v", loadout)
	}
	if loadout.Character == nil || loadout.Character.MasterTotalMSP != 3309499 {
		t.Fatalf("Logs MLv55 must use its minimum MSP threshold: %#v", loadout.Character)
	}
	if loadout.Weapon == nil || !loadout.Weapon.EnhancementCaptured || loadout.Weapon.Transcendence != 7 {
		t.Fatalf("Logs transcendence must be preserved as a complete enhancement snapshot: %#v", loadout.Weapon)
	}
	if loadout.Weapon.Uncap != 0 || loadout.Weapon.Mirage != 0 || loadout.Weapon.Awakening != 0 {
		t.Fatalf("Logs must preserve zero-valued enhancement fields when transcendence is present: %#v", loadout.Weapon)
	}
	expected, err := backend.NewApp().MapLogsMasteryEffectUIIDs("PL0400", loadout.LogsSkillboardEffectUIIDs)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadout.MasteryHashes) != len(expected.Hashes) {
		t.Fatalf("converter mapped an unexpected number of safe hashes: got %#v, want %d", loadout.MasteryHashes, len(expected.Hashes))
	}
	seenHashes := make(map[string]struct{}, len(loadout.MasteryHashes))
	for _, hash := range loadout.MasteryHashes {
		if _, duplicate := seenHashes[hash]; duplicate {
			t.Fatalf("converter must not write a hash twice: %#v", loadout.MasteryHashes)
		}
		seenHashes[hash] = struct{}{}
	}
	if !strings.Contains(strings.Join(player.Warnings, "\n"), "已安全映射") || !strings.Contains(strings.Join(player.Warnings, "\n"), "随机分配") || !strings.Contains(strings.Join(player.Warnings, "\n"), "仅展示不会写入") {
		t.Fatalf("mastery-index warning missing: %#v", player.Warnings)
	}
}


func TestLogsMasterLevelUsesMinimumMSPForEverySupportedLevel(t *testing.T) {
	for level := 1; level <= 55; level++ {
		player := &logsPlayer{
			CharacterType: "PL0400",
			WeaponKey:     "WEP_PL0400_02_01",
			MasterLevel:   uint32(level),
			Sigils:        []logsSigil{{SigilID: 3}},
			Stats:         &logsRecordStats{Level: 100},
		}
		loadouts := logsPlayerLoadouts([]*logsPlayer{player})
		if len(loadouts) != 1 || loadouts[0].Loadout == nil || loadouts[0].Loadout.Character == nil {
			t.Fatalf("MLv%d did not create a character draft: %#v", level, loadouts)
		}
		want, err := backend.MasterTotalMSPForProgress(0, level)
		if err != nil {
			t.Fatalf("MLv%d threshold: %v", level, err)
		}
		if got := loadouts[0].Loadout.Character.MasterTotalMSP; got != want {
			t.Fatalf("MLv%d MSP = %d, want minimum threshold %d", level, got, want)
		}
		if got := loadouts[0].Loadout.Character.MasterProgressIndex; got != level || !loadouts[0].Loadout.Character.MasterProgressCaptured {
			t.Fatalf("MLv%d progress capture = (%d, %t), want (%d, true)", level, got, loadouts[0].Loadout.Character.MasterProgressCaptured, level)
		}
	}
}

func TestLogsWeaponPlusMarksPresence(t *testing.T) {
	zero := uint32(0)
	nonZero := uint32(99)
	for _, test := range []struct {
		name           string
		plusMarks      *uint32
		wantCaptured   bool
		wantMirage     int
	}{
		{name: "missing"},
		{name: "explicit zero", plusMarks: &zero, wantCaptured: true},
		{name: "non-zero", plusMarks: &nonZero, wantCaptured: true, wantMirage: 99},
	} {
		t.Run(test.name, func(t *testing.T) {
			player := &logsPlayer{
				CharacterType: "PL0400",
				WeaponKey:     "WEP_PL0400_02_01",
				Sigils:        []logsSigil{{SigilID: 3}},
				WeaponState:   &logsWeaponState{WeaponID: 41, Exp: 42, PlusMarks: test.plusMarks, AwakeningLevel: 4},
			}
			loadouts := logsPlayerLoadouts([]*logsPlayer{player})
			if len(loadouts) != 1 || loadouts[0].Loadout == nil || loadouts[0].Loadout.Weapon == nil {
				t.Fatalf("expected Logs weapon draft: %#v", loadouts)
			}
			weapon := loadouts[0].Loadout.Weapon
			if weapon.EnhancementCaptured || weapon.Transcendence != 7 || weapon.Awakening != 4 || weapon.MirageCaptured != test.wantCaptured || weapon.Mirage != test.wantMirage {
				t.Fatalf("missing-transcendence Logs weapon must use full transcendence 7 while preserving plusMarks: %#v", weapon)
			}
			if !strings.Contains(strings.Join(loadouts[0].Warnings, "\n"), "按用户选择默认写入满超凡 7") {
				t.Fatalf("missing transcendence default warning absent: %#v", loadouts[0].Warnings)
			}
		})
	}
}

func TestLogsUnknownMasteryEffectUIIDRemainsDisplayOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs-export.json")
	if err := os.WriteFile(path, []byte(`[{"characterType":"PL2700","weaponKey":"WEP_PL2700_02_01","sigils":[{"sigilId":3}],"skillboard":[13980629]}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := readLogsSigilLoadoutsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	loadout := result[0].Loadouts[0].Loadout
	if loadout == nil || len(loadout.MasteryHashes) != 0 || len(loadout.LogsSkillboardEffectUIIDs) != 1 || loadout.LogsSkillboardEffectUIIDs[0] != 13980629 {
		t.Fatalf("Logs EffectUiId must remain display-only: %#v", loadout)
	}
}
