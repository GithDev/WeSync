/**
 * E2E: Device removal cleanup
 *
 * Tests that removing a trusted device properly cleans up:
 *   1. Folder membership on both sides
 *   2. No stale pending folder invites after reconnect
 *   3. Clean state after double-removal (both sides remove each other)
 *
 * This test was created to reproduce:
 *   - Stale device IDs remaining in shared folders after removal
 *   - Unknown pending folder UUIDs appearing when reconnecting
 *
 * Requires dev.ps1 to be running.
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import {
  DEVICE_A,
  DEVICE_B,
  DEVICE_C,
  getStatus,
  getDevices,
  getFolders,
  getPeers,
  getPendingFolders,
  pair,
  shareFolder,
  acceptFolder,
  waitForDevice,
  waitForPeer,
  waitForFolderDevice,
  waitForDeviceGoneFromFolder,
  waitForFolderGone,
  restartAllSyncthing,
  cleanAll,
} from './helpers';
import { StateMonitor } from './state-monitor';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');

async function setupTrustedWithFolder(
  pageA: import('@playwright/test').Page,
  pageB: import('@playwright/test').Page,
  idA: string,
  idB: string,
): Promise<string> {
  // Trust Aâ†”B â€” poll until both sides see each other (handles one-sided Accepted delivery failures)
  await expect
    .poll(
      async () => {
        const [dA, dB] = await Promise.all([
          getDevices(pageA, DEVICE_A),
          getDevices(pageB, DEVICE_B),
        ]);
        if (
          dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
          dB.some((x: { deviceID: string }) => x.deviceID === idA)
        )
          return true;
        await pair(pageA, DEVICE_A, idB, 'B');
        await pair(pageB, DEVICE_B, idA, 'A');
        return false;
      },
      { timeout: 60_000, intervals: [3000] },
    )
    .toBe(true);

  // A shares folder with B
  await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'Cleanup Test', 'sendreceive', idB);
  const folders = await getFolders(pageA, DEVICE_A);
  const folderID = folders.find((f: { label: string }) => f.label === 'Cleanup Test')?.id ?? '';
  expect(folderID).not.toBe('');

  await expect
    .poll(
      async () => {
        const pending = await getPendingFolders(pageB, DEVICE_B);
        return pending.some((p: { folderID: string }) => p.folderID === folderID);
      },
      { timeout: 60_000, intervals: [2000] },
    )
    .toBe(true);

  await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
  await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);
  await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);

  return folderID;
}

test.describe.serial('Device removal cleanup', () => {
  let idA: string, idB: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let monitor: StateMonitor;

  test.beforeAll(async ({ browser }) => {
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B)]);

    const [sA, sB] = await Promise.all([getStatus(pageA, DEVICE_A), getStatus(pageB, DEVICE_B)]);
    idA = sA.myID;
    idB = sB.myID;

    // Full cleanup
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
    ]);

    monitor = new StateMonitor([
      [pageA, DEVICE_A, 'A'],
      [pageB, DEVICE_B, 'B'],
    ]);

    await waitForPeer(pageA, DEVICE_A, idB);
    await waitForPeer(pageB, DEVICE_B, idA);

    const waitForHello = async (
      page: import('@playwright/test').Page,
      base: string,
      targetID: string,
    ) => {
      await expect
        .poll(
          async () => {
            const peers = await getPeers(page, base);
            return peers.some(
              (p: { deviceID: string; name: string }) => p.deviceID === targetID && p.name !== '',
            );
          },
          { timeout: 20_000, intervals: [500] },
        )
        .toBe(true);
    };
    await Promise.all([waitForHello(pageA, DEVICE_A, idB), waitForHello(pageB, DEVICE_B, idA)]);
    await new Promise((r) => setTimeout(r, 2000));
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
  });

  // â”€â”€ Test 1: Folder membership cleans up on both sides after removal â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

  test('1. A removes B â€” folder membership cleans up on both sides', async () => {
    monitor.start();
    const folderID = await setupTrustedWithFolder(pageA, pageB, idA, idB);

    // A removes B
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);

    // A: B gone from device list
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          return !d.some((x: { deviceID: string }) => x.deviceID === idB);
        },
        { message: 'A: B removed from devices', timeout: 30_000 },
      )
      .toBe(true);

    // A: B gone from folder
    await waitForDeviceGoneFromFolder(pageA, DEVICE_A, folderID, idB, 30_000);

    // B: A gone from device list
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageB, DEVICE_B);
          return !d.some((x: { deviceID: string }) => x.deviceID === idA);
        },
        { message: 'B: A removed from devices', timeout: 30_000 },
      )
      .toBe(true);

    // B: A gone from folder (or folder gone entirely)
    await expect
      .poll(
        async () => {
          const folders = await getFolders(pageB, DEVICE_B);
          const folder = folders.find((f: { id: string }) => f.id === folderID);
          if (!folder) return true; // folder gone entirely is also fine
          return !folder.deviceIDs?.includes(idA);
        },
        { message: 'B: folder cleaned up', timeout: 30_000 },
      )
      .toBe(true);

    console.log('âœ” Folder membership cleaned up on both sides after removal');
    monitor.stop();
  });

  // â”€â”€ Test 2: No stale pending folders after remove + reconnect â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

  test('2. No stale pending folders after reconnect', async () => {
    monitor.start();
    // Clean up from previous test
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
    ]);

    // Trust Aâ†”B again and share a folder
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (
            dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
            dB.some((x: { deviceID: string }) => x.deviceID === idA)
          )
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    await shareFolder(pageA, DEVICE_A, FOLDER_B_PATH, 'Reconnect Test', 'sendreceive', idB);
    const folders = await getFolders(pageA, DEVICE_A);
    const folderID = folders.find((f: { label: string }) => f.label === 'Reconnect Test')?.id ?? '';

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageB, DEVICE_B);
          return p.some((pf: { folderID: string }) => pf.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);

    // Now remove B
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          return !d.some((x: { deviceID: string }) => x.deviceID === idB);
        },
        { timeout: 30_000 },
      )
      .toBe(true);

    // Wait for cleanup to propagate
    await new Promise((r) => setTimeout(r, 1000));
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
    ]);
    await new Promise((r) => setTimeout(r, 8000)); // let cleanup goroutines settle

    // Trust each other again
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (
            dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
            dB.some((x: { deviceID: string }) => x.deviceID === idA)
          )
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // Critical check: no stale pending folders should appear on either side
    // Wait a moment for any BEP ClusterConfig messages to arrive
    await new Promise((r) => setTimeout(r, 10_000));

    const pendingOnA = await getPendingFolders(pageA, DEVICE_A);
    const pendingOnB = await getPendingFolders(pageB, DEVICE_B);

    expect(pendingOnA).toHaveLength(0);
    expect(pendingOnB).toHaveLength(0);

    console.log('âœ” No stale pending folders after remove + reconnect');
    monitor.stop();
  });

  // â”€â”€ Test 3: Double removal (both sides remove each other) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

  test('3. Both sides remove each other â€” state is consistent', async () => {
    monitor.start();
    // Setup again
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (
            dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
            dB.some((x: { deviceID: string }) => x.deviceID === idA)
          )
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // Both remove each other simultaneously
    await Promise.all([
      pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`),
      pageB.request.delete(`${DEVICE_B}/api/devices?id=${idA}`),
    ]);

    // Both should end up with no devices
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          return dA.length === 0 && dB.length === 0;
        },
        { message: 'both device lists empty after double removal', timeout: 30_000 },
      )
      .toBe(true);

    // No folders should remain shared between them
    const [fA, fB] = await Promise.all([getFolders(pageA, DEVICE_A), getFolders(pageB, DEVICE_B)]);
    const aHasB = fA.some((f: { deviceIDs?: string[] }) => f.deviceIDs?.includes(idB));
    const bHasA = fB.some((f: { deviceIDs?: string[] }) => f.deviceIDs?.includes(idA));
    expect(aHasB).toBe(false);
    expect(bHasA).toBe(false);

    console.log('âœ” Double removal leaves consistent clean state');
    monitor.stop();
  });

  // â”€â”€ Test 4: Simulated missed Cancelled â€” B still has stale folder â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
  //
  // Reproduces the real bug:
  //   "pending folders with an ID I don't recognize"
  //
  // Scenario: A's WeSync DB is wiped (simulate reinstall / Cancelled not delivered)
  // while B's side still has a shared folder pointing at A.
  // When they reconnect, B's ST announces the folder to A via BEP ClusterConfig.
  // A's WeSync should NOT show it as an unknown pending folder invite.

  test('4. Stale folder on B after A resets â€” no phantom pending invite on A', async () => {
    monitor.start();
    // Clean up any leftover state from previous tests (e.g. FOLDER_B_PATH still in use on B)
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
    ]);

    // Step 1: A and B share a folder
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (
            dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
            dB.some((x: { deviceID: string }) => x.deviceID === idA)
          )
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'Stale Test', 'sendreceive', idB);
    const folders = await getFolders(pageA, DEVICE_A);
    const folderID = folders.find((f: { label: string }) => f.label === 'Stale Test')?.id ?? '';
    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageB, DEVICE_B);
          return p.some((pf: { folderID: string }) => pf.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);

    // Step 2: Simulate "Cancelled never delivered to B" by wiping A's side only.
    // A removes ALL its folders and devices (simulates DB reset / reinstall).
    // B's side is intentionally left untouched â€” it still has the folder with A.
    const foldersOnA = await getFolders(pageA, DEVICE_A);
    for (const f of foldersOnA) {
      await pageA.request.delete(`${DEVICE_A}/api/folder?id=${f.id}`);
    }
    const devicesOnA = await getDevices(pageA, DEVICE_A);
    for (const d of devicesOnA) {
      await pageA.request.delete(`${DEVICE_A}/api/devices?id=${d.deviceID}`);
    }
    // Force A's ST config to match empty DB (simulates full reset)
    await pageA.request.post(`${DEVICE_A}/api/sync`, {});
    await new Promise((r) => setTimeout(r, 1000));

    // Verify A is clean, B still has the stale folder
    const aFolders = await getFolders(pageA, DEVICE_A);
    expect(aFolders).toHaveLength(0);
    const bFolders = await getFolders(pageB, DEVICE_B);
    const bHasStaleFolder = bFolders.some((f: { id: string }) => f.id === folderID);
    expect(bHasStaleFolder).toBe(true); // confirms stale state exists

    // Step 3: Re-trust Aâ†”B so they reconnect
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (
            dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
            dB.some((x: { deviceID: string }) => x.deviceID === idA)
          )
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // Step 4: Wait for BEP ClusterConfig to propagate (B's ST announces stale folder to A)
    await new Promise((r) => setTimeout(r, 20_000));

    // CRITICAL CHECK: A should NOT see phantom pending folders with unknown UUIDs
    // This is the bug: A sees a pending invite for a folder it doesn't recognise
    const pendingOnA = await getPendingFolders(pageA, DEVICE_A);
    const phantomFolders = pendingOnA.filter(
      (p: { folderID: string; deviceID: string }) => p.deviceID === idB,
    );
    expect(phantomFolders).toHaveLength(0);

    console.log('âœ” No phantom pending folder invite after stale state + reconnect');
    monitor.stop();
  });

  // â”€â”€ Test 5: MESH “other participants” â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
  //
  // When B adds C to a shared folder (Aâ†”B), A learns about C via FolderMembers.
  // C appears as a folder participant on A's side without being directly trusted.
  // A can explicitly remove C from the folder.

  test('5. ST Introducer: B sharing folder with C auto-trusts C on A', async ({ browser }) => {
    monitor.start();
    const pageC = await browser.newPage();
    await pageC.goto(DEVICE_C);
    const sC = await getStatus(pageC, DEVICE_C);
    const idC = sC.myID;

    // Clean all three
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);
    // Restart ST to clear blocked-introduction tracking from previous test runs
    await restartAllSyncthing(pageA);

    // Pair A<->B and B<->C (both with Introducer=true)
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (
            dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
            dB.some((x: { deviceID: string }) => x.deviceID === idA)
          )
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    await expect
      .poll(
        async () => {
          const [dB, dC] = await Promise.all([
            getDevices(pageB, DEVICE_B),
            getDevices(pageC, DEVICE_C),
          ]);
          if (
            dB.some((x: { deviceID: string }) => x.deviceID === idC) &&
            dC.some((x: { deviceID: string }) => x.deviceID === idB)
          )
            return true;
          await pair(pageB, DEVICE_B, idC, 'C');
          await pair(pageC, DEVICE_C, idB, 'B');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // A shares folder with B; B then adds C
    await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'Ghost Test', 'sendreceive', idB);
    const folders = await getFolders(pageA, DEVICE_A);
    const folderID = folders.find((f: { label: string }) => f.label === 'Ghost Test')?.id ?? '';
    expect(folderID).not.toBe('');

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageB, DEVICE_B);
          return p.some((pf: { folderID: string }) => pf.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);

    // B adds C to the folder -- BEP Introducer propagates C to A as trusted device
    const bFolders = await getFolders(pageB, DEVICE_B);
    const bFolder = bFolders.find((f: { id: string }) => f.id === folderID)!;
    await shareFolder(pageB, DEVICE_B, bFolder.path, bFolder.label, bFolder.type, idC);

    // A discovers C as a TRUSTED device via BEP Introducer (not just a participant)
    await expect
      .poll(
        async () => {
          const devices = await getDevices(pageA, DEVICE_A);
          return devices.some((d: { deviceID: string }) => d.deviceID === idC);
        },
        {
          message: 'waiting for C to appear as trusted device on A via Introducer',
          timeout: 60_000,
          intervals: [1000],
        },
      )
      .toBe(true);

    // A can explicitly remove C from the folder
    await pageA.request.delete(
      `${DEVICE_A}/api/folder/device?folderID=${folderID}&deviceID=${idC}`,
    );
    await waitForDeviceGoneFromFolder(pageA, DEVICE_A, folderID, idC);

    await pageC.close();
    console.log('ST Introducer auto-trusted C on A; folder removal works correctly');
    monitor.stop();
  });
});
