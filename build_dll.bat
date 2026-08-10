@echo off
setlocal
rem Builds src_dll/patch_core (Release x64) into build/bin/patch_core.dll.
rem Called automatically via //go:generate before `go build` / `wails build`
rem so the embedded DLL is always fresh.

set MSBUILD=D:\Microsoft Visual Studio\2026\MSBuild\Current\Bin\MSBuild.exe
if not exist "%MSBUILD%" (
    echo [build_dll] MSBuild not found at "%MSBUILD%" 1>&2
    exit /b 1
)

cd /d "%~dp0" || exit /b %errorlevel%
echo [build_dll] Building patch_core.dll (Release x64)...
"%MSBUILD%" "src_dll\patch_core\patch_core.vcxproj" /p:Configuration=Release /p:Platform=x64 /m /v:minimal
if errorlevel 1 (
    echo [build_dll] DLL build FAILED 1>&2
    exit /b %errorlevel%
)
if not exist "build\bin\patch_core.dll" (
    echo [build_dll] Output DLL missing after build 1>&2
    exit /b 1
)
echo [build_dll] OK: build\bin\patch_core.dll
exit /b 0
