import { useEffect, useState } from 'react';
import { ConnectivitySection } from './ConnectivitySection';
import { PowerSection } from './PowerSection';

// The Settings page is a thin shell that stacks the self-contained sections.
// Each section owns its own state, data fetching and platform gating
// (PowerSection hides itself off Android).
export function SettingsPage() {
  // Build stamp from the backend (-ldflags -X wesync/internal/api.BuildTime).
  // Direct fetch (not the api client) keeps this self-contained. Shown muted at
  // the bottom so you can confirm at a glance which build a device is running.
  const [build, setBuild] = useState('');
  useEffect(() => {
    fetch('/api/status')
      .then((r) => r.json())
      .then((d: { buildTime?: string }) => setBuild(d.buildTime ?? ''))
      .catch(() => {});
  }, []);

  return (
    <div className="max-w-xl mx-auto w-full px-4 py-6 sm:px-6 sm:py-8 flex flex-col gap-6">
      <div>
        <h1 className="text-lg font-bold text-slate-900">Settings</h1>
        <p className="text-xs text-slate-400 mt-0.5">Global configuration for WeSync</p>
      </div>

      <ConnectivitySection />
      <PowerSection />

      {build && (
        <p className="text-center text-[11px] font-mono text-slate-300 mt-2 select-all">
          build {build}
        </p>
      )}
    </div>
  );
}
