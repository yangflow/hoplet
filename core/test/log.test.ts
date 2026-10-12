import { mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { log, registerSecret } from '../src/log/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-secret-0123456789abcdef';
const PROMPT_TEXT = 'PROMPT-SECRET-do-not-log';
const LINE_SECRET = 'LINE-SECRET-do-not-log';

function capture(): string[] {
  const out: string[] = [];
  vi.spyOn(console, 'log').mockImplementation((line: unknown) => {
    out.push(String(line));
  });
  return out;
}

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  vi.restoreAllMocks();
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

describe('the log module is the only writer to standard output', () => {
  it('has no other console or stdout use in core/src', () => {
    const root = path.resolve(import.meta.dirname, '../src');
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const file = path.join(dir, name);
        if (statSync(file).isDirectory()) walk(file);
        else if (
          file.endsWith('.ts') &&
          !file.endsWith(path.join('log', 'index.ts')) &&
          /\bconsole\.|process\.std(out|err)/.test(readFileSync(file, 'utf8'))
        )
          offenders.push(path.relative(root, file));
      }
    };
    walk(root);
    expect(offenders).toEqual([]);
  });
});

describe('log redaction', () => {
  it('replaces a registered secret wherever it appears', () => {
    const out = capture();
    const secret = 'registered-secret-"quoted"-0123';
    registerSecret(secret);
    log('info', 'test', `message with ${secret}`, {
      nested: { deep: [`list ${secret}`, { more: secret }] },
      error: String(new Error(`failed for ${secret}`)),
      err: new Error(`object ${secret}`),
    });
    expect(out).toHaveLength(1);
    expect(out[0]).not.toContain(secret);
    expect(out[0]).not.toContain('registered-secret');
    expect(JSON.parse(out[0] ?? '').message).toBe('message with [redacted]');
  });

  it('redacts credentials by their shape, registered or not', () => {
    const out = capture();
    log('warn', 'test', 'GET /ws?token=abc123SECRET&x=1 Authorization: Bearer xyz789SECRET', {
      url: 'ws://h/ws?ticket=tkt456SECRET',
      header: 'Bearer hdr000SECRET',
    });
    const text = out.join('\n');
    for (const leaked of ['abc123SECRET', 'xyz789SECRET', 'tkt456SECRET', 'hdr000SECRET']) {
      expect(text).not.toContain(leaked);
    }
    expect(JSON.parse(out[0] ?? '').message).toContain('&x=1');
  });

  it('drops the value of fields that hold content or credentials', () => {
    const out = capture();
    log('info', 'test', 'fields', {
      text: 'prompt text',
      Token: 'tok',
      nested: { authorization: 'a', lines: ['l'], content: 'c', kept: 'ok' },
      sessionId: 's1',
    });
    const entry = JSON.parse(out[0] ?? '');
    expect(entry).toMatchObject({
      text: '[redacted]',
      Token: '[redacted]',
      nested: {
        authorization: '[redacted]',
        lines: '[redacted]',
        content: '[redacted]',
        kept: 'ok',
      },
      sessionId: 's1',
    });
  });

  it('does not register strings too short to be secrets', () => {
    const out = capture();
    registerSecret('abc');
    log('info', 'test', 'abc stays');
    expect(JSON.parse(out[0] ?? '').message).toBe('abc stays');
  });
});

describe('secrets through the running core', () => {
  async function boot() {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-log-'));
    dirs.push(dir);
    const store = new Store(dir);
    const control = createControl(store, { promptTimeoutMs: 100, workspaceTimeoutMs: 100 });
    const server = createServer();
    attachIngest(server, store, control);
    createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    return { store, port: (server.address() as AddressInfo).port };
  }

  it('never writes tokens, tickets, prompts or session lines', async () => {
    const out = capture();
    const { store, port } = await boot();
    const base = `http://127.0.0.1:${port}`;
    const device = store.mintDevice(`name-${LINE_SECRET}`, 'dev-a');

    // Refused credentials on every route family.
    await fetch(`${base}/api/sessions`, { headers: { authorization: `Bearer ${device.token}` } });
    await fetch(`${base}/api/sessions`, {
      headers: { authorization: 'Bearer wrong-secret-value' },
    });
    const ticket = (await (
      await fetch(`${base}/api/ws-tickets`, {
        method: 'POST',
        headers: { authorization: `Bearer ${HOST}` },
      })
    ).json()) as { ticket: string };
    const dial = (url: string, headers: Record<string, string> = {}) =>
      new Promise<void>((resolve) => {
        const ws = new WebSocket(url, { headers });
        sockets.push(ws);
        ws.on('open', () => resolve());
        ws.on('unexpected-response', () => resolve());
        ws.on('error', () => resolve());
      });
    await dial(`ws://127.0.0.1:${port}/ws?token=${HOST}`);
    await dial(`ws://127.0.0.1:${port}/ws/agent?token=${device.token}`);
    await dial(`ws://127.0.0.1:${port}/ws/agent`, { authorization: `Bearer ${HOST}` });
    await dial(`ws://127.0.0.1:${port}/ws?ticket=${ticket.ticket}`);

    // An agent session: hello, an append whose lines hold a secret, a malformed frame, a prompt.
    const agent = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${device.token}` },
    });
    sockets.push(agent);
    await new Promise<void>((resolve) => agent.once('open', () => resolve()));
    agent.on('message', (raw) => {
      const msg = JSON.parse(String(raw)) as {
        type: string;
        promptId?: string;
        sessionId?: string;
      };
      if (msg.type === 'runtime.prompt') {
        agent.send(
          JSON.stringify({
            v: PROTOCOL_VERSION,
            type: 'runtime.rejected',
            sessionId: msg.sessionId,
            promptId: msg.promptId,
            reason: 'synthetic',
          }),
        );
      }
    });
    const send = (m: unknown) => agent.send(typeof m === 'string' ? m : JSON.stringify(m));
    send({ v: PROTOCOL_VERSION, type: 'device.hello', deviceId: 'dev-a', name: 'a', version: '1' });
    send({
      v: PROTOCOL_VERSION,
      type: 'session.append',
      sessionId: 's1',
      projectDir: 'p',
      filePath: '/x',
      byteStart: 0,
      encoding: 'plain',
      lines: `${JSON.stringify({ type: 'user', message: { content: LINE_SECRET } })}\n`,
    });
    send(`not json ${device.token} ${LINE_SECRET}`);
    send({ v: PROTOCOL_VERSION, type: 'nope', note: LINE_SECRET });
    await new Promise((r) => setTimeout(r, 100));
    await fetch(`${base}/api/sessions/s1/prompt`, {
      method: 'POST',
      headers: { authorization: `Bearer ${HOST}`, 'content-type': 'application/json' },
      body: JSON.stringify({ text: PROMPT_TEXT, promptId: 'p-log' }),
    });

    const text = out.join('\n');
    expect(out.length).toBeGreaterThan(3);
    for (const secret of [HOST, device.token, ticket.ticket, PROMPT_TEXT, LINE_SECRET]) {
      expect(text, secret).not.toContain(secret);
    }
    // The audit trail of the prompt is there, without its text.
    expect(
      out.map((l) => JSON.parse(l)).find((e) => e.message === 'prompt forwarded'),
    ).toMatchObject({ module: 'control', sessionId: 's1' });
  });
});
