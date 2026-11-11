# build_personal_sqlite.ps1 — espera memoria libre y compila el binario SQLite personal.
for ($i = 0; $i -lt 120; $i++) {
  $os = Get-CimInstance Win32_OperatingSystem
  $freeGB = [math]::Round($os.FreeVirtualMemory / 1MB, 2)
  if ($freeGB -gt 2.5) {
    Write-Output ("Memoria libre: " + $freeGB + " GB - iniciando build...")
    $env:GOMEMLIMIT = '1536MiB'
    $env:GOMAXPROCS = '4'
    go build -tags mirofish_sqlite -ldflags "-s -w" -o bin\personal\mirofish-personal-sqlite.exe ./bin/personal 2>&1
    if (Test-Path bin\personal\mirofish-personal-sqlite.exe) {
      Write-Output ("BUILD OK: " + [math]::Round((Get-Item bin\personal\mirofish-personal-sqlite.exe).Length / 1MB, 1) + " MB")
    } else {
      Write-Output "BUILD FALLO"
    }
    exit 0
  }
  Start-Sleep -Seconds 10
}
Write-Output "TIMEOUT esperando memoria libre"
