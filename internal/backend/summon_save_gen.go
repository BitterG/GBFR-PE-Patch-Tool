package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type SummonSaveInfo struct {
	Path      string              `json:"path"`
	Inventory SummonSaveInventory `json:"inventory"`
}

type SummonSaveWriteRequest struct {
	Operation string            `json:"operation"`
	Expected  *SummonSaveRecord `json:"expected,omitempty"`
	Draft     SummonTraitState  `json:"draft"`
}

type SummonSaveWriteResult struct {
	OutputPath string              `json:"outputPath"`
	BackupPath string              `json:"backupPath,omitempty"`
	Record     SummonSaveRecord    `json:"record"`
	Inventory  SummonSaveInventory `json:"inventory"`
}

type SummonSaveGen struct {
	mu       sync.Mutex
	ctx      context.Context
	savePath string
}

func NewSummonSaveGen() *SummonSaveGen { return &SummonSaveGen{} }

func (sg *SummonSaveGen) startup(ctx context.Context) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	sg.ctx = ctx
}

func (sg *SummonSaveGen) GetOptions() (SummonOptions, error) {
	return (&App{}).SummonGetOptions()
}

func (sg *SummonSaveGen) LoadSaveFile(path string) (*SummonSaveInfo, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("存档路径不能为空")
	}
	save, err := LoadSave(path)
	if err != nil {
		return nil, err
	}
	inventory, err := save.InspectSummonInventory()
	if err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	sg.savePath = absPath
	return &SummonSaveInfo{Path: absPath, Inventory: inventory}, nil
}

func (sg *SummonSaveGen) SelectInputSave() (string, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	return runtime.OpenFileDialog(sg.ctx, runtime.OpenDialogOptions{
		Title:   "选择 GBFR 存档文件",
		Filters: []runtime.FileFilter{{DisplayName: "GBFR 存档 (*.dat)", Pattern: "*.dat"}},
	})
}

func (sg *SummonSaveGen) SelectOutputSave(defaultPath string) (string, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	if sg.ctx == nil {
		return "", fmt.Errorf("Wails 上下文未初始化")
	}
	return runtime.SaveFileDialog(sg.ctx, runtime.SaveDialogOptions{
		Title:            "选择召唤石修改后的存档",
		DefaultDirectory: filepath.Dir(defaultPath),
		DefaultFilename:  filepath.Base(defaultPath),
		Filters:          []runtime.FileFilter{{DisplayName: "GBFR 存档 (*.dat)", Pattern: "*.dat"}},
	})
}

func (sg *SummonSaveGen) Apply(request SummonSaveWriteRequest, outputPath string) (*SummonSaveWriteResult, error) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	offlineSaveMutationMu.Lock()
	defer offlineSaveMutationMu.Unlock()

	if sg.savePath == "" {
		return nil, fmt.Errorf("未加载存档")
	}
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return nil, fmt.Errorf("输出路径不能为空")
	}
	if err := ensureGeneratorWriteAllowed(outputPath); err != nil {
		return nil, err
	}
	if _, err := os.Stat(sg.savePath); err != nil {
		return nil, fmt.Errorf("重新读取源存档前检查失败: %w", err)
	}
	save, err := LoadSave(sg.savePath)
	if err != nil {
		return nil, err
	}
	var written SummonSaveRecord
	switch strings.ToLower(strings.TrimSpace(request.Operation)) {
	case "create":
		written, err = save.CreateSummonRecord(request.Draft)
	case "update":
		if request.Expected == nil {
			return nil, fmt.Errorf("修改已有召唤石需要完整旧记录快照")
		}
		written, err = save.UpdateSummonRecord(*request.Expected, request.Draft)
	default:
		return nil, fmt.Errorf("未知召唤石存档操作 %q", request.Operation)
	}
	if err != nil {
		return nil, err
	}
	if err := save.FixChecksums(); err != nil {
		return nil, err
	}
	if err := save.Write(outputPath); err != nil {
		return nil, err
	}
	verify, err := LoadSave(outputPath)
	if err != nil {
		return nil, fmt.Errorf("召唤石已写入，但重新读取失败: %w", err)
	}
	if err := verify.VerifySummonRecord(written); err != nil {
		return nil, fmt.Errorf("召唤石已写入，但字段回读失败: %w", err)
	}
	inventory, err := verify.InspectSummonInventory()
	if err != nil {
		return nil, fmt.Errorf("召唤石已写入，但背包回读失败: %w", err)
	}
	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return nil, err
	}
	sg.savePath = absOutput
	return &SummonSaveWriteResult{
		OutputPath: absOutput, BackupPath: save.LastBackupPath(), Record: written, Inventory: inventory,
	}, nil
}
