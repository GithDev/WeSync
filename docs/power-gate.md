# Power gate — Android background sync

What this document captures: **how WeSync decides when to run Syncthing (ST)
in the background on Android**, and how the four trigger modes differ. Read
this before touching anything in `mobile/gate*.go`, `mobile/events.go`,
or `mobile/poll_watcher.go`.

The gate controls ST's **process lifecycle only** — it starts and stops the ST
subprocess. It never touches per-folder pause state; that belongs to the user
via the UI. (Earlier versions paused folders from here and caused steady
state-desync bugs because two writers owned the same flag.)

---

## The core formula

There is exactly one computed property:

```
desiredRunning =
    appForeground || withinForegroundGrace     // UI needs the ST API
    || (networkAllowed                         // privacy + cost gate
        && (charging && keepSyncingWhileCharging   // charging override
            || (batteryAllowed && sessionOpen)))   // normal background run
```

`networkAllowed` is an **absolute** gate — even charging can't buy past it.
`batteryAllowed` is bypassed when plugged in and `keepSyncingWhileCharging` is
set. A session being open is the background reason for ST to run in every
trigger mode (see [Session lifecycle](#session-lifecycle) below).

The pure function `snapshot.desiredRunning()` in `gate_decision.go` implements
this exactly. Every other file just updates an input field and kicks the
reconcile loop; the loop calls `desiredRunning()` from scratch on every wake.

---

## Trigger modes

The gate has four trigger modes. They differ only in **when a session opens**,
not in how the session runs or closes — that part is shared.

### `periodic`

AlarmManager fires every `periodicMinutes`. `OnTriggerAlarm` checks the power
gate and opens a session. Nothing else. ST sleeps between alarms.

### `scheduled`

Same as periodic but the alarm is set to specific times-of-day
(`scheduledTimes`, "HH:MM" strings). AlarmManager wakes at each time;
`OnTriggerAlarm` opens a session.

### `on_change_poll`

Change detection via directory-mtime polling. `pollCheckChanged()` walks
directory mtimes on every alarm tick and compares against a snapshot.

Key characteristics:
- **Service does NOT need to stay resident** between alarms — there is no live
  watcher holding a WakeLock. `ShouldStayResident()` uses the normal
  `desiredRunning` logic.
- **Structural changes only**: directory mtime updates on file/directory
  create, delete, rename — but NOT on content-only writes (editing an existing
  file's bytes). For receive/sendreceive folders this is fine because the alarm
  opens a session regardless of the poll result (peer changes always sync). For
  sendonly folders a pure in-place edit won't trigger until a structural change
  also occurs.
- **Cold-start always syncs**: `pollCheckChanged` returns `true` when the
  snapshot is nil (first call after a restart), triggering a catch-up session.
- The alarm interval is the throttle — there is no sub-interval low-latency
  path; `OnTriggerPollAlarm` is the fast path, `OnTriggerAlarm` is the backstop.

---

## Session lifecycle

A session is the gate's permission for ST to run in the background. It has an
expiry time (`sessionEndsAt`); when the expiry lapses without being extended,
`desiredRunning` goes false and the loop stops ST.

### Opening

`OpenSyncSession()` sets `sessionEndsAt = now + connectGrace` (120 s). The
grace covers the cold connect path: announce → discovery → relay handshake →
BEP — far slower than LAN when connectivity is global (level 2–3). At the old
60 s a background trigger could lapse before a relayed peer even connected.

Re-triggering an in-flight session (backstop tick, manual Sync Now, the watcher
firing again) extends `sessionEndsAt` without resetting `sessionStartedAt` or
the stall guard — those are anchored to the current sync, not the trigger.

### Extending

The reconcile loop polls ST every 15 s while a session is open. Each poll
decides the new `sessionEndsAt`:

| ST state | Action |
|---|---|
| Folder busy (scanning/syncing) | Extend by `activeSyncExtend` (5 min) |
| Connected peer still needs our data | Extend if bytes are still moving |
| No peer connected, within connect grace | Hold open (deadline unchanged) |
| Idle + connected + nobody behind | Let deadline lapse → session closes |

There is **no fixed session cap** — a large transfer (e.g. a 4 GB download to
a peer) can outlast any ceiling. The stall guard bounds no-progress sessions
instead.

### Stall guard

When a connected peer still needs data from us but no bytes have moved for
`stallPollLimit` (3) consecutive polls (with a floor of 64 KB per poll to
ignore ST's own keepalive chatter), the session is treated as stalled and the
deadline is allowed to lapse. This prevents a wedged ST REST endpoint or a
stuck transfer from pinning ST forever.

A folder scan (`folderBusy`) is NOT stall-guarded — scanning is local
CPU/disk work and ST flips to idle on its own when done.

---

## Input events

All inputs enter through `events.go`. Each one updates a single field under
the lock and kicks the reconcile loop. The loop never blocks on the caller.

| Event | Function | What it changes |
|---|---|---|
| App foreground/background | `OnAppForeground(fg bool)` | `appForeground`, `foregroundUntil` |
| Network change | `OnNetworkState(...)` | `currentSSID`, `hasWifi`, `hasMobile`, `metered`, `roaming`, `activeWifi` |
| Charging plug/unplug | `OnChargingState(charging bool)` | `charging` |
| Battery low warning | `OnBatteryLow(low bool)` | `batteryLow` |
| AlarmManager tick | `OnTriggerAlarm()` | may call `OpenSyncSession` |
| Settings changed | `RefreshPowerSettings()` | re-reads DB, updates `settings` |

`OnNetworkState` also triggers `OpenSyncSession()` immediately when the
network goes from blocked to allowed — covers "came home, want sync now" for
all trigger modes without waiting for the next alarm.

`OnAppForeground(false)` sets a `foregroundGrace` deadline (60 s) so a
transient background (SAF folder picker, permission dialog, settings page)
doesn't tear ST down and break the next API call.

---

## Poll watcher (`on_change_poll`)

Lives in `poll_watcher.go`. Takes a snapshot of directory mtimes across all
synced folders and compares it against the previous snapshot on each alarm tick.

**What it detects:** file/directory create, delete, rename — all update the
parent directory's mtime on Linux/Android. Pure in-place content edits do not.

**Snapshot:** stored in memory only (`poll.dirs`). Cold-start (nil snapshot)
always returns `true` so the first alarm after a restart triggers a full sync.
`resetPollSnapshot()` discards it when the folder set changes.

**Hidden directories are skipped** (dot-prefix) to avoid walking `.stversions`,
`.git`, `node_modules/.cache` etc. The folder root itself is exempted so a
path beginning with `.` still works.

---

## Network gate details

`networkAllowed()` in `gate_decision.go`:

1. **`blockMeteredRoaming`** (default on): refuses roaming cellular OR metered
   WiFi (hotspot/tethering). Ordinary mobile data at home is NOT refused —
   Android marks all cellular metered, but `metered && activeWifi` pins it to
   "metered WiFi specifically", not "any cellular".

2. **`networkMode`:**
   - `any` — WiFi or mobile, passes once blockMeteredRoaming clears.
   - `any_wifi` — WiFi interface must be connected (`hasWifi`).
   - `trusted_wifi` — WiFi AND the current SSID must be in `trustedSSIDs`
     (case-insensitive). Falls back to blocked if location permission is denied
     (SSID becomes empty string).

---

## Android integration

The gate is embedded in the WeSync Android service (`WeSyncService.kt`).
Integration points:

| Gate API | Android caller |
|---|---|
| `SetPowerHost(h)` | Service `onCreate` — registers to receive `OnSyncActive` |
| `OnAppForeground` | `MainActivity.onResume` / `onPause` |
| `OnNetworkState` | `NetworkStateReceiver` broadcast listener |
| `OnChargingState`, `OnBatteryLow` | `PowerStateReceiver` broadcast listener |
| `OnTriggerAlarm` | `AlarmReceiver` — AlarmManager fires via `WakePlanJSON` schedule |
| `RefreshPowerSettings` | Kotlin bridge after `PUT /api/power` succeeds |
| `ShouldStayResident()` | Service `onStartCommand` shutdown path |
| `WakePlanJSON()` | Kotlin re-arms AlarmManager after every settings change |
| `LogPowerEvent(kind, msg)` | Kotlin logs service lifecycle events (wake, shutdown) |

`WakePlanJSON()` returns the full alarm schedule so Android is a dumb executor
— it arms whatever the gate says. The gate owns all trigger interpretation.

---

## Settings flow

```
User changes setting in UI
  → PUT /api/power (HTTP)
  → store.SavePowerSettings (SQLite)
  → Kotlin bridge calls RefreshPowerSettings()
  → refreshSettingsFromDB() re-reads SQLite, updates g.settings
  → applyFSWatcherDelay()                 // async: reset ST's fsWatcher to default
  → emitEvent("settings", ...)            // visible in Recent activity
  → requestReconcile()
```

`applyFSWatcherDelay()` is a migration cleanup function: old app versions pushed
`OnChangeDebounceMinutes` into ST's own `fsWatcherDelayS` to throttle ST's
watcher. WeSync now owns its own change coalescing so ST's fsWatcher just needs
its default (10 s).

---

## Key invariants

- **One decision, one actor.** `desiredRunning()` is the only property, `reconcileLoop`
  is the only place that starts/stops ST. No other code touches the subprocess.
- **No incremental state.** The loop recomputes from scratch every time. There are no
  "last applied" flags; the only genuinely stored state is `sessionEndsAt`.
- **Never pause folders from the gate.** That path causes state-desync bugs. Pause
  state belongs to the user exclusively.

---

## Code layout (`mobile/` package)

| File | Responsibility |
|---|---|
| `gate.go` | `gate` struct, lifecycle (`initGate`, `markStopped`), event log, reconcile trigger |
| `gate_decision.go` | Pure snapshot + `desiredRunning` logic — no I/O, fully unit-testable |
| `gate_reconcile.go` | Loop that acts on the decision: starts/stops ST, probes ST, drives stall guard |
| `gate_settings.go` | DB reads, `applyFSWatcherDelay`, folder-unpause migration, ST client helper |
| `events.go` | Input entry points from Android (`OnAppForeground`, `OnNetworkState`, etc.) |
| `poll_watcher.go` | `on_change_poll` directory-mtime snapshot |
| `gate_test.go` | Unit tests for `desiredRunning` (clock-injected, no real ST) |
| `poll_watcher_test.go` | Unit tests for `pollCheckChanged` |
