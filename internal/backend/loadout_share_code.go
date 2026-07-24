package backend

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"unicode"

	"github.com/andybalholm/brotli"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	loadoutShareCodePrefix             = "GBFRC1A."
	loadoutShareCodeFrameMagic         = "GBLC"
	loadoutShareCodeFrameVersion       = 2
	loadoutShareCodeLegacyFrameVersion = 1
	loadoutShareCodeCodec              = 1 // MessagePack array schema + Brotli
	loadoutShareCodeHeaderSize         = 18
	loadoutShareCodeMaxFrameSize       = 256 * 1024
)

type LoadoutShareCodeResult struct {
	CompatibilityCode string `json:"compatibilityCode"`
	CharacterName     string `json:"characterName"`
	LoadoutName       string `json:"loadoutName"`
	JSONBytes         int    `json:"jsonBytes"`
	PackedBytes       int    `json:"packedBytes"`
	CompressedBytes   int    `json:"compressedBytes"`
	FrameBytes        int    `json:"frameBytes"`
}

type loadoutShareCodePayload struct {
	_msgpack          struct{} `msgpack:",as_array"`
	ShareVersion      int
	CharaHash         uint32
	CharaName         string
	OwnerCode         string
	Name              string
	WeaponHash        uint32
	WeaponName        string
	Sigils            []loadoutShareCodeSigil
	Summons           []loadoutShareCodeSummon
	Skills            []loadoutShareCodeSkill
	WeaponSkillHashes []uint32
	MasteryHashes     []uint32
	Character         *loadoutShareCodeCharacter
	Weapon            *loadoutShareCodeWeapon
	OverLimit         []loadoutShareCodeOverLimit
}

type loadoutShareCodePayloadV1 struct {
	_msgpack          struct{} `msgpack:",as_array"`
	ShareVersion      int
	CharaHash         uint32
	CharaName         string
	OwnerCode         string
	Name              string
	WeaponHash        uint32
	WeaponName        string
	Sigils            []loadoutShareCodeSigil
	Summons           []loadoutShareCodeSummon
	Skills            []loadoutShareCodeSkill
	WeaponSkillHashes []uint32
	MasteryHashes     []uint32
	Character         *loadoutShareCodeCharacterV1
	Weapon            *loadoutShareCodeWeapon
	OverLimit         []loadoutShareCodeOverLimit
}

type loadoutShareCodeSigil struct {
	_msgpack       struct{} `msgpack:",as_array"`
	Index          int
	Hash           uint32
	Name           string
	Level          int
	PrimaryHash    uint32
	PrimaryLevel   int
	SecondaryHash  uint32
	SecondaryLevel int
}

type loadoutShareCodeSummon struct {
	_msgpack       struct{} `msgpack:",as_array"`
	TypeHash       uint32
	Name           string
	MainTraitHash  uint32
	MainTraitLevel uint32
	SubParamHash   uint32
	SubParamLevel  uint32
	Rank           uint32
}

type loadoutShareCodeSkill struct {
	_msgpack struct{} `msgpack:",as_array"`
	Hash     uint32
	Name     string
	Key      string
}

type loadoutShareCodeCharacter struct {
	_msgpack                   struct{} `msgpack:",as_array"`
	CharacterLevel             int
	BaseHP                     int
	BaseATK                    int
	BaseStunBits               uint32
	BaseCritRate               int
	CharacterBaseCaptured      bool
	MasterTotalMSP             int
	LegacyProgress             int
	EnhancementPanel           []int
	EnhancementNodes           []loadoutShareCodeEnhancementNode
	Weapons                    []loadoutShareCodeProgressionWeapon
	WeaponWrightstonesCaptured bool
}

type loadoutShareCodeCharacterV1 struct {
	_msgpack                   struct{} `msgpack:",as_array"`
	CharacterLevel             int
	MasterTotalMSP             int
	LegacyProgress             int
	EnhancementPanel           []int
	EnhancementNodes           []loadoutShareCodeEnhancementNode
	Weapons                    []loadoutShareCodeProgressionWeapon
	WeaponWrightstonesCaptured bool
}

type loadoutShareCodeEnhancementNode struct {
	_msgpack struct{} `msgpack:",as_array"`
	Index    int
	Value    int
}

type loadoutShareCodeProgressionWeapon struct {
	_msgpack           struct{} `msgpack:",as_array"`
	Hash               uint32
	BaseHash           uint32
	InternalID         string
	Level              int
	Uncap              int
	Mirage             int
	Awakening          int
	Transcendence      int
	TranscendenceSkill string
	Wrightstone        *loadoutShareCodeWrightstone
}

type loadoutShareCodeWrightstone struct {
	_msgpack struct{} `msgpack:",as_array"`
	Hash     uint32
	Traits   []loadoutShareCodeWrightstoneTrait
}

type loadoutShareCodeWrightstoneTrait struct {
	_msgpack struct{} `msgpack:",as_array"`
	Index    int
	Hash     uint32
	Level    int
}

type loadoutShareCodeWeapon struct {
	_msgpack             struct{} `msgpack:",as_array"`
	StoredHash           uint32
	XP                   uint32
	Uncap                int
	Mirage               int
	Awakening            int
	Transcendence        int
	ExactState           bool
	Flags                uint32
	WrightstoneReference uint32
	State                int
	SkillHashes          []uint32
	Wrightstone          *loadoutShareCodeWrightstone
}

type loadoutShareCodeOverLimit struct {
	_msgpack      struct{} `msgpack:",as_array"`
	Index         int
	AttributeHash uint32
	Level         int
}

func loadoutShareCodeHash(text, field string, optional bool) (uint32, error) {
	text = strings.TrimSpace(text)
	if text == "" && optional {
		return 0, nil
	}
	value, err := ParseHashHex(text)
	if err != nil {
		return 0, fmt.Errorf("%s无效: %w", field, err)
	}
	return value, nil
}

func loadoutShareCodeOptionalHash(value uint32) string {
	if value == 0 {
		return ""
	}
	return hashText(value)
}

func compactLoadoutShareWrightstone(source *LoadoutWeaponWrightstone) (*loadoutShareCodeWrightstone, error) {
	if source == nil {
		return nil, nil
	}
	hash, err := loadoutShareCodeHash(source.Hash, "武器祝福哈希", false)
	if err != nil {
		return nil, err
	}
	result := &loadoutShareCodeWrightstone{Hash: hash}
	for _, trait := range source.Traits {
		traitHash, parseErr := loadoutShareCodeHash(trait.Hash, "武器祝福词条哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		result.Traits = append(result.Traits, loadoutShareCodeWrightstoneTrait{
			Index: trait.Index, Hash: traitHash, Level: trait.Level,
		})
	}
	return result, nil
}

func expandLoadoutShareWrightstone(source *loadoutShareCodeWrightstone) *LoadoutWeaponWrightstone {
	if source == nil {
		return nil
	}
	result := &LoadoutWeaponWrightstone{Hash: hashText(source.Hash)}
	for _, trait := range source.Traits {
		result.Traits = append(result.Traits, LoadoutWeaponWrightstoneTrait{
			Index: int(trait.Index), Hash: hashText(trait.Hash), Level: int(trait.Level),
		})
	}
	return result
}

func compactLoadoutShare(source *LoadoutShare) (*loadoutShareCodePayload, error) {
	if source == nil || source.Format != loadoutShareFormat || source.Version != loadoutShareVersion {
		return nil, fmt.Errorf("只能为当前 v%d 单套配装生成分享码", loadoutShareVersion)
	}
	charaHash, err := loadoutShareCodeHash(source.CharaHash, "角色哈希", false)
	if err != nil {
		return nil, err
	}
	weaponHash, err := loadoutShareCodeHash(source.WeaponHash, "武器哈希", true)
	if err != nil {
		return nil, err
	}
	result := &loadoutShareCodePayload{
		ShareVersion: source.Version, CharaHash: charaHash, CharaName: source.CharaName,
		OwnerCode: source.OwnerCode, Name: source.Name, WeaponHash: weaponHash, WeaponName: source.WeaponName,
	}
	for _, sigil := range source.Sigils {
		if sigil.Index == nil {
			return nil, fmt.Errorf("因子 %s 缺少槽位索引", sigil.Name)
		}
		hash, parseErr := loadoutShareCodeHash(sigil.Hash, "因子哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		primary, parseErr := loadoutShareCodeHash(sigil.PrimaryTraitHash, "因子主词条哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		secondary, parseErr := loadoutShareCodeHash(sigil.SecondaryTraitHash, "因子副词条哈希", true)
		if parseErr != nil {
			return nil, parseErr
		}
		result.Sigils = append(result.Sigils, loadoutShareCodeSigil{
			Index: *sigil.Index, Hash: hash, Name: sigil.Name, Level: sigil.Level,
			PrimaryHash: primary, PrimaryLevel: sigil.PrimaryTraitLevel,
			SecondaryHash: secondary, SecondaryLevel: sigil.SecondaryTraitLevel,
		})
	}
	for _, summon := range source.Summons {
		typeHash, parseErr := loadoutShareCodeHash(summon.TypeHash, "召唤石类型哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		mainHash, parseErr := loadoutShareCodeHash(summon.MainTraitHash, "召唤石主加护哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		subHash, parseErr := loadoutShareCodeHash(summon.SubParamHash, "召唤石副参数哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		result.Summons = append(result.Summons, loadoutShareCodeSummon{
			TypeHash: typeHash, Name: summon.Name, MainTraitHash: mainHash,
			MainTraitLevel: uint32(summon.MainTraitLevel), SubParamHash: subHash,
			SubParamLevel: uint32(summon.SubParamLevel), Rank: uint32(summon.Rank),
		})
	}
	for _, skill := range source.Skills {
		hash, parseErr := loadoutShareCodeHash(skill.Hash, "角色技能哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		result.Skills = append(result.Skills, loadoutShareCodeSkill{Hash: hash, Name: skill.Name, Key: skill.Key})
	}
	for _, value := range source.WeaponSkillHashes {
		hash, parseErr := loadoutShareCodeHash(value, "武器技能哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		result.WeaponSkillHashes = append(result.WeaponSkillHashes, hash)
	}
	for _, value := range source.MasteryHashes {
		hash, parseErr := loadoutShareCodeHash(value, "专精节点哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		result.MasteryHashes = append(result.MasteryHashes, hash)
	}
	if source.Character != nil {
		character := &loadoutShareCodeCharacter{
			CharacterLevel: source.Character.CharacterLevel,
			BaseHP:         source.Character.BaseHP, BaseATK: source.Character.BaseATK,
			BaseStunBits: source.Character.BaseStunBits, BaseCritRate: source.Character.BaseCritRate,
			CharacterBaseCaptured:      source.Character.CharacterBaseCaptured,
			MasterTotalMSP:             source.Character.MasterTotalMSP,
			LegacyProgress:             source.Character.LegacyProgress,
			WeaponWrightstonesCaptured: source.Character.WeaponWrightstonesCaptured,
		}
		for _, value := range source.Character.EnhancementPanel {
			character.EnhancementPanel = append(character.EnhancementPanel, value)
		}
		for _, node := range source.Character.EnhancementNodes {
			character.EnhancementNodes = append(character.EnhancementNodes, loadoutShareCodeEnhancementNode{
				Index: node.Index, Value: node.Value,
			})
		}
		for _, weapon := range source.Character.Weapons {
			hash, parseErr := loadoutShareCodeHash(weapon.Hash, "角色武器哈希", false)
			if parseErr != nil {
				return nil, parseErr
			}
			baseHash, parseErr := loadoutShareCodeHash(weapon.BaseHash, "角色武器基础哈希", true)
			if parseErr != nil {
				return nil, parseErr
			}
			wrightstone, compactErr := compactLoadoutShareWrightstone(weapon.Wrightstone)
			if compactErr != nil {
				return nil, compactErr
			}
			character.Weapons = append(character.Weapons, loadoutShareCodeProgressionWeapon{
				Hash: hash, BaseHash: baseHash, InternalID: weapon.InternalID, Level: weapon.Level,
				Uncap: weapon.Uncap, Mirage: weapon.Mirage, Awakening: weapon.Awakening,
				Transcendence: weapon.Transcendence, TranscendenceSkill: weapon.TranscendenceSkill,
				Wrightstone: wrightstone,
			})
		}
		result.Character = character
	}
	if source.Weapon != nil {
		storedHash, parseErr := loadoutShareCodeHash(source.Weapon.StoredHash, "当前武器存储哈希", false)
		if parseErr != nil {
			return nil, parseErr
		}
		reference, parseErr := loadoutShareCodeHash(source.Weapon.WrightstoneReference, "武器祝福兼容引用", true)
		if parseErr != nil {
			return nil, parseErr
		}
		wrightstone, compactErr := compactLoadoutShareWrightstone(source.Weapon.Wrightstone)
		if compactErr != nil {
			return nil, compactErr
		}
		weapon := &loadoutShareCodeWeapon{
			StoredHash: storedHash, XP: source.Weapon.XP, Uncap: source.Weapon.Uncap,
			Mirage: source.Weapon.Mirage, Awakening: source.Weapon.Awakening,
			Transcendence: source.Weapon.Transcendence, ExactState: source.Weapon.ExactState,
			Flags: source.Weapon.Flags, WrightstoneReference: reference, State: source.Weapon.State,
			Wrightstone: wrightstone,
		}
		for _, value := range source.Weapon.SkillHashes {
			hash, parseErr := loadoutShareCodeHash(value, "当前武器技能哈希", false)
			if parseErr != nil {
				return nil, parseErr
			}
			weapon.SkillHashes = append(weapon.SkillHashes, hash)
		}
		result.Weapon = weapon
	}
	for _, slot := range source.OverLimit {
		hash, parseErr := loadoutShareCodeHash(slot.AttributeHash, "上限突破属性哈希", true)
		if parseErr != nil {
			return nil, parseErr
		}
		result.OverLimit = append(result.OverLimit, loadoutShareCodeOverLimit{
			Index: slot.Index, AttributeHash: hash, Level: slot.Level,
		})
	}
	return result, nil
}

func expandLoadoutShare(source *loadoutShareCodePayload) *LoadoutShare {
	result := &LoadoutShare{
		Format: loadoutShareFormat, Version: int(source.ShareVersion), CharaHash: hashText(source.CharaHash),
		CharaName: source.CharaName, OwnerCode: source.OwnerCode, Name: source.Name,
		WeaponHash: loadoutShareCodeOptionalHash(source.WeaponHash), WeaponName: source.WeaponName,
	}
	for _, sigil := range source.Sigils {
		index := int(sigil.Index)
		result.Sigils = append(result.Sigils, LoadoutShareSigil{
			Index: &index, Hash: hashText(sigil.Hash), Name: sigil.Name, Level: int(sigil.Level),
			PrimaryTraitHash: hashText(sigil.PrimaryHash), PrimaryTraitLevel: int(sigil.PrimaryLevel),
			SecondaryTraitHash:  loadoutShareCodeOptionalHash(sigil.SecondaryHash),
			SecondaryTraitLevel: int(sigil.SecondaryLevel),
		})
	}
	for _, summon := range source.Summons {
		result.Summons = append(result.Summons, LoadoutShareSummon{
			TypeHash: hashText(summon.TypeHash), Name: summon.Name,
			MainTraitHash: hashText(summon.MainTraitHash), MainTraitLevel: int(summon.MainTraitLevel),
			SubParamHash: hashText(summon.SubParamHash), SubParamLevel: int(summon.SubParamLevel),
			Rank: int(summon.Rank),
		})
	}
	for _, skill := range source.Skills {
		result.Skills = append(result.Skills, LoadoutSkill{Hash: hashText(skill.Hash), Name: skill.Name, Key: skill.Key})
	}
	for _, hash := range source.WeaponSkillHashes {
		result.WeaponSkillHashes = append(result.WeaponSkillHashes, hashText(hash))
	}
	for _, hash := range source.MasteryHashes {
		result.MasteryHashes = append(result.MasteryHashes, hashText(hash))
	}
	if source.Character != nil {
		character := &LoadoutShareCharacterProgression{
			CharacterLevel: int(source.Character.CharacterLevel),
			BaseHP:         int(source.Character.BaseHP), BaseATK: int(source.Character.BaseATK),
			BaseStunBits: source.Character.BaseStunBits, BaseCritRate: int(source.Character.BaseCritRate),
			CharacterBaseCaptured:      source.Character.CharacterBaseCaptured,
			MasterTotalMSP:             int(source.Character.MasterTotalMSP),
			LegacyProgress:             int(source.Character.LegacyProgress),
			WeaponWrightstonesCaptured: source.Character.WeaponWrightstonesCaptured,
		}
		for _, value := range source.Character.EnhancementPanel {
			character.EnhancementPanel = append(character.EnhancementPanel, int(value))
		}
		for _, node := range source.Character.EnhancementNodes {
			character.EnhancementNodes = append(character.EnhancementNodes, LoadoutShareEnhancementNode{
				Index: int(node.Index), Value: int(node.Value),
			})
		}
		for _, weapon := range source.Character.Weapons {
			character.Weapons = append(character.Weapons, LoadoutShareProgressionWeapon{
				Hash: hashText(weapon.Hash), BaseHash: loadoutShareCodeOptionalHash(weapon.BaseHash),
				InternalID: weapon.InternalID, Level: int(weapon.Level), Uncap: int(weapon.Uncap),
				Mirage: int(weapon.Mirage), Awakening: int(weapon.Awakening),
				Transcendence: int(weapon.Transcendence), TranscendenceSkill: weapon.TranscendenceSkill,
				Wrightstone: expandLoadoutShareWrightstone(weapon.Wrightstone),
			})
		}
		result.Character = character
	}
	if source.Weapon != nil {
		weapon := &LoadoutShareWeaponState{
			StoredHash: hashText(source.Weapon.StoredHash), XP: source.Weapon.XP,
			Uncap: int(source.Weapon.Uncap), Mirage: int(source.Weapon.Mirage),
			Awakening: int(source.Weapon.Awakening), Transcendence: int(source.Weapon.Transcendence),
			ExactState: source.Weapon.ExactState, Flags: source.Weapon.Flags,
			WrightstoneReference: loadoutShareCodeOptionalHash(source.Weapon.WrightstoneReference),
			State:                int(source.Weapon.State), Wrightstone: expandLoadoutShareWrightstone(source.Weapon.Wrightstone),
		}
		for _, hash := range source.Weapon.SkillHashes {
			weapon.SkillHashes = append(weapon.SkillHashes, hashText(hash))
		}
		result.Weapon = weapon
	}
	for _, slot := range source.OverLimit {
		result.OverLimit = append(result.OverLimit, LoadoutShareOverLimit{
			Index: int(slot.Index), AttributeHash: loadoutShareCodeOptionalHash(slot.AttributeHash), Level: int(slot.Level),
		})
	}
	return result
}

func encodeLoadoutShareCode(source *LoadoutShare) (*LoadoutShareCodeResult, error) {
	compact, err := compactLoadoutShare(source)
	if err != nil {
		return nil, err
	}
	packed, err := msgpack.Marshal(compact)
	if err != nil {
		return nil, fmt.Errorf("编码紧凑配装失败: %w", err)
	}
	if len(packed) > loadoutShareMaxSize {
		return nil, fmt.Errorf("紧凑配装超过 1 MiB")
	}
	var compressed bytes.Buffer
	writer := brotli.NewWriterLevel(&compressed, brotli.BestCompression)
	if _, err := writer.Write(packed); err != nil {
		return nil, fmt.Errorf("压缩配装失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("结束配装压缩失败: %w", err)
	}
	frame := make([]byte, loadoutShareCodeHeaderSize, loadoutShareCodeHeaderSize+compressed.Len())
	copy(frame[:4], loadoutShareCodeFrameMagic)
	frame[4] = loadoutShareCodeFrameVersion
	frame[5] = loadoutShareCodeCodec
	binary.LittleEndian.PutUint32(frame[6:10], uint32(len(packed)))
	binary.LittleEndian.PutUint32(frame[10:14], crc32.ChecksumIEEE(packed))
	binary.LittleEndian.PutUint32(frame[14:18], uint32(compressed.Len()))
	frame = append(frame, compressed.Bytes()...)
	if len(frame) > loadoutShareCodeMaxFrameSize {
		return nil, fmt.Errorf("压缩后的配装分享码超过 %d KiB", loadoutShareCodeMaxFrameSize/1024)
	}
	jsonPayload, _ := json.Marshal(source)
	return &LoadoutShareCodeResult{
		CompatibilityCode: loadoutShareCodePrefix + base64.RawURLEncoding.EncodeToString(frame),
		CharacterName:     source.CharaName, LoadoutName: source.Name,
		JSONBytes: len(jsonPayload), PackedBytes: len(packed), CompressedBytes: compressed.Len(), FrameBytes: len(frame),
	}, nil
}

func normalizeLoadoutShareCode(code string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(code))
}

func validateCompactLoadoutShare(source *loadoutShareCodePayload, expectedVersion int, requireCharacterBase bool) error {
	if source.ShareVersion != expectedVersion {
		return fmt.Errorf("分享码内的配装版本为 v%d，当前帧需要 v%d", source.ShareVersion, expectedVersion)
	}
	if len(source.Sigils) > loadoutMaxSigils {
		return fmt.Errorf("分享码包含 %d 个因子，超过 %d 格上限", len(source.Sigils), loadoutMaxSigils)
	}
	if len(source.Summons) != 4 {
		return fmt.Errorf("分享码的召唤石配置需要恰好 4 槽")
	}
	if len(source.Skills) > loadoutMaxSkills {
		return fmt.Errorf("分享码包含 %d 个角色技能，超过 %d 格上限", len(source.Skills), loadoutMaxSkills)
	}
	if len(source.WeaponSkillHashes) != 5 {
		return fmt.Errorf("分享码的武器技能快照需要恰好 5 槽")
	}
	if len(source.MasteryHashes) != loadoutMaxMastery {
		return fmt.Errorf("分享码的专精快照需要恰好 %d 槽", loadoutMaxMastery)
	}
	if len(source.OverLimit) != 4 {
		return fmt.Errorf("分享码的上限突破配置需要恰好 4 槽")
	}
	if source.Character == nil || len(source.Character.EnhancementPanel) != 2 ||
		len(source.Character.EnhancementNodes) == 0 || len(source.Character.EnhancementNodes) > 1000 ||
		len(source.Character.Weapons) > 128 {
		return fmt.Errorf("分享码的角色强化快照结构无效")
	}
	if requireCharacterBase && !source.Character.CharacterBaseCaptured {
		return fmt.Errorf("分享码缺少角色等级基础快照")
	}
	for _, weapon := range source.Character.Weapons {
		if weapon.Wrightstone != nil && len(weapon.Wrightstone.Traits) > 3 {
			return fmt.Errorf("分享码的角色武器祝福超过 3 个词条槽")
		}
	}
	if source.Weapon != nil {
		if len(source.Weapon.SkillHashes) != 5 {
			return fmt.Errorf("分享码的当前武器技能快照需要恰好 5 槽")
		}
		if source.Weapon.Wrightstone != nil && len(source.Weapon.Wrightstone.Traits) > 3 {
			return fmt.Errorf("分享码的当前武器祝福超过 3 个词条槽")
		}
	}
	return nil
}

func upgradeLoadoutShareCodePayloadV1(source *loadoutShareCodePayloadV1) *loadoutShareCodePayload {
	if source == nil {
		return nil
	}
	result := &loadoutShareCodePayload{
		ShareVersion: source.ShareVersion, CharaHash: source.CharaHash, CharaName: source.CharaName,
		OwnerCode: source.OwnerCode, Name: source.Name, WeaponHash: source.WeaponHash, WeaponName: source.WeaponName,
		Sigils: source.Sigils, Summons: source.Summons, Skills: source.Skills,
		WeaponSkillHashes: source.WeaponSkillHashes, MasteryHashes: source.MasteryHashes,
		Weapon: source.Weapon, OverLimit: source.OverLimit,
	}
	if source.Character != nil {
		result.Character = &loadoutShareCodeCharacter{
			CharacterLevel: source.Character.CharacterLevel, MasterTotalMSP: source.Character.MasterTotalMSP,
			LegacyProgress: source.Character.LegacyProgress, EnhancementPanel: source.Character.EnhancementPanel,
			EnhancementNodes: source.Character.EnhancementNodes, Weapons: source.Character.Weapons,
			WeaponWrightstonesCaptured: source.Character.WeaponWrightstonesCaptured,
		}
	}
	return result
}

func decodeLoadoutShareCode(code string) (*LoadoutShare, error) {
	code = normalizeLoadoutShareCode(code)
	if !strings.HasPrefix(code, loadoutShareCodePrefix) {
		return nil, fmt.Errorf("分享码前缀无效；请粘贴 GBFRC1 紧凑码或兼容码")
	}
	encoded := strings.TrimPrefix(code, loadoutShareCodePrefix)
	if len(encoded) == 0 || len(encoded) > base64.RawURLEncoding.EncodedLen(loadoutShareCodeMaxFrameSize) {
		return nil, fmt.Errorf("分享码长度无效")
	}
	frame, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("分享码文本损坏: %w", err)
	}
	if len(frame) < loadoutShareCodeHeaderSize || string(frame[:4]) != loadoutShareCodeFrameMagic {
		return nil, fmt.Errorf("分享码帧头无效")
	}
	if frame[4] != loadoutShareCodeFrameVersion && frame[4] != loadoutShareCodeLegacyFrameVersion {
		return nil, fmt.Errorf("暂不支持分享码协议版本 %d", frame[4])
	}
	if frame[5] != loadoutShareCodeCodec {
		return nil, fmt.Errorf("暂不支持分享码压缩格式 %d", frame[5])
	}
	rawSize := int(binary.LittleEndian.Uint32(frame[6:10]))
	if rawSize <= 0 || rawSize > loadoutShareMaxSize {
		return nil, fmt.Errorf("分享码声明的原始长度无效")
	}
	compressedSize := int(binary.LittleEndian.Uint32(frame[14:18]))
	if compressedSize <= 0 || compressedSize != len(frame)-loadoutShareCodeHeaderSize {
		return nil, fmt.Errorf("分享码压缩长度校验失败")
	}
	reader := brotli.NewReader(bytes.NewReader(frame[loadoutShareCodeHeaderSize:]))
	packed, err := io.ReadAll(io.LimitReader(reader, int64(loadoutShareMaxSize+1)))
	if err != nil {
		return nil, fmt.Errorf("解压分享码失败: %w", err)
	}
	if len(packed) != rawSize {
		return nil, fmt.Errorf("分享码长度校验失败")
	}
	if crc32.ChecksumIEEE(packed) != binary.LittleEndian.Uint32(frame[10:14]) {
		return nil, fmt.Errorf("分享码校验和不一致，内容可能被聊天软件截断或改写")
	}
	var compact *loadoutShareCodePayload
	if frame[4] == loadoutShareCodeLegacyFrameVersion {
		var legacy loadoutShareCodePayloadV1
		if err := msgpack.Unmarshal(packed, &legacy); err != nil {
			return nil, fmt.Errorf("解析旧版紧凑配装失败: %w", err)
		}
		compact = upgradeLoadoutShareCodePayloadV1(&legacy)
		if err := validateCompactLoadoutShare(compact, 8, false); err != nil {
			return nil, err
		}
	} else {
		compact = &loadoutShareCodePayload{}
		if err := msgpack.Unmarshal(packed, compact); err != nil {
			return nil, fmt.Errorf("解析紧凑配装失败: %w", err)
		}
		if err := validateCompactLoadoutShare(compact, loadoutShareVersion, true); err != nil {
			return nil, err
		}
	}
	share := expandLoadoutShare(compact)
	return share, nil
}

func (a *App) LoadoutShareCode(savePath string, unitID uint32) (*LoadoutShareCodeResult, error) {
	share, err := buildLoadoutShare(savePath, unitID)
	if err != nil {
		return nil, err
	}
	return encodeLoadoutShareCode(share)
}

func (a *App) LoadoutImportCode(savePath, expectCharaHash, code string) (*LoadoutImportDraft, error) {
	share, err := decodeLoadoutShareCode(code)
	if err != nil {
		return nil, err
	}
	return resolveLoadoutShare(savePath, expectCharaHash, share)
}
