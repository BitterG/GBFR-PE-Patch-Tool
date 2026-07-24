package backend

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"strings"
)

const formulaStatusObjectScanSize = 0x6000
const formulaStatusObjectScanChunkSize = 0x1000
const formulaPlausibleIntegerLimit = int64(10_000_000)
const formulaPlausibleFloatLimit = float32(1_000_000_000)
const formulaCandidateWordLimit = 4096

type formulaScanMemory interface {
	ReadAt(address uintptr, destination []byte) error
}

type formulaMappedAddressProbe func(uint64) (bool, error)

type FormulaScanBudget struct {
	MaxBytes uint64 `json:"maxBytes"`
}

type FormulaScalarKind string

const (
	FormulaScalarI32 FormulaScalarKind = "i32"
	FormulaScalarF32 FormulaScalarKind = "f32"
)

type FormulaScalarCandidate struct {
	Offset uint32            `json:"offset"`
	Kind   FormulaScalarKind `json:"kind"`
	Delta  float64           `json:"delta"`
	ABits  uint32            `json:"-"`
	BBits  uint32            `json:"-"`
}

func captureFormulaStatusObject(ctx context.Context, memory formulaScanMemory, status uintptr, budget FormulaScanBudget) ([]byte, error) {
	if ctx == nil || memory == nil {
		return nil, fmt.Errorf("formula status capture parameters are invalid")
	}
	if budget.MaxBytes < formulaStatusObjectScanSize {
		return nil, fmt.Errorf("formula scan budget %d is below required %d bytes", budget.MaxBytes, formulaStatusObjectScanSize)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot := make([]byte, formulaStatusObjectScanSize)
	for offset := 0; offset < len(snapshot); offset += formulaStatusObjectScanChunkSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(offset+formulaStatusObjectScanChunkSize, len(snapshot))
		if ^uintptr(0)-status < uintptr(offset) {
			return nil, fmt.Errorf("formula status address overflow")
		}
		if err := memory.ReadAt(status+uintptr(offset), snapshot[offset:end]); err != nil {
			return nil, fmt.Errorf("read formula status chunk 0x%X: %w", offset, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func diffFormulaStatusABAB(a1, b1, a2, b2 []byte, isMappedAddress formulaMappedAddressProbe, excludedWords map[int]struct{}) ([]FormulaScalarCandidate, error) {
	for _, snapshot := range [][]byte{a1, b1, a2, b2} {
		if len(snapshot) < formulaStatusObjectScanSize {
			return nil, fmt.Errorf("formula status snapshot is %d bytes, want at least %d", len(snapshot), formulaStatusObjectScanSize)
		}
	}
	if isMappedAddress == nil {
		return nil, fmt.Errorf("formula address redactor is required")
	}
	changedWords := make(map[int]struct{})
	for offset := 0; offset+4 <= formulaStatusObjectScanSize; offset += 4 {
		aBits := binary.LittleEndian.Uint32(a1[offset : offset+4])
		bBits := binary.LittleEndian.Uint32(b1[offset : offset+4])
		if aBits == binary.LittleEndian.Uint32(a2[offset:offset+4]) &&
			bBits == binary.LittleEndian.Uint32(b2[offset:offset+4]) && aBits != bBits {
			changedWords[offset] = struct{}{}
		}
	}
	if len(changedWords) > formulaCandidateWordLimit {
		return nil, fmt.Errorf("公式候选字数量 %d 超过脱敏上限 %d", len(changedWords), formulaCandidateWordLimit)
	}
	pointerWords, err := formulaPointerWordOffsets(changedWords, isMappedAddress, a1, b1, a2, b2)
	if err != nil {
		return nil, err
	}
	for offset := range excludedWords {
		pointerWords[offset] = struct{}{}
	}
	candidates := make([]FormulaScalarCandidate, 0)
	for offset := range changedWords {
		if _, isPointerWord := pointerWords[offset]; isPointerWord {
			continue
		}
		a1Bits := binary.LittleEndian.Uint32(a1[offset : offset+4])
		b1Bits := binary.LittleEndian.Uint32(b1[offset : offset+4])
		base := FormulaScalarCandidate{Offset: uint32(offset), ABits: a1Bits, BBits: b1Bits}
		aInteger, bInteger := int64(int32(a1Bits)), int64(int32(b1Bits))
		if absInt64(aInteger) <= formulaPlausibleIntegerLimit && absInt64(bInteger) <= formulaPlausibleIntegerLimit {
			integer := base
			integer.Kind = FormulaScalarI32
			integer.Delta = float64(bInteger - aInteger)
			candidates = append(candidates, integer)
		}
		aFloat, bFloat := math.Float32frombits(a1Bits), math.Float32frombits(b1Bits)
		if plausibleFormulaFloat(aFloat) && plausibleFormulaFloat(bFloat) {
			floating := base
			floating.Kind = FormulaScalarF32
			floating.Delta = float64(bFloat - aFloat)
			candidates = append(candidates, floating)
		}
	}
	sortFormulaScalarCandidates(candidates)
	return candidates, nil
}

func formulaPointerWordOffsets(changedWords map[int]struct{}, isMappedAddress formulaMappedAddressProbe, snapshots ...[]byte) (map[int]struct{}, error) {
	offsets := make(map[int]struct{})
	cache := make(map[uint64]bool)
	for changedOffset := range changedWords {
		starts := []int{changedOffset}
		if changedOffset >= 4 {
			starts = append(starts, changedOffset-4)
		}
		for _, start := range starts {
			if start < 0 || start+8 > formulaStatusObjectScanSize {
				continue
			}
			for _, snapshot := range snapshots {
				value := binary.LittleEndian.Uint64(snapshot[start : start+8])
				mapped, seen := cache[value]
				if !seen {
					var err error
					mapped, err = isMappedAddress(value)
					if err != nil {
						return nil, fmt.Errorf("classify formula candidate address: %w", err)
					}
					cache[value] = mapped
				}
				if mapped {
					offsets[start] = struct{}{}
					offsets[start+4] = struct{}{}
					break
				}
			}
		}
	}
	return offsets, nil
}

func redactFormulaMappedPointerWords(snapshot []byte, isMappedAddress formulaMappedAddressProbe) ([]byte, map[int]struct{}, error) {
	if len(snapshot) != formulaStatusObjectScanSize || isMappedAddress == nil {
		return nil, nil, fmt.Errorf("formula pointer redaction parameters are invalid")
	}
	redacted := append([]byte(nil), snapshot...)
	pointerWords := make(map[int]struct{})
	cache := make(map[uint64]bool)
	for offset := 0; offset+8 <= len(snapshot); offset += 4 {
		value := binary.LittleEndian.Uint64(snapshot[offset : offset+8])
		mapped, seen := cache[value]
		if !seen {
			var err error
			mapped, err = isMappedAddress(value)
			if err != nil {
				return nil, nil, fmt.Errorf("redact formula mapped address: %w", err)
			}
			cache[value] = mapped
		}
		if mapped {
			clear(redacted[offset : offset+8])
			pointerWords[offset] = struct{}{}
			pointerWords[offset+4] = struct{}{}
		}
	}
	return redacted, pointerWords, nil
}

func sortFormulaScalarCandidates(candidates []FormulaScalarCandidate) {
	slices.SortFunc(candidates, func(left, right FormulaScalarCandidate) int {
		if left.Offset < right.Offset {
			return -1
		}
		if left.Offset > right.Offset {
			return 1
		}
		return strings.Compare(string(left.Kind), string(right.Kind))
	})
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func plausibleFormulaFloat(value float32) bool {
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return false
	}
	absolute := float32(math.Abs(float64(value)))
	return absolute == 0 || (absolute >= 0.000001 && absolute <= formulaPlausibleFloatLimit)
}
