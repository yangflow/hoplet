import * as v from 'valibot';
import { SessionIdSchema } from '../ids.ts';
import { queryInt } from './common.ts';

/** What `GET /api/usage` can group by. `day` is a calendar day, `YYYY-MM-DD`, at the query's `utcOffset`. */
export const USAGE_DIMENSIONS = ['session', 'project', 'device', 'model', 'day'] as const;
export const UsageDimensionSchema = v.picklist(USAGE_DIMENSIONS);
export type UsageDimension = v.InferOutput<typeof UsageDimensionSchema>;

/** The most models the host can set a price for. */
export const MAX_USAGE_PRICES = 500;

/** The most rows one usage response holds. */
export const MAX_USAGE_ROWS = 500;

const OptionalText = v.optional(v.pipe(v.string(), v.minLength(1), v.maxLength(1024)));
const OptionalMillis = v.optional(
  v.pipe(
    v.string(),
    v.regex(/^\d{1,15}$/),
    v.transform((raw) => Number(raw)),
  ),
);

/**
 * GET /api/usage: the usage in [from, to) (Unix ms; open ended when absent), grouped by one or two of
 * `groupBy` (comma separated, default `model`), narrowed by the filters.
 */
export const UsageQuerySchema = v.object({
  from: OptionalMillis,
  to: OptionalMillis,
  groupBy: v.optional(
    v.pipe(
      v.string(),
      v.transform((raw) => raw.split(',').map((s) => s.trim())),
      v.check(
        (dims) =>
          dims.length >= 1 &&
          dims.length <= 2 &&
          new Set(dims).size === dims.length &&
          dims.every((d) => (USAGE_DIMENSIONS as readonly string[]).includes(d)),
        'groupBy is one or two different dimensions',
      ),
      v.transform((dims) => dims as UsageDimension[]),
    ),
    'model',
  ),
  /** Minutes east of UTC (-840 to 840, default 0): the `day` dimension is the day at that offset. */
  utcOffset: v.optional(
    v.pipe(
      v.string(),
      v.regex(/^-?\d{1,4}$/),
      v.transform((raw) => Number(raw)),
      v.minValue(-840),
      v.maxValue(840),
    ),
    '0',
  ),
  session: OptionalText,
  project: OptionalText,
  device: OptionalText,
  model: OptionalText,
  offset: queryInt(0, 0),
  limit: queryInt(100, 1, MAX_USAGE_ROWS),
});
export type UsageQuery = v.InferOutput<typeof UsageQuerySchema>;

const TokenCount = v.pipe(v.number(), v.integer(), v.minValue(0));

/** Tokens by kind, the sum of the messages counted. */
export const UsageTokensSchema = v.object({
  input: TokenCount,
  output: TokenCount,
  cacheWrite: TokenCount,
  cacheRead: TokenCount,
  /** Assistant messages counted: a message that Claude wrote as several lines counts once. */
  messages: TokenCount,
});
export type UsageTokens = v.InferOutput<typeof UsageTokensSchema>;

/**
 * Tokens with the cost they are estimated to have. `estimatedCostUsd` is an estimate from the price table,
 * not a bill: it is null when no model of the row has a price, and `costComplete` is false when some of the
 * row's tokens are of models without one (the cost then leaves them out).
 */
export const UsageTotalsSchema = v.object({
  ...UsageTokensSchema.entries,
  estimatedCostUsd: v.nullable(v.number()),
  costComplete: v.boolean(),
});
export type UsageTotals = v.InferOutput<typeof UsageTotalsSchema>;

/** One value of a grouping dimension: `value` identifies it, `label` is what to show (a title, a name). */
export const UsageKeySchema = v.object({ value: v.string(), label: v.string() });
export type UsageKey = v.InferOutput<typeof UsageKeySchema>;

/** One group: a value per dimension of `groupBy`, in that order. */
export const UsageRowSchema = v.object({
  ...UsageTotalsSchema.entries,
  keys: v.array(UsageKeySchema),
});
export type UsageRow = v.InferOutput<typeof UsageRowSchema>;

export const UsageResponseSchema = v.object({
  /** Always true: costs are estimates from the price table. */
  estimated: v.literal(true),
  groupBy: v.array(UsageDimensionSchema),
  /** True while the usage of sessions that existed before it was recorded is still being read. */
  backfilling: v.boolean(),
  total: UsageTotalsSchema,
  /** The groups, most tokens first (oldest first for `day`), from `offset`. */
  rows: v.array(UsageRowSchema),
  /** The groups there are, over all pages. */
  rowCount: v.number(),
  offset: v.number(),
});
export type UsageResponse = v.InferOutput<typeof UsageResponseSchema>;

/** GET /api/sessions/:id/usage: one session's usage in total and by model. */
export const SessionUsageSchema = v.object({
  sessionId: SessionIdSchema,
  estimated: v.literal(true),
  /** False while the session's lines from before usage was recorded have not been read yet. */
  complete: v.boolean(),
  total: UsageTotalsSchema,
  models: v.array(UsageRowSchema),
});
export type SessionUsage = v.InferOutput<typeof SessionUsageSchema>;

/** USD per million tokens. */
export const UsagePriceSchema = v.object({
  input: v.pipe(v.number(), v.minValue(0), v.maxValue(1_000_000)),
  output: v.pipe(v.number(), v.minValue(0), v.maxValue(1_000_000)),
  cacheWrite: v.pipe(v.number(), v.minValue(0), v.maxValue(1_000_000)),
  cacheRead: v.pipe(v.number(), v.minValue(0), v.maxValue(1_000_000)),
});
export type UsagePrice = v.InferOutput<typeof UsagePriceSchema>;

const ModelName = v.pipe(v.string(), v.minLength(1), v.maxLength(256));

/**
 * GET and PUT /api/usage/prices: `defaults` are the prices built into the core, `overrides` what the host set
 * (they win), both keyed by model ID. A price applies to the model with that ID and to its dated variants
 * (`<id>-YYYYMMDD`). PUT replaces all overrides.
 */
export const UsagePricesSchema = v.object({
  defaults: v.record(ModelName, UsagePriceSchema),
  overrides: v.record(ModelName, UsagePriceSchema),
});
export type UsagePrices = v.InferOutput<typeof UsagePricesSchema>;

export const UsagePricesBodySchema = v.object({
  overrides: v.record(ModelName, UsagePriceSchema),
});
export type UsagePricesBody = v.InferOutput<typeof UsagePricesBodySchema>;
