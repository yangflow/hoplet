// The audit log (#80, ADR 0030): one global, append-only hash chain of the actions people and the core took.
// Every string of a summary is scrubbed before the hash is computed, so the chain covers what is stored.
import { createHash } from 'node:crypto';
import {
  AUDIT_SUMMARY_MAX_BYTES,
  type AuditActorKind,
  type AuditBreakReason,
  type AuditChannel,
  type AuditEvent,
  type AuditEventType,
  type AuditFilter,
  type AuditHead,
  type AuditVerifyResponse,
} from '@hoplet/shared';
import { scrubText } from '../scrub/index.ts';
import type { StoreContext } from './layout.ts';

/** The `prev_hash` of the first record. */
export const AUDIT_GENESIS_HASH = '0'.repeat(64);

/** Records one retention transaction removes at most. */
export const AUDIT_PRUNE_BATCH = 10_000;

/** Records one verification batch reads before it yields. */
const VERIFY_BATCH = 1000;

const DAY_MS = 86_400_000;
const MAX_TEXT_CHARS = 500;
const MAX_ARRAY_ITEMS = 20;
const MAX_DEPTH = 4;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
/** A SHA-256 hex digest, as the checkpoint keeps; the scrubber would take it for a key. */
const DIGEST = /^[0-9a-f]{64}$/;

/** The action cannot be recorded, so it must not happen (`audit-unavailable`). */
export class AuditUnavailableError extends Error {
  constructor(cause: unknown) {
    super('audit log unavailable', { cause });
  }
}

/** What a caller says about an action; the store adds the chain. */
export interface AuditInput {
  type: AuditEventType;
  /** When the action happened, Unix ms; default now. */
  at?: number;
  sessionId?: string | null;
  deviceId?: string | null;
  /** Default `operator` with no ID. */
  actor?: { kind: AuditActorKind; id?: string };
  /** Default `rest`. */
  channel?: AuditChannel;
  ref?: string | null;
  /** IDs, names, codes and short text; every string is scrubbed and cut. */
  summary?: Record<string, unknown>;
}

interface Row {
  seq: number;
  at: number;
  recorded_at: number;
  type: string;
  session_id: string | null;
  device_id: string | null;
  actor_kind: string;
  actor_id: string;
  channel: string;
  ref: string | null;
  summary: string;
  prev_hash: string;
  hash: string;
}

/** JSON with sorted keys and no whitespace; a number that is not an integer is not canonical. */
export function canonicalJson(value: unknown): string {
  if (value === null || typeof value === 'boolean' || typeof value === 'string') {
    return JSON.stringify(value);
  }
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) throw new Error('canonical JSON holds integers only');
    return String(value);
  }
  if (Array.isArray(value)) return `[${value.map((item) => canonicalJson(item)).join(',')}]`;
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, item]) => item !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
    return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${canonicalJson(item)}`).join(',')}}`;
  }
  throw new Error('canonical JSON cannot hold this value');
}

/** The hash of a record: SHA-256 of the previous hash and the canonical JSON of the other fields. */
export function auditHash(
  prevHash: string,
  fields: Omit<Row, 'prev_hash' | 'hash' | 'summary'> & { summary: unknown },
): string {
  return createHash('sha256').update(prevHash).update(canonicalJson(fields)).digest('hex');
}

function scrubString(text: string): string {
  const cut = [...text].slice(0, MAX_TEXT_CHARS * 4).join('');
  const scrubbed = UUID.test(cut) || DIGEST.test(cut) ? cut : scrubText(cut);
  const chars = [...scrubbed];
  return chars.length > MAX_TEXT_CHARS ? `${chars.slice(0, MAX_TEXT_CHARS).join('')}...` : scrubbed;
}

/** Scrubs every string, cuts long ones and lists, and turns what canonical JSON cannot hold into text. */
function sanitize(value: unknown, depth: number): unknown {
  if (value === null || typeof value === 'boolean') return value;
  if (typeof value === 'string') return scrubString(value);
  if (typeof value === 'number') {
    if (Number.isSafeInteger(value)) return value;
    return Number.isFinite(value) ? String(value) : null;
  }
  if (depth >= MAX_DEPTH) return null;
  if (Array.isArray(value)) {
    return value.slice(0, MAX_ARRAY_ITEMS).map((item) => sanitize(item, depth + 1) ?? null);
  }
  if (typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      if (item === undefined || typeof item === 'function') continue;
      out[scrubString(key)] = sanitize(item, depth + 1);
    }
    return out;
  }
  return null;
}

function nextSeq(ctx: StoreContext): number {
  const row = ctx.db.prepare(`SELECT seq FROM sqlite_sequence WHERE name = 'audit_events'`).get() as
    | { seq: number }
    | undefined;
  return Number(row?.seq ?? 0) + 1;
}

function headRow(ctx: StoreContext): { seq: number; hash: string } | null {
  const row = ctx.db
    .prepare(`SELECT seq, hash FROM audit_events ORDER BY seq DESC LIMIT 1`)
    .get() as { seq: number; hash: string } | undefined;
  return row ? { seq: Number(row.seq), hash: row.hash } : null;
}

/**
 * Appends one record. It joins the caller's transaction when there is one, so the record and the change it
 * describes commit together; otherwise it takes its own. A failure throws `AuditUnavailableError`, and the
 * caller must not carry out the action.
 */
export function appendAudit(
  ctx: StoreContext,
  input: AuditInput,
  now: number = Date.now(),
): AuditHead {
  const own = !ctx.db.isTransaction;
  try {
    if (own) ctx.db.exec('BEGIN IMMEDIATE');
    let summary = canonicalJson(sanitize(input.summary ?? {}, 0));
    if (Buffer.byteLength(summary) > AUDIT_SUMMARY_MAX_BYTES) summary = '{"truncated":true}';
    const prev = headRow(ctx)?.hash ?? AUDIT_GENESIS_HASH;
    const fields = {
      seq: nextSeq(ctx),
      at: Math.trunc(input.at ?? now),
      recorded_at: Math.trunc(now),
      type: input.type,
      session_id: input.sessionId ?? null,
      device_id: input.deviceId ?? null,
      actor_kind: input.actor?.kind ?? 'operator',
      actor_id: input.actor?.id ?? '',
      channel: input.channel ?? 'rest',
      ref: input.ref ?? null,
      summary: JSON.parse(summary) as unknown,
    };
    const hash = auditHash(prev, fields);
    ctx.db
      .prepare(
        `INSERT INTO audit_events(seq, at, recorded_at, type, session_id, device_id, actor_kind, actor_id, channel, ref, summary, prev_hash, hash)
         VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(
        fields.seq,
        fields.at,
        fields.recorded_at,
        fields.type,
        fields.session_id,
        fields.device_id,
        fields.actor_kind,
        fields.actor_id,
        fields.channel,
        fields.ref,
        summary,
        prev,
        hash,
      );
    if (own) ctx.db.exec('COMMIT');
    return { seq: fields.seq, hash };
  } catch (err) {
    if (own && ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw new AuditUnavailableError(err);
  }
}

/** Writes `audit.started` the first time the feature runs; later starts leave the log alone. */
export function startAudit(ctx: StoreContext, now: number = Date.now()): void {
  if (nextSeq(ctx) > 1) return;
  appendAudit(
    ctx,
    { type: 'audit.started', actor: { kind: 'core', id: 'startup' }, channel: 'core' },
    now,
  );
}

function toEvent(row: Row): AuditEvent {
  let summary: Record<string, unknown> = {};
  try {
    summary = JSON.parse(row.summary) as Record<string, unknown>;
  } catch {
    // An unreadable summary is shown empty; verification reports the record.
  }
  return {
    seq: Number(row.seq),
    at: Number(row.at),
    recordedAt: Number(row.recorded_at),
    type: row.type,
    sessionId: row.session_id,
    deviceId: row.device_id,
    actorKind: row.actor_kind as AuditActorKind,
    actorId: row.actor_id,
    channel: row.channel as AuditChannel,
    ref: row.ref,
    summary,
    prevHash: row.prev_hash,
    hash: row.hash,
  };
}

export interface AuditPageOptions {
  /** The `seq` the previous page ended at. */
  after?: number | undefined;
  order?: 'asc' | 'desc';
  limit: number;
  /** Only records up to this `seq`; an export reads one fixed range. */
  upTo?: number;
}

function where(filter: AuditFilter, extra: string[], args: (string | number)[]) {
  const clauses = [...extra];
  if (filter.session !== undefined) {
    clauses.push('session_id = ?');
    args.push(filter.session);
  }
  if (filter.device !== undefined) {
    clauses.push('device_id = ?');
    args.push(filter.device);
  }
  if (filter.actor) {
    clauses.push('actor_kind = ?');
    args.push(filter.actor.kind);
    if (filter.actor.id !== undefined) {
      clauses.push('actor_id = ?');
      args.push(filter.actor.id);
    }
  }
  if (filter.type !== undefined) {
    if (filter.type.endsWith('.')) {
      clauses.push('substr(type, 1, ?) = ?');
      args.push(filter.type.length, filter.type);
    } else {
      clauses.push('type = ?');
      args.push(filter.type);
    }
  }
  if (filter.from !== undefined) {
    clauses.push('at >= ?');
    args.push(filter.from);
  }
  if (filter.to !== undefined) {
    clauses.push('at < ?');
    args.push(filter.to);
  }
  if (filter.q !== undefined) {
    clauses.push('instr(summary, ?) > 0');
    args.push(filter.q);
  }
  return clauses.length ? `WHERE ${clauses.join(' AND ')}` : '';
}

/** A page of records; `next` is the cursor for the following page, null at the end. */
export function listAudit(
  ctx: StoreContext,
  filter: AuditFilter,
  options: AuditPageOptions,
): { events: AuditEvent[]; next: number | null } {
  const asc = options.order === 'asc';
  const args: (string | number)[] = [];
  const extra: string[] = [];
  if (options.after !== undefined) {
    extra.push(asc ? 'seq > ?' : 'seq < ?');
    args.push(options.after);
  }
  if (options.upTo !== undefined) {
    extra.push('seq <= ?');
    args.push(options.upTo);
  }
  const clause = where(filter, extra, args);
  const rows = ctx.db
    .prepare(`SELECT * FROM audit_events ${clause} ORDER BY seq ${asc ? 'ASC' : 'DESC'} LIMIT ?`)
    .all(...args, options.limit + 1) as unknown as Row[];
  const page = rows.slice(0, options.limit).map(toEvent);
  const last = page.at(-1);
  return { events: page, next: rows.length > options.limit && last ? last.seq : null };
}

/** The records the filters match up to `upTo`. */
export function countAudit(ctx: StoreContext, filter: AuditFilter, upTo: number): number {
  const args: (string | number)[] = [upTo];
  const clause = where(filter, ['seq <= ?'], args);
  const row = ctx.db.prepare(`SELECT COUNT(*) AS n FROM audit_events ${clause}`).get(...args) as {
    n: number;
  };
  return Number(row.n);
}

/** The newest record's `seq` and hash; null for an empty log. */
export function auditHead(ctx: StoreContext): AuditHead | null {
  return headRow(ctx);
}

function recompute(row: Row): string {
  let summary: unknown = null;
  try {
    summary = JSON.parse(row.summary);
  } catch {
    // The hash cannot match an unreadable summary.
  }
  return auditHash(row.prev_hash, {
    seq: Number(row.seq),
    at: Number(row.at),
    recorded_at: Number(row.recorded_at),
    type: row.type,
    session_id: row.session_id,
    device_id: row.device_id,
    actor_kind: row.actor_kind,
    actor_id: row.actor_id,
    channel: row.channel,
    ref: row.ref,
    summary,
  });
}

function checkpointCovers(ctx: StoreContext, seq: number, hash: string): boolean {
  const rows = ctx.db
    .prepare(`SELECT summary FROM audit_events WHERE type = 'audit.checkpoint'`)
    .all() as { summary: string }[];
  return rows.some((row) => {
    try {
      const s = JSON.parse(row.summary) as { removedThrough?: unknown; removedHash?: unknown };
      return s.removedThrough === seq && s.removedHash === hash;
    } catch {
      return false;
    }
  });
}

const yieldTurn = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

/**
 * Walks the chain from the oldest record: every hash is recomputed, `prev_hash` and `seq` must follow, and the
 * start must be the genesis or a checkpoint. It reads in batches and yields between them. `expected` is a
 * head written down elsewhere, which must still be the stored record at its `seq`.
 */
export async function verifyAudit(
  ctx: StoreContext,
  expected?: AuditHead,
): Promise<AuditVerifyResponse> {
  let checked = 0;
  let after = 0;
  let previous: { seq: number; hash: string } | null = null;
  let broken: { seq: number; reason: AuditBreakReason; message: string } | null = null;
  const stop = (seq: number, reason: AuditBreakReason, message: string): void => {
    broken = { seq, reason, message };
  };

  while (!broken) {
    const rows = ctx.db
      .prepare(`SELECT * FROM audit_events WHERE seq > ? ORDER BY seq ASC LIMIT ?`)
      .all(after, VERIFY_BATCH) as unknown as Row[];
    if (rows.length === 0) break;
    for (const row of rows) {
      const seq = Number(row.seq);
      checked++;
      if (!previous) {
        const starts =
          row.prev_hash === AUDIT_GENESIS_HASH
            ? seq === 1
            : checkpointCovers(ctx, seq - 1, row.prev_hash);
        if (!starts) {
          stop(
            seq,
            'removed-outside-retention',
            `records before seq ${seq} were removed outside retention`,
          );
          break;
        }
      } else {
        if (seq !== previous.seq + 1) {
          stop(seq, 'gap', `records from seq ${previous.seq + 1} to ${seq - 1} are missing`);
          break;
        }
        if (row.prev_hash !== previous.hash) {
          stop(
            seq,
            'prev-hash-mismatch',
            `record ${seq} does not follow the hash of record ${previous.seq}`,
          );
          break;
        }
      }
      if (recompute(row) !== row.hash) {
        stop(seq, 'hash-mismatch', `record ${seq} does not match its hash`);
        break;
      }
      previous = { seq, hash: row.hash };
    }
    after = Number(rows[rows.length - 1]?.seq);
    if (rows.length < VERIFY_BATCH) break;
    await yieldTurn();
  }

  if (!broken && expected) {
    const stored = ctx.db
      .prepare(`SELECT hash FROM audit_events WHERE seq = ?`)
      .get(expected.seq) as { hash: string } | undefined;
    if (!stored) {
      stop(expected.seq, 'head-missing', `no record has seq ${expected.seq}`);
    } else if (stored.hash !== expected.hash) {
      stop(expected.seq, 'head-mismatch', `record ${expected.seq} does not have the hash given`);
    }
  }

  return { ok: broken === null, checked, head: headRow(ctx), firstBreak: broken };
}

/**
 * Removes the records older than `days` days, oldest first, at most 10,000 per transaction. Each transaction
 * first writes an `audit.checkpoint` with the `seq` and hash of the last record it removes, so the chain that
 * remains still verifies. Returns the number of records removed. `days` of 0 keeps everything.
 */
export async function pruneAudit(
  ctx: StoreContext,
  days: number,
  now: number = Date.now(),
): Promise<number> {
  if (days <= 0) return 0;
  const cutoff = now - days * DAY_MS;
  let removed = 0;
  for (;;) {
    const count = pruneBatch(ctx, cutoff, now);
    removed += count;
    if (count < AUDIT_PRUNE_BATCH) return removed;
    await yieldTurn();
  }
}

function pruneBatch(ctx: StoreContext, cutoff: number, now: number): number {
  try {
    ctx.db.exec('BEGIN IMMEDIATE');
    const rows = ctx.db
      .prepare(`SELECT seq, hash, recorded_at FROM audit_events ORDER BY seq ASC LIMIT ?`)
      .all(AUDIT_PRUNE_BATCH) as { seq: number; hash: string; recorded_at: number }[];
    let count = 0;
    while (count < rows.length && Number(rows[count]?.recorded_at) < cutoff) count++;
    const last = rows[count - 1];
    if (count === 0 || !last) {
      ctx.db.exec('COMMIT');
      return 0;
    }
    appendAudit(
      ctx,
      {
        type: 'audit.checkpoint',
        actor: { kind: 'core', id: 'retention' },
        channel: 'core',
        summary: { removedThrough: Number(last.seq), removedHash: last.hash, removedCount: count },
      },
      now,
    );
    ctx.db.prepare(`DELETE FROM audit_events WHERE seq <= ?`).run(last.seq);
    ctx.db.exec('COMMIT');
    return count;
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}
