/**
 * E2E: Folder mesh flow
 *
 * Requires dev.ps1 to be running (3 WeSync + 3 Syncthing instances).
 * Run with: npx playwright test --config e2e/playwright.config.ts
 *
 * Scenario:
 *   0. No folders exist on any device
 *   1. A creates folder "A"
 *   2. A adds device B → B accepts
 *   3. B adds device C → C accepts
 *   4. C removes device A → A, B, C all update
 *   5. B adds A back → all three devices have the folder again
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import fs from 'fs';
import {
  DEVICE_A,
  DEVICE_B,
  DEVICE_C,
  getStatus,
  getFolders,
  getDevices,
  getPeers,
  pair,
  shareFolder,
  acceptFolder,
  removeDeviceFromFolder,
  waitForPeer,
  waitForFolder,
  waitForFolderDevice,
  waitForFolderGone,
  waitForDeviceGoneFromFolder,
  getPendingFolders,
  waitForDeviceAccepted,
  restartAllSyncthing,
  cleanAll,
} from './helpers';
import { StateMonitor } from './state-monitor';

// Local folder paths (relative to repo root, tests run from web/)
const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');
const FOLDER_C_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-c');

test.describe.serial('Folder mesh', () => {
  let idA: string, idB: string, idC: string;
  let folderID: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let pageC: import('@playwright/test').Page;
  let monitor: StateMonitor;

  // ── Setup ───────────────────────────────────────────────────────────────────

  test.beforeAll(async ({ browser }) => {
    test.setTimeout(120_000);
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    pageC = await browser.newPage();
    // Retry goto in case a previous test left WeSync in a restarting state
    for (const [page, url] of [
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ] as const) {
      await expect
        .poll(
          async () => {
            try {
              await page.goto(url);
              return true;
            } catch {
              return false;
            }
          },
          { timeout: 30_000, intervals: [1000] },
        )
        .toBe(true);
    }

    // Resolve device IDs
    const [statusA, statusB, statusC] = await Promise.all([
      getStatus(pageA, DEVICE_A),
      getStatus(pageB, DEVICE_B),
      getStatus(pageC, DEVICE_C),
    ]);
    idA = statusA.myID;
    idB = statusB.myID;
    idC = statusC.myID;

    console.log(`A: ${idA.slice(0, 7)}  B: ${idB.slice(0, 7)}  C: ${idC.slice(0, 7)}`);

    // Clean up: remove all folders and paired devices to start fresh
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    monitor = new StateMonitor([
      [pageA, DEVICE_A, 'A'],
      [pageB, DEVICE_B, 'B'],
      [pageC, DEVICE_C, 'C'],
    ]);

    // Force WeSync to sync the empty state to Syncthing (removes stale ST config)
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
      pageC.request.post(`${DEVICE_C}/api/sync`, {}),
    ]);
    await new Promise((r) => setTimeout(r, 500));

    // Restart ST to clear in-memory "blocked re-introduction" tracking.
    await restartAllSyncthing(pageA);

    // Wait for discovery to kick in (all devices find each other via UDP)
    await Promise.all([
      waitForPeer(pageA, DEVICE_A, idB),
      waitForPeer(pageA, DEVICE_A, idC),
      waitForPeer(pageB, DEVICE_B, idA),
      waitForPeer(pageB, DEVICE_B, idC),
      waitForPeer(pageC, DEVICE_C, idA),
      waitForPeer(pageC, DEVICE_C, idB),
    ]);
    console.log('All devices discovered each other ✓');
    // Wait for WeSync sockets to be fully established.
    // A peer with a non-empty name in /api/peers means Hello was received via socket.
    const waitForSocketHello = async (
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
          {
            message: `waiting for Hello from ${targetID.slice(0, 7)} on ${base}`,
            timeout: 20_000,
            intervals: [500],
          },
        )
        .toBe(true);
    };
    await Promise.all([
      waitForSocketHello(pageA, DEVICE_A, idB),
      waitForSocketHello(pageA, DEVICE_A, idC),
      waitForSocketHello(pageB, DEVICE_B, idA),
      waitForSocketHello(pageB, DEVICE_B, idC),
      waitForSocketHello(pageC, DEVICE_C, idA),
      waitForSocketHello(pageC, DEVICE_C, idB),
    ]);
    console.log('All WeSync sockets established ✓');
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  // ── Step 0: No folders ──────────────────────────────────────────────────────

  test('0. No device has folders initially', async () => {
    const [fA, fB, fC] = await Promise.all([
      getFolders(pageA, DEVICE_A),
      getFolders(pageB, DEVICE_B),
      getFolders(pageC, DEVICE_C),
    ]);
    expect(fA).toHaveLength(0);
    expect(fB).toHaveLength(0);
    expect(fC).toHaveLength(0);
  });

  // ── Step 1: A creates folder "A" ───────────────────────────────────────────

  test('1. A creates folder "A"', async () => {
    folderID = await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'A', 'sendreceive');
    expect(folderID).toBeTruthy();
    await waitForFolder(pageA, DEVICE_A, folderID);
    const folders = await getFolders(pageA, DEVICE_A);
    expect(folders).toHaveLength(1);
    expect(folders[0].label).toBe('A');
  });

  // ── Step 2: A adds B → B accepts ───────────────────────────────────────────

  test('2. A adds device B, B accepts', async () => {
    // Pair A↔B — retry until both sides confirm.
    await expect
      .poll(
        async () => {
          const [onA, onB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (onA.some((d) => d.deviceID === idB) && onB.some((d) => d.deviceID === idA))
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { message: 'pairing A↔B', timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // A adds B to folder
    const folders = await getFolders(pageA, DEVICE_A);
    folderID = folders[0].id;
    await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'A', 'sendreceive', idB);

    // B should appear as pending (not yet accepted) on A's side.
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);
    await waitForDeviceAccepted(pageA, DEVICE_A, folderID, idB, false);

    // Wait for B to see A's folder offer via Syncthing BEP ClusterConfig.
    // ST needs ~14s to establish first connection on fresh instances.
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p) => p.folderID === folderID && p.deviceID === idA);
        },
        { message: "waiting for B to see A's folder offer via ST BEP", timeout: 60_000 },
      )
      .toBe(true);

    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);

    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);
  });

  // ── Step 3: B adds C → C accepts ───────────────────────────────────────────

  test('3. B adds device C, C accepts', async () => {
    // Pair B↔C
    // Pair B↔C — retry until both sides confirm.
    await expect
      .poll(
        async () => {
          const [onB, onC] = await Promise.all([
            getDevices(pageB, DEVICE_B),
            getDevices(pageC, DEVICE_C),
          ]);
          if (onB.some((d) => d.deviceID === idC) && onC.some((d) => d.deviceID === idB))
            return true;
          await pair(pageB, DEVICE_B, idC, 'C');
          await pair(pageC, DEVICE_C, idB, 'B');
          return false;
        },
        { message: 'pairing B↔C', timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // B adds C to folder — this also broadcasts state to A, so A learns about C
    const foldersOnB = await getFolders(pageB, DEVICE_B);
    const folderOnB = foldersOnB.find((f) => f.id === folderID)!;
    await shareFolder(pageB, DEVICE_B, folderOnB.path, folderOnB.label, folderOnB.type, idC);

    // Wait for C to see B's direct offer (B is paired with C)
    // A will be added to C's folder via WeSync state propagation after accept
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageC, DEVICE_C);
          return pending.some((p) => p.folderID === folderID && p.deviceID === idB);
        },
        { message: "waiting for C to see B's folder offer", timeout: 35_000 },
      )
      .toBe(true);

    await acceptFolder(pageC, DEVICE_C, folderID, idB, FOLDER_C_PATH);

    // All three must have each other.
    // ST BEP first connection + pairing retry can take up to ~30s total.
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idC, 60_000);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idC, 60_000);
    await waitForFolderDevice(pageC, DEVICE_C, folderID, idA, 60_000);
    await waitForFolderDevice(pageC, DEVICE_C, folderID, idB, 60_000);

    console.log('Step 3 ✓ — A, B, C all have each other in folder');
  });

  // ── Step 3c: A↔C file sync without direct pairing ─────────────────────────
  // This test verifies that Syncthing actually syncs files between A and C even
  // though they are NOT directly paired in WeSync. If this fails, MESH provides
  // folder-membership propagation only — not actual file sync.

  test('3c. Files sync directly between A and C (no direct pairing)', async () => {
    // Ensure test directories exist
    fs.mkdirSync(FOLDER_A_PATH, { recursive: true });
    fs.mkdirSync(FOLDER_C_PATH, { recursive: true });

    // A writes a file — should appear on C without going through B
    const fileFromA = `mesh-a-to-c-${Date.now()}.txt`;
    fs.writeFileSync(path.join(FOLDER_A_PATH, fileFromA), 'from A');

    await expect
      .poll(() => fs.existsSync(path.join(FOLDER_C_PATH, fileFromA)), {
        message: 'waiting for A→C file sync (A and C not directly paired)',
        timeout: 60_000,
      })
      .toBe(true);
    console.log(`A→C ✓ (${fileFromA})`);

    // C writes a file — should appear on A
    const fileFromC = `mesh-c-to-a-${Date.now()}.txt`;
    fs.writeFileSync(path.join(FOLDER_C_PATH, fileFromC), 'from C');

    await expect
      .poll(() => fs.existsSync(path.join(FOLDER_A_PATH, fileFromC)), {
        message: 'waiting for C→A file sync (A and C not directly paired)',
        timeout: 60_000,
      })
      .toBe(true);
    console.log(`C→A ✓ (${fileFromC})`);

    console.log('Step 3c ✓ — A and C sync files directly without explicit pairing');
  });

  // ── Step 3b: direction change (skipped — deviceTypes removed with FolderState) ─

  // ── Step 4: C removes A ─────────────────────────────────────────────────────

  test('4. C removes device A from folder', async () => {
    await removeDeviceFromFolder(pageC, DEVICE_C, folderID, idA);

    // Run all three checks in parallel — they're independent and B/C notifications
    // arrive at different times depending on wire latency.
    await Promise.all([
      waitForFolderGone(pageA, DEVICE_A, folderID, 35_000),
      waitForDeviceGoneFromFolder(pageB, DEVICE_B, folderID, idA, 35_000),
      waitForDeviceGoneFromFolder(pageC, DEVICE_C, folderID, idA, 35_000),
    ]);

    console.log('Step 4 ✓ — A removed, B and C remain');
  });

  // ── Step 5: B adds A back ───────────────────────────────────────────────────

  test('5. B adds device A back to folder', async () => {
    const foldersOnB = await getFolders(pageB, DEVICE_B);
    const folderOnB = foldersOnB.find((f) => f.id === folderID)!;
    await shareFolder(pageB, DEVICE_B, folderOnB.path, folderOnB.label, folderOnB.type, idA);

    // Accept directly — state propagation via WeSync peerwire handles the rest
    // even if Syncthing BEP hasn't reconnected yet.
    await acceptFolder(pageA, DEVICE_A, folderID, idB, FOLDER_A_PATH);

    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idC, 60_000);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);
    await waitForFolderDevice(pageC, DEVICE_C, folderID, idA);

    console.log('Step 5 ✓ — A, B, C all reunited in folder');
  });

  // ── Step 6: A removes folder — B and C keep it without A ───────────────────

  test('6. A removes folder — B and C keep it, A removed from their lists', async () => {
    test.setTimeout(120_000);
    // A removes their own folder entirely
    await pageA.request.delete(`${DEVICE_A}/api/folder?id=${folderID}`);

    await Promise.all([
      waitForFolderGone(pageA, DEVICE_A, folderID, 90_000),
      waitForDeviceGoneFromFolder(pageB, DEVICE_B, folderID, idA, 90_000),
      waitForDeviceGoneFromFolder(pageC, DEVICE_C, folderID, idA, 90_000),
    ]);

    // B and C still have each other
    await Promise.all([
      waitForFolderDevice(pageB, DEVICE_B, folderID, idC),
      waitForFolderDevice(pageC, DEVICE_C, folderID, idB),
    ]);

    console.log('Step 6 ✓ — A left, B and C remain with each other');
  });
});

// ── Helpers ───────────────────────────────────────────────────────────────────

async function getFolderID(
  page: Parameters<typeof getFolders>[0],
  base: string,
  label: string,
): Promise<string> {
  const folders = await getFolders(page, base);
  return folders.find((f) => f.label === label)?.id ?? '';
}
