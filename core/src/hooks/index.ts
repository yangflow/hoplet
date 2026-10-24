// Hooks: sends session lifecycle events to webhooks and to the script hooks of the agents (ADR 0031). It listens to `control.onEvent` like notify does
// and never blocks the event: a matching hook gets a row in the delivery queue, and the sender works through
// the queue in the background, with retries, a few requests at a time. The queue lives in hoplet.db, so a restart
// goes on where it stopped. A script hook is declared in an agent's own config.json: the core lists it, switches
// it and tests it, sends its events to that one machine as `hook.event` and records the `hook.result` it gets back.

import { randomBytes } from 'node:crypto';
import {
  type HookActor,
  type HookCreateRequest,
  type HookDelivery,
  type HookEnvelope,
  type HookEventType,
  type HookInfo,
  type HookResult,
  type HookScope,
  type HookUpdateRequest,
  MAX_HOOKS,
  type ScriptHookInfo,
  type WebhookHookInfo,
} from '@hoplet/shared';
import type { Control, ControlEvent } from '../control/index.ts';
import { log, registerSecret } from '../log/index.ts';
import { clip, type Fetch, post, summarize } from '../notify/index.ts';
import {
  type DeliveryRecord,
  type HookRecord,
  type ScriptHookRecord,
  type Store,
  scriptHookId,
} from '../store/index.ts';
import {
  createPinnedFetch,
  type Resolver,
  refuseUrl,
  systemResolver,
  type UrlRefusal,
} from './address.ts';
import { signatureHeader } from './sign.ts';
import { createUlid } from './ulid.ts';

export { classifyAddress, createPinnedFetch, type Resolver, refuseUrl } from './address.ts';
export { signatureHeader } from './sign.ts';

const ATTEMPT_TIMEOUT_MS = 5_000;
/** How long after the event a delivery is still tried. */
const DELIVERY_WINDOW_MS = 24 * 3_600_000;
/** How long the replaced secret of a rotation stays valid. */
const ROTATION_GRACE_MS = 24 * 3_600_000;
/** Waits before the 2nd, 3rd, 4th and 5th attempt; every later wait is `LATER_BACKOFF_MS`. */
const BACKOFF_MS = [30_000, 120_000, 600_000, 1_800_000];
const LATER_BACKOFF_MS = 2 * 3_600_000;
const MAX_IN_FLIGHT = 4;
const PRUNE_EVERY_MS = 3_600_000;
const TITLE_MAX = 80;
const SUMMARY_MAX = 80;
const REASON_MAX = 200;
const PROJECT_MAX = 120;
const CONTEXT_CACHE_MAX = 1000;
const MAX_TIMER_MS = 2 ** 31 - 1;
const HISTORY_LIMIT = 100;

export interface HooksOptions {
  /** `HOPLET_HOOK_ALLOW_PRIVATE`: lets hooks call loopback, private and shared addresses, and use `http`. */
  allowPrivate?: boolean;
  /** The HTTP client of every delivery; the default connects only to a checked address. */
  fetch?: Fetch;
  /** Resolves host names; the system resolver by default. */
  resolve?: Resolver;
  now?: () => number;
}

export type CreateResult =
  | { ok: true; hook: WebhookHookInfo; secret: string }
  | { ok: false; error: 'too-many-hooks' }
  | { ok: false; error: 'bad-url'; reason: UrlRefusal };

export type UpdateResult =
  | { ok: true; hook: HookInfo }
  | { ok: false; error: 'not-found' }
  /** A script hook is declared on its machine: only `enabled` can be changed here. */
  | { ok: false; error: 'script-managed' }
  | { ok: false; error: 'bad-url'; reason: UrlRefusal };

export interface Hooks {
  /** Starts listening to the control events and sending what is queued. */
  start(): void;
  stop(): void;
  list(): HookInfo[];
  get(id: string): HookInfo | null;
  create(request: HookCreateRequest): Promise<CreateResult>;
  update(id: string, request: HookUpdateRequest): Promise<UpdateResult>;
  remove(id: string): boolean;
  /** Replaces the signing secret; the old one stays valid for 24 hours. Null when there is no such hook. */
  rotate(id: string): { hook: WebhookHookInfo; secret: string } | null;
  /** Whether `id` names a script hook, which cannot be created, edited, deleted or given a secret here. */
  isScript(id: string): boolean;
  deliveries(id: string): HookDelivery[] | null;
  /**
   * Sends one `hook.test` event now, without retry, and records it as a delivery. A script hook's test is
   * answered with the delivery still `pending`: the agent's result settles it.
   */
  test(id: string): Promise<{ hook: HookInfo; delivery: HookDelivery } | null>;
  /** Sends what is due now. The sender calls it itself; tests call it after moving the clock. */
  pump(): void;
  /** Resolves when no request is in flight. */
  idle(): Promise<void>;
}

interface Context {
  deviceId: string | null;
  projectPath: string | null;
}

function projectName(projectPath: string | null): string {
  const parts = (projectPath ?? '').split(/[\\/]/).filter((p) => p !== '');
  return clip(parts[parts.length - 1] ?? projectPath ?? '', PROJECT_MAX);
}

/** What an event is to a hook before the per-hook opt-in fields are added. */
interface Prepared {
  type: HookEventType | 'hook.test';
  sessionId: string | undefined;
  context: Context;
  actor: HookActor;
  /** The data every hook gets. */
  data: Record<string, unknown>;
  /** The data only a hook with `summary` gets; computed when one is asked for. */
  extra?: () => Record<string, unknown>;
  /** Handoff events name both machines. */
  machines?: { from: string; to: string };
  /** A machine replayed the event after a reconnect: it happened at this time, epoch ms (ADR 0039). */
  replayedAt?: number;
}

function backoff(attempts: number): number {
  return BACKOFF_MS[attempts - 1] ?? LATER_BACKOFF_MS;
}

export function newSecret(): string {
  return `whsec_${randomBytes(32).toString('base64url')}`;
}

/** The project filter of a script hook: empty takes every project, else the project or one under it. */
function projectMatches(projects: readonly string[], projectPath: string | null): boolean {
  if (projects.length === 0) return true;
  if (projectPath === null) return false;
  return projects.some((p) => projectPath === p || projectPath.startsWith(`${p}/`));
}

/** Splits `script:<deviceId>:<slug>`; the slug holds no colon. */
function parseScriptId(id: string): { deviceId: string; slug: string } | null {
  if (!id.startsWith('script:')) return null;
  const last = id.lastIndexOf(':');
  if (last <= 'script:'.length) return null;
  return { deviceId: id.slice('script:'.length, last), slug: id.slice(last + 1) };
}

/** The URL as a list shows it: the query string may carry a credential, so it is cut. */
function displayUrl(url: string): string {
  try {
    const parsed = new URL(url);
    return `${parsed.origin}${parsed.pathname}`;
  } catch {
    return '';
  }
}

export function createHooks(store: Store, control: Control, options: HooksOptions = {}): Hooks {
  const now = options.now ?? Date.now;
  const allowPrivate = options.allowPrivate ?? false;
  const resolve = options.resolve ?? systemResolver;
  const fetchImpl: Fetch = options.fetch ?? createPinnedFetch({ allowPrivate, resolve });
  const newEventId = createUlid(now);
  const contexts = new Map<string, Context>();
  const sending = new Set<string>();
  const inFlight = new Set<Promise<void>>();
  let unsubscribe: (() => void) | null = null;
  let unsubscribeResult: (() => void) | null = null;
  let unsubscribeGone: (() => void) | null = null;
  let timer: NodeJS.Timeout | null = null;
  let pruneTimer: NodeJS.Timeout | null = null;
  let running = false;

  function registerSecrets(): void {
    for (const hook of store.listHooks()) {
      const secret = store.hookSecret(hook.id);
      if (secret) {
        registerSecret(secret.current);
        if (secret.previous) registerSecret(secret.previous.secret);
      }
    }
  }

  function lastDeliveryOf(id: string): Pick<ScriptHookInfo, 'lastDelivery'> {
    const last = store.lastSettledHookDelivery(id);
    return last && last.settledAt !== undefined
      ? {
          lastDelivery: {
            at: last.settledAt,
            state: last.state,
            ...(last.error ? { error: last.error } : {}),
          },
        }
      : {};
  }

  function scriptInfo(hook: ScriptHookRecord): ScriptHookInfo {
    const id = scriptHookId(hook.deviceId, hook.id);
    return {
      id,
      kind: 'script',
      label: hook.id,
      machine: {
        ...machineRef(hook.deviceId),
        online: control.ownerOnline(hook.deviceId),
      },
      command: hook.command,
      events: hook.events as ScriptHookInfo['events'],
      projects: hook.projects,
      timeoutMs: hook.timeoutMs,
      env: hook.env,
      runs: hook.runs,
      failed: hook.failed,
      dropped: hook.dropped,
      enabled: hook.enabled,
      status: hook.enabled ? 'active' : 'disabled',
      updatedAt: hook.updatedAt,
      pending: store.pendingHookDeliveries(id),
      ...lastDeliveryOf(id),
    };
  }

  function info(hook: HookRecord): WebhookHookInfo {
    const last = store.lastSettledHookDelivery(hook.id);
    const status = !hook.enabled
      ? 'disabled'
      : store.hookSecret(hook.id)
        ? 'active'
        : 'needs-secret';
    return {
      id: hook.id,
      kind: 'webhook',
      label: hook.label,
      url: displayUrl(hook.url),
      events: hook.events,
      scope: hook.scope,
      summary: hook.summary,
      enabled: hook.enabled,
      status,
      version: 1,
      createdAt: hook.createdAt,
      updatedAt: hook.updatedAt,
      pending: store.pendingHookDeliveries(hook.id),
      ...(last && last.settledAt !== undefined
        ? {
            lastDelivery: {
              at: last.settledAt,
              state: last.state,
              ...(last.error ? { error: last.error } : {}),
            },
          }
        : {}),
    };
  }

  function deliveryInfo(d: DeliveryRecord): HookDelivery {
    return {
      id: d.id,
      eventId: d.eventId,
      type: d.type,
      state: d.state,
      attempts: d.attempts,
      ...(d.error ? { error: d.error } : {}),
      createdAt: d.createdAt,
      ...(d.settledAt !== undefined ? { settledAt: d.settledAt } : {}),
      ...(d.nextAttemptAt !== undefined ? { nextAttemptAt: d.nextAttemptAt } : {}),
      ...(d.run ? { run: d.run } : {}),
    };
  }

  // ---- events -> envelopes ----

  function remember(sessionId: string, context: Context): void {
    contexts.delete(sessionId);
    contexts.set(sessionId, context);
    if (contexts.size > CONTEXT_CACHE_MAX) {
      const oldest = contexts.keys().next().value;
      if (oldest !== undefined) contexts.delete(oldest);
    }
  }

  /** The machine and project of a session: the store when it knows the session, else what the emitter said. */
  function contextOf(sessionId: string, hint: Partial<Context> = {}): Context {
    const meta = store.getSession(sessionId);
    const cached = contexts.get(sessionId);
    const context: Context = {
      deviceId: meta?.ownerDeviceId ?? hint.deviceId ?? cached?.deviceId ?? null,
      projectPath: meta?.projectPath ?? hint.projectPath ?? cached?.projectPath ?? null,
    };
    if (context.deviceId !== null || context.projectPath !== null) remember(sessionId, context);
    return context;
  }

  function prepare(event: ControlEvent): Prepared | null {
    switch (event.kind) {
      case 'started':
        return {
          type: 'session.started',
          sessionId: event.sessionId,
          context: contextOf(event.sessionId, {
            deviceId: event.deviceId,
            projectPath: event.projectPath,
          }),
          actor: { kind: 'user' },
          data: {
            provider: event.provider,
            ...(event.configuration !== undefined ? { configuration: event.configuration } : {}),
            origin: event.origin,
          },
        };
      case 'turn-done':
        return {
          type: 'turn.completed',
          sessionId: event.sessionId,
          context: contextOf(event.sessionId),
          actor: { kind: 'system' },
          data: { turn: event.turn ?? 0, durationMs: event.durationMs ?? 0 },
          extra: () => {
            const meta = store.getSession(event.sessionId);
            const total = store.sessionUsage(event.sessionId)?.total;
            return {
              ...(meta ? { title: clip(meta.title, TITLE_MAX) } : {}),
              ...(total
                ? {
                    usage: {
                      inputTokens: total.input,
                      outputTokens: total.output,
                      ...(total.costComplete && total.estimatedCostUsd !== null
                        ? { costUsd: total.estimatedCostUsd }
                        : {}),
                    },
                  }
                : {}),
            };
          },
        };
      case 'ended': {
        const context = contextOf(event.sessionId, {
          ...(event.deviceId !== undefined ? { deviceId: event.deviceId } : {}),
          ...(event.projectPath !== undefined ? { projectPath: event.projectPath } : {}),
        });
        if (event.reason === 'deleted') contexts.delete(event.sessionId);
        return {
          type: 'session.ended',
          sessionId: event.sessionId,
          context,
          actor: { kind: event.reason === 'idle' ? 'system' : 'user' },
          data: { reason: event.reason },
        };
      }
      case 'permission':
        return {
          type: 'permission.requested',
          sessionId: event.sessionId,
          context: contextOf(event.sessionId),
          actor: { kind: 'system' },
          data: {
            requestId: event.requestId,
            tool: event.tool,
            plan: event.plan,
            ...(event.expiresAt !== undefined ? { expiresAt: event.expiresAt } : {}),
          },
          extra: () => {
            const summary = event.plan
              ? undefined
              : summarize(event.tool, event.input, SUMMARY_MAX);
            return summary ? { summary } : {};
          },
        };
      case 'resolved':
        return {
          type: 'permission.resolved',
          sessionId: event.sessionId,
          context: contextOf(event.sessionId),
          actor: event.actor,
          data: { requestId: event.requestId, resolution: event.resolution },
        };
      case 'failure':
        return {
          type: 'session.error',
          sessionId: event.sessionId,
          context: event.sessionId
            ? contextOf(event.sessionId, {
                ...(event.deviceId !== undefined ? { deviceId: event.deviceId } : {}),
                ...(event.projectPath !== undefined ? { projectPath: event.projectPath } : {}),
              })
            : { deviceId: event.deviceId ?? null, projectPath: event.projectPath ?? null },
          actor: { kind: 'system' },
          data: { stage: event.source, code: event.error },
        };
      case 'runtime-error':
        return {
          type: 'session.error',
          sessionId: event.sessionId,
          context: contextOf(event.sessionId),
          actor: { kind: 'system' },
          data: { stage: 'runtime', code: event.code },
          extra: () => (event.reason ? { reason: clip(event.reason, REASON_MAX) } : {}),
        };
      case 'handoff-started':
      case 'handoff-done': {
        const started = event.kind === 'handoff-started';
        return {
          type: started ? 'handoff.started' : 'handoff.completed',
          sessionId: event.sessionId,
          context: contextOf(event.sessionId, {
            deviceId: started ? event.from : event.to,
          }),
          actor: { kind: 'user' },
          data: { handoffId: event.handoffId },
          machines: { from: event.from, to: event.to },
        };
      }
      default:
        return null;
    }
  }

  function machineRef(deviceId: string | null): { id: string; name: string } {
    if (deviceId === null) return { id: '', name: '' };
    const name = store.listDevices().find((d) => d.deviceId === deviceId)?.name;
    return { id: deviceId, name: name ?? deviceId };
  }

  function inScope(scope: HookScope, context: Context): boolean {
    if (scope.kind === 'all') return true;
    return context.deviceId === scope.deviceId && context.projectPath === scope.projectPath;
  }

  function envelope(
    prepared: Prepared,
    eventId: string,
    at: number,
    withExtra: boolean,
  ): HookEnvelope {
    const data: Record<string, unknown> = { ...prepared.data };
    if (prepared.machines) {
      data.from = machineRef(prepared.machines.from);
      data.to = machineRef(prepared.machines.to);
    }
    if (withExtra && prepared.extra) Object.assign(data, prepared.extra());
    return {
      v: 1,
      id: eventId,
      type: prepared.type,
      at: new Date(prepared.replayedAt ?? at).toISOString(),
      ...(prepared.replayedAt !== undefined ? { replayed: true } : {}),
      session: {
        ...(prepared.sessionId !== undefined ? { id: prepared.sessionId } : {}),
        project: { name: projectName(prepared.context.projectPath) },
        machine: machineRef(prepared.context.deviceId),
      },
      actor: prepared.actor,
      data,
    } as HookEnvelope;
  }

  /** The envelope a script receives: the project also carries its directory, which the agent filters by. */
  function scriptEnvelope(
    prepared: Prepared,
    eventId: string,
    at: number,
    withExtra: boolean,
  ): HookEnvelope {
    const base = envelope(prepared, eventId, at, withExtra);
    const path = prepared.context.projectPath;
    if (path === null || path === '') return base;
    return {
      ...base,
      session: { ...base.session, project: { ...base.session.project, path } },
    } as HookEnvelope;
  }

  /** Records a script delivery that never left: the machine is offline. */
  function dropOffline(d: DeliveryRecord): void {
    store.recordHookDelivery(d.id, {
      state: 'dropped',
      attempts: 0,
      error: 'offline',
      settledAt: now(),
    });
  }

  /** Sends one event to a script hook on its machine and queues the delivery that waits for the result. */
  function dispatchScript(
    hook: { deviceId: string; id: string },
    body: HookEnvelope,
    at: number,
  ): DeliveryRecord {
    const id = scriptHookId(hook.deviceId, hook.id);
    const row = store.enqueueHookDelivery(
      {
        hookId: id,
        eventId: body.id,
        type: body.type,
        body: '',
        expiresAt: at,
        script: true,
      },
      at,
    );
    if (!control.sendHookEvent(hook.deviceId, hook.id, body)) dropOffline(row);
    return row;
  }

  function onScriptEvent(prepared: Prepared, eventId: string, at: number): void {
    const deviceId = prepared.context.deviceId;
    if (deviceId === null) return;
    for (const hook of store.listScriptHooks()) {
      if (hook.deviceId !== deviceId || !hook.enabled) continue;
      if (!hook.events.includes('*') && !hook.events.includes(prepared.type)) continue;
      if (!projectMatches(hook.projects, prepared.context.projectPath)) continue;
      dispatchScript(hook, scriptEnvelope(prepared, eventId, at, true), at);
    }
  }

  function onHookResult(deviceId: string, result: HookResult): void {
    try {
      const id = scriptHookId(deviceId, result.hookId);
      const d = store.findPendingHookDelivery(id, result.eventId);
      if (!d) return;
      const state = result.ok ? 'delivered' : result.error === 'dropped' ? 'dropped' : 'failed';
      const error = result.ok
        ? undefined
        : (result.error ?? (result.exitCode !== undefined ? `exit-${result.exitCode}` : 'failed'));
      store.recordHookDelivery(d.id, {
        state,
        attempts: 1,
        ...(error ? { error } : {}),
        settledAt: now(),
        run: {
          ...(result.exitCode !== undefined ? { exitCode: result.exitCode } : {}),
          durationMs: result.durationMs,
          stderr: result.stderr,
        },
      });
    } catch (err) {
      log('error', 'hooks', 'recording a script result failed', { error: String(err) });
    }
  }

  function onEvent(event: ControlEvent): void {
    try {
      const matching = store
        .listHooks()
        .filter((h) => h.enabled && store.hookSecret(h.id) !== null);
      const prepared = prepare(event);
      if (!prepared) return;
      if (event.replayed && event.at !== undefined) prepared.replayedAt = event.at;
      const targets = matching.filter(
        (h) => (h.events as string[]).includes(prepared.type) && inScope(h.scope, prepared.context),
      );
      const at = now();
      const eventId = `evt_${newEventId()}`;
      onScriptEvent(prepared, eventId, at);
      if (targets.length === 0) return;
      for (const hook of targets) {
        const body = JSON.stringify(envelope(prepared, eventId, at, hook.summary));
        store.enqueueHookDelivery(
          {
            hookId: hook.id,
            eventId,
            type: prepared.type,
            body,
            expiresAt: at + DELIVERY_WINDOW_MS,
          },
          at,
        );
      }
      setImmediate(pump);
    } catch (err) {
      log('error', 'hooks', 'queueing an event failed', { error: String(err) });
    }
  }

  // ---- sending ----

  function headersFor(
    hookId: string,
    eventType: string,
    body: string,
  ): Record<string, string> | null {
    const secret = store.hookSecret(hookId);
    if (!secret) return null;
    const at = now();
    const secrets = [secret.current];
    if (secret.previous && secret.previous.until > at) secrets.push(secret.previous.secret);
    return {
      'content-type': 'application/json',
      'user-agent': 'Hoplet-Hook/1',
      'hoplet-event': eventType,
      'hoplet-signature': signatureHeader(secrets, body, at),
    };
  }

  /** Makes one attempt for a delivery and records what came of it. */
  async function attempt(d: DeliveryRecord, oneShot: boolean): Promise<void> {
    const hook = store.getHook(d.hookId);
    if (!hook) return;
    const settle = (state: 'delivered' | 'failed' | 'dropped', attempts: number, error?: string) =>
      store.recordHookDelivery(d.id, {
        state,
        attempts,
        ...(error ? { error } : {}),
        settledAt: now(),
      });
    if (!hook.enabled) {
      settle('dropped', d.attempts, 'disabled');
      return;
    }
    if (!oneShot && now() >= d.expiresAt) {
      settle('failed', d.attempts, 'expired');
      return;
    }
    const headers = headersFor(hook.id, d.type, d.body);
    if (!headers) {
      settle('failed', d.attempts, 'needs-secret');
      return;
    }
    const result = await post(fetchImpl, hook.url, headers, d.body, ATTEMPT_TIMEOUT_MS, {
      hook: true,
    });
    const attempts = d.attempts + 1;
    if (result.ok) {
      settle('delivered', attempts);
      return;
    }
    const next = now() + backoff(attempts);
    if (result.retry && !oneShot && next < d.expiresAt) {
      store.recordHookDelivery(d.id, {
        state: 'pending',
        attempts,
        error: result.error,
        nextAttemptAt: next,
      });
      return;
    }
    settle('failed', attempts, result.error);
  }

  function schedule(): void {
    if (timer) clearTimeout(timer);
    timer = null;
    if (!running) return;
    const next = store.nextHookDeliveryAt();
    if (next === null) return;
    timer = setTimeout(pump, Math.min(Math.max(0, next - now()), MAX_TIMER_MS));
    timer.unref();
  }

  function pump(): void {
    if (!running) return;
    try {
      const due = store.dueHookDeliveries(now(), 200).filter((d) => !sending.has(d.id));
      // One hook with a long backlog must not starve the others: take one delivery per hook in turn.
      const byHook = new Map<string, DeliveryRecord[]>();
      for (const d of due) byHook.set(d.hookId, [...(byHook.get(d.hookId) ?? []), d]);
      const queues = [...byHook.values()];
      while (sending.size < MAX_IN_FLIGHT && queues.some((q) => q.length > 0)) {
        for (const q of queues) {
          const d = q.shift();
          if (!d || sending.size >= MAX_IN_FLIGHT) continue;
          sending.add(d.id);
          const run: Promise<void> = attempt(d, false)
            .catch((err) =>
              log('error', 'hooks', 'delivery failed unexpectedly', { error: String(err) }),
            )
            .finally(() => {
              sending.delete(d.id);
              inFlight.delete(run);
              pump();
            });
          inFlight.add(run);
        }
      }
    } catch (err) {
      log('error', 'hooks', 'sending failed', { error: String(err) });
    }
    schedule();
  }

  // ---- management ----

  async function refuse(url: string): Promise<UrlRefusal | null> {
    return refuseUrl(url, allowPrivate, resolve);
  }

  return {
    start() {
      if (running) return;
      running = true;
      registerSecrets();
      unsubscribe = control.onEvent(onEvent);
      unsubscribeResult = control.onHookResult(onHookResult);
      unsubscribeGone = control.onDevicesGone((ids) => {
        for (const id of ids) store.settlePendingScriptDeliveries(id, 'connection-lost', now());
      });
      store.settlePendingScriptDeliveries(null, 'interrupted', now());
      store.pruneHookDeliveries(now());
      pruneTimer = setInterval(() => store.pruneHookDeliveries(now()), PRUNE_EVERY_MS);
      pruneTimer.unref();
      pump();
    },
    stop() {
      running = false;
      unsubscribe?.();
      unsubscribe = null;
      unsubscribeResult?.();
      unsubscribeResult = null;
      unsubscribeGone?.();
      unsubscribeGone = null;
      if (timer) clearTimeout(timer);
      if (pruneTimer) clearInterval(pruneTimer);
      timer = null;
      pruneTimer = null;
    },
    list: () => [...store.listHooks().map(info), ...store.listScriptHooks().map(scriptInfo)],
    get(id) {
      const script = parseScriptId(id);
      if (script) {
        const record = store.getScriptHook(script.deviceId, script.slug);
        return record ? scriptInfo(record) : null;
      }
      const hook = store.getHook(id);
      return hook ? info(hook) : null;
    },
    isScript: (id) => parseScriptId(id) !== null,
    async create(request) {
      if (store.hookCount() >= MAX_HOOKS) return { ok: false, error: 'too-many-hooks' };
      const reason = await refuse(request.url);
      if (reason) return { ok: false, error: 'bad-url', reason };
      const secret = newSecret();
      registerSecret(secret);
      const hook = store.createHook(
        {
          url: request.url,
          label: request.label,
          enabled: request.enabled ?? true,
          events: request.events,
          scope: request.scope ?? { kind: 'all' },
          summary: request.summary ?? false,
        },
        secret,
        now(),
      );
      return { ok: true, hook: info(hook), secret };
    },
    async update(id, request) {
      const script = parseScriptId(id);
      if (script) {
        if (!store.getScriptHook(script.deviceId, script.slug)) {
          return { ok: false, error: 'not-found' };
        }
        const { enabled, ...rest } = request;
        if (Object.values(rest).some((value) => value !== undefined)) {
          return { ok: false, error: 'script-managed' };
        }
        const updated =
          enabled === undefined
            ? store.getScriptHook(script.deviceId, script.slug)
            : store.setScriptHookEnabled(script.deviceId, script.slug, enabled);
        if (!updated) return { ok: false, error: 'not-found' };
        return { ok: true, hook: scriptInfo(updated) };
      }
      if (!store.getHook(id)) return { ok: false, error: 'not-found' };
      if (request.url !== undefined) {
        const reason = await refuse(request.url);
        if (reason) return { ok: false, error: 'bad-url', reason };
      }
      const updated = store.updateHook(
        id,
        {
          ...(request.url === undefined ? {} : { url: request.url }),
          ...(request.label === undefined ? {} : { label: request.label }),
          ...(request.enabled === undefined ? {} : { enabled: request.enabled }),
          ...(request.events === undefined ? {} : { events: request.events }),
          ...(request.scope === undefined ? {} : { scope: request.scope }),
          ...(request.summary === undefined ? {} : { summary: request.summary }),
        },
        now(),
      );
      if (!updated) return { ok: false, error: 'not-found' };
      if (request.enabled === false) pump();
      return { ok: true, hook: info(updated) };
    },
    remove: (id) => !parseScriptId(id) && store.deleteHook(id),
    rotate(id) {
      if (parseScriptId(id)) return null;
      const hook = store.getHook(id);
      if (!hook) return null;
      const secret = newSecret();
      registerSecret(secret);
      store.setHookSecret(id, secret, now() + ROTATION_GRACE_MS);
      store.auditHookAction(id, 'rotated');
      return { hook: info(hook), secret };
    },
    deliveries(id) {
      const script = parseScriptId(id);
      if (script ? !store.getScriptHook(script.deviceId, script.slug) : !store.getHook(id)) {
        return null;
      }
      return store.listHookDeliveries(id, HISTORY_LIMIT).map(deliveryInfo);
    },
    async test(id) {
      const script = parseScriptId(id);
      if (script) {
        const record = store.getScriptHook(script.deviceId, script.slug);
        if (!record) return null;
        const at = now();
        const body = scriptEnvelope(
          {
            type: 'hook.test',
            sessionId: undefined,
            context: { deviceId: null, projectPath: null },
            actor: { kind: 'user' },
            data: { hookId: id },
          },
          `evt_${newEventId()}`,
          at,
          false,
        );
        // A test goes out even when the hook is disabled, like a webhook's.
        const row = dispatchScript(record, body, at);
        store.auditScriptHookTest(record.deviceId, record.id);
        const current = store.getHookDelivery(row.id);
        if (!current) return null;
        return { hook: scriptInfo(record), delivery: deliveryInfo(current) };
      }
      const hook = store.getHook(id);
      if (!hook) return null;
      const at = now();
      const eventId = `evt_${newEventId()}`;
      const body = JSON.stringify(
        envelope(
          {
            type: 'hook.test',
            sessionId: undefined,
            context: { deviceId: null, projectPath: null },
            actor: { kind: 'user' },
            data: { hookId: id },
          },
          eventId,
          at,
          false,
        ),
      );
      const row = store.enqueueHookDelivery(
        { hookId: id, eventId, type: 'hook.test', body, expiresAt: at },
        at,
      );
      sending.add(row.id);
      try {
        // A test goes out even when the hook is disabled: it answers whether the endpoint works.
        const headers = headersFor(id, 'hook.test', body);
        if (!headers) {
          store.recordHookDelivery(row.id, {
            state: 'failed',
            attempts: 0,
            error: 'needs-secret',
            settledAt: now(),
          });
        } else {
          const result = await post(fetchImpl, hook.url, headers, body, ATTEMPT_TIMEOUT_MS, {
            hook: true,
          });
          store.recordHookDelivery(row.id, {
            state: result.ok ? 'delivered' : 'failed',
            attempts: 1,
            ...(result.ok ? {} : { error: result.error }),
            settledAt: now(),
          });
        }
      } finally {
        sending.delete(row.id);
      }
      store.auditHookAction(id, 'tested');
      const settled = store.getHookDelivery(row.id);
      if (!settled) return null;
      return { hook: info(hook), delivery: deliveryInfo(settled) };
    },
    pump,
    async idle() {
      while (inFlight.size > 0) await Promise.all([...inFlight]);
    },
  };
}
