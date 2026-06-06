import { useWS } from '../../api/websocket';
import { api } from '../../api/client';
import { AsyncButton } from '../base/Button/AsyncButton';

/** Global visibility control — a single button under the nav, on every page.
 *  You're discoverable by default; the button flips in place: blue "Discoverable"
 *  ⇄ muted "Not discoverable". So you can go hidden even before trusting anyone
 *  (e.g. on public WiFi). Listening is unaffected; this only toggles announce. */
export function VisibilityBar() {
  const { visible } = useWS();
  if (visible === null) return null; // wait for the first WS snapshot

  // No optimistic local copy on purpose: the server owns this flag and AsyncButton
  // already shows an in-flight spinner, so we render the single source of truth (the
  // WS broadcast) and let it flip the label when the round-trip lands. A second,
  // local copy would just be another source that can diverge from the server on
  // out-of-order / rapid toggles.
  const dot = (
    <span
      className={`w-2 h-2 rounded-full flex-shrink-0 ${visible ? 'bg-white/90 animate-pulse' : 'bg-slate-400'}`}
    />
  );
  return (
    <div className="bg-white border-b border-slate-200 px-4 sm:px-6 py-2 flex justify-center">
      <AsyncButton
        unstyled
        icon={dot}
        onClick={() => api.setMode(!visible)}
        title={
          visible
            ? 'Other WeSync devices nearby can find you. Tap to hide.'
            : "Hidden — nearby devices can't find you. Tap to become discoverable."
        }
        className={`px-3 py-1.5 rounded-full text-sm ${
          visible
            ? 'bg-blue-500 text-white hover:bg-blue-600'
            : 'bg-slate-100 text-slate-500 hover:bg-slate-200'
        }`}
      >
        {visible ? 'Discoverable' : 'Not discoverable'}
      </AsyncButton>
    </div>
  );
}
