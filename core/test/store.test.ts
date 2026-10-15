import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { NotOwnerError, Store, summarizeTitle } from '../src/store/index.ts';

const dirs: string[] = [];
function newStore(): { store: Store; dir: string } {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-store-'));
  dirs.push(dir);
  return { store: new Store(dir), dir };
}

afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const line = (i: number): string =>
  JSON.stringify({
    type: 'user',
    cwd: '/tmp/proj',
    message: { role: 'user', content: `line ${i}` },
  });

describe('Store.append', () => {
  it('advances the byte cursor by exactly the bytes appended', () => {
    const { store } = newStore();
    const first = store.append(
      { sessionId: 's1', projectDir: 'proj', filePath: '/x/s1.jsonl', deviceId: 'd1' },
      [line(0), line(1)],
    );
    expect(first.byteSize).toBe(`${line(0)}\n${line(1)}\n`.length);
    expect(first.lineCount).toBe(2);
    expect(first.isNew).toBe(true);

    // An empty batch does not move the cursor and reports the current state
    const empty = store.append(
      { sessionId: 's1', projectDir: 'proj', filePath: '/x/s1.jsonl', deviceId: 'd1' },
      [],
    );
    expect(empty).toEqual({
      byteSize: first.byteSize,
      lineCount: 2,
      appendedLines: 0,
      isNew: false,
    });
  });

  it('rolls chunks at 500 lines and compresses the full ones', async () => {
    const { store, dir } = newStore();
    const lines = Array.from({ length: 1200 }, (_, i) => line(i));
    const r = store.append(
      { sessionId: 's2', projectDir: 'p', filePath: '/x', deviceId: 'd' },
      lines,
    );
    expect(r.lineCount).toBe(1200);
    // Reads see the full chunks whether or not compression has run yet.
    expect((await store.readLines('s2', 498, 4))?.lines).toEqual(
      [498, 499, 500, 501].map((i) => JSON.parse(line(i))),
    );
    await store.idle();
    // 1200 lines → chunks 0 and 1 full (gzipped), chunk 2 open with 200 lines
    const sessionDir = path.join(dir, 'sessions', 's2');
    expect(existsSync(path.join(sessionDir, 'c-0.ndjson.gz'))).toBe(true);
    expect(existsSync(path.join(sessionDir, 'c-0.ndjson'))).toBe(false);
    expect(existsSync(path.join(sessionDir, 'c-2.ndjson'))).toBe(true);
    const meta = store.getSession('s2');
    expect(meta?.lineCount).toBe(1200);
    expect(r.byteSize).toBe(lines.map((l) => `${l}\n`).join('').length);
  });

  it('rejects an append from a device that does not own the session', () => {
    const { store } = newStore();
    store.append({ sessionId: 's3', projectDir: 'p', filePath: '/x', deviceId: 'd1' }, [line(0)]);
    expect(() =>
      store.append({ sessionId: 's3', projectDir: 'p', filePath: '/x', deviceId: 'd2' }, [line(1)]),
    ).toThrow(NotOwnerError);
    expect(store.ownerDeviceId('s3')).toBe('d1');
    expect(store.ownerDeviceId('missing')).toBeNull();
  });

  it('rejects metadata-only requests from a non-owner before changing the title', () => {
    const { store } = newStore();
    const input = { sessionId: 's-empty-owner', projectDir: 'p', filePath: '/x', deviceId: 'd1' };
    store.append(input, [line(0)]);
    const before = store.getSession(input.sessionId);
    expect(() => store.append({ ...input, deviceId: 'd2', title: 'Foreign title' }, [])).toThrow(
      NotOwnerError,
    );
    expect(store.getSession(input.sessionId)).toEqual(before);
  });

  it('moves ownership so the previous device cannot append', () => {
    const { store } = newStore();
    store.append({ sessionId: 's-ho', projectDir: 'p', filePath: '/x', deviceId: 'd1' }, [line(0)]);
    expect(
      store.handoffOwner(
        's-ho',
        'd1',
        'd2',
        store.getSession('s-ho')?.byteSize ?? -1,
        0,
        '/w',
        'hoplet/w',
      ),
    ).toBe(1);
    expect(store.ownerDeviceId('s-ho')).toBe('d2');
    expect(() =>
      store.append({ sessionId: 's-ho', projectDir: 'p', filePath: '/x', deviceId: 'd1' }, [
        line(1),
      ]),
    ).toThrow(NotOwnerError);
  });

  it('extracts project path and title from the lines', () => {
    const { store } = newStore();
    store.append({ sessionId: 's4', projectDir: 'p', filePath: '/x', deviceId: 'd' }, [
      JSON.stringify({
        type: 'user',
        cwd: '/Users/mark/proj',
        message: { role: 'user', content: 'Fix the cursor bug' },
      }),
    ]);
    const meta = store.getSession('s4');
    expect(meta?.projectPath).toBe('/Users/mark/proj');
    expect(meta?.title).toBe('Fix the cursor bug');
  });

  it('prefers vendor ai-title over a prompt heuristic and can upgrade later', () => {
    const { store } = newStore();
    store.append({ sessionId: 's4b', projectDir: 'p', filePath: '/x', deviceId: 'd' }, [
      JSON.stringify({
        type: 'user',
        cwd: '/Users/mark/proj',
        message: { role: 'user', content: 'Fix the cursor bug please now' },
      }),
    ]);
    expect(store.getSession('s4b')?.title).toBe('Fix the cursor bug please now');
    store.append({ sessionId: 's4b', projectDir: 'p', filePath: '/x', deviceId: 'd' }, [
      JSON.stringify({ type: 'ai-title', aiTitle: 'cursor bug fix' }),
    ]);
    expect(store.getSession('s4b')?.title).toBe('cursor bug fix');
  });

  it('applies agent PeekTitle on append and on a title-only empty batch', () => {
    const { store } = newStore();
    store.append(
      {
        sessionId: 's4c',
        projectDir: 'p',
        filePath: '/x',
        deviceId: 'd',
        title: '你好，测试 hoplet',
      },
      [
        JSON.stringify({
          type: 'user',
          cwd: '/Users/mark/proj',
          message: { role: 'user', content: '你好，测试 hoplet' },
        }),
      ],
    );
    expect(store.getSession('s4c')?.title).toBe('你好，测试 hoplet');
    // Cursor caught up; agent peeks a later vendor ai-title without new bytes.
    store.append(
      { sessionId: 's4c', projectDir: 'p', filePath: '/x', deviceId: 'd', title: 'hoplet 测试' },
      [],
    );
    expect(store.getSession('s4c')?.title).toBe('hoplet 测试');
    expect(store.getSession('s4c')?.byteSize).toBeGreaterThan(0);
  });

  it('joins the device name onto session meta', () => {
    const { store } = newStore();
    store.upsertDevice('d-mac', 'MacBook Pro', '0.2.0');
    store.append({ sessionId: 's-dev', projectDir: 'p', filePath: '/x', deviceId: 'd-mac' }, [
      line(0),
    ]);
    expect(store.getSession('s-dev')?.deviceName).toBe('MacBook Pro');
    expect(store.listSessions()[0]?.deviceName).toBe('MacBook Pro');
  });

  it('hashes a minted device token and looks it up', () => {
    const { store } = newStore();
    const minted = store.mintDevice('Mac', 'd-mac');
    expect(minted.deviceId).toBe('d-mac');
    expect(store.deviceIdForToken(minted.token)).toBe('d-mac');
    expect(store.deviceIdForToken('wrong')).toBeNull();
    const rotated = store.mintDevice('Mac', 'd-mac');
    expect(store.deviceIdForToken(minted.token)).toBeNull();
    expect(store.deviceIdForToken(rotated.token)).toBe('d-mac');
  });
});

describe('Store.readLines', () => {
  it('reads pages and the tail without loading the whole session', async () => {
    const { store } = newStore();
    const lines = Array.from({ length: 1100 }, (_, i) => line(i));
    store.append({ sessionId: 's5', projectDir: 'p', filePath: '/x', deviceId: 'd' }, lines);

    const tail = await store.readLines('s5', -80, 80);
    expect(tail?.start).toBe(1020);
    expect(tail?.total).toBe(1100);
    expect((tail?.lines[0] as { message?: { content?: string } })?.message?.content).toBe(
      'line 1020',
    );

    const head = await store.readLines('s5', 0, 3);
    expect(head?.end).toBe(3);

    // A start past the end clamps to the end and returns no lines
    const past = await store.readLines('s5', 5000, 10);
    expect(past?.start).toBe(1100);
    expect(past?.lines).toHaveLength(0);

    expect(await store.readLines('missing', 0, 10)).toBe(null);
  });
});

describe('migrations', () => {
  it('reopens a data directory without re-running or failing', () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-store-'));
    dirs.push(dir);
    const first = new Store(dir);
    first.append({ sessionId: 's6', projectDir: 'p', filePath: '/x', deviceId: 'd' }, [line(0)]);
    const second = new Store(dir);
    expect(second.sessionCount()).toBe(1);
  });
});

describe('Store.search', () => {
  it('finds lines across chunks, case-insensitively, and pages hits', async () => {
    const { store } = newStore();
    const lines = Array.from({ length: 1200 }, (_, i) =>
      i === 600
        ? JSON.stringify({
            type: 'user',
            message: { role: 'user', content: 'the Needle is hidden here' },
          })
        : line(i),
    );
    store.append({ sessionId: 's7', projectDir: 'p', filePath: '/x', deviceId: 'd' }, lines);

    const first = await store.search('s7', 'needle', 0, 50);
    expect(first?.total).toBe(1);
    expect(first?.hits[0]?.lineNo).toBe(600);
    expect(
      (first?.hits[0]?.line as { message?: { content?: string } })?.message?.content,
    ).toContain('Needle');

    const none = await store.search('s7', 'absent-phrase', 0, 50);
    expect(none?.total).toBe(0);
    expect(none?.hits).toHaveLength(0);

    expect(await store.search('missing', 'x', 0, 10)).toBe(null);
  });
});

describe('summarizeTitle', () => {
  it('rejects commands and paste tags', () => {
    expect(summarizeTitle('/compact quiet')).toBe(null);
    expect(summarizeTitle('<pasted content 5 lines>')).toBe(null);
    expect(summarizeTitle('   ')).toBe(null);
  });

  it('keeps a short prompt whole and cuts a long one at a sentence', () => {
    expect(summarizeTitle('Fix the cursor bug')).toBe('Fix the cursor bug');
    const long = `${'A'.repeat(80)}. ${'B'.repeat(80)}`;
    expect(summarizeTitle(long)).toBe(`${'A'.repeat(80)}.`);
    expect(summarizeTitle('a '.repeat(80))?.length).toBeLessThanOrEqual(97);
  });
});

describe('Store.searchAll', () => {
  it('scans sessions newest-first, pages across them and reports exhaustion', async () => {
    const { store } = newStore();
    store.append({ sessionId: 'older', projectDir: 'p', filePath: '/x', deviceId: 'd' }, [
      JSON.stringify({
        type: 'user',
        message: { role: 'user', content: 'the quilt exists here too' },
      }),
      line(0),
    ]);
    await new Promise((r) => setTimeout(r, 5));
    store.append({ sessionId: 'newer', projectDir: 'p', filePath: '/x', deviceId: 'd' }, [
      JSON.stringify({ type: 'user', message: { role: 'user', content: 'The Quilt is here' } }),
      line(1),
    ]);

    const page1 = await store.searchAll('quilt', 0, 1);
    expect(page1.results).toHaveLength(1);
    expect(page1.results[0]?.sessionId).toBe('newer');
    expect(page1.results[0]?.lineNo).toBe(0);
    expect(page1.exhausted).toBe(false);

    const page2 = await store.searchAll('quilt', 1, 10);
    expect(page2.results.map((r) => r.sessionId)).toEqual(['older']);
    expect(page2.exhausted).toBe(true);

    const none = await store.searchAll('absent-everywhere', 0, 10);
    expect(none.results).toHaveLength(0);
    expect(none.exhausted).toBe(true);
  });
});

it('fingerprints exact raw bytes across compressed and open chunks', async () => {
  const { store } = newStore();
  const lines = Array.from({ length: 501 }, (_, i) => `  { "n": ${i}, "text": "字" }  `);
  const bytes = Buffer.from(`${lines.join('\n')}\n`);
  store.append(
    { sessionId: 'proof', projectDir: 'p', filePath: '/synthetic', deviceId: 'd' },
    lines,
  );
  expect(await store.streamFingerprint('proof')).toEqual({
    byteSize: bytes.length,
    streamSha256: createHash('sha256').update(bytes).digest('hex'),
  });
  expect(await store.streamFingerprint('absent')).toBeNull();
});

describe('Store.readStream', () => {
  it('yields exactly the committed bytes in order across compressed and open chunks', async () => {
    const { store } = newStore();
    const lines = Array.from(
      { length: 1203 },
      (_, i) => `{"n":${i},"text":"${'x'.repeat(i % 50)}字"}`,
    );
    store.append({ sessionId: 'rs', projectDir: 'p', filePath: '/x', deviceId: 'd' }, lines);
    await store.idle();
    const expected = Buffer.from(`${lines.join('\n')}\n`);
    const pieces: Buffer[] = [];
    for await (const piece of store.readStream('rs', expected.length, 4096)) {
      expect(piece.length).toBeLessThanOrEqual(4096);
      pieces.push(piece);
    }
    expect(Buffer.concat(pieces).equals(expected)).toBe(true);
  });

  it('refuses a size that is not the cursor', async () => {
    const { store } = newStore();
    store.append({ sessionId: 'rs', projectDir: 'p', filePath: '/x', deviceId: 'd' }, ['{"a":1}']);
    const size = store.getSession('rs')?.byteSize ?? 0;
    const drain = async (id: string, n: number) => {
      for await (const _ of store.readStream(id, n, 1024)) {
        // read to the end
      }
    };
    await expect(drain('rs', size + 1)).rejects.toThrow('stream-changed');
    await expect(drain('rs', size - 1)).rejects.toThrow('stream-changed');
    await expect(drain('absent', 1)).rejects.toThrow('stream-changed');
    await expect(drain('rs', size)).resolves.toBeUndefined();
  });
});

describe('the git branch of a session', () => {
  const branched = (i: number, gitBranch?: string) =>
    JSON.stringify({
      type: 'user',
      cwd: '/tmp/proj',
      gitBranch,
      message: { role: 'user', content: `${i}` },
    });

  it('is the last branch the lines record, kept across appends and restarts', () => {
    const { store, dir } = newStore();
    const at = { sessionId: 'gb', projectDir: 'p', filePath: '/x', deviceId: 'd' };
    store.append(at, [line(0)]);
    expect(store.getSession('gb')?.gitBranch).toBeUndefined();
    store.append(at, [branched(1, 'main'), branched(2, 'feat/x')]);
    expect(store.getSession('gb')?.gitBranch).toBe('feat/x');
    // A line without a branch keeps the last one.
    store.append(at, [line(3)]);
    expect(new Store(dir).getSession('gb')?.gitBranch).toBe('feat/x');
  });
});

describe('Store.handoffOwner', () => {
  const seed = (store: Store) =>
    store.append({ sessionId: 'ho', projectDir: 'p', filePath: '/x', deviceId: 'a' }, [line(0)]);

  it('moves the owner and increases the epoch only when owner, cursor and epoch are as expected', () => {
    const { store } = newStore();
    seed(store);
    const cursor = store.getSession('ho')?.byteSize ?? 0;
    expect(store.handoffOwner('ho', 'b', 'c', cursor, 0, '/w', 'hoplet/w')).toBeNull(); // not the owner
    expect(store.handoffOwner('ho', 'a', 'b', cursor + 1, 0, '/w', 'hoplet/w')).toBeNull(); // the cursor moved
    expect(store.handoffOwner('ho', 'a', 'b', cursor, 5, '/w', 'hoplet/w')).toBeNull(); // another epoch
    expect(store.handoffOwner('absent', 'a', 'b', 0, 0, '/w', 'hoplet/w')).toBeNull();
    expect(store.handoffOwner('../ho', 'a', 'b', cursor, 0, '/w', 'hoplet/w')).toBeNull();
    expect(store.getSession('ho')).toMatchObject({ ownerDeviceId: 'a', ownerEpoch: 0 });

    expect(store.handoffOwner('ho', 'a', 'b', cursor, 0, '/w', 'hoplet/w')).toBe(1);
    expect(store.getSession('ho')).toMatchObject({
      ownerDeviceId: 'b',
      ownerEpoch: 1,
      byteSize: cursor,
    });
    expect(store.getSession('ho')?.projectPath).toBe('/w');
    expect(store.getSession('ho')?.gitBranch).toBe('hoplet/w');
    // The same request cannot be applied twice.
    expect(store.handoffOwner('ho', 'a', 'b', cursor, 0, '/w', 'hoplet/w')).toBeNull();
    expect(store.handoffOwner('ho', 'b', 'a', cursor, 1, '/w', 'hoplet/w')).toBe(2);
    // Lines appended afterwards carry the old cwd and do not move the project back.
    store.append({ sessionId: 'ho', projectDir: 'p', filePath: '/x', deviceId: 'a' }, [line(1)]);
    expect(store.getSession('ho')?.projectPath).toBe('/w');
  });

  it('survives a restart', () => {
    const { store, dir } = newStore();
    seed(store);
    store.handoffOwner('ho', 'a', 'b', store.getSession('ho')?.byteSize ?? 0, 0, '/w', 'hoplet/w');
    expect(new Store(dir).getSession('ho')).toMatchObject({ ownerDeviceId: 'b', ownerEpoch: 1 });
  });
});
