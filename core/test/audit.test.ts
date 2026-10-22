import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import {
  type AuditEvent,
  type AuditEventsResponse,
  type AuditVerifyResponse,
  PROTOCOL_VERSION,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { verifyAuditFile } from '../../cli/src/audit-verify.ts';
import { auditExport } from '../src/api/audit-export.ts';
import { createApi } from '../src/api/index.ts';
import { type ControlReply, createControl, type DeviceLink } from '../src/control/index.ts';
import { AUDIT_GENESIS_HASH, auditHash, canonicalJson } from '../src/store/audit.ts';
import { AuditUnavailableError, Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const DAY = 86_400_000;
const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) {
    s.closeAllConnections();
    await new Promise<void>((r) => s.close(() => r()));
  }
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function open(options: { auditRetentionDays?: number } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-audit-'));
  dirs.push(dir);
  const store = new Store(dir, options);
  const raw = () => new DatabaseSync(path.join(dir, 'hoplet.db'));
  return { dir, store, raw };
}

const all = (store: Store): AuditEvent[] =>
  store.listAudit({}, { order: 'asc', limit: 500 }).events;

describe('canonical JSON and the hash', () => {
  it('sorts keys, drops whitespace and holds integers only', () => {
    expect(canonicalJson({ b: [1, { z: null, a: 'x' }], a: true })).toBe(
      '{"a":true,"b":[1,{"a":"x","z":null}]}',
    );
    expect(() => canonicalJson({ n: 1.5 })).toThrow();
  });

  it('hashes the previous hash and the fields', () => {
    const fields = {
      seq: 1,
      at: 1,
      recorded_at: 1,
      type: 'audit.started',
      session_id: null,
      device_id: null,
      actor_kind: 'core',
      actor_id: '',
      channel: 'core',
      ref: null,
      summary: {},
    };
    expect(auditHash(AUDIT_GENESIS_HASH, fields)).toMatch(/^[0-9a-f]{64}$/);
    expect(auditHash(AUDIT_GENESIS_HASH, fields)).not.toBe(
      auditHash(AUDIT_GENESIS_HASH, { ...fields, at: 2 }),
    );
  });
});

describe('the chain', () => {
  it('starts with audit.started and links every record to the one before', async () => {
    const { store } = open();
    store.audit({ type: 'session.stopped', sessionId: 's1' });
    store.audit({ type: 'session.stopped', sessionId: 's2' });
    const events = all(store);
    expect(events.map((e) => e.type)).toEqual([
      'audit.started',
      'session.stopped',
      'session.stopped',
    ]);
    expect(events[0]?.prevHash).toBe(AUDIT_GENESIS_HASH);
    expect(events[1]?.prevHash).toBe(events[0]?.hash);
    expect(events[2]?.prevHash).toBe(events[1]?.hash);
    expect(store.auditHead()).toEqual({ seq: 3, hash: events[2]?.hash });
    expect(await store.verifyAudit()).toMatchObject({ ok: true, checked: 3, firstBreak: null });
  });

  it('writes audit.started once, however often the core starts', () => {
    const { dir, store } = open();
    store.audit({ type: 'session.stopped' });
    const again = new Store(dir);
    expect(all(again).map((e) => e.type)).toEqual(['audit.started', 'session.stopped']);
  });

  it('numbers records without reusing a removed seq', async () => {
    const { store, raw } = open();
    store.audit({ type: 'session.stopped' });
    const db = raw();
    db.exec('DELETE FROM audit_events WHERE seq = 2');
    db.close();
    store.audit({ type: 'session.stopped' });
    expect(all(store).map((e) => e.seq)).toEqual([1, 3]);
    expect(await store.verifyAudit()).toMatchObject({
      ok: false,
      firstBreak: { seq: 3, reason: 'gap' },
    });
  });

  it('cuts a summary that is too large to the marker', () => {
    const { store } = open();
    store.audit({
      type: 'config.changed',
      summary: Object.fromEntries(Array.from({ length: 200 }, (_, i) => [`k${i}`, 'x'.repeat(30)])),
    });
    expect(all(store).at(-1)?.summary).toEqual({ truncated: true });
  });
});

describe('tampering', () => {
  async function seeded() {
    const t = open();
    for (let i = 0; i < 5; i++) t.store.audit({ type: 'session.stopped', sessionId: `s${i}` });
    return t;
  }

  it('reports an edited record', async () => {
    const { store, raw } = await seeded();
    const db = raw();
    db.exec(`UPDATE audit_events SET session_id = 'other' WHERE seq = 3`);
    db.close();
    expect(await store.verifyAudit()).toMatchObject({
      ok: false,
      checked: 3,
      firstBreak: { seq: 3, reason: 'hash-mismatch' },
    });
  });

  it('reports an edited record whose hash was recomputed, at the next record', async () => {
    const { store, raw } = await seeded();
    const db = raw();
    const row = db.prepare('SELECT * FROM audit_events WHERE seq = 3').get() as Record<
      string,
      unknown
    >;
    const hash = auditHash(String(row.prev_hash), {
      seq: 3,
      at: Number(row.at),
      recorded_at: Number(row.recorded_at),
      type: String(row.type),
      session_id: 'other',
      device_id: null,
      actor_kind: String(row.actor_kind),
      actor_id: String(row.actor_id),
      channel: String(row.channel),
      ref: null,
      summary: {},
    });
    db.prepare(`UPDATE audit_events SET session_id = 'other', hash = ? WHERE seq = 3`).run(hash);
    db.close();
    expect(await store.verifyAudit()).toMatchObject({
      ok: false,
      firstBreak: { seq: 4, reason: 'prev-hash-mismatch' },
    });
  });

  it('reports a removed record in the middle', async () => {
    const { store, raw } = await seeded();
    const db = raw();
    db.exec('DELETE FROM audit_events WHERE seq = 3');
    db.close();
    expect(await store.verifyAudit()).toMatchObject({
      ok: false,
      firstBreak: { seq: 4, reason: 'gap' },
    });
  });

  it('reports a removed tail only against a head written down', async () => {
    const { store, raw } = await seeded();
    const head = store.auditHead();
    if (!head) throw new Error('no head');
    const db = raw();
    db.exec('DELETE FROM audit_events WHERE seq >= 5');
    db.close();
    expect(await store.verifyAudit()).toMatchObject({ ok: true });
    expect(await store.verifyAudit(head)).toMatchObject({
      ok: false,
      firstBreak: { seq: head.seq, reason: 'head-missing' },
    });
  });

  it('reports a replaced record against a head written down', async () => {
    const { store } = await seeded();
    const head = store.auditHead();
    if (!head) throw new Error('no head');
    expect(await store.verifyAudit(head)).toMatchObject({ ok: true });
    expect(await store.verifyAudit({ seq: head.seq, hash: 'f'.repeat(64) })).toMatchObject({
      ok: false,
      firstBreak: { reason: 'head-mismatch' },
    });
  });

  it('reports the front of the log removed without a checkpoint', async () => {
    const { store, raw } = await seeded();
    const db = raw();
    db.exec('DELETE FROM audit_events WHERE seq <= 2');
    db.close();
    expect(await store.verifyAudit()).toMatchObject({
      ok: false,
      firstBreak: { seq: 3, reason: 'removed-outside-retention' },
    });
  });
});

describe('retention', () => {
  it('removes old records behind a checkpoint that keeps the chain verifiable', async () => {
    const { store, raw } = open({ auditRetentionDays: 30 });
    const now = Date.now();
    store.audit({ type: 'session.stopped', sessionId: 'old' });
    store.audit({ type: 'session.stopped', sessionId: 'new-1' }, now + 20 * DAY);
    store.audit({ type: 'session.stopped', sessionId: 'new-2' }, now + 20 * DAY);
    const before = all(store);

    expect(await store.pruneAudit(now + 40 * DAY)).toBe(2);
    const after = all(store);
    expect(after.map((e) => e.type)).toEqual([
      'session.stopped',
      'session.stopped',
      'audit.checkpoint',
    ]);
    expect(after[2]?.summary).toEqual({
      removedThrough: 2,
      removedHash: before[1]?.hash,
      removedCount: 2,
    });
    expect(await store.verifyAudit()).toMatchObject({ ok: true, checked: 3 });

    // The remaining front is only trusted because the checkpoint says so.
    const db = raw();
    db.exec('DELETE FROM audit_events WHERE seq = 3');
    db.close();
    expect(await store.verifyAudit()).toMatchObject({
      ok: false,
      firstBreak: { reason: 'removed-outside-retention' },
    });
  });

  it('keeps everything with a retention of 0 days', async () => {
    const { store } = open({ auditRetentionDays: 0 });
    store.audit({ type: 'session.stopped' });
    expect(await store.pruneAudit(Date.now() + 1000 * DAY)).toBe(0);
    expect(all(store)).toHaveLength(2);
  });
});

describe('scrubbing', () => {
  it('masks credentials in any summary string and in nested values', () => {
    const { store } = open();
    store.audit({
      type: 'config.changed',
      summary: {
        note: 'curl https://me:pw123@host.test/r.git',
        nested: { list: ['echo ghp_abcdefghijklmnopqrstuvwxyz0123'] },
      },
    });
    const text = JSON.stringify(all(store).at(-1)?.summary);
    expect(text).not.toContain('pw123');
    expect(text).not.toContain('ghp_');
  });

  it('masks a device name in the record of a minted token', () => {
    const { store } = open();
    const minted = store.mintDevice('ghp_abcdefghijklmnopqrstuvwxyz0123', 'd1');
    const event = all(store).find((e) => e.type === 'device.token.issued');
    expect(event?.deviceId).toBe('d1');
    expect(JSON.stringify(event)).not.toContain('ghp_');
    expect(JSON.stringify(event)).not.toContain(minted.token);
  });
});

describe('a write that fails', () => {
  function breakWrites(raw: () => DatabaseSync) {
    const db = raw();
    db.exec(
      `CREATE TRIGGER no_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'full'); END`,
    );
    db.close();
  }

  it('throws AuditUnavailableError and leaves no record', () => {
    const { store, raw } = open();
    breakWrites(raw);
    expect(() => store.audit({ type: 'session.stopped' })).toThrow(AuditUnavailableError);
    expect(all(store)).toHaveLength(1);
  });

  it('stops the action it belongs to: no token is minted', () => {
    const { store, raw } = open();
    breakWrites(raw);
    expect(() => store.mintDevice('Mac', 'd1')).toThrow(AuditUnavailableError);
    expect(store.listDevices().some((d) => d.deviceId === 'd1')).toBe(false);
  });
});

function fakeLink(): DeviceLink & { sent: ServerMessage[] } {
  const link = {
    sent: [] as ServerMessage[],
    send(msg: ServerMessage) {
      link.sent.push(msg);
    },
    async sendAndWait(msg: ServerMessage) {
      link.sent.push(msg);
    },
    isOpen: () => true,
  };
  return link;
}

const running = (sessionId: string) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.state',
    sessionId,
    state: 'running',
    capabilities: { permissions: true, interrupt: true, steer: true, config: [] },
  }) as ControlReply;

describe('the routes', () => {
  async function boot() {
    const t = open();
    t.store.append(
      {
        sessionId: 's1',
        provider: 'claude',
        projectDir: 'p',
        filePath: '/x/s1',
        deviceId: 'dev-a',
      },
      [
        JSON.stringify({
          type: 'user',
          cwd: '/work/app',
          message: { role: 'user', content: 'hi' },
        }),
      ],
    );
    const control = createControl(t.store, { promptTimeoutMs: 300 });
    const link = fakeLink();
    control.attach('dev-a', link);
    control.receive(link, running('s1'));
    const server = createServer();
    createApi(t.store, { port: 0, dataDir: t.dir, token: HOST, webDir: null }, control).attach(
      server,
    );
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    const call = (method: string, url: string, auth = true) =>
      fetch(`http://127.0.0.1:${port}${url}`, {
        method,
        headers: auth ? { authorization: `Bearer ${HOST}` } : {},
      });
    return { ...t, call, link };
  }

  it('needs the host token', async () => {
    const { call } = await boot();
    for (const url of ['/api/audit/events', '/api/audit/export', '/api/audit/verify']) {
      expect((await call('GET', url, false)).status).toBe(401);
    }
  });

  it('pages and filters the records', async () => {
    const { call, store } = await boot();
    for (let i = 0; i < 4; i++) store.audit({ type: 'session.stopped', sessionId: `s${i}` });
    store.audit({ type: 'permission.answered', sessionId: 's1', summary: { tool: 'Bash' } });

    const first = (await (
      await call('GET', '/api/audit/events?limit=2')
    ).json()) as AuditEventsResponse;
    expect(first.events.map((e) => e.seq)).toEqual([6, 5]);
    expect(first.next).toBe(5);
    const second = (await (
      await call('GET', `/api/audit/events?limit=2&after=${first.next}`)
    ).json()) as AuditEventsResponse;
    expect(second.events.map((e) => e.seq)).toEqual([4, 3]);

    const byType = (await (
      await call('GET', '/api/audit/events?type=permission.')
    ).json()) as AuditEventsResponse;
    expect(byType.events.map((e) => e.type)).toEqual(['permission.answered']);
    const bySession = (await (
      await call('GET', '/api/audit/events?session=s1&order=asc')
    ).json()) as AuditEventsResponse;
    expect(bySession.events.map((e) => e.type)).toEqual(['session.stopped', 'permission.answered']);
    const bySummary = (await (
      await call('GET', '/api/audit/events?q=Bash')
    ).json()) as AuditEventsResponse;
    expect(bySummary.events).toHaveLength(1);
    expect((await call('GET', '/api/audit/events?actor=nobody')).status).toBe(400);
  });

  it('verifies the chain and a head', async () => {
    const { call, store } = await boot();
    store.audit({ type: 'session.stopped' });
    const head = store.auditHead();
    const ok = (await (await call('GET', '/api/audit/verify')).json()) as AuditVerifyResponse;
    expect(ok).toMatchObject({ ok: true, checked: 2, head });
    const good = (await (
      await call('GET', `/api/audit/verify?head=${head?.seq}:${head?.hash}`)
    ).json()) as AuditVerifyResponse;
    expect(good.ok).toBe(true);
    const bad = (await (
      await call('GET', `/api/audit/verify?head=${head?.seq}:${'a'.repeat(64)}`)
    ).json()) as AuditVerifyResponse;
    expect(bad).toMatchObject({ ok: false, firstBreak: { reason: 'head-mismatch' } });
    expect((await call('GET', '/api/audit/verify?head=nonsense')).status).toBe(400);
  });

  it('exports JSON lines that verify offline, and records the export', async () => {
    const { call, store } = await boot();
    store.audit({ type: 'session.stopped', sessionId: 's1' });
    const res = await call('GET', '/api/audit/export');
    expect(res.status).toBe(200);
    expect(res.headers.get('content-type')).toContain('application/x-ndjson');
    const text = await res.text();
    const lines = text.trim().split('\n');
    expect(lines).toHaveLength(2);
    expect(verifyAuditFile(text)).toMatchObject({ ok: true, checked: 2 });

    const exported = all(store).at(-1);
    expect(exported).toMatchObject({
      type: 'audit.exported',
      summary: { format: 'jsonl', count: 2 },
    });

    const edited = text.replace('"s1"', '"s9"');
    expect(verifyAuditFile(edited)).toMatchObject({
      ok: false,
      firstBreak: { seq: 2, reason: 'hash-mismatch' },
    });
    const head = store.auditHead();
    expect(verifyAuditFile(text, { seq: 2, hash: 'b'.repeat(64) })).toMatchObject({
      firstBreak: { reason: 'head-mismatch' },
    });
    expect(head).not.toBeNull();
  });

  it('exports CSV with a header and a quote before a formula', async () => {
    const { call, store } = await boot();
    store.audit({ type: 'session.stopped', actor: { kind: 'operator', id: '=SUM(1)' } });
    store.audit({ type: 'session.stopped', actor: { kind: 'operator', id: 'a,"b"' } });
    const res = await call('GET', '/api/audit/export?format=csv&type=session.stopped');
    expect(res.headers.get('content-type')).toContain('text/csv');
    const text = await res.text();
    const [header, ...rows] = text.trim().split('\r\n');
    expect(header?.startsWith('seq,at,recordedAt,type,')).toBe(true);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toContain(`"'=SUM(1)"`);
    expect(rows[1]).toContain('"a,""b"""');
    expect(rows[0]).not.toMatch(/,=SUM/);
  });

  it('refuses a third export at once', async () => {
    const { store } = await boot();
    auditExport(store, { format: 'jsonl' });
    auditExport(store, { format: 'jsonl' });
    expect(() => auditExport(store, { format: 'jsonl' })).toThrow(
      expect.objectContaining({ code: 'rate-limited' }),
    );
  });

  it('answers audit-unavailable and does not carry out the action', async () => {
    const { call, raw, link } = await boot();
    const db = raw();
    db.exec(
      `CREATE TRIGGER no_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'full'); END`,
    );
    db.close();
    const res = await call('POST', '/api/sessions/s1/stop');
    expect(res.status).toBe(503);
    expect(await res.json()).toEqual({ code: 'audit-unavailable' });
    expect(link.sent.some((m) => m.type === 'runtime.stop')).toBe(false);
    expect((await call('GET', '/api/audit/export')).status).toBe(503);
  });

  it('records the actions it carries out', async () => {
    const { call, store, link } = await boot();
    expect((await call('POST', '/api/sessions/s1/interrupt')).status).toBe(200);
    expect((await call('POST', '/api/sessions/s1/stop')).status).toBe(200);
    expect(link.sent.some((m) => m.type === 'runtime.stop')).toBe(true);
    const events = all(store);
    expect(events.map((e) => e.type)).toEqual([
      'audit.started',
      'session.interrupted',
      'session.stopped',
    ]);
    expect(events[2]).toMatchObject({
      sessionId: 's1',
      deviceId: 'dev-a',
      actorKind: 'operator',
      channel: 'rest',
    });
  });
});
