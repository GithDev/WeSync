import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion } from 'framer-motion';
import type { Peer, Device, PendingFolder } from '../../api/client';
import { AsyncButton } from '../base/Button/AsyncButton';
import { deviceColors } from './types';
import type { NetworkEntry } from './types';

const STATUS_LABEL: Record<NetworkEntry['kind'], string> = {
  incoming: 'Trust request',
  waiting: 'Waiting…',
  connected: 'Connected',
  offline: 'Offline', // trusted, was connected before, now offline
  discoverable: 'On this network',
};

const OS_LABEL: Record<string, string> = {
  windows: 'Windows',
  linux: 'Linux',
  darwin: 'macOS',
};

interface Props {
  entry: NetworkEntry;
  pendingFolders: PendingFolder[];
  onAccept: (peer: Peer) => void;
  onCancel: (peer: Peer) => void;
  onPair: (peer: Peer) => void;
}

export function DeviceCard({ entry, pendingFolders, onAccept, onCancel, onPair }: Props) {
  const navigate = useNavigate();
  const [expanded, setExpanded] = useState(false);

  const colors = deviceColors(entry.id);
  const peer = 'peer' in entry ? (entry as { peer: Peer }).peer : null;
  const device = 'device' in entry ? (entry as { device: Device }).device : null;
  const pulse = entry.kind === 'incoming' || entry.kind === 'waiting';
  const isPaired = entry.kind === 'connected' || entry.kind === 'offline';
  const isDiscoverable = entry.kind === 'discoverable' || entry.kind === 'incoming';
  const pendingInvites = pendingFolders.filter((f) => f.deviceID === entry.id).length;

  const info = device?.info ?? peer?.info;
  const primaryName = entry.name || info?.hostname || entry.id.slice(0, 7);
  const hostname = info?.hostname && info.hostname !== primaryName ? info.hostname : null;
  const primaryIP = info?.ifaces?.flatMap((i) => i.ips).find((ip) => !ip.includes(':'));
  const hasDetails = isDiscoverable && !!(info?.hostname || info?.os || primaryIP);

  const handleClick = () => {
    if (isPaired) navigate(`/device/${entry.id}`);
    else if (hasDetails) setExpanded((v) => !v);
  };

  const isClickable = isPaired || hasDetails;

  const card = (
    // layoutId is the hinge for the radar→trusted slide animation in
    // DevicesPage. When the user trusts a device, the radar card unmounts
    // and a matching TrustedRow mounts with the same layoutId; framer
    // interpolates between their bounding boxes.
    <motion.div
      layoutId={`device-${entry.id}`}
      className={`bg-white rounded-2xl shadow-sm px-4 py-3 flex flex-col gap-2 w-full min-w-[260px] max-w-sm ${isClickable ? 'cursor-pointer' : ''}`}
      style={{ border: `1.5px solid ${colors.border}` }}
      onClick={isClickable ? handleClick : undefined}
      role={isClickable ? 'button' : undefined}
      tabIndex={isClickable ? 0 : undefined}
      onKeyDown={
        isClickable
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') handleClick();
            }
          : undefined
      }
    >
      {/* Main row — same structure for all kinds */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <span
            className={`w-2.5 h-2.5 rounded-full flex-shrink-0 ${pulse ? 'animate-pulse' : ''}`}
            style={{ backgroundColor: colors.border }}
          />
          <div className="min-w-0">
            <p className="font-semibold text-slate-900 truncate">{primaryName}</p>
            {hostname && <p className="text-xs text-slate-400 truncate">{hostname}</p>}
            <p className="text-xs font-mono text-slate-400">{entry.id.slice(0, 7)}</p>
            <p
              className={`text-xs mt-0.5 ${entry.kind === 'offline' ? 'text-red-400' : ''}`}
              style={entry.kind !== 'offline' ? { color: colors.border } : undefined}
            >
              {STATUS_LABEL[entry.kind]}
            </p>
          </div>
        </div>

        {/* Stop propagation only when there are action buttons to protect */}
        <div
          className="flex items-center gap-2 flex-shrink-0"
          onClick={isPaired ? undefined : (e) => e.stopPropagation()}
          onKeyDown={isPaired ? undefined : (e) => e.stopPropagation()}
        >
          {pendingInvites > 0 && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                navigate('/folders');
              }}
              className="text-xs bg-blue-500 text-white rounded-full px-2 py-0.5 font-medium hover:bg-blue-600 transition-colors"
            >
              {pendingInvites} folder
              {pendingInvites > 1 ? 's' : ''}
            </button>
          )}
          {entry.kind === 'incoming' && peer && (
            <AsyncButton size="sm" onClick={() => onAccept(peer)}>
              Accept
            </AsyncButton>
          )}
          {entry.kind === 'waiting' && peer && (
            <AsyncButton variant="secondary" size="sm" onClick={() => onCancel(peer)}>
              Cancel
            </AsyncButton>
          )}
          {entry.kind === 'discoverable' && peer && (
            <AsyncButton size="sm" onClick={() => onPair(peer)}>
              Trust
            </AsyncButton>
          )}
          {isPaired && (
            <svg
              className="w-4 h-4 text-slate-300"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <path d="M9 18l6-6-6-6" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          )}
        </div>
      </div>

      {/* Expandable identity details — discoverable/incoming only */}
      {expanded && hasDetails && (
        <div className="ml-5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs border-t border-slate-100 pt-2">
          {info?.hostname && info.hostname !== primaryName && (
            <>
              <span className="text-slate-400">Host</span>
              <span className="font-mono text-slate-600">{info.hostname}</span>
            </>
          )}
          {info?.os && (
            <>
              <span className="text-slate-400">OS</span>
              <span className="text-slate-600">
                {OS_LABEL[info.os] ?? info.os}
                {info.osVer && <span className="ml-1 text-slate-400">{info.osVer}</span>}
              </span>
            </>
          )}
          {primaryIP && (
            <>
              <span className="text-slate-400">IP</span>
              <span className="font-mono text-slate-600">{primaryIP}</span>
            </>
          )}
          <span className="text-slate-400">ID</span>
          <span className="font-mono text-slate-400">{entry.id.slice(0, 21)}…</span>
        </div>
      )}
    </motion.div>
  );

  return card;
}
