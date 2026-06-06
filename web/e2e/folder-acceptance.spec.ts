/**
 * E2E: Folder acceptance state
 *
 * Verifies that a device shows as "pending" (accepted=false) on the sharer's
 * side until the recipient explicitly accepts via WeSync UI.
 *
 * Requires dev.ps1 to be running.
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import {
  DEVICE_A,
  DEVICE_B,
  getStatus,
  getDevices,
  getFolders,
  getPendingFolders,
  pair,
  shareFolder,
  acceptFolder,
  waitForDevice,
  waitForPeer,
  waitForFolderDevice,
  cleanAll,
} from './helpers';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');

test.describe.serial('Folder acceptance state', () => {
  let idA: string, idB: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;

  test.beforeAll(async ({ browser }) => {
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B)]);

    const [sA, sB] = await Promise.all([getStatus(pageA, DEVICE_A), getStatus(pageB, DEVICE_B)]);
    idA = sA.myID;
    idB = sB.myID;

    // Clean slate
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
    ]);
    await waitForPeer(pageA, DEVICE_A, idB);
    await waitForPeer(pageB, DEVICE_B, idA);
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
  });

  test('1. Pair A↔B', async () => {
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
  });

  test('2. A shares folder with B — B shows as pending until B accepts', async () => {
    await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'Acceptance Test', 'sendreceive', idB);
    const fA = await getFolders(pageA, DEVICE_A);
    const folderID = fA.find((f) => f.label === 'Acceptance Test')?.id ?? '';
    expect(folderID).not.toBe('');

    // B is in A's folder device list
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);

    // B should show as NOT accepted yet (pending)
    await expect
      .poll(
        async () => {
          const folders = await getFolders(pageA, DEVICE_A);
          const folder = folders.find((f) => f.id === folderID);
          const accepted = folder?.deviceAccepted?.[idB];
          return accepted;
        },
        { message: 'B should be pending (not accepted) before accepting', timeout: 10_000 },
      )
      .toBe(false);

    console.log('✓ B correctly shows as pending before accepting');
  });

  test('3. B accepts — A sees B as accepted', async () => {
    const fA = await getFolders(pageA, DEVICE_A);
    const folderID = fA.find((f) => f.label === 'Acceptance Test')?.id ?? '';

    // Wait for B to see the pending invite
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p) => p.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    // B accepts
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);

    // After B accepts, A should see B as accepted
    await expect
      .poll(
        async () => {
          const folders = await getFolders(pageA, DEVICE_A);
          const folder = folders.find((f) => f.id === folderID);
          return folder?.deviceAccepted?.[idB];
        },
        { message: 'B should be accepted after accepting', timeout: 30_000 },
      )
      .toBe(true);

    console.log('✓ B correctly shows as accepted after accepting');
  });
});
