import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type AgentRuntimeState,
  type Annotation,
  MAX_ANNOTATIONS,
  PROTOCOL_VERSION,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { composeReview, locationOf } from '../src/api/review.ts';
import { createControl, type DeviceLink } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const line = JSON.stringify({
  type: 'user',
  cwd: '/tmp/p',
  message: { role: 'user', content: 'x' },
});
const meta = {
  sessionId: 's1',
  provider: 'claude' as const,
  projectDir: 'p',
  filePath: '/x',
  deviceId: 'dev-a',
};
const tick = (ms = 15) => new Promise((r) => setTimeout(r, ms));

type Sent = { type: string; [k: string]: unknown };

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-annotations-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append(meta, [line]);
  const control = createControl(store, { promptTimeoutMs: 300 });
  const sent: Sent[] = [];
  const link: DeviceLink = {
    send: (m) => void sent.push(m as unknown as Sent),
    sendAndWait: async (m) => void sent.push(m as unknown as Sent),
    isOpen: () => true,
  };
  control.attach('dev-a', link);
  const state = (s: AgentRuntimeState) =>
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.state',
      sessionId: 's1',
      state: s,
      capabilities: { permissions: true, interrupt: true, steer: true, config: [] },
    });
  const server = createServer();
  createApi(store, { port: 0, dataDir: dir, token: 'tok', webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  const call = async (method: string, url: string, body?: unknown) => {
    const res = await fetch(base + url, {
      method,
      headers: { authorization: 'Bearer tok', 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    // biome-ignore lint/suspicious/noExplicitAny: a test reads fields of the JSON answer freely
    return { status: res.status, body: (await res.json()) as Record<string, any> };
  };
  return { store, dir, control, link, sent, state, call };
}

const comment = (id: string, over: Partial<Annotation> = {}) => ({
  id,
  path: 'core/src/store/chunks.ts',
  side: 'new',
  startLine: 42,
  endLine: 44,
  snippet: 'const start = offset;\nconst end = start + line.length;\noffset = end;',
  body: 'Count bytes, not characters.',
  ...over,
});

const URL = '/api/sessions/s1/annotations';

describe('composeReview', () => {
  const base: Annotation = {
    ...(comment('a') as Omit<Annotation, 'state' | 'createdAt' | 'updatedAt'>),
    state: 'pending',
    createdAt: 1,
    updatedAt: 1,
  } as Annotation;

  it('numbers the comments under a heading, the note first, each snippet fenced in its language', () => {
    const text = composeReview(
      [
        base,
        { ...base, path: 'go/main.go', startLine: 7, endLine: 7, snippet: 'x := 1', body: 'Why?' },
      ],
      'Please keep the API.',
    );
    expect(text).toBe(
      [
        'Review comments (2) on the working tree.',
        'Please keep the API.',
        `1. core/src/store/chunks.ts:42-44\n\`\`\`ts\n${base.snippet}\n\`\`\`\nCount bytes, not characters.`,
        '2. go/main.go:7\n```go\nx := 1\n```\nWhy?',
      ].join('\n\n'),
    );
  });

  it('leaves the note out when there is none and fences a snippet holding backticks with more', () => {
    const text = composeReview([{ ...base, snippet: 'a ``` b', path: 'README' }], undefined);
    expect(text).toBe(
      'Review comments (1) on the working tree.\n\n1. README:42-44\n````\na ``` b\n````\nCount bytes, not characters.',
    );
  });

  it('names the removed side', () => {
    expect(locationOf({ path: 'a.ts', side: 'old', startLine: 3, endLine: 3 })).toBe(
      'a.ts:3 (removed lines, as HEAD has them)',
    );
  });
});

describe('annotation routes', () => {
  it('adds, lists, edits and deletes comments; a repeated id keeps the first', async () => {
    const t = await boot();
    let r = await t.call('POST', URL, comment('a'));
    expect(r.status).toBe(201);
    expect(r.body.annotations).toMatchObject([{ id: 'a', state: 'pending', startLine: 42 }]);
    r = await t.call('POST', URL, comment('a', { body: 'other' }));
    expect(r.body.annotations).toHaveLength(1);
    expect(r.body.annotations[0].body).toBe('Count bytes, not characters.');
    r = await t.call('POST', URL, comment('b', { startLine: 47, endLine: 47 }));
    r = await t.call('PATCH', `${URL}/a`, { body: 'Edited.' });
    expect(r.body.annotations.map((a: Annotation) => [a.id, a.body])).toEqual([
      ['a', 'Edited.'],
      ['b', 'Count bytes, not characters.'],
    ]);
    r = await t.call('DELETE', `${URL}/b`);
    expect(r.body.annotations.map((a: Annotation) => a.id)).toEqual(['a']);
    expect((await t.call('DELETE', `${URL}/b`)).body.code).toBe('not-found');
    expect((await t.call('GET', URL)).body.annotations).toHaveLength(1);
    expect((await t.call('GET', '/api/sessions/nope/annotations')).body.code).toBe(
      'session-not-found',
    );
  });

  it('refuses a range that ends before it starts and more than the limit', async () => {
    const t = await boot();
    const bad = await t.call('POST', URL, comment('a', { startLine: 5, endLine: 4 }));
    expect(bad.status).toBe(400);
    for (let i = 0; i < MAX_ANNOTATIONS; i++) {
      t.store.insertAnnotation('s1', { ...comment(`c${i}`), side: 'new' }, 1);
    }
    const full = await t.call('POST', URL, comment('z'));
    expect(full).toMatchObject({ status: 413, body: { code: 'too-large' } });
  });

  it('previews exactly the message it then sends as a prompt, and marks the comments sent', async () => {
    const t = await boot();
    t.state('idle');
    await t.call('POST', URL, comment('a'));
    await t.call(
      'POST',
      URL,
      comment('b', { path: 'core/test/chunks.test.ts', startLine: 58, endLine: 58 }),
    );
    const request = {
      ids: ['b', 'a'],
      note: 'Look at these.',
      delivery: 'prompt',
      requestId: 'r1',
    };
    const preview = await t.call('POST', `${URL}/send`, { ...request, dryRun: true });
    expect(preview.status).toBe(200);
    expect(preview.body).toMatchObject({ sent: false, review: 1, limit: 8000 });
    expect(preview.body.text.indexOf('chunks.ts:42-44')).toBeLessThan(
      preview.body.text.indexOf('chunks.test.ts:58'),
    );
    expect(preview.body.chars).toBe(preview.body.text.length);
    expect(t.store.annotations('s1').every((a) => a.state === 'pending')).toBe(true);

    const sending = t.call('POST', `${URL}/send`, request);
    await tick();
    const prompt = t.sent.find((m) => m.type === 'runtime.prompt');
    expect(prompt).toMatchObject({ promptId: 'r1', text: preview.body.text });
    t.state('running');
    const done = await sending;
    expect(done).toMatchObject({ status: 200, body: { sent: true, review: 1 } });
    expect(done.body.annotations).toMatchObject([
      { id: 'a', state: 'sent', sent: { review: 1, delivery: 'prompt', messageId: 'r1' } },
      { id: 'b', state: 'sent', sent: { review: 1 } },
    ]);
    // A repeated request returns the review it sent and sends nothing more.
    const again = await t.call('POST', `${URL}/send`, request);
    expect(again.body).toMatchObject({ sent: true, review: 1, text: preview.body.text });
    expect(t.sent.filter((m) => m.type === 'runtime.prompt')).toHaveLength(1);
    // A sent comment can no longer be edited or sent; it resolves and reopens.
    expect((await t.call('PATCH', `${URL}/a`, { body: 'x' })).body.code).toBe('stale');
    expect((await t.call('POST', `${URL}/send`, { ...request, requestId: 'r2' })).body.code).toBe(
      'stale',
    );
    let r = await t.call('PATCH', `${URL}/a`, { state: 'resolved' });
    expect(r.body.annotations[0].state).toBe('resolved');
    expect((await t.call('PATCH', `${URL}/a`, { state: 'resolved' })).body.code).toBe('stale');
    r = await t.call('PATCH', `${URL}/a`, { state: 'sent' });
    expect(r.body.annotations[0]).toMatchObject({ state: 'sent', sent: { review: 1 } });
    // The next review gets the next number.
    await t.call('POST', URL, comment('c'));
    const next = await t.call('POST', `${URL}/send`, {
      ids: ['c'],
      delivery: 'queue',
      requestId: 'r3',
      dryRun: true,
    });
    expect(next.body.review).toBe(2);
  });

  it('queues or steers while a turn runs, and keeps the comments pending when delivery fails', async () => {
    const t = await boot();
    t.state('running');
    await t.call('POST', URL, comment('a'));
    await t.call('POST', URL, comment('b'));
    const busy = await t.call('POST', `${URL}/send`, {
      ids: ['a'],
      delivery: 'prompt',
      requestId: 'p1',
    });
    expect(busy.status).toBe(409);
    expect(t.store.annotation('s1', 'a')?.state).toBe('pending');

    const queued = await t.call('POST', `${URL}/send`, {
      ids: ['a'],
      delivery: 'queue',
      requestId: 'q1',
    });
    expect(queued.body.annotations[0]).toMatchObject({
      state: 'sent',
      sent: { delivery: 'queue' },
    });
    expect(t.control.queueOf('s1').items).toMatchObject([{ id: 'q1' }]);

    const steering = t.call('POST', `${URL}/send`, {
      ids: ['b'],
      delivery: 'steer',
      requestId: 's1x',
    });
    await tick();
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.steered',
      sessionId: 's1',
      steerId: 's1x',
    });
    const steered = await steering;
    expect(steered.body.annotations[1]).toMatchObject({
      state: 'sent',
      sent: { delivery: 'steer', review: 2 },
    });
  });

  it('refuses to send a message over 8,000 characters but previews it', async () => {
    const t = await boot();
    t.state('idle');
    await t.call('POST', URL, comment('a', { body: 'x'.repeat(4000), snippet: 'y'.repeat(4000) }));
    await t.call('POST', URL, comment('b', { body: 'x'.repeat(100) }));
    const request = { ids: ['a', 'b'], delivery: 'prompt', requestId: 'r1' };
    const preview = await t.call('POST', `${URL}/send`, { ...request, dryRun: true });
    expect(preview.body.chars).toBeGreaterThan(8000);
    const refused = await t.call('POST', `${URL}/send`, request);
    expect(refused).toMatchObject({ status: 413, body: { code: 'too-large' } });
    expect(t.store.annotations('s1').every((a) => a.state === 'pending')).toBe(true);
  });

  it('goes with the session when it is purged', async () => {
    const t = await boot();
    await t.call('POST', URL, comment('a'));
    t.store.purgeSession('s1');
    expect(t.store.annotations('s1')).toEqual([]);
  });
});
