import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { migrate } from '../src/migrations/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const line = '{"type":"user","message":{"role":"user","content":"hello"}}';

function newStore(ids: string[]) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-continuations-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const id of ids) {
    store.append(
      { sessionId: id, provider: 'claude', projectDir: 'p', filePath: `/${id}`, deviceId: 'dev-a' },
      [line],
    );
  }
  return { store, dir };
}

describe('session_continuations migration', () => {
  it('creates the table keyed by the old session with a unique new one, and is recorded', () => {
    const db = new DatabaseSync(':memory:');
    migrate(db);
    migrate(db); // a second run changes nothing
    const insert = db.prepare('INSERT INTO session_continuations VALUES(?, ?, ?, ?, ?)');
    insert.run('a', 'b', 'r', 'default', 1);
    expect(() => insert.run('a', 'c', 'r', 'default', 1)).toThrow();
    expect(() => insert.run('d', 'b', 'r', 'default', 1)).toThrow();
    expect(
      db.prepare("SELECT name FROM schema_migrations WHERE name = 'session-continuations'").get(),
    ).toBeDefined();
    db.close();
  });
});

describe('Store continuations', () => {
  it('records, replaces with a later continuation and looks up both directions', () => {
    const { store } = newStore(['s1', 's2']);
    store.recordContinuation('s1', 'n1', 'r1', 'default', 1);
    expect(store.continuationLinks('s1')).toEqual({ continuedIn: 'n1' });
    expect(store.continuationLinks('n1')).toEqual({ continuedFrom: 's1' });
    store.recordContinuation('s1', 'n2', 'r2', 'plan', 2);
    expect(store.continuationLinks('s1')).toEqual({ continuedIn: 'n2' });
    expect(store.continuationLinks('n1')).toEqual({});
    store.recordContinuation('n2', 'n3', 'r3', 'plan', 3);
    expect(store.continuationLinks('n2')).toEqual({ continuedIn: 'n3', continuedFrom: 's1' });
    store.recordContinuation('s2', 'n3', 'r4', 'plan', 4); // the unique new session takes the later link
    expect(store.continuationLinks('n3')).toEqual({ continuedFrom: 's2' });
    expect(store.continuationLinks('n2')).toEqual({ continuedFrom: 's1' });
  });

  it('removes the row when either session is purged', () => {
    const { store } = newStore(['s1', 's2', 'n2']);
    store.recordContinuation('s1', 'n1', 'r1', 'default', 1);
    store.purgeSession('s1');
    expect(store.continuationLinks('n1')).toEqual({});
    store.recordContinuation('s2', 'n2', 'r2', 'default', 1);
    store.purgeSession('n2');
    expect(store.continuationLinks('s2')).toEqual({});
  });
});

describe('Session meta continuations', () => {
  it('carries both ends with their titles in the list and the detail', async () => {
    const { store, dir } = newStore(['s1', 's2', 'n1']);
    const server = createServer();
    createApi(
      store,
      { port: 0, dataDir: dir, token: HOST, webDir: null },
      createControl(store),
    ).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const get = async (p: string) =>
      (await fetch(base + p, { headers: { authorization: `Bearer ${HOST}` } })).json() as Promise<
        Record<string, unknown>
      >;

    store.updateSessionState('n1', { titleOverride: 'Mine' });
    store.recordContinuation('s1', 'n1', 'r', 'default', 1);
    store.recordContinuation('s2', 'unsynced', 'r', 'default', 1);

    const s1 = await get('/api/sessions/s1');
    expect(s1.continuedIn).toEqual({ sessionId: 'n1', title: 'Mine' });
    expect(s1.continuedFrom).toBeUndefined();

    const list = (await get('/api/sessions')).sessions as {
      sessionId: string;
      title: string;
      continuedIn?: unknown;
      continuedFrom?: unknown;
    }[];
    const byId = (id: string) => list.find((s) => s.sessionId === id);
    expect(byId('n1')?.continuedFrom).toEqual({ sessionId: 's1', title: byId('s1')?.title });
    expect(byId('s2')?.continuedIn).toEqual({ sessionId: 'unsynced', title: '' });
  });
});
