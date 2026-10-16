import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@fontsource-variable/inter/wght.css';
import '@fontsource-variable/jetbrains-mono/wght.css';
import '@fontsource-variable/geist/wght.css';
import '@fontsource-variable/geist-mono/wght.css';
import App from './App.tsx';
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
  if (root)
    createRoot(root).render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
}

void boot();
