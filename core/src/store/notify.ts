import { randomBytes } from 'node:crypto';
import {
  DEFAULT_WEBHOOK_EVENTS,
  NOTIFY_EVENT_KINDS,
  type NotifyDelivery,
  type NotifyEventKind,
  type NotifyPreferences,
  NotifyPreferencesSchema,
  safeParse,
} from '@hoplet/shared';
import type { StoreContext } from './layout.ts';
import { notifySecrets, setWebhookSecret, webhookSecret } from './secrets.ts';

/** What a core starts with: the default preset, no overrides, no quiet hours. */
export const DEFAULT_PREFERENCES: NotifyPreferences = {
  preset: 'needs-me',
  events: {},
  projects: {},
  quietHours: null,
};

export function notifyPreferences(ctx: StoreContext): NotifyPreferences {
  const row = ctx.db.prepare('SELECT json FROM notify_prefs WHERE id = 1').get() as
    | { json: string }
    | undefined;
  if (!row) return structuredClone(DEFAULT_PREFERENCES);
  try {
    const parsed = safeParse(NotifyPreferencesSchema, JSON.parse(row.json));
    if (parsed.success) return parsed.output;
  } catch {
    // Unreadable preferences are the defaults.
  }
  return structuredClone(DEFAULT_PREFERENCES);
}

export function setNotifyPreferences(ctx: StoreContext, prefs: NotifyPreferences): void {
  ctx.db
    .prepare(
      'INSERT INTO notify_prefs(id, json) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET json = excluded.json',
    )
    .run(JSON.stringify(prefs));
}

function newId(): string {
  return randomBytes(9).toString('base64url');
}

function delivery(row: {
  last_at: number | null;
  last_ok: number | null;
  last_error: string | null;
}): NotifyDelivery | undefined {
  if (row.last_at === null) return undefined;
  return {
    at: row.last_at,
    ok: row.last_ok === 1,
    ...(row.last_error ? { error: row.last_error } : {}),
  };
}

interface PushRow {
  id: string;
  endpoint: string;
  p256dh: string;
  auth: string;
  label: string;
  created_at: number;
  last_at: number | null;
  last_ok: number | null;
  last_error: string | null;
}

export interface PushSubscriptionRecord {
  id: string;
  endpoint: string;
  p256dh: string;
  auth: string;
  label: string;
  createdAt: number;
  lastDelivery?: NotifyDelivery;
}

function pushRecord(row: PushRow): PushSubscriptionRecord {
  const last = delivery(row);
  return {
    id: row.id,
    endpoint: row.endpoint,
    p256dh: row.p256dh,
    auth: row.auth,
    label: row.label,
    createdAt: row.created_at,
    ...(last ? { lastDelivery: last } : {}),
  };
}

const PUSH_COLUMNS = 'id, endpoint, p256dh, auth, label, created_at, last_at, last_ok, last_error';

/** Drops the subscriptions made with another VAPID key than the current one: a rotated key cannot reach them. */
export function pruneForeignSubscriptions(ctx: StoreContext): void {
  ctx.db
    .prepare('DELETE FROM push_subscriptions WHERE vapid_key <> ?')
    .run(notifySecrets(ctx).vapid.publicKey);
}

/** Adds a browser subscription; one for the same endpoint is updated in place and keeps its ID. */
export function addPushSubscription(
  ctx: StoreContext,
  input: { endpoint: string; p256dh: string; auth: string; label: string },
  now: number,
): PushSubscriptionRecord {
  const vapid = notifySecrets(ctx).vapid.publicKey;
  const existing = ctx.db
    .prepare('SELECT id FROM push_subscriptions WHERE endpoint = ?')
    .get(input.endpoint) as { id: string } | undefined;
  if (existing) {
    ctx.db
      .prepare(
        'UPDATE push_subscriptions SET p256dh = ?, auth = ?, label = ?, vapid_key = ? WHERE id = ?',
      )
      .run(input.p256dh, input.auth, input.label, vapid, existing.id);
  } else {
    ctx.db
      .prepare(
        'INSERT INTO push_subscriptions(id, endpoint, p256dh, auth, label, vapid_key, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)',
      )
      .run(newId(), input.endpoint, input.p256dh, input.auth, input.label, vapid, now);
  }
  const row = ctx.db
    .prepare(`SELECT ${PUSH_COLUMNS} FROM push_subscriptions WHERE endpoint = ?`)
    .get(input.endpoint) as unknown as PushRow;
  return pushRecord(row);
}

export function listPushSubscriptions(ctx: StoreContext): PushSubscriptionRecord[] {
  return (
    ctx.db
      .prepare(`SELECT ${PUSH_COLUMNS} FROM push_subscriptions ORDER BY created_at, id`)
      .all() as unknown as PushRow[]
  ).map(pushRecord);
}

export function deletePushSubscription(ctx: StoreContext, id: string): boolean {
  return ctx.db.prepare('DELETE FROM push_subscriptions WHERE id = ?').run(id).changes > 0;
}

export function deletePushByEndpoint(ctx: StoreContext, endpoint: string): boolean {
  return (
    ctx.db.prepare('DELETE FROM push_subscriptions WHERE endpoint = ?').run(endpoint).changes > 0
  );
}

export function recordPushDelivery(ctx: StoreContext, id: string, d: NotifyDelivery): void {
  ctx.db
    .prepare('UPDATE push_subscriptions SET last_at = ?, last_ok = ?, last_error = ? WHERE id = ?')
    .run(d.at, d.ok ? 1 : 0, d.error ?? null, id);
}

interface WebhookRow {
  id: string;
  url: string;
  label: string;
  enabled: number;
  events: string;
  created_at: number;
  last_at: number | null;
  last_ok: number | null;
  last_error: string | null;
}

export interface WebhookRecord {
  id: string;
  url: string;
  label: string;
  enabled: boolean;
  /** The event kinds this webhook receives, in the order of `NOTIFY_EVENT_KINDS`. */
  events: NotifyEventKind[];
  createdAt: number;
  hasSecret: boolean;
  lastDelivery?: NotifyDelivery;
}

const WEBHOOK_COLUMNS = 'id, url, label, enabled, events, created_at, last_at, last_ok, last_error';

/** The kinds in the order of `NOTIFY_EVENT_KINDS`, each once. */
function orderedEvents(kinds: readonly NotifyEventKind[]): NotifyEventKind[] {
  return NOTIFY_EVENT_KINDS.filter((kind) => kinds.includes(kind));
}

function parseEvents(json: string): NotifyEventKind[] {
  try {
    const raw: unknown = JSON.parse(json);
    if (Array.isArray(raw)) {
      const kinds = orderedEvents(raw as NotifyEventKind[]);
      if (kinds.length > 0) return kinds;
    }
  } catch {
    // Unreadable events are the defaults.
  }
  return [...DEFAULT_WEBHOOK_EVENTS];
}

function webhookRecord(ctx: StoreContext, row: WebhookRow): WebhookRecord {
  const last = delivery(row);
  return {
    id: row.id,
    url: row.url,
    label: row.label,
    enabled: row.enabled === 1,
    events: parseEvents(row.events),
    createdAt: row.created_at,
    hasSecret: webhookSecret(ctx, row.id) !== null,
    ...(last ? { lastDelivery: last } : {}),
  };
}

export function listWebhooks(ctx: StoreContext): WebhookRecord[] {
  return (
    ctx.db
      .prepare(`SELECT ${WEBHOOK_COLUMNS} FROM webhooks ORDER BY created_at, id`)
      .all() as unknown as WebhookRow[]
  ).map((row) => webhookRecord(ctx, row));
}

export function getWebhook(ctx: StoreContext, id: string): WebhookRecord | null {
  const row = ctx.db.prepare(`SELECT ${WEBHOOK_COLUMNS} FROM webhooks WHERE id = ?`).get(id) as
    | WebhookRow
    | undefined;
  return row ? webhookRecord(ctx, row) : null;
}

export function addWebhook(
  ctx: StoreContext,
  input: { url: string; label: string; secret?: string; events?: readonly NotifyEventKind[] },
  now: number,
): WebhookRecord {
  const id = newId();
  const events = orderedEvents(input.events ?? DEFAULT_WEBHOOK_EVENTS);
  ctx.db
    .prepare(
      'INSERT INTO webhooks(id, url, label, enabled, events, created_at) VALUES(?, ?, ?, 1, ?, ?)',
    )
    .run(id, input.url, input.label, JSON.stringify(events), now);
  if (input.secret !== undefined) setWebhookSecret(ctx, id, input.secret);
  return getWebhook(ctx, id) as WebhookRecord;
}

export function updateWebhook(
  ctx: StoreContext,
  id: string,
  patch: {
    url?: string;
    label?: string;
    enabled?: boolean;
    secret?: string | null;
    events?: readonly NotifyEventKind[];
  },
): WebhookRecord | null {
  if (!getWebhook(ctx, id)) return null;
  if (patch.url !== undefined)
    ctx.db.prepare('UPDATE webhooks SET url = ? WHERE id = ?').run(patch.url, id);
  if (patch.label !== undefined)
    ctx.db.prepare('UPDATE webhooks SET label = ? WHERE id = ?').run(patch.label, id);
  if (patch.enabled !== undefined)
    ctx.db.prepare('UPDATE webhooks SET enabled = ? WHERE id = ?').run(patch.enabled ? 1 : 0, id);
  if (patch.events !== undefined)
    ctx.db
      .prepare('UPDATE webhooks SET events = ? WHERE id = ?')
      .run(JSON.stringify(orderedEvents(patch.events)), id);
  if (patch.secret !== undefined) setWebhookSecret(ctx, id, patch.secret);
  return getWebhook(ctx, id);
}

export function deleteWebhook(ctx: StoreContext, id: string): boolean {
  const gone = ctx.db.prepare('DELETE FROM webhooks WHERE id = ?').run(id).changes > 0;
  if (gone) setWebhookSecret(ctx, id, null);
  return gone;
}

export function recordWebhookDelivery(ctx: StoreContext, id: string, d: NotifyDelivery): void {
  ctx.db
    .prepare('UPDATE webhooks SET last_at = ?, last_ok = ?, last_error = ? WHERE id = ?')
    .run(d.at, d.ok ? 1 : 0, d.error ?? null, id);
}

/** Marks an action token used; false when it was already. Expired entries are pruned on the way. */
export function useActionToken(
  ctx: StoreContext,
  tokenId: string,
  expiresAt: number,
  now: number,
): boolean {
  ctx.db.prepare('DELETE FROM action_tokens WHERE expires_at < ?').run(now);
  return (
    ctx.db
      .prepare('INSERT OR IGNORE INTO action_tokens(id, expires_at) VALUES(?, ?)')
      .run(tokenId, expiresAt).changes > 0
  );
}

export function actionTokenUsed(ctx: StoreContext, tokenId: string): boolean {
  return ctx.db.prepare('SELECT 1 FROM action_tokens WHERE id = ?').get(tokenId) !== undefined;
}
