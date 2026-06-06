import { describe, it, expect } from 'vitest';
import { folderDeviceDisplay, dotColorClass, folderStateToRowStatus } from './folder-display';
import { FOLDER_RELATION_STATES } from '../api/client';

describe('folderDeviceDisplay', () => {
  // Pin the full mapping. New states added to FOLDER_RELATION_STATES without
  // updating this switch will hit the default case and surface here.
  it.each([
    ['invited', ['amber', true, undefined]],
    ['accepted-idle', ['emerald', false, undefined]],
    ['accepted-syncing', ['emerald', false, 'Syncing']],
    // sending/stalled stay a live accepted relationship (green, no label) — the
    // transfer truth lives at folder level, not in the per-device badge.
    ['accepted-sending', ['emerald', false, undefined]],
    ['accepted-stalled', ['emerald', false, undefined]],
    ['accepted-behind-offline', ['slate', false, undefined]],
    ['accepted-offline', ['slate', false, undefined]],
    ['accepted-paused-local', ['slate', false, 'Paused']],
    ['accepted-paused-remote', ['slate', false, 'Paused by remote']],
    ['not-member', ['slate', false, undefined]],
  ] as const)('display for %s', (state, [dot, pending, label]) => {
    const d = folderDeviceDisplay(state);
    expect(d.dotColor).toBe(dot);
    expect(d.pending).toBe(pending);
    expect(d.label).toBe(label);
  });

  it('undefined state — defensive, no UI commitments', () => {
    const d = folderDeviceDisplay(undefined);
    expect(d.dotColor).toBe('slate');
    expect(d.pending).toBe(false);
    expect(d.label).toBeUndefined();
  });

  // Drift detector: if a new FolderRelationState is added but not handled
  // here, this test fails because the union includes a state the test
  // matrix doesn't cover.
  it('covers every FolderRelationState the enum declares', () => {
    const covered = new Set([
      'invited',
      'accepted-idle',
      'accepted-syncing',
      'accepted-sending',
      'accepted-stalled',
      'accepted-behind-offline',
      'accepted-offline',
      'accepted-paused-local',
      'accepted-paused-remote',
      'not-member',
    ]);
    for (const s of FOLDER_RELATION_STATES) {
      expect(covered.has(s)).toBe(true);
    }
    expect(covered.size).toBe(FOLDER_RELATION_STATES.length);
  });
});

describe('dotColorClass', () => {
  it.each([
    ['amber', 'bg-amber-400'],
    ['emerald', 'bg-emerald-400'],
    ['slate', 'bg-slate-300'],
  ] as const)('%s → %s', (color, cls) => {
    expect(dotColorClass(color)).toBe(cls);
  });
});

describe('folderStateToRowStatus', () => {
  it.each([
    ['invited', 'waiting'],
    ['accepted-idle', 'online'],
    ['accepted-syncing', 'online'],
    ['accepted-sending', 'online'],
    ['accepted-stalled', 'online'],
    ['accepted-behind-offline', 'offline'],
    ['accepted-offline', 'offline'],
    ['accepted-paused-local', 'offline'],
    ['accepted-paused-remote', 'offline'],
    ['not-member', 'offline'],
  ] as const)('%s → %s', (state, want) => {
    expect(folderStateToRowStatus(state)).toBe(want);
  });

  it('undefined → offline (defensive)', () => {
    expect(folderStateToRowStatus(undefined)).toBe('offline');
  });
});
