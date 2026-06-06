import type { ButtonHTMLAttributes } from 'react';

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'warning' | 'ghost';
  size?: 'sm' | 'md';
  outlined?: boolean;
  /** Skip the variant + size styling and let `className` fully control the look,
   *  while keeping the shared base (flex/centering, transition, disabled state).
   *  For one-off shapes like the visibility pill that still want AsyncButton's
   *  spinner/disabled behaviour without the standard button skin. */
  unstyled?: boolean;
}

const filled = {
  primary: 'bg-blue-600 text-white hover:bg-blue-700',
  secondary: 'bg-slate-100 text-slate-700 hover:bg-slate-200',
  danger: 'bg-red-600 text-white hover:bg-red-700',
  warning: 'bg-amber-600 text-white hover:bg-amber-700',
  ghost:
    'border border-slate-200 text-slate-600 hover:border-slate-400 hover:text-slate-800 bg-transparent',
};

const outline = {
  primary: 'border border-blue-400 text-blue-600 hover:bg-blue-50 bg-transparent',
  secondary: 'border border-slate-300 text-slate-600 hover:bg-slate-50 bg-transparent',
  danger: 'border border-red-300 text-red-500 hover:bg-red-50 bg-transparent',
  warning: 'border border-amber-300 text-amber-700 hover:bg-amber-100 bg-transparent',
  ghost: 'border border-slate-200 text-slate-500 hover:border-slate-400 bg-transparent',
};

const sizes = {
  sm: 'px-2.5 py-1 text-xs rounded-lg',
  md: 'px-5 py-2.5 text-sm rounded-xl',
};

const base =
  'inline-flex items-center justify-center font-medium transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed';

export function Button({
  variant = 'primary',
  size = 'md',
  outlined = false,
  unstyled = false,
  className,
  children,
  ...rest
}: Props) {
  const style = unstyled ? '' : `${outlined ? outline[variant] : filled[variant]} ${sizes[size]}`;
  return (
    <button type="button" className={`${base} ${style} ${className ?? ''}`} {...rest}>
      {children}
    </button>
  );
}
