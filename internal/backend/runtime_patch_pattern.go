package backend

import (
	"fmt"
	"unicode"
	"unsafe"
)

type runtimePatchPattern struct {
	Values []byte
	Mask   []byte
}

func validateRuntimePatchPattern(pattern runtimePatchPattern) error {
	if len(pattern.Mask) == 0 {
		return fmt.Errorf("RuntimePatch pattern mask is empty")
	}
	if len(pattern.Values) != len(pattern.Mask) {
		return fmt.Errorf("RuntimePatch pattern values and mask have different lengths")
	}
	for index, mask := range pattern.Mask {
		switch mask {
		case 0xFF, 0xF0, 0x0F, 0x00:
		default:
			return fmt.Errorf("invalid RuntimePatch pattern mask 0x%02X", mask)
		}
		if pattern.Values[index]&^mask != 0 {
			return fmt.Errorf("nonzero RuntimePatch wildcard bits at byte %d", index)
		}
	}
	return nil
}

func parseRuntimePatchPattern(raw string) (runtimePatchPattern, error) {
	type nibble struct {
		value byte
		mask  byte
	}

	nibbles := make([]nibble, 0, len(raw))
	for _, char := range raw {
		if unicode.IsSpace(char) {
			continue
		}

		var parsed nibble
		switch {
		case char >= '0' && char <= '9':
			parsed = nibble{value: byte(char - '0'), mask: 0xF}
		case char >= 'a' && char <= 'f':
			parsed = nibble{value: byte(char-'a') + 10, mask: 0xF}
		case char >= 'A' && char <= 'F':
			parsed = nibble{value: byte(char-'A') + 10, mask: 0xF}
		case char == 'x' || char == 'X' || char == '?':
			parsed = nibble{}
		default:
			return runtimePatchPattern{}, fmt.Errorf("invalid RuntimePatch pattern character %q", char)
		}
		nibbles = append(nibbles, parsed)
	}

	if len(nibbles) == 0 {
		return runtimePatchPattern{}, fmt.Errorf("RuntimePatch pattern is empty")
	}
	if len(nibbles)%2 != 0 {
		return runtimePatchPattern{}, fmt.Errorf("RuntimePatch pattern has an odd number of nibbles")
	}

	pattern := runtimePatchPattern{
		Values: make([]byte, len(nibbles)/2),
		Mask:   make([]byte, len(nibbles)/2),
	}
	for i := range pattern.Values {
		high, low := nibbles[i*2], nibbles[i*2+1]
		pattern.Values[i] = high.value<<4 | low.value
		pattern.Mask[i] = high.mask<<4 | low.mask
	}
	return pattern, nil
}

func matchRuntimePatchPattern(buf []byte, pattern runtimePatchPattern) bool {
	if validateRuntimePatchPattern(pattern) != nil || len(buf) < len(pattern.Values) {
		return false
	}
	return matchRuntimePatchPatternValidated(buf, pattern)
}

func matchRuntimePatchPatternValidated(buf []byte, pattern runtimePatchPattern) bool {
	for i, value := range pattern.Values {
		mask := pattern.Mask[i]
		if buf[i]&mask != value&mask {
			return false
		}
	}
	return true
}

func findRuntimePatchPatternMatches(buf []byte, base uintptr, pattern runtimePatchPattern) []uintptr {
	if validateRuntimePatchPattern(pattern) != nil || len(buf) < len(pattern.Values) {
		return nil
	}

	var matches []uintptr
	for offset := 0; offset <= len(buf)-len(pattern.Values); offset++ {
		if matchRuntimePatchPatternValidated(buf[offset:], pattern) {
			matches = append(matches, base+uintptr(offset))
		}
	}
	return matches
}

type runtimePatchChunkReader func(addr uintptr, size int) ([]byte, error)

func scanRuntimePatchPatternChunksUnique(base, size, chunkSize uintptr, pattern runtimePatchPattern, label string, read runtimePatchChunkReader) (uintptr, error) {
	if err := validateRuntimePatchPattern(pattern); err != nil {
		return 0, fmt.Errorf("%s: %w", label, err)
	}
	if chunkSize == 0 {
		return 0, fmt.Errorf("%s: RuntimePatch scan chunk size is zero", label)
	}
	if read == nil {
		return 0, fmt.Errorf("%s: RuntimePatch chunk reader is nil", label)
	}

	var (
		carry     []byte
		carryBase uintptr
		match     uintptr
		matches   int
	)
	for offset := uintptr(0); offset < size; {
		readSize := chunkSize
		if remaining := size - offset; readSize > remaining {
			readSize = remaining
		}
		if readSize > uintptr(^uint(0)>>1) {
			return 0, fmt.Errorf("%s: RuntimePatch scan chunk is too large", label)
		}

		address := base + offset
		buf, err := read(address, int(readSize))
		if err != nil || len(buf) != int(readSize) {
			carry = nil
			carryBase = 0
			offset += readSize
			continue
		}

		scanBuf := buf
		scanBase := address
		if len(carry) != 0 {
			scanBuf = make([]byte, 0, len(carry)+len(buf))
			scanBuf = append(scanBuf, carry...)
			scanBuf = append(scanBuf, buf...)
			scanBase = carryBase
		}

		for scanOffset := 0; scanOffset <= len(scanBuf)-len(pattern.Values); scanOffset++ {
			candidate := scanBase + uintptr(scanOffset)
			// A completed match whose end is not in this chunk was already
			// reported while scanning an earlier chunk.
			if candidate+uintptr(len(pattern.Values)) <= address {
				continue
			}
			if !matchRuntimePatchPatternValidated(scanBuf[scanOffset:], pattern) {
				continue
			}
			match = candidate
			matches++
			if matches > 1 {
				return 0, fmt.Errorf("%s: multiple RuntimePatch pattern matches (%d or more)", label, matches)
			}
		}

		keep := len(pattern.Values) - 1
		if keep == 0 {
			carry = nil
			carryBase = 0
		} else {
			if keep > len(scanBuf) {
				keep = len(scanBuf)
			}
			carry = append(carry[:0], scanBuf[len(scanBuf)-keep:]...)
			carryBase = scanBase + uintptr(len(scanBuf)-keep)
		}
		offset += readSize
	}

	if matches == 0 {
		return 0, fmt.Errorf("%s: zero RuntimePatch pattern matches", label)
	}
	return match, nil
}

func (a *App) scanRuntimePatchPatternUnique(pattern runtimePatchPattern, label string) (uintptr, error) {
	moduleSize, err := getRemoteModuleSize(a.hProcess, a.moduleBase)
	if err != nil {
		return 0, err
	}

	read := func(address uintptr, size int) ([]byte, error) {
		buf := make([]byte, size)
		if err := readProcessMemory(a.hProcess, address, unsafe.Pointer(&buf[0]), uintptr(size)); err != nil {
			return nil, err
		}
		return buf, nil
	}
	return scanRuntimePatchPatternChunksUnique(a.moduleBase, moduleSize, 64*1024, pattern, label, read)
}
