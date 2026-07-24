package backend

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unsafe"
)

var monsterEnhanceCaveMarker = []byte{'G', 'B', 'F', 'R', 'M', 'H', '0', '3'}

var (
	monsterDamagePlayerPointerPattern = []byte{
		0xFF, 0x90, 0, 0, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0x8B, 0, 0, 0, 0x00, 0x00,
		0x48, 0x81, 0xC1, 0, 0, 0x00, 0x00, 0xFF, 0, 0, 0, 0x00, 0x00, 0, 0x39,
	}
	monsterDamagePlayerPointerMask = []bool{
		true, true, false, false, true, true, false, false, false, false, false, false, false, false, true, false, false, false, true, true,
		true, true, true, false, false, true, true, true, false, false, false, true, true, false, true,
	}
)

type monsterEnhanceOwnedPatch struct {
	OwnerToken  string
	Target      uintptr
	Original    []byte
	Patched     []byte
	Cave        uintptr
	CaveSize    uintptr
	AuxTarget   uintptr
	AuxOriginal []byte
	AuxPatched  []byte
	AuxCave     uintptr
	AuxCaveSize uintptr
}

type monsterEnhanceAuxPreflight struct {
	Target   uintptr
	Original []byte
	CaveSize uintptr
}

func monsterEnhanceCaveSize(id string) uintptr {
	switch id {
	case "monster_damage":
		return 192
	case "overdrive_state":
		return 128
	case "inventory_set_45":
		return 32
	case "monster_hp", "monster_stun":
		return 128
	default:
		return 0
	}
}

func (a *App) prepareMonsterDamageAuxiliaryHook() (*monsterEnhanceAuxPreflight, error) {
	match, err := a.scanPatternUnique(monsterDamagePlayerPointerPattern, monsterDamagePlayerPointerMask, "怪物伤害玩家对象")
	if err != nil {
		return nil, err
	}
	target := match + 0x14
	original, err := a.readMonsterEnhanceEntry(target, 7)
	if err != nil {
		return nil, fmt.Errorf("read monster damage player-pointer instruction: %w", err)
	}
	if original[0] != 0x48 || original[1] != 0x81 || original[2] != 0xC1 || original[5] != 0 || original[6] != 0 {
		return nil, fmt.Errorf("unexpected monster damage player-pointer instruction: %s", bytesToHex(original))
	}
	return &monsterEnhanceAuxPreflight{Target: target, Original: original, CaveSize: 96}, nil
}

func monsterEnhanceCaveMarkerAddress(cave, caveSize uintptr) uintptr {
	if cave == 0 || caveSize < uintptr(len(monsterEnhanceCaveMarker)) {
		return 0
	}
	return cave + caveSize - uintptr(len(monsterEnhanceCaveMarker))
}

func monsterEnhanceRelJumpTarget(source uintptr, entry []byte) (uintptr, bool) {
	if len(entry) < 5 || entry[0] != 0xE9 {
		return 0, false
	}
	delta := int64(int32(binary.LittleEndian.Uint32(entry[1:5])))
	target := int64(source) + 5 + delta
	if target <= 0 {
		return 0, false
	}
	return uintptr(target), true
}

func (a *App) verifyMonsterEnhanceCaveMarker(record monsterEnhanceOwnedPatch) error {
	if record.Cave == 0 || record.CaveSize == 0 {
		return nil
	}
	markerAddr := monsterEnhanceCaveMarkerAddress(record.Cave, record.CaveSize)
	if markerAddr == 0 {
		return fmt.Errorf("monster cave marker address is invalid")
	}
	marker := make([]byte, len(monsterEnhanceCaveMarker))
	if err := readProcessMemory(a.hProcess, markerAddr, unsafe.Pointer(&marker[0]), uintptr(len(marker))); err != nil {
		return fmt.Errorf("read monster cave marker: %w", err)
	}
	if !bytesEqual(marker, monsterEnhanceCaveMarker) {
		return fmt.Errorf("monster cave marker mismatch: %s", bytesToHex(marker))
	}
	return nil
}

func (a *App) readMonsterEnhanceEntry(target uintptr, size int) ([]byte, error) {
	if target == 0 || size <= 0 {
		return nil, fmt.Errorf("monster patch entry is invalid")
	}
	current := make([]byte, size)
	if err := readProcessMemory(a.hProcess, target, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return nil, err
	}
	return current, nil
}

func (a *App) writeAndVerifyMonsterEnhanceEntry(target uintptr, desired []byte, label string) error {
	if err := writeCodeMemory(a.hProcess, target, desired); err != nil {
		return fmt.Errorf("write %s: %w", label, err)
	}
	actual, err := a.readMonsterEnhanceEntry(target, len(desired))
	if err != nil {
		return fmt.Errorf("read back %s: %w", label, err)
	}
	if !bytesEqual(actual, desired) {
		return fmt.Errorf("%s readback mismatch: got %s", label, bytesToHex(actual))
	}
	return nil
}

func (a *App) monsterEnhanceOwnerForCompatibilityCall() string {
	if a.charaOwnerToken != "" {
		return a.charaOwnerToken
	}
	return "compatibility"
}

func (a *App) prepareMonsterEnhanceEnable(ownerToken string, point *monsterPatchPoint) ([]byte, error) {
	if point == nil {
		return nil, fmt.Errorf("monster patch point is nil")
	}
	if existing, ok := a.monsterEnhanceOwned[point.ID]; ok {
		if existing.OwnerToken != ownerToken {
			return nil, fmt.Errorf("%s is owned by another runtime page", point.Name)
		}
		current, err := a.readMonsterEnhanceEntry(existing.Target, len(existing.Patched))
		if err != nil {
			return nil, fmt.Errorf("read owned %s: %w", point.Name, err)
		}
		if !bytesEqual(current, existing.Patched) {
			return nil, fmt.Errorf("owned %s entry changed: %s", point.Name, bytesToHex(current))
		}
		if err := a.verifyMonsterEnhanceCaveMarker(existing); err != nil {
			return nil, fmt.Errorf("owned %s cave validation failed: %w", point.Name, err)
		}
		return nil, fmt.Errorf("%s is already enabled; disable it before changing its value", point.Name)
	}
	target := a.moduleBase + point.RVA
	current, err := a.readMonsterEnhanceEntry(target, len(point.Original))
	if err != nil {
		return nil, fmt.Errorf("read %s before injection: %w", point.Name, err)
	}
	if !bytesEqual(current, point.Original) {
		return nil, fmt.Errorf("%s is not in its original state and is not owned by this tool instance: %s", point.Name, bytesToHex(current))
	}
	return current, nil
}

func (a *App) claimMonsterEnhancePatch(ownerToken string, point *monsterPatchPoint, original []byte) error {
	return a.claimMonsterEnhancePatchWithAux(ownerToken, point, original, nil)
}

func (a *App) claimMonsterEnhancePatchWithAux(ownerToken string, point *monsterPatchPoint, original []byte, aux *monsterEnhanceAuxPreflight) error {
	if point == nil || len(original) == 0 {
		return fmt.Errorf("cannot claim an empty monster patch")
	}
	target := a.moduleBase + point.RVA
	patched, err := a.readMonsterEnhanceEntry(target, len(point.Original))
	if err != nil {
		return fmt.Errorf("read %s after injection: %w", point.Name, err)
	}
	record := monsterEnhanceOwnedPatch{
		OwnerToken: ownerToken,
		Target:     target,
		Original:   append([]byte(nil), original...),
		Patched:    append([]byte(nil), patched...),
	}
	if point.Hook {
		cave, ok := monsterEnhanceRelJumpTarget(target, patched)
		if !ok {
			return fmt.Errorf("%s did not install a complete rel32 jump: %s", point.Name, bytesToHex(patched))
		}
		record.Cave = cave
		record.CaveSize = monsterEnhanceCaveSize(point.ID)
		if record.CaveSize == 0 {
			return fmt.Errorf("%s has no registered cave size", point.Name)
		}
		if err := a.verifyMonsterEnhanceCaveMarker(record); err != nil {
			return fmt.Errorf("%s cave ownership validation failed: %w", point.Name, err)
		}
	} else if point.ID == "sba_chain_timer" {
		if len(patched) < 2 || patched[0] != 0x48 || patched[1] != 0xB8 {
			return fmt.Errorf("%s patch bytes are invalid: %s", point.Name, bytesToHex(patched))
		}
	} else if !bytesEqual(patched, point.Patch) {
		return fmt.Errorf("%s patch bytes are invalid: %s", point.Name, bytesToHex(patched))
	}
	if a.monsterEnhanceOwned == nil {
		a.monsterEnhanceOwned = make(map[string]monsterEnhanceOwnedPatch)
	}
	a.monsterEnhanceOwned[point.ID] = record

	if aux != nil {
		current, readErr := a.readMonsterEnhanceEntry(aux.Target, len(aux.Original))
		if readErr != nil {
			return fmt.Errorf("read monster damage auxiliary hook: %w", readErr)
		}
		cave, ok := monsterEnhanceRelJumpTarget(aux.Target, current)
		if !ok {
			return fmt.Errorf("monster damage auxiliary hook is incomplete: %s", bytesToHex(current))
		}
		markerRecord := monsterEnhanceOwnedPatch{Cave: cave, CaveSize: aux.CaveSize}
		if err := a.verifyMonsterEnhanceCaveMarker(markerRecord); err != nil {
			return fmt.Errorf("monster damage auxiliary cave ownership validation failed: %w", err)
		}
		record.AuxTarget = aux.Target
		record.AuxOriginal = append([]byte(nil), aux.Original...)
		record.AuxCaveSize = aux.CaveSize
		record.AuxPatched = append([]byte(nil), current...)
		record.AuxCave = cave
	}

	a.monsterEnhanceOwned[point.ID] = record
	return nil
}

func (a *App) rollbackMonsterEnhanceFailedEnable(ownerToken string, point *monsterPatchPoint, original []byte) error {
	return a.rollbackMonsterEnhanceFailedEnableWithAux(ownerToken, point, original, nil)
}

func (a *App) rollbackMonsterEnhanceFailedEnableWithAux(ownerToken string, point *monsterPatchPoint, original []byte, aux *monsterEnhanceAuxPreflight) error {
	if point == nil || len(original) == 0 {
		return fmt.Errorf("failed monster enable has no rollback metadata")
	}
	current, err := a.readMonsterEnhanceEntry(a.moduleBase+point.RVA, len(original))
	if err != nil {
		return fmt.Errorf("read failed monster enable state: %w", err)
	}
	if bytesEqual(current, original) {
		if aux == nil {
			return nil
		}
		auxCurrent, auxErr := a.readMonsterEnhanceEntry(aux.Target, len(aux.Original))
		if auxErr != nil {
			return fmt.Errorf("read failed monster auxiliary enable state: %w", auxErr)
		}
		if bytesEqual(auxCurrent, aux.Original) {
			return nil
		}
		record := monsterEnhanceOwnedPatch{
			OwnerToken:  ownerToken,
			Target:      a.moduleBase + point.RVA,
			Original:    append([]byte(nil), original...),
			Patched:     append([]byte(nil), original...),
			AuxTarget:   aux.Target,
			AuxOriginal: append([]byte(nil), aux.Original...),
			AuxPatched:  append([]byte(nil), auxCurrent...),
			AuxCaveSize: aux.CaveSize,
		}
		cave, ok := monsterEnhanceRelJumpTarget(aux.Target, auxCurrent)
		if !ok {
			return fmt.Errorf("failed monster auxiliary enable is not a rel32 jump: %s", bytesToHex(auxCurrent))
		}
		record.AuxCave = cave
		if err := a.verifyMonsterEnhanceCaveMarker(monsterEnhanceOwnedPatch{Cave: cave, CaveSize: aux.CaveSize}); err != nil {
			return fmt.Errorf("failed monster auxiliary cave validation: %w", err)
		}
		if a.monsterEnhanceOwned == nil {
			a.monsterEnhanceOwned = make(map[string]monsterEnhanceOwnedPatch)
		}
		a.monsterEnhanceOwned[point.ID] = record
		return a.restoreMonsterEnhanceOwned(ownerToken, point.ID, false)
	}
	if err := a.claimMonsterEnhancePatchWithAux(ownerToken, point, original, aux); err != nil {
		claimErr := fmt.Errorf("claim failed monster enable for rollback: %w", err)
		if _, ok := a.monsterEnhanceOwned[point.ID]; ok {
			return errors.Join(claimErr, a.restoreMonsterEnhanceOwned(ownerToken, point.ID, false))
		}
		return claimErr
	}
	if err := a.restoreMonsterEnhanceOwned(ownerToken, point.ID, false); err != nil {
		return fmt.Errorf("rollback failed monster enable: %w", err)
	}
	return nil
}

func (a *App) monsterEnhanceHookMarked(point *monsterPatchPoint, entry []byte) bool {
	if point == nil || !point.Hook {
		return false
	}
	cave, ok := monsterEnhanceRelJumpTarget(a.moduleBase+point.RVA, entry)
	if !ok {
		return false
	}
	record := monsterEnhanceOwnedPatch{Cave: cave, CaveSize: monsterEnhanceCaveSize(point.ID)}
	return record.CaveSize != 0 && a.verifyMonsterEnhanceCaveMarker(record) == nil
}

func (a *App) restoreMonsterEnhanceOwned(ownerToken, id string, forceAllOwners bool) error {
	if len(a.monsterEnhanceOwned) == 0 {
		return nil
	}
	ids := make([]string, 0, len(a.monsterEnhanceOwned))
	for patchID, record := range a.monsterEnhanceOwned {
		if id != "all" && patchID != id {
			continue
		}
		if !forceAllOwners && record.OwnerToken != ownerToken {
			continue
		}
		ids = append(ids, patchID)
	}
	sort.Strings(ids)
	for _, patchID := range ids {
		record := a.monsterEnhanceOwned[patchID]
		current, err := a.readMonsterEnhanceEntry(record.Target, len(record.Patched))
		if err != nil {
			return fmt.Errorf("monster %s entry read failed: %w", patchID, err)
		}
		if !bytesEqual(current, record.Original) {
			if !bytesEqual(current, record.Patched) {
				return fmt.Errorf("monster %s entry is no longer owned: %s", patchID, bytesToHex(current))
			}
			if record.Cave != 0 {
				cave, ok := monsterEnhanceRelJumpTarget(record.Target, current)
				if !ok || cave != record.Cave {
					return fmt.Errorf("monster %s jump target changed", patchID)
				}
				if err := a.verifyMonsterEnhanceCaveMarker(record); err != nil {
					return fmt.Errorf("monster %s cave ownership failed: %w", patchID, err)
				}
			}
			if err := a.writeAndVerifyMonsterEnhanceEntry(record.Target, record.Original, "monster "+patchID+" entry restore"); err != nil {
				return err
			}
		}
		if record.AuxTarget != 0 {
			aux, readErr := a.readMonsterEnhanceEntry(record.AuxTarget, len(record.AuxPatched))
			if readErr != nil {
				return fmt.Errorf("monster %s auxiliary read failed: %w", patchID, readErr)
			}
			if !bytesEqual(aux, record.AuxOriginal) {
				if !bytesEqual(aux, record.AuxPatched) {
					return fmt.Errorf("monster %s auxiliary entry is no longer owned: %s", patchID, bytesToHex(aux))
				}
				if record.AuxCave != 0 {
					cave, ok := monsterEnhanceRelJumpTarget(record.AuxTarget, aux)
					if !ok || cave != record.AuxCave {
						return fmt.Errorf("monster %s auxiliary jump target changed", patchID)
					}
					if err := a.verifyMonsterEnhanceCaveMarker(monsterEnhanceOwnedPatch{Cave: record.AuxCave, CaveSize: record.AuxCaveSize}); err != nil {
						return fmt.Errorf("monster %s auxiliary cave ownership failed: %w", patchID, err)
					}
				}
				if err := a.writeAndVerifyMonsterEnhanceEntry(record.AuxTarget, record.AuxOriginal, "monster "+patchID+" auxiliary restore"); err != nil {
					return err
				}
			}
		}
		delete(a.monsterEnhanceOwned, patchID)
	}
	if len(a.monsterEnhanceOwned) == 0 {
		a.monsterEnhanceOwned = nil
	}
	return nil
}
