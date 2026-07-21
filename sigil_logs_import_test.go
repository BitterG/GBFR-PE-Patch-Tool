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
	blob, err := cbor.Marshal(logsEncounter{PlayerData: [4]*logsPlayer{{
		DisplayName:   "Player",
		CharacterName: "Player",
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
	if len(result) != 2 || result[0].LogTime != 456 || len(result[0].Loadouts) != 1 || len(result[0].Loadouts[0].Entries) != 1 {
		t.Fatalf("unexpected import: %#v", result)
	}
	entry := result[0].Loadouts[0].Entries[0]
	if entry.SigilHash != 3 || entry.PrimaryTraitHash != 1 || entry.SecondaryTraitHash != 2 {
		t.Fatalf("unexpected entry: %#v", entry)
	}
}
