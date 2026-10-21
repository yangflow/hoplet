import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Budgets (#75). A budget names a scope (`global`, a `project` path or a `device` ID in `scope_value`), a
 * calendar period computed at `utc_offset` minutes east of UTC, a unit and an amount. `allowed_period_start` is
 * the start of the period in which the host lifted the pause, so the lift ends with the period. `budget_alerts`
 * holds one row per budget, period start and threshold (`alert` or `limit`) already notified, which keeps a
 * restart from sending the same notification again.
 */
export const budgets: Migration = {
  id: 22,
  name: 'budgets',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE budgets(
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        scope_kind TEXT NOT NULL,
        scope_value TEXT NOT NULL,
        period TEXT NOT NULL,
        unit TEXT NOT NULL,
        amount REAL NOT NULL,
        alert_percent INTEGER NOT NULL,
        on_limit TEXT NOT NULL,
        utc_offset INTEGER NOT NULL,
        created_at INTEGER NOT NULL,
        allowed_period_start INTEGER
      );
      CREATE TABLE budget_alerts(
        budget_id TEXT NOT NULL,
        period_start INTEGER NOT NULL,
        threshold TEXT NOT NULL,
        sent_at INTEGER NOT NULL,
        PRIMARY KEY(budget_id, period_start, threshold)
      ) WITHOUT ROWID;
    `);
  },
};
