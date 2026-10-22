import { HopletError, type HopletEvent, type ServerMessage } from '@hoplet/sdk';
import { str } from './args.ts';
import type { Context } from './context.ts';
import { CliError } from './errors.ts';

const BUSY = new Set(['starting', 'running', 'waiting-permission']);
const SUMMARY_MAX = 120;

type Output = Extract<ServerMessage, { type: 'session.output' }>;
type Permission = Extract<ServerMessage, { type: 'session.permission' }>;

function summarize(value: unknown): string {
  const text = typeof value === 'string' ? value : (JSON.stringify(value) ?? '');
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length > SUMMARY_MAX ? `${flat.slice(0, SUMMARY_MAX - 1)}…` : flat;
}

/** Writes streamed output as text; a line break is added only where a different kind of line starts. */
class Printer {
  private midLine = false;
  private readonly ctx: Context;

  constructor(ctx: Context) {
    this.ctx = ctx;
  }

  private startLine(): void {
    if (this.midLine) this.ctx.deps.io.stdout('\n');
    this.midLine = false;
  }

  output(msg: Output): void {
    const { io } = this.ctx.deps;
    if (msg.kind === 'text') {
      if (msg.text === undefined || msg.text === '') return;
      io.stdout(msg.text);
      this.midLine = !msg.text.endsWith('\n');
    } else if (msg.kind === 'tool-call') {
      this.startLine();
      io.stdout(`> ${msg.tool ?? 'tool'} ${summarize(msg.input)}\n`);
    } else if (msg.kind === 'tool-result') {
      this.startLine();
      const first = (msg.text ?? '').split('\n')[0] ?? '';
      io.stdout(`< ${msg.isError ? 'error: ' : ''}${summarize(first)}\n`);
    }
    // Thinking is not shown; --json carries it.
  }

  note(text: string): void {
    this.startLine();
    this.ctx.deps.io.stderr(`${text}\n`);
  }
}

/** The decision for a permission request, or null when nobody answers. */
async function decide(ctx: Context, msg: Permission): Promise<'allow' | 'deny' | null> {
  const preset = str(ctx.flags, 'answer');
  if (preset === 'allow' || preset === 'deny') return preset;
  const { ask } = ctx.deps;
  if (!ask) return null;
  const what = msg.planExit ? 'Approve the plan' : `Allow ${msg.tool} ${summarize(msg.input)}`;
  const reply = (await ask(`${what}? [y/N] `)).trim().toLowerCase();
  return reply === 'y' || reply === 'yes' ? 'allow' : 'deny';
}

/**
 * Follows a session: prints its streamed output and answers permission requests. It ends when a turn that ran
 * ends, with the exit code of that result, or on Ctrl-C.
 */
export async function attach(ctx: Context, sessionId: string): Promise<number> {
  const preset = str(ctx.flags, 'answer');
  if (preset !== undefined && preset !== 'allow' && preset !== 'deny') {
    throw new CliError('usage', '--answer is allow or deny');
  }
  const printer = new Printer(ctx);
  const first = await ctx.client.getSession(sessionId);
  if (!ctx.json) {
    printer.note(`attached to ${first.title} (${first.runtime.state}); Ctrl-C to leave`);
  }

  const stream = ctx.client.subscribe({ sessionIds: [sessionId] });
  const stop = (): void => stream.close();
  ctx.deps.signal?.addEventListener('abort', stop);
  const answered = new Set<string>();
  let turnRan = false;
  try {
    for await (const event of stream as AsyncIterable<HopletEvent>) {
      if (event.type === 'sdk.connected') continue;
      if (!('sessionId' in event) || event.sessionId !== sessionId) continue;
      if (ctx.json) ctx.doc(event);

      switch (event.type) {
        case 'session.output':
          if (!ctx.json) printer.output(event);
          break;
        case 'session.permission': {
          if (answered.has(event.requestId)) break;
          answered.add(event.requestId);
          if (!ctx.json) printer.note(`permission: ${event.tool} ${summarize(event.input)}`);
          const decision = await decide(ctx, event);
          if (decision === null) {
            if (!ctx.json) printer.note('Waiting for an answer (--answer, or the web UI).');
            break;
          }
          try {
            await ctx.client.answerPermission(sessionId, {
              requestId: event.requestId,
              decision,
              ...(decision === 'allow' && event.planExit ? { mode: event.planExit.default } : {}),
            });
            if (!ctx.json) printer.note(`answered ${decision}`);
          } catch (err) {
            if (!(err instanceof HopletError && err.code === 'request-closed')) throw err;
            if (!ctx.json) printer.note('Already answered elsewhere.');
          }
          break;
        }
        case 'session.runtime': {
          const { state } = event;
          if (BUSY.has(state)) {
            turnRan = true;
          } else if (turnRan) {
            if (!ctx.json) printer.note(`${state}${event.error ? `: ${event.error}` : ''}`);
            if (state === 'ready') return 0;
            return state === 'failed' ? 4 : 5;
          }
          break;
        }
        case 'session.deleted':
          if (!ctx.json) printer.note('The session was deleted.');
          return 5;
        default:
          break;
      }
    }
    return 0;
  } finally {
    ctx.deps.signal?.removeEventListener('abort', stop);
    stream.close();
  }
}
