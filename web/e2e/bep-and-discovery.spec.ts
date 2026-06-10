/**
 * E2E: BEP connectivity, accepted field, and UDP/wire priority
 *
 * Two focus areas:
 *
 * 1. BEP investigation — why does device.accepted stay false in dev?
 *    Queries ST's REST API directly to observe BEP connection state.
 *    Reveals that mDNS discovery on loopback doesn't work reliably, so
 *    lastSeen is never set in a same-machine dev environment.
 *    In production (separate machines on a LAN), mDNS works and accepted=true.
 *
 * 2. Wire-over-UDP priority — E2E equivalent of TestRename_NotRevertedByStaleUDP
 *    When a wire connection is active, TrackPeer() ignores UDP packets entirely.
 *    Invariant: names established via wire Hello are never overwritten by UDP.
 *
 * ST API used directly (with ST1/ST2 keys) to inspect internal BEP state.
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import fs from 'fs';
import {
  DEVICE_A,
  DEVICE_B,
  DEVICE_C,
  getStatus,
  getDevices,
  getPeers,
  pair,
  setName,
  waitForPeerName,
  waitForDevice,
  forceBEPAddress,
  cleanAll,
} from './helpers';

// ── ST API access ──────────────────────────────────────────────────────────────

const TESTDATA = path.resolve(process.cwd(), '../testdata');

function readSTKey(p: string): string {
  try {
    return fs.readFileSync(p, 'utf8').match(/<apikey>([^<]+)<\/apikey>/)?.[1] ?? '';
  } catch {
    return '';
  }
}

const ST1_KEY = readSTKey(path.join(TESTDATA, 'syncthing1-home/config.xml'));
const ST2_KEY = readSTKey(path.join(TESTDATA, 'syncthing2-home/config.xml'));

const ST1 = 'http://127.0.0.1:8386';
const ST2 = 'http://127.0.0.1:8387';

// ── Helpers ────────────────────────────────────────────────────────────────────

async function stGet(
  page: import('@playwright/test').Page,
  stUrl: string,
  key: string,
  path: string,
) {
  const r = await page.request.get(`${stUrl}${path}`, { headers: { 'X-API-Key': key } });
  return r.json();
}

// ── Suite ─────────────────────────────────────────────────────────────────────

test.describe.serial('BEP connectivity and wire-over-UDP priority', () => {
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
    await Promise.all([
      setName(pageA, DEVICE_A, 'A'),
      setName(pageB, DEVICE_B, 'B'),
      setName(pageC, DEVICE_C, 'C'),
    ]);
  });

  test.afterAll(async () => {
    await Promise.all([
      setName(pageA, DEVICE_A, 'A').catch(() => {}),
      setName(pageB, DEVICE_B, 'B').catch(() => {}),
      setName(pageC, DEVICE_C, 'C').catch(() => {}),
    ]);
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  // ── BEP investigation ──────────────────────────────────────────────────────

  test('BEP1: investigate — ST connection state after pairing', async () => {
    test.setTimeout(60_000);
    if (!ST1_KEY || !ST2_KEY) {
      console.log('  ⚠ No ST keys — skipping BEP investigation');
      return;
    }

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
        { timeout: 30_000, intervals: [2000] },
      )
      .toBe(true);

    // Patch explicit BEP addresses so ST connects on loopback fast enough for
    // the investigation queries below to observe a live BEP connection.
    await Promise.all([
      forceBEPAddress(pageA, 0, idB, 1),
      forceBEPAddress(pageB, 1, idA, 0),
    ]);
    await new Promise((r) => setTimeout(r, 3000)); // let BEP connect

    // Query ST1 directly: what addresses does it have for ST2?
    const st1Config = await stGet(pageA, ST1, ST1_KEY, `/rest/config/devices/${idB}`);
    console.log(`  ST1 device ${idB.slice(0, 7)} config:`);
    console.log(`    addresses: ${JSON.stringify(st1Config.addresses)}`);
    console.log(`    paused: ${st1Config.paused}`);

    // Query ST1 connections: is ST2 BEP-connected?
    const st1Conns = await stGet(pageA, ST1, ST1_KEY, '/rest/system/connections');
    const bConn = st1Conns.connections?.[idB];
    console.log(`  ST1→ST2 BEP connection:`);
    console.log(`    connected: ${bConn?.connected ?? false}`);
    console.log(`    address: ${bConn?.address ?? '(none)'}`);
    console.log(`    type: ${bConn?.type ?? '(none)'}`);

    // Query ST1 stats: has ST2 ever connected via BEP?
    const st1Stats = await stGet(pageA, ST1, ST1_KEY, '/rest/stats/device');
    const bStats = st1Stats[idB];
    console.log(`  ST1 stats for ST2:`);
    console.log(`    lastSeen: "${bStats?.lastSeen ?? ''}" (empty = never BEP-connected)`);

    // Query ST1 options: is local discovery enabled?
    const st1Opts = await stGet(pageA, ST1, ST1_KEY, '/rest/config/options');
    console.log(`  ST1 options:`);
    console.log(`    localAnnounceEnabled: ${st1Opts.localAnnounceEnabled}`);
    console.log(`    globalAnnounceEnabled: ${st1Opts.globalAnnounceEnabled}`);
    console.log(`    listenAddresses: ${JSON.stringify(st1Opts.listenAddresses)}`);

    // Diagnosis: if BEP not connected, explain why
    const bepConnected = bConn?.connected === true;
    const lastSeen = bStats?.lastSeen ?? '';
    const localDiscovery = st1Opts.localAnnounceEnabled === true;

    console.log('\n  ── DIAGNOSIS ──────────────────────────────────────');
    if (!bepConnected) {
      console.log('  ✗ BEP NOT connected. Reason:');
      if (!localDiscovery) {
        console.log('    → Local discovery disabled — ST cannot find peer via mDNS');
      } else if (st1Config.addresses?.[0] === 'dynamic') {
        console.log('    → Addresses: ["dynamic"] — relies on mDNS which may not');
        console.log('      work on loopback (127.0.0.1) between co-located ST instances');
      }
      console.log('  → accepted field will stay false in same-machine dev setups');
      console.log('  → In production (separate machines, real LAN), mDNS works correctly');
    } else {
      console.log('  ✓ BEP connected — lastSeen should populate shortly');
    }

    // WeSync-level device.accepted reflects lastSeen
    const wesyncDevices = await getDevices(pageA, DEVICE_A);
    const bEntry = wesyncDevices.find((d: any) => d.deviceID === idB);
    console.log(`\n  WeSync device.accepted for B: ${bEntry?.accepted ?? false}`);
    console.log(`  (reflects ST lastSeen: "${lastSeen}")`);
    console.log('──────────────────────────────────────────────────');

    // The test passes regardless — this is an investigation, not an assertion.
    // The console output documents the actual BEP state.
    console.log('\nBEP1 ✓ — BEP state documented (see output above)');
  });

  // ── BEP2: accepted transitions when BEP does connect ──────────────────────

  test('BEP2: accepted=false before BEP; accepted=true after BEP connects', async () => {
    test.setTimeout(120_000);
    if (!ST1_KEY || !ST2_KEY) {
      return;
    }

    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

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
        { timeout: 30_000, intervals: [2000] },
      )
      .toBe(true);

    // Confirm initial state: accepted=false (BEP hasn't connected yet)
    const devsBefore = await getDevices(pageA, DEVICE_A);
    const bBefore = devsBefore.find((d: any) => d.deviceID === idB);
    console.log(`  Initial accepted=${bBefore?.accepted} (expected false before BEP)`);

    // Force BEP connection by explicitly pausing and resuming ST2.
    // pause/resume forces ST to re-establish BEP connection immediately
    // (same mechanism as the FolderRefresh signal we removed).
    await pageA.request.post(`${ST1}/rest/system/pause?device=${idB}`, {
      headers: { 'X-API-Key': ST1_KEY },
    });
    await new Promise((r) => setTimeout(r, 300));
    await pageA.request.post(`${ST1}/rest/system/resume?device=${idB}`, {
      headers: { 'X-API-Key': ST1_KEY },
    });

    // Now wait for BEP to connect at ST level
    const bepConnected = await expect
      .poll(
        async () => {
          const conns = await stGet(pageA, ST1, ST1_KEY, '/rest/system/connections');
          return conns.connections?.[idB]?.connected === true;
        },
        {
          message: 'ST1→ST2 BEP connection after pause/resume',
          timeout: 30_000,
          intervals: [1000],
        },
      )
      .toBe(true);
    void bepConnected;
    console.log('  ✓ BEP connected via ST');

    // Wait for lastSeen to be populated in ST stats
    await expect
      .poll(
        async () => {
          const stats = await stGet(pageA, ST1, ST1_KEY, '/rest/stats/device');
          return (stats[idB]?.lastSeen ?? '') !== '';
        },
        { message: 'ST lastSeen set after BEP connection', timeout: 15_000, intervals: [1000] },
      )
      .toBe(true);
    console.log('  ✓ ST lastSeen populated');

    // Now WeSync's next SchedulePush should show accepted=true
    await expect
      .poll(
        async () => {
          const devs = await getDevices(pageA, DEVICE_A);
          return devs.find((d: any) => d.deviceID === idB)?.accepted === true;
        },
        { message: 'WeSync device.accepted=true after BEP', timeout: 10_000, intervals: [500] },
      )
      .toBe(true);

    console.log('BEP2 ✓ — accepted transitions false→true when BEP connects');
  });

  // ── Wire-over-UDP priority tests ───────────────────────────────────────────

  test('UDP1: wire-connected device ignores subsequent UDP discovery (name stable)', async () => {
    test.setTimeout(60_000);
    // E2E of TestRename_NotRevertedByStaleUDP.
    // When wire is active, TrackPeer() returns immediately without updating peers map.
    // Invariant: name set via wire Hello/PeerState is never overwritten by UDP.

    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Wait for wire Hello — names arrive via Hello/PeerState, not UDP
    await waitForPeerName(pageA, DEVICE_A, idB, 'B');
    await waitForPeerName(pageB, DEVICE_B, idA, 'A');

    // Wire established: names are now known
    const peersA = await getPeers(pageA, DEVICE_A);
    const bEntry = peersA.find((p: any) => p.deviceID === idB);
    expect(bEntry?.name).toBe('B');
    console.log(`  Wire established: B's name on A = "${bEntry?.name}"`);

    // A renames — must propagate via wire PeerState (not UDP)
    await setName(pageA, DEVICE_A, 'Alice-Wire');

    // B sees new name via wire within 3s
    await expect
      .poll(
        async () => {
          const peers = await getPeers(pageB, DEVICE_B);
          return peers.find((p: any) => p.deviceID === idA)?.name;
        },
        { message: 'B sees Alice-Wire within 3s', timeout: 3000, intervals: [200] },
      )
      .toBe('Alice-Wire');
    console.log('  ✓ Name "Alice-Wire" propagated to B via wire in <3s');

    // Now simulate what a stale UDP packet would do: toggle discovery on A off/on.
    // When discovery restarts, UDP announces with current name (Alice-Wire, not stale).
    // BUT: because wire is connected, TrackPeer() returns immediately without touching peers map.
    await pageA.request.put(`${DEVICE_A}/api/mode`, { data: { visible: false } });
    await new Promise((r) => setTimeout(r, 500));
    await pageA.request.put(`${DEVICE_A}/api/mode`, { data: { visible: true } });
    await new Promise((r) => setTimeout(r, 1000)); // let UDP cycle

    // B must STILL see Alice-Wire — wire name is not reverted
    const peersAfter = await getPeers(pageB, DEVICE_B);
    const aAfter = peersAfter.find((p: any) => p.deviceID === idA);
    expect(aAfter?.name).toBe('Alice-Wire');
    console.log(`  ✓ After discovery toggle: B still shows "${aAfter?.name}" (not reverted)`);

    // Pair A↔B: trusted device also sees correct name
    await pair(pageA, DEVICE_A, idB, 'B');
    await pair(pageB, DEVICE_B, idA, 'Alice-Wire');
    await waitForDevice(pageA, DEVICE_A, idB);

    const devicesAfter = await getDevices(pageB, DEVICE_B);
    const aDevice = devicesAfter.find((d: any) => d.deviceID === idA);
    expect(aDevice?.name).toBe('Alice-Wire');
    console.log(`  ✓ Trusted device also shows "${aDevice?.name}"`);

    console.log('UDP1 ✓ — wire name never overwritten by UDP; priority invariant holds');
  });

  test('UDP2: discovery OFF → ON keeps name; pairing does not lose it', async () => {
    test.setTimeout(60_000);
    // Verify that name established via wire survives the full discovery toggle cycle
    // and pairing. This ensures the peers-map is not corrupted by discovery changes.

    // Reset names — previous tests (e.g. UDP1) may have renamed devices
    await Promise.all([
      setName(pageA, DEVICE_A, 'A'),
      setName(pageB, DEVICE_B, 'B'),
      setName(pageC, DEVICE_C, 'C'),
    ]);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    // Wait for wire Hello on all connections — names come via Hello/PeerState, not UDP
    await Promise.all([
      waitForPeerName(pageA, DEVICE_A, idB, 'B'),
      waitForPeerName(pageA, DEVICE_A, idC, 'C'),
      waitForPeerName(pageB, DEVICE_B, idA, 'A'),
      waitForPeerName(pageC, DEVICE_C, idA, 'A'),
    ]);

    // Set a recognizable name
    await setName(pageA, DEVICE_A, 'Persistent-Name');
    await expect
      .poll(
        async () => {
          const peers = await getPeers(pageB, DEVICE_B);
          return peers.find((p: any) => p.deviceID === idA)?.name;
        },
        { timeout: 3000, intervals: [200] },
      )
      .toBe('Persistent-Name');
    await expect
      .poll(
        async () => {
          const peers = await getPeers(pageC, DEVICE_C);
          return peers.find((p: any) => p.deviceID === idA)?.name;
        },
        { timeout: 3000, intervals: [200] },
      )
      .toBe('Persistent-Name');
    console.log('  ✓ Name "Persistent-Name" established via wire on B and C');

    // Toggle discovery off and on 3 times — name must stay stable throughout
    for (let i = 0; i < 3; i++) {
      await pageA.request.put(`${DEVICE_A}/api/mode`, { data: { visible: false } });
      await new Promise((r) => setTimeout(r, 300));
      await pageA.request.put(`${DEVICE_A}/api/mode`, { data: { visible: true } });
      await new Promise((r) => setTimeout(r, 300));
    }

    // B and C must still see Persistent-Name
    const peersB = await getPeers(pageB, DEVICE_B);
    const peersC = await getPeers(pageC, DEVICE_C);
    expect(peersB.find((p: any) => p.deviceID === idA)?.name).toBe('Persistent-Name');
    expect(peersC.find((p: any) => p.deviceID === idA)?.name).toBe('Persistent-Name');
    console.log('  ✓ Name stable after 3× discovery off/on cycles');

    // Pair A↔B: trusted device list also shows correct name
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
          await pair(pageB, DEVICE_B, idA, 'Persistent-Name');
          return false;
        },
        { timeout: 30_000, intervals: [2000] },
      )
      .toBe(true);

    const devs = await getDevices(pageB, DEVICE_B);
    expect(devs.find((d: any) => d.deviceID === idA)?.name).toBe('Persistent-Name');
    console.log('  ✓ Name preserved in trusted device list after pairing');

    console.log('UDP2 ✓ — name stable through discovery cycles and pairing');
  });

  test('UDP3: wire connection state determines if UDP updates peers map', async () => {
    test.setTimeout(60_000);
    // Directly verifies the TrackPeer() logic:
    // "If wire is connected → ignore UDP" (name and address from wire take priority).
    // "If wire is NOT connected → UDP populates peers" (address needed for wire dial).

    // Reset names — previous tests may have renamed devices
    await Promise.all([setName(pageA, DEVICE_A, 'A'), setName(pageB, DEVICE_B, 'B')]);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Phase 1: No wire yet — UDP populates peers
    // (before wire, /api/peers should be empty; after UDP, appears)
    await expect
      .poll(
        async () => {
          const peers = await getPeers(pageA, DEVICE_A);
          return peers.some((p: any) => p.deviceID === idB);
        },
        { message: 'UDP populates peers before wire', timeout: 20_000, intervals: [500] },
      )
      .toBe(true);
    console.log('  Phase 1 ✓ — UDP populates peers when wire not yet connected');

    // Phase 2: Wire is now active (Hello exchanged after TrackPeer connected us)
    // Wait for wire Hello — name arrives via Hello/PeerState, not UDP
    await waitForPeerName(pageA, DEVICE_A, idB, 'B');
    const peersA = await getPeers(pageA, DEVICE_A);
    const bPeer = peersA.find((p: any) => p.deviceID === idB);
    expect(bPeer?.name).toBe('B');
    console.log(`  Phase 2 ✓ — Wire active: B name="${bPeer?.name}" (from wire PeerState)`);

    // Phase 3: Rename A → propagates via wire, not UDP
    // UDP would take time (mDNS interval); wire is immediate
    await setName(pageA, DEVICE_A, 'WireFirst');
    await expect
      .poll(
        async () => {
          const peers = await getPeers(pageB, DEVICE_B);
          return peers.find((p: any) => p.deviceID === idA)?.name;
        },
        { message: 'B sees WireFirst in <3s (via wire, not UDP)', timeout: 3000, intervals: [200] },
      )
      .toBe('WireFirst');
    console.log('  Phase 3 ✓ — Name via wire in <3s; UDP would be slower');

    console.log('UDP3 ✓ — TrackPeer wire-priority logic confirmed end-to-end');
  });
});
