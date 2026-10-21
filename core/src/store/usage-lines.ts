import type { DatabaseSync } from 'node:sqlite';
import type { Provider } from '@hoplet/shared';

/** What one assistant message used. `messageId` is the vendor's own, so a message written as several lines is one. */
export interface UsageRecord {
  messageId: string;
  model: string;
  /** Unix ms. */
  at: number;
  input: number;
  output: number;
  cacheWrite: number;
  cacheRead: number;
}

export interface UsageScan {
  records: UsageRecord[];
  /** The model that applies to the lines after these (Codex names it once per turn). */
  model: string;
}

/**
 * The usage the appended lines report, without changing them, by the vendor they come from. The dispatch is
 * where a vendor's line shapes plug in. `model` is the model in force before the first line, `fallbackAt` the
 * time of a line that carries none.
 */
export function extractUsage(
  provider: Provider,
  lines: string[],
  model: string,
  fallbackAt: number,
): UsageScan {
  return provider === 'codex'
    ? extractCodexUsage(lines, model, fallbackAt)
    : extractClaudeUsage(lines, model, fallbackAt);
}

function count(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? Math.floor(v) : 0;
}

function timeOf(v: unknown, fallback: number): number {
  if (typeof v !== 'string') return fallback;
  const t = Date.parse(v);
  return Number.isFinite(t) ? t : fallback;
}

function parseObject(raw: string): Record<string, unknown> | null {
  try {
    const o = JSON.parse(raw);
    return typeof o === 'object' && o !== null ? (o as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/**
 * Claude Code writes an assistant message as one line per content block, each repeating the message's `id`,
 * `model` and `usage`; streamed lines can carry a growing `output_tokens`. The largest of each count is the
 * message's. Lines of model `<synthetic>` (local notices) and lines without any tokens are not usage.
 */
function extractClaudeUsage(lines: string[], model: string, fallbackAt: number): UsageScan {
  const byId = new Map<string, UsageRecord>();
  for (const raw of lines) {
    if (!raw.includes('"usage"')) continue;
    const o = parseObject(raw);
    const message = o?.message;
    if (typeof message !== 'object' || message === null) continue;
    const m = message as Record<string, unknown>;
    const usage = m.usage;
    if (typeof usage !== 'object' || usage === null) continue;
    const u = usage as Record<string, unknown>;
    const id = typeof m.id === 'string' ? m.id : '';
    const name = typeof m.model === 'string' ? m.model : '';
    if (!id || name.startsWith('<')) continue;
    const rec: UsageRecord = {
      messageId: id,
      model: name,
      at: timeOf(o?.timestamp, fallbackAt),
      input: count(u.input_tokens),
      output: count(u.output_tokens),
      cacheWrite: count(u.cache_creation_input_tokens),
      cacheRead: count(u.cache_read_input_tokens),
    };
    if (rec.input + rec.output + rec.cacheWrite + rec.cacheRead === 0) continue;
    const seen = byId.get(id);
    if (!seen) {
      byId.set(id, rec);
      continue;
    }
    seen.input = Math.max(seen.input, rec.input);
    seen.output = Math.max(seen.output, rec.output);
    seen.cacheWrite = Math.max(seen.cacheWrite, rec.cacheWrite);
    seen.cacheRead = Math.max(seen.cacheRead, rec.cacheRead);
    seen.at = Math.min(seen.at, rec.at);
    if (rec.model) seen.model = rec.model;
  }
  return { records: [...byId.values()], model };
}

/**
 * A Codex rollout reports usage in `event_msg` lines of payload type `token_count`: `last_token_usage` is what
 * the latest model call used, `total_token_usage` the running total of the session. The running total is
 * unique per call, so it names the record (`c<total>`) and a repeated event is one record. Codex counts cached
 * input within `input_tokens`; it is split out here as cache reads so the four counts add up. The model is the
 * one the latest `turn_context` line named.
 */
function extractCodexUsage(lines: string[], model: string, fallbackAt: number): UsageScan {
  const byId = new Map<string, UsageRecord>();
  let current = model;
  for (const raw of lines) {
    const isContext = raw.includes('"turn_context"');
    if (!isContext && !raw.includes('"token_count"')) continue;
    const o = parseObject(raw);
    const payload = o?.payload;
    if (typeof payload !== 'object' || payload === null) continue;
    const p = payload as Record<string, unknown>;
    if (isContext && o?.type === 'turn_context') {
      if (typeof p.model === 'string' && p.model) current = p.model;
      continue;
    }
    if (o?.type !== 'event_msg' || p.type !== 'token_count') continue;
    const info = p.info;
    if (typeof info !== 'object' || info === null) continue;
    const i = info as Record<string, unknown>;
    const last = i.last_token_usage as Record<string, unknown> | null | undefined;
    const total = i.total_token_usage as Record<string, unknown> | null | undefined;
    if (typeof last !== 'object' || last === null) continue;
    const totalTokens = count(total?.total_tokens);
    if (totalTokens === 0) continue;
    const cached = count(last.cached_input_tokens);
    const rec: UsageRecord = {
      messageId: `c${totalTokens}`,
      model: current,
      at: timeOf(o?.timestamp, fallbackAt),
      input: Math.max(0, count(last.input_tokens) - cached),
      output: count(last.output_tokens),
      cacheWrite: count(last.cache_write_input_tokens),
      cacheRead: cached,
    };
    if (rec.input + rec.output + rec.cacheWrite + rec.cacheRead === 0) continue;
    if (!byId.has(rec.messageId)) byId.set(rec.messageId, rec);
  }
  return { records: [...byId.values()], model: current };
}

/**
 * Writes the records of a session. Counts take the larger of the stored and the new value and the earliest
 * time stands, so recording the same message twice (a live append and the backfill, or a line rewritten as
 * several) changes nothing. The caller owns the transaction.
 */
export function upsertUsage(db: DatabaseSync, sessionId: string, records: UsageRecord[]): void {
  if (records.length === 0) return;
  const stmt = db.prepare(
    `INSERT INTO usage(session_id, message_id, model, at, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens)
     VALUES(?, ?, ?, ?, ?, ?, ?, ?)
     ON CONFLICT(session_id, message_id) DO UPDATE SET
       model = CASE WHEN excluded.model <> '' THEN excluded.model ELSE usage.model END,
       at = MIN(usage.at, excluded.at),
       input_tokens = MAX(usage.input_tokens, excluded.input_tokens),
       output_tokens = MAX(usage.output_tokens, excluded.output_tokens),
       cache_write_tokens = MAX(usage.cache_write_tokens, excluded.cache_write_tokens),
       cache_read_tokens = MAX(usage.cache_read_tokens, excluded.cache_read_tokens)`,
  );
  for (const r of records) {
    stmt.run(sessionId, r.messageId, r.model, r.at, r.input, r.output, r.cacheWrite, r.cacheRead);
  }
}
