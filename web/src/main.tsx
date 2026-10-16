import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@fontsource-variable/inter/wght.css';
import '@fontsource-variable/jetbrains-mono/wght.css';
import '@fontsource-variable/geist/wght.css';
import '@fontsource-variable/geist-mono/wght.css';
import App from './App.tsx';
import { ActionPage } from './screens/action/index.tsx';
import { tokenFromHash } from './screens/action/model.ts';
import './design/tokens.css';
import './components/components.css';
import './styles.css';
import { applyAppearance, loadAppearance } from './screens/settings/prefs.ts';

function applyScheme(): void {
  const scheme = new URLSearchParams(location.search).get('scheme');
  if (scheme === 'light' || scheme === 'dark') document.documentElement.dataset.scheme = scheme;
}

async function boot(): Promise<void> {
  applyAppearance(loadAppearance());
  applyScheme();
  // Development-only states route (docs/testing.md#end-to-end-tests): /__states/<screen>/<state>.
  // Production builds do not contain this branch or the fixtures.
  if (import.meta.env.DEV && location.pathname.startsWith('/__states/')) {
    const parts = location.pathname.split('/').filter(Boolean);
    const { renderState } = await import('./dev/states.ts');
    if (renderState(parts[1] ?? '', parts[2] ?? '')) return;
  }
  const root = document.getElementById('root');
  if (!root) return;
  // The action page (ADR 0021) needs no host token and no login: it is the route before the token gate. The
  // token lives in the fragment; read it once, drop it from the address bar and the history, keep it in memory.
  if (location.pathname === '/a') {
    const token = tokenFromHash(location.hash);
    history.replaceState(null, '', '/a');
    createRoot(root).render(
      <StrictMode>
        <ActionPage token={token} />
      </StrictMode>,
    );
    return;
  }
  registerServiceWorker();
  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}

/** The service worker only shows Web Push notifications; it is registered so enabling push has a worker to use. */
function registerServiceWorker(): void {
  if (!('serviceWorker' in navigator) || !window.isSecureContext) return;
  void navigator.serviceWorker.register('/sw.js').catch(() => undefined);
}

void boot();
