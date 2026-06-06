import { useEffect, type RefObject } from 'react';

/**
 * Calls `onOutside` when a mousedown lands outside `ref`. Only listens while
 * `active` is true, so closed dropdowns don't keep a global listener around.
 * Extracted from the folder invite pickers, which all had the same effect.
 */
export function useClickOutside(
  ref: RefObject<HTMLElement | null>,
  active: boolean,
  onOutside: () => void,
) {
  useEffect(() => {
    if (!active) return undefined;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onOutside();
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [ref, active, onOutside]);
}
