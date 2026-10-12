// Benchmark harness: `pnpm bench [--fixtures 23,128,256] [--live-hours 8] [--claude <path>] [--keep]`.
// Starts a core from source and a current-source agent with isolated state, syncs generated fixtures and
// measures the budgets of docs/spec.md#technical-baseline. The agent's `claude_bin` is the scripted double
// (agent/internal/runtime/fakeclaude) unless `--claude` names a real Claude Code binary; the runtime event
// and live-session budgets need the double. A browser WebSocket client (the global `WebSocket`) observes
// the runtime states the way the web UI does.
import { type ChildProcess, execFileSync, spawn } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdirSync, mkdtempSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { arch, cpus, platform, release, tmpdir, totalmem } from 'node:os';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { chromium } from '@playwright/test';
import { PROTOCOL_VERSION } from '../../shared/src/index.ts';
import { generateFixture, MiB, NEEDLE } from './fixture.ts';

const ROOT = resolve(import.meta.dirname, '../..');
const TOKEN = `bench-${Math.random().toString(36).slice(2)}`;
const CONTROL_BUDGET_MS = 300;
const EVENT_BUDGET_MS = 150;
const START_BUDGET_MS = 5000;
const LIVE_ITEMS_BUDGET = 1000;
const LIVE_HEAP_BUDGET_MIB = 300;
/** The assumed pace of a working day for the live-session budget: turns per simulated hour. */
const LIVE_TURNS_PER_HOUR = 30;

type Status = 'pass' | 'fail' | 'n.a.' | 'info';
type Row = { measure: string; value: string; budget: string; status: Status; raw?: unknown };

const rows: Row[] = [];
const procs = new Set<ChildProcess>();
let tmp = '';
let claudeBin = '';
/** True when `claude_bin` is the scripted double, which understands the prompt words of its header. */
let fake = true;

function add(measure: string, value: string, budget: string, status: Status, raw?: unknown): void {
  rows.push({ measure, value, budget, status, raw });
  console.log(`  ${status.padEnd(4)}  ${measure}: ${value} (budget ${budget})`);
}
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const ms = (n: number) => `${n.toFixed(n < 10 ? 1 : 0)} ms`;
const median = (a: number[]) => [...a].sort((x, y) => x - y)[Math.floor(a.length / 2)] ?? 0;
const pct = (a: number[], p: number) =>
  [...a].sort((x, y) => x - y)[Math.min(a.length - 1, Math.floor(a.length * p))] ?? 0;

async function freePort(): Promise<number> {
  return new Promise((ok, fail) => {
    const s = createServer();
    s.once('error', fail);
    s.listen(0, '127.0.0.1', () => {
      const port = (s.address() as { port: number }).port;
      s.close(() => ok(port));
    });
  });
}

function stop(p: ChildProcess): Promise<void> {
  return new Promise((done) => {
    if (p.exitCode !== null || p.signalCode !== null) {
      procs.delete(p);
      return done();
    }
    const t = setTimeout(() => p.kill('SIGKILL'), 5000);
    p.once('exit', () => {
      clearTimeout(t);
      procs.delete(p);
      done();
    });
    p.kill('SIGTERM');
  });
}

async function stopAll(): Promise<void> {
  await Promise.all([...procs].map(stop));
}

function run(
  cmd: string,
  args: string[],
  opts: { cwd: string; env: Record<string, string>; log: string },
) {
  const out = spawn(cmd, args, { cwd: opts.cwd, env: opts.env, stdio: ['ignore', 'pipe', 'pipe'] });
  procs.add(out);
  const lines: string[] = [];
  const keep = (b: Buffer) => {
    for (const l of b.toString().split('\n')) if (l) lines.push(l);
    if (lines.length > 200) lines.splice(0, lines.length - 200);
  };
  out.stdout?.on('data', keep);
  out.stderr?.on('data', keep);
  return { proc: out, lines, name: opts.log };
}

type Api = {
  get(
    path: string,
  ): Promise<{ ms: number; bytes: number; status: number; json: unknown; encoding: string | null }>;
  post(path: string, body: unknown): Promise<unknown>;
};

function api(base: string): Api {
  const headers = { authorization: `Bearer ${TOKEN}`, 'accept-encoding': 'identity' };
  return {
    async get(path) {
      const t0 = performance.now();
      const res = await fetch(base + path, { headers });
      const buf = new Uint8Array(await res.arrayBuffer());
      const elapsed = performance.now() - t0;
      let json: unknown = null;
      try {
        json = JSON.parse(new TextDecoder().decode(buf));
      } catch {}
      return {
        ms: elapsed,
        bytes: buf.length,
        status: res.status,
        json,
        encoding: res.headers.get('content-encoding'),
      };
    },
    async post(path, body) {
      const res = await fetch(base + path, {
        method: 'POST',
        headers: { ...headers, 'content-type': 'application/json' },
        body: JSON.stringify(body),
      });
      return res.json();
    },
  };
}

type Msg = {
  type: string;
  sessionId?: string;
  state?: string;
  kind?: string;
  text?: string;
  requestId?: string;
};

/** A browser WebSocket client: the runtime states and streamed output a web UI window would receive. */
class Browser {
  readonly state = new Map<string, string>();
  private readonly taps = new Set<(m: Msg, at: number) => void>();
  private ws!: WebSocket;

  static async open(base: string, a: Api): Promise<Browser> {
    const { ticket } = (await a.post('/api/ws-tickets', {})) as { ticket: string };
    const b = new Browser();
    b.ws = new WebSocket(`${base.replace('http', 'ws')}/ws?ticket=${ticket}`);
    b.ws.onmessage = (ev) => {
      const at = performance.now();
      let m: Msg;
      try {
        m = JSON.parse(String(ev.data)) as Msg;
      } catch {
        return;
      }
      if (m.type === 'session.runtime' && m.sessionId && m.state) b.state.set(m.sessionId, m.state);
      for (const tap of [...b.taps]) tap(m, at);
    };
    await new Promise<void>((ok, fail) => {
      b.ws.onopen = () => ok();
      b.ws.onerror = () => fail(new Error('browser websocket failed'));
    });
    return b;
  }

  /** The core answers with the session's `session.runtime` and sends its streamed output from now on. */
  subscribe(sessionId: string): void {
    this.ws.send(JSON.stringify({ v: PROTOCOL_VERSION, type: 'subscribe', sessionId }));
  }

  tap(fn: (m: Msg, at: number) => void): () => void {
    this.taps.add(fn);
    return () => this.taps.delete(fn);
  }

  /** The arrival time of the next `session.runtime` that puts the session in `state`. */
  next(sessionId: string, state: string, timeoutMs: number): Promise<number> {
    return new Promise((ok, fail) => {
      const timer = setTimeout(() => {
        off();
        fail(
          new Error(
            `${sessionId} not ${state} within ${timeoutMs} ms (${this.state.get(sessionId) ?? 'unknown'})`,
          ),
        );
      }, timeoutMs);
      const off = this.tap((m, at) => {
        if (m.type !== 'session.runtime' || m.sessionId !== sessionId || m.state !== state) return;
        clearTimeout(timer);
        off();
        ok(at);
      });
    });
  }

  async until(sessionId: string, state: string, timeoutMs: number): Promise<void> {
    if (this.state.get(sessionId) !== state) await this.next(sessionId, state, timeoutMs);
  }

  close(): void {
    this.ws.close();
  }
}

/** A prompt the scripted double answers quickly; a real Claude Code gets a plain request. */
const quickPrompt = () => (fake ? 'say ok' : 'Reply with the single word ok.');

async function sendPrompt(a: Api, sessionId: string, text: string): Promise<void> {
  const res = (await a.post(`/api/sessions/${sessionId}/prompt`, {
    text,
    promptId: randomUUID(),
  })) as { ok?: boolean; code?: string };
  if (!res.ok) throw new Error(`prompt refused: ${res.code ?? 'no code'}`);
}

/** Leaves the session's process `ready`: starts one with a quick prompt unless it is ready or busy. */
async function makeReady(a: Api, ui: Browser, sessionId: string): Promise<void> {
  const state = ui.state.get(sessionId);
  if (state === 'ready') return;
  const ready = ui.next(sessionId, 'ready', 60_000);
  ready.catch(() => {});
  if (state !== 'starting' && state !== 'running') await sendPrompt(a, sessionId, quickPrompt());
  await ready;
}

/** Sends a prompt to a `ready` process and waits for the turn to end: `running`, then `ready` again. */
async function runTurn(a: Api, ui: Browser, sessionId: string, text: string): Promise<void> {
  const ended = new Promise<void>((ok, fail) => {
    let started = false;
    const timer = setTimeout(() => {
      off();
      fail(new Error(`${sessionId} turn did not end (${ui.state.get(sessionId) ?? 'unknown'})`));
    }, 90_000);
    const off = ui.tap((m) => {
      if (m.type !== 'session.runtime' || m.sessionId !== sessionId) return;
      if (m.state === 'running') started = true;
      else if (started && m.state === 'ready') {
        clearTimeout(timer);
        off();
        ok();
      }
    });
  });
  ended.catch(() => {});
  await sendPrompt(a, sessionId, text);
  await ended;
}

/** `POST` of a prompt to the session's `running` on the browser WebSocket, with its process `ready`. */
async function promptAccepted(a: Api, ui: Browser, sessionId: string): Promise<number> {
  await ui.until(sessionId, 'ready', 30_000);
  const running = ui.next(sessionId, 'running', 10_000);
  running.catch(() => {});
  const t0 = performance.now();
  await sendPrompt(a, sessionId, quickPrompt());
  return (await running) - t0;
}

/** Prompts one after the other, each on the `ready` process, until `end()`; they overlap other work. */
function promptSampler(a: Api, ui: Browser, sessionId: string) {
  const samples: number[] = [];
  const errors: string[] = [];
  let live = true;
  const loop = (async () => {
    while (live) {
      try {
        samples.push(await promptAccepted(a, ui, sessionId));
      } catch (err) {
        errors.push(String(err instanceof Error ? err.message : err));
        await sleep(500);
      }
      await sleep(50);
    }
  })();
  return {
    async end() {
      live = false;
      await loop;
      return { samples, errors };
    },
  };
}

function promptRow(measure: string, r: { samples: number[]; errors: string[] }): void {
  if (r.samples.length === 0) {
    add(
      measure,
      `no prompt measured: ${r.errors[0] ?? 'no sample'}`,
      `${CONTROL_BUDGET_MS} ms`,
      'fail',
    );
    return;
  }
  const worst = Math.max(...r.samples);
  add(
    measure,
    `p50 ${ms(median(r.samples))}, max ${ms(worst)} over ${r.samples.length} prompts${r.errors.length ? `, ${r.errors.length} failed (${r.errors[0]})` : ''}`,
    `${CONTROL_BUDGET_MS} ms, max`,
    worst <= CONTROL_BUDGET_MS && r.errors.length === 0 ? 'pass' : 'fail',
    r.samples,
  );
}

/** Streams `count` timestamped deltas from the double and returns their delay to the browser socket. */
async function eventDelays(
  a: Api,
  ui: Browser,
  sessionId: string,
  count: number,
): Promise<number[]> {
  ui.subscribe(sessionId);
  await makeReady(a, ui, sessionId);
  const delays: number[] = [];
  const off = ui.tap((m) => {
    if (m.type !== 'session.output' || m.sessionId !== sessionId || m.kind !== 'text') return;
    const sentMicros = Number.parseInt(m.text ?? '', 10);
    if (!Number.isFinite(sentMicros)) return;
    delays.push(Math.max(0, Date.now() - sentMicros / 1000));
  });
  try {
    await runTurn(a, ui, sessionId, `stream ${count}`);
  } finally {
    off();
  }
  return delays;
}

/** Drops the page cache where that can be done without a prompt; false when it cannot. */
function dropPageCache(): boolean {
  const cmd =
    platform() === 'darwin' ? ['purge'] : ['sh', '-c', 'sync; echo 3 > /proc/sys/vm/drop_caches'];
  try {
    execFileSync('sudo', ['-n', ...cmd], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

function cpuSeconds(pid: number): number {
  const out = execFileSync('ps', ['-o', 'cputime=', '-p', String(pid)])
    .toString()
    .trim();
  const [m, s] = out.split(':');
  return Number(m) * 60 + Number(s);
}
function rssMiB(pid: number): number {
  return (
    Number(
      execFileSync('ps', ['-o', 'rss=', '-p', String(pid)])
        .toString()
        .trim(),
    ) / 1024
  );
}

type Env = {
  base: string;
  a: Api;
  core: ReturnType<typeof run>;
  startAgent(pollMs: number): Promise<ReturnType<typeof run>>;
};

async function startStack(
  dir: string,
  agentBin: string,
  sessionIds: string[],
  project: string,
  webDir?: string,
): Promise<Env> {
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const core = run('node', ['core/src/main.ts'], {
    cwd: ROOT,
    env: {
      PATH: process.env.PATH ?? '',
      HOPLET_TOKEN: TOKEN,
      HOPLET_DATA: join(dir, 'core-data'),
      HOPLET_PORT: String(port),
      ...(webDir ? { HOPLET_WEB_DIR: webDir } : {}),
    },
    log: 'core',
  });
  const a = api(base);
  for (let i = 0; ; i++) {
    if (core.proc.exitCode !== null)
      throw new Error(`core exited: ${core.lines.slice(-5).join('\n')}`);
    try {
      if ((await a.get('/api/health')).status === 200) break;
    } catch {}
    if (i > 100) throw new Error('core not ready');
    await sleep(100);
  }
  const minted = (await a.post('/api/devices', { name: 'bench', deviceId: 'bench-agent' })) as {
    token?: string;
  };
  if (!minted.token) throw new Error('mint device token failed');
  const home = join(dir, 'agent-home');
  mkdirSync(home, { recursive: true });
  writeFileSync(
    join(home, 'projects.json'),
    JSON.stringify({ projects: [{ path: project, imported: sessionIds }] }),
  );
  const startAgent = async (pollMs: number) => {
    writeFileSync(
      join(home, 'config.json'),
      JSON.stringify({
        server_url: base,
        token: minted.token,
        device_id: 'bench-agent',
        claude_projects: join(dir, 'claude-projects'),
        device_name: 'bench',
        poll_interval_ms: pollMs,
        claude_bin: claudeBin,
        hoplet_home: home,
        exclude_prefixes: [],
      }),
    );
    return run(agentBin, ['--config', join(home, 'config.json'), 'daemon'], {
      cwd: dir,
      env: {
        PATH: process.env.PATH ?? '',
        HOME: home,
        FAKE_CLAUDE_PROJECTS: join(dir, 'claude-projects'),
      },
      log: 'agent',
    });
  };
  return { base, a, core, startAgent };
}

type Listed = { sessionId: string; byteSize: number; lineCount: number };

async function measureFixture(mb: number, agentBin: string, keep: boolean): Promise<void> {
  const label = `${mb} MB`;
  console.log(`\n== fixture ${label}`);
  const dir = join(tmp, `f${mb}`);
  const project = join(dir, 'project');
  const sid = `00000000-0000-4000-8000-${String(mb).padStart(12, '0')}`;
  const projDir = join(dir, 'claude-projects', project.replace(/[^a-zA-Z0-9]/g, '-'));
  mkdirSync(projDir, { recursive: true });
  mkdirSync(project, { recursive: true });
  const file = join(projDir, `${sid}.jsonl`);
  let t0 = performance.now();
  const info = generateFixture({
    path: file,
    bytes: mb * MiB,
    sessionId: sid,
    cwd: project,
    seed: mb,
  });
  const fileSize = statSync(file).size;
  console.log(
    `  generated ${(fileSize / 1e6).toFixed(1)} MB, ${info.lines} lines, ${info.longLines} above 1 MiB, in ${((performance.now() - t0) / 1000).toFixed(1)} s`,
  );

  // A 1 MiB session of the same project carries the prompts: its process stays `ready` while the
  // fixture session is imported and searched.
  const ctl = `00000000-0000-4000-9000-${String(mb).padStart(12, '0')}`;
  const ctlFile = join(projDir, `${ctl}.jsonl`);
  generateFixture({ path: ctlFile, bytes: MiB, sessionId: ctl, cwd: project, seed: mb + 1 });
  const ctlSize = statSync(ctlFile).size;

  const env = await startStack(dir, agentBin, [sid, ctl], project);
  let agent: ReturnType<typeof run> | undefined;
  let ui: Browser | undefined;
  try {
    ui = await Browser.open(env.base, env.a);
    ui.subscribe(ctl);
    ui.subscribe(sid);
    // Full sync, with prompts to the control session running against the same core (the import load).
    t0 = performance.now();
    agent = await env.startAgent(200);
    const sizeOf = async (id: string) =>
      (
        ((await env.a.get('/api/sessions')).json as { sessions?: Listed[] } | null)?.sessions ?? []
      ).find((s) => s.sessionId === id)?.byteSize;
    for (let i = 0; (await sizeOf(ctl)) !== ctlSize; i++) {
      if (i > 600) throw new Error('the control session did not sync');
      await sleep(100);
    }
    await makeReady(env.a, ui, ctl);
    const importPrompts = promptSampler(env.a, ui, ctl);
    let last = -1;
    let lastMove = performance.now();
    let got: Listed | undefined;
    let failure = '';
    for (;;) {
      await sleep(100);
      if (agent.proc.exitCode !== null) {
        failure = `agent exited: ${agent.lines.slice(-3).join(' | ')}`;
        break;
      }
      const list = (await env.a.get('/api/sessions')).json as { sessions?: Listed[] } | null;
      got = list?.sessions?.find((s) => s.sessionId === sid);
      if (got && got.byteSize === fileSize) break;
      const now = performance.now();
      if ((got?.byteSize ?? 0) !== last) {
        last = got?.byteSize ?? 0;
        lastMove = now;
      }
      if (agent.lines.some((l) => l.includes('exceeds supported limit'))) {
        failure = `agent refuses a line at byte ${got?.byteSize ?? 0} of ${fileSize}: sync stops`;
        break;
      }
      if (now - lastMove > 30_000) {
        failure = `stalled at ${got?.byteSize ?? 0} of ${fileSize} bytes; agent: ${agent.lines.slice(-2).join(' | ')}`;
        break;
      }
      if (now - t0 > 20 * 60_000) {
        failure = 'timeout after 20 min';
        break;
      }
    }
    const syncMs = performance.now() - t0;
    const importRun = await importPrompts.end();
    if (failure) {
      add(`${label} full sync`, failure, 'complete, byte_size = file size', 'fail');
      return;
    }
    add(
      `${label} full sync`,
      `${(syncMs / 1000).toFixed(1)} s, ${(fileSize / 1e6 / (syncMs / 1000)).toFixed(1)} MB/s, server byte_size ${got?.byteSize === fileSize ? '=' : '!='} file size`,
      'complete, byte_size = file size',
      got?.byteSize === fileSize ? 'pass' : 'fail',
      { syncMs, fileSize, byteSize: got?.byteSize, lines: info.lines },
    );
    promptRow(`${label} prompt accepted during import (ready process)`, importRun);

    // The same prompt with nothing else running, and the delay of streamed events to the browser socket.
    await makeReady(env.a, ui, ctl);
    const idleRun = { samples: [] as number[], errors: [] as string[] };
    for (let i = 0; i < 10; i++) {
      try {
        idleRun.samples.push(await promptAccepted(env.a, ui, ctl));
      } catch (err) {
        idleRun.errors.push(String(err instanceof Error ? err.message : err));
      }
      await sleep(100);
    }
    promptRow(`${label} prompt accepted, idle (ready process)`, idleRun);
    if (fake) {
      const delays = await eventDelays(env.a, ui, ctl, 200);
      add(
        `${label} runtime event, agent to browser`,
        delays.length
          ? `p50 ${ms(median(delays))}, p95 ${ms(pct(delays, 0.95))}, max ${ms(Math.max(...delays))} over ${delays.length} events (fake Claude Code, 10 ms apart, one machine clock, 1 ms resolution)`
          : 'no event reached the browser socket',
        `${EVENT_BUDGET_MS} ms p95`,
        delays.length >= 100 && pct(delays, 0.95) <= EVENT_BUDGET_MS ? 'pass' : 'fail',
        delays,
      );
    }

    // Open a session and an earlier page.
    const tail: number[] = [];
    let tailBytes = 0;
    let total = 0;
    let encoding: string | null = null;
    for (let i = 0; i < 5; i++) {
      const r = await env.a.get(`/api/sessions/${sid}/lines?start=-80&limit=80`);
      if (r.status !== 200) throw new Error(`lines returned ${r.status}`);
      tail.push(r.ms);
      tailBytes = r.bytes;
      encoding = r.encoding;
      total = (r.json as { total: number }).total;
    }
    add(
      `${label} open: tail page time`,
      `median ${ms(median(tail))}, max ${ms(Math.max(...tail))}`,
      '1000 ms',
      median(tail) <= 1000 ? 'pass' : 'fail',
      tail,
    );
    add(
      `${label} open: tail page bytes`,
      `${(tailBytes / 1024).toFixed(0)} KiB${encoding ? ` (${encoding})` : ''}`,
      '512 KiB',
      tailBytes <= 512 * 1024 ? 'pass' : 'fail',
      { tailBytes },
    );
    const earlier: number[] = [];
    let earlierBytes = 0;
    for (let i = 0; i < 5; i++) {
      const r = await env.a.get(`/api/sessions/${sid}/lines?start=${total - 160}&limit=80`);
      earlier.push(r.ms);
      earlierBytes = r.bytes;
    }
    add(
      `${label} earlier page time`,
      `median ${ms(median(earlier))}, max ${ms(Math.max(...earlier))} (${(earlierBytes / 1024).toFixed(0)} KiB)`,
      '300 ms',
      median(earlier) <= 300 ? 'pass' : 'fail',
      earlier,
    );

    // Idle scan: restart the agent at a 1 s poll interval with nothing to send, read its CPU over a window.
    await stop(agent.proc);
    agent = await env.startAgent(1000);
    await sleep(6000);
    const pid = agent.proc.pid;
    if (pid === undefined || agent.proc.exitCode !== null) throw new Error('agent did not restart');
    const windowS = 15;
    const c0 = cpuSeconds(pid);
    await sleep(windowS * 1000);
    const c1 = cpuSeconds(pid);
    const perScan = ((c1 - c0) / windowS) * 1000;
    add(
      `${label} agent idle scan (fixture and a 1 MiB session)`,
      `${perScan.toFixed(1)} ms CPU per scan (1 scan/s, ${windowS} s window), RSS ${rssMiB(pid).toFixed(0)} MiB; bytes read not observable without root`,
      'no session bytes read',
      'info',
      { cpuMsPerScan: perScan },
    );

    // Global search, with prompts to the control session meanwhile.
    if (mb === 256) {
      await makeReady(env.a, ui, ctl);
      const searchPrompts = promptSampler(env.a, ui, ctl);
      await sleep(200);
      const s = await env.a.get(`/api/search?q=${encodeURIComponent(NEEDLE)}&limit=30`);
      const searchRun = await searchPrompts.end();
      // The control session carries the needle too; the fixture session has the one known hit.
      const hits = (
        (s.json as { results?: { sessionId?: string }[] } | null)?.results ?? []
      ).filter((r) => r.sessionId === sid).length;
      add(
        `${label} global search`,
        `${(s.ms / 1000).toFixed(2)} s, ${hits} hit${hits === 1 ? '' : 's'}${hits === 1 ? '' : ' (expected 1)'}`,
        'none yet (#103)',
        hits === 1 ? 'info' : 'fail',
        { searchMs: s.ms, hits },
      );
      promptRow(`${label} prompt accepted during global search (ready process)`, searchRun);

      // The fixture session has no process yet: a prompt starts one, and `running` follows its `initialize`.
      const cold = dropPageCache();
      const running = ui.next(sid, 'running', 120_000);
      running.catch(() => {});
      const t1 = performance.now();
      await sendPrompt(env.a, sid, quickPrompt());
      const startMs = (await running) - t1;
      const cache = cold
        ? 'cold page cache'
        : 'warm page cache: purge needs sudo -n, not available';
      add(
        `${label} process start to initialize answered`,
        `${(startMs / 1000).toFixed(2)} s, ${cache}${fake ? ' (fake Claude Code, which does not read the session file)' : ''}`,
        `${START_BUDGET_MS / 1000} s, cold page cache`,
        startMs > START_BUDGET_MS ? 'fail' : cold ? 'pass' : 'info',
        { startMs, cold, fake },
      );
    }

    if (mb === 128) {
      await measureHandoff(label, dir, env.base, env.a, agentBin, sid, project, fileSize);
    }
  } finally {
    ui?.close();
    await stopAll();
    if (!keep) rmSync(dir, { recursive: true, force: true });
  }
}

const GIT_ENV = {
  PATH: process.env.PATH ?? '',
  GIT_CONFIG_NOSYSTEM: '1',
  GIT_CONFIG_GLOBAL: '/dev/null',
  GIT_TERMINAL_PROMPT: '0',
  GIT_AUTHOR_NAME: 'Bench',
  GIT_AUTHOR_EMAIL: 'bench@example.invalid',
  GIT_COMMITTER_NAME: 'Bench',
  GIT_COMMITTER_EMAIL: 'bench@example.invalid',
};
const git = (cwd: string, ...args: string[]) =>
  execFileSync('git', args, { cwd, env: GIT_ENV, stdio: ['ignore', 'pipe', 'pipe'] })
    .toString()
    .trim();

/**
 * Online handoff of the fixture session with a 10 MiB workspace snapshot to a second agent with its own
 * clone: from the request to the response, which comes after the target proved its copy and the commit.
 */
async function measureHandoff(
  label: string,
  dir: string,
  base: string,
  a: Api,
  agentBin: string,
  sid: string,
  project: string,
  fileSize: number,
): Promise<void> {
  const origin = join(dir, 'origin.git');
  git(dir, 'init', '--bare', '-b', 'main', origin);
  git(project, 'init', '-b', 'main');
  writeFileSync(join(project, 'README.md'), 'bench\n');
  git(project, 'add', 'README.md');
  git(project, 'commit', '-m', 'seed');
  git(project, 'remote', 'add', 'origin', origin);
  git(project, 'push', 'origin', 'HEAD:main');
  const clone = join(dir, 'target', 'repo');
  mkdirSync(join(dir, 'target'), { recursive: true });
  git(join(dir, 'target'), 'clone', origin, clone);
  // Uncommitted work: a 10 MiB file that does not compress, and an edit.
  writeFileSync(join(project, 'payload.bin'), randomBytes(10 * MiB));
  writeFileSync(join(project, 'README.md'), 'bench\nedited\n');

  const minted = (await a.post('/api/devices', {
    name: 'bench-target',
    deviceId: 'bench-target',
  })) as {
    token?: string;
  };
  if (!minted.token) throw new Error('mint target device token failed');
  const home = join(dir, 'target-home');
  mkdirSync(home, { recursive: true });
  writeFileSync(
    join(home, 'projects.json'),
    JSON.stringify({ projects: [{ path: clone, imported: [] }] }),
  );
  writeFileSync(
    join(home, 'config.json'),
    JSON.stringify({
      server_url: base,
      token: minted.token,
      device_id: 'bench-target',
      claude_projects: join(dir, 'target-claude'),
      device_name: 'bench-target',
      poll_interval_ms: 200,
      hoplet_home: home,
      exclude_prefixes: [],
    }),
  );
  const target = run(agentBin, ['--config', join(home, 'config.json'), 'daemon'], {
    cwd: dir,
    env: { PATH: process.env.PATH ?? '', HOME: home },
    log: 'target-agent',
  });
  for (let i = 0; ; i++) {
    const devices = (await a.get('/api/devices')).json as {
      devices?: { deviceId: string; connected: boolean }[];
    };
    if (devices?.devices?.some((d) => d.deviceId === 'bench-target' && d.connected)) break;
    if (target.proc.exitCode !== null || i > 200) {
      add(
        `${label} online handoff`,
        `target agent did not connect: ${target.lines.slice(-2).join(' | ')}`,
        '30 s',
        'fail',
      );
      return;
    }
    await sleep(100);
  }
  const t0 = performance.now();
  const res = (await a.post(`/api/sessions/${sid}/handoff`, {
    targetDeviceId: 'bench-target',
  })) as {
    ok?: boolean;
    code?: string;
    reason?: string;
  };
  const elapsed = performance.now() - t0;
  const moved = (
    (await a.get('/api/sessions')).json as {
      sessions?: { sessionId: string; ownerDeviceId?: string; byteSize: number }[];
    }
  )?.sessions?.find((s) => s.sessionId === sid);
  const ok =
    res.ok === true && moved?.ownerDeviceId === 'bench-target' && moved.byteSize === fileSize;
  add(
    `${label} online handoff with a 10 MiB snapshot`,
    ok
      ? `${(elapsed / 1000).toFixed(1)} s (session ${(fileSize / 1e6).toFixed(0)} MB moved and verified, owner is the target)`
      : `failed after ${(elapsed / 1000).toFixed(1)} s: ${res.code ?? 'no code'} ${res.reason ?? ''} ${target.lines.slice(-2).join(' | ')}`,
    '30 s',
    ok && elapsed <= 30_000 ? 'pass' : 'fail',
    { handoffMs: elapsed, response: res },
  );
}

/** A reply of about 800 characters, different for every turn. */
function replyText(turn: number): string {
  const words = ['cursor', 'chunk', 'offset', 'ledger', 'page', 'block', 'cache', 'roller', 'sync'];
  let out = `[t${turn}]`;
  for (let i = 0; out.length < 800; i++) out += ` ${words[(turn * 7 + i * 3) % words.length]}`;
  return out;
}

/**
 * The web UI after a live session: `hours` of work compressed into back-to-back turns (LIVE_TURNS_PER_HOUR
 * an hour; every sixth turn runs a tool that the bench allows). Chromium has the session open from the
 * first turn, so streamed output, stored lines and older pages arrive as they do for a user who stays in
 * the window. Then it counts the transcript rows and live rows mounted and reads the JavaScript heap after
 * a forced collection. Wall-clock effects of eight real hours (timers, reconnects) are not simulated.
 */
async function measureLive(hours: number, agentBin: string, keep: boolean): Promise<void> {
  const turns = Math.round(hours * LIVE_TURNS_PER_HOUR);
  const measure = `Web UI after ${hours}-hour live session`;
  const budget = `${LIVE_ITEMS_BUDGET.toLocaleString('en-US')} items, ${LIVE_HEAP_BUDGET_MIB} MB heap`;
  const simulated = `${turns} turns (${LIVE_TURNS_PER_HOUR}/hour, every sixth with a permission request)`;
  console.log(`\n== live session, ${hours} h`);
  const dir = join(tmp, 'live');
  const project = join(dir, 'project');
  const sid = '00000000-0000-4000-a000-000000000001';
  const projDir = join(dir, 'claude-projects', project.replace(/[^a-zA-Z0-9]/g, '-'));
  mkdirSync(projDir, { recursive: true });
  mkdirSync(project, { recursive: true });
  generateFixture({
    path: join(projDir, `${sid}.jsonl`),
    bytes: 2 * MiB,
    sessionId: sid,
    cwd: project,
    seed: 8,
  });

  const env = await startStack(dir, agentBin, [sid], project, join(ROOT, 'web', 'dist'));
  let ui: Browser | undefined;
  let browser: Awaited<ReturnType<typeof chromium.launch>> | undefined;
  try {
    const agent = await env.startAgent(200);
    for (let i = 0; ; i++) {
      const list = (await env.a.get('/api/sessions')).json as { sessions?: Listed[] } | null;
      if (list?.sessions?.some((s) => s.sessionId === sid && s.byteSize > 0)) break;
      if (agent.proc.exitCode !== null || i > 600) throw new Error('the live session did not sync');
      await sleep(100);
    }
    const watcher = await Browser.open(env.base, env.a);
    ui = watcher;
    watcher.subscribe(sid);
    watcher.tap((m) => {
      if (m.type === 'session.permission' && m.sessionId === sid && m.requestId) {
        void env.a.post(`/api/sessions/${sid}/permission`, {
          requestId: m.requestId,
          decision: 'allow',
        });
      }
    });

    browser = await chromium.launch();
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
    await page.addInitScript((token) => localStorage.setItem('hoplet_token', token), TOKEN);
    await page.goto(`${env.base}/?session=${sid}`);
    const conversation = page.getByTestId('cockpit.conversation');
    await conversation.waitFor({ timeout: 30_000 });

    const t0 = performance.now();
    for (let turn = 0; turn < turns; turn++) {
      await makeReady(env.a, watcher, sid);
      const text = replyText(turn);
      await runTurn(env.a, watcher, sid, turn % 6 === 5 ? `tool echo ${text}` : `say ${text}`);
      if ((turn + 1) % 40 === 0) console.log(`  ${turn + 1} / ${turns} turns`);
    }
    await conversation
      .getByText(`[t${turns - 1}]`)
      .first()
      .waitFor({ timeout: 60_000 });
    await sleep(2000);

    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Performance.enable');
    await cdp.send('HeapProfiler.collectGarbage');
    const { metrics } = await cdp.send('Performance.getMetrics');
    const heapMiB = (metrics.find((m) => m.name === 'JSHeapUsedSize')?.value ?? 0) / MiB;
    const counts = await page.evaluate(() => {
      const root = document.querySelector('[data-testid="cockpit.conversation"]');
      const live = [
        'cockpit.live-text',
        'cockpit.live-thinking',
        'cockpit.live-tool',
        'cockpit.live-result',
      ]
        .map((id) => `[data-testid="${id}"]`)
        .join(',');
      return {
        transcript: root?.querySelectorAll('.vrow').length ?? 0,
        live: root?.querySelectorAll(live).length ?? 0,
        dom: document.getElementsByTagName('*').length,
      };
    });
    const items = counts.transcript + counts.live;
    add(
      measure,
      `${items} mounted items (${counts.transcript} transcript rows, ${counts.live} live rows), JS heap ${heapMiB.toFixed(0)} MB after GC, ${counts.dom} DOM nodes; ${simulated} in ${((performance.now() - t0) / 1000).toFixed(0)} s, tail of the session in view (fake Claude Code)`,
      budget,
      items <= LIVE_ITEMS_BUDGET && heapMiB <= LIVE_HEAP_BUDGET_MIB ? 'pass' : 'fail',
      { turns, ...counts, heapMiB },
    );
  } finally {
    ui?.close();
    await browser?.close();
    await stopAll();
    if (!keep) rmSync(dir, { recursive: true, force: true });
  }
}

function machine(): Record<string, string> {
  const sh = (c: string, a: string[]) => {
    try {
      return execFileSync(c, a).toString().trim();
    } catch {
      return 'unknown';
    }
  };
  return {
    model: sh('sysctl', ['-n', 'hw.model']),
    cpu: sh('sysctl', ['-n', 'machdep.cpu.brand_string']) || cpus()[0]?.model || 'unknown',
    cores: String(cpus().length),
    ramGiB: (totalmem() / 2 ** 30).toFixed(0),
    os:
      platform() === 'darwin'
        ? `macOS ${sh('sw_vers', ['-productVersion'])}`
        : `${platform()} ${release()}`,
    arch: arch(),
    node: process.version,
    go: sh('go', ['version']),
    network: 'loopback (core and agent on one machine, not the reference LAN)',
  };
}

function table(): string {
  const w = [
    Math.max(7, ...rows.map((r) => r.measure.length)),
    Math.max(5, ...rows.map((r) => r.value.length)),
    Math.max(6, ...rows.map((r) => r.budget.length)),
  ] as const;
  const line = (a: string, b: string, c: string, d: string) =>
    `| ${a.padEnd(w[0])} | ${b.padEnd(w[1])} | ${c.padEnd(w[2])} | ${d.padEnd(6)} |`;
  return [
    line('Measure', 'Value', 'Budget', 'Result'),
    line('-'.repeat(w[0]), '-'.repeat(w[1]), '-'.repeat(w[2]), '-'.repeat(6)),
    ...rows.map((r) => line(r.measure, r.value, r.budget, r.status)),
  ].join('\n');
}

async function main(): Promise<number> {
  const { values } = parseArgs({
    options: {
      fixtures: { type: 'string', default: '23,128,256' },
      'live-hours': { type: 'string', default: '8' },
      claude: { type: 'string' },
      keep: { type: 'boolean', default: false },
    },
  });
  const sizes = values.fixtures.split(',').map(Number);
  if (!sizes.every((n) => [23, 128, 256].includes(n))) {
    console.error('--fixtures takes a comma list of 23, 128, 256');
    return 2;
  }
  const liveHours = Number(values['live-hours']);
  if (!(liveHours >= 0)) {
    console.error('--live-hours takes the hours of the live session to simulate, 0 to skip it');
    return 2;
  }
  tmp = mkdtempSync(join(tmpdir(), 'hoplet-bench-'));
  const keep = values.keep ?? false;
  console.log(`temp dir: ${tmp}`);
  console.log('building the current-source agent');
  const agentBin = join(tmp, 'hoplet-agent');
  execFileSync('go', ['build', '-o', agentBin, './cmd/hoplet-agent'], { cwd: join(ROOT, 'agent') });
  fake = !values.claude;
  if (values.claude) {
    claudeBin = resolve(values.claude);
  } else {
    console.log('building the scripted Claude Code double');
    claudeBin = join(tmp, 'claude');
    execFileSync('go', ['build', '-o', claudeBin, './internal/runtime/fakeclaude'], {
      cwd: join(ROOT, 'agent'),
    });
  }

  let crashed = '';
  try {
    for (const mb of sizes) {
      await measureFixture(mb, agentBin, keep);
    }
    if (liveHours > 0 && fake) {
      console.log('building the web UI');
      execFileSync('pnpm', ['--filter', '@hoplet/web', 'build'], { cwd: ROOT, stdio: 'ignore' });
      await measureLive(liveHours, agentBin, keep);
    }
  } catch (err) {
    crashed = String(err instanceof Error ? (err.stack ?? err.message) : err);
    console.error(crashed);
  } finally {
    await stopAll();
  }

  if (!sizes.includes(128)) {
    rows.push({
      measure: 'Online handoff, 128 MB with 10 MiB snapshot',
      value: 'needs the 128 MB fixture',
      budget: '30 s',
      status: 'n.a.',
    });
  }
  if (!sizes.includes(256)) {
    for (const [measure, budget] of [
      ['Prompt accepted during global search, 256 MB', `${CONTROL_BUDGET_MS} ms, max`],
      ['Process start to initialize answered, 256 MB, cold cache', `${START_BUDGET_MS / 1000} s`],
    ] as const) {
      rows.push({ measure, value: 'needs the 256 MB fixture', budget, status: 'n.a.' });
    }
  }
  if (!fake) {
    rows.push({
      measure: 'Runtime event, agent to browser',
      value: 'needs the scripted double, which stamps the events (not with --claude)',
      budget: `${EVENT_BUDGET_MS} ms p95`,
      status: 'n.a.',
    });
  }
  if (liveHours <= 0 || !fake) {
    rows.push({
      measure: 'Web UI after 8-hour live session',
      value: fake ? 'skipped by --live-hours 0' : 'needs the scripted double (not with --claude)',
      budget: `${LIVE_ITEMS_BUDGET.toLocaleString('en-US')} items, ${LIVE_HEAP_BUDGET_MIB} MB heap`,
      status: 'n.a.',
    });
  }

  const env = machine();
  const resultsFile = join(
    tmpdir(),
    `hoplet-bench-${new Date().toISOString().replace(/[:.]/g, '-')}.json`,
  );
  writeFileSync(
    resultsFile,
    JSON.stringify(
      { date: new Date().toISOString(), machine: env, sizes, rows, error: crashed || undefined },
      null,
      2,
    ),
  );
  console.log(`\n${table()}\n`);
  console.log(
    Object.entries(env)
      .map(([k, v]) => `${k}: ${v}`)
      .join('\n'),
  );
  console.log(`\nresults: ${resultsFile}`);
  if (keep) console.log(`kept: ${tmp}`);
  else rmSync(tmp, { recursive: true, force: true });
  return crashed ? 1 : 0;
}

for (const sig of ['SIGINT', 'SIGTERM'] as const) {
  process.on(sig, () => {
    for (const p of procs) p.kill('SIGKILL');
    if (tmp) rmSync(tmp, { recursive: true, force: true });
    process.exit(130);
  });
}
process.exit(await main());
