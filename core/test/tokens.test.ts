import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import {
  type AuditEvent,
  MAX_ACTIVE_TOKENS,
  PROTOCOL_VERSION,
  parse,
  type TokenCreateResponse,
  TokenCreateResponseSchema,
  type TokenGrant,
  TokenListResponseSchema,
  WsTicketResponseSchema,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { TOKEN_ENDED_CLOSE_CODE } from '../src/api/browser.ts';
import { createApi } from '../src/api/index.ts';
import { TOKEN_REQUESTS_PER_MINUTE } from '../src/api/router.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

/** A parsed JSON object from the wire; the tests read fields off it. */
// biome-ignore lint/suspicious/noExplicitAny: parsed wire JSON, read by field name
type Json = Record<string, any>;

const HOST = 'host-token-0123456789abcdef';
const DAY = 86_400_000;

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const userLine = (cwd: string): string =>
  JSON.stringify({ type: 'user', cwd, message: { role: 'user', content: 'hello' } });

/** Three sessions: two on machine A (projects /work/a and /work/b), one on machine B (/other/c). */
const SESSIONS = [
  { id: 's-a1', device: 'dev-a', cwd: '/work/a' },
  { id: 's-a2', device: 'dev-a', cwd: '/work/b' },
  { id: 's-b1', device: 'dev-b', cwd: '/other/c' },
];

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-tokens-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const s of SESSIONS) {
    store.append(
      {
        sessionId: s.id,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/x/${s.id}`,
        deviceId: s.device,
      },
      [userLine(s.cwd)],
    );
  }
  const control = createControl(store, { promptTimeoutMs: 100 });
  const server = createServer();
  const clock = { now: 1_000_000 };
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control, {
    now: () => clock.now,
  }).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  const rest = async (method: string, p: string, token: string | null, body?: unknown) => {
    const res = await fetch(`http://127.0.0.1:${port}${p}`, {
      method,
      headers: {
        ...(token === null ? {} : { authorization: `Bearer ${token}` }),
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const text = await res.text();
    return {
      status: res.status,
      headers: res.headers,
      text,
      body: (text ? JSON.parse(text) : null) as Json | null,
    };
  };
  const issue = async (
    grant: TokenGrant,
    expiresInDays = 30,
    name = 'test',
  ): Promise<TokenCreateResponse> => {
    const res = await rest('POST', '/api/tokens', HOST, { name, grant, expiresInDays });
    expect(res.status).toBe(201);
    return parse(TokenCreateResponseSchema, res.body);
  };
  const audit = (): AuditEvent[] => store.listAudit({}, { order: 'asc', limit: 500 }).events;
  const ofType = (type: string): AuditEvent[] => audit().filter((e) => e.type === type);
  return { dir, store, control, port, clock, rest, issue, audit, ofType };
}

type Booted = Awaited<ReturnType<typeof boot>>;

function connect(url: string): Promise<{ outcome: number | 'open'; ws: WebSocket }> {
  return new Promise((resolve) => {
    const ws = new WebSocket(url);
    sockets.push(ws);
    ws.once('open', () => resolve({ outcome: 'open', ws }));
    ws.once('unexpected-response', (_req, res) => resolve({ outcome: res.statusCode ?? 0, ws }));
    ws.once('error', () => {});
  });
}

/** Opens the browser WebSocket with a ticket minted by `secret`, recording every message. */
async function browser(b: Booted, secret: string) {
  const res = await b.rest('POST', '/api/ws-tickets', secret);
  expect(res.status).toBe(200);
  const { ticket } = parse(WsTicketResponseSchema, res.body);
  const { outcome, ws } = await connect(`ws://127.0.0.1:${b.port}/ws?ticket=${ticket}`);
  expect(outcome).toBe('open');
  const heard: Json[] = [];
  ws.on('message', (d) => heard.push(JSON.parse(String(d))));
  const closed = new Promise<number>((r) => ws.once('close', (code) => r(code)));
  return { ws, heard, closed };
}

const settle = (ms = 150) => new Promise((r) => setTimeout(r, ms));

const READ: TokenGrant = { capabilities: ['read'] };

describe('issuing, listing and revoking', () => {
  it('shows the secret once and keeps only its hash', async () => {
    const b = await boot();
    const issued = await b.issue(READ);
    expect(issued.token).toMatch(/^hpt_[A-Za-z0-9_-]{20,}$/);
    expect(issued.state).toBe('active');
    expect(issued.expiresAt).toBe(b.clock.now + 30 * DAY);

    const list = await b.rest('GET', '/api/tokens', HOST);
    expect(list.status).toBe(200);
    expect(list.text).not.toContain(issued.token);
    const { tokens } = parse(TokenListResponseSchema, list.body);
    expect(tokens.map((t) => t.id)).toEqual([issued.id]);
    expect('token' in (tokens[0] as object)).toBe(false);

    // The database holds the SHA-256, never the secret.
    const db = new DatabaseSync(path.join(b.dir, 'hoplet.db'));
    const rows = db.prepare('SELECT * FROM api_tokens').all();
    db.close();
    expect(JSON.stringify(rows)).not.toContain(issued.token);
    expect(JSON.stringify(rows)).toMatch(/[0-9a-f]{64}/);

    // Nor does the audit log, any file of the data directory, or the second read of the token.
    expect(JSON.stringify(b.audit())).not.toContain(issued.token);
    for (const file of readdirSync(b.dir)) {
      if (!file.endsWith('.log')) continue;
      expect(readFileSync(path.join(b.dir, file), 'utf8')).not.toContain(issued.token);
    }
  });

  it('requires a name, a grant and a life of 1 to 365 days', async () => {
    const b = await boot();
    const bad: unknown[] = [
      { name: 'x', grant: READ },
      { name: 'x', grant: READ, expiresInDays: 0 },
      { name: 'x', grant: READ, expiresInDays: 366 },
      { name: 'x', grant: READ, expiresInDays: 1.5 },
      { name: 'x', grant: { capabilities: [] }, expiresInDays: 5 },
      { name: 'x', grant: { capabilities: ['read', 'read'] }, expiresInDays: 5 },
      { name: 'x', grant: { capabilities: ['admin'] }, expiresInDays: 5 },
      { name: 'x', grant: { capabilities: ['read'], allow: [] }, expiresInDays: 5 },
      { name: 'x', grant: { capabilities: ['read'], extra: 1 }, expiresInDays: 5 },
    ];
    for (const body of bad) {
      const res = await b.rest('POST', '/api/tokens', HOST, body);
      expect(res.status, JSON.stringify(body)).toBe(400);
    }
    expect((await b.issue(READ, 365)).expiresAt).toBe(b.clock.now + 365 * DAY);
    expect((await b.issue(READ, 1)).expiresAt).toBe(b.clock.now + DAY);
  });

  it('caps the active tokens and counts only the active ones', async () => {
    const b = await boot();
    const first = await b.issue(READ);
    for (let i = 1; i < MAX_ACTIVE_TOKENS; i++) await b.issue(READ);
    const over = await b.rest('POST', '/api/tokens', HOST, {
      name: 'one too many',
      grant: READ,
      expiresInDays: 5,
    });
    expect(over.status).toBe(413);
    expect(over.body?.code).toBe('too-large');
    expect((await b.rest('DELETE', `/api/tokens/${first.id}`, HOST)).status).toBe(200);
    await b.issue(READ);
  });

  it('revokes at once, keeps the record, and removes it on the second revoke', async () => {
    const b = await boot();
    const t = await b.issue(READ);
    expect((await b.rest('GET', '/api/health', t.token)).status).toBe(200);
    expect((await b.rest('DELETE', `/api/tokens/${t.id}`, HOST)).status).toBe(200);
    const res = await b.rest('GET', '/api/health', t.token);
    expect(res.status).toBe(401);
    expect(res.body).toEqual({ code: 'unauthorized' });

    const list = parse(TokenListResponseSchema, (await b.rest('GET', '/api/tokens', HOST)).body);
    expect(list.tokens[0]?.state).toBe('revoked');

    expect((await b.rest('DELETE', `/api/tokens/${t.id}`, HOST)).status).toBe(200);
    expect(
      parse(TokenListResponseSchema, (await b.rest('GET', '/api/tokens', HOST)).body).tokens,
    ).toEqual([]);
    expect((await b.rest('DELETE', `/api/tokens/${t.id}`, HOST)).status).toBe(404);
  });

  it('refuses a token on the token routes, whatever it can do', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read', 'send', 'create', 'answer'] });
    for (const [method, p, body] of [
      ['GET', '/api/tokens', undefined],
      ['POST', '/api/tokens', { name: 'x', grant: READ, expiresInDays: 1 }],
      ['DELETE', `/api/tokens/${t.id}`, undefined],
    ] as const) {
      const res = await b.rest(method, p, t.token, body);
      expect(res.status, `${method} ${p}`).toBe(403);
      expect(res.body).toEqual({ code: 'forbidden' });
    }
  });

  it('answers 401 to an unknown token and to a token of the wrong shape', async () => {
    const b = await boot();
    for (const secret of ['hpt_unknown', 'hpt_', 'nope']) {
      expect((await b.rest('GET', '/api/health', secret)).status).toBe(401);
    }
  });
});

describe('expiry', () => {
  it('stops working at its expiry and lists as expired', async () => {
    const b = await boot();
    const t = await b.issue(READ, 1);
    b.clock.now += DAY - 1;
    expect((await b.rest('GET', '/api/health', t.token)).status).toBe(200);
    b.clock.now += 1;
    expect((await b.rest('GET', '/api/health', t.token)).status).toBe(401);
    const list = parse(TokenListResponseSchema, (await b.rest('GET', '/api/tokens', HOST)).body);
    expect(list.tokens[0]?.state).toBe('expired');
  });

  it('is not audited when an expired, revoked or unknown token is presented', async () => {
    const b = await boot();
    const t = await b.issue(READ, 1);
    b.clock.now += DAY;
    await b.rest('GET', '/api/sessions', t.token);
    await b.rest('GET', '/api/sessions', 'hpt_unknown');
    expect(b.ofType('token.denied')).toEqual([]);
  });
});

describe('abilities', () => {
  const call = (b: Booted, secret: string, method: string, p: string, body?: unknown) =>
    b.rest(method, p, secret, body);

  it('read sees but cannot send, create or answer', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read'] });
    expect((await call(b, t.token, 'GET', '/api/sessions')).status).toBe(200);
    expect((await call(b, t.token, 'GET', '/api/sessions/s-a1')).status).toBe(200);
    expect((await call(b, t.token, 'GET', '/api/devices')).status).toBe(200);
    for (const [method, p, body] of [
      ['POST', '/api/sessions/s-a1/prompt', { text: 'hi', promptId: 'p1' }],
      ['POST', '/api/sessions/s-a1/interrupt', {}],
      [
        'POST',
        '/api/sessions',
        {
          deviceId: 'dev-a',
          projectPath: '/work/a',
          provider: 'claude',
          text: 'hi',
          promptId: 'n1',
        },
      ],
      ['POST', '/api/sessions/s-a1/permission', { requestId: 'r', decision: 'allow' }],
      ['PATCH', '/api/sessions/s-a1', { title: 'x' }],
      ['DELETE', '/api/sessions/s-a1', undefined],
    ] as const) {
      const res = await call(b, t.token, method, p, body);
      expect(res.status, `${method} ${p}`).toBe(403);
      expect(res.body).toEqual({ code: 'forbidden' });
    }
  });

  it('send reaches the prompt route and the owner check, not the create or answer routes', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read', 'send'] });
    const prompt = await call(b, t.token, 'POST', '/api/sessions/s-a1/prompt', {
      text: 'hi',
      promptId: 'p1',
    });
    // Past the scope check: the owner is offline.
    expect(prompt.status).toBe(409);
    expect(prompt.body?.code).toBe('owner-offline');
    const create = await call(b, t.token, 'POST', '/api/sessions', {
      deviceId: 'dev-a',
      projectPath: '/work/a',
      provider: 'claude',
      text: 'hi',
      promptId: 'n1',
    });
    expect(create.status).toBe(403);
    const answer = await call(b, t.token, 'POST', '/api/sessions/s-a1/permission', {
      requestId: 'r',
      decision: 'allow',
    });
    expect(answer.status).toBe(403);
  });

  it('answer alone does not read or send; with read it reaches the permission route', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['answer'] });
    expect((await call(b, t.token, 'GET', '/api/sessions')).status).toBe(403);
    expect(
      (await call(b, t.token, 'POST', '/api/sessions/s-a1/prompt', { text: 'x', promptId: 'p' }))
        .status,
    ).toBe(403);
    const answer = await call(b, t.token, 'POST', '/api/sessions/s-a1/permission', {
      requestId: 'r',
      decision: 'allow',
    });
    expect(answer.status).not.toBe(403);
    expect(answer.status).not.toBe(401);
  });

  it('keeps configuration, deletion, handoff and terminals with the host', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read', 'send', 'create', 'answer'] });
    for (const [method, p, body] of [
      ['POST', '/api/sessions/s-a1/config', { key: 'permission-mode', value: 'bypassPermissions' }],
      ['GET', '/api/sessions/s-a1/commands', undefined],
      ['DELETE', '/api/sessions/s-a1', undefined],
      ['GET', '/api/audit/events', undefined],
      ['POST', '/api/devices', { name: 'x' }],
    ] as const) {
      const res = await call(b, t.token, method, p, body);
      expect(res.status, `${method} ${p}`).toBe(403);
    }
    // Everything is still there.
    expect((await call(b, HOST, 'GET', '/api/sessions/s-a1')).status).toBe(200);
  });
});

describe('the allow list', () => {
  it('shows a machine-limited token only that machine', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read', 'send'], allow: [{ deviceId: 'dev-a' }] });
    const list = await b.rest('GET', '/api/sessions', t.token);
    expect(list.body?.sessions.map((s: { sessionId: string }) => s.sessionId).sort()).toEqual([
      's-a1',
      's-a2',
    ]);
    expect((await b.rest('GET', '/api/sessions/s-a2', t.token)).status).toBe(200);
    // A session out of scope does not exist for the token.
    for (const [method, p, body] of [
      ['GET', '/api/sessions/s-b1', undefined],
      ['GET', '/api/sessions/s-b1/lines', undefined],
      ['POST', '/api/sessions/s-b1/prompt', { text: 'x', promptId: 'p' }],
      ['POST', '/api/sessions/s-b1/interrupt', {}],
    ] as const) {
      const res = await b.rest(method, p, t.token, body);
      expect(res.status, `${method} ${p}`).toBe(404);
      expect(res.body).toEqual({ code: 'session-not-found' });
    }
    // The same answer as for a session that does not exist.
    const missing = await b.rest('GET', '/api/sessions/nope', t.token);
    expect(missing.status).toBe(404);
    expect(missing.body).toEqual({ code: 'session-not-found' });

    // A machine out of scope is refused.
    const devices = await b.rest('GET', '/api/devices/dev-b/projects', t.token);
    expect(devices.status).toBe(403);
    expect(devices.body).toEqual({ code: 'forbidden' });
  });

  it('limits a project-limited token to that project and below', async () => {
    const b = await boot();
    const t = await b.issue({
      capabilities: ['read'],
      allow: [{ deviceId: 'dev-a', path: '/work/a' }],
    });
    const list = await b.rest('GET', '/api/sessions', t.token);
    expect(list.body?.sessions.map((s: { sessionId: string }) => s.sessionId)).toEqual(['s-a1']);
    expect((await b.rest('GET', '/api/sessions/s-a2', t.token)).status).toBe(404);
    expect((await b.rest('GET', '/api/sessions/s-a1', t.token)).status).toBe(200);
  });

  it('filters the machine list and the global search', async () => {
    const b = await boot();
    b.store.mintDevice('A', 'dev-a');
    b.store.mintDevice('B', 'dev-b');
    const t = await b.issue({ capabilities: ['read'], allow: [{ deviceId: 'dev-b' }] });
    const devices = await b.rest('GET', '/api/devices', t.token);
    expect(devices.body?.devices.map((d: { deviceId: string }) => d.deviceId)).toEqual(['dev-b']);
    const all = await b.rest('GET', '/api/devices', HOST);
    expect(all.body?.devices.length).toBe(2);

    const search = await b.rest('GET', '/api/search?q=hello', t.token);
    expect(search.status).toBe(200);
    const ids = (search.body?.sessions ?? search.body?.results ?? []).map(
      (r: { sessionId?: string; id?: string }) => r.sessionId ?? r.id,
    );
    expect(ids).toEqual(['s-b1']);
  });

  it('refuses to create a session outside the allow list', async () => {
    const b = await boot();
    const t = await b.issue({
      capabilities: ['read', 'send', 'create'],
      allow: [{ deviceId: 'dev-a', path: '/work/a' }],
    });
    for (const projectPath of ['/work/b', '/work/a/../b', '/other/c']) {
      const res = await b.rest('POST', '/api/sessions', t.token, {
        deviceId: 'dev-a',
        projectPath,
        provider: 'claude',
        text: 'hi',
        promptId: 'n1',
      });
      expect(res.status, projectPath).toBe(403);
      expect(res.body).toEqual({ code: 'forbidden' });
    }
    const other = await b.rest('POST', '/api/sessions', t.token, {
      deviceId: 'dev-b',
      projectPath: '/other/c',
      provider: 'claude',
      text: 'hi',
      promptId: 'n1',
    });
    expect(other.status).toBe(403);
  });

  it('applies the create capability and the allow list to a session in a new worktree', async () => {
    const b = await boot();
    const body = (projectPath: string) => ({
      deviceId: 'dev-a',
      projectPath,
      provider: 'claude',
      text: 'hi',
      promptId: 'n1',
      worktree: { branch: 'feature/x' },
    });
    const noCreate = await b.issue({ capabilities: ['read', 'send'] });
    const denied = await b.rest('POST', '/api/sessions', noCreate.token, body('/work/a'));
    expect(denied.status).toBe(403);
    const scoped = await b.issue({
      capabilities: ['read', 'send', 'create'],
      allow: [{ deviceId: 'dev-a', path: '/work/a' }],
    });
    const outside = await b.rest('POST', '/api/sessions', scoped.token, body('/work/b'));
    expect(outside.status).toBe(403);
    expect(outside.body).toEqual({ code: 'forbidden' });
  });

  it('hides a template that starts outside the allow list', async () => {
    const b = await boot();
    const t = await b.issue({
      capabilities: ['read', 'create'],
      allow: [{ deviceId: 'dev-a' }],
    });
    const res = await b.rest('GET', '/api/templates', t.token);
    expect(res.status).toBe(200);
    expect(res.body?.templates ?? []).toEqual([]);
  });
});

describe('bypass permissions stay out of reach', () => {
  const MODE = {
    type: 'select',
    id: 'mode',
    name: 'Permission mode',
    category: 'mode',
    currentValue: 'default',
    options: [
      { value: 'default', name: 'Manual' },
      { value: 'bypassPermissions', name: 'Bypass permissions', risk: 'high' },
    ],
  };
  const PROVIDERS = [
    {
      provider: 'claude',
      configuration: {
        id: 'claude',
        name: 'Claude Code',
        extends: 'claude',
        enabled: true,
        env: [],
        defaults: {},
      },
      capabilities: { permissions: true, interrupt: true, config: [MODE] },
    },
  ];

  async function withAgent(b: Booted) {
    const device = b.store.mintDevice('agent', 'dev-a');
    const got: Json[] = [];
    const ws = await new Promise<WebSocket>((resolve, reject) => {
      const agent = new WebSocket(`ws://127.0.0.1:${b.port}/ws/agent`, {
        headers: { authorization: `Bearer ${device.token}` },
      });
      sockets.push(agent);
      agent.once('error', reject);
      agent.on('message', (d) => {
        const m = JSON.parse(String(d));
        got.push(m);
        if (m.type === 'welcome') resolve(agent);
        if (m.type === 'device.projects.list') {
          agent.send(
            JSON.stringify({
              v: PROTOCOL_VERSION,
              type: 'device.projects',
              requestId: m.requestId,
              candidates: [],
              projects: [{ path: '/work/a', sessions: [] }],
            }),
          );
        }
      });
      agent.on('open', () =>
        agent.send(
          JSON.stringify({
            v: PROTOCOL_VERSION,
            type: 'device.hello',
            deviceId: 'dev-a',
            name: 'dev-a',
            version: 'test',
            providers: PROVIDERS,
          }),
        ),
      );
    });
    return { ws, got };
  }

  const start = (config: Record<string, unknown>) => ({
    deviceId: 'dev-a',
    projectPath: '/work/a',
    provider: 'claude',
    config,
    text: 'hi',
    promptId: 'n1',
  });

  it('refuses a high-risk option when a token starts a session, and sends nothing to the agent', async () => {
    const b = await boot();
    const agent = await withAgent(b);
    const t = await b.issue({ capabilities: ['read', 'send', 'create'] });
    const res = await b.rest(
      'POST',
      '/api/sessions',
      t.token,
      start({ mode: 'bypassPermissions' }),
    );
    expect(res.status).toBe(403);
    expect(res.body).toEqual({ code: 'forbidden' });
    await settle();
    expect(agent.got.some((m) => m.type === 'runtime.start')).toBe(false);
    expect(b.ofType('token.denied').length).toBe(1);
  });

  it('lets the same token start a session with a low-risk option, and the host the high-risk one', async () => {
    const b = await boot();
    const agent = await withAgent(b);
    agent.ws.on('message', (d) => {
      const m = JSON.parse(String(d));
      if (m.type === 'runtime.start') {
        agent.ws.send(
          JSON.stringify({
            v: PROTOCOL_VERSION,
            type: 'runtime.started',
            requestId: m.requestId,
            sessionId: `new-${m.requestId}`,
          }),
        );
      }
    });
    const t = await b.issue({ capabilities: ['read', 'send', 'create'] });
    const low = await b.rest('POST', '/api/sessions', t.token, start({ mode: 'default' }));
    expect(low.status).toBe(200);
    const high = await b.rest('POST', '/api/sessions', HOST, {
      ...start({ mode: 'bypassPermissions' }),
      promptId: 'n2',
    });
    expect(high.status).toBe(200);
    expect(agent.got.filter((m) => m.type === 'runtime.start').length).toBe(2);
  });

  it('keeps the configuration route with the host', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read', 'send', 'create', 'answer'] });
    const config = await b.rest('POST', '/api/sessions/s-a1/config', t.token, {
      configId: 'mode',
      value: 'bypassPermissions',
    });
    expect(config.status).toBe(403);
  });
});

describe('the rate limit', () => {
  it('answers 429 with retry-after after 300 requests a minute, per token', async () => {
    const b = await boot();
    const t = await b.issue(READ);
    const other = await b.issue(READ);
    for (let i = 0; i < TOKEN_REQUESTS_PER_MINUTE; i++) {
      expect((await b.rest('GET', '/api/health', t.token)).status).toBe(200);
    }
    const limited = await b.rest('GET', '/api/health', t.token);
    expect(limited.status).toBe(429);
    expect(limited.body).toEqual({ code: 'rate-limited' });
    expect(limited.headers.get('retry-after')).toBe('60');
    // Another token, and the host, are not affected.
    expect((await b.rest('GET', '/api/health', other.token)).status).toBe(200);
    expect((await b.rest('GET', '/api/health', HOST)).status).toBe(200);
    // The window passes.
    b.clock.now += 60_000;
    expect((await b.rest('GET', '/api/health', t.token)).status).toBe(200);
  }, 30_000);
});

describe('the audit log', () => {
  it('records token.issued and token.revoked as the operator, never the secret', async () => {
    const b = await boot();
    const t = await b.issue(
      { capabilities: ['read', 'send'], allow: [{ deviceId: 'dev-a' }] },
      7,
      'ci',
    );
    await b.rest('DELETE', `/api/tokens/${t.id}`, HOST);
    const [issued] = b.ofType('token.issued');
    expect(issued?.actorKind).toBe('operator');
    expect(issued?.summary).toMatchObject({
      id: t.id,
      name: 'ci',
      grant: { capabilities: ['read', 'send'], allow: [{ deviceId: 'dev-a' }] },
      expiresAt: t.expiresAt,
    });
    const [revoked] = b.ofType('token.revoked');
    expect(revoked?.actorKind).toBe('operator');
    expect(revoked?.summary).toMatchObject({ id: t.id });
    expect(JSON.stringify(b.audit())).not.toContain(t.token);
  });

  it('records a refusal as token.denied, once a minute per token and route', async () => {
    const b = await boot();
    const t = await b.issue(READ);
    for (let i = 0; i < 5; i++)
      await b.rest('POST', '/api/sessions/s-a1/prompt', t.token, { text: 'x', promptId: 'p' });
    await b.rest('GET', '/api/tokens', t.token);
    let denied = b.ofType('token.denied');
    expect(denied.length).toBe(2);
    expect(denied[0]).toMatchObject({ actorKind: 'token', actorId: t.id });
    expect(denied[0]?.summary).toMatchObject({
      id: t.id,
      method: 'POST',
      path: '/api/sessions/:sessionId/prompt',
      reason: 'forbidden',
    });
    b.clock.now += 60_000;
    await b.rest('POST', '/api/sessions/s-a1/prompt', t.token, { text: 'x', promptId: 'p' });
    denied = b.ofType('token.denied');
    expect(denied.length).toBe(3);
  });

  it('does not record a session out of scope, which answers as nonexistent', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read'], allow: [{ deviceId: 'dev-a' }] });
    await b.rest('GET', '/api/sessions/s-b1', t.token);
    expect(b.ofType('token.denied')).toEqual([]);
  });

  it('records a machine out of scope', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read'], allow: [{ deviceId: 'dev-a' }] });
    await b.rest('GET', '/api/devices/dev-b/projects', t.token);
    expect(b.ofType('token.denied').length).toBe(1);
  });

  it('attributes a prompt to the token that sent it, and to the operator otherwise', async () => {
    const b = await boot();
    const device = b.store.mintDevice('agent', 'dev-a');
    const { ws } = await new Promise<{ ws: WebSocket }>((resolve, reject) => {
      const agent = new WebSocket(`ws://127.0.0.1:${b.port}/ws/agent`, {
        headers: { authorization: `Bearer ${device.token}` },
      });
      sockets.push(agent);
      agent.once('error', reject);
      agent.once('message', () => resolve({ ws: agent }));
    });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: 'dev-a',
        name: 'dev-a',
        version: 'test',
      }),
    );
    await settle();
    const t = await b.issue({ capabilities: ['read', 'send'] });
    await b.rest('POST', '/api/sessions/s-a1/prompt', t.token, {
      text: 'by token',
      promptId: 'p1',
    });
    await b.rest('POST', '/api/sessions/s-a1/prompt', HOST, { text: 'by host', promptId: 'p2' });
    const sent = b.ofType('prompt.sent');
    expect(sent.map((e) => e.actorKind)).toEqual(['token', 'operator']);
    expect(sent[0]).toMatchObject({ actorKind: 'token', actorId: t.id });
    expect(JSON.stringify(sent)).not.toContain('by token');
  });
});

describe('the browser WebSocket', () => {
  it('delivers a token only the events of what it reaches', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read'], allow: [{ deviceId: 'dev-a' }] });
    const tokenSocket = await browser(b, t.token);
    const hostSocket = await browser(b, HOST);
    await settle();
    await b.rest('PATCH', '/api/sessions/s-a1', HOST, { title: 'In scope' });
    await b.rest('PATCH', '/api/sessions/s-b1', HOST, { title: 'Out of scope' });
    await settle();
    const titles = (heard: Json[]) =>
      heard.filter((m) => m.type === 'session.updated').map((m) => m.sessionId);
    expect(titles(hostSocket.heard).sort()).toEqual(['s-a1', 's-b1']);
    expect(titles(tokenSocket.heard)).toEqual(['s-a1']);
    expect(JSON.stringify(tokenSocket.heard)).not.toContain('s-b1');
  });

  it('answers a subscription to a session out of scope as a nonexistent session', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read'], allow: [{ deviceId: 'dev-a' }] });
    const s = await browser(b, t.token);
    s.ws.send(
      JSON.stringify({ v: PROTOCOL_VERSION, type: 'subscribe', sessionId: 's-b1', fromLine: 0 }),
    );
    s.ws.send(
      JSON.stringify({ v: PROTOCOL_VERSION, type: 'subscribe', sessionId: 'nope', fromLine: 0 }),
    );
    await settle();
    const errors = s.heard.filter((m) => m.type === 'error');
    expect(errors.length).toBe(2);
    expect(errors[0]).toEqual({ ...errors[1] });
    expect(errors[0]?.code).toBe('session-not-found');
    expect(s.heard.some((m) => m.type === 'session.appended')).toBe(false);
  });

  it('refuses terminal messages from a token', async () => {
    const b = await boot();
    const t = await b.issue({ capabilities: ['read', 'send', 'create', 'answer'] });
    const s = await browser(b, t.token);
    s.ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'terminal.attach',
        terminalId: 'term-1',
      }),
    );
    await settle();
    const errors = s.heard.filter((m) => m.type === 'error');
    expect(errors.length).toBe(1);
    expect(errors[0]?.code).toBe('forbidden');
  });

  it('closes the connection when the token is revoked', async () => {
    const b = await boot();
    const t = await b.issue(READ);
    const s = await browser(b, t.token);
    await b.rest('DELETE', `/api/tokens/${t.id}`, HOST);
    expect(await s.closed).toBe(TOKEN_ENDED_CLOSE_CODE);
  });

  it('keeps the connection of the host when a token is revoked', async () => {
    const b = await boot();
    const t = await b.issue(READ);
    const s = await browser(b, HOST);
    await b.rest('DELETE', `/api/tokens/${t.id}`, HOST);
    await settle();
    expect(s.ws.readyState).toBe(WebSocket.OPEN);
  });

  it('voids the outstanding tickets of a revoked token', async () => {
    const b = await boot();
    const t = await b.issue(READ);
    const res = await b.rest('POST', '/api/ws-tickets', t.token);
    const { ticket } = parse(WsTicketResponseSchema, res.body);
    await b.rest('DELETE', `/api/tokens/${t.id}`, HOST);
    expect((await connect(`ws://127.0.0.1:${b.port}/ws?ticket=${ticket}`)).outcome).toBe(401);
  });

  it('closes the connection at the expiry of the token', async () => {
    const b = await boot();
    // The clock the core reads is the injected one; the expiry timer waits the difference in real time.
    const t = await b.issue(READ, 1);
    b.clock.now += DAY - 200;
    const s = await browser(b, t.token);
    expect(await s.closed).toBe(TOKEN_ENDED_CLOSE_CODE);
  });

  it('refuses a ticket minted for a token that has since expired', async () => {
    const b = await boot();
    const t = await b.issue(READ, 1);
    b.clock.now += DAY;
    expect((await b.rest('POST', '/api/ws-tickets', t.token)).status).toBe(401);
  });
});
