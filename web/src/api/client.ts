// Power-gate settings. Mirrors store.PowerSettings on the Go side. All
// fields are present in every payload — defaults applied server-side
// before persisting so the client never has to do partial-update logic.
export interface PowerSettings {
  syncTrigger: 'periodic' | 'scheduled' | 'on_change';
  periodicMinutes: number;
  scheduledTimes: string[];
  onChangeDebounceMinutes: number;
  networkMode: 'any' | 'any_wifi' | 'trusted_wifi';
  trustedSSIDs: string[];
  pauseWhenBatteryLow: boolean;
  keepSyncingWhileCharging: boolean;
  blockMeteredRoaming: boolean;
}

export interface PowerEvent {
  id: number;
  timestamp: string;
  kind: string;
  message: string;
}

export interface PowerStatus {
  stRunning?: boolean;
  foldersUnpaused?: boolean;
  appForeground?: boolean;
  hasWifi?: boolean;
  hasMobile?: boolean;
  currentSSID?: string;
  batteryLow?: boolean;
  charging?: boolean;
  metered?: boolean;
  roaming?: boolean;
  activeWifi?: boolean;
  networkAllowed?: boolean;
  triggerWindowOpen?: boolean;
  windowEndsInSecs?: number;
}

export interface SystemStatus {
  myID: string;
  name: string;
}

// Whether ST actually has a working relay connection (not merely enabled).
// live + address when a relay:// listener is up; error carries ST's listener
// failure when relay is on but not reaching.
export interface RelayStatus {
  live: boolean;
  address?: string;
  error?: string;
}

// Whether ST is actually reaching the global discovery servers (not merely that
// global discovery is enabled). live + ok/servers when at least one server is
// reachable; error carries a representative failure when none are reaching.
export interface DiscoveryStatus {
  live: boolean;
  servers: number;
  ok: number;
  error?: string;
}

// Combined relay + global-discovery health, from a single backend read of ST's
// system status. The Settings page polls one endpoint instead of one per concern.
export interface ConnectivityStatus {
  relay: RelayStatus;
  discovery: DiscoveryStatus;
}

export interface DeviceIface {
  name: string;
  mac?: string;
  ips: string[];
}

export interface DeviceInfo {
  hostname: string;
  os: string;
  osVer?: string;
  ifaces?: DeviceIface[];
}

export interface Peer {
  name: string;
  deviceID: string;
  addr?: string;
  info?: DeviceInfo;
}

export interface Device {
  deviceID: string;
  name: string;
  connected: boolean;
  stPaired: boolean; // true = explicitly paired
  accepted: boolean; // true = has connected via BEP (lastSeen non-empty); false = waiting for response
  /** ST's last contact time (RFC3339); anchors an offline peer's honest
   *  "in sync as of <time>". Empty/absent when never seen. */
  lastSeen?: string;
  info?: DeviceInfo;
}

export interface IncomingRequest {
  deviceID: string;
  name: string;
}

export interface Mode {
  visible: boolean; // true = announcing via UDP (others can discover us)
}

export interface FolderStatus {
  state: string; // idle | scanning | syncing | error
  needFiles: number;
  needBytes: number;
  globalFiles: number;
  globalBytes: number;
  localFiles: number;
  inSyncFiles: number;
  pullErrors: number;
  error: string;
  stateChanged: string;
  paused: boolean;
  /** Scan completion 0–100 while state === 'scanning'; 0 otherwise or before
   *  the first scan-progress event has arrived. */
  scanPct: number;
  /** Receive-only folders: items (files/dirs/etc.) changed or added locally that
   *  aren't — and won't be — sent to the cluster. >0 means this device holds
   *  changes the source doesn't have. */
  receiveOnlyTotalItems: number;
  receiveOnlyChangedFiles: number;
  receiveOnlyChangedBytes: number;
}

export interface WeSyncFolder {
  id: string;
  label: string;
  path: string;
  type: string; // sendonly | receiveonly | sendreceive
  deviceIDs: string[]; // ALL participants — trusted + others from ST/BEP
  deviceTypes?: Record<string, string>; // deviceID → direction
  deviceAccepted?: Record<string, boolean>; // legacy bool — keep using for now; new code reads deviceState
  deviceTrusted?: Record<string, boolean>; // deviceID → explicitly paired (false = other device via mesh)
  // deviceState is the central per-device FolderRelationState string from
  // the backend — see docs/state-model.md. Always present; new UI code
  // switches on this instead of deriving from deviceAccepted/connected.
  deviceState?: Record<string, FolderRelationState>;
  // devicePeer carries per-device numeric sync detail (how much B still needs
  // FROM US + B's completion of our data), keyed by deviceID. Present only for
  // devices with pending/in-progress work — drives the "Sending — X left" /
  // "N items not yet sent" labels alongside deviceState.
  devicePeer?: Record<string, PeerDetail>;
}

export interface PeerDetail {
  needBytes: number;
  needItems: number;
  completion: number; // 0–100
}

// FolderRelationState mirrors Go's FolderRelationState. Keep this in sync
// with internal/api/folder_relation_state.go — the test in
// folder-relation.test.ts pins the exact set of literals.
export type FolderRelationState =
  | 'not-member'
  | 'invited'
  | 'accepted-paused-local'
  | 'accepted-paused-remote'
  | 'accepted-syncing'
  | 'accepted-sending'
  | 'accepted-stalled'
  | 'accepted-idle'
  | 'accepted-behind-offline'
  | 'accepted-offline';

export const FOLDER_RELATION_STATES: readonly FolderRelationState[] = [
  'not-member',
  'invited',
  'accepted-paused-local',
  'accepted-paused-remote',
  'accepted-syncing',
  'accepted-sending',
  'accepted-stalled',
  'accepted-idle',
  'accepted-behind-offline',
  'accepted-offline',
] as const;

/** Boolean shorthand — true iff the state means B accepted F. */
export function isAccepted(s: FolderRelationState | undefined): boolean {
  if (!s) return false;
  return (
    s === 'accepted-paused-local' ||
    s === 'accepted-paused-remote' ||
    s === 'accepted-syncing' ||
    s === 'accepted-sending' ||
    s === 'accepted-stalled' ||
    s === 'accepted-idle' ||
    s === 'accepted-behind-offline' ||
    s === 'accepted-offline'
  );
}

export interface ConflictFile {
  path: string;
  originalPath: string;
}

export interface PendingFolder {
  folderID: string;
  label: string;
  deviceID: string; // which device offered it
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) throw new Error(`${path} returned ${res.status}`);
  return res.json() as Promise<T>;
}

async function post<T = void>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new Error(text.trim() || `${path} returned ${res.status}`);
  }
  if (res.status === 204 || res.headers.get('content-length') === '0') {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}

async function put(path: string, body: unknown): Promise<void> {
  const res = await fetch(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${path} returned ${res.status}`);
}

async function patch(path: string, body: unknown): Promise<void> {
  const res = await fetch(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${path} returned ${res.status}`);
}

async function del(path: string): Promise<void> {
  const res = await fetch(path, { method: 'DELETE' });
  if (!res.ok) throw new Error(`${path} returned ${res.status}`);
}

export const api = {
  status: () => get<SystemStatus>('/api/status'),
  mode: () => get<Mode>('/api/mode'),
  setMode: (visible: boolean) => put('/api/mode', { visible }),
  peers: () => get<Peer[]>('/api/peers'),
  devices: () => get<Device[]>('/api/devices'),
  removeDevice: (id: string) => del(`/api/devices?id=${encodeURIComponent(id)}`),
  pair: (deviceID: string, name: string) => post('/api/pair', { deviceID, name }),
  pairing: () => get<Record<string, string>>('/api/pairing'),
  incoming: () => get<IncomingRequest[]>('/api/incoming'),
  dismissIncoming: (id: string) => del(`/api/incoming?id=${encodeURIComponent(id)}`),
  sync: () => post('/api/sync', {}),
  setName: (name: string) => put('/api/name', { name }),
  getConnectivityLevel: () => get<{ level: number }>('/api/connectivity'),
  setConnectivityLevel: (level: number) => put('/api/connectivity', { level }),
  getConnectivityStatus: () => get<ConnectivityStatus>('/api/connectivity-status'),
  getPowerSettings: () => get<PowerSettings>('/api/power'),
  setPowerSettings: (p: PowerSettings) => put('/api/power', p),
  getPowerEvents: (limit = 50) => get<PowerEvent[]>(`/api/power/events?limit=${limit}`),
  getPowerStatus: () => get<PowerStatus>('/api/power/status'),
  powerSyncNow: () => post('/api/power/sync-now', {}),
  pickFolder: () => get<{ path: string }>('/api/folder/pick'),
  shareFolder: (deviceID: string, path: string, label?: string, direction?: string) =>
    post<{ folderID: string }>('/api/folder/share', { deviceID, path, label, direction }),
  acceptFolder: (folderID: string, deviceID: string, path: string, direction?: string) =>
    post('/api/folder/accept', { folderID, deviceID, path, direction }),
  declineFolder: (folderID: string, deviceID: string) =>
    post('/api/folder/decline', { folderID, deviceID }),
  removeFolder: (folderID: string) => del(`/api/folder?id=${encodeURIComponent(folderID)}`),
  removeFolderDevice: (folderID: string, deviceID: string) =>
    del(
      `/api/folder/device?folderID=${encodeURIComponent(folderID)}&deviceID=${encodeURIComponent(deviceID)}`,
    ),
  updateFolderDirection: (folderID: string, direction: string) =>
    patch('/api/folder/direction', { folderID, direction }),
  getFolderStatus: (folderID: string) =>
    get<FolderStatus>(`/api/folder/status?id=${encodeURIComponent(folderID)}`),
  updateFolderLabel: (folderID: string, label: string) =>
    patch('/api/folder/label', { folderID, label }),
  checkFolderPath: (path: string) =>
    get<{ empty: boolean; fileCount: number }>(
      `/api/folder/check?path=${encodeURIComponent(path)}`,
    ),
  fixFolderMarker: (folderID: string) =>
    post(`/api/folder/fix-marker?id=${encodeURIComponent(folderID)}`, {}),
  revertFolder: (folderID: string) =>
    post(`/api/folder/revert?id=${encodeURIComponent(folderID)}`, {}),
  setFolderPaused: (folderID: string, paused: boolean) =>
    patch('/api/folder/pause', { folderID, paused }),
  getFolderIgnores: (folderID: string) =>
    get<{ patterns: string[] }>(`/api/folder/ignores?id=${encodeURIComponent(folderID)}`),
  setFolderIgnores: (folderID: string, patterns: string[]) =>
    post(`/api/folder/ignores?id=${encodeURIComponent(folderID)}`, { patterns }),
  getFolderConflicts: (folderID: string) =>
    get<ConflictFile[]>(`/api/folder/conflicts?id=${encodeURIComponent(folderID)}`),
  deleteFolderConflict: (folderID: string, path: string) =>
    del(`/api/folder/conflict?id=${encodeURIComponent(folderID)}&path=${encodeURIComponent(path)}`),
};
