import { DirArrow } from '../../components/base/DirArrow/DirArrow';
import type { Direction } from '../../components/base/DirArrow/DirArrow';

export type { Direction } from '../../components/base/DirArrow/DirArrow';

// All three directions — same for share and accept.
// Both is first so it's the natural default.
const OPTIONS: { value: Direction; label: string; desc: string }[] = [
  { value: 'sendreceive', label: 'Two-way', desc: 'Files stay in sync on both sides.' },
  { value: 'sendonly', label: 'Send only', desc: 'You send — nothing comes back.' },
  { value: 'receiveonly', label: 'Receive only', desc: 'You receive — nothing is sent back.' },
];

interface Props {
  mode?: 'share' | 'accept'; // kept for future specialisation, unused now
  value: Direction;
  onChange: (v: Direction) => void;
  /** compact = small pill row (for modals). Default = full cards (for settings). */
  compact?: boolean;
}

export function DirectionPicker({ value, onChange, compact = false }: Props) {
  if (compact) {
    return (
      <div>
        <p className="text-[11px] text-slate-400 mb-1.5 font-medium uppercase tracking-wide">
          Sync direction
        </p>
        <div className="flex flex-wrap gap-1.5">
          {OPTIONS.map((o) => {
            const selected = value === o.value;
            return (
              <button
                key={o.value}
                type="button"
                onClick={() => onChange(o.value)}
                title={o.desc}
                className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium border transition-colors ${
                  selected
                    ? 'border-blue-400 bg-blue-50 text-blue-700'
                    : 'border-slate-200 text-slate-500 hover:border-slate-300 hover:text-slate-700'
                }`}
              >
                <DirArrow
                  type={o.value}
                  className={`text-xs leading-none ${selected ? 'text-blue-400' : 'text-slate-300'}`}
                />
                {o.label}
              </button>
            );
          })}
        </div>
      </div>
    );
  }

  // Full card layout — used in folder settings page
  return (
    <div className="flex flex-col gap-2">
      {OPTIONS.map((o) => {
        const selected = value === o.value;
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(o.value)}
            className={`flex items-center gap-3 px-4 py-3 rounded-xl border text-left transition-colors ${
              selected ? 'border-blue-400 bg-blue-50' : 'border-slate-200 hover:border-slate-300'
            }`}
          >
            <DirArrow
              type={o.value}
              className={`text-2xl font-bold leading-none ${selected ? 'text-blue-400' : 'text-slate-200'}`}
            />
            <div className="flex-1 min-w-0">
              <p
                className={`text-sm font-semibold ${selected ? 'text-blue-700' : 'text-slate-700'}`}
              >
                {o.label}
              </p>
              <p className="text-xs text-slate-400 mt-0.5">{o.desc}</p>
            </div>
          </button>
        );
      })}
    </div>
  );
}

const SENDER_DISPLAY: Record<string, { arrow: string; title: string; desc: string }> = {
  sendonly: {
    arrow: '→',
    title: '→ Receive files',
    desc: 'Files sync into your folder. Nothing is sent back.',
  },
  sendreceive: { arrow: '↔', title: '↔ Two-way', desc: 'Files stay in sync on both sides.' },
};

export function SenderDirectionDisplay({ senderType }: { senderType: string }) {
  const info = SENDER_DISPLAY[senderType] ?? SENDER_DISPLAY.sendonly;
  return (
    <div className="flex items-start gap-3 px-4 py-3 rounded-xl border border-blue-200 bg-blue-50">
      <span className="text-2xl font-bold text-blue-400 flex-shrink-0 leading-tight">
        {info.arrow}
      </span>
      <div>
        <p className="text-sm font-semibold text-blue-700">{info.title}</p>
        <p className="text-xs text-slate-500 mt-0.5">{info.desc}</p>
      </div>
    </div>
  );
}
