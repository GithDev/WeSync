import type { Peer, Device, IncomingRequest, WeSyncFolder } from '../../api/client';
import type { NetworkEntry } from './types';

export interface NetworkInput {
  devices: Device[];
  incoming: IncomingRequest[];
  folders: WeSyncFolder[];
}

export function deriveNetwork(ws: NetworkInput, accepted: Set<string>): NetworkEntry[] {
  // Defensive against malformed/partial WS state — these are iterated directly
  // below, and a null array (or a never-arrived field) would otherwise throw
  // and blank the whole Devices page.
  const devices = ws.devices ?? [];
  const incoming = ws.incoming ?? [];
  const folders = ws.folders ?? [];

  const folderIDs = new Set(folders.flatMap((f) => f.deviceIDs ?? []));
  const knownIDs = new Set(
    devices.filter((d) => d.stPaired || folderIDs.has(d.deviceID)).map((d) => d.deviceID),
  );
  const incomingIDs = new Set(incoming.map((p) => p.deviceID));

  const deviceByID = new Map(devices.map((d) => [d.deviceID, d]));

  // Incoming trust requests (from ST pending devices).
  const incomingEntries = incoming
    .filter((p) => !knownIDs.has(p.deviceID) && !accepted.has(p.deviceID))
    .map((p) => {
      const dev = deviceByID.get(p.deviceID);
      const name = dev?.name || p.name || p.deviceID.slice(0, 7);
      return {
        kind: 'incoming' as const,
        id: p.deviceID,
        name,
        peer: { deviceID: p.deviceID, name, info: dev?.info } as Peer,
      };
    });

  // Trusted devices (stPaired) not yet BEP-accepted — shown as "waiting"
  // regardless of wire connection state. A live wire connection is an
  // implementation detail; trust hasn't been mutually established until
  // accepted=true (BEP lastSeen non-empty). Showing "connected" for a
  // one-sided trust request is misleading.
  //
  // Note: non-trusted folder participants (stPaired=false) are handled
  // separately below — they show as "connected" when wire-connected because
  // they were introduced via a trusted intermediary and sync is already working.
  const waiting = devices
    .filter((d) => d.stPaired && !d.accepted)
    .map((d) => ({
      kind: 'waiting' as const,
      id: d.deviceID,
      name: d.name,
      peer: { deviceID: d.deviceID, name: d.name, info: d.info } as Peer,
    }));

  // Discoverable = socket-connected, not known, not in pairing flow.
  const discoverable = devices.filter(
    (d) =>
      d.connected &&
      !knownIDs.has(d.deviceID) &&
      !incomingIDs.has(d.deviceID) &&
      !accepted.has(d.deviceID),
  );

  const entries: NetworkEntry[] = [
    ...incomingEntries,
    ...waiting,
    // Wire-connected + known = online. For stPaired devices, also requires
    // accepted (BEP confirmed). For folder-only participants (stPaired=false),
    // wire connection is sufficient — they're here via Introducer.
    ...devices
      .filter((d) => d.connected && knownIDs.has(d.deviceID) && (d.accepted || !d.stPaired))
      .map((d) => ({
        kind: 'connected' as const,
        id: d.deviceID,
        name: d.name,
        device: d,
      })),
    // Trusted + BEP-accepted + not wire-connected = offline (was connected before).
    ...devices
      .filter((d) => !d.connected && knownIDs.has(d.deviceID) && d.accepted)
      .map((d) => ({
        kind: 'offline' as const,
        id: d.deviceID,
        name: d.name,
        device: d,
      })),
    ...discoverable.map((d) => ({
      kind: 'discoverable' as const,
      id: d.deviceID,
      name: d.name,
      peer: { deviceID: d.deviceID, name: d.name, info: d.info } as Peer,
    })),
  ];

  // Name is always a string from the backend, but a missing/odd value must not
  // crash the sort (and take the whole page down with it) — coerce defensively.
  return entries.sort((a, b) =>
    (a.name ?? '').localeCompare(b.name ?? '', undefined, { sensitivity: 'base' }),
  );
}
