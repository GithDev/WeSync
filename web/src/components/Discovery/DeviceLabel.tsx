import { api } from '../../api/client';
import { InlineEdit } from '../base/InlineEdit/InlineEdit';

interface Props {
  myID: string;
  myName: string;
  active: boolean;
}

export function DeviceLabel({ myID, myName, active }: Props) {
  return (
    <div className="absolute bottom-0 left-0 right-0 flex justify-center pb-5">
      <div className="flex flex-col items-center gap-1">
        <InlineEdit
          value={myName || myID.slice(0, 7)}
          onSave={(name) => api.setName(name).catch(() => {})}
          className="text-base font-bold text-slate-800"
          inputClassName="text-base font-bold text-slate-800 text-center w-40"
        />
        {myID && (
          <div className="flex items-center gap-1.5 text-xs text-slate-400">
            <span className="font-mono">{myID.slice(0, 7)}</span>
            <span>·</span>
            {active ? (
              <span
                className="flex items-center gap-1.5"
                title="Other WeSync devices nearby can find you. Turn off if you're on public WiFi."
              >
                <span className="w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse flex-shrink-0" />
                <span>Discoverable</span>
              </span>
            ) : (
              <span
                className="flex items-center gap-1.5"
                title="New devices can't find you. People you already sync with can still reach you."
              >
                <span className="w-1.5 h-1.5 rounded-full bg-amber-400 flex-shrink-0" />
                <span>Not discoverable</span>
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
