/**
 * E2E: Folder invite state machine — A↔C trusted, folder invite lifecycle.
 *
 * Builds on trust being established. Tests:
 *   A invites C → C sees pending → C declines → A's folder loses C, C has no pending
 *   A invites C again → C accepts  → both have folder
 *   A removes C from folder → C loses folder
 */

import { test, expect } from '@playwright/test';
import {
  DEVICE_A,
  DEVICE_C,
  getStatus,
  getFolders,
  getPendingFolders,
  pair,
  shareFolder,
  acceptFolder,
  forceBEPAddress,
  cleanAll,
} from './helpers';
import path from 'path';

const BASE_A = DEVICE_A;
const BASE_C = DEVICE_C;

const FOLDER_A = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_C = path.resolve(process.cwd(), '../testdata/e2e-folder-c');

async function declineFolder(
  page: import('@playwright/test').Page,
  base: string,
  folderID: string,
  fromDeviceID: string,
) {
  await page.request.post(`${base}/api/folder/decline`, {
    data: { folderID, deviceID: fromDeviceID },
  });
}

async function removeDeviceFromFolder(
  page: import('@playwright/test').Page,
  base: string,
  folderID: string,
  deviceID: string,
) {
  await page.request.delete(
    `${base}/api/folder/device?folderID=${encodeURIComponent(folderID)}&deviceID=${encodeURIComponent(deviceID)}`,
  );
}

test.describe.serial('Folder invite flow: A↔C', () => {
  let idA: string, idC: string;
  let folderID: string;
  let pageA: import('@playwright/test').Page;
  let pageC: import('@playwright/test').Page;

  test.beforeAll(async ({ browser }) => {
    pageA = await browser.newPage();
    pageC = await browser.newPage();
    await Promise.all([pageA.goto(BASE_A), pageC.goto(BASE_C)]);
    const [sA, sC] = await Promise.all([getStatus(pageA, BASE_A), getStatus(pageC, BASE_C)]);
    idA = sA.myID;
    idC = sC.myID;
    await cleanAll([
      [pageA, BASE_A],
      [pageC, BASE_C],
    ]);

    // Establish mutual trust
    await new Promise((r) => setTimeout(r, 1000));
    await pair(pageA, BASE_A, idC, 'C');
    await new Promise((r) => setTimeout(r, 500));
    await pair(pageC, BASE_C, idA, 'A');

    await expect
      .poll(
        async () => {
          const [fA, fC] = await Promise.all([
            pageA.request.get(`${BASE_A}/api/devices`).then((r) => r.json()),
            pageC.request.get(`${BASE_C}/api/devices`).then((r) => r.json()),
          ]);
          return (
            fA.some((d: any) => d.deviceID === idC && d.accepted) &&
            fC.some((d: any) => d.deviceID === idA && d.accepted)
          );
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    // Patch explicit BEP addresses so folder invites arrive quickly.
    // The 15s timeout in step 1 is too short for mDNS on loopback.
    await Promise.all([
      forceBEPAddress(pageA, 0, idC, 2),
      forceBEPAddress(pageC, 2, idA, 0),
    ]);

    console.log(`A=${idA.slice(0, 7)}  C=${idC.slice(0, 7)}  — mutual trust confirmed`);
  });

  test.afterAll(async () => {
    await cleanAll([
      [pageA, BASE_A],
      [pageC, BASE_C],
    ]).catch(() => {});
    await pageA?.close();
    await pageC?.close();
  });

  // ── Step 1: A shares folder ────────────────────────────────────────────────

  test('1. A shares folder with C — C sees pending invite', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await shareFolder(pageA, BASE_A, FOLDER_A, 'Shared', 'sendreceive', idC);

    const fA = await getFolders(pageA, BASE_A);
    folderID = fA.find((f: any) => f.label === 'Shared')?.id ?? '';
    expect(folderID, 'folder created on A').not.toBe('');

    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageC, BASE_C);
          return pending.some((p: any) => p.folderID === folderID);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    console.log('Step 1 ✓ — C sees pending invite for folder');
  });

  // ── Step 2: C declines ─────────────────────────────────────────────────────

  test('2. C declines — pending gone, A folder loses C', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await declineFolder(pageC, BASE_C, folderID, idA);

    // C: no more pending
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageC, BASE_C);
          return !pending.some((p: any) => p.folderID === folderID);
        },
        { timeout: 10_000, intervals: [300] },
      )
      .toBe(true);

    // A: C removed from folder device list
    await expect
      .poll(
        async () => {
          const folders = await getFolders(pageA, BASE_A);
          const f = folders.find((f: any) => f.id === folderID);
          return f ? !f.deviceIDs?.includes(idC) : false;
        },
        { timeout: 10_000, intervals: [300] },
      )
      .toBe(true);

    console.log('Step 2 ✓ — C declined: pending cleared, A folder no longer includes C');
  });

  // ── Step 3: A invites again, C accepts ────────────────────────────────────

  test('3. A re-invites, C accepts — both have folder', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await shareFolder(pageA, BASE_A, FOLDER_A, 'Shared', 'sendreceive', idC);

    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageC, BASE_C);
          return pending.some((p: any) => p.folderID === folderID);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    await new Promise((r) => setTimeout(r, 500));
    await acceptFolder(pageC, BASE_C, folderID, idA, FOLDER_C);

    await expect
      .poll(
        async () => {
          const fC = await getFolders(pageC, BASE_C);
          return fC.some((f: any) => f.id === folderID);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    await expect
      .poll(
        async () => {
          const fA = await getFolders(pageA, BASE_A);
          return fA.find((f: any) => f.id === folderID)?.deviceIDs?.includes(idC);
        },
        { timeout: 10_000, intervals: [300] },
      )
      .toBe(true);

    console.log('Step 3 ✓ — Both A and C have the folder');
  });

  // ── Step 4: A removes C from folder ───────────────────────────────────────

  test('4. A removes C from folder — C loses folder', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await removeDeviceFromFolder(pageA, BASE_A, folderID, idC);

    await expect
      .poll(
        async () => {
          const fA = await getFolders(pageA, BASE_A);
          return !fA.find((f: any) => f.id === folderID)?.deviceIDs?.includes(idC);
        },
        { timeout: 10_000, intervals: [300] },
      )
      .toBe(true);

    await expect
      .poll(
        async () => {
          const fC = await getFolders(pageC, BASE_C);
          return !fC.some((f: any) => f.id === folderID);
        },
        { timeout: 15_000, intervals: [500] },
      )
      .toBe(true);

    console.log('Step 4 ✓ — A removed C: C no longer has the folder');
  });
});
