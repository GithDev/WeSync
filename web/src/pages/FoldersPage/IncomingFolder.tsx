import { AsyncButton } from '../../components/base/Button/AsyncButton';
import { FolderIcon } from '../../components/base/FolderIcon/FolderIcon';
import { DevicePill } from '../../components/base/DevicePill/DevicePill';
import type { PendingFolder, WeSyncFolder } from '../../api/client';

interface Offerer {
  name: string;
  hostname?: string;
  connected: boolean;
}

interface Props {
  pending: PendingFolder;
  offerers: Offerer[];
  existingFolder: WeSyncFolder | null;
  onAllow: (pf: PendingFolder) => void;
  onAcceptWithPath: (pf: PendingFolder) => void;
  onDecline: (pf: PendingFolder) => void;
  onPickFolder: () => Promise<string | null>;
}

function OffererPills({ offerers }: { offerers: Offerer[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {offerers.map((o) => (
        <DevicePill
          key={o.name}
          mode="display"
          name={o.name}
          hostname={o.hostname}
          connected={o.connected}
        />
      ))}
    </div>
  );
}

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="bg-white rounded-2xl border border-slate-200 px-5 py-4 flex flex-col gap-3">
      {children}
    </div>
  );
}

function FolderHeader({ label, sub }: { label: string; sub: string }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="min-w-0">
        <div className="flex items-center gap-1.5 mb-0.5">
          <FolderIcon className="w-3.5 h-3.5 text-amber-400 flex-shrink-0" />
          <span className="text-sm font-semibold text-slate-800 truncate">{label}</span>
        </div>
        <p className="text-xs text-slate-400">{sub}</p>
      </div>
    </div>
  );
}

export function IncomingFolder({
  pending,
  offerers,
  existingFolder,
  onAllow,
  onAcceptWithPath,
  onDecline,
}: Props) {
  const verb = offerers.length === 1 ? 'wants' : 'want';
  const sub = `${verb} to sync this folder with you`;

  // Case 1: we already have this folder — just allow access
  if (existingFolder) {
    return (
      <Card>
        <FolderHeader label={pending.label} sub="wants to sync with your existing folder" />
        <OffererPills offerers={offerers} />
        <div className="bg-slate-50 rounded-xl px-3 py-2">
          <span className="text-xs font-mono text-slate-500 truncate">{existingFolder.path}</span>
        </div>
        <div className="flex gap-2">
          <AsyncButton variant="secondary" outlined onClick={() => onDecline(pending)}>
            Decline
          </AsyncButton>
          <AsyncButton className="flex-1" onClick={() => onAllow(pending)}>
            Allow
          </AsyncButton>
        </div>
      </Card>
    );
  }

  // Case 2: new folder — open modal to pick path + configure
  return (
    <Card>
      <FolderHeader label={pending.label} sub={sub} />
      <OffererPills offerers={offerers} />
      <div className="flex gap-2">
        <AsyncButton variant="secondary" outlined onClick={() => onDecline(pending)}>
          Decline
        </AsyncButton>
        <AsyncButton className="flex-1" onClick={() => onAcceptWithPath(pending)}>
          Choose where to save
        </AsyncButton>
      </div>
    </Card>
  );
}
