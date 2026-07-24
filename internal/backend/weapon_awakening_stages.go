package backend

// Generated from DLC 2.0.2 system/table/weapon.tbl. Each entry records the
// inventory hash that the game uses after an awakening milestone.
type weaponAwakeningStage struct {
	Level int
	Hash  uint32
}

var awakeningWeaponStages = map[uint32][]weaponAwakeningStage{
	0xD7CEE3B8: {{Level: 1, Hash: 0x08AC9299}, {Level: 3, Hash: 0x7F31E0D6}, {Level: 6, Hash: 0x2E9C27AC}, {Level: 10, Hash: 0x77AB0809}},
	0xE6518D5A: {{Level: 1, Hash: 0x2AF2A118}, {Level: 3, Hash: 0x9B9F83A2}, {Level: 6, Hash: 0x633DD711}, {Level: 10, Hash: 0x0D7B7A95}},
	0x0E0287DC: {{Level: 1, Hash: 0x775E242A}, {Level: 3, Hash: 0x24C815AB}, {Level: 6, Hash: 0x34AD70E6}, {Level: 10, Hash: 0x1E90ADB4}},
	0xC9FCCCC6: {{Level: 1, Hash: 0xFED83C75}, {Level: 3, Hash: 0x027915BC}, {Level: 6, Hash: 0xE5D2832B}, {Level: 10, Hash: 0xCE12662A}},
	0x8B8FCB4E: {{Level: 3, Hash: 0x2E393B84}, {Level: 6, Hash: 0xA9E77ED8}, {Level: 10, Hash: 0x288D5590}},
	0xE04031CF: {{Level: 3, Hash: 0x606C0C73}, {Level: 6, Hash: 0xD66D351C}, {Level: 10, Hash: 0xA44B08BF}},
	0x4E1AB7BB: {{Level: 3, Hash: 0xD6070A15}, {Level: 6, Hash: 0x1006ED87}, {Level: 10, Hash: 0xD5E0BF7F}},
	0xF1845E70: {{Level: 3, Hash: 0xBC72036A}, {Level: 6, Hash: 0x43A93192}, {Level: 10, Hash: 0x7B5D7822}},
	0x22E79816: {{Level: 3, Hash: 0x26AD10BC}, {Level: 6, Hash: 0xFB5818E6}, {Level: 10, Hash: 0x56676778}},
	0xFEBAC81A: {{Level: 3, Hash: 0xBC5A4248}, {Level: 6, Hash: 0x628521DE}, {Level: 10, Hash: 0x1779CD60}},
	0x9249B9CA: {{Level: 3, Hash: 0xC1F3AD7B}, {Level: 6, Hash: 0xBE726E20}, {Level: 10, Hash: 0xB5341662}},
	0x3F39B5EB: {{Level: 3, Hash: 0x5A744CE3}, {Level: 6, Hash: 0x5F751A07}, {Level: 10, Hash: 0xB3003C9B}},
	0xE92002CA: {{Level: 3, Hash: 0xAED9EBF8}, {Level: 6, Hash: 0x301DBAB7}, {Level: 10, Hash: 0x93244909}},
	0x07BB324C: {{Level: 3, Hash: 0xEBBE11A9}, {Level: 6, Hash: 0x25FF2DB3}, {Level: 10, Hash: 0xBCD65F7D}},
	0x2A924378: {{Level: 3, Hash: 0x1AB040DD}, {Level: 6, Hash: 0x28A1C76A}, {Level: 10, Hash: 0x45ADDF75}},
	0x0FAE326F: {{Level: 3, Hash: 0xDDDC7C2E}, {Level: 6, Hash: 0xD47F8689}, {Level: 10, Hash: 0x33D018E0}},
	0x72AAE787: {{Level: 3, Hash: 0x241B6D32}, {Level: 6, Hash: 0xDC75CE8A}, {Level: 10, Hash: 0x4CA01DB9}},
	0x7027741C: {{Level: 3, Hash: 0x3D86CCE3}, {Level: 6, Hash: 0x40F726F9}, {Level: 10, Hash: 0x9FFA80BB}},
	0x776CA5A8: {{Level: 3, Hash: 0x9FFC73A8}, {Level: 6, Hash: 0x51E080FA}, {Level: 10, Hash: 0xF7C7B5DC}},
	0xC84002E0: {{Level: 3, Hash: 0xD7280FE5}, {Level: 6, Hash: 0x0B1C14C1}, {Level: 10, Hash: 0x227B690E}},
	0x98F8CA3E: {{Level: 3, Hash: 0x79ADECD8}, {Level: 6, Hash: 0x1EA049BC}, {Level: 10, Hash: 0xB3DDAAE1}},
	0x84BF5DE8: {{Level: 3, Hash: 0xDC959F74}, {Level: 6, Hash: 0xF5CF6185}, {Level: 10, Hash: 0x9F671440}},
	0x4C3A7F95: {{Level: 3, Hash: 0x05DEFB89}, {Level: 6, Hash: 0xB81CF304}, {Level: 10, Hash: 0x361E2D95}},
	0xE82DF9EC: {{Level: 3, Hash: 0xF053C91E}, {Level: 6, Hash: 0xE4C1B247}, {Level: 10, Hash: 0x2F639454}},
	0x761A3597: {{Level: 3, Hash: 0x1DEC52B3}, {Level: 6, Hash: 0x90A18C66}, {Level: 10, Hash: 0x1D0E9A84}},
	0x687ECE62: {{Level: 3, Hash: 0xFBCCF69B}, {Level: 6, Hash: 0xCBA1F4E1}, {Level: 10, Hash: 0x847BD571}},
	0xB5C31AB2: {{Level: 3, Hash: 0xDC9D7C26}, {Level: 6, Hash: 0xBC7F985E}, {Level: 10, Hash: 0x51836359}},
	0xA1F477D6: {{Level: 3, Hash: 0xA9E805AE}, {Level: 6, Hash: 0x2A95501E}, {Level: 10, Hash: 0xF6FF4CB2}},
	0x099C8066: {{Level: 3, Hash: 0xF246096B}, {Level: 6, Hash: 0x64DD708D}, {Level: 10, Hash: 0xAFBD8B45}},
	0xFFF2F00F: {{Level: 3, Hash: 0x19A77E54}, {Level: 6, Hash: 0x2CBCACCA}, {Level: 10, Hash: 0xA44EB1B4}},
	0xF4C1DE98: {{Level: 3, Hash: 0x02A9B90B}, {Level: 6, Hash: 0x22E9C126}, {Level: 10, Hash: 0x48110BA3}},
	0xAB6678D2: {{Level: 3, Hash: 0xDC144611}, {Level: 6, Hash: 0xA8D420F2}, {Level: 10, Hash: 0xCD8CA605}},
	0x02352554: {{Level: 3, Hash: 0x5403D78E}, {Level: 6, Hash: 0xFD7BF3D6}, {Level: 10, Hash: 0x292C88E4}},
	0x304E4448: {{Level: 3, Hash: 0xD8B6E17C}, {Level: 6, Hash: 0x348F349C}, {Level: 10, Hash: 0x80AD3AEB}},
	0xBDDCD5E3: {{Level: 3, Hash: 0x607BFD73}, {Level: 6, Hash: 0x4868F8B4}, {Level: 10, Hash: 0xDD177582}},
	0x411C89ED: {{Level: 3, Hash: 0xBE4D1A95}, {Level: 6, Hash: 0x67A31199}, {Level: 10, Hash: 0x419B3CC0}},
	0x06F51A26: {{Level: 3, Hash: 0x2BE820F1}, {Level: 6, Hash: 0x05B4630A}, {Level: 10, Hash: 0x6C6D9083}},
	0x7BFF8253: {{Level: 3, Hash: 0x2E3C9E02}, {Level: 6, Hash: 0x7C88222E}, {Level: 10, Hash: 0x6A9D3DD0}},
	0x95ACAD77: {{Level: 3, Hash: 0xB586AE5B}, {Level: 6, Hash: 0xC05FF343}, {Level: 10, Hash: 0x7A0A0E34}},
	0x1DE38944: {{Level: 3, Hash: 0xC38561AB}, {Level: 6, Hash: 0x3EF31D1C}, {Level: 10, Hash: 0x4D9B69A5}},
	0xDFBB5727: {{Level: 3, Hash: 0x74804589}, {Level: 6, Hash: 0xABDF528D}, {Level: 10, Hash: 0xD016FEB3}},
	0xAD915067: {{Level: 3, Hash: 0x8E2E15EB}, {Level: 6, Hash: 0xD21F4078}, {Level: 10, Hash: 0x1E8011EB}},
	0x74D764B7: {{Level: 3, Hash: 0xAEF15742}, {Level: 6, Hash: 0xD5FE66A2}, {Level: 10, Hash: 0x1EA819A4}},
	0xFA5F32D5: {{Level: 3, Hash: 0x1C963180}, {Level: 6, Hash: 0x5800CFD6}, {Level: 10, Hash: 0x02E70DE2}},
	0x1064441E: {{Level: 3, Hash: 0x82AC447F}, {Level: 6, Hash: 0xDE958CDB}, {Level: 10, Hash: 0x40204755}},
	0x4CBA06D8: {{Level: 3, Hash: 0x60372BEF}, {Level: 6, Hash: 0xB25574F8}, {Level: 10, Hash: 0x7DD506A3}},
	0x3C667A36: {{Level: 3, Hash: 0xFD6B963E}, {Level: 6, Hash: 0xDE80BC37}, {Level: 10, Hash: 0x7F10010D}},
	0x10180036: {{Level: 3, Hash: 0x974DBC97}, {Level: 6, Hash: 0x6A018933}, {Level: 10, Hash: 0x5DE1B311}},
	0x1CC90CAE: {{Level: 3, Hash: 0x1A961AC5}, {Level: 6, Hash: 0x4102B6AB}, {Level: 10, Hash: 0x802B09DA}},
	0x90F5B18F: {{Level: 3, Hash: 0x72AB825D}, {Level: 6, Hash: 0x87E6E352}, {Level: 10, Hash: 0x860AC3BF}},
	0x969CF8C7: {{Level: 3, Hash: 0x1154A6C6}, {Level: 6, Hash: 0x84F432D7}, {Level: 10, Hash: 0x78544A75}},
	0x6CFF175C: {{Level: 3, Hash: 0x51F70303}, {Level: 6, Hash: 0xB2931FAA}, {Level: 10, Hash: 0xA716D1F2}},
	0x14B3AE92: {{Level: 3, Hash: 0x6D381177}, {Level: 6, Hash: 0xE15BA40E}, {Level: 10, Hash: 0x4FEE9341}},
	0x6EEA0D21: {{Level: 3, Hash: 0x7E092CCD}, {Level: 6, Hash: 0xE3B35C0D}, {Level: 10, Hash: 0xDED16FCF}},
	0x6EC326D3: {{Level: 3, Hash: 0x1826996A}, {Level: 6, Hash: 0xA14B98D8}, {Level: 10, Hash: 0x64705E7B}},
	0xD5EB1DEE: {{Level: 3, Hash: 0x82E85115}, {Level: 6, Hash: 0x9AE66FBD}, {Level: 10, Hash: 0xAD99E05E}},
	0x1EB2B398: {{Level: 3, Hash: 0x165F82F5}, {Level: 6, Hash: 0x280EA816}, {Level: 10, Hash: 0x219EE448}},
	0xCDB13688: {{Level: 3, Hash: 0x18B8476C}, {Level: 6, Hash: 0x32B5DC17}, {Level: 10, Hash: 0xF0B8CF77}},
}

func weaponBaseHash(hash uint32) uint32 {
	if base, ok := awakeningWeaponAliases[hash]; ok {
		return base
	}
	return hash
}

func weaponHashForAwakening(hash uint32, level int) uint32 {
	base := weaponBaseHash(hash)
	result := base
	for _, stage := range awakeningWeaponStages[base] {
		if level < stage.Level {
			break
		}
		result = stage.Hash
	}
	return result
}
