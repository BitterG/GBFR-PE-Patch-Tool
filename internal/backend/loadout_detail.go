package backend

import "fmt"

// LoadoutDetail returns the complete portable snapshot for one saved loadout
// slot. It is read-only and contains all fields needed by the offline editor.
func (a *App) LoadoutDetail(savePath string, unitID uint32) (*LoadoutShare, error) {
	if savePath == "" {
		return nil, fmt.Errorf("存档路径不能为空")
	}
	return buildLoadoutShare(savePath, unitID)
}

// LoadoutExportJSON serializes one saved loadout without opening a file dialog.
func (a *App) LoadoutExportJSON(savePath string, unitID uint32) (string, error) {
	share, err := a.LoadoutDetail(savePath, unitID)
	if err != nil {
		return "", err
	}
	payload, err := marshalLoadoutShare(share)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// LoadoutImportJSON resolves a portable loadout against a target character
// without opening a file dialog and without writing the target save.
func (a *App) LoadoutImportJSON(savePath, expectCharaHash, payload string) (*LoadoutImportDraft, error) {
	if len(payload) == 0 || len(payload) > loadoutShareMaxSize {
		return nil, fmt.Errorf("配装 JSON 大小无效")
	}
	share, err := unmarshalLoadoutShare([]byte(payload))
	if err != nil {
		return nil, fmt.Errorf("配装 JSON 无效: %w", err)
	}
	return resolveLoadoutShare(savePath, expectCharaHash, share)
}
