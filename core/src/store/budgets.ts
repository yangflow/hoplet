import { randomBytes } from 'node:crypto';
import type {
  Budget,
  BudgetBody,
  BudgetLimitAction,
  BudgetPeriod,
  BudgetRef,
  BudgetScope,
  BudgetScopeKind,
  BudgetState,
  BudgetUnit,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import type { BudgetBlock, StoreContext } from './layout.ts';
import { scopeSpend } from './usage.ts';

const MINUTE = 60_000;
const DAY = 86_400_000;
/** How long recorded usage may go unseen by the prompt path before the `pause` budgets are recomputed. */
const BLOCK_REFRESH_MS = 1000;

/**
 * The period of `at` as [start, end) in epoch milliseconds, with the calendar kept at `utcOffset` minutes east of
 * UTC. A week starts on Monday.
 */
export function periodBounds(
  period: BudgetPeriod,
  utcOffset: number,
  at: number,
): { start: number; end: number } {
  const shift = utcOffset * MINUTE;
  const local = at + shift;
  const days = Math.floor(local / DAY);
  let start: number;
  let end: number;
  if (period === 'day') {
    start = days * DAY;
    end = start + DAY;
  } else if (period === 'week') {
    // 1970-01-01 was a Thursday, three days after a Monday.
    const sinceMonday = (((days + 3) % 7) + 7) % 7;
    start = (days - sinceMonday) * DAY;
    end = start + 7 * DAY;
  } else {
    const d = new Date(local);
    start = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1);
    end = Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1);
  }
  return { start: start - shift, end: end - shift };
}

interface Row {
  id: string;
  name: string;
  scope_kind: string;
  scope_value: string;
  period: string;
  unit: string;
  amount: number;
  alert_percent: number;
  on_limit: string;
  utc_offset: number;
  created_at: number;
  allowed_period_start: number | null;
}

/** A budget's definition with its spend for the period that contains `now`. */
export interface BudgetStatus extends Budget {
  /** Whether the pause is lifted for the current period. */
  lifted: boolean;
}

function status(ctx: StoreContext, row: Row, now: number): BudgetStatus {
  const period = row.period as BudgetPeriod;
  const unit = row.unit as BudgetUnit;
  const scope: BudgetScope = { kind: row.scope_kind as BudgetScopeKind, value: row.scope_value };
  const { start, end } = periodBounds(period, row.utc_offset, now);
  const spend = scopeSpend(ctx, scope, start, end);
  const spent = unit === 'cost' ? spend.costUsd : spend.tokens;
  const percent = (spent / row.amount) * 100;
  const over = spent >= row.amount;
  const lifted = row.allowed_period_start === start;
  const onLimit = row.on_limit as BudgetLimitAction;
  const paused = over && !lifted && onLimit === 'pause';
  let state: BudgetState = 'ok';
  if (over) state = lifted ? 'allowed' : 'over';
  else if (percent >= row.alert_percent) state = 'alert';
  if (onLimit === 'pause') {
    ctx.budgetBlocks.set(row.id, {
      scope,
      name: row.name,
      createdAt: row.created_at,
      periodStart: start,
      periodEnd: end,
      paused,
      stale: false,
    });
  } else {
    ctx.budgetBlocks.delete(row.id);
  }
  return {
    id: row.id,
    name: row.name,
    scope,
    period,
    unit,
    amount: row.amount,
    alertPercent: row.alert_percent,
    onLimit,
    utcOffset: row.utc_offset,
    createdAt: row.created_at,
    periodStart: start,
    periodEnd: end,
    spent,
    percent,
    state,
    paused,
    costComplete: unit === 'cost' ? spend.costComplete : true,
    lifted,
  };
}

function rows(ctx: StoreContext): Row[] {
  return ctx.db.prepare('SELECT * FROM budgets ORDER BY created_at, id').all() as unknown as Row[];
}

function toBudget(s: BudgetStatus): Budget {
  const { lifted: _lifted, ...budget } = s;
  return budget;
}

export function listBudgets(ctx: StoreContext, now: number): Budget[] {
  return rows(ctx).map((row) => toBudget(status(ctx, row, now)));
}

/** Every budget with its spend, for the evaluation that decides notifications. */
export function budgetStatuses(ctx: StoreContext, now: number): BudgetStatus[] {
  return rows(ctx).map((row) => status(ctx, row, now));
}

export function getBudget(ctx: StoreContext, id: string, now: number): Budget | null {
  const row = ctx.db.prepare('SELECT * FROM budgets WHERE id = ?').get(id) as Row | undefined;
  return row ? toBudget(status(ctx, row, now)) : null;
}

export function budgetCount(ctx: StoreContext): number {
  return Number((ctx.db.prepare('SELECT COUNT(*) AS n FROM budgets').get() as { n: number }).n);
}

export function createBudget(ctx: StoreContext, body: BudgetBody, now: number): Budget {
  const id = randomBytes(9).toString('base64url');
  ctx.db
    .prepare(
      `INSERT INTO budgets(id, name, scope_kind, scope_value, period, unit, amount, alert_percent, on_limit, utc_offset, created_at)
       VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .run(
      id,
      body.name,
      body.scope.kind,
      body.scope.value,
      body.period,
      body.unit,
      body.amount,
      body.alertPercent,
      body.onLimit,
      body.utcOffset,
      now,
    );
  const created = getBudget(ctx, id, now);
  if (!created) throw new Error('budget vanished');
  return created;
}

/**
 * Replaces a budget's definition; null when there is none with that ID. A change to what is measured or where the
 * lines sit (scope, period, offset, unit, amount, alert) starts the notifications and the lifted pause over.
 */
export function updateBudget(
  ctx: StoreContext,
  id: string,
  body: BudgetBody,
  now: number,
): Budget | null {
  const old = ctx.db.prepare('SELECT * FROM budgets WHERE id = ?').get(id) as Row | undefined;
  if (!old) return null;
  const moved =
    old.scope_kind !== body.scope.kind ||
    old.scope_value !== body.scope.value ||
    old.period !== body.period ||
    old.unit !== body.unit ||
    old.amount !== body.amount ||
    old.alert_percent !== body.alertPercent ||
    old.utc_offset !== body.utcOffset;
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db
      .prepare(
        `UPDATE budgets SET name = ?, scope_kind = ?, scope_value = ?, period = ?, unit = ?, amount = ?,
           alert_percent = ?, on_limit = ?, utc_offset = ? WHERE id = ?`,
      )
      .run(
        body.name,
        body.scope.kind,
        body.scope.value,
        body.period,
        body.unit,
        body.amount,
        body.alertPercent,
        body.onLimit,
        body.utcOffset,
        id,
      );
    if (moved) {
      ctx.db.prepare('DELETE FROM budget_alerts WHERE budget_id = ?').run(id);
      ctx.db.prepare('UPDATE budgets SET allowed_period_start = NULL WHERE id = ?').run(id);
    }
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  return getBudget(ctx, id, now);
}

export function deleteBudget(ctx: StoreContext, id: string): boolean {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const result = ctx.db.prepare('DELETE FROM budgets WHERE id = ?').run(id);
    ctx.db.prepare('DELETE FROM budget_alerts WHERE budget_id = ?').run(id);
    ctx.db.exec('COMMIT');
    ctx.budgetBlocks.delete(id);
    return Number(result.changes) > 0;
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

/** Lifts the pause of a budget until its current period ends; null when there is no such budget. */
export function allowBudget(ctx: StoreContext, id: string, now: number): Budget | null {
  const row = ctx.db.prepare('SELECT * FROM budgets WHERE id = ?').get(id) as Row | undefined;
  if (!row) return null;
  const { start } = periodBounds(row.period as BudgetPeriod, row.utc_offset, now);
  ctx.db.prepare('UPDATE budgets SET allowed_period_start = ? WHERE id = ?').run(start, id);
  return getBudget(ctx, id, now);
}

/** Takes back the lift of a budget's pause, so the pause applies again at once; null when there is no such budget. */
export function withdrawBudgetAllowance(ctx: StoreContext, id: string, now: number): Budget | null {
  const row = ctx.db.prepare('SELECT id FROM budgets WHERE id = ?').get(id);
  if (!row) return null;
  ctx.db.prepare('UPDATE budgets SET allowed_period_start = NULL WHERE id = ?').run(id);
  return getBudget(ctx, id, now);
}

/**
 * Records that `threshold` was notified for the budget's period; true the first time, false when it already was.
 * Recorded before the notification is sent, so a crash in between loses a notification instead of repeating it.
 */
export function claimBudgetAlert(
  ctx: StoreContext,
  budgetId: string,
  periodStart: number,
  threshold: 'alert' | 'limit',
  now: number,
): boolean {
  const result = ctx.db
    .prepare(
      'INSERT OR IGNORE INTO budget_alerts(budget_id, period_start, threshold, sent_at) VALUES(?, ?, ?, ?)',
    )
    .run(budgetId, periodStart, threshold, now);
  return Number(result.changes) > 0;
}

/** Drops the alert rows of finished periods; the newest period of each budget is kept. */
export function pruneBudgetAlerts(ctx: StoreContext): void {
  ctx.db
    .prepare(
      `DELETE FROM budget_alerts WHERE budget_id NOT IN (SELECT id FROM budgets)
       OR period_start < (SELECT MAX(period_start) FROM budget_alerts a WHERE a.budget_id = budget_alerts.budget_id)`,
    )
    .run();
}

/** Computes the `pause` budgets from the usage table; the prompt path reads the result instead of the table. */
export function loadBudgetBlocks(ctx: StoreContext, now: number): void {
  ctx.budgetBlocks.clear();
  for (const row of rows(ctx)) status(ctx, row, now);
}

/** Recorded usage may have moved a `pause` budget over its limit; they are recomputed once usage has been quiet. */
export function budgetUsageRecorded(ctx: StoreContext): void {
  if (ctx.budgetBlocks.size === 0) return;
  for (const block of ctx.budgetBlocks.values()) block.stale = true;
  if (ctx.budgetBlockTimer) return;
  ctx.budgetBlockTimer = setTimeout(() => {
    ctx.budgetBlockTimer = null;
    try {
      refreshBudgetBlocks(ctx, Date.now());
    } catch (err) {
      log('error', 'store', 'budget refresh failed', { error: String(err) });
    }
  }, BLOCK_REFRESH_MS);
  ctx.budgetBlockTimer.unref();
}

/** Recomputes the `pause` budgets that recorded usage made stale. */
export function refreshBudgetBlocks(ctx: StoreContext, now: number): void {
  for (const [id, block] of [...ctx.budgetBlocks]) {
    if (block.stale) refreshBudgetBlock(ctx, id, now);
  }
}

function refreshBudgetBlock(ctx: StoreContext, id: string, now: number): void {
  const row = ctx.db.prepare('SELECT * FROM budgets WHERE id = ?').get(id) as Row | undefined;
  if (row) status(ctx, row, now);
  else ctx.budgetBlocks.delete(id);
}

/**
 * The first budget that refuses a new prompt in the project and on the machine: its scope covers them, its limit is
 * `pause`, the spend of its period has reached the amount and the pause was not lifted. Reads the cached `pause`
 * budgets without touching the usage table; only a budget whose period is not the one that contains `now` is
 * recomputed, and only that one.
 */
export function blockingBudget(
  ctx: StoreContext,
  where: { projectPath: string; deviceId: string },
  now: number,
): BudgetRef | null {
  let first: { id: string; block: BudgetBlock } | null = null;
  for (const [id, block] of [...ctx.budgetBlocks]) {
    const { kind, value } = block.scope;
    const covers =
      kind === 'global' ||
      (kind === 'project' && value === where.projectPath) ||
      (kind === 'device' && value === where.deviceId);
    if (!covers) continue;
    if (now < block.periodStart || now >= block.periodEnd) refreshBudgetBlock(ctx, id, now);
    const current = ctx.budgetBlocks.get(id);
    if (!current?.paused) continue;
    if (
      !first ||
      current.createdAt < first.block.createdAt ||
      (current.createdAt === first.block.createdAt && id < first.id)
    ) {
      first = { id, block: current };
    }
  }
  return first ? { id: first.id, name: first.block.name } : null;
}
