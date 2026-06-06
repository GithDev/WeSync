import { useMemo } from 'react';
import { useFolderStatus } from './useFolderStatus';
import { useWS } from '../api/websocket';
import { deriveFolderSyncSummary, type SyncSummary } from '../state/folder-sync-summary';
import type { WeSyncFolder, FolderStatus } from '../api/client';

// useFolderSyncStatus is the one place the UI asks "what is this folder's honest
// status?". It wraps useFolderStatus (local ST state) and resolves the per-peer
// reach truth into a single SyncSummary, naming devices from the live WS device
// list. A drop-in superset of useFolderStatus — returns status/pct/refresh too —
// so any folder view can swap to it and gain `summary` for free.
export function useFolderSyncStatus(folder: WeSyncFolder | undefined): {
  status: FolderStatus | null;
  pct: number | null;
  refresh: () => Promise<void>;
  summary: SyncSummary | null;
} {
  const { status, pct, refresh } = useFolderStatus(folder?.id);
  const { devices } = useWS();

  const deviceState = folder?.deviceState;
  const devicePeer = folder?.devicePeer;
  const summary = useMemo(() => {
    const nameOf = (id: string) => {
      const d = devices.find((x) => x.deviceID === id);
      return d?.name || d?.info?.hostname || id.slice(0, 7);
    };
    return deriveFolderSyncSummary({
      status,
      pct,
      deviceState: deviceState ?? {},
      devicePeer: devicePeer ?? {},
      nameOf,
    });
  }, [status, pct, deviceState, devicePeer, devices]);

  return { status, pct, refresh, summary };
}
