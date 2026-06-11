# Power gate — Android background sync

What this document captures: **how WeSync decides when to run Syncthing (ST)
in the background on Android**, and how the three trigger modes differ. Read
this before touching anything in `mobile/gate*.go`, `mobile/events.go`,
`mobile/poll_watcher.go`, or the Kotlin power layer (`SyncScheduler.kt`,
`SyncWorker.kt`, `BackendOwnership.kt`, `PowerController.kt`, `PowerSignals.kt`).

The gate controls ST's **process lifecycle only** — it starts and stops the ST
subprocess. It never touches per-folder pause state; that belongs to the user
via the UI. (Earlier versions paused folders from here and caused steady
state-desync bugs because two writers owned the same flag.)

---

## Two layers

The model has exactly two layers. **Conditions** are absolute gates; **when**
chooses the trigger cadence once the conditions pass.

### Layer 1 — Conditions (absolute gates)

If any of these fails, nothing syncs in the background. They are decided in the
Go gate (`gate_decision.go`), never via WorkManager constraints — one decision
maker, no drift.

- **Network mode** (`any` / `any_wifi` / `trusted_wifi`). `trusted_wifi`
  requires the current SSID to be in the trusted list (needs background
  location to read the SSID while the app is closed).
- **Block metered + roaming** — refuses roaming cellular and metered WiFi
  (hotspot/tethering). Ordinary metered cellular (the user's normal mobile
  data) still syncs.
- **Pause when battery low** — Android's own low-battery signal
  (`ACTION_BATTERY_LOW`), not battery-saver mode.

These map 1:1 to the FE "Where to sync" + "Battery & data" sections
(`web/src/pages/SettingsPage/PowerSection.tsx`), which is the authoritative
picture of the conditions.

### Layer 2 — When (trigger modes)

Only once the conditions pass does the trigger decide *when* a sync session
opens. Three modes:

- **`periodic`** — a session opens every `periodicMinutes`.
- **`scheduled`** — a session opens at specific times of day (`scheduledTimes`,
  "HH:MM").
- **`on_change_poll`** — a lightweight directory-mtime walker
  (`pollCheckChanged`) runs every `onChangePollMinutes` and opens a session only
  if something changed, PLUS a `periodicMinutes` safety-net that always opens a
  session (so peer changes still arrive even with no local change).

**15-minute floor.** All background wake-ups go through WorkManager, whose
minimum periodic interval is 15 minutes — and under Doze no mechanism can wake
more often than that anyway. So every interval is clamped to ≥15 min and is
approximate (±~15 min). The FE only offers 15/30/60+ for this reason.

---

## The core decision

There is exactly one computed property, `snapshot.desiredRunning()` in
`gate_decision.go`:

```
desiredRunning =
    appForeground || withinForegroundGrace   // UI needs the ST API
    || (networkAllowed                        // Layer-1 network gate
        && batteryAllowed                      // Layer-1 battery gate
        && sessionOpen)                        // a trigger opened a session
```

`networkAllowed` and `batteryAllowed` are absolute — an open session can't buy
past them. Every other file just updates an input field and kicks the reconcile
loop; the loop calls `desiredRunning()` from scratch on every wake. There is no
charging override (the old `keepSyncingWhileCharging` feature was removed; the
DB column is a harmless orphan).

---

## Scheduling — WorkManager (the Android side)

WorkManager is the scheduling backbone. It is Doze-aware, persists its queue
across reboot (so there is **no boot receiver**), and owns the
foreground-service start itself — which is why the old AlarmManager path is
gone (a background `startForegroundService` from an inexact alarm was silently
rejected on Android 12+, killing the whole alarm chain).

`SyncScheduler` reads the gate's wake plan (`Mobile.wakePlanJSON()`) and enqueues:

| Mode | WorkManager requests |
|---|---|
| `periodic` | one `PeriodicWorkRequest` (role=`trigger`) |
| `on_change_poll` | a poll `PeriodicWorkRequest` (role=`poll`, syncs only if changed) + a safety-net `PeriodicWorkRequest` (role=`trigger`, always syncs) |
| `scheduled` | a `OneTimeWorkRequest` with an initial delay to the next HH:MM; re-enqueued on completion |

Settings changes (and app open) call `SyncScheduler.reapply`, which runs a
one-shot `ReapplyScheduleWorker` that re-reads the plan and re-enqueues with
`UPDATE`/`REPLACE`. The gate owns all trigger interpretation; Android is a dumb
executor.

### The sync worker

`SyncWorker` is one background wake-up, a **long-running foreground worker**:

1. `setForeground()` — a dataSync FGS for the sync's duration (WorkManager owns
   the FGS start, so the background-start restriction can't bite).
2. `BackendOwnership.acquire("worker:<id>")` — brings the Go backend up if down.
3. `PowerSignals.pushToGate()` — seeds current network/battery state into the
   gate (a cold worker has no live callbacks, so this one-shot read is required
   or the gate evaluates zero-value inputs and skips).
4. Fires the trigger: `onTriggerPollAlarm()` (role=poll) or `onTriggerAlarm()`
   (everything else).
5. **Awaits session close** — polls `Mobile.shouldStayResident()` until the gate
   lets the session lapse, so a sync is **never interrupted**. A refused trigger
   (conditions not met) reads not-resident immediately and returns in ~1–2 s
   without holding the FGS.
6. Releases the backend; re-arms the next `scheduled` occurrence if applicable.

**Known limitation:** Android 14 caps a `dataSync` FGS at ~6 h; a longer single
sync makes the worker return `Result.retry()` and re-attach (ST keeps running
under the gate across the gap).

---

## Backend ownership

The Go backend (`Mobile.start`) is a process-global singleton shared by the UI
foreground service and the sync worker. `BackendOwnership` refcounts owners
(`"ui"`, `"worker:<id>"`):

- `acquire` starts the backend if down (idempotent).
- `release` stops it (`Mobile.stop`) **only** when no owner remains AND
  `Mobile.shouldStayResident()` is false (no sync still finishing). This is the
  single chokepoint for `Mobile.stop`.

`BackendOwnership` is also the gate's single `PowerHost`: on `OnSyncActive` it
holds/releases the MulticastLock (LAN discovery) and a partial WakeLock (CPU
through a Doze-time sync). The UI service no longer self-stops on a timer; it
just holds the `"ui"` token while the activity is visible and releases it after
a short grace when the user leaves.

---

## Session lifecycle

A session is the gate's permission for ST to run in the background. It has an
expiry (`sessionEndsAt`); when it lapses without extension, `desiredRunning`
goes false and the reconcile loop stops ST.

### Opening

`OpenSyncSession()` sets `sessionEndsAt = now + connectGrace` (120 s). The grace
covers the cold connect path (announce → discovery → relay handshake → BEP).
Re-triggering an in-flight session extends `sessionEndsAt` without resetting
`sessionStartedAt`.

### Extending

The reconcile loop polls ST every 15 s while a session is open:

| ST state | Action |
|---|---|
| Folder busy (scanning/syncing) | Extend by `activeSyncExtend` (5 min) |
| Connected peer still needs our data | Extend |
| No peer connected, within connect grace | Hold open |
| Idle + connected + nobody behind | Let the deadline lapse → session closes |

**There is no stall guard and no session cap.** Once ST is awake the sync runs
to completion — a wedged peer-pull or a hung ST REST will keep the session open
(the Android 14 FGS cap is the only backstop). This is the deliberate
"never interrupt a sync" guarantee.

---

## Input events

All inputs enter through `events.go`. Each updates a single field under the lock
and kicks the reconcile loop.

| Event | Function | What it changes |
|---|---|---|
| App foreground/background | `OnAppForeground(fg)` | `appForeground`, `foregroundUntil` |
| Network change | `OnNetworkState(...)` | SSID, wifi/mobile, metered, roaming, activeWifi |
| Battery low warning | `OnBatteryLow(low)` | `batteryLow` |
| Trigger wake-up | `OnTriggerAlarm()` / `OnTriggerPollAlarm()` | may call `OpenSyncSession` |
| Settings changed | `RefreshPowerSettings()` | re-reads DB, updates `settings` |

`OnNetworkState` also opens a session immediately when the network goes from
blocked to allowed ("came home, want sync now"), guarded against rapid
reconnects. `OnAppForeground(false)` sets a 60 s `foregroundGrace` so a transient
background (picker, dialog) doesn't tear ST down.

Live network/battery state is fed by `PowerController` while the UI is open, and
by `PowerSignals` (a one-shot read) from a cold background worker.

---

## Poll watcher (`on_change_poll`)

`poll_watcher.go` takes a snapshot of directory mtimes across all synced folders
and compares it on each poll wake-up. Detects file/dir create, delete, rename
(which bump the parent dir mtime); pure in-place content edits do not bump it,
which is why the safety-net always syncs regardless. Cold-start (nil snapshot)
returns `true` to force a catch-up. Hidden directories (dot-prefix) are skipped.

---

## Key invariants

- **One decision, one actor.** `desiredRunning()` is the only property;
  `reconcileLoop` is the only place that starts/stops ST.
- **No incremental state.** The loop recomputes from scratch every time; the only
  stored state is `sessionEndsAt` / `sessionStartedAt`.
- **Never pause folders from the gate.** Pause state belongs to the user.
- **WorkManager schedules, the gate decides.** No WorkManager network/battery
  constraints — the gate is the single source of truth for whether ST may run.
- **Never interrupt a sync.** Once ST is awake it runs to completion.

---

## Code layout

### Go (`mobile/` package)
| File | Responsibility |
|---|---|
| `gate.go` | `gate` struct, lifecycle, event log, reconcile trigger |
| `gate_decision.go` | Pure snapshot + `desiredRunning` — no I/O, unit-testable |
| `gate_reconcile.go` | Loop that starts/stops ST, probes ST, extends the session |
| `gate_settings.go` | DB reads, `applyFSWatcherDelay`, folder-unpause migration |
| `events.go` | Input entry points + `OpenSyncSession` / `ShouldStayResident` / `WakePlanJSON` |
| `poll_watcher.go` | `on_change_poll` directory-mtime snapshot |

### Kotlin (`platform/android/.../com/wesync/app/`)
| File | Responsibility |
|---|---|
| `SyncScheduler.kt` | Wake plan → WorkManager requests; `ReapplyScheduleWorker` |
| `SyncWorker.kt` | Long-running foreground worker; awaits session close |
| `BackendOwnership.kt` | Backend refcount, single `Mobile.stop` chokepoint, `PowerHost` + locks |
| `PowerSignals.kt` | One-shot network/battery read for a cold worker |
| `PowerController.kt` | Live network/battery/SSID feed while the UI is foreground |
| `WeSyncService.kt` | UI-foreground host (holds the `"ui"` backend token) |
| `SyncNotification.kt` | Shared foreground-service notification |
