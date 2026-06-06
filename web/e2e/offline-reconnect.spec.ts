/**
 * E2E: Offline → online state consistency
 *
 * Tests that state is correct after a device goes offline and comes back.
 * Uses WeSync restart to simulate offline/online.
 *
 * Key scenarios:
 *   Trust:  C offline → A removes trust → C restarts → both clean (Hello re-establishes)
 *   Folder: C offline → A removes C from folder → C restarts → C loses the folder
 */

import { test, expect } from '@playwright/test';
import { spawn, execSync } from 'child_process';
import * as net from 'net';
import path from 'path';
import fs from 'fs';
import {
  DEVICE_A,
  DEVICE_C,
  getStatus,
  getDevices,
  getFolders,
  getPendingFolders,
  pair,
  shareFolder,
  acceptFolder,
  cleanAll,
  apiPort,
  peerPort,
  stHome,
} from './helpers';

const BASE_A = DEVICE_A;
const BASE_C = DEVICE_C;
const FOLDER_A = path.resolve(process.cwd(), '../testdata/e2e-folder-a');
const FOLDER_C = path.resolve(process.cwd(), '../testdata/e2e-folder-c');
const TESTDATA = path.resolve(process.cwd(), '../testdata');
const WESYNC = path.resolve(process.cwd(), '../wesync.exe');

function readSTKey(p: string) {
  try {
    return fs.readFileSync(p, 'utf8').match(/<apikey>([^<]+)<\/apikey>/)?.[1] ?? '';
  } catch {
    return '';
  }
}
const ST3_KEY = readSTKey(path.join(TESTDATA, 'syncthing3-home/config.xml'));

function killByPort(port: number) {
  try {
    const out = execSync('netstat -ano', { stdio: 'pipe', windowsHide: true }).toString();
    for (const line of out.split(/\r?\n/)) {
      if (!line.includes('LISTENING')) continue;
      if (!line.includes(`:${port} `) && !line.includes(`:${port}\t`)) continue;
      const pid = line.trim().split(/\s+/).pop();
      if (pid && /^\d+$/.test(pid)) {
        try {
          execSync(`taskkill /F /PID ${pid}`, { stdio: 'pipe', windowsHide: true });
        } catch {}
      }
    }
  } catch {}
}

async function waitForPort(port: number, ms = 15_000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
    const ok = await new Promise<boolean>((resolve) => {
      const s = net.createConnection(port, '127.0.0.1');
      s.setTimeout(400);
      s.on('connect', () => {
        s.destroy();
        resolve(true);
      });
      s.on('error', () => {
        s.destroy();
        resolve(false);
      });
      s.on('timeout', () => {
        s.destroy();
        resolve(false);
      });
    });
    if (ok) return;
    await new Promise((r) => setTimeout(r, 300));
  }
  throw new Error(`Port ${port} not up after ${ms}ms`);
}

async function restartC() {
  killByPort(apiPort(2));
  await new Promise((r) => setTimeout(r, 800));
  spawn(
    WESYNC,
    [
      '--syncthing-url=http://127.0.0.1:8388',
      `--syncthing-key=${ST3_KEY}`,
      `--syncthing-home=${stHome(2)}`,
      `--port=${apiPort(2)}`,
      `--peer-port=${peerPort(2)}`,
      `--db=${path.join(TESTDATA, 'wesync3.db')}`,
      '--debug',
    ],
    { detached: true, stdio: 'ignore', windowsHide: true },
  ).unref();
  await waitForPort(apiPort(2));
  await new Promise((r) => setTimeout(r, 1000)); // let wire reconnect
}

test.describe.serial('Offline → online: A↔C', () => {
  let idA: string, idC: string;
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
    console.log(`A=${idA.slice(0, 7)}  C=${idC.slice(0, 7)}`);
  });

  test.afterAll(async () => {
    await cleanAll([
      [pageA, BASE_A],
      [pageC, BASE_C],
    ]).catch(() => {});
    await pageA?.close();
    await pageC?.close();
  });

  // ── Trust offline scenario ─────────────────────────────────────────────────

  test('T1: A trusts C, C goes offline, A removes trust, C comes back → both clean', async () => {
    // Setup: mutual trust
    await new Promise((r) => setTimeout(r, 1000));
    await pair(pageA, BASE_A, idC, 'C');
    await new Promise((r) => setTimeout(r, 500));
    await pair(pageC, BASE_C, idA, 'A');
    await expect
      .poll(
        async () => {
          const devA = await getDevices(pageA, BASE_A);
          const devC = await getDevices(pageC, BASE_C);
          return (
            devA.some((d: any) => d.deviceID === idC && d.accepted) &&
            devC.some((d: any) => d.deviceID === idA && d.accepted)
          );
        },
        { timeout: 15_000 },
      )
      .toBe(true);
    console.log('  ✓ Mutual trust established');

    // C goes offline
    killByPort(apiPort(2));
    await new Promise((r) => setTimeout(r, 1000));

    // A removes trust while C is offline
    await pageA.request.delete(`${BASE_A}/api/devices?id=${encodeURIComponent(idC)}`);
    await expect
      .poll(
        async () => {
          const devA = await getDevices(pageA, BASE_A);
          return !devA.some((d: any) => d.deviceID === idC);
        },
        { timeout: 5_000 },
      )
      .toBe(true);
    console.log('  ✓ A removed C while offline');

    // C comes back online
    await restartC();
    await pageC.goto(BASE_C);
    await new Promise((r) => setTimeout(r, 2000));

    // A: C should not be trusted (A removed during offline)
    const devA = await getDevices(pageA, BASE_A);
    expect(
      devA.some((d: any) => d.deviceID === idC),
      'A: C not trusted',
    ).toBe(false);

    // C: A should not be trusted (C gets trusted=false from A on reconnect)
    await expect
      .poll(
        async () => {
          const devC = await getDevices(pageC, BASE_C);
          return !devC.some((d: any) => d.deviceID === idA);
        },
        { timeout: 10_000 },
      )
      .toBe(true);

    console.log('T1 ✓ — trust removed offline, both clean after reconnect');
  });

  // ── Folder offline scenario ────────────────────────────────────────────────

  test('T2: A shares folder with C, C goes offline, A removes C, C comes back → C loses folder', async () => {
    // Setup: mutual trust + shared folder
    // Allow async goroutines from T1 (e.g. untrustDevice) to settle before modifying state.
    await new Promise((r) => setTimeout(r, 3000));
    await pair(pageA, BASE_A, idC, 'C');
    await new Promise((r) => setTimeout(r, 500));
    await pair(pageC, BASE_C, idA, 'A');
    await expect
      .poll(
        async () => {
          const devA = await getDevices(pageA, BASE_A);
          const devC = await getDevices(pageC, BASE_C);
          return (
            devA.some((d: any) => d.deviceID === idC && d.accepted) &&
            devC.some((d: any) => d.deviceID === idA && d.accepted)
          );
        },
        { timeout: 15_000 },
      )
      .toBe(true);

    await shareFolder(pageA, BASE_A, FOLDER_A, 'OfflineTest', 'sendreceive', idC);
    const fA = await getFolders(pageA, BASE_A);
    const folderID = fA.find((f: any) => f.label === 'OfflineTest')?.id ?? '';
    expect(folderID).not.toBe('');

    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageC, BASE_C);
          return p.some((pf: any) => pf.folderID === folderID);
        },
        { timeout: 15_000 },
      )
      .toBe(true);
    await acceptFolder(pageC, BASE_C, folderID, idA, FOLDER_C);
    await expect
      .poll(
        async () => {
          const fc = await getFolders(pageC, BASE_C);
          return fc.some((f: any) => f.id === folderID);
        },
        { timeout: 10_000 },
      )
      .toBe(true);
    console.log('  ✓ Folder shared and accepted');

    // C goes offline
    killByPort(apiPort(2));
    await new Promise((r) => setTimeout(r, 1000));

    // A removes C from folder while C is offline
    await pageA.request.delete(
      `${BASE_A}/api/folder/device?folderID=${encodeURIComponent(folderID)}&deviceID=${encodeURIComponent(idC)}`,
    );
    await expect
      .poll(
        async () => {
          const fa = await getFolders(pageA, BASE_A);
          return !fa.find((f: any) => f.id === folderID)?.deviceIDs?.includes(idC);
        },
        { timeout: 5_000 },
      )
      .toBe(true);
    console.log('  ✓ A removed C from folder while offline');

    // C comes back online
    await restartC();
    await pageC.goto(BASE_C);

    // C should lose the folder (via wire FolderRemove on reconnect OR via ST BEP).
    // After a prior test run there may be extra BEP churn — allow up to 30s.
    await expect
      .poll(
        async () => {
          const fc = await getFolders(pageC, BASE_C);
          return !fc.some((f: any) => f.id === folderID);
        },
        { timeout: 30_000, intervals: [500] },
      )
      .toBe(true);

    console.log('T2 ✓ — folder removed offline, C loses it after reconnect');
  });
});
