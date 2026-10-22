import type { HookAnnounce } from '@hoplet/shared';
import { appendAudit } from './audit.ts';
import type { StoreContext } from './layout.ts';

/** The id of a script hook in the REST API and in `hook_deliveries.hook_id`. */
export function scriptHookId(deviceId: string, slug: string): string {
  return `script:${deviceId}:${slug}`;
}

export interface ScriptHookRecord {
  deviceId: string;
  /** The slug of the entry in the agent's `config.json`. */
  id: string;
  command: string;
  events: string[];
  projects: string[];
  timeoutMs: number;
  env: string[];
  runs: number;
  failed: number;
  dropped: number;
  enabled: boolean;
  updatedAt: number;
}

interface Row {
  device_id: string;
  id: string;
  command: string;
  events: string;
  projects: string;
  timeout_ms: number;
  env: string;
  runs: number;
  failed: number;
  dropped: number;
  enabled: number;
  updated_at: number;
}

function strings(json: string): string[] {
  try {
    const parsed: unknown = JSON.parse(json);
    return Array.isArray(parsed) ? parsed.filter((s): s is string => typeof s === 'string') : [];
  } catch {
    return [];
  }
}

function record(row: Row): ScriptHookRecord {
  return {
    deviceId: row.device_id,
    id: row.id,
    command: row.command,
    events: strings(row.events),
    projects: strings(row.projects),
    timeoutMs: row.timeout_ms,
    env: strings(row.env),
    runs: row.runs,
    failed: row.failed,
    dropped: row.dropped,
    enabled: row.enabled === 1,
    updatedAt: row.updated_at,
  };
}

/**
 * Takes what a device announced: new hooks are enabled, known ones keep their switch, and one that is not
 * announced any more is forgotten.
 */
export function replaceScriptHooks(
  ctx: StoreContext,
  deviceId: string,
  announced: readonly HookAnnounce[],
  now: number,
): void {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const keep = new Set(announced.map((h) => h.id));
    for (const row of ctx.db
      .prepare('SELECT id FROM script_hooks WHERE device_id = ?')
      .all(deviceId) as { id: string }[]) {
      if (!keep.has(row.id)) {
        ctx.db
          .prepare('DELETE FROM script_hooks WHERE device_id = ? AND id = ?')
          .run(deviceId, row.id);
      }
    }
    const upsert = ctx.db.prepare(
      `INSERT INTO script_hooks(device_id, id, command, events, projects, timeout_ms, env, runs, failed,
         dropped, enabled, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
         ON CONFLICT(device_id, id) DO UPDATE SET command = excluded.command, events = excluded.events,
           projects = excluded.projects, timeout_ms = excluded.timeout_ms, env = excluded.env,
           runs = excluded.runs, failed = excluded.failed, dropped = excluded.dropped,
           updated_at = excluded.updated_at`,
    );
    for (const h of announced) {
      upsert.run(
        deviceId,
        h.id,
        h.command,
        JSON.stringify(h.events),
        JSON.stringify(h.projects),
        h.timeoutMs,
        JSON.stringify(h.env),
        h.runs,
        h.failed,
        h.dropped,
        now,
      );
    }
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

export function listScriptHooks(ctx: StoreContext): ScriptHookRecord[] {
  return (
    ctx.db.prepare('SELECT * FROM script_hooks ORDER BY device_id, id').all() as unknown as Row[]
  ).map(record);
}

export function getScriptHook(
  ctx: StoreContext,
  deviceId: string,
  slug: string,
): ScriptHookRecord | null {
  const row = ctx.db
    .prepare('SELECT * FROM script_hooks WHERE device_id = ? AND id = ?')
    .get(deviceId, slug) as Row | undefined;
  return row ? record(row) : null;
}

function audit(ctx: StoreContext, deviceId: string, slug: string, action: string): void {
  appendAudit(ctx, {
    type: 'config.changed',
    deviceId,
    summary: { area: 'hooks', id: scriptHookId(deviceId, slug), action, fields: [] },
  });
}

/** Switches a script hook and audits the change with the machine; null when the device has no such hook. */
export function setScriptHookEnabled(
  ctx: StoreContext,
  deviceId: string,
  slug: string,
  enabled: boolean,
): ScriptHookRecord | null {
  const current = getScriptHook(ctx, deviceId, slug);
  if (!current) return null;
  if (current.enabled === enabled) return current;
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db
      .prepare('UPDATE script_hooks SET enabled = ? WHERE device_id = ? AND id = ?')
      .run(enabled ? 1 : 0, deviceId, slug);
    audit(ctx, deviceId, slug, enabled ? 'enabled' : 'disabled');
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  return getScriptHook(ctx, deviceId, slug);
}

/** Records that a test went out; the audit entry names the hook and the machine, nothing else. */
export function auditScriptHookTest(ctx: StoreContext, deviceId: string, slug: string): void {
  audit(ctx, deviceId, slug, 'tested');
}

/**
 * Settles the deliveries of script hooks that still wait for a result: those of one device when its link
 * drops (`connection-lost`), and all of them when the core starts (`interrupted`).
 */
export function settlePendingScriptDeliveries(
  ctx: StoreContext,
  deviceId: string | null,
  error: string,
  now: number,
): void {
  const prefix = deviceId === null ? 'script:' : `script:${deviceId}:`;
  ctx.db
    .prepare(
      `UPDATE hook_deliveries SET state = 'failed', last_error = ?, settled_at = ?, next_attempt_at = NULL
         WHERE state = 'pending' AND instr(hook_id, ?) = 1 AND substr(hook_id, 1, 7) = 'script:'`,
    )
    .run(error, now, prefix);
}
