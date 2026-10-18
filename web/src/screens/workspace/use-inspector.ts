import { useCallback, useMemo, useState } from 'react';
import { DEFAULT_TABS, loadTabs, saveTabs, TOOLS, type Tool } from './prefs.ts';
import type { Opened } from './use-workspace.ts';

/** What the inspector shows: its open tabs, the chosen one, the open path, and whether Hand off is open. */
export type InspectorView = {
  tabs: Tool[];
  /** The chosen tab; null until the reader picks one (Changes first when the tree has changes). */
  active: Tool | null;
  opened: Opened | null;
  handoffOpen: boolean;
};

export type InspectorActions = {
  /** Opens a tool's tab (again) and shows it. */
  openTool: (tool: Tool) => void;
  closeTool: (tool: Tool) => void;
  /** Opens a path in the viewer, showing the list it belongs to when `tool` is given. */
  openPath: (path: string, mode: Opened['mode'], tool?: Tool) => void;
  closeViewer: () => void;
  setHandoffOpen: (open: boolean) => void;
  /** Forgets the open path and the panel, as when another session is selected. */
  reset: () => void;
};

/** The inspector's state, held by the cockpit so a turn's Edit row and the shortcuts can open a path or a tool. */
export function useInspectorView(
  initial?: Partial<InspectorView>,
): [InspectorView, InspectorActions] {
  const [view, setView] = useState<InspectorView>(() => ({
    tabs: initial ? (initial.tabs ?? [...DEFAULT_TABS]) : loadTabs(),
    active: initial?.active ?? null,
    opened: initial?.opened ?? null,
    handoffOpen: initial?.handoffOpen ?? false,
  }));
  const persist = initial === undefined;

  const openTool = useCallback(
    (tool: Tool) =>
      setView((v) => {
        const tabs = v.tabs.includes(tool)
          ? v.tabs
          : TOOLS.filter((x) => x === tool || v.tabs.includes(x));
        if (persist && tabs !== v.tabs) saveTabs(tabs);
        return { ...v, tabs, active: tool };
      }),
    [persist],
  );
  const closeTool = useCallback(
    (tool: Tool) =>
      setView((v) => {
        const tabs = v.tabs.filter((x) => x !== tool);
        if (persist) saveTabs(tabs);
        const active = v.active === tool || v.active === null ? (tabs[0] ?? null) : v.active;
        return { ...v, tabs, active, opened: tabs.length === 0 ? null : v.opened };
      }),
    [persist],
  );
  const openPath = useCallback(
    (path: string, mode: Opened['mode'], tool?: Tool) => {
      if (tool) openTool(tool);
      setView((v) => ({ ...v, opened: { path, mode } }));
    },
    [openTool],
  );
  const closeViewer = useCallback(() => setView((v) => ({ ...v, opened: null })), []);
  const setHandoffOpen = useCallback(
    (handoffOpen: boolean) => setView((v) => ({ ...v, handoffOpen })),
    [],
  );
  const reset = useCallback(() => setView((v) => ({ ...v, opened: null, handoffOpen: false })), []);

  const actions = useMemo(
    () => ({ openTool, closeTool, openPath, closeViewer, setHandoffOpen, reset }),
    [openTool, closeTool, openPath, closeViewer, setHandoffOpen, reset],
  );
  return [view, actions];
}
