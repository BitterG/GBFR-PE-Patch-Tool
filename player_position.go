package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

const (
	playerPositionSignatureRVA    = uintptr(0x22CECA0)
	playerPositionSlotTableRVA    = uintptr(0x7036860)
	playerPositionSignatureSize   = 69
	playerPositionTransformRoot   = uintptr(0x28)
	playerPositionTransformNode   = uintptr(0x08)
	playerPositionXOffset         = uintptr(0xD8)
	playerPositionYOffset         = uintptr(0xD4)
	playerPositionZOffset         = uintptr(0xD0)
	playerPositionMaximumAbsValue = float32(10_000_000)
)

var playerPositionSignature = []byte{
	0x48, 0x8B, 0, 0, 0, 0, 0, 0x48, 0x85, 0, 0x74, 0, 0x48, 0x8B, 0, 0xFF,
	0, 0, 0, 0, 0, 0x48, 0x85, 0, 0x74, 0, 0x48, 0x8B, 0, 0, 0x48, 0x8B,
	0, 0, 0x48, 0x8B, 0, 0x48, 0x8D, 0, 0, 0, 0xFF, 0, 0, 0, 0, 0, 0xEB,
	0, 0xC5, 0, 0, 0, 0, 0, 0, 0, 0xC5, 0, 0, 0, 0, 0, 0x48, 0x8B, 0,
	0x48, 0x8D,
}

var playerPositionSignatureMask = []bool{
	true, true, false, false, false, false, false, true, true, false, true, false, true, true, false, true,
	false, false, false, false, false, true, true, false, true, false, true, true, false, false, true, true,
	false, false, true, true, false, true, true, false, false, false, true, false, false, false, false, false,
	true, false, true, false, false, false, false, false, false, false, true, false, false, false, false, false,
	true, true, false, true, true,
}

type PlayerPosition struct {
	X       float32 `json:"x"`
	Y       float32 `json:"y"`
	Z       float32 `json:"z"`
	Address uint64  `json:"address"`
}

// PlayerPositionGet reads the primary player's world position from the verified 2.0.2 runtime layout.
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
	if a.hProcess == 0 || a.moduleBase == 0 {
		return 0, 0, fmt.Errorf("未连接游戏进程")
	}

	signatureAddress := a.moduleBase + playerPositionSignatureRVA
	signature := make([]byte, playerPositionSignatureSize)
	if err := readProcessMemory(a.hProcess, signatureAddress, unsafe.Pointer(&signature[0]), uintptr(len(signature))); err != nil {
		return 0, 0, fmt.Errorf("读取玩家坐标签名失败: %w", err)
	}
	if !matchPattern(signature, playerPositionSignature, playerPositionSignatureMask) {
		return 0, 0, fmt.Errorf("玩家坐标仅支持游戏 2.0.2，当前签名不匹配")
	}

	displacement := int64(int32(binary.LittleEndian.Uint32(signature[3:7])))
	slotTable := uintptr(int64(signatureAddress) + 7 + displacement)
	if slotTable != a.moduleBase+playerPositionSlotTableRVA {
		return 0, 0, fmt.Errorf("玩家坐标根表校验失败")
	}

	player, err := a.readPlayerPositionPointer(slotTable)
	if err != nil || player == 0 {
		return 0, 0, fmt.Errorf("读取玩家实体失败: %w", pointerReadError(err))
	}
	transformRoot, err := a.readPlayerPositionPointer(player + playerPositionTransformRoot)
	if err != nil || transformRoot == 0 {
		return 0, 0, fmt.Errorf("读取玩家坐标根节点失败: %w", pointerReadError(err))
	}
	transformNode, err := a.readPlayerPositionPointer(transformRoot + playerPositionTransformNode)
	if err != nil || transformNode == 0 {
		return 0, 0, fmt.Errorf("读取玩家坐标节点失败: %w", pointerReadError(err))
	}
	return player, transformNode, nil
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
