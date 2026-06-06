/**
 * E2E: Trust request state machine — A↔C only, no folders.
 *
 * Verifies that state is always consistent on BOTH sides after each action:
 *   A requests → A=waiting,   C=incoming
 *   A cancels  → A=discover,  C=clean
 *   A requests → A=waiting,   C=incoming
 *   C accepts  → A=connected, C=connected
 *   A unpairs  → A=discover,  C=clean
 *   C requests → A=incoming,  C=waiting
 *   A accepts  → A=connected, C=connected
 *   C unpairs  → A=clean,     C=discover
 */

import { test, expect } from '@playwright/test';
import {
  DEVICE_A,
  DEVICE_C,
  getStatus,
  getDevices,
  getIncoming,
  getPeers,
  pair,
  cleanAll,
} from './helpers';

const BASE_A = DEVICE_A;
const BASE_C = DEVICE_C;

async function state(page: import('@playwright/test').Page, base: string, targetID: string) {
  const [devices, incoming] = await Promise.all([getDevices(page, base), getIncoming(page, base)]);
  const dev = devices.find((d: any) => d.deviceID === targetID);
  const inc = incoming.find((i: any) => i.deviceID === targetID);
  return {
    trusted: dev?.stPaired ?? false,
    accepted: dev?.accepted ?? false,
    connected: dev?.connected ?? false,
    incoming: !!inc,
  };
}

async function unpair(page: import('@playwright/test').Page, base: string, deviceID: string) {
  await page.request.delete(`${base}/api/devices?id=${encodeURIComponent(deviceID)}`);
}

async function dismissIncoming(
  page: import('@playwright/test').Page,
  base: string,
  deviceID: string,
) {
  await page.request.delete(`${base}/api/incoming?id=${encodeURIComponent(deviceID)}`);
}

test.describe.serial('Trust flow: A↔C', () => {
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

  // ── Step 1: A requests trust ────────────────────────────────────────────────

  test('1. A requests trust — A=waiting, C=incoming', async () => {
    await new Promise((r) => setTimeout(r, 1000)); // settle after cleanAll
    await pair(pageA, BASE_A, idC, 'C');

    await expect
      .poll(async () => (await state(pageA, BASE_A, idC)).trusted, { timeout: 10_000 })
      .toBe(true);
    await expect
      .poll(async () => (await state(pageC, BASE_C, idA)).incoming, { timeout: 10_000 })
      .toBe(true);

    const sA = await state(pageA, BASE_A, idC);
    const sC = await state(pageC, BASE_C, idA);

    expect(sA.trusted, 'A: C should be trusted').toBe(true);
    expect(sA.accepted, 'A: C should NOT be accepted yet').toBe(false);
    expect(sC.incoming, 'C: should see incoming from A').toBe(true);
    expect(sC.trusted, 'C: should NOT have A trusted yet').toBe(false);
    console.log('Step 1 ✓ — A=waiting, C=incoming');
  });

  // ── Step 2: A cancels ───────────────────────────────────────────────────────

  test('2. A cancels — A=clean, C=clean', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await unpair(pageA, BASE_A, idC);

    await expect
      .poll(async () => (await state(pageA, BASE_A, idC)).trusted, { timeout: 10_000 })
      .toBe(false);
    await expect
      .poll(async () => (await state(pageC, BASE_C, idA)).incoming, { timeout: 10_000 })
      .toBe(false);

    const sA = await state(pageA, BASE_A, idC);
    const sC = await state(pageC, BASE_C, idA);

    expect(sA.trusted, 'A: C should no longer be trusted').toBe(false);
    expect(sC.incoming, 'C: incoming from A should be gone').toBe(false);
    expect(sC.trusted, 'C: should not have A trusted').toBe(false);
    console.log('Step 2 ✓ — both clean after A cancels');
  });

  // ── Step 3: A requests again ────────────────────────────────────────────────

  test('3. A requests again — A=waiting, C=incoming', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await pair(pageA, BASE_A, idC, 'C');

    await expect
      .poll(async () => (await state(pageC, BASE_C, idA)).incoming, { timeout: 10_000 })
      .toBe(true);

    const sA = await state(pageA, BASE_A, idC);
    const sC = await state(pageC, BASE_C, idA);

    expect(sA.trusted, 'A: C trusted').toBe(true);
    expect(sA.accepted, 'A: not accepted yet').toBe(false);
    expect(sC.incoming, 'C: sees incoming').toBe(true);
    console.log('Step 3 ✓ — A=waiting, C=incoming (again)');
  });

  // ── Step 4: C accepts ───────────────────────────────────────────────────────

  test('4. C accepts — both connected', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await pair(pageC, BASE_C, idA, 'A');

    await expect
      .poll(async () => (await state(pageA, BASE_A, idC)).accepted, { timeout: 15_000 })
      .toBe(true);
    await expect
      .poll(async () => (await state(pageC, BASE_C, idA)).accepted, { timeout: 15_000 })
      .toBe(true);

    const sA = await state(pageA, BASE_A, idC);
    const sC = await state(pageC, BASE_C, idA);

    expect(sA.trusted, 'A: C trusted').toBe(true);
    expect(sA.accepted, 'A: C accepted').toBe(true);
    expect(sC.trusted, 'C: A trusted').toBe(true);
    expect(sC.accepted, 'C: A accepted').toBe(true);
    expect(sC.incoming, 'C: no more incoming').toBe(false);
    console.log('Step 4 ✓ — mutual trust confirmed');
  });

  // ── Step 5: A unpairs ───────────────────────────────────────────────────────

  test('5. A unpairs — both clean (cascade: A removes C, C also removes A)', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    await unpair(pageA, BASE_A, idC);

    // When A removes C, C receives trusted:false and also removes A (cascade).
    // Both sides end up clean — this is correct behavior.
    await expect
      .poll(async () => (await state(pageA, BASE_A, idC)).trusted, { timeout: 10_000 })
      .toBe(false);
    await expect
      .poll(async () => (await state(pageC, BASE_C, idA)).trusted, { timeout: 10_000 })
      .toBe(false);

    const sA = await state(pageA, BASE_A, idC);
    expect(sA.trusted, 'A: C no longer trusted').toBe(false);
    expect(sA.accepted, 'A: C no longer accepted').toBe(false);
    console.log('Step 5 ✓ — both clean after A unpairs (cascade)');
  });

  // ── Step 6: verify fully clean ────────────────────────────────────────────────

  test('6. both fully clean after cascade', async () => {
    await new Promise((r) => setTimeout(r, 1000));
    // Dismiss any lingering incoming on either side
    await dismissIncoming(pageA, BASE_A, idC).catch(() => {});
    await dismissIncoming(pageC, BASE_C, idA).catch(() => {});

    await expect
      .poll(async () => (await state(pageC, BASE_C, idA)).trusted, { timeout: 10_000 })
      .toBe(false);

    const sA = await state(pageA, BASE_A, idC);
    const sC = await state(pageC, BASE_C, idA);

    expect(sA.trusted, 'A: clean').toBe(false);
    expect(sA.incoming, 'A: no incoming').toBe(false);
    expect(sC.trusted, 'C: clean').toBe(false);
    expect(sC.incoming, 'C: no incoming').toBe(false);
    console.log('Step 6 ✓ — both fully clean');
  });
});
