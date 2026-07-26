package backend

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func TestLoadoutShareConstructedSigilAllowsEmptySecondary(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name          string
		secondaryHash string
	}{
		{name: "missing"},
		{name: "empty string", secondaryHash: "  "},
		{name: "zero hash", secondaryHash: "00000000"},
		{name: "empty hash", secondaryHash: fmt.Sprintf("%08X", EmptyHash)},
	} {
		t.Run(test.name, func(t *testing.T) {
			draft, err := loadoutShareConstructedSigil(catalog, LoadoutShareSigil{
				Hash:                "49434696",
				Name:                "可怕的漆黑钳蟹因子",
				Level:               20,
				PrimaryTraitHash:    "BF78FBFC",
				PrimaryTraitLevel:   20,
				SecondaryTraitHash:  test.secondaryHash,
				SecondaryTraitLevel: 9,
			}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if draft.ExactSecondaryTraitHash != "" || draft.Item.SecondaryTraitID != "" || draft.Item.SecondaryLevel != 0 {
				t.Fatalf("empty secondary draft = %#v, want empty ID/hash and level 0", draft)
			}
			prepared, err := prepareExactLoadoutSigil(catalog, draft)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.hasSecondary || prepared.secondaryHash != EmptyHash || prepared.secondaryLevel != 0 {
				t.Fatalf("prepared empty secondary = %#v, want hasSecondary=false, EmptyHash/0", prepared)
			}
		})
	}
}

func TestPrepareExactLoadoutSigilNormalizesEmptySecondaryHashes(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, secondaryHash := range []string{"", "00000000", fmt.Sprintf("%08X", EmptyHash)} {
		t.Run(secondaryHash, func(t *testing.T) {
			prepared, err := prepareExactLoadoutSigil(catalog, LoadoutConstructedSigil{
				Index: 0,
				Item: QueueItem{Level: 20, PrimaryLevel: 20, SecondaryLevel: 9},
				ExactSigilHash: "49434696", ExactPrimaryTraitHash: "BF78FBFC",
				ExactSecondaryTraitHash: secondaryHash,
			})
			if err != nil {
				t.Fatal(err)
			}
			if prepared.hasSecondary || prepared.secondaryHash != EmptyHash || prepared.secondaryLevel != 0 || prepared.item.SecondaryLevel != 0 {
				t.Fatalf("prepared empty secondary = %#v, want hasSecondary=false and EmptyHash/0", prepared)
			}
		})
	}
}

func TestPrepareExactLoadoutSigilPreservesNonEmptySecondary(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	secondary := catalog.Traits[0]
	secondaryHash, err := ParseHashHex(secondary.Hash)
	if err != nil {
		t.Fatal(err)
	}

	draft := LoadoutConstructedSigil{
		Index:                  0,
		Item:                   QueueItem{SigilID: "49434696", Level: 20, PrimaryLevel: 20, SecondaryLevel: 1},
		ExactSigilHash:         "49434696",
		ExactPrimaryTraitHash:  "BF78FBFC",
		ExactSecondaryTraitHash: fmt.Sprintf("%08X", secondaryHash),
	}
	prepared, err := prepareExactLoadoutSigil(catalog, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.hasSecondary || prepared.secondaryHash != secondaryHash || prepared.secondaryLevel != 1 {
		t.Fatalf("prepared non-empty secondary = %#v, want hash %08X, level 1, hasSecondary=true", prepared, secondaryHash)
	}
}

func TestPrepareExactLoadoutSigilRejectsLevelForNonEmptySecondary(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	secondaryHash, err := ParseHashHex(catalog.Traits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepareExactLoadoutSigil(catalog, LoadoutConstructedSigil{
		Index:                  0,
		Item:                   QueueItem{Level: 20, PrimaryLevel: 20},
		ExactSigilHash:         "49434696",
		ExactPrimaryTraitHash:  "BF78FBFC",
		ExactSecondaryTraitHash: fmt.Sprintf("%08X", secondaryHash),
	})
	if err == nil {
		t.Fatal("non-empty exact secondary with level 0 must fail")
	}
}

func TestPatchAndVerifySigilWithFlagsClearEmptySecondary(t *testing.T) {
	gemUnitID := GemSlotBaseID
	primaryUnitID := TraitSlotBase
	secondaryUnitID := primaryUnitID + 1
	save := newSigilStoreTestSave(t,
		storeTestField{GemSlotIDType, uint32(gemUnitID), 99},
		storeTestField{GemIDType, uint32(gemUnitID), 1},
		storeTestField{GemLevelIDType, uint32(gemUnitID), 1},
		storeTestField{GemWornByIDType, uint32(gemUnitID), 1},
		storeTestField{GemFlagsIDType, uint32(gemUnitID), 1},
		storeTestField{TraitHashIDType, uint32(primaryUnitID), 1},
		storeTestField{TraitLevelIDType, uint32(primaryUnitID), 1},
		storeTestField{TraitHashIDType, uint32(secondaryUnitID), 0x12345678},
		storeTestField{TraitLevelIDType, uint32(secondaryUnitID), 15},
	)
	if err := save.PatchSigilWithFlags(gemUnitID, 100, 0x11111111, 20, 0x22222222, 20, 0x33333333, 9, false, NormalSigilFlags); err != nil {
		t.Fatal(err)
	}
	if err := save.VerifySigilWithFlags(gemUnitID, 100, 0x11111111, 20, 0x22222222, 20, 0x33333333, 9, false, NormalSigilFlags); err != nil {
		t.Fatal(err)
	}
	secondaryHash, ok := save.findUnit(TraitHashIDType, uint32(secondaryUnitID))
	if !ok || secondaryHash.Uint32() != EmptyHash {
		t.Fatalf("secondary hash = %#x, want EmptyHash", secondaryHash.Uint32())
	}
	secondaryLevel, ok := save.findUnit(TraitLevelIDType, uint32(secondaryUnitID))
	if !ok || secondaryLevel.Int32() != 0 {
		t.Fatalf("secondary level = %d, want 0", secondaryLevel.Int32())
	}
}

func TestPatchAndVerifySigilWithFlagsPreservesNonEmptySecondary(t *testing.T) {
	gemUnitID := GemSlotBaseID
	primaryUnitID := TraitSlotBase
	secondaryUnitID := primaryUnitID + 1
	save := newSigilStoreTestSave(t,
		storeTestField{GemSlotIDType, uint32(gemUnitID), 0},
		storeTestField{GemIDType, uint32(gemUnitID), EmptyHash},
		storeTestField{GemLevelIDType, uint32(gemUnitID), 0},
		storeTestField{GemWornByIDType, uint32(gemUnitID), EmptyHash},
		storeTestField{GemFlagsIDType, uint32(gemUnitID), 0},
		storeTestField{TraitHashIDType, uint32(primaryUnitID), EmptyHash},
		storeTestField{TraitLevelIDType, uint32(primaryUnitID), 0},
		storeTestField{TraitHashIDType, uint32(secondaryUnitID), EmptyHash},
		storeTestField{TraitLevelIDType, uint32(secondaryUnitID), 0},
	)
	if err := save.PatchSigilWithFlags(gemUnitID, 100, 0x11111111, 20, 0x22222222, 20, 0x33333333, 9, true, NormalSigilFlags); err != nil {
		t.Fatal(err)
	}
	if err := save.VerifySigilWithFlags(gemUnitID, 100, 0x11111111, 20, 0x22222222, 20, 0x33333333, 9, true, NormalSigilFlags); err != nil {
		t.Fatal(err)
	}
	secondaryHash, _ := save.findUnit(TraitHashIDType, uint32(secondaryUnitID))
	secondaryLevel, _ := save.findUnit(TraitLevelIDType, uint32(secondaryUnitID))
	if secondaryHash.Uint32() != 0x33333333 || secondaryLevel.Int32() != 9 {
		t.Fatalf("secondary fields = (%#x, %d), want (0x33333333, 9)", secondaryHash.Uint32(), secondaryLevel.Int32())
	}
}

type storeTestField struct {
	idType uint32
	unitID uint32
	value  uint32
}

func newSigilStoreTestSave(t *testing.T, fields ...storeTestField) *SaveData {
	t.Helper()
	const recordSize = 48
	data := make([]byte, recordSize*len(fields))
	for index, field := range fields {
		table := index*recordSize + 16
		vtable := table - 12
		binary.LittleEndian.PutUint16(data[vtable:], 10)
		binary.LittleEndian.PutUint16(data[vtable+2:], 16)
		binary.LittleEndian.PutUint16(data[vtable+4:], 4)
		binary.LittleEndian.PutUint16(data[vtable+6:], 8)
		binary.LittleEndian.PutUint16(data[vtable+8:], 12)
		binary.LittleEndian.PutUint32(data[table:], 12)
		binary.LittleEndian.PutUint32(data[table+4:], field.idType)
		binary.LittleEndian.PutUint32(data[table+8:], field.unitID)
		binary.LittleEndian.PutUint32(data[table+12:], 4)
		binary.LittleEndian.PutUint32(data[table+16:], 1)
		binary.LittleEndian.PutUint32(data[table+20:], field.value)
	}
	return &SaveData{data: data, slotLen: int64(len(data))}
}
