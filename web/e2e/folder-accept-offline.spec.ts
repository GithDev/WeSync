/**
 * E2E: Folder acceptance must survive the inviter going offline-from-invitee.
 *
 * Scenario: A invites B, B accepts. A and B are syncing.
 * Then A's ST disconnects from B (simulating B going offline from A's view).
 * A's UI must still show B as "accepted" — not flip back to "Invited".
 *
 * The acceptance is a historical fact: B has the folder configured on their
 * side. We must not infer pending-status from a missing live BEP connection.
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
  getFolders,
  getPendingFolders,
  pair,
  waitForPeer,
  waitForDevice,
  shareFolder,
  acceptFolder,
  waitForFolderDevice,
  waitForDeviceAccepted,
  cleanAll,
} from './helpers';

const F1_A = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const F1_B = path.resolve(process.cwd(), '../testdata/e2e-folder-b');

// A's Syncthing instance — used to pause/resume B's device from A's perspective.
const ST1 = 'http://127.0.0.1:8386';

function readSTKey(configPath: string): string {
  try {
    const xml = fs.readFileSync(configPath, 'utf8');
    const m = xml.match(/<apikey>([^<]+)<\/apikey>/);
    return m?.[1] ?? '';
  } catch {
    return '';
  }
}

const ST1_KEY = readSTKey(path.resolve(process.cwd(), '../testdata/syncthing1-home/config.xml'));

test.describe.serial('Folder accept survives offline', () => {
  let idA: string;
  let idB: string;
  let folderID: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;

  // Assert deviceAccepted holds at the expected value over a window — catches
  // the bug where a single refresh tick flips the status incorrectly.
  async function assertAcceptedSticks(
    page: import('@playwright/test').Page,
    base: string,
    fid: string,
    deviceID: string,
    expected: boolean,
    durationMs = 3_000,
  ) {
    const deadline = Date.now() + durationMs;
    while (Date.now() < deadline) {
      const folders = await getFolders(page, base);
      const f = folders.find((ff) => ff.id === fid);
      const status = f?.deviceAccepted?.[deviceID];
      const actual = status === true;
      const ctx = `${base} folder=${fid.slice(0, 8)} device=${deviceID.slice(0, 7)} expected=${expected} got=${actual}`;
      expect(actual, ctx).toBe(expected);
      await new Promise((r) => setTimeout(r, 250));
    }
  }

  test.beforeAll(async ({ browser }) => {
    test.setTimeout(120_000);
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B)]);

    const [sA, sB] = await Promise.all([getStatus(pageA, DEVICE_A), getStatus(pageB, DEVICE_B)]);
    idA = sA.myID;
    idB = sB.myID;

    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);

    await Promise.all([waitForPeer(pageA, DEVICE_A, idB), waitForPeer(pageB, DEVICE_B, idA)]);
    await pair(pageA, DEVICE_A, idB, 'B');
    await pair(pageB, DEVICE_B, idA, 'A');
    await Promise.all([waitForDevice(pageA, DEVICE_A, idB), waitForDevice(pageB, DEVICE_B, idA)]);
  });

  test.afterAll(async () => {
    // Always resume B in case the test failed mid-pause.
    if (ST1_KEY && idB) {
      try {
        await pageA.request.post(`${ST1}/rest/system/resume?device=${idB}`, {
          headers: { 'X-API-Key': ST1_KEY },
        });
      } catch {
        /* ignore */
      }
    }
    await pageA?.close();
    await pageB?.close();
  });

  test('1. A shares folder with B and B accepts', async () => {
    folderID = await shareFolder(pageA, DEVICE_A, F1_A, 'Survive Offline', 'sendreceive', idB);
    expect(folderID).toBeTruthy();

    await expect
      .poll(
        async () => {
          const pending = await getPendingFolders(pageB, DEVICE_B);
          return pending.some((p) => p.folderID === folderID && p.deviceID === idA);
        },
        { message: 'waiting for B to see folder invite', timeout: 60_000 },
      )
      .toBe(true);

    await acceptFolder(pageB, DEVICE_B, folderID, idA, F1_B);

    await waitForDeviceAccepted(pageA, DEVICE_A, folderID, idB, true);
    await waitForFolderDevice(pageB, DEVICE_B, folderID, idA);
  });

  test("2. B goes offline (paused in A's ST) — A must still see B as accepted", async () => {
    expect(ST1_KEY, 'need ST1 API key to pause device').toBeTruthy();

    // Pause B's device on A's ST. This drops the BEP connection — equivalent
    // to B going offline from A's perspective.
    const pauseRes = await pageA.request.post(`${ST1}/rest/system/pause?device=${idB}`, {
      headers: { 'X-API-Key': ST1_KEY },
    });
    expect(pauseRes.ok(), 'pause request failed').toBeTruthy();

    // Wait a few refresh cycles — RefreshFolderCompletion runs on ST events,
    // typically every few seconds. Plenty of time for any flip to happen.
    await new Promise((r) => setTimeout(r, 4_000));

    // CRITICAL: B is offline, but B previously accepted the folder.
    // A's UI must NOT flip B back to "Invited" — acceptance is a historical
    // fact, not a live connection state.
    await assertAcceptedSticks(pageA, DEVICE_A, folderID, idB, true, 3_000);
  });

  test('3. Resume B — A still sees B as accepted (no regression)', async () => {
    const resumeRes = await pageA.request.post(`${ST1}/rest/system/resume?device=${idB}`, {
      headers: { 'X-API-Key': ST1_KEY },
    });
    expect(resumeRes.ok(), 'resume request failed').toBeTruthy();

    // Brief settle, then verify still accepted.
    await new Promise((r) => setTimeout(r, 3_000));
    await assertAcceptedSticks(pageA, DEVICE_A, folderID, idB, true, 2_000);
  });
});
