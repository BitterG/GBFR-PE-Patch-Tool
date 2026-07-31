package main

import (
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	appVersion  = "v1.9.4"
	repoOwner   = "BitterG"
	repoName    = "GBFR-PE-Patch-Tool"
)

//go:embed build/bin/patch_core.dll
var patchCoreDLL []byte

// ── 补丁定义 ──

// PatchDef 描述一个补丁点
type PatchDef struct {
	ID         string // 唯一标识
	Name       string // 显示名称
	RVA        uint32 // 补丁目标 RVA
	OrigBytes  []byte // 原始字节（用于校验和恢复）
	PatchSize  int    // 补丁覆盖的字节数
	NeedCave   bool   // 是否需要代码跳板
	CallTarget uint32 // 跳板中 call 的目标 RVA（仅 NeedCave 时使用）
}

var patchDefs = []PatchDef{
	{
		ID:        "mission",
		Name:      "挑战次数",
		RVA:       0x003583FF,
		OrigBytes: []byte{0xB8, 0x3F, 0x42, 0x0F, 0x00, 0x41, 0x0F, 0x42, 0xC0},
		PatchSize: 9,
		NeedCave:  false,
	},
	{
		ID:        "likes",
		Name:      "点赞数值",
		RVA:       0x00A919CF,
		OrigBytes: []byte{0xB8, 0x3F, 0x42, 0x0F, 0x00, 0x0F, 0x42, 0xC6},
		PatchSize: 8,
		NeedCave:  false,
	},
}

// ── 状态结构 ──

type PatchStatus struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state"` // "original" | "patched" | "unknown"
	CurrentValue uint32 `json:"currentValue"`
	CurrentBytes string `json:"currentBytes"`
}

type StatusInfo struct {
	ExePath      string        `json:"exePath"`
	FileExists   bool          `json:"fileExists"`
	FileSize     int64         `json:"fileSize"`
	BackupExists bool          `json:"backupExists"`
	BackupSize   int64         `json:"backupSize"`
	Patches      []PatchStatus `json:"patches"`
}

type UpdateAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type UpdateInfo struct {
	CurrentVersion string        `json:"currentVersion"`
	LatestVersion  string        `json:"latestVersion"`
	HasUpdate      bool          `json:"hasUpdate"`
	ReleaseURL     string        `json:"releaseUrl"`
	Body           string        `json:"body"`
	Assets         []UpdateAsset `json:"assets"`
}

type AppConfig struct {
	LastSavePath string `json:"lastSavePath"`
	WindowWidth  int    `json:"windowWidth"`
	WindowHeight int    `json:"windowHeight"`
}

// ── App ──

type App struct {
	ctx                        context.Context
	exePath                    string
	hProcess                   windows.Handle
	moduleBase                 uintptr
	managerPtr                 uintptr
	charaListBase              uintptr
	charaPID                   uint32
	countdownAddr              uintptr
	faceAccessoryAddr          uintptr
	infiniteChallengeAddr      uintptr
	overLimitHookAddr          uintptr
	overLimitCaveAddr          uintptr
	overLimitCommitAddr        uintptr
	unlockAllTrophyAddr        uintptr
	terminusDropAddr           uintptr
	terminusDropOrig           []byte
	collectibleTaskBase        uintptr
	sigilMemoryHookAddr        uintptr
	sigilMemoryCaveAddr        uintptr
	sigilMemoryOriginal        []byte
	wrightstoneMemoryHookAddr  uintptr
	wrightstoneMemoryCaveAddr  uintptr
	wrightstoneMemoryOriginal  []byte
	summonMemoryHookAddr       uintptr
	summonMemoryCaveAddr       uintptr
	summonMemoryPointerAddr    uintptr
	summonMemoryOriginal       []byte
	damageMeterMapping         windows.Handle
	damageMeterView            uintptr
	damageOverlay              *damageOverlayWindow
	playerDamageMapping        windows.Handle
	playerDamageView           uintptr
	monsterDamageHookInstalled bool
	monsterDamageHookID        string
	monsterDamageEnabled       bool
	materialConsumeCaveAddr    uintptr
	flyingEnabled              bool
	flightSpeed                float32
	flightStop                 chan struct{}
	config                     AppConfig
	configLoaded               bool
}

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
	a.saveWindowSize(ctx)
	return false
}

func (a *App) shutdown(ctx context.Context) {
	a.saveWindowSize(ctx)
	if a.damageOverlay != nil {
		a.damageOverlay.stop()
	}
	a.closeDamageMeter()
	a.closePlayerDamageState()
	a.CharaDetach()
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
	return c.WindowWidth, c.WindowHeight
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
		HTMLURL string `json:"html_url"`
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
		ReleaseURL:     release.HTMLURL,
		Body:           release.Body,
	}
	for _, asset := range release.Assets {
		info.Assets = append(info.Assets, UpdateAsset{Name: asset.Name, URL: asset.URL})
	}
	return info, nil
}

func (a *App) OpenReleasePage(url string) error {
	if strings.TrimSpace(url) == "" {
		url = fmt.Sprintf("https://github.com/%s/%s/releases", repoOwner, repoName)
	}
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

// GetStatus 获取所有补丁点的状态
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
	if !info.FileExists {
		return info
	}

	data, err := os.ReadFile(a.exePath)
	if err != nil {
		return info
	}

	for _, def := range patchDefs {
		ps := PatchStatus{ID: def.ID, Name: def.Name, State: "unknown"}
		offset, ok := rvaToFileOffset(data, def.RVA)
		if !ok || int(offset)+def.PatchSize > len(data) {
			info.Patches = append(info.Patches, ps)
			continue
		}
		target := data[offset : offset+uint32(def.PatchSize)]
		ps.CurrentBytes = bytesToHex(target)

		if bytesEqual(target, def.OrigBytes) {
			ps.State = "original"
		} else if def.NeedCave {
			// 跳板补丁：检查是否为 JMP rel32 + NOPs
			if target[0] == 0xE9 && allNop(target[5:]) {
				ps.State = "patched"
				// 读取跳板中的值
				ps.CurrentValue = readCaveValue(data, offset, def)
			}
		} else {
			// 直接补丁：检查 B8 xx xx xx xx + NOP 填充
			if target[0] == 0xB8 && isNopFill(target[5:]) {
				ps.State = "patched"
				ps.CurrentValue = binary.LittleEndian.Uint32(target[1:5])
			}
		}
		info.Patches = append(info.Patches, ps)
	}
	return info
}

// PatchFile 对指定补丁点应用补丁
func (a *App) PatchFile(patchID string, value uint32) error {
	if a.exePath == "" {
		return fmt.Errorf("未选择文件")
	}

	def := findPatchDef(patchID)
	if def == nil {
		return fmt.Errorf("未知补丁: %s", patchID)
	}

	data, err := os.ReadFile(a.exePath)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}

	offset, ok := rvaToFileOffset(data, def.RVA)
	if !ok {
		return fmt.Errorf("无法定位 RVA 0x%X", def.RVA)
	}
	if int(offset)+def.PatchSize > len(data) {
		return fmt.Errorf("补丁超出文件范围")
	}

	target := data[offset : offset+uint32(def.PatchSize)]

	// 校验：必须是原始字节或已补丁状态
	isOrig := bytesEqual(target, def.OrigBytes)
	isPatched := false
	if def.NeedCave {
		isPatched = target[0] == 0xE9 && allNop(target[5:])
	} else {
		isPatched = target[0] == 0xB8 && isNopFill(target[5:])
	}
	if !isOrig && !isPatched {
		return fmt.Errorf("目标字节异常，拒绝补丁\n当前: %s", bytesToHex(target))
	}

	if def.NeedCave {
		err = applyCavePatch(data, offset, *def, value, isPatched)
	} else {
		err = applyDirectPatch(data, offset, *def, value)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(a.exePath, data, 0644)
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

// ── 补丁实现 ──

// applyDirectPatch 直接替换字节（mov eax,imm32 + NOP 填充）
func applyDirectPatch(data []byte, offset uint32, def PatchDef, value uint32) error {
	patch := make([]byte, def.PatchSize)
	patch[0] = 0xB8
	binary.LittleEndian.PutUint32(patch[1:5], value)
	// 剩余字节填 NOP
	switch def.PatchSize - 5 {
	case 4: // 9 字节: mov eax,imm32 + 4-byte NOP (0F 1F 40 00)
		patch[5] = 0x0F
		patch[6] = 0x1F
		patch[7] = 0x40
		patch[8] = 0x00
	case 3: // 8 字节: mov eax,imm32 + 3-byte NOP (0F 1F 00)
		patch[5] = 0x0F
		patch[6] = 0x1F
		patch[7] = 0x00
	default: // 其他情况用单字节 NOP 填充
		for i := 5; i < def.PatchSize; i++ {
			patch[i] = 0x90
		}
	}
	copy(data[offset:], patch)
	return nil
}

// applyCavePatch 使用代码跳板（用于 likes 类型）
func applyCavePatch(data []byte, offset uint32, def PatchDef, value uint32, alreadyPatched bool) error {
	// 跳板代码布局（17 字节）:
	//   B8 xx xx xx xx    ; mov eax, <value>
	//   89 01             ; mov [rcx], eax
	//   E8 yy yy yy yy   ; call <target>
	//   E9 zz zz zz zz   ; jmp back
	const caveSize = 17

	var caveOffset uint32
	var caveRVA uint32

	if alreadyPatched {
		// 已有跳板，读取 JMP 目标找到 cave 位置
		jmpRel := int32(binary.LittleEndian.Uint32(data[offset+1 : offset+5]))
		jmpNextRVA := def.RVA + 5
		caveRVA = uint32(int32(jmpNextRVA) + jmpRel)
		var ok bool
		caveOffset, ok = rvaToFileOffset(data, caveRVA)
		if !ok {
			return fmt.Errorf("无法定位已有跳板")
		}
	} else {
		// 首次补丁：在 .text 段末尾找空间
		var ok bool
		caveRVA, caveOffset, ok = findCaveSpace(data, caveSize)
		if !ok {
			return fmt.Errorf("找不到可用的代码空间")
		}
	}

	// 写跳板代码
	cave := make([]byte, caveSize)
	cave[0] = 0xB8
	binary.LittleEndian.PutUint32(cave[1:5], value)
	cave[5] = 0x89
	cave[6] = 0x01 // mov [rcx], eax

	// call <target>: E8 rel32, rel32 = target - (cave_call_rva + 5)
	cave[7] = 0xE8
	callRVA := caveRVA + 7
	callRel := int32(def.CallTarget) - int32(callRVA+5)
	binary.LittleEndian.PutUint32(cave[8:12], uint32(callRel))

	// jmp back: E9 rel32, rel32 = return_rva - (cave_jmp_rva + 5)
	cave[12] = 0xE9
	returnRVA := def.RVA + uint32(def.PatchSize)
	jmpRVA := caveRVA + 12
	jmpRel := int32(returnRVA) - int32(jmpRVA+5)
	binary.LittleEndian.PutUint32(cave[13:17], uint32(jmpRel))

	copy(data[caveOffset:], cave)

	// 写原始位置的 JMP + NOPs
	patch := make([]byte, def.PatchSize)
	patch[0] = 0xE9
	origJmpRel := int32(caveRVA) - int32(def.RVA+5)
	binary.LittleEndian.PutUint32(patch[1:5], uint32(origJmpRel))
	for i := 5; i < def.PatchSize; i++ {
		patch[i] = 0x90 // NOP
	}
	copy(data[offset:], patch)

	return nil
}

// findCaveSpace 在 PE 段的 rawData 末尾找零填充区，
// 并扩展 VirtualSize + SizeOfImage 确保运行时该区域被映射到内存。
func findCaveSpace(data []byte, size int) (rva uint32, fileOffset uint32, ok bool) {
	peOffset := binary.LittleEndian.Uint32(data[0x3C:0x40])
	coffHeader := peOffset + 4
	numSections := binary.LittleEndian.Uint16(data[coffHeader+2 : coffHeader+4])
	optHeaderSize := binary.LittleEndian.Uint16(data[coffHeader+16 : coffHeader+18])
	sectionStart := coffHeader + 20 + uint32(optHeaderSize)
	optHeader := coffHeader + 20

	// SizeOfImage 在 optional header offset 56 (PE32+)
	sizeOfImageOff := optHeader + 56
	// SectionAlignment 在 optional header offset 32
	sectionAlignment := binary.LittleEndian.Uint32(data[optHeader+32 : optHeader+36])

	for i := uint16(0); i < numSections; i++ {
		off := sectionStart + uint32(i)*40
		if int(off)+40 > len(data) {
			continue
		}
		virtualSize := binary.LittleEndian.Uint32(data[off+8 : off+12])
		virtualAddr := binary.LittleEndian.Uint32(data[off+12 : off+16])
		rawSize := binary.LittleEndian.Uint32(data[off+16 : off+20])
		rawPtr := binary.LittleEndian.Uint32(data[off+20 : off+24])
		characteristics := binary.LittleEndian.Uint32(data[off+36 : off+40])

		isExecutable := (characteristics & 0x20000020) != 0
		if !isExecutable || rawSize == 0 || rawPtr == 0 {
			continue
		}

		rawEnd := rawPtr + rawSize
		if rawEnd > uint32(len(data)) {
			rawEnd = uint32(len(data))
		}

		// 从段 raw 末尾往前找连续零字节
		zeroCount := 0
		for pos := int(rawEnd) - 1; pos >= int(rawPtr) && pos >= 0; pos-- {
			if data[pos] == 0 {
				zeroCount++
			} else {
				break
			}
		}
		if zeroCount < size+16 {
			continue
		}

		caveFileOff := rawEnd - uint32(size) - 8
		caveRVA := virtualAddr + (caveFileOff - rawPtr)

		// 关键：如果 cave 超出 virtualSize，扩展 VirtualSize 使其被映射到内存
		caveEnd := caveRVA - virtualAddr + uint32(size) + 8
		if caveEnd > virtualSize {
			// 对齐到 SectionAlignment
			newVirtualSize := alignUp(caveEnd, sectionAlignment)
			binary.LittleEndian.PutUint32(data[off+8:off+12], newVirtualSize)

			// 更新 SizeOfImage = 最后一个段的 VirtualAddress + 对齐后的 VirtualSize
			// 找最后一个段来计算
			newSizeOfImage := uint32(0)
			for j := uint16(0); j < numSections; j++ {
				soff := sectionStart + uint32(j)*40
				va := binary.LittleEndian.Uint32(data[soff+12 : soff+16])
				vs := binary.LittleEndian.Uint32(data[soff+8 : soff+12])
				end := va + alignUp(vs, sectionAlignment)
				if end > newSizeOfImage {
					newSizeOfImage = end
				}
			}
			binary.LittleEndian.PutUint32(data[sizeOfImageOff:sizeOfImageOff+4], newSizeOfImage)
		}

		return caveRVA, caveFileOff, true
	}
	return 0, 0, false
}

func alignUp(value, alignment uint32) uint32 {
	if alignment == 0 {
		return value
	}
	return (value + alignment - 1) & ^(alignment - 1)
}

// readCaveValue 从跳板中读取当前值
func readCaveValue(data []byte, offset uint32, def PatchDef) uint32 {
	if data[offset] != 0xE9 {
		return 0
	}
	jmpRel := int32(binary.LittleEndian.Uint32(data[offset+1 : offset+5]))
	caveRVA := uint32(int32(def.RVA+5) + jmpRel)
	caveOffset, ok := rvaToFileOffset(data, caveRVA)
	if !ok || int(caveOffset)+5 > len(data) {
		return 0
	}
	if data[caveOffset] != 0xB8 {
		return 0
	}
	return binary.LittleEndian.Uint32(data[caveOffset+1 : caveOffset+5])
}

func allNop(b []byte) bool {
	for _, v := range b {
		if v != 0x90 {
			return false
		}
	}
	return true
}

// isNopFill 检查字节是否为已知的多字节 NOP 填充
func isNopFill(b []byte) bool {
	switch len(b) {
	case 4: // 0F 1F 40 00
		return b[0] == 0x0F && b[1] == 0x1F && b[2] == 0x40 && b[3] == 0x00
	case 3: // 0F 1F 00
		return b[0] == 0x0F && b[1] == 0x1F && b[2] == 0x00
	default:
		return allNop(b)
	}
}

func findPatchDef(id string) *PatchDef {
	for i := range patchDefs {
		if patchDefs[i].ID == id {
			return &patchDefs[i]
		}
	}
	return nil
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

func rvaToFileOffset(data []byte, rva uint32) (uint32, bool) {
	if len(data) < 64 {
		return 0, false
	}
	if data[0] != 'M' || data[1] != 'Z' {
		return 0, false
	}
	peOffset := binary.LittleEndian.Uint32(data[0x3C:0x40])
	if int(peOffset)+24 > len(data) {
		return 0, false
	}
	if data[peOffset] != 'P' || data[peOffset+1] != 'E' || data[peOffset+2] != 0 || data[peOffset+3] != 0 {
		return 0, false
	}
	coffHeader := peOffset + 4
	numSections := binary.LittleEndian.Uint16(data[coffHeader+2 : coffHeader+4])
	optHeaderSize := binary.LittleEndian.Uint16(data[coffHeader+16 : coffHeader+18])
	optHeader := coffHeader + 20
	if int(optHeader)+2 > len(data) {
		return 0, false
	}
	magic := binary.LittleEndian.Uint16(data[optHeader : optHeader+2])
	if magic != 0x020B {
		return 0, false
	}
	sectionStart := optHeader + uint32(optHeaderSize)
	for i := uint16(0); i < numSections; i++ {
		off := sectionStart + uint32(i)*40
		if int(off)+40 > len(data) {
			return 0, false
		}
		virtualSize := binary.LittleEndian.Uint32(data[off+8 : off+12])
		virtualAddr := binary.LittleEndian.Uint32(data[off+12 : off+16])
		rawSize := binary.LittleEndian.Uint32(data[off+16 : off+20])
		rawPtr := binary.LittleEndian.Uint32(data[off+20 : off+24])
		span := rawSize
		if virtualSize > span {
			span = virtualSize
		}
		if rva >= virtualAddr && rva < virtualAddr+span {
			return rawPtr + (rva - virtualAddr), true
		}
	}
	return 0, false
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
	RVA    uintptr
	Offset uintptr
}

var currencyDefs = []currencyDef{
	{ID: "msp", Name: "MSP", RVA: 0x0701E220, Offset: 0x98},
	{ID: "rupies", Name: "金币", RVA: 0x0701E220, Offset: 0x30},
	{ID: "purple_msp", Name: "紫MSP", RVA: 0x07C49CB0, Offset: 0x9C},
	{ID: "cp_extreme_void", Name: "CP(极沌空域)", RVA: 0x07C23E38, Offset: 0x24},
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
	// Close existing handle if any
	if a.hProcess != 0 {
		a.CharaDetach()
	}

	pid, err := findProcessByName(charaProcessName)
	if err != nil {
		return CharaProcessInfo{}, fmt.Errorf("未找到游戏进程，请先启动游戏")
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

	a.hProcess = h
	a.moduleBase = modBase
	a.charaPID = pid
	manager, err := a.charaManager()
	if err != nil {
		a.CharaDetach()
		return CharaProcessInfo{}, err
	}
	a.managerPtr = manager

	return CharaProcessInfo{
		PID:        pid,
		ModuleBase: uint64(modBase),
		Manager:    uint64(manager),
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
	if err := a.ensureGameProcess(); err != nil {
		return CollectibleTaskStatus{}, err
	}
	base, err := a.collectibleTaskAddress()
	if err != nil {
		return CollectibleTaskStatus{}, err
	}

	flags := make([]byte, collectibleTaskEntries*int(collectibleTaskStride))
	if err := readProcessMemory(a.hProcess, base, unsafe.Pointer(&flags[0]), uintptr(len(flags))); err != nil {
		return CollectibleTaskStatus{}, fmt.Errorf("读取收集任务列表失败: %w", err)
	}
	for i := 0; i < collectibleTaskEntries; i++ {
		flags[i*int(collectibleTaskStride)+int(collectibleTaskFlag)] = 1
	}
	if err := writeProcessMemory(a.hProcess, base, unsafe.Pointer(&flags[0]), uintptr(len(flags))); err != nil {
		return CollectibleTaskStatus{}, fmt.Errorf("写入收集任务完成标记失败: %w", err)
	}
	return CollectibleTaskStatus{Found: true, Address: uint64(base), Completed: collectibleTaskEntries, Total: collectibleTaskEntries}, nil
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

// CharaDetach closes the process handle.
func (a *App) CharaDetach() {
	// Restore hooks while the target process is still available. Leaving the
	// jump installed makes a later tool instance mistake it for an unsupported
	// game build and can also leave the game executing tool-owned code.
	_ = a.releaseSigilMemoryHook()
	_ = a.releaseSummonMemoryHook()
	_ = a.releaseMaterialConsumeHook()
	// Stop the input loop before closing the target handle.
	a.FlightSetEnabled(false, 0)
	if a.hProcess != 0 {
		windows.CloseHandle(a.hProcess)
		a.hProcess = 0
	}
	a.moduleBase = 0
	a.managerPtr = 0
	a.charaListBase = 0
	a.charaPID = 0
	a.countdownAddr = 0
	a.faceAccessoryAddr = 0
	a.infiniteChallengeAddr = 0
	a.overLimitHookAddr = 0
	a.overLimitCaveAddr = 0
	a.overLimitCommitAddr = 0
	a.unlockAllTrophyAddr = 0
	a.terminusDropAddr = 0
	a.terminusDropOrig = nil
	a.collectibleTaskBase = 0
	a.monsterDamageHookInstalled = false
	a.monsterDamageHookID = ""
	a.monsterDamageEnabled = false
	a.materialConsumeCaveAddr = 0
	a.sigilMemoryHookAddr = 0
	a.sigilMemoryCaveAddr = 0
	a.sigilMemoryOriginal = nil
	a.wrightstoneMemoryHookAddr = 0
	a.wrightstoneMemoryCaveAddr = 0
	a.wrightstoneMemoryOriginal = nil
	a.summonMemoryHookAddr = 0
	a.summonMemoryCaveAddr = 0
	a.summonMemoryPointerAddr = 0
	a.summonMemoryOriginal = nil
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
	var base uintptr
	ptrAddr := a.moduleBase + def.RVA
	if err := readProcessMemory(a.hProcess, ptrAddr, unsafe.Pointer(&base), unsafe.Sizeof(base)); err != nil {
		return 0, fmt.Errorf("读取%s指针失败: %w", def.Name, err)
	}
	if base == 0 {
		return 0, fmt.Errorf("%s指针为空，请确保已进入游戏存档", def.Name)
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
	return CurrencyInfo{ID: def.ID, Name: def.Name, RVA: uint64(def.RVA), Offset: uint64(def.Offset), Address: uint64(addr), Value: value}, nil
}

// CurrencyGetAll reads all supported currency values from stable pointer paths.
func (a *App) CurrencyGetAll() ([]CurrencyInfo, error) {
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
	id = strings.TrimSpace(id)
	if value < 0 || value > math.MaxInt32 {
		return CurrencyInfo{}, fmt.Errorf("请输入 0 到 %d 之间的整数", math.MaxInt32)
	}
	for _, def := range currencyDefs {
		if def.ID != id {
			continue
		}
		addr, err := a.currencyAddress(def)
		if err != nil {
			return CurrencyInfo{}, err
		}
		newVal := int32(value)
		if err := writeProcessMemory(a.hProcess, addr, unsafe.Pointer(&newVal), unsafe.Sizeof(newVal)); err != nil {
			return CurrencyInfo{}, fmt.Errorf("写入%s失败: %w", def.Name, err)
		}
		return a.readCurrency(def)
	}
	return CurrencyInfo{}, fmt.Errorf("未知货币: %s", id)
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
		newVal := int32(value)
		if err := writeProcessMemory(a.hProcess, addr, unsafe.Pointer(&newVal), unsafe.Sizeof(newVal)); err != nil {
			return PotionInfo{}, fmt.Errorf("写入%s失败: %w", def.Name, err)
		}
		return a.readPotion(def)
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
	CurrentBytes string `json:"currentBytes"`
}

var (
	infiniteChallengePattern = []byte{0x41, 0xFF, 0, 0xB2, 0, 0xE9}
	infiniteChallengeMask    = []bool{true, true, false, true, false, true}
	infiniteChallengeOrig    = []byte{0x41, 0xFF, 0xC0}
	infiniteChallengePatch   = []byte{0x90, 0x90, 0x90}
)

func (a *App) InfiniteChallengeGetStatus() (InfiniteChallengeStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	addr, err := a.infiniteChallengeAddress()
	if err != nil {
		return InfiniteChallengeStatus{}, err
	}
	return a.readInfiniteChallengeStatus(addr)
}

func (a *App) InfiniteChallengeSetEnabled(enabled bool) (InfiniteChallengeStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return InfiniteChallengeStatus{}, err
	}
	addr, err := a.infiniteChallengeAddress()
	if err != nil {
		return InfiniteChallengeStatus{}, err
	}
	patch := infiniteChallengeOrig
	if enabled {
		patch = infiniteChallengePatch
	}
	if err := writeCodeMemory(a.hProcess, addr, patch); err != nil {
		return InfiniteChallengeStatus{}, fmt.Errorf("写入无限挑战失败: %w", err)
	}
	return a.readInfiniteChallengeStatus(addr)
}

func (a *App) infiniteChallengeAddress() (uintptr, error) {
	if a.infiniteChallengeAddr != 0 {
		return a.infiniteChallengeAddr, nil
	}
	addr, err := a.scanPatternUnique(infiniteChallengePattern, infiniteChallengeMask, "无限挑战特征")
	if err != nil {
		return 0, err
	}
	a.infiniteChallengeAddr = addr
	return addr, nil
}

func (a *App) readInfiniteChallengeStatus(addr uintptr) (InfiniteChallengeStatus, error) {
	buf := make([]byte, len(infiniteChallengeOrig))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return InfiniteChallengeStatus{}, fmt.Errorf("读取无限挑战指令失败: %w", err)
	}
	if !bytesEqual(buf, infiniteChallengeOrig) && !bytesEqual(buf, infiniteChallengePatch) {
		return InfiniteChallengeStatus{}, fmt.Errorf("无限挑战指令字节异常: %s", bytesToHex(buf))
	}
	return InfiniteChallengeStatus{
		RVA:          uint64(addr - a.moduleBase),
		Enabled:      bytesEqual(buf, infiniteChallengePatch),
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
	materialConsumeOrig        = []byte{0x41, 0x01, 0x76, 0x04, 0x4C, 0x89, 0xE1}
	materialConsumeLegacyPatch = []byte{0x90, 0x90, 0x90, 0x90, 0x4C, 0x89, 0xE1}
)

func (a *App) MaterialConsumeGetStatus() (MaterialConsumeStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	return a.readMaterialConsumeStatus()
}

func (a *App) MaterialConsumeSetEnabled(enabled bool) (MaterialConsumeStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	if enabled {
		if err := a.installMaterialConsumeHook(); err != nil {
			return MaterialConsumeStatus{}, err
		}
	} else if err := a.releaseMaterialConsumeHook(); err != nil {
		return MaterialConsumeStatus{}, err
	}
	return a.readMaterialConsumeStatus()
}

func (a *App) installMaterialConsumeHook() error {
	addr := a.moduleBase + materialConsumeRVA
	current := make([]byte, len(materialConsumeOrig))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return fmt.Errorf("读取升级/强化材料消耗指令失败: %w", err)
	}
	if isMaterialConsumeHook(current) {
		return nil
	}
	if !bytesEqual(current, materialConsumeOrig) && !bytesEqual(current, materialConsumeLegacyPatch) {
		return fmt.Errorf("升级/强化材料消耗指令字节异常: %s", bytesToHex(current))
	}

	const caveSize = 25
	caveAddr, err := virtualAllocRemoteNear(a.hProcess, addr, caveSize)
	if err != nil {
		return fmt.Errorf("分配升级/强化材料消耗代码洞失败: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = virtualFreeRemote(a.hProcess, caveAddr)
		}
	}()

	// 仅在 esi 为负数时跳过 add；药水、奖励等正数增量保留原逻辑。
	cave := []byte{
		0x85, 0xF6,
		0x0F, 0x89, 0x05, 0x00, 0x00, 0x00,
		0xE9, 0x00, 0x00, 0x00, 0x00,
		0x41, 0x01, 0x76, 0x04,
		0x4C, 0x89, 0xE1,
		0xE9, 0x00, 0x00, 0x00, 0x00,
	}
	returnAddr := addr + uintptr(len(materialConsumeOrig))
	binary.LittleEndian.PutUint32(cave[9:13], uint32(int64(returnAddr)-int64(caveAddr+13)))
	binary.LittleEndian.PutUint32(cave[21:25], uint32(int64(returnAddr)-int64(caveAddr+25)))
	if err := writeCodeMemory(a.hProcess, caveAddr, cave); err != nil {
		return fmt.Errorf("写入升级/强化材料消耗代码洞失败: %w", err)
	}

	patch := []byte{0xE9, 0, 0, 0, 0, 0x90, 0x90}
	binary.LittleEndian.PutUint32(patch[1:5], uint32(int64(caveAddr)-int64(addr+5)))
	if err := writeCodeMemory(a.hProcess, addr, patch); err != nil {
		return fmt.Errorf("写入升级/强化材料消耗跳转失败: %w", err)
	}
	a.materialConsumeCaveAddr = caveAddr
	cleanup = false
	return nil
}

func (a *App) releaseMaterialConsumeHook() error {
	if a.hProcess == 0 || a.moduleBase == 0 {
		return nil
	}
	addr := a.moduleBase + materialConsumeRVA
	current := make([]byte, len(materialConsumeOrig))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return fmt.Errorf("读取升级/强化材料消耗指令失败: %w", err)
	}
	if bytesEqual(current, materialConsumeOrig) {
		return nil
	}
	if !isMaterialConsumeHook(current) && !bytesEqual(current, materialConsumeLegacyPatch) {
		return fmt.Errorf("升级/强化材料消耗指令字节异常: %s", bytesToHex(current))
	}
	if err := writeCodeMemory(a.hProcess, addr, materialConsumeOrig); err != nil {
		return fmt.Errorf("恢复升级/强化材料消耗指令失败: %w", err)
	}
	if a.materialConsumeCaveAddr != 0 {
		if err := virtualFreeRemote(a.hProcess, a.materialConsumeCaveAddr); err != nil {
			return fmt.Errorf("释放升级/强化材料消耗代码洞失败: %w", err)
		}
		a.materialConsumeCaveAddr = 0
	}
	return nil
}

func isMaterialConsumeHook(buf []byte) bool {
	return len(buf) == len(materialConsumeOrig) && buf[0] == 0xE9 && buf[5] == 0x90 && buf[6] == 0x90
}

func (a *App) readMaterialConsumeStatus() (MaterialConsumeStatus, error) {
	addr := a.moduleBase + materialConsumeRVA
	buf := make([]byte, len(materialConsumeOrig))
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return MaterialConsumeStatus{}, fmt.Errorf("读取升级/强化材料消耗指令失败: %w", err)
	}
	if !bytesEqual(buf, materialConsumeOrig) && !bytesEqual(buf, materialConsumeLegacyPatch) && !isMaterialConsumeHook(buf) {
		return MaterialConsumeStatus{}, fmt.Errorf("升级/强化材料消耗指令字节异常: %s", bytesToHex(buf))
	}
	return MaterialConsumeStatus{
		RVA:          uint64(materialConsumeRVA),
		Enabled:      !bytesEqual(buf, materialConsumeOrig),
		CurrentBytes: bytesToHex(buf),
	}, nil
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
	if err := a.ensureGameProcess(); err != nil {
		return TerminusDropStatus{}, err
	}
	addr, err := a.scanPatternUnique(terminusDropPattern, terminusDropMask, "巴武掉落特征")
	if err != nil {
		a.terminusDropAddr = 0
		return TerminusDropStatus{}, err
	}
	a.terminusDropAddr = addr
	return a.readTerminusDropStatus(addr)
}

func (a *App) TerminusDropGetStatus() (TerminusDropStatus, error) {
	if err := a.ensureGameProcess(); err != nil {
		return TerminusDropStatus{}, err
	}
	if a.terminusDropAddr == 0 {
		return a.TerminusDropScan()
	}
	status, err := a.readTerminusDropStatus(a.terminusDropAddr)
	if err != nil {
		a.terminusDropAddr = 0
		return a.TerminusDropScan()
	}
	return status, nil
}

func (a *App) TerminusDropSetEnabled(enabled bool) (TerminusDropStatus, error) {
	status, err := a.TerminusDropGetStatus()
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
	if err := writeCodeMemory(a.hProcess, a.terminusDropAddr, patch); err != nil {
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
	pid, err := findProcessByName(charaProcessName)
	if err != nil {
		return fmt.Errorf("未找到游戏进程，请先启动游戏")
	}
	if a.hProcess != 0 && a.moduleBase != 0 && a.charaPID == pid {
		return nil
	}
	// The game was restarted while the tool stayed open. Drop every address
	// derived from the old process before attaching to the new PID.
	if a.hProcess != 0 || a.moduleBase != 0 || a.charaPID != 0 {
		a.CharaDetach()
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
	a.hProcess = h
	a.moduleBase = modBase
	a.charaPID = pid
	return nil
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
	ID           string `json:"id"`
	Name         string `json:"name"`
	RVA          uint64 `json:"rva"`
	Enabled      bool   `json:"enabled"`
	CurrentBytes string `json:"currentBytes"`
}

type monsterPatchPoint struct {
	ID       string
	Name     string
	RVA      uintptr
	Original []byte
	Patch    []byte
	Hook     bool
}

var monsterPatchPoints = []monsterPatchPoint{
	{
		ID:       "link_time_no_drain",
		Name:     "无限 link time",
		RVA:      0x187228,
		Original: []byte{0xC4, 0xC1, 0x7A, 0x11, 0x9C, 0x24, 0xB4, 0x01, 0x00, 0x00},
		Patch:    []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
	},
	{
		ID:       "link_time_disable",
		Name:     "无法进入 link time",
		RVA:      0x187228,
		Original: []byte{0xC4, 0xC1, 0x7A, 0x11, 0x9C, 0x24, 0xB4, 0x01, 0x00, 0x00},
		Patch:    []byte{0xC4, 0xC1, 0x7A, 0x11, 0x84, 0x24, 0xB4, 0x01, 0x00, 0x00},
	},
	{
		ID:       "monster_hp",
		Name:     "怪物多倍血",
		RVA:      0x1F7A820,
		Original: []byte{0x48, 0x8B, 0x41, 0x10, 0x45, 0x31, 0xC9},
		Hook:     true,
	},
	{
		ID:       "monster_damage_new",
		Name:     "怪物伤害-新",
		RVA:      0x1F7A810,
		Original: []byte{0x48, 0x89, 0x51, 0x10, 0xC3, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC},
		Hook:     true,
	},
	{
		ID:       "monster_damage",
		Name:     "怪物伤害",
		RVA:      0x1FBDEB4,
		Original: []byte{0x81, 0xBE, 0xD4, 0x00, 0x00, 0x00, 0x00, 0xE1, 0xF5, 0x05},
		Hook:     true,
	},
	{
		ID:       "monster_stun",
		Name:     "怪物多倍昏厥条",
		RVA:      0xB29128,
		Original: []byte{0xC5, 0xFA, 0x58, 0x86, 0x60, 0x08, 0x00, 0x00},
		Hook:     true,
	},
	{
		ID:       "overdrive_state",
		Name:     "怪物 Overdrive 状态",
		RVA:      0x22CB316,
		Original: []byte{0x8B, 0x46, 0x10, 0x83, 0xF8, 0x03, 0x0F, 0x84, 0xC7, 0x00, 0x00, 0x00},
		Hook:     true,
	},
	{
		ID:       "inventory_set_45",
		Name:     "设置背包物品数量为 45",
		RVA:      0x356621,
		Original: []byte{0x41, 0x01, 0x76, 0x04, 0x4C, 0x89, 0xE1},
		Hook:     true,
	},
	{
		ID:       "sba_chain_timer",
		Name:     "奥义接续计时",
		RVA:      0x677B45,
		Original: []byte{0x48, 0xB8, 0x00, 0x00, 0x40, 0x40, 0x00, 0x00, 0x40, 0x40},
	},
	{
		ID:       "purple_drain",
		Name:     "紫条不自然扣减",
		RVA:      0xA0379A,
		Original: []byte{0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x10, 0x0A, 0x00, 0x00},
		Patch:    []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
	},
	{
		ID:       "blue_grow",
		Name:     "昏厥蓝条不增长",
		RVA:      0xA09AF1,
		Original: []byte{0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x20, 0x07, 0x00, 0x00},
		Patch:    []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
	},
	{
		ID:       "blue_drain",
		Name:     "昏厥蓝条不自然扣减",
		RVA:      0xA03F38,
		Original: []byte{0xC4, 0xC1, 0x7A, 0x11, 0x85, 0x70, 0x0A, 0x00, 0x00},
		Patch:    []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
	},
}

const damageMeterMappingName = "Local\\GBFRPlayerInfoEditDamageMeterV4"
const damageMeterSize = 8
const playerDamageMappingName = "Local\\GBFRPlayerInfoEditPlayerPointersV1"
const playerDamageStateSize = 80
const playerDamageEnabledOffset = 4
const playerDamageScaleOffset = 8

type DamageMeterStatus struct {
	Connected     bool   `json:"connected"`
	TotalDamage   uint64 `json:"totalDamage"`
	MonsterDamage uint64 `json:"monsterDamage"`
}

func (a *App) DamageMeterGetStatus() (DamageMeterStatus, error) {
	if err := a.ensureDamageMeter(); err != nil {
		return DamageMeterStatus{}, err
	}
	monsterDamage := uint64(*(*int64)(unsafe.Pointer(a.damageMeterView)))
	return DamageMeterStatus{Connected: true, TotalDamage: monsterDamage, MonsterDamage: monsterDamage}, nil
}

func (a *App) DamageMeterReset() (DamageMeterStatus, error) {
	if err := a.ensureDamageMeter(); err != nil {
		return DamageMeterStatus{}, err
	}
	for i := 0; i < damageMeterSize; i++ {
		*(*byte)(unsafe.Pointer(a.damageMeterView + uintptr(i))) = 0
	}
	return DamageMeterStatus{Connected: true}, nil
}

func (a *App) ensureDamageMeter() error {
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
	if a.damageMeterView != 0 {
		_ = windows.UnmapViewOfFile(a.damageMeterView)
		a.damageMeterView = 0
	}
	if a.damageMeterMapping != 0 {
		_ = windows.CloseHandle(a.damageMeterMapping)
		a.damageMeterMapping = 0
	}
}

func (a *App) ensurePlayerDamageState() error {
	if a.playerDamageView != 0 {
		return nil
	}
	name, err := windows.UTF16PtrFromString(playerDamageMappingName)
	if err != nil {
		return err
	}
	mapping, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, playerDamageStateSize, name)
	if err != nil && (mapping == 0 || err != windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("创建玩家伤害共享内存失败: %w", err)
	}
	view, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_READ|windows.FILE_MAP_WRITE, 0, 0, playerDamageStateSize)
	if err != nil {
		windows.CloseHandle(mapping)
		return fmt.Errorf("映射玩家伤害共享内存失败: %w", err)
	}
	a.playerDamageMapping = mapping
	a.playerDamageView = view
	return nil
}

func (a *App) setPlayerDamageEnabled(enabled bool, scale float64) error {
	if err := a.ensurePlayerDamageState(); err != nil {
		return err
	}
	if enabled {
		*(*uint32)(unsafe.Pointer(a.playerDamageView + playerDamageScaleOffset)) = math.Float32bits(float32(scale))
	}
	if enabled {
		*(*int32)(unsafe.Pointer(a.playerDamageView + playerDamageEnabledOffset)) = 1
	} else {
		*(*int32)(unsafe.Pointer(a.playerDamageView + playerDamageEnabledOffset)) = 0
	}
	return nil
}

func (a *App) closePlayerDamageState() {
	if a.playerDamageView != 0 {
		_ = windows.UnmapViewOfFile(a.playerDamageView)
		a.playerDamageView = 0
	}
	if a.playerDamageMapping != 0 {
		_ = windows.CloseHandle(a.playerDamageMapping)
		a.playerDamageMapping = 0
	}
}

func (a *App) MonsterEnhanceGetStatus() (MonsterEnhanceResult, error) {
	if err := a.ensureGameProcess(); err != nil {
		return MonsterEnhanceResult{}, err
	}
	return a.readMonsterEnhanceStatus("")
}

func (a *App) MonsterEnhanceSetEnabled(enabled bool) (MonsterEnhanceResult, error) {
	return a.MonsterEnhanceSetPatchEnabled("all", enabled)
}

func (a *App) MonsterEnhanceSetPatchEnabled(id string, enabled bool) (MonsterEnhanceResult, error) {
	return a.MonsterEnhanceSetPatchValueEnabled(id, enabled, 0)
}

func (a *App) MonsterEnhanceSetPatchValueEnabled(id string, enabled bool, hpMultiplier float64) (MonsterEnhanceResult, error) {
	if err := a.ensureGameProcess(); err != nil {
		return MonsterEnhanceResult{}, err
	}
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
	if pointID == "monster_damage" {
		return MonsterEnhanceResult{}, fmt.Errorf("旧怪物伤害已移除，请使用怪物伤害-新")
	}
	point := findMonsterPatchPoint(pointID)
	if pointID != "all" && point == nil {
		return MonsterEnhanceResult{}, fmt.Errorf("未知怪物增强项目: %s", id)
	}
	if enabled && point != nil && point.ID != "overdrive_state" && needsMonsterValue(point.ID) && (math.IsNaN(hpMultiplier) || math.IsInf(hpMultiplier, 0) || hpMultiplier <= 0 || hpMultiplier > 9999) {
		return MonsterEnhanceResult{}, fmt.Errorf("怪物倍率请输入 0 到 9999 之间的数值")
	}
	if enabled && point != nil && point.ID == "sba_chain_timer" && (math.IsNaN(hpMultiplier) || math.IsInf(hpMultiplier, 0) || hpMultiplier <= 0 || hpMultiplier > 9999) {
		return MonsterEnhanceResult{}, fmt.Errorf("奥义接续计时请输入 0 到 9999 之间的数值")
	}
	if enabled && point != nil && point.ID == "overdrive_state" && (math.IsNaN(hpMultiplier) || math.IsInf(hpMultiplier, 0) || (hpMultiplier != 0 && hpMultiplier != 3 && hpMultiplier != 9)) {
		return MonsterEnhanceResult{}, fmt.Errorf("Overdrive 状态请选择 空条、满黄条或自动OD")
	}

	if (pointID == "monster_damage" || pointID == "monster_damage_new") && a.monsterDamageHookInstalled {
		if a.monsterDamageHookID != pointID {
			return MonsterEnhanceResult{}, fmt.Errorf("%s与%s共用玩家伤害 Hook，请先重启游戏后再切换", point.Name, a.monsterDamageHookID)
		}
		if err := a.setPlayerDamageEnabled(enabled, hpMultiplier); err != nil {
			return MonsterEnhanceResult{}, err
		}
		a.monsterDamageEnabled = enabled
		return a.readMonsterEnhanceStatus("")
	}

	if enabled {
		if point != nil && point.ID == "sba_chain_timer" {
			if err := a.setSBAChainTimer(point, hpMultiplier); err != nil {
				return MonsterEnhanceResult{}, err
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
			if point.ID == "monster_hp" || point.ID == "monster_stun" {
				commandValue = 1 / hpMultiplier
			}
			command = fmt.Sprintf("%s %.8g", command, commandValue)
		}
		dllPath, err := extractPatchCoreDLL(command)
		if err != nil {
			return MonsterEnhanceResult{}, err
		}
		if err := injectDLL(a.hProcess, dllPath); err != nil {
			return MonsterEnhanceResult{}, fmt.Errorf("注入怪物增强 DLL 失败: %w", err)
		}
		if applyOnce {
			if _, err := a.waitMonsterEnhanceApplied(pointID, dllPath); err != nil {
				return MonsterEnhanceResult{}, err
			}
			time.Sleep(150 * time.Millisecond)
			if err := a.restoreMonsterEnhance(pointID); err != nil {
				return MonsterEnhanceResult{}, err
			}
			status, err := a.readMonsterEnhanceStatus(dllPath)
			if err != nil {
				return MonsterEnhanceResult{}, err
			}
			status.Injected = true
			return status, nil
		}
		status, err := a.waitMonsterEnhanceApplied(pointID, dllPath)
		if err != nil {
			return MonsterEnhanceResult{}, err
		}
		if pointID == "monster_damage" || pointID == "monster_damage_new" {
			if a.monsterDamageHookInstalled && a.monsterDamageHookID != pointID {
				return MonsterEnhanceResult{}, fmt.Errorf("%s与%s共用玩家伤害 Hook，请先重启游戏后再切换", point.Name, a.monsterDamageHookID)
			}
			if err := a.setPlayerDamageEnabled(true, hpMultiplier); err != nil {
				return MonsterEnhanceResult{}, err
			}
			a.monsterDamageHookInstalled = true
			a.monsterDamageHookID = pointID
			a.monsterDamageEnabled = true
		}
		status.Injected = true
		return status, nil
	}

	if (id == "monster_damage" || id == "monster_damage_new") && !enabled {
		if !a.monsterDamageHookInstalled || a.monsterDamageHookID != id {
			return a.readMonsterEnhanceStatus("")
		}
		if err := a.setPlayerDamageEnabled(false, 0); err != nil {
			return MonsterEnhanceResult{}, err
		}
		a.monsterDamageEnabled = false
		return a.readMonsterEnhanceStatus("")
	}

	if err := a.restoreMonsterEnhance(id); err != nil {
		return MonsterEnhanceResult{}, err
	}
	return a.readMonsterEnhanceStatus("")
}

func (a *App) MonsterEnhanceInject() (MonsterEnhanceResult, error) {
	return a.MonsterEnhanceSetEnabled(true)
}

func (a *App) captureMonsterHookInstall(id string) error {
	if id == "all" {
		return nil
	}
	point := findMonsterPatchPoint(id)
	if point == nil || !point.Hook {
		return nil
	}
	current := make([]byte, len(point.Original))
	addr := a.moduleBase + point.RVA
	if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
		return err
	}
	if current[0] != 0xE9 {
		return fmt.Errorf("%s Hook 未写入目标地址", point.Name)
	}
	return nil
}

func (a *App) waitMonsterEnhanceApplied(id string, dllPath string) (MonsterEnhanceResult, error) {
	var last MonsterEnhanceResult
	var err error
	deadline := time.Now().Add(2 * time.Second)
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
		if point.ID == a.monsterDamageHookID && a.monsterDamageHookInstalled {
			enabled = a.monsterDamageEnabled
		} else if point.ID == "sba_chain_timer" {
			enabled = !bytesEqual(current, point.Original)
		} else if point.Hook {
			enabled = len(current) > 0 && current[0] == 0xE9
		} else {
			enabled = bytesEqual(current, point.Patch)
		}
		if enabled {
			patched++
		}
		items = append(items, MonsterEnhanceItem{
			ID:           point.ID,
			Name:         point.Name,
			RVA:          uint64(point.RVA),
			Enabled:      enabled,
			CurrentBytes: currentHex,
		})
	}
	return MonsterEnhanceResult{
		PID:          a.charaPID,
		DLLPath:      dllPath,
		Enabled:      patched == len(monsterPatchPoints),
		CurrentBytes: strings.Join(parts, " | "),
		Items:        items,
	}, nil
}

func (a *App) restoreMonsterEnhance(id string) error {
	if err := suspendProcess(a.hProcess); err != nil {
		return fmt.Errorf("暂停游戏进程失败: %w", err)
	}
	defer func() { _ = resumeProcess(a.hProcess) }()

	for _, point := range monsterPatchPoints {
		if id != "all" && point.ID != id {
			continue
		}
		current := make([]byte, len(point.Original))
		addr := a.moduleBase + point.RVA
		if err := readProcessMemory(a.hProcess, addr, unsafe.Pointer(&current[0]), uintptr(len(current))); err != nil {
			return fmt.Errorf("读取%s失败: %w", point.Name, err)
		}
		if bytesEqual(current, point.Original) {
			continue
		}
		currentIsPatch := false
		if point.ID == "sba_chain_timer" {
			currentIsPatch = len(current) >= 2 && current[0] == 0x48 && current[1] == 0xB8
		} else if point.Hook {
			currentIsPatch = len(current) > 0 && current[0] == 0xE9
		} else {
			currentIsPatch = bytesEqual(current, point.Patch)
		}
		if !currentIsPatch {
			if id == "all" && isMonsterPatchBytesAtRVA(point.RVA, current) {
				continue
			}
			return fmt.Errorf("%s指令字节未知: %s", point.Name, bytesToHex(current))
		}
		if err := writeCodeMemory(a.hProcess, addr, point.Original); err != nil {
			return fmt.Errorf("恢复%s失败: %w", point.Name, err)
		}
	}
	return nil
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

func isMonsterPatchBytesAtRVA(rva uintptr, data []byte) bool {
	for _, point := range monsterPatchPoints {
		if point.RVA != rva {
			continue
		}
		if point.ID == "sba_chain_timer" && len(data) >= 2 && data[0] == 0x48 && data[1] == 0xB8 {
			return true
		}
		if point.Hook && len(data) > 0 && data[0] == 0xE9 {
			return true
		}
		if !point.Hook && bytesEqual(data, point.Patch) {
			return true
		}
	}
	return false
}

func needsMonsterValue(id string) bool {
	return id == "monster_hp" || id == "monster_stun" || id == "monster_damage" || id == "monster_damage_new" || id == "overdrive_state"
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
	if err := os.WriteFile(filepath.Join(dir, "patch_core_command.txt"), []byte(patchID), 0o644); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("patch_core_%d.dll", time.Now().UnixNano()))
	if err := os.WriteFile(path, patchCoreDLL, 0o644); err != nil {
		return "", err
	}
	return path, nil
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
	defer func() { _ = virtualFreeRemote(h, remotePath) }()

	if err := writeProcessMemory(h, remotePath, unsafe.Pointer(&utf16Path[0]), size); err != nil {
		return err
	}

	thread, err := createRemoteThread(h, procLoadLibraryW.Addr(), remotePath)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)

	_, err = windows.WaitForSingleObject(thread, 10000)
	return err
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
	procNtSuspendProcess          = modNtdll.NewProc("NtSuspendProcess")
	procNtResumeProcess           = modNtdll.NewProc("NtResumeProcess")
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
	return windows.ReadProcessMemory(h, addr, (*byte)(buf), size, &read)
}

func writeProcessMemory(h windows.Handle, addr uintptr, buf unsafe.Pointer, size uintptr) error {
	var written uintptr
	return windows.WriteProcessMemory(h, addr, (*byte)(buf), size, &written)
}

func suspendProcess(h windows.Handle) error {
	status, _, _ := procNtSuspendProcess.Call(uintptr(h))
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func resumeProcess(h windows.Handle) error {
	status, _, _ := procNtResumeProcess.Call(uintptr(h))
	if status != 0 {
		return windows.Errno(status)
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
	var restoreProtect uint32
	_ = windows.VirtualProtectEx(h, addr, uintptr(len(data)), oldProtect, &restoreProtect)
	return writeErr
}

var (
	modKernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procVirtualAllocEx     = modKernel32.NewProc("VirtualAllocEx")
	procVirtualFreeEx      = modKernel32.NewProc("VirtualFreeEx")
	procVirtualQueryEx     = modKernel32.NewProc("VirtualQueryEx")
	procLoadLibraryW       = modKernel32.NewProc("LoadLibraryW")
	procCreateRemoteThread = modKernel32.NewProc("CreateRemoteThread")
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
