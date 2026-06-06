<#
.SYNOPSIS
  Start all WeSync development services, each in its own terminal window.

.PARAMETER NoBuild
  Skip the Go build step (useful if the binary is already up to date).

.PARAMETER Frontend
  Also start the Vite dev server on :5173 with hot-reload.

.EXAMPLE
  .\dev.ps1                  # build + start 3-device test setup (ports 8083/8084/8085)
  .\dev.ps1 -NoBuild         # skip build, just (re)start services
  .\dev.ps1 -Frontend        # also start Vite dev server on :5173

NOTE: All three Syncthing instances run from testdata\ — fully isolated from your
      personal Syncthing. Run .\clean.ps1 to wipe state and start completely fresh.
#>
param(
  [switch]$NoBuild,
  [switch]$Frontend
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$root      = $PSScriptRoot
$stExe     = "$root\testdata\syncthing.exe"
$st1Home   = "$root\testdata\syncthing1-home"
$st2Home   = "$root\testdata\syncthing2-home"
$st3Home   = "$root\testdata\syncthing3-home"
$wesyncExe = "$root\wesync.exe"

# ── Ensure ST home dirs exist ──────────────────────────────────────────────────
foreach ($dir in @($st1Home, $st2Home, $st3Home)) {
  if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir | Out-Null }
}

# ── Build ──────────────────────────────────────────────────────────────────────
if (-not $NoBuild) {
  Write-Host "Building frontend…" -ForegroundColor Cyan
  Push-Location "$root\web"
  npm run build
  $ok = $LASTEXITCODE
  Pop-Location
  if ($ok -ne 0) { Write-Error "Frontend build failed"; exit 1 }
  Write-Host "Frontend OK" -ForegroundColor Green

  Write-Host "Building Go binary…" -ForegroundColor Cyan
  $buildTime = (Get-Date -Format "yyyyMMdd-HHmmss")
  Push-Location $root
  go build -ldflags "-X wesync/internal/api.BuildTime=$buildTime" -o $wesyncExe .
  $ok = $LASTEXITCODE
  Pop-Location
  if ($ok -ne 0) { Write-Error "Go build failed"; exit 1 }
  Write-Host "Build OK" -ForegroundColor Green
}

# ── Kill all existing Syncthing and WeSync processes ──────────────────────────
Write-Host "Stopping existing processes…" -ForegroundColor DarkGray
Get-Process syncthing, wesync -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
foreach ($port in @(8083, 8084, 8085, 8386, 8387, 8388, 47831, 47832, 47833)) {
  Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue |
    Select-Object -ExpandProperty OwningProcess |
    ForEach-Object { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }
}
Start-Sleep 2
Write-Host "Processes stopped" -ForegroundColor DarkGray

# ── Helper ────────────────────────────────────────────────────────────────────
function Open-Window([string]$title, [string]$cmd) {
  $shell = if (Get-Command pwsh -ErrorAction SilentlyContinue) { "pwsh" } else { "powershell" }
  $wrapped = "`$Host.UI.RawUI.WindowTitle = '$title'; $cmd"
  Start-Process $shell -ArgumentList @("-NoExit", "-NoProfile", "-Command", $wrapped)
}

# ── Start Syncthing 1 (:8386) ─────────────────────────────────────────────────
Open-Window "Syncthing 1  :8386" "& '$stExe' serve --no-browser --home '$st1Home' --gui-address=127.0.0.1:8386"

# ── Start Syncthing 2 (:8387) ─────────────────────────────────────────────────
Open-Window "Syncthing 2  :8387" "& '$stExe' serve --no-browser --home '$st2Home' --gui-address=127.0.0.1:8387"

# ── Start Syncthing 3 (:8388) ─────────────────────────────────────────────────
Open-Window "Syncthing 3  :8388" "& '$stExe' serve --no-browser --home '$st3Home' --gui-address=127.0.0.1:8388"

# Give Syncthing a moment to initialise and write config.
Write-Host "Waiting 4 s for Syncthing to initialise…" -ForegroundColor DarkGray
Start-Sleep 4

# ── Read API keys ──────────────────────────────────────────────────────────────
$st1Key = ([xml](Get-Content "$st1Home\config.xml")).configuration.gui.apikey
$st2Key = ([xml](Get-Content "$st2Home\config.xml")).configuration.gui.apikey
$st3Key = ([xml](Get-Content "$st3Home\config.xml")).configuration.gui.apikey

if (-not $st1Key) { Write-Error "Could not read ST1 API key"; exit 1 }
if (-not $st2Key) { Write-Error "Could not read ST2 API key"; exit 1 }
if (-not $st3Key) { Write-Error "Could not read ST3 API key"; exit 1 }

Write-Host "ST1 key: $($st1Key.Substring(0,7))…  ST2 key: $($st2Key.Substring(0,7))…  ST3 key: $($st3Key.Substring(0,7))…" -ForegroundColor DarkGray

# ── Set distinct sync ports ────────────────────────────────────────────────────
Write-Host "Configuring Syncthing sync ports…" -ForegroundColor DarkGray
try {
  $body1 = '{"listenAddresses":["tcp://0.0.0.0:23000","quic://0.0.0.0:23000"]}'
  Invoke-RestMethod -Method Patch -Uri "http://127.0.0.1:8386/rest/config/options" `
    -Headers @{"X-API-Key"=$st1Key} -ContentType "application/json" -Body $body1 | Out-Null
  Write-Host "  ST1 sync port: 23000" -ForegroundColor DarkGray
} catch { Write-Host "  ST1 port config failed: $_" -ForegroundColor Yellow }

try {
  $body2 = '{"listenAddresses":["tcp://0.0.0.0:23001","quic://0.0.0.0:23001"]}'
  Invoke-RestMethod -Method Patch -Uri "http://127.0.0.1:8387/rest/config/options" `
    -Headers @{"X-API-Key"=$st2Key} -ContentType "application/json" -Body $body2 | Out-Null
  Write-Host "  ST2 sync port: 23001" -ForegroundColor DarkGray
} catch { Write-Host "  ST2 port config failed: $_" -ForegroundColor Yellow }

try {
  $body3 = '{"listenAddresses":["tcp://0.0.0.0:23002","quic://0.0.0.0:23002"]}'
  Invoke-RestMethod -Method Patch -Uri "http://127.0.0.1:8388/rest/config/options" `
    -Headers @{"X-API-Key"=$st3Key} -ContentType "application/json" -Body $body3 | Out-Null
  Write-Host "  ST3 sync port: 23002" -ForegroundColor DarkGray
} catch { Write-Host "  ST3 port config failed: $_" -ForegroundColor Yellow }

Start-Sleep 1

# ── Start WeSync 1 (:8083, peer :47831) ───────────────────────────────────────
Open-Window "WeSync 1  :8083" "& '$wesyncExe' --syncthing-url=http://127.0.0.1:8386 --syncthing-key=$st1Key --syncthing-home='$st1Home' --port=8083 --peer-port=47831 --db='$root\testdata\wesync1.db' --debug"

# ── Start WeSync 2 (:8084, peer :47832) ───────────────────────────────────────
Open-Window "WeSync 2  :8084" "& '$wesyncExe' --syncthing-url=http://127.0.0.1:8387 --syncthing-key=$st2Key --syncthing-home='$st2Home' --port=8084 --peer-port=47832 --db='$root\testdata\wesync2.db' --debug"

# ── Start WeSync 3 (:8085, peer :47833) ───────────────────────────────────────
Open-Window "WeSync 3  :8085" "& '$wesyncExe' --syncthing-url=http://127.0.0.1:8388 --syncthing-key=$st3Key --syncthing-home='$st3Home' --port=8085 --peer-port=47833 --db='$root\testdata\wesync3.db' --debug"

# ── Frontend dev server (optional) ────────────────────────────────────────────
if ($Frontend) {
  Open-Window "Frontend  :5173  (Vite)" "Set-Location '$root\web'; npm run dev"
}

# ── Summary ───────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "Services started:" -ForegroundColor Green
Write-Host "  WeSync 1   http://localhost:8083" -ForegroundColor Cyan
Write-Host "  WeSync 2   http://localhost:8084" -ForegroundColor Cyan
Write-Host "  WeSync 3   http://localhost:8085" -ForegroundColor Cyan
if ($Frontend) {
  Write-Host "  Frontend   http://localhost:5173  (Vite)" -ForegroundColor Cyan
}
Write-Host ""
Write-Host "To stop everything:  Get-Process wesync,syncthing | Stop-Process" -ForegroundColor DarkGray
