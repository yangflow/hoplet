import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

for (const [point, committed] of [
  ['after-append', false],
  ['before-index', false],
  ['after-commit', true],
  ['gzip-written', true],
] as const) {
  it(`advertises and enforces the recovered cursor over WebSocket after ${point}`, async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-ws-'));
    const child = spawnSync(
      process.execPath,
      ['core/test/fixtures/interrupted-append.ts', dir, point, 'kill', '499', '502'],
      { encoding: 'utf8' },
    );
    expect(child.signal, child.stderr).toBe('SIGKILL');
    const store = new Store(dir);
    const server = createServer();
    attachIngest(server, store, createControl(store));
    const sockets: WebSocket[] = [];
    const receive = (ws: WebSocket): Promise<Record<string, unknown>> =>
      new Promise((resolve, reject) => {
        ws.once('message', (raw) => resolve(JSON.parse(String(raw))));
        ws.once('error', reject);
      });
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = (server.address() as AddressInfo).port;
    const connect = async (id: string) => {
      const token = store.mintDevice(id, id).token;
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
          version: 'synthetic',
        }),
      );
      expect(await rules).toMatchObject({ type: 'device.rules' });
      return { ws, welcome };
    };
    const request = async (ws: WebSocket, byteStart: number, lines: string) => {
      const response = receive(ws);
      ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'session.append',
          sessionId: 'recovery',
          provider: 'claude',
          projectDir: 'p',
          filePath: '/synthetic',
          byteStart,
          encoding: 'plain',
          lines,
        }),
      );
      return response;
    };
    const lines = Array.from({ length: 1001 }, (_, n) =>
      JSON.stringify({ n, text: '原始\r bytes' }),
    );
    const initial = Buffer.byteLength(`${lines.slice(0, 499).join('\n')}\n`);
    const total = Buffer.byteLength(`${lines.join('\n')}\n`);
    const cursor = committed ? total : initial;
    try {
      const owner = await connect('d');
      expect(owner.welcome).toMatchObject({
        sessions: [{ sessionId: 'recovery', byteSize: cursor, lineCount: committed ? 1001 : 499 }],
      });
      const other = await connect('other');
      expect(await request(other.ws, 0, '{}\n')).toMatchObject({
        type: 'session.rejected',
        reason: 'not-owner',
      });
      expect(await request(owner.ws, 0, '{}\n')).toMatchObject({
        type: 'session.resync',
        byteSize: cursor,
      });
      if (committed) {
        expect(await request(owner.ws, initial, `${lines.slice(499).join('\n')}\n`)).toMatchObject({
          type: 'session.resync',
          byteSize: total,
        });
      } else {
        expect(await request(owner.ws, initial, `${lines.slice(499).join('\n')}\n`)).toMatchObject({
          type: 'session.ack',
          byteSize: total,
          lineCount: 1001,
        });
      }
      expect(await request(owner.ws, total, '{"next":true}\n')).toMatchObject({
        type: 'session.ack',
        byteSize: total + 14,
        lineCount: 1002,
      });
      expect((await store.readLines('recovery', -2, 2))?.lines).toEqual([
        { n: 1000, text: '原始\r bytes' },
        { next: true },
      ]);
      expect(readFileSync(path.join(dir, 'sessions/recovery/c-2.ndjson'), 'utf8')).toBe(
        `${lines[1000]}\n{"next":true}\n`,
      );
    } finally {
      for (const ws of sockets) ws.terminate();
      await new Promise<void>((resolve) => server.close(() => resolve()));
      rmSync(dir, { recursive: true, force: true });
    }
  });
}
