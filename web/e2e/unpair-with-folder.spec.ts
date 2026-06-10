/**
 * E2E: Unpair with shared folder
 *
 * Tests the critical scenario that was broken:
 * When A unpairs B while they share a folder, the folder membership
 * must be cleaned up on ALL peers â€” not just the two directly involved.
 *
 * Scenario:
 *   1. A pairs with B, B pairs with C
 *   2. A creates folder and shares with B; B shares with C (mesh)
 *   3. A unpairs B
 *      â†’ A: B removed from device list AND from folder
 *      â†’ B: A removed from device list AND from folder
 *      â†’ C: A removed from folder (B still in folder)
 *   4. B's folder still exists with C only
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
  pair,
  shareFolder,
  acceptFolder,
  waitForDevice,
  waitForPeer,
  waitForFolderDevice,
  waitForDeviceGoneFromFolder,
  waitForFolderGone,
  getPendingFolders,
  forceBEPAddress,
  restartAllSyncthing,
  cleanAll,
} from './helpers';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');
const FOLDER_C_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-c');

test.describe.serial('Unpair with shared folder', () => {
  let idA: string, idB: string, idC: string;
  let folderID: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let pageC: import('@playwright/test').Page;

  test.beforeAll(async ({ browser }) => {
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

    // Clean up
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
      pageC.request.post(`${DEVICE_C}/api/sync`, {}),
    ]);

    // Restart ST to clear in-memory "blocked re-introduction" tracking.
    // Without this, ST refuses to re-introduce a device that was removed
    // in a previous test run, breaking the Introducer-based mesh scenario.
    await restartAllSyncthing(pageA);

    await Promise.all([
      waitForPeer(pageA, DEVICE_A, idB),
      waitForPeer(pageB, DEVICE_B, idA),
      waitForPeer(pageB, DEVICE_B, idC),
      waitForPeer(pageC, DEVICE_C, idB),
    ]);
    // Wait for socket Hellos to confirm sockets are ready for pairing.
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
    await Promise.all([
      waitForHello(pageA, DEVICE_A, idB),
      waitForHello(pageB, DEVICE_B, idA),
      waitForHello(pageB, DEVICE_B, idC),
      waitForHello(pageC, DEVICE_C, idB),
    ]);
    // Allow pending goroutines from cleanup to drain before pairing.
    await new Promise((r) => setTimeout(r, 2000));
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  test('1. Pair Aâ†”B and Bâ†”C', async () => {
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x) => x.deviceID === idB) && dB.some((x) => x.deviceID === idA)) return true;
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
          if (dB.some((x) => x.deviceID === idC) && dC.some((x) => x.deviceID === idB)) return true;
          await pair(pageB, DEVICE_B, idC, 'C');
          await pair(pageC, DEVICE_C, idB, 'B');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);
  });

  test('2. A shares folder with B, B shares with C', async () => {
    // Patch explicit BEP addresses so folder invites propagate quickly on loopback
    await Promise.all([
      forceBEPAddress(pageA, 0, idB, 1),
      forceBEPAddress(pageB, 1, idA, 0),
      forceBEPAddress(pageB, 1, idC, 2),
      forceBEPAddress(pageC, 2, idB, 1),
    ]);

    folderID = await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'Shared', 'sendreceive', idB);
    expect(folderID).toBeTruthy();

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageB, DEVICE_B);
          return p.some((x) => x.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);

    const fB = await getFolders(pageB, DEVICE_B);
    const fb = fB.find((f) => f.id === folderID)!;
    await shareFolder(pageB, DEVICE_B, fb.path, fb.label, fb.type, idC);

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageC, DEVICE_C);
          return p.some((x) => x.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    await acceptFolder(pageC, DEVICE_C, folderID, idB, FOLDER_C_PATH);

    await waitForFolderDevice(pageA, DEVICE_A, folderID, idC, 60_000);
    await waitForFolderDevice(pageC, DEVICE_C, folderID, idA, 60_000);
    console.log('All three sharing folder âœ“');
  });

  test('3. A unpairs B â€” folder membership updates everywhere', async () => {
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);

    // A: B gone from device list
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          return !d.some((x) => x.deviceID === idB);
        },
        { message: 'A: B removed from devices', timeout: 30_000 },
      )
      .toBe(true);

    // A: B gone from folder
    await waitForDeviceGoneFromFolder(pageA, DEVICE_A, folderID, idB);

    // B: A gone from device list (may take time if socket reconnects)
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageB, DEVICE_B);
          return !d.some((x) => x.deviceID === idA);
        },
        { message: 'B: A removed from devices', timeout: 30_000 },
      )
      .toBe(true);

    // B: A gone from folder (but folder still exists with C)
    await waitForDeviceGoneFromFolder(pageB, DEVICE_B, folderID, idA);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idC);

    // C: A gone from folder (C still has B)
    await waitForDeviceGoneFromFolder(pageC, DEVICE_C, folderID, idA);
    await waitForFolderDevice(pageC, DEVICE_C, folderID, idB);

    console.log('Unpair with folder cleanup âœ“');
  });
});
