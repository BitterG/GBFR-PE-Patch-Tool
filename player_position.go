package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
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
	// 引用 slotTable 的代码模式（mov rcx,[rip+disp]...），AOB 通配字节用 0 + mask false
	signatureAOB   []byte
	signatureAMask []bool
}

// 2.0.4 玩家坐标获取（实测 0xC2541B 起 26 字节；displacement 指向 slotTable）
// 48 8B 0D ?? ?? ?? ?? | 48 85 C9 | 74 71 | 48 8B 01 | FF 90 C0 04 00 00 | 48 85 C0 | 74 63
var playerPositionLayout204 = playerPositionLayout{
	version: "2.0.4",
	signatureAOB: []byte{
		0x48, 0x8B, 0x0D, 0, 0, 0, 0, 0x48, 0x85, 0xC9, 0x74, 0x71, 0x48, 0x8B, 0x01, 0xFF,
		0x90, 0xC0, 0x04, 0x00, 0x00, 0x48, 0x85, 0xC0, 0x74, 0x63,
	},
	signatureAMask: []bool{
		true, true, true, false, false, false, false, true, true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true, true, true,
	},
}

var playerPositionLayouts = [...]playerPositionLayout{playerPositionLayout204}

// 玩家坐标 getter（AOB 唯一命中）：mov rax,rdx; vmovaps xmm0,[rcx+0xD0]; vmovaps [rdx],xmm0; ret
var gravityGetterAOB = []byte{
	0x48, 0x89, 0xD0, 0xC5, 0xF8, 0x28, 0x81, 0xD0, 0x00, 0x00, 0x00, 0xC5, 0xF8, 0x29, 0x02, 0xC3,
}
var gravityGetterMask = []bool{
	true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true,
}

// 坐标 setter（位于 getter 前方 0x200 内）：vmovaps xmm0,[rdx]; vmovaps [rcx+0xD0],xmm0; ret
// gravity 指令（vmovaps [rcx+0xD0],xmm0）= setter 命中 + 4
var gravitySetterAOB = []byte{
	0xC5, 0xF8, 0x28, 0x02, 0xC5, 0xF8, 0x29, 0x81, 0xD0, 0x00, 0x00, 0x00, 0xC3,
}
var gravitySetterMask = []bool{
	true, true, true, true, true, true, true, true, true, true, true, true, true,
}

var gravityOriginal = []byte{0xC5, 0xF8, 0x29, 0x81, 0xD0, 0x00, 0x00, 0x00}

type PlayerPosition struct {
	X       float32 `json:"x"`
	Y       float32 `json:"y"`
	Z       float32 `json:"z"`
	Address uint64  `json:"address"`
}

// PlayerPositionGet reads the primary player's world position from a verified runtime layout.
func (a *App) PlayerPositionGet() (PlayerPosition, error) {
	player, transformNode, err := a.cachedPlayerPositionAddresses()
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
	player, transformNode, err := a.cachedPlayerPositionAddresses()
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

func (a *App) playerPositionAddresses() (uintptr, uintptr, uintptr, error) {
	player, transformNode, gravityAddr, err := a.playerPositionAddressesForLayout()
	return player, transformNode, gravityAddr, err
}

// cachedPlayerPositionAddresses 返回缓存的玩家实体/坐标节点；缓存失效时重新 AOB 定位。
// 飞行循环每 16ms 调用 PlayerPositionGet，AOB 全模块扫描不能走热路径。
func (a *App) cachedPlayerPositionAddresses() (uintptr, uintptr, error) {
	if a.playerPosPlayer != 0 && a.playerPosNode != 0 && a.validPlayerPositionNode(a.playerPosNode) {
		return a.playerPosPlayer, a.playerPosNode, nil
	}
	player, node, _, err := a.playerPositionAddresses()
	if err != nil {
		return 0, 0, err
	}
	a.playerPosPlayer, a.playerPosNode = player, node
	return player, node, nil
}

// playerPositionAddressesForLayout 用 AOB 定位玩家坐标链：签名代码 → slotTable（动态 disp）
// → player → +0x28 → root → +0x08 → node，并校验 node 坐标合理性；同时解析飞行重力地址。
func (a *App) playerPositionAddressesForLayout() (uintptr, uintptr, uintptr, error) {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return 0, 0, 0, fmt.Errorf("未连接游戏进程")
	}

	var lastErr error
	for _, layout := range playerPositionLayouts {
		matches, err := a.scanPatternAll(layout.signatureAOB, layout.signatureAMask)
		if err != nil {
			lastErr = err
			continue
		}
		for _, signatureAddress := range matches {
			candidate := make([]byte, playerPositionSignatureSize)
			if err := readProcessMemory(a.hProcess, signatureAddress, unsafe.Pointer(&candidate[0]), uintptr(len(candidate))); err != nil {
				continue
			}
			if !matchPattern(candidate, layout.signatureAOB, layout.signatureAMask) {
				continue
			}
			// displacement（lea/mov [rip+disp] 的 4 字节）→ slotTable
			displacement := int64(int32(binary.LittleEndian.Uint32(candidate[3:7])))
			slotTable := uintptr(int64(signatureAddress) + 7 + displacement)
			if slotTable == 0 || slotTable < a.moduleBase {
				continue
			}
			player, err := a.readPlayerPositionPointer(slotTable)
			if err != nil || player == 0 {
				continue
			}
			transformRoot, err := a.readPlayerPositionPointer(player + playerPositionTransformRoot)
			if err != nil || transformRoot == 0 {
				continue
			}
			transformNode, err := a.readPlayerPositionPointer(transformRoot + playerPositionTransformNode)
			if err != nil || transformNode == 0 {
				continue
			}
			if !a.validPlayerPositionNode(transformNode) {
				continue
			}
			gravityAddr, err := a.resolveGravityAddress()
			if err != nil {
				return 0, 0, 0, fmt.Errorf("定位飞行重力失败: %w", err)
			}
			return player, transformNode, gravityAddr, nil
		}
	}
	if lastErr != nil {
		return 0, 0, 0, fmt.Errorf("玩家坐标 AOB 扫描失败: %v", lastErr)
	}
	return 0, 0, 0, fmt.Errorf("玩家坐标签名不匹配，暂不支持当前游戏版本")
}

func (a *App) validPlayerPositionNode(node uintptr) bool {
	for _, off := range []uintptr{playerPositionXOffset, playerPositionYOffset, playerPositionZOffset} {
		value, err := a.readPlayerPositionFloat(node + off)
		if err != nil || math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || math.Abs(float64(value)) > float64(playerPositionMaximumAbsValue) {
			return false
		}
	}
	return true
}

// resolveGravityAddress 定位飞行重力指令（vmovaps [rcx+0xD0],xmm0）：
// 玩家坐标 getter AOB 唯一命中 → 向前 0x200 内找坐标 setter → gravity 指令 = setter+4。结果缓存。
func (a *App) resolveGravityAddress() (uintptr, error) {
	if a.playerGravityAddr != 0 {
		return a.playerGravityAddr, nil
	}
	getter, err := a.scanPatternUnique(gravityGetterAOB, gravityGetterMask, "飞行坐标读取特征")
	if err != nil {
		return 0, err
	}
	setter, err := scanPatternBackward(a.hProcess, a.moduleBase, getter, gravitySetterAOB, gravitySetterMask, 0x200)
	if err != nil {
		return 0, err
	}
	gravityAddr := setter + 4
	current := make([]byte, 8)
	if err := readProcessMemory(a.hProcess, gravityAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return 0, fmt.Errorf("读取飞行重力指令失败: %w", err)
	}
	if !bytesEqual(current, gravityOriginal) {
		return 0, fmt.Errorf("飞行重力指令字节未知: %s", bytesToHex(current))
	}
	a.playerGravityAddr = gravityAddr
	return gravityAddr, nil
}

// scanPatternBackward 从 endAddr 向前 maxBack 字节范围内逆向匹配（取离 endAddr 最近的命中）。
func scanPatternBackward(h windows.Handle, moduleBase, endAddr uintptr, pattern []byte, mask []bool, maxBack uintptr) (uintptr, error) {
	start := endAddr - maxBack
	if start < moduleBase {
		start = moduleBase
	}
	size := endAddr - start
	buf := make([]byte, int(size))
	if err := readProcessMemory(h, start, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return 0, fmt.Errorf("读取逆向扫描区域失败: %w", err)
	}
	for i := len(buf) - len(pattern); i >= 0; i-- {
		if matchPattern(buf[i:], pattern, mask) {
			return start + uintptr(i), nil
		}
	}
	return 0, fmt.Errorf("逆向未找到飞行坐标写入特征")
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
