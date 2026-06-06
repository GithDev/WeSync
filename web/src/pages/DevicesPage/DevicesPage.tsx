import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion, LayoutGroup } from 'framer-motion';
import { HelpCircle } from 'lucide-react';
import { api } from '../../api/client';
import type { Peer } from '../../api/client';
import { useWS } from '../../api/websocket';
import { useToast } from '../../components/base/Toast/Toast';
import { DeviceCard } from '../../components/Discovery/DeviceCard';
import { RadarBackground } from '../../components/Discovery/RadarBackground';
import { DeviceLabel } from '../../components/Discovery/DeviceLabel';
import { deriveNetwork } from '../../components/Discovery/Discovery.logic';
import type { NetworkEntry } from '../../components/Discovery/types';
import { ListCard, ListRow, RowChevron } from '../../components/base/ListRow/ListRow';
import { FolderIcon } from '../../components/base/FolderIcon/FolderIcon';
import { GettingStarted } from '../../components/GettingStarted/GettingStarted';

function entryStatus(entry: NetworkEntry) {
  if (entry.kind === 'connected') return 'online' as const;
  if (entry.kind === 'waiting') return 'waiting' as const;
  return 'offline' as const;
}

function entryLabel(entry: NetworkEntry) {
  if (entry.kind === 'connected') return 'Connected';
  if (entry.kind === 'waiting') return 'Waiting…';
  return 'Offline';
}

function TrustedRow({ entry }: { entry: NetworkEntry }) {
  const device =
    'device' in entry ? (entry as { device: import('../../api/client').Device }).device : null;
  const name = entry.name || device?.info?.hostname || entry.id.slice(0, 7);
  const hostname =
    device?.info?.hostname && device.info.hostname !== name ? device.info.hostname : null;

  // Matching layoutId with the DeviceCard on the radar — when state flips
  // from discoverable to connected, framer-motion slides the card down
  // into this row's position instead of letting it pop out of existence.
  return (
    <motion.div layoutId={`device-${entry.id}`}>
      <ListRow
        to={`/device/${entry.id}`}
        status={entryStatus(entry)}
        primary={name}
        secondary={hostname}
        trailing={
          <>
            <span className="text-xs text-slate-400">{entryLabel(entry)}</span>
            <RowChevron />
          </>
        }
      />
    </motion.div>
  );
}

const GETTING_STARTED_DISMISSED_KEY = 'wesync.gettingStartedDismissed';

export function DevicesPage() {
  const navigate = useNavigate();
  const { addToast } = useToast();
  const { myID, myName, devices, incoming, visible, listening, pendingFolders, folders } = useWS();

  const network = deriveNetwork({ devices, incoming, folders }, new Set());
  const trusted = network.filter(
    (e) => e.kind === 'connected' || e.kind === 'offline' || e.kind === 'waiting',
  );
  const discoverable = network.filter((e) => e.kind === 'discoverable' || e.kind === 'incoming');
  // scanning = we're looking for others (listening); active = we're visible to
  // others (our own announce). Independent: switching off our discoverability
  // must NOT stop the radar — we keep seeing nearby devices either way.
  const scanning = listening === true;
  const active = visible === true;
  const hasSharedFolders = folders.length > 0;

  // First-visit guide modal: auto-opens once for users with no trusted
  // devices; afterwards only re-openable via the help button. Gated on
  // `visible !== null` so we don't flash the modal during the brief window
  // before the first WS state arrives (when trusted is empty by default).
  const [guideOpen, setGuideOpen] = useState(false);
  const wsReady = visible !== null;
  useEffect(() => {
    if (!wsReady) return;
    if (trusted.length > 0) return;
    if (localStorage.getItem(GETTING_STARTED_DISMISSED_KEY)) return;
    setGuideOpen(true);
  }, [wsReady, trusted.length]);
  const closeGuide = () => {
    localStorage.setItem(GETTING_STARTED_DISMISSED_KEY, '1');
    setGuideOpen(false);
  };

  // Accepting an incoming trust request → nudge to share a folder.
  const handleAccept = async (peer: Peer) => {
    await api.pair(peer.deviceID, peer.name);
    addToast(`Now trusting ${peer.name}`, 'success', {
      label: 'Share a folder',
      onClick: () => navigate('/folders'),
    });
  };

  // Sending a trust request → waiting for the other side to accept.
  const handleTrust = async (peer: Peer) => {
    await api.pair(peer.deviceID, peer.name);
    addToast(`Trust request sent to ${peer.name}`, 'info');
  };

  const handleCancel = (peer: Peer) => api.removeDevice(peer.deviceID);

  return (
    <LayoutGroup>
      <div className="flex flex-col flex-1 select-none">
        {/* ── Radar / discover ── */}
        <div className="relative flex-1 overflow-hidden">
          <RadarBackground active={scanning} />
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 px-4 pb-10">
            {discoverable.length === 0 && scanning && (
              <p className="text-sm text-slate-400">Scanning for nearby devices…</p>
            )}
            {discoverable.map((entry) => (
              <DeviceCard
                key={entry.id}
                entry={entry}
                pendingFolders={pendingFolders}
                onAccept={handleAccept}
                onCancel={handleCancel}
                onPair={handleTrust}
              />
            ))}
          </div>
          <DeviceLabel myID={myID} myName={myName} active={active} />

          {/* Help button — re-opens the getting-started guide. */}
          <button
            type="button"
            onClick={() => setGuideOpen(true)}
            className="absolute top-3 right-3 w-8 h-8 rounded-full flex items-center justify-center text-slate-400 hover:text-slate-700 hover:bg-white/60 transition-colors"
            title="How it works"
            aria-label="Open getting-started guide"
          >
            <HelpCircle className="w-5 h-5" />
          </button>
        </div>

        <GettingStarted open={guideOpen} onClose={closeGuide} />

        {/* ── Trusted devices ── */}
        {trusted.length > 0 && (
          <div className="px-3 pt-4 pb-5 sm:px-4 flex flex-col gap-3">
            <p className="text-xs font-semibold text-slate-400 uppercase tracking-wide px-2">
              Trusted
            </p>
            <ListCard
              footer={
                <ListRow
                  to="/folders"
                  leading={<FolderIcon className="w-4 h-4 text-amber-400" />}
                  primary={hasSharedFolders ? 'Synced folders' : 'Share a folder'}
                  secondary={
                    hasSharedFolders
                      ? undefined
                      : 'Next step — sync files with your trusted devices'
                  }
                  trailing={<RowChevron />}
                />
              }
            >
              {trusted.map((entry) => (
                <TrustedRow key={entry.id} entry={entry} />
              ))}
            </ListCard>
          </div>
        )}
      </div>
    </LayoutGroup>
  );
}
