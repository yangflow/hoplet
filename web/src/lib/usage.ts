// The data and format logic of the usage views (docs/spec.md#settings-usage): ranges at the browser's UTC
// offset, token and cost text, the daily chart by model, the ranked groups and the price table. No React in
// here: the Mac client reuses it.
import type {
  UsagePrice,
  UsagePrices,
  UsageResponse,
  UsageRow,
  UsageTokens,
  UsageTotals,
} from '@hoplet/shared';
import { currentLanguage, type Language } from './i18n.ts';

const MINUTE = 60_000;
const DAY = 86_400_000;

// ---- ranges ------------------------------------------------------------------------------------------------

export const RANGE_KINDS = ['day', 'week', 'month', 'custom'] as const;
export type RangeKind = (typeof RANGE_KINDS)[number];

/** [from, to) in Unix ms, whole days at the offset. */
export type UsageRange = { kind: RangeKind; from: number; to: number };

/** Minutes east of UTC at `at`, as the API's `utcOffset` takes it. */
export function utcOffsetOf(at: Date = new Date()): number {
  return -at.getTimezoneOffset() || 0;
}

/** The calendar day of an instant at the offset, `YYYY-MM-DD`. */
export function dayKey(ms: number, offset: number): string {
  return new Date(ms + offset * MINUTE).toISOString().slice(0, 10);
}

/** The first instant of a `YYYY-MM-DD` day at the offset; NaN when the text is not a day. */
export function dayStart(key: string, offset: number): number {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(key)) return Number.NaN;
  const ms = Date.parse(`${key}T00:00:00Z`);
  return Number.isNaN(ms) || dayKey(ms, 0) !== key ? Number.NaN : ms - offset * MINUTE;
}

/** The day `n` days after the day that starts at `start` (a start of day at the offset). */
function addDays(start: number, n: number, offset: number): number {
  return dayStart(dayKey(start + n * DAY, offset), offset);
}

function addMonths(start: number, n: number, offset: number): number {
  const d = new Date(start + offset * MINUTE);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + n, 1) - offset * MINUTE;
}

/** The day, Monday-first week or month that holds `at`. */
export function rangeOf(
  kind: Exclude<RangeKind, 'custom'>,
  at: number,
  offset: number,
): UsageRange {
  const day = dayStart(dayKey(at, offset), offset);
  if (kind === 'day') return { kind, from: day, to: addDays(day, 1, offset) };
  if (kind === 'week') {
    const weekday = new Date(day + offset * MINUTE).getUTCDay();
    const from = addDays(day, -((weekday + 6) % 7), offset);
    return { kind, from, to: addDays(from, 7, offset) };
  }
  const from = addMonths(day, 0, offset);
  return { kind, from, to: addMonths(from, 1, offset) };
}

/** The range before (`-1`) or after (`1`) this one; a custom range moves by its own length. */
export function shiftRange(range: UsageRange, step: -1 | 1, offset: number): UsageRange {
  if (range.kind === 'month') {
    const from = addMonths(range.from, step, offset);
    return { kind: 'month', from, to: addMonths(from, 1, offset) };
  }
  const days = rangeDays(range, offset);
  const by = (range.kind === 'day' ? 1 : range.kind === 'week' ? 7 : days) * step;
  return {
    kind: range.kind,
    from: addDays(range.from, by, offset),
    to: addDays(range.to, by, offset),
  };
}

/** A custom range from two `YYYY-MM-DD` days, both included; null when either is not a day or they are reversed. */
export function customRange(first: string, last: string, offset: number): UsageRange | null {
  const from = dayStart(first, offset);
  const lastStart = dayStart(last, offset);
  if (Number.isNaN(from) || Number.isNaN(lastStart) || lastStart < from) return null;
  return { kind: 'custom', from, to: addDays(lastStart, 1, offset) };
}

/** The days a range covers. */
export function rangeDays(range: UsageRange, offset: number): number {
  return Math.round((range.to - range.from) / DAY) || daysBetween(range, offset);
}

function daysBetween(range: UsageRange, offset: number): number {
  let n = 0;
  for (let at = range.from; at < range.to && n < 3660; at = addDays(at, 1, offset)) n++;
  return n;
}

/** Most columns the chart draws for a range; a longer one draws only the days that have usage. */
export const MAX_CHART_DAYS = 92;

/** The `YYYY-MM-DD` of each day of the range, oldest first; empty for a range past `MAX_CHART_DAYS`. */
export function dayKeys(range: UsageRange, offset: number): string[] {
  const keys: string[] = [];
  for (let at = range.from; at < range.to; at = addDays(at, 1, offset)) {
    if (keys.length >= MAX_CHART_DAYS) return [];
    keys.push(dayKey(at, offset));
  }
  return keys;
}

/** Whether the range ends in the future: its last days have not happened. */
export function isCurrent(range: UsageRange, now: number): boolean {
  return range.from <= now && now < range.to;
}

function utcDate(key: string): Date {
  return new Date(`${key}T00:00:00Z`);
}

function intl(lang: Language, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat(lang === 'zh-Hans' ? 'zh-CN' : 'en-US', {
    timeZone: 'UTC',
    ...options,
  });
}

/** `Oct 12 – 18, 2026`, `Oct 12, 2026`, `October 2026`, in the interface language. */
export function rangeLabel(
  range: UsageRange,
  offset: number,
  lang: Language = currentLanguage(),
): string {
  const first = utcDate(dayKey(range.from, offset));
  const last = utcDate(dayKey(range.to - 1, offset));
  if (range.kind === 'month') return intl(lang, { year: 'numeric', month: 'long' }).format(first);
  const full = intl(lang, { year: 'numeric', month: 'short', day: 'numeric' });
  if (range.kind === 'day' || first.getTime() === last.getTime()) return full.format(first);
  return full.formatRange(first, last);
}

/** `Mon 12`: the weekday and the day of the month, for a chart column. */
export function columnLabel(key: string, lang: Language = currentLanguage()): string {
  const d = utcDate(key);
  return `${intl(lang, { weekday: 'short' }).format(d)} ${d.getUTCDate()}`;
}

/** A column's full date for the tooltip: `Thu, Oct 15`. */
export function tooltipDate(key: string, lang: Language = currentLanguage()): string {
  return intl(lang, { weekday: 'short', month: 'short', day: 'numeric' }).format(utcDate(key));
}

// ---- the query ---------------------------------------------------------------------------------------------

export const GROUPS = ['session', 'project', 'machine', 'model'] as const;
export type Group = (typeof GROUPS)[number];

/** The API's name of each grouping. */
export const GROUP_DIMENSION = {
  session: 'session',
  project: 'project',
  machine: 'device',
  model: 'model',
} as const;

/** The `GET /api/usage` query of the daily chart: a row per day and model. */
export function chartQuery(range: UsageRange, offset: number) {
  return {
    from: range.from,
    to: range.to,
    groupBy: 'day,model',
    utcOffset: offset,
    limit: 500,
  };
}

/** The query of the ranked table; a session row also names its project. */
export function groupQuery(range: UsageRange, group: Group, offset: number) {
  return {
    from: range.from,
    to: range.to,
    groupBy: group === 'session' ? 'session,project' : GROUP_DIMENSION[group],
    utcOffset: offset,
    limit: 100,
  };
}

// ---- tokens and cost ---------------------------------------------------------------------------------------

/** All four kinds of tokens. */
export function totalTokens(t: Pick<UsageTokens, 'input' | 'output' | 'cacheWrite' | 'cacheRead'>) {
  return t.input + t.output + t.cacheWrite + t.cacheRead;
}

/** Three significant digits: `842`, `9.8k`, `12.4k`, `214k`, `2.42M`, `18.4M`, `1.2B`. */
export function formatTokens(n: number): string {
  if (n < 1000) return String(Math.round(n));
  const units = [
    [1e3, 'k'],
    [1e6, 'M'],
    [1e9, 'B'],
  ] as const;
  for (const [i, [size, suffix]] of units.entries()) {
    const v = n / size;
    const rounded = Number(v.toFixed(v < 10 ? 2 : v < 100 ? 1 : 0));
    // 999.6k is 1M, not "1000k".
    if (rounded >= 1000 && i < units.length - 1) continue;
    return `${rounded}${suffix}`;
  }
  return String(n);
}

const MONEY = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

/** `$8.12`; a cost under a cent is `<$0.01`; null (no price) is `—`. */
export function formatCost(usd: number | null): string {
  if (usd === null) return '—';
  if (usd > 0 && usd < 0.005) return '<$0.01';
  return MONEY.format(usd);
}

/** A group's share of the total, `38%`; under one percent `<1%`. */
export function formatShare(tokens: number, total: number): string {
  if (total <= 0 || tokens <= 0) return '0%';
  const pct = (tokens / total) * 100;
  return pct < 1 ? '<1%' : `${Math.round(pct)}%`;
}

/** The tokens of the rows that have no price at all (their cost is null), for "excludes N tokens". */
export function unpricedTokens(rows: readonly UsageRow[]): number {
  return rows.reduce((n, r) => (r.estimatedCostUsd === null ? n + totalTokens(r) : n), 0);
}

/** The models with tokens and no price, most tokens first; `rows` are grouped by model (last key). */
export function unpricedModels(
  rows: readonly UsageRow[],
): { model: string; label: string; tokens: number }[] {
  const byModel = new Map<string, { model: string; label: string; tokens: number }>();
  for (const r of rows) {
    if (r.estimatedCostUsd !== null) continue;
    const key = r.keys[r.keys.length - 1];
    if (!key) continue;
    const at = byModel.get(key.value);
    if (at) at.tokens += totalTokens(r);
    else byModel.set(key.value, { model: key.value, label: key.label, tokens: totalTokens(r) });
  }
  return [...byModel.values()].sort((a, b) => b.tokens - a.tokens);
}

/** A cost to show: null when the user asked for tokens only. */
export function costToShow(totals: Pick<UsageTotals, 'estimatedCostUsd'>, tokensOnly: boolean) {
  return tokensOnly ? null : formatCost(totals.estimatedCostUsd);
}

// ---- the daily chart ---------------------------------------------------------------------------------------

/** The class of each stack step: three models in opacity steps of the accent, then the rest in grey. */
export const MODEL_STEPS = ['s0', 's1', 's2'] as const;
export type ModelStep = (typeof MODEL_STEPS)[number] | 'ot';

export type ChartModel = { id: string; label: string; step: ModelStep };
export type ChartDay = {
  key: string;
  tokens: number;
  costUsd: number | null;
  segments: { step: ModelStep; tokens: number }[];
};
export type Chart = {
  models: ChartModel[];
  days: ChartDay[];
  /** The top of the y axis, a round number at or above the busiest day. */
  max: number;
};

/** A round number (1, 2, 5, 10, ...) at or above `n`; 1 for nothing. */
export function niceMax(n: number): number {
  if (n <= 0) return 1;
  const pow = 10 ** Math.floor(Math.log10(n));
  const f = n / pow;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * pow;
}

/**
 * The chart of rows grouped by day and model. `days` are the columns to draw (empty: only the days with
 * usage); the three biggest models keep a step each and the rest share the grey one.
 */
export function buildChart(rows: readonly UsageRow[], days: readonly string[]): Chart {
  const perModel = new Map<string, { label: string; tokens: number }>();
  for (const r of rows) {
    const model = r.keys[1];
    if (!model) continue;
    const at = perModel.get(model.value);
    if (at) at.tokens += totalTokens(r);
    else perModel.set(model.value, { label: model.label, tokens: totalTokens(r) });
  }
  const ranked = [...perModel.entries()].sort((a, b) => b[1].tokens - a[1].tokens);
  const stepOf = new Map<string, ModelStep>();
  const models: ChartModel[] = [];
  ranked.forEach(([id, m], i) => {
    const step: ModelStep = MODEL_STEPS[i] ?? 'ot';
    stepOf.set(id, step);
    if (i < MODEL_STEPS.length) models.push({ id, label: m.label, step });
  });
  if (ranked.length > MODEL_STEPS.length) models.push({ id: '', label: '', step: 'ot' });

  const byDay = new Map<string, UsageRow[]>();
  for (const r of rows) {
    const key = r.keys[0]?.value;
    if (key === undefined) continue;
    byDay.set(key, [...(byDay.get(key) ?? []), r]);
  }
  const keys = days.length > 0 ? [...days] : [...byDay.keys()].sort();
  const columns: ChartDay[] = keys.map((key) => {
    const part = byDay.get(key) ?? [];
    const segments = [...MODEL_STEPS, 'ot' as const]
      .map((step) => ({
        step,
        tokens: part
          .filter((r) => stepOf.get(r.keys[1]?.value ?? '') === step)
          .reduce((n, r) => n + totalTokens(r), 0),
      }))
      .filter((s) => s.tokens > 0);
    const priced = part.filter((r) => r.estimatedCostUsd !== null);
    return {
      key,
      tokens: segments.reduce((n, s) => n + s.tokens, 0),
      costUsd: priced.length ? priced.reduce((n, r) => n + (r.estimatedCostUsd ?? 0), 0) : null,
      segments,
    };
  });
  return { models, days: columns, max: niceMax(Math.max(0, ...columns.map((c) => c.tokens))) };
}

// ---- the ranked groups -------------------------------------------------------------------------------------

export type GroupRow = {
  id: string;
  name: string;
  /** The second line: a session's project. */
  detail: string | null;
  tokens: number;
  costUsd: number | null;
  /** Some of the row's tokens are of models without a price. */
  unpriced: boolean;
};

export function groupRows(response: Pick<UsageResponse, 'rows'>): GroupRow[] {
  return response.rows.map((r) => ({
    id: r.keys.map((k) => k.value).join('\u0000'),
    name: r.keys[0]?.label ?? '',
    detail: r.keys[1]?.label ?? null,
    tokens: totalTokens(r),
    costUsd: r.estimatedCostUsd,
    unpriced: !r.costComplete,
  }));
}

// ---- prices ------------------------------------------------------------------------------------------------

export const PRICE_FIELDS = ['input', 'output', 'cacheWrite', 'cacheRead'] as const;
export type PriceField = (typeof PRICE_FIELDS)[number];

export type PriceRow = {
  model: string;
  price: UsagePrice | null;
  source: 'builtin' | 'yours' | 'none';
  /** Tokens counted without a price in the range, for a model that has none. */
  tokens: number;
};

/** A model's ID without a `-YYYYMMDD` date, the way prices apply to dated variants. */
export function undated(model: string): string {
  return model.replace(/-\d{8}$/, '');
}

/**
 * The price table to show: every built-in price, the host's own on top of them, and a row without a price for
 * each model in `missing`. Overrides come first within their kind, in the order the table sorts by name.
 */
export function priceRows(
  prices: UsagePrices,
  missing: readonly { model: string; tokens: number }[] = [],
): PriceRow[] {
  const rows = new Map<string, PriceRow>();
  for (const [model, price] of Object.entries(prices.defaults)) {
    rows.set(model, { model, price, source: 'builtin', tokens: 0 });
  }
  for (const [model, price] of Object.entries(prices.overrides)) {
    rows.set(model, { model, price, source: 'yours', tokens: 0 });
  }
  for (const m of missing) {
    if (!rows.has(m.model) && !rows.has(undated(m.model))) {
      rows.set(m.model, { model: m.model, price: null, source: 'none', tokens: m.tokens });
    }
  }
  const order = { none: 0, yours: 1, builtin: 2 } as const;
  return [...rows.values()].sort(
    (a, b) => order[a.source] - order[b.source] || a.model.localeCompare(b.model),
  );
}

/** A price as the fields of the editor show it. */
export function priceFields(price: UsagePrice | null): Record<PriceField, string> {
  const text = (n: number | undefined) => (n === undefined ? '' : String(n));
  return {
    input: text(price?.input),
    output: text(price?.output),
    cacheWrite: text(price?.cacheWrite),
    cacheRead: text(price?.cacheRead),
  };
}

/** Two decimals for the table: `15.00`, `0.08`; more when a price needs them. */
export function formatPrice(n: number): string {
  const text = n.toFixed(2);
  return Number(text) === n ? text : String(n);
}

/** The price the fields hold; an empty field is 0. Null when a field is not a number from 0 to 1,000,000. */
export function parsePrice(fields: Record<PriceField, string>): UsagePrice | null {
  const out: Partial<UsagePrice> = {};
  for (const f of PRICE_FIELDS) {
    const raw = fields[f].trim();
    const n = raw === '' ? 0 : Number(raw);
    if (!Number.isFinite(n) || n < 0 || n > 1_000_000) return null;
    out[f] = n;
  }
  return out as UsagePrice;
}

/** The overrides with this model's price set. */
export function withOverride(
  overrides: UsagePrices['overrides'],
  model: string,
  price: UsagePrice,
): UsagePrices['overrides'] {
  return { ...overrides, [model]: price };
}

/** The overrides without this model's price. */
export function withoutOverride(
  overrides: UsagePrices['overrides'],
  model: string,
): UsagePrices['overrides'] {
  const { [model]: _gone, ...rest } = overrides;
  return rest;
}
