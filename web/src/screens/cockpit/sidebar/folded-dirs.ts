import { useCallback, useState } from 'react';

/**
 * Which sidebar directories are folded (docs/spec.md#hoplet): remembered in the browser per device and directory,
 * so the choice survives a reload and is not shared with another browser.
 */
export const FOLDED_KEY = 'hoplet.folded-dirs';

/** The path that stands for a machine's own row: the device folds its directories under the same key scheme. */
export const DEVICE_ROW = '';

/** The key of one directory on one device. */
export function dirKey(device: string, path: string): string {
  return `${device}\u0000${path}`;
}

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function storage(): Store | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

/** The folded directories as saved; none when nothing is saved or the saved value is not a list of strings. */
export function loadFolded(store: Store | null = storage()): Set<string> {
  try {
    const raw = store?.getItem(FOLDED_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return new Set(
      Array.isArray(parsed) ? parsed.filter((k): k is string => typeof k === 'string') : [],
    );
  } catch {
    return new Set();
  }
}

export function saveFolded(folded: ReadonlySet<string>, store: Store | null = storage()): void {
  try {
    store?.setItem(FOLDED_KEY, JSON.stringify([...folded].sort()));
  } catch {
    /* a browser that refuses storage forgets the choice on reload */
  }
}

/** The folded set after one directory is folded or unfolded. */
export function toggled(folded: ReadonlySet<string>, key: string): Set<string> {
  const next = new Set(folded);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  return next;
}

/** The folded directories and a toggle that saves; `initial` replaces what is saved (development fixtures). */
export function useFoldedDirs(initial?: readonly string[]): {
  isFolded: (device: string, path: string) => boolean;
  toggle: (device: string, path: string) => void;
  /** Unfolds these keys' rows and no others; a row that is already open changes nothing. */
  reveal: (...keys: string[]) => void;
} {
  const [folded, setFolded] = useState<Set<string>>(() =>
    initial ? new Set(initial) : loadFolded(),
  );
  const toggle = useCallback((device: string, path: string) => {
    setFolded((current) => {
      const next = toggled(current, dirKey(device, path));
      saveFolded(next);
      return next;
    });
  }, []);
  const reveal = useCallback((...keys: string[]) => {
    setFolded((current) => {
      if (!keys.some((k) => current.has(k))) return current;
      const next = new Set(current);
      for (const k of keys) next.delete(k);
      saveFolded(next);
      return next;
    });
  }, []);
  return { isFolded: (device, path) => folded.has(dirKey(device, path)), toggle, reveal };
}
