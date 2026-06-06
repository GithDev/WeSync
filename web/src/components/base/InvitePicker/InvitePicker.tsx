import { useRef, useState, useCallback } from 'react';
import { useClickOutside } from '../../../hooks/useClickOutside';
import { deviceLabel } from '../../../state/device-display';

export interface InvitableDevice {
  deviceID: string;
  name: string;
  hostname?: string;
  connected: boolean;
}

interface Props {
  available: InvitableDevice[];
  onPick: (deviceID: string) => void;
  /**
   * 'block' — full-width dashed row (folder detail page).
   * 'pill'  — compact "+ Invite" chip (folder list cards).
   */
  variant: 'block' | 'pill';
  /** Extra classes for the positioning wrapper (e.g. padding in 'block'). */
  className?: string;
}

/**
 * The "invite a trusted device to this folder" dropdown. Owns its own
 * open/close + click-outside; the two folder views differ only in trigger and
 * panel placement, captured by `variant`. The device list itself is identical.
 */
export function InvitePicker({ available, onPick, variant, className = '' }: Props) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useClickOutside(
    ref,
    open,
    useCallback(() => setOpen(false), []),
  );

  if (available.length === 0) return null;

  const panelClass =
    variant === 'block'
      ? 'absolute left-4 right-4 top-full mt-1.5 z-50 bg-white rounded-xl border border-slate-200 shadow-lg py-1'
      : 'absolute left-0 top-full mt-1.5 z-20 bg-white rounded-xl border border-slate-200 shadow-lg py-1 min-w-[180px]';

  return (
    <div className={`relative ${className}`} ref={ref}>
      {variant === 'block' ? (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="flex items-center gap-3 w-full px-0 py-1.5 rounded-xl border border-dashed border-slate-300 hover:border-blue-300 hover:bg-blue-50 transition-colors text-left group focus:outline-none"
        >
          <span className="w-2 h-2 rounded-full flex-shrink-0 bg-slate-200 ml-4" />
          <span className="flex-1 text-sm text-slate-400 group-hover:text-blue-500">Invite</span>
          <span className="text-slate-300 group-hover:text-blue-400 transition-colors text-lg leading-none mr-4">
            +
          </span>
        </button>
      ) : (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full border border-dashed border-slate-300 text-xs text-slate-400 hover:border-blue-300 hover:text-blue-600 transition-colors"
        >
          + Invite
        </button>
      )}
      {open && (
        <div className={panelClass}>
          {available.map((d) => (
            <button
              key={d.deviceID}
              type="button"
              onClick={() => {
                onPick(d.deviceID);
                setOpen(false);
              }}
              className="w-full flex items-center gap-2.5 px-3 py-2 hover:bg-slate-50 text-left transition-colors"
            >
              <span
                className={`w-2 h-2 rounded-full flex-shrink-0 ${d.connected ? 'bg-emerald-400' : 'bg-slate-300'}`}
              />
              <span className="text-sm text-slate-700 truncate">{deviceLabel(d)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
