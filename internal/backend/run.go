package backend

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
	"golang.org/x/sys/windows"
)

// Run starts the command-line mode or the Wails desktop application.
// The frontend filesystem is embedded by the root command package so the
// backend can remain under internal/backend with its tests and data.
func Run(assets embed.FS) {
	if len(os.Args) > 1 {
		if err := runWrightstoneCLI(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	app := NewApp()
	sigilGen := NewSigilGen()
	wrightstoneGen := NewWrightstoneGen()
	summonSaveGen := NewSummonSaveGen()

	err := wails.Run(&options.App{
		Title:     "GBFR PE Patch Tool",
		Width:     defaultAppWidth,
		Height:    defaultAppHeight,
		MinWidth:  minAppWidth,
		MinHeight: minAppHeight,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 243, G: 234, B: 211, A: 1},
		Windows: &wailswindows.Options{
			Theme: wailswindows.Light,
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			sigilGen.startup(ctx)
			wrightstoneGen.startup(ctx)
			summonSaveGen.startup(ctx)
		},
		OnBeforeClose: app.beforeClose,
		OnShutdown:    app.shutdown,
		Bind: []interface{}{
			app,
			sigilGen,
			wrightstoneGen,
			summonSaveGen,
		},
	})

	if err != nil {
		reportStartupError(err)
	}
}

// reportStartupError makes GUI-startup failures visible. Release builds do not
// have a console window, so printing the error alone looked like a no-op to the
// user (issue #19). Keep a small local log as well so WebView2/graphics/runtime
// failures can be diagnosed without guessing.
func reportStartupError(runErr error) {
	logPath := appendDiagnosticError("startup", runErr)

	text, _ := windows.UTF16PtrFromString(fmt.Sprintf(
		"GBFR PE Patch Tool 启动失败：\n\n%v\n\n诊断日志：\n%s\n\n请检查 Microsoft Edge WebView2 Runtime 和安全软件拦截记录。",
		runErr, logPath,
	))
	caption, _ := windows.UTF16PtrFromString("GBFR PE Patch Tool")
	_, _ = windows.MessageBox(0, text, caption, 0x00000010) // MB_ICONERROR
}

func appendDiagnosticError(scope string, reportErr error) string {
	logDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "GBFR-PE-Patch-Tool")
	if logDir == "GBFR-PE-Patch-Tool" {
		logDir = filepath.Join(os.TempDir(), "GBFR-PE-Patch-Tool")
	}
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "startup.log")
	label := appVersion
	if scope != "" {
		label += " " + scope
	}
	message := fmt.Sprintf("[%s] %s: %v\n", time.Now().Format(time.RFC3339), label, reportErr)
	if file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_, _ = file.WriteString(message)
		_ = file.Close()
	}
	return logPath
}

func runWrightstoneCLI(args []string) error {
	values, err := parseWrightstoneCLIArgs(args)
	if err != nil {
		return err
	}

	quantity := 1
	if raw := values["quantity"]; raw != "" {
		quantity, err = strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("--quantity 必须是数字: %w", err)
		}
	}

	firstLevel, err := requiredIntArg(values, "first-level")
	if err != nil {
		return err
	}
	secondLevel, err := requiredIntArg(values, "second-level")
	if err != nil {
		return err
	}
	thirdLevel, err := requiredIntArg(values, "third-level")
	if err != nil {
		return err
	}

	wg := NewWrightstoneGen()
	if _, err := wg.LoadSaveFile(values["input"]); err != nil {
		return err
	}
	result, err := wg.ApplyItems([]WrightstoneQueueItem{{
		WrightstoneID: values["wrightstone"],
		FirstTraitID:  values["first-trait"],
		FirstLevel:    firstLevel,
		SecondTraitID: values["second-trait"],
		SecondLevel:   secondLevel,
		ThirdTraitID:  values["third-trait"],
		ThirdLevel:    thirdLevel,
		Quantity:      quantity,
	}}, values["output"])
	if err != nil {
		return err
	}

	fmt.Printf("Created %d Wrightstone(s).\n", result.CreatedCount)
	fmt.Printf("Output written: %s\n", result.OutputPath)
	fmt.Printf("Verified %d Wrightstone(s).\n", result.VerifiedCount)
	return nil
}

func parseWrightstoneCLIArgs(args []string) (map[string]string, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) < 3 || arg[:2] != "--" {
			return nil, fmt.Errorf("未知参数: %s", arg)
		}
		key := arg[2:]
		if i+1 >= len(args) || len(args[i+1]) >= 2 && args[i+1][:2] == "--" {
			return nil, fmt.Errorf("参数 --%s 缺少值", key)
		}
		values[key] = args[i+1]
		i++
	}

	for _, key := range []string{
		"input", "output", "wrightstone",
		"first-trait", "first-level",
		"second-trait", "second-level",
		"third-trait", "third-level",
	} {
		if values[key] == "" {
			return nil, fmt.Errorf("缺少参数 --%s", key)
		}
	}
	return values, nil
}

func requiredIntArg(values map[string]string, key string) (int, error) {
	value, err := strconv.Atoi(values[key])
	if err != nil {
		return 0, fmt.Errorf("--%s 必须是数字: %w", key, err)
	}
	return value, nil
}
