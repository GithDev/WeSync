# power-smoke-test.ps1 - on-device smoke tests for the WeSync Android power gate.
#
# Exercises the WorkManager-based background-sync rebuild against a connected
# device (USB debugging). Read-mostly, but a few tests intentionally spoof
# device state (battery level) or reboot - all restored afterwards.
#
#   powershell platform/android/power-smoke-test.ps1            # tests 1-4 (fast)
#   powershell platform/android/power-smoke-test.ps1 -Reboot    # also test 5 (reboot)
#
# Requires: ANDROID_HOME set, the debug APK installed, a folder shared.
param(
    [switch]$Reboot,
    [int]$ApiHostPort = 48820
)

$ErrorActionPreference = "Continue"
$adb = if ($env:ANDROID_HOME) { Join-Path $env:ANDROID_HOME "platform-tools\adb.exe" } else { "adb" }
$pkg = "com.wesync.app"
$script:results = @()

function Pass($n, $d) { $script:results += [pscustomobject]@{ Test = $n; Result = "PASS"; Detail = $d }; Write-Host "PASS  $n  -  $d" -ForegroundColor Green }
function Fail($n, $d) { $script:results += [pscustomobject]@{ Test = $n; Result = "FAIL"; Detail = $d }; Write-Host "FAIL  $n  -  $d" -ForegroundColor Red }
function Info($m) { Write-Host "      $m" -ForegroundColor DarkGray }

function DeviceEpochMs { [int64]((& $adb shell date '+%s').Trim()) * 1000 }
function Background { & $adb shell input keyevent KEYCODE_HOME 2>&1 | Out-Null; Start-Sleep -Seconds 2 }

function WesyncJobIds {
    (& $adb shell dumpsys jobscheduler 2>&1 |
        Select-String -Pattern "JOB #u0a\d+/(\d+): .*$([regex]::Escape($pkg))/androidx.work").Matches |
        ForEach-Object { $_.Groups[1].Value } | Select-Object -Unique
}

function ForcePolls {
    $ids = WesyncJobIds
    foreach ($id in $ids) { & $adb shell cmd jobscheduler run -f $pkg $id 2>&1 | Out-Null }
    Info "forced WorkManager jobs: $($ids -join ', ')"
    return $ids
}

# Launches the activity (brings the Go backend up) and reads the persistent
# power-event log via the loopback API. Reading history is safe: the events we
# assert on were already written by the forced background job.
function ReadEvents([int]$limit = 50) {
    & $adb shell am start -n "$pkg/.MainActivity" 2>&1 | Out-Null
    Start-Sleep -Seconds 14
    & $adb forward "tcp:$ApiHostPort" "tcp:47820" 2>$null | Out-Null
    Start-Sleep -Seconds 2
    try { return @(Invoke-RestMethod "http://127.0.0.1:$ApiHostPort/api/power/events?limit=$limit" -TimeoutSec 10) }
    catch { Info "API read failed: $($_.Exception.Message)"; return @() }
}

function EventsSince($events, [int64]$sinceMs, $kinds) {
    $events | Where-Object {
        ([DateTimeOffset]::Parse($_.timestamp).ToUnixTimeMilliseconds() -ge ($sinceMs - 3000)) -and
        ($null -eq $kinds -or $_.kind -in $kinds)
    } | Sort-Object timestamp
}

function FirstFolderPath {
    try {
        $f = Invoke-RestMethod "http://127.0.0.1:$ApiHostPort/api/folders" -TimeoutSec 10
        return (@($f)[0].path)
    } catch { return $null }
}

Write-Host ""
Write-Host "=== WeSync power-gate smoke test ===" -ForegroundColor Cyan
$dev = (& $adb shell getprop ro.product.model 2>&1).Trim()
$sdk = (& $adb shell getprop ro.build.version.sdk 2>&1).Trim()
if (-not $dev) { Write-Host "No device. Connect USB + enable debugging." -ForegroundColor Red; exit 1 }
Info "device: $dev (API $sdk)"

# Test 1: WorkManager jobs scheduled
$ids = WesyncJobIds
if ($ids.Count -ge 1) { Pass "jobs-scheduled" "$($ids.Count) WorkManager job(s): $($ids -join ', ')" }
else { Fail "jobs-scheduled" "no WorkManager SystemJobService jobs for $pkg" }

# Test 2: change detection opens a session
$null = ReadEvents 50
$folder = FirstFolderPath
if (-not $folder) {
    Fail "change-detection" "no shared folder found (share one first) - skipping"
}
else {
    Info "folder: $folder"
    $testFile = "$folder/wesync-smoke-$([int64](Get-Date -UFormat %s)).txt"
    & $adb shell "echo smoke > '$testFile'" 2>&1 | Out-Null
    Background
    $t0 = DeviceEpochMs
    ForcePolls | Out-Null
    Start-Sleep -Seconds 10
    $ev = ReadEvents 50
    $hit = EventsSince $ev $t0 @('tick', 'trigger') | Where-Object { $_.message -match "structural changes detected|session opened" }
    if ($hit) { Pass "change-detection" "poll detected the new file and opened a session" }
    else { Fail "change-detection" "no 'structural changes / session opened' after creating the test file" }
    & $adb shell "rm -f '$testFile'" 2>&1 | Out-Null
}

# Test 3: battery-low gate refuses
Background
& $adb shell dumpsys battery set level 5 2>&1 | Out-Null
& $adb shell dumpsys battery set status 3 2>&1 | Out-Null
Start-Sleep -Seconds 2
$t0 = DeviceEpochMs
ForcePolls | Out-Null
Start-Sleep -Seconds 8
$ev = ReadEvents 50
$refused = EventsSince $ev $t0 @('tick') | Where-Object { $_.message -match "conditions not met|skipped" }
$opened = EventsSince $ev $t0 @('trigger') | Where-Object { $_.message -match "session opened" }
if ($refused -and -not $opened) { Pass "battery-low-gate" "low battery -> poll refused (no session opened)" }
elseif ($opened) { Fail "battery-low-gate" "session opened despite low battery (gate did not refuse)" }
else { Fail "battery-low-gate" "no refusal event observed (pauseWhenBatteryLow off, or threshold not crossed)" }
& $adb shell dumpsys battery reset 2>&1 | Out-Null

# Test 4: job is allowed to run in Doze (or battery-exempt)
$dump = & $adb shell dumpsys jobscheduler 2>&1
$flag = $dump | Select-String -Pattern "$([regex]::Escape($pkg)).*(ALLOWED_IN_DOZE|WHITELISTED)"
if ($flag) { Pass "doze-allowed" "WorkManager job runs in Doze / battery-exempt" }
else { Fail "doze-allowed" "job not flagged ALLOWED_IN_DOZE/WHITELISTED" }

# Test 5: reboot survival (opt-in)
if ($Reboot) {
    Info "rebooting device - takes ~30-60s"
    & $adb reboot
    & $adb wait-for-device
    for ($i = 0; $i -lt 60; $i++) { if ((& $adb shell getprop sys.boot_completed 2>$null).Trim() -eq "1") { break }; Start-Sleep -Seconds 5 }
    Start-Sleep -Seconds 20
    $idsAfter = WesyncJobIds
    if ($idsAfter.Count -ge 1) { Pass "reboot-survival" "WorkManager rescheduled $($idsAfter.Count) job(s) after reboot without opening the app" }
    else { Fail "reboot-survival" "no WorkManager jobs after reboot (schedule did not survive)" }
}

# Summary
Write-Host ""
Write-Host "=== Summary ===" -ForegroundColor Cyan
$script:results | ForEach-Object { $c = if ($_.Result -eq "PASS") { "Green" } else { "Red" }; Write-Host ("{0,-5} {1,-20} {2}" -f $_.Result, $_.Test, $_.Detail) -ForegroundColor $c }
$passed = @($script:results | Where-Object { $_.Result -eq 'PASS' }).Count
$fails = @($script:results | Where-Object { $_.Result -eq 'FAIL' }).Count
Write-Host ""
Write-Host "$passed passed, $fails failed" -ForegroundColor Cyan
exit $fails
