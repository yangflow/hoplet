import { existsSync, readFileSync, statSync } from 'node:fs';
import type { ServerResponse } from 'node:http';
import { extname, normalize, resolve, sep } from 'node:path';

const MIME: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript',
  '.css': 'text/css',
  '.json': 'application/json',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.webmanifest': 'application/manifest+json',
  '.woff2': 'font/woff2',
};

/** Serves the built web UI. A path resolves inside `webDir` or is refused; unknown paths get the SPA shell. */
export function serveStatic(res: ServerResponse, urlPath: string, webDir: string): void {
  const webRoot = resolve(webDir);
  let target = resolve(webRoot, `.${normalize(urlPath === '/' ? '/index.html' : urlPath)}`);
  if (target !== webRoot && !target.startsWith(webRoot + sep)) {
    res.writeHead(403);
    res.end();
    return;
  }
  if (!existsSync(target) || statSync(target).isDirectory()) {
    target = resolve(webRoot, 'index.html'); // SPA fallback
  }
  if (!existsSync(target)) {
    res.writeHead(404);
    res.end('not found');
    return;
  }
  res.writeHead(200, { 'content-type': MIME[extname(target)] ?? 'application/octet-stream' });
  res.end(readFileSync(target));
}
