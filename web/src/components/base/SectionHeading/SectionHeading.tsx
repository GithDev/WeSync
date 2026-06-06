import type { ReactNode } from 'react';

/** The small uppercase slate label that titles each page section. */
export function SectionHeading({
  className = '',
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <h2 className={`text-xs font-semibold text-slate-500 uppercase tracking-wide ${className}`}>
      {children}
    </h2>
  );
}
