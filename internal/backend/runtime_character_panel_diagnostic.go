package backend

import "fmt"

const (
	runtimeCharacterPanelLayoutSchemaVersion = 1
	runtimeCharacterPanelLayoutID            = "relink-2.0.2-runtime-character-panel-v3"
	runtimeCharacterPanelGameEXESHA256       = "63340832BCF731FBC97796F686B05C988418E83D451D4A49B2244A85D00E297F"
)

type RuntimeCharacterPanelGuardDescriptor struct {
	RVA   string `json:"rva"`
	Bytes string `json:"bytes"`
}

type RuntimeCharacterPanelFieldDescriptor struct {
	Name            string  `json:"name"`
	RawType         string  `json:"rawType"`
	RelativeOffset  string  `json:"relativeOffset"`
	DisplayScale    float32 `json:"displayScale"`
	SampleRoleCount int     `json:"sampleRoleCount"`
	EvidenceLevel   string  `json:"evidenceLevel"`
}

type RuntimeCharacterPanelLayoutDescriptor struct {
	SchemaVersion        int                                    `json:"schemaVersion"`
	LayoutID             string                                 `json:"layoutId"`
	GameVersion          string                                 `json:"gameVersion"`
	GameExecutableSHA256 string                                 `json:"gameExecutableSha256"`
	Access               string                                 `json:"access"`
	ManagerRVA           string                                 `json:"managerRva"`
	AccessChain          []string                               `json:"accessChain"`
	Guards               []RuntimeCharacterPanelGuardDescriptor `json:"guards"`
	Fields               []RuntimeCharacterPanelFieldDescriptor `json:"fields"`
	EvidenceLevel        string                                 `json:"evidenceLevel"`
}

type RuntimeCharacterPanelObjectDiagnostic struct {
	DirectoryName       string                      `json:"directoryName"`
	DirectoryHash       string                      `json:"directoryHash"`
	RuntimeID           string                      `json:"runtimeId"`
	MapKey              string                      `json:"mapKey"`
	CandidateObjectHash string                      `json:"candidateObjectHash"`
	InIDVector          bool                        `json:"inIdVector"`
	Ready               byte                        `json:"ready"`
	Eligibility         byte                        `json:"eligibility"`
	Panel               *RuntimeCharacterPanelStats `json:"panel,omitempty"`
	EvidenceLevel       string                      `json:"evidenceLevel"`
	NegativeObservation string                      `json:"negativeObservation,omitempty"`
}

type RuntimeCharacterPanelRuntimeCatalog struct {
	SchemaVersion        int                                     `json:"schemaVersion"`
	Layout               RuntimeCharacterPanelLayoutDescriptor   `json:"layout"`
	VectorIDs            []string                                `json:"vectorIds"`
	Objects              []RuntimeCharacterPanelObjectDiagnostic `json:"objects"`
	SelectionObservation string                                  `json:"selectionObservation"`
}

func runtimeCharacterPanelLayoutDescriptor() RuntimeCharacterPanelLayoutDescriptor {
	guards := make([]RuntimeCharacterPanelGuardDescriptor, len(runtimeCharacterPanelVersionGuards))
	for index, guard := range runtimeCharacterPanelVersionGuards {
		guards[index] = RuntimeCharacterPanelGuardDescriptor{RVA: fmt.Sprintf("0x%X", guard.RVA), Bytes: fmt.Sprintf("%X", guard.Bytes)}
	}
	return RuntimeCharacterPanelLayoutDescriptor{
		SchemaVersion:        runtimeCharacterPanelLayoutSchemaVersion,
		LayoutID:             runtimeCharacterPanelLayoutID,
		GameVersion:          "2.0.2",
		GameExecutableSHA256: runtimeCharacterPanelGameEXESHA256,
		Access:               "PROCESS_QUERY_INFORMATION | PROCESS_VM_READ",
		ManagerRVA:           fmt.Sprintf("0x%X", runtimeCharacterPanelManagerRVA),
		AccessChain: []string{
			"module + managerRva -> manager",
			"manager + 0x08/0x10 -> directory/runtime ID vector",
			"manager + 0xA40/0xA58 -> status map bucket table/mask",
			"bucket node + 0x10/0x30 -> map key/status object",
		},
		Guards: guards,
		Fields: []RuntimeCharacterPanelFieldDescriptor{
			{Name: "hp", RawType: "i32", RelativeOffset: "0x04", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "attack", RawType: "i32", RelativeOffset: "0x08", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "stunPower", RawType: "f32", RelativeOffset: "0x10", DisplayScale: runtimeCharacterPanelStunDisplayScale, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads_and_screen"},
			{Name: "critRate", RawType: "f32", RelativeOffset: "0x14", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "permanentAttack", RawType: "f32", RelativeOffset: "0x58F8", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "permanentHP", RawType: "f32", RelativeOffset: "0x58FC", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "permanentCritRate", RawType: "f32", RelativeOffset: "0x5900", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "permanentStun", RawType: "f32", RelativeOffset: "0x5904", DisplayScale: runtimeCharacterPanelStunDisplayScale, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads_and_panel_scale"},
			{Name: "level", RawType: "i32", RelativeOffset: "0x5B44", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "baseHP", RawType: "i32", RelativeOffset: "0x5B48", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "baseAttack", RawType: "i32", RelativeOffset: "0x5B4C", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "baseStun", RawType: "f32", RelativeOffset: "0x5B54", DisplayScale: runtimeCharacterPanelStunDisplayScale, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads_and_panel_scale"},
			{Name: "baseCritRate", RawType: "i32", RelativeOffset: "0x5B58", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "masterHP", RawType: "i32", RelativeOffset: "0x5B64", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "masterAttack", RawType: "i32", RelativeOffset: "0x5B68", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads"},
			{Name: "fateHP", RawType: "i32", RelativeOffset: "0x5B70", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads_and_final_panel_match"},
			{Name: "fateAttack", RawType: "i32", RelativeOffset: "0x5B74", DisplayScale: 1, SampleRoleCount: 2, EvidenceLevel: "verified_two_roles_three_reads_and_final_panel_match"},
		},
		EvidenceLevel: "verified_two_roles_three_reads",
	}
}

func readRuntimeCharacterPanelObjectStable(memory runtimeCharacterPanelMemory, object runtimeCharacterPanelObject, directoryHash uint32) (RuntimeCharacterPanelStats, error) {
	return readStableRuntimeCharacterPanelSnapshots(func() (RuntimeCharacterPanelStats, error) {
		stats, err := readRuntimeCharacterPanelValues(memory, object.Status, directoryHash)
		if err != nil {
			return RuntimeCharacterPanelStats{}, err
		}
		stats.RuntimeID = hashText(object.RuntimeID)
		stats.CandidateObjectHash = hashText(object.CandidateCharacterHash)
		if object.MapKey == directoryHash {
			stats.IdentitySource = "map_key"
		} else {
			stats.IdentitySource = "candidate_object_hash"
		}
		return stats, nil
	})
}

func enumerateRuntimeCharacterPanelDiagnostics(memory runtimeCharacterPanelMemory, moduleBase uintptr) (RuntimeCharacterPanelRuntimeCatalog, error) {
	enumeration, err := enumerateRuntimeCharacterPanelStatuses(memory, moduleBase)
	if err != nil {
		return RuntimeCharacterPanelRuntimeCatalog{}, err
	}
	result := RuntimeCharacterPanelRuntimeCatalog{
		SchemaVersion:        runtimeCharacterPanelLayoutSchemaVersion,
		Layout:               runtimeCharacterPanelLayoutDescriptor(),
		VectorIDs:            make([]string, len(enumeration.VectorIDs)),
		Objects:              make([]RuntimeCharacterPanelObjectDiagnostic, 0, len(enumeration.Objects)),
		SelectionObservation: "候选：manager/map 可稳定枚举角色对象；负观察：切换装备页角色时 manager 与全部 24 KiB 状态窗口未变化，当前屏幕角色仍需用户确认。",
	}
	for index, id := range enumeration.VectorIDs {
		result.VectorIDs[index] = hashText(id)
	}
	for _, object := range enumeration.Objects {
		directoryHash := object.MapKey
		name, knownDirectoryHash := characterNameByHash[directoryHash]
		if !knownDirectoryHash {
			if candidateName, ok := characterNameByHash[object.CandidateCharacterHash]; ok {
				directoryHash = object.CandidateCharacterHash
				name = candidateName
				knownDirectoryHash = true
			}
		}
		diagnostic := RuntimeCharacterPanelObjectDiagnostic{
			DirectoryName:       name,
			DirectoryHash:       hashText(directoryHash),
			RuntimeID:           hashText(object.RuntimeID),
			MapKey:              hashText(object.MapKey),
			CandidateObjectHash: hashText(object.CandidateCharacterHash),
			InIDVector:          object.InIDVector,
			Ready:               object.Ready,
			Eligibility:         object.Eligibility,
			EvidenceLevel:       "candidate_runtime_object",
		}
		if object.Status == 0 {
			diagnostic.NegativeObservation = "map 节点状态指针为空"
		} else if stats, readErr := readRuntimeCharacterPanelObjectStable(memory, object, directoryHash); readErr != nil {
			diagnostic.NegativeObservation = readErr.Error()
		} else {
			diagnostic.Panel = &stats
			if knownDirectoryHash && object.MapKey == directoryHash {
				diagnostic.EvidenceLevel = "verified_map_key_three_reads"
			}
		}
		if !knownDirectoryHash && diagnostic.NegativeObservation == "" {
			diagnostic.NegativeObservation = "运行时 ID 尚未映射到目录角色"
		}
		result.Objects = append(result.Objects, diagnostic)
	}
	return result, nil
}

func (a *App) FormulaSamplerRuntimeObjects() (RuntimeCharacterPanelRuntimeCatalog, error) {
	process, err := openReadOnlyGameProcess(windowsReadOnlyProcessBackend{}, charaProcessName, runtimeCharacterPanelVersionGuards)
	if err != nil {
		return RuntimeCharacterPanelRuntimeCatalog{}, err
	}
	defer process.Close()
	return enumerateRuntimeCharacterPanelDiagnostics(process, process.moduleBase)
}
