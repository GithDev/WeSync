import { Link } from 'react-router-dom';
import { DirArrow, dirLabel } from '../../components/base/DirArrow/DirArrow';
import { DevicePill } from '../../components/base/DevicePill/DevicePill';
import { FolderIcon } from '../../components/base/FolderIcon/FolderIcon';
import { FolderSyncStatus } from '../../components/base/FolderSyncStatus/FolderSyncStatus';
import { InvitePicker } from '../../components/base/InvitePicker/InvitePicker';
import { Card } from '../../components/base/Card/Card';
import { useWS } from '../../api/websocket';
import { useFolderDevices } from '../../hooks/useFolderDevices';
import { useFolderSyncStatus } from '../../hooks/useFolderSyncStatus';
import type { WeSyncFolder } from '../../api/client';

interface Props {
  folder: WeSyncFolder;
  conflictCount: number;
  onAddDevice: (folderID: string, deviceID: string) => void;
  onRemoveDevice: (folderID: string, deviceID: string) => void;
}

export function FolderGroup({ folder, conflictCount, onAddDevice, onRemoveDevice }: Props) {
  const { devices } = useWS();
  const { members, others, available } = useFolderDevices(folder, devices);
  // Remove is only safe with exactly one other participant — mesh (2+) uses Introducer
  // which would re-add the removed device immediately.
  const canRemove = members.length + others.length === 1;
  const { summary } = useFolderSyncStatus(folder);

  return (
    <Card className="px-5 py-4 flex flex-col gap-3">
      {/* Header */}
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 mb-0.5">
            <DirArrow type={folder.type} className="text-slate-400 text-sm" />
            <FolderIcon className="w-3.5 h-3.5 text-amber-400 flex-shrink-0" />
            <Link
              to={`/folder/${folder.id}`}
              className="text-sm font-semibold text-slate-800 truncate hover:text-blue-600 transition-colors"
              onClick={(e) => e.stopPropagation()}
            >
              {folder.label}
            </Link>
            <span className="text-xs text-slate-400 flex-shrink-0">{dirLabel(folder.type)}</span>
            {conflictCount > 0 && (
              <span className="inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-semibold bg-amber-100 text-amber-700 flex-shrink-0">
                {conflictCount} conflict{conflictCount > 1 ? 's' : ''}
              </span>
            )}
          </div>
          <p className="text-xs font-mono text-slate-400 truncate">{folder.path}</p>
        </div>
        <Link
          to={`/folder/${folder.id}`}
          className="p-2 -mr-2 text-slate-300 hover:text-slate-600 hover:bg-slate-100 rounded-xl transition-colors flex-shrink-0"
          title="Folder settings"
        >
          <svg
            className="w-4 h-4"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
          >
            <path d="M9 18l6-6-6-6" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </Link>
      </div>

      {/* Shared with — always shown so users see what to do next.
          Three states: no trusted devices / trusted exist but none here / has members. */}
      <div className="flex flex-col gap-1.5">
        <p className="text-[10px] font-semibold text-slate-400 uppercase tracking-wide">
          Shared with
        </p>

        {members.length === 0 && available.length === 0 ? (
          // State 1: no trusted devices at all → point at /devices.
          <Link
            to="/devices"
            className="inline-flex items-center gap-1.5 text-xs text-slate-400 hover:text-blue-600 transition-colors w-fit"
          >
            <span>No trusted devices yet.</span>
            <span className="text-blue-500 font-medium">Trust a device →</span>
          </Link>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            {/* State 2: trusted exist but none on this folder — prefix the invite button. */}
            {members.length === 0 && (
              <span className="text-xs text-slate-400">Not shared yet —</span>
            )}
            {members.map((d) =>
              canRemove ? (
                <DevicePill
                  key={d.deviceID}
                  mode="removable"
                  name={d.name}
                  hostname={d.hostname}
                  connected={d.connected}
                  state={d.state}
                  directionType={d.peerType}
                  onRemove={() => onRemoveDevice(folder.id, d.deviceID)}
                  title={`Remove ${d.name}`}
                />
              ) : (
                <DevicePill
                  key={d.deviceID}
                  mode="display"
                  name={d.name}
                  hostname={d.hostname}
                  connected={d.connected}
                  state={d.state}
                  directionType={d.peerType}
                />
              ),
            )}
            <InvitePicker
              variant="pill"
              available={available}
              onPick={(deviceID) => onAddDevice(folder.id, deviceID)}
            />
          </div>
        )}
      </div>

      {/* Other participants — separate section */}
      {others.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <p className="text-[10px] font-semibold text-slate-400 uppercase tracking-wide">Others</p>
          <div className="flex flex-wrap gap-2">
            {others.map((d) => (
              <span
                key={d.deviceID}
                className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border border-dashed border-slate-300 text-xs text-slate-400"
                title="Added by another"
              >
                <span className="w-1.5 h-1.5 rounded-full bg-slate-300 flex-shrink-0" />
                {d.name}
                {d.hostname && d.hostname !== d.name && (
                  <span className="opacity-70">{d.hostname}</span>
                )}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Sync status */}
      <FolderSyncStatus summary={summary} variant="compact" />
    </Card>
  );
}
