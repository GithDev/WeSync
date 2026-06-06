<#
.SYNOPSIS
  Measure the idle battery cost of WeSync + the bundled Syncthing on a real
  Android device, using `dumpsys batterystats`. Read-only: this script never
  touches app code or config -- it only drives adb and reads counters.

.DESCRIPTION
  Answers the question "is an always-on Syncthing actually a battery problem
  on this phone?" by isolating ST's idle draw. You run TWO timed windows on
  the SAME device, screen off, simulated-on-battery:

    1. baseline  -- WeSync force-stopped. Pure OS idle drain.
    2. st-idle   -- WeSync running, ST alive, nothing to sync.

  The delta between the two per-uid power figures is what an always-on ST
  costs per hour at idle. No gate changes needed -- this measures the thing
  the gate is meant to avoid.

  WHY A PHYSICAL DEVICE: emulators do not model the radio / Doze / power
  rails, so batterystats power figures on an emulator are meaningless for
  this question. Use a real phone with USB debugging on.

  CAVEAT: an active adb/USB link can itself hold the device out of deep
  Doze, which inflates BOTH windows similarly (so the delta stays useful but
  absolute numbers run high). For the cleanest absolute numbers, measure over
  longer windows (>=30 min) and compare deltas, not raw totals.

.PARAMETER Scenario
  Which window to run: "baseline" (force-stop the app first) or "st-idle"
  (launch the app first). Required unless -Diff is used.

.PARAMETER DurationMin
  How long to hold the idle window before collecting stats. Default 30.
  Shorter windows are noisier; 30+ min is recommended.

.PARAMETER OutDir
  Where to write the raw dumps and summary. Default .\power-measurements\

.PARAMETER Diff
  Don't measure -- just compare the two most recent baseline + st-idle
  summaries already in OutDir and print the delta.

.EXAMPLE
  # Run the two windows (do baseline first, then st-idle), then diff:
  .\measure-power.ps1 -Scenario baseline -DurationMin 30
  .\measure-power.ps1 -Scenario st-idle  -DurationMin 30
  .\measure-power.ps1 -Diff

NOTE: Put the phone down, screen will be turned off automatically. Don't touch
      it during a window. Leave it on Wi-Fi (matching how it normally syncs).
#>
param(
  [ValidateSet("baseline", "st-idle")]
  [string]$Scenario,
  [int]$DurationMin = 30,
  [string]$OutDir = "$PSScriptRoot\power-measurements",
  [switch]$Diff
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Package = "com.wesync.app"

# -- Locate adb --------------------------------------------------------------
function Resolve-Adb {
  $onPath = Get-Command adb -ErrorAction SilentlyContinue
  if ($onPath) { return $onPath.Source }
  $candidates = @(
    "$env:LOCALAPPDATA\Android\Sdk\platform-tools\adb.exe",
    "$env:ANDROID_HOME\platform-tools\adb.exe",
    "$env:ANDROID_SDK_ROOT\platform-tools\adb.exe"
  )
  foreach ($c in $candidates) { if ($c -and (Test-Path $c)) { return $c } }
  throw "adb not found. Install platform-tools or add adb to PATH."
}

$adb = Resolve-Adb

function Require-Device {
  $lines = (& $adb devices) -split "`n" | Where-Object { $_ -match "\tdevice$" }
  if (-not $lines) {
    throw "No device connected. Plug in a physical phone with USB debugging enabled (adb devices should list it as 'device')."
  }
  if (@($lines).Count -gt 1) {
    throw "Multiple devices attached. Disconnect all but the one you want to measure."
  }
}

# Map the app's Linux uid (e.g. 10234) to batterystats' uXa form (u0a234).
function Get-AppUidTag {
  $out = & $adb shell dumpsys package $Package
  $m = ($out | Select-String -Pattern "userId=(\d+)" | Select-Object -First 1)
  if (-not $m) { throw "Could not find a uid for $Package. Is it installed?" }
  $uid = [int]$m.Matches[0].Groups[1].Value
  if ($uid -ge 10000) { return "u0a$($uid - 10000)" }
  return "u0a$uid"
}

# -- Diff mode ---------------------------------------------------------------
function Invoke-Diff {
  function Latest($name) {
    Get-ChildItem -Path $OutDir -Filter "$name-*.summary.json" -ErrorAction SilentlyContinue |
      Sort-Object LastWriteTime -Descending | Select-Object -First 1
  }
  $b = Latest "baseline"; $s = Latest "st-idle"
  if (-not $b -or -not $s) {
    throw "Need one 'baseline' and one 'st-idle' summary in $OutDir. Run both scenarios first."
  }
  $bj = Get-Content $b.FullName -Raw | ConvertFrom-Json
  $sj = Get-Content $s.FullName -Raw | ConvertFrom-Json
  Write-Host ""
  Write-Host "== Idle power comparison ==" -ForegroundColor Cyan
  Write-Host ("  baseline ({0} min): {1,8:N2} mAh  ({2})" -f $bj.durationMin, $bj.appMah, $b.Name)
  Write-Host ("  st-idle  ({0} min): {1,8:N2} mAh  ({2})" -f $sj.durationMin, $sj.appMah, $s.Name)
  $deltaB = $sj.appMah - $bj.appMah
  $perHour = if ($sj.durationMin -gt 0) { $deltaB * (60.0 / $sj.durationMin) } else { 0 }
  Write-Host ""
  Write-Host ("  ST idle cost (app uid delta): {0:N2} mAh over the window" -f $deltaB) -ForegroundColor Yellow
  Write-Host ("  Extrapolated:                 {0:N2} mAh / hour" -f $perHour) -ForegroundColor Yellow
  Write-Host ""
  Write-Host "  Wakeups (app):    baseline=$($bj.appWakeups)  st-idle=$($sj.appWakeups)"
  Write-Host "  Mobile radio on:  baseline=$($bj.mobileRadioActive)  st-idle=$($sj.mobileRadioActive)"
  Write-Host "  Wifi running:     baseline=$($bj.wifiRunning)  st-idle=$($sj.wifiRunning)"
  Write-Host ""
  Write-Host "  Raw dumps kept alongside the summaries in $OutDir for Battery Historian." -ForegroundColor DarkGray
}

if ($Diff) { Invoke-Diff; return }

if (-not $Scenario) {
  throw "Specify -Scenario baseline | st-idle  (or -Diff to compare existing runs)."
}

# -- Measurement run ---------------------------------------------------------
Require-Device
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$uidTag = Get-AppUidTag
Write-Host "Device ready. App uid tag: $uidTag" -ForegroundColor Green

$restoreBattery = $false
try {
  # Prepare the scenario.
  if ($Scenario -eq "baseline") {
    Write-Host "Scenario baseline: force-stopping $Package ..." -ForegroundColor Cyan
    & $adb shell am force-stop $Package
  }
  else {
    Write-Host "Scenario st-idle: launching $Package ..." -ForegroundColor Cyan
    & $adb shell monkey -p $Package -c android.intent.category.LAUNCHER 1 | Out-Null
    Start-Sleep -Seconds 8  # let ST boot before we sleep the screen
  }

  # Screen off so the display doesn't dominate the measurement.
  & $adb shell input keyevent 223  # KEYCODE_SLEEP

  # Simulate "on battery" so drain accrues even though USB is attached.
  & $adb shell dumpsys battery unplug | Out-Null
  $restoreBattery = $true

  # Reset counters, then hold the window.
  & $adb shell dumpsys batterystats --reset | Out-Null
  $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
  Write-Host "Measuring '$Scenario' for $DurationMin min -- do not touch the phone." -ForegroundColor Yellow
  $endAt = (Get-Date).AddMinutes($DurationMin)
  while ((Get-Date) -lt $endAt) {
    $left = [int]($endAt - (Get-Date)).TotalSeconds
    Write-Host -NoNewline ("`r  {0,5}s remaining ..." -f $left)
    Start-Sleep -Seconds 15
  }
  Write-Host "`r  done.                       "

  # Collect.
  $rawPath = Join-Path $OutDir "$Scenario-$stamp.batterystats.txt"
  & $adb shell dumpsys batterystats > $rawPath
  $raw = Get-Content $rawPath

  # Per-uid estimated power: lines like "  Uid u0a234: 12.3 (...)" or "u0a234: 12.3".
  $appMah = 0.0
  $pat = "\b" + [regex]::Escape($uidTag) + ":\s*([\d.]+)"
  $pm = $raw | Select-String -Pattern $pat
  if ($pm) { $appMah = [double]$pm.Matches[0].Groups[1].Value }

  $appWakeups      = @($raw | Select-String -Pattern ($uidTag + ".*wakeup")).Count
  $mobileRadioLine = ($raw | Select-String -Pattern "Mobile radio active.*:" | Select-Object -First 1)
  $wifiRunningLine = ($raw | Select-String -Pattern "Wifi.*running.*:" | Select-Object -First 1)

  $summary = [ordered]@{
    scenario          = $Scenario
    timestamp         = $stamp
    durationMin       = $DurationMin
    uidTag            = $uidTag
    appMah            = $appMah
    appWakeups        = $appWakeups
    mobileRadioActive = if ($mobileRadioLine) { $mobileRadioLine.Line.Trim() } else { "n/a" }
    wifiRunning       = if ($wifiRunningLine) { $wifiRunningLine.Line.Trim() } else { "n/a" }
    rawDump           = (Split-Path $rawPath -Leaf)
  }
  $summaryPath = Join-Path $OutDir "$Scenario-$stamp.summary.json"
  ($summary | ConvertTo-Json) | Out-File -FilePath $summaryPath -Encoding utf8

  Write-Host ""
  Write-Host "Summary written: $summaryPath" -ForegroundColor Green
  Write-Host ("  app uid power: {0:N2} mAh over {1} min" -f $appMah, $DurationMin)
  Write-Host "  raw dump:      $rawPath"
  Write-Host ""
  if ($Scenario -eq "baseline") {
    Write-Host "Next: run  .\measure-power.ps1 -Scenario st-idle  -DurationMin $DurationMin" -ForegroundColor Cyan
  }
  else {
    Write-Host "Next: run  .\measure-power.ps1 -Diff   to see the delta." -ForegroundColor Cyan
  }
}
finally {
  if ($restoreBattery) {
    # CRITICAL: undo the simulated unplug, or the phone keeps thinking it's on
    # battery (won't charge over USB) until rebooted.
    & $adb shell dumpsys battery reset | Out-Null
    Write-Host "Battery state restored (charging re-enabled)." -ForegroundColor DarkGray
  }
}
