import type { AuditEvent, AuditVerifyResponse } from '@hoplet/shared';
import { currentLanguage, t } from './i18n.ts';
import { baseName, fill } from './templates.ts';

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

/** The values of the filter bar. `all` is no filter. */
export type AuditFilters = {
  session: string;
  machine: string;
  actor: string;
  type: string;
  time: string;
  q: string;
};

export const DEFAULT_FILTERS: AuditFilters = {
  session: 'all',
  machine: 'all',
  actor: 'all',
  type: 'all',
  time: '7d',
  q: '',
};

export type AuditOption = { value: string; label: string };

/** The windows of the Time filter, in the order of the list. */
export const TIME_SPANS: Record<string, number | null> = {
  '24h': DAY,
  '7d': 7 * DAY,
  '30d': 30 * DAY,
  all: null,
};

export function timeOptions(): AuditOption[] {
  return [
    { value: '24h', label: t('Last 24 hours') },
    { value: '7d', label: t('Last 7 days') },
    { value: '30d', label: t('Last 30 days') },
    { value: 'all', label: t('All time') },
  ];
}

/** The types and type prefixes the Type filter offers. */
export const TYPE_CHOICES = [
  'tool.call',
  'permission.',
  'session.',
  'prompt.sent',
  'mode.changed',
  'config.changed',
  'terminal.',
  'device.token.issued',
  'token.',
  'audit.',
] as const;

export function typeOptions(): AuditOption[] {
  return [
    { value: 'all', label: t('All') },
    ...TYPE_CHOICES.map((value) => ({ value, label: value })),
  ];
}

export function actorOptions(): AuditOption[] {
  return [
    { value: 'all', label: t('Anyone') },
    { value: 'operator', label: t('Operator') },
    { value: 'device', label: t('Machines') },
    { value: 'session', label: t('Sessions') },
    { value: 'core', label: t('Core') },
    { value: 'token', label: t('API tokens') },
  ];
}

/** The filters that narrow the list; false while the bar is at its defaults. */
export function isFiltered(f: AuditFilters): boolean {
  return (
    f.session !== 'all' ||
    f.machine !== 'all' ||
    f.actor !== 'all' ||
    f.type !== 'all' ||
    f.time !== DEFAULT_FILTERS.time ||
    f.q.trim() !== ''
  );
}

/** The query of `GET /api/audit/events` and `/api/audit/export` for a filter bar read at `now`. */
export function queryOf(f: AuditFilters, now: number): Record<string, string> {
  const out: Record<string, string> = {};
  if (f.session !== 'all') out.session = f.session;
  if (f.machine !== 'all') out.device = f.machine;
  if (f.actor !== 'all') out.actor = f.actor;
  if (f.type !== 'all') out.type = f.type;
  const span = TIME_SPANS[f.time];
  if (span !== null && span !== undefined) out.from = String(Math.max(0, now - span));
  const q = f.q.trim();
  if (q !== '') out.q = q.slice(0, 1024);
  return out;
}

/** Whether `event` is one `query` (as `queryOf` made it) asks for; the way fixtures filter. */
export function matchesQuery(event: AuditEvent, query: Record<string, string>): boolean {
  if (query.session !== undefined && event.sessionId !== query.session) return false;
  if (query.device !== undefined && event.deviceId !== query.device) return false;
  if (query.actor !== undefined) {
    const colon = query.actor.indexOf(':');
    const kind = colon < 0 ? query.actor : query.actor.slice(0, colon);
    if (event.actorKind !== kind) return false;
    if (colon >= 0 && event.actorId !== query.actor.slice(colon + 1)) return false;
  }
  if (query.type !== undefined) {
    const type = query.type;
    if (type.endsWith('.') ? !event.type.startsWith(type) : event.type !== type) return false;
  }
  if (query.from !== undefined && event.at < Number(query.from)) return false;
  if (query.to !== undefined && event.at >= Number(query.to)) return false;
  if (query.q !== undefined) {
    if (!JSON.stringify(event.summary).toLowerCase().includes(query.q.toLowerCase())) return false;
  }
  return true;
}

export function formatCount(n: number): string {
  return n.toLocaleString('en-US');
}

const pad = (n: number) => String(n).padStart(2, '0');

/** `14:32`, in the time zone of the browser. */
export function clock(at: number): string {
  const d = new Date(at);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** `22 Oct 14:32:07` / `10月22日 14:32:07`. */
export function fullTime(at: number): string {
  const d = new Date(at);
  const day = new Intl.DateTimeFormat(currentLanguage(), { day: 'numeric', month: 'short' }).format(
    d,
  );
  return `${day} ${clock(at)}:${pad(d.getSeconds())}`;
}

function dayStart(at: number): number {
  const d = new Date(at);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/** The heading of a day: `Today · Thu 22 Oct`, `Yesterday · …`, else `Mon 19 Oct`. */
export function dayLabel(at: number, now: number): string {
  const date = new Intl.DateTimeFormat(currentLanguage(), {
    weekday: 'short',
    day: 'numeric',
    month: 'short',
  }).format(new Date(at));
  const today = dayStart(now);
  const day = dayStart(at);
  if (day === today) return fill(t('Today · {date}'), { date });
  if (day === dayStart(today - 1)) return fill(t('Yesterday · {date}'), { date });
  return date;
}

export type AuditDay = { key: number; label: string; events: AuditEvent[] };

/** The events, in the order given, in a group per calendar day. */
export function groupByDay(events: readonly AuditEvent[], now: number): AuditDay[] {
  const days: AuditDay[] = [];
  for (const event of events) {
    const key = dayStart(event.at);
    const last = days[days.length - 1];
    if (last && last.key === key) last.events.push(event);
    else days.push({ key, label: dayLabel(event.at, now), events: [event] });
  }
  return days;
}

/** What the list shows of a record: a lead, its code-like part and a dimmed tail. */
export type SummaryParts = { lead: string; mono?: string; tail?: string };

/** Where a record's ID can be turned into a name. */
export type AuditNames = {
  device: (deviceId: string) => string;
  session: (sessionId: string) => string;
};

const str = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined);
const num = (v: unknown): number | undefined => (typeof v === 'number' ? v : undefined);
const strings = (v: unknown): string[] =>
  Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];

const AREAS: Record<string, string> = {
  'permission-rules': 'Permission rule',
  templates: 'Template',
  budgets: 'Budget',
  notifications: 'Notification settings',
  webhooks: 'Webhook',
  hooks: 'Hook',
  providers: 'Model provider',
  'watched-projects': 'Watched project',
  'device-labels': 'Machine labels',
  'resource-thresholds': 'Resource thresholds',
  'price-overrides': 'Price overrides',
};

function areaLabel(area: string): string {
  const known = AREAS[area];
  return known ? t(known) : area;
}

function actionLabel(area: string, action: string): string {
  const label = areaLabel(area);
  if (action === 'created') return fill(t('{area} added'), { area: label });
  if (action === 'deleted') return fill(t('{area} deleted'), { area: label });
  return fill(t('{area} changed'), { area: label });
}

function handoffLead(outcome: string | undefined): string {
  if (outcome === 'done') return t('Handoff done');
  if (outcome === 'failed') return t('Handoff failed');
  return outcome ? fill(t('Handoff {outcome}'), { outcome }) : t('Handoff');
}

function decisionWord(decision: string | undefined): string {
  if (decision === 'allow') return t('Allowed');
  if (decision === 'deny') return t('Denied');
  if (decision === 'ask') return t('Asked');
  return decision ?? '';
}

function toolDetail(s: Record<string, unknown>): { mono?: string; tail?: string } {
  const command = str(s.command);
  const paths = strings(s.paths);
  const count = num(s.pathCount) ?? paths.length;
  const mono = command ?? paths[0];
  if (mono === undefined) return {};
  const more = command === undefined ? count - 1 : count;
  if (more <= 0) return { mono };
  return {
    mono,
    tail: fill(more === 1 ? t('+ {count} file') : t('+ {count} files'), { count: more }),
  };
}

/** The one-line summary of a record. Never throws: a summary it does not know is shown as its type. */
export function summaryParts(event: AuditEvent, names: AuditNames): SummaryParts {
  const s = event.summary;
  const tool = str(s.tool);
  switch (event.type) {
    case 'tool.call':
      return { lead: tool ?? t('Tool call'), ...toolDetail(s) };
    case 'permission.requested':
      return {
        lead: s.planExit === true ? t('Plan approval') : (tool ?? t('Permission requested')),
        ...toolDetail(s),
      };
    case 'permission.answered': {
      const by = str(s.answeredBy);
      const word = decisionWord(str(s.decision));
      const lead = tool ? `${word} · ${tool}` : word;
      return by && by !== 'operator' && by !== 'link'
        ? { lead, tail: fill(t('closed by {reason}'), { reason: by }) }
        : { lead };
    }
    case 'permission.auto': {
      const rule = str(s.ruleId);
      return {
        lead: fill(t('{decision} by rule'), { decision: decisionWord(str(s.decision)) }),
        ...(rule ? { mono: rule } : {}),
        ...(str(s.scope) ? { tail: str(s.scope) as string } : {}),
      };
    }
    case 'mode.changed': {
      const option = str(s.option);
      const value = str(s.value) ?? (s.value === undefined ? undefined : String(s.value));
      return { lead: option && value ? `${t(option)} → ${t(value)}` : t('Setting changed') };
    }
    case 'session.created': {
      const path = str(s.projectPath);
      return { lead: t('Session created'), ...(path ? { mono: baseName(path) } : {}) };
    }
    case 'session.stopped':
      return { lead: t('Session stopped') };
    case 'session.interrupted':
      return { lead: t('Session interrupted') };
    case 'session.deleted':
      return { lead: t('Session deleted') };
    case 'session.handoff': {
      const from = str(s.from);
      const to = str(s.to);
      const outcome = str(s.outcome);
      return {
        lead: handoffLead(outcome),
        ...(from && to ? { mono: `${names.device(from)} → ${names.device(to)}` } : {}),
      };
    }
    case 'prompt.sent': {
      const length = num(s.length);
      const lead = s.steer === true ? t('Steer sent') : t('Prompt sent');
      return {
        lead:
          length === undefined
            ? lead
            : `${lead} · ${fill(t('{count} characters'), { count: formatCount(length) })}`,
      };
    }
    case 'config.changed': {
      const area = str(s.area);
      const fields = strings(s.fields);
      return {
        lead: area ? actionLabel(area, str(s.action) ?? 'updated') : t('Settings changed'),
        ...(fields.length > 0 ? { mono: fields.slice(0, 3).join(', ') } : {}),
      };
    }
    case 'device.token.issued': {
      const name = str(s.name);
      return {
        lead: s.replaced === true ? t('Device token replaced') : t('Device token issued'),
        ...(name ? { mono: name } : {}),
      };
    }
    case 'token.issued':
    case 'token.revoked': {
      const name = str(s.name);
      return {
        lead: event.type === 'token.issued' ? t('API token issued') : t('API token revoked'),
        ...(name ? { mono: name } : {}),
      };
    }
    case 'token.denied': {
      const method = str(s.method);
      const path = str(s.path);
      return {
        lead: t('API token refused'),
        ...(method && path ? { mono: `${method} ${path}` } : {}),
      };
    }
    case 'terminal.opened':
      return { lead: t('Terminal opened') };
    case 'terminal.closed':
      return { lead: t('Terminal closed') };
    case 'audit.exported': {
      const count = num(s.count);
      const format = str(s.format);
      return {
        lead:
          count !== undefined && format
            ? fill(t('Exported {count} records as {format}'), {
                count: formatCount(count),
                format: format === 'csv' ? 'CSV' : 'JSON Lines',
              })
            : t('Exported records'),
      };
    }
    case 'audit.started':
      return { lead: t('Audit log started') };
    case 'audit.checkpoint': {
      const removed = num(s.removedCount);
      return {
        lead:
          removed === undefined
            ? t('Retention checkpoint')
            : fill(t('Retention removed {count} records'), { count: formatCount(removed) }),
      };
    }
    case 'verify.result':
      return { lead: t('Chain verified') };
    default:
      return { lead: event.type };
  }
}

/** The plain text of a summary, for search and accessible names. */
export function summaryText(parts: SummaryParts): string {
  return [parts.lead, parts.mono, parts.tail].filter(Boolean).join(' · ');
}

/** Who did it, for the actor column. */
export function actorLabel(event: AuditEvent, names: AuditNames): string {
  switch (event.actorKind) {
    case 'operator':
      return t('Operator');
    case 'core':
      return t('Core');
    case 'device':
      return names.device(event.actorId);
    case 'session':
      return fill(t('Session · {name}'), { name: names.session(event.actorId) });
    case 'schedule':
      return t('Schedule');
    case 'token':
      return fill(t('API token · {id}'), { id: event.actorId });
  }
}

/** The command a record carries, and the paths, for the detail; empty when it has none. */
export function detailOf(event: AuditEvent): { command?: string; paths: string[] } {
  const command = str(event.summary.command);
  return { ...(command ? { command } : {}), paths: strings(event.summary.paths) };
}

/** `9f3a…c41e` for a SHA-256 hex. */
export function shortHash(hash: string): string {
  return hash.length > 9 ? `${hash.slice(0, 4)}…${hash.slice(-4)}` : hash;
}

/** The rest of a record's summary, one `key value` per line, for the detail. */
export function summaryRest(event: AuditEvent): [string, string][] {
  const skip = new Set(['command', 'paths', 'pathCount']);
  return Object.entries(event.summary)
    .filter(([key]) => !skip.has(key))
    .map(([key, value]) => [
      key,
      typeof value === 'string' ? value : (JSON.stringify(value) ?? String(value)),
    ]);
}

/** The sentence of a break, by reason. */
export function breakText(reason: string): string {
  switch (reason) {
    case 'hash-mismatch':
      return t('The hash of this record does not match its content.');
    case 'prev-hash-mismatch':
      return t('This record does not follow the hash of the one before it.');
    case 'gap':
      return t('A record is missing before this one.');
    case 'removed-outside-retention':
      return t('Records were removed without a retention checkpoint.');
    case 'head-mismatch':
      return t('The newest record is not the head that was written down.');
    case 'head-missing':
      return t('The head that was written down is no longer in the log.');
    default:
      return reason;
  }
}

/** The line of a verification that passed. */
export function verifiedLine(result: AuditVerifyResponse): string {
  const head = result.head
    ? ` · #${formatCount(result.head.seq)} · ${shortHash(result.head.hash)}`
    : '';
  return `${fill(t('{count} records checked'), { count: formatCount(result.checked) })}${head}`;
}

/** The file name of an export made on `date`. */
export function exportFileName(date: Date, format: 'jsonl' | 'csv'): string {
  return `hoplet-audit-${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}.${format}`;
}

/** Offers `blob` as a download; false where the browser cannot (a test). */
export function downloadBlob(name: string, blob: Blob): boolean {
  if (typeof URL.createObjectURL !== 'function') return false;
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
  return true;
}
