@echo off
title MiroFish
cd /d "%~dp0"
rem Mata cualquier instancia anterior de mirofish (libera el puerto)
taskkill /F /IM mirofish.exe >nul 2>&1
mirofish.exe
if errorlevel 1 pause
