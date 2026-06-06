/**
 * FolderSyncStatus — the single folder-level status indicator.
 *
 * It renders a pre-computed SyncSummary (see state/folder-sync-summary.ts), which
 * combines our own local folder state (paused / error / scanning / syncing) with
 * the per-peer reach truth (sending / stalled / waiting) into one honest answer.
 * Get the summary from the useFolderSyncStatus hook. The per-device row is a pure
 * relationship badge and deliberately does NOT repeat any of this.
 *
 * variant='compact'  list cards — dot + text, with a progress bar while scan/sync.
 * variant='badge'    detail header — a coloured pill.
 */

import type { SyncSummary, SyncTone } from '../../../state/folder-sync-summary';

const TONE: Record<SyncTone, { text: string; dot: string; badge: string }> = {
  emerald: {
    text: 'text-emerald-600',
    dot: 'bg-emerald-500',
    badge: 'text-emerald-700 bg-emerald-50 border-emerald-200',
  },
  blue: {
    text: 'text-blue-600',
    dot: 'bg-blue-500',
    badge: 'text-blue-600 bg-blue-50 border-blue-200',
  },
  amber: {
    text: 'text-amber-600',
    dot: 'bg-amber-500',
    badge: 'text-amber-600 bg-amber-50 border-amber-200',
  },
  slate: {
    text: 'text-slate-500',
    dot: 'bg-slate-400',
    badge: 'text-slate-500 bg-slate-100 border-slate-200',
  },
  red: {
    text: 'text-red-600',
    dot: 'bg-red-500',
    badge: 'text-red-600 bg-red-50 border-red-200',
  },
};

interface Props {
  summary: SyncSummary | null;
  variant?: 'compact' | 'badge';
}

export function FolderSyncStatus({ summary, variant = 'compact' }: Props) {
  if (!summary) return null;
  const tone = TONE[summary.tone];
  const dot = (
    <span
      className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${tone.dot} ${summary.pulse ? 'animate-pulse' : ''}`}
    />
  );

  if (variant === 'badge') {
    return (
      <span
        className={`inline-flex items-center gap-1.5 text-xs font-medium rounded-full px-2.5 py-0.5 border ${tone.badge}`}
      >
        {dot}
        {summary.text}
      </span>
    );
  }

  // compact
  const showBar = summary.kind === 'scanning' || summary.kind === 'syncing';
  const pct = summary.pct ?? 0;
  return (
    <div className="flex flex-col gap-1 pt-1">
      <div className="flex items-center gap-1.5">
        {dot}
        <span className={`text-xs font-medium ${tone.text}`}>{summary.text}</span>
        {showBar && pct > 0 && (
          <span className="text-[10px] text-slate-400 tabular-nums ml-auto">{pct}%</span>
        )}
      </div>
      {showBar &&
        (pct > 0 ? (
          <div className="h-1.5 bg-slate-100 rounded-full overflow-hidden">
            <div
              className={`h-full rounded-full transition-all duration-500 ${tone.dot}`}
              style={{ width: `${pct}%` }}
            />
          </div>
        ) : (
          // Size unknown yet — indeterminate slide so the bar never looks stuck at 0.
          <div className="h-1.5 bg-slate-100 rounded-full overflow-hidden relative">
            <div className={`absolute h-full rounded-full animate-scan-slide ${tone.dot}`} />
          </div>
        ))}
    </div>
  );
}
