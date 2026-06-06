import type { FolderRelationState } from '../api/client';
import type { RowStatus } from '../components/base/ListRow/ListRow';

// Central display mapping for the per-(folder, device) FolderRelationState.
// Everything UI consumes (color, badge text, "pending" treatment) flows
// through here — components never switch on the raw state string. New
// states are added in three places together: the Go enum
// (internal/api/folder_relation_state.go), the TS union (api/client.ts),
// and this switch.
//
// See docs/state-model.md for the underlying model. The visual choices below
// reflect WeSync's color language:
//   - amber: needs user action (invited)
//   - emerald: live connection, healthy
//   - slate: accepted but quiet (offline / paused)

export type StatusDotColor = 'amber' | 'emerald' | 'slate';

export interface FolderDeviceDisplay {
  /** Color of the small status dot rendered next to the device name. */
  dotColor: StatusDotColor;
  /**
   * Whether the device is awaiting acceptance — drives the "Invited" badge
   * and suppresses direction-arrow rendering. Distinct from offline/paused.
   */
  pending: boolean;
  /**
   * Secondary label rendered in muted text next to the device name. Empty
   * for the common idle case; populated for non-default states the user
   * benefits from seeing inline ("Syncing", "Paused by remote", etc.).
   */
  label?: string;
}

export function folderDeviceDisplay(state: FolderRelationState | undefined): FolderDeviceDisplay {
  switch (state) {
    case 'invited':
      return { dotColor: 'amber', pending: true };
    case 'accepted-idle':
      return { dotColor: 'emerald', pending: false };
    case 'accepted-syncing':
      return { dotColor: 'emerald', pending: false, label: 'Syncing' };
    // accepted-sending / accepted-stalled are still a live, accepted relationship
    // (connected, dot green). Whether B has actually received our data is a
    // TRANSFER fact, surfaced at FOLDER level (see folder-sync-summary), NOT
    // crammed into the per-device relationship badge.
    case 'accepted-sending':
    case 'accepted-stalled':
      return { dotColor: 'emerald', pending: false };
    // Offline counterparts: just "offline" at the relationship level. The folder
    // status says whether anything is still owed to them.
    case 'accepted-behind-offline':
    case 'accepted-offline':
      return { dotColor: 'slate', pending: false };
    case 'accepted-paused-local':
      return { dotColor: 'slate', pending: false, label: 'Paused' };
    case 'accepted-paused-remote':
      return { dotColor: 'slate', pending: false, label: 'Paused by remote' };
    case 'not-member':
    case undefined:
    default:
      // Defensive: an unknown state shouldn't crash UI but also shouldn't
      // claim anything misleading — grey dot, no badges, no label.
      return { dotColor: 'slate', pending: false };
  }
}

// Tailwind class for a dot color — kept here so callers can't accidentally
// drift from the color language defined above.
const DOT_CLASS: Record<StatusDotColor, string> = {
  amber: 'bg-amber-400',
  emerald: 'bg-emerald-400',
  slate: 'bg-slate-300',
};

export function dotColorClass(c: StatusDotColor): string {
  return DOT_CLASS[c];
}

// ListRow uses its own four-valued RowStatus enum for the leading dot.
// Map from FolderRelationState so any ListRow-rendered list of folder
// devices (FolderPage's "Shared with" table) stays consistent with the
// pill-rendered version (FolderGroup).
//
// "waiting" (amber + pulse) is chosen for `invited` to draw the user's
// eye to action-needed rows. Paused states sit on the offline dot —
// the inline label carries the nuance.
export function folderStateToRowStatus(state: FolderRelationState | undefined): RowStatus {
  switch (state) {
    case 'invited':
      return 'waiting';
    case 'accepted-idle':
    case 'accepted-syncing':
    case 'accepted-sending':
    case 'accepted-stalled':
      // All still a live accepted relationship → online dot. Transfer health
      // (sending / stalled) is a folder-level concern, not a row dot.
      return 'online';
    case 'accepted-offline':
    case 'accepted-behind-offline':
    case 'accepted-paused-local':
    case 'accepted-paused-remote':
      return 'offline';
    case 'not-member':
    case undefined:
    default:
      return 'offline';
  }
}
