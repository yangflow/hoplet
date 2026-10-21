import * as v from 'valibot';
import { OPAQUE_ID_PATTERN } from '../ids.ts';
import { TrimmedSchema } from './common.ts';

/** The most budgets the host can keep. */
export const MAX_BUDGETS = 100;

/** What a budget covers: everything, the sessions of one project path, or the sessions owned by one machine. */
export const BUDGET_SCOPE_KINDS = ['global', 'project', 'device'] as const;
export const BudgetScopeKindSchema = v.picklist(BUDGET_SCOPE_KINDS);
export type BudgetScopeKind = v.InferOutput<typeof BudgetScopeKindSchema>;

/** `value` is the project path or the device ID; it is empty for `global` and not empty otherwise. */
export const BudgetScopeSchema = v.object({
  kind: BudgetScopeKindSchema,
  value: v.pipe(v.string(), v.maxLength(4096)),
});
export type BudgetScope = v.InferOutput<typeof BudgetScopeSchema>;

/** Calendar periods; a week starts on Monday. They are computed at the budget's `utcOffset`. */
export const BUDGET_PERIODS = ['day', 'week', 'month'] as const;
export const BudgetPeriodSchema = v.picklist(BUDGET_PERIODS);
export type BudgetPeriod = v.InferOutput<typeof BudgetPeriodSchema>;

/** `cost` is estimated cost in US dollars, `tokens` the sum of all token kinds. */
export const BUDGET_UNITS = ['cost', 'tokens'] as const;
export const BudgetUnitSchema = v.picklist(BUDGET_UNITS);
export type BudgetUnit = v.InferOutput<typeof BudgetUnitSchema>;

/** What happens at the limit: `warn` only notifies, `pause` also refuses new prompts. */
export const BUDGET_LIMIT_ACTIONS = ['warn', 'pause'] as const;
export const BudgetLimitActionSchema = v.picklist(BUDGET_LIMIT_ACTIONS);
export type BudgetLimitAction = v.InferOutput<typeof BudgetLimitActionSchema>;

/** `allowed` is a pause lifted for the rest of the period. */
export const BUDGET_STATES = ['ok', 'alert', 'over', 'allowed'] as const;
export const BudgetStateSchema = v.picklist(BUDGET_STATES);
export type BudgetState = v.InferOutput<typeof BudgetStateSchema>;

export const BudgetIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));

/** POST and PUT /api/budgets: the whole definition of a budget. */
export const BudgetBodySchema = v.object({
  name: v.pipe(TrimmedSchema, v.maxLength(80)),
  scope: BudgetScopeSchema,
  period: BudgetPeriodSchema,
  unit: BudgetUnitSchema,
  /** US dollars for `cost`, a whole number of tokens for `tokens`. */
  amount: v.pipe(v.number(), v.gtValue(0), v.maxValue(1e12)),
  /** The percentage of the amount that raises the alert. */
  alertPercent: v.optional(v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(100)), 80),
  onLimit: v.optional(BudgetLimitActionSchema, 'warn'),
  /** Minutes east of UTC (-840 to 840) the period boundaries are computed at. */
  utcOffset: v.optional(v.pipe(v.number(), v.integer(), v.minValue(-840), v.maxValue(840)), 0),
});
export type BudgetBody = v.InferOutput<typeof BudgetBodySchema>;

/** A budget with the spend of its current period. */
export const BudgetSchema = v.object({
  id: BudgetIdSchema,
  name: v.string(),
  scope: BudgetScopeSchema,
  period: BudgetPeriodSchema,
  unit: BudgetUnitSchema,
  amount: v.number(),
  alertPercent: v.number(),
  onLimit: BudgetLimitActionSchema,
  utcOffset: v.number(),
  createdAt: v.number(),
  /** Epoch milliseconds: the current period is [periodStart, periodEnd). */
  periodStart: v.number(),
  periodEnd: v.number(),
  /** In `unit`; for `cost` only usage of priced models counts. */
  spent: v.number(),
  /** `spent` as a percentage of `amount`, not capped. */
  percent: v.number(),
  state: BudgetStateSchema,
  /** Whether new prompts in this budget's scope are refused now. */
  paused: v.boolean(),
  /** For `cost`: false when usage of an unpriced model is in the scope and period, so `spent` is a lower bound. */
  costComplete: v.boolean(),
});
export type Budget = v.InferOutput<typeof BudgetSchema>;

export const BudgetListResponseSchema = v.object({
  budgets: v.array(BudgetSchema),
  /** Whether the usage of earlier sessions is still being read, so spend may grow. */
  backfilling: v.boolean(),
});
export type BudgetListResponse = v.InferOutput<typeof BudgetListResponseSchema>;

/** Which budget refused a prompt (`over-budget`). */
export const BudgetRefSchema = v.object({ id: BudgetIdSchema, name: v.string() });
export type BudgetRef = v.InferOutput<typeof BudgetRefSchema>;
