/**
 * E2E: Folder marker fix
 *
 * Verifies that when the .stfolder marker is missing,
 * the fix-marker endpoint creates it and triggers a Syncthing rescan
 * so the error clears.
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import fs from 'fs';
import {
  DEVICE_A,
  DEVICE_B,
  getStatus,
  getFolders,
  getDevices,
  pair,
  shareFolder,
  acceptFolder,
  waitForPeer,
  waitForFolderDevice,
  cleanAll,
} from './helpers';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');
const MARKER = (folderPath: string) => path.join(folderPath, '.stfolder');

test.describe.serial('Folder marker fix', () => {
  let idA: string, idB: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let folderID = '';

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

    await waitForPeer(pageA, DEVICE_A, idB);
    await waitForPeer(pageB, DEVICE_B, idA);
    await new Promise((r) => setTimeout(r, 1000));
  });

  test.afterAll(async () => {
    // Restore marker if test left it missing
    for (const p of [FOLDER_A_PATH, FOLDER_B_PATH]) {
      const marker = MARKER(p);
      if (!fs.existsSync(marker)) fs.mkdirSync(marker, { recursive: true });
    }
    await pageA?.close();
    await pageB?.close();
  });

  test('1. Pair A↔B and share folder', async () => {
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
        { timeout: 30_000, intervals: [2000] },
      )
      .toBe(true);

    await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'Marker Test', 'sendreceive', idB);
    const fA = await getFolders(pageA, DEVICE_A);
    folderID = fA.find((f: { label: string }) => f.label === 'Marker Test')?.id ?? '';
    expect(folderID).not.toBe('');

    await expect
      .poll(
        async () => {
          const p = await pageB.request
            .get(`${DEVICE_B}/api/folders/pending`)
            .then((r) => r.json());
          return p.some((pf: { folderID: string }) => pf.folderID === folderID);
        },
        { timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);

    console.log('✓ Folder shared and accepted');
  });

  test('2. Remove .stfolder marker — folder enters error state', async () => {
    const marker = MARKER(FOLDER_B_PATH);
    if (fs.existsSync(marker)) fs.rmdirSync(marker, { recursive: true });
    expect(fs.existsSync(marker)).toBe(false);

    // Trigger ST rescan so it notices the missing marker
    await pageB.request.post(`${DEVICE_B}/api/sync`, {});

    // Wait for error state — ST should detect missing marker within 30s
    await expect
      .poll(
        async () => {
          const res = await pageB.request.get(
            `${DEVICE_B}/api/folder/status?id=${encodeURIComponent(folderID)}`,
          );
          const status = await res.json();
          return status.error?.includes('marker') || status.state === 'error';
        },
        { message: 'waiting for marker-missing error', timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);

    console.log('✓ Missing marker detected by Syncthing');
  });

  test('3. Fix marker via API — error clears', async () => {
    const res = await pageB.request.post(
      `${DEVICE_B}/api/folder/fix-marker?id=${encodeURIComponent(folderID)}`,
    );
    expect(res.status()).toBe(204);

    const marker = MARKER(FOLDER_B_PATH);
    expect(fs.existsSync(marker)).toBe(true);

    // Wait for error to clear after rescan
    await expect
      .poll(
        async () => {
          const res = await pageB.request.get(
            `${DEVICE_B}/api/folder/status?id=${encodeURIComponent(folderID)}`,
          );
          const status = await res.json();
          return !status.error?.includes('marker') && status.state !== 'error';
        },
        { message: 'waiting for error to clear after fix', timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);

    console.log('✓ Marker created, Syncthing error cleared');
  });
});
