import type { ReactNode } from 'react';

// The white rounded panel used all over the app. Only the constant part
// (rounded-2xl bg-white border + border colour) lives here; padding, flex and
// gap stay at the call site via `className`, since those vary per card.
type Tone = 'default' | 'amber' | 'red';

const TONE: Record<Tone, string> = {
  default: 'border-slate-200',
  amber: 'border-amber-200',
  red: 'border-red-100',
};

export function Card({
  tone = 'default',
  className = '',
  children,
}: {
  tone?: Tone;
  className?: string;
  children: ReactNode;
}) {
  return <div className={`rounded-2xl bg-white border ${TONE[tone]} ${className}`}>{children}</div>;
}
