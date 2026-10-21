import type {
  BudgetScope,
  SessionUsage,
  UsageDimension,
  UsagePrices,
  UsagePricesBody,
  UsageQuery,
  UsageResponse,
  UsageRow,
  UsageTotals,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { readChunkBytes, splitLines, yieldToEventLoop } from './chunks.ts';
import type { StoreContext } from './layout.ts';
import { committedLines, getRow } from './sessions.ts';
import { extractUsage, upsertUsage } from './usage-lines.ts';
import { notifyUsage } from './usage-listeners.ts';
import {
  costOf,
  DEFAULT_USAGE_PRICES,
  priceOf,
  priceOverrides,
  setPriceOverrides,
} from './usage-prices.ts';

/**
 * Reads the usage of the sessions that existed before usage was recorded, one chunk at a time, handing the
 * event loop back between chunks. A session is marked done only after its last chunk, so a restart resumes
 * with the sessions not yet marked; reading one again changes nothing (see `upsertUsage`). Live appends write
 * their own usage meanwhile.
 */
export async function backfillUsage(ctx: StoreContext): Promise<void> {
  const ids = ctx.db
    .prepare(
      `SELECT s.session_id FROM sessions s WHERE s.usage_backfilled = 0
       AND NOT EXISTS (SELECT 1 FROM session_tombstones t WHERE t.session_id = s.session_id)
       ORDER BY s.created_at`,
    )
    .all() as { session_id: string }[];
  if (ids.length === 0) return;
  log('info', 'store', 'usage backfill started', { sessions: ids.length });
  let done = 0;
  for (const { session_id: sessionId } of ids) {
    try {
      if (await backfillSession(ctx, sessionId)) done++;
    } catch (err) {
      log('warn', 'store', 'usage backfill of a session failed', {
        sessionId,
        error: err instanceof Error ? err.message : String(err),
      });
    }
    await yieldToEventLoop();
  }
  log('info', 'store', 'usage backfill finished', { sessions: ids.length, done });
}

async function backfillSession(ctx: StoreContext, sessionId: string): Promise<boolean> {
  let model = '';
  for (let seq = 0; ; seq++) {
    // The row is read again for every chunk: appends meanwhile grow the open chunk.
    const row = getRow(ctx, sessionId);
    if (!row) return false;
    if (seq >= row.chunk_count) break;
    const bytes = await readChunkBytes(ctx, sessionId, seq);
    if (!bytes) continue;
    const lines = splitLines(bytes.toString('utf8'), committedLines(row, seq));
    const scan = extractUsage(
      row.provider === 'codex' ? 'codex' : 'claude',
      lines,
      model,
      row.created_at,
    );
    model = scan.model;
    ctx.db.exec('BEGIN IMMEDIATE');
    try {
      upsertUsage(ctx.db, sessionId, scan.records);
      ctx.db.exec('COMMIT');
      if (scan.records.length > 0) notifyUsage(ctx);
    } catch (err) {
      if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
      throw err;
    }
    await yieldToEventLoop();
  }
  ctx.db
    .prepare(
      `UPDATE sessions SET usage_backfilled = 1,
         usage_model = CASE WHEN usage_model = '' THEN ? ELSE usage_model END
       WHERE session_id = ?`,
    )
    .run(model, sessionId);
  return true;
}

// ---- Queries ----

type Cell = {
  value: string;
  label: string;
};

const DIMENSIONS: Record<UsageDimension, { value: string; label: string }> = {
  session: {
    value: 's.session_id',
    label: `COALESCE(NULLIF(s.title_override, ''), NULLIF(s.title, ''), s.session_id)`,
  },
  project: {
    value: `CASE WHEN s.project_path <> '' THEN s.project_path ELSE s.project_dir END`,
    label: `CASE WHEN s.project_path <> '' THEN s.project_path ELSE s.project_dir END`,
  },
  device: { value: 's.device_id', label: `COALESCE(NULLIF(d.name, ''), s.device_id)` },
  model: { value: 'u.model', label: 'u.model' },
  // The day is the caller's: `dayExpr` shifts it by the query's `utcOffset`.
  day: { value: 'DAY_EXPR', label: 'DAY_EXPR' },
};

/** SQL of a dimension; `day` is shifted by `utcOffset` minutes east of UTC (an integer, validated by the schema). */
function dimension(dim: UsageDimension, utcOffset: number): { value: string; label: string } {
  if (dim !== 'day') return DIMENSIONS[dim];
  const expr = `strftime('%Y-%m-%d', u.at / 1000 + ${Math.trunc(utcOffset) * 60}, 'unixepoch')`;
  return { value: expr, label: expr };
}

const FROM = `FROM usage u
  JOIN sessions s ON s.session_id = u.session_id
  LEFT JOIN devices d ON d.device_id = s.device_id
  WHERE NOT EXISTS (SELECT 1 FROM session_tombstones t WHERE t.session_id = s.session_id)`;

interface Accumulator {
  keys: Cell[];
  input: number;
  output: number;
  cacheWrite: number;
  cacheRead: number;
  messages: number;
  cost: number;
  priced: boolean;
  complete: boolean;
}

function newAccumulator(keys: Cell[]): Accumulator {
  return {
    keys,
    input: 0,
    output: 0,
    cacheWrite: 0,
    cacheRead: 0,
    messages: 0,
    cost: 0,
    priced: false,
    complete: true,
  };
}

function toTotals(a: Accumulator): UsageTotals {
  return {
    input: a.input,
    output: a.output,
    cacheWrite: a.cacheWrite,
    cacheRead: a.cacheRead,
    messages: a.messages,
    estimatedCostUsd: a.priced ? a.cost : null,
    costComplete: a.complete,
  };
}

function backfilling(ctx: StoreContext): boolean {
  return (
    ctx.db
      .prepare(
        `SELECT 1 FROM sessions s WHERE s.usage_backfilled = 0
         AND NOT EXISTS (SELECT 1 FROM session_tombstones t WHERE t.session_id = s.session_id) LIMIT 1`,
      )
      .get() !== undefined
  );
}

interface Group {
  keys: Cell[];
  model: string;
  input: number;
  output: number;
  cache_write: number;
  cache_read: number;
  messages: number;
}

function groups(
  ctx: StoreContext,
  dims: UsageDimension[],
  where: string[],
  args: (string | number)[],
  utcOffset = 0,
): Group[] {
  const selects: string[] = [];
  dims.forEach((dim, i) => {
    selects.push(
      `${dimension(dim, utcOffset).value} AS v${i}`,
      `${dimension(dim, utcOffset).label} AS l${i}`,
    );
  });
  const sql = `SELECT ${[...selects, 'u.model AS model'].join(', ')},
      SUM(u.input_tokens) AS input, SUM(u.output_tokens) AS output,
      SUM(u.cache_write_tokens) AS cache_write, SUM(u.cache_read_tokens) AS cache_read, COUNT(*) AS messages
    ${FROM}${where.map((w) => ` AND ${w}`).join('')}
    GROUP BY ${[...dims.map((_, i) => `v${i}`), 'u.model'].join(', ')}`;
  const rows = ctx.db.prepare(sql).all(...args) as Record<string, string | number>[];
  return rows.map((r) => ({
    keys: dims.map((_, i) => ({ value: String(r[`v${i}`]), label: String(r[`l${i}`]) })),
    model: String(r.model),
    input: Number(r.input),
    output: Number(r.output),
    cache_write: Number(r.cache_write),
    cache_read: Number(r.cache_read),
    messages: Number(r.messages),
  }));
}

/** Folds the per-model groups into one row per distinct keys, pricing each model's tokens. */
function fold(rows: Group[], overrides: UsagePrices['overrides']): Accumulator[] {
  const out = new Map<string, Accumulator>();
  for (const g of rows) {
    const id = JSON.stringify(g.keys.map((k) => k.value));
    let acc = out.get(id);
    if (!acc) {
      acc = newAccumulator(g.keys);
      out.set(id, acc);
    }
    addGroup(acc, g, overrides);
  }
  return [...out.values()];
}

function addGroup(acc: Accumulator, g: Group, overrides: UsagePrices['overrides']): void {
  acc.input += g.input;
  acc.output += g.output;
  acc.cacheWrite += g.cache_write;
  acc.cacheRead += g.cache_read;
  acc.messages += g.messages;
  const price = priceOf(g.model, overrides);
  if (price) {
    acc.priced = true;
    acc.cost += costOf(price, {
      input: g.input,
      output: g.output,
      cacheWrite: g.cache_write,
      cacheRead: g.cache_read,
    });
  } else {
    acc.complete = false;
  }
}

/** What the usage of `scope` in [from, to) comes to: all token kinds, and the cost of the priced models. */
export function scopeSpend(
  ctx: StoreContext,
  scope: BudgetScope,
  from: number,
  to: number,
): { tokens: number; costUsd: number; costComplete: boolean } {
  const where = ['u.at >= ?', 'u.at < ?'];
  const args: (string | number)[] = [from, to];
  if (scope.kind === 'project') {
    where.push(`${DIMENSIONS.project.value} = ?`);
    args.push(scope.value);
  } else if (scope.kind === 'device') {
    where.push('s.device_id = ?');
    args.push(scope.value);
  }
  const total = newAccumulator([]);
  const overrides = priceOverrides(ctx);
  for (const g of groups(ctx, [], where, args)) addGroup(total, g, overrides);
  return {
    tokens: total.input + total.output + total.cacheWrite + total.cacheRead,
    costUsd: total.cost,
    costComplete: total.complete,
  };
}

/** Whether sessions recorded before usage was are still being read. */
export function usageBackfilling(ctx: StoreContext): boolean {
  return backfilling(ctx);
}

export function queryUsage(ctx: StoreContext, q: UsageQuery): UsageResponse {
  const where: string[] = [];
  const args: (string | number)[] = [];
  if (q.from !== undefined) {
    where.push('u.at >= ?');
    args.push(q.from);
  }
  if (q.to !== undefined) {
    where.push('u.at < ?');
    args.push(q.to);
  }
  if (q.session !== undefined) {
    where.push('s.session_id = ?');
    args.push(q.session);
  }
  if (q.project !== undefined) {
    where.push(`${DIMENSIONS.project.value} = ?`);
    args.push(q.project);
  }
  if (q.device !== undefined) {
    where.push('s.device_id = ?');
    args.push(q.device);
  }
  if (q.model !== undefined) {
    where.push('u.model = ?');
    args.push(q.model);
  }
  const overrides = priceOverrides(ctx);
  const total = newAccumulator([]);
  for (const g of groups(ctx, [], where, args)) addGroup(total, g, overrides);
  const all = fold(groups(ctx, q.groupBy, where, args, q.utcOffset), overrides);
  const byDay = q.groupBy[0] === 'day';
  all.sort((a, b) => {
    if (byDay) return (a.keys[0]?.value ?? '').localeCompare(b.keys[0]?.value ?? '');
    const d =
      b.input +
      b.output +
      b.cacheWrite +
      b.cacheRead -
      (a.input + a.output + a.cacheWrite + a.cacheRead);
    if (d !== 0) return d;
    return a.keys
      .map((k) => k.value)
      .join('\0')
      .localeCompare(b.keys.map((k) => k.value).join('\0'));
  });
  return {
    estimated: true,
    groupBy: q.groupBy,
    backfilling: backfilling(ctx),
    total: toTotals(total),
    rows: all.slice(q.offset, q.offset + q.limit).map((a) => ({ ...toTotals(a), keys: a.keys })),
    rowCount: all.length,
    offset: q.offset,
  };
}

/** One session's usage, or null when there is no such session. */
export function sessionUsage(ctx: StoreContext, sessionId: string): SessionUsage | null {
  const row = getRow(ctx, sessionId);
  if (!row) return null;
  const overrides = priceOverrides(ctx);
  const where = ['s.session_id = ?'];
  const args = [sessionId];
  const total = newAccumulator([]);
  for (const g of groups(ctx, [], where, args)) addGroup(total, g, overrides);
  const models: UsageRow[] = fold(groups(ctx, ['model'], where, args), overrides)
    .sort(
      (a, b) =>
        b.input +
        b.output +
        b.cacheWrite +
        b.cacheRead -
        (a.input + a.output + a.cacheWrite + a.cacheRead),
    )
    .map((a) => ({ ...toTotals(a), keys: a.keys }));
  return {
    sessionId,
    estimated: true,
    complete: row.usage_backfilled === 1,
    total: toTotals(total),
    models,
  };
}

export function usagePrices(ctx: StoreContext): UsagePrices {
  return { defaults: structuredClone(DEFAULT_USAGE_PRICES), overrides: priceOverrides(ctx) };
}

export function setUsagePrices(ctx: StoreContext, body: UsagePricesBody): void {
  setPriceOverrides(ctx, body.overrides);
}
