import type { LiveSessionMeta, Queue, RuntimeState } from '@hoplet/shared';
import { HopletError } from './error.ts';
import type { EventStream } from './events.ts';

/** What a session looked like just before a prompt was sent; `waitForTurn` needs to see a start after it. */
export type Baseline = {
  sessionId: string;
  /** The number of stored lines. */
  lineCount: number;
  state: RuntimeState;
};

export type TurnOutcome = 'ok' | 'failed' | 'ended' | 'blocked' | 'timeout';

export type TurnResult = {
  /**
   * `ok`: the turn finished and the session is ready. `failed`: the runtime failed (`error` says why).
   * `ended`: the runtime went idle, offline or external before finishing. `blocked`: waiting for a permission
   * answer (only with `untilBlocked`). `timeout`: `timeout` or `startTimeout` ran out; the turn may still run.
   */
  result: TurnOutcome;
  /** The runtime state when the wait ended. */
  state: RuntimeState;
  error?: string;
  lineCount: number;
};

export type WaitOptions = {
  /** The baseline `sendPrompt` or `createSession` returned. Without it the wait starts from the session as it is. */
  since?: Baseline;
  /** How long to wait for the turn to start after `since`, in milliseconds. Default 30000. */
  startTimeout?: number;
  /** How long to wait in all, in milliseconds. Default: no limit. */
  timeout?: number;
  /** Also end with `blocked` when the session waits for a permission answer. */
  untilBlocked?: boolean;
  /** Also wait until the queued messages have run. */
  drain?: boolean;
  signal?: AbortSignal;
  /** The safety-net poll in milliseconds when the event stream is not delivering. Default 1000. */
  pollIntervalMs?: number;
};

export type TurnIo = {
  getSession: (sessionId: string) => Promise<LiveSessionMeta>;
  getQueue: (sessionId: string) => Promise<Queue>;
  /** Null when events are unavailable; the wait then polls. */
  openEvents: (sessionId: string) => EventStream | null;
};

export const DEFAULT_START_TIMEOUT_MS = 30000;
const DEFAULT_POLL_MS = 1000;
/** While the socket is connected, polling is only a safety net. */
const CONNECTED_POLL_MS = 5000;

const BUSY: ReadonlySet<RuntimeState> = new Set(['starting', 'running', 'waiting-permission']);

type Observation = { state: RuntimeState; error?: string | undefined; lineCount: number };

function result(obs: Observation, outcome: TurnOutcome): TurnResult {
  return {
    result: outcome,
    state: obs.state,
    ...(obs.error === undefined ? {} : { error: obs.error }),
    lineCount: obs.lineCount,
  };
}

/** What the observation means for the turn; null while the wait goes on. `started` is sticky across calls. */
export function judge(
  obs: Observation,
  started: boolean,
  opts: Pick<WaitOptions, 'since' | 'untilBlocked'>,
): { started: boolean; outcome: TurnOutcome | null } {
  const sawStart =
    started ||
    BUSY.has(obs.state) ||
    (opts.since !== undefined && obs.lineCount > opts.since.lineCount);
  if (obs.state === 'waiting-permission' && opts.untilBlocked) {
    return { started: true, outcome: 'blocked' };
  }
  if (opts.since !== undefined && !sawStart) return { started: false, outcome: null };
  // Without a baseline a session that is not busy has no turn to wait for.
  if (BUSY.has(obs.state)) return { started: true, outcome: null };
  switch (obs.state) {
    case 'ready':
      return { started: true, outcome: 'ok' };
    case 'failed':
      return { started: true, outcome: 'failed' };
    default:
      return { started: true, outcome: 'ended' };
  }
}

function aborted(): HopletError {
  return new HopletError({ status: 0, code: 'aborted', reason: 'The wait was aborted.' });
}

/** Waits for the turn of `sessionId` to end; see `WaitOptions` and `TurnResult`. */
export async function waitForTurn(
  io: TurnIo,
  sessionId: string,
  opts: WaitOptions = {},
): Promise<TurnResult> {
  const startTimeout = opts.startTimeout ?? DEFAULT_START_TIMEOUT_MS;
  const pollOverride = opts.pollIntervalMs;
  const t0 = Date.now();
  const stream = io.openEvents(sessionId);

  let pending = false;
  let wake: (() => void) | null = null;
  let connected = false;
  const bump = (): void => {
    pending = true;
    wake?.();
  };
  const pump = (async () => {
    try {
      if (stream === null) return;
      for await (const event of stream) {
        if (event.type === 'sdk.connected') {
          connected = true;
          bump();
        } else if ('sessionId' in event && event.sessionId === sessionId) {
          bump();
        }
      }
    } catch {
      // A refused token fails the next read with the same error; polling reports it.
    }
  })();

  const sleep = (ms: number): Promise<void> =>
    new Promise((resolve) => {
      const done = (): void => {
        clearTimeout(timer);
        opts.signal?.removeEventListener('abort', done);
        wake = null;
        resolve();
      };
      const timer = setTimeout(done, ms);
      wake = done;
      opts.signal?.addEventListener('abort', done);
    });

  try {
    let started = false;
    let last: Observation | null = null;
    for (;;) {
      if (opts.signal?.aborted) throw aborted();
      pending = false;
      try {
        const meta = await io.getSession(sessionId);
        last = { state: meta.runtime.state, error: meta.runtime.error, lineCount: meta.lineCount };
        const verdict = judge(last, started, opts);
        started = verdict.started;
        if (verdict.outcome === 'ok' && opts.drain) {
          const queue = await io.getQueue(sessionId);
          if (queue.items.length === 0) return result(last, 'ok');
          if (queue.paused === 'error') {
            return result({ ...last, error: queue.error ?? last.error }, 'failed');
          }
          if (queue.paused === 'interrupted') return result(last, 'ended');
        } else if (verdict.outcome !== null) {
          return result(last, verdict.outcome);
        }
      } catch (err) {
        if (!(err instanceof HopletError && err.code === 'network-error')) throw err;
      }

      const elapsed = Date.now() - t0;
      const fallback: Observation = last ?? { state: 'offline', lineCount: 0 };
      if (opts.timeout !== undefined && elapsed >= opts.timeout) return result(fallback, 'timeout');
      if (opts.since !== undefined && !started && elapsed >= startTimeout) {
        return result(fallback, 'timeout');
      }
      if (pending) continue;
      let wait = pollOverride ?? (connected ? CONNECTED_POLL_MS : DEFAULT_POLL_MS);
      if (opts.timeout !== undefined) wait = Math.min(wait, opts.timeout - elapsed);
      if (opts.since !== undefined && !started) wait = Math.min(wait, startTimeout - elapsed);
      await sleep(Math.max(wait, 1));
    }
  } finally {
    stream?.close();
    await pump;
  }
}
