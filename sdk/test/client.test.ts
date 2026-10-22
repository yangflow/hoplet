import { PROTOCOL_VERSION } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { Hoplet, HopletError } from '../src/index.ts';
import { type Call, fakeFetch, type Handler, session } from './support.ts';

const URL = 'http://core.test:8787/';

function client(...handlers: Handler[]) {
  const { fetch, calls } = fakeFetch(...handlers);
  return { hoplet: new Hoplet({ url: URL, token: 'secret-token', fetch }), calls };
}

const on =
  (method: string, path: string, reply: ReturnType<Handler>): Handler =>
  (call) =>
    call.method === method && call.path === path ? reply : undefined;

async function failure(promise: Promise<unknown>): Promise<HopletError> {
  try {
    await promise;
  } catch (err) {
    expect(err).toBeInstanceOf(HopletError);
    return err as HopletError;
  }
  throw new Error('expected a rejection');
}

describe('requests', () => {
  it('sends the bearer token to the base URL without its trailing slash', async () => {
    const { hoplet, calls } = client(
      on('GET', '/api/sessions', { json: { sessions: [session('ready', 4)] } }),
    );
    const sessions = await hoplet.listSessions();
    expect(sessions.map((s) => s.sessionId)).toEqual(['s1']);
    const call = calls.find((c) => c.path === '/api/sessions');
    expect(call?.authorization).toBe('Bearer secret-token');
    expect(hoplet.url).toBe('http://core.test:8787');
  });

  it('filters the list by device, provider and state', async () => {
    const other = session('running', 1, {}, { sessionId: 's2', ownerDeviceId: 'dev2' });
    const { hoplet } = client(
      on('GET', '/api/sessions', { json: { sessions: [session('ready', 4), other] } }),
    );
    expect((await hoplet.listSessions({ deviceId: 'dev2' })).map((s) => s.sessionId)).toEqual([
      's2',
    ]);
    expect((await hoplet.listSessions({ state: 'ready' })).map((s) => s.sessionId)).toEqual(['s1']);
    expect(await hoplet.listSessions({ provider: 'codex' })).toEqual([]);
  });

  it('gets one session', async () => {
    const { hoplet, calls } = client(
      on('GET', '/api/sessions/a%2Fb', { json: session('idle', 0) }),
    );
    expect((await hoplet.getSession('a/b')).runtime.state).toBe('idle');
    expect(calls.at(-1)?.path).toBe('/api/sessions/a%2Fb');
  });

  it('lists devices and projects, and answers a permission and an interrupt', async () => {
    const { hoplet, calls } = client(
      on('GET', '/api/devices', { json: { devices: [] } }),
      on('GET', '/api/devices/dev1/projects', {
        json: { deviceId: 'dev1', candidates: [], projects: [] },
      }),
      on('POST', '/api/sessions/s1/permission', { json: { ok: true } }),
      on('POST', '/api/sessions/s1/interrupt', { json: { ok: true } }),
      on('GET', '/api/sessions/s1/queue', { json: { items: [] } }),
    );
    expect(await hoplet.listDevices()).toEqual([]);
    expect((await hoplet.listProjects('dev1')).deviceId).toBe('dev1');
    await hoplet.answerPermission('s1', { requestId: 'r1', decision: 'allow' });
    await hoplet.interrupt('s1');
    expect((await hoplet.getQueue('s1')).items).toEqual([]);
    expect(calls.find((c) => c.path.endsWith('/permission'))?.body).toEqual({
      requestId: 'r1',
      decision: 'allow',
    });
  });

  it('reads one block', async () => {
    const { hoplet } = client(
      on('GET', '/api/sessions/s1/blocks/3/1', { json: { type: 'text', text: 'full' } }),
    );
    expect(await hoplet.readBlock('s1', 3, 1)).toEqual({ type: 'text', text: 'full' });
  });

  it('does the host-only calls', async () => {
    const { hoplet, calls } = client(
      on('POST', '/api/sessions/s1/handoff', {
        json: { ok: true, sessionId: 's1', targetDeviceId: 'dev2' },
      }),
      on('POST', '/api/devices', { json: { deviceId: 'd9', token: 'tok', name: 'box' } }),
      on('GET', '/api/audit/events?limit=5&actor=token', {
        json: { events: [], next: null },
      }),
      on('GET', '/api/audit/export?format=csv', { text: 'a,b\n' }),
      on('GET', '/api/tokens', { json: { tokens: [] } }),
      on('DELETE', '/api/tokens/t1', { json: { ok: true } }),
    );
    expect((await hoplet.mintDevice('box')).deviceId).toBe('d9');
    expect(calls.at(-1)?.body).toEqual({ name: 'box' });
    expect(
      (await hoplet.auditEvents({ limit: 5, actor: 'token', after: undefined })).next,
    ).toBeNull();
    expect(await hoplet.auditExport({ format: 'csv' })).toBe('a,b\n');
    expect(await hoplet.listTokens()).toEqual({ tokens: [] });
    expect(await hoplet.revokeToken('t1')).toEqual({ ok: true });
    await hoplet.handoff('s1', { targetDeviceId: 'dev2' }).then(
      () => undefined,
      () => undefined,
    );
    expect(calls.find((c) => c.path === '/api/sessions/s1/handoff')?.body).toEqual({
      targetDeviceId: 'dev2',
    });
  });
});

describe('prompts', () => {
  const accepted: Handler = (call) =>
    call.method === 'POST' && call.path === '/api/sessions/s1/prompt'
      ? { status: 202, json: { ok: true, promptId: (call.body as { promptId: string }).promptId } }
      : undefined;

  it('generates the promptId and returns a baseline read before the send', async () => {
    const { hoplet, calls } = client(
      on('GET', '/api/sessions/s1', { json: session('ready', 7) }),
      accepted,
    );
    const sent = await hoplet.sendPrompt('s1', 'hello');
    const body = calls.find((c) => c.method === 'POST')?.body as { text: string; promptId: string };
    expect(body.text).toBe('hello');
    expect(sent.promptId).toBe(body.promptId);
    expect(sent.promptId.length).toBeGreaterThan(8);
    expect(sent.baseline).toEqual({ sessionId: 's1', lineCount: 7, state: 'ready' });
  });

  it('takes a caller promptId', async () => {
    const { hoplet } = client(
      on('GET', '/api/sessions/s1', { json: session('ready', 0) }),
      accepted,
    );
    const sent = await hoplet.sendPrompt('s1', { text: 'x', promptId: 'mine' });
    expect(sent.promptId).toBe('mine');
  });

  it('repeats a lost request with the same promptId', async () => {
    let attempts = 0;
    const seen: unknown[] = [];
    const base = fakeFetch(on('GET', '/api/sessions/s1', { json: session('ready', 0) }), accepted);
    const fetchImpl = ((input: string | URL | Request, init?: RequestInit) => {
      if (String(input).endsWith('/prompt') && attempts++ < 2) {
        seen.push(JSON.parse(String(init?.body)).promptId);
        return Promise.reject(new TypeError('socket hang up'));
      }
      return base.fetch(input, init);
    }) as typeof fetch;
    const hoplet = new Hoplet({ url: URL, token: 't', fetch: fetchImpl });
    const sent = await hoplet.sendPrompt('s1', 'hello');
    expect(attempts).toBe(3);
    expect(seen[0]).toBe(seen[1]);
    expect(sent.promptId).toBe(seen[0]);
  });

  it('gives up after three lost requests with network-error', async () => {
    const hoplet = new Hoplet({
      url: URL,
      token: 't',
      fetch: (() => Promise.reject(new TypeError('down'))) as typeof fetch,
    });
    const err = await failure(hoplet.sendPrompt('s1', 'hi'));
    expect(err.code).toBe('network-error');
    expect(err.status).toBe(0);
  });

  it('creates a session and returns a baseline', async () => {
    const { hoplet, calls } = client(
      on('POST', '/api/sessions', {
        status: 202,
        json: { ok: true, sessionId: 'new1', promptId: 'p' },
      }),
    );
    const started = await hoplet.createSession({
      deviceId: 'dev1',
      projectPath: '/work/app',
      provider: 'claude',
      text: 'start',
      promptId: 'p',
    });
    expect(started).toEqual({
      sessionId: 'new1',
      promptId: 'p',
      baseline: { sessionId: 'new1', lineCount: 0, state: 'idle' },
    });
    expect((calls.at(-1) as Call).body).toMatchObject({ deviceId: 'dev1', promptId: 'p' });
  });
});

describe('readSession', () => {
  const lines =
    (total: number): Handler =>
    (call) => {
      const match = /^\/api\/sessions\/s1\/lines\?(.*)$/.exec(call.path);
      if (!match) return undefined;
      const params = new URLSearchParams(match[1]);
      const limit = Number(params.get('limit'));
      let start = Number(params.get('start'));
      if (start < 0) start = Math.max(0, total + start);
      const end = Math.min(total, start + limit);
      return { json: { sessionId: 's1', start, end, total, entries: [] } };
    };

  it('pages forward until the end', async () => {
    const { hoplet, calls } = client(lines(250));
    const ranges: [number, number][] = [];
    for await (const page of hoplet.readSession('s1', { limit: 100 }))
      ranges.push([page.start, page.end]);
    expect(ranges).toEqual([
      [0, 100],
      [100, 200],
      [200, 250],
    ]);
    expect(calls.filter((c) => c.path.includes('/lines')).length).toBe(3);
  });

  it('pages backward from the end, newest first', async () => {
    const { hoplet } = client(lines(250));
    const ranges: [number, number][] = [];
    for await (const page of hoplet.readSession('s1', { limit: 100, direction: 'backward' }))
      ranges.push([page.start, page.end]);
    expect(ranges).toEqual([
      [150, 250],
      [50, 150],
      [0, 50],
    ]);
  });

  it('pages backward from a line', async () => {
    const { hoplet } = client(lines(250));
    const ranges: [number, number][] = [];
    for await (const page of hoplet.readSession('s1', {
      start: 120,
      limit: 100,
      direction: 'backward',
    }))
      ranges.push([page.start, page.end]);
    expect(ranges).toEqual([
      [20, 120],
      [0, 20],
    ]);
  });

  it('yields one empty page for an empty session and caps the page size', async () => {
    const { hoplet, calls } = client(lines(0));
    const pages = [];
    for await (const page of hoplet.readSession('s1', { limit: 9999 })) pages.push(page);
    expect(pages).toHaveLength(1);
    expect(calls.at(-1)?.path).toContain('limit=500');
  });

  it('stops when a page makes no progress', async () => {
    const stuck = on('GET', '/api/sessions/s1/lines?start=0&limit=100', {
      json: { sessionId: 's1', start: 0, end: 0, total: 50, entries: [] },
    });
    const { hoplet } = client(stuck);
    const pages = [];
    for await (const page of hoplet.readSession('s1')) pages.push(page);
    expect(pages).toHaveLength(1);
  });
});

describe('errors', () => {
  it('maps the stable code, status, reason and promptId', async () => {
    const { hoplet } = client(
      on('GET', '/api/sessions/s1', {
        status: 404,
        json: { code: 'session-not-found', reason: 'no such session', promptId: 'p9' },
      }),
    );
    const err = await failure(hoplet.getSession('s1'));
    expect(err.name).toBe('HopletError');
    expect(err.code).toBe('session-not-found');
    expect(err.status).toBe(404);
    expect(err.reason).toBe('no such session');
    expect(err.promptId).toBe('p9');
  });

  it('maps forbidden and unauthorized', async () => {
    const { hoplet } = client(
      on('GET', '/api/tokens', { status: 403, json: { code: 'forbidden' } }),
      on('GET', '/api/devices', { status: 401, json: { code: 'unauthorized' } }),
    );
    expect((await failure(hoplet.listTokens())).code).toBe('forbidden');
    expect((await failure(hoplet.listDevices())).code).toBe('unauthorized');
  });

  it('passes an unknown code through', async () => {
    const { hoplet } = client(
      on('GET', '/api/sessions/s1', { status: 418, json: { code: 'teapot', reason: 'short' } }),
    );
    const err = await failure(hoplet.getSession('s1'));
    expect(err.code).toBe('teapot');
    expect(err.status).toBe(418);
  });

  it('reports a body that is not an error response as internal', async () => {
    const { hoplet } = client(on('GET', '/api/sessions/s1', { status: 502, text: '<html>' }));
    const err = await failure(hoplet.getSession('s1'));
    expect(err.code).toBe('internal');
    expect(err.status).toBe(502);
  });

  it('reports a response of the wrong shape as invalid-response', async () => {
    const { hoplet } = client(on('GET', '/api/sessions/s1', { json: { nope: true } }));
    expect((await failure(hoplet.getSession('s1'))).code).toBe('invalid-response');
  });

  it('reports an unreachable core as network-error', async () => {
    const hoplet = new Hoplet({
      url: URL,
      token: 't',
      fetch: (() => Promise.reject(new TypeError('refused'))) as typeof fetch,
    });
    const err = await failure(hoplet.listSessions());
    expect(err.code).toBe('network-error');
    expect(err.status).toBe(0);
  });

  it('refuses a core on another protocol version, naming both', async () => {
    const mismatch = new Hoplet({
      url: URL,
      token: 't',
      fetch: (async (input: string | URL | Request) =>
        String(input).endsWith('/api/health')
          ? new Response(
              JSON.stringify({
                ok: true,
                sessions: 0,
                uptimeSec: 1,
                coreVersion: '9.9.9',
                protocolVersion: PROTOCOL_VERSION + 1,
              }),
            )
          : new Response('{}')) as typeof fetch,
    });
    const err = await failure(mismatch.listSessions());
    expect(err.code).toBe('protocol-version-mismatch');
    expect(err.message).toContain(String(PROTOCOL_VERSION));
    expect(err.message).toContain(String(PROTOCOL_VERSION + 1));
    expect((await failure(mismatch.listSessions())).code).toBe('protocol-version-mismatch');
  });

  it('checks the version once', async () => {
    const { hoplet, calls } = client(on('GET', '/api/devices', { json: { devices: [] } }));
    await hoplet.listDevices();
    await hoplet.listDevices();
    expect(calls.filter((c) => c.path === '/api/health')).toHaveLength(1);
  });
});
