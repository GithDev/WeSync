import { useState, useCallback } from 'react';
import { api } from '../api/client';

interface FallbackState {
  resolve: (path: string | null) => void;
  path: string;
}

// Calls the Android JsBridge.pickFolder, which launches the system SAF
// picker asynchronously. The bridge can't return a value directly (the user
// might spend seconds in the picker), so we hand it a callback id and wait
// for `window.__weSyncPickResult(id, path)` to fire from Kotlin.
function pickViaAndroidBridge(): Promise<string | null> {
  return new Promise((resolve) => {
    const w = window as unknown as {
      WeSync?: { pickFolder: (id: string) => void };
      __weSyncPickResolvers?: Record<string, (path: string | null) => void>;
      __weSyncPickResult?: (id: string, path: string | null) => void;
    };
    if (!w.__weSyncPickResolvers) {
      w.__weSyncPickResolvers = {};
      w.__weSyncPickResult = (id, path) => {
        const r = w.__weSyncPickResolvers?.[id];
        if (!r) return;
        delete w.__weSyncPickResolvers![id];
        r(path || null);
      };
    }
    const id = `pick_${Date.now()}_${Math.floor(Math.random() * 1e6)}`;
    w.__weSyncPickResolvers[id] = resolve;
    w.WeSync!.pickFolder(id);
  });
}

/**
 * Returns a `pick()` function and a `modal` element to render.
 *
 * `pick()` tries the native OS folder picker first. If it fails (Linux
 * headless, WSL, no zenity/kdialog), a modal appears asking for a
 * manual path instead — so the app works everywhere.
 */
export function usePickFolder() {
  const [fallback, setFallback] = useState<FallbackState | null>(null);

  const pick = useCallback(async (): Promise<string | null> => {
    // 1. Android SAF picker — when running inside our WebView wrapper,
    //    `window.WeSync.pickFolder` is the only native picker available
    //    (Android 11+ scoped storage blocks every other route).
    const androidBridge = (window as unknown as { WeSync?: { pickFolder?: unknown } }).WeSync
      ?.pickFolder;
    if (typeof androidBridge === 'function') {
      const path = await pickViaAndroidBridge();
      if (path !== null) return path;
      // null means user cancelled OR picked a non-primary-storage tree;
      // fall through to manual-input so they still have an escape hatch.
      return new Promise((resolve) => setFallback({ resolve, path: '' }));
    }

    // 2. Wails native picker — available when running inside wesync-app.exe.
    //    The Wails bridge is injected into every document the WebView loads,
    //    so window.go is available even after navigating to localhost:47820.
    const wailsPick = (
      window as unknown as {
        go?: { main?: { App?: { PickFolder?: () => Promise<string> } } };
      }
    ).go?.main?.App?.PickFolder;
    if (wailsPick) {
      try {
        const path: string = await wailsPick();
        return path || null;
      } catch {
        // fall through to OS picker
      }
    }

    // 3. OS picker via backend API (works on desktop when not a SYSTEM service).
    try {
      const result = await api.pickFolder();
      return result.path;
    } catch {
      // 4. Manual text input fallback (headless / service context).
      return new Promise((resolve) => {
        setFallback({ resolve, path: '' });
      });
    }
  }, []);

  const confirm = () => {
    if (!fallback) return;
    const path = fallback.path.trim();
    fallback.resolve(path || null);
    setFallback(null);
  };

  const cancel = () => {
    fallback?.resolve(null);
    setFallback(null);
  };

  const modal =
    fallback !== null ? (
      <div className="fixed inset-0 bg-black/25 backdrop-blur-sm z-50 flex items-end sm:items-center justify-center p-4">
        <div className="bg-white rounded-2xl shadow-xl w-full max-w-sm px-5 py-5 flex flex-col gap-4">
          <div>
            <p className="text-base font-semibold text-slate-900">Enter folder path</p>
            <p className="text-sm text-slate-500 mt-1">
              No folder picker available on this system — type the path manually.
            </p>
          </div>
          <input
            autoFocus
            type="text"
            value={fallback.path}
            onChange={(e) => setFallback((s) => (s ? { ...s, path: e.target.value } : null))}
            onKeyDown={(e) => e.key === 'Enter' && confirm()}
            placeholder="/home/user/documents"
            className="w-full text-sm px-3 py-2.5 rounded-xl border border-slate-200 focus:outline-none focus:border-blue-400 font-mono placeholder:font-sans placeholder:text-slate-400"
          />
          <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <button
              type="button"
              onClick={cancel}
              className="sm:w-auto w-full px-4 py-2.5 rounded-xl text-sm font-medium border border-slate-200 text-slate-600 hover:bg-slate-50 transition-colors"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={confirm}
              disabled={!fallback.path.trim()}
              className="sm:w-auto w-full px-4 py-2.5 rounded-xl text-sm font-medium bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            >
              Use this path
            </button>
          </div>
        </div>
      </div>
    ) : null;

  return { pick, modal };
}
