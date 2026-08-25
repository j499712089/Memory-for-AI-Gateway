@echo off
REM ============================================
REM Start Memory Gateway Service
REM ============================================
chcp 65001 >nul

REM Change to gateway directory
cd /d "%~dp0"

REM Start gateway.exe in background
start /b "" gateway.exe

REM Exit immediately (don't wait for gateway to finish)
exit
