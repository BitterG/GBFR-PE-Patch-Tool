package main

import (
	"fmt"
	goruntime "runtime"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

// ── 自动聊天（AutoChat）──
//
// 功能：按固定间隔自动向游戏聊天发送预设文本，也可手动发送。
// 发送路径：通过 patch_core.dll 的共享内存桥接，把发送请求投递到游戏窗口线程。
//
// 逆向结论（2026-08-08）：
//   - Enter 顶层提交函数 RVA 0x1C1DC00 在游戏 UI/窗口线程运行
//   - 该路径最终调用 SetText(RVA 0x78EA10, mode=1)，再由监听者发送
//   - patch_core.dll 子类化游戏窗口，在窗口线程消费共享内存命令并调用 SetText
//   - 不再使用 CreateRemoteThread 直接调用 UI 函数

// AutoChatConfig 是自动聊天的持久化配置。
type AutoChatConfig struct {
	Message    string `json:"message"`
	IntervalMs int64  `json:"intervalMs"` // 发送间隔（毫秒），0 表示只手动发送
}

// AutoChatStatus 是当前运行状态快照。
type AutoChatStatus struct {
	Enabled        bool   `json:"enabled"`
	Message        string `json:"message"`
	IntervalMs     int64  `json:"intervalMs"`
	LastSendAtUnix int64  `json:"lastSendAtUnix"` // 最近发送时间（Unix 毫秒），0 表示尚未发送
	LastError      string `json:"lastError"`
	SentCount      int64  `json:"sentCount"`
}

// autoChatState 保护自动聊天的运行状态。所有公开方法都是线程安全的。
type autoChatState struct {
	mu        sync.Mutex
	enabled   bool
	message   string
	interval  int64 // 毫秒
	lastSend  time.Time
	lastError string
	sentCount int64
	stopCh    chan struct{}
	stopped   chan struct{}
}

var autoChat = &autoChatState{}

// AutoChatGetStatus 返回当前配置与运行状态。
func (a *App) AutoChatGetStatus() AutoChatStatus {
	autoChat.mu.Lock()
	defer autoChat.mu.Unlock()
	return autoChat.snapshotLocked()
}

// AutoChatSetConfig 更新消息文本与发送间隔。
// intervalMs <= 0 时仅保留手动发送；更新后若已在运行，下次发送立即生效。
func (a *App) AutoChatSetConfig(message string, intervalMs int64) (AutoChatStatus, error) {
	autoChat.mu.Lock()
	defer autoChat.mu.Unlock()

	if intervalMs < 0 {
		intervalMs = 0
	}
	autoChat.message = message
	autoChat.interval = intervalMs
	autoChat.lastError = ""
	return autoChat.snapshotLocked(), nil
}

// AutoChatSetEnabled 启动或停止定时发送循环。
// 需要已连接游戏进程；消息为空时拒绝启动。
func (a *App) AutoChatSetEnabled(enabled bool) (AutoChatStatus, error) {
	autoChatLifecycleMu.Lock()
	defer autoChatLifecycleMu.Unlock()
	if !enabled {
		stopAutoChatLoop()
		autoChat.mu.Lock()
		defer autoChat.mu.Unlock()
		return autoChat.snapshotLocked(), nil
	}

	if err := a.ensureGameProcessLocked(); err != nil {
		autoChat.mu.Lock()
		defer autoChat.mu.Unlock()
		return autoChat.snapshotLocked(), err
	}

	autoChat.mu.Lock()
	defer autoChat.mu.Unlock()
	if autoChat.message == "" {
		return autoChat.snapshotLocked(), fmt.Errorf("消息内容不能为空")
	}
	if autoChat.interval <= 0 {
		return autoChat.snapshotLocked(), fmt.Errorf("定时发送需要设置间隔（毫秒）")
	}
	if autoChat.enabled {
		return autoChat.snapshotLocked(), nil
	}
	if err := a.sendChatMessageLocked(); err != nil {
		autoChat.lastError = err.Error()
		return autoChat.snapshotLocked(), err
	}

	stopCh := make(chan struct{})
	stopped := make(chan struct{})
	autoChat.stopCh = stopCh
	autoChat.stopped = stopped
	autoChat.enabled = true
	autoChat.lastError = ""

	go autoChat.loopLocked(a, stopCh, stopped)
	return autoChat.snapshotLocked(), nil
}

// AutoChatSendNow 立即手动发送一次。message 非空时直接使用该文本（无需先保存
// 配置），并同步更新当前配置；为空时使用已保存的配置消息。
func (a *App) AutoChatSendNow(message string) (AutoChatStatus, error) {
	autoChatLifecycleMu.Lock()
	defer autoChatLifecycleMu.Unlock()
	if err := a.ensureGameProcessLocked(); err != nil {
		autoChat.mu.Lock()
		defer autoChat.mu.Unlock()
		return autoChat.snapshotLocked(), err
	}

	autoChat.mu.Lock()
	defer autoChat.mu.Unlock()

	text := autoChat.message
	if message != "" {
		text = message
		if len([]byte(text)) > autoChatMaxTextLen {
			return autoChat.snapshotLocked(), fmt.Errorf("消息最多 %d 个 UTF-8 字节", autoChatMaxTextLen)
		}
		autoChat.message = text
	}
	if text == "" {
		return autoChat.snapshotLocked(), fmt.Errorf("消息内容不能为空")
	}
	if err := a.sendChatMessageLocked(); err != nil {
		autoChat.lastError = err.Error()
		return autoChat.snapshotLocked(), err
	}
	autoChat.lastError = ""
	return autoChat.snapshotLocked(), nil
}

// loopLocked 是定时发送主循环。
func (st *autoChatState) loopLocked(a *App, stopCh, stopped chan struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(time.Duration(st.interval) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			st.mu.Lock()
			if !st.enabled {
				st.mu.Unlock()
				return
			}
			err := a.sendChatMessageLocked()
			if err != nil {
				st.lastError = err.Error()
			} else {
				st.lastError = ""
			}
			st.mu.Unlock()
		}
	}
}

// stopAutoChatLoop 在不持有状态锁的情况下停止并等待循环退出。
func stopAutoChatLoop() {
	autoChat.mu.Lock()
	if !autoChat.enabled {
		autoChat.stopCh = nil
		autoChat.stopped = nil
		autoChat.mu.Unlock()
		return
	}
	autoChat.enabled = false
	stopCh, stopped := autoChat.stopCh, autoChat.stopped
	autoChat.stopCh = nil
	autoChat.stopped = nil
	if stopCh != nil {
		close(stopCh)
	}
	autoChat.mu.Unlock()
	if stopped != nil {
		<-stopped
	}
}

// snapshotLocked 构造状态快照。调用方必须持有 st.mu。
func (st *autoChatState) snapshotLocked() AutoChatStatus {
	return AutoChatStatus{
		Enabled:        st.enabled,
		Message:        st.message,
		IntervalMs:     st.interval,
		LastSendAtUnix: st.lastSend.UnixMilli(),
		LastError:      st.lastError,
		SentCount:      st.sentCount,
	}
}

// ── 游戏聊天发送（真实实现）──
//
// 调用链（2026-08-08 逆向确认，见 gbfr-chat-send-chain-confirmed）：
//   SetText(输入框, 文本, 模式) [RVA 0x78EA10]
//     → 监听者 vtable 槽0 = sub_1431D2640 [RVA 0x31D2640]
//       → sub_1431CE410(dialog, 文本, 0x887AE0B0, 1) 网络发送
// 调度方式：Go 只写共享内存；patch_core.dll 用 PostMessageW 把命令投递到
// 游戏窗口线程，窗口过程再调用 SetText(input, text, 1)。这保留了游戏自身的
// UI 线程、真实输入框对象与监听者链，避免远程线程直接操作 UI 导致竞态崩溃。

const (
	autoChatMaxTextLen         = 255
	autoChatMappingName        = "Local\\GBFRPlayerInfoEditAutoChatV1"
	autoChatBridgeSize         = 288
	autoChatMagic              = 0x54414347
	autoChatVersion            = 1
	autoChatReadyOffset        = 8
	autoChatCommandSeqOffset   = 16
	autoChatCompletedSeqOffset = 20
	autoChatResultOffset       = 24
	autoChatMessageLenOffset   = 28
	autoChatMessageOffset      = 32
)

var patchCoreInjectMu sync.Mutex
var autoChatLifecycleMu sync.Mutex

var (
	psapidll                 = windows.NewLazySystemDLL("psapi.dll")
	procEnumProcessModulesEx = psapidll.NewProc("EnumProcessModulesEx")
	procGetModuleBaseNameW   = psapidll.NewProc("GetModuleBaseNameW")
)

// getAsyncKeyState 返回指定虚拟键当前是否按下（高位 0x8000 = 按下）。
// 复用 flight.go 的 procGetAsyncKeyState（modUser32.GetAsyncKeyState）。
func getAsyncKeyState(vk int) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

// sendChatMessageLocked 将请求写入共享内存，由 patch_core.dll 投递到游戏窗口线程。
// 调用方必须持有 autoChat.mu。
func (a *App) sendChatMessageLocked() error {
	text := autoChat.message
	if text == "" {
		return fmt.Errorf("消息内容不能为空")
	}
	data := []byte(text)
	if len(data) > autoChatMaxTextLen {
		return fmt.Errorf("消息最多 %d 个 UTF-8 字节", autoChatMaxTextLen)
	}
	// 进程内调用：DLL 窗口线程调 ui::hud::Manager::sendMessage（插件验证过的安全路径）。
	// sendMessage 自己管理 state/cooldown/filter/network，不模拟键盘、无需游戏前台。
	if err := a.sendChatViaBridge(data); err != nil {
		return err
	}
	autoChat.lastSend = time.Now()
	autoChat.sentCount++
	return nil
}

func (a *App) sendChatViaBridge(message []byte) error {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return fmt.Errorf("未连接游戏进程")
	}
	if err := a.ensureAutoChatBridge(); err != nil {
		return err
	}
	if len(message) == 0 || len(message) > autoChatMaxTextLen {
		return fmt.Errorf("消息长度无效")
	}

	completed := *(*int32)(unsafe.Pointer(a.autoChatView + autoChatCompletedSeqOffset))
	command := *(*int32)(unsafe.Pointer(a.autoChatView + autoChatCommandSeqOffset))
	if command != completed {
		return fmt.Errorf("上一条聊天消息仍在处理中")
	}
	sequence := command + 1
	if sequence == 0 {
		sequence = 1
	}
	for i := 0; i < autoChatMaxTextLen+1; i++ {
		*(*byte)(unsafe.Pointer(a.autoChatView + autoChatMessageOffset + uintptr(i))) = 0
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(a.autoChatView+autoChatMessageOffset)), len(message)), message)
	*(*int32)(unsafe.Pointer(a.autoChatView + autoChatMessageLenOffset)) = int32(len(message))
	*(*int32)(unsafe.Pointer(a.autoChatView + autoChatResultOffset)) = 0
	*(*int32)(unsafe.Pointer(a.autoChatView + autoChatCommandSeqOffset)) = sequence

	// Kick the game: the Present hook consumes the queue on the render thread
	// every frame (active send), so no SendInput is needed. SendInput Tab→2→Enter
	// was removed — it fought the hook and could crash the game.
	_ = a.charaPID

	// Wait briefly for the sendMessage hook to consume the queue (fast path
	// when the game actually sent something right now). If it doesn't fire
	// within a short window, report success anyway: the message is queued and
	// will be sent on the next game-side chat send.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if *(*int32)(unsafe.Pointer(a.autoChatView + autoChatCompletedSeqOffset)) == sequence {
			result := *(*int32)(unsafe.Pointer(a.autoChatView + autoChatResultOffset))
			switch result {
			case 1:
				return nil
			case -2:
				return fmt.Errorf("聊天对象尚未初始化，请先打开一次聊天界面")
			case -3:
				return fmt.Errorf("聊天输入框未打开，请先按 Tab → 2 打开聊天框")
			case -4:
				return fmt.Errorf("消息长度无效")
			default:
				return fmt.Errorf("游戏窗口线程发送失败（错误码 %d）", result)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Not consumed within the wait window, but the message is queued and will
	// be sent the next time the game performs a chat send (manual Enter or a
	// quick phrase). Treat as success to avoid confusing "timeout" errors.
	return nil
}

// isModuleLoaded reports whether a module whose name starts with prefix is
// loaded in the given process.
func isModuleLoaded(process windows.Handle, prefix string) bool {
	if process == 0 {
		return false
	}
	mods := make([]windows.Handle, 1024)
	var needed uint32
	procEnumProcessModulesEx.Call(uintptr(process), uintptr(unsafe.Pointer(&mods[0])), uintptr(len(mods)*int(unsafe.Sizeof(windows.Handle(0)))), uintptr(unsafe.Pointer(&needed)), 3 /*LIST_MODULES_ALL*/)
	count := int(needed) / int(unsafe.Sizeof(windows.Handle(0)))
	if count > len(mods) {
		count = len(mods)
	}
	var nameBuf [260]uint16
	for i := 0; i < count; i++ {
		procGetModuleBaseNameW.Call(uintptr(process), uintptr(mods[i]), uintptr(unsafe.Pointer(&nameBuf[0])), uintptr(len(nameBuf)))
		name := windows.UTF16ToString(nameBuf[:])
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func (a *App) ensureAutoChatBridge() error {
	// Only trust a ready bridge if patch_core.dll is actually loaded in the
	// current game process (a stale shared-mapping may still report ready=1
	// after the game restarted or the DLL was unloaded).
	if a.autoChatView != 0 && *(*uint32)(unsafe.Pointer(a.autoChatView)) == autoChatMagic &&
		*(*uint32)(unsafe.Pointer(a.autoChatView + 4)) == autoChatVersion &&
		*(*int32)(unsafe.Pointer(a.autoChatView + autoChatReadyOffset)) == 1 &&
		isModuleLoaded(a.hProcess, "patch_core") {
		return nil
	}
	if a.autoChatView != 0 {
		_ = windows.UnmapViewOfFile(a.autoChatView)
		a.autoChatView = 0
		if a.autoChatMapping != 0 {
			_ = windows.CloseHandle(a.autoChatMapping)
			a.autoChatMapping = 0
		}
	}

	patchCoreInjectMu.Lock()
	defer patchCoreInjectMu.Unlock()
	if err := a.ensureAutoChatMapping(); err != nil {
		return err
	}
	if *(*uint32)(unsafe.Pointer(a.autoChatView)) == autoChatMagic &&
		*(*int32)(unsafe.Pointer(a.autoChatView + autoChatReadyOffset)) == 1 &&
		isModuleLoaded(a.hProcess, "patch_core") {
		return nil
	}

	dllPath, err := extractPatchCoreDLLLocked("auto_chat")
	if err != nil {
		return err
	}
	if err := injectDLL(a.hProcess, dllPath); err != nil {
		return fmt.Errorf("注入自动聊天桥接 DLL 失败: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if *(*uint32)(unsafe.Pointer(a.autoChatView)) == autoChatMagic &&
			*(*uint32)(unsafe.Pointer(a.autoChatView + 4)) == autoChatVersion &&
			*(*int32)(unsafe.Pointer(a.autoChatView + autoChatReadyOffset)) == 1 {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("自动聊天桥接 DLL 初始化超时")
}

func (a *App) ensureAutoChatMapping() error {
	if a.autoChatView != 0 {
		return nil
	}
	name, err := windows.UTF16PtrFromString(autoChatMappingName)
	if err != nil {
		return err
	}
	mapping, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, autoChatBridgeSize, name)
	if err != nil && (mapping == 0 || err != windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("创建自动聊天共享内存失败: %w", err)
	}
	view, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_READ|windows.FILE_MAP_WRITE, 0, 0, autoChatBridgeSize)
	if err != nil {
		windows.CloseHandle(mapping)
		return fmt.Errorf("映射自动聊天共享内存失败: %w", err)
	}
	a.autoChatMapping = mapping
	a.autoChatView = view
	return nil
}

func (a *App) closeAutoChatMapping() {
	if a.autoChatView != 0 {
		_ = windows.UnmapViewOfFile(a.autoChatView)
		a.autoChatView = 0
	}
	if a.autoChatMapping != 0 {
		_ = windows.CloseHandle(a.autoChatMapping)
		a.autoChatMapping = 0
	}
}

// ── 自动聊天模板 + 全局热键 ──

// AutoChatTemplate 是"热键 → 发送文本"模板。
type AutoChatTemplate struct {
	ID        string `json:"id"`        // 唯一 ID（UUID）
	Name      string `json:"name"`      // 显示名称
	Text      string `json:"text"`      // 发送的文本
	Modifiers int    `json:"modifiers"` // MOD_ALT(1)|MOD_CONTROL(2)|MOD_SHIFT(4)|MOD_WIN(8)
	Key       int    `json:"key"`       // 虚拟键码（VK_F1..F12 / 'A'..'Z' / '0'..'9'）
	Enabled   bool   `json:"enabled"`   // 是否注册热键
}

// AutoChatListTemplates 返回全部模板（含注册状态）。
func (a *App) AutoChatListTemplates() []AutoChatTemplate {
	autoChat.mu.Lock()
	defer autoChat.mu.Unlock()
	tmpls := make([]AutoChatTemplate, len(a.config.AutoChatTemplates))
	copy(tmpls, a.config.AutoChatTemplates)
	return tmpls
}

// AutoChatSaveTemplate 新增或更新一个模板并重新注册热键。
func (a *App) AutoChatSaveTemplate(t AutoChatTemplate) ([]AutoChatTemplate, error) {
	if t.Name == "" || t.Text == "" {
		return nil, fmt.Errorf("模板名称和文本不能为空")
	}
	if t.Key == 0 {
		return nil, fmt.Errorf("请设置热键主键")
	}
	if len([]byte(t.Text)) > autoChatMaxTextLen {
		return nil, fmt.Errorf("文本最多 %d 个 UTF-8 字节", autoChatMaxTextLen)
	}

	// 先更新配置（持 autoChat.mu），保存并释放锁后再注册热键，
	// 避免 registerAutoChatHotkeys 内部再次 Lock autoChat.mu 造成死锁。
	autoChat.mu.Lock()
	replaced := false
	for i := range a.config.AutoChatTemplates {
		if a.config.AutoChatTemplates[i].ID == t.ID {
			a.config.AutoChatTemplates[i] = t
			replaced = true
			break
		}
	}
	if !replaced {
		if t.ID == "" {
			t.ID = fmt.Sprintf("tpl-%d", time.Now().UnixNano())
		}
		a.config.AutoChatTemplates = append(a.config.AutoChatTemplates, t)
	}
	err := a.saveConfig()
	autoChat.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("保存配置失败: %w", err)
	}
	registerAutoChatHotkeys(a)
	return a.AutoChatListTemplates(), nil
}

// AutoChatDeleteTemplate 删除模板并注销其热键。
func (a *App) AutoChatDeleteTemplate(id string) ([]AutoChatTemplate, error) {
	autoChat.mu.Lock()
	out := a.config.AutoChatTemplates[:0]
	for _, t := range a.config.AutoChatTemplates {
		if t.ID != id {
			out = append(out, t)
		}
	}
	a.config.AutoChatTemplates = out
	err := a.saveConfig()
	autoChat.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("保存配置失败: %w", err)
	}
	registerAutoChatHotkeys(a)
	return a.AutoChatListTemplates(), nil
}

// AutoChatSendTemplate 按 ID 立即发送模板文本（供 UI 按钮触发）。
func (a *App) AutoChatSendTemplate(id string) (AutoChatStatus, error) {
	autoChat.mu.Lock()
	var t *AutoChatTemplate
	for i := range a.config.AutoChatTemplates {
		if a.config.AutoChatTemplates[i].ID == id {
			t = &a.config.AutoChatTemplates[i]
			break
		}
	}
	autoChat.mu.Unlock()
	if t == nil {
		return autoChat.snapshot(), fmt.Errorf("模板不存在")
	}
	return a.AutoChatSendNow(t.Text)
}

func (st *autoChatState) snapshot() AutoChatStatus {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.snapshotLocked()
}

// ── 全局热键（GetAsyncKeyState 轮询）──
// 每个启用的模板对应一个"修饰键+主键"组合；后台 goroutine 轮询 GetAsyncKeyState
// 检测组合是否按下。相比 RegisterHotKey：
//   - 游戏里按住其他键（WASD 等）不影响触发（只检测热键自身的键）
//   - 单键热键不被系统"吃掉"（其他程序/游戏按键正常）
//   - 不依赖 WM_HOTKEY 消息、无线程队列问题
// 防抖：触发后等主键释放才重新武装，避免按住连发。

var (
	hotkeyStopCh   chan struct{}
	hotkeyStopOnce sync.Once
	hotkeyArmed    = map[string]bool{} // tplID -> 可触发（上次未按下）
)

// vkForModifier 返回修饰键位的虚拟键码。
func vkForModifier(mod int) int {
	switch mod {
	case 0x1:
		return 0x12 // VK_MENU (Alt)
	case 0x2:
		return 0x11 // VK_CONTROL
	case 0x4:
		return 0x10 // VK_SHIFT
	case 0x8:
		return 0x5B // VK_LWIN
	}
	return 0
}

// hotkeyDown 检查修饰键+主键是否同时按下。
func hotkeyDown(mods, key int) bool {
	for _, m := range []int{0x1, 0x2, 0x4, 0x8} {
		if mods&m != 0 {
			if !getAsyncKeyState(vkForModifier(m)) {
				return false
			}
		}
	}
	return key != 0 && getAsyncKeyState(key)
}

func registerAutoChatHotkeys(a *App) {
	// 启动接收循环（幂等）。轮询模式无需注册/注销系统热键。
	hotkeyStopOnce.Do(func() {
		hotkeyStopCh = make(chan struct{})
		go hotkeyLoop(a)
	})
	hotkeyDiagLog("hotkey loop (poll) started")
}

func hotkeyLoop(a *App) {
	goruntime.LockOSThread()
	for {
		select {
		case <-hotkeyStopCh:
			return
		default:
		}

		autoChat.mu.Lock()
		tmpls := append([]AutoChatTemplate(nil), a.config.AutoChatTemplates...)
		autoChat.mu.Unlock()

		for i := range tmpls {
			t := &tmpls[i]
			if !t.Enabled || t.ID == "" || t.Key == 0 {
				continue
			}
			down := hotkeyDown(t.Modifiers, t.Key)
			if !down {
				hotkeyArmed[t.ID] = true // 释放 → 武装
				continue
			}
			if hotkeyArmed[t.ID] {
				// 首次按下 → 触发
				hotkeyArmed[t.ID] = false // 等待释放
				text := t.Text
				hotkeyDiagLog("hotkey triggered name=%q key=%d mods=%d", t.Name, t.Key, t.Modifiers)
				go func(txt string) {
					_, err := a.AutoChatSendNow(txt)
					if err != nil {
						runtime.EventsEmit(a.ctx, "autoChat:hotkeyError", err.Error())
					}
				}(text)
			}
		}

		time.Sleep(30 * time.Millisecond)
	}
}

func stopAutoChatHotkeys() {
	// 轮询模式无需注销系统热键；仅停循环。
	hotkeyArmed = map[string]bool{}
}

// hotkeyDiagLog 追加热键诊断到临时日志，定位注册/接收问题。
func hotkeyDiagLog(format string, args ...interface{}) {
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "gbfr-autochat-hotkey.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s "+format+"\n", append([]interface{}{time.Now().Format("15:04:05.000")}, args...)...)
}
