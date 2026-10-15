import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { gzipSync } from 'node:zlib';
import { afterEach, expect, it } from 'vitest';
import { initialSchema } from '../src/migrations/0001-initial-schema.ts';
import { sessionProvider } from '../src/migrations/0002-session-provider.ts';
import { deviceTokenHash } from '../src/migrations/0003-device-token-hash.ts';
import { sessionLinks } from '../src/migrations/0004-session-links.ts';
import { sessionLinksDirected } from '../src/migrations/0005-session-links-directed.ts';
import { storageRecovery } from '../src/migrations/0006-storage-recovery.ts';
import { migrate } from '../src/migrations/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { force: true, recursive: true });
});
const oldMigrations = [
  initialSchema,
  sessionProvider,
  deviceTokenHash,
  sessionLinks,
  sessionLinksDirected,
];
for (const schema of [1, 2, 3, 4, 5]) {
  it(`upgrades schema ${schema} preserving old gzip and plaintext bytes`, async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-migration-'));
    dirs.push(dir);
    const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
    db.exec('CREATE TABLE schema_migrations(id INTEGER PRIMARY KEY, name TEXT NOT NULL)');
    for (const migration of oldMigrations.slice(0, schema)) {
      migration.up(db);
      db.prepare('INSERT INTO schema_migrations VALUES(?, ?)').run(migration.id, migration.name);
    }
    // Independently constructed historical representation: 500 LF lines plus one CRLF tail.
    const sealed = gzipSync(Buffer.from('{}\n'.repeat(500)));
    const tail = '{ "old": "字节" }\r\n';
    const sessionDir = path.join(dir, 'sessions', 'old');
    mkdirSync(sessionDir, { recursive: true });
    writeFileSync(path.join(sessionDir, 'c-0.ndjson.gz'), sealed);
    writeFileSync(path.join(sessionDir, 'c-1.ndjson'), tail);
    db.prepare(`INSERT INTO sessions(session_id, created_at, updated_at, line_count, byte_size,
      chunk_count, cur_chunk_lines, device_id) VALUES('old', 1, 1, 501, ?, 2, 1, 'd')`).run(
      1500 + Buffer.byteLength(tail),
    );
    db.close();
    const store = new Store(dir);
    expect(store.getSession('old')?.byteSize).toBe(1500 + Buffer.byteLength(tail));
    expect((await store.readLines('old', -1, 1))?.lines).toEqual([{ old: '字节' }]);
    store.append({ sessionId: 'old', projectDir: 'p', filePath: '/synthetic', deviceId: 'd' }, [
      '{"new":true}',
    ]);
    expect(readFileSync(path.join(sessionDir, 'c-0.ndjson.gz')).equals(sealed)).toBe(true);
    expect(readFileSync(path.join(sessionDir, 'c-1.ndjson'), 'utf8')).toBe(`${tail}{"new":true}\n`);
    expect(new Store(dir).getSession('old')?.lineCount).toBe(502);
  });
}

it('refuses a future schema without changing its data', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-migration-'));
  dirs.push(dir);
  new Store(dir);
  const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
  db.prepare('INSERT INTO schema_migrations VALUES(999, ?)').run('future');
  db.close();
  expect(() => new Store(dir)).toThrow('data schema is newer');
});

const SESSION_COLUMNS = `session_id, created_at, updated_at, line_count, byte_size, chunk_count,
  cur_chunk_lines, device_id`;

/** A data directory at schema 6 holding a Claude Code session, two ZCode sessions and links. */
function oldDataDir(): { dir: string; db: DatabaseSync } {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-purge-'));
  dirs.push(dir);
  const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
  db.exec('CREATE TABLE schema_migrations(id INTEGER PRIMARY KEY, name TEXT NOT NULL)');
  for (const migration of [...oldMigrations, storageRecovery]) {
    migration.up(db);
    db.prepare('INSERT INTO schema_migrations VALUES(?, ?)').run(migration.id, migration.name);
  }
  const line = '{"type":"user"}\n';
  for (const [id, provider] of [
    ['keep', 'claude'],
    ['ses_z1', 'zcode'],
    ['ses_z2', 'zcode'],
  ] as const) {
    db.prepare(
      `INSERT INTO sessions(${SESSION_COLUMNS}, provider) VALUES(?, 1, 1, 2, ?, 1, 2, 'd', ?)`,
    ).run(id, line.length * 2, provider);
    const sessionDir = path.join(dir, 'sessions', id);
    mkdirSync(sessionDir, { recursive: true });
    writeFileSync(path.join(sessionDir, 'c-0.ndjson'), line + line);
  }
  // A sealed chunk next to the open one, as a long session has.
  writeFileSync(path.join(dir, 'sessions', 'ses_z1', 'c-1.ndjson.gz'), gzipSync(line));
  db.exec(`INSERT INTO session_links VALUES
    ('keep', 'ses_z1', 'related', 1), ('ses_z1', 'ses_z2', 'spawned', 1), ('keep', 'ses_z2', 'related', 1)`);
  db.exec("INSERT INTO append_recovery VALUES('ses_z2', 0, 0, 0)");
  db.exec("INSERT INTO compression_pending VALUES('ses_z2', 0)");
  return { dir, db };
}

const tables = (db: DatabaseSync): string[] =>
  (db.prepare("SELECT name FROM sqlite_master WHERE type='table'").all() as { name: string }[]).map(
    (r) => r.name,
  );

it('drops links and removes ZCode sessions with their chunks', async () => {
  const { dir, db } = oldDataDir();
  db.close();
  const store = new Store(dir);
  expect(store.sessionCount()).toBe(1);
  expect(store.getSession('keep')?.lineCount).toBe(2);
  expect((await store.readLines('keep', 0, 10))?.lines).toEqual([
    { type: 'user' },
    { type: 'user' },
  ]);
  for (const id of ['ses_z1', 'ses_z2']) {
    expect(store.getSession(id)).toBeNull();
    expect(await store.readLines(id, 0, 10)).toBeNull();
    expect(existsSync(path.join(dir, 'sessions', id))).toBe(false);
  }
  expect(existsSync(path.join(dir, 'sessions', 'keep', 'c-0.ndjson'))).toBe(true);
  const after = new DatabaseSync(path.join(dir, 'hoplet.db'));
  expect(tables(after)).not.toContain('session_links');
  expect(store.getSession('keep')?.provider).toBe('claude');
  for (const table of ['append_recovery', 'compression_pending', 'session_purge']) {
    expect(after.prepare(`SELECT COUNT(*) AS n FROM ${table}`).get()?.n).toBe(0);
  }
  expect(after.prepare('SELECT id FROM schema_migrations WHERE id = 7').get()).toBeDefined();
  after.close();
});

it('serves nothing from ZCode sessions when the process stops before the chunks are removed', () => {
  const { dir, db } = oldDataDir();
  migrate(db); // Committed; the process dies before the store removes the queued directories.
  db.close();
  for (const id of ['ses_z1', 'ses_z2']) {
    expect(existsSync(path.join(dir, 'sessions', id))).toBe(true);
  }
  const queued = new DatabaseSync(path.join(dir, 'hoplet.db'));
  expect(queued.prepare('SELECT COUNT(*) AS n FROM session_purge').get()?.n).toBe(2);
  expect(queued.prepare('SELECT COUNT(*) AS n FROM sessions').get()?.n).toBe(1);
  queued.close();
  // A partly removed directory from an interrupted purge does not matter to the retry.
  rmSync(path.join(dir, 'sessions', 'ses_z1', 'c-0.ndjson'));
  const store = new Store(dir);
  expect(store.sessionCount()).toBe(1);
  for (const id of ['ses_z1', 'ses_z2']) {
    expect(existsSync(path.join(dir, 'sessions', id))).toBe(false);
  }
  const done = new DatabaseSync(path.join(dir, 'hoplet.db'));
  expect(done.prepare('SELECT COUNT(*) AS n FROM session_purge').get()?.n).toBe(0);
  done.close();
  // A second start has nothing left to do.
  expect(new Store(dir).sessionCount()).toBe(1);
});

it('keeps every session and chunk when the migration fails part-way', () => {
  const { dir, db } = oldDataDir();
  db.exec('DROP TABLE session_links'); // The last step of the migration then fails.
  db.close();
  expect(() => new Store(dir)).toThrow();
  const failed = new DatabaseSync(path.join(dir, 'hoplet.db'));
  expect(failed.prepare('SELECT COUNT(*) AS n FROM sessions').get()?.n).toBe(3);
  expect(failed.prepare('SELECT COUNT(*) AS n FROM append_recovery').get()?.n).toBe(1);
  expect(tables(failed)).not.toContain('session_purge');
  expect(failed.prepare('SELECT id FROM schema_migrations WHERE id = 7').get()).toBeUndefined();
  failed.close();
  for (const id of ['keep', 'ses_z1', 'ses_z2']) {
    expect(existsSync(path.join(dir, 'sessions', id, 'c-0.ndjson'))).toBe(true);
  }
});

it('records the claude provider for every session that exists when the column returns', () => {
  const { dir, db } = oldDataDir();
  migrate(db); // Through migration 8; the purge queue is irrelevant to this check.
  db.close();
  const after = new DatabaseSync(path.join(dir, 'hoplet.db'));
  const columns = after.prepare('PRAGMA table_info(sessions)').all() as {
    name: string;
    notnull: number;
    dflt_value: string | null;
  }[];
  const provider = columns.find((c) => c.name === 'provider');
  expect(provider).toMatchObject({ notnull: 1, dflt_value: "'claude'" });
  expect(after.prepare('SELECT provider FROM sessions').all()).toEqual([{ provider: 'claude' }]);
  expect(after.prepare('SELECT id FROM schema_migrations WHERE id = 8').get()).toBeDefined();
  after.close();
  const store = new Store(dir);
  expect(store.getSession('keep')?.provider).toBe('claude');
  expect(store.listSessions().map((s) => s.provider)).toEqual(['claude']);
  store.append({ sessionId: 'fresh', projectDir: 'p', filePath: '/synthetic', deviceId: 'd' }, [
    '{}',
  ]);
  expect(store.getSession('fresh')?.provider).toBe('claude');
});
