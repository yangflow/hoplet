// End to end: the real Go agent, an in-process core and the scripted fake `claude` executable
// (agent/internal/runtime/fakeclaude) walk a session through every runtime state of docs/spec.md.
import { type ChildProcess, spawn, spawnSync } from 'node:child_process';
import {
  appendFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';

vi.setConfig({ testTimeout: 60_000 });

import { createApi } from '../../core/src/api/index.ts';
import { createControl } from '../../core/src/control/index.ts';
import { attachIngest } from '../../core/src/ingest/index.ts';
import { Store } from '../../core/src/store/index.ts';
import { PROTOCOL_VERSION, RuntimeCommandResultSchema } from '../../shared/src/index.ts';
import { HostClient } from '../hoplet/client.ts';

const HOST = 'runtime-e2e-host-token';
const SESSION = '11111111-2222-4333-8444-5555555555aa';

type Msg = Record<string, unknown>;

/** The browser socket of the web UI, keeping every message. */
class Browser {
  readonly got: Msg[] = [];
  readonly ws: WebSocket;
  constructor(ws: WebSocket) {
    this.ws = ws;
    ws.addEventListener('message', (ev) => this.got.push(JSON.parse(String(ev.data)) as Msg));
  }
  send(msg: Msg): void {
    this.ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
  }
  states(): string[] {
    return this.got.filter((m) => m.type === 'session.runtime').map((m) => String(m.state));
  }
  async until(what: string, check: () => boolean, ms = 15_000): Promise<void> {
    const deadline = Date.now() + ms;
    while (!check()) {
      if (Date.now() > deadline) {
        throw new Error(`timed out waiting for ${what}; states ${this.states().join(',')}`);
      }
      await new Promise((r) => setTimeout(r, 10));
    }
  }
  /** Waits until the last reported state is `state` and returns the number of messages seen by then. */
  async state(state: string): Promise<void> {
    await this.until(`state ${state}`, () => this.states().at(-1) === state);
  }
  /** The last runtime state reported after `mark`. */
  lastState(mark: number): string | undefined {
    return this.since(mark)
      .filter((m) => m.type === 'session.runtime')
      .at(-1)?.state as string | undefined;
  }
  mark(): number {
    return this.got.length;
  }
  since(mark: number): Msg[] {
    return this.got.slice(mark);
  }
}

let root = '';
let server: Server;
let agent: ChildProcess;
let store: Store;
let control: ReturnType<typeof createControl>;
let client: HostClient;
let browser: Browser;
let native = '';
let project = '';
const agentLog: string[] = [];

const nativeSize = () => readFileSync(native).byteLength;

async function synced(): Promise<void> {
  await browser.until('the session file to be synced', () => {
    return store.getSession(SESSION)?.byteSize === nativeSize();
  });
}

beforeAll(async () => {
  root = mkdtempSync(join(tmpdir(), 'hoplet-runtime-e2e-'));
  project = join(root, 'project');
  mkdirSync(project);
  const run = (args: string[]) => {
    const r = spawnSync('go', args, { cwd: resolve('agent'), encoding: 'utf8' });
    expect(r.status, `go ${args.join(' ')}: ${r.stderr}`).toBe(0);
  };
  const agentBin = join(root, 'hoplet-agent');
  mkdirSync(join(root, 'fake-bin'));
  const fakeBin = join(root, 'fake-bin', 'claude');
  run(['build', '-o', agentBin, './cmd/hoplet-agent']);
  run(['build', '-o', fakeBin, './internal/runtime/fakeclaude']);

  store = new Store(join(root, 'data'));
  control = createControl(store);
  server = createServer();
  attachIngest(server, store, control);
  createApi(
    store,
    { port: 0, dataDir: join(root, 'data'), token: HOST, webDir: null },
    control,
  ).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  client = new HostClient({ baseUrl: base, token: HOST });

  const home = join(root, 'home');
  const projects = join(root, 'claude');
  const nativeDir = join(projects, project.replaceAll('/', '-'));
  mkdirSync(home);
  mkdirSync(nativeDir, { recursive: true });
  native = join(nativeDir, `${SESSION}.jsonl`);
  writeFileSync(
    native,
    `${JSON.stringify({ type: 'user', cwd: project, sessionId: SESSION, message: { role: 'user', content: 'start' } })}\n`,
  );
  const minted = store.mintDevice('runtime-e2e', 'runtime-e2e');
  const conf = join(home, 'config.json');
  writeFileSync(
    conf,
    JSON.stringify({
      server_url: base,
      token: minted.token,
      device_id: 'runtime-e2e',
      device_name: 'runtime-e2e',
      hoplet_home: home,
      claude_projects: projects,
      claude_bin: fakeBin,
      exclude_prefixes: [],
      poll_interval_ms: 50,
    }),
  );
  writeFileSync(
    join(home, 'projects.json'),
    JSON.stringify({ projects: [{ path: project, imported: [SESSION] }] }),
  );
  agent = spawn(agentBin, ['--config', conf, 'daemon'], {
    env: { ...process.env, FAKE_CLAUDE_PROJECTS: projects },
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  agent.stderr?.on('data', (d) => agentLog.push(String(d)));

  const deadline = Date.now() + 30_000;
  while (
    !(control.ownerOnline('runtime-e2e') && store.getSession(SESSION)?.byteSize === nativeSize())
  ) {
    if (Date.now() > deadline) throw new Error(`the agent did not sync: ${agentLog.join('')}`);
    await new Promise((r) => setTimeout(r, 25));
  }
  const ticket = await fetch(`${base}/api/ws-tickets`, {
    method: 'POST',
    headers: { authorization: `Bearer ${HOST}` },
  }).then((r) => r.json() as Promise<{ ticket: string }>);
  const ws = new WebSocket(`${base.replace('http', 'ws')}/ws?ticket=${ticket.ticket}`);
  browser = new Browser(ws);
  await new Promise<void>((r) => ws.addEventListener('open', () => r(), { once: true }));
  browser.send({ type: 'subscribe', sessionId: SESSION });
}, 120_000);

afterAll(async () => {
  browser?.ws.close();
  if (agent && agent.exitCode === null) {
    const exited = new Promise<void>((r) => agent.once('exit', () => r()));
    agent.kill('SIGTERM');
    await exited;
  }
  await new Promise<void>((r) => (server ? server.close(() => r()) : r()));
  rmSync(root, { recursive: true, force: true });
});

describe('remote control against a scripted Claude Code', () => {
  it('starts idle with the capabilities of the Claude driver', async () => {
    await browser.state('idle');
    expect(control.runtimeOf(SESSION).state).toBe('idle');
  });

  it('runs a prompt: starting, running, streamed output, then the stored turn', async () => {
    const mark = browser.mark();
    const accepted = await client.prompt(SESSION, 'say hello from the fake');
    expect(accepted.ok).toBe(true);
    await browser.until('a ready session after the turn', () => {
      const states = browser.since(mark).filter((m) => m.type === 'session.runtime');
      return states.at(-1)?.state === 'ready';
    });
    const events = browser.since(mark);
    const states = events.filter((m) => m.type === 'session.runtime').map((m) => m.state);
    expect(states.slice(0, 3)).toEqual(['starting', 'running', 'ready']);
    expect(events.find((m) => m.type === 'session.runtime')).toMatchObject({
      capabilities: {
        permissions: true,
        interrupt: true,
        thinking: true,
        fast: true,
        models: ['fable', 'opus', 'sonnet'],
      },
    });

    const outputs = events.filter((m) => m.type === 'session.output');
    expect(
      outputs
        .filter((m) => m.kind === 'text')
        .map((m) => m.text)
        .join(''),
    ).toBe('hello from the fake');
    expect(outputs.some((m) => m.kind === 'thinking' && m.tokens === 50)).toBe(true);
    expect(JSON.stringify(events)).not.toContain('SIGNATURE');

    // Claude Code wrote the turn to the session file; sync stored it byte for byte.
    await synced();
    const stored = readFileSync(native, 'utf8');
    expect(stored).toContain('say hello from the fake');
    expect(store.getSession(SESSION)?.byteSize).toBe(nativeSize());
  });

  it('accepts a prompt within the budget while the process is ready', async () => {
    await browser.state('ready');
    const mark = browser.mark();
    const t0 = performance.now();
    const accepted = await client.prompt(SESSION, 'say again');
    const acceptedAfter = performance.now() - t0;
    expect(accepted.ok).toBe(true);
    // docs/spec.md: POST to `running` with the process `ready`, 300 ms.
    expect(acceptedAfter).toBeLessThan(300);
    await browser.until('running', () => browser.since(mark).some((m) => m.state === 'running'));
    await browser.until('ready', () => browser.lastState(mark) === 'ready');
    await synced();
  });

  it('asks for permission, runs the tool after allow, and writes the turn', async () => {
    await browser.state('ready');
    const mark = browser.mark();
    expect((await client.prompt(SESSION, 'tool echo allowed')).ok).toBe(true);
    await browser.until('a permission request', () =>
      browser.since(mark).some((m) => m.type === 'session.permission'),
    );
    await browser.state('waiting-permission');
    const request = browser.since(mark).find((m) => m.type === 'session.permission') as Msg;
    expect(request).toMatchObject({ tool: 'Bash', input: { command: 'echo allowed' } });
    expect(control.runtimeOf(SESSION).state).toBe('waiting-permission');

    const answer = await client.request(
      'POST',
      `/api/sessions/${SESSION}/permission`,
      RuntimeCommandResultSchema,
      { requestId: request.requestId, decision: 'allow' },
    );
    expect(answer.ok).toBe(true);
    await browser.until('ready after the allowed tool', () => {
      const states = browser.since(mark).filter((m) => m.type === 'session.runtime');
      return states.at(-1)?.state === 'ready' && states.some((m) => m.state === 'running');
    });
    const outputs = browser.since(mark).filter((m) => m.type === 'session.output');
    expect(outputs.find((m) => m.kind === 'tool-call')).toMatchObject({
      tool: 'Bash',
      input: { command: 'echo allowed' },
    });
    expect(outputs.find((m) => m.kind === 'tool-result')).toMatchObject({
      text: 'ran: echo allowed',
    });
    expect(outputs.find((m) => m.kind === 'tool-result')?.isError).toBeFalsy();
    await synced();
    expect(readFileSync(native, 'utf8')).toContain('DONE');
  });

  it('continues the turn after a deny', async () => {
    await browser.state('ready');
    const mark = browser.mark();
    expect((await client.prompt(SESSION, 'tool rm -rf nothing')).ok).toBe(true);
    await browser.until('a permission request', () =>
      browser.since(mark).some((m) => m.type === 'session.permission'),
    );
    const request = browser.since(mark).find((m) => m.type === 'session.permission') as Msg;
    expect(
      (
        await client.request(
          'POST',
          `/api/sessions/${SESSION}/permission`,
          RuntimeCommandResultSchema,
          {
            requestId: request.requestId,
            decision: 'deny',
          },
        )
      ).ok,
    ).toBe(true);
    // A second answer to the same request is refused: the first one won.
    const second = await client.request(
      'POST',
      `/api/sessions/${SESSION}/permission`,
      RuntimeCommandResultSchema,
      { requestId: request.requestId, decision: 'allow' },
    );
    expect(second).toMatchObject({ ok: false, error: { status: 409, code: 'request-closed' } });
    await browser.until('ready after the deny', () => browser.lastState(mark) === 'ready');
    const outputs = browser.since(mark).filter((m) => m.type === 'session.output');
    expect(outputs.find((m) => m.kind === 'tool-result')).toMatchObject({ isError: true });
    expect(
      outputs
        .filter((m) => m.kind === 'text')
        .map((m) => m.text)
        .join(''),
    ).toBe('REFUSED');
    await synced();
  });

  it('interrupts a running turn and keeps the process', async () => {
    await browser.state('ready');
    const mark = browser.mark();
    expect((await client.prompt(SESSION, 'sleep')).ok).toBe(true);
    await browser.until('running', () => browser.since(mark).some((m) => m.state === 'running'));
    const interrupted = await client.interrupt(SESSION);
    expect(interrupted).toEqual({ ok: true, data: { ok: true } });
    await browser.until('ready after the interrupt', () => browser.lastState(mark) === 'ready');
    const states = browser
      .since(mark)
      .filter((m) => m.type === 'session.runtime')
      .map((m) => m.state);
    expect(states).not.toContain('failed');
    expect(states).not.toContain('idle');
    // Nothing to interrupt now.
    expect(await client.interrupt(SESSION)).toMatchObject({
      ok: false,
      error: { code: 'not-running' },
    });
    await synced();
  });

  it('shows a failed turn and recovers on the next prompt', async () => {
    await browser.state('ready');
    const mark = browser.mark();
    expect((await client.prompt(SESSION, 'fail')).ok).toBe(true);
    await browser.until('failed', () => browser.since(mark).some((m) => m.state === 'failed'));
    const failed = browser.since(mark).find((m) => m.state === 'failed') as Msg;
    expect(String(failed.error)).toContain('boom');
    expect((await client.prompt(SESSION, 'say recovered')).ok).toBe(true);
    await browser.until('ready', () => browser.lastState(mark) === 'ready');
    await synced();
  });

  it('stops the process on request', async () => {
    await browser.state('ready');
    const mark = browser.mark();
    const res = await fetch(`${client.baseUrl}/api/sessions/${SESSION}/stop`, {
      method: 'POST',
      headers: { authorization: `Bearer ${HOST}` },
    });
    expect(res.status).toBe(200);
    await browser.until('idle after the stop', () => browser.lastState(mark) === 'idle');
  });

  it('turns external when a terminal writes the session, and refuses prompts', async () => {
    // A new process first, so the agent knows the size Hoplet's own process left.
    expect((await client.prompt(SESSION, 'say before the terminal')).ok).toBe(true);
    await browser.state('ready');
    await synced();
    const mark = browser.mark();
    appendFileSync(
      native,
      `${JSON.stringify({ type: 'user', cwd: project, message: { role: 'user', content: 'typed in a terminal' } })}\n`,
    );
    await browser.until('external', () => browser.since(mark).some((m) => m.state === 'external'));
    expect(control.runtimeOf(SESSION).state).toBe('external');
    const refused = await client.prompt(SESSION, 'say no');
    expect(refused).toMatchObject({ ok: false, error: { status: 409, code: 'external' } });
    await synced();
  });
});
