import { useEffect, useState } from 'react';
import { api, SyncTrigger, NetworkMode } from '../../api/client';
import type { PowerEvent, PowerSettings, PowerStatus } from '../../api/client';
import { fmtMinutes, whenSummary, describeStatus } from './PowerSection.logic';
import { useToast } from '../../components/base/Toast/Toast';
import { AsyncButton } from '../../components/base/Button/AsyncButton';
import { Card } from '../../components/base/Card/Card';
import { SectionHeading } from '../../components/base/SectionHeading/SectionHeading';
import { SettingRow } from '../../components/base/SettingRow/SettingRow';
import { isAndroid } from '../../platform';

// The power section governs *when* WeSync is allowed to sync in the
// background on Android. Desktop ignores it.
//
// Visual order, top to bottom:
//   1. "Now" panel: real-time state + Sync now button — the answer to
//      "is anything happening, and can I make it happen?"
//   2. Settings: when-to-sync radio, where-to-sync radio, battery toggle.
//      These shape the autonomous behaviour.
//   3. Recent activity: collapsed-by-default audit trail of past events.
//      For users who want to verify the autonomous flow actually ran.

const DEFAULTS: PowerSettings = {
  syncTrigger: SyncTrigger.OnChangePoll,
  periodicMinutes: 240,
  onChangePollMinutes: 30,
  scheduledTimes: [],
  networkMode: NetworkMode.AnyWifi,
  trustedSSIDs: [],
  pauseWhenBatteryLow: true,
  blockMeteredRoaming: true,
};

const PERIODIC_OPTIONS = [15, 30, 60, 120, 240, 480];
// Background wake-ups can't fire more often than every 15 min (WorkManager's
// floor; Doze batches anyway), so the quick-scan interval starts there too.
const POLL_OPTIONS = [15, 30, 60];

// A minutes dropdown for the periodic-interval picker.
function MinutesSelect({
  options,
  value,
  onChange,
  disabled,
}: {
  options: number[];
  value: number;
  onChange: (minutes: number) => void;
  disabled?: boolean;
}) {
  return (
    <select
      className="text-sm border border-slate-200 rounded-lg px-2 py-1"
      value={value}
      onChange={(e) => onChange(parseInt(e.target.value, 10))}
      disabled={disabled}
    >
      {options.map((m) => (
        <option key={m} value={m}>
          {fmtMinutes(m)}
        </option>
      ))}
    </select>
  );
}

function whereSummary(s: PowerSettings): string {
  if (s.networkMode === NetworkMode.Any) return 'Any network';
  if (s.networkMode === NetworkMode.TrustedWifi) return 'Only on selected WiFi';
  return 'Only on WiFi';
}

function batterySummary(s: PowerSettings): string {
  const on = [
    s.pauseWhenBatteryLow && 'Pause when low',
    s.blockMeteredRoaming && 'Skip metered',
  ].filter(Boolean);
  return on.length ? on.join(' · ') : 'All off';
}

interface WeSyncBridge {
  notifyPowerSettingsChanged?: () => void;
  requestLocationPermission?: () => void;
  isLocationGranted?: () => boolean;
  isForegroundLocationGranted?: () => boolean;
}

function bridge(): WeSyncBridge | undefined {
  return (window as unknown as { WeSync?: WeSyncBridge }).WeSync;
}

function notifyAndroid() {
  bridge()?.notifyPowerSettingsChanged?.();
}

// 'always'      — granted all the time; SSID readable in the background (what we need)
// 'foreground'  — granted only "while using"; SSID reads blank when the app is closed
// 'none'        — no location permission at all
// 'unknown'     — not Android / no bridge (desktop)
type LocState = 'always' | 'foreground' | 'none' | 'unknown';

function readLocationState(): LocState {
  const b = bridge();
  if (!b?.isLocationGranted) return 'unknown';
  if (b.isLocationGranted()) return 'always';
  if (b.isForegroundLocationGranted?.()) return 'foreground';
  return 'none';
}

function requestLocationPermissionIfNeeded() {
  const b = bridge();
  if (!b?.requestLocationPermission || !b.isLocationGranted) return;
  if (b.isLocationGranted()) return;
  b.requestLocationPermission();
}

export function PowerSection() {
  const { addToast } = useToast();
  const [settings, setSettings] = useState<PowerSettings>(DEFAULTS);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);

  const [locState, setLocState] = useState<LocState>('unknown');

  useEffect(() => {
    api
      .getPowerSettings()
      .then((p) => setSettings(p))
      .catch(() => {
        /* defaults stay */
      })
      .finally(() => setLoaded(true));
  }, []);

  // Re-read the location grant on mount and whenever the app regains focus —
  // notably after the user returns from the system "Allow all the time"
  // settings page, so the warning clears without a manual refresh.
  useEffect(() => {
    const refresh = () => setLocState(readLocationState());
    refresh();
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, []);

  const persist = async (next: PowerSettings) => {
    setSettings(next);
    setSaving(true);
    try {
      await api.setPowerSettings(next);
      notifyAndroid();
    } catch (e: unknown) {
      addToast(e instanceof Error ? e.message : 'Could not save', 'warning');
    } finally {
      setSaving(false);
    }
  };

  // The whole power section is Android-only — desktop/Linux run ST
  // continuously and ignore the gate entirely, so these controls would be
  // inert. Hide it rather than show options that do nothing.
  if (!isAndroid()) return null;
  if (!loaded) return null;

  return (
    <div className="flex flex-col gap-3">
      <SectionHeading>Power</SectionHeading>

      <NowPanel settings={settings} addToast={addToast} />

      {/* ── When to sync ── */}
      <SettingRow title="When to sync" summary={whenSummary(settings)}>
        <div className="flex flex-col gap-3">
          <Radio
            name="trigger"
            value="on_change_poll"
            label="When something changes"
            sublabel="Lightweight check runs at the chosen interval — only a directory scan, no data transfer. Sync only opens if files actually changed."
            checked={settings.syncTrigger === SyncTrigger.OnChangePoll}
            onChange={() => persist({ ...settings, syncTrigger: SyncTrigger.OnChangePoll })}
            disabled={saving}
          />
          {settings.syncTrigger === SyncTrigger.OnChangePoll && (
            <div className="ml-7 flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <label className="text-xs text-slate-500">Quick scan every</label>
                <MinutesSelect
                  options={POLL_OPTIONS}
                  value={settings.onChangePollMinutes}
                  onChange={(onChangePollMinutes) => persist({ ...settings, onChangePollMinutes })}
                  disabled={saving}
                />
              </div>
              <div className="flex items-center gap-2">
                <label className="text-xs text-slate-500">Also sync at least every</label>
                <MinutesSelect
                  options={PERIODIC_OPTIONS}
                  value={settings.periodicMinutes}
                  onChange={(periodicMinutes) => persist({ ...settings, periodicMinutes })}
                  disabled={saving}
                />
                <span className="text-xs text-slate-400">to receive changes from peers</span>
              </div>
            </div>
          )}
          <Radio
            name="trigger"
            value="periodic"
            label="Every so often"
            sublabel="Wake up at a regular interval and sync regardless of whether anything changed."
            checked={settings.syncTrigger === SyncTrigger.Periodic}
            onChange={() => persist({ ...settings, syncTrigger: SyncTrigger.Periodic })}
            disabled={saving}
          />
          {settings.syncTrigger === SyncTrigger.Periodic && (
            <div className="ml-7 flex items-center gap-2">
              <label className="text-xs text-slate-500">Every</label>
              <MinutesSelect
                options={PERIODIC_OPTIONS}
                value={settings.periodicMinutes}
                onChange={(periodicMinutes) => persist({ ...settings, periodicMinutes })}
                disabled={saving}
              />
              <span className="text-xs text-slate-400">(approximate, ±15 min)</span>
            </div>
          )}
          <Radio
            name="trigger"
            value="scheduled"
            label="At specific times"
            sublabel="Sync at fixed times of day, like 02:00 every night."
            checked={settings.syncTrigger === SyncTrigger.Scheduled}
            onChange={() => persist({ ...settings, syncTrigger: SyncTrigger.Scheduled })}
            disabled={saving}
          />
          {settings.syncTrigger === SyncTrigger.Scheduled && (
            <ScheduledTimes
              times={settings.scheduledTimes}
              onChange={(times) => persist({ ...settings, scheduledTimes: times })}
              disabled={saving}
            />
          )}
        </div>
      </SettingRow>

      {/* ── Where to sync ── */}
      <SettingRow title="Where to sync" summary={whereSummary(settings)}>
        <div className="flex flex-col gap-3">
          <p className="text-xs text-slate-500">
            What kind of network has to be available. If this isn't met, syncing stays paused.
          </p>
          <Radio
            name="net"
            value="any"
            label="Any network"
            sublabel="WiFi or mobile data, whichever is available."
            checked={settings.networkMode === NetworkMode.Any}
            onChange={() => persist({ ...settings, networkMode: NetworkMode.Any })}
            disabled={saving}
          />
          <Radio
            name="net"
            value="any_wifi"
            label="Only on WiFi"
            sublabel="Skip mobile data, even if WiFi is unavailable."
            checked={settings.networkMode === NetworkMode.AnyWifi}
            onChange={() => persist({ ...settings, networkMode: NetworkMode.AnyWifi })}
            disabled={saving}
          />
          <Radio
            name="net"
            value="trusted_wifi"
            label="Only on selected WiFi"
            sublabel="Sync only on the networks you list below — e.g. home and work, but not guest or public WiFi."
            checked={settings.networkMode === NetworkMode.TrustedWifi}
            onChange={() => {
              requestLocationPermissionIfNeeded();
              persist({ ...settings, networkMode: NetworkMode.TrustedWifi });
            }}
            disabled={saving}
          />
          {settings.networkMode === NetworkMode.TrustedWifi && (
            <>
              {locState !== 'always' && locState !== 'unknown' && (
                <LocationWarning
                  state={locState}
                  onFix={() => bridge()?.requestLocationPermission?.()}
                />
              )}
              <TrustedSSIDs
                ssids={settings.trustedSSIDs}
                onChange={(s) => persist({ ...settings, trustedSSIDs: s })}
                onAddCurrent={async () => {
                  try {
                    const st = await api.getPowerStatus();
                    const cur = (st.currentSSID ?? '').trim();
                    if (!cur) {
                      addToast("Couldn't read your WiFi name — is Location on?", 'warning');
                      return;
                    }
                    if (settings.trustedSSIDs.some((x) => x.toLowerCase() === cur.toLowerCase())) {
                      addToast(`"${cur}" is already in the list`, 'info');
                      return;
                    }
                    persist({ ...settings, trustedSSIDs: [...settings.trustedSSIDs, cur] });
                    addToast(`Added "${cur}"`, 'success');
                  } catch {
                    addToast("Couldn't read your WiFi name", 'warning');
                  }
                }}
                disabled={saving}
              />
            </>
          )}
        </div>
      </SettingRow>

      {/* The three battery/data guards share one shape (an on/off rule) and one
          concern (when *not* to sync), so they collapse into a single row
          rather than three full-height cards. */}
      <SettingRow title="Battery & data" summary={batterySummary(settings)}>
        <div className="flex flex-col divide-y divide-slate-100">
          <ToggleLine
            title="Pause when battery is low"
            description="Stop syncing once the battery drops to the level where your phone warns you it's low."
            checked={settings.pauseWhenBatteryLow}
            onChange={(v) => persist({ ...settings, pauseWhenBatteryLow: v })}
            disabled={saving}
          />
          <ToggleLine
            title="Don't sync on metered or roaming"
            description="Skip metered WiFi (like phone hotspots or tethering) and roaming abroad. Your normal mobile data isn't affected."
            checked={settings.blockMeteredRoaming}
            onChange={(v) => persist({ ...settings, blockMeteredRoaming: v })}
            disabled={saving}
          />
        </div>
      </SettingRow>

      <ActivityPanel />
    </div>
  );
}

// A labelled on/off setting. The battery/network switches are all this shape;
// they stack inside the "Battery & data" row separated by hairline dividers
// (so no per-switch Card wrapper — the row's card is the container).
function ToggleLine({
  title,
  description,
  checked,
  onChange,
  disabled,
}: {
  title: string;
  description: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <div className="py-3 first:pt-0 last:pb-0 flex items-center justify-between gap-3">
      <div>
        <p className="text-sm font-medium text-slate-800">{title}</p>
        <p className="text-xs text-slate-500 mt-0.5">{description}</p>
      </div>
      <input
        type="checkbox"
        className="w-5 h-5 flex-shrink-0"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
      />
    </div>
  );
}

// "What's happening right now" + the Sync now action. Polls the gate
// for live state. Hidden on desktop (status endpoint returns {}).
function NowPanel({
  settings,
  addToast,
}: {
  settings: PowerSettings;
  addToast: (m: string, kind?: 'success' | 'warning' | 'info' | 'error') => void;
}) {
  const [status, setStatus] = useState<PowerStatus | null>(null);

  const refresh = () => {
    api
      .getPowerStatus()
      .then(setStatus)
      .catch(() => setStatus(null));
  };

  useEffect(() => {
    refresh();
    const id = window.setInterval(refresh, 3000);
    return () => window.clearInterval(id);
  }, []);

  const syncNow = async () => {
    try {
      await api.powerSyncNow();
      addToast('Sync started', 'success');
      window.setTimeout(refresh, 500);
    } catch (e: unknown) {
      addToast(e instanceof Error ? e.message : 'Could not start sync', 'warning');
    }
  };

  if (!isAndroid()) return null;

  const lines = describeStatus(status, settings);

  return (
    <Card className="p-4 flex flex-col gap-3">
      <div className="flex items-start justify-between gap-3">
        <p className="text-sm font-semibold text-slate-800">Now</p>
        <AsyncButton
          unstyled
          onClick={syncNow}
          className="text-xs px-3 py-1.5 rounded-lg bg-blue-600 text-white"
        >
          Sync now
        </AsyncButton>
      </div>
      <div className="flex flex-col gap-1.5">
        {lines.length === 0 && <p className="text-xs text-slate-400">Checking…</p>}
        {lines.map((l, i) => (
          <div key={i} className="flex items-baseline gap-2 text-xs">
            <span className={`flex-shrink-0 ${l.good ? 'text-emerald-500' : 'text-amber-500'}`}>
              {l.good ? '✓' : '•'}
            </span>
            <span className="text-slate-700">{l.text}</span>
          </div>
        ))}
      </div>
    </Card>
  );
}

// Collapsed by default — most users don't need to look at this, but
// power users / debuggers can expand to see the full audit trail.
function ActivityPanel() {
  const [events, setEvents] = useState<PowerEvent[] | null>(null);
  const [expanded, setExpanded] = useState(false);

  const refresh = () => {
    api
      .getPowerEvents(50)
      .then(setEvents)
      .catch(() => {
        /* desktop is empty */
      });
  };

  useEffect(() => {
    if (!expanded) return;
    refresh();
    const id = window.setInterval(refresh, 5000);
    return () => window.clearInterval(id);
  }, [expanded]);

  return (
    <Card className="p-4 flex flex-col gap-2">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center justify-between text-left"
      >
        <div>
          <p className="text-sm font-semibold text-slate-800">Recent activity</p>
          <p className="text-xs text-slate-500 mt-0.5">
            What WeSync did recently, including while the app was closed.
          </p>
        </div>
        <span className="text-slate-400 text-sm">{expanded ? '▾' : '▸'}</span>
      </button>
      {expanded && events && events.length === 0 && (
        <p className="text-xs text-slate-400">
          No events yet — they'll appear here after the first sync.
        </p>
      )}
      {expanded && events && events.length > 0 && (
        <div className="flex flex-col gap-1 max-h-72 overflow-y-auto">
          {events.map((e) => (
            <ActivityRow key={e.id} event={e} />
          ))}
        </div>
      )}
    </Card>
  );
}

function ActivityRow({ event }: { event: PowerEvent }) {
  const when = new Date(event.timestamp).toLocaleString();
  const color = kindColor(event.kind);
  return (
    <div className="flex items-baseline gap-2 text-xs">
      <span className="font-mono text-slate-400 flex-shrink-0">{when}</span>
      <span className={`font-medium ${color}`}>{event.kind}</span>
      <span className="text-slate-600">{event.message}</span>
    </div>
  );
}

function kindColor(kind: string): string {
  if (kind.startsWith('error')) return 'text-rose-600';
  if (kind.startsWith('st_start') || kind === 'folders_unpaused') return 'text-emerald-600';
  if (kind.startsWith('st_stop') || kind === 'folders_paused') return 'text-slate-500';
  if (kind === 'trigger') return 'text-blue-600';
  return 'text-slate-600';
}

function Radio({
  name,
  value,
  label,
  sublabel,
  checked,
  onChange,
  disabled,
}: {
  name: string;
  value: string;
  label: string;
  sublabel: string;
  checked: boolean;
  onChange: () => void;
  disabled?: boolean;
}) {
  return (
    <label
      className={`flex items-start gap-3 px-2 py-2 rounded-lg cursor-pointer ${checked ? 'bg-emerald-50' : 'hover:bg-slate-50'} ${disabled ? 'opacity-60 cursor-wait' : ''}`}
    >
      <input
        type="radio"
        name={name}
        value={value}
        checked={checked}
        onChange={onChange}
        disabled={disabled}
        className="mt-1"
      />
      <div className="flex-1">
        <p className="text-sm font-medium text-slate-800">{label}</p>
        <p className="text-xs text-slate-500">{sublabel}</p>
      </div>
    </label>
  );
}

function ScheduledTimes({
  times,
  onChange,
  disabled,
}: {
  times: string[];
  onChange: (t: string[]) => void;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState('');

  const add = () => {
    const v = draft.trim();
    if (!/^\d{1,2}:\d{2}$/.test(v)) return;
    const [h, m] = v.split(':').map((n) => parseInt(n, 10));
    if (h > 23 || m > 59) return;
    const formatted = `${h.toString().padStart(2, '0')}:${m.toString().padStart(2, '0')}`;
    if (times.includes(formatted)) {
      setDraft('');
      return;
    }
    onChange([...times, formatted].sort());
    setDraft('');
  };

  return (
    <div className="ml-7 flex flex-col gap-2">
      {times.length === 0 && (
        <p className="text-xs text-slate-400">No times set yet — add one below.</p>
      )}
      {times.map((t) => (
        <div key={t} className="flex items-center gap-2">
          <span className="font-mono text-sm">{t}</span>
          <button
            type="button"
            className="text-xs text-rose-500 hover:underline"
            onClick={() => onChange(times.filter((x) => x !== t))}
            disabled={disabled}
          >
            Remove
          </button>
        </div>
      ))}
      <div className="flex items-center gap-2">
        <input
          type="time"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="text-sm border border-slate-200 rounded-lg px-2 py-1"
          disabled={disabled}
        />
        <button
          type="button"
          onClick={add}
          className="text-xs px-3 py-1.5 rounded-lg bg-blue-600 text-white disabled:opacity-50"
          disabled={disabled || !draft}
        >
          Add
        </button>
      </div>
    </div>
  );
}

// Surfaced when "my own WiFi" is active but location isn't granted "all the
// time". Without background location Android hands a closed app a blank SSID,
// so the gate silently can't tell which network you're on — sync just never
// starts in the background. In the foreground the SSID reads fine, which is
// exactly why this needs an explicit warning: the status panel looks healthy
// while the background path is broken.
function LocationWarning({ state, onFix }: { state: LocState; onFix: () => void }) {
  const foreground = state === 'foreground';
  return (
    <div className="ml-7 rounded-lg border border-amber-300 bg-amber-50 p-3 flex flex-col gap-2">
      <p className="text-xs text-amber-800">
        {foreground ? (
          <>
            Location is allowed only <strong>“While using the app”</strong>. Background sync can’t
            read your WiFi name, so it won’t start while WeSync is closed. Set location to{' '}
            <strong>“Allow all the time”</strong>.
          </>
        ) : (
          <>
            This mode needs <strong>location permission</strong> to read your WiFi name (Android
            requirement). Grant it, then choose <strong>“Allow all the time”</strong>.
          </>
        )}
      </p>
      <p className="text-[11px] text-amber-700">
        Also make sure the device’s Location is switched on — the WiFi name reads blank otherwise.
      </p>
      <button
        type="button"
        onClick={onFix}
        className="self-start text-xs px-3 py-1.5 rounded-lg bg-amber-600 text-white"
      >
        {foreground ? 'Set to “Allow all the time”' : 'Grant location'}
      </button>
    </div>
  );
}

function TrustedSSIDs({
  ssids,
  onChange,
  onAddCurrent,
  disabled,
}: {
  ssids: string[];
  onChange: (s: string[]) => void;
  onAddCurrent: () => void;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState('');

  const add = () => {
    const v = draft.trim();
    // Match the gate's case-insensitive comparison (strings.EqualFold), so
    // "Home" and "home" aren't accepted as two separate entries.
    if (!v || ssids.some((x) => x.toLowerCase() === v.toLowerCase())) return;
    onChange([...ssids, v]);
    setDraft('');
  };

  return (
    <div className="ml-7 flex flex-col gap-2">
      {ssids.length === 0 && (
        <p className="text-xs text-amber-600">
          No networks added yet — sync will stay paused until you list at least one.
        </p>
      )}
      {ssids.map((s) => (
        <div key={s} className="flex items-center gap-2">
          <span className="text-sm">{s}</span>
          <button
            type="button"
            className="text-xs text-rose-500 hover:underline"
            onClick={() => onChange(ssids.filter((x) => x !== s))}
            disabled={disabled}
          >
            Remove
          </button>
        </div>
      ))}
      {/* The easy, correct path: grab whatever network you're on right now —
          no typing, exact string, and it proves the SSID can actually be read. */}
      <button
        type="button"
        onClick={onAddCurrent}
        className="self-start text-xs px-3 py-1.5 rounded-lg border border-blue-600 text-blue-600 disabled:opacity-50"
        disabled={disabled}
      >
        + Add current network
      </button>
      <div className="flex items-center gap-2">
        <input
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="…or type a network name (SSID)"
          className="flex-1 text-sm border border-slate-200 rounded-lg px-2 py-1"
          disabled={disabled}
        />
        <button
          type="button"
          onClick={add}
          className="text-xs px-3 py-1.5 rounded-lg bg-blue-600 text-white disabled:opacity-50"
          disabled={disabled || !draft.trim()}
        >
          Add
        </button>
      </div>
      <p className="text-xs text-slate-400">
        Capitalisation doesn&apos;t matter — &ldquo;Home&rdquo; and &ldquo;home&rdquo; are treated
        as the same network.
      </p>
    </div>
  );
}
