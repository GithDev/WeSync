import { useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { useWS } from '../api/websocket';
import { useToast } from '../components/base/Toast/Toast';
import { deviceMaps } from '../state/device-display';

export function useNotifications() {
  const { devices, folders, incoming, pendingFolders } = useWS();
  const { addToast } = useToast();
  const navigate = useNavigate();

  const seenNearby = useRef(new Set<string>());
  const seenIncoming = useRef(new Set<string>());
  const seenFolders = useRef(new Set<string>());

  const collaboratingIDs = new Set(folders.flatMap((f) => f.deviceIDs ?? []));
  const incomingIDs = new Set(incoming.map((r) => r.deviceID));

  const { nameMap, hostnameMap } = deviceMaps(devices);

  const displayName = (id: string) => {
    const name = nameMap[id] || id.slice(0, 7);
    const host = hostnameMap[id];
    return host && host !== name ? `${name} (${host})` : name;
  };

  // Notify when a new device connects that isn't collaborating yet
  const nearbyDevices = devices.filter(
    (d) => d.connected && !collaboratingIDs.has(d.deviceID) && !incomingIDs.has(d.deviceID),
  );

  useEffect(() => {
    nearbyDevices.forEach((d) => {
      if (!seenNearby.current.has(d.deviceID)) {
        seenNearby.current.add(d.deviceID);
        addToast(`${displayName(d.deviceID)} is nearby`, 'info', {
          label: 'Go to Devices',
          onClick: () => navigate('/devices'),
        });
      }
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nearbyDevices.map((d) => d.deviceID).join(',')]);

  useEffect(() => {
    incoming.forEach((r) => {
      if (!seenIncoming.current.has(r.deviceID)) {
        seenIncoming.current.add(r.deviceID);
        addToast(`Trust request from ${displayName(r.deviceID)}`, 'info', {
          label: 'Go to Devices',
          onClick: () => navigate('/devices'),
        });
      }
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [incoming.map((r) => r.deviceID).join(',')]);

  useEffect(() => {
    pendingFolders.forEach((pf) => {
      const key = `${pf.folderID}:${pf.deviceID}`;
      if (!seenFolders.current.has(key)) {
        seenFolders.current.add(key);
        addToast(`${displayName(pf.deviceID)} wants to share "${pf.label}"`, 'info', {
          label: 'Go to Folders',
          onClick: () => navigate('/folders'),
        });
      }
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pendingFolders.map((pf) => `${pf.folderID}:${pf.deviceID}`).join(',')]);
}
