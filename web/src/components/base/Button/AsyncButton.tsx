import { useState } from 'react';
import type { ComponentProps, MouseEvent, ReactNode } from 'react';
import { Button } from './Button';

type ButtonProps = ComponentProps<typeof Button>;

interface Props extends Omit<ButtonProps, 'onClick'> {
  onClick: (e: MouseEvent<HTMLButtonElement>) => Promise<void> | void;
  /** Optional leading element shown when idle; the spinner replaces it while
   *  in-flight. Omit for a plain button — the spinner then just prepends the
   *  label. Lets one component serve both standard buttons and custom shapes
   *  (e.g. an `unstyled` pill with a status dot) without re-rolling the spinner. */
  icon?: ReactNode;
}

/**
 * A Button that tracks its own async loading state.
 * Shows a spinner and disables itself while onClick is in-flight.
 * Accepts all the same props as Button (variant, size, outlined, unstyled, etc).
 *
 * const handleRemove = useConfirm(...);
 * <AsyncButton variant="danger" outlined onClick={handleRemove}>Remove</AsyncButton>
 * <AsyncButton unstyled icon={dot} onClick={toggle} className="rounded-full …">Visible</AsyncButton>
 */
export function AsyncButton({ onClick, children, disabled, icon, ...rest }: Props) {
  const [loading, setLoading] = useState(false);

  const handle = async (e: MouseEvent<HTMLButtonElement>) => {
    if (loading) return;
    setLoading(true);
    try {
      await onClick(e);
    } finally {
      setLoading(false);
    }
  };

  const spinner = (
    <span className="w-3 h-3 border-2 border-current border-t-transparent rounded-full animate-spin" />
  );

  return (
    <Button onClick={handle} disabled={disabled || loading} {...rest}>
      {loading || icon !== undefined ? (
        <span className="inline-flex items-center gap-1.5">
          {loading ? spinner : icon}
          {children}
        </span>
      ) : (
        children
      )}
    </Button>
  );
}
