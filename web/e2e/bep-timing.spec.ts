/**
 * E2E: Comprehensive BEP propagation timing
 *
 * A↔B paired (Introducer=true), B↔C paired (Introducer=true).
 * B shares folder with A and C. Measures A↔C discovery via ST Introducer.
 *
 * Scenarios:
 *   1. WeSync shareFolder (FolderRefresh wire signal + Introducer) — baseline
 *   2. ST direct only (no wire signal) — pure BEP
 *   3. ST direct + manual ST pause/resume on B — BEP with forced reconnect
 *
 * Each scenario fully restarts all ST + WeSync instances for clean BEP state.
 * Process stdout/stderr is written to testdata/bep-*.log for debugging.
 *
 * Timing from T0 (B adds C to folder):
 *   T1: C sees pending folder invite
 *   T2: C accepts
 *   T3: A discovers C in trusted devices
 *   T4: C discovers A in trusted devices
 */

import { test, expect } from '@playwright/test';
import { spawn, execSync, ChildProcess } from 'child_process';
import path from 'path';
import fs from 'fs';
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
  waitForFolderDevice,
  apiPort,
  peerPort,
  stGuiPort,
  stSyncPort,
  stURL,
  stHome,
} from './helpers';

// ── Paths ─────────────────────────────────────────────────────────────────────

const ROOT = path.resolve(process.cwd(), '..'); // c:\code\st
const TESTDATA = path.join(ROOT, 'testdata');
const ST_EXE = path.join(TESTDATA, 'syncthing.exe');
const WESYNC_EXE = path.join(ROOT, 'wesync.exe'); // always the freshly-built root binary

const FOLDER_A_PATH = path.join(TESTDATA, 'e2e-folder-a');
const FOLDER_B_PATH = path.join(TESTDATA, 'e2e-folder-b');
const FOLDER_C_PATH = path.join(TESTDATA, 'e2e-folder-c');

// ── ST API keys (read from config files — stable across restarts) ─────────────

function readSTKey(p: string): string {
  try {
    return fs.readFileSync(p, 'utf8').match(/<apikey>([^<]+)<\/apikey>/)?.[1] ?? '';
  } catch {
    return '';
  }
}

// All derived from the central port/home scheme in helpers.ts, indexed 0/1/2.
// ST keys read from each instance's real home (syncthing{1,2,3}-home); no
// fallback to a personal Syncthing, which previously took the whole suite down.
const ST_URL = [0, 1, 2].map(stURL);
const ST_KEY = [0, 1, 2].map((i) => readSTKey(path.join(stHome(i), 'config.xml')));
const [ST1, ST2, ST3] = ST_URL;
const [ST1_KEY, ST2_KEY, ST3_KEY] = ST_KEY;

// ── Timing ───────────────────────────────────────────────────────────────────

const MEASURE_MS = 40_000;
const POLL_MS = 300;
const fmt = (ms: number | null, from: string) =>
  ms === null ? `>${MEASURE_MS / 1000}s ✗` : `${(ms / 1000).toFixed(1)}s (from ${from})`;

// ── Process management ────────────────────────────────────────────────────────

const procs: ChildProcess[] = [];

function launch(exe: string, args: string[], logFile: string): ChildProcess {
  // Pass the log file FD directly to the child so it survives parent exit.
  // Piping (stdio: 'pipe') causes a broken pipe when the Playwright worker exits,
  // which kills the child process. File descriptor inheritance has no such problem.
  const fd = fs.openSync(logFile, 'w');
  const p = spawn(exe, args, { detached: true, stdio: ['ignore', fd, fd], windowsHide: true });
  fs.closeSync(fd); // parent closes its copy; child keeps its own FD
  p.on('exit', () => {}); // no-op: we can't write to the fd from here after close
  p.unref();
  procs.push(p);
  return p;
}

// Kill only processes started by this suite (tracked in procs[]).
function killTrackedProcs(): void {
  const pids = procs.map((p) => p.pid).filter((pid): pid is number => pid != null && pid > 0);
  for (const p of procs) {
    try {
      p.kill();
    } catch {}
  }
  procs.length = 0;
  if (pids.length > 0) {
    try {
      execSync(`taskkill /F ${pids.map((p) => `/PID ${p}`).join(' ')}`, {
        stdio: 'pipe',
        timeout: 5000,
        windowsHide: true,
      });
    } catch {}
  }
}

// Kill whichever process is listening on the given TCP ports (frees ports held by external servers).
function killByPort(...ports: number[]): void {
  try {
    const out = execSync('netstat -ano', {
      stdio: 'pipe',
      timeout: 3000,
      windowsHide: true,
    }).toString();
    for (const line of out.split(/\r?\n/)) {
      if (!line.includes('LISTENING')) continue;
      for (const port of ports) {
        if (line.includes(`:${port} `) || line.includes(`:${port}\t`)) {
          const pid = line.trim().split(/\s+/).pop();
          if (pid && /^\d+$/.test(pid)) {
            try {
              execSync(`taskkill /F /PID ${pid}`, {
                stdio: 'pipe',
                timeout: 3000,
                windowsHide: true,
              });
            } catch {}
          }
        }
      }
    }
  } catch {}
}

async function waitForHTTP(
  url: string,
  headers: Record<string, string> = {},
  timeoutMs = 35_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(url, { headers, signal: AbortSignal.timeout(2000) });
      if (r.status < 500) return; // 200, 401, 403 all mean the server is up
    } catch {}
    await new Promise((r) => setTimeout(r, 400));
  }
  throw new Error(`Timed out waiting for ${url}`);
}

// ── ST config cleanup (before killing — writes clean state to disk) ───────────

async function cleanSTConfig(stUrl: string, key: string): Promise<void> {
  if (!key) return;
  const h = { 'X-API-Key': key };
  try {
    const dr = await fetch(`${stUrl}/rest/config/devices`, {
      headers: h,
      signal: AbortSignal.timeout(3000),
    });
    if (dr.ok) {
      const devs = (await dr.json()) as { deviceID: string }[];
      await Promise.all(
        devs.map((d) =>
          fetch(`${stUrl}/rest/config/devices/${d.deviceID}`, {
            method: 'DELETE',
            headers: h,
          }).catch(() => {}),
        ),
      );
    }
    const fr = await fetch(`${stUrl}/rest/config/folders`, {
      headers: h,
      signal: AbortSignal.timeout(3000),
    });
    if (fr.ok) {
      const folders = (await fr.json()) as { id: string }[];
      await Promise.all(
        folders.map((f) =>
          fetch(`${stUrl}/rest/config/folders/${f.id}`, { method: 'DELETE', headers: h }).catch(
            () => {},
          ),
        ),
      );
    }
  } catch {}
}

// ── Full reset: clean config → kill all → restart ST → restart WeSync ─────────

async function fullReset(): Promise<void> {
  console.log('\n  [reset] Cleaning ST config via API...');
  await Promise.all([
    cleanSTConfig(ST1, ST1_KEY),
    cleanSTConfig(ST2, ST2_KEY),
    cleanSTConfig(ST3, ST3_KEY),
  ]);

  console.log('  [reset] Killing existing instances...');
  killTrackedProcs(); // kill any we started in a prior scenario
  killByPort(...[0, 1, 2].flatMap((i) => [apiPort(i), stGuiPort(i)])); // free ports held by external servers
  await new Promise((r) => setTimeout(r, 2000));

  console.log('  [reset] Starting ST instances...');
  [0, 1, 2].forEach((i) => {
    launch(
      ST_EXE,
      ['serve', '--no-browser', `--home=${stHome(i)}`, `--gui-address=127.0.0.1:${stGuiPort(i)}`],
      path.join(TESTDATA, `bep-st${i + 1}.log`),
    );
  });

  const t0 = Date.now();
  await Promise.all(
    [0, 1, 2].map((i) => waitForHTTP(`${ST_URL[i]}/rest/system/ping`, { 'X-API-Key': ST_KEY[i] })),
  );
  console.log(`  [reset] ST ready in ${Date.now() - t0}ms. Setting sync ports...`);

  await Promise.all(
    [0, 1, 2].map((i) =>
      fetch(`${ST_URL[i]}/rest/config/options`, {
        method: 'PATCH',
        headers: { 'X-API-Key': ST_KEY[i], 'Content-Type': 'application/json' },
        body: JSON.stringify({
          listenAddresses: [`tcp://0.0.0.0:${stSyncPort(i)}`, `quic://0.0.0.0:${stSyncPort(i)}`],
        }),
      }).catch(() => {}),
    ),
  );
  await new Promise((r) => setTimeout(r, 300));

  console.log('  [reset] Starting WeSync instances...');
  [0, 1, 2].forEach((i) => {
    launch(
      WESYNC_EXE,
      [
        '--syncthing-url',
        ST_URL[i],
        '--syncthing-key',
        ST_KEY[i],
        '--syncthing-home',
        stHome(i),
        '--port',
        String(apiPort(i)),
        '--peer-port',
        String(peerPort(i)),
        '--db',
        path.join(TESTDATA, `wesync${i + 1}.db`),
        '--debug',
      ],
      path.join(TESTDATA, `bep-ws${i + 1}.log`),
    );
  });

  const t1 = Date.now();
  await Promise.all([
    waitForHTTP(`${DEVICE_A}/api/status`),
    waitForHTTP(`${DEVICE_B}/api/status`),
    waitForHTTP(`${DEVICE_C}/api/status`),
  ]);
  console.log(`  [reset] WeSync ready in ${Date.now() - t1}ms.\n`);
}

// ── Scenario setup: fresh pairing + base folder ───────────────────────────────

async function setupScenario(
  pageA: import('@playwright/test').Page,
  pageB: import('@playwright/test').Page,
  pageC: import('@playwright/test').Page,
  idA: string,
  idB: string,
  idC: string,
): Promise<string> {
  await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B), pageC.goto(DEVICE_C)]);

  console.log('  [setup] Pairing A↔B...');
  await expect
    .poll(
      async () => {
        const [dA, dB] = await Promise.all([
          getDevices(pageA, DEVICE_A),
          getDevices(pageB, DEVICE_B),
        ]);
        if (dA.some((x: any) => x.deviceID === idB) && dB.some((x: any) => x.deviceID === idA))
          return true;
        await pair(pageA, DEVICE_A, idB, 'B');
        await pair(pageB, DEVICE_B, idA, 'A');
        return false;
      },
      { timeout: 60_000, intervals: [2000] },
    )
    .toBe(true);

  console.log('  [setup] Pairing B↔C...');
  await expect
    .poll(
      async () => {
        const [dB, dC] = await Promise.all([
          getDevices(pageB, DEVICE_B),
          getDevices(pageC, DEVICE_C),
        ]);
        if (dB.some((x: any) => x.deviceID === idC) && dC.some((x: any) => x.deviceID === idB))
          return true;
        await pair(pageB, DEVICE_B, idC, 'C');
        await pair(pageC, DEVICE_C, idB, 'B');
        return false;
      },
      { timeout: 60_000, intervals: [2000] },
    )
    .toBe(true);

  console.log('  [setup] Creating base folder BEPBase (B → A)...');
  await shareFolder(pageB, DEVICE_B, FOLDER_B_PATH, 'BEPBase', 'sendreceive', idA);
  const fB = await getFolders(pageB, DEVICE_B);
  const baseFolderID = fB.find((f: any) => f.label === 'BEPBase')?.id ?? '';
  expect(baseFolderID).not.toBe('');

  await expect
    .poll(
      async () =>
        (await getPendingFolders(pageA, DEVICE_A)).some((pf: any) => pf.folderID === baseFolderID),
      { timeout: 30_000, intervals: [1000] },
    )
    .toBe(true);
  await acceptFolder(pageA, DEVICE_A, baseFolderID, idB, FOLDER_A_PATH);
  await waitForFolderDevice(pageA, DEVICE_A, baseFolderID, idB);

  console.log(`  [setup] Done — folder ${baseFolderID.slice(0, 8)}\n`);
  return baseFolderID;
}

// ── Discovery poller ──────────────────────────────────────────────────────────

async function waitForDiscovery(
  pageA: import('@playwright/test').Page,
  pageC: import('@playwright/test').Page,
  idA: string,
  idC: string,
  t0: number,
): Promise<{ t3: number | null; t4: number | null }> {
  let t3: number | null = null,
    t4: number | null = null;
  const deadline = t0 + MEASURE_MS;
  while (Date.now() < deadline) {
    const [da, dc] = await Promise.all([getDevices(pageA, DEVICE_A), getDevices(pageC, DEVICE_C)]);
    if (!t3 && da.some((d: any) => d.deviceID === idC)) t3 = Date.now() - t0;
    if (!t4 && dc.some((d: any) => d.deviceID === idA)) t4 = Date.now() - t0;
    if (t3 && t4) break;
    await new Promise((r) => setTimeout(r, POLL_MS));
  }
  return { t3, t4 };
}

// ── Suite ─────────────────────────────────────────────────────────────────────

test.describe.serial('BEP timing — fresh instances per scenario', () => {
  let idA = '',
    idB = '',
    idC = '';
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let pageC: import('@playwright/test').Page;
  const summary: string[] = [];

  test.beforeAll(async ({ browser }) => {
    test.setTimeout(120_000);
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    pageC = await browser.newPage();

    await fullReset();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B), pageC.goto(DEVICE_C)]);

    const [sA, sB, sC] = await Promise.all([
      getStatus(pageA, DEVICE_A),
      getStatus(pageB, DEVICE_B),
      getStatus(pageC, DEVICE_C),
    ]);
    idA = sA.myID;
    idB = sB.myID;
    idC = sC.myID;
    console.log(`✓ A=${idA.slice(0, 7)}  B=${idB.slice(0, 7)}  C=${idC.slice(0, 7)}`);
    console.log(`  ST2 key: ${ST2_KEY ? 'found' : 'MISSING'}`);
    console.log(`  ST3 key: ${ST3_KEY ? 'found' : 'MISSING'}\n`);
  });

  test.afterAll(async () => {
    console.log('\n\n══════════════════════════════════════════════════════');
    console.log('📊 FINAL SUMMARY — A↔C discovery via Introducer+BEP');
    console.log('══════════════════════════════════════════════════════');
    for (const line of summary) console.log(line);
    console.log('══════════════════════════════════════════════════════\n');
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
    // Leave servers running — they use the same ports/DBs as the dev env.
    // Subsequent test suites connect to these instances; cleanAll() resets state per test.
  });

  // ── 1: WeSync shareFolder ─────────────────────────────────────────────────

  test('1. WeSync shareFolder (FolderRefresh + Introducer)', async () => {
    test.setTimeout(180_000);
    await fullReset();
    const baseFolderID = await setupScenario(pageA, pageB, pageC, idA, idB, idC);

    const t0 = Date.now();
    await shareFolder(pageB, DEVICE_B, FOLDER_B_PATH, 'BEPBase', 'sendreceive', idC);

    let t1: number | null = null;
    await expect
      .poll(
        async () => {
          const p = await getPendingFolders(pageC, DEVICE_C);
          if (p.some((pf: any) => pf.folderID === baseFolderID)) {
            t1 = Date.now() - t0;
            return true;
          }
          return false;
        },
        { timeout: 30_000, intervals: [500] },
      )
      .toBe(true);
    await acceptFolder(pageC, DEVICE_C, baseFolderID, idB, FOLDER_C_PATH);
    const t2 = Date.now() - t0;

    const { t3, t4 } = await waitForDiscovery(pageA, pageC, idA, idC, t0);

    const lines = [
      '\n  1. WeSync shareFolder (FolderRefresh + wire cascade + Introducer):',
      `     T1 C invite:   ${fmt(t1, 'T0')}`,
      `     T2 C accepted: ${fmt(t2, 'T0')}`,
      `     T3 A→C:        ${fmt(t3, 'T0')}`,
      `     T4 C→A:        ${fmt(t4, 'T0')}`,
    ];
    lines.forEach((l) => console.log(l));
    summary.push(...lines);
  });

  // ── 2: ST direct, pure BEP ────────────────────────────────────────────────

  test('2. ST direct only (no WeSync signal — pure BEP)', async () => {
    test.setTimeout(180_000);
    if (!ST2_KEY) {
      summary.push('\n  2. ST direct: SKIPPED (no key)');
      return;
    }

    await fullReset();
    const baseFolderID = await setupScenario(pageA, pageB, pageC, idA, idB, idC);

    const t0 = Date.now();
    const cfgRes = await pageB.request.get(`${ST2}/rest/config/folders/${baseFolderID}`, {
      headers: { 'X-API-Key': ST2_KEY },
    });
    const folderCfg = await cfgRes.json();
    folderCfg.devices = [...(folderCfg.devices || []), { deviceID: idC }];
    await pageB.request.fetch(`${ST2}/rest/config/folders/${baseFolderID}`, {
      method: 'PUT',
      headers: { 'X-API-Key': ST2_KEY, 'Content-Type': 'application/json' },
      data: JSON.stringify(folderCfg),
    });

    let t1: number | null = null;
    let gotInvite = false;
    const inviteDeadline = t0 + 45_000;
    while (Date.now() < inviteDeadline && !gotInvite) {
      const p = await getPendingFolders(pageC, DEVICE_C);
      if (p.some((pf: any) => pf.folderID === baseFolderID)) {
        t1 = Date.now() - t0;
        gotInvite = true;
      } else await new Promise((r) => setTimeout(r, 500));
    }
    let t2: number | null = null;
    if (gotInvite) {
      await acceptFolder(pageC, DEVICE_C, baseFolderID, idB, FOLDER_C_PATH);
      t2 = Date.now() - t0;
    }

    const { t3, t4 } = await waitForDiscovery(pageA, pageC, idA, idC, t0);

    const lines = [
      '\n  2. ST direct (pure BEP, no WeSync signal):',
      `     T1 C invite:   ${fmt(t1, 'T0')}`,
      `     T2 C accepted: ${fmt(t2, 'T0')}`,
      `     T3 A→C:        ${fmt(t3, 'T0')}`,
      `     T4 C→A:        ${fmt(t4, 'T0')}`,
    ];
    lines.forEach((l) => console.log(l));
    summary.push(...lines);
  });

  // ── 3: ST direct + pause/resume ───────────────────────────────────────────

  test('3. ST direct + pause/resume on B (no wire signal)', async () => {
    test.setTimeout(180_000);
    if (!ST2_KEY) {
      summary.push('\n  3. ST direct+pause: SKIPPED');
      return;
    }

    await fullReset();
    const baseFolderID = await setupScenario(pageA, pageB, pageC, idA, idB, idC);

    const t0 = Date.now();
    const cfgRes = await pageB.request.get(`${ST2}/rest/config/folders/${baseFolderID}`, {
      headers: { 'X-API-Key': ST2_KEY },
    });
    const folderCfg = await cfgRes.json();
    folderCfg.devices = [...(folderCfg.devices || []), { deviceID: idC }];
    await pageB.request.fetch(`${ST2}/rest/config/folders/${baseFolderID}`, {
      method: 'PUT',
      headers: { 'X-API-Key': ST2_KEY, 'Content-Type': 'application/json' },
      data: JSON.stringify(folderCfg),
    });

    const stDevRes = await pageB.request.get(`${ST2}/rest/config/devices`, {
      headers: { 'X-API-Key': ST2_KEY },
    });
    const devList = (await stDevRes.json()) as any[];
    for (const d of devList) {
      if (d.deviceID !== idB) {
        await pageB.request.post(`${ST2}/rest/system/pause?device=${d.deviceID}`, {
          headers: { 'X-API-Key': ST2_KEY },
        });
      }
    }
    await new Promise((r) => setTimeout(r, 300));
    for (const d of devList) {
      if (d.deviceID !== idB) {
        await pageB.request.post(`${ST2}/rest/system/resume?device=${d.deviceID}`, {
          headers: { 'X-API-Key': ST2_KEY },
        });
      }
    }

    let t1: number | null = null;
    let gotInvite = false;
    const inviteDeadline = t0 + 30_000;
    while (Date.now() < inviteDeadline && !gotInvite) {
      const p = await getPendingFolders(pageC, DEVICE_C);
      if (p.some((pf: any) => pf.folderID === baseFolderID)) {
        t1 = Date.now() - t0;
        gotInvite = true;
      } else await new Promise((r) => setTimeout(r, 500));
    }
    let t2: number | null = null;
    if (gotInvite) {
      await acceptFolder(pageC, DEVICE_C, baseFolderID, idB, FOLDER_C_PATH);
      t2 = Date.now() - t0;
    }

    const { t3, t4 } = await waitForDiscovery(pageA, pageC, idA, idC, t0);

    const lines = [
      '\n  3. ST direct + pause/resume B (no wire signal):',
      `     T1 C invite:   ${fmt(t1, 'T0')}`,
      `     T2 C accepted: ${fmt(t2, 'T0')}`,
      `     T3 A→C:        ${fmt(t3, 'T0')}`,
      `     T4 C→A:        ${fmt(t4, 'T0')}`,
    ];
    lines.forEach((l) => console.log(l));
    summary.push(...lines);
  });
});
