# State model — device trust + folder relations

What this document captures: the **atomic dimensions** that drive state in
WeSync, and the **derived states** the UI presents. Read this before
touching any code that displays "Invited" / "Connected" / "Offline" / etc.,
or before adding a new state — every scattered string-comparison we have is
because someone re-derived states ad-hoc instead of going through this list.

The principle: **ST is the source of truth**. Wire signals (peerwire
FolderAccept, etc.) accelerate, but ST must work alone — every dimension
below must be read directly from ST's HTTP API.

---

## Level 1 — Device trust (A's view of peer B)

### Atomic dimensions

| Dimension | Source | Meaning |
|---|---|---|
| `stPaired(B)` | `ST.devices` (config) | B is in our ST device config — we trust them |
| `theyTrustUs(B)` | peerwire `Hello.trusted` *(wire-only)* | B's wire Hello declared they trust us back |
| `wireConnected(B)` | peerwire hub | Live peerwire TLS session |
| `bepConnected(B)` | `ST /rest/system/connections` | Live BEP connection in ST |
| `incoming(B)` | `ST /rest/cluster/pending/devices` | B is trying to pair with us — visible in ST pending |
| `udpVisible(B)` | discovery service | UDP announcement received recently |
| `explicitlyRemoved(B)` | in-memory set | We unpaired B recently; suppress until they acknowledge |
| `inFolderMesh(B)` | any `folder.Devices` contains B | B participates in a folder we have |

> `theyTrustUs` is currently wire-only. If wire is down and B is fresh, we
> can't tell whether they trust us — the field stays `false`. This is a known
> gap; trust establishment requires wire today.

### Derived `DeviceTrustState`

Each device collapses to exactly one display state. The order below is the
resolution order — first match wins.

| State | Predicate | UI |
|---|---|---|
| `incoming` | `incoming(B) && !stPaired(B)` | Trust-request card on /devices |
| `waiting` | `stPaired(B) && !theyTrustUs(B)` | "Waiting…" — independent of wire/BEP |
| `connected` | `stPaired(B) && theyTrustUs(B) && wireConnected(B)` | Green dot |
| `offline` | `stPaired(B) && theyTrustUs(B) && !wireConnected(B)` | Grey dot |
| `other` | `!stPaired(B) && inFolderMesh(B) && wireConnected(B)` | "Others" section on folder cards |
| `discoverable` | `udpVisible(B) && !stPaired(B) && !incoming(B)` | Radar bubble on /devices |
| `(absent)` | none of the above | Not shown anywhere |

Notes on the resolution:
- `waiting` is **not** "we have wire to them but they haven't accepted" — it's
  purely about ST trust state. A wire connection during waiting is incidental.
- `other` exists because the ST Introducer mechanism adds devices to folder
  configs without us explicitly trusting them. They appear in folder views
  only, never in /devices.

---

## Level 2 — Folder relation (folder F on A's side, peer B)

### Atomic dimensions

| Dimension | Source | Meaning |
|---|---|---|
| `inDeviceList(F,B)` | `F.Devices` (ST config) | We added B as a folder participant |
| `inRemoteSequence(F,B)` | `ST /rest/db/status?folder=F` → `remoteSequence[B]` key present | **B's ST sent us a cluster config that includes F = B accepted.** Persists across B going offline + folder paused. Empty folder still has key present with value 0. |
| `bepLive(F,B)` | `ST /rest/db/completion?folder=F&device=B` → `remoteState == "valid"` | BEP session active for this folder right now |
| `folderState(F)` | `ST /rest/db/status?folder=F` → `state` | Our local folder state: `idle` / `scanning` / `syncing` / `error` |
| `folderPausedLocally(F)` | `ST.folders[F].paused` (config) | We paused F locally |
| `folderPausedRemotely(F,B)` | `comp.remoteState == "paused"` | B has paused F on their side |
| `wireAccepted(F,B)` | in-memory map *(session-only)*, set on peerwire `FolderAccept` | Display accelerator: a wire `FolderAccept` flips the UI to accepted immediately, before ST's cluster-config propagates. Reset on re-invite, gone on restart. **Acceptance = `inRemoteSequence \|\| wireAccepted`** — wire alone is enough for the live session, but ST (`inRemoteSequence`) is the durable source that must work with wire down and survives restart. |
| `peerNeed(F,B)` | `ST /rest/db/completion?folder=F&device=B` → `needItems/needDeletes/needBytes > 0` | **B still needs data FROM US.** The device-level "has our state actually reached B?" signal — independent of our own `folderState` (which only reflects what WE pull). A sender sits `idle` while serving a download, so without this the relation looked "synced" mid-transfer. Cached by `RefreshFolderCompletion` (per-(F,B) sweep, deduped) and read in `listFolders`. |
| `peerStalled(F,B)` | derived in `RefreshFolderCompletion` | B needs bytes from us but `needBytes` hasn't dropped for a while (`stallAfter`). Only meaningful with `peerNeed`. Surfaces a stuck transfer instead of a fake "syncing". |

> `comp.remoteState` values seen in the wild: `"valid"`, `"unknown"`,
> `"notSharing"`, `"paused"`. Critically, **`notSharing` ≠ accepted** —
> it means a connected device that does NOT have this folder configured.
> Earlier code did `RemoteState != "unknown" && != ""` and got fooled.
> Use `inRemoteSequence` for acceptance, `comp.remoteState` only for live state.

### Derived `FolderRelationState`

**`accepted` = `inRemoteSequence || wireAccepted`** (see the `wireAccepted` note above):
ST's `inRemoteSequence` is the durable source, the wire signal accelerates the live
session. The rows below key off `accepted`.

| State | Predicate | UI |
|---|---|---|
| `not-member` | `!inDeviceList` | B not rendered for F at all |
| `invited` | `inDeviceList && !accepted` | Amber pill, "Invited" badge |
| `accepted-paused-local` | `accepted && folderPausedLocally` | "Paused" — our choice |
| `accepted-paused-remote` | `accepted && folderPausedRemotely` | "Paused by B" |
| `accepted-syncing` | `accepted && bepLive && folderState ∈ {syncing,scanning}` | Spinner / progress — OUR local pull/scan |
| `accepted-sending` | `accepted && bepLive && folderState == idle && peerNeed && !peerStalled` | "Sending — X left" — B is pulling from us |
| `accepted-stalled` | `accepted && bepLive && folderState == idle && peerNeed && peerStalled` | Amber, "Stalled" — needs attention |
| `accepted-idle` | `accepted && bepLive && folderState == idle && !peerNeed` | Green dot, "Up to date" — genuinely caught up |
| `accepted-behind-offline` | `accepted && !bepLive && peerNeed` | "Offline — N items not yet sent" |
| `accepted-offline` | `accepted && !bepLive && !peerNeed` | Grey dot, "In sync as of <time>" |

Key invariants:
- **Acceptance = `inRemoteSequence || wireAccepted`.** `inRemoteSequence` (ST) is the
  durable, restart-surviving, works-with-wire-down source; `wireAccepted` is a
  session-only accelerator (reset on re-invite, gone on restart). Either alone flips
  B from `invited` to accepted; ST must be *able* to confirm alone, but need not be
  the *only* confirmation in a live session. Resolution order is fixed in
  `DeriveFolderRelationState` — first match wins, and BEP liveness only refines
  already-accepted devices.
- **`accepted-idle` ("up to date") requires `!peerNeed`.** The old definition
  omitted this and so claimed "up to date" on a sender the instant it finished
  scanning, while peers were still downloading — the lie this split fixes. Our own
  `folderState` (syncing/scanning) still dominates the row: local work first, then
  the per-peer question.

---

## Bug-class summary (historical)

These bugs all came from mixing dimensions in ad-hoc derivations:

| Bug | Misread | Correct read |
|---|---|---|
| "Invite never arrives" | Assumed ST auto-pushes cluster config on every folder change | Need `PauseDevice + ResumeDevice` to force fresh cluster config |
| "Multi-folder: 2nd folder shows Accepted before B accepted" | `RemoteState != "unknown"` matched `"notSharing"` | Use `inRemoteSequence` map key, not `RemoteState` |
| "B offline → A shows Invited again" | `RemoteState` collapses to `"unknown"` when device disconnects | `inRemoteSequence` persists across disconnect |

---

## How this is realized in code

The atomic dimensions and derived states above live in **`internal/node`** — the
package that owns all in-memory node state (peers, trust sets, folder acceptance)
behind one mutex, with copy-on-read accessors. `internal/api` reads from it and
never holds its own state map. Syncthing stays the source of truth; `node` is the
derived projection plus a few wire-only fast-path signals.

### Level 2 — Folder relation: formalized ✅
- `node.FolderRelationState` (enum) + `node.DeriveFolderRelationState(node.FolderRelationDimensions)`
  — one pure derive function, no other code computes the folder state.
- Exhaustive **table-driven test**: `internal/node/derive_test.go`.
- `listFolders()` (Go) builds the dimensions from ST + node and calls the derive
  function. The TS side mirrors the string values in `web/src/state/folder-display.ts`.

### Level 1 — Device trust: still predicate-derived ⚠️ (remaining work)
There is **no** `DeviceTrustState` enum / `deriveDeviceTrustState` yet. The device
display states (`incoming` / `waiting` / `connected` / `offline` / `discoverable`)
are still derived ad-hoc from `node` predicates — `IsTrusted`, `IsMutuallyTrusted`,
the `theyTrustUs` set, ST `pending/devices`, and live wire/BEP connection — split
across `buildDeviceList()` (Go) and `deriveNetwork()` (TS).

**Next step (when it's worth it):** formalize Level 1 the same way — a
`node.DeviceTrustState` enum + one `node.DeriveDeviceTrustState(dimensions)` with
a table test, then collapse `buildDeviceList()` and `deriveNetwork()` into calls
to it. Mirror the enum strings in TS; UI maps `enum → text/color`, never re-derives.
