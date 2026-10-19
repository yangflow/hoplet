import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import {
  type Control,
  type ControlReply,
  createControl,
  type DeviceLink,
} from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];
afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const SESSION = 's1';
type Behavior = (msg: ServerMessage, device: Device) => void;

/** An agent as the core sees it: a link that records what it is sent and answers through a script. */
class Device {
  readonly sent: ServerMessage[] = [];
  open = true;
  behavior: Behavior = () => {};
  readonly link: DeviceLink;
  private readonly control: Control & { receive(l: DeviceLink, m: ControlReply): void };
  constructor(control: Control & { receive(l: DeviceLink, m: ControlReply): void }) {
    this.control = control;
    this.link = {
      send: (msg) => {
        this.sent.push(msg);
        queueMicrotask(() => this.behavior(msg, this));
      },
      sendAndWait: async (msg) => {
        this.sent.push(msg);
        queueMicrotask(() => this.behavior(msg, this));
      },
      isOpen: () => this.open,
    };
  }
  reply(msg: Record<string, unknown>): void {
    this.control.receive(this.link, { v: PROTOCOL_VERSION, ...msg } as ControlReply);
  }
  of(type: string): Record<string, unknown>[] {
    return this.sent.filter((m) => m.type === type) as unknown as Record<string, unknown>[];
  }
}

function setup(options = { workspaceTimeoutMs: 200 }) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-delete-'));
  dirs.push(dir);
  const store = new Store(dir);
  const control = createControl(store, options);
  store.append(
    { sessionId: SESSION, provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'owner' },
    ['{"type":"user"}'],
  );
  const owner = new Device(control);
  const published: ServerMessage[] = [];
  control.subscribe((m) => published.push(m));
  return { dir, store, control, owner, published };
}

const ALL_DONE = [
  { step: 'stop-syncing', status: 'done' },
  { step: 'worktree', status: 'done' },
  { step: 'branch', status: 'done' },
  { step: 'vendor-file', status: 'done' },
];

/** An owner that answers inspect, delete and forget with `steps`. */
function answering(device: Device, steps: unknown[] = ALL_DONE) {
  device.behavior = (msg, d) => {
    const requestId = (msg as { requestId: string }).requestId;
    if (msg.type === 'device.session.inspect') {
      d.reply({
        type: 'device.session.inspected',
        requestId,
        vendorFile: { path: '/home/a/.claude/projects/p/s1.jsonl' },
        worktree: {
          path: '/home/a/r-s1',
          branch: 'hoplet/s1',
          dirty: { modified: 1, untracked: 2 },
          unpushed: 3,
        },
      });
    }
    if (msg.type === 'device.session.delete')
      d.reply({ type: 'device.session.deleted', requestId, steps });
    if (msg.type === 'device.session.forget') {
      d.reply({ type: 'device.session.forgotten', requestId, sessionId: SESSION });
    }
  };
}

const size = (store: Store): number => store.getSession(SESSION)?.byteSize ?? 0;

function recordWorktree(store: Store): void {
  store.handoffOwner(
    SESSION,
    'owner',
    'owner',
    size(store),
    0,
    '/home/a/r-s1',
    'hoplet/s1',
    '/home/a/r',
  );
}

describe('deletion preview', () => {
  it('reports the owner, the running check, the vendor file and the worktree', async () => {
    const { store, control, owner } = setup();
    store.upsertDevice('owner', 'Mac', '0.3.0');
    control.attach('owner', owner.link);
    answering(owner);
    recordWorktree(store);
    const out = await control.deletionPreview(SESSION);
    expect(out).toMatchObject({
      ok: true,
      preview: {
        owner: { deviceId: 'owner', name: 'Mac', online: true },
        running: false,
        vendorFile: { path: '/home/a/.claude/projects/p/s1.jsonl' },
        worktree: { branch: 'hoplet/s1', dirty: { modified: 1, untracked: 2 }, unpushed: 3 },
      },
    });
  });

  it('offers no worktree the core has no record of, and answers offline from the core', async () => {
    const { store, control, owner } = setup();
    control.attach('owner', owner.link);
    answering(owner);
    const noRecord = await control.deletionPreview(SESSION);
    expect(noRecord).toMatchObject({ ok: true, preview: { worktree: null } });
    owner.open = false;
    recordWorktree(store);
    expect(await control.deletionPreview(SESSION)).toMatchObject({
      ok: true,
      preview: { owner: { online: false }, vendorFile: null, worktree: null },
    });
    expect(await control.deletionPreview('nope')).toEqual({
      ok: false,
      error: 'session-not-found',
    });
  });

  it('names the previous owner after a handoff', async () => {
    const { store, control } = setup();
    store.upsertDevice('old', 'Old Box', '0.3.0');
    store.handoffOwner(SESSION, 'owner', 'new', size(store), 0, '/w', 'hoplet/s1', '/c');
    store.upsertDevice('new', 'New Box', '0.3.0');
    expect(await control.deletionPreview(SESSION)).toMatchObject({
      ok: true,
      preview: { owner: { deviceId: 'new' }, previousOwner: { deviceId: 'owner' } },
    });
  });
});

describe('deleting a session', () => {
  it('core only: tombstones, publishes, purges and asks the agent for nothing optional', async () => {
    const { dir, store, control, owner, published } = setup();
    control.attach('owner', owner.link);
    answering(owner, [{ step: 'stop-syncing', status: 'done' }]);
    const out = await control.deleteSession(SESSION, {
      vendorFile: false,
      worktree: false,
      worktreeDirty: false,
    });
    expect(out).toEqual({
      ok: true,
      steps: [
        { step: 'stop-syncing', status: 'done' },
        { step: 'core-copy', status: 'done' },
      ],
    });
    expect(owner.of('device.session.delete')[0]).toMatchObject({
      vendorFile: false,
      worktree: false,
      worktreeDirty: false,
    });
    expect(published).toContainEqual({
      v: PROTOCOL_VERSION,
      type: 'session.deleted',
      sessionId: SESSION,
    });
    expect(store.getSession(SESSION)).toBeNull();
    expect(store.listSessions()).toEqual([]);
    expect(store.isTombstoned(SESSION)).toBe(true);
    expect(store.tombstoneOf(SESSION)?.forgotten).toBe(true);
    expect(existsSync(path.join(dir, 'sessions', SESSION))).toBe(false);
    expect(
      await control.deleteSession(SESSION, {
        vendorFile: false,
        worktree: false,
        worktreeDirty: false,
      }),
    ).toEqual({
      ok: false,
      error: 'session-not-found',
    });
  });

  it('asks for the vendor file and the worktree only when ticked, and the core holds the record', async () => {
    const { store, control, owner } = setup();
    control.attach('owner', owner.link);
    answering(owner);
    recordWorktree(store);
    const out = await control.deleteSession(SESSION, {
      vendorFile: true,
      worktree: true,
      worktreeDirty: false,
    });
    expect(owner.of('device.session.delete')[0]).toMatchObject({
      vendorFile: true,
      worktree: true,
    });
    expect(out).toMatchObject({ ok: true });
    if (out.ok) expect(out.steps.at(-1)).toEqual({ step: 'core-copy', status: 'done' });
    expect(store.sessionWorktree(SESSION)).toBeNull();
  });

  it('passes worktreeDirty to the agent', async () => {
    const { store, control, owner } = setup();
    control.attach('owner', owner.link);
    answering(owner);
    recordWorktree(store);
    await control.deleteSession(SESSION, {
      vendorFile: false,
      worktree: true,
      worktreeDirty: true,
    });
    expect(owner.of('device.session.delete')[0]).toMatchObject({
      worktree: true,
      worktreeDirty: true,
    });
  });

  it('does not ask for a worktree the core never recorded', async () => {
    const { control, owner } = setup();
    control.attach('owner', owner.link);
    answering(owner);
    await control.deleteSession(SESSION, {
      vendorFile: false,
      worktree: true,
      worktreeDirty: false,
    });
    expect(owner.of('device.session.delete')[0]).toMatchObject({ worktree: false });
  });

  it('refuses a running session', async () => {
    const { store, control, owner } = setup();
    control.attach('owner', owner.link);
    answering(owner);
    owner.reply({
      type: 'runtime.state',
      sessionId: SESSION,
      state: 'running',
      capabilities: { permissions: true, interrupt: true, config: [] },
    });
    expect(
      await control.deleteSession(SESSION, {
        vendorFile: false,
        worktree: false,
        worktreeDirty: false,
      }),
    ).toEqual({
      ok: false,
      error: 'session-running',
    });
    expect(store.isTombstoned(SESSION)).toBe(false);
    expect(owner.of('device.session.delete')).toHaveLength(0);
  });

  it('with the owner offline deletes the core copy and forgets on reconnect', async () => {
    const { store, control, owner } = setup();
    expect(
      await control.deleteSession(SESSION, {
        vendorFile: true,
        worktree: false,
        worktreeDirty: false,
      }),
    ).toEqual({
      ok: false,
      error: 'owner-offline',
    });
    expect(store.isTombstoned(SESSION)).toBe(false);
    const out = await control.deleteSession(SESSION, {
      vendorFile: false,
      worktree: false,
      worktreeDirty: false,
    });
    expect(out).toMatchObject({
      ok: true,
      steps: [
        { step: 'stop-syncing', status: 'skipped', reason: 'owner-offline' },
        { step: 'core-copy', status: 'done' },
      ],
    });
    expect(store.tombstoneOf(SESSION)?.forgotten).toBe(false);
    answering(owner);
    control.attach('owner', owner.link);
    await new Promise((r) => setTimeout(r, 20));
    expect(owner.of('device.session.forget')).toHaveLength(1);
    expect(store.tombstoneOf(SESSION)?.forgotten).toBe(true);
    expect(store.unforgottenTombstones('owner')).toEqual([]);
  });

  it('keeps the core copy and the tombstone on a failed step, and a retry finishes', async () => {
    const { store, control, owner } = setup();
    control.attach('owner', owner.link);
    recordWorktree(store);
    answering(owner, [
      { step: 'stop-syncing', status: 'done' },
      { step: 'worktree', status: 'failed', reason: 'remove-failed' },
      { step: 'branch', status: 'skipped' },
      { step: 'vendor-file', status: 'skipped' },
    ]);
    const first = await control.deleteSession(SESSION, {
      vendorFile: true,
      worktree: true,
      worktreeDirty: false,
    });
    expect(first).toMatchObject({ ok: false, error: 'delete-partial' });
    if (!first.ok && first.error === 'delete-partial') {
      expect(first.steps.at(-1)).toEqual({ step: 'core-copy', status: 'skipped' });
      expect(first.steps).toContainEqual({
        step: 'worktree',
        status: 'failed',
        reason: 'remove-failed',
      });
    }
    expect(store.deletingOwner(SESSION)).not.toBeNull();
    expect(store.isTombstoned(SESSION)).toBe(true);
    expect(store.sessionWorktree(SESSION)).not.toBeNull();
    expect(store.listSessions()).toEqual([]);

    answering(owner);
    const retry = await control.deleteSession(SESSION, {
      vendorFile: true,
      worktree: true,
      worktreeDirty: false,
    });
    expect(retry).toMatchObject({ ok: true });
    expect(store.getSession(SESSION)).toBeNull();
    expect(store.sessionWorktree(SESSION)).toBeNull();
  });

  it('answers timeout when the agent stays silent and keeps the tombstone', async () => {
    const { store, control, owner } = setup({ workspaceTimeoutMs: 30 });
    control.attach('owner', owner.link);
    expect(
      await control.deleteSession(SESSION, {
        vendorFile: false,
        worktree: false,
        worktreeDirty: false,
      }),
    ).toEqual({
      ok: false,
      error: 'timeout',
    });
    expect(store.isTombstoned(SESSION)).toBe(true);
    expect(store.deletingOwner(SESSION)).not.toBeNull();
  });
});

describe('tombstones and import', () => {
  it('an import lifts the tombstone, and restores it when the agent refuses', async () => {
    const { store, control, owner } = setup();
    control.attach('owner', owner.link);
    store.tombstone(SESSION, 'owner');
    owner.behavior = (msg, d) => {
      if (msg.type === 'device.sessions.import') {
        d.reply({
          type: 'device.projects.rejected',
          requestId: (msg as { requestId: string }).requestId,
          reason: 'no',
        });
      }
    };
    const refused = await control.importSessions('owner', '/p', [SESSION]);
    expect(refused.ok).toBe(false);
    expect(store.isTombstoned(SESSION)).toBe(true);
    owner.behavior = (msg, d) => {
      if (msg.type === 'device.sessions.import') {
        d.reply({
          type: 'device.projects',
          requestId: (msg as { requestId: string }).requestId,
          candidates: [],
          projects: [],
        });
      }
    };
    const accepted = await control.importSessions('owner', '/p', [SESSION]);
    expect(accepted.ok).toBe(true);
    expect(store.isTombstoned(SESSION)).toBe(false);
  });

  it('hides a tombstoned session from lists and search', async () => {
    const { store } = setup();
    store.append(
      { sessionId: 's2', provider: 'claude', projectDir: 'p', filePath: '/y', deviceId: 'owner' },
      ['{"type":"user","message":{"role":"user","content":"needle"}}'],
    );
    expect(
      store
        .listSessions()
        .map((s) => s.sessionId)
        .sort(),
    ).toEqual(['s1', 's2']);
    store.tombstone('s2', 'owner');
    expect(store.listSessions().map((s) => s.sessionId)).toEqual(['s1']);
    const found = await store.searchAll('needle', 0, 10);
    expect(JSON.stringify(found)).not.toContain('s2');
  });

  it('purges the rows and directories of a session, and the startup finishes a queued purge', () => {
    const { dir, store } = setup();
    store.queueAdd(SESSION, { id: 'q1', text: 'later', attachments: [] });
    store.purgeSession(SESSION);
    expect(store.getSession(SESSION)).toBeNull();
    expect(store.queueOf(SESSION).items).toEqual([]);
    expect(existsSync(path.join(dir, 'sessions', SESSION))).toBe(false);
  });
});

describe('a handoff records the worktree', () => {
  it('stores clone, worktree, branch and the previous owner with the ownership change', () => {
    const { store } = setup();
    expect(
      store.handoffOwner(SESSION, 'owner', 'target', size(store), 0, '/w', 'hoplet/s1', '/c'),
    ).toBe(1);
    expect(store.sessionWorktree(SESSION)).toMatchObject({
      deviceId: 'target',
      fromDeviceId: 'owner',
      clone: '/c',
      worktree: '/w',
      branch: 'hoplet/s1',
    });
  });

  it('records nothing when the handoff created no worktree', () => {
    const { store } = setup();
    store.handoffOwner(SESSION, 'owner', 'target', size(store), 0, '/w', 'main', null);
    expect(store.sessionWorktree(SESSION)).toBeNull();
  });
});

describe('REST', () => {
  async function boot() {
    const ctx = setup();
    const server = createServer();
    createApi(ctx.store, { port: 0, dataDir: '', token: 'host', webDir: null }, ctx.control).attach(
      server,
    );
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const call = (method: string, p: string, body?: unknown) =>
      fetch(base + p, {
        method,
        headers: { authorization: 'Bearer host' },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    return { ...ctx, call };
  }

  it('previews and deletes', async () => {
    const { store, control, owner, call } = await boot();
    control.attach('owner', owner.link);
    answering(owner, [{ step: 'stop-syncing', status: 'done' }]);
    const preview = await call('GET', `/api/sessions/${SESSION}/deletion`);
    expect(preview.status).toBe(200);
    expect(await preview.json()).toMatchObject({ owner: { deviceId: 'owner', online: true } });
    const del = await call('DELETE', `/api/sessions/${SESSION}`, {
      vendorFile: false,
      worktree: false,
      worktreeDirty: false,
    });
    expect(del.status).toBe(200);
    expect(await del.json()).toEqual({
      steps: [
        { step: 'stop-syncing', status: 'done' },
        { step: 'core-copy', status: 'done' },
      ],
    });
    expect(store.getSession(SESSION)).toBeNull();
    expect(
      (
        await call('DELETE', `/api/sessions/${SESSION}`, {
          vendorFile: false,
          worktree: false,
          worktreeDirty: false,
        })
      ).status,
    ).toBe(404);
  });

  it('answers 409 session-running, 409 owner-offline and 502 delete-partial with the steps', async () => {
    const { control, owner, call } = await boot();
    const body = { vendorFile: true, worktree: false, worktreeDirty: false };
    let res = await call('DELETE', `/api/sessions/${SESSION}`, body);
    expect(res.status).toBe(409);
    expect(await res.json()).toMatchObject({ code: 'owner-offline' });
    control.attach('owner', owner.link);
    answering(owner, [
      { step: 'stop-syncing', status: 'done' },
      { step: 'vendor-file', status: 'failed', reason: 'remove-failed' },
    ]);
    res = await call('DELETE', `/api/sessions/${SESSION}`, body);
    expect(res.status).toBe(502);
    const failed = (await res.json()) as { code: string; steps: unknown[] };
    expect(failed.code).toBe('delete-partial');
    expect(failed.steps).toContainEqual({
      step: 'vendor-file',
      status: 'failed',
      reason: 'remove-failed',
    });
    expect((await call('DELETE', `/api/sessions/${SESSION}`, { vendorFile: 'x' })).status).toBe(
      400,
    );
    owner.reply({
      type: 'runtime.state',
      sessionId: 's9',
      state: 'running',
      capabilities: { permissions: true, interrupt: true, config: [] },
    });
  });

  it('answers 409 session-running for a running session', async () => {
    const { control, owner, call } = await boot();
    control.attach('owner', owner.link);
    owner.reply({
      type: 'runtime.state',
      sessionId: SESSION,
      state: 'running',
      capabilities: { permissions: true, interrupt: true, config: [] },
    });
    const res = await call('DELETE', `/api/sessions/${SESSION}`, {
      vendorFile: false,
      worktree: false,
      worktreeDirty: false,
    });
    expect(res.status).toBe(409);
    expect(await res.json()).toMatchObject({ code: 'session-running' });
  });
});

describe('ingest', () => {
  it('rejects an append of a tombstoned session with reason deleted', async () => {
    const { store, control } = setup();
    store.tombstone(SESSION, 'owner');
    const server = createServer();
    attachIngest(server, store, control);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    const token = store.mintDevice('owner', 'owner').token;
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${token}` },
    });
    const next = (until = (_: Record<string, unknown>) => true) =>
      new Promise<Record<string, unknown>>((resolve, reject) => {
        const on = (raw: unknown) => {
          const msg = JSON.parse(String(raw)) as Record<string, unknown>;
          if (!until(msg)) return;
          ws.off('message', on);
          resolve(msg);
        };
        ws.on('message', on);
        ws.once('error', reject);
      });
    await next();
    try {
      ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'device.hello',
          deviceId: 'owner',
          name: 'owner',
          version: 'synthetic',
        }),
      );
      const reply = next((m) => m.type === 'session.rejected');
      ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'session.append',
          sessionId: SESSION,
          provider: 'claude',
          projectDir: 'p',
          filePath: '/x',
          byteStart: 0,
          encoding: 'plain',
          lines: '{"a":1}\n',
        }),
      );
      expect(await reply).toMatchObject({
        type: 'session.rejected',
        sessionId: SESSION,
        reason: 'deleted',
      });
    } finally {
      ws.terminate();
    }
  });
});
