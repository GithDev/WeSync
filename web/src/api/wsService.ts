import type { Device, IncomingRequest, PendingFolder, WeSyncFolder } from './client';

export interface WSState {
  myID: string;
  myName: string;
  devices: Device[];
  incoming: IncomingRequest[];
  visible: boolean | null; // announcing via UDP — others can discover us
  listening: boolean | null; // receiving UDP announcements — we can discover others
  pendingFolders: PendingFolder[];
  folders: WeSyncFolder[];
}

const defaults: WSState = {
  myID: '',
  myName: '',
  devices: [],
  incoming: [],
  visible: null,
  listening: null,
  pendingFolders: [],
  folders: [],
};

type Listener = (state: WSState) => void;

// Array fields are consumed directly across the UI (.map/.filter/.flatMap),
// so a null/non-array value in any push would throw during render and — with
// NavBar et al. rendered on every route — blank the whole app. Coerce here so
// a malformed or partial push can never poison downstream renders: only a real
// array replaces the previous value; anything else keeps what we had.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function arr<T>(next: any, prev: T[]): T[] {
  return Array.isArray(next) ? next : prev;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function merge(prev: WSState, s: Record<string, any>): WSState {
  return {
    myID: s.myID !== undefined ? s.myID : prev.myID,
    myName: s.name !== undefined ? s.name : prev.myName,
    devices: arr(s.devices, prev.devices),
    incoming: arr(s.incoming, prev.incoming),
    visible: s.visible !== undefined ? s.visible : prev.visible,
    listening: s.listening !== undefined ? s.listening : prev.listening,
    pendingFolders: arr(s.pendingFolders, prev.pendingFolders),
    folders: arr(s.folders, prev.folders),
  };
}

class WebSocketService {
  private ws: WebSocket | null = null;

  private state: WSState = { ...defaults };

  private listeners = new Set<Listener>();

  connect(): void {
    if (this.ws) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    // When running inside Wails WebView, location.host is 'wails.localhost' (no port).
    // Use the known WeSync backend port directly in that case.
    const host = location.hostname.includes('wails') ? 'localhost:47820' : location.host;
    const socket = new WebSocket(`${proto}//${host}/api/ws`);

    socket.onmessage = (e: MessageEvent) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const raw = JSON.parse(e.data as string) as Record<string, any>;
      console.log('[WS push]', new Date().toISOString(), raw);
      this.state = merge(this.state, raw);
      this.notify();
    };

    socket.onclose = () => {
      this.ws = null;
      setTimeout(() => this.connect(), 3000);
    };

    this.ws = socket;
  }

  getState(): WSState {
    return this.state;
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private notify(): void {
    this.listeners.forEach((l) => l(this.state));
  }
}

export const wsService = new WebSocketService();
wsService.connect();
