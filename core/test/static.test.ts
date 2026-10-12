import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import type { ServerResponse } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { serveStatic } from '../src/api/static.ts';

let root = '';
let web = '';

beforeEach(() => {
  root = mkdtempSync(path.join(tmpdir(), 'hoplet-static-'));
  web = path.join(root, 'web');
  mkdirSync(path.join(web, 'assets'), { recursive: true });
  writeFileSync(path.join(web, 'index.html'), '<html>shell</html>');
  writeFileSync(path.join(web, 'assets', 'app.js'), 'console.log(1)');
  // A sibling whose name starts with the web root's name, and a file beside the web root.
  mkdirSync(`${web}-private`);
  writeFileSync(path.join(`${web}-private`, 'secret.txt'), 'secret');
  writeFileSync(path.join(root, 'outside.txt'), 'outside');
});

afterEach(() => rmSync(root, { recursive: true, force: true }));

function serve(urlPath: string): { status: number; headers: Record<string, string>; body: string } {
  const out = { status: 0, headers: {} as Record<string, string>, body: '' };
  const res = {
    writeHead(status: number, headers: Record<string, string> = {}) {
      out.status = status;
      out.headers = headers;
    },
    end(chunk?: string | Buffer) {
      out.body = chunk === undefined ? '' : chunk.toString();
    },
  } as unknown as ServerResponse;
  serveStatic(res, urlPath, web);
  return out;
}

describe('serveStatic', () => {
  it('serves the shell at the root and files with their type', () => {
    expect(serve('/').body).toBe('<html>shell</html>');
    const js = serve('/assets/app.js');
    expect(js.status).toBe(200);
    expect(js.headers['content-type']).toBe('text/javascript');
  });

  it('answers unknown paths with the shell so client routes load', () => {
    const res = serve('/sessions/abc');
    expect(res.status).toBe(200);
    expect(res.body).toBe('<html>shell</html>');
  });

  it('never serves a file outside the web root', () => {
    for (const p of [
      '/../outside.txt',
      '/assets/../../outside.txt',
      '/../web-private/secret.txt',
      '/..%2foutside.txt',
    ]) {
      const res = serve(p);
      expect(res.body, p).not.toContain('outside');
      expect(res.body, p).not.toContain('secret');
    }
  });
});
