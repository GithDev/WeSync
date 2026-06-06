import { useWS } from '../api/websocket';

/**
 * Badge counts shared by the desktop NavBar and the mobile BottomNav, so the
 * "nearby" / "pending folders" dot logic lives in exactly one place.
 *
 * `nearbyCount` = connected, unpaired peers we aren't already collaborating
 * with, plus any incoming pair requests.
 */
export function useNavBadges(): { nearbyCount: number; pendingCount: number } {
  const { devices, folders, incoming, pendingFolders } = useWS();

  const collaboratingIDs = new Set(folders.flatMap((f) => f.deviceIDs ?? []));
  const nearby = devices.filter(
    (d) => d.connected && !d.stPaired && !collaboratingIDs.has(d.deviceID),
  );
  const nearbyCount = nearby.length + incoming.length;

  return { nearbyCount, pendingCount: pendingFolders.length };
}
