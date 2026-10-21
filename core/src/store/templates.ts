// Session templates (#76): what a person types at the start of a session again and again. The placeholders of
// `text` are checked by the caller; the store keeps names unique and records every change in the audit log.
import { randomBytes } from 'node:crypto';
import {
  parseTemplate,
  type SessionConfig,
  type Template,
  type TemplateBody,
} from '@hoplet/shared';
import { appendAudit } from './audit.ts';
import type { StoreContext } from './layout.ts';

interface Row {
  id: string;
  name: string;
  text: string;
  device_id: string | null;
  project_path: string | null;
  provider: string;
  configuration: string | null;
  config: string | null;
  created_at: number;
  updated_at: number;
}

function toTemplate(row: Row): Template {
  const parsed = parseTemplate(row.text);
  return {
    id: row.id,
    name: row.name,
    text: row.text,
    ...(row.device_id !== null ? { deviceId: row.device_id } : {}),
    ...(row.project_path !== null ? { projectPath: row.project_path } : {}),
    provider: row.provider as Template['provider'],
    ...(row.configuration !== null ? { configuration: row.configuration } : {}),
    ...(row.config !== null ? { config: JSON.parse(row.config) as SessionConfig } : {}),
    placeholders: parsed.ok ? parsed.placeholders : [],
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

export function listTemplates(ctx: StoreContext): Template[] {
  const rows = ctx.db
    .prepare('SELECT * FROM templates ORDER BY name COLLATE NOCASE, id')
    .all() as unknown as Row[];
  return rows.map(toTemplate);
}

export function getTemplate(ctx: StoreContext, id: string): Template | null {
  const row = ctx.db.prepare('SELECT * FROM templates WHERE id = ?').get(id) as Row | undefined;
  return row ? toTemplate(row) : null;
}

export function templateCount(ctx: StoreContext): number {
  return Number((ctx.db.prepare('SELECT COUNT(*) AS n FROM templates').get() as { n: number }).n);
}

/** The audit log records names, machine, directory and provider, and which fields changed; never the text. */
function auditFields(body: TemplateBody): Record<string, unknown> {
  return {
    name: body.name,
    deviceId: body.deviceId ?? null,
    projectPath: body.projectPath ?? null,
    provider: body.provider,
    configuration: body.configuration ?? null,
  };
}

function rowBody(row: Row): TemplateBody {
  return {
    name: row.name,
    text: row.text,
    ...(row.device_id !== null ? { deviceId: row.device_id } : {}),
    ...(row.project_path !== null ? { projectPath: row.project_path } : {}),
    provider: row.provider as TemplateBody['provider'],
    ...(row.configuration !== null ? { configuration: row.configuration } : {}),
    ...(row.config !== null ? { config: JSON.parse(row.config) as SessionConfig } : {}),
  };
}

function nameTaken(ctx: StoreContext, name: string, exceptId?: string): boolean {
  const row = ctx.db.prepare('SELECT id FROM templates WHERE name = ?').get(name) as
    | { id: string }
    | undefined;
  return row !== undefined && row.id !== exceptId;
}

function insert(ctx: StoreContext, body: TemplateBody, now: number): string {
  const id = randomBytes(9).toString('base64url');
  ctx.db
    .prepare(
      `INSERT INTO templates(id, name, text, device_id, project_path, provider, configuration, config, created_at, updated_at)
       VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .run(
      id,
      body.name,
      body.text,
      body.deviceId ?? null,
      body.projectPath ?? null,
      body.provider,
      body.configuration ?? null,
      body.config ? JSON.stringify(body.config) : null,
      now,
      now,
    );
  appendAudit(ctx, {
    type: 'config.changed',
    summary: {
      area: 'templates',
      id,
      action: 'created',
      fields: Object.keys(auditFields(body)),
      current: auditFields(body),
    },
  });
  return id;
}

/** Creates a template; `name-taken` when another has the name. */
export function createTemplate(
  ctx: StoreContext,
  body: TemplateBody,
  now: number,
): Template | 'name-taken' {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    if (nameTaken(ctx, body.name)) {
      ctx.db.exec('ROLLBACK');
      return 'name-taken';
    }
    const id = insert(ctx, body, now);
    ctx.db.exec('COMMIT');
    const created = getTemplate(ctx, id);
    if (!created) throw new Error('template vanished');
    return created;
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

/** Replaces a template's definition; null when there is none with that ID, `name-taken` on a clash. */
export function updateTemplate(
  ctx: StoreContext,
  id: string,
  body: TemplateBody,
  now: number,
): Template | null | 'name-taken' {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const old = ctx.db.prepare('SELECT * FROM templates WHERE id = ?').get(id) as Row | undefined;
    if (!old) {
      ctx.db.exec('ROLLBACK');
      return null;
    }
    if (nameTaken(ctx, body.name, id)) {
      ctx.db.exec('ROLLBACK');
      return 'name-taken';
    }
    ctx.db
      .prepare(
        `UPDATE templates SET name = ?, text = ?, device_id = ?, project_path = ?, provider = ?, configuration = ?,
           config = ?, updated_at = ? WHERE id = ?`,
      )
      .run(
        body.name,
        body.text,
        body.deviceId ?? null,
        body.projectPath ?? null,
        body.provider,
        body.configuration ?? null,
        body.config ? JSON.stringify(body.config) : null,
        now,
        id,
      );
    const before = rowBody(old);
    const beforeAudit = auditFields(before);
    const afterAudit = auditFields(body);
    const changed = Object.keys(afterAudit).filter((key) => beforeAudit[key] !== afterAudit[key]);
    if (before.text !== body.text) changed.push('text');
    if (JSON.stringify(before.config ?? null) !== JSON.stringify(body.config ?? null)) {
      changed.push('config');
    }
    appendAudit(ctx, {
      type: 'config.changed',
      summary: {
        area: 'templates',
        id,
        action: 'updated',
        fields: changed,
        previous: Object.fromEntries(
          changed.filter((key) => key in beforeAudit).map((key) => [key, beforeAudit[key]]),
        ),
        current: Object.fromEntries(
          changed.filter((key) => key in afterAudit).map((key) => [key, afterAudit[key]]),
        ),
      },
    });
    ctx.db.exec('COMMIT');
    return getTemplate(ctx, id);
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

export function deleteTemplate(ctx: StoreContext, id: string): boolean {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const result = ctx.db.prepare('DELETE FROM templates WHERE id = ?').run(id);
    const deleted = Number(result.changes) > 0;
    if (deleted) {
      appendAudit(ctx, {
        type: 'config.changed',
        summary: { area: 'templates', id, action: 'deleted', fields: [] },
      });
    }
    ctx.db.exec('COMMIT');
    return deleted;
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

/** The first of `name`, `name (2)`, `name (3)`, ... that is not in `taken`; the suffix keeps within 80 characters. */
export function freeName(name: string, taken: ReadonlySet<string>): string {
  if (!taken.has(name)) return name;
  for (let n = 2; ; n += 1) {
    const suffix = ` (${n})`;
    const candidate = `${name.slice(0, 80 - suffix.length)}${suffix}`;
    if (!taken.has(candidate)) return candidate;
  }
}

/** The names in use, for choosing the names an import gives. */
export function templateNames(ctx: StoreContext): Set<string> {
  return new Set(
    (ctx.db.prepare('SELECT name FROM templates').all() as { name: string }[]).map((r) => r.name),
  );
}

/** Stores all of `bodies` or none; their names are already free and distinct. */
export function createTemplates(
  ctx: StoreContext,
  bodies: readonly TemplateBody[],
  now: number,
): Template[] {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const ids = bodies.map((body) => insert(ctx, body, now));
    ctx.db.exec('COMMIT');
    return ids.map((id) => {
      const created = getTemplate(ctx, id);
      if (!created) throw new Error('template vanished');
      return created;
    });
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}
