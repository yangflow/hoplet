/** Entrypoint: wire config, store, control, REST API and WS ingestion, then listen. */
import { createServer } from 'node:http';
import { resolve } from 'node:path';
import { createApi } from './api/index.ts';
import { loadConfig } from './config/index.ts';
import { createControl } from './control/index.ts';
import { attachIngest } from './ingest/index.ts';
import { log } from './log/index.ts';
import { createNotify } from './notify/index.ts';
import { Store } from './store/index.ts';

const config = loadConfig();
const store = new Store(config.dataDir);
store.startUsageBackfill();
const control = createControl(store);
const server = createServer();
attachIngest(server, store, control);
const notify = createNotify(store, control, { publicUrl: config.publicUrl ?? null });
notify.start();
createApi(store, config, control, { notify }).attach(server);
// The two WebSocket paths are taken by the modules above; any other upgrade is refused.
server.on('upgrade', (req, socket) => {
  const { pathname } = new URL(req.url ?? '/', 'http://localhost');
  if (pathname !== '/ws' && pathname !== '/ws/agent') socket.destroy();
});

server.listen(config.port, () => {
  log('info', 'main', 'listening', {
    port: config.port,
    dataDir: resolve(config.dataDir),
    sessions: store.sessionCount(),
    web: config.webDir ? resolve(config.webDir) : null,
  });
});
