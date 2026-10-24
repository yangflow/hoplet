import { describe, expect, it } from 'vitest';
import {
  actorLabel,
  breakText,
  clock,
  DEFAULT_FILTERS,
  detailOf,
  exportFileName,
  groupByDay,
  inKind,
  isFiltered,
  matchesQuery,
  queryOf,
  sessionFilters,
  shortHash,
  summaryParts,
  summaryRest,
  TAB_KINDS,
  tabType,
} from '../src/lib/audit.ts';
import { AUDIT_EVENTS } from '../src/screens/settings/audit-fixtures.ts';

const NOW = new Date(2026, 9, 22, 14, 40).getTime();
const names = {
  device: (id: string) => (id === 'dev-macbook' ? 'MacBook Pro' : 'studio'),
  session: () => 'fix-ingest',
};
const event = (seq: number) => {
  const found = AUDIT_EVENTS.find((e) => e.seq === seq);
  if (!found) throw new Error(`no fixture ${seq}`);
  return found;
};

describe('audit filters', () => {
  it('start unfiltered, on the last 7 days', () => {
    expect(isFiltered(DEFAULT_FILTERS)).toBe(false);
    expect(queryOf(DEFAULT_FILTERS, NOW)).toEqual({ from: String(NOW - 7 * 86_400_000) });
  });

  it('turn each choice into the parameter the route knows', () => {
    const query = queryOf(
      {
        session: 's1',
        machine: 'dev-studio',
        actor: 'operator',
        type: 'permission.',
        time: 'all',
        q: ' pnpm ',
      },
      NOW,
    );
    expect(query).toEqual({
      session: 's1',
      device: 'dev-studio',
      actor: 'operator',
      type: 'permission.',
      q: 'pnpm',
    });
  });

  it('count the time window as a filter only when it is not the default', () => {
    expect(isFiltered({ ...DEFAULT_FILTERS, time: '24h' })).toBe(true);
    expect(isFiltered({ ...DEFAULT_FILTERS, q: 'x' })).toBe(true);
  });

  it('select fixture records the way the route does', () => {
    const count = (query: Record<string, string>) =>
      AUDIT_EVENTS.filter((e) => matchesQuery(e, query)).length;
    expect(count({})).toBe(14);
    expect(count({ type: 'permission.' })).toBe(3);
    expect(count({ type: 'tool.call' })).toBe(3);
    expect(count({ type: 'permission.', q: 'pnpm' })).toBe(3);
    expect(count({ q: 'terraform' })).toBe(0);
    expect(count({ actor: 'operator' })).toBe(9);
    expect(count({ device: 'dev-studio' })).toBe(1);
    expect(count({ from: String(new Date(2026, 9, 22).getTime()) })).toBe(10);
  });
});

describe('audit tab of the inspector', () => {
  it('asks the core for one session, all time, and the type of the segment', () => {
    expect(sessionFilters('s1', 'all')).toEqual({
      ...DEFAULT_FILTERS,
      session: 's1',
      time: 'all',
      type: 'all',
    });
    expect(queryOf(sessionFilters('s1', 'tools'), NOW)).toEqual({
      session: 's1',
      type: 'tool.call',
    });
    expect(queryOf(sessionFilters('s1', 'perm'), NOW)).toEqual({
      session: 's1',
      type: 'permission.',
    });
  });

  it('sorts each type into exactly one of Tools, Permissions and Other', () => {
    expect(TAB_KINDS).toEqual(['all', 'tools', 'perm', 'other']);
    expect(tabType('other')).toBe('all');
    const types = ['tool.call', 'permission.requested', 'permission.auto', 'prompt.sent'];
    expect(types.map((type) => inKind(type, 'tools'))).toEqual([true, false, false, false]);
    expect(types.map((type) => inKind(type, 'perm'))).toEqual([false, true, true, false]);
    expect(types.map((type) => inKind(type, 'other'))).toEqual([false, false, false, true]);
    expect(types.every((type) => inKind(type, 'all'))).toBe(true);
  });
});

describe('audit days', () => {
  it('group records under a heading per calendar day, newest first', () => {
    const days = groupByDay(AUDIT_EVENTS, NOW);
    expect(days.map((d) => d.events.length)).toEqual([10, 4]);
    expect(days[0]?.label).toMatch(/^Today · /);
    expect(days[1]?.label).toMatch(/^Yesterday · /);
  });

  it('write the time of a record as the clock of the browser', () => {
    expect(clock(event(1285).at)).toBe('14:32');
  });
});

describe('audit summaries', () => {
  it('lead a tool call with the tool and its command or first path', () => {
    expect(summaryParts(event(1285), names)).toEqual({
      lead: 'Bash',
      mono: 'pnpm test --filter core',
    });
    expect(summaryParts(event(1282), names)).toEqual({
      lead: 'Edit',
      mono: 'core/src/ingest/append.ts',
      tail: '+ 1 file',
    });
  });

  it('name the decision, the rule and the machines', () => {
    expect(summaryParts(event(1284), names).lead).toBe('Allowed · Bash');
    expect(summaryParts(event(1280), names)).toMatchObject({
      lead: 'Allowed by rule',
      mono: 'Run pnpm check',
    });
    expect(summaryParts(event(1275), names)).toEqual({
      lead: 'Handoff done',
      mono: 'MacBook Pro → studio',
    });
    expect(summaryParts(event(1274), names)).toEqual({
      lead: 'Permission rule added',
      mono: 'Run pnpm check',
    });
  });

  it('count the characters of a prompt and the records of an export', () => {
    expect(summaryParts(event(1278), names).lead).toBe('Prompt sent · 214 characters');
    expect(summaryParts(event(1276), names).lead).toBe('Exported 61 records as CSV');
  });

  it('name a session export, its conversation and the extra masks', () => {
    const exported = (summary: Record<string, unknown>) => ({
      ...event(1285),
      type: 'session.exported',
      summary,
    });
    expect(
      summaryParts(exported({ conversation: 'replies', changes: true, masks: 2 }), names),
    ).toEqual({ lead: 'Session exported as Markdown', mono: 'replies', tail: '2 extra masks' });
    expect(summaryParts(exported({ conversation: 'all', masks: 0 }), names)).toEqual({
      lead: 'Session exported as Markdown',
      mono: 'all',
    });
  });

  it('show a type it does not know as its type, and never throw', () => {
    const odd = { ...event(1285), type: 'future.thing', summary: { surprise: [1] } };
    expect(summaryParts(odd, names)).toEqual({ lead: 'future.thing' });
  });

  it('name who did it', () => {
    expect(actorLabel(event(1285), names)).toBe('Session · fix-ingest');
    expect(actorLabel(event(1284), names)).toBe('Operator');
    expect(actorLabel(event(1280), names)).toBe('Core');
  });

  it('names what an API token did and was refused', () => {
    const denied = {
      ...event(1284),
      type: 'token.denied',
      actorKind: 'token' as const,
      actorId: '9f3ac41e',
      summary: { id: '9f3ac41e', method: 'POST', path: '/api/sessions', reason: 'forbidden' },
    };
    expect(actorLabel(denied, names)).toBe('API token · 9f3ac41e');
    expect(summaryParts(denied, names)).toEqual({
      lead: 'API token refused',
      mono: 'POST /api/sessions',
    });
    const issued = { ...denied, type: 'token.issued', summary: { id: '9f3ac41e', name: 'ci' } };
    expect(summaryParts(issued, names)).toEqual({ lead: 'API token issued', mono: 'ci' });
  });
});

describe('audit detail', () => {
  it('hands out the scrubbed command and the paths', () => {
    expect(detailOf(event(1281)).command).toContain('Bearer ***');
    expect(detailOf(event(1282)).paths).toHaveLength(2);
    expect(detailOf(event(1278))).toEqual({ paths: [] });
  });

  it('lists the rest of the summary as key and value', () => {
    expect(summaryRest(event(1282))).toEqual([['tool', 'Edit']]);
    expect(summaryRest(event(1284))).toEqual([
      ['tool', 'Bash'],
      ['decision', 'allow'],
    ]);
  });

  it('shortens a hash', () => {
    expect(shortHash(`9f3a${'0'.repeat(56)}c41e`)).toBe('9f3a…c41e');
  });
});

describe('audit chain and export', () => {
  it('says what each break means', () => {
    expect(breakText('hash-mismatch')).toContain('does not match its content');
    expect(breakText('gap')).toContain('missing');
    expect(breakText('something-new')).toBe('something-new');
  });

  it('names the export file by day', () => {
    expect(exportFileName(new Date(2026, 9, 22), 'csv')).toBe('hoplet-audit-2026-10-22.csv');
    expect(exportFileName(new Date(2026, 0, 5), 'jsonl')).toBe('hoplet-audit-2026-01-05.jsonl');
  });
});
