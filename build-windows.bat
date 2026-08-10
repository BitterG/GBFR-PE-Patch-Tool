@echo off
setlocal

echo [1/4] Building patch_core.dll (auto chat / monster hooks)...
cd /d "%~dp0" || exit /b %errorlevel%
call build_dll.bat
if errorlevel 1 exit /b %errorlevel%

echo [2/4] Generating Wails bindings...
cd /d "%~dp0" || exit /b %errorlevel%
wails generate module
if errorlevel 1 exit /b %errorlevel%

echo [3/4] Building frontend...
cd /d "%~dp0frontend" || exit /b %errorlevel%
if not exist "node_modules\pinyin-pro\package.json" (
	echo Installing frontend dependencies...
	call npm install
	if errorlevel 1 exit /b %errorlevel%
)
where bun >nul 2>nul
if %errorlevel%==0 (
	echo Using bun for frontend build...
	call bun run build
) else (
	call npm run build
)
if errorlevel 1 exit /b %errorlevel%

echo [4/4] Building Wails app...
cd /d "%~dp0" || exit /b %errorlevel%
wails build -s
if errorlevel 1 exit /b %errorlevel%

echo Build complete.
exit /b 0
