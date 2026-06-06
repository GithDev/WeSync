/**
 * E2E: Folder ignore patterns
 *
 * Verifies that files matching ignore patterns are NOT synced between devices.
 *
 * Scenario:
 *   1. A shares folder with B, setting *.log as ignore pattern
 *   2. A creates test.txt (should sync) and test.log (should NOT sync)
 *   3. Verify test.txt appears on B but test.log does not
 *
 * Requires dev.ps1 to be running.
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import fs from 'fs';
import {
  DEVICE_A,
  DEVICE_B,
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
  getPendingFolders,
  cleanAll,
} from './helpers';

const FOLDER_A_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_B_PATH = path.resolve(process.cwd(), '../testdata/e2e-folder-b');

test.describe.serial('Folder ignore patterns', () => {
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
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
    ]);

    // Ensure test directories exist
    fs.mkdirSync(FOLDER_A_PATH, { recursive: true });
    fs.mkdirSync(FOLDER_B_PATH, { recursive: true });

    // Clean up any existing test files
    for (const dir of [FOLDER_A_PATH, FOLDER_B_PATH]) {
      try {
        for (const f of fs.readdirSync(dir)) {
          fs.unlinkSync(path.join(dir, f));
        }
      } catch {
        /* ignore */
      }
    }

    await waitForPeer(pageA, DEVICE_A, idB);
    await waitForPeer(pageB, DEVICE_B, idA);

    // Wait for socket Hellos
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
    await Promise.all([waitForHello(pageA, DEVICE_A, idB), waitForHello(pageB, DEVICE_B, idA)]);
    await new Promise((r) => setTimeout(r, 1000));
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
  });

  test('1. A trusts B', async () => {
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          if (d.some((x: { deviceID: string }) => x.deviceID === idB)) return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 30_000, intervals: [3000] },
      )
      .toBe(true);
    await waitForDevice(pageB, DEVICE_B, idA);
  });

  test('2. A shares folder with *.log ignore pattern', async () => {
    // Share the folder
    const result = await shareFolder(
      pageA,
      DEVICE_A,
      FOLDER_A_PATH,
      'Ignores Test',
      'sendreceive',
      idB,
    );
    const fA = await getFolders(pageA, DEVICE_A);
    folderID = fA.find((f: { label: string }) => f.label === 'Ignores Test')?.id ?? '';
    expect(folderID).not.toBe('');

    // Set ignore patterns before B accepts
    await pageA.request.post(`${DEVICE_A}/api/folder/ignores?id=${encodeURIComponent(folderID)}`, {
      data: { patterns: ['*.log', '*.tmp'] },
    });

    // B accepts the folder
    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p: { folderID: string }) => p.folderID === folderID);
        },
        { message: 'waiting for B to see folder offer', timeout: 60_000, intervals: [2000] },
      )
      .toBe(true);

    await acceptFolder(pageB, DEVICE_B, folderID, idA, FOLDER_B_PATH);
    await waitForFolderDevice(pageA, DEVICE_A, folderID, idB);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);

    void result;
  });

  test('3. Ignored files do not sync; non-ignored files do', async () => {
    const syncFile = `sync-${Date.now()}.txt`;
    const ignoreFile = `ignore-${Date.now()}.log`;
    const ignoreTmpFile = `ignore-${Date.now()}.tmp`;

    // Write both files on A's side
    fs.writeFileSync(path.join(FOLDER_A_PATH, syncFile), 'should sync');
    fs.writeFileSync(path.join(FOLDER_A_PATH, ignoreFile), 'should NOT sync');
    fs.writeFileSync(path.join(FOLDER_A_PATH, ignoreTmpFile), 'should NOT sync either');

    // .txt file SHOULD appear on B
    await expect
      .poll(() => fs.existsSync(path.join(FOLDER_B_PATH, syncFile)), {
        message: `waiting for ${syncFile} to sync to B`,
        timeout: 60_000,
      })
      .toBe(true);

    // Wait a bit more to ensure ignored files would have synced if they were going to
    await new Promise((r) => setTimeout(r, 5000));

    // .log file should NOT appear on B
    expect(fs.existsSync(path.join(FOLDER_B_PATH, ignoreFile))).toBe(false);
    expect(fs.existsSync(path.join(FOLDER_B_PATH, ignoreTmpFile))).toBe(false);

    console.log(`✓ ${syncFile} synced, *.log and *.tmp correctly ignored`);
  });

  test('4. Verify ignore patterns are persisted via API', async () => {
    const res = await pageA.request.get(
      `${DEVICE_A}/api/folder/ignores?id=${encodeURIComponent(folderID)}`,
    );
    const data = (await res.json()) as { patterns: string[] };
    expect(data.patterns).toContain('*.log');
    expect(data.patterns).toContain('*.tmp');
  });
});
