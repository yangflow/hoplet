import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import type { Config } from '../src/config/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) {
    ws.terminate();
  }
  for (const s of servers.splice(0)) {
    await new Promise<void>((resolve) => s.close(() => resolve()));
  }
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const line = (i: number): string =>
  JSON.stringify({
    type: 'user',
    cwd: '/tmp/proj',
    message: { role: 'user', content: `line ${i}` },
  });

async function boot(): Promise<{ port: number; store: Store; token: string }> {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-prompt-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append(
    { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    [line(0)],
  );
  const token = 'tok';
  const config: Config = { port: 0, dataDir: dir, token, webDir: null };
  const server = createServer();
  const control = createControl(store, {
    promptTimeoutMs: 400,
    workspaceTimeoutMs: 400,
  });
  attachIngest(server, store, control);
  createApi(store, config, control).attach(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  return { port, store, token };
}

function openWs(
  port: number,
  token: string,
): Promise<{ ws: WebSocket; welcome: Record<string, unknown> }> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${token}` },
    });
    sockets.push(ws);
    const onErr = (err: Error) => reject(err);
    ws.once('error', onErr);
    ws.once('message', (data) => {
      ws.off('error', onErr);
      resolve({ ws, welcome: JSON.parse(String(data)) as Record<string, unknown> });
    });
  });
}

async function openAgent(
  port: number,
  store: Store,
  deviceId: string,
): Promise<{ ws: WebSocket; welcome: Record<string, unknown> }> {
  const minted = store.mintDevice(deviceId, deviceId);
  return openWs(port, minted.token);
}

function onceJson(ws: WebSocket): Promise<Record<string, unknown>> {
  return new Promise((resolve, reject) => {
    const onMsg = (data: WebSocket.RawData) => {
      ws.off('error', onErr);
      resolve(JSON.parse(String(data)) as Record<string, unknown>);
    };
    const onErr = (err: Error) => {
      ws.off('message', onMsg);
      reject(err);
    };
    ws.once('message', onMsg);
    ws.once('error', onErr);
  });
}

function hello(ws: WebSocket, deviceId: string): void {
  ws.send(
    JSON.stringify({
      v: PROTOCOL_VERSION,
      type: 'device.hello',
      deviceId,
      name: deviceId,
      version: 'test',
    }),
  );
}

async function waitOwner(port: number, token: string, connected: boolean): Promise<void> {
  for (let i = 0; i < 50; i++) {
    const res = await fetch(`http://127.0.0.1:${port}/api/sessions`, {
      headers: { authorization: `Bearer ${token}` },
    });
    const body = (await res.json()) as { sessions: { ownerConnected?: boolean }[] };
    if (body.sessions[0]?.ownerConnected === connected) return;
    await new Promise((r) => setTimeout(r, 20));
  }
  throw new Error(`ownerConnected did not become ${connected}`);
}

async function postPrompt(
  port: number,
  token: string,
  sessionId: string,
  text: string,
  promptId = `p-${Math.random().toString(36).slice(2)}`,
): Promise<{ status: number; body: Record<string, unknown> }> {
  const res = await fetch(`http://127.0.0.1:${port}/api/sessions/${sessionId}/prompt`, {
    method: 'POST',
    headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
    body: JSON.stringify({ text, promptId }),
  });
  return { status: res.status, body: (await res.json()) as Record<string, unknown> };
}

describe('POST /api/sessions/:id/prompt', () => {
  it('refuses when the owner agent is offline', async () => {
    const { port, token } = await boot();
    const r = await postPrompt(port, token, 's1', 'hello');
    expect(r.status).toBe(409);
    expect(r.body.code).toBe('owner-offline');
  });

  it('returns 404 for an unknown session', async () => {
    const { port, token } = await boot();
    const r = await postPrompt(port, token, 'missing', 'hello');
    expect(r.status).toBe(404);
  });

  it('forwards runtime.prompt only to the owner connection', async () => {
    const { port, token, store } = await boot();
    const owner = await openAgent(port, store, 'dev-a');
    const other = await openAgent(port, store, 'dev-b');
    hello(owner.ws, 'dev-a');
    hello(other.ws, 'dev-b');
    await waitOwner(port, token, true);

    const ownerGot: Record<string, unknown>[] = [];
    const otherGot: Record<string, unknown>[] = [];
    owner.ws.on('message', (d) => ownerGot.push(JSON.parse(String(d)) as Record<string, unknown>));
    other.ws.on('message', (d) => otherGot.push(JSON.parse(String(d)) as Record<string, unknown>));

    const pending = postPrompt(port, token, 's1', 'continue here');
    let prompt: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      prompt = ownerGot.find((m) => m.type === 'runtime.prompt');
      if (prompt) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(prompt).toMatchObject({
      type: 'runtime.prompt',
      sessionId: 's1',
      text: 'continue here',
    });
    expect(otherGot.some((m) => m.type === 'runtime.prompt')).toBe(false);

    owner.ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'runtime.state',
        sessionId: 's1',
        state: 'running',
        capabilities: {
          permissions: true,
          interrupt: true,
          config: [],
        },
      }),
    );
    const r = await pending;
    expect(r.status).toBe(202);
    expect(r.body.ok).toBe(true);
    expect(r.body.promptId).toBe(prompt?.promptId);
  });

  it('marks ownerConnected on GET /api/sessions while the owner is live', async () => {
    const { port, token, store } = await boot();
    const listed = await fetch(`http://127.0.0.1:${port}/api/sessions`, {
      headers: { authorization: `Bearer ${token}` },
    });
    const cold = (await listed.json()) as { sessions: { ownerConnected: boolean }[] };
    expect(cold.sessions[0]?.ownerConnected).toBe(false);

    const owner = await openAgent(port, store, 'dev-a');
    hello(owner.ws, 'dev-a');
    await waitOwner(port, token, true);
  });

  it('rejects session.append from an agent that never sent device.hello', async () => {
    const { port, store } = await boot();
    const browser = await openAgent(port, store, 'dev-x');
    expect(browser.welcome.type).toBe('welcome');
    const reply = onceJson(browser.ws);
    browser.ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'session.append',
        sessionId: 's1',
        provider: 'claude',
        projectDir: 'p',
        filePath: '/x',
        byteStart: 0,
        encoding: 'plain',
        lines: `${line(1)}\n`,
      }),
    );
    const msg = await reply;
    expect(msg).toMatchObject({ type: 'error', code: 'hello-first' });
  });
});

describe('GET /api/sessions/:id/workspace', () => {
  it('refuses when the owner agent is offline', async () => {
    const { port, token } = await boot();
    const res = await fetch(`http://127.0.0.1:${port}/api/sessions/s1/workspace`, {
      headers: { authorization: `Bearer ${token}` },
    });
    expect(res.status).toBe(409);
    expect(await res.json()).toMatchObject({ code: 'owner-offline' });
  });

  it('forwards session.workspace.list only to the owner', async () => {
    const { port, token, store } = await boot();
    const owner = await openAgent(port, store, 'dev-a');
    const other = await openAgent(port, store, 'dev-b');
    hello(owner.ws, 'dev-a');
    hello(other.ws, 'dev-b');
    await waitOwner(port, token, true);

    const ownerGot: Record<string, unknown>[] = [];
    const otherGot: Record<string, unknown>[] = [];
    owner.ws.on('message', (d) => ownerGot.push(JSON.parse(String(d)) as Record<string, unknown>));
    other.ws.on('message', (d) => otherGot.push(JSON.parse(String(d)) as Record<string, unknown>));

    const pending = fetch(`http://127.0.0.1:${port}/api/sessions/s1/workspace`, {
      headers: { authorization: `Bearer ${token}` },
    });
    let listMsg: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      listMsg = ownerGot.find((m) => m.type === 'session.workspace.list');
      if (listMsg) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(listMsg).toMatchObject({ type: 'session.workspace.list', sessionId: 's1' });
    expect(otherGot.some((m) => m.type === 'session.workspace.list')).toBe(false);

    owner.ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'session.workspace',
        sessionId: 's1',
        requestId: listMsg?.requestId,
        git: true,
        commit: 'abc123',
        origin: 'git@example.test:o/r.git',
        paths: ['README.md', 'src/a.go'],
        changes: [
          { path: 'src/a.go', status: 'modified', added: 2, removed: 1 },
          { path: 'logo.png', status: 'untracked', binary: true },
        ],
      }),
    );
    const res = await pending;
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      sessionId: 's1',
      git: true,
      commit: 'abc123',
      origin: 'git@example.test:o/r.git',
      paths: ['README.md', 'src/a.go'],
      truncated: false,
      changes: [
        { path: 'src/a.go', status: 'modified', added: 2, removed: 1 },
        { path: 'logo.png', status: 'untracked', binary: true },
      ],
    });
  });

  it('forwards session.workspace.file.read only to the owner', async () => {
    const { port, token, store } = await boot();
    const owner = await openAgent(port, store, 'dev-a');
    hello(owner.ws, 'dev-a');
    await waitOwner(port, token, true);
    const ownerGot: Record<string, unknown>[] = [];
    owner.ws.on('message', (d) => ownerGot.push(JSON.parse(String(d)) as Record<string, unknown>));

    const pending = fetch(
      `http://127.0.0.1:${port}/api/sessions/s1/workspace/file?path=${encodeURIComponent('README.md')}`,
      { headers: { authorization: `Bearer ${token}` } },
    );
    let readMsg: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      readMsg = ownerGot.find((m) => m.type === 'session.workspace.file.read');
      if (readMsg) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(readMsg).toMatchObject({ type: 'session.workspace.file.read', path: 'README.md' });
    owner.ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'session.workspace.file',
        sessionId: 's1',
        requestId: readMsg?.requestId,
        path: 'README.md',
        kind: 'text',
        size: 5,
        text: '# hi\n',
      }),
    );
    const res = await pending;
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      kind: 'text',
      sessionId: 's1',
      path: 'README.md',
      size: 5,
      text: '# hi\n',
      truncated: false,
    });
  });

  /** Sends a request through REST, answers it as the owner with `answer` and returns the response. */
  async function relay(
    request: string,
    type: string,
    answer: (requestId: unknown) => Record<string, unknown>,
  ): Promise<{ status: number; body: unknown; sent: Record<string, unknown> | undefined }> {
    const { port, token, store } = await boot();
    const owner = await openAgent(port, store, 'dev-a');
    hello(owner.ws, 'dev-a');
    await waitOwner(port, token, true);
    const got: Record<string, unknown>[] = [];
    owner.ws.on('message', (d) => got.push(JSON.parse(String(d)) as Record<string, unknown>));
    const pending = fetch(`http://127.0.0.1:${port}${request}`, {
      headers: { authorization: `Bearer ${token}` },
    });
    let sent: Record<string, unknown> | undefined;
    for (let i = 0; i < 50 && !sent; i++) {
      sent = got.find((m) => m.type === type);
      if (!sent) await new Promise((r) => setTimeout(r, 20));
    }
    owner.ws.send(
      JSON.stringify({ v: PROTOCOL_VERSION, sessionId: 's1', ...answer(sent?.requestId) }),
    );
    const res = await pending;
    return { status: res.status, body: await res.json(), sent };
  }

  it('forwards at=head and relays an image whole', async () => {
    const { status, body, sent } = await relay(
      '/api/sessions/s1/workspace/file?path=logo.png&at=head',
      'session.workspace.file.read',
      (requestId) => ({
        type: 'session.workspace.file',
        requestId,
        path: 'logo.png',
        kind: 'image',
        size: 3,
        mediaType: 'image/png',
        data: 'AQID',
      }),
    );
    expect(sent).toMatchObject({ path: 'logo.png', at: 'head' });
    expect(status).toBe(200);
    expect(body).toEqual({
      kind: 'image',
      sessionId: 's1',
      path: 'logo.png',
      size: 3,
      mediaType: 'image/png',
      data: 'AQID',
    });
  });

  it('forwards session.workspace.diff.read and relays a text or binary diff', async () => {
    const text = await relay(
      '/api/sessions/s1/workspace/diff?path=a.txt',
      'session.workspace.diff.read',
      (requestId) => ({
        type: 'session.workspace.diff',
        requestId,
        path: 'a.txt',
        status: 'modified',
        kind: 'text',
        diff: '@@ -1 +1 @@\n-a\n+b\n',
        size: 20,
      }),
    );
    expect(text.sent).toMatchObject({ type: 'session.workspace.diff.read', path: 'a.txt' });
    expect(text.body).toEqual({
      kind: 'text',
      sessionId: 's1',
      path: 'a.txt',
      status: 'modified',
      diff: '@@ -1 +1 @@\n-a\n+b\n',
      truncated: false,
      size: 20,
    });
    const binary = await relay(
      '/api/sessions/s1/workspace/diff?path=new.bin',
      'session.workspace.diff.read',
      (requestId) => ({
        type: 'session.workspace.diff',
        requestId,
        path: 'new.bin',
        status: 'untracked',
        kind: 'binary',
        newSize: 9,
      }),
    );
    expect(binary.body).toEqual({
      kind: 'binary',
      sessionId: 's1',
      path: 'new.bin',
      status: 'untracked',
      newSize: 9,
    });
    const refused = await relay(
      '/api/sessions/s1/workspace/diff?path=same.txt',
      'session.workspace.diff.read',
      (requestId) => ({ type: 'session.workspace.rejected', requestId, reason: 'not-changed' }),
    );
    expect(refused).toMatchObject({
      status: 502,
      body: { code: 'rejected', reason: 'not-changed' },
    });
  });

  it('forwards the base, cursor and limit of a diff page and relays next', async () => {
    const page = await relay(
      '/api/sessions/s1/workspace/diff?path=a.txt&against=base&cursor=100&limit=4096',
      'session.workspace.diff.read',
      (requestId) => ({
        type: 'session.workspace.diff',
        requestId,
        path: 'a.txt',
        status: 'modified',
        kind: 'text',
        diff: '+x\n',
        truncated: true,
        next: 103,
        size: 9000,
      }),
    );
    expect(page.sent).toMatchObject({ path: 'a.txt', base: true, cursor: 100, limit: 4096 });
    expect(page.body).toMatchObject({ kind: 'text', truncated: true, next: 103, size: 9000 });
    const plain = await relay(
      '/api/sessions/s1/workspace/diff?path=a.txt',
      'session.workspace.diff.read',
      (requestId) => ({ type: 'session.workspace.rejected', requestId, reason: 'invalid-cursor' }),
    );
    expect(plain.sent?.base).toBeUndefined();
    expect(plain.sent?.cursor).toBeUndefined();
    expect(plain.sent?.limit).toBeUndefined();
    expect(plain).toMatchObject({ status: 502, body: { reason: 'invalid-cursor' } });
  });

  it('refuses a diff query with a limit or cursor out of range', async () => {
    const { port, token } = await boot();
    for (const q of ['limit=0', 'limit=262145', 'cursor=-1', 'cursor=x', 'against=other']) {
      const res = await fetch(
        `http://127.0.0.1:${port}/api/sessions/s1/workspace/diff?path=a.txt&${q}`,
        { headers: { authorization: `Bearer ${token}` } },
      );
      expect(res.status, q).toBe(400);
    }
  });

  it('forwards session.workspace.changes.read and relays the list or a refusal', async () => {
    const listed = await relay(
      '/api/sessions/s1/workspace/changes',
      'session.workspace.changes.read',
      (requestId) => ({
        type: 'session.workspace.changes',
        requestId,
        branch: 'hoplet/child',
        base: 'abc1234',
        total: 1,
        files: [{ path: 'a.txt', status: 'modified', added: 2, removed: 1 }],
      }),
    );
    expect(listed.sent).toMatchObject({ type: 'session.workspace.changes.read' });
    expect(listed.body).toEqual({
      sessionId: 's1',
      branch: 'hoplet/child',
      base: 'abc1234',
      shared: false,
      total: 1,
      files: [{ path: 'a.txt', status: 'modified', added: 2, removed: 1 }],
    });
    const refused = await relay(
      '/api/sessions/s1/workspace/changes',
      'session.workspace.changes.read',
      (requestId) => ({
        type: 'session.workspace.rejected',
        requestId,
        reason: 'not-a-git-repository',
      }),
    );
    expect(refused).toMatchObject({
      status: 502,
      body: { code: 'rejected', reason: 'not-a-git-repository' },
    });
  });
});

describe('POST /api/devices', () => {
  it('mints a device token that can hello', async () => {
    const { port, token, store } = await boot();
    const mint = await fetch(`http://127.0.0.1:${port}/api/devices`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ name: 'mac', deviceId: 'dev-a' }),
    });
    expect(mint.status).toBe(200);
    const body = (await mint.json()) as { deviceId: string; token: string; name: string };
    expect(body).toMatchObject({ deviceId: 'dev-a', name: 'mac' });
    expect(body.token).toMatch(/^[0-9a-f]{64}$/);
    expect(store.deviceIdForToken(body.token)).toBe('dev-a');

    const agent = await openWs(port, body.token);
    hello(agent.ws, 'dev-a');
    await waitOwner(port, token, true);

    const other = store.mintDevice('other', 'dev-b');
    const mismatch = await openWs(port, other.token);
    const mismatchReply = onceJson(mismatch.ws);
    hello(mismatch.ws, 'dev-a');
    expect(await mismatchReply).toMatchObject({ type: 'error', code: 'device-mismatch' });
  });
});
