package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
)

func TestReadLogsSigilLoadouts(t *testing.T) {
	blob, err := cbor.Marshal(logsEncounter{QuestID: 0x40B301, PlayerData: [4]*logsPlayer{{
		ActorIndex:    12,
		DisplayName:   "Player",
		CharacterName: "Player",
		CharacterType: "PL2700",
		WeaponKey:     "WEP_PL2700_02_01",
		Abilities:     []uint32{11, 12},
		Skillboard:    []uint32{21},
		Summons:       []logsSummon{{SummonID: 31}, {SummonID: 32}, {SummonID: 33}, {SummonID: 34}},
		WeaponState:   &logsWeaponState{WeaponID: 41, Exp: 42},
		Stats:         &logsRecordStats{Level: 100, HP: 1000, Attack: 2000},
		Sigils: []logsSigil{{
			FirstTraitID: 1, FirstTraitLevel: 15,
			SecondTraitID: 2, SecondTraitLevel: 15,
			SigilID: 3, SigilLevel: 15,
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(blob, nil)
	encoder.Close()

	path := filepath.Join(t.TempDir(), "logs.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE logs (time INTEGER NOT NULL, data BLOB NOT NULL, version INTEGER NOT NULL)`)
	if err == nil {
		_, err = db.Exec(`INSERT INTO logs (time, data, version) VALUES (?, ?, 1), (?, ?, 1)`, 123, compressed, 456, compressed)
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	result, err := readLogsSigilLoadouts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].LogTime != 456 || result[0].QuestID != 0x40B301 || result[0].QuestName != "世界临界" || len(result[0].Loadouts) != 1 || len(result[0].Loadouts[0].Entries) != 1 {
		t.Fatalf("unexpected import: %#v", result)
	}
	entry := result[0].Loadouts[0].Entries[0]
	if entry.SigilHash != 3 || entry.PrimaryTraitHash != 1 || entry.SecondaryTraitHash != 2 {
		t.Fatalf("unexpected entry: %#v", entry)
	}
	loadout := result[0].Loadouts[0].Loadout
	if loadout == nil || loadout.OwnerCode != "PL2700" || loadout.WeaponHash != "00000029" || len(loadout.Summons) != 4 || len(loadout.Skills) != 2 || len(loadout.MasteryHashes) != 0 || len(loadout.Weapon.Wrightstone.Traits) != 0 {
		t.Fatalf("unexpected complete loadout draft: %#v", loadout)
	}
}
