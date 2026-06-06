import { describe, it, expect } from 'vitest';
import { FOLDER_RELATION_STATES, isAccepted, type FolderRelationState } from './client';

// These tests pin the TS mirror to Go's authoritative enum. If a new state
// is added to internal/api/folder_relation_state.go, add it here, to the
// FolderRelationState type union, and to FOLDER_RELATION_STATES.
//
// Likewise if a state changes its "accepted" classification — keep both
// languages aligned.
describe('FolderRelationState', () => {
  it('enumerates exactly the states from the state model', () => {
    expect(FOLDER_RELATION_STATES).toEqual([
      'not-member',
      'invited',
      'accepted-paused-local',
      'accepted-paused-remote',
      'accepted-syncing',
      'accepted-sending',
      'accepted-stalled',
      'accepted-idle',
      'accepted-behind-offline',
      'accepted-offline',
    ]);
  });

  it.each([
    ['not-member', false],
    ['invited', false],
    ['accepted-paused-local', true],
    ['accepted-paused-remote', true],
    ['accepted-syncing', true],
    ['accepted-sending', true],
    ['accepted-stalled', true],
    ['accepted-idle', true],
    ['accepted-behind-offline', true],
    ['accepted-offline', true],
  ] as const)('isAccepted(%s) === %s', (state: FolderRelationState, want) => {
    expect(isAccepted(state)).toBe(want);
  });

  it('isAccepted(undefined) returns false — defensive for legacy data', () => {
    expect(isAccepted(undefined)).toBe(false);
  });
});
