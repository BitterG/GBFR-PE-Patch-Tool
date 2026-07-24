package backend

import (
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	steamAppID  = "881020"
	gameExeName = "granblue_fantasy_relink.exe"
	gameFolder  = "Granblue Fantasy Relink"
	appVersion  = "v1.91.14"
	repoOwner   = "Whitelinker574"
	repoName    = "GBFR-PE-Patch-Tool"
)

//go:embed resources/patch_core.dll
var patchCoreDLL []byte

// ── 状态结构 ──

type StatusInfo struct {
	ExePath      string `json:"exePath"`
	FileExists   bool   `json:"fileExists"`
	FileSize     int64  `json:"fileSize"`
	BackupExists bool   `json:"backupExists"`
	BackupSize   int64  `json:"backupSize"`
}

type UpdateAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type UpdateInfo struct {
	CurrentVersion string        `json:"currentVersion"`
	LatestVersion  string        `json:"latestVersion"`
	HasUpdate      bool          `json:"hasUpdate"`
	Body           string        `json:"body"`
	Assets         []UpdateAsset `json:"assets"`
}

type AppConfig struct {
	LastSavePath string `json:"lastSavePath"`
	WindowWidth  int    `json:"windowWidth"`
	WindowHeight int    `json:"windowHeight"`
}

const (
	defaultAppWidth  = 1280
	defaultAppHeight = 800
	minAppWidth      = 960
	minAppHeight     = 620
	// Persist real desktop sizes instead of forcing every restored/maximised
	// window back into the old 1320×820 review canvas. The upper bound only
	// rejects corrupted configuration values and still covers dual-4K setups.
	maxAppWidth  = 7680
	maxAppHeight = 4320
)

// ── App ──

type App struct {
	ctx           context.Context
	exePath       string
	hProcess      windows.Handle
	moduleBase    uintptr
	managerPtr    uintptr
	charaListBase uintptr
	charaPID      uint32
	charaCreated  uint64
	// Runtime acquire generations and owner tokens are protected by procMu. The
	// request generation is global across features, so an older async Acquire
	// cannot finish late and replace a newer page's owner or runtime resource.
	latestRuntimeAcquireRequestID uint64
	runtimeOwnerSequence          uint64
	charaOwnerToken               string
	sigilMemoryOwnerToken         string
	wrightstoneMemoryOwnerToken   string
	overLimitOwnerToken           string
	// liveMemoryIndeterminateProcess poisons runtime item writes after a remote
	// save thread times out. Creation time is part of the identity because
	// Windows may reuse a PID after the old game process exits.
	liveMemoryIndeterminateProcess processInstanceID
	countdownAddr                  uintptr
	faceAccessoryAddr              uintptr
	overLimitHookAddr              uintptr
	overLimitCaveAddr              uintptr
	unlockAllTrophyAddr            uintptr
	terminusDropAddr               uintptr
	terminusDropOrig               []byte
	collectibleTaskBase            uintptr
	sigilMemoryHookAddr            uintptr
	sigilMemoryCaveAddr            uintptr
	sigilMemoryOriginal            []byte
	wrightstoneMemoryHookAddr      uintptr
	wrightstoneMemoryCaveAddr      uintptr
	wrightstoneMemoryOriginal      []byte
	currencyHookAddr               uintptr
	currencyCaveAddr               uintptr
	currencyOriginal               []byte
	// monsterEnhanceOwned contains only patches installed and fully verified by
	// this App instance. Exact entry bytes, rel32 cave and an in-cave marker are
	// retained until restoration succeeds, so detach can fail closed and retry.
	monsterEnhanceOwned map[string]monsterEnhanceOwnedPatch
	// runtimePatchPatchLeases owns only independently verified direct patches. The
	// process identity and exact bytes make every record a retryable recovery
	// lease; runtimePatchPatchOrder preserves reverse installation order on detach.
	runtimePatchPatchLeases map[string]runtimePatchPatchLease
	runtimePatchPatchOrder  []string
	// The selected-item monitor uses two independent read-only address-capture
	// hooks. Keep exact recovery evidence until both entry restoration and
	// tool-owned cave-pointer clearing are proven.
	runtimePatchSelectedMaterialHook runtimePatchSelectedCaptureLease
	runtimePatchSelectedKeyItemHook  runtimePatchSelectedCaptureLease
	// retiredRuntimeCaves were reachable from a published entry whose original
	// bytes are now proven restored. They intentionally remain mapped until the
	// game exits because entry restoration cannot quiesce an in-flight thread.
	// The metadata is dropped only when this process connection is detached.
	retiredRuntimeCaves         []retiredRuntimeCave
	materialConsumeAddr         uintptr
	infiniteChallengeAddr       uintptr
	infiniteChallengeOwnerToken string
	// runtimePatchMu serializes the two features sharing RVA 0x356621. Their
	// read/validate/write sequence must be atomic or concurrent Wails calls can
	// both observe the original bytes and then overwrite each other.
	runtimePatchMu sync.Mutex
	// procMu serializes the game-process handle lifecycle (open/close/swap) so
	// two Wails-dispatched goroutines cannot double-close the handle, leak a
	// handle via a check-then-act race in ensureGameProcess, or observe a torn
	// hProcess/moduleBase/{PID, Created} transition. Held only by lifecycle
	// transitions (CharaAttach/CharaDetach/ensureGameProcess); the unexported
	// charaDetachLocked variant runs the detach body without re-locking.
	procMu sync.Mutex
	// formulaSamplerMu protects a separate strict read-only process handle and
	// its A/B/A/B evidence state. It never shares the editor lifecycle above.
	formulaSamplerMu         sync.Mutex
	formulaSamplerSession    *formulaSamplerSession
	formulaSamplerGeneration uint64
	damageMeterMapping       windows.Handle
	damageMeterView          uintptr
	// damageMu guards the damage-meter shared-memory lifecycle and every mapped
	// view read/write. Without it frontend polling could race shutdown after the
	// view was unmapped.
	damageMu      sync.Mutex
	damageOverlay *damageOverlayWindow
	config        AppConfig
	configLoaded  bool
}

var (
	liveMemoryWriteMu             sync.Mutex
	errLiveMemoryRollbackUnproven = errors.New("实时内存事务回滚未能完成并回读验证")
	errRuntimeAcquireRequestStale = errors.New("运行时连接请求已过期")
	errRuntimeOwnerLeaseStale     = errors.New("运行时页面所有权已过期")
	closeMessageDialog            = runtime.MessageDialog
)

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.loadConfig(); err != nil {
		return
	}
	width, height := a.config.windowSize()
	if width > 0 && height > 0 {
		runtime.WindowSetSize(ctx, width, height)
	}
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	_ = a.closeFormulaSampler()
	if handleDetachBeforeClose(ctx, a.CharaDetach()) {
		return true
	}
	a.saveWindowSize(ctx)
	return false
}

func (a *App) shutdown(ctx context.Context) {
	a.saveWindowSize(ctx)
	_ = a.closeFormulaSampler()
	if a.damageOverlay != nil {
		a.damageOverlay.stop()
	}
	a.closeDamageMeter()
	if err := a.CharaDetach(); err != nil {
		logPath := appendDiagnosticError("shutdown hook restoration", err)
		runtime.LogErrorf(ctx, "关闭时恢复运行时 Hook 失败；诊断日志：%s；错误：%v", logPath, err)
	}
}

func handleDetachBeforeClose(ctx context.Context, detachErr error) bool {
	if detachErr == nil {
		return false
	}
	logPath := appendDiagnosticError("before-close hook restoration", detachErr)
	_, _ = closeMessageDialog(ctx, runtime.MessageDialogOptions{
		Type:  runtime.ErrorDialog,
		Title: "无法安全关闭 GBFR PE Patch Tool",
		Message: fmt.Sprintf(
			"工具未能恢复游戏进程中的运行时 Hook，因此已阻止关闭，以免游戏继续跳转到工具管理的内存。\n\n请保持工具开启，退出游戏后再关闭工具，或再次尝试关闭。\n\n错误：%v\n\n诊断日志：%s",
			detachErr, logPath,
		),
		Buttons:       []string{"确定"},
		DefaultButton: "确定",
	})
	return true
}

func (a *App) saveWindowSize(ctx context.Context) {
	width, height := runtime.WindowGetSize(ctx)
	if width <= 0 || height <= 0 {
		return
	}
	if err := a.loadConfig(); err != nil {
		return
	}
	a.config.WindowWidth = width
	a.config.WindowHeight = height
	_ = a.saveConfig()
}

func (c AppConfig) windowSize() (int, int) {
	if c.WindowWidth < 400 || c.WindowHeight < 300 {
		return 0, 0
	}
	return max(minAppWidth, min(c.WindowWidth, maxAppWidth)), max(minAppHeight, min(c.WindowHeight, maxAppHeight))
}

func (a *App) configFilePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "gbfr-player-info-edit", "config.json"), nil
}

func (a *App) loadConfig() error {
	if a.configLoaded {
		return nil
	}
	a.configLoaded = true
	path, err := a.configFilePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			a.config = AppConfig{}
			return nil
		}
		return err
	}
	if len(data) == 0 {
		a.config = AppConfig{}
		return nil
	}
	if err := json.Unmarshal(data, &a.config); err != nil {
		a.config = AppConfig{}
		return nil
	}
	return nil
}

func (a *App) saveConfig() error {
	path, err := a.configFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) GetLastSavePath() (string, error) {
	if err := a.loadConfig(); err != nil {
		return "", err
	}
	return strings.TrimSpace(a.config.LastSavePath), nil
}

func (a *App) SetLastSavePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := a.loadConfig(); err != nil {
		return err
	}
	a.config.LastSavePath = path
	return a.saveConfig()
}

func (a *App) GetAppVersion() string {
	return appVersion
}

func (a *App) CheckUpdate() (UpdateInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return UpdateInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", repoName+"/"+appVersion)

	resp, err := client.Do(req)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("检查更新失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("检查更新失败: GitHub 返回 %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return UpdateInfo{}, fmt.Errorf("解析更新信息失败: %w", err)
	}

	info := UpdateInfo{
		CurrentVersion: appVersion,
		LatestVersion:  release.TagName,
		HasUpdate:      compareVersionTags(release.TagName, appVersion) > 0,
		Body:           release.Body,
	}
	for _, asset := range release.Assets {
		info.Assets = append(info.Assets, UpdateAsset{Name: asset.Name, URL: asset.URL})
	}
	return info, nil
}

func (a *App) OpenReleasePage() error {
	url := fmt.Sprintf("https://github.com/%s/%s/releases", repoOwner, repoName)
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

func compareVersionTags(a, b string) int {
	ap := parseVersionTag(a)
	bp := parseVersionTag(b)
	for i := 0; i < len(ap); i++ {
		if ap[i] > bp[i] {
			return 1
		}
		if ap[i] < bp[i] {
			return -1
		}
	}
	return 0
}

func parseVersionTag(tag string) [3]int {
	var parts [3]int
	cleaned := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	fields := strings.Split(cleaned, ".")
	for i := 0; i < len(parts) && i < len(fields); i++ {
		text := fields[i]
		if idx := strings.IndexAny(text, "-+"); idx >= 0 {
			text = text[:idx]
		}
		if n, err := strconv.Atoi(text); err == nil {
			parts[i] = n
		}
	}
	return parts
}

// AutoDetect 自动扫描 Steam 安装路径
func (a *App) AutoDetect() string {
	for _, dir := range findSteamLibraryFolders() {
		candidate := filepath.Join(dir, "steamapps", "common", gameFolder, gameExeName)
		if _, err := os.Stat(candidate); err == nil {
			a.exePath = candidate
			return candidate
		}
	}
	return ""
}

// SetExePath 手动设置 exe 路径
func (a *App) SetExePath(path string) (StatusInfo, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return StatusInfo{}, fmt.Errorf("文件不存在: %s", path)
	}
	a.exePath = path
	return a.GetStatus(""), nil
}

// GetStatus 获取游戏文件及备份状态
func (a *App) GetStatus(exePath string) StatusInfo {
	if exePath != "" {
		a.exePath = exePath
	}
	info := StatusInfo{ExePath: a.exePath}
	if a.exePath == "" {
		return info
	}

	bakPath := a.exePath + ".bak"
	if fi, err := os.Stat(a.exePath); err == nil {
		info.FileExists = true
		info.FileSize = fi.Size()
	}
	if fi, err := os.Stat(bakPath); err == nil {
		info.BackupExists = true
		info.BackupSize = fi.Size()
	}
	return info
}

// BackupFile 创建备份
func (a *App) BackupFile(force bool) error {
	if a.exePath == "" {
		return fmt.Errorf("未选择文件")
	}
	bakPath := a.exePath + ".bak"
	if _, err := os.Stat(a.exePath); os.IsNotExist(err) {
		return fmt.Errorf("目标文件不存在")
	}
	if !force {
		if _, err := os.Stat(bakPath); err == nil {
			return fmt.Errorf("备份已存在，使用强制覆盖选项")
		}
	}
	data, err := os.ReadFile(a.exePath)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}
	return os.WriteFile(bakPath, data, 0644)
}

// RestoreFile 从备份恢复
func (a *App) RestoreFile() error {
	if a.exePath == "" {
		return fmt.Errorf("未选择文件")
	}
	bakPath := a.exePath + ".bak"
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		return fmt.Errorf("备份文件不存在")
	}
	data, err := os.ReadFile(bakPath)
	if err != nil {
		return fmt.Errorf("读取备份失败: %w", err)
	}
	return os.WriteFile(a.exePath, data, 0644)
}

// ── PE / 工具函数 ──

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bytesToHex(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	return strings.Join(parts, " ")
}

// ── Steam 路径扫描 ──

func findSteamLibraryFolders() []string {
	var dirs []string
	steamPath := ""
	for _, keyPath := range []string{`SOFTWARE\Valve\Steam`, `SOFTWARE\WOW6432Node\Valve\Steam`} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		val, _, err := k.GetStringValue("InstallPath")
		k.Close()
		if err == nil && val != "" {
			steamPath = val
			dirs = append(dirs, val)
			break
		}
	}
	if steamPath == "" {
		k, err := registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\Valve\Steam`, registry.QUERY_VALUE)
		if err == nil {
			val, _, err := k.GetStringValue("SteamPath")
			k.Close()
			if err == nil && val != "" {
				steamPath = filepath.FromSlash(val)
				dirs = append(dirs, steamPath)
			}
		}
	}
	if steamPath != "" {
		vdfPath := filepath.Join(steamPath, "steamapps", "libraryfolders.vdf")
		if data, err := os.ReadFile(vdfPath); err == nil {
			dirs = append(dirs, parseLibraryPaths(string(data))...)
		}
	}
	for _, fb := range []string{
		`C:\Program Files (x86)\Steam`, `C:\Program Files\Steam`,
		`D:\Steam`, `D:\SteamLibrary`, `E:\Steam`, `E:\SteamLibrary`,
	} {
		found := false
		for _, d := range dirs {
			if strings.EqualFold(d, fb) {
				found = true
				break
			}
		}
		if !found {
			dirs = append(dirs, fb)
		}
	}
	return dirs
}

func parseLibraryPaths(content string) []string {
	var paths []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"path"`) {
			parts := strings.SplitN(line, `"path"`, 2)
			if len(parts) < 2 {
				continue
			}
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"`)
			val = strings.ReplaceAll(val, `\\`, `\`)
			if val != "" {
				paths = append(paths, val)
			}
		}
	}
	return paths
}

// ── 角色使用次数 (运行时内存读写) ──

const (
	charaProcessName = "granblue_fantasy_relink.exe"
	charaStride      = 0x5B70
	charaCountOffset = 0x68
	charaStateOffset = 0x6C
	maxCharacters    = 40
)

var charaNames = [maxCharacters]string{
	"古兰", "姬塔", "卡塔莉娜", "拉卡姆", "伊欧", "欧根",
	"", "萝赛塔", "冈达葛萨", "菲莉", "兰斯洛特", "巴恩", "珀西瓦尔",
	"", "齐格飞", "夏洛特", "索恩", "尤达拉哈", "娜露梅",
	"", "塞达", "伊德", "巴萨拉卡",
	"", "卡莉奥丝特罗",
	"", "", "圣德芬", "希耶提",
	"", "", "", "", "", "", "", "", "", "", "",
}

type CharaProcessInfo struct {
	PID        uint32 `json:"pid"`
	ModuleBase uint64 `json:"moduleBase"`
	Manager    uint64 `json:"manager"`
	Connected  bool   `json:"connected"`
	OwnerToken string `json:"ownerToken,omitempty"`
}

type CharaInfo struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Count int32  `json:"count"`
}

type CurrencyInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	RVA     uint64 `json:"rva"`
	Offset  uint64 `json:"offset"`
	Address uint64 `json:"address"`
	Value   int32  `json:"value"`
}

type PotionInfo struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	RVA     uint64   `json:"rva"`
	Offsets []uint64 `json:"offsets"`
	Address uint64   `json:"address"`
	Value   int32    `json:"value"`
}

type currencyDef struct {
	ID     string
	Name   string
	Offset uintptr
}

var currencyDefs = []currencyDef{
	{ID: "rupies", Name: "金币", Offset: 0x30},
	{ID: "transmarvel", Name: "高级炼成点数", Offset: 0x34},
	{ID: "msp", Name: "MSP", Offset: 0x98},
	{ID: "rp", Name: "共鸣点数（RP）", Offset: 0x9C},
}

func lookupCurrencyDef(id string) (currencyDef, bool) {
	id = strings.TrimSpace(id)
	if id == "cp" {
		id = "rp"
	}
	for _, def := range currencyDefs {
		if def.ID == id {
			return def, true
		}
	}
	return currencyDef{}, false
}

type potionDef struct {
	ID      string
	Name    string
	RVA     uintptr
	Offsets []uintptr
}

var potionDefs = []potionDef{
	{ID: "revive", Name: "复活药水", RVA: 0x071B69B8, Offsets: []uintptr{0x28, 0x8, 0x8, 0x18, 0x38}},
	{ID: "group_chat", Name: "群疗药水", RVA: 0x071B69B8, Offsets: []uintptr{0x28, 0x8, 0x8, 0x18, 0x18}},
}

// CharaAttach finds the game process, opens a handle, reads module base and manager pointer.
func (a *App) CharaAttach() (CharaProcessInfo, error) {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if len(a.runtimePatchPatchLeases) != 0 || len(a.runtimePatchPatchOrder) != 0 {
		return CharaProcessInfo{}, fmt.Errorf("实时补丁仍由当前页面持有，请先安全释放")
	}
	if a.hasRuntimePatchSelectedCaptureLeaseLocked() {
		return CharaProcessInfo{}, fmt.Errorf("选中物品捕获仍由当前页面持有，请先安全释放")
	}
	if len(a.monsterEnhanceOwned) != 0 {
		return CharaProcessInfo{}, fmt.Errorf("怪物增强 Hook 仍由当前页面持有，请先关闭或断开该页面")
	}
	info, err := a.charaAttachLocked()
	if err == nil {
		// Compatibility callers deliberately take an unowned connection. This
		// invalidates any earlier owned cleanup without requiring a token.
		a.charaOwnerToken = ""
	}
	return info, err
}

// CharaAcquire attaches to the game and rotates the frontend owner lease.
func (a *App) CharaAcquire(requestID uint64) (CharaProcessInfo, error) {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if err := a.acceptRuntimeAcquireRequestLocked(requestID); err != nil {
		return CharaProcessInfo{}, err
	}
	if len(a.runtimePatchPatchLeases) != 0 || len(a.runtimePatchPatchOrder) != 0 {
		return CharaProcessInfo{}, fmt.Errorf("实时补丁由另一运行时页面持有，请先安全释放")
	}
	if a.hasRuntimePatchSelectedCaptureLeaseLocked() {
		return CharaProcessInfo{}, fmt.Errorf("选中物品捕获由另一运行时页面持有，请先安全释放")
	}
	if len(a.monsterEnhanceOwned) != 0 {
		return CharaProcessInfo{}, fmt.Errorf("怪物增强 Hook 仍由另一个页面持有，请等待该页面完成安全释放后重试")
	}
	info, err := a.charaAttachLocked()
	if err != nil {
		return CharaProcessInfo{}, err
	}
	return a.grantCharaOwner(info), nil
}

func (a *App) charaAttachLocked() (CharaProcessInfo, error) {
	pid, err := findProcessByName(charaProcessName)
	if err != nil {
		return CharaProcessInfo{}, fmt.Errorf("未找到游戏进程，请先启动游戏")
	}
	if canReuseGameProcess(a.charaPID, pid, a.hProcess != 0, a.moduleBase != 0, processHandleAlive(a.hProcess)) {
		return CharaProcessInfo{
			PID:        pid,
			ModuleBase: uint64(a.moduleBase),
			Connected:  true,
		}, nil
	}
	// A repeated connection to the same live PID is intentionally idempotent.
	// Only a dead/replaced process tears down hooks and address-derived state.
	if a.hProcess != 0 || a.moduleBase != 0 || a.charaPID != 0 {
		if err := a.charaDetachLocked(); err != nil {
			return CharaProcessInfo{}, fmt.Errorf("cannot safely replace the current game-process connection: %w", err)
		}
	}

	h, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, pid)
	if err != nil {
		return CharaProcessInfo{}, fmt.Errorf("无法打开进程 (错误 %v)，请以管理员身份运行", err)
	}

	modBase, err := getModuleBase(h)
	if err != nil {
		windows.CloseHandle(h)
		return CharaProcessInfo{}, fmt.Errorf("无法获取模块基址 (ptrSize=%d): %v", unsafe.Sizeof(uintptr(0)), err)
	}
	created, err := processCreationTime(h)
	if err != nil {
		windows.CloseHandle(h)
		return CharaProcessInfo{}, fmt.Errorf("无法读取游戏进程创建时间: %v", err)
	}

	a.hProcess = h
	a.moduleBase = modBase
	a.charaPID = pid
	a.charaCreated = created
	a.clearLiveMemoryPoisonForNewProcess(a.currentProcessInstance())

	return CharaProcessInfo{
		PID:        pid,
		ModuleBase: uint64(modBase),
		Connected:  true,
	}, nil
}

// charaManager locates current 40-entry runtime character-use list.
// Game 1.7.5 stores records 0x5B70 bytes apart; use count is at +0x68.
func (a *App) charaManager() (uintptr, error) {
	if a.hProcess == 0 {
		return 0, fmt.Errorf("未连接游戏进程")
	}
	if a.charaListBase != 0 {
		if a.isCharaListAddress(a.charaListBase) {
			return a.charaListBase, nil
		}
		a.charaListBase = 0
	}

	const (
		memCommit  = 0x1000
		memPrivate = 0x20000
	)
	const chunkSize = uintptr(0x100000)
	const listSize = uintptr((maxCharacters-1)*charaStride + charaStateOffset + 4)
	for addr := uintptr(0); ; {
		var mbi memoryBasicInformation
		ret, _, _ := procVirtualQueryEx.Call(uintptr(a.hProcess), addr, uintptr(unsafe.Pointer(&mbi)), unsafe.Sizeof(mbi))
		if ret == 0 {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		if mbi.State == memCommit && mbi.Type == memPrivate && mbi.RegionSize >= listSize {
			for off := uintptr(0); off+listSize <= mbi.RegionSize; off += chunkSize {
				size := chunkSize
				if off+size > mbi.RegionSize {
					size = mbi.RegionSize - off
				}
				if size < listSize {
					break
				}
				buf := make([]byte, size)
				chunkBase := mbi.BaseAddress + off
				if err := readProcessMemory(a.hProcess, chunkBase, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
					continue
				}
				for countIndex := int(charaCountOffset); countIndex+int(listSize)-int(charaCountOffset) <= len(buf); countIndex += 8 {
					if binary.LittleEndian.Uint32(buf[countIndex:]) == 0 || !isCharaListData(buf, countIndex) {
						continue
					}
					base := chunkBase + uintptr(countIndex) - charaCountOffset
					a.charaListBase = base
					a.managerPtr = base
					return base, nil
				}
			}
		}
		if next <= addr {
			break
		}
		addr = next
	}
	return 0, fmt.Errorf("未定位角色场次列表，请先进入游戏存档")
}

type CollectibleTaskStatus struct {
	Found     bool   `json:"found"`
	Address   uint64 `json:"address"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
}

const (
	collectibleTaskEntries = 45
	collectibleTaskStride  = uintptr(0xC)
	collectibleTaskFlag    = uintptr(0x8)
)

func (a *App) CollectibleTaskComplete() (CollectibleTaskStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return CollectibleTaskStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return CollectibleTaskStatus{}, err
	}
	return a.collectibleTaskCompleteLocked()
}

func (a *App) CollectibleTaskCompleteOwned(token string) (CollectibleTaskStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return CollectibleTaskStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return CollectibleTaskStatus{}, err
	}
	return a.collectibleTaskCompleteLocked()
}

func (a *App) collectibleTaskCompleteLocked() (CollectibleTaskStatus, error) {
	base, err := a.collectibleTaskAddress()
	if err != nil {
		return CollectibleTaskStatus{}, err
	}

	original := make([]byte, collectibleTaskEntries*int(collectibleTaskStride))
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&original[0]), uintptr(len(original))); err != nil {
		return CollectibleTaskStatus{}, fmt.Errorf("读取收集任务列表失败: %w", err)
	}
	desired := append([]byte(nil), original...)
	for i := 0; i < collectibleTaskEntries; i++ {
		desired[i*int(collectibleTaskStride)+int(collectibleTaskFlag)] = 1
	}
	if err := snapshotBeforeLiveSaveChange("小钳蟹收集任务写入前自动备份"); err != nil {
		return CollectibleTaskStatus{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}
	confirmedBase, err := a.collectibleTaskAddress()
	if err != nil {
		return CollectibleTaskStatus{}, fmt.Errorf("自动备份后复核收集任务列表失败: %w", err)
	}
	confirmed := make([]byte, len(original))
	if err := readProcessMemory(a.hProcess, confirmedBase, unsafe.Pointer(&confirmed[0]), uintptr(len(confirmed))); err != nil {
		return CollectibleTaskStatus{}, fmt.Errorf("自动备份后复核收集任务内容失败: %w", err)
	}
	if confirmedBase != base || !bytesEqual(confirmed, original) {
		return CollectibleTaskStatus{}, fmt.Errorf("自动备份期间收集任务列表已变化，请刷新后重试")
	}
	if err := a.writeBytesTransactionalLocked(base, original, desired, "小钳蟹收集任务"); err != nil {
		return CollectibleTaskStatus{}, err
	}
	return CollectibleTaskStatus{Found: true, Address: uint64(base), Completed: collectibleTaskEntries, Total: collectibleTaskEntries}, nil
}

func (a *App) writeBytesTransactionalLocked(addr uintptr, original, desired []byte, label string) error {
	if len(original) == 0 || len(original) != len(desired) {
		return fmt.Errorf("%s写入参数无效", label)
	}
	write := func(data []byte) error {
		return writeProcessMemory(a.hProcess, addr, unsafe.Pointer(&data[0]), uintptr(len(data)))
	}
	read := func() ([]byte, error) {
		data := make([]byte, len(original))
		err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&data[0]), uintptr(len(data)))
		return data, err
	}
	rollback := func(cause error) error {
		rollbackErr := write(original)
		rolledBack, verifyErr := read()
		if rollbackErr == nil && verifyErr == nil && bytesEqual(rolledBack, original) {
			return fmt.Errorf("%s写入未获确认，已恢复原记录: %w", label, cause)
		}
		a.poisonCurrentLiveMemoryWrites()
		if verifyErr == nil && !bytesEqual(rolledBack, original) {
			verifyErr = fmt.Errorf("回滚后的完整记录不一致")
		}
		return errors.Join(
			fmt.Errorf("%s写入状态无法确认，已隔离当前游戏进程的后续写入: %w", label, cause),
			rollbackErr,
			verifyErr,
		)
	}
	if err := write(desired); err != nil {
		return rollback(fmt.Errorf("写入失败: %w", err))
	}
	actual, err := read()
	if err != nil {
		return rollback(fmt.Errorf("写后回读失败: %w", err))
	}
	if !bytesEqual(actual, desired) {
		return rollback(fmt.Errorf("写后完整记录不一致"))
	}
	return nil
}

func (a *App) collectibleTaskAddress() (uintptr, error) {
	if a.collectibleTaskBase != 0 && a.isCollectibleTaskAddress(a.collectibleTaskBase) {
		return a.collectibleTaskBase, nil
	}
	a.collectibleTaskBase = 0

	const (
		memCommit  = 0x1000
		memPrivate = 0x20000
		chunkSize  = uintptr(0x100000)
	)
	var matches []uintptr
	for addr := uintptr(0); ; {
		var mbi memoryBasicInformation
		ret, _, _ := procVirtualQueryEx.Call(uintptr(a.hProcess), addr, uintptr(unsafe.Pointer(&mbi)), unsafe.Sizeof(mbi))
		if ret == 0 {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		if mbi.State == memCommit && mbi.Type == memPrivate && mbi.RegionSize >= 16 {
			for off := uintptr(0); off < mbi.RegionSize; off += chunkSize {
				size := chunkSize
				if off+size > mbi.RegionSize {
					size = mbi.RegionSize - off
				}
				if size < 16 {
					continue
				}
				buf := make([]byte, size)
				chunkBase := mbi.BaseAddress + off
				if err := readProcessMemory(a.hProcess, chunkBase, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
					continue
				}
				for i := 0; i+16 <= len(buf); i += 8 {
					base := uintptr(binary.LittleEndian.Uint64(buf[i:]))
					end := uintptr(binary.LittleEndian.Uint64(buf[i+8:]))
					if end-base != collectibleTaskEntries*collectibleTaskStride || !a.isCollectibleTaskAddress(base) {
						continue
					}
					matches = append(matches, base)
					if len(matches) > 1 {
						return 0, fmt.Errorf("收集任务列表命中多个位置，请重进存档后重试")
					}
				}
			}
		}
		if next <= addr {
			break
		}
		addr = next
	}
	if len(matches) == 0 {
		return 0, fmt.Errorf("未定位收集任务列表，请先进入包含该任务的存档")
	}
	a.collectibleTaskBase = matches[0]
	return matches[0], nil
}

func (a *App) isCollectibleTaskAddress(base uintptr) bool {
	if base == 0 {
		return false
	}
	data := make([]byte, collectibleTaskEntries*int(collectibleTaskStride))
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&data[0]), uintptr(len(data))); err != nil {
		return false
	}
	for i := 0; i < collectibleTaskEntries; i++ {
		entry := i * int(collectibleTaskStride)
		if binary.LittleEndian.Uint32(data[entry:]) == 0 || data[entry+int(collectibleTaskFlag)] > 1 {
			return false
		}
	}
	return true
}

func (a *App) isCharaListAddress(base uintptr) bool {
	var data [maxCharacters * 8]byte
	for i := 0; i < maxCharacters; i++ {
		addr := base + uintptr(i)*charaStride + charaCountOffset
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&data[i*8]), 8); err != nil {
			return false
		}
	}
	return isCharaListData(data[:], 0)
}

func isCharaListData(data []byte, countIndex int) bool {
	active := 0
	positive := 0
	for i := 0; i < maxCharacters; i++ {
		offset := countIndex + i*charaStride
		count := int32(binary.LittleEndian.Uint32(data[offset:]))
		state := int32(binary.LittleEndian.Uint32(data[offset+charaStateOffset-charaCountOffset:]))
		if count < 0 || count > 10_000_000 || (state != 0 && state != -1) {
			return false
		}
		if state == 0 {
			active++
			if count > 0 {
				positive++
			}
		}
	}
	return active >= 20 && positive >= 3
}

// CharaDetach restores owned runtime hooks before closing the process handle.
func (a *App) CharaDetach() error {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	a.procMu.Lock()
	defer a.procMu.Unlock()
	err := a.charaDetachLocked()
	if err == nil {
		a.charaOwnerToken = ""
	}
	return err
}

// CharaRelease detaches only when token still owns the logical connection.
// A stale cleanup is an idempotent no-op and cannot close a newer page's lease.
func (a *App) CharaRelease(token string) error {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if !runtimeOwnerTokenMatches(a.charaOwnerToken, token) {
		return nil
	}
	processLive := a.hProcess != 0 && processHandleAlive(a.hProcess)
	if processLive {
		a.runtimePatchMu.Lock()
		selectedErr := a.releaseRuntimePatchSelectedCaptureHooksLocked(token, false)
		ctErr := a.restoreAllRuntimePatchPatchesLocked(token)
		challengeErr := a.restoreInfiniteChallengeOwnedLocked(token, false)
		a.runtimePatchMu.Unlock()
		if combined := errors.Join(selectedErr, ctErr, challengeErr); combined != nil {
			return fmt.Errorf("runtime restoration failed; connection remains owned: %w", combined)
		}
		if err := a.restoreMonsterEnhanceOwned(token, "all", false); err != nil {
			return fmt.Errorf("monster-enhance hook restoration failed; connection remains owned: %w", err)
		}
	} else {
		a.dropRuntimePatchSelectedCaptureHooksLocked(token, false)
		a.dropRuntimePatchPatchesForOwnerLocked(token)
		if runtimeOwnerTokenMatches(a.infiniteChallengeOwnerToken, token) {
			a.infiniteChallengeOwnerToken = ""
			a.infiniteChallengeAddr = 0
		}
		// Once the game process is gone there is no executable jump left to
		// restore. Consume only this page's monster recovery records; unrelated
		// runtime owners keep their existing detach semantics below.
		for id, record := range a.monsterEnhanceOwned {
			if record.OwnerToken == token {
				delete(a.monsterEnhanceOwned, id)
			}
		}
		if len(a.monsterEnhanceOwned) == 0 {
			a.monsterEnhanceOwned = nil
		}
	}
	// Currency capture is installed by the misc-tools page under this exact
	// Chara owner. Restore it before consuming the token; on any proof failure
	// retain both token and process handle so the lease manager can retry.
	if processLive &&
		(a.currencyHookAddr != 0 || a.currencyCaveAddr != 0 || len(a.currencyOriginal) != 0) {
		if err := a.releaseCurrencyHook(); err != nil {
			return fmt.Errorf("currency hook restoration failed; connection remains owned: %w", err)
		}
	}
	// A character page owns only the shared process connection. Once another
	// runtime page has installed a hook (or retained recovery state for one), a
	// delayed character cleanup must not use that broader connection lease to
	// tear the newer hook down. Consume this token so it cannot be replayed after
	// the hook is later released.
	if a.hasActiveRuntimeHookLeaseLocked() {
		a.charaOwnerToken = ""
		return nil
	}
	err := a.charaDetachLocked()
	if err == nil {
		a.charaOwnerToken = ""
	}
	return err
}

// charaDetachLocked runs the detach body assuming the caller already holds
// procMu (ensureGameProcess/CharaAttach call it during a handle swap).
func (a *App) charaDetachLocked() error {
	// Restore hooks while the target process is still available. Leaving the
	// jump installed makes a later tool instance mistake it for an unsupported
	// game build and can also leave the game executing tool-owned code.
	if a.hProcess != 0 && processHandleAlive(a.hProcess) {
		var releaseErr error
		a.runtimePatchMu.Lock()
		if err := a.restoreInfiniteChallengeOwnedLocked("", true); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("continuous challenge: %w", err))
		}
		if err := a.releaseRuntimePatchSelectedCaptureHooksLocked("", true); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("selected-item capture: %w", err))
		}
		if err := a.restoreAllRuntimePatchPatchesLocked(""); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("live patches: %w", err))
		}
		a.runtimePatchMu.Unlock()
		if err := a.releaseOverLimitHook(); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("OverLimit hook: %w", err))
		}
		if err := a.releaseSigilMemoryHook(); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("sigil-memory hook: %w", err))
		}
		if err := a.releaseWrightstoneMemoryHook(); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("wrightstone-memory hook: %w", err))
		}
		if err := a.releaseCurrencyHook(); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("currency hook: %w", err))
		}
		if err := a.restoreMonsterEnhanceOwned("", "all", true); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("monster-enhance hook: %w", err))
		}
		if releaseErr != nil {
			// Keep the live handle and every unresolved address so a later detach
			// can retry. Closing now would discard our only recovery lease while
			// the game may still jump into tool-owned memory.
			return fmt.Errorf("runtime hook restoration failed; process remains attached: %w", releaseErr)
		}
	}
	if a.hProcess != 0 {
		windows.CloseHandle(a.hProcess)
		a.hProcess = 0
	}
	a.moduleBase = 0
	a.managerPtr = 0
	a.charaListBase = 0
	a.charaPID = 0
	a.charaCreated = 0
	a.countdownAddr = 0
	a.faceAccessoryAddr = 0
	a.overLimitHookAddr = 0
	a.overLimitCaveAddr = 0
	a.unlockAllTrophyAddr = 0
	a.terminusDropAddr = 0
	a.terminusDropOrig = nil
	a.collectibleTaskBase = 0
	a.sigilMemoryHookAddr = 0
	a.sigilMemoryCaveAddr = 0
	a.sigilMemoryOriginal = nil
	a.wrightstoneMemoryHookAddr = 0
	a.wrightstoneMemoryCaveAddr = 0
	a.wrightstoneMemoryOriginal = nil
	a.currencyHookAddr = 0
	a.currencyCaveAddr = 0
	a.currencyOriginal = nil
	a.monsterEnhanceOwned = nil
	a.runtimePatchPatchLeases = nil
	a.runtimePatchPatchOrder = nil
	a.runtimePatchSelectedMaterialHook = runtimePatchSelectedCaptureLease{}
	a.runtimePatchSelectedKeyItemHook = runtimePatchSelectedCaptureLease{}
	a.retiredRuntimeCaves = nil
	a.materialConsumeAddr = 0
	a.infiniteChallengeAddr = 0
	a.infiniteChallengeOwnerToken = ""
	a.charaOwnerToken = ""
	a.sigilMemoryOwnerToken = ""
	a.wrightstoneMemoryOwnerToken = ""
	a.overLimitOwnerToken = ""
	return nil
}

// CharaGetAll reads all character counts, returns valid characters (skipping empty slots).
func (a *App) CharaGetAll() ([]CharaInfo, error) {
	if a.hProcess == 0 {
		return nil, fmt.Errorf("未连接游戏进程")
	}

	manager, err := a.charaManager()
	if err != nil {
		return nil, err
	}

	var result []CharaInfo
	for i := 0; i < maxCharacters; i++ {
		countAddr := manager + uintptr(i)*charaStride + charaCountOffset
		var val, state int32
		err := readProcessMemory(a.hProcess, countAddr, unsafe.Pointer(&val), unsafe.Sizeof(val))
		if err != nil {
			continue
		}
		if err := readProcessMemory(a.hProcess, countAddr+(charaStateOffset-charaCountOffset), unsafe.Pointer(&state), unsafe.Sizeof(state)); err != nil || state != 0 {
			continue
		}
		if charaNames[i] == "" && val == 0 {
			continue // skip empty slots
		}
		if val == -1 {
			continue // skip uninitialized slots
		}
		name := charaNames[i]
		if name == "" {
			name = fmt.Sprintf("槽位 %d", i)
		}
		result = append(result, CharaInfo{Index: i, Name: name, Count: val})
	}
	return result, nil
}

// CharaSetOne sets a single character's count by slot index.
func (a *App) CharaSetOne(index int, value int) error {
	if a.hProcess == 0 {
		return fmt.Errorf("未连接游戏进程")
	}
	if index < 0 || index >= maxCharacters {
		return fmt.Errorf("无效的角色索引: %d", index)
	}

	manager, err := a.charaManager()
	if err != nil {
		return err
	}

	countAddr := manager + uintptr(index)*charaStride + charaCountOffset
	var state int32
	if err := readProcessMemory(a.hProcess, countAddr+(charaStateOffset-charaCountOffset), unsafe.Pointer(&state), unsafe.Sizeof(state)); err != nil || state != 0 {
		return fmt.Errorf("角色槽位未初始化: %d", index)
	}
	if err := snapshotBeforeLiveSaveChange(fmt.Sprintf("角色次数槽位%d写入前自动备份", index+1)); err != nil {
		return fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}
	val := int32(value)
	return writeProcessMemory(a.hProcess, countAddr, unsafe.Pointer(&val), unsafe.Sizeof(val))
}

// CharaSetAll sets all valid character counts to the given value, returns number modified.
func (a *App) CharaSetAll(value int) (int, error) {
	if a.hProcess == 0 {
		return 0, fmt.Errorf("未连接游戏进程")
	}

	manager, err := a.charaManager()
	if err != nil {
		return 0, err
	}
	if err := snapshotBeforeLiveSaveChange("全部角色次数写入前自动备份"); err != nil {
		return 0, fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}

	modified := 0
	newVal := int32(value)
	for i := 0; i < maxCharacters; i++ {
		countAddr := manager + uintptr(i)*charaStride + charaCountOffset
		var cur, state int32
		err := readProcessMemory(a.hProcess, countAddr, unsafe.Pointer(&cur), unsafe.Sizeof(cur))
		if err != nil {
			continue
		}
		if err := readProcessMemory(a.hProcess, countAddr+(charaStateOffset-charaCountOffset), unsafe.Pointer(&state), unsafe.Sizeof(state)); err != nil || state != 0 {
			continue
		}
		if charaNames[i] == "" {
			continue // skip unused and uninitialized slots
		}
		err = writeProcessMemory(a.hProcess, countAddr, unsafe.Pointer(&newVal), unsafe.Sizeof(newVal))
		if err == nil {
			modified++
		}
	}
	return modified, nil
}

func (a *App) currencyAddress(def currencyDef) (uintptr, error) {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return 0, fmt.Errorf("未连接游戏进程")
	}
	base, err := a.currencyRoot()
	if err != nil {
		return 0, err
	}
	return base + def.Offset, nil
}

func (a *App) readCurrency(def currencyDef) (CurrencyInfo, error) {
	addr, err := a.currencyAddress(def)
	if err != nil {
		return CurrencyInfo{}, err
	}
	var value int32
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&value), unsafe.Sizeof(value)); err != nil {
		return CurrencyInfo{}, fmt.Errorf("读取%s失败: %w", def.Name, err)
	}
	rva := uint64(0)
	if a.currencyHookAddr >= a.moduleBase {
		rva = uint64(a.currencyHookAddr - a.moduleBase)
	}
	return CurrencyInfo{ID: def.ID, Name: def.Name, RVA: rva, Offset: uint64(def.Offset), Address: uint64(addr), Value: value}, nil
}

// CurrencyGetAll reads all supported currency values from the DLC 2.0.2
// resource structure captured through a validated AOB hook.
func (a *App) CurrencyGetAll() ([]CurrencyInfo, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return nil, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return nil, err
	}
	return a.currencyGetAllLocked()
}

// CurrencyGetAllOwned pins the current character-page lease before installing
// or reading the currency capture hook. A stale page cannot reopen the process
// or leave a hook behind after a newer runtime page has taken ownership.
func (a *App) CurrencyGetAllOwned(token string) ([]CurrencyInfo, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return nil, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return nil, err
	}
	return a.currencyGetAllLocked()
}

func (a *App) currencyGetAllLocked() ([]CurrencyInfo, error) {
	result := make([]CurrencyInfo, 0, len(currencyDefs))
	for _, def := range currencyDefs {
		info, err := a.readCurrency(def)
		if err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	return result, nil
}

// CurrencySetOne writes one supported currency value by id.
func (a *App) CurrencySetOne(id string, value int) (CurrencyInfo, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return CurrencyInfo{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return CurrencyInfo{}, err
	}
	return a.currencySetOneLocked(id, value)
}

// CurrencySetOneOwned performs the complete backup/revalidate/write/readback
// transaction under the current character-page ownership lease.
func (a *App) CurrencySetOneOwned(token, id string, value int) (CurrencyInfo, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return CurrencyInfo{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return CurrencyInfo{}, err
	}
	return a.currencySetOneLocked(id, value)
}

func (a *App) currencySetOneLocked(id string, value int) (CurrencyInfo, error) {
	if value < 0 || value > math.MaxInt32 {
		return CurrencyInfo{}, fmt.Errorf("请输入 0 到 %d 之间的整数", math.MaxInt32)
	}
	requestedID := id
	def, ok := lookupCurrencyDef(id)
	if !ok {
		return CurrencyInfo{}, fmt.Errorf("未知货币: %s", strings.TrimSpace(requestedID))
	}
	root, err := a.currencyRoot()
	if err != nil {
		return CurrencyInfo{}, err
	}
	addr := root + def.Offset
	var originalValue int32
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&originalValue), unsafe.Sizeof(originalValue)); err != nil {
		return CurrencyInfo{}, fmt.Errorf("读取%s写入前原值失败: %w", def.Name, err)
	}
	if err := snapshotBeforeLiveSaveChange(def.Name + "写入前自动备份"); err != nil {
		return CurrencyInfo{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
	}
	confirmedRoot, err := a.currencyRoot()
	if err != nil {
		return CurrencyInfo{}, fmt.Errorf("自动备份后复核%s资源根指针失败: %w", def.Name, err)
	}
	confirmedAddr := confirmedRoot + def.Offset
	if confirmedRoot != root || confirmedAddr != addr {
		return CurrencyInfo{}, fmt.Errorf("自动备份期间%s资源结构已重建，请刷新后重试", def.Name)
	}
	var confirmedValue int32
	if err := readProcessMemory(a.hProcess, confirmedAddr, unsafe.Pointer(&confirmedValue), unsafe.Sizeof(confirmedValue)); err != nil {
		return CurrencyInfo{}, fmt.Errorf("自动备份后复核%s原值失败: %w", def.Name, err)
	}
	if confirmedValue != originalValue {
		return CurrencyInfo{}, fmt.Errorf("自动备份期间%s已从 %d 变化为 %d，请刷新后重试", def.Name, originalValue, confirmedValue)
	}
	newVal := int32(value)
	if err := a.writeInt32TransactionalLocked(confirmedAddr, confirmedValue, newVal, def.Name); err != nil {
		return CurrencyInfo{}, err
	}
	rva := uint64(0)
	if a.currencyHookAddr >= a.moduleBase {
		rva = uint64(a.currencyHookAddr - a.moduleBase)
	}
	return CurrencyInfo{ID: def.ID, Name: def.Name, RVA: rva, Offset: uint64(def.Offset), Address: uint64(confirmedAddr), Value: newVal}, nil
}

// writeInt32TransactionalLocked proves either the requested value or the exact
// original value. An unprovable rollback quarantines the current process from
// further live writes. The caller holds liveMemoryWriteMu and procMu.
func (a *App) writeInt32TransactionalLocked(addr uintptr, original, next int32, label string) error {
	writeValue := func(value int32) error {
		return writeProcessMemory(a.hProcess, addr, unsafe.Pointer(&value), unsafe.Sizeof(value))
	}
	readValue := func() (int32, error) {
		var value int32
		err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&value), unsafe.Sizeof(value))
		return value, err
	}
	rollback := func(cause error) error {
		rollbackErr := writeValue(original)
		rolledBack, verifyErr := readValue()
		if rollbackErr == nil && verifyErr == nil && rolledBack == original {
			return fmt.Errorf("%s写入未获确认，已恢复原值: %w", label, cause)
		}
		a.poisonCurrentLiveMemoryWrites()
		proofErr := verifyErr
		if proofErr == nil && rolledBack != original {
			proofErr = fmt.Errorf("回滚回读为 %d，期望 %d", rolledBack, original)
		}
		return errors.Join(
			fmt.Errorf("%s写入状态无法确认，已隔离当前游戏进程的后续写入: %w", label, cause),
			rollbackErr,
			proofErr,
		)
	}
	if err := writeValue(next); err != nil {
		return rollback(fmt.Errorf("写入失败: %w", err))
	}
	actual, err := readValue()
	if err != nil {
		return rollback(fmt.Errorf("写后回读失败: %w", err))
	}
	if actual != next {
		return rollback(fmt.Errorf("写后回读为 %d，期望 %d", actual, next))
	}
	return nil
}

func (a *App) potionAddress(def potionDef) (uintptr, error) {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return 0, fmt.Errorf("未连接游戏进程")
	}
	if len(def.Offsets) == 0 {
		return 0, fmt.Errorf("%s指针路径为空", def.Name)
	}
	var addr uintptr
	ptrAddr := a.moduleBase + def.RVA
	if err := readProcessMemory(a.hProcess, ptrAddr, unsafe.Pointer(&addr), unsafe.Sizeof(addr)); err != nil {
		return 0, fmt.Errorf("读取%s指针失败: %w", def.Name, err)
	}
	if addr == 0 {
		return 0, fmt.Errorf("%s指针为空，请确保已进入游戏存档", def.Name)
	}
	for i, offset := range def.Offsets {
		addr += offset
		if i == len(def.Offsets)-1 {
			return addr, nil
		}
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&addr), unsafe.Sizeof(addr)); err != nil {
			return 0, fmt.Errorf("读取%s指针链失败: %w", def.Name, err)
		}
		if addr == 0 {
			return 0, fmt.Errorf("%s指针链为空，请确保已进入游戏存档", def.Name)
		}
	}
	return addr, nil
}

func potionOffsetsJSON(offsets []uintptr) []uint64 {
	result := make([]uint64, 0, len(offsets))
	for _, offset := range offsets {
		result = append(result, uint64(offset))
	}
	return result
}

func (a *App) readPotion(def potionDef) (PotionInfo, error) {
	addr, err := a.potionAddress(def)
	if err != nil {
		return PotionInfo{}, err
	}
	var value int32
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&value), unsafe.Sizeof(value)); err != nil {
		return PotionInfo{}, fmt.Errorf("读取%s失败: %w", def.Name, err)
	}
	return PotionInfo{ID: def.ID, Name: def.Name, RVA: uint64(def.RVA), Offsets: potionOffsetsJSON(def.Offsets), Address: uint64(addr), Value: value}, nil
}

// PotionGetAll reads all supported potion values from stable pointer chains.
func (a *App) PotionGetAll() ([]PotionInfo, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return nil, err
	}
	defer a.procMu.Unlock()
	return a.potionGetAllLocked()
}

// PotionGetAllOwned pins reads to the page that acquired the shared character
// connection, preventing a stale async refresh from reopening a released one.
func (a *App) PotionGetAllOwned(token string) ([]PotionInfo, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return nil, err
	}
	defer a.procMu.Unlock()
	return a.potionGetAllLocked()
}

func (a *App) potionGetAllLocked() ([]PotionInfo, error) {
	result := make([]PotionInfo, 0, len(potionDefs))
	for _, def := range potionDefs {
		info, err := a.readPotion(def)
		if err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	return result, nil
}

// PotionSetOne writes one supported potion value by id.
func (a *App) PotionSetOne(id string, value int) (PotionInfo, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return PotionInfo{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return PotionInfo{}, err
	}
	return a.potionSetOneLocked(id, value)
}

// PotionSetOneOwned applies the potion transaction only for the current page.
func (a *App) PotionSetOneOwned(token, id string, value int) (PotionInfo, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return PotionInfo{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return PotionInfo{}, err
	}
	return a.potionSetOneLocked(id, value)
}

func (a *App) potionSetOneLocked(id string, value int) (PotionInfo, error) {
	id = strings.TrimSpace(id)
	if value < 0 || value > math.MaxInt32 {
		return PotionInfo{}, fmt.Errorf("请输入 0 到 %d 之间的整数", math.MaxInt32)
	}
	for _, def := range potionDefs {
		if def.ID != id {
			continue
		}
		addr, err := a.potionAddress(def)
		if err != nil {
			return PotionInfo{}, err
		}
		var originalValue int32
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&originalValue), unsafe.Sizeof(originalValue)); err != nil {
			return PotionInfo{}, fmt.Errorf("读取%s写入前原值失败: %w", def.Name, err)
		}
		if err := snapshotBeforeLiveSaveChange(def.Name + "写入前自动备份"); err != nil {
			return PotionInfo{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
		}
		confirmedAddr, err := a.potionAddress(def)
		if err != nil {
			return PotionInfo{}, fmt.Errorf("自动备份后复核%s指针失败: %w", def.Name, err)
		}
		if confirmedAddr != addr {
			return PotionInfo{}, fmt.Errorf("自动备份期间%s指针链已变化，请刷新后重试", def.Name)
		}
		var confirmedValue int32
		if err := readProcessMemory(a.hProcess, confirmedAddr, unsafe.Pointer(&confirmedValue), unsafe.Sizeof(confirmedValue)); err != nil {
			return PotionInfo{}, fmt.Errorf("自动备份后复核%s原值失败: %w", def.Name, err)
		}
		if confirmedValue != originalValue {
			return PotionInfo{}, fmt.Errorf("自动备份期间%s已从 %d 变化为 %d，请刷新后重试", def.Name, originalValue, confirmedValue)
		}
		newVal := int32(value)
		if err := a.writeInt32TransactionalLocked(confirmedAddr, confirmedValue, newVal, def.Name); err != nil {
			return PotionInfo{}, err
		}
		return PotionInfo{ID: def.ID, Name: def.Name, RVA: uint64(def.RVA), Offsets: potionOffsetsJSON(def.Offsets), Address: uint64(confirmedAddr), Value: newVal}, nil
	}
	return PotionInfo{}, fmt.Errorf("未知药水: %s", id)
}

// ── 角色脸部符文显示 (运行时 JE/JNE 切换) ──

var faceAccessoryPattern = []byte{
	0x49, 0x8B, 0x45, 0,
	0x4C, 0x39, 0xF0,
	0x0F, 0, 0, 0, 0, 0,
	0x4C, 0x89, 0xE9,
}

var faceAccessoryMask = []bool{
	true, true, true, false,
	true, true, true,
	true, false, false, false, false, false,
	true, true, true,
}

type FaceAccessoryStatus struct {
	Found        bool   `json:"found"`
	Address      uint64 `json:"address"`
	RVA          uint64 `json:"rva"`
	Hidden       bool   `json:"hidden"`
	JumpOpcode   string `json:"jumpOpcode"`
	CurrentBytes string `json:"currentBytes"`
}

func (a *App) FaceAccessoryScan() (FaceAccessoryStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return FaceAccessoryStatus{}, err
	}
	addr, err := a.scanPatternUnique(faceAccessoryPattern, faceAccessoryMask, "脸部符文特征")
	if err != nil {
		a.faceAccessoryAddr = 0
		return FaceAccessoryStatus{}, err
	}
	a.faceAccessoryAddr = addr
	return a.readFaceAccessoryStatus(addr)
}

func (a *App) FaceAccessoryGetStatus() (FaceAccessoryStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return FaceAccessoryStatus{}, err
	}
	if a.faceAccessoryAddr == 0 {
		return a.FaceAccessoryScan()
	}
	status, err := a.readFaceAccessoryStatus(a.faceAccessoryAddr)
	if err != nil {
		a.faceAccessoryAddr = 0
		return a.FaceAccessoryScan()
	}
	return status, nil
}

func (a *App) FaceAccessorySetHidden(hidden bool) (FaceAccessoryStatus, error) {
	status, err := a.FaceAccessoryGetStatus()
	if err != nil {
		return FaceAccessoryStatus{}, err
	}
	if !status.Found || a.faceAccessoryAddr == 0 {
		return FaceAccessoryStatus{}, fmt.Errorf("未定位脸部符文指令")
	}
	opcode := byte(0x84)
	if hidden {
		opcode = 0x85
	}
	if err := writeCodeMemory(a.hProcess, a.faceAccessoryAddr+8, []byte{opcode}); err != nil {
		return FaceAccessoryStatus{}, fmt.Errorf("写入脸部符文显示开关失败: %w", err)
	}
	return a.readFaceAccessoryStatus(a.faceAccessoryAddr)
}

func (a *App) readFaceAccessoryStatus(addr uintptr) (FaceAccessoryStatus, error) {
	buf := make([]byte, len(faceAccessoryPattern))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return FaceAccessoryStatus{}, fmt.Errorf("读取脸部符文指令失败: %w", err)
	}
	if !matchPattern(buf, faceAccessoryPattern, faceAccessoryMask) {
		return FaceAccessoryStatus{}, fmt.Errorf("脸部符文指令字节已变化，请重新扫描")
	}
	if buf[8] != 0x84 && buf[8] != 0x85 {
		return FaceAccessoryStatus{}, fmt.Errorf("脸部符文跳转 opcode 异常: 0x%02X", buf[8])
	}
	jumpOpcode := "JE"
	if buf[8] == 0x85 {
		jumpOpcode = "JNE"
	}
	return FaceAccessoryStatus{
		Found:        true,
		Address:      uint64(addr),
		RVA:          uint64(addr - a.moduleBase),
		Hidden:       buf[8] == 0x85,
		JumpOpcode:   jumpOpcode,
		CurrentBytes: bytesToHex(buf),
	}, nil
}

// ── 无限挑战 (运行时 NOP 挑战次数递增) ──

type InfiniteChallengeStatus struct {
	RVA          uint64 `json:"rva"`
	Enabled      bool   `json:"enabled"`
	Owned        bool   `json:"owned"`
	CurrentBytes string `json:"currentBytes"`
}

var (
	infiniteChallengePattern = []byte{0x41, 0xFF, 0x00, 0xB2, 0x00, 0xE9}
	infiniteChallengeMask    = []bool{true, true, false, true, false, true}
	infiniteChallengeOrig    = []byte{0x41, 0xFF, 0xC0}
	infiniteChallengePatch   = []byte{0x90, 0x90, 0x90}
)

func (a *App) InfiniteChallengeGetStatus() (InfiniteChallengeStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.readInfiniteChallengeStatus()
}

func (a *App) InfiniteChallengeGetStatusOwned(token string) (InfiniteChallengeStatus, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.readInfiniteChallengeStatus()
}

func (a *App) InfiniteChallengeSetEnabled(enabled bool) (InfiniteChallengeStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.infiniteChallengeSetEnabledLocked(a.charaOwnerToken, enabled)
}

func (a *App) InfiniteChallengeSetEnabledOwned(token string, enabled bool) (InfiniteChallengeStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.infiniteChallengeSetEnabledLocked(token, enabled)
}

func (a *App) infiniteChallengeSetEnabledLocked(ownerToken string, enabled bool) (InfiniteChallengeStatus, error) {
	status, err := a.readInfiniteChallengeStatus()
	if err != nil {
		return InfiniteChallengeStatus{}, err
	}
	if status.Enabled == enabled {
		return status, nil
	}
	if !enabled && !runtimeOwnerTokenMatches(a.infiniteChallengeOwnerToken, ownerToken) {
		return InfiniteChallengeStatus{}, fmt.Errorf("连续挑战补丁不属于当前页面，拒绝恢复未知写入")
	}
	patch := infiniteChallengeOrig
	if enabled {
		patch = infiniteChallengePatch
	}
	addr := a.infiniteChallengeAddr
	if err := writeCodeMemory(a.hProcess, addr, patch); err != nil {
		return InfiniteChallengeStatus{}, fmt.Errorf("写入无限挑战失败: %w", err)
	}
	verified, err := a.readInfiniteChallengeStatusAt(addr)
	if err != nil {
		return InfiniteChallengeStatus{}, err
	}
	if verified.Enabled != enabled {
		return InfiniteChallengeStatus{}, fmt.Errorf("无限挑战写后回读状态不匹配")
	}
	if enabled {
		if ownerToken == "" {
			ownerToken = "compatibility-call"
		}
		a.infiniteChallengeOwnerToken = ownerToken
	} else {
		a.infiniteChallengeOwnerToken = ""
	}
	return verified, nil
}

// restoreInfiniteChallengeOwnedLocked runs while procMu and runtimePatchMu are
// held. The recovery lease is consumed only after the original instruction is
// written and read back, so detach can safely retry after a transient failure.
func (a *App) restoreInfiniteChallengeOwnedLocked(ownerToken string, force bool) error {
	if a.infiniteChallengeOwnerToken == "" || (!force && !runtimeOwnerTokenMatches(a.infiniteChallengeOwnerToken, ownerToken)) {
		return nil
	}
	status, err := a.readInfiniteChallengeStatus()
	if err != nil {
		return err
	}
	if status.Enabled {
		if err := writeCodeMemory(a.hProcess, a.infiniteChallengeAddr, infiniteChallengeOrig); err != nil {
			return err
		}
		status, err = a.readInfiniteChallengeStatusAt(a.infiniteChallengeAddr)
		if err != nil || status.Enabled {
			return errors.Join(err, fmt.Errorf("连续挑战原始指令回读失败"))
		}
	}
	a.infiniteChallengeOwnerToken = ""
	return nil
}

func (a *App) readInfiniteChallengeStatus() (InfiniteChallengeStatus, error) {
	addr, err := a.locateInfiniteChallenge()
	if err != nil {
		return InfiniteChallengeStatus{}, err
	}
	return a.readInfiniteChallengeStatusAt(addr)
}

func (a *App) locateInfiniteChallenge() (uintptr, error) {
	if a.infiniteChallengeAddr != 0 {
		if _, err := a.readInfiniteChallengeStatusAt(a.infiniteChallengeAddr); err == nil {
			return a.infiniteChallengeAddr, nil
		}
		a.infiniteChallengeAddr = 0
	}
	addr, err := a.scanPatternUnique(infiniteChallengePattern, infiniteChallengeMask, "连续挑战特征")
	if err != nil {
		patchedPattern := append([]byte(nil), infiniteChallengePattern...)
		copy(patchedPattern, infiniteChallengePatch)
		addr, err = a.scanPatternUnique(patchedPattern, infiniteChallengeMask, "已启用连续挑战特征")
	}
	if err != nil {
		return 0, err
	}
	a.infiniteChallengeAddr = addr
	return addr, nil
}

func (a *App) readInfiniteChallengeStatusAt(addr uintptr) (InfiniteChallengeStatus, error) {
	buf := make([]byte, len(infiniteChallengePattern))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return InfiniteChallengeStatus{}, fmt.Errorf("读取无限挑战指令失败: %w", err)
	}
	knownInstruction := bytesEqual(buf[:len(infiniteChallengeOrig)], infiniteChallengeOrig) || bytesEqual(buf[:len(infiniteChallengePatch)], infiniteChallengePatch)
	for index := len(infiniteChallengeOrig); index < len(infiniteChallengePattern); index++ {
		if infiniteChallengeMask[index] && buf[index] != infiniteChallengePattern[index] {
			knownInstruction = false
		}
	}
	if !knownInstruction {
		return InfiniteChallengeStatus{}, fmt.Errorf("无限挑战指令字节异常: %s", bytesToHex(buf))
	}
	return InfiniteChallengeStatus{
		RVA:          uint64(addr - a.moduleBase),
		Enabled:      bytesEqual(buf[:len(infiniteChallengePatch)], infiniteChallengePatch),
		Owned:        a.infiniteChallengeOwnerToken != "",
		CurrentBytes: bytesToHex(buf),
	}, nil
}

// ── 升级/强化材料消耗 (运行时 NOP add [r14+04],esi) ──

type MaterialConsumeStatus struct {
	RVA          uint64 `json:"rva"`
	Enabled      bool   `json:"enabled"`
	CurrentBytes string `json:"currentBytes"`
}

const materialConsumeRVA = uintptr(0x356621)

var (
	materialConsumeOrig  = []byte{0x41, 0x01, 0x76, 0x04}
	materialConsumePatch = []byte{0x90, 0x90, 0x90, 0x90}
)

func (a *App) MaterialConsumeGetStatus() (MaterialConsumeStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	defer a.procMu.Unlock()
	return a.materialConsumeGetStatusLocked()
}

func (a *App) MaterialConsumeGetStatusOwned(token string) (MaterialConsumeStatus, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return MaterialConsumeStatus{}, err
	}
	defer a.procMu.Unlock()
	return a.materialConsumeGetStatusLocked()
}

func (a *App) materialConsumeGetStatusLocked() (MaterialConsumeStatus, error) {
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	if _, err := a.locateMaterialConsume(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	return a.readMaterialConsumeStatus()
}

func (a *App) MaterialConsumeSetEnabled(enabled bool) (MaterialConsumeStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	return a.materialConsumeSetEnabledLocked(enabled)
}

func (a *App) MaterialConsumeSetEnabledOwned(token string, enabled bool) (MaterialConsumeStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return MaterialConsumeStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	return a.materialConsumeSetEnabledLocked(enabled)
}

func (a *App) materialConsumeSetEnabledLocked(enabled bool) (MaterialConsumeStatus, error) {
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	patch := materialConsumeOrig
	if enabled {
		patch = materialConsumePatch
	}
	addr, err := a.locateMaterialConsume()
	if err != nil {
		return MaterialConsumeStatus{}, err
	}
	current, err := a.readSharedRuntimePatch(addr)
	if err != nil {
		return MaterialConsumeStatus{}, err
	}
	if err := validateSharedRuntimePatchTransition(current, sharedRuntimePatchOwnerMaterialConsume, enabled); err != nil {
		return MaterialConsumeStatus{}, err
	}
	writer := func(data []byte) error { return writeCodeMemory(a.hProcess, addr, data) }
	reader := func() ([]byte, error) { return a.readSharedRuntimePatch(addr) }
	installResult, err := installCodeHookAtomic(current, patch, writer, reader)
	if err != nil {
		if installResult.RequiresRecoveryLease() {
			a.poisonCurrentLiveMemoryWrites()
		}
		return MaterialConsumeStatus{}, fmt.Errorf("写入升级/强化材料消耗失败: %w", err)
	}
	return a.readMaterialConsumeStatus()
}

func (a *App) readMaterialConsumeStatus() (MaterialConsumeStatus, error) {
	addr, err := a.locateMaterialConsume()
	if err != nil {
		return MaterialConsumeStatus{}, err
	}
	buf, err := a.readSharedRuntimePatch(addr)
	if err != nil {
		return MaterialConsumeStatus{}, err
	}
	owner := classifySharedRuntimePatch(buf)
	if owner != sharedRuntimePatchOwnerNone && owner != sharedRuntimePatchOwnerMaterialConsume {
		if owner == sharedRuntimePatchOwnerUnknown {
			return MaterialConsumeStatus{}, fmt.Errorf("升级/强化材料消耗指令字节异常: %s", bytesToHex(buf))
		}
		return MaterialConsumeStatus{}, fmt.Errorf("共享补丁地址正由%s占用，请先恢复后再读取素材状态", sharedRuntimePatchOwnerLabel(owner))
	}
	return MaterialConsumeStatus{
		RVA:          uint64(addr - a.moduleBase),
		Enabled:      owner == sharedRuntimePatchOwnerMaterialConsume,
		CurrentBytes: bytesToHex(buf),
	}, nil
}

func (a *App) readSharedRuntimePatch(addr uintptr) ([]byte, error) {
	buf := make([]byte, len(sharedInventoryMaterialOriginal))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return nil, fmt.Errorf("读取素材/小钳蟹共享指令失败: %w", err)
	}
	return buf, nil
}

func (a *App) locateMaterialConsume() (uintptr, error) {
	if a.materialConsumeAddr != 0 {
		return a.materialConsumeAddr, nil
	}
	fixed := a.moduleBase + materialConsumeRVA
	if buf, err := a.readSharedRuntimePatch(fixed); err == nil {
		owner := classifySharedRuntimePatch(buf)
		if owner == sharedRuntimePatchOwnerNone || owner == sharedRuntimePatchOwnerMaterialConsume {
			a.materialConsumeAddr = fixed
			return fixed, nil
		}
		if owner == sharedRuntimePatchOwnerInventoryQuantity {
			return 0, fmt.Errorf("共享补丁地址正由%s占用，请先恢复", sharedRuntimePatchOwnerLabel(owner))
		}
	}
	mask := make([]bool, len(materialConsumeOrig))
	for i := range mask {
		mask[i] = true
	}
	addr, err := a.scanPatternUnique(materialConsumeOrig, mask, "升级/强化材料增减指令")
	if err != nil {
		return 0, fmt.Errorf("固定 RVA 已变化且特征扫描失败: %w", err)
	}
	a.materialConsumeAddr = addr
	return addr, nil
}

// ── 其他皮肤紫色符文显示 (运行时 JNE/JE 切换) ──

type OtherSkinPurpleRuneStatus struct {
	RVA          uint64 `json:"rva"`
	Enabled      bool   `json:"enabled"`
	JumpOpcode   string `json:"jumpOpcode"`
	CurrentBytes string `json:"currentBytes"`
}

const otherSkinPurpleRuneRVA = uintptr(0x9175B6)

func (a *App) OtherSkinPurpleRuneGetStatus() (OtherSkinPurpleRuneStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return OtherSkinPurpleRuneStatus{}, err
	}
	return a.readOtherSkinPurpleRuneStatus()
}

func (a *App) OtherSkinPurpleRuneSetEnabled(enabled bool) (OtherSkinPurpleRuneStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return OtherSkinPurpleRuneStatus{}, err
	}
	opcode := byte(0x75)
	if enabled {
		opcode = 0x74
	}
	addr := a.moduleBase + otherSkinPurpleRuneRVA
	if err := writeCodeMemory(a.hProcess, addr, []byte{opcode, 0x16}); err != nil {
		return OtherSkinPurpleRuneStatus{}, fmt.Errorf("写入其他皮肤紫色符文显示失败: %w", err)
	}
	return a.readOtherSkinPurpleRuneStatus()
}

func (a *App) readOtherSkinPurpleRuneStatus() (OtherSkinPurpleRuneStatus, error) {
	addr := a.moduleBase + otherSkinPurpleRuneRVA
	buf := make([]byte, 2)
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return OtherSkinPurpleRuneStatus{}, fmt.Errorf("读取其他皮肤紫色符文显示失败: %w", err)
	}
	if buf[1] != 0x16 || (buf[0] != 0x74 && buf[0] != 0x75) {
		return OtherSkinPurpleRuneStatus{}, fmt.Errorf("其他皮肤紫色符文跳转字节异常: %s", bytesToHex(buf))
	}
	jumpOpcode := "JNE"
	if buf[0] == 0x74 {
		jumpOpcode = "JE"
	}
	return OtherSkinPurpleRuneStatus{
		RVA:          uint64(otherSkinPurpleRuneRVA),
		Enabled:      buf[0] == 0x74,
		JumpOpcode:   jumpOpcode,
		CurrentBytes: bytesToHex(buf),
	}, nil
}

// ── 游戏内全称号解锁 (运行时 SETNE/SETNO 切换) ──

type UnlockAllTrophyStatus struct {
	Found        bool   `json:"found"`
	Address      uint64 `json:"address"`
	RVA          uint64 `json:"rva"`
	Enabled      bool   `json:"enabled"`
	CurrentBytes string `json:"currentBytes"`
}

var unlockAllTrophyPattern = []byte{
	0x80, 0xBC, 0x1F, 0x89, 0x00, 0x00, 0x00, 0x00,
	0x0F, 0x00, 0xC0, 0x40, 0x30, 0xE8, 0x75, 0xE0,
}

var unlockAllTrophyMask = []bool{
	true, true, true, true, true, true, true, true,
	true, false, true, true, true, true, true, true,
}

func (a *App) UnlockAllTrophyScan() (UnlockAllTrophyStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return UnlockAllTrophyStatus{}, err
	}
	addr, err := a.scanPatternUnique(unlockAllTrophyPattern, unlockAllTrophyMask, "全称号解锁特征")
	if err != nil {
		a.unlockAllTrophyAddr = 0
		return UnlockAllTrophyStatus{}, err
	}
	a.unlockAllTrophyAddr = addr
	return a.readUnlockAllTrophyStatus(addr)
}

func (a *App) UnlockAllTrophyGetStatus() (UnlockAllTrophyStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return UnlockAllTrophyStatus{}, err
	}
	if a.unlockAllTrophyAddr == 0 {
		return a.UnlockAllTrophyScan()
	}
	status, err := a.readUnlockAllTrophyStatus(a.unlockAllTrophyAddr)
	if err != nil {
		a.unlockAllTrophyAddr = 0
		return a.UnlockAllTrophyScan()
	}
	return status, nil
}

func (a *App) UnlockAllTrophySetEnabled(enabled bool) (UnlockAllTrophyStatus, error) {
	status, err := a.UnlockAllTrophyGetStatus()
	if err != nil {
		return UnlockAllTrophyStatus{}, err
	}
	if !status.Found || a.unlockAllTrophyAddr == 0 {
		return UnlockAllTrophyStatus{}, fmt.Errorf("未定位全称号解锁指令")
	}
	if enabled {
		if err := snapshotBeforeLiveSaveChange("全称号解锁前自动备份"); err != nil {
			return UnlockAllTrophyStatus{}, fmt.Errorf("自动备份失败，已取消写入: %w", err)
		}
	}
	opcode := byte(0x95)
	if enabled {
		opcode = 0x91
	}
	if err := writeCodeMemory(a.hProcess, a.unlockAllTrophyAddr+9, []byte{opcode}); err != nil {
		return UnlockAllTrophyStatus{}, fmt.Errorf("写入全称号解锁失败: %w", err)
	}
	return a.readUnlockAllTrophyStatus(a.unlockAllTrophyAddr)
}

func (a *App) readUnlockAllTrophyStatus(addr uintptr) (UnlockAllTrophyStatus, error) {
	buf := make([]byte, len(unlockAllTrophyPattern))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return UnlockAllTrophyStatus{}, fmt.Errorf("读取全称号解锁指令失败: %w", err)
	}
	if !matchPattern(buf, unlockAllTrophyPattern, unlockAllTrophyMask) {
		return UnlockAllTrophyStatus{}, fmt.Errorf("全称号解锁指令字节已变化，请重新扫描")
	}
	if buf[9] != 0x91 && buf[9] != 0x95 {
		return UnlockAllTrophyStatus{}, fmt.Errorf("全称号解锁 opcode 异常: 0x%02X", buf[9])
	}
	return UnlockAllTrophyStatus{
		Found:        true,
		Address:      uint64(addr),
		RVA:          uint64(addr - a.moduleBase),
		Enabled:      buf[9] == 0x91,
		CurrentBytes: bytesToHex(buf),
	}, nil
}

// ── 巴武掉落 100% (运行时 NOP 原巴巴武 lot 80% 排除检查) ──

type TerminusDropStatus struct {
	Found        bool   `json:"found"`
	Address      uint64 `json:"address"`
	RVA          uint64 `json:"rva"`
	Enabled      bool   `json:"enabled"`
	CurrentBytes string `json:"currentBytes"`
}

// GFR Public v0.4.5: 77?? 458B???? 4181?????????? 74?? 4488.
// Jump displacement changes with each game build, so retain bytes read at runtime.
var terminusDropPattern = []byte{
	0x77, 0,
	0x45, 0x8B, 0, 0,
	0x41, 0x81, 0, 0, 0, 0, 0,
	0x74, 0,
	0x44, 0x88,
}

var terminusDropMask = []bool{
	true, false,
	true, true, false, false,
	true, true, false, false, false, false, false,
	true, false,
	true, true,
}

var terminusDropPatch = []byte{0x90, 0x90}

func (a *App) TerminusDropScan() (TerminusDropStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return TerminusDropStatus{}, err
	}
	defer a.procMu.Unlock()
	return a.terminusDropScanLocked()
}

func (a *App) TerminusDropScanOwned(token string) (TerminusDropStatus, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return TerminusDropStatus{}, err
	}
	defer a.procMu.Unlock()
	return a.terminusDropScanLocked()
}

func (a *App) terminusDropScanLocked() (TerminusDropStatus, error) {
	addr, err := a.scanPatternUnique(terminusDropPattern, terminusDropMask, "巴武掉落特征")
	if err != nil {
		a.terminusDropAddr = 0
		return TerminusDropStatus{}, err
	}
	a.terminusDropAddr = addr
	return a.readTerminusDropStatus(addr)
}

func (a *App) TerminusDropGetStatus() (TerminusDropStatus, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return TerminusDropStatus{}, err
	}
	defer a.procMu.Unlock()
	return a.terminusDropGetStatusLocked()
}

func (a *App) TerminusDropGetStatusOwned(token string) (TerminusDropStatus, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return TerminusDropStatus{}, err
	}
	defer a.procMu.Unlock()
	return a.terminusDropGetStatusLocked()
}

func (a *App) terminusDropGetStatusLocked() (TerminusDropStatus, error) {
	if a.terminusDropAddr == 0 {
		return a.terminusDropScanLocked()
	}
	status, err := a.readTerminusDropStatus(a.terminusDropAddr)
	if err != nil {
		a.terminusDropAddr = 0
		return a.terminusDropScanLocked()
	}
	return status, nil
}

func (a *App) TerminusDropSetEnabled(enabled bool) (TerminusDropStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return TerminusDropStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return TerminusDropStatus{}, err
	}
	return a.terminusDropSetEnabledLocked(enabled)
}

func (a *App) TerminusDropSetEnabledOwned(token string, enabled bool) (TerminusDropStatus, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return TerminusDropStatus{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return TerminusDropStatus{}, err
	}
	return a.terminusDropSetEnabledLocked(enabled)
}

func (a *App) terminusDropSetEnabledLocked(enabled bool) (TerminusDropStatus, error) {
	status, err := a.terminusDropGetStatusLocked()
	if err != nil {
		return TerminusDropStatus{}, err
	}
	if !status.Found || a.terminusDropAddr == 0 {
		return TerminusDropStatus{}, fmt.Errorf("未定位巴武掉落指令")
	}
	patch := a.terminusDropOrig
	if enabled {
		patch = terminusDropPatch
	}
	if len(patch) != len(terminusDropPatch) {
		return TerminusDropStatus{}, fmt.Errorf("未保存巴武掉落原始跳转，请重启游戏后重新扫描")
	}
	current := make([]byte, len(terminusDropPatch))
	if err := readProcessMemory(a.hProcess, a.terminusDropAddr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return TerminusDropStatus{}, fmt.Errorf("写入前读取巴武掉落指令失败: %w", err)
	}
	writer := func(data []byte) error { return writeCodeMemory(a.hProcess, a.terminusDropAddr, data) }
	reader := func() ([]byte, error) {
		data := make([]byte, len(current))
		err := readProcessMemory(a.hProcess, a.terminusDropAddr, unsafe.Pointer(&data[0]), uintptr(len(data)))
		return data, err
	}
	installResult, err := installCodeHookAtomic(current, patch, writer, reader)
	if err != nil {
		if installResult.RequiresRecoveryLease() {
			a.poisonCurrentLiveMemoryWrites()
		}
		return TerminusDropStatus{}, fmt.Errorf("写入巴武掉落失败: %w", err)
	}
	return a.readTerminusDropStatus(a.terminusDropAddr)
}

func (a *App) readTerminusDropStatus(addr uintptr) (TerminusDropStatus, error) {
	buf := make([]byte, len(terminusDropPattern))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return TerminusDropStatus{}, fmt.Errorf("读取巴武掉落指令失败: %w", err)
	}
	if buf[0] == 0x77 {
		a.terminusDropOrig = append(a.terminusDropOrig[:0], buf[:2]...)
	} else if !bytesEqual(buf[:2], terminusDropPatch) {
		return TerminusDropStatus{}, fmt.Errorf("巴武掉落跳转字节异常: %s", bytesToHex(buf))
	}
	check := append([]byte(nil), buf...)
	copy(check[:2], []byte{0x77, 0})
	if !matchPattern(check, terminusDropPattern, terminusDropMask) {
		return TerminusDropStatus{}, fmt.Errorf("巴武掉落指令字节已变化，请重新扫描")
	}
	return TerminusDropStatus{
		Found:        true,
		Address:      uint64(addr),
		RVA:          uint64(addr - a.moduleBase),
		Enabled:      bytesEqual(buf[:2], terminusDropPatch),
		CurrentBytes: bytesToHex(buf),
	}, nil
}

// ── 固定倒计时 (运行时指令立即数修改) ──

var countdownPattern = []byte{
	0x48, 0xB8, 0, 0, 0, 0, 0, 0, 0, 0,
	0x48, 0x89, 0x87, 0, 0, 0, 0,
	0xC5, 0xFA, 0x10, 0x05,
}

var countdownMask = []bool{
	true, true, false, false, false, false, false, false, false, false,
	true, true, true, false, false, false, false,
	true, true, true, true,
}

type CountdownStatus struct {
	Found        bool    `json:"found"`
	Address      uint64  `json:"address"`
	RVA          uint64  `json:"rva"`
	Value1       float32 `json:"value1"`
	Value2       float32 `json:"value2"`
	CurrentBytes string  `json:"currentBytes"`
}

func (a *App) CountdownScan() (CountdownStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return CountdownStatus{}, err
	}

	addr, err := a.scanCountdownPattern()
	if err != nil {
		a.countdownAddr = 0
		return CountdownStatus{}, err
	}
	a.countdownAddr = addr
	return a.readCountdownStatus(addr)
}

func (a *App) CountdownGetStatus() (CountdownStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return CountdownStatus{}, err
	}
	if a.countdownAddr == 0 {
		return a.CountdownScan()
	}
	status, err := a.readCountdownStatus(a.countdownAddr)
	if err != nil {
		a.countdownAddr = 0
		return a.CountdownScan()
	}
	return status, nil
}

func (a *App) CountdownSet(value float64) (CountdownStatus, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 9999 {
		return CountdownStatus{}, fmt.Errorf("请输入 0 到 9999 之间的有效倒计时数值")
	}
	status, err := a.CountdownGetStatus()
	if err != nil {
		return CountdownStatus{}, err
	}
	if !status.Found || a.countdownAddr == 0 {
		return CountdownStatus{}, fmt.Errorf("未定位倒计时指令")
	}

	val := float32(value)
	bits := math.Float32bits(val)
	patch := make([]byte, 8)
	binary.LittleEndian.PutUint32(patch[0:4], bits)
	binary.LittleEndian.PutUint32(patch[4:8], bits)

	if err := writeCodeMemory(a.hProcess, a.countdownAddr+2, patch); err != nil {
		return CountdownStatus{}, fmt.Errorf("写入倒计时失败: %w", err)
	}
	return a.readCountdownStatus(a.countdownAddr)
}

func (a *App) ensureGameProcess() error {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	return a.ensureGameProcessLocked()
}

// ensureGameProcessLocked opens or replaces the shared game connection while
// the caller holds procMu.
func (a *App) ensureGameProcessLocked() error {
	pid, err := findProcessByName(charaProcessName)
	if err != nil {
		return fmt.Errorf("未找到游戏进程，请先启动游戏")
	}
	if canReuseGameProcess(a.charaPID, pid, a.hProcess != 0, a.moduleBase != 0, processHandleAlive(a.hProcess)) {
		return nil
	}
	// The game was restarted while the tool stayed open. Drop every address
	// derived from the old process before attaching to the new PID.
	if a.hProcess != 0 || a.moduleBase != 0 || a.charaPID != 0 {
		if err := a.charaDetachLocked(); err != nil {
			return fmt.Errorf("cannot safely replace the current game-process connection: %w", err)
		}
	}
	h, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, pid)
	if err != nil {
		return fmt.Errorf("无法打开进程 (错误 %v)，请以管理员身份运行", err)
	}
	modBase, err := getModuleBase(h)
	if err != nil {
		windows.CloseHandle(h)
		return fmt.Errorf("无法获取模块基址: %v", err)
	}
	created, err := processCreationTime(h)
	if err != nil {
		windows.CloseHandle(h)
		return fmt.Errorf("无法读取游戏进程创建时间: %v", err)
	}
	a.hProcess = h
	a.moduleBase = modBase
	a.charaPID = pid
	a.charaCreated = created
	a.clearLiveMemoryPoisonForNewProcess(a.currentProcessInstance())
	return nil
}

// acquireGameProcessLease pins hProcess/moduleBase/{PID, Created} until the caller
// unlocks procMu. It closes the gap between an idempotent ensure and the first
// Read/WriteProcessMemory call, where a concurrent detach used to close the
// handle underneath a live editor.
func (a *App) acquireGameProcessLease() error {
	a.procMu.Lock()
	if err := a.ensureGameProcessLocked(); err != nil {
		a.procMu.Unlock()
		return err
	}
	if a.hProcess == 0 || a.moduleBase == 0 || a.charaPID == 0 || a.charaCreated == 0 || !processHandleAlive(a.hProcess) {
		a.procMu.Unlock()
		return fmt.Errorf("游戏进程连接已失效，请重新连接")
	}
	return nil
}

// acquireOwnedGameProcessLease validates the global frontend request generation
// before opening, replacing or mutating any process resource. It returns with
// procMu held on success, matching acquireGameProcessLease.
func (a *App) acquireOwnedGameProcessLease(requestID uint64) error {
	a.procMu.Lock()
	if err := a.acceptRuntimeAcquireRequestLocked(requestID); err != nil {
		a.procMu.Unlock()
		return err
	}
	if err := a.ensureGameProcessLocked(); err != nil {
		a.procMu.Unlock()
		return err
	}
	if a.hProcess == 0 || a.moduleBase == 0 || a.charaPID == 0 || a.charaCreated == 0 || !processHandleAlive(a.hProcess) {
		a.procMu.Unlock()
		return fmt.Errorf("游戏进程连接已失效，请重新连接")
	}
	return nil
}

type runtimeOwnerScope uint8

const (
	runtimeOwnerChara runtimeOwnerScope = iota + 1
	runtimeOwnerSigil
	runtimeOwnerWrightstone
	runtimeOwnerOverLimit
)

func (a *App) runtimeOwnerTokenLocked(scope runtimeOwnerScope) string {
	switch scope {
	case runtimeOwnerChara:
		return a.charaOwnerToken
	case runtimeOwnerSigil:
		return a.sigilMemoryOwnerToken
	case runtimeOwnerWrightstone:
		return a.wrightstoneMemoryOwnerToken
	case runtimeOwnerOverLimit:
		return a.overLimitOwnerToken
	default:
		return ""
	}
}

// acquireLegacyRuntimeMutationLease preserves compatibility for callers that
// predate owner tokens, but never lets them mutate a resource currently leased
// by an owned page. The owner check happens before process discovery and stays
// protected by procMu through the caller's full lifecycle or write operation.
func (a *App) acquireLegacyRuntimeMutationLease(scope runtimeOwnerScope) error {
	a.procMu.Lock()
	if a.runtimeOwnerTokenLocked(scope) != "" {
		a.procMu.Unlock()
		return errRuntimeOwnerLeaseStale
	}
	if err := a.ensureGameProcessLocked(); err != nil {
		a.procMu.Unlock()
		return err
	}
	if a.runtimeOwnerTokenLocked(scope) != "" {
		a.procMu.Unlock()
		return errRuntimeOwnerLeaseStale
	}
	if a.hProcess == 0 || a.moduleBase == 0 || a.charaPID == 0 || a.charaCreated == 0 || !processHandleAlive(a.hProcess) {
		a.procMu.Unlock()
		return fmt.Errorf("游戏进程连接已失效，请重新连接")
	}
	return nil
}

// acquireOwnedRuntimeWriteLease validates the page token before any process IO
// and returns with procMu held. Keeping the lock through the caller's complete
// read/validate/write transaction prevents a concurrent Acquire from rotating
// the owner between validation and the final write.
func (a *App) acquireOwnedRuntimeWriteLease(scope runtimeOwnerScope, token string) error {
	a.procMu.Lock()
	if !runtimeOwnerTokenMatches(a.runtimeOwnerTokenLocked(scope), token) {
		a.procMu.Unlock()
		return errRuntimeOwnerLeaseStale
	}
	if err := a.ensureGameProcessLocked(); err != nil {
		a.procMu.Unlock()
		return err
	}
	// A process replacement clears every resource owner. Recheck after ensuring
	// the connection so a lease from the old game instance cannot write the new.
	if !runtimeOwnerTokenMatches(a.runtimeOwnerTokenLocked(scope), token) {
		a.procMu.Unlock()
		return errRuntimeOwnerLeaseStale
	}
	if a.hProcess == 0 || a.moduleBase == 0 || a.charaPID == 0 || a.charaCreated == 0 || !processHandleAlive(a.hProcess) {
		a.procMu.Unlock()
		return fmt.Errorf("游戏进程连接已失效，请重新连接")
	}
	return nil
}

func canReuseGameProcess(cachedPID, discoveredPID uint32, hasHandle, hasModule, live bool) bool {
	return cachedPID != 0 && cachedPID == discoveredPID && hasHandle && hasModule && live
}

func runtimeOwnerTokenMatches(current, presented string) bool {
	return current != "" && presented != "" && current == presented
}

// acceptRuntimeAcquireRequestLocked enforces one strictly increasing request
// generation across every owned runtime feature. The caller holds procMu.
func (a *App) acceptRuntimeAcquireRequestLocked(requestID uint64) error {
	if requestID == 0 || requestID <= a.latestRuntimeAcquireRequestID {
		return fmt.Errorf("%w: requestID=%d, latest=%d", errRuntimeAcquireRequestStale, requestID, a.latestRuntimeAcquireRequestID)
	}
	a.latestRuntimeAcquireRequestID = requestID
	return nil
}

// hasActiveRuntimeHookLeaseLocked reports every hook or unresolved recovery
// lease that CharaDetach would otherwise restore. The caller holds procMu.
func (a *App) hasActiveRuntimeHookLeaseLocked() bool {
	return a.sigilMemoryHookAddr != 0 ||
		a.sigilMemoryCaveAddr != 0 ||
		len(a.sigilMemoryOriginal) != 0 ||
		a.wrightstoneMemoryHookAddr != 0 ||
		a.wrightstoneMemoryCaveAddr != 0 ||
		len(a.wrightstoneMemoryOriginal) != 0 ||
		a.overLimitHookAddr != 0 ||
		a.overLimitCaveAddr != 0 ||
		a.currencyHookAddr != 0 ||
		a.currencyCaveAddr != 0 ||
		len(a.currencyOriginal) != 0 ||
		len(a.monsterEnhanceOwned) != 0 ||
		len(a.runtimePatchPatchLeases) != 0 ||
		len(a.runtimePatchPatchOrder) != 0 ||
		a.infiniteChallengeOwnerToken != "" ||
		a.hasRuntimePatchSelectedCaptureLeaseLocked()
}

// nextRuntimeOwnerToken is called only while procMu is held.
func (a *App) nextRuntimeOwnerToken(scope string) string {
	a.runtimeOwnerSequence++
	return fmt.Sprintf("%s-%016X", scope, a.runtimeOwnerSequence)
}

func (a *App) grantCharaOwner(info CharaProcessInfo) CharaProcessInfo {
	token := a.nextRuntimeOwnerToken("chara")
	a.charaOwnerToken = token
	info.OwnerToken = token
	return info
}

type processInstanceID struct {
	PID     uint32
	Created uint64
}

func sameProcessInstance(left, right processInstanceID) bool {
	return left.PID != 0 && left.Created != 0 && left == right
}

func liveMemoryWritePoisoned(poison, current processInstanceID) bool {
	if poison.PID == 0 || current.PID == 0 {
		return false
	}
	if poison.Created == 0 || current.Created == 0 {
		// Missing creation metadata is an invariant failure. Fall back to the
		// safer PID-only quarantine instead of accidentally permitting a retry.
		return poison.PID == current.PID
	}
	return sameProcessInstance(poison, current)
}

func (a *App) currentProcessInstance() processInstanceID {
	return processInstanceID{PID: a.charaPID, Created: a.charaCreated}
}

// clearLiveMemoryPoisonForNewProcess must be called while procMu is held and
// only after the replacement handle, module base and creation time are known.
func (a *App) clearLiveMemoryPoisonForNewProcess(current processInstanceID) {
	if current.PID == 0 || current.Created == 0 {
		return
	}
	if a.liveMemoryIndeterminateProcess.PID != 0 && !sameProcessInstance(a.liveMemoryIndeterminateProcess, current) {
		a.liveMemoryIndeterminateProcess = processInstanceID{}
	}
}

// poisonCurrentLiveMemoryWrites is called while a game-process lease holds
// procMu, making the whole {PID, Created} assignment atomic to lifecycle swaps.
func (a *App) poisonCurrentLiveMemoryWrites() {
	a.liveMemoryIndeterminateProcess = a.currentProcessInstance()
}

func (a *App) ensureLiveMemoryWritesSafe() error {
	if liveMemoryWritePoisoned(a.liveMemoryIndeterminateProcess, a.currentProcessInstance()) {
		return fmt.Errorf("此前远程线程状态不确定，已锁定当前游戏进程的实时内存写入；请完全退出并重新启动游戏后再试")
	}
	return nil
}

func processCreationTime(handle windows.Handle) (uint64, error) {
	if handle == 0 {
		return 0, fmt.Errorf("process handle is empty")
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	created := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	if created == 0 {
		return 0, fmt.Errorf("process creation time is empty")
	}
	return created, nil
}

func processHandleAlive(handle windows.Handle) bool {
	if handle == 0 {
		return false
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	const stillActive = 259
	return exitCode == stillActive
}

func (a *App) scanCountdownPattern() (uintptr, error) {
	return a.scanPatternUnique(countdownPattern, countdownMask, "倒计时特征")
}

func (a *App) scanPatternUnique(pattern []byte, mask []bool, label string) (uintptr, error) {
	moduleSize, err := getRemoteModuleSize(a.hProcess, a.moduleBase)
	if err != nil {
		return 0, err
	}
	const chunkSize uintptr = 0x10000
	patternLen := len(pattern)
	var matches []uintptr
	var carry []byte
	var carryBase uintptr

	for off := uintptr(0); off < moduleSize; off += chunkSize {
		size := chunkSize
		if off+size > moduleSize {
			size = moduleSize - off
		}
		buf := make([]byte, int(size))
		addr := a.moduleBase + off
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
			carry = nil
			continue
		}

		scanBuf := buf
		scanBase := addr
		if len(carry) > 0 {
			scanBuf = append(append([]byte{}, carry...), buf...)
			scanBase = carryBase
		}
		matches = append(matches, findPatternMatches(scanBuf, scanBase, pattern, mask)...)
		if len(matches) > 1 {
			return 0, fmt.Errorf("%s命中多个位置: %d", label, len(matches))
		}

		if len(buf) >= patternLen-1 {
			carry = append([]byte{}, buf[len(buf)-patternLen+1:]...)
			carryBase = addr + uintptr(len(buf)-patternLen+1)
		} else {
			carry = append(append([]byte{}, carry...), buf...)
			if len(carry) > patternLen-1 {
				carry = carry[len(carry)-patternLen+1:]
				carryBase = addr + uintptr(len(buf)-len(carry))
			}
		}
	}

	if len(matches) == 0 {
		return 0, fmt.Errorf("未找到%s码", label)
	}
	return matches[0], nil
}

func (a *App) readCountdownStatus(addr uintptr) (CountdownStatus, error) {
	buf := make([]byte, len(countdownPattern))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return CountdownStatus{}, fmt.Errorf("读取倒计时指令失败: %w", err)
	}
	if !matchPattern(buf, countdownPattern, countdownMask) {
		return CountdownStatus{}, fmt.Errorf("倒计时指令字节已变化，请重新扫描")
	}
	v1 := math.Float32frombits(binary.LittleEndian.Uint32(buf[2:6]))
	v2 := math.Float32frombits(binary.LittleEndian.Uint32(buf[6:10]))
	return CountdownStatus{
		Found:        true,
		Address:      uint64(addr),
		RVA:          uint64(addr - a.moduleBase),
		Value1:       v1,
		Value2:       v2,
		CurrentBytes: bytesToHex(buf),
	}, nil
}

// ── 怪物增强 (注入 patch_core.dll) ──

type MonsterEnhanceResult struct {
	PID          uint32               `json:"pid"`
	DLLPath      string               `json:"dllPath"`
	Injected     bool                 `json:"injected"`
	Enabled      bool                 `json:"enabled"`
	CurrentBytes string               `json:"currentBytes"`
	Items        []MonsterEnhanceItem `json:"items"`
}

type MonsterEnhanceItem struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	RVA               uint64 `json:"rva"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
	Enabled           bool   `json:"enabled"`
	CurrentBytes      string `json:"currentBytes"`
}

type monsterPatchPoint struct {
	ID                string
	Name              string
	RVA               uintptr
	Original          []byte
	Patch             []byte
	Hook              bool
	Available         bool
	UnavailableReason string
}

var monsterPatchPoints = []monsterPatchPoint{
	{
		ID:                "link_time_no_drain",
		Name:              "无限 link time",
		RVA:               0x187228,
		Original:          []byte{0xC4, 0xC1, 0x7A, 0x11, 0x9C, 0x24, 0xB4, 0x01, 0x00, 0x00},
		Patch:             []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
		UnavailableReason: "当前游戏版本定位未闭环",
	},
	{
		ID:                "link_time_disable",
		Name:              "无法进入 link time",
		RVA:               0x187228,
		Original:          []byte{0xC4, 0xC1, 0x7A, 0x11, 0x9C, 0x24, 0xB4, 0x01, 0x00, 0x00},
		Patch:             []byte{0xC4, 0xC1, 0x7A, 0x11, 0x84, 0x24, 0xB4, 0x01, 0x00, 0x00},
		UnavailableReason: "当前游戏版本定位未闭环",
	},
	{
		ID:        "monster_hp",
		Name:      "怪物多倍血",
		RVA:       0x1F7A820,
		Original:  []byte{0x48, 0x8B, 0x41, 0x10, 0x45, 0x31, 0xC9},
		Hook:      true,
		Available: true,
	},
	{
		ID:        "monster_damage",
		Name:      "怪物伤害",
		RVA:       0x1FBDEB4,
		Original:  []byte{0x81, 0xBE, 0xD4, 0x00, 0x00, 0x00, 0x00, 0xE1, 0xF5, 0x05},
		Hook:      true,
		Available: true,
	},
	{
		ID:                "crocodile_damage",
		Name:              "鳄鱼多倍血(鳄鱼需单独设置)",
		RVA:               0x23FD449,
		Original:          []byte{0x01, 0xBE, 0xB8, 0x15, 0x00, 0x00, 0x48, 0x8D, 0x8E, 0xB0, 0xFE, 0xFF, 0xFF, 0x8B, 0x46, 0x10},
		Hook:              true,
		UnavailableReason: "当前游戏版本定位未闭环",
	},
	{
		ID:        "monster_stun",
		Name:      "怪物多倍昏厥条",
		RVA:       0xB29128,
		Original:  []byte{0xC5, 0xFA, 0x58, 0x86, 0x60, 0x08, 0x00, 0x00},
		Hook:      true,
		Available: true,
	},
	{
		ID:        "overdrive_state",
		Name:      "怪物 Overdrive 状态",
		RVA:       0x22CB316,
		Original:  []byte{0x8B, 0x46, 0x10, 0x83, 0xF8, 0x03, 0x0F, 0x84, 0xC7, 0x00, 0x00, 0x00},
		Hook:      true,
		Available: true,
	},
	{
		ID:        "inventory_set_45",
		Name:      "设置背包物品数量为 45",
		RVA:       0x356621,
		Original:  []byte{0x41, 0x01, 0x76, 0x04, 0x4C, 0x89, 0xE1},
		Hook:      true,
		Available: true,
	},
	{
		ID:                "sba_chain_timer",
		Name:              "奥义接续计时",
		RVA:               0x677B45,
		Original:          []byte{0x48, 0xB8, 0x00, 0x00, 0x40, 0x40, 0x00, 0x00, 0x40, 0x40},
		UnavailableReason: "当前游戏版本定位未闭环",
	},
	{
		ID:                "purple_drain",
		Name:              "紫条不自然扣减",
		RVA:               0xA0379A,
		Original:          []byte{0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x10, 0x0A, 0x00, 0x00},
		Patch:             []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
		UnavailableReason: "当前游戏版本定位未闭环",
	},
	{
		ID:                "blue_grow",
		Name:              "昏厥蓝条不增长",
		RVA:               0xA09AF1,
		Original:          []byte{0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x20, 0x07, 0x00, 0x00},
		Patch:             []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
		UnavailableReason: "当前游戏版本定位未闭环",
	},
	{
		ID:                "blue_drain",
		Name:              "昏厥蓝条不自然扣减",
		RVA:               0xA03F38,
		Original:          []byte{0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x70, 0x0A, 0x00, 0x00},
		Patch:             []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
		UnavailableReason: "当前游戏版本定位未闭环",
	},
}

const damageMeterMappingName = "Local\\GBFRPlayerInfoEditDamageMeterV3"
const damageMeterSize = 16

type DamageMeterStatus struct {
	Connected       bool   `json:"connected"`
	TotalDamage     uint64 `json:"totalDamage"`
	MonsterDamage   uint64 `json:"monsterDamage"`
	CrocodileDamage uint64 `json:"crocodileDamage"`
}

func (a *App) DamageMeterGetStatus() (DamageMeterStatus, error) {
	a.damageMu.Lock()
	defer a.damageMu.Unlock()
	if err := a.ensureDamageMeterLocked(); err != nil {
		return DamageMeterStatus{}, err
	}
	var values [2]int64
	if err := readProcessMemory(windows.CurrentProcess(), a.damageMeterView, unsafe.Pointer(&values[0]), damageMeterSize); err != nil {
		return DamageMeterStatus{}, fmt.Errorf("读取伤害统计共享内存失败: %w", err)
	}
	monsterDamage := uint64(values[0])
	crocodileDamage := uint64(values[1])
	return DamageMeterStatus{Connected: true, TotalDamage: monsterDamage + crocodileDamage, MonsterDamage: monsterDamage, CrocodileDamage: crocodileDamage}, nil
}

func (a *App) DamageMeterReset() (DamageMeterStatus, error) {
	a.damageMu.Lock()
	defer a.damageMu.Unlock()
	if err := a.ensureDamageMeterLocked(); err != nil {
		return DamageMeterStatus{}, err
	}
	zeros := [damageMeterSize]byte{}
	if err := writeProcessMemory(windows.CurrentProcess(), a.damageMeterView, unsafe.Pointer(&zeros[0]), damageMeterSize); err != nil {
		return DamageMeterStatus{}, fmt.Errorf("清空伤害统计共享内存失败: %w", err)
	}
	var verified [damageMeterSize]byte
	if err := readProcessMemory(windows.CurrentProcess(), a.damageMeterView, unsafe.Pointer(&verified[0]), damageMeterSize); err != nil {
		return DamageMeterStatus{}, fmt.Errorf("清空伤害统计后回读失败: %w", err)
	}
	if verified != zeros {
		return DamageMeterStatus{}, fmt.Errorf("清空伤害统计后回读不一致")
	}
	return DamageMeterStatus{Connected: true}, nil
}

// ensureDamageMeterLocked maps the shared-memory view. Caller must hold damageMu.
func (a *App) ensureDamageMeterLocked() error {
	if a.damageMeterView != 0 {
		return nil
	}
	name, err := windows.UTF16PtrFromString(damageMeterMappingName)
	if err != nil {
		return err
	}
	mapping, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, damageMeterSize, name)
	if err != nil && (mapping == 0 || err != windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("创建伤害记录共享内存失败: %w", err)
	}
	view, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_READ|windows.FILE_MAP_WRITE, 0, 0, damageMeterSize)
	if err != nil {
		windows.CloseHandle(mapping)
		return fmt.Errorf("映射伤害记录共享内存失败: %w", err)
	}
	a.damageMeterMapping = mapping
	a.damageMeterView = view
	return nil
}

func (a *App) closeDamageMeter() {
	a.damageMu.Lock()
	defer a.damageMu.Unlock()
	if a.damageMeterView != 0 {
		_ = windows.UnmapViewOfFile(a.damageMeterView)
		a.damageMeterView = 0
	}
	if a.damageMeterMapping != 0 {
		_ = windows.CloseHandle(a.damageMeterMapping)
		a.damageMeterMapping = 0
	}
}

func (a *App) MonsterEnhanceGetStatus() (MonsterEnhanceResult, error) {
	if err := a.acquireGameProcessLease(); err != nil {
		return MonsterEnhanceResult{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.readMonsterEnhanceStatus("")
}

func (a *App) MonsterEnhanceGetStatusOwned(token string) (MonsterEnhanceResult, error) {
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return MonsterEnhanceResult{}, err
	}
	defer a.procMu.Unlock()
	a.runtimePatchMu.Lock()
	defer a.runtimePatchMu.Unlock()
	return a.readMonsterEnhanceStatus("")
}

func (a *App) MonsterEnhanceSetEnabled(enabled bool) (MonsterEnhanceResult, error) {
	return a.MonsterEnhanceSetPatchEnabled("all", enabled)
}

func (a *App) MonsterEnhanceSetPatchEnabled(id string, enabled bool) (MonsterEnhanceResult, error) {
	return a.MonsterEnhanceSetPatchValueEnabled(id, enabled, 0)
}

func (a *App) MonsterEnhanceSetPatchValueEnabled(id string, enabled bool, hpMultiplier float64) (MonsterEnhanceResult, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireGameProcessLease(); err != nil {
		return MonsterEnhanceResult{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return MonsterEnhanceResult{}, err
	}
	return a.monsterEnhanceSetPatchValueEnabledLocked(a.monsterEnhanceOwnerForCompatibilityCall(), id, enabled, hpMultiplier)
}

func (a *App) MonsterEnhanceSetPatchValueEnabledOwned(token, id string, enabled bool, hpMultiplier float64) (MonsterEnhanceResult, error) {
	liveMemoryWriteMu.Lock()
	defer liveMemoryWriteMu.Unlock()
	if err := a.acquireOwnedRuntimeWriteLease(runtimeOwnerChara, token); err != nil {
		return MonsterEnhanceResult{}, err
	}
	defer a.procMu.Unlock()
	if err := a.ensureLiveMemoryWritesSafe(); err != nil {
		return MonsterEnhanceResult{}, err
	}
	return a.monsterEnhanceSetPatchValueEnabledLocked(token, id, enabled, hpMultiplier)
}

func (a *App) monsterEnhanceSetPatchValueEnabledLocked(ownerToken, id string, enabled bool, hpMultiplier float64) (MonsterEnhanceResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return MonsterEnhanceResult{}, fmt.Errorf("怪物增强项目为空")
	}
	pointID := id
	applyOnce := false
	if id == "overdrive_state_apply" {
		pointID = "overdrive_state"
		applyOnce = true
	}
	point := findMonsterPatchPoint(pointID)
	if pointID != "all" && point == nil {
		return MonsterEnhanceResult{}, fmt.Errorf("未知怪物增强项目: %s", id)
	}
	if point != nil && !point.Available {
		reason := point.UnavailableReason
		if reason == "" {
			reason = "当前游戏版本不可用"
		}
		return MonsterEnhanceResult{}, fmt.Errorf("%s不可用：%s", point.Name, reason)
	}
	if pointID == "all" || (point != nil && point.ID == "inventory_set_45") {
		a.runtimePatchMu.Lock()
		defer a.runtimePatchMu.Unlock()
		addr := a.moduleBase + materialConsumeRVA
		current, err := a.readSharedRuntimePatch(addr)
		if err != nil {
			return MonsterEnhanceResult{}, err
		}
		if err := validateSharedRuntimePatchTransition(current, sharedRuntimePatchOwnerInventoryQuantity, enabled); err != nil {
			return MonsterEnhanceResult{}, err
		}
	}
	if enabled && point != nil && needsMonsterValue(point.ID) && (math.IsNaN(hpMultiplier) || math.IsInf(hpMultiplier, 0) || hpMultiplier <= 0 || hpMultiplier > 9999) {
		return MonsterEnhanceResult{}, fmt.Errorf("怪物倍率请输入 0 到 9999 之间的数值")
	}
	if enabled && point != nil && point.ID == "sba_chain_timer" && (math.IsNaN(hpMultiplier) || math.IsInf(hpMultiplier, 0) || hpMultiplier <= 0 || hpMultiplier > 9999) {
		return MonsterEnhanceResult{}, fmt.Errorf("奥义接续计时请输入 0 到 9999 之间的数值")
	}
	if enabled && point != nil && point.ID == "overdrive_state" && (math.IsNaN(hpMultiplier) || math.IsInf(hpMultiplier, 0) || (hpMultiplier != 0 && hpMultiplier != 3 && hpMultiplier != 9)) {
		return MonsterEnhanceResult{}, fmt.Errorf("Overdrive 状态请选择空条、满黄条或自动OD")
	}

	if enabled {
		if pointID == "all" {
			return MonsterEnhanceResult{}, fmt.Errorf("怪物增强批量 Hook 无法证明逐项所有权，请分别开启需要的功能")
		}
		original, err := a.prepareMonsterEnhanceEnable(ownerToken, point)
		if err != nil {
			return MonsterEnhanceResult{}, err
		}
		var auxiliary *monsterEnhanceAuxPreflight
		if point != nil && point.ID == "monster_damage" {
			auxiliary, err = a.prepareMonsterDamageAuxiliaryHook()
			if err != nil {
				return MonsterEnhanceResult{}, err
			}
		}
		if point != nil && point.ID == "sba_chain_timer" {
			if err := a.setSBAChainTimer(point, hpMultiplier); err != nil {
				return MonsterEnhanceResult{}, err
			}
			if err := a.claimMonsterEnhancePatch(ownerToken, point, original); err != nil {
				rollbackErr := a.writeAndVerifyMonsterEnhanceEntry(a.moduleBase+point.RVA, original, point.Name+" rollback")
				return MonsterEnhanceResult{}, errors.Join(err, rollbackErr)
			}
			return a.readMonsterEnhanceStatus("")
		}
		command := pointID
		if point != nil && point.ID == "inventory_set_45" {
			if math.IsNaN(hpMultiplier) || math.IsInf(hpMultiplier, 0) || hpMultiplier < 1 || hpMultiplier > 9999 || math.Trunc(hpMultiplier) != hpMultiplier {
				return MonsterEnhanceResult{}, fmt.Errorf("背包物品数量请输入 1 到 9999 之间的整数")
			}
			command = fmt.Sprintf("%s %d", pointID, int(hpMultiplier))
		} else if point != nil && needsMonsterValue(point.ID) {
			commandValue := hpMultiplier
			if point.ID == "monster_hp" || point.ID == "monster_stun" || point.ID == "crocodile_damage" {
				commandValue = 1 / hpMultiplier
			}
			command = fmt.Sprintf("%s %.8g", command, commandValue)
		}
		dllPath, err := extractPatchCoreDLL(command)
		if err != nil {
			return MonsterEnhanceResult{}, err
		}
		if err := injectDLL(a.hProcess, dllPath); err != nil {
			if isRemoteCallIndeterminate(err) {
				a.poisonCurrentLiveMemoryWrites()
			}
			return MonsterEnhanceResult{}, fmt.Errorf("注入怪物增强 DLL 失败: %w", err)
		}
		status, err := a.waitMonsterEnhanceApplied(pointID, dllPath)
		if err != nil {
			a.poisonCurrentLiveMemoryWrites()
			rollbackErr := a.rollbackMonsterEnhanceFailedEnableWithAux(ownerToken, point, original, auxiliary)
			return MonsterEnhanceResult{}, errors.Join(err, rollbackErr)
		}
		if err := a.claimMonsterEnhancePatchWithAux(ownerToken, point, original, auxiliary); err != nil {
			restoreErr := a.rollbackMonsterEnhanceFailedEnableWithAux(ownerToken, point, original, auxiliary)
			return MonsterEnhanceResult{}, errors.Join(err, restoreErr)
		}
		if applyOnce {
			time.Sleep(150 * time.Millisecond)
			if err := a.restoreMonsterEnhanceOwned(ownerToken, pointID, false); err != nil {
				return MonsterEnhanceResult{}, err
			}
			status, err = a.readMonsterEnhanceStatus(dllPath)
			if err != nil {
				return MonsterEnhanceResult{}, err
			}
		}
		status.Injected = true
		return status, nil
	}

	if pointID != "all" {
		record, owned := a.monsterEnhanceOwned[pointID]
		if !owned {
			current, err := a.readMonsterEnhanceEntry(a.moduleBase+point.RVA, len(point.Original))
			if err != nil {
				return MonsterEnhanceResult{}, fmt.Errorf("读取%s失败: %w", point.Name, err)
			}
			if !bytesEqual(current, point.Original) {
				return MonsterEnhanceResult{}, fmt.Errorf("%s不是本页面拥有的 Patch，已拒绝覆盖: %s", point.Name, bytesToHex(current))
			}
			return a.readMonsterEnhanceStatus("")
		}
		if record.OwnerToken != ownerToken {
			return MonsterEnhanceResult{}, fmt.Errorf("%s由另一个运行时页面持有", point.Name)
		}
	}
	if err := a.restoreMonsterEnhanceOwned(ownerToken, pointID, false); err != nil {
		return MonsterEnhanceResult{}, err
	}
	return a.readMonsterEnhanceStatus("")
}

func (a *App) MonsterEnhanceInject() (MonsterEnhanceResult, error) {
	return a.MonsterEnhanceSetEnabled(true)
}

func (a *App) waitMonsterEnhanceApplied(id string, dllPath string) (MonsterEnhanceResult, error) {
	var last MonsterEnhanceResult
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for {
		last, err = a.readMonsterEnhanceStatus(dllPath)
		if err == nil && monsterStatusHasPatch(last, id) {
			return last, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return MonsterEnhanceResult{}, err
			}
			return MonsterEnhanceResult{}, fmt.Errorf("怪物增强 Hook 未写入目标地址")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func monsterStatusHasPatch(status MonsterEnhanceResult, id string) bool {
	if id == "all" {
		return status.Enabled
	}
	for _, item := range status.Items {
		if item.ID == id {
			return item.Enabled
		}
	}
	return false
}

func (a *App) readMonsterEnhanceStatus(dllPath string) (MonsterEnhanceResult, error) {
	patched := 0
	available := 0
	var parts []string
	items := make([]MonsterEnhanceItem, 0, len(monsterPatchPoints))
	for _, point := range monsterPatchPoints {
		current := make([]byte, len(point.Original))
		addr := a.moduleBase + point.RVA
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
			return MonsterEnhanceResult{}, fmt.Errorf("读取%s失败: %w", point.Name, err)
		}
		currentHex := bytesToHex(current)
		parts = append(parts, fmt.Sprintf("%s:%s", point.Name, currentHex))
		enabled := false
		if !point.Available {
			enabled = false
		} else if point.ID == "sba_chain_timer" {
			enabled = !bytesEqual(current, point.Original)
		} else if point.Hook {
			enabled = a.monsterEnhanceHookMarked(&point, current)
		} else {
			enabled = bytesEqual(current, point.Patch)
		}
		if point.Available {
			available++
		}
		if enabled && point.Available {
			patched++
		}
		items = append(items, MonsterEnhanceItem{
			ID:                point.ID,
			Name:              point.Name,
			RVA:               uint64(point.RVA),
			Available:         point.Available,
			UnavailableReason: point.UnavailableReason,
			Enabled:           enabled,
			CurrentBytes:      currentHex,
		})
	}
	return MonsterEnhanceResult{
		PID:          a.charaPID,
		DLLPath:      dllPath,
		Enabled:      available > 0 && patched == available,
		CurrentBytes: strings.Join(parts, " | "),
		Items:        items,
	}, nil
}

func (a *App) setSBAChainTimer(point *monsterPatchPoint, value float64) error {
	addr := a.moduleBase + point.RVA
	current := make([]byte, len(point.Original))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return fmt.Errorf("读取%s失败: %w", point.Name, err)
	}
	if current[0] != 0x48 || current[1] != 0xB8 {
		return fmt.Errorf("%s指令字节未知: %s", point.Name, bytesToHex(current))
	}
	bits := math.Float32bits(float32(value))
	patch := append([]byte{}, point.Original...)
	binary.LittleEndian.PutUint32(patch[2:6], bits)
	binary.LittleEndian.PutUint32(patch[6:10], bits)
	if err := writeCodeMemory(a.hProcess, addr, patch); err != nil {
		return fmt.Errorf("写入%s失败: %w", point.Name, err)
	}
	return nil
}

func needsMonsterValue(id string) bool {
	return id == "monster_hp" || id == "monster_stun" || id == "monster_damage" || id == "crocodile_damage" || id == "overdrive_state"
}

func findMonsterPatchPoint(id string) *monsterPatchPoint {
	for i := range monsterPatchPoints {
		if monsterPatchPoints[i].ID == id {
			return &monsterPatchPoints[i]
		}
	}
	return nil
}

func extractPatchCoreDLL(patchID string) (string, error) {
	if len(patchCoreDLL) == 0 {
		return "", fmt.Errorf("内置 patch_core.dll 为空，请先编译 src_dll/patch_core Release x64")
	}
	dir := filepath.Join(os.TempDir(), "gbfr-player-info-edit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	pruneUnlockedPatchCoreDLLs(dir)
	if err := os.WriteFile(filepath.Join(dir, "patch_core_command.txt"), []byte(patchID), 0o644); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("patch_core_%d.dll", time.Now().UnixNano()))
	if err := os.WriteFile(path, patchCoreDLL, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func pruneUnlockedPatchCoreDLLs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "patch_core_") || !strings.HasSuffix(strings.ToLower(name), ".dll") {
			continue
		}
		// Loaded DLLs are locked by Windows and remain until the game exits.
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func injectDLL(h windows.Handle, dllPath string) error {
	utf16Path, err := windows.UTF16FromString(dllPath)
	if err != nil {
		return err
	}
	size := uintptr(len(utf16Path) * 2)
	remotePath, err := virtualAllocRemote(h, size, windows.PAGE_READWRITE)
	if err != nil {
		return err
	}
	releaseRemotePath := true
	defer func() {
		if releaseRemotePath {
			_ = virtualFreeRemote(h, remotePath)
		}
	}()

	if err := writeProcessMemory(h, remotePath, unsafe.Pointer(&utf16Path[0]), size); err != nil {
		return err
	}

	thread, err := createRemoteThread(h, procLoadLibraryW.Addr(), remotePath)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)

	wait, waitErr := windows.WaitForSingleObject(thread, 10000)
	if err := classifyRemoteCallWaitWithTimeout(wait, waitErr, "等待 10 秒后 DLL 注入线程仍未结束"); err != nil {
		// The remote thread may still read its argument. Keep that tiny allocation
		// alive until the game exits instead of introducing a use-after-free.
		releaseRemotePath = false
		return err
	}
	var exitCode uint32
	ret, _, callErr := procGetExitCodeThread.Call(uintptr(thread), uintptr(unsafe.Pointer(&exitCode)))
	if ret == 0 {
		return fmt.Errorf("读取 DLL 注入线程退出码失败: %w", callErr)
	}
	if exitCode == 0 {
		return fmt.Errorf("LoadLibraryW 返回空模块句柄")
	}
	return nil
}

func findPatternMatches(buf []byte, base uintptr, pattern []byte, mask []bool) []uintptr {
	if len(buf) < len(pattern) {
		return nil
	}
	var matches []uintptr
	for i := 0; i <= len(buf)-len(pattern); i++ {
		if matchPattern(buf[i:i+len(pattern)], pattern, mask) {
			matches = append(matches, base+uintptr(i))
		}
	}
	return matches
}

func matchPattern(buf []byte, pattern []byte, mask []bool) bool {
	if len(buf) < len(pattern) {
		return false
	}
	for i := range pattern {
		if mask[i] && buf[i] != pattern[i] {
			return false
		}
	}
	return true
}

func getRemoteModuleSize(h windows.Handle, moduleBase uintptr) (uintptr, error) {
	headers := make([]byte, 0x400)
	if err := readProcessMemory(h, moduleBase, unsafe.Pointer(&headers[0]), uintptr(len(headers))); err != nil {
		return 0, fmt.Errorf("读取模块头失败: %w", err)
	}
	if headers[0] != 'M' || headers[1] != 'Z' {
		return 0, fmt.Errorf("模块 DOS 头无效")
	}
	peOff := int(binary.LittleEndian.Uint32(headers[0x3C:0x40]))
	if peOff <= 0 || peOff+0x5C > len(headers) {
		return 0, fmt.Errorf("模块 PE 头偏移无效")
	}
	if headers[peOff] != 'P' || headers[peOff+1] != 'E' || headers[peOff+2] != 0 || headers[peOff+3] != 0 {
		return 0, fmt.Errorf("模块 PE 头无效")
	}
	sizeOfImage := binary.LittleEndian.Uint32(headers[peOff+0x18+0x38 : peOff+0x18+0x3C])
	if sizeOfImage == 0 {
		return 0, fmt.Errorf("模块 SizeOfImage 无效")
	}
	return uintptr(sizeOfImage), nil
}

// ── Windows 进程操作辅助函数 ──

func findProcessByName(name string) (uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	err = windows.Process32First(snap, &pe)
	if err != nil {
		return 0, err
	}
	for {
		exeName := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(exeName, name) {
			return pe.ProcessID, nil
		}
		err = windows.Process32Next(snap, &pe)
		if err != nil {
			break
		}
	}
	return 0, fmt.Errorf("进程未找到: %s", name)
}

var (
	modNtdll                      = windows.NewLazySystemDLL("ntdll.dll")
	procNtQueryInformationProcess = modNtdll.NewProc("NtQueryInformationProcess")
)

// getModuleBase reads the image base address from the remote process's PEB.
// This avoids module enumeration APIs which can fail with ERROR_PARTIAL_COPY.
func getModuleBase(hProcess windows.Handle) (uintptr, error) {
	// PROCESS_BASIC_INFORMATION (64-bit layout):
	//   ExitStatus          uintptr  (offset 0)
	//   PebBaseAddress      uintptr  (offset 8)
	//   AffinityMask        uintptr  (offset 16)
	//   BasePriority        uintptr  (offset 24)
	//   UniqueProcessId     uintptr  (offset 32)
	//   InheritedFromUnique uintptr  (offset 40)
	type processBasicInformation struct {
		ExitStatus                   uintptr
		PebBaseAddress               uintptr
		AffinityMask                 uintptr
		BasePriority                 uintptr
		UniqueProcessId              uintptr
		InheritedFromUniqueProcessId uintptr
	}

	var pbi processBasicInformation
	var retLen uint32
	r1, _, _ := procNtQueryInformationProcess.Call(
		uintptr(hProcess),
		0, // ProcessBasicInformation
		uintptr(unsafe.Pointer(&pbi)),
		unsafe.Sizeof(pbi),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if r1 != 0 {
		return 0, fmt.Errorf("NtQueryInformationProcess 失败: NTSTATUS 0x%X", r1)
	}
	if pbi.PebBaseAddress == 0 {
		return 0, fmt.Errorf("PEB 地址为空")
	}

	// Read ImageBaseAddress from PEB (offset 0x10 in 64-bit PEB)
	var imageBase uintptr
	err := readProcessMemory(hProcess, pbi.PebBaseAddress+0x10, unsafe.Pointer(&imageBase), unsafe.Sizeof(imageBase))
	if err != nil {
		return 0, fmt.Errorf("读取 PEB.ImageBaseAddress 失败: %v", err)
	}
	if imageBase == 0 {
		return 0, fmt.Errorf("ImageBaseAddress 为空")
	}
	return imageBase, nil
}

func readProcessMemory(h windows.Handle, addr uintptr, buf unsafe.Pointer, size uintptr) error {
	var read uintptr
	if err := windows.ReadProcessMemory(h, addr, (*byte)(buf), size, &read); err != nil {
		return err
	}
	return validateProcessTransfer("读取进程内存", size, read)
}

func writeProcessMemory(h windows.Handle, addr uintptr, buf unsafe.Pointer, size uintptr) error {
	var written uintptr
	if err := windows.WriteProcessMemory(h, addr, (*byte)(buf), size, &written); err != nil {
		return err
	}
	return validateProcessTransfer("写入进程内存", size, written)
}

func validateProcessTransfer(operation string, expected, actual uintptr) error {
	if actual != expected {
		return fmt.Errorf("%s不完整: %d/%d 字节", operation, actual, expected)
	}
	return nil
}

func writeCodeMemory(h windows.Handle, addr uintptr, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var oldProtect uint32
	if err := windows.VirtualProtectEx(h, addr, uintptr(len(data)), windows.PAGE_EXECUTE_READWRITE, &oldProtect); err != nil {
		return err
	}
	writeErr := writeProcessMemory(h, addr, unsafe.Pointer(&data[0]), uintptr(len(data)))
	if writeErr == nil {
		ret, _, callErr := procFlushInstructionCache.Call(uintptr(h), addr, uintptr(len(data)))
		if ret == 0 {
			if callErr == nil || callErr == windows.ERROR_SUCCESS {
				callErr = fmt.Errorf("FlushInstructionCache 失败")
			}
			writeErr = callErr
		}
	}
	var restoreProtect uint32
	restoreErr := windows.VirtualProtectEx(h, addr, uintptr(len(data)), oldProtect, &restoreProtect)
	return errors.Join(writeErr, restoreErr)
}

var (
	modKernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procVirtualAllocEx        = modKernel32.NewProc("VirtualAllocEx")
	procVirtualFreeEx         = modKernel32.NewProc("VirtualFreeEx")
	procVirtualQueryEx        = modKernel32.NewProc("VirtualQueryEx")
	procFlushInstructionCache = modKernel32.NewProc("FlushInstructionCache")
	procLoadLibraryW          = modKernel32.NewProc("LoadLibraryW")
	procGetExitCodeThread     = modKernel32.NewProc("GetExitCodeThread")
	procCreateRemoteThread    = modKernel32.NewProc("CreateRemoteThread")
)

type memoryBasicInformation struct {
	BaseAddress       uintptr
	AllocationBase    uintptr
	AllocationProtect uint32
	PartitionId       uint16
	RegionSize        uintptr
	State             uint32
	Protect           uint32
	Type              uint32
}

func virtualAllocRemote(h windows.Handle, size uintptr, protect uint32) (uintptr, error) {
	const (
		memCommit  = 0x1000
		memReserve = 0x2000
	)
	ret, _, callErr := procVirtualAllocEx.Call(
		uintptr(h),
		0,
		size,
		uintptr(memCommit|memReserve),
		uintptr(protect),
	)
	if ret == 0 {
		return 0, callErr
	}
	return ret, nil
}

func createRemoteThread(h windows.Handle, startAddr uintptr, param uintptr) (windows.Handle, error) {
	ret, _, callErr := procCreateRemoteThread.Call(
		uintptr(h),
		0,
		0,
		startAddr,
		param,
		0,
		0,
	)
	if ret == 0 {
		return 0, callErr
	}
	return windows.Handle(ret), nil
}

func virtualAllocRemoteNear(h windows.Handle, nearAddr uintptr, size uintptr) (uintptr, error) {
	const (
		memCommit                  = 0x1000
		memReserve                 = 0x2000
		memFree                    = 0x10000
		pageExecuteReadWrite       = 0x40
		allocGranularity           = uintptr(0x10000)
		maxRel32Distance     int64 = 0x7FFFFFFF
	)

	alignDown := func(v uintptr) uintptr { return v &^ (allocGranularity - 1) }
	tryAlloc := func(addr uintptr) uintptr {
		ret, _, _ := procVirtualAllocEx.Call(
			uintptr(h),
			addr,
			size,
			uintptr(memCommit|memReserve),
			uintptr(pageExecuteReadWrite),
		)
		return ret
	}
	isReachable := func(addr uintptr) bool {
		delta := int64(addr) - int64(nearAddr)
		if delta < 0 {
			delta = -delta
		}
		return delta <= maxRel32Distance
	}

	if addr := tryAlloc(nearAddr); addr != 0 && isReachable(addr) {
		return addr, nil
	}

	base := alignDown(nearAddr)
	for step := uintptr(0); step <= uintptr(maxRel32Distance); step += allocGranularity {
		candidates := [2]uintptr{}
		count := 0
		if step == 0 {
			candidates[count] = base
			count++
		} else {
			if base >= step {
				candidates[count] = base - step
				count++
			}
			if base <= ^uintptr(0)-step {
				candidates[count] = base + step
				count++
			}
		}

		for i := 0; i < count; i++ {
			candidate := candidates[i]
			if !isReachable(candidate) {
				continue
			}

			var mbi memoryBasicInformation
			ret, _, _ := procVirtualQueryEx.Call(
				uintptr(h),
				candidate,
				uintptr(unsafe.Pointer(&mbi)),
				unsafe.Sizeof(mbi),
			)
			if ret == 0 {
				continue
			}
			if mbi.State != memFree || mbi.RegionSize < size {
				continue
			}
			allocBase := alignDown(mbi.BaseAddress)
			if !isReachable(allocBase) {
				continue
			}
			if addr := tryAlloc(allocBase); addr != 0 && isReachable(addr) {
				return addr, nil
			}
		}
	}

	return 0, fmt.Errorf("VirtualAllocEx 附近分配失败")
}

func virtualFreeRemote(h windows.Handle, addr uintptr) error {
	ret, _, _ := procVirtualFreeEx.Call(
		uintptr(h),
		addr,
		0,
		uintptr(0x8000), // MEM_RELEASE
	)
	if ret == 0 {
		return fmt.Errorf("VirtualFreeEx 失败")
	}
	return nil
}
