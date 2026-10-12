import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  FullBlockSchema,
  GlobalSearchResultSchema,
  LinesPageSchema,
  SearchResultSchema,
  safeParse,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { ELIDE_ABOVE_BYTES, PREVIEW_BYTES } from '../src/api/projection/index.ts';
import { createControl } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';
import { assistant, userResult, userText } from './fixtures/claude-grammar.ts';

const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const cleanup of cleanups.splice(0)) cleanup();
});

const SESSION = 'read-s1';
const BIG_OUTPUT = `${'tool output row\n'.repeat(4000)}tail-marker`;
const BIG_INPUT = { content: 'w'.repeat(30_000) };

const LINES = [
  userText('first prompt'),
  assistant([{ type: 'text', text: 'answer with the needle inside' }]),
  assistant([{ type: 'tool_use', id: 'big-call', name: 'Write', input: BIG_INPUT }]),
  userResult('big-call', BIG_OUTPUT),
  { type: 'mode', mode: 'normal' },
  userText('last prompt'),
];

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-read-'));
  const store = new Store(dir);
  store.append(
    { sessionId: SESSION, projectDir: 'p', filePath: '/x', deviceId: 'd' },
    LINES.map((l) => JSON.stringify(l)),
  );
  const server = createServer();
  createApi(
    store,
    { port: 0, dataDir: dir, token: 'host-token', webDir: null },
    createControl(store),
  ).attach(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;
  cleanups.push(() => {
    server.close();
    server.closeAllConnections();
    rmSync(dir, { force: true, recursive: true });
  });
  const get = (url: string) =>
    fetch(`http://127.0.0.1:${port}${url}`, { headers: { authorization: 'Bearer host-token' } });
  return { dir, store, get };
}

/** Every file under the session's chunk directory with its bytes. */
function chunkFiles(dir: string): Record<string, string> {
  const root = path.join(dir, 'sessions', SESSION);
  return Object.fromEntries(
    readdirSync(root)
      .sort()
      .map((name) => [name, readFileSync(path.join(root, name)).toString('base64')]),
  );
}

describe('GET /api/sessions/:id/lines', () => {
  it('serves entries of the conversation model with large blocks elided', async () => {
    const { get } = await boot();
    const res = await get(`/api/sessions/${SESSION}/lines?start=0&limit=100`);
    expect(res.status).toBe(200);
    const parsed = safeParse(LinesPageSchema, await res.json());
    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    const page = parsed.output;
    expect([page.start, page.end, page.total]).toEqual([0, 6, 6]);
    // The bookkeeping line at 4 has no entry.
    expect(page.entries.map((e) => e.lineNo)).toEqual([0, 1, 2, 3, 5]);
    expect(page.entries[0]).toMatchObject({
      role: 'user',
      blocks: [{ type: 'text', text: 'first prompt' }],
    });
    const call = page.entries[2]?.blocks[0];
    expect(call).toMatchObject({ elided: true, type: 'tool-call', ref: '2/0' });
    const result = page.entries[3]?.blocks[0];
    expect(result).toMatchObject({ elided: true, type: 'tool-result', ref: '3/0' });
    for (const block of [call, result]) {
      if (!block || !('elided' in block)) throw new Error('expected an elided block');
      expect(block.bytes).toBeGreaterThan(ELIDE_ABOVE_BYTES);
      expect(Buffer.byteLength(block.preview)).toBeLessThanOrEqual(PREVIEW_BYTES);
    }
    expect(JSON.stringify(page).length).toBeLessThan(ELIDE_ABOVE_BYTES);
  });

  it('pages from the tail', async () => {
    const { get } = await boot();
    const page = await (await get(`/api/sessions/${SESSION}/lines?start=-2&limit=2`)).json();
    expect(page).toMatchObject({ start: 4, end: 6, total: 6 });
    expect(page.entries.map((e: { lineNo: number }) => e.lineNo)).toEqual([5]);
  });
});

describe('GET /api/sessions/:id/blocks/:line/:block', () => {
  it('returns the full block, equal to the original content', async () => {
    const { get } = await boot();
    const result = await (await get(`/api/sessions/${SESSION}/blocks/3/0`)).json();
    expect(safeParse(FullBlockSchema, result).success).toBe(true);
    expect(result).toEqual({ type: 'tool-result', id: 'big-call', content: BIG_OUTPUT });
    const call = await (await get(`/api/sessions/${SESSION}/blocks/2/0`)).json();
    expect(call).toEqual({
      type: 'tool-call',
      id: 'big-call',
      name: 'Write',
      kind: 'write',
      input: BIG_INPUT,
    });
  });

  it('returns a block that was never elided too', async () => {
    const { get } = await boot();
    const block = await (await get(`/api/sessions/${SESSION}/blocks/0/0`)).json();
    expect(block).toEqual({ type: 'text', text: 'first prompt' });
  });

  it('refuses unknown sessions, lines and blocks and malformed references', async () => {
    const { get } = await boot();
    const code = async (url: string) => {
      const res = await get(url);
      return [res.status, ((await res.json()) as { code: string }).code];
    };
    expect(await code('/api/sessions/absent/blocks/0/0')).toEqual([404, 'session-not-found']);
    expect(await code(`/api/sessions/${SESSION}/blocks/99/0`)).toEqual([404, 'not-found']);
    expect(await code(`/api/sessions/${SESSION}/blocks/0/5`)).toEqual([404, 'not-found']);
    expect(await code(`/api/sessions/${SESSION}/blocks/4/0`)).toEqual([404, 'not-found']);
    expect(await code(`/api/sessions/${SESSION}/blocks/-1/0`)).toEqual([400, 'invalid-request']);
    expect(await code(`/api/sessions/${SESSION}/blocks/x/0`)).toEqual([400, 'invalid-request']);
  });
});

describe('search excerpts', () => {
  it('returns short excerpts through the projection, never line bodies', async () => {
    const { get } = await boot();
    const one = safeParse(
      SearchResultSchema,
      await (await get(`/api/sessions/${SESSION}/search?q=needle`)).json(),
    );
    expect(one.success && one.output.hits).toEqual([
      { lineNo: 1, excerpt: 'answer with the needle inside' },
    ]);

    const big = safeParse(
      GlobalSearchResultSchema,
      await (await get('/api/search?q=tail-marker')).json(),
    );
    expect(big.success).toBe(true);
    if (!big.success) return;
    expect(big.output.results).toHaveLength(1);
    expect(big.output.results[0]).toMatchObject({ sessionId: SESSION, lineNo: 3 });
    expect(big.output.results[0]?.excerpt.length).toBeLessThanOrEqual(160);
    expect(big.output.results[0]?.excerpt).toContain('tail-marker');
  });
});

describe('stored bytes', () => {
  it('stay identical, with the same byte_size, after reads, block fetches and searches', async () => {
    const { dir, get } = await boot();
    const before = chunkFiles(dir);
    const meta = await (await get(`/api/sessions/${SESSION}`)).json();
    for (const url of [
      `/api/sessions/${SESSION}/lines?start=0&limit=100`,
      `/api/sessions/${SESSION}/lines?start=-3&limit=3`,
      `/api/sessions/${SESSION}/blocks/3/0`,
      `/api/sessions/${SESSION}/blocks/2/0`,
      `/api/sessions/${SESSION}/search?q=tail-marker`,
      '/api/search?q=tail-marker',
    ]) {
      expect((await get(url)).status).toBe(200);
    }
    expect(chunkFiles(dir)).toEqual(before);
    const after = await (await get(`/api/sessions/${SESSION}`)).json();
    expect(after.byteSize).toBe(meta.byteSize);
    const stored = Object.values(before).reduce(
      (n, b64) => n + Buffer.from(b64, 'base64').length,
      0,
    );
    expect(stored).toBe(meta.byteSize);
  });
});
