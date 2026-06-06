/**
 * E2E: Multi-folder status across A, B, C
 *
 * Tests that folder acceptance status stays correct when:
 *   - Two folders are shared between the same device pair
 *   - Sharing happens in sequence (folder1 first, folder2 later)
 *   - Folder1 statuses remain stable across folder2 invitation
 *   - Each device's view of every (folder, peer) pair is consistent
 *
 * Specifically catches the bug where A invites B to a second folder while
 * folder1 between A↔B is already accepted: A's UI was incorrectly showing
 * B as accepted on folder2 before B had actually accepted, because the
 * existing BEP connection's RemoteState bled into the new folder's status.
 *
 * Requires dev.ps1 to be running.
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
  getFolders,
  getPendingFolders,
  pair,
  waitForDevice,
  waitForPeer,
  shareFolder,
  acceptFolder,
  waitForFolderDevice,
  waitForDeviceAccepted,
  cleanAll,
} from './helpers';

// Folder 1 — paths per device
const F1_A = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const F1_B = path.resolve(process.cwd(), '../testdata/e2e-folder-b');
const F1_C = path.resolve(process.cwd(), '../testdata/e2e-folder-c');

// Folder 2 — separate paths to avoid collision with folder1
const F2_A = path.resolve(process.cwd(), '../testdata/e2e-folder-a2');
const F2_B = path.resolve(process.cwd(), '../testdata/e2e-folder-b2');
const F2_C = path.resolve(process.cwd(), '../testdata/e2e-folder-c2');

test.describe.serial('Multi-folder status — A, B, C with two folders', () => {
  let idA: string;
  let idB: string;
  let idC: string;
  let f1ID: string;
  let f2ID: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let pageC: import('@playwright/test').Page;

  // Helper: assert deviceAccepted holds at the expected value across multiple
  // samples — catches flicker and transient-true bugs that a single poll misses.
  async function assertAcceptedSticks(
    page: import('@playwright/test').Page,
    base: string,
    folderID: string,
    deviceID: string,
    expected: boolean,
    durationMs = 2_500,
  ) {
    const deadline = Date.now() + durationMs;
    while (Date.now() < deadline) {
      const folders = await getFolders(page, base);
      const f = folders.find((ff) => ff.id === folderID);
      const status = f?.deviceAccepted?.[deviceID];
      const actual = status === true;
      const ctx = `${base} folder=${folderID.slice(0, 8)} device=${deviceID.slice(0, 7)} expected accepted=${expected} got=${actual}`;
      expect(actual, ctx).toBe(expected);
      await new Promise((r) => setTimeout(r, 250));
    }
  }

  test.beforeAll(async ({ browser }) => {
    test.setTimeout(180_000);

    // Make sure folder2 paths exist on disk (ST needs the directory to add the folder).
    for (const p of [F2_A, F2_B, F2_C]) {
      try {
        fs.mkdirSync(p, { recursive: true });
      } catch {
        /* ignore */
      }
    }

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

    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    // Pair every pair (full trust mesh) so introducer doesn't interfere.
    const ensurePaired = async (
      p1: import('@playwright/test').Page,
      b1: string,
      id2: string,
      name2: string,
      p2: import('@playwright/test').Page,
      b2: string,
      id1: string,
      name1: string,
    ) => {
      await Promise.all([waitForPeer(p1, b1, id2), waitForPeer(p2, b2, id1)]);
      await pair(p1, b1, id2, name2);
      await pair(p2, b2, id1, name1);
      await Promise.all([waitForDevice(p1, b1, id2), waitForDevice(p2, b2, id1)]);
    };
    await ensurePaired(pageA, DEVICE_A, idB, 'B', pageB, DEVICE_B, idA, 'A');
    await ensurePaired(pageA, DEVICE_A, idC, 'C', pageC, DEVICE_C, idA, 'A');
    await ensurePaired(pageB, DEVICE_B, idC, 'C', pageC, DEVICE_C, idB, 'B');
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  test('1. A shares folder1 with B — B accepts, A sees B accepted', async () => {
    f1ID = await shareFolder(pageA, DEVICE_A, F1_A, 'Folder 1', 'sendreceive', idB);
    expect(f1ID).toBeTruthy();

    // Before B accepts, A should see B as pending.
    await waitForDeviceAccepted(pageA, DEVICE_A, f1ID, idB, false);

    // B sees the invite and accepts.
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p) => p.folderID === f1ID && p.deviceID === idA);
        },
        { message: 'waiting for B to see folder1 invite', timeout: 60_000 },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, f1ID, idA, F1_B);

    await waitForDeviceAccepted(pageA, DEVICE_A, f1ID, idB, true);
    await waitForFolderDevice(pageB, DEVICE_B, f1ID, idA);
  });

  test('2. A shares folder1 with C — C accepts, no regression on B status', async () => {
    await shareFolder(pageA, DEVICE_A, F1_A, 'Folder 1', 'sendreceive', idC);
    await waitForDeviceAccepted(pageA, DEVICE_A, f1ID, idC, false);

    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageC, DEVICE_C);
          return pending.some((p) => p.folderID === f1ID && p.deviceID === idA);
        },
        { message: 'waiting for C to see folder1 invite', timeout: 60_000 },
      )
      .toBe(true);
    await acceptFolder(pageC, DEVICE_C, f1ID, idA, F1_C);

    await waitForDeviceAccepted(pageA, DEVICE_A, f1ID, idC, true);
    // B's accept status for folder1 must stay true through this.
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idB, true, 1_500);
  });

  test('3. A shares folder2 with B — A must see B as PENDING (sustained)', async () => {
    f2ID = await shareFolder(pageA, DEVICE_A, F2_A, 'Folder 2', 'sendreceive', idB);
    expect(f2ID).toBeTruthy();
    expect(f2ID).not.toBe(f1ID);

    // CRITICAL: A must see B as NOT accepted for folder2 across multiple samples.
    // The bug: existing BEP connection's idle RemoteState was leaking into the
    // new folder's accepted status before B actually accepted it.
    await assertAcceptedSticks(pageA, DEVICE_A, f2ID, idB, false, 3_000);

    // Folder1 statuses must be unchanged through this.
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idB, true, 500);
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idC, true, 500);
  });

  test('4. B accepts folder2 — A sees B accepted, folder1 unchanged', async () => {
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p) => p.folderID === f2ID && p.deviceID === idA);
        },
        { message: 'waiting for B to see folder2 invite', timeout: 60_000 },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, f2ID, idA, F2_B);

    await waitForDeviceAccepted(pageA, DEVICE_A, f2ID, idB, true);
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idB, true, 500);
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idC, true, 500);
  });

  test('5. A shares folder2 with C — A must see C as PENDING (sustained)', async () => {
    await shareFolder(pageA, DEVICE_A, F2_A, 'Folder 2', 'sendreceive', idC);

    // Same bug as step 3 but for C, with both folder1 AND folder2-with-B
    // already established. RemoteState of C's existing BEP connection
    // (for folder1) must not bleed into folder2's status.
    await assertAcceptedSticks(pageA, DEVICE_A, f2ID, idC, false, 3_000);

    // All previous statuses unchanged.
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idB, true, 500);
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idC, true, 500);
    await assertAcceptedSticks(pageA, DEVICE_A, f2ID, idB, true, 500);
  });

  test('6. C accepts folder2 — full mesh, all accepted', async () => {
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageC, DEVICE_C);
          return pending.some((p) => p.folderID === f2ID && p.deviceID === idA);
        },
        { message: 'waiting for C to see folder2 invite', timeout: 60_000 },
      )
      .toBe(true);
    await acceptFolder(pageC, DEVICE_C, f2ID, idA, F2_C);

    // Every (folder × peer) on every device must report accepted=true.
    await waitForDeviceAccepted(pageA, DEVICE_A, f1ID, idB, true);
    await waitForDeviceAccepted(pageA, DEVICE_A, f1ID, idC, true);
    await waitForDeviceAccepted(pageA, DEVICE_A, f2ID, idB, true);
    await waitForDeviceAccepted(pageA, DEVICE_A, f2ID, idC, true);
    await waitForDeviceAccepted(pageB, DEVICE_B, f1ID, idA, true);
    await waitForDeviceAccepted(pageB, DEVICE_B, f2ID, idA, true);
    await waitForDeviceAccepted(pageC, DEVICE_C, f1ID, idA, true);
    await waitForDeviceAccepted(pageC, DEVICE_C, f2ID, idA, true);
  });

  test('7. Sustained stability — statuses remain correct after settle', async () => {
    // Wait 3s and re-verify all pairs simultaneously. Catches statuses that
    // briefly flicker false after a reconciliation tick or BEP reconnect.
    await new Promise((r) => setTimeout(r, 3_000));

    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idB, true, 800);
    await assertAcceptedSticks(pageA, DEVICE_A, f1ID, idC, true, 800);
    await assertAcceptedSticks(pageA, DEVICE_A, f2ID, idB, true, 800);
    await assertAcceptedSticks(pageA, DEVICE_A, f2ID, idC, true, 800);
    await assertAcceptedSticks(pageB, DEVICE_B, f1ID, idA, true, 800);
    await assertAcceptedSticks(pageB, DEVICE_B, f2ID, idA, true, 800);
    await assertAcceptedSticks(pageC, DEVICE_C, f1ID, idA, true, 800);
    await assertAcceptedSticks(pageC, DEVICE_C, f2ID, idA, true, 800);
  });
});
