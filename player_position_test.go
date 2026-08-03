package main

import (
	"math"
	"testing"
)

func TestPlayerPositionSignatureLayout(t *testing.T) {
	if len(playerPositionSignature) != playerPositionSignatureSize {
		t.Fatalf("signature length = %d, want %d", len(playerPositionSignature), playerPositionSignatureSize)
	}
	if len(playerPositionSignatureMask) != playerPositionSignatureSize {
		t.Fatalf("signature mask length = %d, want %d", len(playerPositionSignatureMask), playerPositionSignatureSize)
	}
	if playerPositionSignature[0] != 0x48 || playerPositionSignature[1] != 0x8B || !playerPositionSignatureMask[0] || !playerPositionSignatureMask[1] {
	}
}

func TestPlayerPositionLayouts(t *testing.T) {
	if len(playerPositionLayouts) != 2 {
		t.Fatalf("layout count = %d, want 2", len(playerPositionLayouts))
	}
	if playerPositionLayouts[0].version != "2.0.2" || playerPositionLayouts[1].version != "2.0.3" {
		t.Fatalf("unexpected layout versions: %#v", playerPositionLayouts)
	}
	if playerPositionLayouts[1].signatureRVA != 0x22C9310 || playerPositionLayouts[1].slotTableRVA != 0x7033820 || playerPositionLayouts[1].gravityRVA != 0x39D8E24 {
		t.Fatalf("2.0.3 layout = %#v", playerPositionLayouts[1])
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
