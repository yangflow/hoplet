// The environment of `pnpm e2e:image`: the built image (core + web UI), a real agent on the host whose
// `claude_bin` is the scripted fake (agent/internal/runtime/fakeclaude), and generated fixture sessions.
// Playwright runs the default export before the specs and the function it returns afterwards.
import { type ChildProcess, spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  freePort,
  LABEL,
  removeContainer,
  root,
  run,
  sessionLines,
  storeName,
  HOST_TOKEN as TOKEN,
  until,
} from './stack.ts';

export const DEVICE = 'e2e-mac';
export const LONG_SESSION = '11111111-2222-4333-8444-555555555551';
export const SHORT_SESSION = '11111111-2222-4333-8444-555555555552';
export const NOTES_SESSION = '11111111-2222-4333-8444-555555555553';
export const LONG_TURNS = 120;
export const LONG_TITLE = 'Trace the byte cursor catalog';
export const SHORT_TITLE = 'Rename the chunk roller';
export const NOTES_TITLE = 'Draft the release notes';

export default async function setup(): Promise<() => Promise<void>> {
  const tmp = realpathSync(mkdtempSync(join(tmpdir(), 'hoplet-e2e.')));
  const children: ChildProcess[] = [];
  let container = '';

  const teardown = async () => {
    for (const child of children) child.kill('SIGTERM');
    await Promise.all(
      children.map(
        (c) => new Promise((r) => (c.exitCode !== null ? r(null) : c.once('exit', () => r(null)))),
      ),
    );
    if (container) {
      removeContainer(container);
      container = '';
    }
    // The stacks of the other specs stop themselves; this removes what a failed spec left behind.
    const left = run('docker', ['ps', '-aq', '--filter', `label=${LABEL}=${process.pid}`]).split(
      '\n',
    );
    for (const id of left.filter(Boolean)) removeContainer(id);
    const volumes = run('docker', [
      'volume',
      'ls',
      '-q',
      '--filter',
      `name=hoplet-e2e-${process.pid}-`,
    ]);
    for (const name of volumes.split('\n').filter(Boolean))
      run('docker', ['volume', 'rm', '-f', name]);
    rmSync(tmp, { recursive: true, force: true });
  };

  try {
    // 1. The image: an existing one (E2E_IMAGE, the CI job's) or a fresh build.
    let image = process.env.E2E_IMAGE;
    if (!image) {
      image = 'hoplet:e2e';
      run('docker', ['build', '-q', '-f', 'image/Dockerfile', '-t', image, '.']);
    }

    // 2. The core, with the compose file's isolation.
    process.env.E2E_RUN = String(process.pid);
    process.env.E2E_IMAGE_REF = image;
    const port = await freePort();
    container = `hoplet-e2e-${process.pid}`;
    run('docker', [
      'run',
      '-d',
      '--rm',
      '--name',
      container,
      '--label',
      `${LABEL}=${process.pid}`,
      '-p',
      `127.0.0.1:${port}:7788`,
      '-e',
      `HOPLET_TOKEN=${TOKEN}`,
      '-e',
      'HOPLET_PORT=7788',
      '--read-only',
      '--tmpfs',
      '/tmp:size=64m',
      '--cap-drop',
      'ALL',
      '--security-opt',
      'no-new-privileges:true',
      '-v',
      '/data',
      image,
    ]);
    const url = `http://127.0.0.1:${port}`;
    const auth = { Authorization: `Bearer ${TOKEN}` };
    await until('the core', 60_000, async () => {
      const r = await fetch(`${url}/api/health`, { headers: auth });
      return r.ok ? true : null;
    });

    // 3. The fixture projects: two directories on the device, three sessions.
    const claudeProjects = join(tmp, 'claude-projects');
    const bin = join(tmp, 'bin');
    mkdirSync(bin, { recursive: true });
    const sessions: [string, string, string, number][] = [
      [LONG_SESSION, 'hoplet', LONG_TITLE, LONG_TURNS],
      [SHORT_SESSION, 'hoplet', SHORT_TITLE, 3],
      [NOTES_SESSION, 'notes', NOTES_TITLE, 3],
    ];
    const projects: Record<string, string[]> = {};
    for (const [id, dirName, title, turns] of sessions) {
      const dir = join(tmp, 'work', dirName);
      mkdirSync(dir, { recursive: true });
      const store = join(claudeProjects, storeName(dir));
      mkdirSync(store, { recursive: true });
      writeFileSync(join(store, `${id}.jsonl`), sessionLines(id, dir, title, turns));
      projects[dir] = [...(projects[dir] ?? []), id];
    }

    // 4. The agent: current source, the fake Claude Code behind claude_bin.
    run(
      'go',
      ['build', '-o', join(bin, 'hoplet-agent'), './cmd/hoplet-agent'],
      join(root, 'agent'),
    );
    run(
      'go',
      ['build', '-o', join(bin, 'claude'), './internal/runtime/fakeclaude'],
      join(root, 'agent'),
    );
    process.env.E2E_BIN = bin;
    const minted = await fetch(`${url}/api/devices`, {
      method: 'POST',
      headers: { ...auth, 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: DEVICE, deviceId: 'e2e-agent' }),
    });
    const { token: agentToken } = (await minted.json()) as { token: string };
    // What Add project may browse: a root of its own with one git repository two levels down.
    const browse = join(tmp, 'browse');
    mkdirSync(join(browse, 'projects', 'fixture-repo', '.git'), { recursive: true });
    const home = join(tmp, 'agent-home');
    mkdirSync(home, { recursive: true });
    writeFileSync(
      join(home, 'config.json'),
      JSON.stringify({
        server_url: url,
        token: agentToken,
        device_id: 'e2e-agent',
        device_name: DEVICE,
        claude_projects: claudeProjects,
        claude_bin: join(bin, 'claude'),
        browse_root: browse,
        poll_interval_ms: 300,
        hoplet_home: home,
        exclude_prefixes: [],
      }),
    );
    writeFileSync(
      join(home, 'projects.json'),
      JSON.stringify({
        projects: Object.entries(projects).map(([path, imported]) => ({ path, imported })),
      }),
    );
    const agent = spawn(
      join(bin, 'hoplet-agent'),
      ['--config', join(home, 'config.json'), 'daemon'],
      {
        env: { ...process.env, FAKE_CLAUDE_PROJECTS: claudeProjects },
        stdio: ['ignore', 'ignore', 'inherit'],
      },
    );
    children.push(agent);
    await until('the fixture sessions to sync', 60_000, async () => {
      const r = await fetch(`${url}/api/sessions`, { headers: auth });
      const { sessions: list } = (await r.json()) as { sessions: { lineCount: number }[] };
      return list.length === sessions.length && list.every((s) => s.lineCount > 0) ? true : null;
    });

    process.env.E2E_URL = url;
    process.env.E2E_TOKEN = TOKEN;
    process.env.E2E_WORK = join(tmp, 'work');
  } catch (err) {
    await teardown();
    throw err;
  }
  return teardown;
}
