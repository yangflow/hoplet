import type { RuntimeState } from '@hoplet/shared';
import { t } from './i18n.ts';

/** The state in words, for tooltips, accessible names and the live region. */
export function stateName(state: RuntimeState): string {
  switch (state) {
    case 'offline':
      return t('Offline');
    case 'external':
      return t('External, read only');
    case 'idle':
      return t('Idle');
    case 'starting':
      return t('Starting');
    case 'ready':
      return t('Ready');
    case 'running':
      return t('Running');
    case 'waiting-permission':
      return t('Waiting for permission');
    case 'failed':
      return t('Failed');
  }
}
