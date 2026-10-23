// What the inspector remembers in the browser (docs/spec.md#workspace-inspector): its open tabs and Wrap.

/** The inspector's tools, in tab order. */
export const TOOLS = ['changes', 'files', 'terminal', 'browser', 'sessions', 'audit'] as const;
export type Tool = (typeof TOOLS)[number];
/** The tabs open when nothing is saved: Terminal and Browser are opened from the + menu. */
export const DEFAULT_TABS: readonly Tool[] = ['changes', 'files'];

export const TABS_KEY = 'hoplet.inspector-tabs';
export const WRAP_KEY = 'hoplet.inspector-wrap';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function storage(): Store | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

/** The open tabs as saved, in tool order; Changes and Files when nothing usable is saved. */
export function loadTabs(store: Store | null = storage()): Tool[] {
  try {
    const raw = store?.getItem(TABS_KEY);
    if (!raw) return [...DEFAULT_TABS];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [...DEFAULT_TABS];
    return TOOLS.filter((tool) => parsed.includes(tool));
  } catch {
    return [...DEFAULT_TABS];
  }
}

export function saveTabs(tabs: readonly Tool[], store: Store | null = storage()): void {
  try {
    store?.setItem(TABS_KEY, JSON.stringify(TOOLS.filter((tool) => tabs.includes(tool))));
  } catch {
    /* a browser that refuses storage forgets the choice on reload */
  }
}

/** Whether long lines wrap; they scroll sideways unless Wrap was turned on. */
export function loadWrap(store: Store | null = storage()): boolean {
  try {
    return store?.getItem(WRAP_KEY) === '1';
  } catch {
    return false;
  }
}

export function saveWrap(wrap: boolean, store: Store | null = storage()): void {
  try {
    store?.setItem(WRAP_KEY, wrap ? '1' : '0');
  } catch {
    /* a browser that refuses storage forgets the choice on reload */
  }
}
