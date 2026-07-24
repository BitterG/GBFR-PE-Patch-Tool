//go:build windows

package backend

import (
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	damageOverlayClassName = "GBFRDamageOverlayWindow"

	cwUseDefault     = 0x80000000
	wsPopup          = 0x80000000
	wsExLayered      = 0x00080000
	wsExTopmost      = 0x00000008
	wsExToolWindow   = 0x00000080
	swShow           = 5
	lwaColorKey      = 0x00000001
	wmClose          = 0x0010
	wmDestroy        = 0x0002
	wmPaint          = 0x000F
	wmTimer          = 0x0113
	wmNcHitTest      = 0x0084
	htCaption        = 2
	htBottomRight    = 17
	dtCenter         = 0x00000001
	dtVCenter        = 0x00000004
	dtSingleLine     = 0x00000020
	transparentBk    = 1
	outDefaultPrec   = 0
	clipDefaultPrec  = 0
	cleartypeQuality = 5
	ffDontCare       = 0
	fwBold           = 700
	animationTimerID = 1
	animationMs      = 220
)

type point struct {
	x int32
	y int32
}

type rect struct {
	left   int32
	top    int32
	right  int32
	bottom int32
}

type paintStruct struct {
	hdc         syscall.Handle
	fErase      int32
	rcPaint     rect
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type damageOverlayWindow struct {
	mu           sync.Mutex
	hwnd         syscall.Handle
	value        uint64
	displayValue float64
	startValue   float64
	targetValue  float64
	fontSize     int
	animStart    time.Time
	animating    bool
	ready        chan error
}

var damageOverlayProc = syscall.NewCallback(damageOverlayWndProc)
var activeDamageOverlay *damageOverlayWindow

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	gdi32                = windows.NewLazySystemDLL("gdi32.dll")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procSetLayeredAttrs  = user32.NewProc("SetLayeredWindowAttributes")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procSetTimer         = user32.NewProc("SetTimer")
	procKillTimer        = user32.NewProc("KillTimer")
	procInvalidateRect   = user32.NewProc("InvalidateRect")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procScreenToClient   = user32.NewProc("ScreenToClient")
	procBeginPaint       = user32.NewProc("BeginPaint")
	procEndPaint         = user32.NewProc("EndPaint")
	procGetModuleHandleW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procFillRect         = user32.NewProc("FillRect")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
	procSetBkMode        = gdi32.NewProc("SetBkMode")
	procSetTextColor     = gdi32.NewProc("SetTextColor")
	procCreateFontW      = gdi32.NewProc("CreateFontW")
	procSelectObject     = gdi32.NewProc("SelectObject")
	procDrawTextW        = user32.NewProc("DrawTextW")
)

func newDamageOverlayWindow() *damageOverlayWindow {
	return &damageOverlayWindow{fontSize: 48}
}

func (a *App) DamageOverlaySetEnabled(enabled bool) error {
	if a.damageOverlay == nil {
		a.damageOverlay = newDamageOverlayWindow()
	}
	if enabled {
		return a.damageOverlay.start()
	}
	a.damageOverlay.stop()
	return nil
}

func (a *App) DamageOverlaySetValue(value uint64) error {
	if a.damageOverlay == nil {
		return nil
	}
	a.damageOverlay.setValue(value)
	return nil
}

func (a *App) DamageOverlaySetFontSize(size int) error {
	if a.damageOverlay == nil {
		a.damageOverlay = newDamageOverlayWindow()
	}
	a.damageOverlay.setFontSize(size)
	return nil
}

func (o *damageOverlayWindow) start() error {
	o.mu.Lock()
	if o.hwnd != 0 {
		o.mu.Unlock()
		return nil
	}
	o.ready = make(chan error, 1)
	activeDamageOverlay = o
	o.mu.Unlock()

	go o.run()
	return <-o.ready
}

func (o *damageOverlayWindow) stop() {
	o.mu.Lock()
	hwnd := o.hwnd
	o.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
	}
}

func (o *damageOverlayWindow) setValue(value uint64) {
	o.mu.Lock()
	if o.value == value {
		hwnd := o.hwnd
		o.mu.Unlock()
		if hwnd != 0 {
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		}
		return
	}
	o.value = value
	o.startValue = o.displayValue
	o.targetValue = float64(value)
	o.animStart = time.Now()
	o.animating = true
	hwnd := o.hwnd
	o.mu.Unlock()
	if hwnd != 0 {
		procSetTimer.Call(uintptr(hwnd), animationTimerID, 16, 0)
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	}
}

func (o *damageOverlayWindow) setFontSize(size int) {
	if size < 18 {
		size = 18
	}
	if size > 120 {
		size = 120
	}
	o.mu.Lock()
	o.fontSize = size
	hwnd := o.hwnd
	o.mu.Unlock()
	if hwnd != 0 {
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	}
}

func (o *damageOverlayWindow) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := syscall.UTF16PtrFromString(damageOverlayClassName)
	instance, _, _ := procGetModuleHandleW.Call(0)
	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   damageOverlayProc,
		hInstance:     syscall.Handle(instance),
		lpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, err := procCreateWindowExW.Call(
		wsExLayered|wsExTopmost|wsExToolWindow,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		wsPopup,
		uintptr(uint32(cwUseDefault)), uintptr(uint32(cwUseDefault)), 360, 120,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		o.ready <- fmt.Errorf("创建伤害悬浮窗失败: %w", err)
		return
	}

	procSetLayeredAttrs.Call(hwnd, 0, 255, lwaColorKey)
	o.mu.Lock()
	o.hwnd = syscall.Handle(hwnd)
	o.mu.Unlock()
	o.ready <- nil

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)

	var msg [7]uintptr
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
	}
}

func damageOverlayWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmNcHitTest:
		var pt point
		pt.x = int32(int16(lParam & 0xFFFF))
		pt.y = int32(int16((lParam >> 16) & 0xFFFF))
		procScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
		var rc rect
		procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))
		if rc.right-pt.x <= 18 && rc.bottom-pt.y <= 18 {
			return htBottomRight
		}
		return htCaption
	case wmPaint:
		paintDamageOverlay(hwnd)
		return 0
	case wmTimer:
		if activeDamageOverlay != nil {
			activeDamageOverlay.updateAnimation(hwnd)
		}
		return 0
	case wmClose:
		procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
		return 0
	case wmDestroy:
		if activeDamageOverlay != nil {
			activeDamageOverlay.mu.Lock()
			activeDamageOverlay.hwnd = 0
			activeDamageOverlay.mu.Unlock()
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func (o *damageOverlayWindow) updateAnimation(hwnd syscall.Handle) {
	o.mu.Lock()
	if !o.animating {
		o.mu.Unlock()
		procKillTimer.Call(uintptr(hwnd), animationTimerID)
		return
	}
	elapsed := time.Since(o.animStart)
	if elapsed >= animationMs*time.Millisecond {
		o.displayValue = o.targetValue
		o.animating = false
		o.mu.Unlock()
		procKillTimer.Call(uintptr(hwnd), animationTimerID)
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}
	t := float64(elapsed) / float64(animationMs*time.Millisecond)
	t = 1 - (1-t)*(1-t)*(1-t)
	o.displayValue = o.startValue + (o.targetValue-o.startValue)*t
	o.mu.Unlock()
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
}

func paintDamageOverlay(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

	var rc rect
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))
	brush, _, _ := procCreateSolidBrush.Call(0)
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), brush)
	procDeleteObject.Call(brush)

	value := uint64(0)
	fontSize := 48
	if activeDamageOverlay != nil {
		activeDamageOverlay.mu.Lock()
		if activeDamageOverlay.displayValue <= 0 {
			value = activeDamageOverlay.value
		} else {
			value = uint64(activeDamageOverlay.displayValue + 0.5)
		}
		fontSize = activeDamageOverlay.fontSize
		activeDamageOverlay.mu.Unlock()
	}

	fontName, _ := syscall.UTF16PtrFromString("Segoe UI")
	font, _, _ := procCreateFontW.Call(
		uintptr(-fontSize), 0, 0, 0, fwBold, 0, 0, 0, 0, outDefaultPrec, clipDefaultPrec, cleartypeQuality, ffDontCare,
		uintptr(unsafe.Pointer(fontName)),
	)
	oldFont, _, _ := procSelectObject.Call(hdc, font)
	procSetBkMode.Call(hdc, transparentBk)
	procSetTextColor.Call(hdc, 0x00F8E867)
	text, _ := syscall.UTF16PtrFromString(formatOverlayNumber(value))
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), dtCenter|dtVCenter|dtSingleLine)
	procSelectObject.Call(hdc, oldFont)
	procDeleteObject.Call(font)
}

func formatOverlayNumber(value uint64) string {
	text := strconv.FormatUint(value, 10)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}
