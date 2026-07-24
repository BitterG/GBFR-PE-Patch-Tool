package backend

// sigilMemoryName supplements catalog entries absent from data JSON.
type sigilMemoryName struct {
	Hash uint32
	Name string
}

// First column supplied from game memory: trait hashes.
var sigilMemoryTraits = []sigilMemoryName{
	{0x73220725, "天星之止息"},
	{0x9232DC17, "天星之界"},
	{0x3EB345D7, "雷狼的战气"},
	{0x0DE887A0, "天星之炼"},
	{0xA898E283, "天星之雪"},
	{0xF26BAEA5, "分歧"},
	{0x36E3848D, "天星之焰"},
	{0xD029FE08, "浪迹天涯"},
	{0xA7726190, "天星之煌"},
	{0x06719232, "黑龙的咒印"},
	{0x461A8E07, "群青的逆境"},
	{0x9ACE140B, "刃姬的轮舞曲"},
	{0x7D75D904, "雷狼的弹匣"},
	{0x5559232F, "黑龙的战气"},
	{0xED8D8AD8, "黑龙的折跃"},
	{0xB953CC1E, "群青的战气"},
	{0x7B5B081D, "刃姬的小夜曲"},
	{0xBE3404B9, "雷狼的慧眼"},
	{0x807B6684, "转世的战气"},
	{0xDBA19768, "狼王的战气"},
	{0x79266456, "刃姬的战气"},
	{0x1DE14C65, "狼王的激昂"},
	{0x26956F25, "狼王的大转轮"},
	{0xD176D262, "群青的剑光"},
	{0x30773197, "转世的跃动"},
	{0x47384248, "转世的恩宠"},
	{0xBF78FBFC, "可怕的漆黑钳蟹因子"},
	{0x46EE3116, "漆黑之谊"},
	{0x89C66ACB, "相扑斗力"},
}

// Third column supplied from game memory: sigil hashes.
var sigilMemorySigils = []sigilMemoryName{
	{0x9300FADB, "天星之止息"},
	{0xD29CD8E0, "天星之界"},
	{0xD8C61507, "雷狼的战气"},
	{0x3BA37635, "HP吸收"},
	{0x2679A4F0, "明镜止水"},
	{0x08DA7279, "攻击力"},
	{0x837B3D64, "自愈"},
	{0x04AC2281, "激昂"},
	{0xB5B23F02, "体力"},
	{0x3ED16FB2, "药水携带数"},
	{0xD340651C, "自动复活"},
	{0x8B8085C0, "天星之炼"},
	{0xE14E1598, "天星之雪"},
	{0x7B4AAB30, "分歧"},
	{0x74061B0C, "天星之焰"},
	{0x5BF84FD1, "浪迹天涯"},
	{0x20492635, "天星之煌"},
	{0x0523A202, "黑龙的咒印"},
	{0xD4117FF3, "群青的逆境"},
	{0x96D6FE5E, "刃姬的轮舞曲"},
	{0xF964A4CA, "雷狼的弹匣"},
	{0x9ABD2DA5, "黑龙的战气"},
	{0x0723F7EC, "黑龙的折跃"},
	{0x51E98A7C, "群青的战气"},
	{0xEC9FFE77, "刃姬的小夜曲"},
	{0x1A359B67, "雷狼的慧眼"},
	{0xA8A0CBFF, "黑龙之觉醒"},
	{0x2D70C37D, "转世的战气"},
	{0x5A360EA8, "转世之觉醒"},
	{0x41AC1082, "狼王的战气"},
	{0xEB766D87, "刃姬的战气"},
	{0x282DBFF0, "狼王的激昂"},
	{0x895ABBF6, "狼王之觉醒"},
	{0xF21404B1, "狼王的大转轮"},
	{0x9EC6C56D, "群青的剑光"},
	{0x23953FD4, "雷狼之觉醒"},
	{0x95CC3CB8, "群青之觉醒"},
	{0xD8A464F1, "刃姬之觉醒"},
	{0xBA28C81C, "转世的跃动"},
	{0x64301E91, "转世的恩宠"},
	{0x49434696, "可怕的漆黑钳蟹因子"},
	{0x65F0420A, "漆黑之谊"},
	{0xB289A9AD, "相扑斗力"},
}

var sigilMemoryEnglishNames = map[string]string{
	"HP吸收":      "Drain",
	"体力":        "Health",
	"刃姬之觉醒":     "Bladequeen's Awakening",
	"刃姬的轮舞曲":    "Bladequeen's Circuit",
	"刃姬的小夜曲":    "Bladequeen's Serenade",
	"刃姬的战气":     "Bladequeen's Warpath",
	"分歧":        "Divergence",
	"可怕的漆黑钳蟹因子": "Immortal Shell",
	"天星之止息":     "Celestial Ventus",
	"天星之焰":      "Celestial Incendo",
	"天星之煌":      "Celestial Lumen",
	"天星之界":      "Celestial Terra",
	"天星之炼":      "Celestial Nyx",
	"天星之雪":      "Celestial Aqua",
	"攻击力":       "Attack Power",
	"明镜止水":      "Nimble Onslaught",
	"浪迹天涯":      "Fatebreaker",
	"漆黑之谊":      "In a Pinch",
	"激昂":        "Uplift",
	"狼王之觉醒":     "Gladiator's Awakening",
	"狼王的大转轮":    "Gladiator's Top",
	"狼王的大轮转":    "Gladiator's Top",
	"狼王的战气":     "Gladiator's Warpath",
	"狼王的激昂":     "Gladiator's Frenzy",
	"相扑斗力":      "Sumo Force",
	"群青之觉醒":     "Ultramarine's Awakening",
	"群青的剑光":     "Ultramarine's Flash",
	"群青的战气":     "Ultramarine's Warpath",
	"群青的逆境":     "Ultramarine's Adversity",
	"自动复活":      "Autorevive",
	"自愈":        "Regen",
	"药水携带数":     "Potion Hoarder",
	"转世之觉醒":     "Enchantress's Awakening",
	"转世的恩宠":     "Enchantress's Blessing",
	"转世的战气":     "Enchantress's Warpath",
	"转世的跃动":     "Enchantress's Rhythm",
	"闪避性能":      "Improved Dodge",
	"雷狼之觉醒":     "Thunderwolf's Awakening",
	"雷狼的弹匣":     "Thunderwolf's Recharge",
	"雷狼的慧眼":     "Thunderwolf's Acuity",
	"雷狼的战气":     "Thunderwolf's Warpath",
	"黑龙之觉醒":     "The Black's Awakening",
	"黑龙的咒印":     "The Black's Mark",
	"黑龙的战气":     "The Black's Warpath",
	"黑龙的折跃":     "The Black's Impulse",
}

func localizedSigilMemoryName(name string) string {
	if useChinese() {
		return name
	}
	if translated, ok := sigilMemoryEnglishNames[name]; ok {
		return translated
	}
	return name
}

func sigilMemoryNameByHash(entries []sigilMemoryName, hash uint32) string {
	for _, entry := range entries {
		if entry.Hash == hash {
			return localizedSigilMemoryName(entry.Name)
		}
	}
	return ""
}
