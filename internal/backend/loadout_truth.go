package backend

import (
	"fmt"
	"strings"
)

// loadoutSigilAccess classifies a factor without turning "not in our catalog"
// into "universal". Known generic factors are always selectable; known
// character factors and unknown real-save factors require an existing use by
// the same character. The generic result is true only when the catalog proves
// that classification.
func loadoutSigilAccess(cat *Catalog, hash uint32, precedent map[uint32]bool) (generic, allowed bool) {
	if cat == nil || hash == 0 || hash == EmptyHash {
		return false, false
	}
	def := cat.LookupSigilByHash(hash)
	if def == nil {
		return false, precedent[hash]
	}
	if def.Category != nil && *def.Category == "character_sigil" {
		return false, precedent[hash]
	}
	return true, true
}

// loadoutSigilDisplayNameFromTraits keeps uncatalogued save records useful
// without presenting their opaque hash as if it were a game-facing name.
func loadoutSigilDisplayNameFromTraits(hash uint32, primary, secondary string) string {
	if name := strings.TrimSpace(sigilDisplayName(hash)); name != "" {
		return name
	}
	primary = strings.TrimSpace(primary)
	secondary = strings.TrimSpace(secondary)
	switch {
	case primary != "" && secondary != "" && primary != secondary:
		return primary + " + " + secondary
	case primary != "":
		return primary
	case secondary != "":
		return secondary
	case useChinese():
		return "未收录因子"
	default:
		return "Uncatalogued Sigil"
	}
}

func validateLoadoutWeaponDefinition(hash uint32, ownerCode string) (ProgressionWeaponDef, error) {
	def, ok := progressionWeaponDefForLoadout(hash)
	if !ok {
		return ProgressionWeaponDef{}, fmt.Errorf("武器 %08X 未收录、是哨兵值或属于内部隐藏记录", hash)
	}
	if def.OwnerCode == "" {
		return def, nil
	}
	if ownerCode == "" {
		return ProgressionWeaponDef{}, fmt.Errorf("无法确定角色武器归属码，不能装备「%s」", progressionWeaponName(def))
	}
	if def.OwnerCode != ownerCode {
		return ProgressionWeaponDef{}, fmt.Errorf("武器「%s」属于 %s，不能装到 %s", progressionWeaponName(def), def.OwnerCode, ownerCode)
	}
	return def, nil
}
