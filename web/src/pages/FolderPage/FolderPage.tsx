import { useEffect, useState } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import { InlineEdit } from '../../components/base/InlineEdit/InlineEdit';
import { api } from '../../api/client';
import type { ConflictFile } from '../../api/client';
import { useFolderSyncStatus } from '../../hooks/useFolderSyncStatus';
import { SyncProgress } from '../../components/base/SyncProgress/SyncProgress';
import { FolderSyncStatus } from '../../components/base/FolderSyncStatus/FolderSyncStatus';
import { useWS } from '../../api/websocket';
import { useToast } from '../../components/base/Toast/Toast';
import { useConfirm } from '../../components/base/ConfirmDialog/ConfirmDialog';
import { AsyncButton } from '../../components/base/Button/AsyncButton';
import { DirArrow, dirLabel } from '../../components/base/DirArrow/DirArrow';
import { FolderDirection } from '../../types/enums';
import { ListCard, ListRow, RowRemoveButton } from '../../components/base/ListRow/ListRow';
import { Card } from '../../components/base/Card/Card';
import { SectionHeading } from '../../components/base/SectionHeading/SectionHeading';
import { InvitePicker } from '../../components/base/InvitePicker/InvitePicker';
import { IgnorePatternsEditor } from '../../components/base/IgnorePatternsEditor/IgnorePatternsEditor';
import { useFolderDevices } from '../../hooks/useFolderDevices';
import { useApiToast } from '../../hooks/useApiToast';
import { folderDeviceDisplay, folderStateToRowStatus } from '../../state/folder-display';
import { deviceLabel } from '../../state/device-display';
import { formatBytes } from '../../state/format';

const ALL_DIRECTIONS: FolderDirection[] = [FolderDirection.SendReceive, FolderDirection.SendOnly, FolderDirection.ReceiveOnly];

export function FolderPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { devices, folders, visible } = useWS();
  const { addToast } = useToast();
  const run = useApiToast();
  const folder = folders.find((f) => f.id === id);
  const {
    status: syncStatus,
    refresh: refreshStatus,
    summary: syncSummary,
  } = useFolderSyncStatus(folder);
  const [pauseOverride, setPauseOverride] = useState<boolean | null>(null);
  const displayPaused = pauseOverride !== null ? pauseOverride : (syncStatus?.paused ?? false);
  const [togglingPause, setTogglingPause] = useState(false);
  const [ignorePatterns, setIgnorePatterns] = useState<string[]>([]);
  const [conflicts, setConflicts] = useState<ConflictFile[]>([]);

  const { members, others, available } = useFolderDevices(folder, devices);

  // Folder may have been removed (here or by a peer) while open, or the URL may
  // be stale. Once the first WS snapshot has arrived (visible !== null), a
  // missing folder means it's gone — send the user home with a heads-up.
  const wsReady = visible !== null;
  useEffect(() => {
    if (wsReady && !folder) {
      addToast('That folder is no longer available.', 'info');
      navigate('/', { replace: true });
    }
  }, [wsReady, folder, navigate, addToast]);

  // Load ignores + conflicts once when the folder ID is known
  useEffect(() => {
    if (!id) return;
    api
      .getFolderIgnores(id)
      .then((r) => setIgnorePatterns(r.patterns))
      .catch(() => {});
    api
      .getFolderConflicts(id)
      .then(setConflicts)
      .catch(() => {});
  }, [id]);

  const handleDeleteConflict = async (path: string) => {
    if (!id) return;
    await run(api.deleteFolderConflict(id, path), 'Could not delete conflict file');
    setConflicts((prev) => prev.filter((c) => c.path !== path));
  };

  const handleChangeDirection = async (direction: FolderDirection) => {
    if (!id) return;
    await run(api.updateFolderDirection(id, direction), 'Could not update direction');
  };

  const handleRemoveDevice = useConfirm(
    async (deviceID: string) => {
      if (!id) return;
      await run(api.removeFolderDevice(id, deviceID), 'Could not remove device');
    },
    {
      title: 'Remove device from folder?',
      description:
        'The device will no longer participate in this folder and will not receive file changes.',
      confirmLabel: 'Remove',
    },
  );

  const handleRemoveFolder = useConfirm(
    async () => {
      if (!id) return;
      await run(api.removeFolder(id), 'Could not remove folder');
      navigate('/folders');
    },
    {
      title: 'Stop syncing this folder?',
      description: 'Stops syncing this folder in WeSync. Your files are untouched.',
      confirmLabel: 'Stop syncing',
    },
  );

  const handleAddDevice = async (deviceID: string) => {
    if (!folder) return;
    await run(
      api.shareFolder(deviceID, folder.path, folder.label, folder.type),
      'Could not add device',
    );
  };

  const localChanges = syncStatus?.receiveOnlyTotalItems ?? 0;
  const handleRevert = useConfirm(
    async () => {
      if (!id) return;
      await run(api.revertFolder(id), 'Could not revert local changes');
      await refreshStatus();
    },
    {
      title: 'Revert local changes?',
      description: `This permanently deletes the ${localChanges} item${localChanges !== 1 ? 's' : ''} that exist only on this device and replaces any files you edited here with the version from your other devices. This can't be undone.`,
      confirmLabel: 'Revert & discard',
    },
  );

  const handleTogglePause = async () => {
    if (!id || !syncStatus || togglingPause) return;
    const next = !displayPaused;
    setPauseOverride(next); // optimistic update — UI responds immediately
    setTogglingPause(true);
    await api.setFolderPaused(id, next).catch((e: Error) => {
      setPauseOverride(null); // revert on failure
      addToast(e.message || 'Could not toggle sync', 'warning');
    });
    setTogglingPause(false);
    await refreshStatus(); // sync with real ST state
    setPauseOverride(null); // clear override, use real state
  };

  if (!folder) return null; // redirecting (or waiting for the first WS snapshot)

  return (
    <div className="max-w-2xl mx-auto w-full px-4 py-6 sm:px-6 sm:py-8 flex flex-col gap-6">
      {/* Header */}
      <div>
        <button
          type="button"
          onClick={() => navigate('/folders')}
          className="text-xs text-slate-400 hover:text-slate-600 transition-colors mb-3 flex items-center gap-1"
        >
          ← Synced folders
        </button>
        <div className="flex items-start justify-between gap-3">
          <div className="flex-1 min-w-0">
            <InlineEdit
              value={folder.label}
              onSave={async (label) => {
                if (!id) return;
                await run(api.updateFolderLabel(id, label), 'Could not rename');
              }}
              className="text-2xl font-bold text-slate-900"
              inputClassName="text-2xl font-bold text-slate-900 w-full"
            />
            <p className="text-xs font-mono text-slate-400 mt-1 break-all">{folder.path}</p>
          </div>
          <FolderSyncStatus summary={syncSummary} variant="badge" />
        </div>
      </div>

      {/* Conflicts */}
      {conflicts.length > 0 && (
        <Card tone="amber" className="px-5 py-4">
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-xs font-semibold text-amber-600 uppercase tracking-wide">
              {conflicts.length} conflict
              {conflicts.length > 1 ? 's' : ''}
            </h2>
            <AsyncButton
              unstyled
              onClick={() =>
                Promise.all(conflicts.map((c) => handleDeleteConflict(c.path))).then(() => {})
              }
              className="text-xs text-red-500 hover:text-red-700 font-medium"
            >
              Delete all
            </AsyncButton>
          </div>
          <div className="flex flex-col divide-y divide-slate-100">
            {conflicts.map((c) => (
              <div key={c.path} className="flex items-start gap-3 py-2.5">
                <div className="flex-1 min-w-0">
                  <p className="text-xs font-mono text-slate-700 truncate">{c.path}</p>
                  <p className="text-[10px] text-slate-400 truncate mt-0.5">
                    original:
                    {c.originalPath}
                  </p>
                </div>
                <RowRemoveButton
                  onClick={() => handleDeleteConflict(c.path)}
                  title="Delete conflict file"
                />
              </div>
            ))}
          </div>
        </Card>
      )}

      {/* Marker missing warning */}
      {syncStatus?.error?.includes('marker') && (
        <div className="bg-amber-50 border border-amber-200 rounded-2xl px-5 py-4 flex items-start gap-3">
          <span className="text-amber-500 text-lg flex-shrink-0">⚠</span>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-amber-800">Sync location needs to be set up</p>
            <p className="text-xs text-amber-700 mt-0.5">
              This folder is missing a required file. Click Fix to create it automatically.
            </p>
          </div>
          <AsyncButton
            size="sm"
            onClick={async () => {
              if (!id) return;
              await run(api.fixFolderMarker(id), 'Could not fix');
              await refreshStatus();
            }}
          >
            Fix
          </AsyncButton>
        </div>
      )}

      {/* Local changes on a receive-only folder — files this device has that the
          source doesn't. Awareness first; revert is the clearly-labelled,
          destructive escape hatch. */}
      {folder.type === FolderDirection.ReceiveOnly && syncStatus && localChanges > 0 && (
        <Card tone="amber" className="px-5 py-4 flex items-start gap-3">
          <AlertTriangle className="w-5 h-5 text-amber-500 flex-shrink-0 mt-0.5" />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-amber-800">Files only on this device</p>
            <p className="text-xs text-amber-700 mt-0.5">
              This folder is Receive only, so changes made here aren&apos;t sent to your other
              devices. WeSync found {localChanges} item{localChanges !== 1 ? 's' : ''}
              {syncStatus.receiveOnlyChangedBytes > 0
                ? ` (${formatBytes(syncStatus.receiveOnlyChangedBytes)})`
                : ''}{' '}
              that {localChanges !== 1 ? 'were' : 'was'} added or changed only here. They
              aren&apos;t backed up — if this device is lost, so are they.
            </p>
            <p className="text-xs text-amber-600 mt-1">
              To keep them, copy them elsewhere or switch this folder to Two-way before reverting.
            </p>
            <div className="mt-3">
              <AsyncButton variant="warning" size="sm" outlined onClick={handleRevert}>
                Revert local changes
              </AsyncButton>
            </div>
          </div>
        </Card>
      )}

      {/* Sync stats */}
      {syncStatus && (
        <Card className="px-5 py-4">
          <SectionHeading className="mb-3">Sync status</SectionHeading>
          <SyncProgress status={syncStatus} />

          <div className="flex items-center justify-between pt-3 mt-3 border-t border-slate-100">
            <div>
              <p className="text-sm font-medium text-slate-700">Sync enabled</p>
              <p className="text-xs text-slate-400 mt-0.5">
                Turn off to temporarily pause this folder
              </p>
            </div>
            <button
              type="button"
              onClick={handleTogglePause}
              disabled={togglingPause}
              className="relative inline-flex h-6 w-11 items-center rounded-full transition-colors disabled:opacity-50 disabled:cursor-wait focus:outline-none"
              style={{
                backgroundColor: togglingPause ? '#6b7280' : !displayPaused ? '#6366f1' : '#e2e8f0',
              }}
            >
              {togglingPause ? (
                <svg
                  className="absolute inset-0 m-auto w-3.5 h-3.5 animate-spin text-white"
                  viewBox="0 0 24 24"
                  fill="none"
                >
                  <circle
                    className="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    strokeWidth="4"
                  />
                  <path
                    className="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8v2l2-2-2-2v2a8 8 0 000 16v-2l-2 2 2 2v-2a8 8 0 01-8-8z"
                  />
                </svg>
              ) : (
                <span
                  className={`inline-block h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${!displayPaused ? 'translate-x-6' : 'translate-x-1'}`}
                />
              )}
            </button>
          </div>
        </Card>
      )}

      {/* Direction */}
      <Card className="px-5 py-4">
        <SectionHeading className="mb-3">My sync direction</SectionHeading>
        <div className="flex flex-col gap-2">
          {ALL_DIRECTIONS.map((d) => {
            const selected = folder.type === d;
            return (
              <button
                key={d}
                type="button"
                onClick={() => handleChangeDirection(d)}
                className={`flex items-center gap-3 px-4 py-3 rounded-xl border text-left transition-colors ${
                  selected
                    ? 'border-blue-400 bg-blue-50'
                    : 'border-slate-200 hover:border-slate-300'
                }`}
              >
                <DirArrow
                  type={d}
                  className={`text-xl font-bold ${selected ? 'text-blue-400' : 'text-slate-300'}`}
                />
                <span
                  className={`text-sm font-medium ${selected ? 'text-blue-700' : 'text-slate-700'}`}
                >
                  {dirLabel(d)}
                </span>
              </button>
            );
          })}
        </div>
      </Card>

      {/* Devices */}
      <ListCard
        title="Shared with"
        footer={
          available.length > 0 ? (
            <InvitePicker
              variant="block"
              className="px-4 py-3"
              available={available}
              onPick={handleAddDevice}
            />
          ) : undefined
        }
      >
        {members.length === 0 && others.length === 0 && (
          <div className="px-4 py-5 text-center">
            {available.length > 0 ? (
              <p className="text-sm text-slate-400">
                Not shared yet — use <span className="font-medium text-slate-500">Invite</span>{' '}
                below to start syncing
              </p>
            ) : (
              <p className="text-sm text-slate-400">
                No trusted devices.{' '}
                <Link to="/devices" className="text-blue-500 hover:text-blue-700">
                  Trust a device
                </Link>{' '}
                first, then add it here.
              </p>
            )}
          </div>
        )}
        {(() => {
          const canRemove = members.length + others.length === 1;
          return members.map((d) => {
            // All visual decisions for this row come from the central display
            // map keyed on FolderRelationState. See state/folder-display.ts.
            const display = folderDeviceDisplay(d.state);
            return (
              <ListRow
                key={d.deviceID}
                status={folderStateToRowStatus(d.state)}
                primary={deviceLabel(d)}
                trailing={
                  <div className="flex items-center gap-2">
                    {display.pending ? (
                      <span className="text-xs text-slate-400 italic">Invited</span>
                    ) : display.label ? (
                      <span className="text-xs text-slate-400 italic">{display.label}</span>
                    ) : (
                      d.peerType && (
                        <span className="flex items-center gap-1 text-xs text-slate-400">
                          <DirArrow type={d.peerType} className="text-xs" />
                          {dirLabel(d.peerType)}
                        </span>
                      )
                    )}
                    {canRemove && (
                      <RowRemoveButton
                        onClick={() => handleRemoveDevice(d.deviceID)}
                        title="Remove from folder"
                      />
                    )}
                  </div>
                }
              />
            );
          });
        })()}
        {others.length > 0 && (
          <>
            <div className="px-4 pt-3 pb-1">
              <p className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Others</p>
              <p className="text-xs text-slate-400 mt-0.5" />
            </div>
            {others.map((d) => (
              <ListRow
                key={d.deviceID}
                status={d.connected ? 'online' : 'offline'}
                primary={deviceLabel(d)}
              />
            ))}
          </>
        )}
      </ListCard>

      {/* Ignore patterns */}
      <Card className="px-5 py-4">
        <SectionHeading className="mb-3">Skip these files</SectionHeading>
        <p className="text-xs text-slate-400 mb-3">
          Files matching these patterns won't be synced. Uses glob syntax, e.g.
          <code className="font-mono bg-slate-100 px-1 rounded">*.tmp</code>
        </p>

        <IgnorePatternsEditor
          patterns={ignorePatterns}
          onChange={(next) => {
            if (!id) return;
            setIgnorePatterns(next);
            run(api.setFolderIgnores(id, next), 'Could not save ignore patterns');
          }}
        />
      </Card>

      {/* Danger zone */}
      <Card tone="red" className="px-5 py-4">
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm font-medium text-slate-700">Stop syncing</p>
            <p className="text-xs text-slate-400 mt-0.5">
              Stops syncing this folder in WeSync. Your files are untouched.
            </p>
          </div>
          <AsyncButton variant="danger" outlined onClick={handleRemoveFolder}>
            Stop syncing
          </AsyncButton>
        </div>
      </Card>
    </div>
  );
}
