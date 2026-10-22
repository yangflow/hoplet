import { Hoplet, type HopletOptions, PROTOCOL_VERSION } from '@hoplet/sdk';
import type { Deps } from '../src/context.ts';
import { runCli } from '../src/run.ts';

export type Call = { method: string; path: string; body: unknown; authorization: string | null };
export type Reply = { status?: number; json?: unknown; text?: string };
export type Handler = (call: Call) => Reply | undefined;

/** Answers one method and path (the query string is ignored when `path` has none). */
export function on(method: string, path: string, reply: Reply | ((call: Call) => Reply)): Handler {
  return (call) => {
    const bare = path.includes('?') ? call.path : call.path.split('?')[0];
    if (call.method !== method || bare !== path) return undefined;
    return typeof reply === 'function' ? reply(call) : reply;
  };
}

const health: Handler = (call) =>
  call.path === '/api/health'
    ? {
        json: {
          ok: true,
          sessions: 0,
          uptimeSec: 1,
          coreVersion: '0.0.0',
          protocolVersion: PROTOCOL_VERSION,
        },
      }
    : undefined;

/** A fetch that answers from `handlers` (first reply wins) and records every call. */
export function fakeFetch(...handlers: Handler[]): { fetch: typeof fetch; calls: Call[] } {
  const calls: Call[] = [];
  const impl = async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input));
    const call: Call = {
      method: init?.method ?? 'GET',
      path: `${url.pathname}${url.search}`,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      authorization: new Headers(init?.headers).get('authorization'),
    };
    calls.push(call);
    for (const handler of [health, ...handlers]) {
      const reply = handler(call);
      if (!reply) continue;
      return new Response(reply.text ?? JSON.stringify(reply.json ?? {}), {
        status: reply.status ?? 200,
      });
    }
    return new Response(JSON.stringify({ code: 'not-found' }), { status: 404 });
  };
  return { fetch: impl as typeof fetch, calls };
}

export function session(
  state: string,
  extra: Record<string, unknown> = {},
  overrides: Record<string, unknown> = {},
) {
  return {
    sessionId: 's1',
    projectPath: '/work/app',
    projectDir: '-work-app',
    deviceName: 'laptop',
    ownerDeviceId: 'dev1',
    ownerEpoch: 1,
    title: 'A session',
    provider: 'claude',
    createdAt: 1,
    updatedAt: 2,
    lineCount: 3,
    byteSize: 30,
    ownerConnected: true,
    runtime: { state, ...extra },
    ...overrides,
  };
}

export function device(deviceId: string, name: string, connected = true) {
  return { deviceId, name, connected, system: null, providers: [], labels: [] };
}

export const devicesHandler = on('GET', '/api/devices', {
  json: { devices: [device('dev1', 'laptop'), device('dev2', 'desktop', false)] },
});

export type RunOptions = {
  env?: Record<string, string | undefined>;
  handlers?: Handler[];
  deps?: Partial<Deps>;
  /** Wraps the client the command gets, to replace one of its methods. */
  patch?: (client: Hoplet) => void;
};

export type Result = {
  code: number;
  out: string;
  err: string;
  calls: Call[];
  options: HopletOptions[];
};

/** Runs one command line against a fake core. */
export async function run(argv: string[], opts: RunOptions = {}): Promise<Result> {
  const { fetch, calls } = fakeFetch(...(opts.handlers ?? []));
  let out = '';
  let err = '';
  const options: HopletOptions[] = [];
  const code = await runCli(argv, {
    env: { HOPLET_TOKEN: 'tok', HOPLET_URL: 'http://core.test', ...opts.env },
    io: {
      stdout: (text) => {
        out += text;
      },
      stderr: (text) => {
        err += text;
      },
    },
    fetch,
    createClient: (o) => {
      options.push(o);
      const client = new Hoplet(o);
      opts.patch?.(client);
      return client;
    },
    ...opts.deps,
  });
  return { code, out, err, calls, options };
}

export const lines = (text: string): string[] => text.split('\n').filter((l) => l !== '');
export const docs = (text: string): unknown[] => lines(text).map((l) => JSON.parse(l));

/** The JSON body of the first POST a run made. */
export const posted = (r: { calls: Call[] }): Record<string, unknown> =>
  (r.calls.find((c) => c.method === 'POST')?.body ?? {}) as Record<string, unknown>;
