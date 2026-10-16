// Notify: turns control events into notifications and delivers them (ADR 0021). The core decides here, a channel
// only delivers. It listens to `control.onEvent` and never blocks the event: delivery is asynchronous.

import type {
  ActionDecideResponse,
  ActionInspectResponse,
  NotifyChannel,
  NotifyDelivery,
  NotifyEventKind,
  NotifyPayload,
  WebhookTestResponse,
} from '@hoplet/shared';
import type { Control, ControlEvent } from '../control/index.ts';
import { log, registerSecret } from '../log/index.ts';
import type { PushSubscriptionRecord, Store, WebhookRecord } from '../store/index.ts';
import { webUiOnly } from './classify.ts';
import { type Attempt, type Fetch, post, withRetry } from './deliver.ts';
import { LINK_MAX_MS, signToken, verifyToken } from './links.ts';
import { pushRequest } from './push.ts';
import { clip, PAGE_SUMMARY_MAX, SUMMARY_MAX, summarize } from './redact.ts';
import { channelsFor } from './rules.ts';
import { webhookHeaders } from './webhook.ts';

export { webUiOnly } from './classify.ts';
export { LINK_MAX_MS, signToken, verifyToken } from './links.ts';
export { encryptPayload, vapidJwt } from './push.ts';
export { clip, redactText, summarize } from './redact.ts';
export { channelsFor, inQuietHours } from './rules.ts';
export { signBody } from './webhook.ts';

const DELIVERY_TIMEOUT_MS = 5_000;
const RETRY_DELAY_MS = 1_000;
const PLAN_TEXT_MAX = 20_000;
const TITLE_MAX = 120;
const PROJECT_MAX = 120;

export interface NotifyOptions {
  /** The address the core is reached at from outside, without a trailing slash; links need it. */
  publicUrl: string | null;
  now?: () => number;
  /** The HTTP client of every outbound delivery. */
  fetch?: Fetch;
  timeoutMs?: number;
  retryDelayMs?: number;
}

export type DecideResult =
  | ActionDecideResponse
  | { error: 'invalid-token' | 'owner-offline' | 'invalid-request' };

export interface Notify {
  /** Starts listening to the control events. */
  start(): void;
  stop(): void;
  /** The VAPID public key subscriptions are made against. */
  pushKey(): string;
  /** Sends a test message to one webhook; null when there is none with that ID. */
  testWebhook(id: string): Promise<WebhookTestResponse | null>;
  /** What the action page shows for a token; null when the token is not one this core signed. */
  inspect(token: string): ActionInspectResponse | null;
  /** Answers the request a token names; an `error` when the token is not one this core signed or the answer cannot be given. */
  decide(token: string, decision: 'allow' | 'deny'): DecideResult;
}

function projectName(projectPath: string): string {
  const parts = projectPath.split(/[\\/]/).filter((p) => p !== '');
  return clip(parts[parts.length - 1] ?? projectPath, PROJECT_MAX);
}

export function createNotify(store: Store, control: Control, options: NotifyOptions): Notify {
  const now = options.now ?? Date.now;
  const fetchImpl: Fetch = options.fetch ?? fetch;
  const timeoutMs = options.timeoutMs ?? DELIVERY_TIMEOUT_MS;
  const retryDelayMs = options.retryDelayMs ?? RETRY_DELAY_MS;
  const publicUrl = options.publicUrl;
  const subject = publicUrl?.startsWith('https://') ? publicUrl : 'mailto:hoplet@localhost';
  let unsubscribe: (() => void) | null = null;

  const secrets = () => store.notifySecrets();

  function registerSecrets(): void {
    const s = secrets();
    registerSecret(s.actionKey);
    registerSecret(s.vapid.privateKey);
    for (const secret of Object.values(s.webhooks)) registerSecret(secret);
  }

  /** The open request a token names, with how to treat it. */
  function openRequest(sessionId: string, requestId: string) {
    return control.openPermissions(sessionId).find((p) => p.requestId === requestId);
  }

  function linkFor(sessionId: string, requestId: string, expiresAt: number): string | undefined {
    if (!publicUrl) return undefined;
    return `${publicUrl}/a#${signToken(secrets().actionKey, { sessionId, requestId, expiresAt })}`;
  }

  function buildPayload(event: ControlEvent): { kind: NotifyEventKind; payload: NotifyPayload } {
    if (event.kind === 'failure') {
      const meta = event.sessionId ? store.getSession(event.sessionId) : null;
      const projectPath = meta?.projectPath ?? event.projectPath ?? '';
      return {
        kind: 'failure',
        payload: {
          kind: 'failure',
          ...(event.sessionId ? { sessionId: event.sessionId } : {}),
          project: projectName(projectPath),
          ...(meta ? { title: clip(meta.title, TITLE_MAX) } : {}),
          reason: event.error,
        },
      };
    }
    const meta = store.getSession(event.sessionId);
    const project = projectName(meta?.projectPath ?? '');
    const title = meta ? { title: clip(meta.title, TITLE_MAX) } : {};
    if (event.kind === 'turn-done') {
      return {
        kind: 'turn-done',
        payload: { kind: 'turn-done', sessionId: event.sessionId, project, ...title },
      };
    }
    const kind = event.plan ? 'plan' : 'permission';
    const summary = event.plan ? undefined : summarize(event.tool, event.input, SUMMARY_MAX);
    // A plan never expires; its link lives the longest a link does.
    const expiresAt = Math.min(event.expiresAt ?? Number.POSITIVE_INFINITY, now() + LINK_MAX_MS);
    const link =
      expiresAt > now() ? linkFor(event.sessionId, event.requestId, expiresAt) : undefined;
    return {
      kind,
      payload: {
        kind,
        sessionId: event.sessionId,
        project,
        ...title,
        tool: event.tool,
        ...(summary ? { summary } : {}),
        requestId: event.requestId,
        ...(link ? { link } : {}),
      },
    };
  }

  function record(
    channel: NotifyChannel,
    kind: NotifyEventKind,
    payload: NotifyPayload,
    result: Attempt,
  ): NotifyDelivery {
    const delivery: NotifyDelivery = {
      at: now(),
      ok: result.ok,
      ...(result.ok ? {} : { error: result.error }),
    };
    log(
      result.ok ? 'info' : 'warn',
      'notify',
      result.ok ? 'notification sent' : 'notification failed',
      {
        kind,
        channel,
        sessionId: payload.sessionId,
        requestId: payload.requestId,
        ...(result.ok ? {} : { reason: result.error }),
      },
    );
    return delivery;
  }

  async function sendPush(
    sub: PushSubscriptionRecord,
    kind: NotifyEventKind,
    payload: NotifyPayload,
    body: Buffer,
  ): Promise<void> {
    const result = await withRetry(async () => {
      let request: ReturnType<typeof pushRequest>;
      try {
        request = pushRequest(
          sub,
          body,
          secrets().vapid,
          subject,
          now(),
          kind === 'turn-done' ? 'normal' : 'high',
        );
      } catch {
        return { ok: false, error: 'error', retry: false } satisfies Attempt;
      }
      return post(fetchImpl, sub.endpoint, request.headers, request.body, timeoutMs);
    }, retryDelayMs);
    const delivery = record('push', kind, payload, result);
    if (!result.ok && result.error === 'gone') store.deletePushSubscription(sub.id);
    else store.recordPushDelivery(sub.id, delivery);
  }

  async function sendWebhook(
    hook: WebhookRecord,
    kind: NotifyEventKind,
    payload: NotifyPayload,
    retry: boolean,
  ): Promise<NotifyDelivery> {
    const body = JSON.stringify(payload);
    const attempt = () =>
      post(
        fetchImpl,
        hook.url,
        webhookHeaders(body, store.webhookSecret(hook.id), kind),
        body,
        timeoutMs,
      );
    const result = await (retry ? withRetry(attempt, retryDelayMs) : attempt());
    const delivery = record('webhook', kind, payload, result);
    store.recordWebhookDelivery(hook.id, delivery);
    return delivery;
  }

  async function dispatch(
    kind: NotifyEventKind,
    payload: NotifyPayload,
    channels: NotifyChannel[],
  ): Promise<void> {
    const jobs: Promise<unknown>[] = [];
    if (channels.includes('push')) {
      const body = Buffer.from(JSON.stringify(payload));
      for (const sub of store.listPushSubscriptions())
        jobs.push(sendPush(sub, kind, payload, body));
    }
    if (channels.includes('webhook')) {
      for (const hook of store.listWebhooks()) {
        if (hook.enabled) jobs.push(sendWebhook(hook, kind, payload, true));
      }
    }
    // `native` has no sender yet: the browser WebSocket `notify` message is reserved for the Mac client.
    for (const result of await Promise.allSettled(jobs)) {
      if (result.status === 'rejected') {
        log('error', 'notify', 'delivery crashed', { error: String(result.reason) });
      }
    }
  }

  function onEvent(event: ControlEvent): void {
    if (event.kind === 'turn-done' && control.viewing.isOpen(event.sessionId)) return;
    const { kind, payload } = buildPayload(event);
    const session =
      'sessionId' in event && event.sessionId ? store.getSession(event.sessionId) : null;
    const projectPath =
      session?.projectPath ?? (event.kind === 'failure' ? (event.projectPath ?? '') : '');
    const channels = channelsFor(store.notifyPreferences(), kind, projectPath, now());
    if (channels.length === 0) return;
    void dispatch(kind, payload, channels).catch((err) =>
      log('error', 'notify', 'dispatch failed', { kind, error: String(err) }),
    );
  }

  return {
    start() {
      if (unsubscribe) return;
      store.pruneForeignSubscriptions();
      registerSecrets();
      unsubscribe = control.onEvent(onEvent);
    },
    stop() {
      unsubscribe?.();
      unsubscribe = null;
    },
    pushKey: () => secrets().vapid.publicKey,

    async testWebhook(id) {
      const hook = store.getWebhook(id);
      if (!hook) return null;
      const payload: NotifyPayload = {
        kind: 'turn-done',
        project: 'hoplet',
        title: 'Test notification from Hoplet',
      };
      const delivery = await sendWebhook(hook, 'turn-done', payload, false);
      return { webhook: store.getWebhook(id) ?? hook, delivery };
    },

    inspect(token) {
      const claims = verifyToken(secrets().actionKey, token);
      if (!claims) return null;
      if (store.actionTokenUsed(claims.id)) return { state: 'used' };
      if (now() > claims.expiresAt) return { state: 'expired' };
      const request = openRequest(claims.sessionId, claims.requestId);
      if (!request) return { state: 'answered' };
      const meta = store.getSession(claims.sessionId);
      const plan = request.planExit;
      const base = {
        sessionId: claims.sessionId,
        requestId: claims.requestId,
        kind: plan ? ('plan' as const) : ('permission' as const),
        project: projectName(meta?.projectPath ?? ''),
        ...(meta ? { title: clip(meta.title, TITLE_MAX) } : {}),
        tool: request.tool,
        expiresAt: claims.expiresAt,
      };
      if (plan) {
        return {
          state: 'open',
          ...base,
          planMode: plan.default,
          plan: plan.plan.slice(0, PLAN_TEXT_MAX),
        };
      }
      const summary = summarize(request.tool, request.input, PAGE_SUMMARY_MAX);
      const only = webUiOnly(request.tool, request.input, meta?.projectPath ?? '');
      return { state: only ? 'webui-only' : 'open', ...base, ...(summary ? { summary } : {}) };
    },

    decide(token, decision) {
      const claims = verifyToken(secrets().actionKey, token);
      if (!claims) return { error: 'invalid-token' };
      const state = (value: ActionDecideResponse['state']): ActionDecideResponse => ({
        state: value,
      });
      if (store.actionTokenUsed(claims.id)) return state('used');
      if (now() > claims.expiresAt) return state('expired');
      const request = openRequest(claims.sessionId, claims.requestId);
      const owner = store.ownerDeviceId(claims.sessionId);
      if (!request || !owner) return state('answered');
      const meta = store.getSession(claims.sessionId);
      if (webUiOnly(request.tool, request.input, meta?.projectPath ?? ''))
        return state('webui-only');
      // This runs without yielding, so the check above and the spending below cannot interleave with another call.
      const outcome = control.answerPermission(claims.sessionId, owner, claims.requestId, {
        decision,
        ...(request.planExit && decision === 'allow' ? { mode: request.planExit.default } : {}),
      });
      if (!outcome.ok && outcome.error === 'owner-offline') return { error: 'owner-offline' };
      if (!outcome.ok && outcome.error === 'invalid-request') return { error: 'invalid-request' };
      if (!outcome.ok) return state('answered');
      store.useActionToken(claims.id, claims.expiresAt, now());
      log('info', 'notify', 'action link used', {
        kind: request.planExit ? 'plan' : 'permission',
        channel: 'link',
        sessionId: claims.sessionId,
        requestId: claims.requestId,
        decision,
      });
      return state('decided');
    },
  };
}
