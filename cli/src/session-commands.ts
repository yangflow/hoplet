import type { Hoplet, LiveSessionMeta, TurnResult } from '@hoplet/sdk';
import { bool, list, str } from './args.ts';
import { type Context, EXIT_OF_RESULT } from './context.ts';
import { CliError } from './errors.ts';

const STATES = [
  'offline',
  'external',
  'idle',
  'starting',
  'ready',
  'running',
  'waiting-permission',
  'failed',
] as const;
type State = (typeof STATES)[number];

/** A machine given by its ID or its name. */
export async function resolveDevice(client: Hoplet, ref: string): Promise<string> {
  const devices = await client.listDevices();
  const byId = devices.find((d) => d.deviceId === ref);
  if (byId) return byId.deviceId;
  const byName = devices.filter((d) => d.name === ref);
  if (byName.length === 1 && byName[0]) return byName[0].deviceId;
  if (byName.length > 1) {
    throw new CliError('usage', `More than one machine is named "${ref}"; use its id.`);
  }
  throw new CliError('not-found', `No machine "${ref}". See hoplet devices.`);
}

function seconds(flags: Context['flags'], name: string): number | undefined {
  const raw = str(flags, name);
  if (raw === undefined) return undefined;
  const value = Number(raw);
  if (!Number.isFinite(value) || value <= 0) {
    throw new CliError('usage', `--${name} is a number of seconds greater than 0`);
  }
  return Math.round(value * 1000);
}

async function textOf(ctx: Context, words: string[]): Promise<string> {
  const text = words.join(' ');
  if (text === '-') {
    if (!ctx.deps.readStdin) throw new CliError('usage', 'There is no standard input to read.');
    return (await ctx.deps.readStdin()).replace(/\r?\n$/, '');
  }
  return text;
}

export async function sessions(ctx: Context): Promise<number> {
  const deviceRef = str(ctx.flags, 'device');
  const state = str(ctx.flags, 'state');
  if (state !== undefined && !STATES.includes(state as State)) {
    throw new CliError('usage', `--state is one of ${STATES.join(', ')}`);
  }
  const deviceId = deviceRef === undefined ? undefined : await resolveDevice(ctx.client, deviceRef);
  const found = await ctx.client.listSessions({
    ...(deviceId === undefined ? {} : { deviceId }),
    ...(state === undefined ? {} : { state: state as State }),
  });
  if (ctx.json) {
    ctx.doc({ sessions: found });
    return 0;
  }
  for (const s of found) ctx.line(sessionLine(s));
  return 0;
}

function sessionLine(s: LiveSessionMeta): string {
  return [s.sessionId, s.runtime.state, s.deviceName, s.provider, s.projectPath, s.title].join(
    '\t',
  );
}

/** Waits as `--wait` and `wait` ask, and reports the result with the exit code of ADR 0033. */
async function report(ctx: Context, result: TurnResult, extra: Record<string, unknown>) {
  if (ctx.json) {
    ctx.doc({
      ...extra,
      result: result.result,
      state: result.state,
      ...(result.error === undefined ? {} : { error: result.error }),
    });
  } else {
    ctx.line(`${result.result}\t${result.state}`);
    if (result.error !== undefined) ctx.note(result.error);
  }
  return EXIT_OF_RESULT[result.result];
}

function waitOptions(ctx: Context) {
  const timeout = seconds(ctx.flags, 'timeout');
  const startTimeout = seconds(ctx.flags, 'start-timeout');
  return {
    ...(timeout === undefined ? {} : { timeout }),
    ...(startTimeout === undefined ? {} : { startTimeout }),
    untilBlocked: bool(ctx.flags, 'until-blocked'),
    drain: bool(ctx.flags, 'drain'),
    ...(ctx.deps.signal ? { signal: ctx.deps.signal } : {}),
  };
}

function coerce(value: string): string | boolean {
  if (value === 'true') return true;
  if (value === 'false') return false;
  return value;
}

export async function newSession(ctx: Context, words: string[]): Promise<number> {
  const { flags } = ctx;
  if (str(flags, 'template') !== undefined) {
    throw new CliError('not-supported', '--template is not available yet.');
  }
  const deviceRef = str(flags, 'device');
  const projectPath = str(flags, 'path');
  if (deviceRef === undefined || projectPath === undefined) {
    throw new CliError('usage', 'new needs --device <machine> and --path <dir>.');
  }
  const provider = str(flags, 'provider') ?? 'claude';
  if (provider !== 'claude' && provider !== 'codex') {
    throw new CliError('usage', '--provider is claude or codex');
  }
  const config: Record<string, string | boolean> = {};
  const model = str(flags, 'model');
  if (model !== undefined) config.model = model;
  for (const entry of list(flags, 'set')) {
    const eq = entry.indexOf('=');
    if (eq < 1) throw new CliError('usage', `--set is option=value, not "${entry}"`);
    config[entry.slice(0, eq)] = coerce(entry.slice(eq + 1));
  }
  const text = await textOf(ctx, words);
  if (text === '') throw new CliError('usage', 'The prompt is empty.');
  const options = bool(flags, 'wait') ? waitOptions(ctx) : undefined;
  // --worktree starts the session in a new git worktree on a generated branch, --worktree=<branch> on that one.
  const wanted = flags.worktree;
  const worktree =
    wanted === undefined ? undefined : typeof wanted === 'string' ? { branch: wanted } : {};

  const deviceId = await resolveDevice(ctx.client, deviceRef);
  const started = await ctx.client.createSession({
    deviceId,
    projectPath,
    provider,
    ...(worktree === undefined ? {} : { worktree }),
    ...(Object.keys(config).length > 0 ? { config } : {}),
    text,
  });
  const info = {
    sessionId: started.sessionId,
    promptId: started.promptId,
    ...(started.worktree ? { worktree: started.worktree } : {}),
  };
  if (options === undefined) {
    if (ctx.json) ctx.doc(info);
    else {
      ctx.line(started.sessionId);
      if (started.worktree)
        ctx.note(`worktree ${started.worktree.path} (${started.worktree.branch})`);
    }
    return 0;
  }
  if (!ctx.json) {
    ctx.note(`session ${started.sessionId}`);
    if (started.worktree)
      ctx.note(`worktree ${started.worktree.path} (${started.worktree.branch})`);
  }
  const result = await ctx.client.waitForTurn(started.sessionId, {
    ...options,
    since: started.baseline,
  });
  return report(ctx, result, info);
}

export async function send(ctx: Context, sessionId: string, words: string[]): Promise<number> {
  const text = await textOf(ctx, words);
  if (text === '') throw new CliError('usage', 'The text is empty.');
  const options = bool(ctx.flags, 'wait') ? waitOptions(ctx) : undefined;
  const sent = await ctx.client.sendPrompt(sessionId, text);
  const info = { promptId: sent.promptId };
  if (options === undefined) {
    if (ctx.json) ctx.doc(info);
    else ctx.line(sent.promptId);
    return 0;
  }
  const result = await ctx.client.waitForTurn(sessionId, { ...options, since: sent.baseline });
  return report(ctx, result, info);
}

export async function wait(ctx: Context, sessionId: string): Promise<number> {
  const result = await ctx.client.waitForTurn(sessionId, waitOptions(ctx));
  return report(ctx, result, {});
}

export async function interrupt(ctx: Context, sessionId: string): Promise<number> {
  await ctx.client.interrupt(sessionId);
  if (ctx.json) ctx.doc({ ok: true });
  else ctx.line('interrupted');
  return 0;
}

export async function handoff(ctx: Context, sessionId: string, machine: string): Promise<number> {
  const targetDeviceId = await resolveDevice(ctx.client, machine);
  const targetPath = str(ctx.flags, 'path');
  const result = await ctx.client.handoff(sessionId, {
    targetDeviceId,
    ...(targetPath === undefined ? {} : { targetPath }),
  });
  ctx.doc(result);
  return 0;
}
