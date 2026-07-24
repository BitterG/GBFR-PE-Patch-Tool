package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type FormulaSamplerStatus struct {
	Connected      bool                 `json:"connected"`
	Complete       bool                 `json:"complete"`
	Events         []FormulaSampleEvent `json:"events"`
	SessionToken   string               `json:"sessionToken"`
	ExperimentType string               `json:"experimentType"`
}

type FormulaChangeRecordingStatus struct {
	Recording     bool                       `json:"recording"`
	Before        RuntimeCharacterPanelStats `json:"before"`
	EvidenceLevel string                     `json:"evidenceLevel"`
}

type FormulaChangeAnalysis struct {
	Before              RuntimeCharacterPanelStats `json:"before"`
	After               RuntimeCharacterPanelStats `json:"after"`
	PanelDelta          FormulaPanelDelta          `json:"panelDelta"`
	Candidates          []FormulaScalarCandidate   `json:"candidates"`
	PanelChanged        bool                       `json:"panelChanged"`
	EvidenceLevel       string                     `json:"evidenceLevel"`
	NegativeObservation string                     `json:"negativeObservation,omitempty"`
}

type formulaSamplerSession struct {
	mu              sync.Mutex
	process         *readOnlyGameProcess
	sampler         *formulaSampler
	raw             map[FormulaSamplePhase][]byte
	pointerWords    map[FormulaSamplePhase]map[int]struct{}
	captureStatus   func() ([]byte, error)
	isMappedAddress formulaMappedAddressProbe
	token           string
	experimentType  string
	targetHash      uint32
	statusAddress   uintptr
	recording       bool
	recordingPanel  RuntimeCharacterPanelStats
	recordingRaw    []byte
	recordingWords  map[int]struct{}
	ctx             context.Context
	cancel          context.CancelFunc
}

func (session *formulaSamplerSession) startChangeRecording() (FormulaChangeRecordingStatus, error) {
	if session == nil || session.sampler == nil || session.captureStatus == nil {
		return FormulaChangeRecordingStatus{}, fmt.Errorf("公式变化记录器未初始化")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.recording {
		return FormulaChangeRecordingStatus{}, fmt.Errorf("公式变化记录已经开始")
	}
	if session.process == nil {
		return FormulaChangeRecordingStatus{}, fmt.Errorf("只读公式采样连接已关闭")
	}
	panel, err := readStableFormulaPanelSnapshots(session.sampler.readSnapshot)
	if err != nil {
		return FormulaChangeRecordingStatus{}, err
	}
	raw, err := session.captureStatus()
	if err != nil {
		return FormulaChangeRecordingStatus{}, err
	}
	raw, words, err := redactFormulaMappedPointerWords(raw, session.isMappedAddress)
	if err != nil {
		return FormulaChangeRecordingStatus{}, err
	}
	session.recording = true
	session.recordingPanel = panel
	session.recordingRaw = raw
	session.recordingWords = words
	return FormulaChangeRecordingStatus{Recording: true, Before: panel, EvidenceLevel: "stable_before_candidate_transition"}, nil
}

func (session *formulaSamplerSession) stopChangeRecording() (FormulaChangeAnalysis, error) {
	if session == nil || session.sampler == nil || session.captureStatus == nil {
		return FormulaChangeAnalysis{}, fmt.Errorf("公式变化记录器未初始化")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.recording || len(session.recordingRaw) != formulaStatusObjectScanSize {
		return FormulaChangeAnalysis{}, fmt.Errorf("公式变化记录尚未开始")
	}
	if session.process == nil {
		return FormulaChangeAnalysis{}, fmt.Errorf("只读公式采样连接已关闭")
	}
	after, err := readStableFormulaPanelSnapshots(session.sampler.readSnapshot)
	if err != nil {
		return FormulaChangeAnalysis{}, err
	}
	raw, err := session.captureStatus()
	if err != nil {
		return FormulaChangeAnalysis{}, err
	}
	raw, afterWords, err := redactFormulaMappedPointerWords(raw, session.isMappedAddress)
	if err != nil {
		return FormulaChangeAnalysis{}, err
	}
	excluded := make(map[int]struct{}, len(session.recordingWords)+len(afterWords))
	for offset := range session.recordingWords {
		excluded[offset] = struct{}{}
	}
	for offset := range afterWords {
		excluded[offset] = struct{}{}
	}
	candidates, err := diffFormulaStatusABAB(session.recordingRaw, raw, session.recordingRaw, raw, session.isMappedAddress, excluded)
	if err != nil {
		return FormulaChangeAnalysis{}, err
	}
	analysis := FormulaChangeAnalysis{
		Before:        session.recordingPanel,
		After:         after,
		PanelDelta:    formulaPanelDelta(session.recordingPanel, after),
		Candidates:    candidates,
		PanelChanged:  !formulaPanelValuesBitEqual(session.recordingPanel, after),
		EvidenceLevel: "candidate_single_transition_stable_endpoints",
	}
	if !analysis.PanelChanged && len(candidates) == 0 {
		analysis.EvidenceLevel = "negative_observation"
		analysis.NegativeObservation = "稳定前后四项面板及 24 KiB 状态窗口均未观察到变化；这不是无效果证明。"
	}
	session.recording = false
	session.recordingPanel = RuntimeCharacterPanelStats{}
	session.recordingRaw = nil
	session.recordingWords = nil
	return analysis, nil
}

func (session *formulaSamplerSession) observe() (RuntimeCharacterPanelStats, error) {
	if session == nil || session.sampler == nil || session.sampler.readSnapshot == nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("公式采样会话未初始化")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.process == nil {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("只读公式采样连接已关闭")
	}
	if err := session.process.Validate(); err != nil {
		return RuntimeCharacterPanelStats{}, err
	}
	currentAddress, err := locateRuntimeCharacterPanelStatus(session.process, session.process.moduleBase, session.targetHash)
	if err != nil {
		return RuntimeCharacterPanelStats{}, err
	}
	if currentAddress != session.statusAddress {
		return RuntimeCharacterPanelStats{}, fmt.Errorf("角色状态对象已重建，请断开后重新连接采样器")
	}
	return readStableFormulaPanelSnapshots(session.sampler.readSnapshot)
}

func captureStableFormulaStatusSnapshots(validate func() error, readSnapshot func() ([]byte, error)) ([]byte, error) {
	if validate == nil || readSnapshot == nil {
		return nil, fmt.Errorf("公式状态采样器未初始化")
	}
	if err := validate(); err != nil {
		return nil, fmt.Errorf("公式状态采样前校验失败: %w", err)
	}
	frames := make([][]byte, formulaSamplerStableSnapshotCount)
	for index := range frames {
		frame, err := readSnapshot()
		if err != nil {
			return nil, fmt.Errorf("第 %d 次公式状态快照失败: %w", index+1, err)
		}
		frames[index] = frame
	}
	if err := validate(); err != nil {
		return nil, fmt.Errorf("公式状态采样后校验失败: %w", err)
	}
	if len(frames[0]) != formulaStatusObjectScanSize ||
		!bytes.Equal(frames[0], frames[1]) || !bytes.Equal(frames[0], frames[2]) {
		return nil, fmt.Errorf("角色状态对象在连续三次位精确快照间发生变化，请等待稳定后重试")
	}
	return append([]byte(nil), frames[0]...), nil
}

func (session *formulaSamplerSession) capture(phase FormulaSamplePhase) (FormulaSampleEvent, error) {
	if session == nil || session.sampler == nil || session.captureStatus == nil {
		return FormulaSampleEvent{}, fmt.Errorf("公式采样会话未初始化")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.ctx != nil {
		if err := session.ctx.Err(); err != nil {
			return FormulaSampleEvent{}, err
		}
	}
	before := len(session.sampler.events)
	event, err := session.sampler.Capture(phase)
	if err != nil {
		return FormulaSampleEvent{}, err
	}
	raw, err := session.captureStatus()
	if err != nil {
		session.sampler.events = session.sampler.events[:before]
		return FormulaSampleEvent{}, err
	}
	var pointerWords map[int]struct{}
	raw, pointerWords, err = redactFormulaMappedPointerWords(raw, session.isMappedAddress)
	if err != nil {
		session.sampler.events = session.sampler.events[:before]
		return FormulaSampleEvent{}, err
	}
	if err := session.sampler.validate(); err != nil {
		session.sampler.events = session.sampler.events[:before]
		return FormulaSampleEvent{}, fmt.Errorf("validate formula panel after status capture: %w", err)
	}
	afterPanel, err := readStableFormulaPanelSnapshots(session.sampler.readSnapshot)
	if err != nil {
		session.sampler.events = session.sampler.events[:before]
		return FormulaSampleEvent{}, err
	}
	if err := session.sampler.validate(); err != nil {
		session.sampler.events = session.sampler.events[:before]
		return FormulaSampleEvent{}, fmt.Errorf("validate formula panel after consistency capture: %w", err)
	}
	if !formulaPanelSnapshotsBitEqual(event.Panel, afterPanel) {
		session.sampler.events = session.sampler.events[:before]
		return FormulaSampleEvent{}, fmt.Errorf("formula panel changed across the status-object capture; wait for a stable screen and retry")
	}
	if session.raw == nil {
		session.raw = make(map[FormulaSamplePhase][]byte, len(formulaSamplePhaseOrder))
	}
	if session.pointerWords == nil {
		session.pointerWords = make(map[FormulaSamplePhase]map[int]struct{}, len(formulaSamplePhaseOrder))
	}
	session.raw[phase] = raw
	session.pointerWords[phase] = pointerWords
	return event, nil
}

func (session *formulaSamplerSession) close() error {
	if session == nil {
		return nil
	}
	if session.cancel != nil {
		session.cancel()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.cancel = nil
	if session.process == nil {
		return nil
	}
	process := session.process
	session.process = nil
	return process.Close()
}

func (session *formulaSamplerSession) status() FormulaSamplerStatus {
	if session == nil || session.sampler == nil {
		return FormulaSamplerStatus{}
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	events := append([]FormulaSampleEvent(nil), session.sampler.events...)
	return FormulaSamplerStatus{Connected: true, Complete: session.sampler.Complete(), Events: events, SessionToken: session.token, ExperimentType: session.experimentType}
}

func (session *formulaSamplerSession) validate() error {
	if session == nil {
		return fmt.Errorf("read-only formula sampler session is closed")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.process == nil {
		return fmt.Errorf("read-only formula sampler process is closed")
	}
	return session.process.Validate()
}

func (session *formulaSamplerSession) bundle() ([]byte, error) {
	if session == nil {
		return nil, fmt.Errorf("formula sampler session is closed")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.sampler == nil {
		return nil, fmt.Errorf("formula sampler session is not initialized")
	}
	excludedWords := make(map[int]struct{})
	for _, words := range session.pointerWords {
		for offset := range words {
			excludedWords[offset] = struct{}{}
		}
	}
	return buildFormulaSampleBundle(session.experimentType, session.sampler.events, session.raw, session.isMappedAddress, excludedWords)
}

// FormulaSamplerAttach starts an independent read-only connection. The only
// granted rights are query-information and VM-read; no editor handle, hook or
// code cave is reused by this page.
func (a *App) FormulaSamplerAttach(charaHex, experimentType string) (FormulaSamplerStatus, error) {
	targetHash, err := ParseHashHex(charaHex)
	if err != nil || targetHash == 0 {
		return FormulaSamplerStatus{}, fmt.Errorf("角色 hash %q 无效", charaHex)
	}
	if !validFormulaExperimentType(experimentType) {
		return FormulaSamplerStatus{}, fmt.Errorf("公式实验类型 %q 无效", experimentType)
	}
	a.formulaSamplerMu.Lock()
	defer a.formulaSamplerMu.Unlock()
	if a.formulaSamplerSession != nil {
		_ = a.formulaSamplerSession.close()
		a.formulaSamplerSession = nil
	}
	process, err := openReadOnlyGameProcess(windowsReadOnlyProcessBackend{}, charaProcessName, runtimeCharacterPanelVersionGuards)
	if err != nil {
		return FormulaSamplerStatus{}, err
	}
	statusAddress, err := locateRuntimeCharacterPanelStatus(process, process.moduleBase, targetHash)
	if err != nil {
		_ = process.Close()
		return FormulaSamplerStatus{}, err
	}
	session := &formulaSamplerSession{
		process:         process,
		raw:             make(map[FormulaSamplePhase][]byte, len(formulaSamplePhaseOrder)),
		pointerWords:    make(map[FormulaSamplePhase]map[int]struct{}, len(formulaSamplePhaseOrder)),
		experimentType:  experimentType,
		targetHash:      targetHash,
		statusAddress:   statusAddress,
		isMappedAddress: process.IsMappedAddress,
	}
	a.formulaSamplerGeneration++
	session.token = fmt.Sprintf("formula-%d", a.formulaSamplerGeneration)
	session.ctx, session.cancel = context.WithCancel(context.Background())
	session.sampler = newFormulaSamplerForExperiment(experimentType, process.Validate, func() (RuntimeCharacterPanelStats, error) {
		return readRuntimeCharacterPanel(process, process.moduleBase, targetHash)
	})
	session.captureStatus = func() ([]byte, error) {
		captureContext, cancel := context.WithTimeout(session.ctx, 5*time.Second)
		defer cancel()
		return captureStableFormulaStatusSnapshots(process.Validate, func() ([]byte, error) {
			currentAddress, locateErr := locateRuntimeCharacterPanelStatus(process, process.moduleBase, targetHash)
			if locateErr != nil {
				return nil, locateErr
			}
			if currentAddress != statusAddress {
				return nil, fmt.Errorf("角色状态对象已重建，请断开后重新连接采样器")
			}
			return captureFormulaStatusObject(captureContext, process, currentAddress, FormulaScanBudget{MaxBytes: formulaStatusObjectScanSize})
		})
	}
	a.formulaSamplerSession = session
	return session.status(), nil
}

func (a *App) FormulaSamplerObserveOwned(token string) (RuntimeCharacterPanelStats, error) {
	a.formulaSamplerMu.Lock()
	session := a.formulaSamplerSession
	if token == "" {
		a.formulaSamplerMu.Unlock()
		return RuntimeCharacterPanelStats{}, fmt.Errorf("公式采样器必须提供页面所有权令牌")
	}
	if session == nil {
		a.formulaSamplerMu.Unlock()
		return RuntimeCharacterPanelStats{}, fmt.Errorf("请先连接只读公式采样器")
	}
	if token != session.token {
		a.formulaSamplerMu.Unlock()
		return RuntimeCharacterPanelStats{}, fmt.Errorf("公式采样会话已被替换")
	}
	a.formulaSamplerMu.Unlock()
	return session.observe()
}

func (a *App) FormulaSamplerStartChangeRecordingOwned(token string) (FormulaChangeRecordingStatus, error) {
	a.formulaSamplerMu.Lock()
	session := a.formulaSamplerSession
	if token == "" || session == nil || token != session.token {
		a.formulaSamplerMu.Unlock()
		return FormulaChangeRecordingStatus{}, fmt.Errorf("公式变化记录需要当前页面的有效所有权令牌")
	}
	a.formulaSamplerMu.Unlock()
	return session.startChangeRecording()
}

func (a *App) FormulaSamplerStopChangeRecordingOwned(token string) (FormulaChangeAnalysis, error) {
	a.formulaSamplerMu.Lock()
	session := a.formulaSamplerSession
	if token == "" || session == nil || token != session.token {
		a.formulaSamplerMu.Unlock()
		return FormulaChangeAnalysis{}, fmt.Errorf("公式变化分析需要当前页面的有效所有权令牌")
	}
	a.formulaSamplerMu.Unlock()
	return session.stopChangeRecording()
}

func (a *App) FormulaSamplerStatus() FormulaSamplerStatus {
	a.formulaSamplerMu.Lock()
	session := a.formulaSamplerSession
	a.formulaSamplerMu.Unlock()
	if session == nil {
		return FormulaSamplerStatus{}
	}
	if err := session.validate(); err != nil {
		a.formulaSamplerMu.Lock()
		isCurrent := a.formulaSamplerSession == session
		if isCurrent {
			a.formulaSamplerSession = nil
		}
		a.formulaSamplerMu.Unlock()
		if isCurrent {
			_ = session.close()
		}
		return FormulaSamplerStatus{}
	}
	return session.status()
}

func (a *App) FormulaSamplerCaptureOwned(token string, phase FormulaSamplePhase) (FormulaSampleEvent, error) {
	a.formulaSamplerMu.Lock()
	if token == "" {
		a.formulaSamplerMu.Unlock()
		return FormulaSampleEvent{}, fmt.Errorf("公式采样器必须提供页面所有权令牌")
	}
	session := a.formulaSamplerSession
	if session == nil {
		a.formulaSamplerMu.Unlock()
		return FormulaSampleEvent{}, fmt.Errorf("请先连接只读公式采样器")
	}
	if token != session.token {
		a.formulaSamplerMu.Unlock()
		return FormulaSampleEvent{}, fmt.Errorf("公式采样会话已被替换")
	}
	a.formulaSamplerMu.Unlock()
	event, err := session.capture(phase)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return FormulaSampleEvent{}, err
		}
		if validateErr := session.validate(); validateErr != nil {
			a.formulaSamplerMu.Lock()
			isCurrent := a.formulaSamplerSession == session
			if isCurrent {
				a.formulaSamplerSession = nil
			}
			a.formulaSamplerMu.Unlock()
			if isCurrent {
				_ = session.close()
			}
			return FormulaSampleEvent{}, fmt.Errorf("只读公式采样连接已失效并关闭: %w", validateErr)
		}
	}
	return event, err
}

func (a *App) FormulaSamplerCloseOwned(token string) error {
	a.formulaSamplerMu.Lock()
	if token == "" {
		a.formulaSamplerMu.Unlock()
		return fmt.Errorf("公式采样器必须提供页面所有权令牌")
	}
	if a.formulaSamplerSession == nil {
		a.formulaSamplerMu.Unlock()
		return nil
	}
	if token != a.formulaSamplerSession.token {
		a.formulaSamplerMu.Unlock()
		return fmt.Errorf("公式采样会话已被替换")
	}
	session := a.formulaSamplerSession
	a.formulaSamplerSession = nil
	a.formulaSamplerMu.Unlock()
	return session.close()
}

func (a *App) closeFormulaSampler() error {
	a.formulaSamplerMu.Lock()
	session := a.formulaSamplerSession
	a.formulaSamplerSession = nil
	a.formulaSamplerMu.Unlock()
	return session.close()
}

func (a *App) FormulaSamplerExport(token string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	a.formulaSamplerMu.Lock()
	session := a.formulaSamplerSession
	if token == "" {
		a.formulaSamplerMu.Unlock()
		return "", fmt.Errorf("公式采样器必须提供页面所有权令牌")
	}
	if session != nil && token != session.token {
		a.formulaSamplerMu.Unlock()
		return "", fmt.Errorf("公式采样会话已被替换")
	}
	a.formulaSamplerMu.Unlock()
	if session == nil {
		return "", fmt.Errorf("没有可导出的公式采样会话")
	}
	bundle, err := session.bundle()
	if err != nil {
		return "", err
	}
	outputPath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出脱敏角色公式证据",
		DefaultFilename: "GBFR角色公式样本.gbfr-formula-sample.zip",
		Filters:         []runtime.FileFilter{{DisplayName: "GBFR 公式证据 (*.gbfr-formula-sample.zip)", Pattern: "*.gbfr-formula-sample.zip"}},
	})
	if err != nil || outputPath == "" {
		return outputPath, err
	}
	if filepath.Ext(outputPath) != ".zip" {
		outputPath += ".gbfr-formula-sample.zip"
	}
	if err := os.WriteFile(outputPath, bundle, 0600); err != nil {
		return "", err
	}
	return outputPath, nil
}
