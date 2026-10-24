import { t } from '../../lib/i18n.ts';
import type { RuntimeState } from './model.ts';

/** The states in which a turn is in progress: they show on the live turn's header. */
export function isTurnState(state: RuntimeState): boolean {
  return state === 'starting' || state === 'running' || state === 'waiting-permission';
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
  /**
   * Present while a turn runs and the field stays open: what is written goes to the queue, or, with `steer`, may
   * also go into the running turn (ADR 0020).
   */
  sending?: { steer: boolean };
};

/** The short name of a start failure: the problem in a few words; the full text is the tooltip. */
export function startErrorName(error: string | undefined, providerName: string): string {
  if (error && /ENOENT|not found|no such file/i.test(error)) {
    return t('{provider} not found').replace('{provider}', providerName);
  }
  return t('Failed to start');
}

/** What the composer's bar says of an offline owner: with what the session was doing when it was last heard. */
export function offlineText(deviceName: string, lastState: RuntimeState | undefined): string {
  const text =
    lastState === 'waiting-permission'
      ? t('{device} offline · a permission request is waiting')
      : lastState && isTurnState(lastState)
        ? t('{device} offline · the session may still be running there')
        : t('{device} offline');
  return text.replace('{device}', deviceName);
}

/**
 * The second line of a session's row in the list (ADR 0039): what a session whose machine is away was doing, or
 * how far its catch-up has come; null when the row needs none.
 */
export function awayNote(info: {
  state: RuntimeState;
  lastState?: RuntimeState | undefined;
  catchUp?: { received: number; total: number } | undefined;
}): string | null {
  if (info.state === 'offline') {
    if (info.lastState === 'waiting-permission') return t('Waiting for you · machine offline');
    if (info.lastState && isTurnState(info.lastState)) return t('May still be running');
    return null;
  }
  if (info.catchUp)
    return t('Catching up · {n}%').replace('{n}', String(catchUpPercent(info.catchUp)));
  return null;
}

/** How much of a catch-up has arrived, 0 to 100. */
export function catchUpPercent(catchUp: { received: number; total: number }): number {
  if (catchUp.total <= 0) return 100;
  return Math.min(100, Math.floor((catchUp.received / catchUp.total) * 100));
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
  /** The session's driver takes a steer (`capabilities.steer`). */
  canSteer?: boolean;
  /** The state before the owner went offline (ADR 0039). */
  lastState?: RuntimeState | undefined;
}): ComposerView {
  const ask = t('Ask {provider}…').replace('{provider}', input.providerName);
  const { state } = input;
  if (state === 'offline' || !input.ownerConnected) {
    return {
      layout: 'bar',
      placeholder: t('Owner offline'),
      inputEnabled: false,
      line: {
        text: offlineText(input.deviceName, input.lastState),
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
    case 'running': {
      const steer = input.canSteer === true;
      return {
        layout: 'field',
        placeholder: steer
          ? t('Queue or steer, without stopping the turn…')
          : t('Queue a message for after this turn…'),
        inputEnabled: true,
        sending: { steer },
      };
    }
    case 'waiting-permission':
      return {
        layout: 'field',
        placeholder: input.planPending
          ? t('Waiting for your decision on the plan')
          : t('Queue a message for after this turn…'),
        inputEnabled: true,
        sending: { steer: false },
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
