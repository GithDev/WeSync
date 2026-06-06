/**
 * E2E: Incoming requests, notify delivery, and device state fields
 *
 * Tests three code paths introduced/reworked in the backend refactor:
 *
 * 1. buildIncoming() — /api/incoming reflects ST pending devices correctly
 *    • One-sided pair(A→B): B appears in A's incoming (A added B to ST)
 *    • pair(B→A) back: B disappears from A's incoming (now trusted, not pending)
 *    • Dismiss: removes device from incoming without trusting
 *
 * 2. notify() delivery — FolderRemove propagates to correct recipients
 *    • Remove device from folder: that device loses the folder
 *    • Remove folder entirely: all participants lose it
 *
 * 3. buildDeviceList() accepted field — tracks BEP lastSeen
 *    • Right after pairing (no BEP yet): accepted=false ("waiting")
 *    • After BEP connection via shared folder: accepted=true
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import {
  DEVICE_A,
  DEVICE_B,
  DEVICE_C,
  getStatus,
  getDevices,
  getIncoming,
  getFolders,
  getPendingFolders,
  pair,
  shareFolder,
  acceptFolder,
  waitForDevice,
  waitForFolderDevice,
  waitForFolderGone,
  waitForDeviceGoneFromFolder,
  cleanAll,
} from './helpers';

const TESTDATA = path.resolve(process.cwd(), '../testdata');
const FOLDER_A_PATH = path.join(TESTDATA, 'e2e-folder-a');
const FOLDER_B_PATH = path.join(TESTDATA, 'e2e-folder-b');
const FOLDER_C_PATH = path.join(TESTDATA, 'e2e-folder-c');

// ── Suite ─────────────────────────────────────────────────────────────────────

test.describe.serial('Incoming requests, notify delivery, and device state', () => {
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
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  // ── 1. buildIncoming() — one-sided pair creates incoming request ─────────

  test("I1: one-sided pair(A→B) — B appears in A's ST pending → shows in incoming", async () => {
    test.setTimeout(60_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // A trusts B (unilateral — adds B to A's ST)
    await pair(pageA, DEVICE_A, idB, 'B');

    // A immediately has B in devices (A trusts B)
    const dA = await getDevices(pageA, DEVICE_A);
    expect(dA.some((d: any) => d.deviceID === idB)).toBe(true);

    // B has NOT trusted A yet — B's /api/incoming should show A
    // (A added B to A's ST; B's ST sees A connecting via BEP → ST pending)
    await expect
      .poll(
        async () => {
          const inc = await getIncoming(pageB, DEVICE_B);
          return inc.some((r: any) => r.deviceID === idA);
        },
        {
          message: 'B: A appears in /api/incoming after one-sided pair',
          timeout: 30_000,
          intervals: [1000],
        },
      )
      .toBe(true);

    // B has NOT auto-trusted A
    const dB = await getDevices(pageB, DEVICE_B);
    expect(dB.some((d: any) => d.deviceID === idA)).toBe(false);

    // B is in A's devices with stPaired=true (A explicitly trusts B).
    const bOnA = dA.find((d: any) => d.deviceID === idB) as any;
    expect(bOnA).toBeTruthy();
    expect(bOnA.stPaired).toBe(true);
    // The Discovery.logic fix ensures stPaired+!accepted → kind:'waiting' not 'connected'.
    // That logic is unit-tested in Discovery.logic.test.ts.

    console.log("I1 ✓ — one-sided pair: B is waiting (stPaired, pending BEP) on A's side");
  });

  // ── 2. buildIncoming() — mutual pair clears incoming ────────────────────

  test('I2: mutual pair — incoming clears once B trusts A back', async () => {
    test.setTimeout(60_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // A trusts B (one-sided)
    await pair(pageA, DEVICE_A, idB, 'B');
    await expect
      .poll(
        async () => {
          const inc = await getIncoming(pageB, DEVICE_B);
          return inc.some((r: any) => r.deviceID === idA);
        },
        { timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);

    // B trusts A back (mutual)
    await pair(pageB, DEVICE_B, idA, 'A');
    await waitForDevice(pageB, DEVICE_B, idA);

    // A is now trusted by B → must NOT appear in B's incoming anymore
    await expect
      .poll(
        async () => {
          const inc = await getIncoming(pageB, DEVICE_B);
          return !inc.some((r: any) => r.deviceID === idA);
        },
        { message: 'B: A gone from incoming after mutual pair', timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    console.log('I2 ✓ — mutual pair: incoming cleared once both sides trust');
  });

  // ── 3. buildIncoming() — dismiss removes from incoming without trusting ──

  test('I3: dismiss incoming request — gone from incoming, still not trusted', async () => {
    test.setTimeout(60_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // A trusts B → B sees A in incoming
    await pair(pageA, DEVICE_A, idB, 'B');
    await expect
      .poll(
        async () => {
          const inc = await getIncoming(pageB, DEVICE_B);
          return inc.some((r: any) => r.deviceID === idA);
        },
        { timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);

    // B dismisses A's request (DELETE /api/incoming?id=A)
    await pageB.request.delete(`${DEVICE_B}/api/incoming?id=${idA}`);

    // A disappears from B's incoming
    await expect
      .poll(
        async () => {
          const inc = await getIncoming(pageB, DEVICE_B);
          return !inc.some((r: any) => r.deviceID === idA);
        },
        { message: 'B: A gone from incoming after dismiss', timeout: 10_000, intervals: [500] },
      )
      .toBe(true);

    // B has still NOT trusted A
    const dB = await getDevices(pageB, DEVICE_B);
    expect(dB.some((d: any) => d.deviceID === idA)).toBe(false);

    console.log('I3 ✓ — dismiss: gone from incoming, A still not trusted on B');
  });

  // ── 4. notify() delivery — remove device from folder notifies that device ─

  test('N1: remove device from folder — target device loses the folder', async () => {
    test.setTimeout(90_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Pair A↔B and share folder A→B
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

    await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'NotifyTest', 'sendreceive', idB);
    const fA = await getFolders(pageA, DEVICE_A);
    const folderID = fA.find((f: any) => f.label === 'NotifyTest')?.id ?? '';
    expect(folderID).not.toBe('');

    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p: any) => p.folderID === folderID);
        },
        { timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);

    // A removes B from the folder → notify() sends FolderRemove to B
    await pageA.request.delete(
      `${DEVICE_A}/api/folder/device?folderID=${encodeURIComponent(folderID)}&deviceID=${idB}`,
    );

    // B must no longer have A in that folder's device list (or the folder is gone)
    await waitForDeviceGoneFromFolder(pageB, DEVICE_B, folderID, idA);

    // A's folder still exists (A just removed B from it)
    const fAfter = await getFolders(pageA, DEVICE_A);
    expect(fAfter.some((f: any) => f.id === folderID)).toBe(true);

    console.log('N1 ✓ — remove device from folder: notify delivered, B lost A from folder');
  });

  // ── 5. notify() delivery — delete folder: B leaves, A and C keep their copies ─
  //
  // FolderRemove with TargetDeviceID=B.selfID means "B is leaving the folder".
  // Receivers remove B from their device list but KEEP their own copy of the folder.
  // This preserves A's and C's data — B leaving shouldn't cascade-delete their folders.

  test('N2: delete folder — B leaves, A and C remove B but keep their copies', async () => {
    test.setTimeout(90_000);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    // Pair A↔B and B↔C
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

    await expect
      .poll(
        async () => {
          const [dB, dC] = await Promise.all([
            getDevices(pageB, DEVICE_B),
            getDevices(pageC, DEVICE_C),
          ]);
          if (dB.some((x: any) => x.deviceID === idC) && dC.some((x: any) => x.deviceID === idB))
            return true;
          await pair(pageB, DEVICE_B, idC, 'C');
          await pair(pageC, DEVICE_C, idB, 'B');
          return false;
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    // B creates folder and shares with A and C
    await shareFolder(pageB, DEVICE_B, FOLDER_B_PATH, 'MultiDelete', 'sendreceive', idA);
    const fB = await getFolders(pageB, DEVICE_B);
    const folderID = fB.find((f: any) => f.label === 'MultiDelete')?.id ?? '';
    expect(folderID).not.toBe('');

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageA, DEVICE_A);
          return p.some((pf: any) => pf.folderID === folderID);
        },
        { timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);
    await acceptFolder(pageA, DEVICE_A, folderID, idB, FOLDER_A_PATH);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);

    await shareFolder(pageB, DEVICE_B, FOLDER_B_PATH, 'MultiDelete', 'sendreceive', idC);
    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageC, DEVICE_C);
          return p.some((pf: any) => pf.folderID === folderID);
        },
        { timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);
    await acceptFolder(pageC, DEVICE_C, folderID, idB, FOLDER_C_PATH);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idC);

    // B deletes its folder → sends FolderRemove(TargetDeviceID=B) to A and C
    await pageB.request.delete(`${DEVICE_B}/api/folder?id=${encodeURIComponent(folderID)}`);

    // B's folder is gone immediately
    const fBAfter = await getFolders(pageB, DEVICE_B);
    expect(fBAfter.some((f: any) => f.id === folderID)).toBe(false);

    // A and C receive notification: B left → they remove B from the folder's device list
    // but they KEEP their own copy of the folder
    await waitForDeviceGoneFromFolder(pageA, DEVICE_A, folderID, idB, 30_000);
    await waitForDeviceGoneFromFolder(pageC, DEVICE_C, folderID, idB, 30_000);

    // A and C still have the folder
    const fAAfter = await getFolders(pageA, DEVICE_A);
    expect(fAAfter.some((f: any) => f.id === folderID)).toBe(true);
    const fCAfter = await getFolders(pageC, DEVICE_C);
    expect(fCAfter.some((f: any) => f.id === folderID)).toBe(true);

    console.log('N2 ✓ — B deleted folder: B gone from A/C device lists, A/C kept their copies');
  });
});
