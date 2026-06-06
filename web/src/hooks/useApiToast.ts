import { useCallback } from 'react';
import { useToast } from '../components/base/Toast/Toast';

/**
 * The one place the "await an api call, show a warning toast if it throws"
 * pattern lives. Replaces the dozens of hand-rolled
 * `.catch((e: Error) => addToast(e.message || '…', 'warning'))` call sites.
 *
 * Returns `run(promise, fallback)` which resolves to the value on success or
 * `undefined` on failure (after toasting). Callers that need the result can
 * still branch on it; fire-and-forget callers just `await run(...)`.
 */
export function useApiToast() {
  const { addToast } = useToast();
  return useCallback(
    async <T>(promise: Promise<T>, fallback: string): Promise<T | undefined> => {
      try {
        return await promise;
      } catch (e: unknown) {
        addToast(e instanceof Error && e.message ? e.message : fallback, 'warning');
        return undefined;
      }
    },
    [addToast],
  );
}
