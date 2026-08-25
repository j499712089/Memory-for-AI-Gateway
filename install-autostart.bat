@echo off
REM ============================================
REM Install Memory Gateway Auto-Start
REM ============================================
chcp 65001 >nul
title Install Auto-Start

echo.
echo ========================================
echo   Memory Gateway - 开机自启动安装
echo ========================================
echo.

REM Get startup folder path
for /f "tokens=3*" %%a in ('reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders" /v Startup 2^>nul ^| findstr Startup') do set STARTUP=%%b
call set STARTUP=%STARTUP%

REM If not found, use default path
if "%STARTUP%"=="" set STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup

echo [INFO] Startup folder: %STARTUP%
echo.

REM Create VBScript for silent startup
set GATEWAY_DIR=%~dp0
set VBS_FILE=%STARTUP%\MemoryGateway.vbs
echo [INFO] Creating auto-start script...

(
echo ' Memory Gateway Auto-Start
echo ' Launches the gateway service silently on Windows login
echo CreateObject^("WScript.Shell"^).Run "%GATEWAY_DIR%start-gateway.bat", 0, False
) > "%VBS_FILE%"

if exist "%VBS_FILE%" (
    echo [SUCCESS] Auto-start installed successfully!
    echo.
    echo ========================================
    echo   Installation Complete
    echo ----------------------------------------
    echo   Location: %VBS_FILE%
    echo   Gateway will start automatically on next login
    echo ========================================
) else (
    echo [ERROR] Failed to create auto-start script
    echo Please check permissions for: %STARTUP%
    exit /b 1
)

echo.
echo [INFO] To test auto-start without reboot:
echo   1. Log out and log back in
echo   2. Or double-click: %VBS_FILE%
echo.
pause
