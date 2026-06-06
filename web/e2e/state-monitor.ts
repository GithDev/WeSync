/**
 * StateMonitor — parallel state sampler for E2E tests.
 *
 * Polls all WeSync instances every POLL_MS, records every state change with a
 * relative timestamp, and prints a human-readable timeline after the test.
 *
 * Detects:
 *   - Flicker: a boolean field that goes true→false→true (or vice versa)
 *     within FLICKER_WINDOW_MS — signals transient UI glitches.
 *   - Asymmetry: device A knows about X but device B does not, when the
 *     state should be symmetric (e.g. both sides of a pairing).
 *
 * Usage:
 *   const mon = new StateMonitor([[pageA,'A'], [pageB,'B'], [pageC,'C']]);
 *   mon.start();
 *   // ... do test actions ...
 *   mon.stop();           // prints timeline automatically
 *   mon.assertNoFlicker(); // throws if flicker detected
 */

import type { Page } from '@playwright/test';

const POLL_MS = 500;
const FLICKER_WINDOW_MS = 2_000;

// ── Types ─────────────────────────────────────────────────────────────────────

interface DeviceSnap {
  deviceID: string;
  name: string;
  connected: boolean;
  accepted: boolean;
  stPaired: boolean;
}

interface FolderSnap {
  id: string;
  label: string;
  deviceIDs: string[];
  deviceAccepted: Record<string, boolean>;
}

interface IncomingSnap {
  deviceID: string;
  name: string;
}

interface Snap {
  ts: number; // ms from monitor start
  label: string;
  base: string;
  devices: DeviceSnap[];
  folders: FolderSnap[];
  incoming: IncomingSnap[];
}

interface FlickerEvent {
  label: string;
  field: string;
  ts: number;
  detail: string;
}

// ── StateMonitor ──────────────────────────────────────────────────────────────

export class StateMonitor {
  private pages: [Page, string, string][]; // [page, base, label]
  private snaps: Snap[] = [];
  private startTs: number = 0;
  private timer?: ReturnType<typeof setInterval>;
  private running = false;

  constructor(pages: [Page, string, string][]) {
    this.pages = pages;
  }

  start(): void {
    this.startTs = Date.now();
    this.snaps = [];
    this.running = true;
    void this.poll();
    this.timer = setInterval(() => {
      if (this.running) void this.poll();
    }, POLL_MS);
  }

  stop(): void {
    this.running = false;
    if (this.timer) clearInterval(this.timer);
    this.printTimeline();
    this.reportAsymmetry();
  }

  // ── Polling ──────────────────────────────────────────────────────────────

  private async poll(): Promise<void> {
    const ts = Date.now() - this.startTs;
    await Promise.all(
      this.pages.map(async ([page, base, label]) => {
        try {
          const [devRes, folRes, incRes] = await Promise.all([
            page.request.get(`${base}/api/devices`),
            page.request.get(`${base}/api/folders`),
            page.request.get(`${base}/api/incoming`),
          ]);
          const [devices, folders, incoming] = await Promise.all([
            devRes.ok() ? devRes.json() : [],
            folRes.ok() ? folRes.json() : [],
            incRes.ok() ? incRes.json() : [],
          ]);
          this.snaps.push({ ts, label, base, devices, folders, incoming });
        } catch {
          /* instance may be restarting */
        }
      }),
    );
  }

  // ── Timeline ─────────────────────────────────────────────────────────────

  printTimeline(): void {
    if (this.snaps.length === 0) return;

    // Per device: track the last serialised state so we only print changes
    const prev = new Map<string, string>();
    const lines: string[] = [];

    for (const s of this.snaps) {
      const cur = this.serialise(s);
      if (prev.get(s.label) === cur) continue;
      prev.set(s.label, cur);

      const tag = `[+${String(s.ts).padStart(6)}ms] ${s.label}`;
      lines.push(`${tag}: ${cur}`);

      // Mark obvious flicker inline
      const prev2 = prev.get(s.label + '__prev');
      if (prev2 && prev2 !== cur) {
        const revert = this.snaps
          .filter((x) => x.label === s.label && x.ts > s.ts && x.ts < s.ts + FLICKER_WINDOW_MS)
          .find((x) => this.serialise(x) === prev2);
        if (revert) {
          lines[lines.length - 1] += '  ⚡ FLICKER';
        }
      }
      prev.set(s.label + '__prev', cur);
    }

    if (lines.length === 0) {
      console.log('\n── State timeline: no changes detected ──\n');
      return;
    }
    console.log('\n── State timeline ──────────────────────────────────────────');
    lines.forEach((l) => console.log(l));
    console.log('────────────────────────────────────────────────────────────\n');
  }

  private serialise(s: Snap): string {
    const devs = (s.devices ?? [])
      .map((d) => `${d.name}(${d.connected ? '●' : '○'}${d.accepted ? '✓' : '?'})`)
      .sort()
      .join(' ');

    const fols = (s.folders ?? [])
      .map((f) => {
        const acc = (f.deviceIDs ?? [])
          .map((id) => (f.deviceAccepted?.[id] ? id.slice(0, 7) + '✓' : id.slice(0, 7) + '?'))
          .sort()
          .join(',');
        return `${f.label}[${acc}]`;
      })
      .sort()
      .join(' ');

    const inc = (s.incoming ?? [])
      .map((i) => `!${i.name}`)
      .sort()
      .join(' ');

    return [devs || '—', fols || '—', inc || ''].filter(Boolean).join(' | ');
  }

  // ── Flicker detection ─────────────────────────────────────────────────────

  detectFlicker(): FlickerEvent[] {
    const events: FlickerEvent[] = [];

    for (const label of new Set(this.snaps.map((s) => s.label))) {
      const mine = this.snaps.filter((s) => s.label === label);

      // Check deviceAccepted flicker per folder per device
      for (let i = 1; i < mine.length; i++) {
        const prev = mine[i - 1];
        const curr = mine[i];
        if (curr.ts - prev.ts > FLICKER_WINDOW_MS) continue;

        for (const cf of curr.folders ?? []) {
          const pf = (prev.folders ?? []).find((f) => f.id === cf.id);
          if (!pf) continue;
          for (const did of cf.deviceIDs) {
            const wasAcc = pf.deviceAccepted?.[did] ?? false;
            const isAcc = cf.deviceAccepted?.[did] ?? false;
            if (wasAcc && !isAcc) {
              // Check if it recovers quickly
              const recovery = mine
                .slice(i + 1)
                .find(
                  (s) =>
                    s.ts < curr.ts + FLICKER_WINDOW_MS &&
                    s.folders.find((f) => f.id === cf.id)?.deviceAccepted?.[did],
                );
              if (recovery) {
                events.push({
                  label,
                  field: `folders[${cf.label}].deviceAccepted[${did.slice(0, 7)}]`,
                  ts: curr.ts,
                  detail: `true→false→true within ${recovery.ts - prev.ts}ms`,
                });
              }
            }
          }
        }
      }
    }
    return events;
  }

  assertNoFlicker(): void {
    const flickers = this.detectFlicker();
    if (flickers.length > 0) {
      const msg = flickers
        .map((f) => `  ⚡ ${f.label} ${f.field} at +${f.ts}ms: ${f.detail}`)
        .join('\n');
      throw new Error(`Flicker detected:\n${msg}`);
    }
  }

  // ── Asymmetry detection ───────────────────────────────────────────────────

  /**
   * Finds moments where the trusted-device state is asymmetric:
   *   - A has B in its device list but B does not have A
   *   - A's folder lists B as a participant but B's folder doesn't list A
   *
   * Snapshots are bucketed by poll interval so we compare near-simultaneous reads.
   */
  detectAsymmetry(): { ts: number; detail: string }[] {
    const events: { ts: number; detail: string }[] = [];

    // Build a map from deviceID → monitor label for quick lookup
    const idToLabel = new Map<string, string>();
    for (const [, , label] of this.pages) {
      const first = this.snaps.find((s) => s.label === label);
      if (first) {
        for (const d of first.devices ?? []) idToLabel.set(d.deviceID, label);
      }
    }
    // Also capture self-IDs from status (devices often know their own ID via peerState)
    // We approximate: the first deviceID that appears on page X but not on other pages' own lists
    // is X's own ID. A simpler approach: pages[i] corresponds to DEVICE_x, so index = label.

    // Group snaps into buckets (POLL_MS width) so we compare near-simultaneous reads
    const buckets = new Map<number, Map<string, Snap>>();
    for (const s of this.snaps) {
      const bucket = Math.floor(s.ts / POLL_MS) * POLL_MS;
      if (!buckets.has(bucket)) buckets.set(bucket, new Map());
      buckets.get(bucket)!.set(s.label, s);
    }

    const seen = new Set<string>(); // dedup identical events

    for (const [ts, group] of buckets) {
      for (const [labelA, snapA] of group) {
        for (const devA of snapA.devices ?? []) {
          if (!devA.stPaired) continue;
          // Find which monitor label corresponds to devA's deviceID
          const labelB = idToLabel.get(devA.deviceID);
          if (!labelB || labelB === labelA) continue;
          const snapB = group.get(labelB);
          if (!snapB) continue;

          // Check: A has B in devices — does B have A in its devices?
          // We need A's own deviceID. Find it via snapB's devices listing labelA's device.
          const aOnB = snapB.devices.find(
            (d) => idToLabel.get(d.deviceID) === labelA || !idToLabel.has(d.deviceID),
          );
          if (!aOnB) {
            const key = `dev:${ts}:${labelA}→${labelB}`;
            if (!seen.has(key)) {
              seen.add(key);
              events.push({
                ts,
                detail: `${labelA} has ${labelB} as trusted device, but ${labelB} does not have ${labelA}`,
              });
            }
          }
        }

        // Check folder device list asymmetry
        for (const folderA of snapA.folders ?? []) {
          for (const did of folderA.deviceIDs) {
            const labelB = idToLabel.get(did);
            if (!labelB || labelB === labelA) continue;
            const snapB = group.get(labelB);
            if (!snapB) continue;
            // Find the same folder on B
            const folderB = snapB.folders.find((f) => f.id === folderA.id);
            if (!folderB) continue;
            // A's folder lists B — does B's folder list A?
            // A's deviceID is not directly available, approximate: any ID in B's folder that
            // maps to labelA
            const aRepresentedInB = folderB.deviceIDs.some((id) => idToLabel.get(id) === labelA);
            if (!aRepresentedInB) {
              const key = `folder:${ts}:${labelA}→${labelB}:${folderA.id}`;
              if (!seen.has(key)) {
                seen.add(key);
                events.push({
                  ts,
                  detail: `folder "${folderA.label}": ${labelA} lists ${labelB} as participant, but ${labelB}'s copy doesn't list ${labelA}`,
                });
              }
            }
          }
        }
      }
    }

    return events;
  }

  /** Prints any detected asymmetries to console. */
  reportAsymmetry(): void {
    const events = this.detectAsymmetry();
    if (events.length === 0) return;
    console.log('\n── Asymmetry detected ───────────────────────────────────────');
    events.slice(0, 10).forEach((e) => console.log(`  [+${e.ts}ms] ⚠ ${e.detail}`));
    if (events.length > 10) console.log(`  … and ${events.length - 10} more`);
    console.log('─────────────────────────────────────────────────────────────\n');
  }
}
