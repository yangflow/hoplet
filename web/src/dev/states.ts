// The states registry for the development-only /__states routes. Never imported outside a DEV guard.
import type { ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { states as actionStates } from '../screens/action/states.tsx';
import { states as cockpitStates } from '../screens/cockpit/states.tsx';
import { states as settingsStates } from '../screens/settings/states.tsx';
import { states as workspaceStates } from '../screens/workspace/states.tsx';

const registry: Record<string, Record<string, () => ReactNode>> = {
  cockpit: cockpitStates,
  workspace: workspaceStates,
  settings: settingsStates,
  action: actionStates,
};

export function renderState(screen: string, state: string): boolean {
  const render = registry[screen]?.[state];
  if (!render) return false;
  const root = document.getElementById('root');
  if (root) createRoot(root).render(render());
  return true;
}
