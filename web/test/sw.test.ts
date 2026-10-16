import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it } from 'vitest';

type Notification = {
  title: string;
  options: { body: string; tag: string; data: { link: string | null; sessionId: string | null } };
};
type Exports = {
  describe: (payload: Record<string, unknown>, language: string) => Notification;
  target: (data: unknown, origin: string) => string;
};

// public/sw.js is a classic script for the service-worker scope; run it against a stub scope.
function load(): Exports {
  const hook: { exports?: Exports } = {};
  const scope = {
    addEventListener: () => undefined,
    location: { origin: 'https://h.example' },
    __hopletSwTest: hook,
  };
  runInNewContext(readFileSync(new URL('../public/sw.js', import.meta.url), 'utf8'), {
    ...scope,
    self: scope,
    globalThis: scope,
    URL,
  });
  if (!hook.exports) throw new Error('sw.js exports nothing under test');
  return hook.exports;
}

const sw = load();

describe('the notification a push becomes', () => {
  it('names the tool and command of a permission request, and replaces an earlier one by its request', () => {
    const n = sw.describe(
      {
        kind: 'permission',
        project: 'hoplet',
        tool: 'Bash',
        summary: 'pnpm test',
        requestId: 'r1',
        sessionId: 's1',
      },
      'en-US',
    );
    expect(n.title).toBe('Permission needed · hoplet');
    expect(n.options.body).toBe('Bash · pnpm test');
    expect(n.options.tag).toBe('request-r1');
  });

  it('titles a finished turn by its session and a failure by its reason', () => {
    expect(
      sw.describe({ kind: 'turn-done', project: 'hoplet', title: 'Fix drift' }, 'en').title,
    ).toBe('Done · Fix drift');
    expect(
      sw.describe(
        { kind: 'failure', project: 'hoplet', title: 'Migrate', reason: 'studio went offline' },
        'en',
      ).options.body,
    ).toBe('studio went offline');
  });

  it('speaks Chinese for a Chinese browser', () => {
    expect(sw.describe({ kind: 'plan', project: 'hoplet', title: 'x' }, 'zh-CN').title).toBe(
      '计划待审阅 · hoplet',
    );
  });
});

describe('where a click goes', () => {
  it('keeps the token of the action link and moves it to this origin', () => {
    expect(
      sw.target({ link: 'https://public.example/a#abc.def', sessionId: 's1' }, 'https://h.example'),
    ).toBe('https://h.example/a#abc.def');
  });

  it('ignores a link that is not the action page', () => {
    expect(
      sw.target({ link: 'https://evil.example/x', sessionId: 's 1' }, 'https://h.example'),
    ).toBe('https://h.example/?session=s%201');
  });

  it('falls back to the app', () => {
    expect(sw.target(null, 'https://h.example')).toBe('https://h.example/');
  });
});
