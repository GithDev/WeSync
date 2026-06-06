import type { Device } from '../api/client';

/**
 * Where the root path ("/") should redirect. Once the user has at least one
 * *paired* device they're past setup → folders. Otherwise → devices, which
 * hosts the getting-started guide.
 *
 * `devices` also carries unpaired discovered peers, so we must gate on
 * `stPaired` rather than array length — keying on length would skip the guide
 * whenever any device merely happens to be nearby.
 */
export function homeTarget(devices: Pick<Device, 'stPaired'>[]): '/folders' | '/devices' {
  return devices.some((d) => d.stPaired) ? '/folders' : '/devices';
}
