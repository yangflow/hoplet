// The composer's height (docs/spec.md#hoplet): the field grows with its text up to 8 lines; dragging the top edge
// sets a height the browser remembers, at most what leaves the conversation 240 px; a double-click forgets it.

export const HEIGHT_KEY = 'hoplet.composer-height';

/** One line of the field (`--leading-body`). */
export const LINE_PX = 24;
/** The field grows with its text up to this, then scrolls. */
export const AUTO_MAX_PX = 8 * LINE_PX;
/** What the conversation keeps above the composer. */
export const CONVERSATION_MIN_PX = 240;

type Store = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

function storage(): Store | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

/**
 * The tallest the field may be: the main area less the conversation's minimum and the composer's own
 * chrome (location row, toolbar, padding and the dock), never below one line.
 */
export function maxFieldHeight(mainHeight: number, chrome: number): number {
  return Math.max(LINE_PX, Math.floor(mainHeight - CONVERSATION_MIN_PX - chrome));
}

/** A dragged height kept within one line and the window's maximum. */
export function clampHeight(px: number, max: number): number {
  return Math.round(Math.min(Math.max(px, LINE_PX), Math.max(max, LINE_PX)));
}

/** The remembered field height, or null when the field sizes itself. */
export function loadHeight(store: Store | null = storage()): number | null {
  try {
    const raw = store?.getItem(HEIGHT_KEY);
    const px = raw ? Number(raw) : Number.NaN;
    return Number.isFinite(px) && px >= LINE_PX ? Math.round(px) : null;
  } catch {
    return null;
  }
}

export function saveHeight(px: number | null, store: Store | null = storage()): void {
  try {
    if (px === null) store?.removeItem(HEIGHT_KEY);
    else store?.setItem(HEIGHT_KEY, String(Math.round(px)));
  } catch {
    /* a browser that refuses storage forgets the height on reload */
  }
}
