import type { Peer, Device } from '../../api/client';

export type NetworkEntry =
  | { kind: 'incoming'; id: string; name: string; peer: Peer }
  | { kind: 'waiting'; id: string; name: string; peer: Peer }
  | { kind: 'connected'; id: string; name: string; device: Device }
  | { kind: 'offline'; id: string; name: string; device: Device }
  | { kind: 'discoverable'; id: string; name: string; peer: Peer };

export function deviceColors(id: string) {
  let h = 0;
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  const hue = h % 360;
  return {
    bg: `hsl(${hue},60%,90%)`,
    border: `hsl(${hue},55%,58%)`,
    text: `hsl(${hue},55%,28%)`,
  };
}
