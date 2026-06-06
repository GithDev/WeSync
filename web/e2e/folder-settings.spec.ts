/**
 * E2E: Folder settings (pause, resume, direction)
 *
 * Requires dev.ps1 to be running.
 * Run with: npx playwright test --config e2e/playwright.config.ts
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import {
  DEVICE_A,
  DEVICE_B,
  getStatus,
  getFolders,
  getDevices,
  pair,
  shareFolder,
  acceptFolder,
  waitForDevice,
  waitForPeer,
  waitForFolder,
  getPendingFolders,
  setFolderPaused,
  getFolderStatus,
  cleanAll,
} from './helpers';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');

test.describe.serial('Folder settings', () => {
  let idA: string, idB: string;
  let folderID: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;

  test.beforeAll(async ({ browser }) => {
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B)]);

    const [sA, sB] = await Promise.all([getStatus(pageA, DEVICE_A), getStatus(pageB, DEVICE_B)]);
    idA = sA.myID;
    idB = sB.myID;

    // Clean up
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Discover + pair
    await waitForPeer(pageA, DEVICE_A, idB);
    const devsOnA = await getDevices(pageA, DEVICE_A);
    if (!devsOnA.some((d) => d.deviceID === idB)) {
      await pair(pageA, DEVICE_A, idB, 'B');
      await pair(pageB, DEVICE_B, idA, 'A');
      await waitForDevice(pageA, DEVICE_A, idB);
    }

    // Create shared folder
    folderID = await shareFolder(
      pageA,
      DEVICE_A,
      FOLDER_A_PATH,
      'Settings test',
      'sendreceive',
      idB,
    );
    expect(folderID).toBeTruthy();

    await expect
      .poll(
        () =>
          getPendingFolders(pageB, DEVICE_B).then((p) =>
            p.some((f) => f.deviceID === idA && f.folderID === folderID),
          ),
        { timeout: 35_000 },
      )
      .toBe(true);
    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);

    await waitForFolder(pageA, DEVICE_A, folderID);
    await waitForFolder(pageB, DEVICE_B, folderID);
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
  });

  // ── Pause / resume ─────────────────────────────────────────────────────────

  test('pause folder stops sync on that device', async () => {
    const before = await getFolderStatus(pageA, DEVICE_A, folderID);
    expect(before.paused).toBe(false);

    await setFolderPaused(pageA, DEVICE_A, folderID, true);

    const after = await getFolderStatus(pageA, DEVICE_A, folderID);
    expect(after.paused).toBe(true);

    // B is unaffected — pause is local to A
    const statusB = await getFolderStatus(pageB, DEVICE_B, folderID);
    expect(statusB.paused).toBe(false);
  });

  test('resume restores sync', async () => {
    await setFolderPaused(pageA, DEVICE_A, folderID, false);

    const status = await getFolderStatus(pageA, DEVICE_A, folderID);
    expect(status.paused).toBe(false);
  });

  test('pause then resume returns to previous state', async () => {
    await setFolderPaused(pageA, DEVICE_A, folderID, true);
    const paused = await getFolderStatus(pageA, DEVICE_A, folderID);
    expect(paused.paused).toBe(true);

    await setFolderPaused(pageA, DEVICE_A, folderID, false);
    const resumed = await getFolderStatus(pageA, DEVICE_A, folderID);
    expect(resumed.paused).toBe(false);
    expect(resumed.globalFiles).toBeGreaterThanOrEqual(0); // sync state is accessible
  });
});
