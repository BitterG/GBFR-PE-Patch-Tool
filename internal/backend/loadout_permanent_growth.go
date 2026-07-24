package backend

import (
	"math/bits"
	"sort"
)

// LoadoutPermanentGrowth is save-backed, permanent character progression.
// It is part of the panel baseline and must not be presented as a swappable
// equipment/factor/mastery bonus.
type LoadoutPermanentGrowth struct {
	RuntimeObserved       bool                       `json:"runtimeObserved"`
	StableReads           int                        `json:"stableReads"`
	Evidence              string                     `json:"evidence"`
	FateDataAvailable     bool                       `json:"fateDataAvailable"`
	MasterSystemAvailable bool                       `json:"masterSystemAvailable"`
	LegacySystemAvailable bool                       `json:"legacySystemAvailable"`
	FateEpisodeMask       uint32                     `json:"fateEpisodeMask"`
	FateEpisodeCount      int                        `json:"fateEpisodeCount"`
	FateHP                int                        `json:"fateHp"`
	FateATK               int                        `json:"fateAtk"`
	MasterTotalMSP        int                        `json:"masterTotalMsp"`
	MasterProgressIndex   int                        `json:"masterProgressIndex"`
	MasterLevel           int                        `json:"masterLevel"`
	MasterHP              int                        `json:"masterHp"`
	MasterATK             int                        `json:"masterAtk"`
	MasterDamageCap       float64                    `json:"masterDamageCap"`
	MasteryRankCaps       map[string]int             `json:"masteryRankCaps"`
	LegacyProgress        int                        `json:"legacyProgress"`
	LegacyMastery         LoadoutLegacyMasteryGrowth `json:"legacyMastery"`
}

type fateGrowthRow struct {
	requiredEpisodes int
	hp               int
	atk              int
}

type fateGrowth struct {
	EpisodeCount int
	HP           int
	ATK          int
}

// chara_status_fate.tbl 2.0.2. Rows with zero stat changes are omitted; their
// Unk6 episode thresholds are still represented by counting the save mask.
var standardFateGrowthRows = []fateGrowthRow{
	{1, 10, 3}, {2, 15, 5}, {3, 20, 7}, {4, 25, 10},
	{6, 50, 15}, {7, 70, 20}, {8, 100, 25}, {10, 150, 30}, {11, 200, 50},
}

// The raw keys below are the chara_status_fate.Key hashes from the 2.0.2
// table. PL2900 is known but has no HP/ATK Fate rows in that table.
var fateGrowthRowsByCharacter = map[uint32][]fateGrowthRow{
	0x2A26B1B2: standardFateGrowthRows, // PL0000
	0xA4ACBA76: standardFateGrowthRows, // PL0100
	0x18E2F9F9: standardFateGrowthRows, // PL0200
	0x079DF0CC: standardFateGrowthRows, // PL0300
	0x4D0A60C3: standardFateGrowthRows, // PL0400
	0xDD7A151E: standardFateGrowthRows, // PL0500
	0xC8616284: standardFateGrowthRows, // PL0600
	0xC3FFD418: standardFateGrowthRows, // PL0700
	0x22E437E5: standardFateGrowthRows, // PL0800
	0x2EBE91D5: standardFateGrowthRows, // PL0900
	0xBDEF7181: standardFateGrowthRows, // PL1000
	0x627BCB0D: standardFateGrowthRows, // PL1100
	0xFD3BE362: standardFateGrowthRows, // PL1200
	0xFC6CDF7B: standardFateGrowthRows, // PL1300
	0xE7053919: standardFateGrowthRows, // PL1400
	0x978E4B18: standardFateGrowthRows, // PL1500
	0x0D21B430: standardFateGrowthRows, // PL1600
	0xF0EB77EF: standardFateGrowthRows, // PL1700
	0xAA66178A: standardFateGrowthRows, // PL1800
	0xA3A3CB2F: standardFateGrowthRows, // PL1900
	0x718E1A14: standardFateGrowthRows, // PL2100
	0x296471BE: standardFateGrowthRows, // PL2200
	0xBAD16E3B: standardFateGrowthRows, // PL2300
	0x1BB37EF0: standardFateGrowthRows, // PL2400
	0x25D46F4B: standardFateGrowthRows, // PL2500
	0x9A8AF295: standardFateGrowthRows, // PL2600
	0x9B15CFB1: standardFateGrowthRows, // PL2700
	0x646C3168: standardFateGrowthRows, // PL2800
	0x74DD4C79: nil,                    // PL2900: table rows are all zero
}

func deriveFateGrowth(charaHash, episodeMask uint32) (fateGrowth, bool) {
	rows, known := fateGrowthRowsByCharacter[charaHash]
	if !known {
		return fateGrowth{}, false
	}
	completed := bits.OnesCount32(episodeMask & 0x7FF)
	result := fateGrowth{EpisodeCount: completed}
	for _, row := range rows {
		if row.requiredEpisodes > completed {
			break
		}
		result.HP += row.hp
		result.ATK += row.atk
	}
	return result, true
}

type masterGrowth struct {
	ProgressIndex   int
	MasterLevel     int
	HP              int
	ATK             int
	DamageCap       float64
	MasteryRankCaps map[string]int
}

// chara_master_exp.tbl 2.0.2. The two initial zero rows mean TotalMSP=0 is
// progress index/Master Level 1. Indices 51..55 are post-50 progression.
var characterMasterExpThresholds = [...]int{
	0, 0, 3000, 6000, 9000, 12000, 15000, 20000, 25000, 30000,
	37500, 45000, 52500, 60000, 67500, 77500, 87500, 97500, 109500, 121500,
	136500, 151500, 166500, 181500, 198500, 215500, 232500, 249500, 269500, 289500,
	339500, 389500, 439500, 489500, 539500, 589500, 639500, 689500, 739500, 789500,
	864500, 939500, 1014500, 1089500, 1164500, 1239500, 1314500, 1389500, 1464500, 1539500,
	1639499, 1839499, 2089499, 2409499, 2809499, 3309499,
}

type masterStatUnlockRow struct {
	level     int
	hp        int
	atk       int
	damageCap int
}

// Non-zero HealthAdd/AttackAdd/DmgCapAdd rows from skillboard_unlock.tbl
// 2.0.2. Permanent stat unlocks end at MLv50; MLv51..55 remain real
// progression levels and are shown as the five post-50 stars.
var masterStatUnlockRows = [...]masterStatUnlockRow{
	{2, 400, 200, 5}, {5, 400, 200, 5}, {10, 500, 250, 6},
	{15, 500, 250, 6}, {20, 600, 300, 7}, {25, 600, 300, 7},
	{30, 600, 300, 10}, {35, 600, 300, 10}, {40, 600, 300, 12},
	{45, 600, 300, 12}, {50, 600, 300, 20},
}

func deriveMasterGrowth(totalMSP int) masterGrowth {
	thresholdCount := sort.Search(len(characterMasterExpThresholds), func(index int) bool {
		return characterMasterExpThresholds[index] > totalMSP
	})
	progressIndex := thresholdCount - 1
	if progressIndex < 0 {
		progressIndex = 0
	}
	masterLevel := min(progressIndex, 55)
	result := masterGrowth{
		ProgressIndex: progressIndex,
		MasterLevel:   masterLevel,
		MasteryRankCaps: map[string]int{
			"R1": min(masterLevel, 10),
			"R2": min(max(masterLevel-10, 0), 10),
			"R3": min(max(masterLevel-20, 0), 10),
			"EX": min(max(masterLevel-30, 0), 20),
		},
	}
	for _, row := range masterStatUnlockRows {
		if row.level > masterLevel {
			break
		}
		result.HP += row.hp
		result.ATK += row.atk
		result.DamageCap += float64(row.damageCap)
	}
	return result
}

func readLoadoutPermanentGrowth(data *SaveDataBinary, charaHash, charaUnitID uint32, warnings *[]string) (LoadoutPermanentGrowth, error) {
	fateUnit, fateOK := uintUnitExact(data, 1318, charaUnitID)
	fateAvailable := fateOK && len(fateUnit.ValueData) == 1
	episodeMask := uint32(0)
	if fateAvailable {
		episodeMask = fateUnit.ValueData[0]
	} else {
		appendWarning(warnings, "角色 %d 尚未建立 Fate 篇章字段；按未开启/零成长处理", charaUnitID)
	}
	masterUnit, masterOK := intUnitExact(data, 1323, charaUnitID)
	masterAvailable := masterOK && len(masterUnit.ValueData) == 1
	masterTotalMSP := 0
	if masterAvailable {
		masterTotalMSP = int(masterUnit.ValueData[0])
	} else {
		appendWarning(warnings, "角色 %d 尚未建立 Master/MSP 字段；专精按未开启处理", charaUnitID)
	}
	legacyUnit, legacyOK := uintUnitExact(data, 1321, charaUnitID)
	legacyAvailable := legacyOK && len(legacyUnit.ValueData) == 1
	legacyProgress := 0
	if legacyAvailable {
		legacyProgress = int(legacyUnit.ValueData[0])
	} else {
		appendWarning(warnings, "角色 %d 尚未建立角色强化进度字段；离线时不假算固定强化", charaUnitID)
	}

	if episodeMask&^uint32(0x7FF) != 0 {
		appendWarning(warnings, "角色 %08X 的 1318 含 11 个 Fate 篇章以外的位 0x%X，固定基准只读取低 11 位", charaHash, episodeMask&^uint32(0x7FF))
	}
	fate, known := deriveFateGrowth(charaHash, episodeMask)
	if !known {
		appendWarning(warnings, "角色 %08X 未收录于 2.0.2 chara_status_fate 固定成长表", charaHash)
	}
	master := deriveMasterGrowth(masterTotalMSP)
	return LoadoutPermanentGrowth{
		FateDataAvailable: fateAvailable, MasterSystemAvailable: masterAvailable, LegacySystemAvailable: legacyAvailable,
		FateEpisodeMask: episodeMask, FateEpisodeCount: fate.EpisodeCount,
		FateHP: fate.HP, FateATK: fate.ATK,
		MasterTotalMSP: masterTotalMSP, MasterProgressIndex: master.ProgressIndex,
		MasterLevel: master.MasterLevel, MasterHP: master.HP, MasterATK: master.ATK,
		MasterDamageCap: master.DamageCap, MasteryRankCaps: master.MasteryRankCaps,
		LegacyProgress: legacyProgress,
	}, nil
}
