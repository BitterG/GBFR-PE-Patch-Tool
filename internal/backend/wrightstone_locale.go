package backend

var wrightstoneCN = map[string]string{
	"Dread Wrightstone":         "畏惧之祝福",
	"Vitality Wrightstone":      "生机之祝福",
	"Fortification Wrightstone": "镇守之祝福",
	"Sequestration Wrightstone": "隔绝之祝福",
}

var wrightstoneTraitCN = map[string]string{
	"Provoke":                 "挑衅",
	"Blight Resistance":       "灾祸抗性",
	"Fast Learner":            "获得经验值",
	"Rupie Tycoon":            "获得金币",
	"Path to Mastery":         "获得MSP",
	"Paralysis Resistance":    "麻痹抗性",
	"SBA Sealed Resistance":   "奥义封印抗性",
	"Skill Sealed Resistance": "能力封印抗性",
	"Glaciate Resistance":     "冰冻抗性",
	"Sandtomb Resistance":     "泥沙抗性",
	"Improved Healing":        "回复性能",
	"Defense Down Resistance": "防御DOWN抗性",
	"Dizzy Resistance":        "昏迷抗性",
	"Poison Resistance":       "中毒抗性",
	"Darkflame Resistance":    "异能耐受",
	"Held Under Resistance":   "水牢抗性",
	"Burn Resistance":         "灼热抗性",
	"Slow Resistance":         "缓速抗性",
}

func cnWrightstone(en string) string {
	if !useChinese() {
		return en
	}
	if v, ok := wrightstoneCN[en]; ok {
		return v
	}
	return en
}

// Blessing legality comes from data/wrightstone_traits.json; display names use
// the same authoritative bilingual trait catalog as sigils and weapon skills.
func cnWrightstoneTrait(en string) string {
	if !useChinese() {
		return en
	}
	if v, ok := wrightstoneTraitCN[en]; ok {
		return v
	}
	return cnTrait(en)
}
