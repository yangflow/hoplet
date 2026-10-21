import type {
  SessionUsage,
  UsageKey,
  UsagePrices,
  UsageResponse,
  UsageRow,
  UsageTotals,
} from '@hoplet/shared';
import { type Group, rangeOf, type UsageRange } from '../../lib/usage.ts';
import type { UsageFixture } from './usage.tsx';

// Fixtures for the usage states of /__states/settings and /__states/cockpit: the week of design/screens/usage.html.
// Nothing here is read by the real screens.

/** Sunday 18 October 2026, noon UTC: the instant the pages treat as now. */
export const USAGE_NOW = Date.UTC(2026, 9, 18, 12, 0, 0);
export const WEEK: UsageRange = rangeOf('week', USAGE_NOW, 0);

const OPUS = 'claude-opus-4-5';
const SONNET = 'claude-sonnet-4-5';
const HAIKU = 'claude-haiku-4-5';
const GLM = 'glm-5';

const key = (value: string, label = value): UsageKey => ({ value, label });

/** Tokens split the way real usage is: mostly cache reads. */
function split(tokens: number) {
  const input = Math.round(tokens * 0.065);
  const output = Math.round(tokens * 0.049);
  const cacheWrite = Math.round(tokens * 0.114);
  return {
    input,
    output,
    cacheWrite,
    cacheRead: tokens - input - output - cacheWrite,
    messages: Math.max(1, Math.round(tokens / 20_000)),
  };
}

function row(tokens: number, cost: number | null, keys: UsageKey[]): UsageRow {
  return { ...split(tokens), estimatedCostUsd: cost, costComplete: cost !== null, keys };
}

function sum(rows: UsageRow[], complete = true): UsageTotals {
  const priced = rows.filter((r) => r.estimatedCostUsd !== null);
  return {
    input: rows.reduce((n, r) => n + r.input, 0),
    output: rows.reduce((n, r) => n + r.output, 0),
    cacheWrite: rows.reduce((n, r) => n + r.cacheWrite, 0),
    cacheRead: rows.reduce((n, r) => n + r.cacheRead, 0),
    messages: rows.reduce((n, r) => n + r.messages, 0),
    estimatedCostUsd: priced.length
      ? Math.round(priced.reduce((n, r) => n + (r.estimatedCostUsd ?? 0), 0) * 100) / 100
      : null,
    costComplete: complete && priced.length === rows.length,
  };
}

function response(
  groupBy: UsageResponse['groupBy'],
  rows: UsageRow[],
  over: Partial<UsageResponse> = {},
): UsageResponse {
  return {
    estimated: true,
    groupBy,
    backfilling: false,
    total: sum(rows),
    rows,
    rowCount: rows.length,
    offset: 0,
    ...over,
  };
}

const M = 1_000_000;
const MODELS: [string, number, number | null][] = [
  [OPUS, 2.9 * M, 18.23],
  [SONNET, 9.4 * M, 15.59],
  [HAIKU, 4.0 * M, 2.22],
  [GLM, 2.1 * M, null],
];
/** The share of each weekday (Monday first); the weekend is quiet. */
const DAYS = [0.1, 0.17, 0.19, 0.21, 0.14, 0.09, 0.1];

function dayRows(days: number, scale = 1): UsageRow[] {
  const rows: UsageRow[] = [];
  const first = days === 7 ? 12 : 5;
  for (let d = 0; d < days; d++) {
    const w = days === 7 ? (DAYS[d] ?? 0) : (DAYS[d % 7] ?? 0) / 2;
    const day = key(`2026-10-${String(first + d).padStart(2, '0')}`);
    for (const [model, tokens, cost] of MODELS) {
      rows.push(
        row(
          Math.round(tokens * w * scale),
          cost === null ? null : Math.round(cost * w * scale * 100) / 100,
          [day, key(model)],
        ),
      );
    }
  }
  return rows;
}

const sessions: [string, string, number, number | null][] = [
  ['Refactor the sync layer', 'hoplet', 4.8 * M, 9.8],
  ['Fix the flaky smoke test', 'hoplet', 3.9 * M, 8.12],
  ['Draft the usage view', 'hoplet', 3.3 * M, 7.31],
  ['Migrate the blog to Astro', 'blog', 3.4 * M, 6.4],
  ['Tidy the shell config', 'dotfiles', 3.0 * M, 4.41],
];
const projects: [string, number, number | null][] = [
  ['hoplet', 11.2 * M, 28.1],
  ['dotfiles', 4.1 * M, 4.3],
  ['blog', 3.1 * M, 3.64],
];
const machines: [string, number, number | null][] = [
  ['MacBook Pro', 9.6 * M, 20.1],
  ['Studio', 6.2 * M, 12.3],
  ['Office desktop', 2.6 * M, 3.64],
];

function groups(over: Partial<UsageResponse> = {}): Record<Group, UsageResponse> {
  return {
    session: response(
      ['session', 'project'],
      sessions.map(([title, project, t, c]) => row(t, c, [key(title, title), key(project)])),
      over,
    ),
    project: response(
      ['project'],
      projects.map(([p, t, c]) => row(t, c, [key(p)])),
      over,
    ),
    machine: response(
      ['device'],
      machines.map(([m, t, c]) => row(t, c, [key(m)])),
      over,
    ),
    model: response(
      ['model'],
      MODELS.map(([m, t, c]) => row(t, c, [key(m)])),
      over,
    ),
  };
}

export const PRICES: UsagePrices = {
  defaults: {
    [OPUS]: { input: 15, output: 75, cacheWrite: 18.75, cacheRead: 1.5 },
    [SONNET]: { input: 3, output: 15, cacheWrite: 3.75, cacheRead: 0.3 },
    [HAIKU]: { input: 1, output: 5, cacheWrite: 1.25, cacheRead: 0.1 },
  },
  overrides: {},
};

export function usageFixture(over: Partial<UsageFixture> = {}): UsageFixture {
  return {
    now: USAGE_NOW,
    range: WEEK,
    group: 'project',
    chart: response(['day', 'model'], dayRows(7)),
    groups: groups(),
    prices: PRICES,
    ...over,
  };
}

/** The same week with no usage at all. */
export function emptyUsage(): Pick<UsageFixture, 'chart' | 'groups'> {
  const none = (by: UsageResponse['groupBy']) => response(by, []);
  return {
    chart: none(['day', 'model']),
    groups: {
      session: none(['session', 'project']),
      project: none(['project']),
      machine: none(['device']),
      model: none(['model']),
    },
  };
}

/** The week while the core still counts existing sessions: partial numbers and a flag. */
export function backfillingUsage(): Pick<UsageFixture, 'chart' | 'groups'> {
  const part = dayRows(7, 0.35).map((r, i) => (i >= 20 ? { ...r, ...split(0) } : r));
  return {
    chart: response(['day', 'model'], part, { backfilling: true }),
    groups: groups({ backfilling: true }),
  };
}

/** Two weeks, the custom range of the design. */
export function customUsage(): Pick<UsageFixture, 'chart' | 'range'> {
  const rows = dayRows(14);
  return {
    range: { kind: 'custom', from: Date.UTC(2026, 9, 5), to: Date.UTC(2026, 9, 19) },
    chart: response(['day', 'model'], rows),
  };
}

function modelRow(model: string, tokens: number, cost: number | null) {
  return row(tokens, cost, [key(model)]);
}

/** One session's usage: priced models, or one without a price. */
export function sessionUsage(unknown = false): SessionUsage {
  const models = unknown
    ? [modelRow(GLM, 1.1 * M, null)]
    : [modelRow(SONNET, 2.3 * M, 1.82), modelRow(OPUS, 0.9 * M, 6.3)];
  const total = unknown
    ? { ...sum(models), ...splitTotals(1.1 * M, 96_000, 71_000, 181_000, 752_000) }
    : { ...sum(models), ...splitTotals(3.2 * M, 214_000, 168_000, 402_000, 2_420_000) };
  return { sessionId: 'ses-usage', estimated: true, complete: true, total, models };
}

function splitTotals(tokens: number, input: number, output: number, cw: number, cr: number) {
  return { input, output, cacheWrite: cw, cacheRead: cr, messages: Math.round(tokens / 20_000) };
}
