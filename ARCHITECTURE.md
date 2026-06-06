# WeSync — Architecture

*Verified against the source on 2026-06-06.*

WeSync is a coordination and signaling layer in front of **Syncthing**. Syncthing does the actual file transfer and is the **source of truth** for which devices are paired and which folders are shared; WeSync provides discovery, mutual-consent pairing, a peer coordination channel, folder-sharing orchestration, and the UI. The companion document is [`docs/state-model.md`](docs/state-model.md) (the exact per-device folder state dimensions). The live endpoint list is [`internal/api/server.go`](internal/api/server.go).

---

## What WeSync is

1. **UDP multicast peer discovery** — find other WeSync nodes on the LAN.
2. **Mutual-consent pairing** — both sides must agree before anything is shared; pairing writes straight to each side's Syncthing config.
3. **A TLS cert-pinned peer wire** — a device-to-device coordination channel whose identity *is* the TLS certificate.
4. **Folder sharing** — choose which folders sync to which paired device.

**Syncthing is both the execution engine (file transfer over BEP) and the source of truth** for devices, folders, folder-device relationships, and device names. WeSync's SQLite database holds only a `Settings` row and a `PowerEvent` log; everything about who is paired and what is shared is read live from Syncthing's REST API.

WeSync runs its **own dedicated Syncthing instance** — a private home dir (its own `config.xml`/`cert.pem`/`key.pem`, i.e. its own device identity) with the GUI/API on `127.0.0.1:8385`, deliberately off Syncthing's default `8384` so it coexists with a user's personal Syncthing without clashing. It does **not** adopt or import an existing installation.

---

## Repository layout

```
main.go                  — service entry point (flags/env, manages Syncthing, starts backend.Run)
cmd/app/                 — Wails desktop wrapper (tray, window, spawns the service)
mobile/                  — gomobile bind surface for Android (Go; embeds web/dist)
web/                     — React + Vite frontend (pages/, components/, state/, api/, hooks/)
platform/android/        — Android app: Kotlin/Gradle shell over the AAR
platform/windows/        — NSIS installer
platform/linux/          — .deb/.rpm packaging
internal/
  backend/               — Run(): wires every package together + lifecycle
  api/                   — HTTP API + UI WebSocket hub + pairing/folder orchestration
                           (handlers.go, server.go, hub.go, pairing.go, peers.go, folders.go, push.go, …)
  node/                  — in-memory node STATE: peers/trust/folder-acceptance behind one
                           mutex, copy-on-read accessors + the FolderRelationState derive
                           function (state.go, derive.go). Owns all locking.
  peerwire/              — TLS cert-pinned peer wire (hub.go, conn.go, egress.go, message.go)
  discovery/             — UDP multicast announce/listen (service.go, announcer.go, listener.go)
  syncthing/             — ST REST client + Backend interface, the source of truth
                           (client.go, config.go, devices.go, folders.go, events.go)
  stmanager/             — launches/owns WeSync's own dedicated Syncthing subprocess
                           (private home + identity, API on :8385 to avoid a personal ST on :8384)
  store/                 — SQLite/GORM: Settings + PowerEvent only (models.go, store.go)
  certid/                — device-ID ⇄ TLS cert fingerprint; loads ST's cert for the wire
  ratelimit/             — per-key fixed-window limiter (guards /api/pair)
  sysinfo/               — host/device info for the wire Hello
  uid/                   — short ID helper
```

**Where state lives:** `internal/node` owns the mutable in-memory projection (peers, trust sets, folder acceptance) behind a single mutex with copy-on-read accessors. `internal/api` is transport + orchestration and holds no state map of its own. Syncthing is the source of truth; the SQLite store keeps only `Settings` + a `PowerEvent` log. Build/run helpers: `dev.ps1` (multi-instance local dev), `build.ps1`, and the `Makefile` (containerized cross-platform builds).

---

## CLI flags

```
--syncthing-url    string  Syncthing API URL              (env WESYNC_SYNCTHING_URL)
--syncthing-key    string  Syncthing API key              (env WESYNC_SYNCTHING_KEY)
--syncthing-home   string  Syncthing home dir (TLS cert)  (env WESYNC_SYNCTHING_HOME)
--db               string  SQLite database path           (env WESYNC_DB)
--port             int     HTTP API port, localhost only  (default 47820, env WESYNC_PORT)
--peer-port        int     WSS peer port, LAN             (default 47821, env WESYNC_PEER_PORT)
--debug            bool    Enable debug logging           (env WESYNC_DEBUG)
```

Flags take precedence over env vars, which take precedence over defaults. In the normal desktop/Android packaging WeSync manages Syncthing itself (embedded binary, see `internal/stmanager`): when `--syncthing-key` is empty it extracts/starts the bundled Syncthing and uses its home dir, so these flags are mostly for development. `--syncthing-home` must resolve to Syncthing's home — the wire requires ST's `cert.pem`/`key.pem`, and the backend refuses to start without them (see [Startup sequence](#startup-sequence)).

`main.go` also handles Windows-service entry points: `--syncthing-service` runs only the embedded Syncthing as a Windows service, and the binary detects when it is launched as a service.

For local multi-instance dev, use separate `--db` and `--port` per instance:

```powershell
.\dev.ps1   # starts Syncthing + WeSync instances in separate terminal windows
```

---

## Storage: SQLite + GORM

- Driver: `github.com/glebarez/sqlite` (pure Go, no CGO); ORM: `gorm.io/gorm`.
- **`SetMaxOpenConns(1)`** — SQLite has no concurrent writers; without it `:memory:` also yields a fresh empty DB per connection. WAL mode is on. `AutoMigrate` runs on startup.

Only two tables exist; everything about devices/folders/relationships lives in Syncthing and is read live (`internal/store/models.go`):

```
settings        (single row — WeSync-only config ST has no concept of)
  id                              INTEGER PK
  name                            device display name (falls back to hostname)
  connectivity_level              1=local (default) · 2=discovery · 3=relay
  introducer                      bool (default true)
  visible                         UDP-announce preference (persisted; still gated on foreground)
  power_*                         Android power-gate settings (sync trigger, periodic minutes,
                                  scheduled times, network mode, trusted SSIDs, on-change debounce,
                                  battery/charging/metered gates)
  unpause_migration_done          one-shot upgrade marker

power_events    (rolling log of power-gate transitions; ~200 most recent kept)
  id          INTEGER PK
  timestamp   unix ms (indexed)
  kind        start | stop | trigger | sync | gate | error
  message     text
```

`FolderWithDevices`, `Folder`, and `PeerDetail` in the same file are **API view types**, not tables — they are assembled from Syncthing on each read.

---

## Portability & isolation

The `wesync` binary is self-contained — a static Go binary that finds `syncthing`/`syncthing.exe` next to itself — so it runs from anywhere. *Where* it keeps its data is a per-OS default, not a constraint:

- **Windows** — `DataDir()` is `<exe-dir>/data/` (`internal/stmanager/datadir_windows.go`). Copy the folder (exe + Syncthing + `data/`) anywhere — a USB stick, another machine — and it just works; the only thing an install adds is the autostart task.
- **Linux/macOS** — `DataDir()` follows XDG (`$XDG_DATA_HOME`, else `~/.local/share/wesync`) by convention (`datadir_unix.go`). For a portable layout, point it back next to the binary with `WESYNC_DB` (its parent dir becomes the data dir) or `XDG_DATA_HOME`.

Because the data dir, ports, and Syncthing home are all selectable per process (`--db`/`--port`/`--syncthing-home`, or the `WESYNC_*` env), several **fully independent** WeSync instances can run side by side on one machine — each with its own Syncthing identity. That's exactly what `dev.ps1` does (three isolated instances from `testdata/`) and what the API tests rely on (two in-process instances on `:memory:` SQLite).

---

## HTTP API

Two servers (canonical, full route list in `internal/api/server.go`):

- **API server** — `127.0.0.1:47820`, plain HTTP (localhost only, no TLS → no cert warnings in the browser/WebView). Serves the SPA and all `/api/*`. CORS accepts only `localhost`/`127.0.0.1`/`::1`/`*.localhost`; bodies capped at 1 MB; `/api/pair` is rate-limited (20/min per IP).
- **Peer server** — `0.0.0.0:47821`, HTTPS/WSS with TLS + cert-pinning. Serves only `/peer/ws`, the LAN peer wire.

Endpoints grouped by area (see the handlers for exact methods):

| Area | Paths |
|------|-------|
| Identity / lifecycle | `/api/status` (`{myID, name, buildTime}`), `/api/exit` |
| Discovery / foreground | `/api/mode` (UDP announce on/off), `/api/active` (UI foreground/background) |
| Devices / pairing | `/api/peers`, `/api/devices`, `/api/pair`, `/api/incoming` |
| Refresh | `/api/sync` (re-read ST + refresh UI — **not** a DB→ST push) |
| Name / connectivity | `/api/name`, `/api/connectivity`, `/api/connectivity-status` |
| Power (Android) | `/api/power`, `/api/power/events`, `/api/power/sync-now`, `/api/power/status` |
| Folders | `/api/folders`, `/api/folders/pending`, and the `/api/folder/*` set (pick, share, accept, decline, device, direction, label, check, fix-marker, revert, pause, status, ignores, conflicts, conflict) plus `DELETE /api/folder` |
| WebSockets | `/api/ws` (UI state push, API server) · `/peer/ws` (peer wire, peer server) |
| SPA | `/` (serves `web/dist`, falls back to `index.html`) |

`PUT /api/active {active: bool}` is the desktop app's explicit foreground signal: hiding to tray sends `false` (discovery + wire go silent), re-showing sends `true`. The UI-WebSocket connect/disconnect count is the backstop for the pure-web case (see `hub.OnActiveChange` in `backend.go`). File sync (ST/BEP) is unaffected — only WeSync's control plane goes quiet.

---

## Coordination wire (`peerwire`)

The wire is a **TLS cert-pinned WebSocket control plane** between peers. Identity IS the certificate: the device ID is derived from the cert (`internal/certid`), so a different cert is simply a different device. Incoming connections are validated by `OnValidateCertFP`, which rejects a fingerprint that doesn't match what was pinned from a previous session.

- **Transport:** `peerwire.Hub` keeps one outbound connection per known peer (`Connect`/`ConnectBySID` on UDP discovery), reconnecting with exponential backoff; a fresh announce nudges a backed-off conn to retry now. Inbound peers are accepted at `/peer/ws` (`ServeWS`), gated so the whole wire goes silent when the app is backgrounded. Messages are sent with `SendSync`/`SendHelloTo` or broadcast with `BroadcastHello`/`BroadcastPeerState`.
- **Message vocabulary** (`internal/peerwire/message.go`): `Hello` (identity + name + sysinfo + a `Trusted` flag), `PeerState` (name/sysinfo updates, sent to all peers), `Accepted`/`Cancelled`, and the folder signals `FolderOffer` / `FolderAccept` / `FolderDecline` / `FolderRemove` / `FolderSync`.
- **Role:** the wire *accelerates* what ST also exposes — it carries the `Trusted` flag and fast folder signals — but is never the sole source: every derived state has an ST-only read path. See `docs/state-model.md`.

---

## Pairing (ST-direct)

Pairing writes **straight to each side's Syncthing config** — there is no WeSync-side pairing store to reconcile.

- **Trust a peer:** `POST /api/pair {deviceID, name}` → `st.AddDevice(...)` and mark trusted in `internal/node`. The peer learns of it from our wire `Hello{trusted:true}` and records that we trust them.
- **Incoming / waiting:** derived, not stored. `incoming` = a peer's `Hello{trusted:true}` while we don't trust them yet (`node.TheyTrustUs`, **not** ST `pending`, which reflects laggy BEP state); `waiting` = we trust them but haven't seen their `trusted:true` back.
- **Remove:** `DELETE /api/devices?id=` → `st.RemoveDevice` + mark explicitly removed (so a stale ST re-add can't resurrect it) + notify the peer with `Hello{trusted:false}`, which cascades a symmetric removal on their side.

The handler steps live in `internal/api/pairing.go` + `peers.go`; the state transitions are defined in `docs/state-model.md`. `POST /api/sync` only re-reads ST and refreshes the UI — it does not push WeSync state into ST.

---

## Folder sharing

Folder operations read and write **directly in Syncthing** — there is no `folder_devices` table. An offer/accept handshake runs over the wire (`FolderOffer`/`FolderAccept`/`FolderDecline`/`FolderRemove`, with `FolderSync` carrying the set of current offers), and the resulting membership is written to each side's ST config. Each folder's per-device state is the `FolderRelationState` enum — one pure derive function, `node.DeriveFolderRelationState`, is the only place it is computed (canonical list + predicates in [`internal/node/derive.go`](internal/node/derive.go) and [`docs/state-model.md`](docs/state-model.md)). It is surfaced as `DeviceState` on `FolderWithDevices`:

```
not-member | invited |
accepted-paused-local | accepted-paused-remote |
accepted-syncing | accepted-sending | accepted-stalled |
accepted-idle | accepted-behind-offline | accepted-offline
```

Handlers live in `internal/api/folders.go` and `folder_state.go`.

---

## State push to the browser

`api.Hub` broadcasts JSON state snapshots to all UI WebSocket clients at `/api/ws`. Two scheduling primitives (`internal/api/push.go`, `pipeline.go`):

- **`SchedulePush()`** — debounced broadcast of the current in-memory snapshot. Use when the change is already reflected in memory.
- **`SchedulePipeline()`** — re-reads Syncthing into the in-memory projection, then pushes. Use when the change lives in ST. Called on ST events (`st.WatchEvents`) and on a 30 s ticker.

`snapshot()` builds the `wsState` struct (`internal/api/push.go`) by reading `internal/node` (copy-on-read) plus ST live:

```go
type wsState struct {
    MyID           string                    `json:"myID"`
    Name           string                    `json:"name"`
    Devices        []DeviceWithStatus        `json:"devices"`
    Incoming       []IncomingRequest         `json:"incoming"`
    Outgoing       map[string]string         `json:"outgoing"` // always empty — pairing is ST-direct
    Visible        bool                      `json:"visible"`   // discoverability preference
    Listening      bool                      `json:"listening"` // we are receiving UDP announces
    PendingFolders []syncthing.PendingFolder `json:"pendingFolders"`
    Folders        []store.FolderWithDevices `json:"folders"`
}
```

`Incoming` comes from the wire `trusted` signal (`node.TheyTrustUs`), filtered to peers with a live WeSync presence — not from ST `pending`. A device's `Connected` comes straight from `st.ConnectedDeviceIDs()` (the real sync link); each folder's per-device state is the `FolderRelationState` above.

---

## Frontend

- **State:** `web/src/api/wsService.ts` holds a singleton WebSocket connection and merges incoming JSON pushes into the client `WSState`; components subscribe via the `useWS()` hook (`web/src/api/websocket.tsx`). The shape mirrors the Go `wsState`.
- **Network view:** `web/src/components/Discovery/Discovery.logic.ts` exports `deriveNetwork(input, accepted)`, a pure function. `input` is `{ devices, incoming, folders }`; `accepted` is the set of optimistically-accepted device IDs to hide immediately. It returns a `NetworkEntry[]` discriminated union (`web/src/components/Discovery/types.ts`):

  ```typescript
  type NetworkEntry =
    | { kind: 'incoming';     id; name; peer }    // peer trusts us, we don't trust them yet
    | { kind: 'waiting';      id; name; peer }    // we trust them, not mutually accepted yet
    | { kind: 'connected';    id; name; device }  // paired/introduced and connected
    | { kind: 'offline';      id; name; device }  // known but not currently connected
    | { kind: 'discoverable'; id; name; peer };   // socket-connected, not known, not pairing
  ```

  Covered by `Discovery.logic.test.ts`. The page surfaces live in `pages/DevicesPage`, `pages/DevicePage`, `pages/FoldersPage`, `pages/FolderPage`, and `pages/SettingsPage` (`ConnectivitySection`, `PowerSection`).
- **REST:** `web/src/api/client.ts` wraps the endpoints (`pair`, `removeDevice`, `incoming`, `sync`, the folder operations, etc.).

---

## Startup sequence

`main.go` parses flags/env, reconciles with any backend already on the API port (defers to a healthy same-build instance, or asks a stale build to exit and takes over), acquires a port-scoped single-instance lock, starts the embedded Syncthing if no external key was given, then calls `backend.Run`, which:

```
1. Connect to Syncthing; poll SystemStatus until ready (up to 30 attempts), get MyID
2. st.LockToLocalNetwork(); if --debug, enable ST's GUI
3. Open SQLite (Settings + PowerEvent); derive device name (saved name, else hostname)
   and push it to ST on first run
4. Create discovery.Service; load ST's cert.pem/key.pem for the wire (FATAL if missing)
5. Create api.Hub + api.Handlers (seeds the node trust set from ST's device list);
   wire the hub's foreground callback → handlers.SetForeground
6. Apply the saved connectivity level; seed the discoverability preference (announce
   stays gated on foreground)
7. SchedulePipeline() — read ST into the in-memory projection (no DB→ST reconcile);
   no startup MaintainConnections — the wire stays quiet until the UI opens
8. Start goroutines: discovery.Run, st.WatchEvents → SchedulePipeline, forward
   discovered peers → TrackPeer and PeerGone → DropPeer, plus 30 s tickers for
   SchedulePipeline and MaintainConnections
9. Run the API + peer HTTP servers concurrently; return when either exits or ctx cancels
```

---

## Test strategy

- `internal/node/*_test.go` — the state machine: table-driven `DeriveFolderRelationState` plus a concurrent-hammer test proving the copy-on-read API is race-free.
- `internal/store/*_test.go` — Settings + PowerEvent round-trips on `:memory:` SQLite, plus power/coherence tests.
- `internal/peerwire/*_test.go` — two in-process Hubs exchange WS messages; trust-flag, nudge, accepting-gate, cert validation.
- `internal/api/*_test.go` — two in-process WeSync instances over real HTTP + WS against a mock Syncthing backend and `:memory:` SQLite (pairing, folders, foreground/reopen, security, concurrent-map guard).
- Frontend — vitest unit tests (e.g. `Discovery.logic.test.ts`, `folder-sync-summary.test.ts`), run with `npm test` in `web/`; Playwright e2e under `web/e2e/`.

**Timing note:** `SendSync` returns when the TCP write completes, but the remote processes the message asynchronously. API tests synchronize by polling an HTTP read (e.g. `/api/devices`) until it reflects the change.

---

## Key architectural decisions

1. **Syncthing owns the truth; WeSync is the human layer on top.** ST's config + `pending/devices` + connections endpoints *are* the pairing/folder state. WeSync reads them and assembles the human-facing view; it writes directly to ST when the user pairs or shares. There is no separate WeSync pairing store to keep in sync — the wire only accelerates the signalling.
2. **Pairing writes directly to Syncthing.** Pairing actions write to each side's ST config via its REST API, so there is no "WeSync DB ahead of ST until a sync" gap.
3. **The wire is always outbound, inbound is additive.** Every node maintains an outbound WS connection to every peer it discovers, and also accepts inbound connections at `/peer/ws`. Two peers may hold two sockets to each other; signals flow over whichever socket the sender initiated.
4. **Separate `--db`/`--port` per instance for same-machine testing.** `dev.ps1` runs multiple instances with distinct DB paths and ports.
