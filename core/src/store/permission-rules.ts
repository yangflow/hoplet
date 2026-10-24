// Permission rules (#98, ADR 0029): the rules the user made, the switches of the built-in ones, and a revision per
// machine that grows with every change that reaches it. The agent decides; the core only stores.
import { randomBytes } from 'node:crypto';
import {
  BUILTIN_PERMISSION_RULES,
  MAX_PERMISSION_RULES,
  type PermissionMatch,
  type PermissionRuleBody,
  type PermissionRuleEntry,
  type PermissionRuleListQuery,
  type PermissionScope,
} from '@hoplet/shared';
import { appendAudit } from './audit.ts';
import type { StoreContext } from './layout.ts';
import { allChildren } from './session-mcp.ts';
import { ownerDeviceId } from './sessions.ts';

interface Row {
  id: string;
  position: number;
  scope: string;
  device_id: string | null;
  project_path: string | null;
  session_id: string | null;
  tool: string;
  match: string;
  action: string;
  note: string;
  enabled: number;
  created_at: number;
  updated_at: number;
}

/** The `device_id` of the revision row that counts the changes reaching every machine. */
const EVERY_MACHINE = '';

function toEntry(row: Row): PermissionRuleEntry {
  return {
    id: row.id,
    tool: row.tool,
    match: JSON.parse(row.match) as PermissionMatch,
    action: row.action as PermissionRuleEntry['action'],
    scope: row.scope as PermissionScope,
    note: row.note,
    enabled: row.enabled !== 0,
    builtin: false,
    ...(row.device_id !== null ? { deviceId: row.device_id } : {}),
    ...(row.project_path !== null ? { projectPath: row.project_path } : {}),
    ...(row.session_id !== null ? { sessionId: row.session_id } : {}),
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

function builtinEntries(ctx: StoreContext): PermissionRuleEntry[] {
  const switches = new Map(
    (
      ctx.db.prepare('SELECT id, enabled, updated_at FROM permission_builtins').all() as {
        id: string;
        enabled: number;
        updated_at: number;
      }[]
    ).map((r) => [r.id, r]),
  );
  return BUILTIN_PERMISSION_RULES.map((rule) => {
    const changed = switches.get(rule.id);
    return {
      ...structuredClone(rule),
      enabled: changed ? changed.enabled !== 0 : rule.enabled,
      ...(changed ? { updatedAt: changed.updated_at } : {}),
    };
  });
}

/** The built-ins first, then the user's rules in the order they were made. */
export function listPermissionRules(
  ctx: StoreContext,
  query: PermissionRuleListQuery = {},
): PermissionRuleEntry[] {
  const rules = [
    ...builtinEntries(ctx),
    ...(
      ctx.db
        .prepare('SELECT * FROM permission_rules ORDER BY position, id')
        .all() as unknown as Row[]
    ).map(toEntry),
  ];
  return rules.filter((rule) => {
    if (query.scope !== undefined && rule.scope !== query.scope) return false;
    if (rule.scope === 'project') {
      if (query.deviceId !== undefined && rule.deviceId !== query.deviceId) return false;
      if (query.projectPath !== undefined && rule.projectPath !== query.projectPath) return false;
    }
    if (rule.scope === 'session') {
      if (query.sessionId !== undefined && rule.sessionId !== query.sessionId) return false;
    }
    return true;
  });
}

export function getPermissionRule(ctx: StoreContext, id: string): PermissionRuleEntry | null {
  const builtin = id.startsWith('default.');
  if (builtin) return builtinEntries(ctx).find((r) => r.id === id) ?? null;
  const row = ctx.db.prepare('SELECT * FROM permission_rules WHERE id = ?').get(id) as
    | Row
    | undefined;
  return row ? toEntry(row) : null;
}

export function permissionRuleCount(ctx: StoreContext): number {
  return Number(
    (ctx.db.prepare('SELECT COUNT(*) AS n FROM permission_rules').get() as { n: number }).n,
  );
}

/**
 * The revision of the rules that apply to a machine: the changes that reached every machine plus the changes that
 * reached this one. It only grows, so the agent can tell which set is newer.
 */
export function permissionRevision(ctx: StoreContext, deviceId: string): number {
  const rows = ctx.db
    .prepare('SELECT revision FROM permission_revisions WHERE device_id IN (?, ?)')
    .all(EVERY_MACHINE, deviceId) as { revision: number }[];
  return rows.reduce((sum, r) => sum + Number(r.revision), 0);
}

/**
 * The machines a rule reaches: every machine (`''`), the machine of a project, or the owner of a session. A
 * session's rules also reach the owners of the sessions it started, which obey its `deny` and `ask` rules (ADR 0029).
 */
function reach(ctx: StoreContext, rule: Pick<Row, 'scope' | 'device_id' | 'session_id'>): string[] {
  if (rule.scope === 'global') return [EVERY_MACHINE];
  if (rule.scope === 'project') return rule.device_id ? [rule.device_id] : [];
  if (!rule.session_id) return [];
  const targets = new Set<string>();
  const owner = ownerDeviceId(ctx, rule.session_id);
  if (owner) targets.add(owner);
  for (const [childId, child] of allChildren(ctx)) {
    if (!child.chain.includes(rule.session_id)) continue;
    const childOwner = ownerDeviceId(ctx, childId);
    if (childOwner) targets.add(childOwner);
  }
  return [...targets];
}

/**
 * The rules a session started by `ancestors` (root first, parent last) obeys besides its own: the enabled `deny`
 * and `ask` rules of the session scope of each ancestor, never an `allow`. A child is never more permissive than
 * its parent's rules say (ADR 0029, ADR 0035).
 */
export function inheritedPermissionRules(
  ctx: StoreContext,
  ancestors: readonly string[],
): PermissionRuleEntry[] {
  if (ancestors.length === 0) return [];
  const wanted = new Set(ancestors);
  return (
    ctx.db
      .prepare(
        `SELECT * FROM permission_rules WHERE scope = 'session' AND enabled = 1 AND action != 'allow'
         ORDER BY position, id`,
      )
      .all() as unknown as Row[]
  )
    .filter((row) => row.session_id !== null && wanted.has(row.session_id))
    .map(toEntry);
}

/** Counts a change that reached the machine without a rule changing, so the agent can tell its set is newer. */
export function bumpPermissionRevision(ctx: StoreContext, deviceId: string): void {
  bump(ctx, [deviceId]);
}

function bump(ctx: StoreContext, targets: Iterable<string>): void {
  for (const target of new Set(targets)) {
    ctx.db
      .prepare(
        `INSERT INTO permission_revisions(device_id, revision) VALUES(?, 1)
         ON CONFLICT(device_id) DO UPDATE SET revision = revision + 1`,
      )
      .run(target);
  }
}

/**
 * What the audit log keeps of a rule: its short fields. The match patterns and the note can hold a command or a
 * path, so a change to them is named in `fields` and its text is not recorded.
 */
function auditFields(rule: {
  scope: string;
  deviceId?: string | null | undefined;
  sessionId?: string | null | undefined;
  tool: string;
  action: string;
  enabled: boolean;
}): Record<string, string | boolean> {
  return {
    scope: rule.scope,
    tool: rule.tool,
    action: rule.action,
    enabled: rule.enabled,
    ...(rule.scope === 'project' && rule.deviceId ? { deviceId: rule.deviceId } : {}),
    ...(rule.scope === 'session' && rule.sessionId ? { sessionId: rule.sessionId } : {}),
  };
}

function bodyColumns(body: PermissionRuleBody) {
  return [
    body.scope,
    body.scope === 'project' ? (body.deviceId ?? null) : null,
    body.scope === 'project' ? (body.projectPath ?? null) : null,
    body.scope === 'session' ? (body.sessionId ?? null) : null,
    body.tool,
    JSON.stringify(body.match),
    body.action,
    body.note,
    body.enabled ? 1 : 0,
  ] as const;
}

export function createPermissionRule(
  ctx: StoreContext,
  body: PermissionRuleBody,
  now: number,
): PermissionRuleEntry {
  const id = randomBytes(9).toString('base64url');
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const next =
      Number(
        (
          ctx.db.prepare('SELECT COALESCE(MAX(position), 0) AS p FROM permission_rules').get() as {
            p: number;
          }
        ).p,
      ) + 1;
    const columns = bodyColumns(body);
    ctx.db
      .prepare(
        `INSERT INTO permission_rules(id, position, scope, device_id, project_path, session_id, tool, match, action,
           note, enabled, created_at, updated_at)
         VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(id, next, ...columns, now, now);
    bump(ctx, reach(ctx, { scope: columns[0], device_id: columns[1], session_id: columns[3] }));
    appendAudit(ctx, {
      type: 'config.changed',
      summary: {
        area: 'permission-rules',
        id,
        action: 'created',
        fields: [...Object.keys(auditFields(body)), 'match', 'note'],
        current: auditFields(body),
      },
    });
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  const created = getPermissionRule(ctx, id);
  if (!created) throw new Error('permission rule vanished');
  return created;
}

/** Replaces the definition of a rule the user made; null when there is none with that ID. */
export function updatePermissionRule(
  ctx: StoreContext,
  id: string,
  body: PermissionRuleBody,
  now: number,
): PermissionRuleEntry | null {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const old = ctx.db.prepare('SELECT * FROM permission_rules WHERE id = ?').get(id) as
      | Row
      | undefined;
    if (!old) {
      ctx.db.exec('ROLLBACK');
      return null;
    }
    const columns = bodyColumns(body);
    ctx.db
      .prepare(
        `UPDATE permission_rules SET scope = ?, device_id = ?, project_path = ?, session_id = ?, tool = ?, match = ?,
           action = ?, note = ?, enabled = ?, updated_at = ? WHERE id = ?`,
      )
      .run(...columns, now, id);
    bump(ctx, [
      ...reach(ctx, old),
      ...reach(ctx, { scope: columns[0], device_id: columns[1], session_id: columns[3] }),
    ]);
    const before = auditFields({
      scope: old.scope,
      deviceId: old.device_id,
      sessionId: old.session_id,
      tool: old.tool,
      action: old.action,
      enabled: old.enabled !== 0,
    });
    const after = auditFields(body);
    const changed = [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(
      (key) => before[key] !== after[key],
    );
    if (old.match !== columns[5]) changed.push('match');
    if (old.note !== body.note) changed.push('note');
    appendAudit(ctx, {
      type: 'config.changed',
      summary: {
        area: 'permission-rules',
        id,
        action: 'updated',
        fields: changed,
        previous: Object.fromEntries(
          changed.filter((key) => key in before).map((key) => [key, before[key]]),
        ),
        current: Object.fromEntries(
          changed.filter((key) => key in after).map((key) => [key, after[key]]),
        ),
      },
    });
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  return getPermissionRule(ctx, id);
}

/** Turns a built-in rule on or off; null when there is no such built-in. A change of nothing is not a change. */
export function setBuiltinEnabled(
  ctx: StoreContext,
  id: string,
  enabled: boolean,
  now: number,
): PermissionRuleEntry | null {
  const current = getPermissionRule(ctx, id);
  if (!current?.builtin) return null;
  if (current.enabled === enabled) return current;
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db
      .prepare(
        `INSERT INTO permission_builtins(id, enabled, updated_at) VALUES(?, ?, ?)
         ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
      )
      .run(id, enabled ? 1 : 0, now);
    bump(ctx, [EVERY_MACHINE]);
    appendAudit(ctx, {
      type: 'config.changed',
      summary: {
        area: 'permission-rules',
        id,
        action: 'updated',
        fields: ['enabled'],
        previous: { enabled: current.enabled },
        current: { enabled },
      },
    });
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  return getPermissionRule(ctx, id);
}

export function deletePermissionRule(ctx: StoreContext, id: string): boolean {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const old = ctx.db.prepare('SELECT * FROM permission_rules WHERE id = ?').get(id) as
      | Row
      | undefined;
    if (old) {
      ctx.db.prepare('DELETE FROM permission_rules WHERE id = ?').run(id);
      bump(ctx, reach(ctx, old));
      appendAudit(ctx, {
        type: 'config.changed',
        summary: {
          area: 'permission-rules',
          id,
          action: 'deleted',
          fields: [],
          previous: auditFields({
            scope: old.scope,
            deviceId: old.device_id,
            sessionId: old.session_id,
            tool: old.tool,
            action: old.action,
            enabled: old.enabled !== 0,
          }),
        },
      });
    }
    ctx.db.exec('COMMIT');
    return old !== undefined;
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

/** Drops the rules of a session that is being deleted; the caller holds the transaction and has not yet removed the session. */
export function dropSessionRules(ctx: StoreContext, sessionId: string): void {
  const rows = ctx.db
    .prepare('SELECT * FROM permission_rules WHERE session_id = ?')
    .all(sessionId) as unknown as Row[];
  if (rows.length === 0) return;
  ctx.db.prepare('DELETE FROM permission_rules WHERE session_id = ?').run(sessionId);
  bump(
    ctx,
    rows.flatMap((row) => reach(ctx, row)),
  );
}

/** Who a handoff's copy of project rules is for, as the audit log names it (ADR 0037). */
export interface RuleCopyLink {
  deviceId: string;
  fromDeviceId: string;
  sessionId: string;
}

function byAction(actions: readonly string[]): Record<string, number> {
  const counts: Record<string, number> = { deny: 0, ask: 0, allow: 0 };
  for (const action of actions) counts[action] = (counts[action] ?? 0) + 1;
  return counts;
}

/**
 * Stores the project rules a handoff copies to its target, in one transaction with one `config.changed` record
 * that names the machines, the session and the counts and never a pattern. Null, with nothing stored, when the
 * copies would take the host past `MAX_PERMISSION_RULES`.
 */
export function copyPermissionRules(
  ctx: StoreContext,
  bodies: readonly PermissionRuleBody[],
  link: RuleCopyLink,
  now: number,
): string[] | null {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    if (permissionRuleCount(ctx) + bodies.length > MAX_PERMISSION_RULES) {
      ctx.db.exec('ROLLBACK');
      return null;
    }
    let position = Number(
      (
        ctx.db.prepare('SELECT COALESCE(MAX(position), 0) AS p FROM permission_rules').get() as {
          p: number;
        }
      ).p,
    );
    const insert = ctx.db.prepare(
      `INSERT INTO permission_rules(id, position, scope, device_id, project_path, session_id, tool, match, action,
         note, enabled, created_at, updated_at)
       VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    );
    const ids: string[] = [];
    for (const body of bodies) {
      const id = randomBytes(9).toString('base64url');
      position += 1;
      insert.run(id, position, ...bodyColumns(body), now, now);
      ids.push(id);
    }
    bump(ctx, [link.deviceId]);
    appendAudit(ctx, {
      type: 'config.changed',
      sessionId: link.sessionId,
      deviceId: link.deviceId,
      summary: {
        area: 'permission-rules',
        action: 'copied',
        deviceId: link.deviceId,
        fromDeviceId: link.fromDeviceId,
        sessionId: link.sessionId,
        count: bodies.length,
        byAction: byAction(bodies.map((b) => b.action)),
      },
    });
    ctx.db.exec('COMMIT');
    return ids;
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

/** Deletes the rules a failed handoff copied, with one `config.changed` record of the removal. */
export function removeCopiedPermissionRules(
  ctx: StoreContext,
  ids: readonly string[],
  link: RuleCopyLink,
): void {
  if (ids.length === 0) return;
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const actions: string[] = [];
    const remove = ctx.db.prepare('DELETE FROM permission_rules WHERE id = ? RETURNING action');
    for (const id of ids) {
      const row = remove.get(id) as { action: string } | undefined;
      if (row) actions.push(row.action);
    }
    bump(ctx, [link.deviceId]);
    if (actions.length > 0) {
      appendAudit(ctx, {
        type: 'config.changed',
        sessionId: link.sessionId,
        deviceId: link.deviceId,
        summary: {
          area: 'permission-rules',
          action: 'removed',
          deviceId: link.deviceId,
          fromDeviceId: link.fromDeviceId,
          sessionId: link.sessionId,
          count: actions.length,
          byAction: byAction(actions),
        },
      });
    }
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}
