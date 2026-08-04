<p align="center">
  <img src="build/appicon.png" width="128" alt="GBFR Save Editor icon" />
</p>

# GBFR 存档修改工具

[English README](README_EN.md)

适用于 **Granblue Fantasy: Relink（碧蓝幻想：Relink）** 的 Windows 桌面工具，提供存档编辑、因子、召唤石、祝福、配装编辑、运行时修改、怪物增强等功能。

> 工具默认使用英文。首次启动请在「语言」页选择 English 或 简体中文；选择会保存在本机，并在下次启动时恢复。

<img src="https://img.shields.io/github/downloads/BitterG/GBFR-PE-Patch-Tool/total" alt="GitHub downloads" />

## 功能概览

### 存档、因子与祝福

- **因子生成** — 搜索因子，设置因子等级、主/副特性及等级；支持队列批量写入和删除已有因子。
- **因子生成-新** — 读取游戏当前选中的因子，编辑因子、主/副特性和等级；包含 DLC / Endless Ragnarok 相关数据。
- **因子配装恢复** — 从导出或分享数据恢复角色配装。
- **离线配装编辑** — 在不连接游戏的情况下查看和编辑角色装备、因子、专精与祝福配置。（支持从villith/relink-logs导入玩家配装json）
- **祝福生成** — 搜索祝福，配置三个特性及等级，使用队列批量生成。
- **祝福生成-新** — 读取游戏当前选中的武器祝福并编辑其特性与等级。
- **副本次数** — 扫描存档槽位，查看任务/副本通关次数和存档摘要。
- **称号** — 查看与解锁称号相关内容。
- **召唤石** — 编辑召唤石相关数据。
- **原地修改** — 因子和祝福功能可以直接覆盖输入存档；务必先备份。

### 本地化

- 支持 **English** 和 **简体中文** 界面切换。
- 因子、特性、祝福及运行时内存目录名称会随语言切换显示。
- 中文翻译和祝福特性翻译覆盖内置 JSON 数据，并有回归测试防止遗漏。

### 运行时功能

运行时功能需要游戏正在运行；部分功能可能要求以管理员身份启动工具。

- **角色使用次数** — 查看和修改角色使用次数。
- **杂项** — 包含货币、药水等内存编辑，以及部分便利功能。
- **连续挑战** — 无视十次连续挑战限制。
- **飞行模式** — 根据世界轴方向控制。
- **称号解锁** — 编辑存档实现。
- **巴武掉落** — 调整巴哈姆特武器掉落相关判定。
- **全队伤害统计** — 根据怪物真实生命值变化统计伤害，不计入溢出伤害。
- **上限突破** — 扫描、刷新、编辑并保存角色上限突破属性。
- **怪物增强** — 调整怪物生命、伤害、昏厥条、Overdrive状态。
- **检查更新** — 从 GitHub Releases 检查新版本。

## 使用前须知

1. 写入存档或使用原地修改前，先备份存档。
2. 运行时内存修改在多人游戏中可能影响其他玩家；使用前请告知队友。
3. 游戏更新后，内存地址和部分数据功能可能失效；请以 Release 说明和实际测试结果为准。

默认存档路径：

```text
C:\Users\<用户名>\AppData\Local\GBFR\Saved\SaveGames\
```

## 快速使用

### 因子、祝福和其他存档功能

1. 打开相应标签页，例如「因子生成」「因子生成-新」「祝福生成」或「副本次数」。
2. 点击「浏览」选择 `.dat` 存档，或使用自动扫描出的存档槽位。
3. 选择或配置需要编辑的内容。
4. 选择输出路径；默认建议写入新文件。确认无误后再使用「原地修改」。

### 运行时功能

1. 启动游戏并进入存档。
2. 打开「角色次数统计」「杂项」「上限突破」或「怪物增强」。
3. 刷新/连接游戏进程，按界面提示读取、应用或恢复设置。
4. 重启游戏后，大多数运行时设置需要重新连接并重新应用。

### PE 补丁

1. 关闭游戏。
2. 打开补丁页，自动识别或手动选择 `granblue_fantasy_relink.exe`。
3. 点击「备份」创建 `.bak`。
4. 输入数值并点击「应用」，随后启动游戏验证。

### 怪物增强说明

- 「怪物多倍血」和「鳄鱼多倍血」输入 `10` 表示等效 `10 倍生命值`。
- 「OD条变化率」输入 `10` 表示 OD 条增长速度变为 `10 倍`（本次增长 100 变为 1000），输入 `0.1` 变为 `1/10`（100 变为 10），输入 `1` 不缩放；增长与 OD 中扣减同比例缩放。
- 「怪物 Overdrive 状态」支持 `1 满红条`、`4 满黄条` 和「自动OD」。
- 「锁定」会持续写入状态；「应用」仅写入一次后恢复原始指令。
- 「自动OD」会在非红条时写入一次满黄条，红条期间不重复触发。
- 「奥义接续计时」默认 `3 秒`，可设置自定义秒数并恢复默认。
- 部分怪物增强功能依赖内置 `patch_core.dll`。

## 构建

### 环境要求

- Windows amd64
- Go 1.23+
- Node.js 与 npm
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation)
- Microsoft Edge WebView2 Runtime
- Visual Studio / MSBuild（仅在修改 `src_dll/patch_core` 后重新构建 DLL 时需要）

安装 Wails：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### Windows 一键构建

根目录的脚本会生成 Wails 绑定、安装缺失的前端依赖、构建前端并打包应用：

```powershell
.\build-windows.bat
```

构建产物：

```text
build\bin\GBFR PE Patch Tool.exe
```

### 手动构建

```powershell
# 安装前端依赖
cd frontend
npm install
cd ..

# 开发模式
wails dev

# 完整构建
wails build

# 仅构建 Go，跳过前端编译
wails build -s
```

修改 `src_dll/patch_core` 后，请先在 Visual Studio 中构建 **Release x64**，并确认生成的 DLL 覆盖：

```text
build\bin\patch_core.dll
```

## 数据与项目结构

主要数据位于 `data/`，并嵌入最终二进制：

| 路径 | 说明 |
| --- | --- |
| `data/sigils.json` | 因子定义 |
| `data/traits.json` | 因子特性定义 |
| `data/secondary-trait-rules.json` | 副特性兼容性规则 |
| `data/wrightstones.json` | 祝福定义 |
| `data/wrightstone_traits.json` | 祝福特性定义 |
| `data/quest_names_i18n.csv` | 任务 ID 与名称映射 |
| `sigil_locale.go` | 因子/特性本地化与运行时目录名称回退 |
| `wrightstone_locale.go` | 祝福及祝福特性本地化 |

关键目录：

```text
.
├── main.go, app.go                 # Wails 入口、PE 补丁和运行时功能
├── sigil_*.go                      # 因子、内存因子与配装相关逻辑
├── wrightstone_*.go                # 祝福与武器祝福相关逻辑
├── save_*.go                       # 存档扫描、解析和写回
├── overlimit.go                    # 上限突破编辑
├── summon_*.go                     # 召唤石编辑
├── frontend/src/components/        # Vue 界面组件
├── src_dll/patch_core/             # 怪物增强注入 DLL 源码
├── data/                           # 嵌入式 JSON/CSV 数据
└── build-windows.bat               # Windows 构建脚本
└── internal/                       # 一键导入导出(json/logs json)配装相关               
```

## 支持

如果本项目帮你节省了时间或带来更多乐趣，欢迎请我喝杯咖啡。完全自愿，不影响功能优先级或问题处理顺序。

<p align="center">
  <img src="./QRcode.png" width="256" alt="微信支付二维码" />
</p>

## 免责声明与致谢

本工具仅供学习研究使用。使用本工具修改游戏文件、存档或运行时内存所产生的一切后果由使用者自行承担。

- 因子存档解析参考 [GBFR-Sigil-Generator](https://github.com/Xzire91x/GBFR-Sigil-Generator)。
- 祝福添加解析参考 [GBFR-Wrightstone-Generator](https://github.com/Xzire91x/GBFR-Wrightstone-Generator)。
- 存档解析基于 [GBFRDataTools.SaveFile](https://github.com/Nenkai/GBFRDataTools/tree/master/GBFRDataTools.SaveFile)。
- 存档角色配装编 [Whitelinker574/GBFR-PE-Patch-Tool](https://github.com/Whitelinker574/GBFR-PE-Patch-Tool)。
