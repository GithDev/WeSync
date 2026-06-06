import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { AsyncButton } from '../Button/AsyncButton';

export type RowStatus = 'online' | 'offline' | 'pending' | 'waiting';

const dotClass: Record<RowStatus, string> = {
  online: 'bg-emerald-400',
  offline: 'bg-slate-300',
  pending: 'bg-amber-400',
  waiting: 'bg-amber-400 animate-pulse',
};

// ── ListCard ───────────────────────────────────────────────────────────────────

interface ListCardProps {
  title?: string;
  children: ReactNode;
  /** Rendered below the row list, separated by a hairline. Use for "add" sections. */
  footer?: ReactNode;
  className?: string;
}

export function ListCard({ title, children, footer, className = '' }: ListCardProps) {
  return (
    <div className={`bg-white rounded-2xl border border-slate-200 ${className}`}>
      {/* overflow-hidden only on this inner section so row hover-bg clips to rounded corners,
          while the footer (which may contain a dropdown) can overflow freely. */}
      <div className={`overflow-hidden ${footer ? 'rounded-t-2xl' : 'rounded-2xl'}`}>
        {title && (
          <div className="px-4 pt-4 pb-2">
            <p className="text-xs font-semibold text-slate-500 uppercase tracking-wide">{title}</p>
          </div>
        )}
        <div className="divide-y divide-slate-100">{children}</div>
      </div>
      {footer && <div className="border-t border-slate-100">{footer}</div>}
    </div>
  );
}

// ── ListRow ────────────────────────────────────────────────────────────────────

interface ListRowProps {
  primary: string;
  secondary?: string | null;
  /** Colored status dot. Ignored when `leading` is set. */
  status?: RowStatus;
  /** Custom leading element (icon, avatar…). Takes the place of the status dot. */
  leading?: ReactNode;
  trailing?: ReactNode;
  /** Render as <Link to={...}> */
  to?: string;
  /** Render as <button onClick={...}>. Ignored when `to` is set. */
  onClick?: () => void;
  /**
   * 'default' — flat row inside a ListCard (divide-y provides separation).
   * 'add'     — standalone rounded row with dashed border (for "add device" affordances).
   */
  variant?: 'default' | 'add';
  className?: string;
}

export function ListRow({
  primary,
  secondary,
  status,
  leading,
  trailing,
  to,
  onClick,
  variant = 'default',
  className = '',
}: ListRowProps) {
  const isAdd = variant === 'add';

  const inner = (
    <>
      {leading ? (
        <span className="flex-shrink-0 flex items-center">{leading}</span>
      ) : (
        status && <span className={`w-2 h-2 rounded-full flex-shrink-0 ${dotClass[status]}`} />
      )}
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-slate-800 truncate">{primary}</p>
        {secondary && <p className="text-xs text-slate-400 truncate">{secondary}</p>}
      </div>
      {trailing !== undefined && (
        <div className="flex items-center gap-2 flex-shrink-0">{trailing}</div>
      )}
    </>
  );

  const addBase = `flex items-center gap-3 px-4 py-2.5 rounded-xl border border-dashed border-slate-300 hover:border-blue-300 hover:bg-blue-50 transition-colors w-full text-left ${className}`;
  const defaultBase = `flex items-center gap-3 px-4 py-2.5 w-full ${className}`;

  if (isAdd) {
    if (to) {
      return (
        <Link to={to} className={addBase}>
          {inner}
        </Link>
      );
    }
    return (
      <button type="button" onClick={onClick} className={addBase}>
        {inner}
      </button>
    );
  }

  if (to) {
    return (
      <Link to={to} className={`${defaultBase} hover:bg-slate-50 transition-colors`}>
        {inner}
      </Link>
    );
  }
  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        className={`${defaultBase} hover:bg-slate-50 transition-colors`}
      >
        {inner}
      </button>
    );
  }
  return <div className={defaultBase}>{inner}</div>;
}

// ── Helpers ────────────────────────────────────────────────────────────────────

/** Standard right-facing chevron for navigable rows. */
export function RowChevron() {
  return (
    <svg
      className="w-4 h-4 text-slate-300 flex-shrink-0"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <path d="M9 18l6-6-6-6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** Standard × remove button for use in a ListRow's trailing slot. Async-aware:
 *  the × becomes a spinner and the button disables while the (often backend)
 *  onClick is in flight. Stops propagation so it never triggers a clickable row. */
export function RowRemoveButton({
  onClick,
  title,
}: {
  onClick: () => Promise<void> | void;
  title?: string;
}) {
  return (
    <AsyncButton
      unstyled
      title={title}
      onClick={(e) => {
        e.stopPropagation();
        return onClick();
      }}
      className="w-7 h-7 rounded-lg text-slate-400 hover:text-red-500 hover:bg-red-50"
      icon={
        <svg
          viewBox="0 0 24 24"
          className="w-3.5 h-3.5"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
        >
          <path d="M18 6L6 18M6 6l12 12" />
        </svg>
      }
    />
  );
}
