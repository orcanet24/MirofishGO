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
    echo [ERROR] No existe .env en la raiz del proyecto, junto a este .bat:
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

if exist "%ROOT%\frontend\package.json" (
    where npm >nul 2>&1
    if errorlevel 1 (
        echo [ERROR] npm no esta en el PATH.
        echo         Instala Node.js LTS desde https://nodejs.org
        pause
        exit /b 1
    )
)

if exist "%ROOT%\frontend\package.json" goto frontend_build

echo [AVISO] No existe carpeta frontend\ en el proyecto.
echo         Se usara el frontend ya compilado en internal\webui\dist
goto embed_frontend

:frontend_build
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

:embed_frontend
echo [2/5] Incrustando frontend en el paquete Go...
if exist "internal\webui\dist\index.html" goto check_embed
if exist "frontend\dist\index.html" (
    if exist "internal\webui\dist" rmdir /s /q "internal\webui\dist"
    xcopy "frontend\dist" "internal\webui\dist\" /E /I /Y /Q >nul
)
:check_embed
if not exist "internal\webui\dist\index.html" (
    echo [ERROR] Ni frontend\dist ni internal\webui\dist contienen index.html.
    echo         No hay frontend disponible para incrustar en el binario.
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

echo [3b/5] Compilando binario para Linux (amd64)...
pushd "%ROOT%"
set "CGO_ENABLED=0"
set "GOOS=linux"
set "GOARCH=amd64"
go build -trimpath -ldflags "-s -w" -o "bin\mirofish-linux" .\cmd\mirofish
set "BUILD_LIN=%ERRORLEVEL%"
set "GOOS="
set "GOARCH="
popd
if not "%BUILD_LIN%"=="0" (
    echo.
    echo [ERROR] go build ^(Linux^) fallo. Revisa los mensajes anteriores.
    pause
    exit /b 1
)
if not exist "bin\mirofish-linux" (
    echo [ERROR] go build no produjo bin\mirofish-linux
    pause
    exit /b 1
)

echo [3c/5] Compilando binario personal (Windows + Linux)...
pushd "%ROOT%"
set "CGO_ENABLED=0"
go build -trimpath -ldflags "-s -w" -o "bin\mirofish-personal.exe" .\cmd\mirofish-personal
set "BUILD_PERS=%ERRORLEVEL%"
set "GOOS=linux"
set "GOARCH=amd64"
go build -trimpath -ldflags "-s -w" -o "bin\mirofish-personal-linux" .\cmd\mirofish-personal
set "BUILD_PERS_LIN=%ERRORLEVEL%"
set "GOOS="
set "GOARCH="
popd
if not "%BUILD_PERS%"=="0" (
    echo.
    echo [ERROR] go build ^(personal Windows^) fallo.
    pause
    exit /b 1
)
if not "%BUILD_PERS_LIN%"=="0" (
    echo.
    echo [ERROR] go build ^(personal Linux^) fallo.
    pause
    exit /b 1
)

echo [3d/5] Compilando binario personal SQLite (Windows + Linux, sin MySQL)...
pushd "%ROOT%"
set "CGO_ENABLED=0"
go build -trimpath -tags mirofish_sqlite -ldflags "-s -w" -o "bin\personal\mirofish-personal-sqlite.exe" .\bin\personal
set "BUILD_SQL=%ERRORLEVEL%"
set "GOOS=linux"
set "GOARCH=amd64"
go build -trimpath -tags mirofish_sqlite -ldflags "-s -w" -o "bin\personal\mirofish-personal-sqlite-linux" .\bin\personal
set "BuildSqlLin=%ERRORLEVEL%"
set "GOOS="
set "GOARCH="
popd
if not "%BUILD_SQL%"=="0" (
    echo.
    echo [ERROR] go build ^(personal SQLite Windows^) fallo.
    pause
    exit /b 1
)
if not "%BuildSqlLin%"=="0" (
    echo.
    echo [ERROR] go build ^(personal SQLite Linux^) fallo.
    pause
    exit /b 1
)
if not exist "bin\personal\.env" (
    echo [AVISO] bin\personal\.env no existe; se copia el .env de la raiz para la primera ejecucion.
    copy /Y "%ROOT%\.env" "bin\personal\.env" >nul
)
if not exist "bin\personal\backend\uploads" mkdir "bin\personal\backend\uploads"
if not exist "bin\personal\backend\scripts" mkdir "bin\personal\backend\scripts"
if not exist "bin\personal\logs" mkdir "bin\personal\logs"

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
(
    echo @echo off
    echo title MiroFish
    echo cd /d "%%~dp0"
    echo rem Mata cualquier instancia anterior de mirofish ^(libera el puerto^)
    echo taskkill /F /IM mirofish.exe ^>nul 2^>^&1
    echo mirofish.exe
    echo if errorlevel 1 pause
) > "bin\start.bat"
(
    echo @echo off
    echo title MiroFish Personal
    echo cd /d "%%~dp0"
    echo rem Mata cualquier instancia anterior del binario personal ^(libera el puerto^)
    echo taskkill /F /IM mirofish-personal.exe ^>nul 2^>^&1
    echo mirofish-personal.exe
    echo if errorlevel 1 pause
) > "bin\start-personal.bat"

echo.
echo ============================================
echo   Listo. Contenido en bin\
echo ============================================
echo   bin\mirofish.exe   binario con frontend embebido (Windows)
echo   bin\mirofish-linux binario con frontend embebido (Linux amd64)
echo   bin\mirofish-personal.exe   personal: acceso por ACCESS_KEY (Windows)
echo   bin\mirofish-personal-linux personal: acceso por ACCESS_KEY (Linux amd64)
echo   bin\personal\mirofish-personal-sqlite.exe       personal SQLite SIN MySQL (Windows)
echo   bin\personal\mirofish-personal-sqlite-linux     personal SQLite SIN MySQL (Linux amd64)
echo   .\.env             para Linux, copia tambien .env y carpetas de datos
echo   bin\.env           configuracion (MySQL, LLM, ACCESS_KEY)
echo   bin\start.bat      arranca el binario desde bin\
echo   bin\backend\...    scripts y datos (uploads)
echo   bin\logs\          logs del servidor
echo.
echo Arranca con: "%CD%\start.bat"  (Windows)  o  bin/mirofish-linux  (Linux)
echo   Personal: "%CD%\start-personal.bat"  o  bin/mirofish-personal.exe
echo   Personal SQLite (sin MySQL): "%CD%\personal\start.bat"
echo   En Linux, primero: chmod +x bin/mirofish-linux
echo UI y API en el mismo puerto (por defecto http://localhost:5001).
echo Los binarios mirofish/mirofish-personal requieren MySQL 8/MariaDB segun .env.
echo El binario personal sqlite guarda todo en bin\personal\mirofish.db (sin MySQL).
echo.
pause
exit /b 0