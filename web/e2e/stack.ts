// What the end-to-end specs build on: a core in a container, agents on the host whose `claude_bin` is the
// scripted fake (agent/internal/runtime/fakeclaude), session fixtures, and a raw agent-protocol client.
// `image-env.ts` builds the image and the binaries once; every spec that restarts or breaks something starts a
// stack of its own with `Stack.start`.
import { type ChildProcess, execFileSync, spawn } from 'node:child_process';
import { closeSync, mkdirSync, openSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { join } from 'node:path';
import { PROTOCOL_VERSION } from '@hoplet/shared';

export const root = join(import.meta.dirname, '..', '..');
export const HOST_TOKEN = 'e2e-token';
/** The label that lets the global teardown find the containers a crashed run left behind. */
export const LABEL = 'hoplet-e2e';

export function run(cmd: string, args: string[], cwd = root): string {
  return execFileSync(cmd, args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] });
}

export async function freePort(): Promise<number> {
  return await new Promise((resolve, reject) => {
    const srv = createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const address = srv.address();
      srv.close(() => resolve(typeof address === 'object' && address ? address.port : 0));
    });
  });
}

export async function until<T>(
  what: string,
  ms: number,
  probe: () => Promise<T | null | undefined | false>,
  every = 300,
): Promise<T> {
  const deadline = Date.now() + ms;
  for (;;) {
    const value = await probe().catch(() => null);
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, every));
  }
}

export const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** A Claude Code session file: `turns` user and assistant lines, the last result carrying a 12 KiB block. */
export function sessionLines(id: string, cwd: string, title: string, turns: number): string {
  const lines: string[] = [];
  let ts = Date.now() - turns * 60_000;
  const stamp = () => {
    ts += 30_000;
    return new Date(ts).toISOString();
  };
  for (let i = 0; i < turns; i++) {
    const base = { sessionId: id, cwd, isSidechain: false, userType: 'external' };
    const prompt =
      i === 0 ? title : `Follow-up ${i}: check the ${i % 2 ? 'roller' : 'cursor'} again`;
    lines.push(
      JSON.stringify({
        ...base,
        type: 'user',
        timestamp: stamp(),
        message: { role: 'user', content: prompt },
      }),
    );
    const last = i === turns - 1;
    lines.push(
      JSON.stringify({
        ...base,
        type: 'assistant',
        timestamp: stamp(),
        message: {
          role: 'assistant',
          content: [
            { type: 'text', text: `Answer ${i}: the cursor advances by raw bytes.` },
            ...(last
              ? [
                  {
                    type: 'tool_use',
                    id: 'toolu_big',
                    name: 'Read',
                    input: { file_path: '/x/log.txt' },
                  },
                ]
              : []),
          ],
        },
      }),
    );
    if (last) {
      lines.push(
        JSON.stringify({
          ...base,
          type: 'user',
          timestamp: stamp(),
          message: {
            role: 'user',
            content: [
              {
                type: 'tool_result',
                tool_use_id: 'toolu_big',
                content: `BIGBLOCK-START ${'0123456789abcdef'.repeat(768)} BIGBLOCK-END`,
              },
            ],
          },
        }),
      );
    }
  }
  return `${lines.join('\n')}\n`;
}

/** The directory of the vendor's store for a working directory (the spec's rule, short paths only). */
export function storeName(dir: string): string {
  return dir.replace(/[^A-Za-z0-9]/g, '-');
}

let counter = 0;

export type SessionInfo = {
  sessionId: string;
  ownerDeviceId: string | null;
  ownerEpoch: number;
  byteSize: number;
  lineCount: number;
  ownerConnected: boolean;
  runtime: { state: string; error?: string };
  projectPath: string;
  title: string;
};

export type AgentOptions = {
  name: string;
  deviceId: string;
  /** Watched project path -> imported session IDs. */
  projects?: Record<string, string[]>;
  /** Added to the agent's environment, and so to the fake Claude Code it starts. */
  env?: Record<string, string>;
};

/** An agent process on this machine with a home and a Claude Code store of its own. */
export class Agent {
  readonly home: string;
  readonly claudeProjects: string;
  proc: ChildProcess | null = null;
  private readonly config: string;
  private readonly projectsFile: string;
  private readonly projects: Record<string, string[]>;

  readonly stack: Stack;
  readonly options: AgentOptions;
  readonly token: string;

  constructor(stack: Stack, options: AgentOptions, token: string) {
    this.stack = stack;
    this.options = options;
    this.token = token;
    this.home = join(stack.tmp, `agent-${options.deviceId}`);
    this.claudeProjects = join(this.home, 'claude-projects');
    mkdirSync(this.claudeProjects, { recursive: true });
    this.config = join(this.home, 'config.json');
    this.projectsFile = join(this.home, 'projects.json');
    this.projects = { ...options.projects };
    writeFileSync(
      this.config,
      JSON.stringify({
        server_url: stack.url,
        token,
        device_id: options.deviceId,
        device_name: options.name,
        claude_projects: this.claudeProjects,
        claude_bin: join(stack.bin, 'claude'),
        poll_interval_ms: 200,
        hoplet_home: this.home,
        exclude_prefixes: [],
      }),
    );
    this.writeProjects();
  }

  private writeProjects(): void {
    writeFileSync(
      this.projectsFile,
      JSON.stringify({
        projects: Object.entries(this.projects).map(([path, imported]) => ({ path, imported })),
      }),
    );
  }

  /** Watches `path` with the sessions imported; the running agent reads the file on its next scan. */
  watch(path: string, imported: string[]): void {
    this.projects[path] = [...(this.projects[path] ?? []), ...imported];
    this.writeProjects();
  }

  /** The session file Claude Code keeps for a session of the working directory `cwd`. */
  sessionFile(cwd: string, sessionId: string): string {
    return join(this.claudeProjects, storeName(realpathSync(cwd)), `${sessionId}.jsonl`);
  }

  /** Writes a fixture session into this agent's store and returns its file. */
  seed(cwd: string, sessionId: string, title: string, turns = 3): string {
    mkdirSync(cwd, { recursive: true });
    const dir = join(this.claudeProjects, storeName(realpathSync(cwd)));
    mkdirSync(dir, { recursive: true });
    const file = join(dir, `${sessionId}.jsonl`);
    writeFileSync(file, sessionLines(sessionId, cwd, title, turns));
    return file;
  }

  start(): void {
    if (this.proc && this.proc.exitCode === null && this.proc.signalCode === null) return;
    const log = openSync(join(this.home, 'agent.log'), 'a');
    this.proc = spawn(join(this.stack.bin, 'hoplet-agent'), ['--config', this.config, 'daemon'], {
      env: { ...process.env, FAKE_CLAUDE_PROJECTS: this.claudeProjects, ...this.options.env },
      stdio: ['ignore', log, log],
    });
    closeSync(log);
  }

  async kill(signal: NodeJS.Signals = 'SIGTERM'): Promise<void> {
    const proc = this.proc;
    if (!proc || proc.exitCode !== null || proc.signalCode !== null) return;
    const exited = new Promise((r) => proc.once('exit', r));
    proc.kill(signal);
    await exited;
  }

  async restart(signal: NodeJS.Signals = 'SIGKILL'): Promise<void> {
    await this.kill(signal);
    this.start();
  }

  /** Resolves once the core lists this device as connected. */
  async online(ms = 30_000): Promise<void> {
    await until(`${this.options.name} to connect`, ms, async () => {
      const { devices } = await this.stack.json<{
        devices: { deviceId: string; connected: boolean }[];
      }>('/api/devices');
      return devices.some((d) => d.deviceId === this.options.deviceId && d.connected);
    });
  }
}

/** A core in a container of its own, with the compose file's isolation, and the agents that talk to it. */
export class Stack {
  readonly agents: Agent[] = [];

  readonly tmp: string;
  readonly bin: string;
  readonly container: string;
  readonly volume: string;
  readonly port: number;

  private constructor(tmp: string, bin: string, container: string, volume: string, port: number) {
    this.tmp = tmp;
    this.bin = bin;
    this.container = container;
    this.volume = volume;
    this.port = port;
  }

  get url(): string {
    return `http://127.0.0.1:${this.port}`;
  }

  get auth(): Record<string, string> {
    return { Authorization: `Bearer ${HOST_TOKEN}` };
  }

  /** Starts a core. `tmp` is the stack's directory (a real path); `stop()` removes it. */
  static async start(tmp: string): Promise<Stack> {
    const image = process.env.E2E_IMAGE_REF ?? '';
    const bin = process.env.E2E_BIN ?? '';
    if (!image || !bin) throw new Error('run through `pnpm e2e:image`: no image or binaries built');
    const id = `${process.env.E2E_RUN ?? process.pid}-${++counter}-${Math.random().toString(36).slice(2, 7)}`;
    const container = `hoplet-e2e-${id}`;
    const volume = `${container}-data`;
    const port = await freePort();
    run('docker', [
      'run',
      '-d',
      '--name',
      container,
      '--label',
      `${LABEL}=${process.env.E2E_RUN ?? ''}`,
      '-p',
      `127.0.0.1:${port}:7788`,
      '-e',
      `HOPLET_TOKEN=${HOST_TOKEN}`,
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
      `${volume}:/data`,
      image,
    ]);
    const stack = new Stack(realpathSync(tmp), bin, container, volume, port);
    await stack.ready();
    return stack;
  }

  async ready(): Promise<void> {
    await until('the core', 60_000, async () => {
      const r = await fetch(`${this.url}/api/health`, { headers: this.auth });
      return r.ok;
    });
  }

  /** Restarts the container; the data volume stays and the agents reconnect by themselves. */
  async restartCore(): Promise<void> {
    run('docker', ['restart', '-t', '2', this.container]);
    await this.ready();
  }

  async mint(name: string, deviceId: string): Promise<string> {
    const r = await this.request('POST', '/api/devices', { name, deviceId });
    return (r.body as { token: string }).token;
  }

  /** An agent that has not started yet; seed its store, then `start()` it. */
  async agent(options: AgentOptions): Promise<Agent> {
    const token = await this.mint(options.name, options.deviceId);
    const agent = new Agent(this, options, token);
    this.agents.push(agent);
    return agent;
  }

  async request(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<{ status: number; body: unknown }> {
    const r = await fetch(`${this.url}${path}`, {
      method,
      headers: {
        ...this.auth,
        ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      },
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    });
    const text = await r.text();
    return { status: r.status, body: text ? JSON.parse(text) : null };
  }

  async json<T>(path: string): Promise<T> {
    const r = await this.request('GET', path);
    if (r.status !== 200) throw new Error(`GET ${path} answered ${r.status}`);
    return r.body as T;
  }

  async session(id: string): Promise<SessionInfo | undefined> {
    const { sessions } = await this.json<{ sessions: SessionInfo[] }>('/api/sessions');
    return sessions.find((s) => s.sessionId === id);
  }

  async prompt(sessionId: string, text: string): Promise<{ status: number; body: unknown }> {
    return await this.request('POST', `/api/sessions/${sessionId}/prompt`, {
      text,
      promptId: `p-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    });
  }

  async stop(): Promise<void> {
    for (const agent of this.agents) await agent.kill('SIGKILL');
    removeContainer(this.container, this.volume);
    rmSync(this.tmp, { recursive: true, force: true });
  }
}

export function removeContainer(container: string, volume = ''): void {
  try {
    run('docker', ['rm', '-f', '-v', container]);
  } catch {}
  if (!volume) return;
  try {
    run('docker', ['volume', 'rm', '-f', volume]);
  } catch {}
}

/** An agent connection the test drives by hand: the device side of `/ws/agent`. */
export class RawDevice {
  readonly inbox: Record<string, unknown>[] = [];

  private readonly ws: WebSocket;
  private readonly handlers: ((message: Record<string, unknown>) => void)[] = [];

  private constructor(ws: WebSocket) {
    this.ws = ws;
    ws.addEventListener('message', (e) => {
      const message = JSON.parse(String(e.data)) as Record<string, unknown>;
      this.inbox.push(message);
      for (const handler of this.handlers) handler(message);
    });
  }

  /** Calls `handler` with every message that arrives from now on. */
  on(handler: (message: Record<string, unknown>) => void): void {
    this.handlers.push(handler);
  }

  /** Mints a device token and connects with it. */
  static async connect(stack: Stack, deviceId: string, name = deviceId): Promise<RawDevice> {
    return await RawDevice.open(stack, deviceId, await stack.mint(name, deviceId), name);
  }

  /** Connects with the token of an existing device. */
  static async open(
    stack: Stack,
    deviceId: string,
    token: string,
    name = deviceId,
  ): Promise<RawDevice> {
    const url = `${stack.url.replace('http', 'ws')}/ws/agent`;
    const ws = new WebSocket(url, { headers: { Authorization: `Bearer ${token}` } } as never);
    const device = new RawDevice(ws);
    await new Promise<void>((resolve, reject) => {
      ws.addEventListener('open', () => resolve(), { once: true });
      ws.addEventListener('error', () => reject(new Error('agent socket refused')), { once: true });
    });
    device.send({ type: 'device.hello', deviceId, name, version: 'e2e' });
    await device.next((m) => m.type === 'welcome');
    return device;
  }

  send(message: Record<string, unknown>): void {
    this.ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...message }));
  }

  /** The first message in the inbox that `match` accepts, taken out of it. */
  async next(
    match: (m: Record<string, unknown>) => boolean,
    ms = 15_000,
  ): Promise<Record<string, unknown>> {
    return await until(
      'a message',
      ms,
      async () => {
        const at = this.inbox.findIndex(match);
        return at < 0 ? null : (this.inbox.splice(at, 1)[0] ?? null);
      },
      20,
    );
  }

  /** Appends `text` (whole lines) to a session at `byteStart` and returns the core's answer. */
  async append(sessionId: string, byteStart: number, text: string) {
    this.send({
      type: 'session.append',
      sessionId,
      projectDir: '/e2e',
      filePath: `/e2e/${sessionId}.jsonl`,
      byteStart,
      encoding: 'plain',
      lines: text,
    });
    return await this.next(
      (m) => m.sessionId === sessionId && /^session\.(ack|resync|rejected)$/.test(String(m.type)),
    );
  }

  close(): void {
    this.ws.close();
  }
}

/** A browser connection of the host: the messages the core broadcasts, such as `handoff.progress`. */
export class BrowserFeed {
  readonly messages: Record<string, unknown>[] = [];
  private readonly ws: WebSocket;

  private constructor(ws: WebSocket) {
    this.ws = ws;
    ws.addEventListener('message', (e) => {
      this.messages.push(JSON.parse(String(e.data)) as Record<string, unknown>);
    });
  }

  static async open(stack: Stack): Promise<BrowserFeed> {
    const { body } = await stack.request('POST', '/api/ws-tickets');
    const { ticket } = body as { ticket: string };
    const ws = new WebSocket(`${stack.url.replace('http', 'ws')}/ws?ticket=${ticket}`);
    await new Promise<void>((resolve, reject) => {
      ws.addEventListener('open', () => resolve(), { once: true });
      ws.addEventListener('error', () => reject(new Error('browser socket refused')), {
        once: true,
      });
    });
    return new BrowserFeed(ws);
  }

  /** Resolves with the first message `match` accepts, also one that arrived before the call. */
  async wait(match: (m: Record<string, unknown>) => boolean, ms = 30_000) {
    return await until('a broadcast', ms, async () => this.messages.find(match) ?? null, 20);
  }

  /** The steps of the handoffs seen so far, in order. */
  steps(sessionId: string): string[] {
    return this.messages
      .filter((m) => m.type === 'handoff.progress' && m.sessionId === sessionId)
      .map((m) => String(m.step));
  }

  close(): void {
    this.ws.close();
  }
}
