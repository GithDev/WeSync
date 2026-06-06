import { createContext, useCallback, useContext, useState, useRef } from 'react';

export type ToastKind = 'info' | 'success' | 'warning' | 'error';

export interface ToastAction {
  label: string;
  onClick: () => void;
}

interface ToastItem {
  id: number;
  message: string;
  kind: ToastKind;
  action?: ToastAction;
}

interface ToastContextValue {
  addToast: (message: string, kind?: ToastKind, action?: ToastAction) => void;
}

const ToastContext = createContext<ToastContextValue>({ addToast: () => {} });

export function useToast() {
  return useContext(ToastContext);
}

const KIND: Record<ToastKind, { border: string; icon: string; action: string }> = {
  info: {
    border: 'border-blue-200',
    icon: 'text-blue-500',
    action: 'text-blue-600 hover:text-blue-800',
  },
  success: {
    border: 'border-emerald-200',
    icon: 'text-emerald-500',
    action: 'text-emerald-600 hover:text-emerald-800',
  },
  warning: {
    border: 'border-amber-200',
    icon: 'text-amber-500',
    action: 'text-amber-600 hover:text-amber-800',
  },
  error: {
    border: 'border-red-200',
    icon: 'text-red-500',
    action: 'text-red-600 hover:text-red-800',
  },
};

const ICON: Record<ToastKind, string> = {
  info: '●',
  success: '✓',
  warning: '!',
  error: '✕',
};

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const counter = useRef(0);

  const addToast = useCallback(
    (message: string, kind: ToastKind = 'info', action?: ToastAction) => {
      const id = ++counter.current;
      setToasts((prev) => [
        ...prev,
        {
          id,
          message,
          kind,
          action,
        },
      ]);
      setTimeout(() => {
        setToasts((prev) => prev.filter((t) => t.id !== id));
      }, 8000);
    },
    [],
  );

  const dismiss = (id: number) => setToasts((prev) => prev.filter((t) => t.id !== id));

  return (
    <ToastContext.Provider value={{ addToast }}>
      {children}
      <div className="fixed bottom-20 right-4 sm:bottom-5 sm:right-5 flex flex-col gap-2 z-50 pointer-events-none max-w-sm w-full">
        {toasts.map((t) => {
          const s = KIND[t.kind];
          return (
            <div
              key={t.id}
              className={`bg-white border ${s.border} rounded-2xl shadow-lg px-4 py-3 pointer-events-auto animate-fade-in flex flex-col gap-2`}
            >
              <div className="flex items-start gap-3">
                <span className={`text-xs font-bold mt-0.5 flex-shrink-0 ${s.icon}`}>
                  {ICON[t.kind]}
                </span>
                <p className="text-sm text-slate-700 flex-1 leading-snug">{t.message}</p>
                <button
                  type="button"
                  onClick={() => dismiss(t.id)}
                  className="text-slate-300 hover:text-slate-500 transition-colors text-lg leading-none flex-shrink-0"
                >
                  ×
                </button>
              </div>
              {t.action && (
                <div className="pl-5">
                  <button
                    type="button"
                    onClick={() => {
                      t.action!.onClick();
                      dismiss(t.id);
                    }}
                    className={`text-xs font-semibold transition-colors ${s.action}`}
                  >
                    {t.action.label} →
                  </button>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}
