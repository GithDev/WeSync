import type { Device } from '../api/client';

/**
 * The display label for a device: "Name · hostname" when the hostname adds
 * information, otherwise just the name. Centralises the
 * `hostname && hostname !== name ? …` formula that was copied across the
 * folder views. Accepts raw or already-cleaned device shapes.
 */
export function deviceLabel(d: { name: string; hostname?: string }): string {
  return d.hostname && d.hostname !== d.name ? `${d.name} · ${d.hostname}` : d.name;
}

/**
 * deviceID → name / hostname / connected lookups, built in one pass. Several
 * views need one or more of these maps over the full device list; this keeps
 * the `Object.fromEntries(devices.map(...))` triplet in a single place.
 */
export function deviceMaps(devices: Device[]): {
  nameMap: Record<string, string>;
  hostnameMap: Record<string, string | undefined>;
  connectedMap: Record<string, boolean>;
} {
  return {
    nameMap: Object.fromEntries(devices.map((d) => [d.deviceID, d.name])),
    hostnameMap: Object.fromEntries(devices.map((d) => [d.deviceID, d.info?.hostname])),
    connectedMap: Object.fromEntries(devices.map((d) => [d.deviceID, d.connected])),
  };
}
