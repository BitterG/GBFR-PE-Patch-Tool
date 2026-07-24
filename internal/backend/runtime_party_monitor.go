package backend

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

// The party monitor resolves the live slot table through this RIP-relative
// instruction. The full signature and the resolved RVA are
// independently checked against the supplied 2.0.2 executable by the opt-in
// local-EXE truth test.
const (
	runtimePatchPartyPointerAOB      = "488Bxxxxxxxxxx4885xx74xx488BxxFFxxxxxxxxxx4885xx74xx488Bxxxx488Bxxxx488Bxx488DxxxxxxFFxxxxxxxxxxEBxxC5xxxxxxxxxxxxxxC5xxxxxxxxxx488Bxx488D"
	runtimePatchPartyPointerRVA      = uintptr(0x22CECA0)
	runtimePatchPartySlotTableRVA    = uintptr(0x7036860)
	runtimePatchPartySignatureLength = 69

	runtimePatchPartyHPOffset            = uintptr(0x160)
	runtimePatchPartyMaxHPOffset         = uintptr(0x168)
	runtimePatchPartyDodgeOffset         = uintptr(0x5788)
	runtimePatchPartySBAOffset           = uintptr(0x32AC)
	runtimePatchPartyMaxSBAOffset        = uintptr(0x32B0)
	runtimePatchPartyTransformRootOffset = uintptr(0x28)
	runtimePatchPartyTransformNodeOffset = uintptr(0x08)
	runtimePatchPartyPositionXOffset     = uintptr(0xD8)
	runtimePatchPartyPositionYOffset     = uintptr(0xD4)
	runtimePatchPartyPositionZOffset     = uintptr(0xD0)

	runtimePatchPartyCompanionSlotOffset    = uintptr(0x38)
	runtimePatchPartyCompanionEntityOffset  = uintptr(0x70)
	runtimePatchPartyCompanionDirectXOffset = uintptr(0x1588)
	runtimePatchPartyCompanionDirectYOffset = uintptr(0x1584)
	runtimePatchPartyCompanionDirectZOffset = uintptr(0x1580)

	runtimePatchPartyMaximumPlausibleHP         = uint64(1_000_000_000)
	runtimePatchPartyMaximumPlausibleSBA        = float32(1_000_000)
	runtimePatchPartyMaximumCoordinateMagnitude = float32(10_000_000)
	runtimePatchPartySnapshotCount              = 3
)

type RuntimePatchVector3 struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

type RuntimePatchPartyCapabilities struct {
	Dodge          bool `json:"dodge"`
	SBA            bool `json:"sba"`
	DirectPosition bool `json:"directPosition"`
}

// RuntimePatchPartyEntity mirrors only fields proven by the bounded read-only
// pointer path. Optional pointers distinguish an unavailable capability from
// a real in-game zero.
type RuntimePatchPartyEntity struct {
	Role           string                        `json:"role"`
	Present        bool                          `json:"present"`
	DisplayName    string                        `json:"displayName"`
	Address        uint64                        `json:"address"`
	HP             uint64                        `json:"hp"`
	MaxHP          uint64                        `json:"maxHp"`
	DodgeCount     *uint32                       `json:"dodgeCount,omitempty"`
	SBA            *float32                      `json:"sba,omitempty"`
	MaxSBA         *float32                      `json:"maxSba,omitempty"`
	Position       RuntimePatchVector3           `json:"position"`
	DirectPosition *RuntimePatchVector3          `json:"directPosition,omitempty"`
	Capabilities   RuntimePatchPartyCapabilities `json:"capabilities"`
}

type RuntimePatchPartyMonitor struct {
	OwnerToken      string                    `json:"ownerToken,omitempty"`
	PID             uint32                    `json:"pid"`
	ProcessCreated  uint64                    `json:"processCreated"`
	RootAddress     uint64                    `json:"rootAddress"`
	Entities        []RuntimePatchPartyEntity `json:"entities"`
	Source          string                    `json:"source"`
	Verification    string                    `json:"verification"`
	GameVersion     string                    `json:"gameVersion"`
	SnapshotCount   int                       `json:"snapshotCount"`
	RuntimeVerified bool                      `json:"runtimeVerified"`
}

type runtimePatchPartyTopology struct {
	Root               uintptr
	Entities           [5]uintptr
	TransformNodes     [5][2]uintptr
	CompanionContainer uintptr
}

type runtimePatchPartySnapshot struct {
	Topology runtimePatchPartyTopology
	Result   RuntimePatchPartyMonitor
}

type runtimePatchPartyMemory interface {
	ReadAt(address uintptr, destination []byte) error
}

type remoteRuntimePatchPartyMemory struct {
	app *App
}

func (memory remoteRuntimePatchPartyMemory) ReadAt(address uintptr, destination []byte) error {
	if memory.app == nil || memory.app.hProcess == 0 {
		return fmt.Errorf("game process handle is empty")
	}
	if len(destination) == 0 {
		return nil
	}
	return readProcessMemory(memory.app.hProcess, address, unsafe.Pointer(&destination[0]), uintptr(len(destination)))
}

// RuntimePatchPartyMonitorOwned performs no writes and installs no hook. The Chara
// owner lease pins hProcess/moduleBase/{PID, Created} for all three snapshots,
// so a reused PID or concurrent page cleanup cannot splice two processes into
// one result.
func (a *App) RuntimePatchPartyMonitorOwned(token string) (RuntimePatchPartyMonitor, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return RuntimePatchPartyMonitor{}, err
	}
	defer a.procMu.Unlock()

	memory := remoteRuntimePatchPartyMemory{app: a}
	result, err := readStableRuntimePatchPartySnapshots(func() (runtimePatchPartySnapshot, error) {
		return readRuntimePatchPartySnapshot(memory, a.moduleBase)
	})
	if err != nil {
		return RuntimePatchPartyMonitor{}, err
	}
	result.OwnerToken = token
	result.PID = a.charaPID
	result.ProcessCreated = a.charaCreated
	return result, nil
}

func readStableRuntimePatchPartySnapshots(readSnapshot func() (runtimePatchPartySnapshot, error)) (RuntimePatchPartyMonitor, error) {
	if readSnapshot == nil {
		return RuntimePatchPartyMonitor{}, fmt.Errorf("%s", runtimePatchMonitorText("队伍快照读取器为空", "Party snapshot reader is nil"))
	}
	var frames [runtimePatchPartySnapshotCount]runtimePatchPartySnapshot
	for index := range frames {
		frame, err := readSnapshot()
		if err != nil {
			return RuntimePatchPartyMonitor{}, fmt.Errorf("%s: %w", runtimePatchMonitorText(
				fmt.Sprintf("第 %d 次队伍快照读取失败", index+1),
				fmt.Sprintf("party snapshot %d failed", index+1),
			), err)
		}
		frames[index] = frame
	}
	if frames[0].Topology != frames[1].Topology || frames[0].Topology != frames[2].Topology {
		return RuntimePatchPartyMonitor{}, fmt.Errorf("%s", runtimePatchMonitorText(
			"队伍根指针、实体或坐标链拓扑在连续三次快照间发生变化，请等待场景稳定后重试",
			"Party root, entity, or coordinate-chain topology changed across three snapshots; wait for a stable scene and retry",
		))
	}
	result := frames[len(frames)-1].Result
	result.RootAddress = uint64(frames[len(frames)-1].Topology.Root)
	result.Source = "game_runtime_patch_2.0.2"
	result.Verification = runtimePatchMonitorText("连续三快照拓扑验证", "three-snapshot topology verification")
	result.GameVersion = "2.0.2"
	result.SnapshotCount = runtimePatchPartySnapshotCount
	result.RuntimeVerified = true
	return result, nil
}

func readRuntimePatchPartySnapshot(memory runtimePatchPartyMemory, moduleBase uintptr) (runtimePatchPartySnapshot, error) {
	if memory == nil || moduleBase == 0 {
		return runtimePatchPartySnapshot{}, fmt.Errorf("%s", runtimePatchMonitorText("队伍监测读取参数无效", "Invalid party monitor read parameters"))
	}
	root, err := verifyRuntimePatchPartyPointerSignature(memory, moduleBase)
	if err != nil {
		return runtimePatchPartySnapshot{}, err
	}

	var snapshot runtimePatchPartySnapshot
	snapshot.Topology.Root = root
	snapshot.Result.Entities = make([]RuntimePatchPartyEntity, 0, 5)
	roles := [...]string{"player", "party1", "party2", "party3"}
	for index, role := range roles {
		entity, readErr := readRuntimePatchPointer(memory, root+uintptr(index)*8)
		if readErr != nil {
			return runtimePatchPartySnapshot{}, fmt.Errorf("%s: %w", runtimePatchPartyRoleName(role), normalizeRuntimePatchPartyReadError(readErr))
		}
		if entity == 0 {
			if index == 0 {
				return runtimePatchPartySnapshot{}, fmt.Errorf("%s: %w", runtimePatchPartyRoleName(role), normalizeRuntimePatchPartyReadError(nil))
			}
			snapshot.Result.Entities = append(snapshot.Result.Entities, emptyRuntimePatchPartyEntity(role))
			continue
		}
		result, nodes, readErr := readRuntimePatchPartyEntity(memory, entity, role, true, false)
		if readErr != nil {
			return runtimePatchPartySnapshot{}, readErr
		}
		snapshot.Topology.Entities[index] = entity
		snapshot.Topology.TransformNodes[index] = nodes
		snapshot.Result.Entities = append(snapshot.Result.Entities, result)
	}

	container, err := readRuntimePatchPointer(memory, root+runtimePatchPartyCompanionSlotOffset)
	if err != nil {
		return runtimePatchPartySnapshot{}, fmt.Errorf("%s: %w", runtimePatchPartyRoleName("companion"), normalizeRuntimePatchPartyReadError(err))
	}
	if container == 0 {
		snapshot.Result.Entities = append(snapshot.Result.Entities, emptyRuntimePatchPartyEntity("companion"))
		return snapshot, nil
	}
	snapshot.Topology.CompanionContainer = container
	companion, err := readRuntimePatchPointer(memory, container+runtimePatchPartyCompanionEntityOffset)
	if err != nil {
		return runtimePatchPartySnapshot{}, fmt.Errorf("%s: %w", runtimePatchPartyRoleName("companion"), normalizeRuntimePatchPartyReadError(err))
	}
	if companion == 0 {
		snapshot.Result.Entities = append(snapshot.Result.Entities, emptyRuntimePatchPartyEntity("companion"))
		return snapshot, nil
	}
	companionResult, companionNodes, err := readRuntimePatchPartyEntity(memory, companion, "companion", false, true)
	if err != nil {
		return runtimePatchPartySnapshot{}, err
	}
	snapshot.Topology.Entities[4] = companion
	snapshot.Topology.TransformNodes[4] = companionNodes
	snapshot.Result.Entities = append(snapshot.Result.Entities, companionResult)
	return snapshot, nil
}

func emptyRuntimePatchPartyEntity(role string) RuntimePatchPartyEntity {
	return RuntimePatchPartyEntity{
		Role:         role,
		DisplayName:  runtimePatchPartyRoleName(role),
		Position:     RuntimePatchVector3{},
		Capabilities: RuntimePatchPartyCapabilities{},
	}
}

func verifyRuntimePatchPartyPointerSignature(memory runtimePatchPartyMemory, moduleBase uintptr) (uintptr, error) {
	pattern, err := parseRuntimePatchPattern(runtimePatchPartyPointerAOB)
	if err != nil {
		return 0, err
	}
	if len(pattern.Values) != runtimePatchPartySignatureLength {
		return 0, fmt.Errorf("party pointer signature length=%d, want %d", len(pattern.Values), runtimePatchPartySignatureLength)
	}
	site, ok := checkedRuntimePatchMonitorAddress(moduleBase, runtimePatchPartyPointerRVA)
	if !ok {
		return 0, fmt.Errorf("party pointer signature address overflow")
	}
	actual := make([]byte, len(pattern.Values))
	if err := memory.ReadAt(site, actual); err != nil {
		return 0, fmt.Errorf("%s: %w", runtimePatchMonitorText("读取队伍指针签名失败", "Read party pointer signature"), err)
	}
	if !matchRuntimePatchPattern(actual, pattern) {
		return 0, fmt.Errorf("%s: RVA 0x%X", runtimePatchMonitorText("队伍指针签名与游戏 2.0.2 不匹配", "Party pointer signature does not match game 2.0.2"), runtimePatchPartyPointerRVA)
	}
	displacement := int64(int32(binary.LittleEndian.Uint32(actual[3:7])))
	resolvedSigned := int64(site) + 7 + displacement
	if resolvedSigned <= 0 || uint64(resolvedSigned) > uint64(^uintptr(0)) {
		return 0, fmt.Errorf("party pointer RIP target overflow")
	}
	resolved := uintptr(resolvedSigned)
	expected, ok := checkedRuntimePatchMonitorAddress(moduleBase, runtimePatchPartySlotTableRVA)
	if !ok || resolved != expected {
		return 0, fmt.Errorf("%s: got RVA 0x%X, want 0x%X", runtimePatchMonitorText("队伍根槽解析结果不匹配", "Party root slot resolution mismatch"), resolved-moduleBase, runtimePatchPartySlotTableRVA)
	}
	return resolved, nil
}

func readRuntimePatchPartyEntity(memory runtimePatchPartyMemory, address uintptr, role string, supportsCombat, supportsDirectPosition bool) (RuntimePatchPartyEntity, [2]uintptr, error) {
	var nodes [2]uintptr
	hp, err := readRuntimePatchU64At(memory, address+runtimePatchPartyHPOffset)
	if err != nil {
		return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s HP: %w", runtimePatchPartyRoleName(role), err)
	}
	maxHP, err := readRuntimePatchU64At(memory, address+runtimePatchPartyMaxHPOffset)
	if err != nil {
		return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s max HP: %w", runtimePatchPartyRoleName(role), err)
	}
	nodes[0], err = readRuntimePatchPointer(memory, address+runtimePatchPartyTransformRootOffset)
	if err != nil || nodes[0] == 0 {
		return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s transform root: %w", runtimePatchPartyRoleName(role), normalizeRuntimePatchPartyReadError(err))
	}
	nodes[1], err = readRuntimePatchPointer(memory, nodes[0]+runtimePatchPartyTransformNodeOffset)
	if err != nil || nodes[1] == 0 {
		return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s transform node: %w", runtimePatchPartyRoleName(role), normalizeRuntimePatchPartyReadError(err))
	}
	position, err := readRuntimePatchVector3(memory, nodes[1], runtimePatchPartyPositionXOffset, runtimePatchPartyPositionYOffset, runtimePatchPartyPositionZOffset)
	if err != nil {
		return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s position: %w", runtimePatchPartyRoleName(role), err)
	}
	result := RuntimePatchPartyEntity{
		Role:         role,
		Present:      true,
		DisplayName:  runtimePatchPartyRoleName(role),
		Address:      uint64(address),
		HP:           hp,
		MaxHP:        maxHP,
		Position:     position,
		Capabilities: RuntimePatchPartyCapabilities{Dodge: supportsCombat, SBA: supportsCombat, DirectPosition: supportsDirectPosition},
	}
	if supportsCombat {
		dodge, readErr := readRuntimePatchU32At(memory, address+runtimePatchPartyDodgeOffset)
		if readErr != nil {
			return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s dodge count: %w", runtimePatchPartyRoleName(role), readErr)
		}
		sba, readErr := readRuntimePatchF32At(memory, address+runtimePatchPartySBAOffset)
		if readErr != nil {
			return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s SBA: %w", runtimePatchPartyRoleName(role), readErr)
		}
		maxSBA, readErr := readRuntimePatchF32At(memory, address+runtimePatchPartyMaxSBAOffset)
		if readErr != nil {
			return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s max SBA: %w", runtimePatchPartyRoleName(role), readErr)
		}
		result.DodgeCount = &dodge
		result.SBA = &sba
		result.MaxSBA = &maxSBA
	}
	if supportsDirectPosition {
		direct, readErr := readRuntimePatchVector3(memory, address, runtimePatchPartyCompanionDirectXOffset, runtimePatchPartyCompanionDirectYOffset, runtimePatchPartyCompanionDirectZOffset)
		if readErr != nil {
			return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s direct position: %w", runtimePatchPartyRoleName(role), readErr)
		}
		result.DirectPosition = &direct
	}
	if err := validateRuntimePatchPartyEntity(result); err != nil {
		return RuntimePatchPartyEntity{}, nodes, fmt.Errorf("%s: %w", runtimePatchPartyRoleName(role), err)
	}
	return result, nodes, nil
}

func validateRuntimePatchPartyEntity(entity RuntimePatchPartyEntity) error {
	if !entity.Present {
		if entity.Address != 0 || entity.HP != 0 || entity.MaxHP != 0 || entity.DodgeCount != nil || entity.SBA != nil || entity.MaxSBA != nil || entity.DirectPosition != nil || entity.Capabilities != (RuntimePatchPartyCapabilities{}) || entity.Position != (RuntimePatchVector3{}) {
			return fmt.Errorf("absent party slot contains runtime entity data")
		}
		return nil
	}
	if entity.MaxHP == 0 || entity.MaxHP > runtimePatchPartyMaximumPlausibleHP || entity.HP > entity.MaxHP {
		return fmt.Errorf("HP is outside [0,max] or max HP is implausible: %d/%d", entity.HP, entity.MaxHP)
	}
	if entity.Capabilities.Dodge != (entity.DodgeCount != nil) {
		return fmt.Errorf("dodge capability and value availability disagree")
	}
	if entity.Capabilities.SBA != (entity.SBA != nil && entity.MaxSBA != nil) || (entity.SBA == nil) != (entity.MaxSBA == nil) {
		return fmt.Errorf("SBA capability and value availability disagree")
	}
	if entity.Capabilities.DirectPosition != (entity.DirectPosition != nil) {
		return fmt.Errorf("direct-position capability and value availability disagree")
	}
	if entity.SBA != nil {
		current, maximum := *entity.SBA, *entity.MaxSBA
		if !finiteRuntimePatchFloat(current) || !finiteRuntimePatchFloat(maximum) || maximum <= 0 || maximum > runtimePatchPartyMaximumPlausibleSBA || current < 0 || current > maximum {
			return fmt.Errorf("SBA is invalid: %v/%v", current, maximum)
		}
	}
	if err := validateRuntimePatchVector3(entity.Position); err != nil {
		return err
	}
	if entity.DirectPosition != nil {
		if err := validateRuntimePatchVector3(*entity.DirectPosition); err != nil {
			return fmt.Errorf("direct position: %w", err)
		}
	}
	return nil
}

func validateRuntimePatchVector3(value RuntimePatchVector3) error {
	for _, coordinate := range []float32{value.X, value.Y, value.Z} {
		if !finiteRuntimePatchFloat(coordinate) || float32(math.Abs(float64(coordinate))) > runtimePatchPartyMaximumCoordinateMagnitude {
			return fmt.Errorf("coordinate is non-finite or outside world bounds: %v", coordinate)
		}
	}
	return nil
}

func finiteRuntimePatchFloat(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func readRuntimePatchVector3(memory runtimePatchPartyMemory, base, xOffset, yOffset, zOffset uintptr) (RuntimePatchVector3, error) {
	x, err := readRuntimePatchF32At(memory, base+xOffset)
	if err != nil {
		return RuntimePatchVector3{}, err
	}
	y, err := readRuntimePatchF32At(memory, base+yOffset)
	if err != nil {
		return RuntimePatchVector3{}, err
	}
	z, err := readRuntimePatchF32At(memory, base+zOffset)
	if err != nil {
		return RuntimePatchVector3{}, err
	}
	return RuntimePatchVector3{X: x, Y: y, Z: z}, nil
}

func readRuntimePatchPointer(memory runtimePatchPartyMemory, address uintptr) (uintptr, error) {
	value, err := readRuntimePatchU64At(memory, address)
	if err != nil {
		return 0, err
	}
	if uint64(uintptr(value)) != value {
		return 0, fmt.Errorf("pointer 0x%X is outside the local address width", value)
	}
	return uintptr(value), nil
}

func readRuntimePatchU64At(memory runtimePatchPartyMemory, address uintptr) (uint64, error) {
	encoded := make([]byte, 8)
	if err := memory.ReadAt(address, encoded); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(encoded), nil
}

func readRuntimePatchU32At(memory runtimePatchPartyMemory, address uintptr) (uint32, error) {
	encoded := make([]byte, 4)
	if err := memory.ReadAt(address, encoded); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(encoded), nil
}

func readRuntimePatchF32At(memory runtimePatchPartyMemory, address uintptr) (float32, error) {
	value, err := readRuntimePatchU32At(memory, address)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(value), nil
}

func checkedRuntimePatchMonitorAddress(base, offset uintptr) (uintptr, bool) {
	if ^uintptr(0)-base < offset {
		return 0, false
	}
	return base + offset, true
}

func normalizeRuntimePatchPartyReadError(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("pointer is null")
}

func runtimePatchPartyRoleName(role string) string {
	if useChinese() {
		switch role {
		case "player":
			return "玩家"
		case "party1":
			return "队伍成员 1"
		case "party2":
			return "队伍成员 2"
		case "party3":
			return "队伍成员 3"
		case "companion":
			return "碧的小红龙"
		}
	}
	switch role {
	case "player":
		return "Player"
	case "party1":
		return "Party Member 1"
	case "party2":
		return "Party Member 2"
	case "party3":
		return "Party Member 3"
	case "companion":
		return "Vyrn"
	default:
		return role
	}
}

func runtimePatchMonitorText(chinese, english string) string {
	if useChinese() {
		return chinese
	}
	return english
}
