@echo off
setlocal EnableExtensions
chcp 65001 >nul
title MiroFish - Go binario autonomo

cd /d "%~dp0"
set "ROOT=%CD%"

echo ============================================
echo   MiroFish - binario Go autonomo
echo ============================================
echo.

if not exist "%ROOT%\.env" (
    echo [ERROR] No existe .env en la raiz del proyecto (junto a este .bat):
    echo         %ROOT%\.env
    echo.
    echo Copia .env.example a .env y configura LLM y MySQL.
    pause
    exit /b 1
)

if not exist "%ROOT%\go.mod" (
    echo [ERROR] No se encuentra go.mod en %ROOT%
    pause
    exit /b 1
)

where go >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Go no esta en el PATH.
    echo         Instala Go 1.22+ desde https://go.dev/dl/
    pause
    exit /b 1
)

where npm >nul 2>&1
if errorlevel 1 (
    echo [ERROR] npm no esta en el PATH.
    echo         Instala Node.js LTS desde https://nodejs.org
    pause
    exit /b 1
)

if not exist "%ROOT%\frontend\node_modules" (
    echo [0/5] Instalando dependencias del frontend...
    pushd "%ROOT%\frontend"
    call npm install
    popd
    if errorlevel 1 (
        echo [ERROR] npm install fallo.
        pause
        exit /b 1
    )
)

echo [1/5] Compilando frontend Vite...
pushd "%ROOT%\frontend"
call npm run build
popd
if errorlevel 1 (
    echo [ERROR] npm run build fallo.
    pause
    exit /b 1
)
if not exist "frontend\dist\index.html" (
    echo [ERROR] frontend\dist no contiene index.html
    pause
    exit /b 1
)

echo [2/5] Incrustando frontend en el paquete Go...
if exist "internal\webui\dist" rmdir /s /q "internal\webui\dist"
xcopy "frontend\dist" "internal\webui\dist\" /E /I /Y /Q >nul
if not exist "internal\webui\dist\index.html" (
    echo [ERROR] El build embebido quedo incompleto.
    pause
    exit /b 1
)

echo [3/5] Compilando backend Go...
if not exist "bin" mkdir "bin"
pushd "%ROOT%"
set "CGO_ENABLED=0"
go build -trimpath -ldflags "-s -w" -o "bin\mirofish.exe" .\cmd\mirofish
set "BUILD_RC=%ERRORLEVEL%"
popd
if not "%BUILD_RC%"=="0" (
    echo.
    echo [ERROR] go build fallo. Revisa los mensajes anteriores.
    pause
    exit /b 1
)
if not exist "bin\mirofish.exe" (
    echo [ERROR] go build no produjo bin\mirofish.exe
    pause
    exit /b 1
)

echo [4/5] Copiando .env y scripts...
copy /Y "%ROOT%\.env" "bin\.env" >nul
if errorlevel 1 (
    echo [ERROR] No se pudo copiar .env a bin\
    pause
    exit /b 1
)

if not exist "bin\backend\scripts" mkdir "bin\backend\scripts"
if not exist "bin\backend\uploads" mkdir "bin\backend\uploads"
if not exist "bin\logs" mkdir "bin\logs"

for %%F in (action_logger.py run_twitter_simulation.py run_reddit_simulation.py run_parallel_simulation.py) do (
    if exist "%ROOT%\backend\scripts\%%F" (
        copy /Y "%ROOT%\backend\scripts\%%F" "bin\backend\scripts\%%F" >nul
    ) else (
        echo [AVISO] Falta backend\scripts\%%F; no se copiara.
    )
)

echo [5/5] Creando lanzador...
>(bin\start.bat) (
    echo @echo off
    echo title MiroFish
    echo cd /d "%%~dp0"
    echo rem Mata cualquier instancia anterior de mirofish (libera el puerto^)
    echo taskkill /F /IM mirofish.exe ^>nul 2^>^&1
    echo mirofish.exe
    echo if errorlevel 1 pause
)

echo.
echo ============================================
echo   Listo. Contenido en bin\
echo ============================================
echo   bin\mirofish.exe   binario con frontend embebido
echo   bin\.env           configuracion (MySQL, LLM, ACCESS_KEY)
echo   bin\start.bat      arranca el binario desde bin\
echo   bin\backend\...    scripts y datos (uploads)
echo   bin\logs\          logs del servidor
echo.
echo Arranca con: "%CD%\start.bat"
echo UI y API en el mismo puerto (por defecto http://localhost:5001).
echo Requiere MySQL 8/MariaDB accesible con lo configurado en .env.
echo.
pause
exit /b 0