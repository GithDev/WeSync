# WeSync

**Private peer-to-peer folder sync for all your devices — a friendly wrapper around [Syncthing](https://syncthing.net/).**

WeSync keeps folders in sync across the devices *you* own — phone, laptop, desktop — with no cloud account and no central server. Your files travel directly between your devices over your own network, and no third party ever holds a copy.

The actual syncing is **100% Syncthing**: the same encrypted, peer-to-peer engine that's been proven for over a decade. WeSync never touches your files — it just takes care of *everything around* Syncthing so you don't have to: **installation, autostart, power management on Android, a clean UI, and settings.**

---

## Why WeSync exists

I'd used Syncthing before and loved it. Then I reinstalled my phone — and setting it all back up was fiddly enough that I kept putting it off for six months, leaning on Resilio Sync in the meantime.

Eventually I got tired of the "I'll do it later." So instead of grinding through the setup yet again, I built the wrapper I wished existed. That's WeSync — so getting back up and running takes minutes.

WeSync is for everyone — whether this is your first sync tool or you already know Syncthing well.

---

## What WeSync does for you

Syncthing is powerful, but getting it installed, running on boot, and configured the way you want is the part that stops most people. WeSync handles all of it:

- **Installs and bundles Syncthing** — one installer, nothing else to download or wire up.
- **Starts on boot and stays running** in the background, on every platform.
- **A clean UI** for the handful of things you actually care about — your devices, your folders, your settings — instead of Syncthing's full control panel.
- **Automatic discovery + mutual-consent pairing** — devices find each other on your network; both sides tap "trust," and you're paired. No long device IDs to copy back and forth, and nobody pairs with you without your say-so.
- **One-knob folder sharing** — pick a folder, pick a paired device, done.
- **Power management on Android** — WeSync only wakes and syncs when it makes sense (battery, charging state, trusted Wi-Fi, schedule), so it stays light on your battery instead of running around the clock.
- **Sensible privacy defaults** — it stays on your local network unless you choose to reach further (see below).

---

## How it works

1. **Discovery** — WeSync nodes announce themselves over UDP multicast and find each other on the LAN.
2. **Pairing** — you ask to trust a discovered device; the other side accepts. Both sides write straight into their own Syncthing config, so there's no separate state to drift out of sync.
3. **Sync** — Syncthing does the actual file transfer over its proven Block Exchange Protocol. WeSync orchestrates and shows you a friendly view of what's happening.

Locally, WeSync runs its UI + API on `127.0.0.1:47820` (localhost only, no certificate warnings) and an encrypted, cert-pinned peer channel on `:47821`. Each device is identified by its TLS certificate fingerprint, so there's nothing to exchange out-of-band and nothing to impersonate. For the full design, see [ARCHITECTURE.md](ARCHITECTURE.md).

> **Good to know:** WeSync runs its *own private* Syncthing instance — its own identity, config, and API port (`8385`, chosen to avoid Syncthing's default `8384`). If you already run Syncthing, WeSync lives happily alongside it but does **not** import or manage that setup: it's a separate device with its own paired devices and folders.

---

## Privacy & reach

You decide how far WeSync reaches, in three levels:

- **Local only (default)** — talks only to devices on your own network. No global discovery, no relays, no UPnP. Nothing about you leaves the LAN.
- **Global discovery** — lets devices find each other across the internet via Syncthing's discovery servers (which then learn your IP and device ID).
- **Relay** — routes traffic through public relays when a direct connection isn't possible. Relays see connection metadata only, never your file contents — everything stays end-to-end encrypted.

No account, no sign-up, no telemetry, no phone-home. On Android you can also restrict syncing to trusted Wi-Fi networks and block metered/roaming connections.

---

## Download & install

Grab the latest build for your platform from the [**Releases**](../../releases) page. Every release is built reproducibly in Docker and contains:

| Platform | What you get |
|----------|--------------|
| **Linux** | `.deb` and `.rpm` packages — service + desktop GUI + bundled Syncthing, autostarts at login |
| **Windows** | Installer (`WeSync-<version>-setup.exe`) — service + GUI + bundled Syncthing |
| **Android** | `.apk` |

Prefer to build it yourself? See [Building from source](#building-from-source).

---

## Building from source

All builds run in pinned container images (Docker or podman); the repo is mounted at build time and artifacts land in `dist/<platform>/`. From the repo root:

```sh
make web            # build the React frontend once (every target embeds it)
make linux          # Linux service + GUI + Syncthing
make linux-pkg      # .deb + .rpm
make windows        # Windows installer (cross-compiled)
make android        # Android APK
make all            # everything: .deb/.rpm + Windows installer + APK
make help           # full target list
```

The Android `.aar` (the Go core, via `gomobile bind`) and the web bundle are regenerated on every build, so they aren't committed. Release-signing keys are injected via environment variables / CI secrets and never live in the repo.

---

## Project layout

```
main.go              service entry point
cmd/app/             Wails desktop wrapper (tray, window)
mobile/              gomobile bind surface for Android (embeds the web UI)
web/                 React + Vite frontend
platform/            android/ · windows/ · linux/ packaging
internal/
  backend/           wires everything together + lifecycle
  api/               HTTP API + UI WebSocket + pairing/folder orchestration
  node/              in-memory device/folder/trust state (single source of locking)
  peerwire/          TLS cert-pinned peer channel
  discovery/         UDP multicast announce/listen
  syncthing/         Syncthing REST client (the source of truth)
  stmanager/         owns the embedded Syncthing process
  store/             SQLite: settings + power-event log only
  certid/            device-ID ⇄ TLS cert fingerprint
```

See [ARCHITECTURE.md](ARCHITECTURE.md) and [docs/state-model.md](docs/state-model.md) for the deep dive.

---

## Contributing

Issues and pull requests are welcome. Open an [issue](../../issues) to report a bug or to discuss an idea before sending a large change.

---

## License

WeSync is free and open source under the [**GNU GPLv3**](LICENSE). It embeds and builds on [Syncthing](https://syncthing.net/), which is licensed under the MPL-2.0.
