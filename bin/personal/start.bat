@echo off
set "PORT=5001"
set "EXE=mirofish-personal-sqlite.exe"
set "DATA_DIR=%~dp0data"
title MiroFish Personal SQLite
cd /d "%~dp0"

rem Verifica que el ejecutable exista
if not exist "%EXE%" (
    echo ERROR: No se encontro "%EXE%" en esta carpeta.
    pause
    exit /b 1
)

rem Crea el directorio de datos si no existe
if not exist "%DATA_DIR%" mkdir "%DATA_DIR%"

rem Mata cualquier instancia anterior (libera el puerto)
taskkill /F /IM "%EXE%" >nul 2>&1
timeout /t 1 /nobreak >nul

rem Abre el navegador 2 segundos despues en segundo plano
start /min cmd /c "timeout /t 2 /nobreak >nul & start "" http://127.0.0.1:%PORT%"

rem Ejecuta el servidor en primer plano (esta ventana)
"%EXE%" -addr 127.0.0.1:%PORT% -data "%DATA_DIR%"

echo.
echo El sistema se cerro.
pause