import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  appendFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { gzipSync } from 'node:zlib';
import { expect, type Page, test } from '@playwright/test';
import { type Agent, BrowserFeed, RawDevice, Stack, storeName, until } from './stack.ts';
import { open, send, unlock } from './ui.ts';

// docs/spec.md#handoff: an online and an offline handoff with uncommitted and untracked changes into the
// second agent's own clone, and a failure at each step that leaves owner, cursor and both machines' files as
// they were. The failures are injected from outside: a process that dies mid-step, a clone that is missing, a
// path or a session file that is taken, and scripted devices on the agent socket that answer a step wrongly.
test.describe.configure({ mode: 'serial', timeout: 180_000 });

const SRC = { name: 'e2e-source', id: 'dev-source' };
const DST = { name: 'e2e-target', id: 'dev-target' };

let stack: Stack;
let src: Agent;
let dst: Agent;
let feed: BrowserFeed;
let repo = '';
let clone = '';
let page: Page;

const git = (cwd: string, ...args: string[]) =>
  execFileSync(
    'git',
    [
      '--no-optional-locks',
      '-C',
      cwd,
      '-c',
      'user.name=e2e',
      '-c',
      'user.email=e2e@example.invalid',
      '-c',
      'commit.gpgsign=false',
      ...args,
    ],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] },
  ).trim();

function write(path: string, content: string | Buffer) {
  mkdirSync(join(path, '..'), { recursive: true });
  writeFileSync(path, content);
}

test.beforeAll(async ({ browser }) => {
  stack = await Stack.start(mkdtempSync(join(tmpdir(), 'hoplet-e2e-handoff.')));
  const origin = join(stack.tmp, 'origin.git');
  git(stack.tmp, 'init', '--bare', '-b', 'main', origin);
  const seed = join(stack.tmp, 'seed');
  git(stack.tmp, 'clone', '--quiet', origin, seed);
  git(seed, 'checkout', '-b', 'main');
  write(join(seed, 'README.md'), '# fixture\n');
  write(join(seed, 'a.txt'), 'a: committed\n');
  write(join(seed, 'lib', 'b.txt'), 'b: committed\n');
  write(join(seed, 'bin.dat'), Buffer.from([0, 1, 2, 3, 4, 5]));
  write(join(seed, '.gitignore'), 'ignored.log\n');
  git(seed, 'add', '-A');
  git(seed, 'commit', '--quiet', '-m', 'fixture');
  git(seed, 'push', '--quiet', 'origin', 'main');
  repo = join(stack.tmp, 'src', 'repo');
  clone = join(stack.tmp, 'dst', 'clone');
  git(stack.tmp, 'clone', '--quiet', origin, repo);
  git(stack.tmp, 'clone', '--quiet', origin, clone);
  // The uncommitted work: a modified tracked file, a staged one, a changed binary, an untracked file in a
  // new directory and an ignored one that stays behind.
  write(join(repo, 'a.txt'), 'a: committed\na: edited, not staged\n');
  write(join(repo, 'lib', 'b.txt'), 'b: committed\nb: edited and staged\n');
  git(repo, 'add', 'lib/b.txt');
  write(join(repo, 'bin.dat'), Buffer.from([9, 8, 7, 6, 5, 4, 3]));
  write(join(repo, 'notes', 'todo.txt'), 'untracked: write the handoff test\n');
  write(join(repo, 'ignored.log'), 'never leaves the machine\n');

  src = await stack.agent({ name: SRC.name, deviceId: SRC.id, projects: { [repo]: [] } });
  dst = await stack.agent({ name: DST.name, deviceId: DST.id, projects: { [clone]: [] } });
  src.start();
  dst.start();
  await src.online();
  await dst.online();
  feed = await BrowserFeed.open(stack);
  page = await browser.newPage({ viewport: { width: 1280, height: 800 }, locale: 'en-US' });
});

test.afterAll(async () => {
  feed?.close();
  await page?.close();
  await stack?.stop();
});

const sessionId = (n: number) => `1000000${n}-5555-4666-8777-888888888888`;
const short = (id: string) => id.slice(0, 8);
const titleOf = (n: number) => `Handoff fixture ${n}`;

/** A session of the source whose turn has completed, so the core holds the stream and a snapshot. */
async function sourceSession(n: number, dir = repo, agent = src): Promise<string> {
  const id = sessionId(n);
  const file = agent.seed(dir, id, titleOf(n));
  agent.watch(dir, [id]);
  await until(`${id} to sync`, 60_000, async () => ((await stack.session(id))?.lineCount ?? 0) > 0);
  if (dir === repo) {
    expect((await stack.prompt(id, `say snapshot ${n}`)).status).toBe(202);
    await until(`the snapshot of ${id}`, 60_000, async () => {
      const r = await stack.request('GET', `/api/sessions/${id}/snapshot/changes`);
      return r.status === 200;
    });
    await until(`${id} to settle`, 60_000, async () => {
      const s = await stack.session(id);
      return s?.runtime.state === 'ready' && s.byteSize === statSync(file).size;
    });
  }
  return id;
}

type Tree = Record<string, string>;

/** Every file under `dir` with a digest, outside `.git`. */
function walk(dir: string, rel = '', out: Tree = {}): Tree {
  if (!existsSync(dir)) return out;
  for (const e of readdirSync(dir, { withFileTypes: true }).sort((a, b) =>
    a.name < b.name ? -1 : 1,
  )) {
    if (e.name === '.git') continue;
    const path = join(dir, e.name);
    if (e.isDirectory()) {
      out[`${rel}${e.name}/`] = 'dir';
      walk(path, `${rel}${e.name}/`, out);
    } else {
      out[`${rel}${e.name}`] = createHash('sha256').update(readFileSync(path)).digest('hex');
    }
  }
  return out;
}

const repoState = (dir: string) => ({
  head: git(dir, 'rev-parse', 'HEAD'),
  status: git(dir, 'status', '--porcelain', '-uall'),
  branches: git(dir, 'branch', '--list'),
  worktrees: git(dir, 'worktree', 'list', '--porcelain'),
});

/** Owner, cursor and the files of both machines: what a failed handoff must leave as it was. */
async function observe(id: string) {
  const s = await stack.session(id);
  return {
    owner: s?.ownerDeviceId,
    epoch: s?.ownerEpoch,
    byteSize: s?.byteSize,
    lineCount: s?.lineCount,
    source: {
      files: walk(join(stack.tmp, 'src')),
      git: repoState(repo),
      sessions: walk(src.claudeProjects),
    },
    target: {
      files: walk(join(stack.tmp, 'dst')),
      git: repoState(clone),
      sessions: walk(dst.claudeProjects),
      projects: JSON.parse(readFileSync(join(dst.home, 'projects.json'), 'utf8')) as unknown,
    },
  };
}

const handoff = (id: string, targetDeviceId: string, targetPath?: string) =>
  stack.request('POST', `/api/sessions/${id}/handoff`, {
    targetDeviceId,
    ...(targetPath !== undefined ? { targetPath } : {}),
  });

async function expectRefused(
  id: string,
  attempt: () => Promise<{ status: number; body: unknown }>,
  code: string | string[],
) {
  const before = await observe(id);
  const res = await attempt();
  expect(res.status).toBeGreaterThanOrEqual(400);
  expect(res.body).toMatchObject({
    code: expect.stringMatching(new RegExp(`^(${[code].flat().join('|')})$`)),
  });
  expect(await observe(id)).toEqual(before);
  return res.body as { code: string; reason?: string };
}

async function restart(agent: Agent) {
  agent.start();
  await agent.online(60_000);
}

test.describe('a failed handoff leaves owner, cursor and both machines unchanged', () => {
  test('prepare: the owner cannot take a snapshot', async () => {
    const plain = join(stack.tmp, 'src', 'plain');
    const id = '10000009-5555-4666-8777-888888888888';
    mkdirSync(plain, { recursive: true });
    await sourceSession(9, plain);
    expect(id).toBe(sessionId(9));
    await expectRefused(id, () => handoff(id, DST.id), ['rejected', 'no-snapshot']);
  });

  test('prepare: the owner dies', async () => {
    const id = await sourceSession(1);
    const before = await observe(id);
    const pid = src.proc?.pid ?? 0;
    process.kill(pid, 'SIGSTOP');
    const pending = handoff(id, DST.id);
    await feed.wait(
      (m) => m.type === 'handoff.progress' && m.sessionId === id && m.step === 'prepare',
    );
    await src.kill('SIGKILL');
    const res = await pending;
    expect(res.body).toMatchObject({ code: 'owner-offline' });
    await restart(src);
    const after = await observe(id);
    expect(after).toEqual(before);
  });

  test('transfer: the target dies', async () => {
    const id = await sourceSession(2);
    const before = await observe(id);
    process.kill(dst.proc?.pid ?? 0, 'SIGSTOP');
    const pending = handoff(id, DST.id);
    await feed.wait(
      (m) => m.type === 'handoff.progress' && m.sessionId === id && m.step === 'transfer',
    );
    await dst.kill('SIGKILL');
    const res = await pending;
    expect(res.body).toMatchObject({ code: 'target-offline' });
    await restart(dst);
    expect(await observe(id)).toEqual(before);
  });

  test('install: the target has no clone', async () => {
    const id = await sourceSession(3);
    const bare = join(stack.tmp, 'bare');
    mkdirSync(join(bare, 'plain'), { recursive: true });
    const empty = await stack.agent({ name: 'e2e-empty', deviceId: 'dev-empty' });
    empty.start();
    await empty.online();
    const before = walk(bare);
    await expectRefused(id, () => handoff(id, 'dev-empty'), 'no-clone');
    // A path that is not a clone of anything is no clone either.
    await expectRefused(id, () => handoff(id, 'dev-empty', join(bare, 'plain')), 'no-clone');
    expect(walk(bare)).toEqual(before);
    await empty.kill('SIGKILL');
  });

  test('install: the worktree path is taken and its content is left alone', async () => {
    const id = await sourceSession(4);
    const taken = join(stack.tmp, 'dst', `clone-${short(id)}`);
    write(join(taken, 'keep.txt'), 'not ours\n');
    await expectRefused(id, () => handoff(id, DST.id), 'rejected');
    expect(readFileSync(join(taken, 'keep.txt'), 'utf8')).toBe('not ours\n');
  });

  test('install: a session file in the way rolls the whole installation back', async () => {
    const id = await sourceSession(5);
    const worktree = join(stack.tmp, 'dst', `clone-${short(id)}`);
    const inTheWay = join(dst.claudeProjects, storeName(worktree), `${id}.jsonl`);
    write(inTheWay, 'keep\n');
    await expectRefused(id, () => handoff(id, DST.id), 'rejected');
    expect(readFileSync(inTheWay, 'utf8')).toBe('keep\n');
    expect(existsSync(worktree)).toBe(false);
    expect(git(clone, 'branch', '--list', `hoplet/${short(id)}`)).toBe('');
  });

  test('the target is the owner, or is not connected', async () => {
    const id = sessionId(1);
    await expectRefused(id, () => handoff(id, SRC.id), 'same-device');
    await expectRefused(id, () => handoff(id, 'dev-nobody'), 'target-offline');
  });

  test.describe('scripted devices on the agent socket', () => {
    const OBJECT_ID = 'a'.repeat(40);
    const TREE = 'b'.repeat(40);

    type Step = {
      id: string;
      owner: RawDevice;
      target: RawDevice;
      ask: Record<string, unknown>;
      install: Record<string, unknown>;
      stored: number;
    };

    /**
     * An owner that holds one stored line and a snapshot, and a target that installs nothing; `verify`
     * answers the target's verification. Returns the answer to the handoff request and the session before
     * and after it.
     */
    async function scripted(name: string, verify: (step: Step) => Promise<void>) {
      const id = `2000000${name.length}-5555-4666-8777-${name.padEnd(12, '0').slice(0, 12)}`;
      const owner = await RawDevice.connect(stack, `dev-${name}-owner`);
      const target = await RawDevice.connect(stack, `dev-${name}-target`);
      const line = `${JSON.stringify({ type: 'user', n: 1 })}\n`;
      const stored = Buffer.byteLength(line);
      await owner.append(id, 0, line);
      const archive = gzipSync('scripted snapshot');
      owner.on((m) => {
        if (m.type !== 'handoff.prepare') return;
        owner.send({
          type: 'workspace.snapshot',
          sessionId: id,
          snapshotId: 'scripted-snapshot',
          createdAt: Date.now(),
          baseCommit: OBJECT_ID,
          originUrl: '',
          treeHash: TREE,
          byteSize: archive.length,
          sha256: createHash('sha256').update(archive).digest('hex'),
          seq: 0,
          last: true,
          data: archive.toString('base64'),
        });
        owner.send({
          type: 'handoff.prepared',
          sessionId: id,
          handoffId: m.handoffId,
          snapshotId: 'scripted-snapshot',
          byteSize: stored,
        });
      });
      const aborts: unknown[] = [];
      let install: Record<string, unknown> = {};
      let parts = 2;
      target.on((m) => {
        if (m.type === 'handoff.install') install = m;
        if (m.type === 'handoff.abort') aborts.push(m);
        if (m.type === 'handoff.data' && m.last === true && --parts === 0) {
          target.send({
            type: 'handoff.installed',
            sessionId: id,
            handoffId: m.handoffId,
            worktree: '/scripted/worktree',
            branch: 'hoplet/scripted',
          });
        }
        if (m.type === 'handoff.verify')
          void verify({ id, owner, target, ask: m, install, stored });
      });
      const before = await stack.session(id);
      const res = await handoff(id, `dev-${name}-target`);
      await until('the abort', 10_000, async () => aborts.length > 0);
      const after = await stack.session(id);
      owner.close();
      target.close();
      return { res, before, after };
    }

    const verified = (step: Step, over: Record<string, unknown> = {}) =>
      step.target.send({
        type: 'handoff.verified',
        sessionId: step.id,
        handoffId: step.ask.handoffId,
        sessionBytes: step.install.sessionBytes,
        sessionSha256: step.install.sessionSha256,
        treeHash: TREE,
        ...over,
      });

    test('verify: a copy that does not match is refused and the target is told to remove it', async () => {
      const { res, before, after } = await scripted('verify', async (step) =>
        verified(step, { sessionSha256: '0'.repeat(64) }),
      );
      expect(res.body).toMatchObject({ code: 'verify-failed' });
      expect(after).toMatchObject({
        ownerDeviceId: before?.ownerDeviceId,
        ownerEpoch: before?.ownerEpoch,
        byteSize: before?.byteSize,
      });
    });

    test('commit: a cursor that moved after the copy was verified keeps the owner', async () => {
      const { res, before, after } = await scripted('commit', async (step) => {
        // The owner appends between the target's verification and the core's commit.
        await step.owner.append(
          step.id,
          step.stored,
          `${JSON.stringify({ type: 'user', n: 2 })}\n`,
        );
        verified(step);
      });
      expect(res.body).toMatchObject({ code: 'session-changed' });
      expect(after?.ownerDeviceId).toBe(before?.ownerDeviceId);
      expect(after?.ownerEpoch).toBe(before?.ownerEpoch);
      expect(after?.byteSize).toBeGreaterThan(before?.byteSize ?? 0);
    });
  });
});

/** What a target must hold after a handoff: the files of the source, the session byte for byte. */
async function expectInstalled(id: string, srcFile: string) {
  const worktree = join(stack.tmp, 'dst', `clone-${short(id)}`);
  for (const f of ['README.md', 'a.txt', 'lib/b.txt', 'bin.dat', 'notes/todo.txt'])
    expect(readFileSync(join(worktree, f)), f).toEqual(readFileSync(join(repo, f)));
  expect(existsSync(join(worktree, 'ignored.log'))).toBe(false);
  expect(git(worktree, 'rev-parse', 'HEAD')).toBe(git(repo, 'rev-parse', 'HEAD'));
  expect(git(worktree, 'rev-parse', '--abbrev-ref', 'HEAD')).toBe(`hoplet/${short(id)}`);
  expect(git(worktree, 'diff', 'HEAD', '--name-status')).toBe(
    git(repo, 'diff', 'HEAD', '--name-status'),
  );
  expect(git(worktree, 'ls-files', '-o', '--exclude-standard')).toBe(
    git(repo, 'ls-files', '-o', '--exclude-standard'),
  );
  expect(git(clone, 'status', '--porcelain', '-uall')).toBe('');
  expect(readFileSync(dst.sessionFile(worktree, id))).toEqual(readFileSync(srcFile));
  return worktree;
}

const missing = (): never => {
  throw new Error('the core lists no such session');
};

const clockOf = (ts: number) => {
  const d = new Date(ts);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
};

let online = { id: sessionId(6), worktree: '', srcFile: '', cursor: 0 };

test('an online handoff with uncommitted and untracked changes installs a worktree of the target clone', async () => {
  const id = await sourceSession(6);
  const srcFile = src.sessionFile(repo, id);
  const before = (await stack.session(id)) ?? missing();
  const sourceBefore = { files: walk(join(stack.tmp, 'src')), git: repoState(repo) };

  await unlock(page, stack);
  await open(page, titleOf(6));
  const row = page.getByTestId('workspace.handoff');
  if (!(await row.isVisible())) await page.getByTestId('cockpit.workspace-toggle').click();
  await row.click();
  const panel = page.getByTestId('workspace.handoff-panel');
  await expect(panel.getByTestId('workspace.handoff-target')).toContainText(DST.name);
  await expect(panel.getByTestId('workspace.handoff-clone')).toContainText(clone);
  await panel.getByTestId('workspace.handoff-run').click();
  const worktree = join(stack.tmp, 'dst', `clone-${short(id)}`);
  const done = page.getByTestId('workspace.handoff-done');
  await expect(done).toContainText(worktree);
  await expect(done).toContainText(`hoplet/${short(id)}`);
  expect(feed.steps(id)).toEqual(
    expect.arrayContaining(['prepare', 'transfer', 'install', 'verify', 'commit', 'done']),
  );

  await expectInstalled(id, srcFile);
  const after = (await stack.session(id)) ?? missing();
  expect(after.ownerDeviceId).toBe(DST.id);
  expect(after.ownerEpoch).toBe(before.ownerEpoch + 1);
  expect(after.byteSize).toBe(statSync(srcFile).size);
  // The source machine keeps its work and its files.
  expect({ files: walk(join(stack.tmp, 'src')), git: repoState(repo) }).toEqual(sourceBefore);
  online = { id, worktree, srcFile, cursor: after.byteSize };
});

// docs/spec.md#workspace: "on success the session moves to its new owner in the sidebar", before the new owner
// appends anything.
test('the sidebar moves the session to its new owner when the handoff completes', async () => {
  const group = page.getByTestId('cockpit.device').filter({ hasText: DST.name });
  await expect(group.getByTestId('cockpit.session').filter({ hasText: titleOf(6) })).toBeVisible({
    timeout: 5000,
  });
});

test('the session continues on the target and the old owner is rejected', async () => {
  const { id, worktree, srcFile, cursor } = online;
  await send(page, 'say continued on the target');
  await expect(
    page.getByTestId('cockpit.conversation').getByText('continued on the target', { exact: true }),
  ).toBeVisible();
  const dstFile = dst.sessionFile(worktree, id);
  await until('the turn to sync', 30_000, async () => {
    const s = await stack.session(id);
    return s && s.byteSize > cursor && s.byteSize === statSync(dstFile).size;
  });
  await expect(
    page.getByTestId('cockpit.device').filter({ hasText: DST.name }).getByTestId('cockpit.session'),
  ).toContainText(titleOf(6));

  // The old owner's file grows on: the core takes none of it.
  const stored = (await stack.session(id))?.byteSize;
  appendFileSync(srcFile, `${JSON.stringify({ type: 'user', n: 'from the old owner' })}\n`);
  await new Promise((r) => setTimeout(r, 2500));
  const last = await stack.session(id);
  expect(last?.byteSize).toBe(stored);
  expect(last?.ownerDeviceId).toBe(DST.id);
});

test('an offline handoff uses the last snapshot, states its time, and rejects the old owner after it reconnects', async () => {
  const id = await sourceSession(7);
  const srcFile = src.sessionFile(repo, id);
  const snapshot = await stack.json<{ snapshotAt: number }>(`/api/sessions/${id}/snapshot/changes`);
  const before = (await stack.session(id)) ?? missing();
  await src.kill('SIGKILL');
  await until('the owner to be offline', 20_000, async () => {
    return (await stack.session(id))?.ownerConnected === false;
  });

  await open(page, titleOf(7));
  await expect(page.getByTestId('cockpit.composer-input')).toHaveCount(0);
  await page.getByTestId('cockpit.handoff').click();
  const panel = page.getByTestId('workspace.handoff-panel');
  await expect(panel.getByTestId('workspace.handoff-snapshot')).toContainText(
    clockOf(snapshot.snapshotAt),
  );
  await expect(panel.getByTestId('workspace.handoff-target')).toContainText(DST.name);
  await panel.getByTestId('workspace.handoff-run').click();
  await expect(page.getByTestId('workspace.handoff-done')).toContainText(`hoplet/${short(id)}`);
  const stated = await feed.wait(
    (m) =>
      m.type === 'handoff.progress' &&
      m.sessionId === id &&
      m.step === 'prepare' &&
      m.snapshotAt !== undefined,
  );
  expect(stated.snapshotAt).toBe(snapshot.snapshotAt);

  await expectInstalled(id, srcFile);
  const after = (await stack.session(id)) ?? missing();
  expect(after.ownerDeviceId).toBe(DST.id);
  expect(after.ownerEpoch).toBe(before.ownerEpoch + 1);
  expect(after.byteSize).toBe(before.byteSize);

  // The old owner returns with a file that grew while it was away: the core takes nothing from it.
  appendFileSync(srcFile, `${JSON.stringify({ type: 'user', n: 'written while away' })}\n`);
  await restart(src);
  await new Promise((r) => setTimeout(r, 2500));
  expect((await stack.session(id))?.byteSize).toBe(before.byteSize);
  // And on the wire: its append is refused as a non-owner's.
  await src.kill('SIGKILL');
  const raw = await RawDevice.open(stack, SRC.id, src.token, SRC.name);
  const refused = await raw.append(
    id,
    before.byteSize,
    `${JSON.stringify({ type: 'user', n: 3 })}\n`,
  );
  expect(refused).toMatchObject({
    type: 'session.rejected',
    reason: 'not-owner',
    ownerDeviceId: DST.id,
  });
  raw.close();
  const final = await stack.session(id);
  expect(final?.ownerDeviceId).toBe(DST.id);
  expect(final?.byteSize).toBe(before.byteSize);
});
