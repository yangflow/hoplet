// End to end handoff (docs/spec.md#handoff): two real Go agents with their own state roots and clones of a
// local bare `origin`, an in-process core and the scripted fake `claude`. A failure at any step leaves the
// owner, the cursor and the files of both machines as they were.
import { createHash } from 'node:crypto';
import {
  chmodSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  readlinkSync,
  realpathSync,
  renameSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { gunzipSync, gzipSync } from 'node:zlib';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import type { HandoffProgress, ServerMessage } from '../../shared/src/index.ts';
import {
  type Binaries,
  buildBinaries,
  type Core,
  claudeDirName,
  type Intercept,
  run,
  sessionLine,
  startCore,
  TestAgent,
  until,
} from './support/e2e.ts';

vi.setConfig({ testTimeout: 120_000, maxConcurrency: 4 });

const HOST = 'handoff-e2e-host-token';
const SESSION = '33333333-4444-4555-8666-777777777701';
const SHORT = SESSION.slice(0, 8);

let binaries: Binaries;
let buildRoot = '';
const worlds: World[] = [];

beforeAll(() => {
  buildRoot = mkdtempSync(join(tmpdir(), 'hoplet-handoff-build-'));
  binaries = buildBinaries(buildRoot);
}, 180_000);

afterAll(async () => {
  for (const w of worlds.splice(0)) await w.close();
  rmSync(buildRoot, { recursive: true, force: true });
});

interface Machine {
  id: string;
  agent: TestAgent;
  clone: string;
  /** The Claude store directory name of the clone. */
  sessionFile: string;
}

interface World {
  root: string;
  core: Core;
  origin: string;
  a: Machine;
  b: Machine;
  progress: HandoffProgress[];
  /** What a test changes in the traffic between the core and the agents; empty means nothing. */
  fault: Intercept;
  close(): Promise<void>;
}

interface WorldOptions {
  /** How long a handoff step waits for an agent; the default leaves room for a slow disk. */
  stepTimeoutMs?: number;
  /** Leave machine B out of the world (no clone, not started). */
  onlyA?: boolean;
  /** Give B no clone of the repository: it watches an unrelated directory. */
  bWithoutClone?: boolean;
  /** Both agents use one Claude Code store and record every start of `claude` in one log, as two agent
   * configurations on one machine do. */
  sharedStore?: boolean;
}

function commit(dir: string, message: string): void {
  run(dir, 'git', ['add', '-A']);
  run(dir, 'git', ['commit', '-m', message]);
}

/** A bare origin with one commit, and two machines with a clone each. */
async function world(options: WorldOptions = {}): Promise<World> {
  const root = mkdtempSync(join(tmpdir(), 'hoplet-handoff-e2e-'));
  const origin = join(root, 'origin.git');
  run(root, 'git', ['init', '--bare', '-b', 'main', origin]);
  const seed = join(root, 'seed');
  run(root, 'git', ['clone', origin, seed]);
  writeFileSync(join(seed, 'README.md'), 'hello\n');
  mkdirSync(join(seed, 'src'));
  writeFileSync(join(seed, 'src', 'main.txt'), 'one\ntwo\nthree\n');
  writeFileSync(join(seed, 'tool.sh'), '#!/bin/sh\necho tool\n');
  chmodSync(join(seed, 'tool.sh'), 0o755);
  writeFileSync(
    join(seed, 'data.bin'),
    Buffer.from(Array.from({ length: 3000 }, (_, i) => i % 251)),
  );
  writeFileSync(join(seed, '.gitignore'), '*.log\n');
  commit(seed, 'seed');
  run(seed, 'git', ['push', 'origin', 'HEAD:main']);

  const fault: Intercept = {};
  const core = await startCore(
    join(root, 'data'),
    HOST,
    { handoffTimeoutMs: options.stepTimeoutMs ?? 20_000 },
    {
      toDevice: (id, msg) => (fault.toDevice ? fault.toDevice(id, msg) : msg),
      fromDevice: (id, msg) => (fault.fromDevice ? fault.fromDevice(id, msg) : msg),
    },
  );
  const progress: HandoffProgress[] = [];
  core.control.subscribe((msg: ServerMessage) => {
    if (msg.type === 'handoff.progress') progress.push(msg);
  });

  const machine = (name: string, id: string): Machine => {
    const dir = join(root, name);
    mkdirSync(dir, { recursive: true });
    const clone = join(dir, 'repo');
    if (!(name === 'b' && options.bWithoutClone)) {
      run(dir, 'git', ['clone', origin, clone]);
    } else {
      mkdirSync(clone);
    }
    const minted = core.store.mintDevice(id, id);
    const agent = new TestAgent({
      root: join(root, `${name}-agent`),
      name: id,
      deviceId: id,
      token: minted.token,
      serverUrl: core.url,
      binaries,
      ...(options.sharedStore
        ? {
            claudeProjects: join(root, 'shared-store'),
            env: { FAKE_CLAUDE_LOG: join(root, 'claude.log') },
          }
        : {}),
    });
    return { id, agent, clone, sessionFile: '' };
  };
  const a = machine('a', 'machine-a1');
  const b = machine('b', 'machine-b1');
  a.sessionFile = a.agent.seedSession(a.clone, SESSION, [sessionLine(a.clone, SESSION, 'start')]);
  a.agent.watch(a.clone, [SESSION]);
  b.agent.watch(b.clone, []);

  const w: World = {
    root,
    core,
    origin,
    a,
    b,
    progress,
    fault,
    async close() {
      await a.agent.stop();
      await b.agent.stop();
      await core.close();
      rmSync(root, { recursive: true, force: true });
    },
  };
  worlds.push(w);
  a.agent.start();
  if (!options.onlyA) b.agent.start();
  const online = () =>
    core.control.ownerOnline(a.id) &&
    (options.onlyA || core.control.ownerOnline(b.id)) &&
    core.store.getSession(SESSION)?.byteSize === readFileSync(a.sessionFile).length;
  await until('both agents online and the session synced', online, 30_000).catch((err) => {
    throw new Error(`${err.message}\nA: ${a.agent.logs()}\nB: ${b.agent.logs()}`);
  });
  return w;
}

/** A turn on the owner: the fake `claude` answers and the agent uploads a snapshot. */
async function turn(w: World, text: string): Promise<void> {
  const before = w.core.store.getSession(SESSION)?.byteSize ?? 0;
  const accepted = await w.core.client.prompt(SESSION, text);
  expect(accepted.ok, JSON.stringify(accepted)).toBe(true);
  await until(`the turn "${text}" to be stored`, () => {
    const meta = w.core.store.getSession(SESSION);
    return (meta?.byteSize ?? 0) > before && w.core.control.runtimeOf(SESSION).state === 'ready';
  });
}

async function snapshotAfterTurn(w: World, after: string | undefined): Promise<void> {
  await until('the snapshot of the turn', () => {
    const latest = w.core.store.latestSnapshot(SESSION);
    return !!latest && latest.snapshotId !== after;
  });
}

/** Content and mode of every file a snapshot carries (tracked and untracked, not ignored). */
function files(dir: string): Record<string, string> {
  const names = run(dir, 'git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'])
    .split('\0')
    .filter(Boolean);
  const out: Record<string, string> = {};
  for (const name of [...new Set(names)].sort()) {
    const path = join(dir, name);
    if (!existsSync(path) && !lstatSyncSafe(path)) continue;
    const info = lstatSync(path);
    out[name] = info.isSymbolicLink()
      ? `link:${readlinkSync(path)}`
      : `${info.mode & 0o111 ? 'x' : '-'}:${createHash('sha256').update(readFileSync(path)).digest('hex')}`;
  }
  return out;
}

function lstatSyncSafe(path: string): boolean {
  try {
    lstatSync(path);
    return true;
  } catch {
    return false;
  }
}

function tolerant<T>(read: () => T): T | string {
  try {
    return read();
  } catch {
    return 'unreadable';
  }
}

/** Everything on a machine that a failed handoff must leave alone. */
function machineState(m: Machine, root: string): string {
  const store = (dir: string): string[] => {
    if (!existsSync(dir)) return [];
    return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
      e.isDirectory() ? store(join(dir, e.name)).map((n) => `${e.name}/${n}`) : [e.name],
    );
  };
  const parent = join(m.clone, '..');
  // The meaning of projects.json, not its formatting: the agent writes it indented.
  const projects = existsSync(join(m.agent.home, 'projects.json'))
    ? JSON.parse(readFileSync(join(m.agent.home, 'projects.json'), 'utf8'))
    : null;
  const sessionHashes = store(m.agent.claudeProjects).map((n) =>
    createHash('sha256')
      .update(readFileSync(join(m.agent.claudeProjects, n)))
      .digest('hex'),
  );
  return JSON.stringify({
    parent: readdirSync(parent).sort(),
    branches: tolerant(() => run(m.clone, 'git', ['branch', '--list'])),
    worktrees: tolerant(() => run(m.clone, 'git', ['worktree', 'list']).split('\n').length),
    claude: store(m.agent.claudeProjects).sort(),
    sessionHashes: sessionHashes.sort(),
    projects,
    tree: tolerant(() => files(m.clone)),
    records: existsSync(join(m.agent.home, 'handoff'))
      ? readdirSync(join(m.agent.home, 'handoff')).filter((n) => n.endsWith('.json'))
      : [],
    root: root.length,
  });
}

/** The work a session leaves uncommitted: an unpushed commit, edits, a deletion, untracked files. */
function makeMess(m: Machine): void {
  writeFileSync(join(m.clone, 'committed.txt'), 'pushed? no\n');
  commit(m.clone, 'local commit');
  writeFileSync(join(m.clone, 'src', 'main.txt'), 'one\nTWO\nthree\nfour\n');
  writeFileSync(join(m.clone, 'README.md'), 'hello\nstaged\n');
  run(m.clone, 'git', ['add', 'README.md']);
  writeFileSync(
    join(m.clone, 'data.bin'),
    Buffer.from(Array.from({ length: 4000 }, (_, i) => (i * 7) % 253)),
  );
  chmodSync(join(m.clone, 'tool.sh'), 0o644);
  mkdirSync(join(m.clone, 'notes'));
  writeFileSync(join(m.clone, 'notes', 'todo.txt'), 'untracked 字\n');
  writeFileSync(join(m.clone, 'notes', 'blob.dat'), Buffer.from([0, 1, 2, 255, 254, 0, 9]));
  writeFileSync(join(m.clone, 'run.sh'), '#!/bin/sh\necho run\n');
  chmodSync(join(m.clone, 'run.sh'), 0o755);
  symlinkSync('notes/todo.txt', join(m.clone, 'todo-link'));
  writeFileSync(join(m.clone, 'debug.log'), 'ignored\n');
}

function target(w: World): string {
  return `${w.b.clone}-${SHORT}`;
}

function handoff(w: World, to = w.b.id, path?: string) {
  return w.core.client.handoff(SESSION, to, path);
}

describe('online handoff', () => {
  it('moves uncommitted and untracked work into a worktree of the target clone and continues there', async () => {
    const w = await world();
    await turn(w, 'say first');
    await snapshotAfterTurn(w, undefined);
    makeMess(w.a);
    const streamBefore = readFileSync(w.a.sessionFile);

    // Before the handoff the target names the clone it would use for the owner's origin, and only that one.
    const listing = await w.core.control.workspace(SESSION, w.a.id);
    expect(listing).toMatchObject({ ok: true, listing: { origin: w.origin } });
    expect(await w.core.control.findClone(w.b.id, w.origin)).toEqual({
      ok: true,
      path: realpathSync(w.b.clone),
    });
    expect(await w.core.control.findClone(w.b.id, '/elsewhere/other.git')).toEqual({
      ok: true,
      path: null,
    });

    const result = await handoff(w);
    expect(result, w.a.agent.logs() + w.b.agent.logs()).toMatchObject({
      ok: true,
      data: {
        ok: true,
        ownerDeviceId: w.b.id,
        ownerEpoch: 1,
        worktree: realpathSync(target(w)),
        branch: `hoplet/${SHORT}`,
      },
    });
    if (!result.ok) return;
    expect(result.data.snapshotAt).toBeUndefined();

    // The same files in a new worktree of the target's own clone, on its own branch.
    expect(existsSync(target(w))).toBe(true);
    expect(files(target(w))).toEqual(files(w.a.clone));
    expect(run(target(w), 'git', ['rev-parse', '--abbrev-ref', 'HEAD'])).toBe(`hoplet/${SHORT}`);
    expect(run(target(w), 'git', ['rev-parse', 'HEAD'])).toBe(
      run(w.a.clone, 'git', ['rev-parse', 'HEAD']),
    );
    expect(existsSync(join(target(w), 'debug.log'))).toBe(false);
    expect(run(w.b.clone, 'git', ['status', '--porcelain'])).toBe('');
    expect(run(w.b.clone, 'git', ['worktree', 'list']).split('\n')).toHaveLength(2);

    // The session file is the stored stream byte for byte, in the store of the worktree.
    const installed = join(w.b.agent.claudeProjects, claudeDirName(target(w)), `${SESSION}.jsonl`);
    expect(readFileSync(installed).equals(streamBefore)).toBe(true);
    const stored = await w.core.store.streamFingerprint(SESSION);
    expect(stored?.streamSha256).toBe(createHash('sha256').update(streamBefore).digest('hex'));
    expect(
      JSON.parse(readFileSync(join(w.b.agent.home, 'projects.json'), 'utf8')).projects,
    ).toEqual(
      expect.arrayContaining([{ path: expect.stringContaining(`-${SHORT}`), imported: [SESSION] }]),
    );

    // Ownership moved in the commit; the epoch increased; progress was reported to browsers.
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.b.id);
    expect(w.core.store.getSession(SESSION)?.ownerEpoch).toBe(1);
    expect(w.progress.map((p) => p.step)).toEqual(
      expect.arrayContaining(['prepare', 'transfer', 'install', 'verify', 'commit', 'done']),
    );
    expect(w.progress.at(-1)).toMatchObject({ step: 'done', targetDeviceId: w.b.id });

    // The target continues: Claude Code resumes in the worktree, writes there, and sync follows.
    const cursor = w.core.store.getSession(SESSION)?.byteSize ?? 0;
    await until(
      'the target runtime to be idle',
      () => w.core.control.runtimeOf(SESSION).state === 'idle',
    );
    await turn(w, 'say from the target');
    expect(w.core.store.getSession(SESSION)?.byteSize).toBeGreaterThan(cursor);
    const lines = readFileSync(installed, 'utf8')
      .trim()
      .split('\n')
      .map((l) => JSON.parse(l));
    expect(lines.at(-1).cwd).toBe(realpathSync(target(w)));
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.b.id);
    await until('the target to sync the new turn', async () => {
      return w.core.store.getSession(SESSION)?.byteSize === readFileSync(installed).length;
    });

    // The old owner's file is still there, but its appends are refused.
    const stuck = w.core.store.getSession(SESSION)?.byteSize;
    writeFileSync(
      w.a.sessionFile,
      `${readFileSync(w.a.sessionFile, 'utf8')}${sessionLine(w.a.clone, SESSION, 'from a terminal')}`,
    );
    await new Promise((r) => setTimeout(r, 800));
    expect(w.core.store.getSession(SESSION)?.byteSize).toBe(stuck);
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.b.id);
  });
});

describe('offline handoff', () => {
  it('installs the last snapshot, states its time, and refuses the old owner when it returns', async () => {
    const w = await world();
    await turn(w, 'say first');
    await snapshotAfterTurn(w, undefined);
    // Work after the last completed turn is not in the snapshot.
    const snapshot = w.core.store.latestSnapshot(SESSION);
    writeFileSync(join(w.a.clone, 'during.txt'), 'before the turn ended\n');
    await turn(w, 'say second');
    await snapshotAfterTurn(w, snapshot?.snapshotId);
    const last = w.core.store.latestSnapshot(SESSION);
    writeFileSync(join(w.a.clone, 'after.txt'), 'after the last snapshot\n');
    const treeAtSnapshot = last?.treeHash;
    expect(treeAtSnapshot).toBeTruthy();

    await w.a.agent.stop();
    await until('the owner to be offline', () => !w.core.control.ownerOnline(w.a.id));

    const result = await handoff(w);
    expect(result, w.b.agent.logs()).toMatchObject({
      ok: true,
      data: { ownerDeviceId: w.b.id, ownerEpoch: 1 },
    });
    if (!result.ok) return;
    expect(result.data.snapshotAt).toBe(last?.createdAt);
    expect(existsSync(join(target(w), 'during.txt'))).toBe(true);
    expect(existsSync(join(target(w), 'after.txt'))).toBe(false);
    expect(w.progress.find((p) => p.step === 'prepare' && p.snapshotAt)?.snapshotAt).toBe(
      last?.createdAt,
    );

    // The old owner comes back: its appends are rejected and it does not run the session.
    const cursor = w.core.store.getSession(SESSION)?.byteSize;
    writeFileSync(
      w.a.sessionFile,
      `${readFileSync(w.a.sessionFile, 'utf8')}${sessionLine(w.a.clone, SESSION, 'late')}`,
    );
    w.a.agent.start();
    await until('the old owner to reconnect', () => w.core.control.ownerOnline(w.a.id));
    await new Promise((r) => setTimeout(r, 1000));
    expect(w.core.store.getSession(SESSION)?.byteSize).toBe(cursor);
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.b.id);
  });

  it('refuses when no snapshot was stored', async () => {
    const w = await world();
    await w.a.agent.stop();
    await until('the owner to be offline', () => !w.core.control.ownerOnline(w.a.id));
    const result = await handoff(w);
    expect(result).toMatchObject({ ok: false, error: { code: 'no-snapshot' } });
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.a.id);
  });
});

describe('two agents on one machine', () => {
  it('starts Claude Code on the target in the worktree and finds the installed session file there', async () => {
    const w = await world({ sharedStore: true });
    await turn(w, 'say first');
    const result = await handoff(w);
    expect(result, w.b.agent.logs()).toMatchObject({ ok: true });
    await until(
      'the target runtime to be idle',
      () => w.core.control.runtimeOf(SESSION).state === 'idle',
    );
    const before = w.core.store.getSession(SESSION)?.byteSize ?? 0;
    const log = join(w.root, 'claude.log');
    const starts = () =>
      readFileSync(log, 'utf8')
        .trim()
        .split('\n')
        .map((l) => JSON.parse(l) as { cwd: string; args: string[] });
    const startedOnA = starts().length;
    await turn(w, 'say from the target');
    const started = starts().slice(startedOnA);
    expect(started).toHaveLength(1);
    // Claude Code resumes in the worktree, not in the directory the session came from: the store holds the
    // session twice and the store directory of the worktree is the one that counts.
    expect(started[0]?.cwd).toBe(realpathSync(target(w)));
    expect(started[0]?.args.slice(-2)).toEqual(['--resume', SESSION]);
    const installed = join(w.b.agent.claudeProjects, claudeDirName(target(w)), `${SESSION}.jsonl`);
    await until(
      'the target to sync the new turn',
      () => w.core.store.getSession(SESSION)?.byteSize === readFileSync(installed).length,
    );
    expect(w.core.store.getSession(SESSION)?.byteSize).toBeGreaterThan(before);
    // The source's file did not receive the turn.
    expect(readFileSync(w.a.sessionFile).length).toBe(before);
  });
});

describe('refusals', () => {
  it('refuses a target that is offline, the owner itself, a handoff in progress and a running turn', async () => {
    const w = await world();
    await turn(w, 'say first');
    await snapshotAfterTurn(w, undefined);

    expect(await handoff(w, w.a.id)).toMatchObject({ ok: false, error: { code: 'same-device' } });
    expect(await handoff(w, 'machine-nope')).toMatchObject({
      ok: false,
      error: { code: 'target-offline' },
    });

    const running = await w.core.client.prompt(SESSION, 'sleep');
    expect(running.ok).toBe(true);
    await until('the turn to run', () => w.core.control.runtimeOf(SESSION).state === 'running');
    expect(await handoff(w)).toMatchObject({ ok: false, error: { code: 'busy' } });
    await w.core.client.interrupt(SESSION);
    await until('the turn to end', () => w.core.control.runtimeOf(SESSION).state === 'ready');
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.a.id);
  });
});

describe.concurrent('failure injection', () => {
  /** Owner, cursor, the session stream and both machines' files, as a failed handoff must leave them. */
  async function state(w: World) {
    const meta = w.core.store.getSession(SESSION);
    return {
      owner: meta?.ownerDeviceId,
      epoch: meta?.ownerEpoch,
      cursor: meta?.byteSize,
      stream: await w.core.store.streamFingerprint(SESSION),
      a: machineState(w.a, w.root),
      b: machineState(w.b, w.root),
    };
  }

  async function prepared(options: WorldOptions = {}): Promise<World> {
    const w = await world(options);
    await turn(w, 'say first');
    await snapshotAfterTurn(w, undefined);
    makeMess(w.a);
    return w;
  }

  /** The handoff must fail with `code` and change nothing; afterwards, without the fault, it succeeds. */
  async function failsCleanly(w: World, code: string, then = true): Promise<void> {
    const before = await state(w);
    const result = await handoff(w);
    expect(result, `${w.a.agent.logs()}${w.b.agent.logs()}`).toMatchObject({
      ok: false,
      error: { code },
    });
    expect(w.progress.at(-1)).toMatchObject({ step: 'failed' });
    await until(
      'the target to have cleaned up',
      async () => (await state(w)).b === before.b,
      10_000,
    ).catch(async (err) => {
      throw new Error(
        `${err.message}\nbefore ${before.b}\nafter ${(await state(w)).b}\nB: ${w.b.agent.logs()}`,
      );
    });
    expect(await state(w)).toEqual(before);
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.a.id);
    if (!then) return;
    delete w.fault.toDevice;
    delete w.fault.fromDevice;
    const retry = await handoff(w);
    expect(retry, `${w.a.agent.logs()}${w.b.agent.logs()}`).toMatchObject({ ok: true });
    expect(files(target(w))).toEqual(files(w.a.clone));
  }

  it('at prepare: the owner cannot snapshot', async () => {
    const w = await prepared();
    // A broken repository: the owner cannot capture its tree.
    renameSync(join(w.a.clone, '.git'), join(w.a.clone, '.git-away'));
    await failsCleanly(w, 'rejected', false);
    renameSync(join(w.a.clone, '.git-away'), join(w.a.clone, '.git'));
  });

  it('at prepare: the owner does not answer', async () => {
    const w = await prepared({ stepTimeoutMs: 2500 });
    w.fault.fromDevice = (_id, msg) => (msg.type === 'handoff.prepared' ? null : msg);
    const result = await handoff(w);
    expect(result).toMatchObject({ ok: false, error: { code: 'timeout' } });
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.a.id);
    expect(existsSync(target(w))).toBe(false);
  });

  it('at install: the target has no clone', async () => {
    const w = await prepared({ bWithoutClone: true });
    await failsCleanly(w, 'no-clone', false);
  });

  it('at install: the worktree path is taken', async () => {
    const w = await prepared();
    mkdirSync(target(w));
    writeFileSync(join(target(w), 'mine.txt'), 'keep me\n');
    await failsCleanly(w, 'rejected', false);
    expect(readFileSync(join(target(w), 'mine.txt'), 'utf8')).toBe('keep me\n');
  });

  it('at install: a chunk is corrupted on the way', async () => {
    const w = await prepared();
    w.fault.toDevice = (_id, msg) => {
      if (msg.type === 'handoff.data' && msg.part === 'snapshot' && msg.seq === 0) {
        const raw = Buffer.from(msg.data, 'base64');
        raw[raw.length - 1] = (raw[raw.length - 1] ?? 0) ^ 0xff;
        return { ...msg, data: raw.toString('base64') };
      }
      return msg;
    };
    await failsCleanly(w, 'rejected');
  });

  it('at install: a chunk of the session stream is corrupted on the way', async () => {
    const w = await prepared();
    w.fault.toDevice = (_id, msg) => {
      if (msg.type === 'handoff.data' && msg.part === 'session' && msg.seq === 0) {
        const plain = gunzipSync(Buffer.from(msg.data, 'base64'));
        plain[0] = (plain[0] ?? 0) ^ 0xff;
        return { ...msg, data: gzipSync(plain).toString('base64') };
      }
      return msg;
    };
    await failsCleanly(w, 'rejected');
  });

  it('at install: a snapshot announced over the limit is refused before it is received', async () => {
    const w = await prepared();
    w.fault.toDevice = (_id, msg) =>
      msg.type === 'handoff.install' ? { ...msg, snapshotBytes: 64 * 1024 * 1024 + 1 } : msg;
    const before = await state(w);
    const result = await handoff(w);
    expect(result).toMatchObject({ ok: false, error: { code: 'rejected' } });
    expect(await state(w)).toEqual(before);
  });

  it('at install: an archive that expands past the limit is refused', async () => {
    const w = await prepared();
    const bomb = craftedArchive([
      { name: 'snapshot.json', data: Buffer.from('{}') },
      { name: 'tracked.diff', data: Buffer.alloc(0) },
      { name: 'untracked/zeros.bin', data: Buffer.alloc(257 * 1024 * 1024) },
    ]);
    expect(bomb.length).toBeLessThan(2 * 1024 * 1024);
    replaceSnapshot(w, bomb);
    await failsCleanly(w, 'rejected', false);
  });

  it('at install: an archive that names a path outside the worktree writes nothing', async () => {
    const w = await prepared();
    replaceSnapshot(
      w,
      craftedArchive([
        { name: 'snapshot.json', data: Buffer.from('{}') },
        { name: 'untracked/../../escaped.txt', data: Buffer.from('pwn') },
      ]),
    );
    await failsCleanly(w, 'rejected', false);
    expect(existsSync(join(w.root, 'b', 'escaped.txt'))).toBe(false);
    expect(existsSync(join(w.root, 'escaped.txt'))).toBe(false);
  });

  it('at verify: the target reports a session copy that differs', async () => {
    const w = await prepared();
    w.fault.fromDevice = (_id, msg) =>
      msg.type === 'handoff.verified' ? { ...msg, sessionSha256: '0'.repeat(64) } : msg;
    await failsCleanly(w, 'verify-failed');
  });

  it('at verify: the target reports a working tree that differs', async () => {
    const w = await prepared();
    w.fault.fromDevice = (_id, msg) =>
      msg.type === 'handoff.verified' ? { ...msg, treeHash: '1'.repeat(40) } : msg;
    await failsCleanly(w, 'verify-failed');
  });

  it('at verify: the target goes away and, back, removes its installation', async () => {
    const w = await prepared();
    const before = await state(w);
    w.fault.toDevice = (_id, msg) => {
      if (msg.type === 'handoff.verify') void w.b.agent.kill();
      return msg;
    };
    const result = await handoff(w);
    expect(result).toMatchObject({ ok: false });
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.a.id);
    // The installation was complete when the target died; it waits for the core's word, which is "not yours".
    expect(existsSync(target(w))).toBe(true);
    delete w.fault.toDevice;
    w.b.agent.start();
    await until('the target to remove what it had installed', () => !existsSync(target(w)), 20_000);
    await until('the target to be settled', async () => (await state(w)).b === before.b, 10_000);
    expect(await state(w)).toEqual(before);
  });

  it('at commit: the session grew after the target proved its copy', async () => {
    const w = await prepared();
    w.fault.fromDevice = async (_id, msg) => {
      if (msg.type !== 'handoff.verified') return msg;
      // The old owner appends while the verification is on its way; the core accepts it.
      const grown = `${readFileSync(w.a.sessionFile, 'utf8')}${sessionLine(w.a.clone, SESSION, 'late')}`;
      writeFileSync(w.a.sessionFile, grown);
      await until(
        'the append',
        () => w.core.store.getSession(SESSION)?.byteSize === Buffer.byteLength(grown),
      );
      return msg;
    };
    const result = await handoff(w);
    expect(result).toMatchObject({ ok: false, error: { code: 'session-changed' } });
    expect(w.core.store.ownerDeviceId(SESSION)).toBe(w.a.id);
    expect(w.core.store.getSession(SESSION)?.ownerEpoch).toBe(0);
    await until('the target to have cleaned up', () => !existsSync(target(w)), 10_000);
    // The cursor is where the legitimate append put it, and the target holds nothing.
    expect(w.core.store.getSession(SESSION)?.byteSize).toBe(readFileSync(w.a.sessionFile).length);
  });
});

/** Makes the core send `archive` as the snapshot, announced with its real size and checksum. */
function replaceSnapshot(w: World, archive: Buffer): void {
  // One chunk is at most 1 MiB of archive; the crafted archives here fit in one.
  expect(archive.length).toBeLessThanOrEqual(1024 * 1024);
  const sha = createHash('sha256').update(archive).digest('hex');
  w.fault.toDevice = (_id, msg) => {
    if (msg.type === 'handoff.install') {
      return { ...msg, snapshotBytes: archive.length, snapshotSha256: sha };
    }
    if (msg.type === 'handoff.data' && msg.part === 'snapshot') {
      if (msg.seq !== 0) return null;
      return { ...msg, last: true, encoding: 'base64', data: archive.toString('base64') };
    }
    return msg;
  };
}

/** A gzip-compressed tar with exactly the entries given, names unchecked. */
function craftedArchive(entries: { name: string; data: Buffer }[]): Buffer {
  const blocks: Buffer[] = [];
  for (const { name, data } of entries) {
    const header = Buffer.alloc(512);
    header.write(name, 0, 100, 'utf8');
    header.write('0000644\0', 100);
    header.write('0000000\0', 108);
    header.write('0000000\0', 116);
    header.write(`${data.length.toString(8).padStart(11, '0')}\0`, 124);
    header.write('00000000000\0', 136);
    header.write('        ', 148);
    header.write('0', 156);
    header.write('ustar\0', 257);
    header.write('00', 263);
    let sum = 0;
    for (const byte of header) sum += byte;
    header.write(`${sum.toString(8).padStart(6, '0')}\0 `, 148);
    blocks.push(header, data, Buffer.alloc((512 - (data.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return gzipSync(Buffer.concat(blocks));
}
