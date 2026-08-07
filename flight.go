package main

import (
	"fmt"
	"math"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	flightTick            = 16 * time.Millisecond
	flightMinimumSpeed    = float32(0.1)
	flightMaximumSpeed    = float32(1000)
	flightDefaultSpeed    = float32(8)
	flightVirtualKeyW     = 0x57
	flightVirtualKeyA     = 0x41
	flightVirtualKeyS     = 0x53
	flightVirtualKeyD     = 0x44
	flightVirtualKeySpace = 0x20
	flightVirtualKeyCtrl  = 0x11
)

var (
	modUser32               = windows.NewLazySystemDLL("user32.dll")
	procGetAsyncKeyState    = modUser32.NewProc("GetAsyncKeyState")
	procGetForegroundWindow = modUser32.NewProc("GetForegroundWindow")
)

type FlightStatus struct {
	Enabled bool    `json:"enabled"`
	Speed   float32 `json:"speed"`
}

var flightMu sync.Mutex

// FlightSetEnabled controls the player in world axes while the game window is foreground.
// X/Z movement is intentionally world-axis based until a verified facing-angle field is available.
func (a *App) FlightSetEnabled(enabled bool, speed float32) (FlightStatus, error) {
	flightMu.Lock()
	defer flightMu.Unlock()

	if !enabled {
		if a.flyingEnabled {
			if err := a.setFlightGravityDisabled(false); err != nil {
				return FlightStatus{Enabled: true, Speed: a.flightSpeed}, err
			}
		}
		if a.flightStop != nil {
			close(a.flightStop)
			a.flightStop = nil
		}
		a.flyingEnabled = false
		return FlightStatus{Enabled: false, Speed: a.flightSpeed}, nil
	}
	if a.hProcess == 0 || a.moduleBase == 0 {
		return FlightStatus{}, fmt.Errorf("未连接游戏进程")
	}
	if speed == 0 {
		speed = flightDefaultSpeed
	}
	if !validFlightSpeed(speed) {
		return FlightStatus{}, fmt.Errorf("飞行速度必须在 %.1f 到 %.0f 之间", flightMinimumSpeed, flightMaximumSpeed)
	}
	if a.flyingEnabled {
		a.flightSpeed = speed
		return FlightStatus{Enabled: true, Speed: speed}, nil
	}
	if _, _, _, err := a.playerPositionAddresses(); err != nil {
		return FlightStatus{}, err
	}
	if err := a.setFlightGravityDisabled(true); err != nil {
		return FlightStatus{}, err
	}

	stop := make(chan struct{})
	a.flightStop = stop
	a.flightSpeed = speed
	a.flyingEnabled = true
	go a.flightLoop(stop)
	return FlightStatus{Enabled: true, Speed: speed}, nil
}

func (a *App) setFlightGravityDisabled(disabled bool) error {
	addr, err := a.resolveGravityAddress()
	if err != nil {
		return fmt.Errorf("定位飞行模式游戏布局失败: %w", err)
	}
	current := make([]byte, 8)
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return fmt.Errorf("读取飞行重力指令失败: %w", err)
	}

	original := []byte{0xC5, 0xF8, 0x29, 0x81, 0xD0, 0x00, 0x00, 0x00}
	if disabled {
		if allNop(current) {
			return nil
		}
		if !bytesEqual(current, original) {
			return fmt.Errorf("飞行重力指令字节未知: %s", bytesToHex(current))
		}
		if err := writeCodeMemory(a.hProcess, addr, []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90}); err != nil {
			return fmt.Errorf("禁用飞行重力失败: %w", err)
		}
		return nil
	}

	if bytesEqual(current, original) {
		return nil
	}
	if !allNop(current) {
		return fmt.Errorf("飞行重力指令字节未知: %s", bytesToHex(current))
	}
	if err := writeCodeMemory(a.hProcess, addr, original); err != nil {
		return fmt.Errorf("恢复飞行重力失败: %w", err)
	}
	return nil
}

func (a *App) FlightGetStatus() FlightStatus {
	flightMu.Lock()
	defer flightMu.Unlock()
	return FlightStatus{Enabled: a.flyingEnabled, Speed: a.flightSpeed}
}

func (a *App) flightLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(flightTick)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.applyFlightInput()
		}
	}
}

func (a *App) applyFlightInput() {
	if foregroundWindow() == 0 || foregroundWindowPID() != a.charaPID {
		return
	}
	input := flightInput{
		forward: keyPressed(flightVirtualKeyW),
		left:    keyPressed(flightVirtualKeyA),
		back:    keyPressed(flightVirtualKeyS),
		right:   keyPressed(flightVirtualKeyD),
		up:      keyPressed(flightVirtualKeySpace),
		down:    keyPressed(flightVirtualKeyCtrl),
	}
	dx, dy, dz := input.vector()
	if dx == 0 && dy == 0 && dz == 0 {
		return
	}

	position, err := a.PlayerPositionGet()
	if err != nil {
		return
	}
	flightMu.Lock()
	speed := a.flightSpeed
	flightMu.Unlock()
	step := speed * float32(flightTick.Seconds())
	_, _ = a.PlayerPositionSet(position.X+dx*step, position.Y+dy*step, position.Z+dz*step)
}

type flightInput struct {
	forward, left, back, right, up, down bool
}

func (input flightInput) vector() (float32, float32, float32) {
	x, y, z := float32(0), float32(0), float32(0)
	if input.forward {
		x++
	}
	if input.back {
		x--
	}
	if input.left {
		z++
	}
	if input.right {
		z--
	}
	if input.up {
		y++
	}
	if input.down {
		y--
	}
	length := float32(math.Sqrt(float64(x*x + y*y + z*z)))
	if length == 0 {
		return 0, 0, 0
	}
	return x / length, y / length, z / length
}

func validFlightSpeed(speed float32) bool {
	return !math.IsNaN(float64(speed)) && !math.IsInf(float64(speed), 0) && speed >= flightMinimumSpeed && speed <= flightMaximumSpeed
}

func keyPressed(virtualKey uintptr) bool {
	state, _, _ := procGetAsyncKeyState.Call(virtualKey)
	return uint16(state)&0x8000 != 0
}

func foregroundWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return hwnd
}

func foregroundWindowPID() uint32 {
	var pid uint32
	windows.GetWindowThreadProcessId(windows.HWND(foregroundWindow()), &pid)
	return pid
}
