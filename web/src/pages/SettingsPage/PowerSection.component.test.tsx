// @vitest-environment happy-dom
/**
 * Component render tests for PowerSection.
 *
 * These exist to catch regressions that pure logic tests cannot: missing UI
 * controls (e.g. a SyncTrigger value that has no radio button), broken render
 * paths, or settings fields that are fetched but never displayed.
 *
 * If you add a new SyncTrigger value, add a case to the allTriggers loop below.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent, cleanup } from '@testing-library/react';
import { PowerSection } from './PowerSection';
import { SyncTrigger, NetworkMode } from '../../types/enums';
import type { PowerSettings } from '../../api/client';

// PowerSection returns null on non-Android — force isAndroid() to true.
vi.mock('../../platform', () => ({ isAndroid: () => true }));

// Stub Toast so we don't need its context provider.
vi.mock('../../components/base/Toast/Toast', () => ({
  useToast: () => ({ addToast: vi.fn() }),
}));

// vi.mock is hoisted, so use vi.hoisted for values referenced inside the factory.
const { mockGetPowerSettings } = vi.hoisted(() => ({
  mockGetPowerSettings: vi.fn(),
}));

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>();
  return {
    ...actual,
    api: {
      getPowerSettings: mockGetPowerSettings,
      getPowerStatus: vi.fn().mockResolvedValue({}),
      getPowerEvents: vi.fn().mockResolvedValue([]),
      setPowerSettings: vi.fn().mockResolvedValue(undefined),
    },
  };
});

const baseSettings: PowerSettings = {
  syncTrigger: SyncTrigger.OnChangePoll,
  periodicMinutes: 240,
  onChangePollMinutes: 30,
  scheduledTimes: [],
  networkMode: NetworkMode.AnyWifi,
  trustedSSIDs: [],
  pauseWhenBatteryLow: true,
  keepSyncingWhileCharging: false,
  blockMeteredRoaming: true,
};

beforeEach(() => {
  mockGetPowerSettings.mockResolvedValue(baseSettings);
});

afterEach(cleanup);

// Regression: every SyncTrigger value must have a radio button in the UI.
// This test renders PowerSection with each trigger pre-selected and asserts
// that a <input type="radio"> with that value exists in the DOM. A missing
// radio means users on that trigger mode can't see or change it.
describe('PowerSection — radio coverage for all SyncTrigger values', () => {
  const allTriggers = Object.values(SyncTrigger);

  for (const trigger of allTriggers) {
    it(`renders a radio button for SyncTrigger.${trigger}`, async () => {
      mockGetPowerSettings.mockResolvedValue({
        ...baseSettings,
        syncTrigger: trigger,
        scheduledTimes: trigger === SyncTrigger.Scheduled ? ['08:00'] : [],
      });

      render(<PowerSection />);

      // The "When to sync" row is collapsed by default — open it first.
      await waitFor(() => {
        expect(screen.getByText('When to sync')).toBeInTheDocument();
      });
      fireEvent.click(screen.getByText('When to sync'));

      await waitFor(() => {
        const radio = screen.getByDisplayValue(trigger);
        expect(radio).toBeInTheDocument();
        expect(radio).toHaveAttribute('type', 'radio');
      });
    });
  }
});
