import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import type { FolderStatus } from '../api/client';

export function useFolderStatus(folderID: string | undefined) {
  const [status, setStatus] = useState<FolderStatus | null>(null);

  const refresh = useCallback(async () => {
    if (!folderID) return;
    try {
      setStatus(await api.getFolderStatus(folderID));
    } catch {
      // ST may not have the folder yet
    }
  }, [folderID]);

  useEffect(() => {
    refresh();
    const busy =
      status?.state === 'syncing' || status?.state === 'scanning' || (status?.needFiles ?? 0) > 0;
    const interval = setInterval(refresh, busy ? 3_000 : 8_000);
    return () => clearInterval(interval);
  }, [refresh, status?.state, status?.needFiles]);

  const pct =
    status && status.globalFiles > 0
      ? Math.round((status.inSyncFiles / status.globalFiles) * 100)
      : null;

  return { status, pct, refresh };
}
