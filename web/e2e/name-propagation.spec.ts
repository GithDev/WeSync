/**
 * E2E: Device name propagation
 *
 * Verifies that when a device renames itself, the new name reaches other
 * devices promptly — both trusted (paired) and untrusted (discovered) peers.
 *
 * Requires dev.ps1 to be running.
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
  waitForDeviceName,
  waitForPeerName,
  waitForPeer,
  waitForDevice,
  cleanAll,
} from './helpers';

const ORIGINAL_NAME_A = 'A';
const ORIGINAL_NAME_B = 'B';
const ORIGINAL_NAME_C = 'C';

test.describe.serial('Name propagation', () => {
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

    // Restore names and clear all devices
    await Promise.all([
      setName(pageA, DEVICE_A, ORIGINAL_NAME_A),
      setName(pageB, DEVICE_B, ORIGINAL_NAME_B),
      setName(pageC, DEVICE_C, ORIGINAL_NAME_C),
    ]);
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
      [pageC, DEVICE_C],
    ]);

    // Wait for socket connections (Hello received → socket ready)
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
    await new Promise((r) => setTimeout(r, 3000));
  });

  test.afterAll(async () => {
    // Restore original names regardless of test outcome
    await Promise.all([
      setName(pageA, DEVICE_A, ORIGINAL_NAME_A).catch(() => {}),
      setName(pageB, DEVICE_B, ORIGINAL_NAME_B).catch(() => {}),
      setName(pageC, DEVICE_C, ORIGINAL_NAME_C).catch(() => {}),
    ]);
    await pageA?.close();
    await pageB?.close();
    await pageC?.close();
  });

  // ── Test 1: Name propagates to unpaired (discovered) peers ───────────────────

  test('1. Rename propagates to unpaired peers via Hello', async () => {
    // Precondition: no devices are paired
    const [dA, dB] = await Promise.all([getDevices(pageA, DEVICE_A), getDevices(pageB, DEVICE_B)]);
    expect(dA.some((d) => d.deviceID === idB)).toBe(false);
    expect(dB.some((d) => d.deviceID === idA)).toBe(false);

    // A renames itself
    await setName(pageA, DEVICE_A, 'Alice');

    // B and C should see the new name in their peers list (unpaired)
    await Promise.all([
      waitForPeerName(pageB, DEVICE_B, idA, 'Alice'),
      waitForPeerName(pageC, DEVICE_C, idA, 'Alice'),
    ]);

    console.log('✓ Renamed A→Alice visible on unpaired B and C');
  });

  // ── Test 2: Name propagates to paired (trusted) peers ────────────────────────

  test('2. Rename propagates to paired peers via Hello', async () => {
    // Pair A↔B
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x) => x.deviceID === idB) && dB.some((x) => x.deviceID === idA)) return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'Alice'); // A is already "Alice" from test 1
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // B renames itself
    await setName(pageB, DEVICE_B, 'Bob');

    // A (paired with B) sees new name in device list
    await waitForDeviceName(pageA, DEVICE_A, idB, 'Bob');

    // C (unpaired with B) sees new name in peer list
    await waitForPeerName(pageC, DEVICE_C, idB, 'Bob');

    console.log('✓ Renamed B→Bob visible on paired A and unpaired C');
  });

  // ── Test 3: Both rename, both propagate ──────────────────────────────────────

  test('3. Both sides rename simultaneously — both updates arrive', async () => {
    // A and B are still paired from test 2; rename both at the same time
    await Promise.all([setName(pageA, DEVICE_A, 'AliceV2'), setName(pageB, DEVICE_B, 'BobV2')]);

    // A sees B's new name; B sees A's new name
    await Promise.all([
      waitForDeviceName(pageA, DEVICE_A, idB, 'BobV2'),
      waitForDeviceName(pageB, DEVICE_B, idA, 'AliceV2'),
    ]);

    // C (unpaired) sees both updates
    await Promise.all([
      waitForPeerName(pageC, DEVICE_C, idA, 'AliceV2'),
      waitForPeerName(pageC, DEVICE_C, idB, 'BobV2'),
    ]);

    console.log('✓ Simultaneous renames both propagated correctly');
  });

  // ── Test 4: Name persists after reconnect ────────────────────────────────────

  test('4. Renamed device is still seen with new name after re-pair', async () => {
    // Remove A from B, then re-pair — name should still be AliceV2
    await pageB.request.delete(`${DEVICE_B}/api/devices?id=${idA}`);
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageB, DEVICE_B);
          return !d.some((x) => x.deviceID === idA);
        },
        { timeout: 15_000 },
      )
      .toBe(true);

    // Re-pair
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x) => x.deviceID === idB) && dB.some((x) => x.deviceID === idA)) return true;
          await pair(pageA, DEVICE_A, idB, 'BobV2');
          await pair(pageB, DEVICE_B, idA, 'AliceV2');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);

    // B should see A as "AliceV2" (name from Hello, not stale DB value)
    await waitForDeviceName(pageB, DEVICE_B, idA, 'AliceV2');

    console.log('✓ Name AliceV2 preserved after re-pair');
  });
});
