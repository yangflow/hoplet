import { randomBytes } from 'node:crypto';
import {
  type HookDeliveryState,
  HookEventsSchema,
  type HookEventType,
  type HookScope,
  safeParse,
} from '@hoplet/shared';
import { appendAudit } from './audit.ts';
import type { StoreContext } from './layout.ts';
import { setHookSecret } from './secrets.ts';

/** Deliveries a hook may have waiting; past it the oldest waiting one is dropped. */
export const MAX_PENDING_PER_HOOK = 1000;
/** Settled deliveries kept per hook, and for how long. */
export const MAX_DELIVERIES_PER_HOOK = 500;
export const DELIVERY_KEEP_MS = 14 * 86_400_000;

export interface HookRecord {
  id: string;
  url: string;
  label: string;
  enabled: boolean;
  events: HookEventType[];
  scope: HookScope;
  summary: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface HookInput {
  url: string;
  label: string;
  enabled: boolean;
  events: HookEventType[];
  scope: HookScope;
  summary: boolean;
}

export interface DeliveryRecord {
  id: string;
  hookId: string;
  eventId: string;
  type: string;
  state: HookDeliveryState;
  attempts: number;
  error?: string;
  body: string;
  createdAt: number;
  settledAt?: number;
  nextAttemptAt?: number;
  expiresAt: number;
}

interface HookRow {
  id: string;
  url: string;
  label: string;
  enabled: number;
  events: string;
  scope_device_id: string | null;
  scope_project_path: string | null;
  summary: number;
  created_at: number;
  updated_at: number;
}

interface DeliveryRow {
  id: string;
  hook_id: string;
  event_id: string;
  type: string;
  state: HookDeliveryState;
  attempts: number;
  last_error: string | null;
  body: string;
  created_at: number;
  settled_at: number | null;
  next_attempt_at: number | null;
  expires_at: number;
}

function newId(): string {
  return randomBytes(9).toString('base64url');
}

function hook(row: HookRow): HookRecord {
  let events: HookEventType[] = [];
  try {
    const parsed = safeParse(HookEventsSchema, JSON.parse(row.events));
    if (parsed.success) events = parsed.output;
  } catch {
    // A list that cannot be read receives nothing.
  }
  return {
    id: row.id,
    url: row.url,
    label: row.label,
    enabled: row.enabled === 1,
    events,
    scope:
      row.scope_device_id !== null && row.scope_project_path !== null
        ? { kind: 'project', deviceId: row.scope_device_id, projectPath: row.scope_project_path }
        : { kind: 'all' },
    summary: row.summary === 1,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

function delivery(row: DeliveryRow): DeliveryRecord {
  return {
    id: row.id,
    hookId: row.hook_id,
    eventId: row.event_id,
    type: row.type,
    state: row.state,
    attempts: row.attempts,
    ...(row.last_error ? { error: row.last_error } : {}),
    body: row.body,
    createdAt: row.created_at,
    ...(row.settled_at !== null ? { settledAt: row.settled_at } : {}),
    ...(row.next_attempt_at !== null ? { nextAttemptAt: row.next_attempt_at } : {}),
    expiresAt: row.expires_at,
  };
}

export function listHooks(ctx: StoreContext): HookRecord[] {
  return (
    ctx.db.prepare('SELECT * FROM hooks ORDER BY created_at, id').all() as unknown as HookRow[]
  ).map(hook);
}

export function getHook(ctx: StoreContext, id: string): HookRecord | null {
  const row = ctx.db.prepare('SELECT * FROM hooks WHERE id = ?').get(id) as HookRow | undefined;
  return row ? hook(row) : null;
}

export function hookCount(ctx: StoreContext): number {
  return (ctx.db.prepare('SELECT COUNT(*) AS n FROM hooks').get() as { n: number }).n;
}

function columns(
  input: HookInput,
): [string, string, number, string, string | null, string | null, number] {
  return [
    input.url,
    input.label,
    input.enabled ? 1 : 0,
    JSON.stringify(input.events),
    input.scope.kind === 'project' ? input.scope.deviceId : null,
    input.scope.kind === 'project' ? input.scope.projectPath : null,
    input.summary ? 1 : 0,
  ];
}

/** Who and what changed, never the URL or the secret. */
function audit(ctx: StoreContext, id: string, action: string, fields: string[] = []): void {
  appendAudit(ctx, { type: 'config.changed', summary: { area: 'hooks', id, action, fields } });
}

/** Adds a hook and its signing secret in one audited step. */
export function createHook(
  ctx: StoreContext,
  input: HookInput,
  secret: string,
  now: number,
): HookRecord {
  const id = newId();
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db
      .prepare(
        `INSERT INTO hooks(id, url, label, enabled, events, scope_device_id, scope_project_path, summary,
           created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(id, ...columns(input), now, now);
    audit(ctx, id, 'created', ['label', 'events', 'scope', 'summary', 'enabled']);
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  setHookSecret(ctx, id, secret);
  const created = getHook(ctx, id);
  if (!created) throw new Error('hook vanished');
  return created;
}

export interface HookPatch {
  url?: string;
  label?: string;
  enabled?: boolean;
  events?: HookEventType[];
  scope?: HookScope;
  summary?: boolean;
}

export function updateHook(
  ctx: StoreContext,
  id: string,
  patch: HookPatch,
  now: number,
): HookRecord | null {
  const current = getHook(ctx, id);
  if (!current) return null;
  const next: HookInput = {
    url: patch.url ?? current.url,
    label: patch.label ?? current.label,
    enabled: patch.enabled ?? current.enabled,
    events: patch.events ?? current.events,
    scope: patch.scope ?? current.scope,
    summary: patch.summary ?? current.summary,
  };
  const fields = Object.keys(patch).filter(
    (k) => (patch as Record<string, unknown>)[k] !== undefined,
  );
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db
      .prepare(
        `UPDATE hooks SET url = ?, label = ?, enabled = ?, events = ?, scope_device_id = ?,
           scope_project_path = ?, summary = ?, updated_at = ? WHERE id = ?`,
      )
      .run(...columns(next), now, id);
    if (patch.enabled !== undefined && patch.enabled !== current.enabled) {
      audit(ctx, id, patch.enabled ? 'enabled' : 'disabled');
    }
    const edits = fields.filter((f) => f !== 'enabled');
    if (edits.length > 0) audit(ctx, id, 'edited', edits);
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  return getHook(ctx, id);
}

/** Removes a hook with its deliveries and secret; false when there is none. */
export function deleteHook(ctx: StoreContext, id: string): boolean {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const result = ctx.db.prepare('DELETE FROM hooks WHERE id = ?').run(id);
    if (Number(result.changes) === 0) {
      ctx.db.exec('ROLLBACK');
      return false;
    }
    ctx.db.prepare('DELETE FROM hook_deliveries WHERE hook_id = ?').run(id);
    audit(ctx, id, 'deleted');
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  setHookSecret(ctx, id, null);
  return true;
}

/** Records that the secret of a hook was replaced (`rotated`) or that a test went out (`tested`). */
export function auditHookAction(ctx: StoreContext, id: string, action: 'rotated' | 'tested'): void {
  audit(ctx, id, action);
}

/**
 * Queues an event for a hook. Past `MAX_PENDING_PER_HOOK` waiting deliveries the oldest waiting one is
 * `dropped` (error `overflow`), so the newest are kept.
 */
export function enqueueDelivery(
  ctx: StoreContext,
  input: { hookId: string; eventId: string; type: string; body: string; expiresAt: number },
  now: number,
): DeliveryRecord {
  const id = newId();
  ctx.db
    .prepare(
      `INSERT INTO hook_deliveries(id, hook_id, event_id, type, state, attempts, body, created_at,
         next_attempt_at, expires_at) VALUES(?, ?, ?, ?, 'pending', 0, ?, ?, ?, ?)`,
    )
    .run(id, input.hookId, input.eventId, input.type, input.body, now, now, input.expiresAt);
  const over = pendingCount(ctx, input.hookId) - MAX_PENDING_PER_HOOK;
  if (over > 0) {
    ctx.db
      .prepare(
        `UPDATE hook_deliveries SET state = 'dropped', last_error = 'overflow', settled_at = ?,
           next_attempt_at = NULL WHERE id IN (SELECT id FROM hook_deliveries WHERE hook_id = ? AND
           state = 'pending' ORDER BY created_at, rowid LIMIT ?)`,
      )
      .run(now, input.hookId, over);
  }
  const row = getDelivery(ctx, id);
  if (!row) throw new Error('delivery vanished');
  return row;
}

export function getDelivery(ctx: StoreContext, id: string): DeliveryRecord | null {
  const row = ctx.db.prepare('SELECT * FROM hook_deliveries WHERE id = ?').get(id) as
    | DeliveryRow
    | undefined;
  return row ? delivery(row) : null;
}

/** Waiting deliveries whose time has come, oldest due first. */
export function dueDeliveries(ctx: StoreContext, now: number, limit: number): DeliveryRecord[] {
  return (
    ctx.db
      .prepare(
        `SELECT * FROM hook_deliveries WHERE state = 'pending' AND next_attempt_at <= ?
           ORDER BY next_attempt_at, rowid LIMIT ?`,
      )
      .all(now, limit) as unknown as DeliveryRow[]
  ).map(delivery);
}

/** When the earliest waiting delivery is due; null when none waits. */
export function nextDeliveryAt(ctx: StoreContext): number | null {
  const row = ctx.db
    .prepare("SELECT MIN(next_attempt_at) AS at FROM hook_deliveries WHERE state = 'pending'")
    .get() as { at: number | null };
  return row.at;
}

export function pendingCount(ctx: StoreContext, hookId: string): number {
  return (
    ctx.db
      .prepare("SELECT COUNT(*) AS n FROM hook_deliveries WHERE hook_id = ? AND state = 'pending'")
      .get(hookId) as { n: number }
  ).n;
}

export interface DeliveryOutcome {
  state: HookDeliveryState;
  attempts: number;
  error?: string;
  settledAt?: number;
  nextAttemptAt?: number;
}

export function recordDelivery(ctx: StoreContext, id: string, outcome: DeliveryOutcome): void {
  ctx.db
    .prepare(
      `UPDATE hook_deliveries SET state = ?, attempts = ?, last_error = ?, settled_at = ?,
         next_attempt_at = ? WHERE id = ?`,
    )
    .run(
      outcome.state,
      outcome.attempts,
      outcome.error ?? null,
      outcome.settledAt ?? null,
      outcome.nextAttemptAt ?? null,
      id,
    );
}

/** A hook's deliveries, newest first. */
export function listDeliveries(ctx: StoreContext, hookId: string, limit: number): DeliveryRecord[] {
  return (
    ctx.db
      .prepare(
        'SELECT * FROM hook_deliveries WHERE hook_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?',
      )
      .all(hookId, limit) as unknown as DeliveryRow[]
  ).map(delivery);
}

/** The latest settled delivery of a hook. */
export function lastSettled(ctx: StoreContext, hookId: string): DeliveryRecord | null {
  const row = ctx.db
    .prepare(
      `SELECT * FROM hook_deliveries WHERE hook_id = ? AND settled_at IS NOT NULL
         ORDER BY settled_at DESC, rowid DESC LIMIT 1`,
    )
    .get(hookId) as DeliveryRow | undefined;
  return row ? delivery(row) : null;
}

/** Removes settled deliveries older than 14 days, and those past 500 per hook. */
export function pruneDeliveries(ctx: StoreContext, now: number): void {
  ctx.db
    .prepare("DELETE FROM hook_deliveries WHERE state != 'pending' AND settled_at < ?")
    .run(now - DELIVERY_KEEP_MS);
  const hooks = ctx.db.prepare('SELECT DISTINCT hook_id FROM hook_deliveries').all() as {
    hook_id: string;
  }[];
  for (const { hook_id } of hooks) {
    ctx.db
      .prepare(
        `DELETE FROM hook_deliveries WHERE hook_id = ? AND state != 'pending' AND id NOT IN (
           SELECT id FROM hook_deliveries WHERE hook_id = ? AND state != 'pending'
           ORDER BY settled_at DESC, rowid DESC LIMIT ?)`,
      )
      .run(hook_id, hook_id, MAX_DELIVERIES_PER_HOOK);
  }
}
