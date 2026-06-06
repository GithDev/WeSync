import type { ReactNode } from 'react';

export type PillVariant =
  | 'default' // grey, read-only
  | 'connected' // emerald, removable
  | 'offline' // slate, removable
  | 'pending' // amber, not yet accepted
  | 'selected' // emerald, selected state
  | 'selectable'; // dashed, unselected state

const VARIANTS: Record<PillVariant, string> = {
  default: 'border-slate-200 bg-slate-50 text-slate-500',
  connected:
    'border-emerald-200 bg-emerald-50 text-emerald-700 hover:border-red-200 hover:bg-red-50 hover:text-red-600',
  offline:
    'border-slate-200 bg-slate-50 text-slate-500 hover:border-red-200 hover:bg-red-50 hover:text-red-500',
  pending:
    'border-amber-200 bg-amber-50 text-amber-700 hover:border-red-200 hover:bg-red-50 hover:text-red-500',
  selected: 'border-emerald-300 bg-emerald-50 text-emerald-700',
  selectable:
    'border-dashed border-slate-300 text-slate-500 hover:border-slate-400 hover:bg-slate-50',
};

interface Props {
  variant?: PillVariant;
  fullWidth?: boolean;
  onClick?: () => void;
  title?: string;
  className?: string;
  children: ReactNode;
}

export function Pill({
  variant = 'default',
  fullWidth,
  onClick,
  title,
  className = '',
  children,
}: Props) {
  const shape = fullWidth
    ? 'w-full flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium'
    : 'inline-flex items-center gap-1.5 pl-2.5 pr-2 py-1 rounded-full text-xs font-medium';

  const variantClass = VARIANTS[variant];

  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        title={title}
        className={`group border transition-colors text-left ${shape} ${variantClass} ${className}`}
      >
        {children}
      </button>
    );
  }

  return <span className={`border ${shape} ${variantClass} ${className}`}>{children}</span>;
}
