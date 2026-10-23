/**
 * The route table engine. A route declares its method, path, access, body limit, request and
 * response schemas and handler (api/routes.ts); this file supplies everything around the handler:
 * matching, authentication, the scope of an API token, body-size limits, schema validation, error mapping and 405.
 */
import type { IncomingMessage, ServerResponse } from 'node:http';
import { type GenericSchema, type InferOutput, safeParse } from '@hoplet/shared';
import { BulkBusyError, type Control } from '../control/index.ts';
import type { Hooks } from '../hooks/index.ts';
import { log } from '../log/index.ts';
import type { Notify } from '../notify/index.ts';
import { AuditUnavailableError, type Store } from '../store/index.ts';
import { ApiError, fail, json, RawResponse, readBody } from './http.ts';
import { type Access, can, type Principal, seesDevice, seesSession } from './principal.ts';
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
  /** The clock, in epoch milliseconds. */
  now: () => number;
}

type Parsed<S> = S extends GenericSchema ? InferOutput<S> : undefined;

/** What a handler receives: the path parameters and the request already parsed by its schemas. */
export interface Call<Q, B> extends ApiDeps {
  /** Who asks: the host, an API token, or (on a `link` route) nobody the router knows. */
  principal: Principal;
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
   * Who may call it; `host` when absent. `host`: the host token only. `{ capability }`: the host token, or an API
   * token whose grant has that ability (ADR 0033). `link`: the request carries its own credential, a signed
   * action token in the body (ADR 0021); the route is rate limited and must check the token itself.
   */
  access?: Access;
  /**
   * `control` runs the handler on arrival. `bulk` runs it through the bulk queue (control/bulk.ts), so
   * it never delays a control request and a flood of it is refused with `busy`.
   */
  lane: 'control' | 'bulk';
  /** A session's own token (ADR 0034) may call the route; every other route refuses it with `forbidden`. */
  session?: boolean;
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
  access: Access;
  session: boolean;
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
    access: spec.access ?? 'host',
    session: spec.session ?? false,
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

export interface RouterOptions extends Omit<ApiDeps, 'now'> {
  routes: readonly Route[];
  /** Who the request's bearer credential is: the host, an API token, or null for none or an unknown one. */
  authenticate(req: IncomingMessage): Principal | null;
  /** The clock of the rate limits. */
  now?: () => number;
}

/** Requests per minute a `link` route answers: one address, and all addresses together. */
const LINK_ROUTE_PER_ADDRESS = 30;
const LINK_ROUTE_TOTAL = 300;

/** Requests per minute one API token may make. */
export const TOKEN_REQUESTS_PER_MINUTE = 300;

/** A token's refusals on one route are recorded at most this often. */
const DENIED_AUDIT_INTERVAL_MS = 60_000;

export interface Router {
  /** Answers a request under `/api/`. */
  handle(req: IncomingMessage, res: ServerResponse, url: URL): Promise<void>;
}

export function createRouter(options: RouterOptions): Router {
  const { routes, authenticate } = options;
  const now = options.now ?? Date.now;
  const deps: ApiDeps = {
    store: options.store,
    control: options.control,
    tickets: options.tickets,
    notify: options.notify,
    hooks: options.hooks,
    now,
  };
  const perAddress = createRateLimiter(LINK_ROUTE_PER_ADDRESS, 60_000, options.now);
  const total = createRateLimiter(LINK_ROUTE_TOTAL, 60_000, options.now);
  const perToken = createRateLimiter(TOKEN_REQUESTS_PER_MINUTE, 60_000, options.now);
  const deniedAt = new Map<string, number>();
  /** The refusals of a tree's limits (ADR 0035), audited as `token.denied` with the code as the reason. */
  const LIMIT_REFUSALS: ReadonlySet<string> = new Set([
    'depth-limit',
    'children-limit',
    'budget-exceeded',
  ]);

  /** Records a token's refusal in the audit log, at most once a minute per token and route. */
  function audited(principal: Principal, route: Route, reason: string = 'forbidden'): void {
    if (principal.kind !== 'token') return;
    const session = principal.token.kind === 'session';
    const at = now();
    if (deniedAt.size > 10_000) {
      for (const [key, last] of deniedAt) {
        if (at - last >= DENIED_AUDIT_INTERVAL_MS) deniedAt.delete(key);
      }
    }
    const key = `${principal.token.id}\n${route.method} ${route.path}\n${reason}`;
    const last = deniedAt.get(key);
    if (last !== undefined && at - last < DENIED_AUDIT_INTERVAL_MS) return;
    deniedAt.set(key, at);
    try {
      deps.store.audit(
        {
          type: 'token.denied',
          actor: session
            ? { kind: 'session', id: principal.token.sessionId }
            : { kind: 'token', id: principal.token.id },
          ...(session ? { sessionId: principal.token.sessionId } : {}),
          summary: {
            id: principal.token.id,
            method: route.method,
            path: route.path,
            reason,
          },
        },
        at,
      );
    } catch (err) {
      // The refusal stands whether or not it could be recorded.
      log('error', 'api', 'token.denied could not be recorded', { error: String(err) });
    }
  }

  /**
   * What an API token's grant refuses before the handler runs: a route its abilities do not cover, a machine it does
   * not list (`forbidden`), a session outside its machines and projects (`session-not-found`, as if it did not exist).
   */
  function scopeRefusal(
    principal: Principal,
    route: Route,
    params: Record<string, string>,
  ): 'forbidden' | 'session-not-found' | null {
    const { access } = route;
    if (access === 'link') return null;
    if (access === 'host' || !can(principal, access.capability)) return 'forbidden';
    if (principal.kind === 'token' && principal.token.kind === 'session' && !route.session) {
      return 'forbidden';
    }
    const { sessionId, deviceId } = params;
    if (sessionId !== undefined && !seesSession(principal, deps.control, sessionId)) {
      return 'session-not-found';
    }
    if (deviceId !== undefined && !seesDevice(principal, deviceId)) return 'forbidden';
    return null;
  }

  return {
    async handle(req, res, url) {
      // Match the original target: URL parsing must not turn dot segments into another session.
      const pathname = (req.url ?? '/').split('?')[0] ?? '/';
      const method = req.method ?? 'GET';
      const onPath = routes.filter((r) => r.pattern.test(pathname));
      const route = onPath.find((r) => r.method === method);
      // Every /api path needs a credential, known or not, so the answer never maps the routes; a `link`
      // route is the exception and answers to its own credential.
      const link = route?.access === 'link';
      const caller = link ? null : authenticate(req);
      if (!link && !caller) {
        fail(res, 'unauthorized');
        return;
      }
      if (link) {
        const address = req.socket.remoteAddress ?? 'unknown';
        if (!perAddress.allow(address) || !total.allow('all')) {
          res.setHeader('retry-after', '60');
          fail(res, 'rate-limited');
          return;
        }
      }
      if (caller?.kind === 'token' && !perToken.allow(caller.token.id)) {
        res.setHeader('retry-after', '60');
        fail(res, 'rate-limited');
        return;
      }
      const principal: Principal = caller ?? { kind: 'link' };
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
        if (principal.kind === 'token') {
          const refused = scopeRefusal(principal, route, params);
          if (refused) {
            if (refused === 'forbidden') audited(principal, route);
            fail(res, refused);
            return;
          }
        }
        const query = route.query ? parseQuery(route.query, url) : undefined;
        const body = route.body
          ? await parseBody(route.body, req, res, route.bodyLimit)
          : undefined;
        const call = { ...deps, principal, params, query, body };
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
          if (err.code === 'forbidden') audited(principal, route);
          else if (LIMIT_REFUSALS.has(err.code)) audited(principal, route, err.code);
          if (err.retryAfter !== undefined) res.setHeader('retry-after', String(err.retryAfter));
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
