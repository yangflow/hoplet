import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { gzipSync } from 'node:zlib';
import { PROTOCOL_VERSION, type SessionAppend } from '@hoplet/shared';
import { expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

it('resyncs wrong offsets without writes, enforces ownership, and resumes after reconnect', async () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-append-'));
  const store = new Store(dir);
  const server = createServer();
  attachIngest(server, store, createControl(store));
  const sockets: WebSocket[] = [];
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;
  const receive = (ws: WebSocket): Promise<Record<string, unknown>> =>
    new Promise((resolve, reject) => {
      ws.once('message', (raw) => resolve(JSON.parse(String(raw))));
      ws.once('error', reject);
    });
  const connect = async (id: string, token: string) => {
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${token}` },
    });
    sockets.push(ws);
    const welcome = await receive(ws);
    const rules = receive(ws);
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: id,
        name: id,
        version: 'test',
      }),
    );
    expect(await rules).toMatchObject({ type: 'device.rules' });
    return { ws, welcome };
  };
  const request = async (ws: WebSocket, msg: SessionAppend) => {
    const response = receive(ws);
    ws.send(JSON.stringify(msg));
    return response;
  };
  const batch = (byteStart: number, lines: string, title = 'Original'): SessionAppend => ({
    v: PROTOCOL_VERSION,
    type: 'session.append',
    sessionId: 's1',
    provider: 'claude',
    projectDir: 'p',
    filePath: '/x',
    byteStart,
    encoding: 'plain',
    lines,
    title,
  });
  try {
    const owner = store.mintDevice('owner', 'owner');
    let { ws } = await connect('owner', owner.token);
    const line = '{"type":"user","message":{"content":"1234567"}}\n';
    expect(Buffer.byteLength(line)).toBe(48);
    expect(await request(ws, { ...batch(10, line), sessionId: 'absent' })).toMatchObject({
      type: 'session.resync',
      byteSize: 0,
    });
    expect(store.getSession('absent')).toBeNull();
    expect(readdirSync(path.join(dir, 'sessions'))).toEqual([]);
    expect(await request(ws, batch(0, line))).toMatchObject({
      type: 'session.ack',
      byteSize: 48,
      lineCount: 1,
    });
    const chunk = path.join(dir, 'sessions/s1/c-0.ndjson');
    const before = store.getSession('s1');
    for (const offset of [0, 47, 49]) {
      for (const payload of [line, '']) {
        expect(await request(ws, batch(offset, payload, 'Rejected title'))).toMatchObject({
          type: 'session.resync',
          byteSize: 48,
        });
        expect(store.getSession('s1')).toEqual(before);
        expect(readFileSync(chunk, 'utf8')).toBe(line);
      }
    }
    // Reconnect after an ack is lost: welcome and resync both advertise stored bytes.
    ws.terminate();
    const reconnected = await connect('owner', owner.token);
    ws = reconnected.ws;
    expect(reconnected.welcome).toMatchObject({ sessions: [{ sessionId: 's1', byteSize: 48 }] });
    expect(await request(ws, batch(0, line))).toMatchObject({
      type: 'session.resync',
      byteSize: 48,
    });
    const next = '\n{"type":"user","cwd":"/中文","message":{"content":"你好"}}\n';
    const total = 48 + Buffer.byteLength(next);
    const compressed = {
      ...batch(48, next),
      encoding: 'gzip+base64' as const,
      lines: gzipSync(next).toString('base64'),
    };
    expect(await request(ws, compressed)).toMatchObject({
      type: 'session.ack',
      byteSize: total,
      lineCount: 3,
    });
    expect(readFileSync(chunk, 'utf8')).toBe(line + next);
    expect(await request(ws, batch(total, '', 'Owner title'))).toMatchObject({
      type: 'session.ack',
      byteSize: total,
      lineCount: 3,
    });
    expect(store.getSession('s1')?.title).toBe('Owner title');
    const envelope = '{"text":""}\n';
    const boundary = `{"text":"${'x'.repeat((1 << 20) - Buffer.byteLength(envelope))}"}\n`;
    expect(Buffer.byteLength(boundary)).toBe(1 << 20);
    for (const encoding of ['plain', 'gzip+base64'] as const) {
      const sessionId = `boundary-${encoding.replace('+', '-')}`;
      expect(
        await request(ws, {
          ...batch(0, encoding === 'plain' ? boundary : gzipSync(boundary).toString('base64')),
          sessionId,
          encoding,
        }),
      ).toMatchObject({ type: 'session.ack', byteSize: 1 << 20, lineCount: 1 });
      expect(await request(ws, { ...batch(1 << 20, '{}\n'), sessionId })).toMatchObject({
        type: 'session.ack',
        byteSize: (1 << 20) + 3,
        lineCount: 2,
      });
      expect(readFileSync(path.join(dir, `sessions/${sessionId}/c-0.ndjson`), 'utf8')).toBe(
        `${boundary}{}\n`,
      );
    }
    const other = store.mintDevice('other', 'other');
    const foreign = await connect('other', other.token);
    const snapshot = store.getSession('s1');
    for (const offset of [0, total]) {
      for (const payload of ['', line]) {
        expect(await request(foreign.ws, batch(offset, payload, 'Foreign title'))).toMatchObject({
          type: 'session.rejected',
          reason: 'not-owner',
          ownerDeviceId: 'owner',
        });
        expect(store.getSession('s1')).toEqual(snapshot);
        expect(readFileSync(chunk, 'utf8')).toBe(line + next);
      }
    }
  } finally {
    for (const ws of sockets) ws.terminate();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    rmSync(dir, { recursive: true, force: true });
  }
});
