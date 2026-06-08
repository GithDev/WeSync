import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../../api/client';
import type { PendingFolder } from '../../api/client';
import { useWS } from '../../api/websocket';
import { useConfirm } from '../../components/base/ConfirmDialog/ConfirmDialog';
import { AsyncButton } from '../../components/base/Button/AsyncButton';
import { SectionHeading } from '../../components/base/SectionHeading/SectionHeading';
import { useApiToast } from '../../hooks/useApiToast';
import { deviceMaps } from '../../state/device-display';
import { FolderDirection } from '../../types/enums';
import { FolderGroup } from './FolderGroup';
import { IncomingFolder } from './IncomingFolder';
import { ShareFolderModal } from './ShareFolderModal';
import { usePickFolder } from '../../hooks/usePickFolder';

interface PendingShare {
  path: string;
  label: string;
  direction: FolderDirection;
  deviceID?: string;
}

export function FoldersPage() {
  const { devices, folders, pendingFolders } = useWS();
  const run = useApiToast();
  const [conflictCounts, setConflictCounts] = useState<Record<string, number>>({});
  const [pendingShare, setPendingShare] = useState<PendingShare | null>(null);

  const folderIDs = folders.map((f) => f.id).join(',');
  useEffect(() => {
    if (!folderIDs) return;
    let cancelled = false;
    Promise.all(
      folders.map((f) =>
        api.getFolderConflicts(f.id)
          .then((cs) => [f.id, cs.length] as const)
          .catch(() => [f.id, 0] as const),
      ),
    ).then((entries) => {
      if (!cancelled) setConflictCounts(Object.fromEntries(entries));
    });
    return () => { cancelled = true; };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [folderIDs]);
  const [pendingAccept, setPendingAccept] = useState<PendingFolder | null>(null);
  const [acceptLabel, setAcceptLabel] = useState('');
  const [acceptFolderDirection, setAcceptFolderDirection] = useState<FolderDirection>(FolderDirection.SendReceive);
  const { pick: nativePick, modal: pickerModal } = usePickFolder();

  const { nameMap, hostnameMap, connectedMap } = deviceMaps(devices);

  // ── Share flow ────────────────────────────────────────────────────────────

  const startShare = async (deviceID?: string) => {
    const path = await nativePick();
    if (!path) return;
    setPendingShare({
      path,
      label: path.split(/[/\\]/).filter(Boolean).pop() ?? path,
      direction: FolderDirection.SendReceive,
      deviceID,
    });
  };

  const confirmShare = async (selectedDeviceIDs: string[], ignorePatterns: string[]) => {
    if (!pendingShare) return;
    // Create folder first (no device or first device)
    const [first, ...rest] = selectedDeviceIDs;
    const result = await run(
      api.shareFolder(first ?? '', pendingShare.path, pendingShare.label, pendingShare.direction),
      'Could not share folder',
    );
    // Apply ignore patterns immediately after folder creation.
    if (result?.folderID && ignorePatterns.length > 0) {
      await run(
        api.setFolderIgnores(result.folderID, ignorePatterns),
        'Could not set ignore patterns',
      );
    }
    // Add remaining devices (shareFolder with same path reuses the folder)
    for (const deviceID of rest) {
      await run(
        api.shareFolder(deviceID, pendingShare.path, pendingShare.label, pendingShare.direction),
        `Could not add device ${deviceID.slice(0, 7)}`,
      );
    }
    setPendingShare(null);
  };

  // ── Folder device management ─────────────────────────────────────────────

  const handleRemoveDevice = useConfirm(
    async (folderID: string, deviceID: string) => {
      await run(api.removeFolderDevice(folderID, deviceID), 'Could not remove device');
    },
    {
      title: 'Remove device from folder?',
      description:
        'The device will no longer participate in this folder and will not receive file changes.',
      confirmLabel: 'Remove',
    },
  );

  const handleAddDevice = async (folderID: string, deviceID: string) => {
    const folder = folders.find((f) => f.id === folderID);
    if (!folder) return;
    await run(
      api.shareFolder(deviceID, folder.path, folder.label, folder.type),
      'Could not add device',
    );
  };

  // ── Accept flow ───────────────────────────────────────────────────────────

  const handleAllow = async (pf: PendingFolder) => {
    await run(api.acceptFolder(pf.folderID, pf.deviceID, ''), 'Could not allow sync');
  };

  // Opens the accept modal for new folders (path + ignores)
  const handleOpenAcceptModal = (pf: PendingFolder) => {
    setAcceptLabel(pf.label);
    setAcceptFolderDirection(FolderDirection.SendReceive);
    setPendingAccept(pf);
  };

  const handleConfirmAccept = async (
    _: string[],
    ignorePatterns: string[],
    acceptPath?: string,
  ) => {
    if (!pendingAccept || !acceptPath) return;
    await run(
      api.acceptFolder(pendingAccept.folderID, pendingAccept.deviceID, acceptPath, acceptFolderDirection),
      'Could not accept folder',
    );
    // Rename if user changed the label
    if (acceptLabel && acceptLabel !== pendingAccept.label) {
      await api.updateFolderLabel(pendingAccept.folderID, acceptLabel).catch(() => {});
    }
    if (ignorePatterns.length > 0) {
      await run(
        api.setFolderIgnores(pendingAccept.folderID, ignorePatterns),
        'Could not set ignore patterns',
      );
    }
    setPendingAccept(null);
  };

  const handleDecline = (pf: PendingFolder) => api.declineFolder(pf.folderID, pf.deviceID);

  return (
    <div className="max-w-2xl mx-auto w-full px-4 py-6 sm:px-6 sm:py-8 flex flex-col gap-6">
      {pickerModal}

      {/* Incoming — one card per folderID, all offering devices shown */}
      {pendingFolders.length > 0 &&
        (() => {
          const byFolder = new Map<string, typeof pendingFolders>();
          for (const pf of pendingFolders) {
            if (!byFolder.has(pf.folderID)) byFolder.set(pf.folderID, []);
            byFolder.get(pf.folderID)!.push(pf);
          }
          return (
            <div className="flex flex-col gap-2">
              <SectionHeading>Folder invites</SectionHeading>
              {Array.from(byFolder.values()).map((group) => {
                const pf = group[0];
                const existingFolder = folders.find((f) => f.id === pf.folderID) ?? null;
                const offerers = group.map((p) => ({
                  name: nameMap[p.deviceID] ?? p.deviceID.slice(0, 7),
                  hostname: hostnameMap[p.deviceID],
                  connected: connectedMap[p.deviceID] ?? false,
                }));
                return (
                  <IncomingFolder
                    key={pf.folderID}
                    pending={pf}
                    offerers={offerers}
                    existingFolder={existingFolder}
                    onAllow={handleAllow}
                    onAcceptWithPath={handleOpenAcceptModal}
                    onDecline={handleDecline}
                    onPickFolder={nativePick}
                  />
                );
              })}
            </div>
          );
        })()}

      {/* Folders */}
      <div className="flex flex-col gap-3">
        <div className="flex items-center justify-between">
          <SectionHeading>Synced folders</SectionHeading>
          <div className="flex gap-2">
            {!devices.some((d) => d.stPaired) && (
              <Link
                to="/devices"
                className="text-xs text-slate-400 hover:text-slate-600 transition-colors self-center"
              >
                Trust a device first →
              </Link>
            )}
            <AsyncButton size="sm" outlined onClick={() => startShare()}>
              + Add folder
            </AsyncButton>
          </div>
        </div>

        {pendingAccept && (
          <ShareFolderModal
            acceptMode
            label={acceptLabel}
            path=""
            direction={acceptFolderDirection}
            pairedDevices={[]}
            onChangeLabel={setAcceptLabel}
            onChangeDirection={setAcceptFolderDirection}
            onConfirm={handleConfirmAccept}
            onCancel={() => setPendingAccept(null)}
            onPickPath={nativePick}
          />
        )}

        {pendingShare && (
          <ShareFolderModal
            label={pendingShare.label}
            path={pendingShare.path}
            direction={pendingShare.direction}
            pairedDevices={devices
              .filter((d) => d.stPaired)
              .map((d) => ({
                deviceID: d.deviceID,
                name: d.name,
                hostname: d.info?.hostname,
                connected: d.connected,
              }))}
            onChangeLabel={(label) => setPendingShare((p) => p && { ...p, label })}
            onChangeDirection={(d) => setPendingShare((p) => p && { ...p, direction: d })}
            onConfirm={confirmShare}
            onCancel={() => setPendingShare(null)}
          />
        )}

        {folders.length === 0 &&
          !pendingShare &&
          pendingFolders.length === 0 &&
          (devices.some((d) => d.stPaired) ? (
            <div className="flex flex-col items-center gap-4 py-10 text-center">
              <p className="text-sm text-slate-400">No synced folders yet.</p>
              <AsyncButton onClick={() => startShare()}>+ Add folder</AsyncButton>
            </div>
          ) : (
            <div className="flex flex-col items-center gap-3 py-10 text-center">
              <p className="text-sm text-slate-400">No synced folders yet.</p>
              <Link
                to="/devices"
                className="text-xs text-blue-500 hover:text-blue-700 transition-colors"
              >
                Trust a device first →
              </Link>
            </div>
          ))}

        {folders.map((folder) => (
          <FolderGroup
            key={folder.id}
            folder={folder}
            conflictCount={conflictCounts[folder.id] ?? 0}
            onRemoveDevice={handleRemoveDevice}
            onAddDevice={handleAddDevice}
          />
        ))}
      </div>
    </div>
  );
}
