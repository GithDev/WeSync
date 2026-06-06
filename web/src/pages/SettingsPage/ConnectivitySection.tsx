import { useState, useEffect } from 'react';
import {
  api,
  type RelayStatus,
  type DiscoveryStatus,
  type ConnectivityStatus,
} from '../../api/client';
import { useToast } from '../../components/base/Toast/Toast';
import { Card } from '../../components/base/Card/Card';
import { SettingRow } from '../../components/base/SettingRow/SettingRow';
import { StatusLine, type StatusTone } from '../../components/base/StatusLine/StatusLine';
import { isAndroid } from '../../platform';

// Connectivity governs *how far* your devices reach each other (and what their
// traffic may pass through). Distinct from PowerSection's "when & where WeSync
// runs". Three cumulative levels — each does everything below it, plus more.

interface Level {
  level: number;
  title: string;
  tagline: string;
  privacyLabel: string;
}

const LEVELS: Level[] = [
  {
    level: 1,
    title: 'Local network only',
    tagline:
      'Like a Chromecast or printer on your network — your devices find each other when they share a router.',
    privacyLabel: 'Highest privacy',
  },
  {
    level: 2,
    title: 'Across the internet',
    tagline:
      'Everything in Local network, plus your devices connect directly over the internet. To find each other, they announce themselves to an external service, and may ask your router to open a port so they can be reached.',
    privacyLabel: 'Some metadata exposed',
  },
  {
    level: 3,
    title: 'Relay when blocked',
    tagline: 'Everything above. A relay server steps in only when a direct connection is blocked.',
    privacyLabel: 'Passes through a third party',
  },
];

interface Property {
  text: string;
  levels: number[];
}

// Additive — `levels` lists every level that HAS each capability, so the same
// capability shows a green check on every level that includes it and a neutral
// dash on the levels below. The cumulative nature reads at a glance: each level
// does everything below it, plus more. The privacy cost of reaching further
// lives in each card's privacyLabel, not this grid (one concept per surface).
const PROPERTIES: Property[] = [
  { text: 'Finds devices on the local network', levels: [1, 2, 3] },
  { text: 'Connects directly, device to device', levels: [1, 2, 3] },
  { text: 'Also reaches devices over the internet', levels: [2, 3] },
  { text: 'Falls back to a relay only when a direct link is blocked', levels: [3] },
];

// Per-level palette. Each level carries its own hue at rest so the choices read
// as three distinct options, not warnings. Selected card deepens to the same hue.
// Cool progression (emerald → sky → indigo) reads as "scope expands", not "risk rises".
const PALETTE: Record<
  number,
  {
    rest: string;
    active: string;
    ringRest: string;
    ringActive: string;
    fill: string;
    title: string;
    chipBorder: string;
    chipText: string;
  }
> = {
  1: {
    rest: 'border-emerald-200 bg-emerald-50 hover:border-emerald-300',
    active: 'border-emerald-400 bg-emerald-100',
    ringRest: 'border-emerald-300',
    ringActive: 'border-emerald-500',
    fill: 'bg-emerald-500',
    title: 'text-emerald-900',
    chipBorder: 'border-emerald-200',
    chipText: 'text-emerald-700',
  },
  2: {
    rest: 'border-sky-200 bg-sky-50 hover:border-sky-300',
    active: 'border-sky-400 bg-sky-100',
    ringRest: 'border-sky-300',
    ringActive: 'border-sky-500',
    fill: 'bg-sky-500',
    title: 'text-sky-900',
    chipBorder: 'border-sky-200',
    chipText: 'text-sky-700',
  },
  3: {
    rest: 'border-indigo-200 bg-indigo-50 hover:border-indigo-300',
    active: 'border-indigo-400 bg-indigo-100',
    ringRest: 'border-indigo-300',
    ringActive: 'border-indigo-500',
    fill: 'bg-indigo-500',
    title: 'text-indigo-900',
    chipBorder: 'border-indigo-200',
    chipText: 'text-indigo-700',
  },
};

// Maps the relay subsystem's status to a StatusLine. The label names the
// subsystem ("Relay") and its state so the row is self-explanatory; the relay://
// address or ST's listener error goes in the detail line. Relay takes a few
// seconds to dial, so a null/empty-error reading reads as "connecting" rather
// than failure — only a real listener error turns it rose.
function relayTone(relay: RelayStatus | null): {
  tone: StatusTone;
  label: string;
  detail?: string;
} {
  if (!relay) return { tone: 'neutral', label: 'Relay: checking…' };
  if (relay.live) return { tone: 'ok', label: 'Relay: connected', detail: relay.address };
  if (relay.error) return { tone: 'error', label: 'Relay: unreachable', detail: relay.error };
  return { tone: 'pending', label: 'Relay: connecting…' };
}

// Live connection status, lifted OUT of the level-picker cards so those stay
// pure choices (one concept per surface). Reflects the active level: global
// discovery at levels 2-3, relay additionally at level 3. The caller renders it
// only when there's external status to show (level >= 2).
function ConnectionStatus({
  current,
  discovery,
  relay,
}: {
  current: number;
  discovery: DiscoveryStatus | null;
  relay: RelayStatus | null;
}) {
  const disc = discoveryTone(discovery);
  const rly = relayTone(relay);
  return (
    <Card className="px-4 py-3 flex flex-col gap-2.5">
      <span className="text-[10px] font-medium uppercase tracking-wide text-slate-400">
        Connection status
      </span>
      <StatusLine tone={disc.tone} label={disc.label} detail={disc.detail} />
      {current === 3 && (
        <StatusLine
          tone={rly.tone}
          label={rly.label}
          detail={rly.detail}
          className="pt-2.5 border-t border-slate-100"
        />
      )}
    </Card>
  );
}

// Maps the global-discovery subsystem's status to a StatusLine. The label names
// the subsystem ("Global discovery") and its state, with the reachable-server
// count packed in: "announced" means this device is registered with the global
// discovery servers, so peers can look it up by device ID. Live uses the same
// steady green (ok) as relay so "green = this channel has contact" reads
// consistently across both rows; ST's error (e.g. a 5xx from a discovery server)
// goes in the detail line. Before the first successful announce it reads as
// "announcing".
function discoveryTone(d: DiscoveryStatus | null): {
  tone: StatusTone;
  label: string;
  detail?: string;
} {
  if (!d) return { tone: 'neutral', label: 'Global discovery: checking…' };
  if (d.live)
    return {
      tone: 'ok',
      label: `Global discovery: announced (${d.ok}/${d.servers} servers)`,
    };
  if (d.error) return { tone: 'error', label: 'Global discovery: unreachable', detail: d.error };
  return { tone: 'pending', label: 'Global discovery: announcing…' };
}

export function ConnectivitySection() {
  const { addToast } = useToast();
  const [current, setCurrent] = useState(1);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<ConnectivityStatus | null>(null);

  useEffect(() => {
    api
      .getConnectivityLevel()
      .then((r) => setCurrent(r.level))
      .catch(() => {});
  }, []);

  // Relay (level 3) and global discovery (levels 2-3) both live in ST's system
  // status. Poll the one combined endpoint while either is in play so the status
  // card reflects whether the device is actually announcing / relaying.
  useEffect(() => {
    if (current < 2) {
      setStatus(null);
      return;
    }
    let cancelled = false;
    const poll = () =>
      api
        .getConnectivityStatus()
        .then((s) => !cancelled && setStatus(s))
        .catch(() => {});
    poll();
    const id = setInterval(poll, 3000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [current]);

  const save = async (level: number) => {
    setSaving(true);
    try {
      await api.setConnectivityLevel(level);
      setCurrent(level);
      addToast('Connectivity level updated', 'success');
    } catch (e: unknown) {
      addToast(e instanceof Error ? e.message : 'Could not update', 'warning');
    } finally {
      setSaving(false);
    }
  };

  const currentTitle = LEVELS.find((l) => l.level === current)?.title ?? `Level ${current}`;

  return (
    <div className="flex flex-col gap-3">
      {/* Live status sits above the picker — "is my reach actually working?" —
          and only when there's something external to report (level >= 2). */}
      {current >= 2 && (
        <ConnectionStatus
          current={current}
          discovery={status?.discovery ?? null}
          relay={status?.relay ?? null}
        />
      )}

      {/* Collapsed to a single scannable row showing the current level. On
          platforms with no Power section (desktop/Windows) Connectivity is the
          only setting on the page, so open it by default rather than make the
          user expand the one thing that's there. */}
      <SettingRow title="Connectivity" summary={currentTitle} defaultOpen={!isAndroid()}>
        <div className="flex flex-col gap-3">
          <p className="text-xs text-slate-400">
            Controls how your devices find each other and how far your reach extends.
          </p>

          {/* A reassurance note, not a card — inline shield + muted text, no box,
              so it doesn't read as a white card buried in a white card. */}
          <div className="flex items-start gap-2.5">
            <svg
              className="w-4 h-4 mt-0.5 flex-shrink-0 text-emerald-500"
              viewBox="0 0 20 20"
              fill="currentColor"
              aria-hidden
            >
              <path
                fillRule="evenodd"
                clipRule="evenodd"
                d="M2.166 4.999A11.954 11.954 0 0010 1.944 11.954 11.954 0 0017.834 5c.11.65.166 1.32.166 2.001 0 5.225-3.34 9.67-8 11.317C5.34 16.67 2 12.225 2 7c0-.682.057-1.35.166-2.001zm11.541 3.708a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
              />
            </svg>
            <p className="text-xs text-slate-500 leading-snug">
              Your files always stay on your devices, end-to-end encrypted — only you can read them.
              No accounts, no central server, no telemetry. The choices below change how your
              devices find each other, and what their traffic may pass through along the way.
            </p>
          </div>

          {/* Hairline separates the explanation from the actual choice. */}
          <div className="border-t border-slate-100" />

          {LEVELS.map((l) => {
            const active = current === l.level;
            const c = PALETTE[l.level];
            return (
              <button
                key={l.level}
                type="button"
                onClick={() => !saving && save(l.level)}
                disabled={saving}
                className={`text-left rounded-2xl border px-5 py-4 transition-all ${active ? c.active : c.rest} ${saving ? 'opacity-60 cursor-wait' : 'cursor-pointer'}`}
              >
                <div className="flex items-center justify-between gap-3 mb-1.5">
                  <div className="flex items-center gap-2.5">
                    <span
                      className={`w-4 h-4 rounded-full border-2 flex-shrink-0 flex items-center justify-center ${active ? c.ringActive : c.ringRest}`}
                    >
                      {active && <span className={`w-2 h-2 rounded-full ${c.fill}`} />}
                    </span>
                    <span
                      className={`text-sm font-semibold ${active ? c.title : 'text-slate-800'}`}
                    >
                      {l.title}
                    </span>
                  </div>
                  <span
                    className={`text-[10px] font-medium ${c.chipText} bg-white border ${c.chipBorder} rounded-full px-2 py-0.5 flex-shrink-0`}
                  >
                    {l.privacyLabel}
                  </span>
                </div>

                <p className="text-xs text-slate-500 ml-6.5 mb-2.5">{l.tagline}</p>

                {/* Every card shows the full capability list so each level's
                    pros and cons are visible before you pick it: a green check
                    for what the level includes, a neutral dash for what it
                    doesn't. Shown on all cards, not just the selected one. */}
                <ul className="ml-6.5 space-y-1.5">
                  {PROPERTIES.map((p) => {
                    const has = p.levels.includes(l.level);
                    return (
                      <li
                        key={p.text}
                        className={`text-xs flex items-start gap-2 ${has ? 'text-slate-700' : 'text-slate-500'}`}
                      >
                        {has ? (
                          <svg
                            className="w-4 h-4 mt-px flex-shrink-0 text-emerald-500"
                            viewBox="0 0 20 20"
                            aria-hidden
                          >
                            <circle cx="10" cy="10" r="10" fill="currentColor" />
                            <path
                              d="M6 10.5l2.5 2.5 5.5-6"
                              stroke="white"
                              strokeWidth="2"
                              fill="none"
                              strokeLinecap="round"
                              strokeLinejoin="round"
                            />
                          </svg>
                        ) : (
                          // Neutral "not at this level" — NOT a red X. With the
                          // additive grid a lower level legitimately just hasn't
                          // added this capability yet; it isn't a failure.
                          <svg
                            className="w-4 h-4 mt-px flex-shrink-0 text-slate-300"
                            viewBox="0 0 20 20"
                            aria-hidden
                          >
                            <circle cx="10" cy="10" r="10" fill="currentColor" />
                            <path
                              d="M6 10h8"
                              stroke="white"
                              strokeWidth="2"
                              fill="none"
                              strokeLinecap="round"
                            />
                          </svg>
                        )}
                        <span className="leading-snug">{p.text}</span>
                      </li>
                    );
                  })}
                </ul>
              </button>
            );
          })}
        </div>
      </SettingRow>
    </div>
  );
}
