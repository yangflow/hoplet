// The watcher of trees of work (#81, ADR 0035): every 30 seconds it looks at the live sessions that sessions
// started and interrupts then stops those past their time limit, and all the live sessions of a tree that has
// spent its tokens. Each one it stops is audited as `child.limit`.
import { log } from '../log/index.ts';
import type { Store } from '../store/index.ts';
import { descendantsOf, LIVE_STATES, membersOf } from './tree.ts';
import type { Control, RuntimeCommandOutcome } from './types.ts';

/** How often the watcher looks. */
export const TREE_CHECK_MS = 30_000;

export interface TreeWatcherOptions {
  store: Store;
  runtime: {
    interrupt(sessionId: string, ownerDeviceId: string): RuntimeCommandOutcome;
    stop(sessionId: string, ownerDeviceId: string): RuntimeCommandOutcome;
  };
  control: Pick<Control, 'ownerOf' | 'runtimeOf'>;
  now: () => number;
  /** Input plus output tokens one tree may spend below its root. */
  treeTokens: number;
  /** Runs `fn` every `ms` until the returned function is called; the default is an unref'd interval. */
  every?: (fn: () => void, ms: number) => () => void;
  intervalMs?: number;
}

export interface TreeWatcher {
  /** Looks once; the timer calls it. */
  check(): void;
  close(): void;
}

function intervalEvery(fn: () => void, ms: number): () => void {
  const timer = setInterval(fn, ms);
  timer.unref();
  return () => clearInterval(timer);
}

type Reason = { limit: 'time'; timeLimitMs: number } | { limit: 'tokens'; treeTokens: number };

export function createTreeWatcher(options: TreeWatcherOptions): TreeWatcher {
  const { store, runtime, control } = options;
  /** Sessions already stopped for a limit and still live: they are not audited twice. */
  const handled = new Set<string>();

  function isLive(id: string): boolean {
    return LIVE_STATES.has(control.runtimeOf(id).state);
  }

  /** Interrupts then stops one session; false when its owner could not be reached, to try again. */
  function end(sessionId: string, root: string, reason: Reason): boolean {
    const owner = control.ownerOf(sessionId);
    if (owner === null) return true;
    const interrupted = runtime.interrupt(sessionId, owner);
    if (!interrupted.ok && interrupted.error === 'owner-offline') return false;
    const stopped = runtime.stop(sessionId, owner);
    if (!stopped.ok) return false;
    store.audit({
      type: 'child.limit',
      sessionId,
      deviceId: owner,
      actor: { kind: 'core' },
      channel: 'core',
      summary: { root, ...reason },
    });
    log('info', 'control', 'child session stopped at a limit', { sessionId, limit: reason.limit });
    return true;
  }

  function check(): void {
    const children = store.sessionChildren();
    const chains = new Map([...children].map(([id, c]) => [id, c.chain]));
    for (const id of [...handled]) if (!isLive(id)) handled.delete(id);

    // What each live session was stopped for, and the root it belongs to.
    const over = new Map<string, { root: string; reason: Reason }>();
    const now = options.now();
    for (const [id, child] of children) {
      if (!isLive(id) || now - child.startedAt < child.timeLimitMs) continue;
      const root = child.chain[0] as string;
      const reason: Reason = { limit: 'time', timeLimitMs: child.timeLimitMs };
      over.set(id, { root, reason });
      // A session past its time takes what it started with it.
      for (const d of descendantsOf(chains, id)) {
        if (isLive(d.sessionId) && !over.has(d.sessionId)) over.set(d.sessionId, { root, reason });
      }
    }

    const roots = new Set<string>();
    for (const chain of chains.values()) if (chain[0] !== undefined) roots.add(chain[0]);
    for (const root of roots) {
      const members = membersOf(chains, root);
      let tokens = 0;
      for (const id of members) {
        const total = store.sessionUsage(id)?.total;
        if (total) tokens += total.input + total.output;
      }
      if (tokens < options.treeTokens) continue;
      for (const id of members) {
        if (isLive(id) && !over.has(id)) {
          over.set(id, { root, reason: { limit: 'tokens', treeTokens: options.treeTokens } });
        }
      }
    }

    for (const [id, { root, reason }] of over) {
      if (handled.has(id)) continue;
      try {
        if (end(id, root, reason)) handled.add(id);
      } catch (err) {
        log('error', 'control', 'tree limit enforcement failed', {
          sessionId: id,
          error: err instanceof Error ? err.message : 'unknown',
        });
      }
    }
  }

  const cancel = (options.every ?? intervalEvery)(check, options.intervalMs ?? TREE_CHECK_MS);
  return { check, close: cancel };
}
