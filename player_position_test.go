package main

import (
	"math"
	"testing"
)

func TestPlayerPositionSignatureLayout(t *testing.T) {
	for _, layout := range playerPositionLayouts {
		if len(layout.signatureAOB) == 0 || len(layout.signatureAMask) == 0 {
			t.Fatalf("%s: empty signature AOB", layout.version)
		}
		if len(layout.signatureAOB) != len(layout.signatureAMask) {
			t.Fatalf("%s: signature AOB/mask length mismatch", layout.version)
		}
		if layout.signatureAOB[0] != 0x48 || layout.signatureAOB[1] != 0x8B || !layout.signatureAMask[0] || !layout.signatureAMask[1] {
			t.Fatalf("%s: unexpected signature prefix", layout.version)
		}
	}
}

func TestPlayerPositionLayouts(t *testing.T) {
	if len(playerPositionLayouts) != 1 {
		t.Fatalf("layout count = %d, want 1", len(playerPositionLayouts))
	}
	if playerPositionLayouts[0].version != "2.0.4" {
		t.Fatalf("unexpected layout version: %#v", playerPositionLayouts[0])
	}
	if playerPositionTransformRoot != 0x28 || playerPositionTransformNode != 0x08 || playerPositionXOffset != 0xD8 || playerPositionYOffset != 0xD4 || playerPositionZOffset != 0xD0 {
		t.Fatal("coordinate chain offsets changed unexpectedly")
	}
}

func TestGravityPatterns(t *testing.T) {
	if len(gravityGetterAOB) != 16 || len(gravityGetterMask) != 16 {
		t.Fatalf("gravity getter pattern length = %d/%d", len(gravityGetterAOB), len(gravityGetterMask))
	}
	if len(gravitySetterAOB) != 13 || len(gravitySetterMask) != 13 {
		t.Fatalf("gravity setter pattern length = %d/%d", len(gravitySetterAOB), len(gravitySetterMask))
	}
	if !bytesEqual(gravityOriginal, []byte{0xC5, 0xF8, 0x29, 0x81, 0xD0, 0x00, 0x00, 0x00}) {
		t.Fatal("gravity original bytes changed")
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
