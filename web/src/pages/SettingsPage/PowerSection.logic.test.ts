import { describe, it, expect } from 'vitest';
import { whenSummary, describeStatus } from './PowerSection.logic';
import type { PowerSettings, PowerStatus } from '../../api/client';
import { SyncTrigger, NetworkMode } from '../../types/enums';

function makeSettings(over: Partial<PowerSettings> = {}): PowerSettings {
  return {
    syncTrigger: SyncTrigger.OnChange,
    periodicMinutes: 240,
    scheduledTimes: [],
    onChangeDebounceMinutes: 5,
    networkMode: NetworkMode.AnyWifi,
    trustedSSIDs: [],
    pauseWhenBatteryLow: true,
    keepSyncingWhileCharging: false,
    blockMeteredRoaming: false,
    ...over,
  };
}

function makeStatus(over: Partial<PowerStatus> = {}): PowerStatus {
  return {
    hasWifi: true,
    hasMobile: false,
    currentSSID: 'Home',
    networkAllowed: true,
    charging: false,
    batteryLow: false,
    roaming: false,
    metered: false,
    activeWifi: false,
    triggerWindowOpen: false,
    ...over,
  };
}

describe('whenSummary', () => {
  it('periodic returns interval string', () => {
    expect(whenSummary(makeSettings({ syncTrigger: SyncTrigger.Periodic, periodicMinutes: 60 }))).toBe(
      'Every 1 h',
    );
  });

  it('periodic uses minutes for values under 60', () => {
    expect(whenSummary(makeSettings({ syncTrigger: SyncTrigger.Periodic, periodicMinutes: 30 }))).toBe(
      'Every 30 min',
    );
  });

  it('scheduled with times lists them', () => {
    expect(
      whenSummary(makeSettings({ syncTrigger: SyncTrigger.Scheduled, scheduledTimes: ['02:00', '14:00'] })),
    ).toBe('02:00, 14:00');
  });

  it('scheduled with no times says so', () => {
    expect(whenSummary(makeSettings({ syncTrigger: SyncTrigger.Scheduled, scheduledTimes: [] }))).toBe(
      'No times set',
    );
  });

  it('on_change returns change string', () => {
    expect(whenSummary(makeSettings({ syncTrigger: SyncTrigger.OnChange }))).toBe(
      'When something changes',
    );
  });

  it('on_change_poll returns same change string — not "Every N h"', () => {
    expect(
      whenSummary(makeSettings({ syncTrigger: SyncTrigger.OnChangePoll, periodicMinutes: 240 })),
    ).toBe('When something changes');
  });
});

describe('describeStatus — trigger lines', () => {
  it('returns empty array when status is null', () => {
    expect(describeStatus(null, makeSettings())).toEqual([]);
  });

  it('on_change emits watching-for-changes line', () => {
    const lines = describeStatus(makeStatus(), makeSettings({ syncTrigger: SyncTrigger.OnChange }));
    expect(lines.some((l) => l.good && l.text.includes('Watching for changes'))).toBe(true);
  });

  it('on_change_poll emits watching-for-changes line — not silent', () => {
    const lines = describeStatus(makeStatus(), makeSettings({ syncTrigger: SyncTrigger.OnChangePoll }));
    expect(lines.some((l) => l.good && l.text.includes('Watching for changes'))).toBe(true);
  });

  it('periodic emits "next sync within" line with the correct interval', () => {
    const lines = describeStatus(
      makeStatus(),
      makeSettings({ syncTrigger: SyncTrigger.Periodic, periodicMinutes: 30 }),
    );
    expect(lines.some((l) => l.good && l.text.includes('30'))).toBe(true);
  });

  it('scheduled with times emits scheduled-time line', () => {
    const lines = describeStatus(
      makeStatus(),
      makeSettings({ syncTrigger: SyncTrigger.Scheduled, scheduledTimes: ['03:00'] }),
    );
    expect(lines.some((l) => l.good && l.text.includes('03:00'))).toBe(true);
  });

  it('scheduled with no times emits a bad line about never running', () => {
    const lines = describeStatus(
      makeStatus(),
      makeSettings({ syncTrigger: SyncTrigger.Scheduled, scheduledTimes: [] }),
    );
    expect(lines.some((l) => !l.good && l.text.includes('never run'))).toBe(true);
  });

  it('window open takes priority over trigger type', () => {
    const lines = describeStatus(
      makeStatus({ triggerWindowOpen: true, windowEndsInSecs: 90 }),
      makeSettings({ syncTrigger: SyncTrigger.OnChange }),
    );
    expect(lines.some((l) => l.good && l.text.includes('Syncing now'))).toBe(true);
  });

  it('battery low line appears when pauseWhenBatteryLow and battery is low', () => {
    const lines = describeStatus(
      makeStatus({ batteryLow: true }),
      makeSettings({ pauseWhenBatteryLow: true }),
    );
    expect(lines.some((l) => !l.good && l.text.includes('Battery low'))).toBe(true);
  });
});

describe('describeStatus — network lines', () => {
  it('any_wifi with wifi connected is good', () => {
    const lines = describeStatus(
      makeStatus({ hasWifi: true, currentSSID: 'MyNet' }),
      makeSettings({ networkMode: NetworkMode.AnyWifi }),
    );
    expect(lines.some((l) => l.good && l.text.includes('MyNet'))).toBe(true);
  });

  it('any_wifi without wifi is bad', () => {
    const lines = describeStatus(
      makeStatus({ hasWifi: false }),
      makeSettings({ networkMode: NetworkMode.AnyWifi }),
    );
    expect(lines.some((l) => !l.good && l.text.includes('Not on WiFi'))).toBe(true);
  });

  it('any mode with mobile data is good', () => {
    const lines = describeStatus(
      makeStatus({ hasWifi: false, hasMobile: true }),
      makeSettings({ networkMode: NetworkMode.Any }),
    );
    expect(lines.some((l) => l.good && l.text.includes('mobile data'))).toBe(true);
  });
});
