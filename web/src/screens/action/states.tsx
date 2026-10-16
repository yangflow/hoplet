import type { ReactNode } from 'react';
import { NotifyHint } from '../../components/NotifyHint.tsx';
import { t } from '../../lib/i18n.ts';
import { ActionCard, type ActionView } from './index.tsx';

// Fixtures for /__states/action/<state>: the states of design/screens/action.html that the app draws itself
// (the OS notification is drawn by the system). Nothing here is read by the real page.

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0);

function card(view: ActionView): ReactNode {
  return <ActionCard view={view} now={NOW} onDecide={() => undefined} />;
}

const OPEN = {
  state: 'open',
  sessionId: 'a1b2c3d4e5f6',
  requestId: 'req-1',
  kind: 'permission',
  project: 'hoplet',
  title: 'Fix cursor drift',
  tool: 'Bash',
  summary: 'pnpm test --filter core',
  expiresAt: NOW + 8 * 60_000,
} as const;

export const states: Record<string, () => ReactNode> = {
  confirm: () => card({ stage: 'open', info: OPEN }),
  'confirm-plan': () =>
    card({
      stage: 'open',
      info: {
        ...OPEN,
        kind: 'plan',
        tool: 'ExitPlanMode',
        planMode: 'acceptEdits',
        plan: '## Fix cursor drift\n\n1. Reproduce it in the composer.\n2. Clamp the caret after a paste.\n3. Add a regression test.',
      },
    }),
  'confirm-answered': () => card({ stage: 'closed', state: 'answered' }),
  'confirm-expired': () => card({ stage: 'closed', state: 'expired' }),
  'confirm-used': () => card({ stage: 'closed', state: 'used' }),
  'confirm-webui': () => card({ stage: 'closed', state: 'webui-only', sessionId: 'a1b2c3d4e5f6' }),
  'hint-blocked': () => (
    <main className="hint-demo">
      <h1 className="sr-only">{t('Notifications')}</h1>
      <NotifyHint onHowTo={() => undefined} onDismiss={() => undefined} />
    </main>
  ),
};
