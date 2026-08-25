@echo off
REM ============================================
REM Uninstall Memory Gateway Auto-Start
REM ============================================
chcp 65001 >nul
title Uninstall Auto-Start

echo.
echo ========================================
echo   Memory Gateway - 卸载开机自启动
echo ========================================
echo.

REM Get startup folder path
for /f "tokens=3*" %%a in ('reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders" /v Startup 2^>nul ^| findstr Startup') do set STARTUP=%%b
call set STARTUP=%STARTUP%

REM If not found, use default path
if "%STARTUP%"=="" set STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup

echo [INFO] Startup folder: %STARTUP%
echo.

REM Remove VBScript file
set VBS_FILE=%STARTUP%\MemoryGateway.vbs

if exist "%VBS_FILE%" (
    del "%VBS_FILE%"
    if not exist "%VBS_FILE%" (
        echo [SUCCESS] Auto-start removed successfully!
        echo.
        echo ========================================
        echo   Uninstallation Complete
        echo ----------------------------------------
        echo   Gateway will no longer start automatically
        echo ========================================
    ) else (
        echo [ERROR] Failed to remove auto-start script
        echo Please check permissions for: %STARTUP%
        exit /b 1
    )
) else (
    echo [WARNING] Auto-start script not found
    echo Location checked: %VBS_FILE%
    echo Already uninstalled or never installed
)

echo.
pause
