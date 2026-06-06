import { createContext, useCallback, useContext, useRef, useState } from 'react';
import { BaseModal, ModalFooter } from '../Modal/Modal';

// ── Types ─────────────────────────────────────────────────────────────────────

export interface ConfirmOptions {
  title: string;
  description?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: 'danger' | 'primary';
}

interface PendingConfirm {
  options: ConfirmOptions;
  onConfirm: () => void;
}

interface ContextValue {
  request: (options: ConfirmOptions, onConfirm: () => void) => void;
}

// ── Context ───────────────────────────────────────────────────────────────────

const Ctx = createContext<ContextValue>({ request: () => {} });

// ── Provider ──────────────────────────────────────────────────────────────────

export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const [pending, setPending] = useState<PendingConfirm | null>(null);

  const request = useCallback((options: ConfirmOptions, onConfirm: () => void) => {
    setPending({ options, onConfirm });
  }, []);

  const handleConfirm = () => {
    pending?.onConfirm();
    setPending(null);
  };
  const handleCancel = () => setPending(null);

  return (
    <Ctx.Provider value={{ request }}>
      {children}
      <BaseModal
        open={pending !== null}
        title={pending?.options.title ?? ''}
        description={pending?.options.description}
      >
        <ModalFooter
          confirmLabel={pending?.options.confirmLabel}
          cancelLabel={pending?.options.cancelLabel}
          confirmVariant={pending?.options.variant ?? 'danger'}
          onConfirm={handleConfirm}
          onCancel={handleCancel}
        />
      </BaseModal>
    </Ctx.Provider>
  );
}

// ── Hook ──────────────────────────────────────────────────────────────────────

export function useConfirm<T extends unknown[]>(
  action: (...args: T) => void | Promise<void>,
  options: ConfirmOptions,
): (...args: T) => void {
  const { request } = useContext(Ctx);
  const actionRef = useRef(action);
  actionRef.current = action;

  return useCallback(
    (...args: T) => {
      request(options, () => actionRef.current(...args));
    },
    // We intentionally depend on the option fields, not the `options` object
    // identity (callers pass a fresh object literal each render).
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [request, options.title, options.description, options.confirmLabel, options.variant],
  );
}
