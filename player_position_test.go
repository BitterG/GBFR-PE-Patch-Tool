package main

import (
	"math"
	"testing"
)

func TestPlayerPositionSignatureLayout(t *testing.T) {
	for _, layout := range playerPositionLayouts {
		if len(layout.signature) != playerPositionSignatureSize {
			t.Fatalf("%s: signature length = %d, want %d", layout.version, len(layout.signature), playerPositionSignatureSize)
		}
		if len(layout.signatureMask) != playerPositionSignatureSize {
			t.Fatalf("%s: signature mask length = %d, want %d", layout.version, len(layout.signatureMask), playerPositionSignatureSize)
		}
		if layout.signature[0] != 0x48 || layout.signature[1] != 0x8B || !layout.signatureMask[0] || !layout.signatureMask[1] {
			t.Fatalf("%s: unexpected signature prefix", layout.version)
		}
	}
}

func TestPlayerPositionLayouts(t *testing.T) {
	if len(playerPositionLayouts) != 3 {
		t.Fatalf("layout count = %d, want 3", len(playerPositionLayouts))
	}
	if playerPositionLayouts[0].version != "2.0.2" || playerPositionLayouts[1].version != "2.0.3" || playerPositionLayouts[2].version != "2.0.4" {
		t.Fatalf("unexpected layout versions: %#v", playerPositionLayouts)
	}
	if playerPositionLayouts[2].signatureRVA != 0xC2541B || playerPositionLayouts[2].slotTableRVA != 0x7034AA0 || playerPositionLayouts[2].gravityRVA != 0x39D9DC4 {
		t.Fatalf("2.0.4 layout = %#v", playerPositionLayouts[2])
	}
	if playerPositionTransformRoot != 0x28 || playerPositionTransformNode != 0x08 || playerPositionXOffset != 0xD8 || playerPositionYOffset != 0xD4 || playerPositionZOffset != 0xD0 {
		t.Fatal("coordinate chain offsets changed unexpectedly")
	}
}
func TestValidatePlayerPosition(t *testing.T) {
	for _, position := range [][3]float32{{0, 0, 0}, {1, -2.5, 3}, {playerPositionMaximumAbsValue, -playerPositionMaximumAbsValue, 1}} {
		if err := validatePlayerPosition(position[0], position[1], position[2]); err != nil {
			t.Fatalf("validatePlayerPosition(%v) returned %v", position, err)
		}
	}
	for _, position := range [][3]float32{{float32(math.Inf(1)), 0, 0}, {float32(math.NaN()), 0, 0}, {playerPositionMaximumAbsValue + 1, 0, 0}} {
		if err := validatePlayerPosition(position[0], position[1], position[2]); err == nil {
			t.Fatalf("validatePlayerPosition(%v) succeeded", position)
		}
	}
}
