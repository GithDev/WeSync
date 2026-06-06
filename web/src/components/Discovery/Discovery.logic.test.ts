import { describe, it, expect } from 'vitest';
import { deriveNetwork } from './Discovery.logic';
import type { NetworkInput } from './Discovery.logic';

const ID_A = 'AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA';
const ID_B = 'BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB';

// device(id, name, connected, stPaired, accepted)
const device = (
  id: string,
  name = id.slice(0, 7),
  connected = false,
  stPaired = false,
  accepted = false,
) => ({ deviceID: id, name, connected, stPaired, accepted });
const incoming = (id: string, name = id.slice(0, 7)) => ({ deviceID: id, name });
const folder = (deviceIDs: string[]) => ({
  id: 'f1',
  label: 'Folder',
  path: '/foo',
  type: 'sendreceive',
  deviceIDs,
});

const empty: NetworkInput = { devices: [], incoming: [], folders: [] };

function kinds(ws: NetworkInput, accepted = new Set<string>()) {
  return deriveNetwork(ws, accepted).map((e) => ({ kind: e.kind, id: e.id }));
}

// ── Discoverable ──────────────────────────────────────────────────────────────

describe('discoverable', () => {
  it('shows a connected device with no shared folder as discoverable', () => {
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', true)] };
    expect(kinds(ws)).toEqual([{ kind: 'discoverable', id: ID_B }]);
  });

  it('does not show an offline device as discoverable', () => {
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', false)] };
    expect(kinds(ws).find((e) => e.kind === 'discoverable')).toBeUndefined();
  });

  it('does not show a collaborating device as discoverable (shows as connected instead)', () => {
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', true)], folders: [folder([ID_B])] };
    const result = kinds(ws);
    expect(result.find((e) => e.kind === 'discoverable')).toBeUndefined();
    expect(result.find((e) => e.kind === 'connected' && e.id === ID_B)).toBeDefined();
  });

  it('hides a device with a pending incoming request', () => {
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', true)], incoming: [incoming(ID_B)] };
    expect(kinds(ws)).toEqual([{ kind: 'incoming', id: ID_B }]);
  });
});

// ── Waiting (stPaired, not yet accepted via BEP) ──────────────────────────────

describe('waiting (stPaired, not accepted)', () => {
  it('shows waiting when device is stPaired but has never connected via BEP', () => {
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', false, true, false)] };
    expect(kinds(ws)).toEqual([{ kind: 'waiting', id: ID_B }]);
  });

  it('wire-connected stPaired device with no BEP yet shows as WAITING (not connected)', () => {
    // Wire connection is an implementation detail — trust has not been mutually
    // established until accepted=true (BEP lastSeen non-empty). A one-sided
    // trust request that happens to have a live wire should NOT look "connected".
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', true, true, false)] };
    expect(kinds(ws).find((e) => e.kind === 'waiting' && e.id === ID_B)).toBeDefined();
    expect(kinds(ws).find((e) => e.kind === 'connected')).toBeUndefined();
  });

  it('not waiting once BEP-accepted', () => {
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', true, true, true)] };
    expect(kinds(ws).find((e) => e.kind === 'waiting')).toBeUndefined();
  });
});

// ── Incoming ──────────────────────────────────────────────────────────────────

describe('incoming (pending)', () => {
  it('shows incoming when device is in pending', () => {
    const ws = { ...empty, incoming: [incoming(ID_A)] };
    expect(kinds(ws)).toEqual([{ kind: 'incoming', id: ID_A }]);
  });

  it('does not show a collaborating device as incoming (folder participant takes priority)', () => {
    // Device is in a shared folder — shows as offline/connected, not incoming
    const ws = {
      ...empty,
      incoming: [incoming(ID_A)],
      devices: [device(ID_A, 'DeviceA', false, false, true)],
      folders: [folder([ID_A])],
    };
    const result = kinds(ws);
    expect(result.find((e) => e.kind === 'incoming')).toBeUndefined();
    expect(result.find((e) => e.kind === 'offline' && e.id === ID_A)).toBeDefined();
  });

  it('hides incoming after local accept (accepted set)', () => {
    const ws = { ...empty, incoming: [incoming(ID_A)] };
    expect(kinds(ws, new Set([ID_A]))).toEqual([]);
  });
});

// ── Accept → Connected ────────────────────────────────────────────────────────

describe('accept', () => {
  it('shows connected when device is wire-connected and in a shared folder', () => {
    const ws = {
      ...empty,
      devices: [device(ID_B, 'DeviceB', true)],
      folders: [folder([ID_B])],
    };
    expect(kinds(ws)).toEqual([{ kind: 'connected', id: ID_B }]);
  });

  it('accepted device does not show as discoverable while WS update is in flight', () => {
    const ws = { ...empty, devices: [device(ID_A, 'DeviceA', true)], incoming: [] };
    const network = deriveNetwork(ws, new Set([ID_A]));
    expect(network.find((e) => e.kind === 'discoverable' && e.id === ID_A)).toBeUndefined();
    expect(network.find((e) => e.kind === 'incoming' && e.id === ID_A)).toBeUndefined();
  });
});

// ── Cancel ────────────────────────────────────────────────────────────────────

describe('cancel', () => {
  it('empty state → no entries', () => {
    expect(kinds(empty)).toEqual([]);
  });

  it('re-exposes device as discoverable after removal if still connected and no folder', () => {
    const ws = { ...empty, devices: [device(ID_B, 'DeviceB', true)] };
    expect(kinds(ws)).toEqual([{ kind: 'discoverable', id: ID_B }]);
  });
});

// ── Ignore ────────────────────────────────────────────────────────────────────

describe('ignore', () => {
  it('hides incoming after accepted set marks it (optimistic UI)', () => {
    const ws = { ...empty, incoming: [incoming(ID_A)] };
    expect(kinds(ws, new Set([ID_A]))).toEqual([]);
  });
});

// ── Remove / offline ──────────────────────────────────────────────────────────

describe('remove', () => {
  it('offline collaborating device shows as kind=offline', () => {
    // accepted=true: was connected before, now offline
    const ws = {
      ...empty,
      devices: [device(ID_B, 'DeviceB', false, true, true)],
      folders: [folder([ID_B])],
    };
    expect(kinds(ws).find((e) => e.id === ID_B && e.kind === 'offline')).toBeDefined();
  });

  it('offline stPaired device that never connected shows as waiting (not offline)', () => {
    // accepted=false: never connected via BEP
    const ws = {
      ...empty,
      devices: [device(ID_B, 'DeviceB', false, true, false)],
      folders: [folder([ID_B])],
    };
    const network = deriveNetwork(ws, new Set());
    expect(network.find((e) => e.kind === 'waiting')).toBeDefined();
    expect(network.find((e) => e.kind === 'offline')).toBeUndefined();
  });
});

// ── Full flows ────────────────────────────────────────────────────────────────

describe('full flow: pair → accept', () => {
  it('goes discoverable → waiting → connected', () => {
    // Before pairing: connected, no stPaired → discoverable
    const wsDiscover = { ...empty, devices: [device(ID_B, 'DeviceB', true, false, false)] };
    expect(kinds(wsDiscover)[0].kind).toBe('discoverable');

    // After pairing: stPaired=true, accepted=false → waiting
    const wsWaiting = { ...empty, devices: [device(ID_B, 'DeviceB', false, true, false)] };
    expect(kinds(wsWaiting)[0].kind).toBe('waiting');

    // After BEP connect: stPaired=true, accepted=true, connected=true, folder → connected
    const wsConnected = {
      ...empty,
      devices: [device(ID_B, 'DeviceB', true, true, true)],
      folders: [folder([ID_B])],
    };
    expect(kinds(wsConnected)[0].kind).toBe('connected');
  });
});

describe('full flow: pair → cancel → pair again', () => {
  it('returns to waiting after re-pair', () => {
    const wsWaiting = { ...empty, devices: [device(ID_B, 'DeviceB', false, true, false)] };
    expect(kinds(wsWaiting)[0].kind).toBe('waiting');

    // After removal: no longer stPaired, device disappears
    const wsCancelled = { ...empty, devices: [device(ID_B, 'DeviceB', true, false, false)] };
    expect(kinds(wsCancelled)[0].kind).toBe('discoverable');

    // Re-pair: stPaired=true again, not accepted yet
    const wsRePaired = { ...empty, devices: [device(ID_B, 'DeviceB', false, true, false)] };
    expect(kinds(wsRePaired)[0].kind).toBe('waiting');
  });
});

describe('full flow: incoming → ignore', () => {
  it('clears incoming when accepted set applied', () => {
    const ws = { ...empty, incoming: [incoming(ID_A)] };
    expect(kinds(ws)[0].kind).toBe('incoming');
    expect(kinds(ws, new Set([ID_A]))).toEqual([]);
  });
});
