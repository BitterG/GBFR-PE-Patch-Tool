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
		"abilities":[11,12], "masterLevel":20, "skillboard":[10,11],
		"stats":{"level":100,"hp":1000,"attack":2000},
		"weaponState":{"weaponId":41,"exp":42}, "overmasteryInfo":{"overmasteries":[]}
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

func TestReadLogsSigilLoadoutsJSONNeverTreatsEffectUIIDAsMasteryHash(t *testing.T) {
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
