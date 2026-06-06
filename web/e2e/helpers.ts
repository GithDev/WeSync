import { Page, expect } from '@playwright/test';
import path from 'path';
import fs from 'fs';
import { execSync, spawn } from 'child_process';
import * as net from 'net';

// ── Port scheme — single source of truth ─────────────────────────────────────
// One base per service; each instance's port is base + index (i = 0 → A, 1 → B,
// 2 → C). Every spec derives its ports from these helpers — never hard-code a
// port literal. Kept off the very common 8080 to avoid clashing with other dev
// servers; ST GUI/sync and peerwire bases are likewise sequential and isolated.
export const API_BASE = 8083; // WeSync HTTP API      → 8083 / 8084 / 8085
export const PEER_BASE = 47831; // peerwire WSS         → 47831 / 47832 / 47833
export const ST_GUI_BASE = 8386; // Syncthing GUI + REST → 8386 / 8387 / 8388
export const ST_SYNC_BASE = 23000; // Syncthing BEP listen → 23000 / 23001 / 23002

export const apiPort = (i: number): number => API_BASE + i;
export const peerPort = (i: number): number => PEER_BASE + i;
export const stGuiPort = (i: number): number => ST_GUI_BASE + i;
export const stSyncPort = (i: number): number => ST_SYNC_BASE + i;
export const deviceURL = (i: number): string => `http://localhost:${apiPort(i)}`;
export const stURL = (i: number): string => `http://127.0.0.1:${stGuiPort(i)}`;

export const DEVICE_A = deviceURL(0);
export const DEVICE_B = deviceURL(1);
export const DEVICE_C = deviceURL(2);

// ── ST API helpers ─────────────────────────────────────────────────────────────

const TESTDATA = path.resolve(process.cwd(), '../testdata');
const ST_EXE = path.join(TESTDATA, 'syncthing.exe');

function readSTKey(p: string): string {
  try {
    return fs.readFileSync(p, 'utf8').match(/<apikey>([^<]+)<\/apikey>/)?.[1] ?? '';
  } catch {
    return '';
  }
}

export const stHome = (i: number): string => path.join(TESTDATA, `syncthing${i + 1}-home`);

// All three test ST instances live entirely under testdata/ and bind to the
// non-default GUI ports above (none on Syncthing's default 8384). This keeps the
// test setup isolated from any personal Syncthing the developer might be running
// locally — previously ST1 used 8384 with no --home flag, which silently collided
// with personal ST and produced "403 / wrong device ID" failures.
const ST_INSTANCES = [0, 1, 2].map((i) => ({
  url: stURL(i),
  port: stGuiPort(i),
  key: readSTKey(path.join(stHome(i), 'config.xml')),
  args: ['serve', '--no-browser', `--home=${stHome(i)}`, `--gui-address=127.0.0.1:${stGuiPort(i)}`],
}));

function killByPort(port: number): void {
  try {
    const out = execSync('netstat -ano', {
      stdio: 'pipe',
      timeout: 3000,
      windowsHide: true,
    }).toString();
    for (const line of out.split(/\r?\n/)) {
      if (!line.includes('LISTENING')) continue;
      if (!line.includes(`:${port} `) && !line.includes(`:${port}\t`)) continue;
      const pid = line.trim().split(/\s+/).pop();
      if (pid && /^\d+$/.test(pid)) {
        try {
          execSync(`taskkill /F /PID ${pid}`, { stdio: 'pipe', timeout: 3000, windowsHide: true });
        } catch {}
      }
    }
  } catch {}
}

async function waitForPort(port: number, timeoutMs = 30_000): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
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
    if (ok) return true;
    await new Promise((r) => setTimeout(r, 300));
  }
  return false;
}

/**
 * Restarts all three ST instances and waits for them to come back up.
 *
 * Use this after device/folder cleanup when tests rely on ST's Introducer
 * mechanism. ST caches "skipped re-introduction" state in memory — a restart
 * clears that cache so Introducer works cleanly on the next pairing cycle.
 */
export async function restartAllSyncthing(_page?: Page): Promise<void> {
  // 1. Kill ALL syncthing instances
  try {
    execSync('taskkill /F /IM syncthing.exe', { stdio: 'pipe', timeout: 5000, windowsHide: true });
  } catch {}
  // 2. Wait for processes to fully exit
  await new Promise((r) => setTimeout(r, 3000));
  // 3. Start all ST instances fresh (same config as dev.ps1)
  for (const { port, args } of ST_INSTANCES) {
    if (!fs.existsSync(ST_EXE)) break;
    spawn(ST_EXE, args, { detached: true, stdio: 'ignore', windowsHide: true }).unref();
    void port; // started; wait below
  }
  // 4. Wait for each port to be up (same as dev.ps1 Test-Port check)
  for (const { url, port } of ST_INSTANCES) {
    const ok = await waitForPort(port, 30_000);
    if (!ok) console.log(`  ⚠ restartAllSyncthing: ${url} did not come up within 30s`);
  }
  // 5. Wait for WeSync to reconnect — poll /api/folders until non-null
  await Promise.all(
    [DEVICE_A, DEVICE_B, DEVICE_C].map(async (base) => {
      const deadline = Date.now() + 15_000;
      while (Date.now() < deadline) {
        try {
          const r = await fetch(`${base}/api/folders`);
          if (r.ok) {
            const body = await r.json();
            if (body !== null) return;
          }
        } catch {}
        await new Promise((r) => setTimeout(r, 300));
      }
    }),
  );
}

// â”€â”€ Types â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

interface Folder {
  id: string;
  label: string;
  path: string;
  type: string;
  deviceIDs: string[];
  deviceTypes?: Record<string, string>;
  deviceAccepted?: Record<string, boolean>;
}

interface Device {
  deviceID: string;
  name: string;
  connected: boolean;
  stPaired?: boolean;
  accepted?: boolean;
}

interface Status {
  myID: string;
  name: string;
}

interface Peer {
  deviceID: string;
  name: string;
}

// â”€â”€ API helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

export async function getStatus(page: Page, base: string): Promise<Status> {
  const res = await page.request.get(`${base}/api/status`);
  return res.json();
}

export async function getDevices(page: Page, base: string): Promise<Device[]> {
  const res = await page.request.get(`${base}/api/devices`);
  return res.json();
}

export async function getPeers(page: Page, base: string): Promise<Peer[]> {
  const res = await page.request.get(`${base}/api/peers`);
  return res.json();
}

export async function getIncoming(
  page: Page,
  base: string,
): Promise<{ deviceID: string; name: string }[]> {
  const res = await page.request.get(`${base}/api/incoming`);
  return res.json();
}

export async function setName(page: Page, base: string, name: string) {
  await page.request.fetch(`${base}/api/name`, { method: 'PUT', data: { name } });
}

// Wait until a specific deviceID shows the expected name in the devices list.
export async function waitForDeviceName(
  page: Page,
  base: string,
  deviceID: string,
  expectedName: string,
  timeout = 10_000,
) {
  await expect
    .poll(
      async () => {
        const devices = await getDevices(page, base);
        return devices.find((d) => d.deviceID === deviceID)?.name;
      },
      {
        message: `waiting for device ${deviceID.slice(0, 7)} to have name "${expectedName}" on ${base}`,
        timeout,
      },
    )
    .toBe(expectedName);
}

// Wait until a specific deviceID shows the expected name in the peers list (unpaired).
export async function waitForPeerName(
  page: Page,
  base: string,
  deviceID: string,
  expectedName: string,
  timeout = 20_000,
) {
  await expect
    .poll(
      async () => {
        const peers = await getPeers(page, base);
        return peers.find((p) => p.deviceID === deviceID)?.name;
      },
      {
        message: `waiting for peer ${deviceID.slice(0, 7)} to have name "${expectedName}" on ${base}`,
        timeout,
      },
    )
    .toBe(expectedName);
}

export async function getFolders(page: Page, base: string): Promise<Folder[]> {
  const res = await page.request.get(`${base}/api/folders`);
  return res.json();
}

// Pair: A initiates, B accepts (or mutual â€” WeSync handles incoming/outgoing)
export async function pair(page: Page, fromBase: string, toDeviceID: string, toName: string) {
  await page.request.post(`${fromBase}/api/pair`, {
    data: { deviceID: toDeviceID, name: toName },
  });
}

// Share or add device to existing folder (same endpoint, idempotent on path).
// Returns the folderID so callers can avoid the race between POST returning
// and the next GET /api/folders observing the new folder. Backend has always
// returned this — tests just weren't using it, which produced flaky
// `fA[0].id` reads.
export async function shareFolder(
  page: Page,
  base: string,
  path: string,
  label: string,
  direction: string,
  deviceID = '',
): Promise<string> {
  const res = await page.request.post(`${base}/api/folder/share`, {
    data: { path, label, direction, deviceID },
  });
  if (!res.ok()) {
    throw new Error(`shareFolder failed: ${res.status()} ${await res.text()}`);
  }
  const body = (await res.json()) as { folderID?: string };
  return body.folderID ?? '';
}

export async function addDeviceToFolder(
  page: Page,
  base: string,
  folderPath: string,
  folderLabel: string,
  folderType: string,
  deviceID: string,
) {
  await shareFolder(page, base, folderPath, folderLabel, folderType, deviceID);
}

export async function acceptFolder(
  page: Page,
  base: string,
  folderID: string,
  deviceID: string,
  localPath: string,
) {
  await page.request.post(`${base}/api/folder/accept`, {
    data: { folderID, deviceID, path: localPath },
  });
}

export async function removeDeviceFromFolder(
  page: Page,
  base: string,
  folderID: string,
  deviceID: string,
) {
  await page.request.delete(`${base}/api/folder/device?folderID=${folderID}&deviceID=${deviceID}`);
}

export async function getPendingFolders(
  page: Page,
  base: string,
): Promise<{ folderID: string; deviceID: string; label: string }[]> {
  const res = await page.request.get(`${base}/api/folders/pending`);
  return res.json();
}

export async function refreshPendingFolders(page: Page, base: string) {
  await page.request.get(`${base}/api/folders/pending`);
}

export async function setFolderPaused(page: Page, base: string, folderID: string, paused: boolean) {
  await page.request.fetch(`${base}/api/folder/pause`, {
    method: 'PATCH',
    data: { folderID, paused },
  });
}

export async function getFolderStatus(page: Page, base: string, folderID: string) {
  const res = await page.request.get(
    `${base}/api/folder/status?id=${encodeURIComponent(folderID)}`,
  );
  return res.json() as Promise<{
    state: string;
    paused: boolean;
    needFiles: number;
    globalFiles: number;
  }>;
}

export async function updateFolderDirection(
  page: Page,
  base: string,
  folderID: string,
  direction: string,
) {
  await page.request.fetch(`${base}/api/folder/direction`, {
    method: 'PATCH',
    data: { folderID, direction },
  });
}

export async function waitForFolderDeviceType(
  page: Page,
  base: string,
  folderID: string,
  deviceID: string,
  direction: string,
  timeout = 10_000,
) {
  await expect
    .poll(
      async () => {
        const folders = await getFolders(page, base);
        const folder = folders.find((f) => f.id === folderID);
        return folder?.deviceTypes?.[deviceID] === direction;
      },
      {
        message: `waiting for device ${deviceID.slice(0, 7)} to show direction ${direction} on ${base}`,
        timeout,
      },
    )
    .toBe(true);
}

// â”€â”€ Polling helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

export async function waitForPeer(page: Page, base: string, deviceID: string) {
  await expect
    .poll(
      async () => {
        const peers = await getPeers(page, base);
        return peers.some((p) => p.deviceID === deviceID);
      },
      { message: `waiting for peer ${deviceID.slice(0, 7)} on ${base}`, timeout: 15_000 },
    )
    .toBe(true);
}

export async function waitForDevice(page: Page, base: string, deviceID: string, timeout = 30_000) {
  await expect
    .poll(
      async () => {
        const devices = await getDevices(page, base);
        return devices.some((d) => d.deviceID === deviceID);
      },
      { message: `waiting for device ${deviceID.slice(0, 7)} on ${base}`, timeout },
    )
    .toBe(true);
}

export async function waitForFolder(page: Page, base: string, folderID: string) {
  await expect
    .poll(
      async () => {
        const folders = await getFolders(page, base);
        return folders.some((f) => f.id === folderID);
      },
      { message: `waiting for folder ${folderID} on ${base}`, timeout: 15_000 },
    )
    .toBe(true);
}

export async function waitForDeviceAccepted(
  page: Page,
  base: string,
  folderID: string,
  deviceID: string,
  accepted: boolean,
  timeout = 15_000,
) {
  await expect
    .poll(
      async () => {
        const folders = await getFolders(page, base);
        const folder = folders.find((f) => f.id === folderID);
        const status = folder?.deviceAccepted?.[deviceID];
        return accepted ? status === true : status !== true;
      },
      {
        message: `waiting for device ${deviceID.slice(0, 7)} accepted=${accepted} on ${base}`,
        timeout,
      },
    )
    .toBe(true);
}

export async function waitForFolderDevice(
  page: Page,
  base: string,
  folderID: string,
  deviceID: string,
  timeout = 15_000,
) {
  await expect
    .poll(
      async () => {
        const folders = await getFolders(page, base);
        const folder = folders.find((f) => f.id === folderID);
        return folder?.deviceIDs?.includes(deviceID) ?? false;
      },
      {
        message: `waiting for device ${deviceID.slice(0, 7)} in folder ${folderID} on ${base}`,
        timeout,
      },
    )
    .toBe(true);
}

export async function waitForFolderGone(
  page: Page,
  base: string,
  folderID: string,
  timeout = 15_000,
) {
  await expect
    .poll(
      async () => {
        const folders = await getFolders(page, base);
        return !folders.some((f) => f.id === folderID);
      },
      { message: `waiting for folder ${folderID} to disappear from ${base}`, timeout },
    )
    .toBe(true);
}

export async function waitForDeviceGoneFromFolder(
  page: Page,
  base: string,
  folderID: string,
  deviceID: string,
  timeout = 15_000,
) {
  await expect
    .poll(
      async () => {
        const folders = await getFolders(page, base);
        const folder = folders.find((f) => f.id === folderID);
        return folder ? !folder.deviceIDs?.includes(deviceID) : true;
      },
      {
        message: `waiting for device ${deviceID.slice(0, 7)} to leave folder ${folderID} on ${base}`,
        timeout,
      },
    )
    .toBe(true);
}

export async function waitForPendingFolder(page: Page, base: string, folderID: string) {
  await expect
    .poll(
      async () => {
        const pending = await getPendingFolders(page, base);
        return pending.some((p) => p.folderID === folderID);
      },
      { message: `waiting for pending folder ${folderID} on ${base}`, timeout: 15_000 },
    )
    .toBe(true);
}

// ── Clean slate helpers ───────────────────────────────────────────────────────

/**
 * cleanAll removes every folder, trusted device, and incoming trust request
 * from all supplied instances, then verifies via assertCleanState.
 *
 * Always include incoming dismissal — ST's pending list outlives WeSync's
 * trustedIDs map and was the source of ghost trust-requests in manual tests.
 */
export async function cleanAll(
  pages: [Page, string][],
  { settle = 1_500 }: { settle?: number } = {},
): Promise<void> {
  // Step 1: remove trusted devices on ALL instances in parallel.
  // This tears down BEP connections on all sides simultaneously, preventing
  // "A removed B, but B's ST reconnects before B is cleaned" race conditions.
  await Promise.all(
    pages.map(async ([page, base]) => {
      const devices = (await getDevices(page, base).catch(() => [])) ?? [];
      await Promise.all(
        (devices as any[]).map((d) =>
          page.request
            .delete(`${base}/api/devices?id=${encodeURIComponent(d.deviceID)}`)
            .catch(() => {}),
        ),
      );
    }),
  );

  // Step 2: remove all folders on ALL instances in parallel.
  await Promise.all(
    pages.map(async ([page, base]) => {
      const folders = (await getFolders(page, base).catch(() => [])) ?? [];
      await Promise.all(
        (folders as any[]).map((f) =>
          page.request.delete(`${base}/api/folder?id=${encodeURIComponent(f.id)}`).catch(() => {}),
        ),
      );
    }),
  );

  // Step 3: wait for BEP connections to settle, then dismiss incoming.
  // We retry up to 3 times because a device may briefly reconnect via BEP
  // and reappear in the pending list right after we dismiss it.
  await new Promise((r) => setTimeout(r, 800));
  for (let attempt = 0; attempt < 3; attempt++) {
    const allIncoming = await Promise.all(
      pages.map(async ([page, base]) => {
        const inc = await getIncoming(page, base).catch(() => []);
        return { page, base, inc: inc as any[] };
      }),
    );
    const anyLeft = allIncoming.some(({ inc }) => inc.length > 0);
    if (!anyLeft) break;
    await Promise.all(
      allIncoming.flatMap(({ page, base, inc }) =>
        inc.map((r) =>
          page.request
            .delete(`${base}/api/incoming?id=${encodeURIComponent(r.deviceID)}`)
            .catch(() => {}),
        ),
      ),
    );
    await new Promise((r) => setTimeout(r, 600));
  }

  // Step 4: reset visibility to on (announcing) — consistent baseline for tests.
  await Promise.all(
    pages.map(([page, base]) =>
      page.request
        .fetch(`${base}/api/mode`, { method: 'PUT', data: { visible: true } })
        .catch(() => {}),
    ),
  );

  await new Promise((r) => setTimeout(r, settle));
  await assertCleanState(pages);
}

/**
 * assertCleanState verifies that every instance has zero devices, folders,
 * and incoming trust requests. Throws with a descriptive message on failure.
 */
export async function assertCleanState(pages: [Page, string][]): Promise<void> {
  for (const [page, base] of pages) {
    const [devices, folders, incoming] = (
      await Promise.all([
        getDevices(page, base).catch(() => []),
        getFolders(page, base).catch(() => []),
        getIncoming(page, base).catch(() => []),
      ])
    ).map((r) => r ?? []);
    const problems: string[] = [];
    if ((devices as any[]).length)
      problems.push(
        `${(devices as any[]).length} device(s): ${(devices as any[]).map((d: any) => d.name).join(', ')}`,
      );
    if ((folders as any[]).length)
      problems.push(
        `${(folders as any[]).length} folder(s): ${(folders as any[]).map((f: any) => f.label).join(', ')}`,
      );
    if ((incoming as any[]).length)
      problems.push(
        `${(incoming as any[]).length} incoming: ${(incoming as any[]).map((i: any) => i.name ?? i.deviceID?.slice(0, 7)).join(', ')}`,
      );
    if (problems.length) {
      throw new Error(`assertCleanState failed on ${base}:\n  ${problems.join('\n  ')}`);
    }
  }
}
