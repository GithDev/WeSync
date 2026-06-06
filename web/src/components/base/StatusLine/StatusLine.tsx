import type { ReactNode } from 'react';

// StatusLine — a flat status row: a small coloured dot + a label, with an
// optional truncated detail line underneath. The dot is vertically centred on
// the label. Presentational only; callers wrap it for layout (margins,
// dividers, click handling).
//
// `tone` carries the meaning, so call sites don't hand-pick colours:
//   neutral  idle / "checking…"        (grey)
//   pending  in progress / connecting  (amber)
//   ok       healthy / active          (emerald)
//   error    failed / unreachable      (rose)
//   info     active-but-neutral state  (blue, e.g. "discoverable")

export type StatusTone = 'neutral' | 'pending' | 'ok' | 'error' | 'info';

const DOT: Record<StatusTone, string> = {
  neutral: 'bg-slate-300',
  pending: 'bg-amber-400',
  ok: 'bg-emerald-500',
  error: 'bg-rose-400',
  info: 'bg-blue-400',
};

const LABEL: Record<StatusTone, string> = {
  neutral: 'text-slate-400',
  pending: 'text-slate-500',
  ok: 'text-slate-600',
  error: 'text-slate-600',
  info: 'text-slate-600',
};

interface Props {
  tone: StatusTone;
  label: ReactNode;
  /** Secondary line under the label; truncated with an ellipsis when long. */
  detail?: string;
  /** Pulse the dot — for live/active states (e.g. discoverable, syncing). */
  pulse?: boolean;
  /** Tooltip on the whole row. */
  title?: string;
  /** Wrapper overrides: margins, dividers, etc. */
  className?: string;
}

export function StatusLine({ tone, label, detail, pulse, title, className = '' }: Props) {
  return (
    <div className={className} title={title}>
      <div className="flex items-center gap-2">
        <span
          className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${DOT[tone]} ${pulse ? 'animate-pulse' : ''}`}
        />
        <span className={`text-xs ${LABEL[tone]}`}>{label}</span>
      </div>
      {/* Indented to line up under the label, not the dot (dot 1.5 + gap 2). */}
      {detail && <span className="block ml-3.5 text-[10px] text-slate-400 truncate">{detail}</span>}
    </div>
  );
}
