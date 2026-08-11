package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

type OverLimitOption struct {
	ID       uint32  `json:"id"`
	Hex      string  `json:"hex"`
	Name     string  `json:"name"`
	EffectID uint32  `json:"effectId"`
	MaxValue float32 `json:"maxValue"`
	Scale    float32 `json:"scale"`
}

type OverLimitSlot struct {
	Index     int     `json:"index"`
	Attribute uint32  `json:"attribute"`
	Level     uint32  `json:"level"`
	Value     float32 `json:"value"`
}

type OverLimitStatus struct {
	Found        bool            `json:"found"`
	Hooked       bool            `json:"hooked"`
	Address      uint64          `json:"address"`
	RVA          uint64          `json:"rva"`
	SelectedAddr uint64          `json:"selectedAddr"`
	CommitRVA    uint64          `json:"commitRva"`
	CurrentBytes string          `json:"currentBytes"`
	Slots        []OverLimitSlot `json:"slots"`
}

type OverLimitUpdate struct {
	Index     int     `json:"index"`
	Attribute uint32  `json:"attribute"`
	Level     uint32  `json:"level"`
	Value     float32 `json:"value"`
}

var overLimitAttributeOptions = []OverLimitOption{
	{0x52A207B5, "52A207B5", "HP", 1, 2000, 1},
	{0x54929589, "54929589", "HP回复上限", 107, 20, 1},
	{0x6CB38EF3, "6CB38EF3", "昏厥值", 3, 20, 10},
	{0xC4925BD7, "C4925BD7", "攻击力", 0, 1000, 1},
	{0x45C65767, "45C65767", "暴击率", 2, 20, 1},
	{0x43B7581D, "43B7581D", "普通攻击上限", 103, 20, 1},
	{0x9A97C049, "9A97C049", "能力伤害", 100, 20, 1},
	{0x9C555433, "9C555433", "能力伤害上限", 104, 20, 1},
	{0x4E42646B, "4E42646B", "奥义伤害", 101, 20, 1},
	{0x4A4C093D, "4A4C093D", "奥义伤害上限", 105, 20, 1},
	{0x68B39018, "68B39018", "奥义连锁伤害", 102, 20, 1},
}

var overLimitLevelOptions = []OverLimitOption{
	{0x00000001, "00000001", "LV1", 0, 0, 0},
	{0x00000002, "00000002", "LV2", 0, 0, 0},
	{0x00000004, "00000004", "LV3", 0, 0, 0},
	{0x00000008, "00000008", "LV4", 0, 0, 0},
	{0x00000010, "00000010", "LV5", 0, 0, 0},
	{0x00000020, "00000020", "LV6", 0, 0, 0},
	{0x00000040, "00000040", "LV7", 0, 0, 0},
	{0x00000080, "00000080", "LV8", 0, 0, 0},
	{0x00000100, "00000100", "LV9", 0, 0, 0},
	{0x00000200, "00000200", "LV10", 0, 0, 0},
}

var (
	overLimitSelectedPattern = []byte{
		0x8B, 0x03, 0x89, 0x02, 0x8B, 0x43, 0x04, 0x89, 0x42, 0x04,
		0x48, 0x8B, 0x43, 0x08, 0x48, 0x89, 0x42, 0x08,
		0x49, 0x83, 0x86, 0xD8, 0x0A, 0x00, 0x00, 0x10,
		0xEB, 0, 0x48, 0x85, 0xF6,
	}
	overLimitSelectedMask = []bool{
		true, true, true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true,
		true, false, true, true, true,
	}
	overLimitSelectedOrig  = []byte{0x8B, 0x03, 0x89, 0x02, 0x8B, 0x43, 0x04}
	overLimitCommitPattern = []byte{
		0x48, 0xC7, 0x85, 0xE8, 0x0F, 0x00, 0x00, 0x2B, 0x00, 0x00, 0x00,
		0x48, 0x8D, 0x4D, 0xB8, 0x48, 0x8D, 0x95, 0xE0, 0x0F, 0x00, 0x00,
		0xC5, 0xF8, 0x77, 0xE8, 0, 0, 0, 0,
		0x48, 0x8B, 0x05, 0, 0, 0, 0, 0x48, 0x8B, 0x4D, 0xB8, 0x48, 0x89, 0x0D, 0, 0, 0, 0,
	}
	overLimitCommitMask = []bool{
		true, true, true, true, true, true, true, true, true, true, true,
		true, true, true, true, true, true, true, true, true, true, true,
		true, true, true, true, false, false, false, false,
		true, true, true, false, false, false, false, true, true, true, true, true, true, true, false, false, false, false,
	}
)

const (
	overLimitCaveDataOffset = uintptr(0x40)
	overLimitSlotStride     = uintptr(0x10)
	overLimitSlotCount      = 4
)

func overLimitSelectedHookedMask() []bool {
	mask := append([]bool{}, overLimitSelectedMask...)
	for i := 0; i < len(overLimitSelectedOrig) && i < len(mask); i++ {
		mask[i] = false
	}
	return mask
}

func (a *App) OverLimitGetOptions() map[string][]OverLimitOption {
	return map[string][]OverLimitOption{
		"attributes": overLimitAttributeOptions,
		"levels":     overLimitLevelOptions,
	}
}

func (a *App) OverLimitScan() (OverLimitStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return OverLimitStatus{}, err
	}
	addr, err := a.scanPatternUnique(overLimitSelectedPattern, overLimitSelectedMask, "上限突破角色指针特征")
	if err != nil {
		addr, err = a.scanPatternUnique(overLimitSelectedPattern, overLimitSelectedHookedMask(), "上限突破角色指针特征")
		if err != nil {
			a.overLimitHookAddr = 0
			return OverLimitStatus{}, err
		}
	}
	a.overLimitHookAddr = addr
	return a.readOverLimitStatus()
}

func (a *App) OverLimitGetStatus() (OverLimitStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return OverLimitStatus{}, err
	}
	if a.overLimitHookAddr == 0 {
		return a.OverLimitScan()
	}
	status, err := a.readOverLimitStatus()
	if err != nil {
		a.overLimitHookAddr = 0
		return a.OverLimitScan()
	}
	return status, nil
}

func (a *App) OverLimitEnable() (OverLimitStatus, error) {
	status, err := a.OverLimitGetStatus()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if status.Hooked {
		return status, nil
	}
	if a.overLimitHookAddr == 0 {
		return OverLimitStatus{}, fmt.Errorf("未定位上限突破角色指针特征")
	}

	cave, err := virtualAllocRemoteNear(a.hProcess, a.overLimitHookAddr, 0x1000)
	if err != nil {
		return OverLimitStatus{}, fmt.Errorf("分配上限突破代码洞失败: %w", err)
	}
	code, err := buildOverLimitSelectedCave(cave, a.overLimitHookAddr+uintptr(len(overLimitSelectedOrig)))
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return OverLimitStatus{}, err
	}
	if err := writeProcessMemory(a.hProcess, cave, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return OverLimitStatus{}, fmt.Errorf("写入上限突破代码洞失败: %w", err)
	}
	patch, err := makeRelJump(a.overLimitHookAddr, cave, len(overLimitSelectedOrig))
	if err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return OverLimitStatus{}, err
	}
	if err := writeCodeMemory(a.hProcess, a.overLimitHookAddr, patch); err != nil {
		_ = virtualFreeRemote(a.hProcess, cave)
		return OverLimitStatus{}, fmt.Errorf("写入上限突破 hook 失败: %w", err)
	}
	a.overLimitCaveAddr = cave
	return a.readOverLimitStatus()
}

func (a *App) OverLimitSetSlot(update OverLimitUpdate) (OverLimitStatus, error) {
	if update.Index < 0 || update.Index >= overLimitSlotCount {
		return OverLimitStatus{}, fmt.Errorf("无效的上限突破槽位: %d", update.Index+1)
	}
	if !validOverLimitAttribute(update.Attribute) {
		return OverLimitStatus{}, fmt.Errorf("无效的上限突破属性: 0x%08X", update.Attribute)
	}
	if !validOverLimitLevel(update.Level) {
		return OverLimitStatus{}, fmt.Errorf("无效的上限突破等级: 0x%08X", update.Level)
	}
	maxValue, scale, ok := overLimitValueSpec(update.Attribute)
	if !ok {
		return OverLimitStatus{}, fmt.Errorf("无效的上限突破属性: 0x%08X", update.Attribute)
	}
	effectID := overLimitEffectID(update.Attribute)
	if update.Value <= 0 {
		update.Value = maxValue
	}
	if update.Value > maxValue {
		return OverLimitStatus{}, fmt.Errorf("%s 数值不能超过 %.0f", overLimitAttributeName(update.Attribute), maxValue)
	}
	status, err := a.OverLimitGetStatus()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if !status.Hooked || status.SelectedAddr == 0 {
		return OverLimitStatus{}, fmt.Errorf("请先开启上限突破读取，并在游戏突破界面加载角色")
	}

	base := uintptr(status.SelectedAddr) + uintptr(update.Index)*overLimitSlotStride
	if err := writeUint32Remote(a.hProcess, base, update.Attribute); err != nil {
		return OverLimitStatus{}, fmt.Errorf("写入槽位 %d 属性失败: %w", update.Index+1, err)
	}
	if err := writeUint32Remote(a.hProcess, base+4, update.Level); err != nil {
		return OverLimitStatus{}, fmt.Errorf("写入槽位 %d 等级失败: %w", update.Index+1, err)
	}
	if err := writeUint32Remote(a.hProcess, base+8, effectID); err != nil {
		return OverLimitStatus{}, fmt.Errorf("写入槽位 %d 效果ID失败: %w", update.Index+1, err)
	}
	if err := writeFloat32Remote(a.hProcess, base+0xC, update.Value/scale); err != nil {
		return OverLimitStatus{}, fmt.Errorf("写入槽位 %d 数值失败: %w", update.Index+1, err)
	}
	return a.readOverLimitStatus()
}

func (a *App) OverLimitCommit() (OverLimitStatus, error) {
	status, err := a.OverLimitGetStatus()
	if err != nil {
		return OverLimitStatus{}, err
	}
	if status.SelectedAddr == 0 {
		return OverLimitStatus{}, fmt.Errorf("请先在游戏突破界面加载角色")
	}
	return status, nil
}

func (a *App) readOverLimitStatus() (OverLimitStatus, error) {
	if a.overLimitHookAddr == 0 {
		return OverLimitStatus{}, fmt.Errorf("未定位上限突破角色指针特征")
	}
	buf := make([]byte, len(overLimitSelectedOrig))
	if err := readProcessMemory(a.hProcess, a.overLimitHookAddr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return OverLimitStatus{}, fmt.Errorf("读取上限突破 hook 指令失败: %w", err)
	}
	orig := bytesEqual(buf, overLimitSelectedOrig)
	hooked := buf[0] == 0xE9 && buf[5] == 0x90 && buf[6] == 0x90
	if !orig && !hooked {
		return OverLimitStatus{}, fmt.Errorf("上限突破指令字节异常: %s", bytesToHex(buf))
	}

	selected := uintptr(0)
	if hooked {
		if a.overLimitCaveAddr == 0 {
			a.overLimitCaveAddr = relJumpTarget(a.overLimitHookAddr, buf)
		}
		if err := readProcessMemory(a.hProcess, a.overLimitCaveAddr+overLimitCaveDataOffset, unsafe.Pointer(&selected), unsafe.Sizeof(selected)); err != nil {
			return OverLimitStatus{}, fmt.Errorf("读取上限突破角色指针失败: %w", err)
		}
	}

	status := OverLimitStatus{
		Found:        true,
		Hooked:       hooked,
		Address:      uint64(a.overLimitHookAddr),
		RVA:          uint64(a.overLimitHookAddr - a.moduleBase),
		SelectedAddr: uint64(selected),
		CurrentBytes: bytesToHex(buf),
	}
	if selected != 0 {
		slots, err := readOverLimitSlots(a.hProcess, selected)
		if err != nil {
			return OverLimitStatus{}, err
		}
		status.Slots = slots
	}
	if a.overLimitCommitAddr != 0 {
		status.CommitRVA = uint64(a.overLimitCommitAddr - a.moduleBase)
	}
	return status, nil
}

func buildOverLimitSelectedCave(cave uintptr, returnAddr uintptr) ([]byte, error) {
	code := make([]byte, 0, 0x80)
	code = append(code, 0x49, 0xBA) // mov r10,data
	code = binary.LittleEndian.AppendUint64(code, uint64(cave+overLimitCaveDataOffset))
	code = append(code, 0x4C, 0x8B, 0xDA) // mov r11,rdx
	code = append(code, 0x49, 0x29, 0xDB) // sub r11,rbx
	code = append(code, 0x49, 0x01, 0xF3) // add r11,rsi
	code = append(code, 0x4D, 0x89, 0x1A) // mov [r10],r11
	code = append(code, overLimitSelectedOrig...)
	jmp, err := makeRelJump(cave+uintptr(len(code)), returnAddr, 5)
	if err != nil {
		return nil, err
	}
	code = append(code, jmp...)
	for len(code) < int(overLimitCaveDataOffset)+8 {
		code = append(code, 0)
	}
	return code, nil
}

func makeRelJump(src uintptr, dst uintptr, size int) ([]byte, error) {
	if size < 5 {
		return nil, fmt.Errorf("跳转覆盖长度不足")
	}
	delta := int64(dst) - int64(src) - 5
	if delta < math.MinInt32 || delta > math.MaxInt32 {
		return nil, fmt.Errorf("跳转距离超过 rel32 范围")
	}
	patch := make([]byte, size)
	patch[0] = 0xE9
	binary.LittleEndian.PutUint32(patch[1:5], uint32(int32(delta)))
	for i := 5; i < len(patch); i++ {
		patch[i] = 0x90
	}
	return patch, nil
}

func relJumpTarget(src uintptr, buf []byte) uintptr {
	return uintptr(int64(src+5) + int64(int32(binary.LittleEndian.Uint32(buf[1:5]))))
}

func readOverLimitSlots(h windows.Handle, base uintptr) ([]OverLimitSlot, error) {
	slots := make([]OverLimitSlot, 0, overLimitSlotCount)
	for i := 0; i < overLimitSlotCount; i++ {
		addr := base + uintptr(i)*overLimitSlotStride
		buf := make([]byte, 0x10)
		if err := readProcessMemory(h, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
			return nil, fmt.Errorf("读取上限突破槽位 %d 失败: %w", i+1, err)
		}
		value := math.Float32frombits(binary.LittleEndian.Uint32(buf[0xC:0x10]))
		if _, scale, ok := overLimitValueSpec(binary.LittleEndian.Uint32(buf[0:4])); ok {
			value *= scale
		}
		slots = append(slots, OverLimitSlot{
			Index:     i,
			Attribute: binary.LittleEndian.Uint32(buf[0:4]),
			Level:     binary.LittleEndian.Uint32(buf[4:8]),
			Value:     value,
		})
	}
	return slots, nil
}

func (a *App) findOverLimitCommitAddr() (uintptr, error) {
	if a.overLimitCommitAddr != 0 {
		return a.overLimitCommitAddr, nil
	}
	addr, err := a.scanPatternUnique(overLimitCommitPattern, overLimitCommitMask, "上限突破保存函数特征")
	if err != nil {
		return 0, err
	}
	callAddr := addr + 0x19
	buf := make([]byte, 5)
	if err := readProcessMemory(a.hProcess, callAddr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return 0, fmt.Errorf("读取上限突破保存 call 失败: %w", err)
	}
	if buf[0] != 0xE8 {
		return 0, fmt.Errorf("上限突破保存 call 字节异常: %s", bytesToHex(buf))
	}
	target := uintptr(int64(callAddr+5) + int64(int32(binary.LittleEndian.Uint32(buf[1:5]))))
	a.overLimitCommitAddr = target
	return target, nil
}

func (a *App) callRemoteOneArg(fn uintptr, arg uintptr) error {
	code := make([]byte, 0, 32)
	code = append(code, 0x48, 0xB9)
	code = binary.LittleEndian.AppendUint64(code, uint64(arg))
	code = append(code, 0x48, 0xB8)
	code = binary.LittleEndian.AppendUint64(code, uint64(fn))
	code = append(code, 0x48, 0x83, 0xEC, 0x28)
	code = append(code, 0xFF, 0xD0)
	code = append(code, 0x48, 0x83, 0xC4, 0x28)
	code = append(code, 0xC3)

	remote, err := virtualAllocRemote(a.hProcess, uintptr(len(code)), windows.PAGE_EXECUTE_READWRITE)
	if err != nil {
		return err
	}
	defer func() { _ = virtualFreeRemote(a.hProcess, remote) }()
	if err := writeProcessMemory(a.hProcess, remote, unsafe.Pointer(&code[0]), uintptr(len(code))); err != nil {
		return err
	}
	thread, err := createRemoteThread(a.hProcess, remote, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	wait, err := windows.WaitForSingleObject(thread, 5000)
	if err != nil {
		return err
	}
	if wait == uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("远程保存调用超时")
	}
	return nil
}

func writeUint32Remote(h windows.Handle, addr uintptr, value uint32) error {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, value)
	return writeProcessMemory(h, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf)))
}

func writeFloat32Remote(h windows.Handle, addr uintptr, value float32) error {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, math.Float32bits(value))
	return writeProcessMemory(h, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf)))
}

func validOverLimitAttribute(v uint32) bool {
	for _, opt := range overLimitAttributeOptions {
		if opt.ID == v {
			return true
		}
	}
	return false
}

func overLimitValueSpec(v uint32) (float32, float32, bool) {
	for _, opt := range overLimitAttributeOptions {
		if opt.ID == v {
			return opt.MaxValue, opt.Scale, true
		}
	}
	return 0, 1, false
}

func overLimitEffectID(v uint32) uint32 {
	for _, opt := range overLimitAttributeOptions {
		if opt.ID == v {
			return opt.EffectID
		}
	}
	return 0
}

func overLimitAttributeName(v uint32) string {
	for _, opt := range overLimitAttributeOptions {
		if opt.ID == v {
			return opt.Name
		}
	}
	return fmt.Sprintf("0x%08X", v)
}

func validOverLimitLevel(v uint32) bool {
	for _, opt := range overLimitLevelOptions {
		if opt.ID == v {
			return true
		}
	}
	return false
}
