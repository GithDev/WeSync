import { describe, it, expect } from 'vitest';
import { useFolderDevices } from './useFolderDevices';
import type { WeSyncFolder, Device } from '../api/client';

const ID_A = 'AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA';
const ID_B = 'BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB';
const ID_C = 'CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC';

function device(
  id: string,
  name: string,
  connected = false,
  stPaired = true,
  accepted = true,
): Device {
  return { deviceID: id, name, connected, stPaired, accepted };
}

function folder(overrides: Partial<WeSyncFolder> = {}): WeSyncFolder {
  return {
    id: 'folder-1',
    label: 'Photos',
    path: '/photos',
    type: 'sendreceive',
    deviceIDs: [],
    ...overrides,
  };
}

// ── members ───────────────────────────────────────────────────────────────────

describe('members', () => {
  it('returns empty when folder has no devices', () => {
    const { members } = useFolderDevices(folder(), [device(ID_A, 'A')]);
    expect(members).toHaveLength(0);
  });

  it('maps each deviceID to name from devices list', () => {
    const f = folder({ deviceIDs: [ID_B] });
    const { members } = useFolderDevices(f, [device(ID_B, 'Laptop')]);
    expect(members[0].name).toBe('Laptop');
  });

  it('untrusted devices in deviceIDs appear in others list', () => {
    const f = folder({ deviceIDs: [ID_B] });
    // ID_B is in deviceIDs but not in devices list (not trusted) → appears in others
    const { members, others } = useFolderDevices(f, []);
    expect(members).toHaveLength(0);
    expect(others).toHaveLength(1);
    expect(others[0].deviceID).toBe(ID_B);
    expect(others[0].name).toBe(ID_B.slice(0, 7)); // fallback to short ID
  });

  it('omits hostname when identical to name', () => {
    const f = folder({ deviceIDs: [ID_B] });
    const devices = [
      { ...device(ID_B, 'Laptop'), info: { hostname: 'Laptop', os: '', osVer: '' } },
    ];
    const { members } = useFolderDevices(f, devices);
    expect(members[0].hostname).toBeUndefined();
  });

  it('includes hostname when different from name', () => {
    const f = folder({ deviceIDs: [ID_B] });
    const devices = [
      { ...device(ID_B, 'B'), info: { hostname: 'MR-OSCAR-LT', os: '', osVer: '' } },
    ];
    const { members } = useFolderDevices(f, devices);
    expect(members[0].hostname).toBe('MR-OSCAR-LT');
  });

  it('includes peerType from deviceTypes', () => {
    const f = folder({ deviceIDs: [ID_B], deviceTypes: { [ID_B]: 'sendonly' } });
    const { members } = useFolderDevices(f, [device(ID_B, 'B')]);
    expect(members[0].peerType).toBe('sendonly');
  });
});

// ── status / accepted logic ───────────────────────────────────────────────────

describe('status', () => {
  it('online when connected and accepted', () => {
    const f = folder({ deviceIDs: [ID_B], deviceAccepted: { [ID_B]: true } });
    const { members } = useFolderDevices(f, [device(ID_B, 'B', true)]);
    expect(members[0].status).toBe('online');
  });

  it('pending when deviceAccepted is false — regardless of connected', () => {
    const f = folder({ deviceIDs: [ID_B], deviceAccepted: { [ID_B]: false } });
    const { members } = useFolderDevices(f, [device(ID_B, 'B', true)]);
    expect(members[0].status).toBe('pending');
  });

  it('offline when not connected and accepted', () => {
    const f = folder({ deviceIDs: [ID_B], deviceAccepted: { [ID_B]: true } });
    const { members } = useFolderDevices(f, [device(ID_B, 'B', false)]);
    expect(members[0].status).toBe('offline');
  });

  it('pending takes priority over connected', () => {
    // Device is connected but hasn't sent state — should be amber, not green
    const f = folder({ deviceIDs: [ID_B], deviceAccepted: { [ID_B]: false } });
    const { members } = useFolderDevices(f, [device(ID_B, 'B', true)]);
    expect(members[0].status).toBe('pending');
  });

  it('pending when deviceAccepted map exists but device key is missing', () => {
    // Key missing in the map — backend now always includes explicit false entries,
    // but the frontend should handle missing keys gracefully too.
    const f = folder({ deviceIDs: [ID_B], deviceAccepted: {} });
    const { members } = useFolderDevices(f, [device(ID_B, 'B', true)]);
    expect(members[0].status).toBe('pending');
  });

  it('mixed status: B accepted (green), C pending (amber)', () => {
    const f = folder({
      deviceIDs: [ID_B, ID_C],
      deviceAccepted: { [ID_B]: true, [ID_C]: false },
    });
    const devices = [device(ID_B, 'B', true), device(ID_C, 'C', true)];
    const { members } = useFolderDevices(f, devices);
    const b = members.find((m) => m.deviceID === ID_B)!;
    const c = members.find((m) => m.deviceID === ID_C)!;
    expect(b.status).toBe('online');
    expect(c.status).toBe('pending');
  });
});

// ── available ─────────────────────────────────────────────────────────────────

describe('available', () => {
  it('returns devices not in the folder', () => {
    const f = folder({ deviceIDs: [ID_B] });
    const { available } = useFolderDevices(f, [device(ID_A, 'A'), device(ID_B, 'B')]);
    expect(available).toHaveLength(1);
    expect(available[0].deviceID).toBe(ID_A);
  });

  it('returns empty when all paired devices are already in folder', () => {
    const f = folder({ deviceIDs: [ID_A, ID_B] });
    const { available } = useFolderDevices(f, [device(ID_A, 'A'), device(ID_B, 'B')]);
    expect(available).toHaveLength(0);
  });

  it('available devices are online/offline, never pending', () => {
    const f = folder({ deviceIDs: [] });
    const { available } = useFolderDevices(f, [device(ID_A, 'A', true), device(ID_B, 'B', false)]);
    expect(available[0].status).toBe('online');
    expect(available[1].status).toBe('offline');
  });

  it('returns empty when folder is undefined', () => {
    const { members, available } = useFolderDevices(undefined, [device(ID_A, 'A')]);
    expect(members).toHaveLength(0);
    expect(available).toHaveLength(1);
  });
});
