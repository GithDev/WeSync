import { describe, it, expect } from 'vitest';
import { deriveFolderSyncSummary, type FolderSyncInput } from './folder-sync-summary';
import type { FolderStatus, FolderRelationState, PeerDetail } from '../api/client';

// Minimal idle/synced FolderStatus; override per case.
function status(over: Partial<FolderStatus> = {}): FolderStatus {
  return {
    state: 'idle',
    needFiles: 0,
    needBytes: 0,
    globalFiles: 10,
    globalBytes: 1000,
    localFiles: 10,
    inSyncFiles: 10,
    pullErrors: 0,
    error: '',
    stateChanged: '',
    paused: false,
    scanPct: 0,
    receiveOnlyTotalItems: 0,
    receiveOnlyChangedFiles: 0,
    receiveOnlyChangedBytes: 0,
    ...over,
  };
}

function input(over: Partial<FolderSyncInput>): FolderSyncInput {
  return {
    status: status(),
    pct: 100,
    deviceState: {},
    devicePeer: {},
    nameOf: (id) => ({ A: 'Laptop', B: 'Server' })[id] ?? id,
    ...over,
  };
}

const dev = (s: FolderRelationState): Record<string, FolderRelationState> => ({ A: s });
const peer = (p: Partial<PeerDetail>): Record<string, PeerDetail> => ({
  A: { needBytes: 0, needItems: 0, completion: 0, ...p },
});

describe('deriveFolderSyncSummary', () => {
  it('returns null before status has loaded', () => {
    expect(deriveFolderSyncSummary(input({ status: null }))).toBeNull();
  });

  it('paused wins over everything', () => {
    const s = deriveFolderSyncSummary(
      input({ status: status({ paused: true }), deviceState: dev('accepted-stalled') }),
    );
    expect(s).toMatchObject({ kind: 'paused', tone: 'slate' });
  });

  it('folder-level failure (state="error") is red', () => {
    const s = deriveFolderSyncSummary(input({ status: status({ state: 'error' }) }))!;
    expect(s.kind).toBe('error');
    expect(s.tone).toBe('red');
  });

  it('pullErrors while idle is amber "not synced", never red', () => {
    // ST counts items that failed the last pull (e.g. source offline) as
    // pullErrors; they stay in needFiles. Idle + failures = stuck, not broken.
    const s = deriveFolderSyncSummary(
      input({ status: status({ pullErrors: 2, needFiles: 2 }), pct: 60 }),
    )!;
    expect(s.kind).toBe('stalled');
    expect(s.tone).toBe('amber');
    expect(s.text).toBe('2 files not synced');
  });

  it('active sync with failures still reads as syncing — it is working', () => {
    const s = deriveFolderSyncSummary(
      input({ status: status({ state: 'syncing', needFiles: 5, pullErrors: 2 }), pct: 50 }),
    )!;
    expect(s.kind).toBe('syncing');
    expect(s.tone).toBe('blue');
    expect(s.text).toBe('Syncing 5 files');
  });

  it('rounding can never fake "Up to date" — one needed file reads as syncing', () => {
    // 9999/10000 rounds to pct=100, but needFiles>0 is the authoritative truth.
    const s = deriveFolderSyncSummary(
      input({
        status: status({ needFiles: 1, needBytes: 100, globalFiles: 10000, inSyncFiles: 9999 }),
        pct: 100,
      }),
    )!;
    expect(s.kind).toBe('syncing');
    expect(s.text).toBe('Syncing 1 file');
  });

  it('scanning shows percent', () => {
    const s = deriveFolderSyncSummary(
      input({ status: status({ state: 'scanning', scanPct: 42 }), pct: null }),
    )!;
    expect(s).toMatchObject({ kind: 'scanning', pct: 42, pulse: true });
  });

  it('our own pull dominates a peer that is also behind', () => {
    const s = deriveFolderSyncSummary(
      input({
        status: status({ state: 'syncing', needFiles: 3 }),
        pct: 40,
        deviceState: dev('accepted-sending'),
      }),
    )!;
    expect(s.kind).toBe('syncing');
    expect(s.text).toBe('Syncing 3 files');
  });

  it('sending names the device and sums bytes left', () => {
    const s = deriveFolderSyncSummary(
      input({ deviceState: dev('accepted-sending'), devicePeer: peer({ needBytes: 1572864 }) }),
    )!;
    expect(s.kind).toBe('sending');
    expect(s.text).toBe('Sending to Laptop — 1.5 MB left');
    expect(s.tone).toBe('blue');
  });

  it('stalled outranks sending', () => {
    const s = deriveFolderSyncSummary(
      input({ deviceState: { A: 'accepted-sending', B: 'accepted-stalled' } }),
    )!;
    expect(s.kind).toBe('stalled');
    expect(s.text).toBe('Stalled — Server');
  });

  it('waiting names an offline peer that still owes data', () => {
    const s = deriveFolderSyncSummary(input({ deviceState: dev('accepted-behind-offline') }))!;
    expect(s).toMatchObject({ kind: 'waiting', tone: 'slate' });
    expect(s.text).toBe('Waiting for Laptop');
  });

  it('multiple sending peers collapse to a count', () => {
    const s = deriveFolderSyncSummary(
      input({ deviceState: { A: 'accepted-sending', B: 'accepted-sending' } }),
    )!;
    expect(s.text).toBe('Sending to 2 devices');
  });

  it('genuinely caught up → Up to date', () => {
    const s = deriveFolderSyncSummary(input({ deviceState: dev('accepted-idle') }))!;
    expect(s).toMatchObject({ kind: 'synced', tone: 'emerald', text: 'Up to date' });
  });

  it('idle + an offline peer that owes nothing is still synced', () => {
    const s = deriveFolderSyncSummary(input({ deviceState: dev('accepted-offline') }))!;
    expect(s.kind).toBe('synced');
  });
});
