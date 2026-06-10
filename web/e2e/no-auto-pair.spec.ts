/**
 * E2E: ST Introducer auto-trust via shared folder
 *
 * When B (Introducer=true) shares a folder with both A and C, the ST Introducer
 * mechanism automatically establishes trust between A and C via BEP ClusterConfig.
 * This replaced the old FolderMembers wire signal approach.
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
  getPendingFolders,
  pair,
  shareFolder,
  acceptFolder,
  waitForPeer,
  waitForFolderDevice,
  forceBEPAddress,
  cleanAll,
} from './helpers';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');
const FOLDER_C_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-c');

test.describe.serial('No auto-pairing via MESH', () => {
  let idA: string, idB: string, idC: string;
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

    // Clean slate — critical: ensure no stale outgoing state
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
    await Promise.all([
      waitForPeer(pageA, DEVICE_A, idB),
      waitForPeer(pageB, DEVICE_B, idA),
      waitForPeer(pageB, DEVICE_B, idC),
      waitForPeer(pageC, DEVICE_C, idB),
    ]);
    await new Promise((r) => setTimeout(r, 2000));
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  let folderID = '';

  test('1. A↔B pair, B↔C pair (A and C do NOT pair with each other)', async () => {
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

    // Confirm A and C are NOT paired
    const [dA, dC] = await Promise.all([getDevices(pageA, DEVICE_A), getDevices(pageC, DEVICE_C)]);
    expect(dA.some((d) => d.deviceID === idC)).toBe(false);
    expect(dC.some((d) => d.deviceID === idA)).toBe(false);

    // Patch explicit BEP addresses so ST can connect on loopback immediately.
    // Without this, mDNS dynamic discovery takes 20-60s on a same-machine setup,
    // causing folder invite and Introducer waits to time out.
    await Promise.all([
      forceBEPAddress(pageA, 0, idB, 1),
      forceBEPAddress(pageB, 1, idA, 0),
      forceBEPAddress(pageB, 1, idC, 2),
      forceBEPAddress(pageC, 2, idB, 1),
    ]);

    console.log('✓ A↔B and B↔C paired; A and C are NOT paired');
  });

  test('2. B shares folder with C, C accepts', async () => {
    await shareFolder(pageB, DEVICE_B, FOLDER_B_PATH, 'Shared', 'sendreceive', idC);
    const fB = await getFolders(pageB, DEVICE_B);
    folderID = fB.find((f) => f.label === 'Shared')?.id ?? '';
    expect(folderID).not.toBe('');

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageC, DEVICE_C);
          return p.some((pf) => pf.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    await acceptFolder(pageC, DEVICE_C, folderID, idB, FOLDER_C_PATH);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idC);

    console.log('✓ B shares folder with C, C accepted');
  });

  test('3. B shares folder with A, A accepts', async () => {
    await shareFolder(pageB, DEVICE_B, FOLDER_B_PATH, 'Shared', 'sendreceive', idA);

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageA, DEVICE_A);
          return p.some((pf) => pf.folderID === folderID);
        },
        { timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);
    await acceptFolder(pageA, DEVICE_A, folderID, idB, FOLDER_A_PATH);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);

    console.log('✓ B shares folder with A, A accepted');
  });

  test('4. ST Introducer auto-trusts A and C via B (intended behaviour)', async () => {
    // With Introducer=true on all devices, B introduces A and C to each other
    // via BEP ClusterConfig when they share a folder through B.
    // This is the correct post-FolderMembers behaviour: no wire signal needed.
    await new Promise((r) => setTimeout(r, 5000));

    // A and C must be in each other's folder
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idC, 30_000);
    await waitForFolderDevice(pageC, DEVICE_C, folderID, idA, 30_000);
    console.log("✓ A and C both appear in each other's folder (via ST Introducer)");

    // With Introducer they are also auto-trusted (SyncFromSyncthing may need a moment)
    await expect
      .poll(
        async () => {
          const dA = await getDevices(pageA, DEVICE_A);
          return dA.some((d: { deviceID: string }) => d.deviceID === idC);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);
    await expect
      .poll(
        async () => {
          const dC = await getDevices(pageC, DEVICE_C);
          return dC.some((d: { deviceID: string }) => d.deviceID === idA);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    console.log('✓ A and C are auto-trusted via ST Introducer — no manual pairing required');
  });
});
