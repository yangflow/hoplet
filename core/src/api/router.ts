/**
 * The route table engine. A route declares its method, path, authentication, body limit, request and
 * response schemas and handler (api/routes.ts); this file supplies everything around the handler:
 * matching, authentication, body-size limits, schema validation, error mapping and 405.
 */
import type { IncomingMessage, ServerResponse } from 'node:http';
import { type GenericSchema, type InferOutput, safeParse } from '@hoplet/shared';
import { BulkBusyError, type Control } from '../control/index.ts';
import type { Hooks } from '../hooks/index.ts';
import { log } from '../log/index.ts';
import type { Notify } from '../notify/index.ts';
import { AuditUnavailableError, type Store } from '../store/index.ts';
import { ApiError, fail, json, RawResponse, readBody } from './http.ts';
import { createRateLimiter } from './ratelimit.ts';
import type { Tickets } from './tickets.ts';

export type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

/** What a handler may use besides the request: the modules api is allowed to call. */
export interface ApiDeps {
  store: Store;
  control: Control;
  tickets: Tickets;
  notify: Notify;
  hooks: Hooks;
}

type Parsed<S> = S extends GenericSchema ? InferOutput<S> : undefined;

/** What a handler receives: the path parameters and the request already parsed by its schemas. */
export interface Call<Q, B> extends ApiDeps {
  params: Record<string, string>;
  query: Q;
  body: B;
}

export interface RouteSpec<
  Q extends GenericSchema | undefined,
  B extends GenericSchema | undefined,
> {
  method: Method;
  /** `/api/...` with `:name` segments for path parameters. */
  path: string;
  /**
   * `host`: the host token is required. `token`: the request carries its own credential, a signed action token
   * in the body (ADR 0021); the route is rate limited and must check the token itself.
   */
  auth: 'host' | 'token';
  /**
   * `control` runs the handler on arrival. `bulk` runs it through the bulk queue (control/bulk.ts), so
   * it never delays a control request and a flood of it is refused with `busy`.
   */
  lane: 'control' | 'bulk';
  /** Largest request body in bytes; required with `body`. */
  bodyLimit?: number;
  /** Schema of the query string. */
  query?: Q;
  /** Schema of the JSON request body. */
  body?: B;
  /** Status of the success response; default 200. */
  status?: number;
  /** Schema of the success response; the handler's return value has this shape. */
  response: GenericSchema;
  handler: (call: Call<Parsed<Q>, Parsed<B>>) => unknown;
}

export interface Route {
  method: Method;
  path: string;
  auth: 'host' | 'token';
  lane: 'control' | 'bulk';
  bodyLimit: number;
  query: GenericSchema | undefined;
  body: GenericSchema | undefined;
  status: number;
  response: GenericSchema;
  pattern: RegExp;
  paramNames: string[];
  handler: (call: Call<unknown, unknown>) => unknown;
}

export function defineRoute<
  Q extends GenericSchema | undefined = undefined,
  B extends GenericSchema | undefined = undefined,
>(spec: RouteSpec<Q, B>): Route {
  if (spec.body && !spec.bodyLimit)
    throw new Error(`route ${spec.path} declares a body without a limit`);
  const paramNames: string[] = [];
  const source = spec.path.replace(/:([A-Za-z]+)/g, (_, name: string) => {
    paramNames.push(name);
    return '([A-Za-z0-9_.-]+)';
  });
  return {
    method: spec.method,
    path: spec.path,
    auth: spec.auth,
    lane: spec.lane,
    bodyLimit: spec.bodyLimit ?? 0,
    query: spec.query,
    body: spec.body,
    status: spec.status ?? 200,
    response: spec.response,
    pattern: new RegExp(`^${source}$`),
    paramNames,
    handler: spec.handler as Route['handler'],
  };
}

export interface RouterOptions extends ApiDeps {
  routes: readonly Route[];
  /** Whether the request carries the host token. */
  hostAuthorized(req: IncomingMessage): boolean;
  /** The clock of the rate limit of `token` routes. */
  now?: () => number;
}

/** Requests per minute a `token` route answers: one address, and all addresses together. */
const TOKEN_ROUTE_PER_ADDRESS = 30;
const TOKEN_ROUTE_TOTAL = 300;

export interface Router {
  /** Answers a request under `/api/`. */
  handle(req: IncomingMessage, res: ServerResponse, url: URL): Promise<void>;
}

export function createRouter(options: RouterOptions): Router {
  const { routes, hostAuthorized } = options;
  const deps: ApiDeps = {
    store: options.store,
    control: options.control,
    tickets: options.tickets,
    notify: options.notify,
    hooks: options.hooks,
  };
  const perAddress = createRateLimiter(TOKEN_ROUTE_PER_ADDRESS, 60_000, options.now);
  const total = createRateLimiter(TOKEN_ROUTE_TOTAL, 60_000, options.now);

  return {
    async handle(req, res, url) {
      // Match the original target: URL parsing must not turn dot segments into another session.
      const pathname = (req.url ?? '/').split('?')[0] ?? '/';
      const method = req.method ?? 'GET';
      const onPath = routes.filter((r) => r.pattern.test(pathname));
      const route = onPath.find((r) => r.method === method);
      // Every /api path needs the host token, known or not, so the answer never maps the routes; a `token`
      // route is the exception and answers to its own credential.
      if (route?.auth !== 'token' && !hostAuthorized(req)) {
        fail(res, 'unauthorized');
        return;
      }
      if (route?.auth === 'token') {
        const address = req.socket.remoteAddress ?? 'unknown';
        if (!perAddress.allow(address) || !total.allow('all')) {
          res.setHeader('retry-after', '60');
          fail(res, 'rate-limited');
          return;
        }
      }
      if (!route) {
        if (onPath.length === 0) {
          fail(res, 'not-found');
          return;
        }
        res.setHeader('allow', onPath.map((r) => r.method).join(', '));
        fail(res, 'method-not-allowed');
        return;
      }
      try {
        const match = route.pattern.exec(pathname);
        const params: Record<string, string> = {};
        route.paramNames.forEach((name, i) => {
          params[name] = match?.[i + 1] ?? '';
        });
        const query = route.query ? parseQuery(route.query, url) : undefined;
        const body = route.body
          ? await parseBody(route.body, req, res, route.bodyLimit)
          : undefined;
        const call = { ...deps, params, query, body };
        const run = async (): Promise<unknown> => {
          const result = await route.handler(call);
          if (result instanceof RawResponse) await result.send(res);
          return result;
        };
        const result = route.lane === 'bulk' ? await deps.control.bulk.run(run) : await run();
        if (!(result instanceof RawResponse)) json(res, route.status, result);
      } catch (err) {
        if (err instanceof BulkBusyError) {
          fail(res, 'busy');
          return;
        }
        if (err instanceof ApiError) {
          fail(res, err.code, err.extra, err.status);
          return;
        }
        if (err instanceof AuditUnavailableError) {
          log('error', 'api', 'audit log unavailable', {
            route: route.path,
            error: String(err.cause),
          });
          fail(res, 'audit-unavailable');
          return;
        }
        log('error', 'api', 'request failed', { route: route.path, error: String(err) });
        fail(res, 'internal');
      }
    },
  };
}

function parseQuery(schema: GenericSchema, url: URL): unknown {
  const parsed = safeParse(schema, Object.fromEntries(url.searchParams));
  if (!parsed.success) throw new ApiError('invalid-request');
  return parsed.output;
}

async function parseBody(
  schema: GenericSchema,
  req: IncomingMessage,
  res: ServerResponse,
  limit: number,
): Promise<unknown> {
  let raw: string;
  try {
    raw = await readBody(req, limit);
  } catch {
    // The unread rest of the body would otherwise sit on a kept-alive connection.
    res.setHeader('connection', 'close');
    throw new ApiError('too-large');
  }
  let body: unknown;
  try {
    body = JSON.parse(raw);
  } catch {
    throw new ApiError('invalid-json');
  }
  const parsed = safeParse(schema, body);
  if (!parsed.success) throw new ApiError('invalid-request');
  return parsed.output;
}
