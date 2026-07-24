package backend

import (
	"fmt"
	"math"
)

const formulaSamplerStableSnapshotCount = 3

type FormulaSamplePhase string

const (
	FormulaPhaseA1 FormulaSamplePhase = "A1"
	FormulaPhaseB1 FormulaSamplePhase = "B1"
	FormulaPhaseA2 FormulaSamplePhase = "A2"
	FormulaPhaseB2 FormulaSamplePhase = "B2"
)

var formulaSamplePhaseOrder = [...]FormulaSamplePhase{
	FormulaPhaseA1, FormulaPhaseB1, FormulaPhaseA2, FormulaPhaseB2,
}

type FormulaSampleEvent struct {
	Phase FormulaSamplePhase         `json:"phase"`
	Panel RuntimeCharacterPanelStats `json:"panel"`
}

type formulaSampler struct {
	validate     func() error
	readSnapshot func() (RuntimeCharacterPanelStats, error)
	events       []FormulaSampleEvent
	control      bool
	scanOnly     bool
}

func newFormulaSampler(validate func() error, readSnapshot func() (RuntimeCharacterPanelStats, error)) *formulaSampler {
	return &formulaSampler{validate: validate, readSnapshot: readSnapshot}
}

func newFormulaSamplerForExperiment(experimentType string, validate func() error, readSnapshot func() (RuntimeCharacterPanelStats, error)) *formulaSampler {
	sampler := newFormulaSampler(validate, readSnapshot)
	sampler.control = experimentType == "control"
	sampler.scanOnly = formulaExperimentAllowsUnchangedKnownPanel(experimentType)
	return sampler
}

func (sampler *formulaSampler) Capture(phase FormulaSamplePhase) (FormulaSampleEvent, error) {
	if sampler == nil || sampler.validate == nil || sampler.readSnapshot == nil {
		return FormulaSampleEvent{}, fmt.Errorf("formula sampler is not initialized")
	}
	if len(sampler.events) >= len(formulaSamplePhaseOrder) {
		return FormulaSampleEvent{}, fmt.Errorf("formula experiment is already complete")
	}
	expected := formulaSamplePhaseOrder[len(sampler.events)]
	if phase != expected {
		return FormulaSampleEvent{}, fmt.Errorf("formula phase %s is out of order; want %s", phase, expected)
	}
	if err := sampler.validate(); err != nil {
		return FormulaSampleEvent{}, fmt.Errorf("validate formula sampler before %s: %w", phase, err)
	}
	panel, err := readStableFormulaPanelSnapshots(sampler.readSnapshot)
	if err != nil {
		return FormulaSampleEvent{}, err
	}
	if err := sampler.validate(); err != nil {
		return FormulaSampleEvent{}, fmt.Errorf("validate formula sampler after %s: %w", phase, err)
	}
	if err := sampler.validateTransition(phase, panel); err != nil {
		return FormulaSampleEvent{}, err
	}
	event := FormulaSampleEvent{Phase: phase, Panel: panel}
	sampler.events = append(sampler.events, event)
	return event, nil
}

func (sampler *formulaSampler) validateTransition(phase FormulaSamplePhase, panel RuntimeCharacterPanelStats) error {
	switch phase {
	case FormulaPhaseA1:
		return nil
	case FormulaPhaseB1:
		if sampler.control {
			if !formulaPanelValuesBitEqual(sampler.events[0].Panel, panel) {
				return fmt.Errorf("formula control phase B1 changed the panel")
			}
			return nil
		}
		if formulaPanelValuesBitEqual(sampler.events[0].Panel, panel) && !sampler.scanOnly {
			return fmt.Errorf("formula phase B1 did not change the panel")
		}
	case FormulaPhaseA2:
		if !formulaPanelValuesBitEqual(sampler.events[0].Panel, panel) {
			return fmt.Errorf("formula phase A2 did not restore A1")
		}
	case FormulaPhaseB2:
		if sampler.control {
			if !formulaPanelValuesBitEqual(sampler.events[0].Panel, panel) {
				return fmt.Errorf("formula control phase B2 changed the panel")
			}
			return nil
		}
		if !formulaPanelValuesBitEqual(sampler.events[1].Panel, panel) {
			return fmt.Errorf("formula phase B2 did not reproduce B1")
		}
	}
	return nil
}

func (sampler *formulaSampler) Complete() bool {
	return sampler != nil && len(sampler.events) == len(formulaSamplePhaseOrder)
}

func readStableFormulaPanelSnapshots(readSnapshot func() (RuntimeCharacterPanelStats, error)) (RuntimeCharacterPanelStats, error) {
	if readSnapshot == nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("formula panel snapshot reader is nil")
	}
	var frames [formulaSamplerStableSnapshotCount]RuntimeCharacterPanelStats
	for index := range frames {
		frame, err := readSnapshot()
		if err != nil {
			return RuntimeCharacterPanelStats{}, fmt.Errorf("read formula panel snapshot %d: %w", index+1, err)
		}
		frames[index] = frame
	}
	if !formulaPanelSnapshotsBitEqual(frames[0], frames[1]) || !formulaPanelSnapshotsBitEqual(frames[0], frames[2]) {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("formula panel changed across three bit-exact snapshots")
	}
	frames[0] = markRuntimeCharacterPanelStable(frames[0], len(frames))
	return frames[0], nil
}

func formulaPanelSnapshotsBitEqual(left, right RuntimeCharacterPanelStats) bool {
	return formulaPanelValuesBitEqual(left, right) &&
		left.CandidateObjectHash == right.CandidateObjectHash &&
		left.IdentitySource == right.IdentitySource &&
		left.HPField == right.HPField &&
		left.AttackField == right.AttackField &&
		left.StunField == right.StunField &&
		left.CritField == right.CritField &&
		left.Source == right.Source &&
		left.Verification == right.Verification &&
		left.GameVersion == right.GameVersion &&
		left.RuntimeVerified == right.RuntimeVerified
}

func formulaPanelValuesBitEqual(left, right RuntimeCharacterPanelStats) bool {
	return left.CharacterHash == right.CharacterHash &&
		left.RuntimeID == right.RuntimeID &&
		left.HP == right.HP &&
		left.Attack == right.Attack &&
		math.Float32bits(left.StunPower) == math.Float32bits(right.StunPower) &&
		math.Float32bits(left.RawStunPower) == math.Float32bits(right.RawStunPower) &&
		math.Float32bits(left.CritRate) == math.Float32bits(right.CritRate)
}
