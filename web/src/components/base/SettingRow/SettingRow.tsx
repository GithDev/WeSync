import { useState, type ReactNode } from 'react';
import { Card } from '../Card/Card';

// A collapsible settings row: the group's title on the left, a one-line
// summary of the *current* choice on the right, and a chevron. Expands to
// reveal the controls that change that choice.
//
// Each row owns its own open/closed state — this is the scannable-list pattern:
// read every current value at a glance, expand only what you want to change.
// Pass `defaultOpen` for a row that should start expanded (e.g. the only
// setting present on a given platform).
export function SettingRow({
  title,
  summary,
  defaultOpen = false,
  children,
}: {
  title: string;
  /** Current value, shown on the right while collapsed. Omit for rows with no single value (e.g. an activity log). */
  summary?: ReactNode;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Card className="overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center justify-between gap-3 px-4 py-3 text-left"
      >
        <span className="text-sm font-semibold text-slate-800 flex-shrink-0">{title}</span>
        <span className="flex items-center gap-2 min-w-0">
          {/* Hide the summary once open — the controls below now carry the detail. */}
          {summary != null && !open && (
            <span className="text-xs text-slate-500 truncate">{summary}</span>
          )}
          <span className="text-slate-400 text-sm flex-shrink-0">{open ? '▾' : '▸'}</span>
        </span>
      </button>
      {/* The card stays a single white surface; a hairline seams the clickable
          header to the body. We deliberately DON'T tint the body — the row's
          content carries its own structure (coloured option cards, divided
          rows), so a contrasting backdrop would just muddy it. Sectioning is
          done with dividers and spacing, not nested boxes. */}
      {open && <div className="border-t border-slate-100 px-4 py-4">{children}</div>}
    </Card>
  );
}
