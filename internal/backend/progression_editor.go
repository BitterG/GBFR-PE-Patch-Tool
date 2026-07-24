package backend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed data/items.json
var progressionItemsJSON []byte

//go:embed data/weapons.json
var progressionWeaponsJSON []byte

const (
	itemIDType      uint32 = 1801
	itemCountIDType uint32 = 1802
	itemFlagsIDType uint32 = 1803
	normalItemFlags uint32 = 12
	maxItemQuantity        = 999

	weaponMaxSlotIDType       uint32 = 2801
	weaponSlotIDType          uint32 = 2802
	weaponIDType              uint32 = 2803
	weaponXPIDType            uint32 = 2804
	weaponUncapIDType         uint32 = 2805
	weaponMirageIDType        uint32 = 2806
	weaponAwakeIDType         uint32 = 2807
	weaponFlagsIDType         uint32 = 2813
	weaponVariantIDType       uint32 = 2814
	weaponStateIDType         uint32 = 2815
	weaponStoneSubType        uint32 = 2816
	weaponTranscendenceIDType uint32 = 2817
	weaponExtraIDType         uint32 = 2818
	weaponSlotBase                   = 40000
	weaponImbuedTraitBase            = 130000000
	weaponTraitStride                = 100
)

func weaponImbuedTraitUnitBase(weaponUnitID uint32) (uint32, error) {
	if weaponUnitID < weaponSlotBase {
		return 0, fmt.Errorf("武器实例 %d 越界，无法定位祝福生效词条", weaponUnitID)
	}
	base := uint64(weaponImbuedTraitBase) + uint64(weaponUnitID-weaponSlotBase)*uint64(weaponTraitStride)
	if base+2 > uint64(^uint32(0)) {
		return 0, fmt.Errorf("武器实例 %d 的祝福生效词条编号溢出", weaponUnitID)
	}
	return uint32(base), nil
}

// weaponExpByLevel is the cumulative EXP table extracted from the installed
// 2.0.2 game's system/table/weapon_exp.tbl. Index 0 is level 1.
var weaponExpByLevel = [...]uint32{
	0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 105, 120, 135, 150, 165, 180, 195, 210, 225, 240,
	260, 280, 300, 320, 340, 360, 380, 400, 420, 440, 470, 500, 530, 560, 590, 630, 670, 710, 750, 790,
	860, 930, 1000, 1070, 1140, 1240, 1340, 1440, 1540, 1740, 1940, 2140, 2340, 2540, 2740, 2940, 3140, 3340, 3540, 3840,
	4140, 4440, 4740, 5040, 5340, 5640, 5940, 6240, 6540, 7040, 7540, 8040, 8540, 9040, 9540, 10040, 10540, 11040, 11540, 12040,
	12540, 13040, 13540, 14040, 14540, 15040, 15540, 16040, 16540, 17040, 17540, 18040, 18540, 19040, 19540, 20040, 20540, 21040, 21540, 22540,
	23540, 24540, 25540, 26540, 27540, 28540, 29540, 30540, 31540, 32740, 33940, 35140, 36340, 37540, 38740, 39940, 41140, 42340, 43540, 45040,
	46540, 48040, 49540, 51040, 52540, 54540, 56540, 58540, 60540, 62540, 65540, 68540, 71540, 74540, 77540, 81540, 85540, 89540, 93540, 97540,
	102540, 107540, 112540, 117540, 122540, 130040, 137540, 145040, 152540, 162540,
}

type ProgressionItemDef struct {
	Hash      string `json:"hash"`
	NameEN    string `json:"nameEn"`
	NameCN    string `json:"nameCn"`
	Category  string `json:"category"`
	Dangerous bool   `json:"dangerous"`
}

type ProgressionWeaponDef struct {
	Hash          string `json:"hash"`
	InternalID    string `json:"internalId"`
	Name          string `json:"name"`
	NameCN        string `json:"nameCn"`
	OwnerCode     string `json:"ownerCode"`
	MaxLevel      int    `json:"maxLevel"`
	WeaponType    string `json:"weaponType"`
	CanAwaken     bool   `json:"canAwaken"`
	AliasOf       string `json:"aliasOf,omitempty"`
	CatalogHidden bool   `json:"catalogHidden,omitempty"`
}

var ascensionWeaponHashes = map[uint32]bool{
	0xD7CEE3B8: true, 0x0E0287DC: true, 0x8B8FCB4E: true, 0x4E1AB7BB: true, 0x22E79816: true,
	0x9249B9CA: true, 0xE92002CA: true, 0x0FAE326F: true, 0x72AAE787: true, 0x776CA5A8: true,
	0x98F8CA3E: true, 0x4C3A7F95: true, 0x761A3597: true, 0xB5C31AB2: true, 0x099C8066: true,
	0xF4C1DE98: true, 0x02352554: true, 0xBDDCD5E3: true, 0x06F51A26: true, 0x95ACAD77: true,
	0x3C667A36: true, 0x1CC90CAE: true, 0x969CF8C7: true, 0x14B3AE92: true, 0x6EC326D3: true,
	0x1EB2B398: true,
}

var terminusWeaponHashes = map[uint32]bool{
	0xE6518D5A: true, 0xC9FCCCC6: true, 0xE04031CF: true, 0xF1845E70: true, 0xFEBAC81A: true,
	0x3F39B5EB: true, 0x07BB324C: true, 0x2A924378: true, 0x7027741C: true, 0xC84002E0: true,
	0x84BF5DE8: true, 0xE82DF9EC: true, 0x687ECE62: true, 0xA1F477D6: true, 0xFFF2F00F: true,
	0xAB6678D2: true, 0x304E4448: true, 0x411C89ED: true, 0x7BFF8253: true, 0x1DE38944: true,
	0xDFBB5727: true, 0x74D764B7: true, 0x1064441E: true, 0x10180036: true, 0x90F5B18F: true,
	0x6CFF175C: true, 0x6EEA0D21: true, 0xD5EB1DEE: true, 0xCDB13688: true,
}

var specialAwakeningWeaponHashes = map[uint32]bool{
	0xAD915067: true, 0xFA5F32D5: true, 0x4CBA06D8: true,
}

// Awakening changes the stored weapon hash at its milestone stages. The UI
// still needs to resolve those records back to the base weapon and category.
// Extracted from 2.0.2 system/table/weapon.tbl (WeaponId2 + awakening rows).
var awakeningWeaponAliases = map[uint32]uint32{
	0x08AC9299: 0xD7CEE3B8, 0x7F31E0D6: 0xD7CEE3B8, 0x2E9C27AC: 0xD7CEE3B8, 0x77AB0809: 0xD7CEE3B8, 0x2AF2A118: 0xE6518D5A, 0x9B9F83A2: 0xE6518D5A, 0x633DD711: 0xE6518D5A, 0x0D7B7A95: 0xE6518D5A,
	0x775E242A: 0x0E0287DC, 0x24C815AB: 0x0E0287DC, 0x34AD70E6: 0x0E0287DC, 0x1E90ADB4: 0x0E0287DC, 0xFED83C75: 0xC9FCCCC6, 0x027915BC: 0xC9FCCCC6, 0xE5D2832B: 0xC9FCCCC6, 0xCE12662A: 0xC9FCCCC6,
	0x2E393B84: 0x8B8FCB4E, 0xA9E77ED8: 0x8B8FCB4E, 0x288D5590: 0x8B8FCB4E, 0x606C0C73: 0xE04031CF, 0xD66D351C: 0xE04031CF, 0xA44B08BF: 0xE04031CF, 0xD6070A15: 0x4E1AB7BB, 0x1006ED87: 0x4E1AB7BB, 0xD5E0BF7F: 0x4E1AB7BB, 0xBC72036A: 0xF1845E70, 0x43A93192: 0xF1845E70, 0x7B5D7822: 0xF1845E70,
	0x26AD10BC: 0x22E79816, 0xFB5818E6: 0x22E79816, 0x56676778: 0x22E79816, 0xBC5A4248: 0xFEBAC81A, 0x628521DE: 0xFEBAC81A, 0x1779CD60: 0xFEBAC81A, 0xC1F3AD7B: 0x9249B9CA, 0xBE726E20: 0x9249B9CA, 0xB5341662: 0x9249B9CA, 0x5A744CE3: 0x3F39B5EB, 0x5F751A07: 0x3F39B5EB, 0xB3003C9B: 0x3F39B5EB,
	0xAED9EBF8: 0xE92002CA, 0x301DBAB7: 0xE92002CA, 0x93244909: 0xE92002CA, 0xEBBE11A9: 0x07BB324C, 0x25FF2DB3: 0x07BB324C, 0xBCD65F7D: 0x07BB324C, 0x1AB040DD: 0x2A924378, 0x28A1C76A: 0x2A924378, 0x45ADDF75: 0x2A924378, 0xDDDC7C2E: 0x0FAE326F, 0xD47F8689: 0x0FAE326F, 0x33D018E0: 0x0FAE326F,
	0x241B6D32: 0x72AAE787, 0xDC75CE8A: 0x72AAE787, 0x4CA01DB9: 0x72AAE787, 0x3D86CCE3: 0x7027741C, 0x40F726F9: 0x7027741C, 0x9FFA80BB: 0x7027741C, 0x9FFC73A8: 0x776CA5A8, 0x51E080FA: 0x776CA5A8, 0xF7C7B5DC: 0x776CA5A8, 0xD7280FE5: 0xC84002E0, 0x0B1C14C1: 0xC84002E0, 0x227B690E: 0xC84002E0,
	0x79ADECD8: 0x98F8CA3E, 0x1EA049BC: 0x98F8CA3E, 0xB3DDAAE1: 0x98F8CA3E, 0xDC959F74: 0x84BF5DE8, 0xF5CF6185: 0x84BF5DE8, 0x9F671440: 0x84BF5DE8, 0x05DEFB89: 0x4C3A7F95, 0xB81CF304: 0x4C3A7F95, 0x361E2D95: 0x4C3A7F95, 0xF053C91E: 0xE82DF9EC, 0xE4C1B247: 0xE82DF9EC, 0x2F639454: 0xE82DF9EC,
	0x1DEC52B3: 0x761A3597, 0x90A18C66: 0x761A3597, 0x1D0E9A84: 0x761A3597, 0xFBCCF69B: 0x687ECE62, 0xCBA1F4E1: 0x687ECE62, 0x847BD571: 0x687ECE62, 0xDC9D7C26: 0xB5C31AB2, 0xBC7F985E: 0xB5C31AB2, 0x51836359: 0xB5C31AB2, 0xA9E805AE: 0xA1F477D6, 0x2A95501E: 0xA1F477D6, 0xF6FF4CB2: 0xA1F477D6,
	0xF246096B: 0x099C8066, 0x64DD708D: 0x099C8066, 0xAFBD8B45: 0x099C8066, 0x19A77E54: 0xFFF2F00F, 0x2CBCACCA: 0xFFF2F00F, 0xA44EB1B4: 0xFFF2F00F, 0x02A9B90B: 0xF4C1DE98, 0x22E9C126: 0xF4C1DE98, 0x48110BA3: 0xF4C1DE98, 0xDC144611: 0xAB6678D2, 0xA8D420F2: 0xAB6678D2, 0xCD8CA605: 0xAB6678D2,
	0x5403D78E: 0x02352554, 0xFD7BF3D6: 0x02352554, 0x292C88E4: 0x02352554, 0xD8B6E17C: 0x304E4448, 0x348F349C: 0x304E4448, 0x80AD3AEB: 0x304E4448, 0x607BFD73: 0xBDDCD5E3, 0x4868F8B4: 0xBDDCD5E3, 0xDD177582: 0xBDDCD5E3, 0xBE4D1A95: 0x411C89ED, 0x67A31199: 0x411C89ED, 0x419B3CC0: 0x411C89ED,
	0x2BE820F1: 0x06F51A26, 0x05B4630A: 0x06F51A26, 0x6C6D9083: 0x06F51A26, 0x2E3C9E02: 0x7BFF8253, 0x7C88222E: 0x7BFF8253, 0x6A9D3DD0: 0x7BFF8253, 0xB586AE5B: 0x95ACAD77, 0xC05FF343: 0x95ACAD77, 0x7A0A0E34: 0x95ACAD77, 0xC38561AB: 0x1DE38944, 0x3EF31D1C: 0x1DE38944, 0x4D9B69A5: 0x1DE38944,
	0x74804589: 0xDFBB5727, 0xABDF528D: 0xDFBB5727, 0xD016FEB3: 0xDFBB5727, 0x8E2E15EB: 0xAD915067, 0xD21F4078: 0xAD915067, 0x1E8011EB: 0xAD915067, 0xAEF15742: 0x74D764B7, 0xD5FE66A2: 0x74D764B7, 0x1EA819A4: 0x74D764B7, 0x1C963180: 0xFA5F32D5, 0x5800CFD6: 0xFA5F32D5, 0x02E70DE2: 0xFA5F32D5,
	0x82AC447F: 0x1064441E, 0xDE958CDB: 0x1064441E, 0x40204755: 0x1064441E, 0x60372BEF: 0x4CBA06D8, 0xB25574F8: 0x4CBA06D8, 0x7DD506A3: 0x4CBA06D8, 0xFD6B963E: 0x3C667A36, 0xDE80BC37: 0x3C667A36, 0x7F10010D: 0x3C667A36, 0x974DBC97: 0x10180036, 0x6A018933: 0x10180036, 0x5DE1B311: 0x10180036,
	0x1A961AC5: 0x1CC90CAE, 0x4102B6AB: 0x1CC90CAE, 0x802B09DA: 0x1CC90CAE, 0x72AB825D: 0x90F5B18F, 0x87E6E352: 0x90F5B18F, 0x860AC3BF: 0x90F5B18F, 0x1154A6C6: 0x969CF8C7, 0x84F432D7: 0x969CF8C7, 0x78544A75: 0x969CF8C7, 0x51F70303: 0x6CFF175C, 0xB2931FAA: 0x6CFF175C, 0xA716D1F2: 0x6CFF175C,
	0x6D381177: 0x14B3AE92, 0xE15BA40E: 0x14B3AE92, 0x4FEE9341: 0x14B3AE92, 0x7E092CCD: 0x6EEA0D21, 0xE3B35C0D: 0x6EEA0D21, 0xDED16FCF: 0x6EEA0D21, 0x1826996A: 0x6EC326D3, 0xA14B98D8: 0x6EC326D3, 0x64705E7B: 0x6EC326D3, 0x82E85115: 0xD5EB1DEE, 0x9AE66FBD: 0xD5EB1DEE, 0xAD99E05E: 0xD5EB1DEE,
	0x165F82F5: 0x1EB2B398, 0x280EA816: 0x1EB2B398, 0x219EE448: 0x1EB2B398, 0x18B8476C: 0xCDB13688, 0x32B5DC17: 0xCDB13688, 0xF0B8CF77: 0xCDB13688,
}

// storedWeaponAliases are additional hashes observed in 2.0.2 WeaponId2 and
// archive save records. They identify the same player-facing weapon as the
// catalog hash on the right; keeping the alias explicit avoids showing a raw
// eight-character hash or treating a character weapon as universal.
var storedWeaponAliases = map[uint32]uint32{
	// WeaponId2 aliases.
	0xC2D446F7: 0xD4CED80E,
	0xCDE3B884: 0x7463358A,
	0xB1C0E0C2: 0xF7D69475,
	0xD2DFBE87: 0x159CA5B6,
	0x8A14E9DB: 0x283CC36B,
	0x3D94F6E9: 0xBEFFB034,
	0x1F0BCDBA: 0xB03EA930,
	0x095082AB: 0x3B2082B6,
	0x90CDA5F3: 0xBA30BD26,
	0xCD19623A: 0x75EC54D0,
	0xE45ED17F: 0x9240D597,
	// Archive/compatibility aliases seen in real saves.
	0xE180DADB: 0x3EC1D082,
	0x76265AA7: 0xDB8ED674,
	0x6E59B0DD: 0xCB5A08CD,
	0x08DE4F36: 0xDAA4D559,
	0x1A977F3F: 0xD4CED80E,
}

func decorateProgressionWeaponDef(def ProgressionWeaponDef, hash uint32) ProgressionWeaponDef {
	def.WeaponType = "normal"
	if ascensionWeaponHashes[hash] {
		def.WeaponType, def.CanAwaken = "ascension", true
	} else if terminusWeaponHashes[hash] {
		def.WeaponType, def.CanAwaken = "terminus", true
	} else if specialAwakeningWeaponHashes[hash] {
		def.WeaponType, def.CanAwaken = "special", true
	}
	return def
}

type ProgressionCatalog struct {
	Version string                 `json:"version"`
	Items   []ProgressionItemDef   `json:"items"`
	Weapons []ProgressionWeaponDef `json:"weapons"`
}

type OwnedProgressionItem struct {
	UnitID   uint32 `json:"unitId"`
	Hash     string `json:"hash"`
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
	Flags    uint32 `json:"flags"`
}

type OwnedProgressionWeapon struct {
	UnitID                 uint32                   `json:"unitId"`
	SlotID                 uint32                   `json:"slotId"`
	Hash                   string                   `json:"hash"`
	Name                   string                   `json:"name"`
	Level                  int                      `json:"level"`
	XP                     uint32                   `json:"xp"`
	Uncap                  int                      `json:"uncap"`
	Mirage                 int                      `json:"mirage"`
	Awakening              int                      `json:"awakening"`
	WeaponType             string                   `json:"weaponType"`
	CanAwaken              bool                     `json:"canAwaken"`
	BaseHash               string                   `json:"baseHash,omitempty"`
	OwnerCode              string                   `json:"ownerCode,omitempty"`
	InternalID             string                   `json:"internalId,omitempty"`
	Transcendence          int                      `json:"transcendence"`
	TranscendenceSkill     string                   `json:"transcendenceSkill,omitempty"`
	TranscendenceSkillName string                   `json:"transcendenceSkillName,omitempty"`
	SkillSlots             []LoadoutWeaponSkillSlot `json:"skillSlots,omitempty"`
}

type ProgressionInventory struct {
	Path          string                   `json:"path"`
	Rupees        int32                    `json:"rupees"`
	Mastery       int32                    `json:"mastery"`
	Commendations int32                    `json:"commendations"`
	Items         []OwnedProgressionItem   `json:"items"`
	Weapons       []OwnedProgressionWeapon `json:"weapons"`
	EmptyItems    int                      `json:"emptyItems"`
	EmptyWeapons  int                      `json:"emptyWeapons"`
}

type ProgressionItemChange struct {
	Hash           string `json:"hash"`
	Quantity       int    `json:"quantity"`
	Mode           string `json:"mode"` // add | set
	AllowDangerous bool   `json:"allowDangerous"`
}

type ProgressionWeaponChange struct {
	Action             string   `json:"action"` // add | update
	UnitID             uint32   `json:"unitId"`
	Hash               string   `json:"hash"`
	Level              int      `json:"level"`
	Uncap              int      `json:"uncap"`
	Mirage             int      `json:"mirage"`
	Awakening          int      `json:"awakening"`
	Transcendence      int      `json:"transcendence"`
	TranscendenceSkill string   `json:"transcendenceSkill"`
	SkillHashes        []string `json:"skillHashes,omitempty"`
}

var weaponTranscendenceSkills = map[uint32]string{
	0xBBD77C33: "超凡强击",
	0x020DB733: "超凡技艺",
	0x3F682593: "超凡奥秘",
	0x79027FC8: "超凡破限",
}

func parseWeaponTranscendenceSkill(value string) (uint32, error) {
	hash, err := ParseHashHex(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("weapon transcendence skill is invalid: %w", err)
	}
	if _, valid := weaponTranscendenceSkills[hash]; !valid {
		return 0, fmt.Errorf("weapon transcendence skill 0x%08X is not in the audited DLC 2.0.2 catalog", hash)
	}
	return hash, nil
}

type ProgressionResourceChange struct {
	Kind  string `json:"kind"` // rupees | mastery | commendations
	Value int32  `json:"value"`
}

type ProgressionApplyResult struct {
	OutputPath      string `json:"outputPath"`
	BackupPath      string `json:"backupPath"`
	ItemsChanged    int    `json:"itemsChanged"`
	WeaponsAdded    int    `json:"weaponsAdded"`
	WeaponsUpdated  int    `json:"weaponsUpdated"`
	VerifiedChanges int    `json:"verifiedChanges"`
}

func progressionItemName(def ProgressionItemDef) string {
	if useChinese() {
		if strings.TrimSpace(def.NameCN) != "" {
			return def.NameCN
		}
		return fmt.Sprintf("未收录物品 %s", def.Hash)
	}
	if strings.TrimSpace(def.NameEN) != "" {
		return def.NameEN
	}
	return fmt.Sprintf("Uncatalogued Item %s", def.Hash)
}

func progressionWeaponName(def ProgressionWeaponDef) string {
	if useChinese() {
		if strings.TrimSpace(def.NameCN) != "" {
			return def.NameCN
		}
		if def.OwnerCode != "" {
			return fmt.Sprintf("未收录角色武器 %s", def.InternalID)
		}
		return fmt.Sprintf("未收录武器 %s", def.Hash)
	}
	if strings.TrimSpace(def.Name) != "" {
		return def.Name
	}
	return fmt.Sprintf("Uncatalogued Weapon %s", def.Hash)
}

var (
	progressionCatalogOnce  sync.Once
	progressionCatalogErr   error
	progressionCatalogCache *ProgressionCatalog
	progressionItemByHash   map[uint32]ProgressionItemDef
	progressionWeaponByHash map[uint32]ProgressionWeaponDef
)

func progressionWeaponDefForHash(hash uint32) (ProgressionWeaponDef, bool) {
	if def, ok := progressionWeaponByHash[hash]; ok {
		return def, true
	}
	baseHash, ok := awakeningWeaponAliases[hash]
	if !ok {
		baseHash, ok = storedWeaponAliases[hash]
		if !ok {
			return ProgressionWeaponDef{}, false
		}
	}
	def, ok := progressionWeaponByHash[baseHash]
	if !ok {
		return ProgressionWeaponDef{}, false
	}
	def.AliasOf = fmt.Sprintf("%08X", baseHash)
	def.CatalogHidden = true
	return def, true
}

// progressionWeaponDefForLoadout is the fail-closed resolver used by the
// loadout editor and writer. Unknown/sentinel hashes and internal compatibility
// mirrors are never offered or accepted; aliases still resolve to the real
// localized weapon definition and owner.
func progressionWeaponDefForLoadout(hash uint32) (ProgressionWeaponDef, bool) {
	if hash == 0 || hash == EmptyHash || !progressionWeaponVisibleInInventory(hash) {
		return ProgressionWeaponDef{}, false
	}
	def, ok := progressionWeaponDefForHash(hash)
	if !ok || strings.TrimSpace(progressionWeaponName(def)) == "" {
		return ProgressionWeaponDef{}, false
	}
	return def, true
}

func progressionWeaponVisibleInInventory(hash uint32) bool {
	// Only explicit compatibility mirrors (catalogued directly in
	// progressionWeaponByHash with CatalogHidden, e.g. 小女巫权杖（兼容副本）)
	// must be hidden — they duplicate a base weapon that is also present.
	//
	// An awakened-variant hash is NOT in progressionWeaponByHash; it only
	// resolves through awakeningWeaponAliases, and progressionWeaponDefForHash
	// synthesizes CatalogHidden=true for it. That variant is the player's real,
	// upgraded weapon and must stay visible — otherwise the owned list drops it,
	// the catalog offers its base as "可添加", yet findWeaponAddSlot rejects the
	// add as a duplicate (via weaponBaseHash). See slot 40255 / [黑榫]幽冥华冠.
	if def, ok := progressionWeaponByHash[hash]; ok {
		return !def.CatalogHidden
	}
	return true
}

func loadProgressionCatalog() (*ProgressionCatalog, error) {
	progressionCatalogOnce.Do(func() {
		progressionCatalogCache, progressionItemByHash, progressionWeaponByHash, progressionCatalogErr = buildProgressionCatalog()
	})
	return progressionCatalogCache, progressionCatalogErr
}

func buildProgressionCatalog() (*ProgressionCatalog, map[uint32]ProgressionItemDef, map[uint32]ProgressionWeaponDef, error) {
	var items struct {
		Version string               `json:"version"`
		Items   []ProgressionItemDef `json:"items"`
	}
	var weapons struct {
		Version string                 `json:"version"`
		Weapons []ProgressionWeaponDef `json:"weapons"`
	}
	if err := json.Unmarshal(progressionItemsJSON, &items); err != nil {
		return nil, nil, nil, fmt.Errorf("解析物品目录失败: %w", err)
	}
	if err := json.Unmarshal(progressionWeaponsJSON, &weapons); err != nil {
		return nil, nil, nil, fmt.Errorf("解析武器目录失败: %w", err)
	}
	if items.Version != "2.0.2" || weapons.Version != "2.0.2" {
		return nil, nil, nil, fmt.Errorf("养成目录版本不一致")
	}

	itemByHash := make(map[uint32]ProgressionItemDef, len(items.Items))
	for _, item := range items.Items {
		hash, err := ParseHashHex(item.Hash)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("物品 %q 哈希无效: %w", item.NameCN, err)
		}
		if _, exists := itemByHash[hash]; exists {
			return nil, nil, nil, fmt.Errorf("物品目录存在重复哈希: %s", item.Hash)
		}
		itemByHash[hash] = item
	}
	weaponByHash := make(map[uint32]ProgressionWeaponDef, len(weapons.Weapons))
	for i, weapon := range weapons.Weapons {
		hash, err := ParseHashHex(weapon.Hash)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("武器 %q 哈希无效: %w", weapon.Name, err)
		}
		if _, exists := weaponByHash[hash]; exists {
			return nil, nil, nil, fmt.Errorf("武器目录存在重复哈希: %s", weapon.Hash)
		}
		weapon = decorateProgressionWeaponDef(weapon, hash)
		weapons.Weapons[i] = weapon
		weaponByHash[hash] = weapon
	}
	// The special awakening rows use three canonical hashes that are absent
	// from weapons.json, while the same named/owned weapon is catalogued under
	// its ordinary record. Materialize resolver-only definitions so every
	// observed awakening stage keeps its true name, owner and category.
	for specialHash, sourceHash := range map[uint32]uint32{
		0xAD915067: 0x2C4CAADD,
		0xFA5F32D5: 0x73D34F1B,
		0x4CBA06D8: 0xDA807CA2,
	} {
		source, ok := weaponByHash[sourceHash]
		if !ok {
			return nil, nil, nil, fmt.Errorf("特殊觉醒武器来源 %08X 未收录", sourceHash)
		}
		source.Hash = fmt.Sprintf("%08X", specialHash)
		weaponByHash[specialHash] = decorateProgressionWeaponDef(source, specialHash)
	}
	// 2.0.2 keeps compatibility copies for a few base weapons. They are valid
	// inventory records but must not be offered as separately addable weapons.
	for _, alias := range []ProgressionWeaponDef{
		{Hash: "EE1EBC2E", InternalID: "WEP_PL0400_A0", Name: "Little Witch Scepter (compatibility copy)", NameCN: "小女巫权杖（兼容副本）", OwnerCode: "PL0400", MaxLevel: 150, AliasOf: "C54B8FE6", CatalogHidden: true},
	} {
		hash, _ := ParseHashHex(alias.Hash)
		alias = decorateProgressionWeaponDef(alias, hash)
		weaponByHash[hash] = alias
		weapons.Weapons = append(weapons.Weapons, alias)
	}

	catalog := &ProgressionCatalog{Version: "2.0.2", Items: items.Items, Weapons: weapons.Weapons}
	return catalog, itemByHash, weaponByHash, nil
}

func (a *App) ProgressionGetCatalog() (*ProgressionCatalog, error) {
	return loadProgressionCatalog()
}

func (a *App) SelectProgressionSave() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "选择 GBFR 存档",
		Filters: []runtime.FileFilter{{DisplayName: "GBFR 存档 (*.dat)", Pattern: "*.dat"}},
	})
}

func (a *App) ProgressionLoad(path string) (*ProgressionInventory, error) {
	if _, err := loadProgressionCatalog(); err != nil {
		return nil, err
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	return progressionInventory(save, path), nil
}

func progressionInventory(save *SaveData, path string) *ProgressionInventory {
	result := &ProgressionInventory{Path: path}
	if entry, ok := save.findUnit(SaveID_Rupees, 0); ok {
		result.Rupees = entry.Int32()
	}
	if entry, ok := save.findUnit(SaveID_MasteryPoints, 0); ok {
		result.Mastery = entry.Int32()
	}
	if entry, ok := save.findUnit(SaveID_Commendations, 0); ok {
		result.Commendations = entry.Int32()
	}
	itemCounts := entriesByUnitID(save.findAllUnitsByType(itemCountIDType))
	itemFlags := entriesByUnitID(save.findAllUnitsByType(itemFlagsIDType))
	for _, entry := range save.findAllUnitsByType(itemIDType) {
		hash := entry.Uint32()
		if hash == EmptyHash {
			result.EmptyItems++
			continue
		}
		quantity := 0
		if count := itemCounts[entry.UnitID]; count != nil {
			quantity = int(count.Int32())
		}
		flags := uint32(0)
		if flag := itemFlags[entry.UnitID]; flag != nil {
			flags = flag.Uint32()
		}
		name := fmt.Sprintf("Unknown Item 0x%08X", hash)
		if useChinese() {
			name = fmt.Sprintf("未知物品 0x%08X", hash)
		}
		if def, ok := progressionItemByHash[hash]; ok {
			name = progressionItemName(def)
		}
		result.Items = append(result.Items, OwnedProgressionItem{UnitID: entry.UnitID, Hash: fmt.Sprintf("%08X", hash), Name: name, Quantity: quantity, Flags: flags})
	}

	weaponSlots := entriesByUnitID(save.findAllUnitsByType(weaponSlotIDType))
	weaponXP := entriesByUnitID(save.findAllUnitsByType(weaponXPIDType))
	weaponUncap := entriesByUnitID(save.findAllUnitsByType(weaponUncapIDType))
	weaponMirage := entriesByUnitID(save.findAllUnitsByType(weaponMirageIDType))
	weaponAwake := entriesByUnitID(save.findAllUnitsByType(weaponAwakeIDType))
	weaponTranscendence := entriesByUnitID(save.findAllUnitsByType(weaponTranscendenceIDType))
	weaponExtra := entriesByUnitID(save.findAllUnitsByType(weaponExtraIDType))
	for _, entry := range save.findAllUnitsByType(weaponIDType) {
		hash := entry.Uint32()
		if hash == EmptyHash {
			result.EmptyWeapons++
			continue
		}
		// Some DLC saves keep an internal compatibility mirror for a weapon.
		// It occupies a real record, but it is not a second player-owned weapon
		// and must not be exposed as a separate editable inventory entry.
		if !progressionWeaponVisibleInInventory(hash) {
			continue
		}
		unknownName := fmt.Sprintf("Unknown Weapon 0x%08X", hash)
		if useChinese() {
			unknownName = fmt.Sprintf("未知武器 0x%08X", hash)
		}
		owned := OwnedProgressionWeapon{UnitID: entry.UnitID, Hash: fmt.Sprintf("%08X", hash), Name: unknownName, Level: 1}
		if def, ok := progressionWeaponDefForHash(hash); ok {
			owned.Name = progressionWeaponName(def)
			owned.WeaponType = def.WeaponType
			owned.CanAwaken = def.CanAwaken
			owned.BaseHash = def.AliasOf
			owned.OwnerCode = def.OwnerCode
			owned.InternalID = def.InternalID
		}
		if e := weaponSlots[entry.UnitID]; e != nil {
			owned.SlotID = e.Uint32()
			if owned.SlotID != 0 && owned.SlotID != EmptyHash {
				if context, contextErr := readLoadoutWeaponContext(save, owned.SlotID); contextErr == nil {
					owned.SkillSlots = context.SkillSlots
				}
			}
		}
		if e := weaponXP[entry.UnitID]; e != nil {
			owned.XP = e.Uint32()
			owned.Level = weaponLevelForXP(owned.XP)
		}
		if e := weaponUncap[entry.UnitID]; e != nil {
			owned.Uncap = int(e.Int32())
		}
		if e := weaponMirage[entry.UnitID]; e != nil {
			owned.Mirage = int(e.Int32())
		}
		if e := weaponAwake[entry.UnitID]; e != nil {
			owned.Awakening = int(e.Int32())
		}
		if e := weaponTranscendence[entry.UnitID]; e != nil {
			owned.Transcendence = int(e.Int32())
		}
		if e := weaponExtra[entry.UnitID]; e != nil && e.ValueCnt >= 5 {
			if hash, err := e.Uint32At(4); err == nil && hash != EmptyHash {
				owned.TranscendenceSkill = fmt.Sprintf("%08X", hash)
				if name, ok := weaponTranscendenceSkills[hash]; ok {
					owned.TranscendenceSkillName = name
				}
			}
		}
		result.Weapons = append(result.Weapons, owned)
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Name < result.Items[j].Name })
	sort.Slice(result.Weapons, func(i, j int) bool { return result.Weapons[i].SlotID < result.Weapons[j].SlotID })
	return result
}

func entriesByUnitID(entries []*unitEntry) map[uint32]*unitEntry {
	result := make(map[uint32]*unitEntry, len(entries))
	for _, entry := range entries {
		result[entry.UnitID] = entry
	}
	return result
}

func weaponLevelForXP(xp uint32) int {
	index := sort.Search(len(weaponExpByLevel), func(i int) bool { return weaponExpByLevel[i] > xp })
	if index == 0 {
		return 1
	}
	if index > len(weaponExpByLevel) {
		return len(weaponExpByLevel)
	}
	return index
}

func weaponXPForLevel(level int) (uint32, error) {
	if level < 1 || level > len(weaponExpByLevel) {
		return 0, fmt.Errorf("武器等级必须在 1 到 %d 之间", len(weaponExpByLevel))
	}
	return weaponExpByLevel[level-1], nil
}

type progressionItemExpected struct {
	UnitID   uint32
	Hash     uint32
	Quantity int32
}

type progressionWeaponExpected struct {
	UnitID             uint32
	Hash               uint32
	XP                 uint32
	Uncap              int32
	Mirage             int32
	Awakening          int32
	Transcendence      int32
	TranscendenceSkill uint32
	SkillHashes        []uint32
}

func (a *App) ProgressionApply(inputPath, outputPath string, resourceChanges []ProgressionResourceChange, itemChanges []ProgressionItemChange, weaponChanges []ProgressionWeaponChange) (*ProgressionApplyResult, error) {
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()

	if len(resourceChanges) == 0 && len(itemChanges) == 0 && len(weaponChanges) == 0 {
		return nil, fmt.Errorf("没有待应用的修改")
	}
	if outputPath == "" {
		outputPath = inputPath
	}
	if samePath(inputPath, outputPath) {
		if _, err := findProcessByName(charaProcessName); err == nil {
			return nil, fmt.Errorf("覆盖当前存档前请先完全退出游戏，避免游戏把旧数据写回")
		}
	}
	if _, err := loadProgressionCatalog(); err != nil {
		return nil, err
	}
	save, err := LoadSave(inputPath)
	if err != nil {
		return nil, err
	}
	result := &ProgressionApplyResult{}
	resourceExpected := make(map[uint32]int32, len(resourceChanges))
	for _, change := range resourceChanges {
		idType, err := progressionResourceID(change.Kind)
		if err != nil {
			return nil, err
		}
		if change.Value < 0 || change.Value > 999999999 {
			return nil, fmt.Errorf("资源数量必须在 0 到 999999999 之间")
		}
		if _, exists := resourceExpected[idType]; exists {
			return nil, fmt.Errorf("资源 %s 被重复提交", change.Kind)
		}
		if err := save.patchInt(idType, 0, int(change.Value)); err != nil {
			return nil, err
		}
		resourceExpected[idType] = change.Value
	}
	itemExpected := make([]progressionItemExpected, 0, len(itemChanges))
	for _, change := range itemChanges {
		expected, err := applyProgressionItemChange(save, change)
		if err != nil {
			return nil, err
		}
		itemExpected = append(itemExpected, expected)
		result.ItemsChanged++
	}
	weaponExpected := make([]progressionWeaponExpected, 0, len(weaponChanges))
	for _, change := range weaponChanges {
		added, expected, err := applyProgressionWeaponChange(save, change)
		if err != nil {
			return nil, err
		}
		weaponExpected = append(weaponExpected, expected)
		if added {
			result.WeaponsAdded++
		} else {
			result.WeaponsUpdated++
		}
	}
	if err := save.FixChecksums(); err != nil {
		return nil, fmt.Errorf("修复存档校验失败: %w", err)
	}
	if err := save.Write(outputPath); err != nil {
		return nil, err
	}
	result.BackupPath = save.LastBackupPath()
	result.OutputPath, _ = filepath.Abs(outputPath)

	verify, err := LoadSave(outputPath)
	if err != nil {
		return nil, fmt.Errorf("修改已写入但重新读取失败: %w", err)
	}
	result.VerifiedChanges, err = verifyProgressionChanges(verify, resourceExpected, itemExpected, weaponExpected)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func progressionResourceID(kind string) (uint32, error) {
	switch kind {
	case "rupees":
		return SaveID_Rupees, nil
	case "mastery":
		return SaveID_MasteryPoints, nil
	case "commendations":
		return SaveID_Commendations, nil
	default:
		return 0, fmt.Errorf("未知资源类型: %s", kind)
	}
}

func applyProgressionItemChange(save *SaveData, change ProgressionItemChange) (progressionItemExpected, error) {
	var expected progressionItemExpected
	hash, err := ParseHashHex(strings.TrimSpace(change.Hash))
	if err != nil {
		return expected, fmt.Errorf("物品哈希无效: %w", err)
	}
	def, allowed := progressionItemByHash[hash]
	if !allowed {
		return expected, fmt.Errorf("物品 0x%08X 不在 DLC 2.0.2 验证目录中", hash)
	}
	itemName := progressionItemName(def)
	if def.Dangerous && !change.AllowDangerous {
		return expected, fmt.Errorf("%s 被标记为可能坏档，未获得实验写入确认", itemName)
	}
	if change.Quantity < 0 || change.Quantity > maxItemQuantity {
		return expected, fmt.Errorf("%s 数量必须在 0 到 %d 之间", itemName, maxItemQuantity)
	}
	if change.Mode != "add" && change.Mode != "set" {
		return expected, fmt.Errorf("未知物品修改模式: %s", change.Mode)
	}

	var target *unitEntry
	var empty *unitEntry
	for _, entry := range save.findAllUnitsByType(itemIDType) {
		if entry.Uint32() == hash {
			if target != nil {
				return expected, fmt.Errorf("存档中物品 %s 出现重复槽，拒绝自动修改", itemName)
			}
			target = entry
		} else if entry.Uint32() == EmptyHash && empty == nil {
			empty = entry
		}
	}
	if target == nil {
		if empty == nil {
			return expected, fmt.Errorf("物品槽已满，无法添加 %s", itemName)
		}
		target = empty
		if err := save.patchUintExact(itemIDType, target.UnitID, hash); err != nil {
			return expected, err
		}
		if err := save.patchUintExact(itemFlagsIDType, target.UnitID, normalItemFlags); err != nil {
			return expected, err
		}
	}
	countEntry, ok := save.findUnitExact(itemCountIDType, target.UnitID)
	if !ok {
		return expected, fmt.Errorf("物品槽 %d 缺少数量字段", target.UnitID)
	}
	quantity := change.Quantity
	if change.Mode == "add" {
		quantity += int(countEntry.Int32())
		if quantity > maxItemQuantity {
			quantity = maxItemQuantity
		}
	}
	countEntry.SetInt32(int32(quantity))
	return progressionItemExpected{UnitID: target.UnitID, Hash: hash, Quantity: int32(quantity)}, nil
}

func findWeaponAddSlot(entries []*unitEntry, hash uint32) (uint32, error) {
	var emptyUnitID uint32
	targetBase := weaponBaseHash(hash)
	for _, entry := range entries {
		if entry.Uint32() != EmptyHash && weaponBaseHash(entry.Uint32()) == targetBase {
			return 0, fmt.Errorf("存档已拥有武器 0x%08X，不能重复添加", hash)
		}
		if emptyUnitID == 0 && entry.Uint32() == EmptyHash && entry.UnitID >= weaponSlotBase {
			emptyUnitID = entry.UnitID
		}
	}
	if emptyUnitID == 0 {
		return 0, fmt.Errorf("武器槽已满")
	}
	return emptyUnitID, nil
}

func applyProgressionWeaponChange(save *SaveData, change ProgressionWeaponChange) (bool, progressionWeaponExpected, error) {
	var expected progressionWeaponExpected
	xp, err := weaponXPForLevel(change.Level)
	if err != nil {
		return false, expected, err
	}
	if change.Uncap < 0 || change.Uncap > 6 {
		return false, expected, fmt.Errorf("武器上限解锁阶段必须在 0 到 6 之间")
	}
	if change.Mirage < 0 || change.Mirage > 99 {
		return false, expected, fmt.Errorf("武器幻晶必须在 0 到 99 之间")
	}
	if change.Awakening < 0 || change.Awakening > 10 {
		return false, expected, fmt.Errorf("武器觉醒等级必须在 0 到 10 之间")
	}
	if change.Transcendence < 0 || change.Transcendence > 7 {
		return false, expected, fmt.Errorf("武器超凡突破等级必须在 0 到 7 之间")
	}

	unitID := change.UnitID
	var weaponHash uint32
	added := change.Action == "add"
	if added {
		hash, err := ParseHashHex(strings.TrimSpace(change.Hash))
		if err != nil {
			return false, expected, fmt.Errorf("武器哈希无效: %w", err)
		}
		weaponHash = hash
		if _, ok := progressionWeaponByHash[hash]; !ok {
			return false, expected, fmt.Errorf("武器 0x%08X 不在 DLC 2.0.2 验证目录中", hash)
		}
		unitID, err = findWeaponAddSlot(save.findAllUnitsByType(weaponIDType), hash)
		if err != nil {
			return false, expected, err
		}
		maxSlot, ok := save.findUnit(weaponMaxSlotIDType, 0)
		if !ok {
			return false, expected, fmt.Errorf("存档缺少武器最大槽位字段")
		}
		newSlotID := maxSlot.Uint32() + 1
		for _, required := range []uint32{weaponSlotIDType, weaponIDType, weaponXPIDType, weaponUncapIDType, weaponMirageIDType, weaponAwakeIDType, weaponFlagsIDType, weaponVariantIDType, weaponStateIDType, weaponStoneSubType, weaponTranscendenceIDType, weaponExtraIDType} {
			if _, ok := save.findUnit(required, unitID); !ok {
				return false, expected, fmt.Errorf("武器槽 %d 缺少字段 %d", unitID, required)
			}
		}
		maxSlot.SetUint32(newSlotID)
		for _, patch := range []struct{ id, value uint32 }{
			{weaponSlotIDType, newSlotID}, {weaponIDType, hash}, {weaponFlagsIDType, 0},
			{weaponVariantIDType, EmptyHash}, {weaponStateIDType, 1}, {weaponStoneSubType, EmptyHash},
		} {
			if err := save.patchUint(patch.id, unitID, patch.value); err != nil {
				return false, expected, err
			}
		}
		if err := save.patchInt(weaponTranscendenceIDType, unitID, 0); err != nil {
			return false, expected, err
		}
		if extra, ok := save.findUnit(weaponExtraIDType, unitID); ok {
			for i := 0; i < extra.ValueCnt; i++ {
				if err := extra.SetUint32At(i, EmptyHash); err != nil {
					return false, expected, err
				}
			}
		}
		traitBase, traitBaseErr := weaponImbuedTraitUnitBase(unitID)
		if traitBaseErr != nil {
			return false, expected, traitBaseErr
		}
		for i := 0; i < 3; i++ {
			traitUnit := traitBase + uint32(i)
			if entry, ok := save.findUnit(TraitHashIDType, traitUnit); ok {
				entry.SetUint32(EmptyHash)
			}
			if entry, ok := save.findUnit(TraitLevelIDType, traitUnit); ok {
				entry.SetInt32(0)
			}
		}
	} else if change.Action == "update" {
		entry, ok := save.findUnit(weaponIDType, unitID)
		if !ok || entry.Uint32() == EmptyHash {
			return false, expected, fmt.Errorf("找不到要修改的武器槽 %d", unitID)
		}
		weaponHash = entry.Uint32()
	} else {
		return false, expected, fmt.Errorf("未知武器操作: %s", change.Action)
	}
	if change.Awakening > 0 {
		def, known := progressionWeaponDefForHash(weaponHash)
		if !known || !def.CanAwaken {
			return false, expected, fmt.Errorf("该武器不是可觉醒武器，觉醒等级必须为 0")
		}
		if change.Level < 150 || change.Uncap < 6 {
			return false, expected, fmt.Errorf("写入武器觉醒前，等级必须为 150 且等级上限解锁为 6")
		}
	}
	if change.Transcendence > 0 && (change.Level < 150 || change.Uncap < 6) {
		return false, expected, fmt.Errorf("写入武器超凡突破前，等级必须为 150 且等级上限解锁为 6")
	}
	weaponHash = weaponHashForAwakening(weaponHash, change.Awakening)
	if err := save.patchUint(weaponIDType, unitID, weaponHash); err != nil {
		return false, expected, err
	}

	if err := save.patchUint(weaponXPIDType, unitID, xp); err != nil {
		return false, expected, err
	}
	if err := save.patchInt(weaponUncapIDType, unitID, change.Uncap); err != nil {
		return false, expected, err
	}
	if err := save.patchInt(weaponMirageIDType, unitID, change.Mirage); err != nil {
		return false, expected, err
	}
	if err := save.patchInt(weaponAwakeIDType, unitID, change.Awakening); err != nil {
		return false, expected, err
	}
	if err := save.patchInt(weaponTranscendenceIDType, unitID, change.Transcendence); err != nil {
		return false, expected, err
	}
	transcendenceSkill := EmptyHash
	var replacementSkills []uint32
	extra, ok := save.findUnit(weaponExtraIDType, unitID)
	if !ok || extra.ValueCnt < 5 {
		return false, expected, fmt.Errorf("武器槽 %d 缺少超凡强化效果字段", unitID)
	}
	if len(change.SkillHashes) > 0 {
		if len(change.SkillHashes) != 5 {
			return false, expected, fmt.Errorf("武器替换技能必须提供完整的 5 个槽位")
		}
		replacementSkills = make([]uint32, 5)
		for index, value := range change.SkillHashes {
			hash, parseErr := ParseHashHex(strings.TrimSpace(value))
			if parseErr != nil {
				return false, expected, fmt.Errorf("武器替换技能槽 %d 的哈希无效: %w", index+1, parseErr)
			}
			replacementSkills[index] = hash
		}
		for index, hash := range replacementSkills {
			if err := extra.SetUint32At(index, hash); err != nil {
				return false, expected, err
			}
		}
		transcendenceSkill = replacementSkills[4]
	}
	if change.Transcendence >= 7 {
		if len(replacementSkills) > 0 {
			transcendenceSkill = replacementSkills[4]
		} else if strings.TrimSpace(change.TranscendenceSkill) != "" {
			transcendenceSkill, err = parseWeaponTranscendenceSkill(change.TranscendenceSkill)
			if err != nil {
				return false, expected, fmt.Errorf("超凡强化效果 ID 无效: %w", err)
			}
		} else if current, readErr := extra.Uint32At(4); readErr == nil && weaponTranscendenceSkills[current] != "" {
			transcendenceSkill = current
		} else {
			transcendenceSkill = 0xBBD77C33
		}
	}
	if err := extra.SetUint32At(4, transcendenceSkill); err != nil {
		return false, expected, err
	}
	return added, progressionWeaponExpected{UnitID: unitID, Hash: weaponHash, XP: xp, Uncap: int32(change.Uncap), Mirage: int32(change.Mirage), Awakening: int32(change.Awakening), Transcendence: int32(change.Transcendence), TranscendenceSkill: transcendenceSkill, SkillHashes: replacementSkills}, nil
}

func verifyProgressionChanges(save *SaveData, resources map[uint32]int32, items []progressionItemExpected, weapons []progressionWeaponExpected) (int, error) {
	verified := 0
	for idType, value := range resources {
		entry, ok := save.findUnit(idType, 0)
		if !ok || entry.Int32() != value {
			return verified, fmt.Errorf("写入后验证资源字段 %d 失败", idType)
		}
		verified++
	}
	for _, expected := range items {
		id, ok := save.findUnitExact(itemIDType, expected.UnitID)
		count, countOK := save.findUnitExact(itemCountIDType, expected.UnitID)
		if !ok || !countOK || id.Uint32() != expected.Hash || count.Int32() != expected.Quantity {
			return verified, fmt.Errorf("写入后验证物品槽 %d 失败", expected.UnitID)
		}
		verified++
	}
	for _, expected := range weapons {
		id, idOK := save.findUnit(weaponIDType, expected.UnitID)
		xp, xpOK := save.findUnit(weaponXPIDType, expected.UnitID)
		uncap, uncapOK := save.findUnit(weaponUncapIDType, expected.UnitID)
		mirage, mirageOK := save.findUnit(weaponMirageIDType, expected.UnitID)
		awake, awakeOK := save.findUnit(weaponAwakeIDType, expected.UnitID)
		transcendence, transcendenceOK := save.findUnit(weaponTranscendenceIDType, expected.UnitID)
		extra, extraOK := save.findUnit(weaponExtraIDType, expected.UnitID)
		storedSkill := uint32(0)
		if extraOK && extra.ValueCnt >= 5 {
			storedSkill, _ = extra.Uint32At(4)
		}
		if !idOK || !xpOK || !uncapOK || !mirageOK || !awakeOK || !transcendenceOK || !extraOK || id.Uint32() != expected.Hash || xp.Uint32() != expected.XP || uncap.Int32() != expected.Uncap || mirage.Int32() != expected.Mirage || awake.Int32() != expected.Awakening || transcendence.Int32() != expected.Transcendence || storedSkill != expected.TranscendenceSkill {
			return verified, fmt.Errorf("写入后验证武器槽 %d 失败", expected.UnitID)
		}
		if len(expected.SkillHashes) > 0 {
			if extra.ValueCnt < len(expected.SkillHashes) {
				return verified, fmt.Errorf("写入后验证武器槽 %d 的替换技能字段不完整", expected.UnitID)
			}
			for index, want := range expected.SkillHashes {
				got, readErr := extra.Uint32At(index)
				if readErr != nil || got != want {
					return verified, fmt.Errorf("写入后验证武器槽 %d 的替换技能槽 %d 失败", expected.UnitID, index+1)
				}
			}
		}
		verified++
	}
	return verified, nil
}

func samePath(a, b string) bool {
	aAbs, _ := filepath.Abs(a)
	bAbs, _ := filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(aAbs), filepath.Clean(bAbs))
}
