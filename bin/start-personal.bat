@echo off
title MiroFish Personal
cd /d "%~dp0"
rem Mata cualquier instancia anterior del binario personal (libera el puerto)
taskkill /F /IM mirofish-personal.exe >nul 2>&1
mirofish-personal.exe
if errorlevel 1 pause
