import type { FolderStatus } from '../../../api/client';
import { formatBytes } from '../../../state/format';

interface Props {
  status: FolderStatus;
  /** compact = just the progress bar, no stats rows */
  compact?: boolean;
}

export function SyncProgress({ status, compact = false }: Props) {
  const syncPct =
    status.globalFiles > 0 ? Math.round((status.inSyncFiles / status.globalFiles) * 100) : 100;

  // While scanning (with a known size) the bar tracks scan progress in amber and
  // is labelled "Scanning"; otherwise it shows sync completion. Before the first
  // scan-progress event (scanPct === 0) we fall back to the sync bar.
  const scanPct = Math.round(status.scanPct);
  const showScan = status.state === 'scanning' && scanPct > 0;
  const barPct = showScan ? scanPct : syncPct;
  const barColor = showScan ? '#f59e0b' : syncPct === 100 ? '#10b981' : '#3b82f6';

  const bar = (
    <div className="w-full bg-slate-100 rounded-full h-1.5 overflow-hidden">
      <div
        className="h-full rounded-full transition-all duration-500"
        style={{ width: `${barPct}%`, backgroundColor: barColor }}
      />
    </div>
  );

  const barRow = (small: boolean) => (
    <div className="flex items-center gap-2">
      {showScan && (
        <span
          className={`${small ? 'text-[10px]' : 'text-xs'} text-amber-500 font-medium flex-shrink-0`}
        >
          Scanning
        </span>
      )}
      <div className="flex-1">{bar}</div>
      <span
        className={`${small ? 'text-[10px] text-slate-400' : 'text-xs text-slate-500'} tabular-nums flex-shrink-0`}
      >
        {barPct}%
      </span>
    </div>
  );

  if (compact) {
    return barRow(true);
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-sm">
        <div className="flex justify-between">
          <span className="text-slate-500">Total files</span>
          <span className="text-slate-700 tabular-nums">{status.globalFiles.toLocaleString()}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-slate-500">Total size</span>
          <span className="text-slate-700 tabular-nums">{formatBytes(status.globalBytes)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-slate-500">In sync</span>
          <span className="text-slate-700 tabular-nums">{status.inSyncFiles.toLocaleString()}</span>
        </div>
        {status.needFiles > 0 && (
          <div className="flex justify-between">
            <span className="text-slate-500">Remaining</span>
            <span className="text-blue-600 tabular-nums">
              {status.needFiles} files · {formatBytes(status.needBytes)}
            </span>
          </div>
        )}
        {status.pullErrors > 0 && (
          <div className="flex justify-between col-span-2">
            <span className="text-slate-500">Errors</span>
            <span className="text-red-500 tabular-nums">{status.pullErrors}</span>
          </div>
        )}
      </div>
      {barRow(false)}
    </div>
  );
}
