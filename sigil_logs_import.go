package main

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	_ "modernc.org/sqlite"
)

const maxSigilLoadoutEntries = 12

type LogsSigilLoadout struct {
	PlayerName    string              `json:"playerName"`
	CharacterName string              `json:"characterName"`
	CharacterType string              `json:"characterType"`
	Entries       []SigilLoadoutEntry `json:"entries"`
}

type LogsSigilLoadoutImport struct {
	LogTime  int64              `json:"logTime"`
	Loadouts []LogsSigilLoadout `json:"loadouts"`
}

type SigilLoadoutEntry struct {
	SigilHash           uint32 `json:"sigilHash"`
	SigilLevel          uint32 `json:"sigilLevel"`
	PrimaryTraitHash    uint32 `json:"primaryTraitHash"`
	PrimaryTraitLevel   uint32 `json:"primaryTraitLevel"`
	SecondaryTraitHash  uint32 `json:"secondaryTraitHash"`
	SecondaryTraitLevel uint32 `json:"secondaryTraitLevel"`
}

type logsEncounter struct {
	PlayerData [4]*logsPlayer `cbor:"playerData"`
}

type logsPlayer struct {
	DisplayName   string      `cbor:"displayName"`
	CharacterName string      `cbor:"characterName"`
	Sigils        []logsSigil `cbor:"sigils"`
}

type logsSigil struct {
	FirstTraitID     uint32 `cbor:"firstTraitId"`
	FirstTraitLevel  uint32 `cbor:"firstTraitLevel"`
	SecondTraitID    uint32 `cbor:"secondTraitId"`
	SecondTraitLevel uint32 `cbor:"secondTraitLevel"`
	SigilID          uint32 `cbor:"sigilId"`
	SigilLevel       uint32 `cbor:"sigilLevel"`
}

// SelectLogsSigilLoadouts reads every supported v1 GBFR Logs record.
func (a *App) SelectLogsSigilLoadouts() ([]LogsSigilLoadoutImport, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("Wails 上下文未初始化")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 GBFR Logs SQLite 文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "SQLite 数据库 (*.db, *.sqlite, *.sqlite3)", Pattern: "*.db;*.sqlite;*.sqlite3"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return nil, err
	}
	return readLogsSigilLoadouts(path)
}

func readLogsSigilLoadouts(path string) ([]LogsSigilLoadoutImport, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("打开日志数据库失败: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT time, data FROM logs WHERE version = 1 ORDER BY time DESC`)
	if err != nil {
		return nil, fmt.Errorf("读取 logs 表失败: %w", err)
	}
	defer rows.Close()
	imports := make([]LogsSigilLoadoutImport, 0)
	for rows.Next() {
		var logTime int64
		var blob []byte
		if err := rows.Scan(&logTime, &blob); err != nil {
			return nil, fmt.Errorf("读取日志记录失败: %w", err)
		}
		encounter, err := decodeLogsEncounter(blob)
		if err != nil {
			continue
		}
		loadouts := logsPlayerLoadouts(encounter)
		if len(loadouts) > 0 {
			imports = append(imports, LogsSigilLoadoutImport{LogTime: logTime, Loadouts: loadouts})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历日志记录失败: %w", err)
	}
	if len(imports) == 0 {
		return nil, fmt.Errorf("未找到包含玩家因子配装的 v1 日志")
	}
	return imports, nil
}

func decodeLogsEncounter(blob []byte) (logsEncounter, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return logsEncounter{}, err
	}
	decompressed, err := decoder.DecodeAll(blob, nil)
	decoder.Close()
	if err != nil {
		return logsEncounter{}, err
	}
	var encounter logsEncounter
	if err := cbor.Unmarshal(decompressed, &encounter); err != nil {
		return logsEncounter{}, err
	}
	return encounter, nil
}

func logsPlayerLoadouts(encounter logsEncounter) []LogsSigilLoadout {
	loadouts := make([]LogsSigilLoadout, 0, len(encounter.PlayerData))
	for _, player := range encounter.PlayerData {
		if player == nil || len(player.Sigils) == 0 {
			continue
		}
		entries := make([]SigilLoadoutEntry, 0, len(player.Sigils))
		for _, sigil := range player.Sigils {
			if sigil.SigilID == 0 {
				continue
			}
			entries = append(entries, SigilLoadoutEntry{
				SigilHash: sigil.SigilID, SigilLevel: sigil.SigilLevel,
				PrimaryTraitHash: sigil.FirstTraitID, PrimaryTraitLevel: sigil.FirstTraitLevel,
				SecondaryTraitHash: sigil.SecondTraitID, SecondaryTraitLevel: sigil.SecondTraitLevel,
			})
		}
		if len(entries) == 0 || len(entries) > maxSigilLoadoutEntries {
			continue
		}
		loadouts = append(loadouts, LogsSigilLoadout{
			PlayerName: player.DisplayName, CharacterName: player.CharacterName, Entries: entries,
		})
	}
	return loadouts
}
