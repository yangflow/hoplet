import { createHash } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { gunzipSync } from 'node:zlib';
import {
  type HandoffData,
  type HandoffProgress,
  PROTOCOL_VERSION,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import {
  type Control,
  type ControlOptions,
  type ControlReply,
  createControl,
  type DeviceLink,
} from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const SESSION = 's1';
const COMMIT = 'a'.repeat(40);
const TREE = 'b'.repeat(40);

const line = (i: number): string =>
  JSON.stringify({
    type: 'user',
    cwd: '/tmp/proj',
    message: { role: 'user', content: `line ${i}` },
  });

/** A gzip-looking archive: the core never opens it. */
function archive(size: number): Buffer {
  const bytes = Buffer.alloc(size, 9);
  bytes[0] = 0x1f;
  bytes[1] = 0x8b;
  return bytes;
}

type Behavior = (msg: ServerMessage, device: Device) => void;

/** An agent as the core sees it: a link that records what it is sent and answers through a script. */
class Device {
  readonly id: string;
  readonly sent: ServerMessage[] = [];
  open = true;
  readonly link: DeviceLink;
  behavior: Behavior = () => {};
  private readonly control: Control & { receive(link: DeviceLink, msg: ControlReply): void };
  constructor(
    id: string,
    control: Control & { receive(link: DeviceLink, msg: ControlReply): void },
  ) {
    this.id = id;
    this.control = control;
    this.link = {
      send: (msg) => {
        this.sent.push(msg);
        queueMicrotask(() => this.behavior(msg, this));
      },
      sendAndWait: async (msg) => {
        if (!this.open) throw new Error('closed');
        this.sent.push(msg);
        queueMicrotask(() => this.behavior(msg, this));
      },
      isOpen: () => this.open,
    };
  }
  reply(msg: Record<string, unknown>): void {
    this.control.receive(this.link, { v: PROTOCOL_VERSION, ...msg } as ControlReply);
  }
  types(): string[] {
    return this.sent.map((m) => m.type);
  }
  of<T extends ServerMessage['type']>(type: T): Extract<ServerMessage, { type: T }>[] {
    return this.sent.filter((m): m is Extract<ServerMessage, { type: T }> => m.type === type);
  }
}

interface Fixture {
  store: Store;
  control: ReturnType<typeof createControl>;
  owner: Device;
  target: Device;
  /** The steps reported to browsers, one entry per run of the same step. */
  progress: string[];
  messages: HandoffProgress[];
}

/** An owner that answers prepare, and a target that installs once it has the last chunk and verifies truthfully. */
async function fixture(options: ControlOptions = {}): Promise<Fixture> {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-handoff-'));
  dirs.push(dir);
  const store = new Store(dir);
  const control = createControl(store, {
    handoffTimeoutMs: 300,
    transferTimeoutMs: 3000,
    ...options,
  });
  store.append(
    { sessionId: SESSION, provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'owner' },
    [line(0), line(1), line(2)],
  );
  uploadSnapshot(store, 'snap1', archive(2_500_000));
  const owner = new Device('owner', control);
  const target = new Device('target', control);
  control.attach('owner', owner.link);
  control.attach('target', target.link);
  const progress: string[] = [];
  const messages: HandoffProgress[] = [];
  control.subscribe((msg) => {
    if (msg.type !== 'handoff.progress') return;
    messages.push(msg);
    if (progress.at(-1) !== msg.step) progress.push(msg.step);
  });

  owner.behavior = (msg, d) => {
    if (msg.type === 'handoff.prepare') {
      d.reply({
        type: 'handoff.prepared',
        sessionId: SESSION,
        handoffId: msg.handoffId,
        snapshotId: store.latestSnapshot(SESSION)?.snapshotId ?? '',
        byteSize: store.getSession(SESSION)?.byteSize ?? 0,
      });
    }
  };
  target.behavior = (msg, d) => {
    if (msg.type === 'handoff.data' && msg.part === 'session' && msg.last) {
      d.reply({
        type: 'handoff.installed',
        sessionId: SESSION,
        handoffId: msg.handoffId,
        worktree: '/home/b/r-sess1234',
        branch: 'hoplet/sess1234',
        clone: '/home/b/r',
      });
    }
    if (msg.type === 'handoff.verify') {
      void (async () => {
        const fingerprint = await store.streamFingerprint(SESSION);
        d.reply({
          type: 'handoff.verified',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          sessionBytes: fingerprint?.byteSize ?? 0,
          sessionSha256: fingerprint?.streamSha256 ?? '',
          treeHash: TREE,
        });
      })();
    }
  };
  return { store, control, owner, target, progress, messages };
}

function uploadSnapshot(store: Store, snapshotId: string, bytes: Buffer): void {
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  const piece = 1024 * 1024;
  for (let offset = 0, seq = 0; offset < bytes.length; offset += piece, seq++) {
    store.putSnapshotChunk({
      sessionId: SESSION,
      snapshotId,
      seq,
      last: offset + piece >= bytes.length,
      data: bytes.subarray(offset, offset + piece).toString('base64'),
      createdAt: 1_700_000_000_000,
      baseCommit: COMMIT,
      originUrl: 'git@example.test:o/r.git',
      treeHash: TREE,
      byteSize: bytes.length,
      sha256,
    });
  }
}

/** What the session and ownership look like; a failed handoff must leave it unchanged. */
function state(store: Store) {
  const meta = store.getSession(SESSION);
  return { owner: meta?.ownerDeviceId, epoch: meta?.ownerEpoch, cursor: meta?.byteSize };
}

describe('a handoff with the owner online', () => {
  it('prepares, transfers, installs, verifies and commits, then moves ownership', async () => {
    const f = await fixture();
    const before = state(f.store);
    const result = await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    expect(result).toEqual({
      ok: true,
      ownerDeviceId: 'target',
      ownerEpoch: 1,
      worktree: '/home/b/r-sess1234',
      branch: 'hoplet/sess1234',
    });
    expect(state(f.store)).toEqual({ owner: 'target', epoch: 1, cursor: before.cursor });
    // The session now sits under the worktree on the new owner, not the directory it was started in.
    expect(f.store.getSession(SESSION)?.projectPath).toBe('/home/b/r-sess1234');

    expect(f.owner.types()).toEqual(['handoff.prepare', 'handoff.commit']);
    expect(f.target.types().filter((t) => t !== 'handoff.data')).toEqual([
      'handoff.install',
      'handoff.verify',
      'handoff.commit',
    ]);
    // The install message comes first and says what the received bytes are held to.
    expect(f.target.sent[0]?.type).toBe('handoff.install');
    const install = f.target.of('handoff.install')[0];
    const snapshot = f.store.latestSnapshot(SESSION);
    expect(install).toMatchObject({
      sessionId: SESSION,
      snapshotBytes: snapshot?.byteSize,
      snapshotSha256: snapshot?.sha256,
      sessionBytes: before.cursor,
    });
    const fingerprint = await f.store.streamFingerprint(SESSION);
    expect(install?.sessionSha256).toBe(fingerprint?.streamSha256);

    // The chunks reassemble to the snapshot and to the session stream, in order, last chunk marked.
    const data = f.target.of('handoff.data');
    const part = (name: string): HandoffData[] => data.filter((d) => d.part === name);
    for (const name of ['snapshot', 'session']) {
      const chunks = part(name);
      expect(chunks.map((c) => c.seq)).toEqual(chunks.map((_, i) => i));
      expect(chunks.map((c) => c.last)).toEqual(chunks.map((_, i) => i === chunks.length - 1));
    }
    const decode = (c: HandoffData) => {
      const raw = Buffer.from(c.data, 'base64');
      return c.encoding === 'gzip+base64' ? gunzipSync(raw) : raw;
    };
    expect(
      createHash('sha256')
        .update(Buffer.concat(part('snapshot').map(decode)))
        .digest('hex'),
    ).toBe(snapshot?.sha256);
    expect(part('snapshot')).toHaveLength(3);
    expect(Buffer.concat(part('session').map(decode)).length).toBe(before.cursor);
    expect(data.indexOf(part('session')[0] as HandoffData)).toBeGreaterThan(
      data.indexOf(part('snapshot').at(-1) as HandoffData),
    );

    // Both sides are told who owns the session now, at which epoch and cursor.
    for (const device of [f.owner, f.target]) {
      expect(device.of('handoff.commit')[0]).toMatchObject({
        sessionId: SESSION,
        ownerDeviceId: 'target',
        ownerEpoch: 1,
        byteSize: before.cursor,
      });
    }
    expect(f.progress).toEqual(['prepare', 'transfer', 'install', 'verify', 'commit', 'done']);

    // Transfer progress counts up to the archive and the stream; done names the new worktree.
    const transfer = f.messages.filter((m) => m.step === 'transfer');
    const total = 2_500_000 + (before.cursor ?? 0);
    expect(transfer[0]).toMatchObject({ sent: 0, total });
    expect(transfer.at(-1)).toMatchObject({ sent: total, total });
    const sent = transfer.map((m) => m.sent ?? -1);
    expect(sent).toEqual([...sent].sort((a, b) => a - b));
    expect(f.messages.at(-1)).toMatchObject({
      step: 'done',
      worktree: '/home/b/r-sess1234',
      branch: 'hoplet/sess1234',
    });
  });

  it('rejects the old owner’s appends after the commit', async () => {
    const f = await fixture();
    await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    expect(() =>
      f.store.append(
        {
          sessionId: SESSION,
          provider: 'claude',
          projectDir: 'p',
          filePath: '/x',
          deviceId: 'owner',
        },
        [line(9)],
      ),
    ).toThrow();
    expect(() =>
      f.store.append(
        {
          sessionId: SESSION,
          provider: 'claude',
          projectDir: 'p',
          filePath: '/x',
          deviceId: 'target',
        },
        [line(9)],
      ),
    ).not.toThrow();
  });

  it('passes the target path to the target', async () => {
    const f = await fixture();
    await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      targetPath: '/work/repo',
    });
    expect(f.target.of('handoff.install')[0]?.targetPath).toBe('/work/repo');
  });

  it('refuses prompts while it runs', async () => {
    const f = await fixture();
    f.target.behavior = () => {};
    const running = f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    await new Promise((r) => setTimeout(r, 50));
    expect(await f.control.prompt(SESSION, 'owner', 'hi', 'p1')).toMatchObject({
      ok: false,
      error: 'busy',
    });
    await running;
  });
});

describe('a handoff with the owner offline', () => {
  it('uses the stored snapshot and reports its time', async () => {
    const f = await fixture();
    f.control.detach(f.owner.link);
    const result = await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    expect(result).toEqual({
      ok: true,
      ownerDeviceId: 'target',
      ownerEpoch: 1,
      worktree: '/home/b/r-sess1234',
      branch: 'hoplet/sess1234',
      snapshotAt: 1_700_000_000_000,
    });
    expect(f.owner.types()).toEqual([]);
    expect(f.progress).toContain('prepare');
  });

  it('fails without a stored snapshot', async () => {
    const f = await fixture();
    f.control.detach(f.owner.link);
    // A session of its own, without any snapshot.
    f.store.append(
      { sessionId: 's2', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'owner' },
      [line(0)],
    );
    expect(await f.control.handoff({ sessionId: 's2', targetDeviceId: 'target' })).toMatchObject({
      ok: false,
      error: 'no-snapshot',
    });
    expect(f.store.ownerDeviceId('s2')).toBe('owner');
  });
});

describe('refusals', () => {
  it('names the reason and sends nothing', async () => {
    const f = await fixture();
    const ask = (over: Partial<{ sessionId: string; targetDeviceId: string }> = {}) =>
      f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target', ...over });
    expect(await ask({ sessionId: 'missing' })).toMatchObject({
      ok: false,
      error: 'session-not-found',
    });
    expect(await ask({ sessionId: '../x' })).toMatchObject({
      ok: false,
      error: 'rejected',
      reason: 'invalid-session-id',
    });
    expect(await ask({ targetDeviceId: 'owner' })).toMatchObject({
      ok: false,
      error: 'same-device',
    });
    expect(await ask({ targetDeviceId: 'nobody' })).toMatchObject({
      ok: false,
      error: 'target-offline',
    });
    f.target.open = false;
    expect(await ask()).toMatchObject({ ok: false, error: 'target-offline' });
    f.target.open = true;
    for (const state of ['running', 'waiting-permission']) {
      f.owner.reply({
        type: 'runtime.state',
        sessionId: SESSION,
        state,
        capabilities: {
          permissions: true,
          interrupt: true,
          config: [],
        },
      });
      expect(await ask()).toMatchObject({ ok: false, error: 'busy' });
    }
    expect(f.owner.types()).toEqual([]);
    expect(f.target.types()).toEqual([]);
  });

  it('refuses a second handoff of a session that is already moving', async () => {
    const f = await fixture();
    f.target.behavior = () => {};
    const first = f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    await new Promise((r) => setTimeout(r, 30));
    expect(await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' })).toMatchObject(
      {
        ok: false,
        error: 'handoff-in-progress',
      },
    );
    expect((await first).ok).toBe(false);
  });
});

describe('failures leave owner and cursor unchanged and abort the target', () => {
  async function fails(f: Fixture, error: string, reason?: string) {
    const before = state(f.store);
    const result = await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    expect(result).toMatchObject({ ok: false, error, ...(reason ? { reason } : {}) });
    expect(state(f.store)).toEqual(before);
    expect(f.progress.at(-1)).toBe('failed');
    expect(f.target.of('handoff.commit')).toEqual([]);
    expect(f.owner.of('handoff.commit')).toEqual([]);
    return before;
  }

  it('when the owner refuses to prepare', async () => {
    const f = await fixture();
    f.owner.behavior = (msg, d) => {
      if (msg.type === 'handoff.prepare') {
        d.reply({
          type: 'handoff.failed',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          step: 'prepare',
          reason: 'not-a-git-repository',
        });
      }
    };
    await fails(f, 'rejected', 'not-a-git-repository');
    expect(f.target.types()).toEqual(['handoff.abort']);
  });

  it('when the owner does not answer', async () => {
    const f = await fixture();
    f.owner.behavior = () => {};
    await fails(f, 'timeout');
  });

  it('when the final append or the snapshot never arrives', async () => {
    const f = await fixture();
    f.owner.behavior = (msg, d) => {
      if (msg.type === 'handoff.prepare') {
        d.reply({
          type: 'handoff.prepared',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          snapshotId: 'never',
          byteSize: 99999,
        });
      }
    };
    await fails(f, 'timeout');
  });

  it('when the target says it has no clone', async () => {
    const f = await fixture();
    f.target.behavior = (msg, d) => {
      if (msg.type === 'handoff.install') {
        d.reply({
          type: 'handoff.failed',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          step: 'install',
          reason: 'no-clone',
        });
      }
    };
    await fails(f, 'no-clone');
    expect(f.target.of('handoff.abort')).toHaveLength(1);
    // The transfer stops once the target has refused: not every chunk was sent.
    expect(f.target.of('handoff.data').length).toBeLessThan(10);
  });

  it('when the target fails to install for another reason', async () => {
    const f = await fixture();
    f.target.behavior = (msg, d) => {
      if (msg.type === 'handoff.data' && msg.part === 'session' && msg.last) {
        d.reply({
          type: 'handoff.failed',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          step: 'install',
          reason: 'snapshot-corrupt',
        });
      }
    };
    await fails(f, 'rejected', 'snapshot-corrupt');
  });

  it('when the target does not install in time', async () => {
    const f = await fixture();
    f.target.behavior = () => {};
    await fails(f, 'timeout');
    expect(f.target.of('handoff.abort')).toHaveLength(1);
  });

  it('when the target’s copy of the session differs', async () => {
    const f = await fixture();
    const honest = f.target.behavior;
    f.target.behavior = (msg, d) => {
      if (msg.type === 'handoff.verify') {
        d.reply({
          type: 'handoff.verified',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          sessionBytes: 1,
          sessionSha256: '0'.repeat(64),
          treeHash: TREE,
        });
        return;
      }
      honest(msg, d);
    };
    await fails(f, 'verify-failed');
  });

  it('when the target’s copy of the size, the checksum or the tree differs', async () => {
    for (const lie of ['size', 'checksum', 'tree'] as const) {
      const f = await fixture();
      const honest = f.target.behavior;
      f.target.behavior = (msg, d) => {
        if (msg.type !== 'handoff.verify') {
          honest(msg, d);
          return;
        }
        void (async () => {
          const fp = await f.store.streamFingerprint(SESSION);
          d.reply({
            type: 'handoff.verified',
            sessionId: SESSION,
            handoffId: msg.handoffId,
            sessionBytes: (fp?.byteSize ?? 0) + (lie === 'size' ? 1 : 0),
            sessionSha256: lie === 'checksum' ? '1'.repeat(64) : (fp?.streamSha256 ?? ''),
            treeHash: lie === 'tree' ? 'c'.repeat(40) : TREE,
          });
        })();
      };
      await fails(f, 'verify-failed');
    }
  });

  it('when the target leaves during the transfer', async () => {
    const f = await fixture();
    f.target.behavior = (msg) => {
      if (msg.type === 'handoff.install') {
        f.target.open = false;
        f.control.detach(f.target.link);
      }
    };
    await fails(f, 'target-offline');
  });

  it('when the target leaves while it verifies', async () => {
    const f = await fixture();
    const honest = f.target.behavior;
    f.target.behavior = (msg, d) => {
      if (msg.type === 'handoff.verify') {
        f.target.open = false;
        f.control.detach(f.target.link);
        return;
      }
      honest(msg, d);
    };
    await fails(f, 'target-offline');
  });

  it('when the owner leaves while it prepares', async () => {
    const f = await fixture();
    f.owner.behavior = (msg) => {
      if (msg.type === 'handoff.prepare') {
        f.owner.open = false;
        f.control.detach(f.owner.link);
      }
    };
    await fails(f, 'owner-offline');
  });

  it('when the session grows after the target proved its copy', async () => {
    const f = await fixture();
    const honest = f.target.behavior;
    f.target.behavior = (msg, d) => {
      if (msg.type === 'handoff.verify') {
        void (async () => {
          const fp = await f.store.streamFingerprint(SESSION);
          // The owner appends before the verification reaches the core.
          f.store.append(
            {
              sessionId: SESSION,
              provider: 'claude',
              projectDir: 'p',
              filePath: '/x',
              deviceId: 'owner',
            },
            [line(7)],
          );
          d.reply({
            type: 'handoff.verified',
            sessionId: SESSION,
            handoffId: msg.handoffId,
            sessionBytes: fp?.byteSize ?? 0,
            sessionSha256: fp?.streamSha256 ?? '',
            treeHash: TREE,
          });
        })();
        return;
      }
      honest(msg, d);
    };
    const before = state(f.store);
    const result = await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    expect(result).toMatchObject({ ok: false, error: 'session-changed' });
    const after = state(f.store);
    expect(after.owner).toBe('owner');
    expect(after.epoch).toBe(0);
    expect(after.cursor).toBeGreaterThan(before.cursor ?? 0);
    expect(f.target.of('handoff.abort')).toHaveLength(1);
  });

  it('ignores answers from a device that is not the one asked', async () => {
    const f = await fixture();
    const stranger = new Device('stranger', f.control);
    f.control.attach('stranger', stranger.link);
    f.owner.behavior = (msg) => {
      if (msg.type === 'handoff.prepare') {
        // Someone else answers for the owner.
        stranger.reply({
          type: 'handoff.prepared',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          snapshotId: 'snap1',
          byteSize: f.store.getSession(SESSION)?.byteSize ?? 0,
        });
      }
    };
    await fails(f, 'timeout');
  });

  it('can be retried after a failure', async () => {
    const f = await fixture();
    const honest = f.target.behavior;
    f.target.behavior = () => {};
    await fails(f, 'timeout');
    f.target.behavior = honest;
    expect(await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' })).toMatchObject(
      { ok: true, ownerEpoch: 1 },
    );
  });
});

describe('a handoff that clones the repository first (ADR 0024)', () => {
  const INTO = '/home/b/work/r';
  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

  /** A target that answers `device.clone.start` through `onStart`, and installs as the fixture does. */
  async function cloning(
    options: ControlOptions,
    onStart: (msg: Extract<ServerMessage, { type: 'device.clone.start' }>, d: Device) => void,
  ): Promise<Fixture> {
    const f = await fixture(options);
    const installs = f.target.behavior;
    f.target.behavior = (msg, d) => {
      if (msg.type === 'device.clone.start') onStart(msg, d);
      else installs(msg, d);
    };
    return f;
  }
  const done = (msg: { requestId: string }, d: Device, path = INTO) =>
    d.reply({ type: 'device.clone.done', requestId: msg.requestId, path });
  const failed = (msg: { requestId: string }, d: Device, reason: string, detail?: string) =>
    d.reply({
      type: 'device.clone.failed',
      requestId: msg.requestId,
      reason,
      ...(detail !== undefined ? { detail } : {}),
    });

  it('clones first, then prepares, and installs without a target path', async () => {
    const f = await cloning({}, done);
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      cloneInto: INTO,
    });
    expect(result).toMatchObject({ ok: true, ownerDeviceId: 'target' });
    const start = f.target.of('device.clone.start')[0];
    expect(start).toMatchObject({
      origin: 'git@example.test:o/r.git',
      cloneInto: INTO,
    });
    expect(start?.handoffId).toBe(f.target.of('handoff.install')[0]?.handoffId);
    // The clone is a watched project once done, so install finds it by origin.
    expect(f.target.of('handoff.install')[0]?.targetPath).toBeUndefined();
    expect(f.target.types()[0]).toBe('device.clone.start');
    expect(f.progress).toEqual([
      'clone',
      'prepare',
      'transfer',
      'install',
      'verify',
      'commit',
      'done',
    ]);
    // The owner was untouched until the clone was done.
    expect(f.owner.types()[0]).toBe('handoff.prepare');
  });

  it('forwards the progress of the clone to browsers', async () => {
    const f = await cloning({}, (msg, d) => {
      d.reply({ type: 'device.clone.progress', requestId: msg.requestId, received: 100 });
      d.reply({
        type: 'device.clone.progress',
        requestId: msg.requestId,
        received: 900,
        total: 1000,
      });
      done(msg, d);
    });
    await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target', cloneInto: INTO });
    const clone = f.messages.filter((m) => m.step === 'clone');
    expect(clone.map((m) => m.sent)).toEqual([undefined, 100, 900]);
    expect(clone.at(-1)).toMatchObject({ sent: 900, total: 1000, targetDeviceId: 'target' });
  });

  it('takes no clone step without cloneInto', async () => {
    const f = await fixture();
    await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target' });
    expect(f.target.of('device.clone.start')).toEqual([]);
    expect(f.progress[0]).toBe('prepare');
  });

  it('is not bound by the 120 s of the handoff request', async () => {
    // handoffTimeoutMs is 300 ms in the fixture; the clone takes longer and still succeeds.
    const f = await cloning({}, (msg, d) => {
      setTimeout(() => done(msg, d), 450);
    });
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      cloneInto: INTO,
    });
    expect(result.ok).toBe(true);
  });

  it.each([['no-access'], ['no-space'], ['destination-taken'], ['git-failed']])(
    'fails as clone-failed (%s) and leaves the owner running',
    async (reason) => {
      const f = await cloning({}, (msg, d) => failed(msg, d, reason));
      const before = state(f.store);
      const result = await f.control.handoff({
        sessionId: SESSION,
        targetDeviceId: 'target',
        cloneInto: INTO,
      });
      expect(result).toEqual({ ok: false, error: 'clone-failed', reason });
      expect(state(f.store)).toEqual(before);
      // The owner never stopped, and nothing was installed, so nothing is aborted.
      expect(f.owner.sent).toEqual([]);
      expect(f.target.types()).toEqual(['device.clone.start']);
      expect(f.progress).toEqual(['clone', 'failed']);
    },
  );

  it('carries the stripped git text of a git-failed clone', async () => {
    const f = await cloning({}, (msg, d) => failed(msg, d, 'git-failed', 'fatal: repository gone'));
    expect(
      await f.control.handoff({ sessionId: SESSION, targetDeviceId: 'target', cloneInto: INTO }),
    ).toEqual({
      ok: false,
      error: 'clone-failed',
      reason: 'git-failed',
      detail: 'fatal: repository gone',
    });
  });

  it('fails as clone-failed timeout when the clone outlives its total limit, and tells the agent', async () => {
    const f = await cloning({ cloneTimeoutMs: 120, cloneStallMs: 5_000 }, (msg, d) => {
      const tick = setInterval(
        () => d.reply({ type: 'device.clone.progress', requestId: msg.requestId, received: 1 }),
        30,
      );
      setTimeout(() => clearInterval(tick), 400);
    });
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      cloneInto: INTO,
    });
    expect(result).toEqual({ ok: false, error: 'clone-failed', reason: 'timeout' });
    expect(f.target.of('device.clone.cancel')).toHaveLength(1);
    expect(f.owner.sent).toEqual([]);
  });

  it('fails as timeout when git stalls, and progress restarts the stall timer', async () => {
    const f = await cloning({ cloneStallMs: 120 }, (msg, d) => {
      // Four progress lines 60 ms apart keep a 120 ms stall timer alive; then silence.
      for (let i = 1; i <= 4; i++) {
        setTimeout(
          () => d.reply({ type: 'device.clone.progress', requestId: msg.requestId, received: i }),
          i * 60,
        );
      }
    });
    const started = Date.now();
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      cloneInto: INTO,
    });
    expect(result).toEqual({ ok: false, error: 'clone-failed', reason: 'timeout' });
    // Later than the first stall window: the progress lines restarted the timer.
    expect(Date.now() - started).toBeGreaterThanOrEqual(300);
    expect(f.target.of('device.clone.cancel')).toHaveLength(1);
  });

  it('fails as target-offline when the target disconnects while cloning', async () => {
    const f = await cloning({}, () => {});
    const running = f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      cloneInto: INTO,
    });
    await wait(30);
    f.target.open = false;
    f.control.detach(f.target.link);
    expect(await running).toMatchObject({ ok: false, error: 'target-offline' });
    expect(f.owner.sent).toEqual([]);
  });

  it('ignores clone messages from a device that was not asked', async () => {
    const f = await cloning({ cloneTimeoutMs: 150 }, () => {});
    const running = f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      cloneInto: INTO,
    });
    await wait(30);
    const requestId = f.target.of('device.clone.start')[0]?.requestId ?? '';
    f.owner.reply({ type: 'device.clone.done', requestId, path: INTO });
    expect(await running).toEqual({ ok: false, error: 'clone-failed', reason: 'timeout' });
  });

  it('fails as no-clone when the origin is unknown', async () => {
    const f = await cloning({}, done);
    // Another session without a snapshot and with the owner offline: no origin anywhere.
    f.store.append(
      { sessionId: 's2', provider: 'claude', projectDir: 'p', filePath: '/y', deviceId: 'owner' },
      [line(0)],
    );
    f.control.detach(f.owner.link);
    expect(
      await f.control.handoff({ sessionId: 's2', targetDeviceId: 'target', cloneInto: INTO }),
    ).toMatchObject({ ok: false, error: 'no-clone' });
    expect(f.target.of('device.clone.start')).toEqual([]);
  });

  describe('cancel', () => {
    it('stops the clone, fails the handoff as cancelled and never stops the owner', async () => {
      const f = await cloning({}, () => {});
      const running = f.control.handoff({
        sessionId: SESSION,
        targetDeviceId: 'target',
        cloneInto: INTO,
      });
      await wait(30);
      expect(f.control.cancelHandoff(SESSION)).toEqual({ ok: true });
      const cancel = f.target.of('device.clone.cancel')[0];
      expect(cancel?.requestId).toBe(f.target.of('device.clone.start')[0]?.requestId);
      // The agent answers a cancel with a failed message once the directory is gone.
      f.target.reply({
        type: 'device.clone.failed',
        requestId: cancel?.requestId,
        reason: 'cancelled',
      });
      expect(await running).toMatchObject({ ok: false, error: 'cancelled' });
      expect(f.owner.sent).toEqual([]);
      expect(state(f.store).owner).toBe('owner');
    });

    it('is idempotent, and sends one cancel', async () => {
      const f = await cloning({}, () => {});
      const running = f.control.handoff({
        sessionId: SESSION,
        targetDeviceId: 'target',
        cloneInto: INTO,
      });
      await wait(30);
      f.control.cancelHandoff(SESSION);
      f.control.cancelHandoff(SESSION);
      expect(f.target.of('device.clone.cancel')).toHaveLength(1);
      f.target.reply({
        type: 'device.clone.failed',
        requestId: f.target.of('device.clone.start')[0]?.requestId,
        reason: 'cancelled',
      });
      await running;
    });

    it('answers handoff-not-running when nothing is cloning', async () => {
      const f = await fixture();
      expect(f.control.cancelHandoff(SESSION)).toEqual({
        ok: false,
        error: 'handoff-not-running',
      });
    });

    it('answers handoff-not-running after the clone step', async () => {
      const f = await cloning({}, done);
      // Hold the owner's prepare so the handoff sits in the prepare step.
      f.owner.behavior = () => {};
      const running = f.control.handoff({
        sessionId: SESSION,
        targetDeviceId: 'target',
        cloneInto: INTO,
      });
      await wait(60);
      expect(f.control.cancelHandoff(SESSION)).toEqual({
        ok: false,
        error: 'handoff-not-running',
      });
      f.control.detach(f.owner.link);
      await running;
    });

    it('fails as cancelled even when the clone finishes in the same moment', async () => {
      const f = await cloning({}, () => {});
      const running = f.control.handoff({
        sessionId: SESSION,
        targetDeviceId: 'target',
        cloneInto: INTO,
      });
      await wait(30);
      f.control.cancelHandoff(SESSION);
      f.target.reply({
        type: 'device.clone.done',
        requestId: f.target.of('device.clone.start')[0]?.requestId,
        path: INTO,
      });
      expect(await running).toMatchObject({ ok: false, error: 'cancelled' });
      expect(f.owner.sent).toEqual([]);
    });
  });
});
