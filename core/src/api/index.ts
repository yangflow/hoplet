/** REST API and browser WebSocket for the web UI, and the static file handler for the built SPA. */
import type { IncomingMessage, Server, ServerResponse } from 'node:http';
import { bearerToken, type Config, secretsMatch } from '../config/index.ts';
import type { Control } from '../control/index.ts';
import { log, registerSecret } from '../log/index.ts';
import { createNotify, type Notify } from '../notify/index.ts';
import type { Store } from '../store/index.ts';
import { attachBrowserSocket } from './browser.ts';
import { fail } from './http.ts';
import { createRouter } from './router.ts';
import { routes } from './routes.ts';
import { serveStatic } from './static.ts';
import { createTickets } from './tickets.ts';

export interface ApiOptions {
  /** The clock tickets expire by. */
  now?: () => number;
  /** The notifications; the API answers for it the action routes. Defaults to one that is not started. */
  notify?: Notify;
}

export interface Api {
  /** Serves REST and the static UI on `server` and accepts browser WebSockets on `/ws`. */
  attach(server: Server): void;
}

export function createApi(
  store: Store,
  config: Config,
  control: Control,
  options: ApiOptions = {},
): Api {
  registerSecret(config.token);
  const tickets = createTickets(options.now);
  const notify =
    options.notify ??
    createNotify(store, control, {
      publicUrl: config.publicUrl ?? null,
      ...(options.now ? { now: options.now } : {}),
    });
  const router = createRouter({
    routes,
    store,
    control,
    tickets,
    notify,
    ...(options.now ? { now: options.now } : {}),
    hostAuthorized: (req) => secretsMatch(bearerToken(req.headers.authorization), config.token),
  });

  const handleRequest = (req: IncomingMessage, res: ServerResponse): void => {
    const url = new URL(req.url ?? '/', `http://${req.headers.host ?? 'localhost'}`);
    // Only the API authenticates; the SPA static pages are public (all data flows through
    // the token-bearing /api and the ticketed /ws)
    if (url.pathname.startsWith('/api/')) {
      router.handle(req, res, url).catch((err) => {
        log('error', 'api', 'request failed', { error: String(err) });
        if (!res.headersSent) fail(res, 'internal');
      });
      return;
    }
    if (config.webDir) {
      try {
        serveStatic(res, url.pathname, config.webDir);
      } catch (err) {
        log('error', 'api', 'request failed', { error: String(err) });
        fail(res, 'internal');
      }
      return;
    }
    fail(res, 'not-found');
  };

  return {
    attach(server) {
      server.on('request', handleRequest);
      attachBrowserSocket(server, control, tickets);
    },
  };
}
