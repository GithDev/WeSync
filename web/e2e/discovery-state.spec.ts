/**
 * E2E: Discovery and trust state machine
 *
 * Validates correct state across all transitions in the peer discovery lifecycle:
 *
 *   discovered (UDP)
 *     → paired (explicit)
 *     → untrusted (removed)
 *     → rediscovered (back to peers)
 *     → re-paired
 *
 * State invariants under test:
 *   PRE-PAIR:    device in /api/peers, NOT in /api/devices
 *   POST-PAIR:   device in /api/devices (stPaired), also in /api/peers (address)
 *   POST-UNTRUST: device NOT in /api/devices, back in /api/peers (from wire addr)
 *   DISC OFF:    untrusted peers hidden (/api/peers empty of them); trusted kept
 *   DISC ON:     untrusted peers reappear via UDP
 *   RESTART:     trustedIDs reload from ST; /api/peers is empty until wire reconnects
 */

import { test, expect } from '@playwright/test';
import { spawn, execSync } from 'child_process';
import * as net from 'net';
import path from 'path';
import fs from 'fs';
import { fileURLToPath } from 'url';
import {
  DEVICE_A,
  DEVICE_B,
  DEVICE_C,
  getStatus,
  getDevices,
  getPeers,
  getFolders,
  pair,
  waitForDevice,
  waitForPeer,
  cleanAll,
  apiPort,
  peerPort,
  stHome,
} from './helpers';

// ── Paths ─────────────────────────────────────────────────────────────────────

// Repo root = two levels up from this spec (web/e2e/ → web/ → repo). Anchored
// to the spec's own location (ESM-safe, no __dirname) so it's correct regardless
// of the process cwd Playwright runs in.
const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(HERE, '..', '..');
const TESTDATA = path.join(ROOT, 'testdata');
const WESYNC_EXE = path.join(ROOT, 'wesync.exe');

function readSTKey(p: string): string {
  try {
    return fs.readFileSync(p, 'utf8').match(/<apikey>([^<]+)<\/apikey>/)?.[1] ?? '';
  } catch {
    return '';
  }
}
// Read the test ST1 instance's key (testdata/syncthing1-home). No fallback to a
// personal Syncthing — that would make the restart test run against the wrong
// instance instead of skipping cleanly.
const ST1_KEY = readSTKey(path.join(TESTDATA, 'syncthing1-home/config.xml'));

// ── Process helpers (WeSync restart) ─────────────────────────────────────────

async function waitForPort(port: number, timeoutMs = 20_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const ok = await new Promise<boolean>((resolve) => {
      const s = net.createConnection(port, '127.0.0.1');
      s.setTimeout(400);
      s.on('connect', () => {
        s.destroy();
        resolve(true);
      });
      s.on('error', () => {
        s.destroy();
        resolve(false);
      });
      s.on('timeout', () => {
        s.destroy();
        resolve(false);
      });
    });
    if (ok) return;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`Port ${port} not open after ${timeoutMs}ms`);
}

function killByPort(port: number): void {
  try {
    // netstat without shell — parse in JS
    const out = execSync('netstat -ano', { stdio: 'pipe', timeout: 3000 }).toString();
    for (const line of out.split('\n')) {
      if (!line.includes(`:${port} `) && !line.includes(`:${port}\t`)) continue;
      if (!line.toUpperCase().includes('LISTENING')) continue;
      const pid = line.trim().split(/\s+/).pop();
      if (pid && /^\d+$/.test(pid)) {
        try {
          execSync(`taskkill /F /PID ${pid}`, { stdio: 'pipe' });
        } catch {}
      }
    }
  } catch {}
}

function readSTKey2(p: string): string {
  try {
    return fs.readFileSync(p, 'utf8').match(/<apikey>([^<]+)<\/apikey>/)?.[1] ?? '';
  } catch {
    return '';
  }
}
const ST2_KEY = readSTKey2(path.join(TESTDATA, 'syncthing2-home/config.xml'));

async function restartWeSyncA(): Promise<void> {
  killByPort(apiPort(0));
  await new Promise((r) => setTimeout(r, 800));
  spawn(
    WESYNC_EXE,
    [
      '--syncthing-url=http://127.0.0.1:8386',
      `--syncthing-key=${ST1_KEY}`,
      `--syncthing-home=${stHome(0)}`,
      `--port=${apiPort(0)}`,
      `--peer-port=${peerPort(0)}`,
      `--db=${path.join(TESTDATA, 'wesync1.db')}`,
      '--debug',
    ],
    { detached: true, stdio: 'ignore', windowsHide: true },
  ).unref();
  await waitForPort(apiPort(0));
  await new Promise((r) => setTimeout(r, 500));
}

async function restartWeSyncB(): Promise<void> {
  killByPort(apiPort(1));
  await new Promise((r) => setTimeout(r, 800));
  spawn(
    WESYNC_EXE,
    [
      '--syncthing-url=http://127.0.0.1:8387',
      `--syncthing-key=${ST2_KEY}`,
      `--syncthing-home=${stHome(1)}`,
      `--port=${apiPort(1)}`,
      `--peer-port=${peerPort(1)}`,
      `--db=${path.join(TESTDATA, 'wesync2.db')}`,
      '--debug',
    ],
    { detached: true, stdio: 'ignore', windowsHide: true },
  ).unref();
  await waitForPort(apiPort(1));
  await new Promise((r) => setTimeout(r, 500));
}

// ── API helpers ───────────────────────────────────────────────────────────────

async function setDiscovery(
  page: import('@playwright/test').Page,
  base: string,
  enabled: boolean,
): Promise<void> {
  await page.request.fetch(`${base}/api/mode`, {
    method: 'PUT',
    data: { visible: enabled },
  });
}

async function getDiscovery(page: import('@playwright/test').Page, base: string): Promise<boolean> {
  const res = await page.request.get(`${base}/api/mode`);
  const body = await res.json();
  return body.visible as boolean;
}

// ── Suite ─────────────────────────────────────────────────────────────────────

test.describe.serial('Discovery and trust state machine', () => {
  let idA: string, idB: string, idC: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let pageC: import('@playwright/test').Page;

  test.beforeAll(async ({ browser }) => {
    test.setTimeout(30_000);
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    pageC = await browser.newPage();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B), pageC.goto(DEVICE_C)]);
    const [sA, sB, sC] = await Promise.all([
      getStatus(pageA, DEVICE_A),
      getStatus(pageB, DEVICE_B),
      getStatus(pageC, DEVICE_C),
    ]);
    idA = sA.myID;
    idB = sB.myID;
    idC = sC.myID;
    console.log(`IDs: A=${idA.slice(0, 7)} B=${idB.slice(0, 7)} C=${idC.slice(0, 7)}`);
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  // ── S1: Before pairing, UDP-discovered device is in peers, NOT in devices ──

  test('S1: pre-pairing — device visible in peers but not devices', async () => {
    test.setTimeout(60_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Wait for UDP discovery: A finds B in /api/peers
    await waitForPeer(pageA, DEVICE_A, idB);
    await waitForPeer(pageB, DEVICE_B, idA);

    // Both should see each other in peers
    const peersA = await getPeers(pageA, DEVICE_A);
    const peersB = await getPeers(pageB, DEVICE_B);
    expect(peersA.some((p: any) => p.deviceID === idB)).toBe(true);
    expect(peersB.some((p: any) => p.deviceID === idA)).toBe(true);

    // But NOT in devices (not yet trusted)
    const devicesA = await getDevices(pageA, DEVICE_A);
    const devicesB = await getDevices(pageB, DEVICE_B);
    expect(devicesA.some((d: any) => d.deviceID === idB)).toBe(false);
    expect(devicesB.some((d: any) => d.deviceID === idA)).toBe(false);

    console.log('S1 ✓ — pre-pairing: in peers, not in devices');
  });

  // ── S2: After pairing, device in devices (trusted) ────────────────────────

  test('S2: post-pairing — device in devices with stPaired=true', async () => {
    test.setTimeout(60_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Pair A↔B
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    // Both must be in /api/devices
    const devicesA = await getDevices(pageA, DEVICE_A);
    const devicesB = await getDevices(pageB, DEVICE_B);
    expect(devicesA.some((d: any) => d.deviceID === idB)).toBe(true);
    expect(devicesB.some((d: any) => d.deviceID === idA)).toBe(true);

    // stPaired must be true (returned from /api/devices, which only has trustedIDs)
    const bOnA = devicesA.find((d: any) => d.deviceID === idB);
    expect(bOnA?.stPaired ?? false).toBe(true);

    console.log('S2 ✓ — post-pairing: in devices with stPaired=true');
  });

  // ── S3: After untrust, device gone from devices, back in peers ────────────

  test('S3: untrust — device leaves devices, returns to peers', async () => {
    test.setTimeout(90_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Pair A↔B
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    // A removes B
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);

    // A: B gone from devices
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          return !d.some((x: any) => x.deviceID === idB);
        },
        { message: 'A: B gone from devices after untrust', timeout: 30_000 },
      )
      .toBe(true);

    // B: A gone from devices (received Cancelled)
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageB, DEVICE_B);
          return !d.some((x: any) => x.deviceID === idA);
        },
        { message: 'B: A gone from devices after Cancelled', timeout: 30_000 },
      )
      .toBe(true);

    // B should reappear in A's peers (re-added from wire address in onCancelled)
    await expect
      .poll(
        async () => {
          const peers = await getPeers(pageA, DEVICE_A);
          return peers.some((p: any) => p.deviceID === idB);
        },
        { message: 'A: B back in peers after untrust', timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    console.log('S3 ✓ — untrust: gone from devices, back in peers');
  });

  // ── S4: visible=false — A goes silent (no announce, no new discovery) ────────
  //
  // Single discovery concept: "visible" drives announce AND listen together.
  // visible=false → A stops announcing itself AND stops forwarding new peer
  // discoveries. Peers A already knows persist (lastSeen keeps refreshing from
  // inbound packets, and wire holds their address), so A doesn't *lose* known
  // peers — it just won't pick up a brand-new one while silent.

  test('S4: visible=false — A goes silent; already-known peers persist', async () => {
    test.setTimeout(60_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Confirm initial visible state, and that A has already discovered B while
    // both were visible (the baseline cleanAll leaves them announcing).
    expect(await getDiscovery(pageA, DEVICE_A)).toBe(true);
    await waitForPeer(pageA, DEVICE_A, idB);

    // A goes silent: stops announcing AND stops scanning for new peers.
    await setDiscovery(pageA, DEVICE_A, false);
    expect(await getDiscovery(pageA, DEVICE_A)).toBe(false);
    console.log('  ✓ A.visible=false confirmed via GET /api/mode');

    // A keeps the peer it already knew (B): going silent doesn't drop known
    // peers, it only stops announcing + new discovery.
    const peersA = await getPeers(pageA, DEVICE_A);
    expect(peersA.some((p: any) => p.deviceID === idB)).toBe(true);
    console.log('  ✓ A (visible=false) keeps already-known B; only announce + new-discovery stop');

    console.log('S4 ✓ — visible=false: A silent on the network, known peers retained');
  });

  // ── S5: visible toggle controls discovery (announce + listen) state ─────────
  //
  // Note: "visible=false" stops UDP announce + new discovery, but does not close
  // existing wire connections — wire is governed separately by UI-active state,
  // not by visibility. A device that already has a wire connection to A keeps A
  // in its peers via wire Hello. So a known peer is retained even when silent
  // (see S4); only brand-new discovery stops.

  test('S5: visible toggle confirmed by mode API; transitions announce state', async () => {
    test.setTimeout(60_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Start: A is visible
    expect(await getDiscovery(pageA, DEVICE_A)).toBe(true);

    // B discovers A via UDP (A is announcing)
    await waitForPeer(pageB, DEVICE_B, idA);
    console.log('  ✓ B discovers A while A is visible=true (announcing)');

    // A goes invisible: stop announcing
    await setDiscovery(pageA, DEVICE_A, false);
    expect(await getDiscovery(pageA, DEVICE_A)).toBe(false);
    console.log('  ✓ A.visible=false — announcements stopped');

    // A goes visible again
    await setDiscovery(pageA, DEVICE_A, true);
    expect(await getDiscovery(pageA, DEVICE_A)).toBe(true);
    console.log('  ✓ A.visible=true — announcing again');

    // A still discovers B (both announce, both listen)
    await waitForPeer(pageA, DEVICE_A, idB);

    console.log('S5 ✓ — visible toggle correctly controls announcing state');
  });

  // ── S6: Trusted device unaffected by visible toggle ───────────────────────

  test('S6: trusted device unaffected by visible OFF/ON cycle', async () => {
    test.setTimeout(90_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Pair A↔B
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    // Disable visible (stop announcing)
    await setDiscovery(pageA, DEVICE_A, false);
    expect(await getDiscovery(pageA, DEVICE_A)).toBe(false);

    // Trusted B still in devices — visible toggle doesn't affect trust
    const devicesOff = await getDevices(pageA, DEVICE_A);
    expect(devicesOff.some((d: any) => d.deviceID === idB)).toBe(true);

    // Trusted B still in peers — wire connection unaffected
    const peersOff = await getPeers(pageA, DEVICE_A);
    expect(peersOff.some((p: any) => p.deviceID === idB)).toBe(true);

    // Re-enable
    await setDiscovery(pageA, DEVICE_A, true);
    const devicesOn = await getDevices(pageA, DEVICE_A);
    expect(devicesOn.some((d: any) => d.deviceID === idB)).toBe(true);

    console.log('S6 ✓ — trusted device unaffected by visible OFF/ON');
  });

  // ── S7: Untrust + visible state — correct semantics ───────────────────────
  //
  // After untrust: B returns to peers (wire address from onCancelled).
  // visible=false does NOT remove an already-known B from A's peers — it only
  // stops A announcing + scanning for new peers. B with fresh state won't find A.

  test('S7: untrust + visible toggle — correct state at each step', async () => {
    test.setTimeout(90_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Pair then untrust
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          return !d.some((x: any) => x.deviceID === idB);
        },
        { timeout: 30_000 },
      )
      .toBe(true);

    // B returns to peers after onCancelled
    await expect
      .poll(
        async () => {
          const peers = await getPeers(pageA, DEVICE_A);
          return peers.some((p: any) => p.deviceID === idB);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);
    console.log('  ✓ B back in peers after untrust');

    // visible=false: A goes silent. B is already known (we have B's address from
    // onCancelled), so going silent doesn't drop B — it only stops announce +
    // new discovery. No disconnect of existing state.
    await setDiscovery(pageA, DEVICE_A, false);
    await new Promise((r) => setTimeout(r, 1000));
    const peersAfterOff = await getPeers(pageA, DEVICE_A);
    expect(peersAfterOff.some((p: any) => p.deviceID === idB)).toBe(true);
    console.log('  ✓ B still in A peers after visible=false (already known — not dropped)');

    // visible=true: A announces again
    await setDiscovery(pageA, DEVICE_A, true);
    expect(await getDiscovery(pageA, DEVICE_A)).toBe(true);
    console.log('  ✓ A visible again after visible=true');

    console.log('S7 ✓ — untrust + visible: trust state and announce state are independent');
  });

  // ── S8: WeSync restart — trustedIDs persist from ST, untrusted peers ephemeral ─

  test('S8: restart — trusted device persists; untrusted peer invisible until UDP rediscovers', async () => {
    test.setTimeout(120_000);
    if (!ST1_KEY) {
      console.log('  ⚠ No ST1 key — skipping restart test');
      return;
    }

    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    // Pair A↔B only; C remains unpaired
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    // Confirm C is in A's peers (UDP-discovered but not paired)
    await waitForPeer(pageA, DEVICE_A, idC);
    expect((await getDevices(pageA, DEVICE_A)).some((d: any) => d.deviceID === idC)).toBe(false);

    // Restart WeSync A
    console.log('  Restarting WeSync A...');
    await restartWeSyncA();
    await pageA.goto(DEVICE_A);

    // INVARIANT 1: B (trusted) is in devices immediately — loaded from ST
    const devicesAfterRestart = await getDevices(pageA, DEVICE_A);
    expect(devicesAfterRestart.some((d: any) => d.deviceID === idB)).toBe(true);
    console.log('  ✓ B still trusted after restart (from ST config)');

    // INVARIANT 2: C (untrusted in THIS test) is NOT in A's trusted devices
    // Note: previous Introducer tests may have introduced C to A's ST. cleanAll
    // removes C from devices, but we verify the key invariant: C is not trusted.
    expect(devicesAfterRestart.some((d: any) => d.deviceID === idC)).toBe(false);
    console.log('  ✓ C not in trusted devices after restart (removed by cleanAll)');

    // INVARIANT 4: B becomes wire-connected (MaintainConnections re-establishes link)
    await expect
      .poll(
        async () => {
          const devs = await getDevices(pageA, DEVICE_A);
          return devs.find((d: any) => d.deviceID === idB)?.connected === true;
        },
        { message: 'B wire-reconnects after restart', timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);
    console.log('  ✓ B wire-reconnected (MaintainConnections works after restart)');

    // INVARIANT 5: C reappears in A's peers once UDP discovery fires again
    await waitForPeer(pageA, DEVICE_A, idC);
    expect((await getPeers(pageA, DEVICE_A)).some((p: any) => p.deviceID === idC)).toBe(true);
    console.log('  ✓ C reappears in peers via UDP rediscovery');

    console.log('S8 ✓ — restart: ST-trust persists, untrusted peers ephemeral, both reconnect');
  });

  // ── S9: C discovered but never paired — stays in peers only ───────────────

  test('S9: unpaired third device — always peers only, never devices', async () => {
    test.setTimeout(90_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    // Pair A↔B only
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    // C is discovered by A via UDP (no pairing)
    await waitForPeer(pageA, DEVICE_A, idC);

    // C must be in A's peers
    const peersA = await getPeers(pageA, DEVICE_A);
    expect(peersA.some((p: any) => p.deviceID === idC)).toBe(true);

    // C must NOT be in A's devices at any point
    const devicesA = await getDevices(pageA, DEVICE_A);
    expect(devicesA.some((d: any) => d.deviceID === idC)).toBe(false);

    // A goes silent (visible=false): stops announcing + new discovery. Already-
    // known peers persist (lastSeen keeps refreshing, wire holds the address), so
    // B and C stay in A's peers. Trust state is NOT changed by the visible toggle.
    await setDiscovery(pageA, DEVICE_A, false);
    await new Promise((r) => setTimeout(r, 1000));

    // Both B (trusted) and C (untrusted) remain in A's peers — already known.
    const peersAfterOff = await getPeers(pageA, DEVICE_A);
    expect(peersAfterOff.some((p: any) => p.deviceID === idB)).toBe(true);
    expect(peersAfterOff.some((p: any) => p.deviceID === idC)).toBe(true);

    // B still trusted; C still not trusted
    const devicesAfterOff = await getDevices(pageA, DEVICE_A);
    expect(devicesAfterOff.some((d: any) => d.deviceID === idB)).toBe(true);
    expect(devicesAfterOff.some((d: any) => d.deviceID === idC)).toBe(false);

    console.log(
      'S9 ✓ — third device: peers only (never trusted); visible=false stays silent, keeps known peers',
    );
  });

  // ── S10: Full cycle — discover → pair → untrust → re-pair ─────────────────

  test('S10: full cycle — discover → pair → untrust → back in peers → re-pair', async () => {
    test.setTimeout(120_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Phase 1: discover (UDP)
    await waitForPeer(pageA, DEVICE_A, idB);
    expect((await getPeers(pageA, DEVICE_A)).some((p: any) => p.deviceID === idB)).toBe(true);
    expect((await getDevices(pageA, DEVICE_A)).some((d: any) => d.deviceID === idB)).toBe(false);
    console.log('  Phase 1 ✓ — discovered: in peers, not in devices');

    // Phase 2: pair
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    expect((await getDevices(pageA, DEVICE_A)).some((d: any) => d.deviceID === idB)).toBe(true);
    console.log('  Phase 2 ✓ — paired: in devices');

    // Phase 3: untrust
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);
    await expect
      .poll(
        async () => {
          return !(await getDevices(pageA, DEVICE_A)).some((d: any) => d.deviceID === idB);
        },
        { timeout: 30_000 },
      )
      .toBe(true);

    // B back in peers after Cancelled/wire address re-add
    await expect
      .poll(
        async () => {
          return (await getPeers(pageA, DEVICE_A)).some((p: any) => p.deviceID === idB);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);
    console.log('  Phase 3 ✓ — untrusted: not in devices, back in peers');

    // Phase 4: re-pair
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    expect((await getDevices(pageA, DEVICE_A)).some((d: any) => d.deviceID === idB)).toBe(true);
    console.log('  Phase 4 ✓ — re-paired: back in devices');

    console.log('S10 ✓ — full cycle: discover → pair → untrust → re-pair');
  });
});
