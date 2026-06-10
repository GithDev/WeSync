/**
 * E2E: ST Introducer auto-trust via shared folder
 *
 * When C (who has A as a trusted device with Introducer=true) adds A to a
 * folder that B already has, B automatically trusts A via ST Introducer.
 * This replaced the old FolderMembers "other device" → explicit trust flow.
 *
 * Scenario: B↔C paired. B has folder. C↔A paired. C adds A to folder.
 * ST Introducer makes B auto-trust A — no explicit pairing needed.
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

const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');
const FOLDER_C_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-c');

test.describe.serial('Other device → trusted member', () => {
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

    // Clean slate
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

  test('1. B↔C pair, C↔A pair', async () => {
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

    await expect
      .poll(
        async () => {
          const [dC, dA] = await Promise.all([
            getDevices(pageC, DEVICE_C),
            getDevices(pageA, DEVICE_A),
          ]);
          if (dC.some((x) => x.deviceID === idA) && dA.some((x) => x.deviceID === idC)) return true;
          await pair(pageC, DEVICE_C, idA, 'A');
          await pair(pageA, DEVICE_A, idC, 'C');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    console.log('✓ B↔C and C↔A paired');
  });

  test('2. B creates folder, C accepts, C adds A → ST Introducer auto-trusts A on B', async () => {
    // Patch explicit BEP addresses so Introducer (C introduces A to B) works
    // within the test timeout — mDNS on loopback takes too long.
    await Promise.all([
      forceBEPAddress(pageB, 1, idC, 2),
      forceBEPAddress(pageC, 2, idB, 1),
      forceBEPAddress(pageC, 2, idA, 0),
      forceBEPAddress(pageA, 0, idC, 2),
    ]);

    await shareFolder(pageB, DEVICE_B, FOLDER_B_PATH, 'MESH Test', 'sendreceive', idC);
    const fB = await getFolders(pageB, DEVICE_B);
    folderID = fB.find((f) => f.label === 'MESH Test')?.id ?? '';
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

    // C adds A to folder — ST Introducer (C is Introducer in B's view) auto-trusts A on B
    const cFolders = await getFolders(pageC, DEVICE_C);
    const cFolder = cFolders.find((f) => f.id === folderID)!;
    await shareFolder(pageC, DEVICE_C, cFolder.path, cFolder.label, cFolder.type, idA);

    // A appears in B's folder device list via ST Introducer
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA, 60_000);

    // With Introducer=true, A is also auto-trusted by B (no explicit pairing needed)
    await expect
      .poll(
        async () => {
          const dB = await getDevices(pageB, DEVICE_B);
          return dB.some((d) => d.deviceID === idA);
        },
        { message: 'B auto-trusts A via ST Introducer', timeout: 30_000, intervals: [1000] },
      )
      .toBe(true);

    // A is in the folder AND trusted — appears as full member, not "other device"
    const foldersB = await getFolders(pageB, DEVICE_B);
    const folder = foldersB.find((f) => f.id === folderID)!;
    expect(folder.deviceIDs).toContain(idA);

    console.log('✓ A is auto-trusted by B via ST Introducer — no manual pairing needed');
  });

  test('3. Verify full state: A is trusted on B and visible in folder', async () => {
    // A is trusted by B (confirmed in test 2 via Introducer)
    const dB = await getDevices(pageB, DEVICE_B);
    expect(dB.some((d) => d.deviceID === idA)).toBe(true);

    // A is in B's folder device list
    const foldersB = await getFolders(pageB, DEVICE_B);
    const folder = foldersB.find((f) => f.id === folderID)!;
    expect(folder).toBeTruthy();
    expect(folder.deviceIDs).toContain(idA);

    const allDevicesB = await pageB.request.get(`${DEVICE_B}/api/devices`).then((r) => r.json());
    const aDevice = allDevicesB.find(
      (d: { deviceID: string; stPaired: boolean }) => d.deviceID === idA,
    );
    expect(aDevice).toBeTruthy();
    expect(aDevice.stPaired).toBe(true);

    // Note: A has a pending folder invite (C added A) but hasn't accepted yet —
    // that's a separate user action. The Introducer auto-trust is already complete.
    const pendingOnA = await pageA.request
      .get(`${DEVICE_A}/api/folders/pending`)
      .then((r) => r.json());
    const hasPending = (pendingOnA as any[]).some((p: any) => p.folderID === folderID);
    console.log(`  A has pending folder invite: ${hasPending} (would accept to join folder)`);

    console.log("✓ A is trusted AND visible in B's folder — Introducer auto-trust complete");
  });
});
