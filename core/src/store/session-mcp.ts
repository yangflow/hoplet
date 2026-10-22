// Sessions that direct sessions (#67, ADR 0034): each session's settings, the defaults they start from, and the
// chain of sessions behind the prompt a session is running.
import {
  type SessionMcpProjectDefault,
  type SessionMcpSettings,
  SessionVisibilitySchema,
  safeParse,
} from '@hoplet/shared';
import { auditedChange } from './audit.ts';
import type { StoreContext } from './layout.ts';

interface SettingsRow {
  send: number;
  create_sessions: number;
  request_handoff: number;
  visibility: string;
}

function settingsOf(row: SettingsRow): SessionMcpSettings {
  const visibility = safeParse(SessionVisibilitySchema, row.visibility);
  return {
    send: row.send === 1,
    create: row.create_sessions === 1,
    requestHandoff: row.request_handoff === 1,
    // A damaged value reaches the least.
    visibility: visibility.success ? visibility.output : 'project',
  };
}

/** `create` implies `send`: the stored value never holds the one without the other. */
export function normalized(settings: SessionMcpSettings): SessionMcpSettings {
  return settings.create && !settings.send ? { ...settings, send: true } : settings;
}

function values(s: SessionMcpSettings): [number, number, number, string] {
  return [s.send ? 1 : 0, s.create ? 1 : 0, s.requestHandoff ? 1 : 0, s.visibility];
}

const SETTINGS_COLUMNS = 'send, create_sessions, request_handoff, visibility';

export function getSessionSettings(
  ctx: StoreContext,
  sessionId: string,
): SessionMcpSettings | null {
  const row = ctx.db
    .prepare(`SELECT ${SETTINGS_COLUMNS} FROM session_mcp_settings WHERE session_id = ?`)
    .get(sessionId) as SettingsRow | undefined;
  return row ? settingsOf(row) : null;
}

/**
 * Stores what a session was given when it first needed settings (resolved from a default). Not an audit event: the
 * user changed nothing. A session that already has settings keeps them.
 */
export function seedSessionSettings(
  ctx: StoreContext,
  sessionId: string,
  settings: SessionMcpSettings,
  now: number,
): void {
  ctx.db
    .prepare(
      `INSERT OR IGNORE INTO session_mcp_settings(session_id, send, create_sessions, request_handoff, visibility, updated_at)
       VALUES(?, ?, ?, ?, ?, ?)`,
    )
    .run(sessionId, ...values(normalized(settings)), now);
}

/** The user's change of one session's settings, recorded as `config.changed`. */
export function putSessionSettings(
  ctx: StoreContext,
  sessionId: string,
  settings: SessionMcpSettings,
  previous: SessionMcpSettings,
  now: number,
): SessionMcpSettings {
  const s = normalized(settings);
  return auditedChange(
    ctx,
    () => {
      ctx.db
        .prepare(
          `INSERT INTO session_mcp_settings(session_id, send, create_sessions, request_handoff, visibility, updated_at)
           VALUES(?, ?, ?, ?, ?, ?)
           ON CONFLICT(session_id) DO UPDATE SET send = excluded.send, create_sessions = excluded.create_sessions,
             request_handoff = excluded.request_handoff, visibility = excluded.visibility,
             updated_at = excluded.updated_at`,
        )
        .run(sessionId, ...values(s), now);
      return s;
    },
    () => ({
      type: 'config.changed',
      sessionId,
      summary: { area: 'session-mcp', action: 'updated', previous, current: s },
    }),
  );
}

/** The default of a project (`deviceId` and `path`), or the global one ('' and ''); null when none is set. */
export function getSessionDefault(
  ctx: StoreContext,
  deviceId: string,
  path: string,
): SessionMcpSettings | null {
  const row = ctx.db
    .prepare(
      `SELECT ${SETTINGS_COLUMNS} FROM session_mcp_defaults WHERE device_id = ? AND project_path = ?`,
    )
    .get(deviceId, path) as SettingsRow | undefined;
  return row ? settingsOf(row) : null;
}

export function putSessionDefault(
  ctx: StoreContext,
  deviceId: string,
  path: string,
  settings: SessionMcpSettings,
  now: number,
): SessionMcpSettings {
  const s = normalized(settings);
  const previous = getSessionDefault(ctx, deviceId, path);
  return auditedChange(
    ctx,
    () => {
      ctx.db
        .prepare(
          `INSERT INTO session_mcp_defaults(device_id, project_path, send, create_sessions, request_handoff, visibility, updated_at)
           VALUES(?, ?, ?, ?, ?, ?, ?)
           ON CONFLICT(device_id, project_path) DO UPDATE SET send = excluded.send,
             create_sessions = excluded.create_sessions, request_handoff = excluded.request_handoff,
             visibility = excluded.visibility, updated_at = excluded.updated_at`,
        )
        .run(deviceId, path, ...values(s), now);
      return s;
    },
    () => ({
      type: 'config.changed',
      ...(deviceId === '' ? {} : { deviceId }),
      summary: {
        area: 'session-mcp-defaults',
        action: previous ? 'updated' : 'created',
        scope: deviceId === '' ? 'global' : 'project',
        ...(deviceId === '' ? {} : { path }),
        previous,
        current: s,
      },
    }),
  );
}

/** Removes a project's default; true when there was one. */
export function deleteSessionDefault(ctx: StoreContext, deviceId: string, path: string): boolean {
  return auditedChange(
    ctx,
    () =>
      Number(
        ctx.db
          .prepare(`DELETE FROM session_mcp_defaults WHERE device_id = ? AND project_path = ?`)
          .run(deviceId, path).changes,
      ) > 0,
    (removed) =>
      removed
        ? {
            type: 'config.changed',
            deviceId,
            summary: { area: 'session-mcp-defaults', action: 'removed', scope: 'project', path },
          }
        : null,
  );
}

export function listProjectDefaults(ctx: StoreContext): SessionMcpProjectDefault[] {
  const rows = ctx.db
    .prepare(
      `SELECT device_id, project_path, ${SETTINGS_COLUMNS} FROM session_mcp_defaults
       WHERE device_id != '' ORDER BY device_id, project_path`,
    )
    .all() as unknown as (SettingsRow & { device_id: string; project_path: string })[];
  return rows.map((row) => ({
    deviceId: row.device_id,
    path: row.project_path,
    settings: settingsOf(row),
  }));
}

/** The sessions that directed the prompt `sessionId` is running, nearest last; empty for a prompt of a user. */
export function getChain(ctx: StoreContext, sessionId: string): string[] {
  const row = ctx.db
    .prepare(`SELECT chain FROM session_provenance WHERE session_id = ?`)
    .get(sessionId) as { chain: string } | undefined;
  if (!row) return [];
  try {
    const parsed: unknown = JSON.parse(row.chain);
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : [];
  } catch {
    return [];
  }
}

/** Records the chain behind the prompt; an empty chain (a prompt of a user) removes the record. */
export function setChain(ctx: StoreContext, sessionId: string, chain: string[]): void {
  if (chain.length === 0) {
    ctx.db.prepare(`DELETE FROM session_provenance WHERE session_id = ?`).run(sessionId);
    return;
  }
  ctx.db
    .prepare(
      `INSERT INTO session_provenance(session_id, chain) VALUES(?, ?)
       ON CONFLICT(session_id) DO UPDATE SET chain = excluded.chain`,
    )
    .run(sessionId, JSON.stringify(chain));
}

/** Forgets a session's settings and provenance (the session was deleted). */
export function forgetSession(ctx: StoreContext, sessionId: string): void {
  ctx.db.prepare(`DELETE FROM session_mcp_settings WHERE session_id = ?`).run(sessionId);
  ctx.db.prepare(`DELETE FROM session_provenance WHERE session_id = ?`).run(sessionId);
}
