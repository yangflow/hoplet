import { mkdirSync, mkdtempSync, readFileSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { generateFixture, MiB } from '../../scripts/bench/fixture.ts';
import { type Agent, RawDevice, Stack, storeName, until } from './stack.ts';

// docs/spec.md#acceptance-checklist, sync: the large fixture arrives whole, a restart of the agent or of the
// core during the sync neither duplicates nor loses a byte, and the core accepts appends only from the owner
// at the byte it holds. `E2E_SYNC_MB` (default 256) sizes the fixture of the first test, and the restart
// tests use at most half of it so that a sync is still under way when they interrupt it.
const SYNC_MB = Number(process.env.E2E_SYNC_MB ?? 256);
const RESTART_MB = Math.max(2, Math.min(SYNC_MB, 128));

let stack: Stack;
test.beforeEach(async () => {
  stack = await Stack.start(mkdtempSync(join(tmpdir(), 'hoplet-e2e-sync.')));
});
test.afterEach(async () => {
  await stack.stop();
});

/** A device with a generated session of `mb` MiB in its store, not started yet. */
async function deviceWithFixture(id: string, mb: number) {
  const agent = await stack.agent({
    name: `fixture-${id.slice(-2)}`,
    deviceId: `dev-${id.slice(-2)}`,
  });
  const cwd = join(stack.tmp, 'work', 'big');
  mkdirSync(cwd, { recursive: true });
  const dir = join(agent.claudeProjects, storeName(cwd));
  mkdirSync(dir, { recursive: true });
  const path = join(dir, `${id}.jsonl`);
  const info = generateFixture({ path, bytes: mb * MiB, sessionId: id, cwd });
  expect(statSync(path).size).toBe(info.bytes);
  agent.watch(cwd, [id]);
  return { agent, info, path };
}

/** The lines of a file as the core counts them: one per `\n`. */
const linesOf = (path: string) => readFileSync(path).filter((b) => b === 0x0a).length;

async function synced(id: string, bytes: number, lines: number, ms: number) {
  await until(
    `${id} to sync completely`,
    ms,
    async () => {
      const s = await stack.session(id);
      return s?.byteSize === bytes && s.lineCount === lines;
    },
    500,
  );
}

/** Interrupts `act` while the session is partly there: some bytes stored, not all. */
async function midSync(id: string, total: number, act: () => Promise<void>) {
  const seen = await until(
    'part of the session to arrive',
    600_000,
    async () => {
      const s = await stack.session(id);
      return s && s.byteSize > 0 ? s.byteSize : null;
    },
    20,
  );
  expect(seen).toBeLessThan(total);
  await act();
}

test('the large fixture syncs completely and the server byte_size equals the file size', async () => {
  test.setTimeout(30 * 60_000);
  const id = '22222222-3333-4444-8555-666666666601';
  const { agent, info, path } = await deviceWithFixture(id, SYNC_MB);
  expect(info.bytes).toBeGreaterThan(SYNC_MB * MiB * 0.99);
  agent.start();
  await synced(id, info.bytes, info.lines, 25 * 60_000);
  const s = await stack.session(id);
  expect(s?.byteSize).toBe(statSync(path).size);
  expect(s?.lineCount).toBe(linesOf(path));
  // Reads stay pages: the tail page is a few entries, not the file.
  const tail = await stack.json<{ total: number; entries: unknown[] }>(
    `/api/sessions/${id}/lines?start=-80&limit=80`,
  );
  expect(tail.total).toBe(info.lines);
  expect(tail.entries.length).toBeLessThanOrEqual(80);
});

test('restarting the agent during the sync neither duplicates nor loses a byte', async () => {
  test.setTimeout(30 * 60_000);
  const id = '22222222-3333-4444-8555-666666666602';
  const { agent, info } = await deviceWithFixture(id, RESTART_MB);
  agent.start();
  await midSync(id, info.bytes, async () => {
    await agent.kill('SIGKILL');
  });
  const before = await stack.session(id);
  expect(before?.byteSize).toBeLessThan(info.bytes);
  agent.start();
  await synced(id, info.bytes, info.lines, 20 * 60_000);
});

test('restarting the core during the sync neither duplicates nor loses a byte', async () => {
  test.setTimeout(30 * 60_000);
  const id = '22222222-3333-4444-8555-666666666603';
  const { agent, info } = await deviceWithFixture(id, RESTART_MB);
  agent.start();
  await midSync(id, info.bytes, async () => {
    await stack.restartCore();
  });
  await agent.online(60_000);
  await synced(id, info.bytes, info.lines, 20 * 60_000);
});

const line = (n: number) => `${JSON.stringify({ type: 'user', n })}\n`;

test('an append from a non-owner is rejected with not-owner and a wrong offset is answered with session.resync', async () => {
  const id = '22222222-3333-4444-8555-666666666604';
  const cwd = join(stack.tmp, 'work', 'owned');
  const agent: Agent = await stack.agent({ name: 'owner', deviceId: 'dev-owner' });
  agent.seed(cwd, id, 'Owned by the agent');
  agent.watch(cwd, [id]);
  agent.start();
  const owned = await until('the session to sync', 30_000, async () => {
    const s = await stack.session(id);
    return s && s.lineCount > 0 ? s : null;
  });
  expect(owned.ownerDeviceId).toBe('dev-owner');

  // A second device appends at the byte the core holds: the owner check comes first.
  const intruder = await RawDevice.connect(stack, 'dev-intruder');
  const refused = await intruder.append(id, owned.byteSize, line(1));
  expect(refused).toMatchObject({
    type: 'session.rejected',
    reason: 'not-owner',
    ownerDeviceId: 'dev-owner',
  });
  // At a wrong offset too: not-owner, never a resync that would reveal the cursor.
  const refusedAgain = await intruder.append(id, 3, line(2));
  expect(refusedAgain).toMatchObject({ type: 'session.rejected', reason: 'not-owner' });
  const after = await stack.session(id);
  expect(after?.byteSize).toBe(owned.byteSize);
  expect(after?.lineCount).toBe(owned.lineCount);
  expect(after?.ownerDeviceId).toBe('dev-owner');
  intruder.close();

  // The owner of a session of its own: a wrong offset is answered with the cursor, a right one with an ack.
  const writer = await RawDevice.connect(stack, 'dev-writer');
  const fresh = '22222222-3333-4444-8555-666666666605';
  const first = line(1);
  expect(await writer.append(fresh, 0, first)).toMatchObject({
    type: 'session.ack',
    byteSize: Buffer.byteLength(first),
    lineCount: 1,
  });
  expect(await writer.append(fresh, 0, line(2))).toMatchObject({
    type: 'session.resync',
    byteSize: Buffer.byteLength(first),
  });
  expect(await writer.append(fresh, Buffer.byteLength(first) + 7, line(2))).toMatchObject({
    type: 'session.resync',
    byteSize: Buffer.byteLength(first),
  });
  const stored = await stack.session(fresh);
  expect(stored?.byteSize).toBe(Buffer.byteLength(first));
  writer.close();
});
