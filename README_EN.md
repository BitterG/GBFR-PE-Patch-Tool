<p align="center">
  <img src="build/appicon.png" width="128" alt="GBFR Save Editor icon" />
</p>

# GBFR Save Editor

[简体中文](README.md)

A Windows desktop tool for **Granblue Fantasy: Relink**, providing save editing, sigil, summon, and wrightstone tools, loadout editing, runtime modifications, and monster enhancements.

> The application uses English by default. On first launch, choose English or Simplified Chinese from the **Language** tab. Your choice is stored locally and restored on the next launch.

<img src="https://img.shields.io/github/downloads/BitterG/GBFR-PE-Patch-Tool/total" alt="GitHub downloads" />

The English README has been translated from Chinese by AI, and its complete accuracy is not guaranteed.
## Feature Overview

### Saves, Sigils, and Wrightstones

- **Sigil Generator** — Search sigils; set sigil levels, primary/secondary traits, and trait levels. Supports queued batch writes and removal of existing sigils.
- **Sigil Generator (New)** — Reads the currently selected in-game sigil and edits its sigil, primary/secondary traits, and levels. Includes DLC / Endless Ragnarok data.
- **Sigil Loadout Restore** — Restores character loadouts from exported or shared data.
- **Offline Loadout Editor** — Views and edits character equipment, sigils, masteries, and wrightstone setups without connecting to the game. Supports importing player-loadout JSON from villith/relink-logs.
- **Wrightstone Generator** — Search wrightstones; configure three traits and their levels; batch-generate with a queue.
- **Wrightstone Generator (New)** — Reads the currently selected weapon wrightstone in-game and edits its traits and levels.
- **Quest Clear Statistics** — Scans save slots and displays quest clear counts and save summaries.
- **Badges** — View and unlock badge-related content.
- **Summons** — Edit summon-related save data.
- **In-place Editing** — Sigil and wrightstone tools can overwrite the input save directly. Always create a backup first.

### Localization

- Switch the interface between **English** and **Simplified Chinese**.
- Sigil, trait, wrightstone, and runtime memory-catalog names follow the selected language.
- Chinese and wrightstone-trait translations cover the embedded JSON data and are protected by regression tests.

### Runtime Tools

Runtime features require the game to be running; some may require launching the tool as administrator.

- **Character Usage Counts** — View and modify character usage counts.
- **Miscellaneous Tools** — Memory editors for currencies, potions, and selected convenience features.
- **Infinite Challenges** — Ignore the ten consecutive-quest limit.
- **Flight Mode** — Movement control based on world-axis direction.
- **Badge Unlocking** — Unlock badges by editing the save.
- **Terminus Weapon Drops** — Adjust checks related to Bahamut weapon drops.
- **Team Damage Meter** — Tracks damage from actual monster HP changes and excludes overkill damage.
- **Over Mastery** — Scan, refresh, edit, and save character Over Mastery values.
- **Monster Enhancements** — Adjust monster HP, damage, stun gauge, and Overdrive state.
- **Update Check** — Check GitHub Releases for newer versions.

## Before You Begin

1. Back up your save before writing changes or using in-place editing.
2. Runtime memory modifications can affect other players in multiplayer. Tell teammates before using them.
3. Game updates can invalidate memory addresses and some data-driven features. Refer to release notes and verify behavior after an update.

Default save location:

```text
C:\Users\YOUR_NAME\AppData\Local\GBFR\Saved\SaveGames\
```

## Quick Start

### Sigils, Wrightstones, and Other Save Features

1. Open the relevant tab, such as **Sigil Generator**, **Sigil Generator (New)**, **Wrightstone Generator**, or **Quest Clear Statistics**.
2. Select a `.dat` save with **Browse**, or use an automatically discovered save slot.
3. Select or configure the content you want to edit.
4. Choose an output path. Writing a new file is recommended; only use in-place editing after confirming the configuration.

### Runtime Features

1. Start the game and load into a save.
2. Open **Character Usage Counts**, **Miscellaneous Tools**, **Over Mastery**, or **Monster Enhancements**.
3. Refresh/connect to the game process, then read, apply, or restore settings as prompted by the interface.
4. After restarting the game, most runtime settings must be reconnected and reapplied.

### PE Patches

1. Close the game.
2. Open the patch page and automatically detect or manually select `granblue_fantasy_relink.exe`.
3. Click **Backup** to create a `.bak` file.
4. Enter a value, click **Apply**, then start the game to verify the result.

### Monster Enhancement Notes

- Entering `10` for **Monster HP Multiplier** or **Crocodile HP Multiplier** produces the equivalent of `10× HP`.
- **Monster Overdrive State** supports `1` for a full red gauge, `4` for a full yellow gauge, and **Auto OD**.
- **Lock** continuously writes the selected state; **Apply** writes it once, then restores the original instruction.
- **Auto OD** writes a full yellow gauge once when the monster is not in the red state, and does not repeat while it is red.
- **SBA Chain Timer** defaults to `3 seconds`; you can set a custom duration or restore the default.
- Some monster enhancements depend on the bundled `patch_core.dll`.

## Building

### Requirements

- Windows amd64
- Go 1.23+
- Node.js and npm
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation)
- Microsoft Edge WebView2 Runtime
- Visual Studio / MSBuild (only needed when rebuilding the DLL after changing `src_dll/patch_core`)

Install Wails:

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### One-Click Windows Build

The root script generates Wails bindings, installs missing frontend dependencies, builds the frontend, and packages the application:

```powershell
.\build-windows.bat
```

Build output:

```text
build\bin\GBFR PE Patch Tool.exe
```

### Manual Build

```powershell
# Install frontend dependencies
cd frontend
npm install
cd ..

# Development mode
wails dev

# Full build
wails build

# Build Go only; skip frontend compilation
wails build -s
```

After changing `src_dll/patch_core`, build **Release x64** in Visual Studio first and ensure the generated DLL replaces:

```text
build\bin\patch_core.dll
```

## Data and Project Structure

The main data files are in `data/` and embedded in the final binary:

| Path | Description |
| --- | --- |
| `data/sigils.json` | Sigil definitions |
| `data/traits.json` | Sigil trait definitions |
| `data/secondary-trait-rules.json` | Secondary-trait compatibility rules |
| `data/wrightstones.json` | Wrightstone definitions |
| `data/wrightstone_traits.json` | Wrightstone trait definitions |
| `data/quest_names_i18n.csv` | Quest ID-to-name mappings |
| `sigil_locale.go` | Sigil/trait localization and runtime catalog-name fallback |
| `wrightstone_locale.go` | Wrightstone and wrightstone-trait localization |

Key paths:

```text
.
├── main.go, app.go                 # Wails entry point, PE patches, and runtime features
├── sigil_*.go                      # Sigils, in-memory sigils, and loadout logic
├── wrightstone_*.go                # Wrightstones and weapon-wrightstone logic
├── save_*.go                       # Save scanning, parsing, and writes
├── overlimit.go                    # Over Mastery editor
├── summon_*.go                     # Summon editor
├── frontend/src/components/        # Vue UI components
├── src_dll/patch_core/             # Monster-enhancement injection DLL source
├── data/                           # Embedded JSON/CSV data
└── build-windows.bat               # Windows build script
```

## Disclaimer and Credits

This tool is provided for learning and research purposes only. You are solely responsible for any consequences of modifying game files, saves, or runtime memory.

- Sigil save parsing references [GBFR-Sigil-Generator](https://github.com/Xzire91x/GBFR-Sigil-Generator).
- Wrightstone addition parsing references [GBFR-Wrightstone-Generator](https://github.com/Xzire91x/GBFR-Wrightstone-Generator).
- Save parsing is based on [GBFRDataTools.SaveFile](https://github.com/Nenkai/GBFRDataTools/tree/master/GBFRDataTools.SaveFile).
- Character-loadout editing references [Whitelinker574/GBFR-PE-Patch-Tool](https://github.com/Whitelinker574/GBFR-PE-Patch-Tool).
