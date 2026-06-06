import { useState, useEffect, useRef } from 'react';
import { api } from '../../api/client';
import type { Peer } from '../../api/client';
import { useWS } from '../../api/websocket';
import { useToast } from '../base/Toast/Toast';
import { deriveNetwork } from './Discovery.logic';
import { DeviceCard } from './DeviceCard';
import { RadarBackground } from './RadarBackground';
import { DeviceLabel } from './DeviceLabel';

export function Discovery() {
  const { myID, myName, devices, folders, incoming, visible, listening, pendingFolders } = useWS();

  const { addToast } = useToast();
  const prevDiscoveryEnabled = useRef(visible);

  useEffect(() => {
    if (prevDiscoveryEnabled.current === true && visible === false) {
      addToast(
        'Discovery turned off — your device is no longer visible to others on the network. Re-enable from the top bar when needed.',
        'info',
      );
    }
    prevDiscoveryEnabled.current = visible;
  }, [visible, addToast]);

  const [accepted, setAccepted] = useState<Set<string>>(new Set());
  useEffect(() => {
    setAccepted((prev) => {
      const knownIDs = new Set([
        ...devices.filter((d) => d.stPaired).map((d) => d.deviceID),
        ...folders.flatMap((f) => f.deviceIDs ?? []),
      ]);
      const next = new Set([...prev].filter((id) => knownIDs.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [devices, folders]);

  const network = deriveNetwork({ devices, incoming, folders }, accepted);
  // scanning = are we looking for others (listening). discoverable = are we
  // visible to others (our own announce). They're independent: turning off our
  // own discoverability must NOT stop the radar from finding nearby devices.
  const scanning = listening === true;
  const discoverable = visible === true;

  const handleAccept = async (peer: Peer) => {
    setAccepted((prev) => new Set(prev).add(peer.deviceID));
    await api.pair(peer.deviceID, peer.name);
  };
  const handlePair = (peer: Peer) => api.pair(peer.deviceID, peer.name);
  const handleCancel = (peer: Peer) => api.removeDevice(peer.deviceID);

  return (
    <div className="select-none flex flex-col flex-1">
      <div className="relative flex-1 overflow-hidden">
        <RadarBackground active={scanning} />

        <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 px-4 pb-16">
          {network.length === 0 ? (
            <p className="text-sm text-slate-400">
              {scanning ? 'Scanning for nearby devices…' : 'No trusted devices'}
            </p>
          ) : (
            network.map((entry) => (
              <DeviceCard
                key={entry.id}
                entry={entry}
                pendingFolders={pendingFolders}
                onAccept={handleAccept}
                onCancel={handleCancel}
                onPair={handlePair}
              />
            ))
          )}
        </div>

        <DeviceLabel myID={myID} myName={myName} active={discoverable} />
      </div>
    </div>
  );
}
