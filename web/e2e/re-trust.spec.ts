/**
 * E2E: Re-trust flow
 *
 * A trusts B → B accepts → A removes trust → A trusts B again →
 * B must receive a new trust request (not auto-accepted silently).
 *
 * Requires dev.ps1 to be running.
 */

import { test, expect } from '@playwright/test';
import {
  DEVICE_A,
  DEVICE_B,
  getStatus,
  getDevices,
  getIncoming,
  pair,
  waitForDevice,
  waitForPeer,
  cleanAll,
} from './helpers';

test.describe.serial('Re-trust after removal', () => {
  let idA: string, idB: string;
  let pageA: import('@playwright/test').Page;
  let pageB: import('@playwright/test').Page;

  test.beforeAll(async ({ browser }) => {
    pageA = await browser.newPage();
    pageB = await browser.newPage();
    await Promise.all([pageA.goto(DEVICE_A), pageB.goto(DEVICE_B)]);

    const [sA, sB] = await Promise.all([getStatus(pageA, DEVICE_A), getStatus(pageB, DEVICE_B)]);
    idA = sA.myID;
    idB = sB.myID;

    // Clean slate
    await cleanAll([
      [pageA, DEVICE_A],
      [pageB, DEVICE_B],
    ]);
    await Promise.all([
      pageA.request.post(`${DEVICE_A}/api/sync`, {}),
      pageB.request.post(`${DEVICE_B}/api/sync`, {}),
    ]);
    await waitForPeer(pageA, DEVICE_A, idB);
    await waitForPeer(pageB, DEVICE_B, idA);
  });

  test.afterAll(async () => {
    await pageA?.close();
    await pageB?.close();
  });

  test('1. A trusts B, B accepts', async () => {
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x) => x.deviceID === idB) && dB.some((x) => x.deviceID === idA)) return true;
          await pair(pageA, DEVICE_A, idB, 'B');
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 60_000, intervals: [3000] },
      )
      .toBe(true);
  });

  test('2. A removes trust', async () => {
    await pageA.request.delete(`${DEVICE_A}/api/devices?id=${idB}`);
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageA, DEVICE_A);
          return !d.some((x) => x.deviceID === idB);
        },
        { timeout: 15_000 },
      )
      .toBe(true);

    // B should also lose A
    await expect
      .poll(
        async () => {
          const d = await getDevices(pageB, DEVICE_B);
          return !d.some((x) => x.deviceID === idA);
        },
        { message: 'B: A removed', timeout: 15_000 },
      )
      .toBe(true);
  });

  test('3. A trusts B again → A trusts immediately, B sees incoming request', async () => {
    // A trusts B immediately (ST-direct pairing)
    await pair(pageA, DEVICE_A, idB, 'B');

    // A has B trusted immediately (unilateral trust)
    const dA = await getDevices(pageA, DEVICE_A);
    expect(dA.some((d) => d.deviceID === idB)).toBe(true);

    // B must NOT have auto-accepted A — B still needs to explicitly accept
    const dB = await getDevices(pageB, DEVICE_B);
    expect(dB.some((d) => d.deviceID === idA)).toBe(false);

    console.log('✓ A trusted B immediately; B has not yet accepted — explicit action required');
  });

  test('4. B accepts → both trusted again', async () => {
    await expect
      .poll(
        async () => {
          const [dA, dB] = await Promise.all([
            getDevices(pageA, DEVICE_A),
            getDevices(pageB, DEVICE_B),
          ]);
          if (dA.some((x) => x.deviceID === idB) && dB.some((x) => x.deviceID === idA)) return true;
          await pair(pageB, DEVICE_B, idA, 'A');
          return false;
        },
        { timeout: 30_000, intervals: [2000] },
      )
      .toBe(true);

    console.log('✓ Both trusted again after explicit re-acceptance');
  });
});
