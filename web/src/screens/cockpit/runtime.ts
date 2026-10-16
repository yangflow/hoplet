import { t } from '../../lib/i18n.ts';
import type { RuntimeState } from './model.ts';

/** The states in which a turn is in progress: they show on the live turn's header. */
export function isTurnState(state: RuntimeState): boolean {
  return state === 'starting' || state === 'running' || state === 'waiting-permission';
}

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

export type ComposerAction = 'stop' | 'retry' | 'handoff';

/** What the composer shows for the session's state: a field or a bar, and only what the state allows. */
export type ComposerView = {
  layout: 'field' | 'bar';
  placeholder: string;
  inputEnabled: boolean;
  /** The session-state line; absent when the state does not block or change what the user can do. */
  line?: {
    text: string;
    title: string;
    glyph: 'ready' | 'off' | 'eye' | 'bad';
    tone: 'plain' | 'bad';
  };
  action?: ComposerAction;
};

/** The short name of a start failure: the problem in a few words; the full text is the tooltip. */
export function startErrorName(error: string | undefined, providerName: string): string {
  if (error && /ENOENT|not found|no such file/i.test(error)) {
    return t('{provider} not found').replace('{provider}', providerName);
  }
  return t('Failed to start');
}

export function composerView(input: {
  state: RuntimeState;
  ownerConnected: boolean;
  /** This window is connected to the core. */
  live: boolean;
  deviceName: string;
  providerName: string;
  /** The error the failed state carries. */
  error?: string | undefined;
  /** The failed state is that of a turn the user watched run, not of the start. */
  turnFailed: boolean;
  /** A plan waits for the user's decision. */
  planPending?: boolean;
}): ComposerView {
  const ask = t('Ask {provider}…').replace('{provider}', input.providerName);
  const { state } = input;
  if (state === 'offline' || !input.ownerConnected) {
    return {
      layout: 'bar',
      placeholder: t('Owner offline'),
      inputEnabled: false,
      line: {
        text: t('{device} offline').replace('{device}', input.deviceName),
        title: t('Owner offline'),
        glyph: 'off',
        tone: 'plain',
      },
      action: 'handoff',
    };
  }
  if (state === 'external') {
    return {
      layout: 'bar',
      placeholder: t('Read only'),
      inputEnabled: false,
      line: {
        text: t('Read only'),
        title: t('Written by a process Hoplet did not start'),
        glyph: 'eye',
        tone: 'plain',
      },
    };
  }
  if (!input.live) return { layout: 'field', placeholder: t('Reconnecting'), inputEnabled: false };
  switch (state) {
    case 'starting':
      return {
        layout: 'field',
        placeholder: t('Starting {provider}').replace('{provider}', input.providerName),
        inputEnabled: false,
      };
    case 'running':
      return { layout: 'field', placeholder: t('Turn running'), inputEnabled: false };
    case 'waiting-permission':
      return {
        layout: 'field',
        placeholder: input.planPending
          ? t('Waiting for your decision on the plan')
          : t('Waiting for permission'),
        inputEnabled: false,
      };
    case 'failed':
      if (input.turnFailed) {
        return { layout: 'field', placeholder: t('Send again to retry'), inputEnabled: true };
      }
      return {
        layout: 'field',
        placeholder: ask,
        inputEnabled: true,
        line: {
          text: startErrorName(input.error, input.providerName),
          title: input.error ? `${t('Failed to start')} · ${input.error}` : t('Failed to start'),
          glyph: 'bad',
          tone: 'bad',
        },
        action: 'retry',
      };
    case 'ready':
      return {
        layout: 'field',
        placeholder: ask,
        inputEnabled: true,
        line: {
          text: '',
          title: t('Ready · {provider} is running').replace('{provider}', input.providerName),
          glyph: 'ready',
          tone: 'plain',
        },
        action: 'stop',
      };
    default:
      return { layout: 'field', placeholder: ask, inputEnabled: true };
  }
}
