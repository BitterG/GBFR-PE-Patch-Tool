package main

import "gbfrPlayerInfoEdit/internal/backend"

// OfflineLoadoutService exposes the isolated offline save editor without
// touching the main App's game-process, flight, or position state.
type OfflineLoadoutService struct {
	service *backend.App
}

type LoadoutWrite = backend.LoadoutWrite
type LoadoutApplyRequest = backend.LoadoutApplyRequest
type LoadoutApplyResult = backend.LoadoutApplyResult
type LoadoutEditContext = backend.LoadoutEditContext
type CharacterLoadouts = backend.CharacterLoadouts
type LoadoutComplianceReport = backend.LoadoutComplianceReport

func NewOfflineLoadoutService() *OfflineLoadoutService {
	return &OfflineLoadoutService{service: backend.NewApp()}
}

func (s *OfflineLoadoutService) LoadoutList(path string) ([]CharacterLoadouts, error) {
	return s.service.LoadoutList(path)
}

func (s *OfflineLoadoutService) LoadoutExportJSON(path string, unitID uint32) (string, error) {
	return s.service.LoadoutExportJSON(path, unitID)
}

func (s *OfflineLoadoutService) LoadoutImportJSON(path, characterHash, payload string) (*backend.LoadoutImportDraft, error) {
	return s.service.LoadoutImportJSON(path, characterHash, payload)
}

func (s *OfflineLoadoutService) LoadoutDetail(path string, unitID uint32) (*backend.LoadoutShare, error) {
	return s.service.LoadoutDetail(path, unitID)
}

func (s *OfflineLoadoutService) LoadoutEditContext(path, charaHash string) (*LoadoutEditContext, error) {
	return s.service.LoadoutEditContext(path, charaHash)
}

func (s *OfflineLoadoutService) LoadoutCheckCompliance(path string, write LoadoutWrite) (*LoadoutComplianceReport, error) {
	return s.service.LoadoutCheckCompliance(path, write)
}

func (s *OfflineLoadoutService) LoadoutApply(inputPath, outputPath string, changes []LoadoutWrite) (*LoadoutApplyResult, error) {
	return s.service.LoadoutApply(inputPath, outputPath, changes)
}

func (s *OfflineLoadoutService) LoadoutApplyWithResources(inputPath, outputPath string, request LoadoutApplyRequest) (*LoadoutApplyResult, error) {
	return s.service.LoadoutApplyWithResources(inputPath, outputPath, request)
}
