import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApi } from '../src/api/index.ts';
import {
  type ControlReply,
  createControl,
  type DeviceLink,
  type TerminalSink,
} from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

type Link = DeviceLink & { sent: ServerMessage[]; ofType(type: string): ServerMessage[] };

function fakeLink(): Link {
  const link = {
    sent: [] as ServerMessage[],
    send(msg: ServerMessage) {
      link.sent.push(msg);
    },
    async sendAndWait(msg: ServerMessage) {
      link.sent.push(msg);
    },
    isOpen: () => true,
    ofType: (type: string) => link.sent.filter((m) => m.type === type),
  };
  return link;
}

function fakeSink(): TerminalSink & { got: ServerMessage[]; buffer: number } {
  const sink = {
    got: [] as ServerMessage[],
    buffer: 0,
    send(msg: ServerMessage) {
      sink.got.push(msg);
    },
    buffered: () => sink.buffer,
  };
  return sink;
}

const state = (sessionId: string, s: string) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.state',
    sessionId,
    state: s,
    capabilities: { permissions: true, interrupt: true, config: [] },
  }) as unknown as ControlReply;

const b64 = (s: string) => Buffer.from(s).toString('base64');

function setup(sessions = ['s1']) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-terminal-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const id of sessions) {
    store.append(
      {
        sessionId: id,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/x/${id}`,
        deviceId: 'dev-a',
      },
      ['{"type":"user"}'],
    );
  }
  const control = createControl(store, { promptTimeoutMs: 100, terminalDetachMs: 60_000 });
  const link = fakeLink();
  control.attach('dev-a', link, [], { capabilities: { terminal: true }, previewHost: 'mac.local' });
  for (const id of sessions) control.receive(link, state(id, 'idle'));
  return { store, control, link };
}

type Ctx = ReturnType<typeof setup>;

/** Opens a terminal as the REST route would, with the agent answering. */
async function openTerminal(ctx: Ctx, sessionId = 's1'): Promise<string> {
  const before = ctx.link.ofType('terminal.open').length;
  const pending = ctx.control.terminals.open(sessionId, 'dev-a', 80, 24);
  const msg = ctx.link.ofType('terminal.open')[before] as unknown as { terminalId: string };
  ctx.control.receive(ctx.link, {
    v: PROTOCOL_VERSION,
    type: 'terminal.opened',
    terminalId: msg.terminalId,
  } as unknown as ControlReply);
  const outcome = await pending;
  expect(outcome.ok).toBe(true);
  return msg.terminalId;
}

const output = (terminalId: string, seq: number, text: string) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'terminal.output',
    terminalId,
    seq,
    data: b64(text),
  }) as unknown as ControlReply;

describe('opening a terminal', () => {
  it('refuses when the agent did not declare the capability', async () => {
    const ctx = setup();
    const link = fakeLink();
    ctx.control.attach('dev-a', link, [], { capabilities: {} });
    ctx.control.receive(link, state('s1', 'idle'));
    expect(await ctx.control.terminals.open('s1', 'dev-a', 80, 24)).toMatchObject({
      ok: false,
      error: 'terminal-unavailable',
    });
    expect(link.ofType('terminal.open')).toHaveLength(0);
  });

  it('refuses without an owner, while a turn runs or waits, and for an external session', async () => {
    const ctx = setup();
    const t = ctx.control.terminals;
    expect(await t.open('s1', 'nobody', 80, 24)).toMatchObject({ error: 'owner-offline' });
    for (const s of ['running', 'waiting-permission']) {
      ctx.control.receive(ctx.link, state('s1', s));
      expect(await t.open('s1', 'dev-a', 80, 24)).toMatchObject({ error: 'session-running' });
    }
    ctx.control.receive(ctx.link, state('s1', 'external'));
    expect(await t.open('s1', 'dev-a', 80, 24)).toMatchObject({ error: 'external' });
    expect(ctx.link.ofType('terminal.open')).toHaveLength(0);
  });

  it('maps the agent rejection reasons to error codes', async () => {
    const ctx = setup();
    const cases: [string, string][] = [
      ['disabled', 'terminal-unavailable'],
      ['terminal-busy', 'terminal-busy'],
      ['turn-running', 'session-running'],
    ];
    for (const [reason, error] of cases) {
      const before = ctx.link.ofType('terminal.open').length;
      const pending = ctx.control.terminals.open('s1', 'dev-a', 80, 24);
      const msg = ctx.link.ofType('terminal.open')[before] as unknown as { terminalId: string };
      ctx.control.receive(ctx.link, {
        v: PROTOCOL_VERSION,
        type: 'terminal.rejected',
        terminalId: msg.terminalId,
        reason,
      } as unknown as ControlReply);
      expect(await pending).toMatchObject({ ok: false, error });
    }
  });

  it('allows one terminal per session and three per machine', async () => {
    const ctx = setup(['s1', 's2', 's3', 's4']);
    await openTerminal(ctx, 's1');
    expect(await ctx.control.terminals.open('s1', 'dev-a', 80, 24)).toMatchObject({
      error: 'terminal-busy',
    });
    await openTerminal(ctx, 's2');
    await openTerminal(ctx, 's3');
    expect(await ctx.control.terminals.open('s4', 'dev-a', 80, 24)).toMatchObject({
      error: 'terminal-busy',
    });
  });

  it('tells the agent to close a terminal it never answered for', async () => {
    const ctx = setup();
    const outcome = await ctx.control.terminals.open('s1', 'dev-a', 80, 24);
    expect(outcome.ok).toBe(false);
    expect(ctx.link.ofType('terminal.close')).toHaveLength(1);
    expect(ctx.control.terminals.ofSession('s1')).toBeNull();
  });
});

describe('relaying a terminal', () => {
  it('forwards output, input and resize, and acknowledges what the browser took', async () => {
    const ctx = setup();
    const id = await openTerminal(ctx);
    const sink = fakeSink();
    expect(ctx.control.terminals.attach(id, sink)).toBe(true);
    expect(sink.got[0]).toMatchObject({ type: 'terminal.attached', terminalId: id, cols: 80 });
    ctx.control.receive(ctx.link, output(id, 0, 'hello'));
    expect(sink.got[1]).toMatchObject({ type: 'terminal.data', seq: 0, data: b64('hello') });
    expect(ctx.link.ofType('terminal.ack')).toMatchObject([{ terminalId: id, bytes: 5 }]);
    ctx.control.terminals.input(id, sink, b64('ls\n'));
    ctx.control.terminals.resize(id, sink, 100, 30);
    expect(ctx.link.ofType('terminal.input')).toMatchObject([{ data: b64('ls\n') }]);
    expect(ctx.link.ofType('terminal.resize')).toMatchObject([{ cols: 100, rows: 30 }]);
  });

  it('withholds the acknowledgement while the browser connection is backed up', async () => {
    vi.useFakeTimers();
    const ctx = setup();
    const id = await openTerminal(ctx);
    const sink = fakeSink();
    ctx.control.terminals.attach(id, sink);
    sink.buffer = 1_000_000;
    ctx.control.receive(ctx.link, output(id, 0, 'x'.repeat(100)));
    expect(ctx.link.ofType('terminal.ack')).toHaveLength(0);
    sink.buffer = 0;
    await vi.advanceTimersByTimeAsync(100);
    expect(ctx.link.ofType('terminal.ack')).toMatchObject([{ bytes: 100 }]);
  });

  it('replays recent output to a browser that attaches again, at most 64 KiB', async () => {
    const ctx = setup();
    const id = await openTerminal(ctx);
    const first = fakeSink();
    ctx.control.terminals.attach(id, first);
    ctx.control.terminals.detach(id, first);
    const chunk = 'a'.repeat(4096);
    for (let seq = 0; seq < 20; seq++) ctx.control.receive(ctx.link, output(id, seq, chunk));
    const second = fakeSink();
    ctx.control.terminals.attach(id, second);
    const data = second.got.filter((m) => m.type === 'terminal.data') as unknown as {
      seq: number;
    }[];
    expect(data).toHaveLength(16);
    expect(data[0]?.seq).toBe(4);
    expect(data.at(-1)?.seq).toBe(19);
  });

  it('answers an unknown terminal as not open', () => {
    const { control } = setup();
    expect(control.terminals.attach('nope', fakeSink())).toBe(false);
  });
});

describe('closing a terminal', () => {
  it('closes it detached 60 seconds after the last browser left', async () => {
    vi.useFakeTimers();
    const ctx = setup();
    const id = await openTerminal(ctx);
    const a = fakeSink();
    const b = fakeSink();
    ctx.control.terminals.attach(id, a);
    ctx.control.terminals.attach(id, b);
    ctx.control.terminals.detach(id, a);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(ctx.link.ofType('terminal.close')).toHaveLength(0);
    ctx.control.terminals.detach(id, b);
    await vi.advanceTimersByTimeAsync(59_000);
    expect(ctx.link.ofType('terminal.close')).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(ctx.link.ofType('terminal.close')).toMatchObject([
      { terminalId: id, reason: 'detached' },
    ]);
    expect(ctx.control.terminals.ofSession('s1')).toBeNull();
  });

  it('closes it before a prompt reaches the agent', async () => {
    const ctx = setup();
    const id = await openTerminal(ctx);
    const sink = fakeSink();
    ctx.control.terminals.attach(id, sink);
    void ctx.control.prompt('s1', 'dev-a', 'go', 'p1');
    const types = ctx.link.sent.map((m) => m.type);
    expect(types.indexOf('terminal.close')).toBeGreaterThan(-1);
    expect(types.indexOf('terminal.close')).toBeLessThan(types.indexOf('runtime.prompt'));
    expect(ctx.link.ofType('terminal.close')).toMatchObject([{ reason: 'turn-started' }]);
    expect(sink.got.at(-1)).toMatchObject({ type: 'terminal.ended', reason: 'turn-started' });
  });

  it('ends it without a message to the agent when the owner disconnects', async () => {
    const ctx = setup();
    const id = await openTerminal(ctx);
    const sink = fakeSink();
    ctx.control.terminals.attach(id, sink);
    ctx.control.detach(ctx.link);
    expect(sink.got.at(-1)).toMatchObject({
      type: 'terminal.ended',
      terminalId: id,
      reason: 'owner-offline',
    });
    expect(ctx.link.ofType('terminal.close')).toHaveLength(0);
    expect(ctx.control.terminals.ofSession('s1')).toBeNull();
  });

  it('ends it with the reason and exit code the agent reports', async () => {
    const ctx = setup();
    const id = await openTerminal(ctx);
    const sink = fakeSink();
    ctx.control.terminals.attach(id, sink);
    ctx.control.receive(ctx.link, {
      v: PROTOCOL_VERSION,
      type: 'terminal.closed',
      terminalId: id,
      reason: 'exit',
      exitCode: 3,
    } as unknown as ControlReply);
    expect(sink.got.at(-1)).toMatchObject({ type: 'terminal.ended', reason: 'exit', exitCode: 3 });
  });

  it('closes it on request and refuses to close it twice', async () => {
    const ctx = setup();
    const id = await openTerminal(ctx);
    expect(ctx.control.terminals.close(id)).toBe(true);
    expect(ctx.control.terminals.close(id)).toBe(false);
    expect(ctx.link.ofType('terminal.close')).toHaveLength(1);
  });
});

describe('the REST routes', () => {
  async function boot() {
    const ctx = setup();
    const config = { port: 0, dataDir: '', token: 'host', webDir: null };
    const server = createServer();
    createApi(ctx.store, config as never, ctx.control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const call = (method: string, p: string, body?: unknown, token = 'host') =>
      fetch(base + p, {
        method,
        headers: {
          ...(token ? { authorization: `Bearer ${token}` } : {}),
          'content-type': 'application/json',
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    return { ...ctx, call };
  }

  it('requires the host token', async () => {
    const { call } = await boot();
    const routes = [
      ['POST', '/api/sessions/s1/terminal', { cols: 80, rows: 24 }],
      ['GET', '/api/sessions/s1/terminal', undefined],
      ['DELETE', '/api/terminals/x', undefined],
      ['GET', '/api/sessions/s1/ports', undefined],
    ] as const;
    for (const [method, p, body] of routes) {
      expect((await call(method, p, body, 'wrong')).status).toBe(401);
      expect((await call(method, p, body, '')).status).toBe(401);
    }
  });

  it('opens, reports and closes a terminal', async () => {
    const ctx = await boot();
    const pending = ctx.call('POST', '/api/sessions/s1/terminal', { cols: 80, rows: 24 });
    for (let i = 0; i < 100 && ctx.link.ofType('terminal.open').length === 0; i++) {
      await new Promise((r) => setTimeout(r, 10));
    }
    const msg = ctx.link.ofType('terminal.open')[0] as unknown as { terminalId: string };
    ctx.control.receive(ctx.link, {
      v: PROTOCOL_VERSION,
      type: 'terminal.opened',
      terminalId: msg.terminalId,
    } as unknown as ControlReply);
    const res = await pending;
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      terminalId: msg.terminalId,
      sessionId: 's1',
      cols: 80,
      rows: 24,
    });
    expect(await (await ctx.call('GET', '/api/sessions/s1/terminal')).json()).toMatchObject({
      terminal: { terminalId: msg.terminalId },
    });
    expect((await ctx.call('DELETE', `/api/terminals/${msg.terminalId}`)).status).toBe(200);
    expect((await ctx.call('DELETE', `/api/terminals/${msg.terminalId}`)).status).toBe(404);
    expect(await (await ctx.call('GET', '/api/sessions/s1/terminal')).json()).toEqual({
      terminal: null,
    });
  });

  it('answers 409 terminal-unavailable when the capability is missing', async () => {
    const ctx = await boot();
    const link = fakeLink();
    ctx.control.attach('dev-a', link, [], {});
    ctx.control.receive(link, state('s1', 'idle'));
    const res = await ctx.call('POST', '/api/sessions/s1/terminal', { cols: 80, rows: 24 });
    expect(res.status).toBe(409);
    expect(JSON.stringify(await res.json())).toContain('terminal-unavailable');
  });
});
