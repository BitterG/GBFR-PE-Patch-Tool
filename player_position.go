package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

const (
	playerPositionSignatureSize   = 69
	playerPositionTransformRoot   = uintptr(0x28)
	playerPositionTransformNode   = uintptr(0x08)
	playerPositionXOffset         = uintptr(0xD8)
	playerPositionYOffset         = uintptr(0xD4)
	playerPositionZOffset         = uintptr(0xD0)
	playerPositionMaximumAbsValue = float32(10_000_000)
)

type playerPositionLayout struct {
	version        string
	signatureRVA   uintptr
	slotTableRVA   uintptr
	gravityRVA     uintptr
	signature      []byte
	signatureMask  []bool
}

// 2.0.2/2.0.3 共享的玩家坐标获取签名（mov rax,[rip+disp] 开头，引用 slotTable）
var playerPositionSignatureLegacy = []byte{
	0x48, 0x8B, 0, 0, 0, 0, 0, 0x48, 0x85, 0, 0x74, 0, 0x48, 0x8B, 0, 0xFF,
	0, 0, 0, 0, 0, 0x48, 0x85, 0, 0x74, 0, 0x48, 0x8B, 0, 0, 0x48, 0x8B,
	0, 0, 0x48, 0x8B, 0, 0x48, 0x8D, 0, 0, 0, 0xFF, 0, 0, 0, 0, 0, 0xEB,
	0, 0xC5, 0, 0, 0, 0, 0, 0, 0, 0xC5, 0, 0, 0, 0, 0, 0x48, 0x8B, 0,
	0x48, 0x8D,
}

var playerPositionSignatureMaskLegacy = []bool{
	true, true, false, false, false, false, false, true, true, false, true, false, true, true, false, true,
	false, false, false, false, false, true, true, false, true, false, true, true, false, false, true, true,
	false, false, true, true, false, true, true, false, false, false, true, false, false, false, false, false,
	true, false, true, false, false, false, false, false, false, false, true, false, false, false, false, false,
	true, true, false, true, true,
}

// 2.0.4 玩家坐标获取签名（mov rcx,[rip+disp] 开头，引用 slotTable；0xC2541B 起 69 字节）
var playerPositionSignature204 = []byte{
	0x48, 0x8B, 0x0D, 0, 0, 0, 0, 0x48, 0x85, 0xC9, 0x74, 0x71, 0x48, 0x8B, 0x01, 0xFF,
	0x90, 0xC0, 0x04, 0x00, 0x00, 0x48, 0x85, 0xC0, 0x74, 0x63, 0x8B, 0x3D, 0, 0, 0, 0,
	0x8D, 0x87, 0x00, 0xF3, 0xFF, 0xFF, 0x83, 0xF8, 0x04, 0x77, 0x42, 0x83, 0xF8, 0x03, 0x74,
	0x3D, 0x48, 0x8B, 0x0D, 0, 0, 0, 0, 0xC5, 0xF8, 0x28, 0xFE, 0x48, 0x85, 0xC9, 0x74,
	0x1A, 0x48, 0x8B, 0x01, 0xFF, 0x90,
}

var playerPositionSignatureMask204 = []bool{
	true, true, true, false, false, false, false, true, true, true, true, true, true, true, true, true,
	true, true, true, true, true, true, true, true, true, true, true, true, false, false, false, false,
	true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true,
	true, true, true, false, false, false, false, true, true, true, true, true, true, true, true,
	true, true, true, true, true, true,
}

var playerPositionLayouts = [...]playerPositionLayout{
	{version: "2.0.2", signatureRVA: 0x22CECA0, slotTableRVA: 0x7036860, gravityRVA: 0x39DD964, signature: playerPositionSignatureLegacy, signatureMask: playerPositionSignatureMaskLegacy},
	{version: "2.0.3", signatureRVA: 0x22C9310, slotTableRVA: 0x7033820, gravityRVA: 0x39D8E24, signature: playerPositionSignatureLegacy, signatureMask: playerPositionSignatureMaskLegacy},
	{version: "2.0.4", signatureRVA: 0xC2541B, slotTableRVA: 0x7034AA0, gravityRVA: 0x39D9DC4, signature: playerPositionSignature204, signatureMask: playerPositionSignatureMask204},
}

type PlayerPosition struct {
	X       float32 `json:"x"`
	Y       float32 `json:"y"`
	Z       float32 `json:"z"`
	Address uint64  `json:"address"`
}

// PlayerPositionGet reads the primary player's world position from a verified runtime layout.
func (a *App) PlayerPositionGet() (PlayerPosition, error) {
	player, transformNode, err := a.playerPositionAddresses()
	if err != nil {
		return PlayerPosition{}, err
	}

	position := PlayerPosition{Address: uint64(player)}
	if position.X, err = a.readPlayerPositionFloat(transformNode + playerPositionXOffset); err != nil {
		return PlayerPosition{}, fmt.Errorf("读取玩家 X 坐标失败: %w", err)
	}
	if position.Y, err = a.readPlayerPositionFloat(transformNode + playerPositionYOffset); err != nil {
		return PlayerPosition{}, fmt.Errorf("读取玩家 Y 坐标失败: %w", err)
	}
	if position.Z, err = a.readPlayerPositionFloat(transformNode + playerPositionZOffset); err != nil {
		return PlayerPosition{}, fmt.Errorf("读取玩家 Z 坐标失败: %w", err)
	}
	if err := validatePlayerPosition(position.X, position.Y, position.Z); err != nil {
		return PlayerPosition{}, err
	}
	return position, nil
}

// PlayerPositionSet writes all world-coordinate components, then reads them back for confirmation.
func (a *App) PlayerPositionSet(x, y, z float32) (PlayerPosition, error) {
	if err := validatePlayerPosition(x, y, z); err != nil {
		return PlayerPosition{}, err
	}
	player, transformNode, err := a.playerPositionAddresses()
	if err != nil {
		return PlayerPosition{}, err
	}
	for _, component := range []struct {
		address uintptr
		value   float32
		name    string
	}{
		{transformNode + playerPositionXOffset, x, "X"},
		{transformNode + playerPositionYOffset, y, "Y"},
		{transformNode + playerPositionZOffset, z, "Z"},
	} {
		if err := writeFloat32Remote(a.hProcess, component.address, component.value); err != nil {
			return PlayerPosition{}, fmt.Errorf("写入玩家 %s 坐标失败: %w", component.name, err)
		}
	}
	position, err := a.PlayerPositionGet()
	if err != nil {
		return PlayerPosition{}, fmt.Errorf("写入后读取玩家坐标失败: %w", err)
	}
	if position.Address != uint64(player) || position.X != x || position.Y != y || position.Z != z {
		return PlayerPosition{}, fmt.Errorf("玩家坐标写入校验失败")
	}
	return position, nil
}

func (a *App) playerPositionAddresses() (uintptr, uintptr, error) {
	player, transformNode, _, err := a.playerPositionAddressesForLayout()
	return player, transformNode, err
}

func (a *App) playerPositionAddressesForLayout() (uintptr, uintptr, playerPositionLayout, error) {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return 0, 0, playerPositionLayout{}, fmt.Errorf("未连接游戏进程")
	}

	var matched playerPositionLayout
	for _, layout := range playerPositionLayouts {
		signatureAddress := a.moduleBase + layout.signatureRVA
		candidate := make([]byte, playerPositionSignatureSize)
		if err := readProcessMemory(a.hProcess, signatureAddress, unsafe.Pointer(&candidate[0]), uintptr(len(candidate))); err != nil {
			continue
		}
		if !matchPattern(candidate, layout.signature, layout.signatureMask) {
			continue
		}
		displacement := int64(int32(binary.LittleEndian.Uint32(candidate[3:7])))
		resolved := int64(signatureAddress) + 7 + displacement
		if resolved <= 0 || uintptr(resolved) != a.moduleBase+layout.slotTableRVA {
			continue
		}
		if matched.version != "" {
			return 0, 0, playerPositionLayout{}, fmt.Errorf("玩家坐标签名匹配多个游戏布局")
		}
		matched = layout
	}
	if matched.version == "" {
		return 0, 0, playerPositionLayout{}, fmt.Errorf("玩家坐标签名不匹配，暂不支持当前游戏版本")
	}
	slotTable := a.moduleBase + matched.slotTableRVA
	player, err := a.readPlayerPositionPointer(slotTable)
	if err != nil || player == 0 {
		return 0, 0, playerPositionLayout{}, fmt.Errorf("读取玩家实体失败: %w", pointerReadError(err))
	}
	transformRoot, err := a.readPlayerPositionPointer(player + playerPositionTransformRoot)
	if err != nil || transformRoot == 0 {
		return 0, 0, playerPositionLayout{}, fmt.Errorf("读取玩家坐标根节点失败: %w", pointerReadError(err))
	}
	transformNode, err := a.readPlayerPositionPointer(transformRoot + playerPositionTransformNode)
	if err != nil || transformNode == 0 {
		return 0, 0, playerPositionLayout{}, fmt.Errorf("读取玩家坐标节点失败: %w", pointerReadError(err))
	}
	return player, transformNode, matched, nil
}

func validatePlayerPosition(x, y, z float32) error {
	for _, value := range []float32{x, y, z} {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || math.Abs(float64(value)) > float64(playerPositionMaximumAbsValue) {
			return fmt.Errorf("玩家坐标超出有效范围")
		}
	}
	return nil
}

func (a *App) readPlayerPositionPointer(address uintptr) (uintptr, error) {
	var value uint64
	if err := readProcessMemory(a.hProcess, address, unsafe.Pointer(&value), unsafe.Sizeof(value)); err != nil {
		return 0, err
	}
	if uint64(uintptr(value)) != value {
		return 0, fmt.Errorf("无效指针 0x%X", value)
	}
	return uintptr(value), nil
}

func (a *App) readPlayerPositionFloat(address uintptr) (float32, error) {
	var bits uint32
	if err := readProcessMemory(a.hProcess, address, unsafe.Pointer(&bits), unsafe.Sizeof(bits)); err != nil {
		return 0, err
	}
	return math.Float32frombits(bits), nil
}

func pointerReadError(err error) error {
	if err == nil {
		return fmt.Errorf("指针为空")
	}
	return err
}
