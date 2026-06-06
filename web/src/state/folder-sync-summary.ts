import type { FolderRelationState, FolderStatus, PeerDetail } from '../api/client';
import { formatBytes } from './format';

// folder-sync-summary — the SINGLE honest answer to "what is this folder's
// status?", combining our own local folder state (paused / error / scanning /
// syncing) with the per-peer reach truth (sending / stalled / waiting) that the
// old "Up to date" hid. One ordered resolution, pure + testable; the hook
// (useFolderSyncStatus) feeds it live data and the FolderSyncStatus component
// renders it. This lives at FOLDER level on purpose — the per-device row stays a
// pure relationship badge (see folder-display.ts), it does not repeat this.

export type SyncTone = 'emerald' | 'blue' | 'amber' | 'slate' | 'red';

export type SyncKind =
  | 'paused'
  | 'error'
  | 'scanning'
  | 'syncing' // our own pull/scan of this folder
  | 'sending' // a connected peer is still pulling our data
  | 'stalled' // stuck: data is needed but nothing is flowing — a peer's receive, or our own failed pulls
  | 'waiting' // an offline peer still owes data — not there yet
  | 'synced'; // genuinely caught up everywhere

export interface SyncSummary {
  kind: SyncKind;
  text: string;
  tone: SyncTone;
  /** 0–100 progress for the bar; present for scanning/syncing only. */
  pct?: number;
  /** Animate the status dot — active, in-flight states. */
  pulse?: boolean;
}

export interface FolderSyncInput {
  status: FolderStatus | null;
  pct: number | null;
  deviceState: Record<string, FolderRelationState>;
  devicePeer: Record<string, PeerDetail>;
  /** Resolve a deviceID to its display name (for "Sending to <name>"). */
  nameOf: (deviceID: string) => string;
}

// Name a set of devices: the single one by name, otherwise "N devices".
function nameList(ids: string[], nameOf: (id: string) => string): string {
  if (ids.length === 1) return nameOf(ids[0]);
  return `${ids.length} devices`;
}

// deriveFolderSyncSummary reduces a folder's live state to exactly one honest
// status. Resolution order is fixed: our own local work first (it dominates the
// folder), then the per-peer reach question once we're idle locally. Returns
// null when status hasn't loaded yet.
export function deriveFolderSyncSummary(input: FolderSyncInput): SyncSummary | null {
  const { status, pct, deviceState, devicePeer, nameOf } = input;
  if (!status) return null;

  if (status.paused) return { kind: 'paused', text: 'Paused', tone: 'slate' };

  // Only a folder-level failure (missing path / marker, bad perms — ST sets
  // state="error") is a real, red error. pullErrors is NOT: ST counts items
  // that failed the last pull cycle, which includes the everyday "the device
  // holding this file is offline / not connected" and other transient cases ST
  // retries every interval. Those items are simply out of sync, not broken —
  // they stay in needFiles and surface below as "Syncing". The raw pullErrors
  // count is still shown as a detail in SyncProgress.
  if (status.state === 'error') {
    return { kind: 'error', text: 'Error', tone: 'red' };
  }

  if (status.state === 'scanning') {
    const s = Math.round(status.scanPct);
    return {
      kind: 'scanning',
      text: s > 0 ? `Scanning ${s}%` : 'Scanning…',
      tone: 'amber',
      pct: s,
      pulse: true,
    };
  }

  // Our own pull: do we still need data? Decide on the AUTHORITATIVE raw counts
  // (needFiles/needBytes), never on the rounded pct — pct is the progress-bar
  // fill only, and round(inSyncFiles/globalFiles) can round a handful of stuck
  // files away to 100, falsely reading "Up to date". One needed byte ⇒ not
  // synced. (Failed items stay in needFiles, so this also covers pullErrors.)
  const needsPull = status.needFiles > 0 || status.needBytes > 0;
  if (status.state === 'syncing' || needsPull) {
    const n = status.needFiles;
    // Stuck items: they failed the last pull and nothing is actively flowing
    // (ST isn't in a sync cycle right now). Honest amber "not synced" beats a
    // pulsing blue "Syncing" that implies motion. A genuine in-flight sync
    // (state === 'syncing') always reads as syncing, errors or not — it's working.
    if (status.state !== 'syncing' && status.pullErrors > 0) {
      return {
        kind: 'stalled',
        text: n > 0 ? `${n} file${n !== 1 ? 's' : ''} not synced` : 'Not synced',
        tone: 'amber',
      };
    }
    const text = n > 0 ? `Syncing ${n} file${n !== 1 ? 's' : ''}` : 'Syncing…';
    return { kind: 'syncing', text, tone: 'blue', pct: pct ?? undefined, pulse: true };
  }

  // Folder is idle locally — now the honest question: has our data reached every
  // peer? (A sender sits idle while a client downloads; the old code lied "Up to
  // date" here.) Worst-first: stalled > sending > waiting-for-offline.
  const ids = Object.keys(deviceState);
  const withState = (s: FolderRelationState) => ids.filter((id) => deviceState[id] === s);
  const stalled = withState('accepted-stalled');
  const sending = withState('accepted-sending');
  const behind = withState('accepted-behind-offline');

  if (stalled.length > 0) {
    return { kind: 'stalled', text: `Stalled — ${nameList(stalled, nameOf)}`, tone: 'amber' };
  }
  if (sending.length > 0) {
    const bytes = sending.reduce((sum, id) => sum + (devicePeer[id]?.needBytes ?? 0), 0);
    const tail = bytes > 0 ? ` — ${formatBytes(bytes)} left` : '';
    return {
      kind: 'sending',
      text: `Sending to ${nameList(sending, nameOf)}${tail}`,
      tone: 'blue',
      pulse: true,
    };
  }
  if (behind.length > 0) {
    return { kind: 'waiting', text: `Waiting for ${nameList(behind, nameOf)}`, tone: 'slate' };
  }

  return { kind: 'synced', text: 'Up to date', tone: 'emerald' };
}
