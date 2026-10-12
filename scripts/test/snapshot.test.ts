// End to end: the real Go agent, an in-process core and the scripted fake `claude`. After a completed turn
// the agent uploads the workspace snapshot of the session, and the core keeps only the latest.
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterAll, beforeAll, expect, it, vi } from 'vitest';
import {
  type Binaries,
  buildBinaries,
  type Core,
  run,
  sessionLine,
  startCore,
  TestAgent,
  until,
} from './support/e2e.ts';

vi.setConfig({ testTimeout: 90_000 });

const SESSION = '22222222-3333-4444-8555-666666666601';
let root = '';
let core: Core;
let agent: TestAgent;
let binaries: Binaries;
let project = '';

/** The tree hash git itself computes for the working tree: add everything to a private index. */
function oracleTree(dir: string): string {
  const index = join(root, 'oracle-index');
  rmSync(index, { force: true });
  const env = { ...process.env, GIT_INDEX_FILE: index };
  run(dir, 'git', ['read-tree', 'HEAD'], { ...env, GIT_CONFIG_GLOBAL: '/dev/null' });
  run(dir, 'git', ['add', '-A'], { ...env, GIT_CONFIG_GLOBAL: '/dev/null' });
  return run(dir, 'git', ['write-tree'], { ...env, GIT_CONFIG_GLOBAL: '/dev/null' });
}

beforeAll(async () => {
  root = mkdtempSync(join(tmpdir(), 'hoplet-snapshot-e2e-'));
  binaries = buildBinaries(root);
  core = await startCore(join(root, 'data'), 'snapshot-e2e-host');
  project = join(root, 'project');
  mkdirSync(project);
  run(project, 'git', ['init', '-b', 'main']);
  writeFileSync(join(project, 'README.md'), 'hello\n');
  run(project, 'git', ['add', 'README.md']);
  run(project, 'git', ['commit', '-m', 'seed']);
  writeFileSync(join(project, 'README.md'), 'hello\nchanged\n');
  writeFileSync(join(project, 'notes.txt'), 'untracked\n');

  const minted = core.store.mintDevice('snapshot-e2e', 'snapshot-e2e');
  agent = new TestAgent({
    root: join(root, 'agent'),
    name: 'snapshot-e2e',
    deviceId: 'snapshot-e2e',
    token: minted.token,
    serverUrl: core.url,
    binaries,
  });
  agent.seedSession(project, SESSION, [sessionLine(project, SESSION, 'start')]);
  agent.watch(project, [SESSION]);
  agent.start();
  await until(
    'the session to sync',
    () => core.control.ownerOnline('snapshot-e2e') && core.store.getSession(SESSION) !== null,
  ).catch((err) => {
    throw new Error(`${err.message}: ${agent.logs()}`);
  });
}, 120_000);

afterAll(async () => {
  await agent?.stop();
  await core?.close();
  rmSync(root, { recursive: true, force: true });
});

it('uploads a snapshot after a completed turn and keeps only the latest', async () => {
  expect(core.store.latestSnapshot(SESSION)).toBeNull();
  const accepted = await core.client.prompt(SESSION, 'say first');
  expect(accepted.ok, JSON.stringify(accepted)).toBe(true);
  await until('the first snapshot', () => core.store.latestSnapshot(SESSION) !== null);

  const first = core.store.latestSnapshot(SESSION);
  if (!first) throw new Error('no snapshot');
  expect(first.baseCommit).toBe(run(project, 'git', ['rev-parse', 'HEAD']));
  expect(first.treeHash).toBe(oracleTree(project));
  expect(first.byteSize).toBeGreaterThan(0);
  const stored = join(root, 'data', 'snapshots', SESSION, `${first.snapshotId}.tar.gz`);
  expect(statSync(stored).size).toBe(first.byteSize);
  const listing = run(root, 'tar', ['-tzf', stored]).split('\n');
  expect(listing).toEqual(
    expect.arrayContaining(['snapshot.json', 'tracked.diff', 'untracked/notes.txt']),
  );

  // Another turn after another edit replaces it.
  writeFileSync(join(project, 'second.txt'), 'second\n');
  await until(
    'the session to be ready for the next prompt',
    () => core.control.runtimeOf(SESSION).state === 'ready',
  );
  const again = await core.client.prompt(SESSION, 'say second');
  expect(again.ok).toBe(true);
  await until(
    'the second snapshot',
    () => core.store.latestSnapshot(SESSION)?.snapshotId !== first.snapshotId,
  );
  const second = core.store.latestSnapshot(SESSION);
  expect(second?.treeHash).toBe(oracleTree(project));
  expect(second?.treeHash).not.toBe(first.treeHash);
  expect(readdirSync(join(root, 'data', 'snapshots', SESSION))).toEqual([
    `${second?.snapshotId}.tar.gz`,
  ]);
  expect(existsSync(stored)).toBe(false);
  // The agent keeps no archive of its own once the upload is done.
  const leftovers = existsSync(join(agent.home, 'handoff', 'snapshots'))
    ? readdirSync(join(agent.home, 'handoff', 'snapshots'))
    : [];
  expect(leftovers).toEqual([]);
});
