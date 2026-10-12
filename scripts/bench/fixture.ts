// Deterministic Claude Code session fixtures with the byte distribution of real sessions
// (docs/spec.md#technical-baseline): about 86% of the bytes in tool_result content, single lines above
// 1 MiB, base64 images, small assistant text, thinking blocks, a uuid/parentUuid chain.
// Everything is synthetic; no real session data is used.
import { closeSync, openSync, writeSync } from 'node:fs';

export const MiB = 1024 * 1024;
export const NEEDLE = 'hoplet-bench-needle-7f3a';
export const CLAUDE_VERSION = '2.1.289';

export type FixtureOptions = {
  path: string;
  /** Target file size in bytes. */
  bytes: number;
  seed?: number;
  sessionId: string;
  cwd: string;
  /** Upper bound for the size of a single line; default none (lines reach about 1.5 MB). */
  maxLineBytes?: number | undefined;
};

export type FixtureInfo = { sessionId: string; bytes: number; lines: number; longLines: number };

type Rng = () => number;

// mulberry32: small, fast, and identical on every platform.
function mulberry32(seed: number): Rng {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const WORDS = (
  'async await const return import export function class interface type string number boolean null undefined ' +
  'error warn info debug request response session offset cursor chunk batch gzip append reconcile socket ' +
  'handler device project server agent token store schema index test expect describe vitest biome tsc ' +
  'PASS FAIL ok skipped duration ms bytes lines diff patch commit branch merge rebase checkout status ' +
  'src/core/ingest/conn.ts src/store/index.ts agent/internal/sync/client.go web/src/lib/api.ts ' +
  'node_modules/.pnpm/ws@8.18.0 /usr/local/lib /private/tmp/build dist/assets/index.js'
).split(' ');

const B64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
const hex = (rng: Rng, n: number): string => {
  let s = '';
  for (let i = 0; i < n; i++) s += '0123456789abcdef'[Math.floor(rng() * 16)];
  return s;
};
const uuid = (rng: Rng): string =>
  `${hex(rng, 8)}-${hex(rng, 4)}-4${hex(rng, 3)}-${'89ab'[Math.floor(rng() * 4)]}${hex(rng, 3)}-${hex(rng, 12)}`;

function textPool(rng: Rng, size: number): string {
  const parts: string[] = [];
  let n = 0;
  while (n < size) {
    const lineWords = 3 + Math.floor(rng() * 14);
    const words: string[] = [];
    for (let i = 0; i < lineWords; i++) words.push(WORDS[Math.floor(rng() * WORDS.length)] ?? '');
    const line = `${Math.floor(rng() * 9999)}:${words.join(' ')}\n`;
    parts.push(line);
    n += line.length;
  }
  return parts.join('');
}

function b64Pool(rng: Rng, size: number): string {
  const chars: string[] = new Array(size);
  for (let i = 0; i < size; i++) chars[i] = B64[Math.floor(rng() * 64)] ?? 'A';
  return chars.join('');
}

const PROMPTS = [
  'Run the tests and fix whatever fails.',
  'Why is the sync slow on the large session? Look at the reconcile loop.',
  'Show me how the cursor is advanced after a batch is acknowledged.',
  'Add a regression test for the partial tail case.',
  'Summarize the diff and check it against the spec.',
  'Open the page and tell me what the layout looks like.',
];
const TOOLS = ['Bash', 'Read', 'Grep', 'Edit', 'Glob'];

const RESULT_SHARE = 0.87;

type ResultSize = { chars: number; image: boolean };

function pickResult(rng: Rng, remaining: number, maxChars: number): ResultSize {
  const r = rng();
  let chars: number;
  let image = false;
  if (r < 0.01)
    chars = Math.floor(1.05 * MiB + rng() * 0.45 * MiB); // single line above 1 MiB
  else if (r < 0.05) chars = Math.floor(60_000 + rng() * 340_000);
  else if (r < 0.3) chars = Math.floor(8_000 + rng() * 52_000);
  else chars = Math.floor(500 + rng() * 7_500);
  if (rng() < 0.03) {
    image = true;
    chars = Math.floor(80_000 + rng() * 800_000);
  }
  return { chars: Math.max(Math.min(chars, remaining - 4_000, maxChars), 200), image };
}

export function generateFixture(opts: FixtureOptions): FixtureInfo {
  const rng = mulberry32(opts.seed ?? 1);
  const text = textPool(rng, 6 * MiB);
  const img = b64Pool(rng, 2 * MiB);
  const { sessionId, cwd } = opts;
  const fd = openSync(opts.path, 'w');
  let bytes = 0;
  let lines = 0;
  let longLines = 0;
  let parent: string | null = null;
  let ts = Date.UTC(2026, 8, 1, 8, 0, 0);
  let resultBytes = 0;

  const slice = (pool: string, n: number): string => {
    const at = Math.floor(rng() * (pool.length - n));
    return pool.slice(at, at + n);
  };

  const emit = (type: 'user' | 'assistant', message: object, extra: object = {}): void => {
    ts += 200 + Math.floor(rng() * 20_000);
    const id = uuid(rng);
    const line = `${JSON.stringify({
      parentUuid: parent,
      isSidechain: false,
      userType: 'external',
      cwd,
      sessionId,
      version: CLAUDE_VERSION,
      gitBranch: 'main',
      type,
      message,
      ...extra,
      uuid: id,
      timestamp: new Date(ts).toISOString(),
    })}\n`;
    const n = Buffer.byteLength(line);
    writeSync(fd, line);
    bytes += n;
    lines++;
    if (n > MiB) longLines++;
    parent = id;
  };

  const assistant = (content: object[], stop: string): void => {
    emit(
      'assistant',
      {
        id: `msg_${hex(rng, 24)}`,
        type: 'message',
        role: 'assistant',
        model: 'claude-sonnet-4-5',
        content,
        stop_reason: stop,
        stop_sequence: null,
        usage: {
          input_tokens: 3 + Math.floor(rng() * 50),
          cache_creation_input_tokens: Math.floor(rng() * 4000),
          cache_read_input_tokens: Math.floor(rng() * 90_000),
          output_tokens: 20 + Math.floor(rng() * 600),
          service_tier: 'standard',
        },
      },
      { requestId: `req_${hex(rng, 24)}` },
    );
  };

  try {
    let first = true;
    while (opts.bytes - bytes > 3_000) {
      const prompt = first
        ? 'Read the project and help me find why large sessions open slowly.'
        : (PROMPTS[Math.floor(rng() * PROMPTS.length)] ?? '');
      emit('user', { role: 'user', content: prompt });
      // Sometimes the user pastes a screenshot.
      if (!first && rng() < 0.02) {
        const n = Math.min(
          Math.floor(60_000 + rng() * 300_000),
          Math.max(opts.bytes - bytes - 4_000, 0),
        );
        if (n > 1_000) {
          emit('user', {
            role: 'user',
            content: [
              {
                type: 'image',
                source: { type: 'base64', media_type: 'image/png', data: slice(img, n) },
              },
            ],
          });
        }
      }
      first = false;
      const cycles = 1 + Math.floor(rng() * 8);
      for (let c = 0; c < cycles && opts.bytes - bytes > 3_000; c++) {
        const toolId = `toolu_${hex(rng, 22)}`;
        const blocks: object[] = [];
        if (rng() < 0.5) {
          blocks.push({
            type: 'thinking',
            thinking: slice(text, 400 + Math.floor(rng() * 1800)),
            signature: slice(img, 100 + Math.floor(rng() * 200)),
          });
        }
        blocks.push({ type: 'text', text: slice(text, 50 + Math.floor(rng() * 200)) });
        const tool = TOOLS[Math.floor(rng() * TOOLS.length)] ?? 'Bash';
        blocks.push({
          type: 'tool_use',
          id: toolId,
          name: tool,
          input: { command: slice(text, 40 + Math.floor(rng() * 200)).replace(/\n/g, ' ') },
        });
        assistant(blocks, 'tool_use');
        let size = pickResult(
          rng,
          opts.bytes - bytes,
          // JSON escapes (newlines) grow a string by a few percent.
          Math.floor(((opts.maxLineBytes ?? Number.MAX_SAFE_INTEGER) - 8_192) * 0.9),
        );
        // Keep tool results near their share of the bytes: a big pick waits until the share is below target.
        if ((resultBytes + size.chars) / (bytes + size.chars) > RESULT_SHARE) {
          size = { chars: Math.floor(300 + rng() * 2_500), image: false };
        }
        let content: string | object[];
        if (size.image) {
          content = [
            { type: 'text', text: 'Screenshot captured.' },
            {
              type: 'image',
              source: { type: 'base64', media_type: 'image/png', data: slice(img, size.chars) },
            },
          ];
        } else {
          content = slice(text, size.chars);
        }
        const before = bytes;
        emit('user', {
          role: 'user',
          content: [{ type: 'tool_result', tool_use_id: toolId, content }],
        });
        resultBytes += bytes - before;
      }
      assistant([{ type: 'text', text: slice(text, 60 + Math.floor(rng() * 220)) }], 'end_turn');
    }
    // The last line carries a needle so searches have one known hit.
    emit('user', { role: 'user', content: `Final check: ${NEEDLE}` });
  } finally {
    closeSync(fd);
  }
  return { sessionId, bytes, lines, longLines };
}
