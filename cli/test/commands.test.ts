import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import type { Hoplet, TurnResult } from '@hoplet/sdk';
import { afterEach, describe, expect, it } from 'vitest';
import { devicesHandler, docs, lines, on, posted, run, session } from './support.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function tokenFile(text: string, mode = 0o600): string {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-cli-'));
  dirs.push(dir);
  const file = path.join(dir, 'token');
  writeFileSync(file, text);
  chmodSync(file, mode);
  return file;
}

const readFile = (file: string) => ({
  text: require_(file),
  mode: modeOf(file),
});

import { readFileSync, statSync } from 'node:fs';

function require_(file: string): string {
  return readFileSync(file, 'utf8');
}
function modeOf(file: string): number {
  return statSync(file).mode;
}

/** Replaces waitForTurn and records the options it was given. */
function waiting(result: TurnResult, seen: unknown[] = []) {
  return (client: Hoplet) => {
    client.waitForTurn = (async (id: string, options: unknown) => {
      seen.push({ id, options });
      return result;
    }) as Hoplet['waitForTurn'];
  };
}

describe('help and usage', () => {
  it('prints the help, which warns about --token', async () => {
    const r = await run([], { env: { HOPLET_TOKEN: undefined } });
    expect(r.code).toBe(0);
    expect(r.out).toContain('sessions [--device <machine>]');
    expect(r.out).toContain('Other processes on this machine can read the arguments');
    const topic = await run(['send', '--help']);
    expect(topic.out).toContain('hoplet send <session>');
  });

  it('exits 1 with a usage message on stderr', async () => {
    const r = await run(['bogus']);
    expect(r.code).toBe(1);
    expect(r.out).toBe('');
    expect(r.err).toContain('Unknown command');
    const json = await run(['bogus', '--json']);
    expect(docs(json.err)).toEqual([
      { code: 'usage', reason: 'Unknown command: bogus. See hoplet --help.' },
    ]);
  });
});

describe('credentials', () => {
  it('needs a token', async () => {
    const r = await run(['sessions'], { env: { HOPLET_TOKEN: undefined } });
    expect(r.code).toBe(1);
    expect(r.err).toContain('No token');
  });

  it('prefers --token, then HOPLET_TOKEN, then a token file', async () => {
    const handlers = [on('GET', '/api/sessions', { json: { sessions: [] } })];
    const flag = await run(['sessions', '--token', 'flag'], { handlers });
    expect(flag.calls.at(-1)?.authorization).toBe('Bearer flag');
    const env = await run(['sessions'], { handlers });
    expect(env.calls.at(-1)?.authorization).toBe('Bearer tok');
    const file = tokenFile('from-file\n');
    const viaEnv = await run(['sessions'], {
      handlers,
      env: { HOPLET_TOKEN: undefined, HOPLET_TOKEN_FILE: file },
      deps: { readFile },
    });
    expect(viaEnv.calls.at(-1)?.authorization).toBe('Bearer from-file');
    const viaFlag = await run(['sessions', '--token-file', file], {
      handlers,
      env: { HOPLET_TOKEN: undefined },
      deps: { readFile },
    });
    expect(viaFlag.calls.at(-1)?.authorization).toBe('Bearer from-file');
    expect(viaFlag.err).toBe('');
  });

  it('refuses both --token and --token-file, an empty file and a missing one', async () => {
    const file = tokenFile('x');
    expect(
      (await run(['sessions', '--token', 'a', '--token-file', file], { deps: { readFile } })).code,
    ).toBe(1);
    const empty = await run(['sessions', '--token-file', tokenFile('  \n')], {
      deps: { readFile },
    });
    expect(empty.code).toBe(1);
    expect(empty.err).toContain('is empty');
    const missing = await run(['sessions', '--token-file', '/nonexistent/token'], {
      deps: { readFile },
    });
    expect(missing.code).toBe(1);
    expect(missing.err).toContain('Cannot read');
  });

  it('warns about a token file other users can read, and never prints the token', async () => {
    const handlers = [on('GET', '/api/sessions', { json: { sessions: [] } })];
    const open = await run(['sessions', '--token-file', tokenFile('secret-token', 0o644)], {
      handlers,
      deps: { readFile },
    });
    expect(open.code).toBe(0);
    expect(open.err).toContain('can be read by other users');
    expect(open.err).not.toContain('secret-token');
    expect(open.out).not.toContain('secret-token');
  });

  it('uses HOPLET_URL, --url and the local default', async () => {
    const handlers = [on('GET', '/api/sessions', { json: { sessions: [] } })];
    expect((await run(['sessions'], { handlers })).options[0]?.url).toBe('http://core.test');
    expect((await run(['sessions', '--url', 'http://x'], { handlers })).options[0]?.url).toBe(
      'http://x',
    );
    const local = await run(['sessions'], { handlers, env: { HOPLET_URL: undefined } });
    expect(local.options[0]?.url).toBe('http://127.0.0.1:7788');
  });

  it('prints the copy of a stable error code, or {code, reason} with --json', async () => {
    const handlers = [
      on('GET', '/api/sessions', { status: 403, json: { code: 'forbidden', reason: 'no read' } }),
    ];
    const r = await run(['sessions'], { handlers });
    expect(r.code).toBe(1);
    expect(r.err).toContain('This access token is not allowed to do that.');
    const json = await run(['sessions', '--json'], { handlers });
    expect(docs(json.err)).toEqual([{ code: 'forbidden', reason: 'no read' }]);
    const unauthorized = await run(['sessions'], {
      handlers: [on('GET', '/api/sessions', { status: 401, json: { code: 'unauthorized' } })],
    });
    expect(unauthorized.err).toContain('The token was not accepted.');
  });
});

describe('sessions', () => {
  const handlers = [
    devicesHandler,
    on('GET', '/api/sessions', { json: { sessions: [session('ready')] } }),
  ];

  it('lists sessions as lines or one document, and resolves a machine name', async () => {
    const r = await run(['sessions', '--device', 'laptop', '--state', 'ready'], { handlers });
    expect(r.code).toBe(0);
    expect(lines(r.out)).toEqual(['s1\tready\tlaptop\tclaude\t/work/app\tA session']);
    const json = await run(['sessions', '--json'], { handlers });
    expect(docs(json.out)).toMatchObject([{ sessions: [{ sessionId: 's1' }] }]);
  });

  it('refuses a bad state and an unknown machine', async () => {
    expect((await run(['sessions', '--state', 'nope'], { handlers })).code).toBe(1);
    const unknown = await run(['sessions', '--device', 'nobody'], { handlers });
    expect(unknown.code).toBe(1);
    expect(unknown.err).toContain('No machine "nobody"');
  });
});

describe('new', () => {
  const created = on('POST', '/api/sessions', {
    status: 202,
    json: { ok: true, sessionId: 'new1', promptId: 'p1' },
  });

  it('starts a session with the model and options, and prints its id', async () => {
    const r = await run(
      [
        'new',
        '--device',
        'laptop',
        '--path',
        '/work/app',
        '--model',
        'opus',
        '--set',
        'fast=true',
        '--set',
        'effort=high',
        'build it',
      ],
      { handlers: [devicesHandler, created] },
    );
    expect(r.code).toBe(0);
    expect(lines(r.out)).toEqual(['new1']);
    expect(r.calls.find((c) => c.method === 'POST')?.body).toMatchObject({
      deviceId: 'dev1',
      projectPath: '/work/app',
      provider: 'claude',
      text: 'build it',
      config: { model: 'opus', fast: true, effort: 'high' },
    });
    const json = await run(['new', '--device', 'dev1', '--path', '/w', '--json', 'x'], {
      handlers: [devicesHandler, created],
    });
    expect(docs(json.out)).toEqual([{ sessionId: 'new1', promptId: expect.any(String) }]);
  });

  it('reads the prompt from standard input with -', async () => {
    const r = await run(['new', '--device', 'dev1', '--path', '/w', '-'], {
      handlers: [devicesHandler, created],
      deps: { readStdin: async () => 'from stdin\n' },
    });
    expect(r.code).toBe(0);
    expect(posted(r).text).toBe('from stdin');
  });

  it('waits with --wait and exits by the result', async () => {
    const seen: unknown[] = [];
    const r = await run(
      ['new', '--device', 'dev1', '--path', '/w', '--wait', '--timeout', '30', '--json', 'go'],
      {
        handlers: [devicesHandler, created],
        patch: waiting({ result: 'ok', state: 'ready', lineCount: 4 }, seen),
      },
    );
    expect(r.code).toBe(0);
    expect(docs(r.out)).toEqual([
      { sessionId: 'new1', promptId: expect.any(String), result: 'ok', state: 'ready' },
    ]);
    expect(seen[0]).toMatchObject({
      id: 'new1',
      options: { timeout: 30_000, since: { lineCount: 0 } },
    });
  });

  it('starts in a new worktree with --worktree and --worktree=<branch>', async () => {
    const withTree = on('POST', '/api/sessions', {
      status: 200,
      json: {
        ok: true,
        sessionId: 'new1',
        promptId: 'p1',
        worktree: { path: '/w-feat-x', branch: 'feat/x' },
      },
    });
    const handlers = [devicesHandler, withTree];
    const named = await run(
      ['new', '--device', 'dev1', '--path', '/w', '--worktree=feat/x', 'go'],
      { handlers },
    );
    expect(named.code).toBe(0);
    expect(lines(named.out)).toEqual(['new1']);
    expect(named.err).toContain('/w-feat-x');
    expect(posted(named)).toMatchObject({ text: 'go', worktree: { branch: 'feat/x' } });

    // Without a value the branch is generated, and the word after the flag is still the prompt.
    const generated = await run(
      ['new', '--device', 'dev1', '--path', '/w', '--worktree', '--json', 'go'],
      { handlers },
    );
    expect(generated.code).toBe(0);
    expect(posted(generated)).toMatchObject({ text: 'go', worktree: {} });
    expect(docs(generated.out)).toEqual([
      {
        sessionId: 'new1',
        promptId: expect.any(String),
        worktree: { path: '/w-feat-x', branch: 'feat/x' },
      },
    ]);

    const none = await run(['new', '--device', 'dev1', '--path', '/w', 'go'], { handlers });
    expect('worktree' in posted(none)).toBe(false);

    const refused = await run(
      ['new', '--device', 'dev1', '--path', '/w', '--worktree=main', 'go'],
      {
        handlers: [
          devicesHandler,
          on('POST', '/api/sessions', {
            status: 400,
            json: { code: 'invalid-request', reason: 'worktree-branch-exists', promptId: 'p1' },
          }),
        ],
      },
    );
    expect(refused.code).toBe(1);
    expect(refused.err).toContain('worktree-branch-exists');
  });

  it('needs --device and --path, and rejects --template', async () => {
    const handlers = [devicesHandler, created];
    expect((await run(['new', '--path', '/w', 'x'], { handlers })).code).toBe(1);
    const template = await run(
      ['new', '--device', 'dev1', '--path', '/w', '--template', 't', 'x'],
      { handlers },
    );
    expect(template.err).toContain('--template is not available');
    const provider = await run(
      ['new', '--device', 'dev1', '--path', '/w', '--provider', 'other', 'x'],
      { handlers },
    );
    expect(provider.code).toBe(1);
    const bad = await run(['new', '--device', 'dev1', '--path', '/w', '--set', 'novalue', 'x'], {
      handlers,
    });
    expect(bad.err).toContain('option=value');
    expect(
      r_calls(
        await run(['new', '--device', 'dev1', '--path', '/w', '--provider', 'other', 'x'], {
          handlers,
        }),
      ),
    ).toBe(0);
  });
});

function r_calls(r: { calls: { method: string }[] }): number {
  return r.calls.filter((c) => c.method === 'POST').length;
}

describe('send, prompt and wait', () => {
  const accepted = on('POST', '/api/sessions/s1/prompt', (call) => ({
    status: 202,
    json: { ok: true, promptId: (call.body as { promptId: string }).promptId },
  }));
  const current = on('GET', '/api/sessions/s1', { json: session('ready') });

  it('sends a prompt, also as prompt, and prints the prompt id', async () => {
    const r = await run(['send', 's1', 'hello', 'there'], { handlers: [current, accepted] });
    expect(r.code).toBe(0);
    expect(posted(r).text).toBe('hello there');
    expect(lines(r.out)).toHaveLength(1);
    const alias = await run(['prompt', 's1', 'hi', '--json'], { handlers: [current, accepted] });
    expect(docs(alias.out)).toEqual([{ promptId: expect.any(String) }]);
  });

  it('maps each wait result to its exit code', async () => {
    const cases: [TurnResult, number][] = [
      [{ result: 'ok', state: 'ready', lineCount: 1 }, 0],
      [{ result: 'failed', state: 'failed', error: 'boom', lineCount: 1 }, 4],
      [{ result: 'ended', state: 'idle', lineCount: 1 }, 5],
      [{ result: 'blocked', state: 'waiting-permission', lineCount: 1 }, 6],
      [{ result: 'timeout', state: 'running', lineCount: 1 }, 7],
    ];
    for (const [result, code] of cases) {
      const r = await run(['wait', 's1'], { patch: waiting(result) });
      expect(r.code).toBe(code);
      expect(lines(r.out)).toEqual([`${result.result}\t${result.state}`]);
    }
    const failed = await run(['wait', 's1', '--json'], {
      patch: waiting({ result: 'failed', state: 'failed', error: 'boom', lineCount: 1 }),
    });
    expect(docs(failed.out)).toEqual([{ result: 'failed', state: 'failed', error: 'boom' }]);
    expect(failed.code).toBe(4);
  });

  it('passes the wait flags on, and send --wait waits from the baseline', async () => {
    const seen: unknown[] = [];
    await run(['wait', 's1', '--timeout', '2.5', '--until-blocked', '--drain'], {
      patch: waiting({ result: 'ok', state: 'ready', lineCount: 1 }, seen),
    });
    expect(seen[0]).toMatchObject({
      id: 's1',
      options: { timeout: 2500, untilBlocked: true, drain: true },
    });
    const sent: unknown[] = [];
    const r = await run(['send', 's1', 'go', '--wait', '--json'], {
      handlers: [current, accepted],
      patch: waiting({ result: 'blocked', state: 'waiting-permission', lineCount: 5 }, sent),
    });
    expect(r.code).toBe(6);
    expect(docs(r.out)).toEqual([
      { promptId: expect.any(String), result: 'blocked', state: 'waiting-permission' },
    ]);
    expect(sent[0]).toMatchObject({ options: { since: { sessionId: 's1', lineCount: 3 } } });
  });

  it('refuses a bad timeout and an empty text', async () => {
    expect((await run(['wait', 's1', '--timeout', '0'])).code).toBe(1);
    expect((await run(['wait', 's1', '--timeout', 'soon'])).code).toBe(1);
    expect((await run(['send', 's1', ''], { handlers: [current, accepted] })).code).toBe(1);
  });

  it('exits 130 when stopped', async () => {
    const r = await run(['wait', 's1'], {
      patch: (client) => {
        client.waitForTurn = (async () => {
          const { HopletError } = await import('@hoplet/sdk');
          throw new HopletError({ status: 0, code: 'aborted', reason: 'stopped' });
        }) as Hoplet['waitForTurn'];
      },
    });
    expect(r.code).toBe(130);
  });
});

describe('interrupt and handoff', () => {
  it('interrupts', async () => {
    const r = await run(['interrupt', 's1'], {
      handlers: [on('POST', '/api/sessions/s1/interrupt', { json: { ok: true } })],
    });
    expect(r.code).toBe(0);
    expect(lines(r.out)).toEqual(['interrupted']);
  });

  it('hands off to a machine by name and passes --path', async () => {
    const r = await run(['handoff', 's1', 'desktop', '--path', '/clone'], {
      handlers: [
        devicesHandler,
        on('POST', '/api/sessions/s1/handoff', {
          json: { ok: true, ownerDeviceId: 'dev2', ownerEpoch: 2, worktree: '/clone', branch: 'b' },
        }),
      ],
    });
    expect(r.code).toBe(0);
    expect(r.calls.find((c) => c.method === 'POST')?.body).toEqual({
      targetDeviceId: 'dev2',
      targetPath: '/clone',
    });
    expect(docs(r.out)).toMatchObject([{ ok: true, ownerDeviceId: 'dev2' }]);
  });
});

describe('devices', () => {
  it('lists machines', async () => {
    const r = await run(['devices'], { handlers: [devicesHandler] });
    expect(lines(r.out)).toEqual(['dev1\tlaptop\tconnected', 'dev2\tdesktop\toffline']);
    const json = await run(['devices', '--json'], { handlers: [devicesHandler] });
    expect(docs(json.out)).toMatchObject([
      { devices: [{ deviceId: 'dev1' }, { deviceId: 'dev2' }] },
    ]);
  });

  it('mints a device token and prints the agent config', async () => {
    const r = await run(['devices', 'add', 'box', '--id', 'd9'], {
      handlers: [
        on('POST', '/api/devices', { json: { deviceId: 'd9', token: 'dtok', name: 'box' } }),
      ],
    });
    expect(r.code).toBe(0);
    expect(docs(r.out)).toEqual([
      { server_url: 'http://core.test', token: 'dtok', device_id: 'd9', device_name: 'box' },
    ]);
    expect(r.err).toContain('shown once');
    expect(r.calls.find((c) => c.method === 'POST')?.body).toEqual({ name: 'box', deviceId: 'd9' });
    const json = await run(['devices', 'add', 'box', '--json'], {
      handlers: [
        on('POST', '/api/devices', { json: { deviceId: 'd9', token: 'dtok', name: 'box' } }),
      ],
    });
    expect(json.err).toBe('');
  });
});

describe('tokens', () => {
  const info = {
    id: 'tk1',
    kind: 'scoped',
    name: 'ci',
    grant: { capabilities: ['read', 'send'] },
    createdAt: 0,
    expiresAt: 86_400_000,
    revokedAt: null,
    lastUsedAt: null,
    state: 'active',
  };

  it('lists tokens', async () => {
    const handlers = [on('GET', '/api/tokens', { json: { tokens: [info] } })];
    const r = await run(['tokens', 'list'], { handlers });
    expect(lines(r.out)).toEqual(['tk1\tactive\tci\tread,send\t1970-01-02T00:00:00.000Z']);
    expect(docs((await run(['tokens', 'list', '--json'], { handlers })).out)).toMatchObject([
      { tokens: [{ id: 'tk1' }] },
    ]);
  });

  it('creates a token: the secret alone on stdout, the grant as asked', async () => {
    const handlers = [
      devicesHandler,
      on('POST', '/api/tokens', { json: { ...info, token: 'secret-value' } }),
    ];
    const r = await run(
      [
        'tokens',
        'create',
        '--name',
        'ci',
        '--can',
        'read,send',
        '--allow',
        'laptop:/work/app',
        '--allow',
        'desktop',
        '--days',
        '7',
      ],
      { handlers },
    );
    expect(r.code).toBe(0);
    expect(lines(r.out)).toEqual(['secret-value']);
    expect(r.err).toContain('shown once');
    expect(r.calls.find((c) => c.method === 'POST')?.body).toEqual({
      name: 'ci',
      grant: {
        capabilities: ['read', 'send'],
        allow: [{ deviceId: 'dev1', path: '/work/app' }, { deviceId: 'dev2' }],
      },
      expiresInDays: 7,
    });
    const json = await run(['tokens', 'create', '--name', 'ci', '--can', 'read', '--json'], {
      handlers,
    });
    expect(docs(json.out)).toMatchObject([{ id: 'tk1', token: 'secret-value' }]);
    expect(json.calls.find((c) => c.method === 'POST')?.body).toMatchObject({ expiresInDays: 90 });
  });

  it('refuses a missing name, an unknown capability and a bad life', async () => {
    const handlers = [devicesHandler, on('POST', '/api/tokens', { json: { ...info, token: 's' } })];
    expect((await run(['tokens', 'create', '--can', 'read'], { handlers })).code).toBe(1);
    expect((await run(['tokens', 'create', '--name', 'a'], { handlers })).code).toBe(1);
    const bad = await run(['tokens', 'create', '--name', 'a', '--can', 'root'], { handlers });
    expect(bad.err).toContain('--can takes');
    expect(
      (
        await run(['tokens', 'create', '--name', 'a', '--can', 'read', '--days', '400'], {
          handlers,
        })
      ).code,
    ).toBe(1);
    expect(
      (
        await run(['tokens', 'create', '--name', 'a', '--can', 'read', '--allow', ':x'], {
          handlers,
        })
      ).code,
    ).toBe(1);
  });

  it('revokes', async () => {
    const r = await run(['tokens', 'revoke', 'tk1'], {
      handlers: [on('DELETE', '/api/tokens/tk1', { json: { ok: true } })],
    });
    expect(r.code).toBe(0);
    expect(lines(r.out)).toEqual(['revoked']);
  });
});
