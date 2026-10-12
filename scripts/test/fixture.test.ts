import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterAll, beforeAll, expect, test } from 'vitest';
import { generateFixture, MiB, NEEDLE } from '../bench/fixture.ts';

const SID = '11111111-2222-4333-8444-555555555555';
const CWD = '/work/project';
const TARGET = 23 * MiB;

let dir: string;
let info: ReturnType<typeof generateFixture>;
let file: string;

beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), 'hoplet-fixture-test-'));
  file = join(dir, 'a.jsonl');
  info = generateFixture({ path: file, bytes: TARGET, sessionId: SID, cwd: CWD });
});
afterAll(() => rmSync(dir, { recursive: true, force: true }));

const sha = (path: string) => createHash('sha256').update(readFileSync(path)).digest('hex');

test('the same seed gives the same bytes, another seed does not', () => {
  const again = join(dir, 'b.jsonl');
  generateFixture({ path: again, bytes: TARGET, sessionId: SID, cwd: CWD });
  expect(sha(again)).toBe(sha(file));
  const other = join(dir, 'c.jsonl');
  generateFixture({ path: other, bytes: TARGET, sessionId: SID, cwd: CWD, seed: 2 });
  expect(sha(other)).not.toBe(sha(file));
});

test('the size is within 2% of the target and matches the report', () => {
  const size = statSync(file).size;
  expect(Math.abs(size - TARGET) / TARGET).toBeLessThan(0.02);
  expect(info.bytes).toBe(size);
});

test('every line is valid JSON and the uuid chain links', () => {
  const raw = readFileSync(file, 'utf8');
  expect(raw.endsWith('\n')).toBe(true);
  const lines = raw.slice(0, -1).split('\n');
  expect(lines.length).toBe(info.lines);
  let parent: string | null = null;
  const seen = new Set<string>();
  let last = 0;
  for (const text of lines) {
    const line = JSON.parse(text);
    expect(line.parentUuid).toBe(parent);
    expect(line.sessionId).toBe(SID);
    expect(line.cwd).toBe(CWD);
    expect(['user', 'assistant']).toContain(line.type);
    expect(seen.has(line.uuid)).toBe(false);
    seen.add(line.uuid);
    const t = Date.parse(line.timestamp);
    expect(t).toBeGreaterThan(last);
    last = t;
    parent = line.uuid;
  }
  expect(lines.at(-1)).toContain(NEEDLE);
});

test('the byte distribution matches real sessions', () => {
  const raw = readFileSync(file, 'utf8').split('\n').slice(0, -1);
  const total = raw.reduce((n, l) => n + Buffer.byteLength(l) + 1, 0);
  const bytes = { toolResult: 0, text: 0, thinking: 0, images: 0 };
  let longLines = 0;
  for (const text of raw) {
    if (Buffer.byteLength(text) + 1 > MiB) longLines++;
    const content = JSON.parse(text).message.content;
    if (!Array.isArray(content)) continue;
    for (const block of content as { type: string }[]) {
      const size = Buffer.byteLength(JSON.stringify(block));
      if (block.type === 'tool_result') {
        bytes.toolResult += size;
        if (JSON.stringify(block).includes('"type":"image"')) bytes.images += size;
      } else if (block.type === 'text') bytes.text += size;
      else if (block.type === 'thinking') bytes.thinking += size;
      else if (block.type === 'image') bytes.images += size;
    }
  }
  expect(bytes.toolResult / total).toBeGreaterThan(0.83);
  expect(bytes.toolResult / total).toBeLessThan(0.89);
  expect(bytes.text / total).toBeLessThan(0.02);
  expect(bytes.text / total).toBeGreaterThan(0.003);
  expect(bytes.thinking / total).toBeGreaterThan(0.005);
  expect(bytes.images / total).toBeGreaterThan(0.01);
  expect(longLines).toBeGreaterThan(0);
  expect(longLines).toBe(info.longLines);
  const max = Math.max(...raw.map((l) => Buffer.byteLength(l)));
  expect(max).toBeLessThan(1.6 * MiB);
});
