// What the inspector remembers in the browser (docs/spec.md#workspace-inspector): its open tabs and Wrap.

/** The inspector's tools; Terminal (#14) and Browser (#15) join when they are built. */
export const TOOLS = ['changes', 'files'] as const;
export type Tool = (typeof TOOLS)[number];

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

/** The open tabs as saved, in tool order; both when nothing usable is saved. */
export function loadTabs(store: Store | null = storage()): Tool[] {
  try {
    const raw = store?.getItem(TABS_KEY);
    if (!raw) return [...TOOLS];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [...TOOLS];
    return TOOLS.filter((tool) => parsed.includes(tool));
  } catch {
    return [...TOOLS];
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
