// Are we running inside the Android WebView host? The native shell injects a
// `window.WeSync` bridge object; desktop/web have no such global. Used to gate
// Android-only settings (the whole Power section) and to decide layout defaults
// (Connectivity expands by default where it's the only setting on the page).
export function isAndroid(): boolean {
  return typeof (window as unknown as { WeSync?: unknown }).WeSync !== 'undefined';
}
