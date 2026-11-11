@echo off
rem Compila la variante personal SQLite (Windows, sin MySQL).
rem Debe ejecutarse desde la raiz del repo (C:\Proyectos\GO\MirofishGO\).
setlocal
set "ROOT=%~dp0..\.."
cd /d "%ROOT%"
go build -tags mirofish_sqlite -ldflags "-s -w" -o bin\personal\mirofish-personal-sqlite.exe ./bin/personal
if errorlevel 1 (
  echo.
  echo [ERROR] Fallo el build. Si el enlazador se queda sin memoria, cierra
  echo navegadores/aplicaciones pesadas y vuelve a ejecutar este script.
  pause
  exit /b 1
)
echo.
echo OK: bin\personal\mirofish-personal-sqlite.exe
pause
