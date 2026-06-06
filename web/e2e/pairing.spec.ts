/**
 * E2E: Pairing flow
 *
 * Requires dev.ps1 to be running (3 WeSync + 3 Syncthing instances).
 * Run with: npx playwright test --config e2e/playwright.config.ts
 *
 * Scenario:
 *   0. No devices paired on any instance
 *   1. A pairs with B, B accepts → both have each other
 *   2. B pairs with C, C accepts → B has C, C has B (A unaffected)
 *   3. A removes B → B loses A; C is unaffected
 *   4. B re-pairs with A, A accepts → both have each other again
 */

import { test, expect } from '@playwright/test';
import {
  DEVICE_A,
  DEVICE_B,
  DEVICE_C,
  getStatus,
  getDevices,
  getPeers,
  pair,
  waitForDevice,
  waitForPeer,
  cleanAll,
} from './helpers';
import { StateMonitor } from './state-monitor';

test.describe.serial('Pairing', () => {
  let idA: string, idB: string, idC: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;
  let pageC: import('@playwright/test').Page;
  let monitor: StateMonitor;

  // ── Setup ───────────────────────────────────────────────────────────────────

  test.beforeAll(async ({ browser }) => {
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    pageC = await browser.newPage();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B), pageC.goto(DEVICE_C)]);

    const [statusA, statusB, statusC] = await Promise.all([
      getStatus(pageA, DEVICE_A),
      getStatus(pageB, DEVICE_B),
      getStatus(pageC, DEVICE_C),
    ]);
    idA = statusA.myID;
    idB = statusB.myID;
    idC = statusC.myID;

    console.log(`A: ${idA.slice(0, 7)}  B: ${idB.slice(0, 7)}  C: ${idC.slice(0, 7)}`);

    // Clean up: remove all paired devices and folders on all instances
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    monitor = new StateMonitor([
      [pageA, DEVICE_A, 'A'],
      [pageB, DEVICE_B, 'B'],
      [pageC, DEVICE_C, 'C'],
    ]);

    // Wait for all devices to discover each other via UDP
    await Promise.all([
      waitForPeer(pageA, DEVICE_A, idB),
      waitForPeer(pageA, DEVICE_A, idC),
      waitForPeer(pageB, DEVICE_B, idA),
      waitForPeer(pageB, DEVICE_B, idC),
      waitForPeer(pageC, DEVICE_C, idA),
      waitForPeer(pageC, DEVICE_C, idB),
    ]);
    console.log('All devices discovered each other ✓');
    // Wait for WeSync sockets to be established (Hello received = socket ready).
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
    await Promise.all([
      waitForHello(pageA, DEVICE_A, idB),
      waitForHello(pageA, DEVICE_A, idC),
      waitForHello(pageB, DEVICE_B, idA),
      waitForHello(pageB, DEVICE_B, idC),
      waitForHello(pageC, DEVICE_C, idA),
      waitForHello(pageC, DEVICE_C, idB),
    ]);
    // Allow pending goroutines from cleanup to drain before pairing.
    await new Promise((r) => setTimeout(r, 2000));
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  // ── Step 0: No paired devices ───────────────────────────────────────────────

  test('0. No devices are paired initially', async () => {
    monitor.start();
    const [dA, dB, dC] = await Promise.all([
      getDevices(pageA, DEVICE_A),
      getDevices(pageB, DEVICE_B),
      getDevices(pageC, DEVICE_C),
    ]);
    expect(dA).toHaveLength(0);
    expect(dB).toHaveLength(0);
    expect(dC).toHaveLength(0);
    monitor.stop();
  });

  // ── Step 1: A pairs with B ──────────────────────────────────────────────────

  test('1. A pairs with B, B accepts', async () => {
    monitor.start();
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          if (d.some((x) => x.deviceID === idB)) return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { message: 'pairing A↔B', timeout: 30_000, intervals: [3000] },
      )
      .toBe(true);
    await waitForDevice(pageB, DEVICE_B, idA);

    // C is unaffected
    const dC = await getDevices(pageC, DEVICE_C);
    expect(dC).toHaveLength(0);

    console.log('Step 1 ✓ — A and B paired');
    monitor.stop();
  });

  // ── Step 2: B pairs with C ──────────────────────────────────────────────────

  test('2. B pairs with C, C accepts', async () => {
    monitor.start();
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageB, DEVICE_B);
          if (d.some((x) => x.deviceID === idC)) return true;
          await pair(pageB, DEVICE_B, idC, 'C');
          await pair(pageC, DEVICE_C, idB, 'B');
          return false;
        },
        { message: 'pairing B↔C', timeout: 30_000, intervals: [3000] },
      )
      .toBe(true);
    await waitForDevice(pageC, DEVICE_C, idB);

    // A still only has B (pairing doesn't auto-propagate)
    const dA = await getDevices(pageA, DEVICE_A);
    expect(dA.map((d) => d.deviceID)).toEqual([idB]);

    // C only has B
    const dC = await getDevices(pageC, DEVICE_C);
    expect(dC.map((d) => d.deviceID)).toEqual([idB]);

    console.log('Step 2 ✓ — B and C paired; A unaffected');
    monitor.stop();
  });

  // ── Step 3: A removes B ─────────────────────────────────────────────────────

  test('3. A removes B → B loses A; C unaffected', async () => {
    monitor.start();
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);

    // A: B gone
    await expect
      .poll(
        async () => {
          const devices = await getDevices(pageA, DEVICE_A);
          return !devices.some((d) => d.deviceID === idB);
        },
        { message: 'waiting for A to lose B', timeout: 10_000 },
      )
      .toBe(true);

    // B: A gone
    await expect
      .poll(
        async () => {
          const devices = await getDevices(pageB, DEVICE_B);
          return !devices.some((d) => d.deviceID === idA);
        },
        { message: 'waiting for B to lose A', timeout: 10_000 },
      )
      .toBe(true);

    // B still has C; C still has B
    const dB = await getDevices(pageB, DEVICE_B);
    expect(dB.map((d) => d.deviceID)).toEqual([idC]);
    const dC = await getDevices(pageC, DEVICE_C);
    expect(dC.map((d) => d.deviceID)).toEqual([idB]);

    console.log('Step 3 ✓ — A removed B; C unaffected');
    monitor.stop();
  });

  // ── Step 4: B re-pairs with A ───────────────────────────────────────────────

  test('4. B re-pairs with A, A accepts → both have each other again', async () => {
    monitor.start();
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          if (d.some((x) => x.deviceID === idB)) return true;
          await pair(pageB, DEVICE_B, idA, 'A');
          await pair(pageA, DEVICE_A, idB, 'B');
          return false;
        },
        { message: 'pairing B↔A', timeout: 30_000, intervals: [3000] },
      )
      .toBe(true);
    await waitForDevice(pageB, DEVICE_B, idA);

    // B now has A and C
    const dB = await getDevices(pageB, DEVICE_B);
    expect(dB.some((d) => d.deviceID === idA)).toBe(true);
    expect(dB.some((d) => d.deviceID === idC)).toBe(true);

    console.log('Step 4 ✓ — A and B re-paired; B still has C');
    monitor.stop();
  });
});
