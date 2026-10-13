import { createContext, useContext } from 'react';

/** What rows share while a transcript is mounted: which folds are open, and where the session is. */
export type ViewState = {
  sessionId: string;
  /** Whether a fold or well is open; a row nobody has toggled is open when `byDefault` says so. */
  isOpen: (key: string, byDefault?: boolean) => boolean;
  setOpen: (key: string, open: boolean) => void;
  /** Opens the full diff of a file (the inspector; docs/design.md#layout-and-breakpoints). */
  openDiff?: (path: string) => void;
};

const noop: ViewState = { sessionId: '', isOpen: () => false, setOpen: () => undefined };

export const ViewContext = createContext<ViewState>(noop);

export function useView(): ViewState {
  return useContext(ViewContext);
}
