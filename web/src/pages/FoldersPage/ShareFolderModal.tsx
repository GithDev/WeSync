import { useState } from 'react';
import { BaseModal, ModalFooter } from '../../components/base/Modal/Modal';
import { InlineEdit } from '../../components/base/InlineEdit/InlineEdit';
import { DevicePill } from '../../components/base/DevicePill/DevicePill';
import { DirectionPicker } from '../DevicePage/DirectionPicker';
import { FolderDirection } from '../../types/enums';
import { IgnorePatternsEditor } from '../../components/base/IgnorePatternsEditor/IgnorePatternsEditor';
import { api } from '../../api/client';

interface Device {
  deviceID: string;
  name: string;
  hostname?: string;
  connected: boolean;
}

interface Props {
  label: string;
  path: string;
  direction: FolderDirection;
  pairedDevices: Device[];
  onChangeLabel: (label: string) => void;
  onChangeDirection: (direction: FolderDirection) => void;
  /** Share mode: (deviceIDs, patterns). Accept mode: also passes acceptPath. */
  onConfirm: (selectedDeviceIDs: string[], ignorePatterns: string[], acceptPath?: string) => void;
  onCancel: () => void;
  /** Accept mode — hides devices, shows path picker instead */
  acceptMode?: boolean;
  onPickPath?: () => Promise<string | null>;
}

export function ShareFolderModal({
  label,
  path,
  direction,
  pairedDevices,
  onChangeLabel,
  onChangeDirection,
  onConfirm,
  onCancel,
  acceptMode = false,
  onPickPath,
}: Props) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [patterns, setPatterns] = useState<string[]>([]);
  const [acceptPath, setAcceptPath] = useState<string | null>(null);
  const [pathWarning, setPathWarning] = useState<string | null>(null);

  const toggle = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const pickPath = async () => {
    const picked = await onPickPath?.();
    if (!picked) return;
    setAcceptPath(picked);
    setPathWarning(null);
    const check = await api.checkFolderPath(picked).catch(() => null);
    if (check && !check.empty) {
      setPathWarning(
        `This folder already contains ${check.fileCount} item${check.fileCount !== 1 ? 's' : ''}. Merge them with the synced files.`,
      );
    }
  };

  const canConfirm = acceptMode ? !!acceptPath : true;
  const confirmLabel = acceptMode
    ? acceptPath
      ? 'Save'
      : 'Choose a location first'
    : selected.size > 0
      ? `Add folder & sync with ${selected.size}`
      : 'Add folder';

  return (
    <BaseModal open title={acceptMode ? 'Accept folder' : 'Add folder'} maxWidth="max-w-md">
      {/* Folder info */}
      <div className="bg-slate-50 rounded-xl px-3 py-2.5">
        <p className="text-xs text-slate-400 mb-1">Folder name</p>
        <InlineEdit
          value={label}
          onSave={onChangeLabel}
          className="text-sm font-semibold text-slate-800"
          inputClassName="text-sm font-semibold text-slate-800 w-full"
          showPencil
        />
        {!acceptMode && <p className="text-xs font-mono text-slate-400 mt-1 break-all">{path}</p>}
      </div>

      {/* Accept mode: path picker */}
      {acceptMode && (
        <div>
          <p className="text-xs font-semibold text-slate-500 uppercase tracking-wide mb-2">
            Save location
          </p>
          {acceptPath ? (
            <div className="flex items-center gap-2 bg-slate-50 rounded-xl px-3 py-2">
              <span className="text-xs font-mono text-slate-600 flex-1 truncate">{acceptPath}</span>
              <button
                type="button"
                onClick={pickPath}
                className="text-xs text-slate-400 hover:text-slate-600 flex-shrink-0"
              >
                Change
              </button>
            </div>
          ) : (
            <button
              type="button"
              onClick={pickPath}
              className="w-full text-sm text-slate-500 border border-dashed border-slate-300 rounded-xl py-3 hover:border-blue-300 hover:text-blue-600 transition-colors"
            >
              Choose where to save…
            </button>
          )}
        </div>
      )}

      {acceptMode && pathWarning && (
        <div className="flex items-start gap-2 bg-amber-50 border border-amber-200 rounded-xl px-3 py-2.5 text-xs text-amber-700">
          <span className="flex-shrink-0 mt-0.5">⚠</span>
          <span>{pathWarning}</span>
        </div>
      )}

      {/* Devices — only in share mode */}
      {!acceptMode && pairedDevices.length > 0 && (
        <div>
          <p className="text-xs font-semibold text-slate-500 uppercase tracking-wide mb-2">
            Share with
          </p>
          <div className="flex flex-col gap-1.5">
            {pairedDevices.map((d) => (
              <DevicePill
                key={d.deviceID}
                mode="selectable"
                name={d.name}
                hostname={d.hostname}
                connected={d.connected}
                selected={selected.has(d.deviceID)}
                onToggle={() => toggle(d.deviceID)}
                fullWidth
              />
            ))}
          </div>
          <p className="text-xs text-slate-400 mt-2 min-h-[1rem]">
            {selected.size === 0
              ? 'No devices selected — you can add them later.'
              : `Will sync with ${selected.size} device${selected.size > 1 ? 's' : ''}.`}
          </p>
        </div>
      )}

      <DirectionPicker value={direction} onChange={onChangeDirection} compact />

      {/* Ignore patterns */}
      <div>
        <p className="text-xs font-semibold text-slate-500 uppercase tracking-wide mb-2">
          Skip these files
        </p>
        <IgnorePatternsEditor patterns={patterns} onChange={setPatterns} />
        <p className="text-xs text-slate-400 mt-1.5">
          Files matching these patterns won't be synced.
        </p>
      </div>

      <ModalFooter
        confirmLabel={confirmLabel}
        confirmVariant="primary"
        confirmDisabled={!canConfirm}
        onConfirm={() => onConfirm(Array.from(selected), patterns, acceptPath ?? undefined)}
        onCancel={onCancel}
      />
    </BaseModal>
  );
}
