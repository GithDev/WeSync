import type { PowerSettings, PowerStatus } from '../../api/client';
import { SyncTrigger, NetworkMode } from '../../types/enums';

export const fmtMinutes = (m: number): string => (m < 60 ? `${m} min` : `${m / 60} h`);

export function whenSummary(s: PowerSettings): string {
  if (s.syncTrigger === SyncTrigger.Scheduled)
    return s.scheduledTimes.length ? s.scheduledTimes.join(', ') : 'No times set';
  if (s.syncTrigger === SyncTrigger.OnChangePoll) return 'When something changes';
  return `Every ${fmtMinutes(s.periodicMinutes)}`;
}

export interface StatusLine {
  good: boolean;
  text: string;
}

export function describeStatus(s: PowerStatus | null, settings: PowerSettings): StatusLine[] {
  if (!s) return [];
  const lines: StatusLine[] = [];

  if (settings.blockMeteredRoaming && (s.roaming || (s.metered && s.activeWifi))) {
    lines.push({ good: false, text: `On ${s.roaming ? 'roaming' : 'metered WiFi'} — paused` });
  } else if (settings.networkMode === NetworkMode.Any) {
    lines.push(
      s.hasWifi || s.hasMobile
        ? { good: true, text: `Connected${s.hasWifi ? ' (WiFi)' : ' (mobile data)'}` }
        : { good: false, text: 'No network' },
    );
  } else if (settings.networkMode === NetworkMode.AnyWifi) {
    lines.push(
      s.hasWifi
        ? { good: true, text: `On WiFi${s.currentSSID ? ` (${s.currentSSID})` : ''}` }
        : { good: false, text: 'Not on WiFi — waiting' },
    );
  } else {
    // trusted_wifi
    if (!s.hasWifi) {
      lines.push({ good: false, text: 'Not on WiFi — waiting' });
    } else if (!s.currentSSID) {
      lines.push({ good: false, text: 'WiFi name unknown (location permission missing?)' });
    } else if (s.networkAllowed) {
      lines.push({ good: true, text: `On a trusted WiFi (${s.currentSSID})` });
    } else {
      lines.push({ good: false, text: `On "${s.currentSSID}" — not in your trusted list` });
    }
  }

  if (settings.keepSyncingWhileCharging && s.charging) {
    lines.push({ good: true, text: 'Charging — syncing continuously' });
  }

  if (settings.pauseWhenBatteryLow) {
    lines.push(
      s.batteryLow
        ? { good: false, text: 'Battery low — sync paused' }
        : { good: true, text: 'Battery OK' },
    );
  }

  // Trigger / window
  if (s.triggerWindowOpen) {
    const secs = s.windowEndsInSecs ?? 0;
    const remaining = secs > 60 ? `${Math.round(secs / 60)} min` : `${secs}s`;
    lines.push({ good: true, text: `Syncing now — window open for ${remaining}` });
  } else if (settings.syncTrigger === SyncTrigger.Periodic) {
    lines.push({ good: true, text: `Waiting — next sync within ~${settings.periodicMinutes} min` });
  } else if (settings.syncTrigger === SyncTrigger.Scheduled) {
    if (settings.scheduledTimes.length === 0) {
      lines.push({ good: false, text: 'No times set — sync will never run' });
    } else {
      lines.push({
        good: true,
        text: `Waiting for next scheduled time (${settings.scheduledTimes.join(', ')})`,
      });
    }
  } else if (settings.syncTrigger === SyncTrigger.OnChangePoll) {
    lines.push({
      good: true,
      text: `Watching for changes (checks every ~${settings.periodicMinutes} min)`,
    });
  }

  return lines;
}
