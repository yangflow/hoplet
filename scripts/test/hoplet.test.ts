import { describe, expect, it, vi } from 'vitest';
import { parseArgv, runCli } from '../hoplet/cli.ts';
import { HostClient } from '../hoplet/client.ts';

describe('parseArgv', () => {
  it('reads env defaults and flags', () => {
    const r = parseArgv(['prompt', 's1', 'hi'], {
      HOPLET_URL: 'http://example:9',
      HOPLET_TOKEN: 'secret',
    });
    expect(r).toEqual({
      ok: true,
      url: 'http://example:9',
      token: 'secret',
      args: ['prompt', 's1', 'hi'],
    });
  });

  it('requires a token', () => {
    const r = parseArgv(['prompt', 's1', 'hi'], {});
    expect(r.ok).toBe(false);
  });

  it('allows --token and --url override', () => {
    const r = parseArgv(['--url', 'http://x', '--token', 't', 'prompt', 's', 'a'], {});
    expect(r).toMatchObject({ ok: true, url: 'http://x', token: 't', args: ['prompt', 's', 'a'] });
  });
});

describe('HostClient', () => {
  it('posts prompt with Authorization', async () => {
    const fetchImpl = vi.fn(async (_url: string, init?: RequestInit) => {
      expect(init?.method).toBe('POST');
      expect(init?.headers).toMatchObject({
        Authorization: 'Bearer tok',
        'content-type': 'application/json',
      });
      expect(JSON.parse(String(init?.body))).toEqual({ text: 'go', promptId: expect.any(String) });
      return new Response(JSON.stringify({ ok: true, promptId: 'p1' }), { status: 200 });
    }) as unknown as typeof fetch;
    const client = new HostClient({
      baseUrl: 'http://127.0.0.1:7788/',
      token: 'tok',
      fetchImpl,
    });
    const r = await client.prompt('parent', 'go');
    expect(r).toEqual({ ok: true, data: { ok: true, promptId: 'p1' } });
    expect(fetchImpl).toHaveBeenCalledWith(
      'http://127.0.0.1:7788/api/sessions/parent/prompt',
      expect.any(Object),
    );
  });

  it('chooses the message from the error code', async () => {
    const fetchImpl = vi.fn(
      async () => new Response(JSON.stringify({ code: 'owner-offline' }), { status: 409 }),
    ) as unknown as typeof fetch;
    const client = new HostClient({ baseUrl: 'http://h', token: 't', fetchImpl });
    const r = await client.prompt('s', 'x');
    expect(r).toEqual({
      ok: false,
      error: {
        status: 409,
        code: 'owner-offline',
        message: 'The machine that owns this session is offline.',
      },
    });
  });

  it('adds the reason of a rejected request and names an unknown body by its status', async () => {
    const rejected = vi.fn(
      async () =>
        new Response(JSON.stringify({ code: 'rejected', reason: 'claude is busy' }), {
          status: 502,
        }),
    ) as unknown as typeof fetch;
    const r = await new HostClient({ baseUrl: 'http://h', token: 't', fetchImpl: rejected }).prompt(
      's',
      'x',
    );
    expect(r).toMatchObject({
      ok: false,
      error: { code: 'rejected', message: 'The machine refused the request. (claude is busy)' },
    });
    const html = vi.fn(
      async () => new Response('<html>', { status: 502 }),
    ) as unknown as typeof fetch;
    const h = await new HostClient({ baseUrl: 'http://h', token: 't', fetchImpl: html }).prompt(
      's',
      'x',
    );
    expect(h).toEqual({ ok: false, error: { status: 502, code: null, message: 'HTTP 502' } });
  });

  it('refuses a success body that is not a prompt result', async () => {
    const fetchImpl = vi.fn(
      async () => new Response(JSON.stringify({ ok: true }), { status: 200 }),
    ) as unknown as typeof fetch;
    const r = await new HostClient({ baseUrl: 'http://h', token: 't', fetchImpl }).prompt('s', 'x');
    expect(r).toMatchObject({ ok: false, error: { code: null } });
  });
});

describe('runCli', () => {
  it('prompt prints JSON and exits 0', async () => {
    const lines: string[] = [];
    const errs: string[] = [];
    let posted: unknown;
    const fetchImpl = (async (_url: string, init?: RequestInit) => {
      posted = init?.body ? JSON.parse(String(init.body)) : undefined;
      return new Response(JSON.stringify({ ok: true, promptId: 'p' }), { status: 200 });
    }) as typeof fetch;
    const code = await runCli(
      ['prompt', 'p1', 'hello', 'world'],
      { HOPLET_TOKEN: 't', HOPLET_URL: 'http://h' },
      { stdout: (l) => lines.push(l), stderr: (l) => errs.push(l) },
      fetchImpl,
    );
    expect(code).toBe(0);
    expect(errs).toEqual([]);
    expect(JSON.parse(lines[0] ?? '')).toEqual({ ok: true, promptId: 'p' });
    expect(posted).toEqual({ text: 'hello world', promptId: expect.any(String) });
  });

  it('interrupt posts to the interrupt route, prints JSON and exits 0', async () => {
    const lines: string[] = [];
    const calls: { url: string; method?: string | undefined; body?: unknown }[] = [];
    const fetchImpl = (async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method, body: init?.body });
      return new Response(JSON.stringify({ ok: true }), { status: 200 });
    }) as typeof fetch;
    const code = await runCli(
      ['interrupt', 's1'],
      { HOPLET_TOKEN: 't', HOPLET_URL: 'http://h' },
      { stdout: (l) => lines.push(l), stderr: () => {} },
      fetchImpl,
    );
    expect(code).toBe(0);
    expect(JSON.parse(lines[0] ?? '')).toEqual({ ok: true });
    expect(calls).toEqual([
      { url: 'http://h/api/sessions/s1/interrupt', method: 'POST', body: undefined },
    ]);
  });

  it('interrupt needs exactly one session and names a refusal', async () => {
    const errs: string[] = [];
    const io = { stdout: () => {}, stderr: (l: string) => errs.push(l) };
    expect(await runCli(['interrupt'], { HOPLET_TOKEN: 't' }, io, vi.fn() as never)).toBe(1);
    expect(await runCli(['interrupt', 'a', 'b'], { HOPLET_TOKEN: 't' }, io, vi.fn() as never)).toBe(
      1,
    );
    expect(errs).toEqual([
      'usage: hoplet interrupt <sessionId>',
      'usage: hoplet interrupt <sessionId>',
    ]);
    const refused = vi.fn(
      async () => new Response(JSON.stringify({ code: 'not-running' }), { status: 409 }),
    ) as unknown as typeof fetch;
    errs.length = 0;
    expect(await runCli(['interrupt', 's1'], { HOPLET_TOKEN: 't' }, io, refused)).toBe(2);
    expect(errs).toEqual(['No turn is running.']);
  });

  it('prompt names why the core refused it', async () => {
    const errs: string[] = [];
    const refused = vi.fn(
      async () =>
        new Response(JSON.stringify({ code: 'external', promptId: 'p' }), { status: 409 }),
    ) as unknown as typeof fetch;
    const code = await runCli(
      ['prompt', 's1', 'x'],
      { HOPLET_TOKEN: 't' },
      { stdout: () => {}, stderr: (l) => errs.push(l) },
      refused,
    );
    expect(code).toBe(2);
    expect(errs).toEqual([
      'The session was written from a terminal. It can be read but not controlled.',
    ]);
  });

  it('rejects commands that no longer exist', async () => {
    const errs: string[] = [];
    const code = await runCli(
      ['links', 'list', 'a'],
      { HOPLET_TOKEN: 't' },
      { stdout: () => {}, stderr: (l) => errs.push(l) },
      vi.fn() as unknown as typeof fetch,
    );
    expect(code).toBe(1);
    expect(errs[0]).toContain('unknown command: links');
  });

  it('exits 2 on API failure', async () => {
    const errs: string[] = [];
    const fetchImpl = vi.fn(
      async () => new Response(JSON.stringify({ code: 'timeout' }), { status: 504 }),
    ) as unknown as typeof fetch;
    const code = await runCli(
      ['prompt', 'p', 'x'],
      { HOPLET_TOKEN: 't' },
      { stdout: () => {}, stderr: (l) => errs.push(l) },
      fetchImpl,
    );
    expect(code).toBe(2);
    expect(errs).toEqual(['The machine did not answer in time.']);
  });
});

describe('hoplet devices', () => {
  const env = { HOPLET_TOKEN: 'host', HOPLET_URL: 'http://h/' };

  it('parses --id', () => {
    const r = parseArgv(['devices', 'add', 'laptop', '--id', 'd1'], env);
    expect(r).toMatchObject({ ok: true, args: ['devices', 'add', 'laptop'], id: 'd1' });
    expect(parseArgv(['devices', 'add', 'x', '--id'], env).ok).toBe(false);
  });

  it('devices add posts the name and prints the agent config, the hint on stderr', async () => {
    const lines: string[] = [];
    const errs: string[] = [];
    const calls: { url: string; method?: string | undefined; body?: unknown }[] = [];
    const fetchImpl = (async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method, body: JSON.parse(String(init?.body)) });
      return new Response(JSON.stringify({ deviceId: 'd1', token: 'dev-tok', name: 'my laptop' }), {
        status: 200,
      });
    }) as typeof fetch;
    const code = await runCli(
      ['devices', 'add', 'my', 'laptop', '--id', 'd1'],
      env,
      { stdout: (l) => lines.push(l), stderr: (l) => errs.push(l) },
      fetchImpl,
    );
    expect(code).toBe(0);
    expect(calls).toEqual([
      { url: 'http://h/api/devices', method: 'POST', body: { name: 'my laptop', deviceId: 'd1' } },
    ]);
    expect(lines).toHaveLength(1);
    expect(JSON.parse(lines[0] ?? '')).toEqual({
      server_url: 'http://h',
      token: 'dev-tok',
      device_id: 'd1',
      device_name: 'my laptop',
    });
    expect(errs).toEqual([
      'Save as ~/.hoplet/config.json on that machine and run hoplet-agent daemon. The token is shown once.',
    ]);
    expect(errs.join()).not.toContain('dev-tok');
  });

  it('devices add without --id sends only the name', async () => {
    let body: unknown;
    const fetchImpl = (async (_url: string, init?: RequestInit) => {
      body = JSON.parse(String(init?.body));
      return new Response(JSON.stringify({ deviceId: 'g', token: 't', name: 'n' }), {
        status: 200,
      });
    }) as typeof fetch;
    const io = { stdout: () => {}, stderr: () => {} };
    expect(await runCli(['devices', 'add', 'n'], env, io, fetchImpl)).toBe(0);
    expect(body).toEqual({ name: 'n' });
  });

  it('devices add needs a name and maps errors without printing a config', async () => {
    const lines: string[] = [];
    const errs: string[] = [];
    const io = { stdout: (l: string) => lines.push(l), stderr: (l: string) => errs.push(l) };
    expect(await runCli(['devices', 'add'], env, io, vi.fn() as never)).toBe(1);
    expect(await runCli(['devices', 'rm', 'x'], env, io, vi.fn() as never)).toBe(1);
    const unauthorized = vi.fn(
      async () => new Response(JSON.stringify({ code: 'unauthorized' }), { status: 401 }),
    ) as unknown as typeof fetch;
    expect(await runCli(['devices', 'add', 'n'], env, io, unauthorized)).toBe(1);
    expect(lines).toEqual([]);
    expect(errs).toEqual([
      'usage: hoplet devices [add <name> [--id <deviceId>]]',
      'usage: hoplet devices [add <name> [--id <deviceId>]]',
      'The token was not accepted.',
    ]);
  });

  it('--id belongs to devices add', async () => {
    const errs: string[] = [];
    const io = { stdout: () => {}, stderr: (l: string) => errs.push(l) };
    expect(await runCli(['interrupt', 's', '--id', 'x'], env, io, vi.fn() as never)).toBe(1);
    expect(errs).toEqual(['--id belongs to hoplet devices add']);
  });

  it('devices lists id, name and connection', async () => {
    const lines: string[] = [];
    const calls: string[] = [];
    const fetchImpl = (async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method} ${url}`);
      return new Response(
        JSON.stringify({
          devices: [
            { deviceId: 'a', name: 'one', connected: true, providers: [], system: null },
            { deviceId: 'b', name: 'two', connected: false, providers: [], system: null },
          ],
        }),
        { status: 200 },
      );
    }) as typeof fetch;
    const code = await runCli(
      ['devices'],
      env,
      { stdout: (l) => lines.push(l), stderr: () => {} },
      fetchImpl,
    );
    expect(code).toBe(0);
    expect(calls).toEqual(['GET http://h/api/devices']);
    expect(lines).toEqual(['a\tone\tconnected', 'b\ttwo\toffline']);
  });
});
