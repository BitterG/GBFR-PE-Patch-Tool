package backend

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

const formulaSampleSchemaVersion = "gbfr-formula-sample/v3"
const formulaSampleCandidateLimit = 4096

var formulaExperimentTypes = map[string]struct{}{
	"weapon": {}, "weapon_skill": {}, "sigil": {}, "mastery": {},
	"overlimit": {}, "summon": {}, "hp_condition": {},
	"battle_condition": {}, "defense": {}, "damage_cap": {},
	"control": {}, "other": {},
}

func validFormulaExperimentType(value string) bool {
	_, ok := formulaExperimentTypes[value]
	return ok
}

func formulaExperimentAllowsUnchangedKnownPanel(value string) bool {
	switch value {
	case "mastery", "defense", "damage_cap":
		return true
	default:
		return false
	}
}

// FormulaSampleManifest intentionally contains no machine, process, path or
// wall-clock identity. A bundle is evidence about a reversible experiment,
// not a diagnostic dump of the operator's computer.
type FormulaSampleManifest struct {
	SchemaVersion  string               `json:"schemaVersion"`
	Generator      string               `json:"generator"`
	GameVersion    string               `json:"gameVersion"`
	ExperimentType string               `json:"experimentType"`
	StrictReadOnly bool                 `json:"strictReadOnly"`
	PhaseOrder     []FormulaSamplePhase `json:"phaseOrder"`
	PhaseCount     int                  `json:"phaseCount"`
	CandidateCount int                  `json:"candidateCount"`
	Entries        []string             `json:"entries"`
	Privacy        string               `json:"privacy"`
}

type FormulaRedactedPanel struct {
	CharacterHash       string                            `json:"characterHash"`
	RuntimeID           string                            `json:"runtimeId"`
	CandidateObjectHash string                            `json:"candidateObjectHash"`
	IdentitySource      string                            `json:"identitySource"`
	HP                  int32                             `json:"hp"`
	Attack              int32                             `json:"attack"`
	StunPower           float32                           `json:"stunPower"`
	RawStunPower        float32                           `json:"rawStunPower"`
	CritRate            float32                           `json:"critRate"`
	HPField             RuntimeCharacterPanelFieldReading `json:"hpField"`
	AttackField         RuntimeCharacterPanelFieldReading `json:"attackField"`
	StunField           RuntimeCharacterPanelFieldReading `json:"stunField"`
	CritField           RuntimeCharacterPanelFieldReading `json:"critField"`
	GameVersion         string                            `json:"gameVersion"`
	RuntimeVerified     bool                              `json:"runtimeVerified"`
}

type FormulaRedactedEvent struct {
	Phase FormulaSamplePhase   `json:"phase"`
	Panel FormulaRedactedPanel `json:"panel"`
}

type FormulaObservation struct {
	Phase        FormulaSamplePhase `json:"phase"`
	Field        string             `json:"field"`
	DisplayValue string             `json:"displayValue"`
	RawBits      string             `json:"rawBits"`
	Evidence     string             `json:"evidence"`
}

type FormulaPanelDelta struct {
	HP        int32   `json:"hp"`
	Attack    int32   `json:"attack"`
	StunPower float32 `json:"stunPower"`
	CritRate  float32 `json:"critRate"`
}

type FormulaEvidenceModel struct {
	Kind                  string            `json:"kind"`
	Status                string            `json:"status"`
	A1ToB1                FormulaPanelDelta `json:"a1ToB1"`
	A2ToB2                FormulaPanelDelta `json:"a2ToB2"`
	PanelRepeatedBitExact bool              `json:"panelRepeatedBitExact"`
	PanelRestoredBitExact bool              `json:"panelRestoredBitExact"`
	CandidateABABVerified bool              `json:"candidateABABVerified"`
	KnownProbeCoverage    []string          `json:"knownProbeCoverage"`
	UnavailableProbes     []string          `json:"unavailableProbes"`
}

type FormulaRedactionReport struct {
	RawMemoryIncluded bool     `json:"rawMemoryIncluded"`
	AbsoluteAddresses bool     `json:"absoluteAddressesIncluded"`
	CandidateLimit    int      `json:"candidateLimit"`
	Omitted           []string `json:"omitted"`
	CandidateFilters  []string `json:"candidateFilters"`
}

func buildFormulaSampleBundle(experimentType string, events []FormulaSampleEvent, raw map[FormulaSamplePhase][]byte, isMappedAddress formulaMappedAddressProbe, excludedWords map[int]struct{}) ([]byte, error) {
	if !validFormulaExperimentType(experimentType) {
		return nil, fmt.Errorf("公式实验类型 %q 无效", experimentType)
	}
	if len(events) != len(formulaSamplePhaseOrder) {
		return nil, fmt.Errorf("公式实验需要完整的 A1/B1/A2/B2 四阶段")
	}
	for index, expected := range formulaSamplePhaseOrder {
		if events[index].Phase != expected {
			return nil, fmt.Errorf("公式实验第 %d 阶段为 %s，应为 %s", index+1, events[index].Phase, expected)
		}
		if !events[index].Panel.RuntimeVerified {
			return nil, fmt.Errorf("公式实验 %s 的游戏面板未通过三次稳定回读", expected)
		}
		if index > 0 && events[index].Panel.CharacterHash != events[0].Panel.CharacterHash {
			return nil, fmt.Errorf("公式实验阶段间角色不一致")
		}
	}
	for _, phase := range formulaSamplePhaseOrder {
		if len(raw[phase]) != formulaStatusObjectScanSize {
			return nil, fmt.Errorf("公式实验 %s 缺少稳定的状态对象证据", phase)
		}
	}
	candidates, err := diffFormulaStatusABAB(
		raw[FormulaPhaseA1], raw[FormulaPhaseB1], raw[FormulaPhaseA2], raw[FormulaPhaseB2],
		isMappedAddress, excludedWords,
	)
	if err != nil {
		return nil, err
	}
	if len(candidates) > formulaSampleCandidateLimit {
		return nil, fmt.Errorf("公式候选数量 %d 超过脱敏上限 %d，请确认每轮只改变一个项目", len(candidates), formulaSampleCandidateLimit)
	}
	panelChanged := !formulaPanelValuesBitEqual(events[0].Panel, events[1].Panel)
	if experimentType != "control" && !formulaExperimentAllowsUnchangedKnownPanel(experimentType) && !panelChanged && len(candidates) == 0 {
		return nil, fmt.Errorf("公式实验的已知面板与状态窗口都没有可逆变化，请确认只改变了目标项目并完成 A/B/A/B")
	}

	redactedEvents := make([]FormulaRedactedEvent, len(events))
	observations := make([]FormulaObservation, 0, len(events)*4)
	for index, event := range events {
		redactedEvents[index] = FormulaRedactedEvent{Phase: event.Phase, Panel: redactFormulaPanel(event.Panel)}
		observations = append(observations, formulaPanelObservations(event)...)
	}
	modelKind := "known-final-panel-difference"
	modelStatus := "evidence-only; known final-panel change repeated; no unverified game formula asserted"
	if !panelChanged && len(candidates) > 0 {
		modelKind = "reversible-status-candidate-scan"
		modelStatus = "evidence-only; known final panel unchanged; reversible status candidates require cross-run semantic identification"
	} else if !panelChanged {
		modelKind = "no-observed-known-panel-or-status-candidate"
		modelStatus = "negative observation only; operator change record and independent repetitions required; absence is not proof of no game effect"
	}
	model := FormulaEvidenceModel{
		Kind:                  modelKind,
		Status:                modelStatus,
		A1ToB1:                formulaPanelDelta(events[0].Panel, events[1].Panel),
		A2ToB2:                formulaPanelDelta(events[2].Panel, events[3].Panel),
		PanelRepeatedBitExact: formulaPanelValuesBitEqual(events[1].Panel, events[3].Panel),
		PanelRestoredBitExact: formulaPanelValuesBitEqual(events[0].Panel, events[2].Panel),
		CandidateABABVerified: len(candidates) > 0,
		KnownProbeCoverage:    []string{"final_hp", "final_attack", "final_stun_power", "final_critical_rate", "status_object_relative_scalar_candidates"},
		UnavailableProbes:     []string{"absolute_defense", "battle_damage_cap_resolution", "conditional_combat_skill_resolution"},
	}
	redaction := FormulaRedactionReport{
		RawMemoryIncluded: false,
		AbsoluteAddresses: false,
		CandidateLimit:    formulaSampleCandidateLimit,
		Omitted: []string{
			"PID and process creation time", "module base and heap addresses", "save and executable paths",
			"Windows user identity", "custom character names and free-form notes", "raw 24 KiB status snapshots",
		},
		CandidateFilters: []string{
			"every exported candidate requires A1=A2, B1=B2 and A!=B at the same relative offset",
			"64-bit values confirmed by VirtualQueryEx as mapped process addresses are removed at capture time and before export; masks are unioned across all phases",
			"candidate exports contain relative offsets and A-to-B deltas only, never absolute A/B scalar bit patterns",
			"non-finite and implausible i32/f32 values are removed", "only relative offsets within 0x0000..0x5FFF are retained",
		},
	}

	entryNames := []string{
		"manifest.json", "events.json", "observations.json", "candidates.json",
		"formula-model.json", "runtime-layout.json", "redaction-report.json", "README.txt", "SHA256SUMS",
	}
	manifest := FormulaSampleManifest{
		SchemaVersion:  formulaSampleSchemaVersion,
		Generator:      appVersion,
		GameVersion:    "DLC 2.0.2",
		ExperimentType: experimentType,
		StrictReadOnly: true,
		PhaseOrder:     append([]FormulaSamplePhase(nil), formulaSamplePhaseOrder[:]...),
		PhaseCount:     len(events),
		CandidateCount: len(candidates),
		Entries:        append([]string(nil), entryNames...),
		Privacy:        "machine and process identity removed; raw blocks omitted",
	}
	entries := map[string][]byte{}
	for name, value := range map[string]any{
		"manifest.json": manifest, "events.json": redactedEvents, "observations.json": observations,
		"candidates.json": candidates, "formula-model.json": model,
		"runtime-layout.json": runtimeCharacterPanelLayoutDescriptor(), "redaction-report.json": redaction,
	} {
		payload, marshalErr := json.MarshalIndent(value, "", "  ")
		if marshalErr != nil {
			return nil, fmt.Errorf("编码公式采样条目 %s: %w", name, marshalErr)
		}
		entries[name] = payload
	}
	entries["README.txt"] = []byte(formulaSampleReadme)
	return writeFormulaSampleZip(entries)
}

func redactFormulaPanel(panel RuntimeCharacterPanelStats) FormulaRedactedPanel {
	return FormulaRedactedPanel{
		CharacterHash: panel.CharacterHash, RuntimeID: panel.RuntimeID,
		CandidateObjectHash: panel.CandidateObjectHash, IdentitySource: panel.IdentitySource,
		HP: panel.HP, Attack: panel.Attack, StunPower: panel.StunPower,
		RawStunPower: panel.RawStunPower, CritRate: panel.CritRate,
		HPField: panel.HPField, AttackField: panel.AttackField,
		StunField: panel.StunField, CritField: panel.CritField,
		GameVersion: panel.GameVersion, RuntimeVerified: panel.RuntimeVerified,
	}
}

func formulaPanelObservations(event FormulaSampleEvent) []FormulaObservation {
	evidence := "runtime-final-panel; three bit-exact reads before and after status-window capture"
	return []FormulaObservation{
		{Phase: event.Phase, Field: "hp", DisplayValue: fmt.Sprintf("%d", event.Panel.HP), RawBits: fmt.Sprintf("0x%08X", uint32(event.Panel.HP)), Evidence: evidence},
		{Phase: event.Phase, Field: "attack", DisplayValue: fmt.Sprintf("%d", event.Panel.Attack), RawBits: fmt.Sprintf("0x%08X", uint32(event.Panel.Attack)), Evidence: evidence},
		{Phase: event.Phase, Field: "stunPower", DisplayValue: fmt.Sprintf("%g", event.Panel.StunPower), RawBits: fmt.Sprintf("0x%08X", math.Float32bits(event.Panel.RawStunPower)), Evidence: evidence + "; raw f32 scaled by runtime-layout displayScale"},
		{Phase: event.Phase, Field: "critRate", DisplayValue: fmt.Sprintf("%g", event.Panel.CritRate), RawBits: fmt.Sprintf("0x%08X", math.Float32bits(event.Panel.CritRate)), Evidence: evidence},
	}
}

func formulaPanelDelta(a, b RuntimeCharacterPanelStats) FormulaPanelDelta {
	return FormulaPanelDelta{HP: b.HP - a.HP, Attack: b.Attack - a.Attack, StunPower: b.StunPower - a.StunPower, CritRate: b.CritRate - a.CritRate}
}

func writeFormulaSampleZip(entries map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		digest := sha256.Sum256(entries[name])
		fmt.Fprintf(&sums, "%x  %s\n", digest, name)
	}
	entries["SHA256SUMS"] = []byte(sums.String())
	names = append(names, "SHA256SUMS")
	sort.Strings(names)

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		stream, err := writer.Create(name)
		if err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("创建公式采样包条目 %s: %w", name, err)
		}
		if _, err := stream.Write(entries[name]); err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("写入公式采样包条目 %s: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("完成公式采样包: %w", err)
	}
	return buffer.Bytes(), nil
}

const formulaSampleReadme = `GBFR character-formula evidence bundle / GBFR 角色公式证据包

This archive contains a strict read-only A1/B1/A2/B2 experiment. It does not contain raw memory,
process identity, absolute addresses, local paths, usernames, timestamps, or free-form notes.
Validate every listed file against SHA256SUMS before analysis. runtime-layout.json records the guarded
access chain, field types, relative offsets, display scales, executable hash and evidence grade. Relative scalar candidates are clues,
not verified formulas; formula-model.json deliberately reports evidence only. A no-change mastery,
defense, or damage-cap bundle is a negative observation that requires operator records and repetitions,
not proof that the game has no effect.

本包来自严格只读的 A1/B1/A2/B2 单变量实验，不包含原始内存、进程身份、绝对地址、本地路径、
用户名、时间戳或自由备注。分析前请核对 SHA256SUMS。runtime-layout.json 记录受守卫保护的访问链、
字段类型、相对偏移、显示倍率、EXE 哈希及证据等级。相对偏移候选只是线索，不等于已验证公式。
专精、防御力或伤害上限实验若没有观察到变化，只能作为需要操作记录和重复实验支持的负观察，不能单独证明游戏没有该效果。
`
