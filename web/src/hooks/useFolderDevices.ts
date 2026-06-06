import type { WeSyncFolder, Device, FolderRelationState } from '../api/client';
import { isAccepted } from '../api/client';
import type { RowStatus } from '../components/base/ListRow/ListRow';
import { deviceMaps } from '../state/device-display';

export interface FolderDevice {
  deviceID: string;
  name: string;
  /** Hostname when different from name. */
  hostname?: string;
  connected: boolean;
  /** undefined = folder doesn't track acceptance (legacy), true = accepted, false = pending */
  accepted?: boolean;
  /** Consolidated visual status for the dot indicator. */
  status: RowStatus;
  /** The sync direction the peer has configured for this folder. */
  peerType?: string;
  /**
   * Authoritative per-device folder state from the backend's derive function.
   * Switch on this for new UI logic — see docs/state-model.md. The legacy
   * `accepted` and `status` fields above are derived from it for back-compat.
   */
  state?: FolderRelationState;
}

function deviceStatus(connected: boolean, accepted: boolean | undefined): RowStatus {
  if (accepted === false) return 'pending';
  return connected ? 'online' : 'offline';
}

/**
 * Derives the enriched device lists for a folder from raw WS data.
 *
 * @returns members   — trusted devices in this folder (editable)
 * @returns others    — other participants not directly trusted (read-only)
 * @returns available — trusted devices not yet in this folder
 */
export function useFolderDevices(
  folder: WeSyncFolder | undefined,
  devices: Device[],
): { members: FolderDevice[]; others: FolderDevice[]; available: FolderDevice[] } {
  const trustedDevices = devices.filter((d) => d.stPaired);
  const trustedIDs = new Set(trustedDevices.map((d) => d.deviceID));
  // Use all devices for name/hostname/connected lookup — others may be socket-connected peers.
  const { nameMap, hostnameMap, connectedMap } = deviceMaps(devices);

  // deviceIDs contains ALL participants — trusted (members) and others.
  // Prefer backend's deviceTrusted map if available; fall back to stPaired from devices list.
  const allFolderIDs = new Set(folder?.deviceIDs ?? []);
  const isTrusted = (id: string) =>
    folder?.deviceTrusted != null ? (folder.deviceTrusted[id] ?? false) : trustedIDs.has(id);
  const memberIDs = new Set(Array.from(allFolderIDs).filter((id) => isTrusted(id)));
  const otherIDs = new Set(Array.from(allFolderIDs).filter((id) => !isTrusted(id)));

  const toFolderDevice = (deviceID: string, isOther = false): FolderDevice => {
    const connected = connectedMap[deviceID] ?? false;
    // Prefer the new deviceState string when present; fall back to the legacy
    // deviceAccepted bool. The `accepted` field is then a derived shorthand —
    // existing UI keeps working while new UI can switch on `state` directly.
    const state = !isOther ? folder?.deviceState?.[deviceID] : undefined;
    const acceptedFromState = state ? isAccepted(state) : undefined;
    const acceptedFromBool =
      !isOther && folder?.deviceAccepted ? (folder.deviceAccepted[deviceID] ?? false) : undefined;
    const accepted = acceptedFromState ?? acceptedFromBool;
    const rawHostname = hostnameMap[deviceID];
    const rawName = nameMap[deviceID];
    // Prefer name → hostname → short device ID as primary label.
    const name = rawName || rawHostname || deviceID.slice(0, 7);
    const hostname = rawHostname && rawHostname !== name ? rawHostname : undefined;
    return {
      deviceID,
      name,
      hostname,
      connected,
      accepted,
      status: isOther ? ('offline' as const) : deviceStatus(connected, accepted),
      peerType: folder?.deviceTypes?.[deviceID],
      state,
    };
  };

  const members = Array.from(memberIDs).map((id) => toFolderDevice(id));
  const others = Array.from(otherIDs).map((id) => toFolderDevice(id, true));

  const available: FolderDevice[] = trustedDevices
    .filter((d) => !memberIDs.has(d.deviceID))
    .map((d) => {
      const hostname = d.info?.hostname;
      return {
        deviceID: d.deviceID,
        name: d.name,
        hostname: hostname && hostname !== d.name ? hostname : undefined,
        connected: d.connected,
        status: d.connected ? 'online' : 'offline',
      };
    });

  return { members, others, available };
}
