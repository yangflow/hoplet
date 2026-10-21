// The logic of budgets (docs/spec.md#budgets): how a row reads, the dates a period ends and begins, the form and
// the body it makes. No React in here: the Mac client reuses it.
import type {
  Budget,
  BudgetBody,
  BudgetPeriod,
  BudgetScopeKind,
  BudgetUnit,
  DeviceInfo,
} from '@hoplet/shared';
import { currentLanguage, type Language, t } from './i18n.ts';
import { formatCost, formatTokens, utcOffsetOf } from './usage.ts';

const MINUTE = 60_000;

/** The last segment of a project path, whichever separator the machine uses. */
export function projectLabel(path: string): string {
  const parts = path.split(/[\\/]+/).filter((x) => x !== '');
  return parts[parts.length - 1] ?? path;
}

// ---- how a row reads ------------------------------------------------------------------------------------------

/** What a row says about its budget; one of these decides the status line and the action. */
export type BudgetStatus = 'ok' | 'near' | 'over' | 'paused' | 'allowed';

export function statusOf(b: Pick<Budget, 'state' | 'onLimit' | 'paused'>): BudgetStatus {
  if (b.state === 'allowed') return 'allowed';
  if (b.state === 'over') return b.onLimit === 'pause' || b.paused ? 'paused' : 'over';
  return b.state === 'alert' ? 'near' : 'ok';
}

/** The colour of a row's bar: red where new prompts are refused, amber at the alert line or past a warn-only limit. */
export function barKind(status: BudgetStatus): 'stop' | 'warn' | '' {
  if (status === 'paused') return 'stop';
  return status === 'ok' ? '' : 'warn';
}

/** The fill of a bar and its progress value: the percent, capped at the full bar. */
export function barPercent(b: Pick<Budget, 'percent'>): number {
  return Math.max(0, Math.min(100, Math.round(b.percent)));
}

/** An amount the way the budget counts it: `$10.60`, or tokens as `10.3M`. */
export function amountText(unit: BudgetUnit, n: number): string {
  return unit === 'cost' ? formatCost(n) : formatTokens(n);
}

/** What is left before the limit, or by how much it is exceeded, in the budget's unit. */
export function leftOf(b: Pick<Budget, 'amount' | 'spent'>): number {
  return Math.abs(b.amount - b.spent);
}

/** Whether the budget counts a session: global counts all, project the sessions of its path, device those it owns. */
export function covers(
  b: Pick<Budget, 'scope'>,
  session: { projectPath: string; ownerDeviceId?: string | undefined },
): boolean {
  const { kind, value } = b.scope;
  if (kind === 'global') return true;
  return kind === 'project' ? value === session.projectPath : value === session.ownerDeviceId;
}

/** Whether a budget has reached its alert line or gone past it, allowed or not. */
export function pastAlert(b: Pick<Budget, 'state'>): boolean {
  return b.state !== 'ok';
}

// ---- words and dates -------------------------------------------------------------------------------------------

export const PERIOD_WORD: Record<BudgetPeriod, () => string> = {
  day: () => t('daily'),
  week: () => t('weekly'),
  month: () => t('monthly'),
};

export const SCOPE_LABEL: Record<BudgetScopeKind, () => string> = {
  global: () => t('Global'),
  project: () => t('Project'),
  device: () => t('Machine'),
};

function intl(lang: Language, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat(lang === 'zh-Hans' ? 'zh-CN' : 'en-US', {
    timeZone: 'UTC',
    ...options,
  });
}

/** An instant as a date at the budget's offset: `Nov 1`, or `Mon, Oct 26` with the weekday. */
export function dateText(
  ms: number,
  offset: number,
  weekday = false,
  lang: Language = currentLanguage(),
): string {
  const at = new Date(ms + offset * MINUTE);
  const day = intl(lang, { month: 'short', day: 'numeric' }).format(at);
  if (!weekday) return day;
  const name = intl(lang, { weekday: 'short' }).format(at);
  return lang === 'zh-Hans' ? `${day}（${name}）` : `${name}, ${day}`;
}

/** The last day of the period, `Oct 31`: the day an allowance lasts until. */
export function endDate(b: Pick<Budget, 'periodEnd' | 'utcOffset'>): string {
  return dateText(b.periodEnd - 1, b.utcOffset);
}

/** The first day of the next period, `Nov 1`. */
export function nextDate(b: Pick<Budget, 'periodEnd' | 'utcOffset'>): string {
  return dateText(b.periodEnd, b.utcOffset);
}

/** When the period resets: `at midnight` for a day, the weekday and date for a week, the date for a month. */
export function resetText(b: Pick<Budget, 'period' | 'periodEnd' | 'utcOffset'>): string {
  if (b.period === 'day') return t('at midnight');
  return dateText(b.periodEnd, b.utcOffset, b.period === 'week');
}

/** The row's title: `Global`, `Project · hoplet`, `Machine · studio`, then the period. */
export function scopeName(b: Pick<Budget, 'scope' | 'name'>): string {
  return b.scope.kind === 'global' ? t('Global') : `${SCOPE_LABEL[b.scope.kind]()} · ${b.name}`;
}

/** `hoplet · weekly`: a budget by its name and period, as the session's popover lists it. */
export function shortName(b: Pick<Budget, 'scope' | 'name' | 'period'>): string {
  const base = b.scope.kind === 'global' ? t('Global') : b.name;
  return `${base} · ${PERIOD_WORD[b.period]()}`;
}

// ---- the form --------------------------------------------------------------------------------------------------

export type BudgetDraft = {
  /** null for a budget not saved yet. */
  id: string | null;
  scope: BudgetScopeKind;
  /** The project path or device ID the scope names; empty for global. */
  target: string;
  period: BudgetPeriod;
  unit: BudgetUnit;
  /** What the person typed: dollars for cost, millions of tokens for tokens. */
  limit: string;
  alert: string;
  onLimit: 'warn' | 'pause';
  utcOffset: number;
};

export function newDraft(): BudgetDraft {
  return {
    id: null,
    scope: 'global',
    target: '',
    period: 'month',
    unit: 'cost',
    limit: '',
    alert: '80',
    onLimit: 'warn',
    utcOffset: utcOffsetOf(),
  };
}

function trimmed(n: number): string {
  return String(Number(n.toFixed(6)));
}

export function draftOf(b: Budget): BudgetDraft {
  return {
    id: b.id,
    scope: b.scope.kind,
    target: b.scope.value,
    period: b.period,
    unit: b.unit,
    limit: trimmed(b.unit === 'tokens' ? b.amount / 1e6 : b.amount),
    alert: String(b.alertPercent),
    onLimit: b.onLimit,
    utcOffset: b.utcOffset,
  };
}

/** The limit typed, as the API takes it (dollars, or whole tokens); null when it is not a positive number. */
export function parseLimit(text: string, unit: BudgetUnit): number | null {
  const n = Number(text.trim().replace(',', '.'));
  if (text.trim() === '' || !Number.isFinite(n) || n <= 0) return null;
  const amount = unit === 'tokens' ? Math.round(n * 1e6) : Math.round(n * 100) / 100;
  return amount > 0 && amount <= 1e12 ? amount : null;
}

/** The alert line typed; null when it is not a whole number from 1 to 100. */
export function parseAlert(text: string): number | null {
  const s = text.trim();
  if (!/^\d{1,3}$/.test(s)) return null;
  const n = Number(s);
  return n >= 1 && n <= 100 ? n : null;
}

export type DraftProblem = 'target' | 'limit' | 'alert';

/** What is wrong with a draft, in the order of the form; null when it can be saved. */
export function problemOf(d: BudgetDraft): DraftProblem | null {
  if (d.scope !== 'global' && d.target === '') return 'target';
  if (parseLimit(d.limit, d.unit) === null) return 'limit';
  return parseAlert(d.alert) === null ? 'alert' : null;
}

/** The name a budget is saved under: the project's folder, the machine's name, or Global. */
export function nameOf(d: BudgetDraft, devices: readonly Pick<DeviceInfo, 'deviceId' | 'name'>[]) {
  if (d.scope === 'global') return t('Global');
  if (d.scope === 'project') return projectLabel(d.target);
  return devices.find((x) => x.deviceId === d.target)?.name ?? d.target;
}

/** The body to save, or null while the draft has a problem. */
export function toBody(
  d: BudgetDraft,
  devices: readonly Pick<DeviceInfo, 'deviceId' | 'name'>[],
): BudgetBody | null {
  if (problemOf(d) !== null) return null;
  const amount = parseLimit(d.limit, d.unit);
  const alertPercent = parseAlert(d.alert);
  if (amount === null || alertPercent === null) return null;
  return {
    name: nameOf(d, devices),
    scope: { kind: d.scope, value: d.scope === 'global' ? '' : d.target },
    period: d.period,
    unit: d.unit,
    amount,
    alertPercent,
    onLimit: d.onLimit,
    utcOffset: d.utcOffset,
  };
}
