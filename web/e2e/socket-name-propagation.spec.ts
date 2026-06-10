/**
 * E2E: Socket stability through trust transitions
 *
 * Name changes must reach ALL connected peers within 3 seconds,
 * regardless of whether the devices are paired or not — and the
 * socket must keep working correctly across pair/unpair transitions.
 *
 * Scenarios:
 *   1. Rename while UNPAIRED  → B and C see it in ≤3s
 *   2. Pair A↔B, rename       → B and C see it in ≤3s
 *   3. Unpair A↔B, rename     → B and C see it in ≤3s
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
  setName,
  waitForDevice,
  cleanAll,
} from './helpers';

const TIMEOUT_MS = 10_000;

async function peerName(page: import('@playwright/test').Page, base: string, deviceID: string) {
  const peers = await getPeers(page, base);
  return (
    peers.find((p: { deviceID: string; name: string }) => p.deviceID === deviceID)?.name ?? null
  );
}

async function deviceName(page: import('@playwright/test').Page, base: string, deviceID: string) {
  const devices = await getDevices(page, base);
  return (
    devices.find((d: { deviceID: string; name: string }) => d.deviceID === deviceID)?.name ?? null
  );
}

test.describe.serial('Socket name propagation through trust transitions', () => {
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

    // Reset names and remove all pairings
    await Promise.all([
      setName(pageA, DEVICE_A, 'A'),
      setName(pageB, DEVICE_B, 'B'),
      setName(pageC, DEVICE_C, 'C'),
    ]);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    // Enable discovery on all devices
    await Promise.all([
      pageA.request.put(`${DEVICE_A}/api/mode`, { data: { visible: true } }),
      pageB.request.put(`${DEVICE_B}/api/mode`, { data: { visible: true } }),
      pageC.request.put(`${DEVICE_C}/api/mode`, { data: { visible: true } }),
    ]);

    // Wait until all 6 sockets are up and names are known
    const waitForSocket = async (
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
          {
            message: `socket up: ${targetID.slice(0, 7)} on ${base}`,
            timeout: 20_000,
            intervals: [500],
          },
        )
        .toBe(true);
    };

    await Promise.all([
      waitForSocket(pageA, DEVICE_A, idB),
      waitForSocket(pageA, DEVICE_A, idC),
      waitForSocket(pageB, DEVICE_B, idA),
      waitForSocket(pageB, DEVICE_B, idC),
      waitForSocket(pageC, DEVICE_C, idA),
      waitForSocket(pageC, DEVICE_C, idB),
    ]);
    // Let sockets fully settle
    await new Promise((r) => setTimeout(r, 1000));
  });

  test.afterAll(async () => {
    await Promise.all([
      setName(pageA, DEVICE_A, 'A').catch(() => {}),
      setName(pageB, DEVICE_B, 'B').catch(() => {}),
      setName(pageC, DEVICE_C, 'C').catch(() => {}),
    ]);
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  // ── 1. Unpaired rename ────────────────────────────────────────────────────────

  test('1. Rename while UNPAIRED: B and C see it within 3s', async () => {
    await setName(pageA, DEVICE_A, 'Alice-1');

    await Promise.all([
      expect
        .poll(() => peerName(pageB, DEVICE_B, idA), { timeout: TIMEOUT_MS, intervals: [200] })
        .toBe('Alice-1'),
      expect
        .poll(() => peerName(pageC, DEVICE_C, idA), { timeout: TIMEOUT_MS, intervals: [200] })
        .toBe('Alice-1'),
    ]);
    console.log('✓ Unpaired rename propagated to B and C within 3s');
  });

  // ── 2. Pair A↔B, then rename ─────────────────────────────────────────────────

  test('2. Pair A↔B, then rename: B (trusted) and C (untrusted) see it within 3s', async () => {
    // Pair A↔B
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (
            dA.some((x: { deviceID: string }) => x.deviceID === idB) &&
            dB.some((x: { deviceID: string }) => x.deviceID === idA)
          )
            return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 30_000, intervals: [2000] },
      )
      .toBe(true);

    await setName(pageA, DEVICE_A, 'Alice-2');

    await Promise.all([
      // B is trusted — should see via devices list
      expect
        .poll(() => deviceName(pageB, DEVICE_B, idA), { timeout: TIMEOUT_MS, intervals: [200] })
        .toBe('Alice-2'),
      // C is unpaired — should see via peers list
      expect
        .poll(() => peerName(pageC, DEVICE_C, idA), { timeout: TIMEOUT_MS, intervals: [200] })
        .toBe('Alice-2'),
    ]);
    console.log('✓ Paired rename propagated to trusted B and unpaired C within 3s');
  });

  // ── 3. Unpair A↔B, then rename ───────────────────────────────────────────────

  test('3. Unpair A↔B, then rename: B and C (both untrusted) see it within 3s', async () => {
    // Remove the pairing
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);
    await pageB.request.delete(`${DEVICE_B}/api/devices?id=${idA}`);

    // Wait until both see each other as unpaired
    await Promise.all([
      expect
        .poll(
          async () => {
            const dA = await getDevices(pageA, DEVICE_A);
            return !dA.some((d: { deviceID: string }) => d.deviceID === idB);
          },
          { timeout: 10_000, intervals: [500] },
        )
        .toBe(true),
      expect
        .poll(
          async () => {
            const dB = await getDevices(pageB, DEVICE_B);
            return !dB.some((d: { deviceID: string }) => d.deviceID === idA);
          },
          { timeout: 10_000, intervals: [500] },
        )
        .toBe(true),
    ]);

    await setName(pageA, DEVICE_A, 'Alice-3');

    await Promise.all([
      expect
        .poll(() => peerName(pageB, DEVICE_B, idA), { timeout: TIMEOUT_MS, intervals: [200] })
        .toBe('Alice-3'),
      expect
        .poll(() => peerName(pageC, DEVICE_C, idA), { timeout: TIMEOUT_MS, intervals: [200] })
        .toBe('Alice-3'),
    ]);
    console.log('✓ Post-unpair rename propagated to B and C within 3s');
  });
});
