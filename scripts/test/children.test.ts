// End to end child sessions (docs/spec.md#child-sessions, ADR 0035): a parent session on machine A starts a
// child on machine B, waits for it, collects its report and changes, reads the diff of a file and cancels a
// subtree, through an in-process core, two real Go agents with `session_mcp` on and the scripted fake `claude`.
// The parent's tool calls go through the real path: the `hoplet-agent mcp` command in the MCP config the agent
// wrote for the parent's process, the agent's socket and the parent's session token. The fake `claude` does not
// call tools itself, so the test speaks MCP to that command in its place; the vendor's own approval prompt before
// an act tool is not part of this path.
import { type ChildProcess, spawn } from 'node:child_process';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import {
  type Binaries,
  buildBinaries,
  type Core,
  claudeDirName,
  run,
  sessionLine,
  startCore,
  TestAgent,
  until,
} from './support/e2e.ts';

vi.setConfig({ testTimeout: 120_000 });

const HOST = 'children-e2e-host-token';
const PARENT = '44444444-5555-4666-8777-888888888801';

type Json = Record<string, unknown>;

/** The parent's tools and the settings that allow them: starting sessions anywhere, nothing else. */
const SETTINGS = {
  send: true,
  create: true,
  requestHandoff: false,
  autoApproveHandoff: false,
  readConfig: true,
  writeConfig: false,
  autoApproveMachines: [],
  visibility: 'all',
};

/** One `hoplet-agent mcp` process, spoken to as Claude Code would: JSON-RPC, one message a line. */
class McpClient {
  private readonly proc: ChildProcess;
  private readonly waiting = new Map<number, (msg: Json) => void>();
  private next = 1;

  constructor(configPath: string) {
    const config = JSON.parse(readFileSync(configPath, 'utf8')) as {
      mcpServers: { hoplet: { command: string; args: string[]; env: Record<string, string> } };
    };
    const server = config.mcpServers.hoplet;
    this.proc = spawn(server.command, server.args, {
      env: { PATH: process.env.PATH ?? '', ...server.env },
      stdio: ['pipe', 'pipe', 'ignore'],
    });
    const lines = createInterface({ input: this.proc.stdout as NodeJS.ReadableStream });
    lines.on('line', (line) => {
      const msg = JSON.parse(line) as Json;
      this.waiting.get(msg.id as number)?.(msg);
      this.waiting.delete(msg.id as number);
    });
  }

  private rpc(method: string, params: Json): Promise<Json> {
    const id = this.next++;
    const answer = new Promise<Json>((r) => this.waiting.set(id, r));
    this.proc.stdin?.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`);
    return answer;
  }

  async initialize(): Promise<void> {
    const res = await this.rpc('initialize', {
      protocolVersion: '2025-06-18',
      capabilities: {},
      clientInfo: { name: 'children-e2e', version: '1' },
    });
    expect(res.result).toBeDefined();
    this.proc.stdin?.write(
      `${JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' })}\n`,
    );
  }

  /** Calls a tool and returns its text; `isError` tells a refusal. */
  async call(name: string, args: Json): Promise<{ text: string; isError: boolean }> {
    const res = await this.rpc('tools/call', { name, arguments: args });
    const result = res.result as { content: { text: string }[]; isError?: boolean };
    return { text: result.content[0]?.text ?? '', isError: result.isError === true };
  }

  async close(): Promise<void> {
    if (this.proc.exitCode !== null) return;
    const exited = new Promise<void>((r) => this.proc.once('exit', () => r()));
    this.proc.stdin?.end();
    setTimeout(() => this.proc.kill('SIGKILL'), 2_000).unref();
    await exited;
  }
}

interface Machine {
  id: string;
  agent: TestAgent;
  clone: string;
  /** The fake `claude`'s log of every start, with its arguments. */
  claudeLog: string;
}

let binaries: Binaries;
let root = '';
let core: Core;
let a: Machine;
let b: Machine;
const clients: McpClient[] = [];

/** A short root: the agent's socket path must stay within the Unix limit (100 bytes, macOS 104). */
function shortTemp(prefix: string): string {
  const base = process.platform === 'darwin' ? '/tmp' : tmpdir();
  return realpathSync(mkdtempSync(join(base, prefix)));
}

async function rest(method: string, path: string, token: string, body?: unknown) {
  const res = await fetch(`${core.url}${path}`, {
    method,
    headers: {
      authorization: `Bearer ${token}`,
      ...(body !== undefined ? { 'content-type': 'application/json' } : {}),
    },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  const text = await res.text();
  return { status: res.status, body: (text ? JSON.parse(text) : null) as Json | null };
}

/** The MCP config the agent wrote for the live process of `sessionId` on machine `m`. */
function mcpConfigOf(m: Machine, sessionId: string): string | null {
  if (!existsSync(m.claudeLog)) return null;
  const starts = readFileSync(m.claudeLog, 'utf8')
    .split('\n')
    .filter(Boolean)
    .map((l) => (JSON.parse(l) as { args: string[] }).args)
    .filter((args) => args.includes(sessionId));
  const args = starts.at(-1);
  const at = args?.indexOf('--mcp-config') ?? -1;
  const path = at >= 0 ? args?.[at + 1] : undefined;
  return path !== undefined && existsSync(path) ? path : null;
}

async function clientOf(m: Machine, sessionId: string): Promise<McpClient> {
  let path: string | null = null;
  await until(`the MCP config of ${sessionId}`, () => {
    path = mcpConfigOf(m, sessionId);
    return path !== null;
  });
  const client = new McpClient(path as unknown as string);
  clients.push(client);
  await client.initialize();
  return client;
}

/** The session ID and worktree in start_session's answer. */
function started(text: string): { sessionId: string; worktree: string | null } {
  const id = /Started session ([0-9a-f-]{36})/.exec(text)?.[1];
  expect(id, text).toBeDefined();
  const worktree = /Worktree: (.+) \(branch /.exec(text)?.[1] ?? null;
  return { sessionId: id as string, worktree };
}

function audit(type: string): Json[] {
  return core.store
    .listAudit({ type }, { order: 'asc', limit: 500 })
    .events.map((e) => e as unknown as Json);
}

function machine(name: string, id: string, origin: string): Machine {
  const dir = join(root, name);
  mkdirSync(dir, { recursive: true });
  const clone = join(dir, 'repo');
  run(dir, 'git', ['clone', origin, clone]);
  const minted = core.store.mintDevice(id, id);
  const claudeLog = join(root, `${name}-claude.log`);
  const agent = new TestAgent({
    root: join(root, `${name}-agent`),
    name: id,
    deviceId: id,
    token: minted.token,
    serverUrl: core.url,
    binaries,
    config: { session_mcp: true },
    env: { FAKE_CLAUDE_LOG: claudeLog },
  });
  return { id, agent, clone, claudeLog };
}

beforeAll(async () => {
  root = shortTemp('hoplet-kids-');
  binaries = buildBinaries(root);
  const origin = join(root, 'origin.git');
  run(root, 'git', ['init', '--bare', '-b', 'main', origin]);
  const seed = join(root, 'seed');
  run(root, 'git', ['clone', origin, seed]);
  mkdirSync(join(seed, 'src'));
  writeFileSync(join(seed, 'src', 'main.txt'), 'one\ntwo\nthree\n');
  run(seed, 'git', ['add', '-A']);
  run(seed, 'git', ['commit', '-m', 'seed']);
  run(seed, 'git', ['push', 'origin', 'HEAD:main']);

  core = await startCore(join(root, 'data'), HOST);
  a = machine('a', 'machine-a', origin);
  b = machine('b', 'machine-b', origin);
  const parentFile = a.agent.seedSession(a.clone, PARENT, [sessionLine(a.clone, PARENT, 'plan')]);
  a.agent.watch(a.clone, [PARENT]);
  b.agent.watch(b.clone, []);
  a.agent.start();
  b.agent.start();
  await until(
    'both agents online with session MCP and the parent synced',
    () =>
      core.control.ownerOnline(a.id) &&
      core.control.ownerOnline(b.id) &&
      core.store.getSession(PARENT)?.byteSize === readFileSync(parentFile).length,
    30_000,
  ).catch((err) => {
    throw new Error(`${err.message}\nA: ${a.agent.logs()}\nB: ${b.agent.logs()}`);
  });
}, 240_000);

afterAll(async () => {
  for (const c of clients.splice(0)) await c.close();
  await a?.agent.stop();
  await b?.agent.stop();
  await core?.close();
  if (root) rmSync(root, { recursive: true, force: true });
});

describe('a tree of work across two machines', () => {
  let parent: McpClient;
  let child = '';
  let worktree = '';

  it('starts a child on another machine from the parent, in a new worktree', async () => {
    const put = await rest('PUT', `/api/sessions/${PARENT}/mcp`, HOST, { settings: SETTINGS });
    expect(put.status).toBe(200);
    // The parent gets a process, and with it the MCP config of that process.
    expect((await core.client.prompt(PARENT, 'say ready')).ok).toBe(true);
    await until('the parent ready', () => core.control.runtimeOf(PARENT).state === 'ready');
    parent = await clientOf(a, PARENT);

    const res = await parent.call('start_session', {
      prompt: 'say REPORT: changed src/main.txt and added notes.txt on the child branch',
      machine: b.id,
      projectPath: realpathSync(b.clone),
      title: 'child one',
    });
    expect(res.isError, res.text).toBe(false);
    const one = started(res.text);
    child = one.sessionId;
    expect(one.worktree).not.toBeNull();
    worktree = one.worktree as string;
    expect(worktree.startsWith(realpathSync(join(root, 'b')))).toBe(true);
    expect(core.control.ownerOf(child)).toBe(b.id);

    // The relation and its audit: the parent is the actor of the start.
    const session = await rest('GET', `/api/sessions/${child}`, HOST);
    expect(session.body).toMatchObject({ parentId: PARENT });
    const created = audit('session.created').find((e) => e.sessionId === child);
    expect(created).toMatchObject({ actorKind: 'session', actorId: PARENT, deviceId: b.id });
  });

  it('waits for the child, then collects its report and the files it changed', async () => {
    const waited = await parent.call('wait_children', {
      sessionIds: [child],
      until: 'all',
      timeoutSeconds: 40,
    });
    expect(waited.isError, waited.text).toBe(false);
    expect(waited.text).toContain('done');
    // What the child did in its worktree, as its own tools would have done it.
    writeFileSync(join(worktree, 'src', 'main.txt'), 'one\nTWO\nthree\n');
    writeFileSync(join(worktree, 'notes.txt'), 'child notes\n');
    // The report is read from the child's stored lines: they are synced from B first.
    const file = join(b.agent.claudeProjects, claudeDirName(worktree), `${child}.jsonl`);
    await until(
      "the child's turn synced",
      () =>
        existsSync(file) && core.store.getSession(child)?.byteSize === readFileSync(file).length,
    );

    const res = await parent.call('collect_child', { sessionId: child });
    expect(res.isError, res.text).toBe(false);
    const collected = JSON.parse(res.text.slice(res.text.indexOf('{'))) as Json;
    expect(collected).toMatchObject({
      sessionId: child,
      state: 'done',
      summary: 'REPORT: changed src/main.txt and added notes.txt on the child branch',
      released: true,
    });
    const changes = collected.changes as { status: string; files?: { path: string }[] };
    expect(changes.status).toBe('ok');
    expect(changes.files?.map((f) => f.path).sort()).toEqual(['notes.txt', 'src/main.txt']);
    await until('the released child to end', () =>
      ['ended', 'offline', 'idle'].includes(core.control.runtimeOf(child).state),
    );

    const event = audit('child.collected').find((e) => e.sessionId === child);
    expect(event).toMatchObject({ actorKind: 'session', actorId: PARENT, channel: 'rest' });
    expect(JSON.stringify(event)).not.toContain('REPORT');
    // The release is the parent's stop.
    expect(audit('session.stopped').find((e) => e.sessionId === child)).toMatchObject({
      actorKind: 'session',
      actorId: PARENT,
    });
  });

  it("reads one file's diff of the child, which is not audited", async () => {
    const before = core.store.listAudit({}, { order: 'asc', limit: 500 }).events.length;
    const res = await parent.call('read_child_diff', { sessionId: child, path: 'src/main.txt' });
    expect(res.isError, res.text).toBe(false);
    expect(res.text).toContain('-two');
    expect(res.text).toContain('+TWO');
    expect(core.store.listAudit({}, { order: 'asc', limit: 500 }).events.length).toBe(before);
  });

  it("leaves a blocked child's permission to the user: the parent's token cannot answer it", async () => {
    // The fake `claude` writes a new session's file when a turn ends, and the start returns once the file is
    // there: the child first finishes a turn, then the parent sends it the turn that asks.
    const res = await parent.call('start_session', {
      prompt: 'say ok',
      machine: b.id,
      projectPath: realpathSync(b.clone),
      worktree: false,
    });
    expect(res.isError, res.text).toBe(false);
    const asking = started(res.text).sessionId;
    await until('the child ready', () => core.control.runtimeOf(asking).state === 'ready');
    const sent = await parent.call('send_prompt', {
      sessionId: asking,
      text: 'tool echo from-child',
    });
    expect(sent.isError, sent.text).toBe(false);
    await until('the child to ask', () => core.control.openPermissions(asking).length === 1);
    const status = await parent.call('wait_children', { sessionIds: [asking], until: 'any' });
    expect(status.text).toContain('blocked');

    const requestId = core.control.openPermissions(asking)[0]?.requestId;
    // A session token of the parent, of the kind its MCP calls use (the agent keeps its own in memory), cannot
    // answer: the route needs `answer`, which a session token never holds.
    const { secret } = core.store.mintSessionToken(PARENT);
    const refused = await rest('POST', `/api/sessions/${asking}/permission`, secret, {
      requestId,
      decision: 'allow',
    });
    expect(refused.status).toBe(403);
    expect(core.control.openPermissions(asking)).toHaveLength(1);
    const answered = await rest('POST', `/api/sessions/${asking}/permission`, HOST, {
      requestId,
      decision: 'allow',
    });
    expect(answered.body).toEqual({ ok: true });
    await until('the child done', () => core.control.runtimeOf(asking).state === 'ready');
  });

  it('cancels a child with the session it started, and audits both as the parent', async () => {
    // `work` writes a new session's first line before it runs, so the start returns while the turn goes on.
    const res = await parent.call('start_session', {
      prompt: 'work 600',
      machine: b.id,
      projectPath: realpathSync(b.clone),
      worktree: false,
      nested: true,
      title: 'child two',
    });
    expect(res.isError, res.text).toBe(false);
    const two = started(res.text).sessionId;
    await until('child two running', () => core.control.runtimeOf(two).state === 'running');

    // Child two starts a session of its own through its own MCP config on B.
    const middle = await clientOf(b, two);
    const deeper = await middle.call('start_session', { prompt: 'work 600', worktree: false });
    expect(deeper.isError, deeper.text).toBe(false);
    const grandchild = started(deeper.text).sessionId;
    expect(core.control.ownerOf(grandchild)).toBe(b.id);
    await until(
      'the grandchild running',
      () => core.control.runtimeOf(grandchild).state === 'running',
    );
    expect(audit('session.created').find((e) => e.sessionId === grandchild)).toMatchObject({
      actorKind: 'session',
      actorId: two,
    });

    const cancelled = await parent.call('cancel_child', { sessionId: two, subtree: true });
    expect(cancelled.isError, cancelled.text).toBe(false);
    expect(cancelled.text).toContain(two);
    expect(cancelled.text).toContain(grandchild);
    await until('both stopped', () =>
      [two, grandchild].every((id) => core.control.runtimeOf(id).state !== 'running'),
    );

    const states = await rest('GET', `/api/sessions/${PARENT}/children/status?ids=${two}`, HOST);
    expect((states.body?.children as Json[] | undefined)?.[0]).toMatchObject({
      sessionId: two,
      state: 'cancelled',
    });
    expect(core.store.sessionChildren().get(grandchild)?.ended).toBe('cancelled');
    for (const type of ['child.cancelled', 'session.interrupted', 'session.stopped']) {
      const made = audit(type).filter((e) => e.sessionId === two || e.sessionId === grandchild);
      expect(made.map((e) => e.sessionId).sort(), type).toEqual([two, grandchild].sort());
      for (const e of made) expect(e).toMatchObject({ actorKind: 'session', actorId: PARENT });
    }
    // Their worktrees and files stay: a cancel stops processes, it deletes nothing.
    expect(existsSync(join(worktree, 'notes.txt'))).toBe(true);
  });

  it('keeps the audit chain intact', async () => {
    const verified = await core.client.auditVerify();
    expect(verified.ok, JSON.stringify(verified)).toBe(true);
    if (verified.ok) expect(verified.data).toMatchObject({ ok: true });
  });
});
