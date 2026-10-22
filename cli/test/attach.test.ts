import type { Hoplet } from '@hoplet/sdk';
import { describe, expect, it } from 'vitest';
import { docs, lines, on, run, session } from './support.ts';

type Msg = Record<string, unknown>;

const output = (kind: string, extra: Msg = {}): Msg => ({
  type: 'session.output',
  sessionId: 's1',
  kind,
  ...extra,
});
const runtime = (state: string, extra: Msg = {}): Msg => ({
  type: 'session.runtime',
  sessionId: 's1',
  state,
  ...extra,
});
const permission = (requestId: string, extra: Msg = {}): Msg => ({
  type: 'session.permission',
  sessionId: 's1',
  requestId,
  tool: 'Bash',
  input: { command: 'ls' },
  ...extra,
});

/** Replaces `subscribe` with a stream of the given messages. */
function streaming(messages: Msg[], state: { closed: boolean } = { closed: false }) {
  return (client: Hoplet) => {
    client.subscribe = (() => ({
      close: () => {
        state.closed = true;
      },
      async *[Symbol.asyncIterator]() {
        yield { type: 'sdk.connected', reconnected: false };
        for (const m of messages) yield m;
      },
    })) as unknown as Hoplet['subscribe'];
  };
}

const current = on('GET', '/api/sessions/s1', { json: session('ready') });
const answered = (decisions: Msg[]) =>
  on('POST', '/api/sessions/s1/permission', (call) => {
    decisions.push(call.body as Msg);
    return { json: { ok: true } };
  });

describe('hoplet attach', () => {
  it('prints the output and ends with the turn, exit 0 when it is ready', async () => {
    const closed = { closed: false };
    const r = await run(['attach', 's1'], {
      handlers: [current],
      patch: streaming(
        [
          runtime('running'),
          output('text', { text: 'Hello ' }),
          output('text', { text: 'there' }),
          output('thinking', { text: 'secret' }),
          output('tool-call', { tool: 'Bash', input: { command: 'ls' } }),
          output('tool-result', { text: 'a.txt\nb.txt' }),
          { type: 'session.output', sessionId: 'other', kind: 'text', text: 'not mine' },
          runtime('ready'),
          output('text', { text: 'after the end' }),
        ],
        closed,
      ),
    });
    expect(r.code).toBe(0);
    expect(r.out).toBe('Hello there\n> Bash {"command":"ls"}\n< a.txt\n');
    expect(r.err).toContain('attached to A session');
    expect(closed.closed).toBe(true);
  });

  it('exits 4 when the turn failed and 5 when it ended or the session was deleted', async () => {
    const failed = await run(['attach', 's1'], {
      handlers: [current],
      patch: streaming([runtime('running'), runtime('failed', { error: 'boom' })]),
    });
    expect(failed.code).toBe(4);
    expect(failed.err).toContain('failed: boom');
    const ended = await run(['attach', 's1'], {
      handlers: [current],
      patch: streaming([runtime('running'), runtime('idle')]),
    });
    expect(ended.code).toBe(5);
    const deleted = await run(['attach', 's1'], {
      handlers: [current],
      patch: streaming([{ type: 'session.deleted', sessionId: 's1' }]),
    });
    expect(deleted.code).toBe(5);
  });

  it('does not end on the state the session was already in', async () => {
    const r = await run(['attach', 's1'], {
      handlers: [current],
      patch: streaming([runtime('ready'), output('text', { text: 'x\n' })]),
    });
    expect(r.code).toBe(0);
    expect(r.out).toBe('x\n');
  });

  it('answers a permission request with --answer, once per request', async () => {
    const decisions: Msg[] = [];
    const r = await run(['attach', 's1', '--answer', 'allow'], {
      handlers: [current, answered(decisions)],
      patch: streaming([
        runtime('running'),
        permission('r1'),
        permission('r1'),
        runtime('waiting-permission'),
        runtime('running'),
        runtime('ready'),
      ]),
    });
    expect(r.code).toBe(0);
    expect(decisions).toEqual([{ requestId: 'r1', decision: 'allow' }]);
    expect(r.err).toContain('answered allow');
  });

  it('approves a plan with its default mode', async () => {
    const decisions: Msg[] = [];
    await run(['attach', 's1', '--answer', 'allow'], {
      handlers: [current, answered(decisions)],
      patch: streaming([
        permission('r1', { planExit: { plan: 'p', default: 'acceptEdits', modes: [] } }),
        runtime('ready'),
      ]),
    });
    expect(decisions[0]).toMatchObject({ decision: 'allow', mode: 'acceptEdits' });
  });

  it('asks on a terminal, and only waits where nobody can answer', async () => {
    const decisions: Msg[] = [];
    const asked: string[] = [];
    const yes = await run(['attach', 's1'], {
      handlers: [current, answered(decisions)],
      deps: {
        ask: async (q) => {
          asked.push(q);
          return 'y';
        },
      },
      patch: streaming([runtime('running'), permission('r1'), runtime('ready')]),
    });
    expect(yes.code).toBe(0);
    expect(asked[0]).toContain('Allow Bash');
    expect(decisions).toEqual([{ requestId: 'r1', decision: 'allow' }]);

    const none: Msg[] = [];
    const quiet = await run(['attach', 's1'], {
      handlers: [current, answered(none)],
      patch: streaming([permission('r1')]),
    });
    expect(none).toEqual([]);
    expect(quiet.err).toContain('Waiting for an answer');
  });

  it('ignores a request someone else already answered', async () => {
    const r = await run(['attach', 's1', '--answer', 'deny'], {
      handlers: [
        current,
        on('POST', '/api/sessions/s1/permission', {
          status: 409,
          json: { code: 'request-closed' },
        }),
      ],
      patch: streaming([permission('r1'), runtime('ready')]),
    });
    expect(r.code).toBe(0);
    expect(r.err).toContain('Already answered elsewhere');
  });

  it('prints one object per line with --json', async () => {
    const r = await run(['attach', 's1', '--json'], {
      handlers: [current],
      patch: streaming([runtime('running'), output('text', { text: 'hi' }), runtime('ready')]),
    });
    expect(lines(r.out)).toHaveLength(3);
    expect(docs(r.out)).toMatchObject([
      { type: 'session.runtime', state: 'running' },
      { type: 'session.output', text: 'hi' },
      { type: 'session.runtime', state: 'ready' },
    ]);
    expect(r.err).toBe('');
  });

  it('leaves on Ctrl-C with exit 0, and refuses a bad --answer', async () => {
    const controller = new AbortController();
    let closed = false;
    const r = await run(['attach', 's1'], {
      handlers: [current],
      deps: { signal: controller.signal },
      patch: (client) => {
        client.subscribe = (() => ({
          close: () => {
            closed = true;
          },
          async *[Symbol.asyncIterator]() {
            yield { type: 'sdk.connected', reconnected: false };
            controller.abort();
          },
        })) as unknown as Hoplet['subscribe'];
      },
    });
    expect(r.code).toBe(0);
    expect(closed).toBe(true);
    expect((await run(['attach', 's1', '--answer', 'maybe'], { handlers: [current] })).code).toBe(
      1,
    );
  });
});
