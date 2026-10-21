import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer, request } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

it('rejects invalid identifiers before append or metadata writes over WebSocket', async () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-identifiers-'));
  const store = new Store(dir);
  const server = createServer();
  const config = { port: 0, dataDir: dir, token: 'test', webDir: null };
  const control = createControl(store);
  attachIngest(server, store, control);
  createApi(store, config, control).attach(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const device = store.mintDevice('synthetic', 'synthetic');
  const ws = new WebSocket(`ws://127.0.0.1:${(server.address() as AddressInfo).port}/ws/agent`, {
    headers: { authorization: `Bearer ${device.token}` },
  });
  const receive = () =>
    new Promise<Record<string, unknown>>((resolve) => {
      ws.once('message', (raw) => resolve(JSON.parse(String(raw))));
    });
  try {
    await receive();
    const rules = receive();
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: 'synthetic',
        name: 'synthetic',
        version: 'test',
      }),
    );
    expect(await rules).toMatchObject({ type: 'device.rules' });
    for (const sessionId of [
      '../escaped',
      'a/b',
      'a\\b',
      '.',
      '..',
      '',
      '/absolute',
      'C:\\absolute',
      '%2e%2e%2fescaped',
      '%252f',
      ' id ',
      'a\u0000b',
      undefined,
      null,
      true,
      12,
      {},
      [],
    ]) {
      for (const lines of ['{}\n', '']) {
        const response = receive();
        ws.send(
          JSON.stringify({
            v: PROTOCOL_VERSION,
            type: 'session.append',
            sessionId,
            byteStart: 0,
            provider: 'claude',
            projectDir: 'p',
            filePath: '/synthetic',
            encoding: 'plain',
            lines,
            title: 'Rejected',
          }),
        );
        expect(await response).toMatchObject({ type: 'error', code: 'invalid-session-id' });
        expect(store.sessionCount()).toBe(0);
        expect(readdirSync(path.join(dir, 'sessions'))).toEqual([]);
        expect(
          readdirSync(dir).filter((name) => !name.startsWith('hoplet.db') && name !== 'sessions'),
        ).toEqual([]);
      }
    }
    expect(store.sessionCount()).toBe(0);
    const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
    for (const table of ['append_recovery', 'compression_pending']) {
      expect(db.prepare(`SELECT COUNT(*) AS n FROM ${table}`).get()?.n).toBe(0);
    }
    db.close();
    for (const sessionId of [
      '00000000-0000-4000-8000-000000000001',
      'ses_synthetic123',
      'legacy_id-1',
    ]) {
      const response = receive();
      ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'session.append',
          sessionId,
          byteStart: 0,
          provider: 'claude',
          projectDir: 'p',
          filePath: '/synthetic',
          encoding: 'plain',
          lines: '{}\n',
        }),
      );
      expect(await response).toMatchObject({ type: 'session.ack', sessionId, byteSize: 3 });
      expect(readFileSync(path.join(dir, 'sessions', sessionId, 'c-0.ndjson'), 'utf8')).toBe(
        '{}\n',
      );
    }
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const headers = { authorization: 'Bearer test', 'content-type': 'application/json' };
    for (const id of [' legacy_id-1 ', '../escaped', '%2f', null, 12]) {
      for (const action of ['import', 'remove']) {
        const imported = await fetch(`${base}/api/devices/synthetic/sessions/${action}`, {
          method: 'POST',
          headers,
          body: JSON.stringify({ path: '/synthetic', sessionIds: ['legacy_id-1', id] }),
        });
        expect(imported.status).toBe(400);
      }
    }
    for (const id of ['%2e%2e%2fescaped', '%252f', 'a%5cb', 'a%2fb']) {
      const res = await fetch(`${base}/api/sessions/${id}/lines`, { headers });
      expect(res.status).toBe(404);
    }
    // Fetch normalizes dot segments itself; send the original HTTP request target.
    for (const rawPath of [
      '/api/sessions/../sessions/legacy_id-1',
      '/api/sessions/%2e%2e/sessions/legacy_id-1',
    ]) {
      const status = await new Promise<number>((resolve, reject) => {
        const req = request(
          {
            hostname: '127.0.0.1',
            port: (server.address() as AddressInfo).port,
            path: rawPath,
            headers,
          },
          (res) => {
            res.resume();
            resolve(res.statusCode ?? 0);
          },
        );
        req.on('error', reject);
        req.end();
      });
      expect(status).toBe(404);
    }
    for (const id of ['../escaped', '', '%2f']) {
      expect(await control.handoff({ sessionId: id, targetDeviceId: 'target' })).toMatchObject({
        ok: false,
        error: 'rejected',
        reason: 'invalid-session-id',
      });
    }
  } finally {
    ws.terminate();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    rmSync(dir, { recursive: true, force: true });
  }
});

it('Store rejects invalid append identifiers without index or journal changes', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-id-store-'));
  const store = new Store(dir);
  try {
    store.append(
      {
        sessionId: 'parent',
        deviceId: 'synthetic',
        provider: 'claude',
        projectDir: 'p',
        filePath: '/synthetic',
      },
      ['{}'],
    );
    const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
    for (const id of ['../escape', 'a/b', 'a\\b', '', '.', '..', '%2f', ' id ', 'a\n', '中文']) {
      for (const lines of [[], ['{}']]) {
        expect(() =>
          store.append(
            {
              sessionId: id,
              deviceId: 'synthetic',
              provider: 'claude',
              projectDir: 'p',
              filePath: '/synthetic',
            },
            lines,
          ),
        ).toThrow('invalid-session-id');
      }
      expect(store.getSession(id)).toBeNull();
      expect(store.sessionCount()).toBe(1);
      for (const table of ['append_recovery', 'compression_pending']) {
        expect(db.prepare(`SELECT COUNT(*) AS n FROM ${table}`).get()?.n).toBe(0);
      }
      expect(readdirSync(path.join(dir, 'sessions'))).toEqual(['parent']);
    }
    db.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

it('preflights every pending ID before recovering any chunk or compression work', () => {
  for (const table of ['append_recovery', 'compression_pending']) {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-id-recovery-'));
    try {
      const store = new Store(dir);
      store.append(
        {
          sessionId: 'valid',
          deviceId: 'synthetic',
          provider: 'claude',
          projectDir: 'p',
          filePath: '/synthetic',
        },
        ['{}'],
      );
      const chunk = path.join(dir, 'sessions/valid/c-0.ndjson');
      writeFileSync(chunk, '{}\nUNCOMMITTED');
      const escaped = path.join(dir, 'escape');
      mkdirSync(escaped);
      writeFileSync(path.join(escaped, 'c-0.ndjson'), 'SENTINEL');
      const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
      db.exec("INSERT INTO append_recovery VALUES('valid', 0, 3, 0)");
      if (table === 'append_recovery')
        db.exec("INSERT INTO append_recovery VALUES('../escape', 0, 0, 0)");
      else db.exec("INSERT INTO compression_pending VALUES('../escape', 0)");
      expect(() => new Store(dir)).toThrow('invalid-session-id');
      expect(readFileSync(chunk, 'utf8')).toBe('{}\nUNCOMMITTED');
      expect(readFileSync(path.join(escaped, 'c-0.ndjson'), 'utf8')).toBe('SENTINEL');
      expect(db.prepare('SELECT COUNT(*) AS n FROM append_recovery').get()?.n).toBe(
        table === 'append_recovery' ? 2 : 1,
      );
      db.close();
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }
});
