import { type ChildProcess, spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { appendFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { expect, test } from 'vitest';
import { createApi } from '../../core/src/api/index.ts';
import { createControl } from '../../core/src/control/index.ts';
import { attachIngest } from '../../core/src/ingest/index.ts';
import { Store } from '../../core/src/store/index.ts';

const MiB = 1024 * 1024;

async function until(check: () => boolean, ms = 60_000): Promise<void> {
  const deadline = Date.now() + ms;
  while (!check()) {
    if (Date.now() > deadline) throw new Error('condition timed out');
    await new Promise((r) => setTimeout(r, 50));
  }
}

async function stop(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null) return;
  const exited = new Promise<void>((r) => child.once('exit', () => r()));
  child.kill('SIGTERM');
  await exited;
}

/** One JSONL line of exactly `bytes` bytes including the LF, with incompressible content. */
function lineOf(bytes: number, cwd: string): string {
  const head = JSON.stringify({ type: 'user', cwd, message: { content: '' } });
  const pad = bytes - Buffer.byteLength(head) - 1;
  const text = randomBytes(Math.ceil((pad * 3) / 4))
    .toString('base64')
    .slice(0, pad);
  const line = `${JSON.stringify({ type: 'user', cwd, message: { content: text } })}\n`;
  return line.length === bytes ? line : lineOf(bytes, cwd);
}

const small = (n: number, cwd: string) =>
  `${JSON.stringify({ type: 'user', cwd, message: { role: 'user', content: `turn ${n}` } })}\n`;

test('lines of 1.2 MB, 20 MiB and 32 MiB sync byte-exactly and a line above 32 MiB blocks only its session', async () => {
  const root = mkdtempSync(join(tmpdir(), 'hoplet-long-lines-'));
  const server = createServer();
  let agent: ChildProcess | undefined;
  const errors: string[] = [];
  try {
    const project = join(root, 'project');
    mkdirSync(project);
    const binary = join(root, 'hoplet-agent');
    const built = spawnSync('go', ['build', '-o', binary, './cmd/hoplet-agent'], {
      cwd: resolve('agent'),
      encoding: 'utf8',
    });
    expect(built.status, built.stderr).toBe(0);

    const store = new Store(join(root, 'data'));
    const config = { port: 0, dataDir: join(root, 'data'), token: 'synthetic-host', webDir: null };
    const control = createControl(store);
    attachIngest(server, store, control);
    createApi(store, config, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;

    const longId = '11111111-2222-4333-8444-5555555555a1';
    const blockedId = '11111111-2222-4333-8444-5555555555a2';
    const otherId = '11111111-2222-4333-8444-5555555555a3';
    const home = join(root, 'home');
    const projects = join(root, 'claude');
    const nativeDir = join(projects, project.replaceAll('/', '-'));
    mkdirSync(home);
    mkdirSync(nativeDir, { recursive: true });

    const long = [
      small(0, project),
      lineOf(1_200_000, project),
      small(1, project),
      lineOf(20 * MiB, project),
      small(2, project),
      lineOf(32 * MiB, project),
      small(3, project),
    ].join('');
    writeFileSync(join(nativeDir, `${longId}.jsonl`), long);
    const blockedHead = small(0, project);
    const blockedLine = `${'x'.repeat(32 * MiB)}\n`;
    writeFileSync(join(nativeDir, `${blockedId}.jsonl`), blockedHead + blockedLine);
    const other = small(0, project);
    writeFileSync(join(nativeDir, `${otherId}.jsonl`), other);

    const minted = store.mintDevice('synthetic', 'long-lines');
    const conf = join(home, 'config.json');
    writeFileSync(
      conf,
      JSON.stringify({
        server_url: base,
        token: minted.token,
        device_id: 'long-lines',
        device_name: 'synthetic',
        hoplet_home: home,
        claude_projects: projects,
        codex_home: `${home}/codex`,
        exclude_prefixes: [],
        poll_interval_ms: 100,
      }),
    );
    writeFileSync(
      join(home, 'projects.json'),
      JSON.stringify({ projects: [{ path: project, imported: [longId, blockedId, otherId] }] }),
    );
    agent = spawn(binary, ['--config', conf, 'daemon'], {
      env: { ...process.env, HOME: home },
      stdio: ['ignore', 'ignore', 'pipe'],
    });
    agent.stderr?.on('data', (d) => errors.push(String(d)));

    await until(
      () =>
        store.getSession(longId)?.byteSize === Buffer.byteLength(long) &&
        store.getSession(blockedId)?.byteSize === Buffer.byteLength(blockedHead) &&
        store.getSession(otherId)?.byteSize === Buffer.byteLength(other),
    ).catch(() => {
      throw new Error(
        `sync incomplete: ${store.listSessions(5).map((s) => `${s.sessionId.slice(-2)}=${s.byteSize}`)} ${errors.join('').slice(-500)}`,
      );
    });
    expect(await store.streamFingerprint(longId)).toEqual({
      byteSize: Buffer.byteLength(long),
      streamSha256: createHash('sha256').update(long).digest('hex'),
    });
    expect(store.getSession(longId)?.lineCount).toBe(7);

    // The blocked session stays at its offset while another session keeps syncing.
    const next = small(1, project);
    appendFileSync(join(nativeDir, `${otherId}.jsonl`), next);
    await until(() => store.getSession(otherId)?.byteSize === Buffer.byteLength(other + next));
    await new Promise((r) => setTimeout(r, 500));
    expect(store.getSession(blockedId)?.byteSize).toBe(Buffer.byteLength(blockedHead));
    const log = errors.join('');
    const reports = log.split('\n').filter((l) => l.includes('exceeds supported limit'));
    expect(reports).toHaveLength(1);
    expect(reports[0]).toContain(`offset ${Buffer.byteLength(blockedHead)}, limit ${32 * MiB}`);
    expect(log).not.toContain('xxxxxxxx');
  } finally {
    if (agent) await stop(agent);
    if (server.listening) await new Promise<void>((r) => server.close(() => r()));
    rmSync(root, { recursive: true, force: true });
  }
}, 180_000);
