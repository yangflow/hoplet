// A machine that loses its connection and comes back (ADR 0039): the welcome's high-water mark, journalled
// events applied once, the owner epoch on appends, the journal gap and a diverged transcript.
import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, type ServerMessage, type SessionAppend } from '@hoplet/shared';
import { afterEach, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { type Control, createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const SESSION = '3f2b6c1e-8d4a-4f0b-9c7e-1a2b3c4d5e6f';
const LINE = '{"type":"user","message":{"content":"hi"}}\n';

let cleanup: (() => Promise<void>) | null = null;
afterEach(async () => {
  await cleanup?.();
  cleanup = null;
});

type Msg = Record<string, unknown>;

async function boot(): Promise<{
  store: Store;
  control: Control;
  published: ServerMessage[];
  connect(
    id: string,
    token: string,
  ): Promise<{ ws: WebSocket; welcome: Msg; next(): Promise<Msg> }>;
}> {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-disconnect-'));
  const store = new Store(dir);
  const control = createControl(store);
  const published: ServerMessage[] = [];
  control.subscribe((msg) => published.push(msg));
  const server: Server = createServer();
  attachIngest(server, store, control);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;
  const sockets: WebSocket[] = [];
  cleanup = async () => {
    for (const ws of sockets) ws.terminate();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    rmSync(dir, { recursive: true, force: true });
  };
  return {
    store,
    control,
    published,
    async connect(id, token) {
      const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
        headers: { authorization: `Bearer ${token}` },
      });
      sockets.push(ws);
      const queue: Msg[] = [];
      const waiting: ((m: Msg) => void)[] = [];
      ws.on('message', (raw) => {
        const msg = JSON.parse(String(raw)) as Msg;
        const wake = waiting.shift();
        if (wake) wake(msg);
        else queue.push(msg);
      });
      const next = (): Promise<Msg> => {
        const queued = queue.shift();
        if (queued) return Promise.resolve(queued);
        return new Promise((resolve) => waiting.push(resolve));
      };
      const welcome = await next();
      ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'device.hello',
          deviceId: id,
          name: id,
          version: 'test',
        }),
      );
      expect(await next()).toMatchObject({ type: 'device.rules' });
      return { ws, welcome, next };
    },
  };
}

const append = (byteStart: number, ownerEpoch?: number): SessionAppend => ({
  v: PROTOCOL_VERSION,
  type: 'session.append',
  sessionId: SESSION,
  provider: 'claude',
  projectDir: 'p',
  filePath: '/x',
  byteStart,
  encoding: 'plain',
  lines: LINE,
  ...(ownerEpoch !== undefined ? { ownerEpoch } : {}),
});

it('tells a returning machine the last journalled event the core applied, and applies each once', async () => {
  const { store, connect } = await boot();
  const device = store.mintDevice('mac', 'mac');
  const first = await connect('mac', device.token);
  expect(first.welcome).toMatchObject({ type: 'welcome', eventSeq: 0 });

  const event = (eventSeq: number) =>
    JSON.stringify({
      v: PROTOCOL_VERSION,
      type: 'journal.gap',
      dropped: 3,
      from: 1000,
      to: 2000,
      eventSeq,
      at: 2000,
      replayed: true,
    });
  first.ws.send(event(5));
  expect(await first.next()).toEqual({ v: PROTOCOL_VERSION, type: 'journal.ack', eventSeq: 5 });
  first.ws.send(event(5));
  expect(await first.next()).toEqual({ v: PROTOCOL_VERSION, type: 'journal.ack', eventSeq: 5 });
  // The gap is in the audit log once, at the time the agent gave.
  const gaps = store.listAudit({ type: 'journal.gap' }, { order: 'asc', limit: 10 }).events;
  expect(gaps).toHaveLength(1);
  expect(gaps[0]).toMatchObject({ at: 2000, deviceId: 'mac' });
  first.ws.close();

  const again = await connect('mac', device.token);
  expect(again.welcome).toMatchObject({ type: 'welcome', eventSeq: 5 });
});

it('refuses an append written under an owner epoch the session has moved past', async () => {
  const { store, connect } = await boot();
  const device = store.mintDevice('mac', 'mac');
  const { ws, next } = await connect('mac', device.token);
  ws.send(JSON.stringify(append(0, 0)));
  expect(await next()).toMatchObject({ type: 'session.ack', byteSize: LINE.length });
  // The session went to another machine and came back: the epoch is now 2.
  store.mintDevice('pc', 'pc');
  expect(store.handoffOwner(SESSION, 'mac', 'pc', LINE.length, 0, '/p', 'main')).toBe(1);
  expect(store.handoffOwner(SESSION, 'pc', 'mac', LINE.length, 1, '/p', 'main')).toBe(2);
  ws.send(JSON.stringify(append(LINE.length, 0)));
  expect(await next()).toMatchObject({ type: 'session.rejected', reason: 'not-owner' });
  expect(store.getSession(SESSION)?.byteSize).toBe(LINE.length);
  ws.send(JSON.stringify(append(LINE.length, 2)));
  expect(await next()).toMatchObject({ type: 'session.ack', byteSize: LINE.length * 2 });
});

it('records and shows a transcript that diverged, and the catch-up of a backlog', async () => {
  const { store, control, published, connect } = await boot();
  const device = store.mintDevice('mac', 'mac');
  const { ws, next } = await connect('mac', device.token);
  ws.send(JSON.stringify(append(0)));
  expect(await next()).toMatchObject({ type: 'session.ack' });
  published.length = 0;
  ws.send(
    JSON.stringify({
      v: PROTOCOL_VERSION,
      type: 'device.backlog',
      sessions: [{ sessionId: SESSION, bytes: LINE.length }],
    }),
  );
  ws.send(JSON.stringify(append(LINE.length)));
  expect(await next()).toMatchObject({ type: 'session.ack' });
  const progress = published.filter((m) => m.type === 'session.runtime' && m.sessionId === SESSION);
  expect(progress.map((m) => ('catchUp' in m ? m.catchUp : null))).toEqual([
    { received: 0, total: LINE.length },
    null,
  ]);
  expect(control.runtimeOf(SESSION).catchUp).toBeUndefined();

  ws.send(
    JSON.stringify({
      v: PROTOCOL_VERSION,
      type: 'session.diverged',
      sessionId: SESSION,
      ownerEpoch: 0,
      bytes: 120,
      lines: 2,
    }),
  );
  await new Promise((r) => setTimeout(r, 50));
  expect(published.find((m) => m.type === 'session.divergence')).toMatchObject({
    sessionId: SESSION,
    deviceId: 'mac',
    bytes: 120,
    lines: 2,
  });
  const records = store.listAudit({ type: 'session.diverged' }, { order: 'asc', limit: 10 }).events;
  expect(records).toHaveLength(1);
  expect(records[0]).toMatchObject({ sessionId: SESSION, deviceId: 'mac' });
});
