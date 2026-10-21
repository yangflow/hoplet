import type { UsagePrice } from '@hoplet/shared';
import type { StoreContext } from './layout.ts';

/**
 * The prices built in, USD per million tokens, keyed by model ID.
 *
 * Source: Anthropic's published per-model prices for the Claude API, as listed in the Claude API model
 * reference of 2026-10-06. A cache write costs 1.25x the input price (the 5-minute duration) and a cache read
 * 0.1x unless the source names another rate (Claude Fable 5 reads at 1.0x, Claude Fable 5.1 at 0.025x). Claude
 * Haiku 5.5 is listed at the rate for prompts of 100K tokens or fewer. Models the source gives no price for,
 * and every other vendor's models, are left out on purpose: their tokens are counted and their cost is null
 * until the host sets a price. These are estimates; the host's overrides win (GET /api/usage/prices).
 */
export const DEFAULT_USAGE_PRICES: Record<string, UsagePrice> = {
  'claude-fable-5-1': { input: 10, output: 50, cacheWrite: 12.5, cacheRead: 0.25 },
  'claude-mythos-5-1': { input: 10, output: 50, cacheWrite: 12.5, cacheRead: 0.25 },
  'claude-fable-5': { input: 10, output: 50, cacheWrite: 12.5, cacheRead: 1 },
  'claude-mythos-5': { input: 10, output: 50, cacheWrite: 12.5, cacheRead: 1 },
  'claude-opus-5-5': { input: 4, output: 20, cacheWrite: 5, cacheRead: 0.2 },
  'claude-opus-5': { input: 5, output: 25, cacheWrite: 6.25, cacheRead: 0.5 },
  'claude-opus-4-8': { input: 5, output: 25, cacheWrite: 6.25, cacheRead: 0.5 },
  'claude-sonnet-5-5': { input: 2, output: 10, cacheWrite: 2.5, cacheRead: 0.2 },
  'claude-sonnet-5': { input: 2, output: 10, cacheWrite: 2.5, cacheRead: 0.2 },
  'claude-haiku-5-5': { input: 0.1, output: 0.5, cacheWrite: 0.125, cacheRead: 0.01 },
};

/** The prices the host set; they win over the built-in ones. */
export function priceOverrides(ctx: StoreContext): Record<string, UsagePrice> {
  const rows = ctx.db
    .prepare('SELECT model, input, output, cache_write, cache_read FROM usage_prices')
    .all() as {
    model: string;
    input: number;
    output: number;
    cache_write: number;
    cache_read: number;
  }[];
  const out: Record<string, UsagePrice> = {};
  for (const r of rows) {
    out[r.model] = {
      input: r.input,
      output: r.output,
      cacheWrite: r.cache_write,
      cacheRead: r.cache_read,
    };
  }
  return out;
}

/** Replaces every override by `overrides`. */
export function setPriceOverrides(ctx: StoreContext, overrides: Record<string, UsagePrice>): void {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db.exec('DELETE FROM usage_prices');
    const insert = ctx.db.prepare(
      'INSERT INTO usage_prices(model, input, output, cache_write, cache_read) VALUES(?, ?, ?, ?, ?)',
    );
    for (const [model, p] of Object.entries(overrides)) {
      insert.run(model, p.input, p.output, p.cacheWrite, p.cacheRead);
    }
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

const DATED = /^(.+)-\d{8}$/;

/**
 * The price of `model`: the override or built-in price of that exact ID, else of the ID without a trailing
 * `-YYYYMMDD`. An override wins over a built-in price of the same ID. Null when there is none.
 */
export function priceOf(model: string, overrides: Record<string, UsagePrice>): UsagePrice | null {
  const ids = [model];
  const dated = DATED.exec(model);
  if (dated?.[1]) ids.push(dated[1]);
  for (const id of ids) {
    const found = Object.hasOwn(overrides, id)
      ? overrides[id]
      : Object.hasOwn(DEFAULT_USAGE_PRICES, id)
        ? DEFAULT_USAGE_PRICES[id]
        : undefined;
    if (found) return found;
  }
  return null;
}

/** USD for the tokens at `price`. */
export function costOf(
  price: UsagePrice,
  t: { input: number; output: number; cacheWrite: number; cacheRead: number },
): number {
  return (
    (t.input * price.input +
      t.output * price.output +
      t.cacheWrite * price.cacheWrite +
      t.cacheRead * price.cacheRead) /
    1_000_000
  );
}
