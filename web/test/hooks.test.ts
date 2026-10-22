import { describe, expect, it } from 'vitest';
import {
  checkHookForm,
  emptyHookForm,
  eventsSummary,
  formOfHook,
  hostOf,
  splitHooks,
  toCreate,
  toUpdate,
} from '../src/lib/hooks.ts';
import { hooksFixture } from '../src/screens/settings/hooks-fixtures.ts';

const webhook = () => {
  const h = hooksFixture().list.hooks.find((x) => x.kind === 'webhook');
  if (h?.kind !== 'webhook') throw new Error('no webhook in the fixture');
  return h;
};

describe('hook form', () => {
  it('refuses an empty form with a reason per field', () => {
    const c = checkHookForm(emptyHookForm());
    expect(c.ok).toBe(false);
    expect(Object.keys(c.errors).sort()).toEqual(['events', 'label', 'url']);
  });

  it('wants a project when the scope is one project', () => {
    const f = {
      ...emptyHookForm(),
      label: 'a',
      url: 'https://x.example/h',
      events: ['session.error' as const],
    };
    expect(checkHookForm(f).ok).toBe(true);
    expect(checkHookForm({ ...f, scope: 'project' }).errors.project).toBeDefined();
  });

  it('sends the URL of an edit only when it changed', () => {
    const f = formOfHook(webhook());
    expect(toUpdate(f).url).toBeUndefined();
    expect(toUpdate({ ...f, url: 'https://other.example/h' }).url).toBe('https://other.example/h');
  });

  it('builds a create request with the events in their fixed order', () => {
    const f = {
      ...emptyHookForm(),
      label: ' Deploy ',
      url: ' https://x.example/h ',
      events: ['handoff.completed' as const, 'session.started' as const],
    };
    const r = toCreate(f);
    expect(r.label).toBe('Deploy');
    expect(r.url).toBe('https://x.example/h');
    expect(r.events).toEqual(['session.started', 'handoff.completed']);
  });
});

describe('hook list', () => {
  it('groups scripts by machine and keeps the webhooks apart', () => {
    const { webhooks, machines } = splitHooks(hooksFixture().list.hooks);
    expect(webhooks.length).toBe(5);
    expect(machines.length).toBeGreaterThan(1);
    expect(machines.every((m) => m.scripts.length > 0)).toBe(true);
  });

  it('names a host and a short event list', () => {
    expect(hostOf('https://ops.example.net/hoplet?token=1')).toBe('ops.example.net');
    expect(eventsSummary(['turn.completed'])).toContain('turn.completed');
  });
});
