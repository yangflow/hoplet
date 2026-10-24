// Building blocks of the end-to-end tests: an in-process core, real `hoplet-agent` processes with their own
// state roots, the scripted fake `claude` (agent/internal/runtime/fakeclaude) and git repositories.
import { type ChildProcess, spawn, spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { join, resolve } from 'node:path';
import { createApi } from '../../../core/src/api/index.ts';
import {
  type AgentControl,
  type Control,
  type ControlOptions,
  type ControlReply,
  createControl,
  type DeviceLink,
} from '../../../core/src/control/index.ts';
import { attachIngest } from '../../../core/src/ingest/index.ts';
import { Store } from '../../../core/src/store/index.ts';
import type { ServerMessage } from '../../../shared/src/index.ts';
import { HostClient } from './host-client.ts';

/** Git without the user's or the system's configuration, and with fixed identities. */
export const isolatedEnv: NodeJS.ProcessEnv = {
  ...process.env,
  LC_ALL: 'C',
  LANG: 'C',
  TZ: 'UTC',
  GIT_CONFIG_NOSYSTEM: '1',
  GIT_CONFIG_GLOBAL: '/dev/null',
  GIT_TERMINAL_PROMPT: '0',
  GIT_AUTHOR_NAME: 'Fixture',
  GIT_AUTHOR_EMAIL: 'fixture@example.invalid',
  GIT_COMMITTER_NAME: 'Fixture',
  GIT_COMMITTER_EMAIL: 'fixture@example.invalid',
};

/** Runs a program and returns its trimmed standard output; a failure throws with the program's stderr. */
export function run(cwd: string, program: string, args: string[], env = isolatedEnv): string {
  const result = spawnSync(program, args, { cwd, env, encoding: 'utf8', maxBuffer: 1 << 28 });
  if (result.status !== 0) {
    throw new Error(`${program} ${args.join(' ')} in ${cwd}: ${result.stderr || result.error}`);
  }
  return result.stdout.trim();
}

export async function until(
  what: string,
  check: () => boolean | Promise<boolean>,
  ms = 20_000,
): Promise<void> {
  const deadline = Date.now() + ms;
  while (!(await check())) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 20));
  }
}

export interface Binaries {
  agent: string;
  claude: string;
}

/** Builds the agent and the fake `claude` into `dir`. */
export function buildBinaries(dir: string): Binaries {
  const bin = join(dir, 'bin');
  mkdirSync(bin, { recursive: true });
  const agent = join(bin, 'hoplet-agent');
  const claude = join(bin, 'claude');
  run(resolve('agent'), 'go', ['build', '-o', agent, './cmd/hoplet-agent']);
  run(resolve('agent'), 'go', ['build', '-o', claude, './internal/runtime/fakeclaude']);
  return { agent, claude };
}

/**
 * Where a test tampers with the control traffic between the core and one device: `toDevice` sees every
 * message the core sends (returning another message, or null to drop it), `fromDevice` every control
 * message the device sends (returning, possibly later, the message to deliver, or null).
 */
export interface Intercept {
  toDevice?(deviceId: string, msg: ServerMessage): ServerMessage | null;
  fromDevice?(
    deviceId: string,
    msg: ControlReply,
  ): ControlReply | null | Promise<ControlReply | null>;
}

export interface Core {
  store: Store;
  control: Control;
  server: Server;
  url: string;
  token: string;
  client: HostClient;
  close(): Promise<void>;
}

/** An in-process core listening on a local port; `dataDir` is its data directory. */
export async function startCore(
  dataDir: string,
  token: string,
  options: ControlOptions = {},
  intercept: Intercept = {},
): Promise<Core> {
  const store = new Store(dataDir);
  const control = createControl(store, options);
  const server = createServer();
  // A test can sit idle longer than the default 5 s between two requests of one client.
  server.keepAliveTimeout = 120_000;
  // The agent socket talks to control through this wrapper, so a test can change what crosses.
  const links = new Map<DeviceLink, { link: DeviceLink; deviceId: string }>();
  const agents: AgentControl = {
    bulk: control.bulk,
    publish: control.publish,
    attach(deviceId, link, providers, hello) {
      const through = (msg: ServerMessage) => intercept.toDevice?.(deviceId, msg) ?? null;
      const wrapped: DeviceLink = {
        isOpen: () => link.isOpen(),
        send(msg) {
          const out = intercept.toDevice ? through(msg) : msg;
          if (out) link.send(out);
        },
        async sendAndWait(msg) {
          const out = intercept.toDevice ? through(msg) : msg;
          if (out) await link.sendAndWait(out);
        },
      };
      links.set(link, { link: wrapped, deviceId });
      // The providers and the hello pass through: the device's capabilities (`sessionMcp`) are in it.
      control.attach(deviceId, wrapped, providers, hello);
    },
    announceRules: control.announceRules,
    appended: control.appended,
    detach(link) {
      control.detach(links.get(link)?.link ?? link);
      links.delete(link);
    },
    receive(link, msg) {
      const known = links.get(link);
      if (!known || !intercept.fromDevice) {
        control.receive(known?.link ?? link, msg);
        return;
      }
      void Promise.resolve(intercept.fromDevice(known.deviceId, msg)).then((out) => {
        if (out) control.receive(known.link, out);
      });
    },
  };
  attachIngest(server, store, agents);
  createApi(store, { port: 0, dataDir, token, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return {
    store,
    control,
    server,
    url,
    token,
    client: new HostClient({ baseUrl: url, token }),
    close: () =>
      new Promise<void>((r) => {
        server.closeAllConnections();
        server.close(() => r());
      }),
  };
}

/** The directory name Claude Code's store uses for a working directory. */
export function claudeDirName(cwd: string): string {
  return realpathSync(cwd).replace(/[^A-Za-z0-9]/g, '-');
}

export interface AgentOptions {
  /** A directory the agent's state root and Claude store are made in. */
  root: string;
  name: string;
  deviceId: string;
  /** The core's token for this device. */
  token: string;
  /** The URL the agent connects to; a proxy stands here when a test tampers with the traffic. */
  serverUrl: string;
  binaries: Binaries;
  /** Claude Code's projects store; by default a directory of the agent's own, for the fake `claude`. */
  claudeProjects?: string;
  env?: Record<string, string>;
  /** More fields of the agent's config.json, such as `session_mcp`. */
  config?: Record<string, unknown>;
}

/** One development machine: its own state root, Claude store and `hoplet-agent` process. */
export class TestAgent {
  readonly home: string;
  readonly claudeProjects: string;
  readonly configPath: string;
  readonly log: string[] = [];
  child: ChildProcess | null = null;
  private readonly options: AgentOptions;

  constructor(options: AgentOptions) {
    this.options = options;
    this.home = join(options.root, 'state');
    this.claudeProjects = options.claudeProjects ?? join(options.root, 'claude');
    this.configPath = join(options.root, 'config.json');
    mkdirSync(this.home, { recursive: true });
    mkdirSync(this.claudeProjects, { recursive: true });
    writeFileSync(
      this.configPath,
      JSON.stringify({
        server_url: options.serverUrl,
        token: options.token,
        device_id: options.deviceId,
        device_name: options.name,
        hoplet_home: this.home,
        claude_projects: this.claudeProjects,
        codex_home: `${this.home}/codex`,
        claude_bin: options.binaries.claude,
        exclude_prefixes: [],
        poll_interval_ms: 50,
        ...options.config,
      }),
    );
  }

  /** Watches `dir` and imports the sessions. */
  watch(dir: string, sessionIds: string[]): void {
    const file = join(this.home, 'projects.json');
    let projects: { path: string; imported: string[] }[] = [];
    try {
      projects = JSON.parse(readFileSync(file, 'utf8')).projects;
    } catch {
      // no projects yet
    }
    const path = realpathSync(dir);
    const existing = projects.find((p) => p.path === path);
    if (existing) existing.imported.push(...sessionIds);
    else projects.push({ path, imported: sessionIds });
    writeFileSync(file, JSON.stringify({ projects }));
  }

  /** Writes a session file for a working directory into this machine's Claude store. */
  seedSession(cwd: string, sessionId: string, lines: string[]): string {
    const dir = join(this.claudeProjects, claudeDirName(cwd));
    mkdirSync(dir, { recursive: true });
    const file = join(dir, `${sessionId}.jsonl`);
    writeFileSync(file, lines.join(''));
    return file;
  }

  start(): ChildProcess {
    const child = spawn(this.options.binaries.agent, ['--config', this.configPath, 'daemon'], {
      env: {
        ...isolatedEnv,
        FAKE_CLAUDE_PROJECTS: this.claudeProjects,
        HOME: this.options.root,
        ...this.options.env,
      },
      stdio: ['ignore', 'ignore', 'pipe'],
    });
    child.stderr?.on('data', (d) => this.log.push(String(d)));
    this.child = child;
    return child;
  }

  async stop(): Promise<void> {
    const child = this.child;
    if (!child || child.exitCode !== null) return;
    const exited = new Promise<void>((r) => child.once('exit', () => r()));
    child.kill('SIGTERM');
    await exited;
  }

  /** Ends the process without a chance to clean up. */
  async kill(): Promise<void> {
    const child = this.child;
    if (!child || child.exitCode !== null) return;
    const exited = new Promise<void>((r) => child.once('exit', () => r()));
    child.kill('SIGKILL');
    await exited;
  }

  logs(): string {
    return this.log.join('');
  }
}

/** A session file line in the shape Claude Code writes, with the working directory it ran in. */
export function sessionLine(cwd: string, sessionId: string, text: string): string {
  return `${JSON.stringify({
    type: 'user',
    cwd: realpathSync(cwd),
    sessionId,
    message: { role: 'user', content: text },
  })}\n`;
}
