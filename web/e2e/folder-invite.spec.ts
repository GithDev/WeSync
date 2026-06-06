/**
 * E2E: Folder invite visibility
 *
 * Verifies that when A shares a folder with B:
 *   - B sees the invite in their pending folders (API)
 *   - B sees the Folders tab badge in the nav
 *   - B sees the invite card on the Folders page
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
  waitForDevice,
  waitForPeer,
  shareFolder,
  cleanAll,
} from './helpers';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');

test.describe.serial('Folder invite visibility', () => {
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

    // Clean up folders and devices
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    // Ensure A and B are paired
    const devicesOnA = await getDevices(pageA, DEVICE_A);
    if (!devicesOnA.some((d) => d.deviceID === idB)) {
      await waitForPeer(pageA, DEVICE_A, idB);
      await pair(pageA, DEVICE_A, idB, 'B');
      await pair(pageB, DEVICE_B, idA, 'A');
      await waitForDevice(pageA, DEVICE_A, idB);
      await waitForDevice(pageB, DEVICE_B, idA);
    }
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
  });

  test('A shares a folder with B — B sees the invite via API', async () => {
    await shareFolder(pageA, DEVICE_A, FOLDER_A_PATH, 'Invite Test', 'sendreceive', idB);

    const foldersOnA = await getFolders(pageA, DEVICE_A);
    folderID = foldersOnA.find((f) => f.label === 'Invite Test')?.id ?? '';
    expect(folderID).not.toBe('');

    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p) => p.folderID === folderID);
        },
        { message: 'waiting for B to see folder invite via API', timeout: 35_000 },
      )
      .toBe(true);
  });

  test('B sees the Folders tab badge in the nav', async () => {
    await pageB.goto(DEVICE_B);

    // The Folders nav link should have a pulsing badge when pendingFolders > 0.
    // Badge is a span sibling to the Folders NavLink with class animate-pulse.
    const foldersBadge = pageB.locator(
      'nav a[href="/folders"] ~ span.animate-pulse, nav span:has(a[href="/folders"]) span.animate-pulse',
    );
    await expect(foldersBadge.first()).toBeVisible({ timeout: 10_000 });
  });

  test('B sees the invite card on the Folders page', async () => {
    await pageB.goto(`${DEVICE_B}/folders`);

    // The invite section heading
    await expect(pageB.getByText('FOLDER INVITES')).toBeVisible({ timeout: 10_000 });

    // The folder name appears in the invite card header
    await expect(
      pageB
        .getByRole('heading', { name: 'Invite Test' })
        .or(pageB.locator('.text-sm.font-semibold', { hasText: 'Invite Test' }).first()),
    ).toBeVisible();

    // "Choose where to save" button exists
    await expect(pageB.getByRole('button', { name: /choose where to save/i })).toBeVisible();
  });
});
