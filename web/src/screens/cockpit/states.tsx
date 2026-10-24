import type {
  CommandsResult,
  DeletionPreview,
  DeviceInfo,
  Entry,
  LiveSessionMeta,
  ProviderInfo,
  Queue,
  RuntimeCapabilities,
  SessionHandoffRequest,
  SessionRuntimeInfo,
} from '@hoplet/shared';
import { type ReactNode, useState } from 'react';
import { blockedDocs, DOCS_ASK, KID_DOCS, kidsTree } from '../../lib/children-fixtures.ts';
import { DEPS, FIX, NOTES, REVIEW, templateList } from '../settings/templates-fixtures.ts';
import { B1, B2, B4, DOCS_PATH, sessionUsage } from '../settings/usage-fixtures.ts';
import { live as liveWorkspace, snapshot } from '../workspace/fixtures.ts';
import type { PathsState } from './composer/use-completions.ts';
import { rememberBlock } from './conversation/blocks.tsx';
import type { LiveTurn, OpenPermission, PlanResolution } from './conversation/live-turn.ts';
import type { CardRules } from './conversation/live-view.tsx';
import type { PlanView } from './conversation/plan-card.tsx';
import { RuleLine } from './conversation/rule-line.tsx';
import type { DeleteFixture } from './delete-dialog.tsx';
import { Cockpit, type CockpitFixture } from './index.tsx';
import type { AddProjectFixture } from './sidebar/add-project.tsx';
import { dirKey } from './sidebar/folded-dirs.ts';
import type { SearchState } from './use-search.ts';

// Fixtures for /__states/cockpit/<state>: every state of the design with its conversation and composer.
// Nothing here is read by the real screen.

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0);
const MINUTE = 60_000;

const catalogId = '11111111-2222-4333-8444-555555555555';
const handoffId = '22222222-2222-4333-8444-555555555555';
const notesId = '33333333-2222-4333-8444-555555555555';
const appId = '44444444-2222-4333-8444-555555555555';

function session(
  over: Partial<LiveSessionMeta> & Pick<LiveSessionMeta, 'sessionId' | 'title' | 'deviceName'>,
): LiveSessionMeta {
  return {
    projectPath: '/Users/mark/github/repopro/hoplet',
    projectDir: '-Users-mark-github-repopro-hoplet',
    provider: 'claude',
    parentId: null,
    childCount: 0,
    ownerConnected: true,
    createdAt: NOW - 7_200_000,
    updatedAt: NOW - MINUTE,
    lineCount: 18_302,
    byteSize: 12_000,
    ownerEpoch: 0,
    gitBranch: 'main',
    runtime: { state: over.ownerConnected === false ? 'offline' : 'idle' },
    ...over,
  };
}

function fixtureSessions(macbookOnline: boolean): LiveSessionMeta[] {
  return [
    session({
      sessionId: catalogId,
      title: 'byte cursor catalog',
      deviceName: 'MacBook Pro',
      ownerDeviceId: 'dev-macbook',
      ownerConnected: macbookOnline,
    }),
    session({
      sessionId: handoffId,
      title: 'Hand off sessions with snapshots',
      deviceName: 'MacBook Pro',
      ownerDeviceId: 'dev-macbook',
      ownerConnected: macbookOnline,
      updatedAt: NOW - 3 * 3_600_000,
    }),
    session({
      sessionId: notesId,
      title: '多设备远程同步 AI 会话项目',
      deviceName: 'studio',
      projectPath: '/Users/mark/notes',
      projectDir: '-Users-mark-notes',
      ownerDeviceId: 'dev-studio',
      updatedAt: NOW - 20 * MINUTE,
    }),
    session({
      sessionId: appId,
      title: 'Liquid Glass tab bar',
      deviceName: 'office-mini',
      projectPath: '/Users/mark/github/repopro/hoplet-app',
      projectDir: '-Users-mark-github-repopro-hoplet-app',
      ownerDeviceId: 'dev-office',
      ownerConnected: false,
      updatedAt: NOW - 4 * 86_400_000,
    }),
  ];
}

/** What a driver declares: the permission mode, a model, Thinking and Fast, with the model `model` (the default until a session picks) and the mode `mode`. */
function capabilitiesOn(model = 'default', mode = 'default'): RuntimeCapabilities {
  return {
    permissions: true,
    interrupt: true,
    images: true,
    steer: true,
    config: [
      {
        type: 'select',
        id: 'mode',
        name: 'Permission mode',
        category: 'mode',
        currentValue: mode,
        options: [
          {
            value: 'default',
            name: 'Manual',
            description: 'Ask before edits and commands.',
            icon: 'shield',
          },
          {
            value: 'acceptEdits',
            name: 'Accept edits',
            icon: 'file-pen',
            description: 'Edit files without asking; ask before commands.',
          },
          {
            value: 'plan',
            name: 'Plan',
            description: 'Explore and plan; no edits or commands.',
            icon: 'list-checks',
          },
          {
            value: 'auto',
            name: 'Auto',
            icon: 'sparkles',
            description: 'Claude decides when to ask, with safety checks.',
          },
          {
            value: 'bypassPermissions',
            name: 'Bypass permissions',
            description: 'Run everything without asking.',
            icon: 'shield-alert',
            risk: 'high',
          },
        ],
      },
      {
        type: 'select',
        id: 'model',
        name: 'Model',
        category: 'model',
        currentValue: model,
        options: [
          { value: 'default', name: 'Default model' },
          { value: 'opus-5.5', name: 'opus-5.5' },
          { value: 'sonnet-5.5', name: 'sonnet-5.5' },
          { value: 'haiku-5', name: 'haiku-5' },
        ],
      },
      {
        type: 'boolean',
        id: 'thinking',
        name: 'Thinking',
        category: 'thought_level',
        currentValue: true,
      },
      { type: 'boolean', id: 'fast', name: 'Fast', category: 'model_config', currentValue: false },
    ],
  };
}

export const capabilities = capabilitiesOn();

/** Two pasted images: a 48 px app tile and a screenshot. */
const COMPOSE_IMAGES = [
  {
    mediaType: 'image/png' as const,
    data: 'iVBORw0KGgoAAAANSUhEUgAAADAAAAAwCAIAAADYYG7QAAAAo0lEQVR42u3YwQmAYAyDUfHuAuKkLq+CIOJ/sW3S9hD4BnjXZDr2tVWTQAIJJBCxbZkbgS5NI9Ct6QJ6NC1Ab0096KMpBo2aSpBPwwK5NRRQRIMHBTVgUFyDBEE0MBBKgwEBNQAQVhMFwTUhEEPjB5E0ThBP4wFRNWYQW2MDJWgMoBzNX1Caxg8qXq5pGg+oxbZP09hAjd6PNI0OK4EEEkigsRMQfjWs1GRa2AAAAABJRU5ErkJggg==',
  },
  {
    mediaType: 'image/png' as const,
    data: 'iVBORw0KGgoAAAANSUhEUgAAADAAAAAwCAIAAADYYG7QAAAAYElEQVR42u3VsQmAMBgF4YwgSgg6qr21bbZNNtAiGPjlgzfAFXe8tJf8sG1dJi8FBqr39d1+AcShN6B2HiMDNB9I9rIH5O1lzyFAspc9hwDJXvYcAiR72ZMakOxlH9+hDrvGzR3AkyXYAAAAAElFTkSuQmCC',
  },
];

/** A configuration that extends Claude Code, as an agent announces it. */
export function claudeConfiguration(
  id: string,
  name: string,
  over: { enabled?: boolean; keySet?: boolean } = {},
): ProviderInfo {
  return {
    provider: 'claude',
    configuration: {
      id,
      name,
      extends: 'claude',
      enabled: over.enabled ?? true,
      ...(id === 'claude' ? {} : { command: 'claude' }),
      env: id === 'claude' ? [] : [{ name: 'ANTHROPIC_AUTH_TOKEN', set: over.keySet ?? true }],
      defaults: {},
    },
    capabilities,
  };
}

/** `labBox` adds a machine whose agent announced no system, as an agent from before the system was reported. */
export function fixtureDevices(
  macbookOnline: boolean,
  terminal = true,
  labBox = false,
  /** The machines report their resources (the design's resources states). */
  reporting = false,
  /** The configurations a machine announces instead of Claude Code alone, by device ID. */
  configurations: Record<string, ProviderInfo[]> = {},
): DeviceInfo[] {
  const providers = [
    {
      provider: 'claude' as const,
      configuration: {
        id: 'claude',
        name: 'Claude Code',
        extends: 'claude' as const,
        enabled: true,
        env: [],
        defaults: {},
      },
      capabilities,
    },
  ];
  const GIB = 1024 ** 3;
  const GB = 1e9;
  const reading = (cpu: number, mem: [number, number], disk: [number, number], ago = 0) => ({
    at: NOW - ago,
    cpu,
    memUsed: Math.round(mem[0] * GIB),
    memTotal: Math.round(mem[1] * GIB),
    diskFree: Math.round(disk[0] * GB),
    diskTotal: Math.round(disk[1] * GB),
  });
  return [
    {
      deviceId: 'dev-macbook',
      name: 'MacBook Pro',
      labels: [],
      connected: macbookOnline,
      system: { os: 'darwin', arch: 'arm64' },
      providers: macbookOnline ? (configurations['dev-macbook'] ?? providers) : [],
      ...(terminal ? { terminal: true } : {}),
      previewHost: 'mbp.tail1f2a.ts.net',
      ...(reporting
        ? {
            resources: reading(12, [20.2, 32], [184, 994], 4_000),
            warn: { memory: false, disk: false },
          }
        : {}),
    },
    {
      deviceId: 'dev-studio',
      name: 'studio',
      labels: [],
      connected: true,
      system: { os: 'linux', arch: 'amd64' },
      providers: configurations['dev-studio'] ?? providers,
      ...(reporting
        ? {
            resources: reading(71, [60.2, 64], [412, 1000], 9_000),
            warn: { memory: true, disk: false },
          }
        : {}),
    },
    {
      deviceId: 'dev-office',
      name: 'office-mini',
      labels: [],
      connected: false,
      system: { os: 'windows', arch: 'amd64' },
      providers: [],
      ...(reporting
        ? {
            resources: reading(8, [6.5, 16], [38, 256], 6 * 3_600_000),
            warn: { memory: false, disk: false },
          }
        : {}),
    },
    ...(labBox
      ? [
          {
            deviceId: 'dev-lab',
            name: 'lab-box',
            connected: true,
            system: null,
            providers,
            labels: [],
          },
        ]
      : []),
  ];
}

const searchFixture: SearchState = {
  scanning: true,
  hits: [
    {
      sessionId: catalogId,
      lineNo: 18_241,
      excerpt: 'Byte cursor vs whole-file read. The file only appends…',
    },
    {
      sessionId: catalogId,
      lineNo: 18_109,
      excerpt: 'Keep the byte cursor in the index row so a restart resumes at the last line',
    },
    {
      sessionId: handoffId,
      lineNo: 9_114,
      excerpt: 'The snapshot leaves the byte cursor unchanged on the target',
    },
    { sessionId: notesId, lineNo: 2_310, excerpt: '同步只看 byte cursor，按偏移续传' },
  ],
};

// The turn the design shows: lines 18,240 to 18,301 of the catalog session.
const at = (time: string) => `2026-10-05T${time}`;
const writeContent = `${source(64)}\n`;
const root = '/Users/mark/github/repopro/hoplet';
const ndjson = [
  '{"line":0,"role":"user","ts":"2026-10-05T09:00:00Z"}',
  '{"line":1,"role":"assistant","blocks":[{"type":"thinking","tokens":412}]}',
  '{"line":2,"role":"assistant","blocks":[{"type":"text","text":"Reading the store"}]}',
  '{"line":3,"role":"assistant","blocks":[{"type":"tool-call","name":"Read"}]}',
  '{"line":4,"role":"user","blocks":[{"type":"tool-result","bytes":6240}]}',
  '{"line":5,"role":"assistant","blocks":[{"type":"text","text":"The index row"}]}',
  '{"line":6,"role":"assistant","blocks":[{"type":"tool-call","name":"Edit"}]}',
  '{"line":7,"role":"user","blocks":[{"type":"tool-result","bytes":118}]}',
  '{"line":8,"role":"assistant","blocks":[{"type":"thinking","tokens":96}]}',
  '{"line":9,"role":"assistant","blocks":[{"type":"text","text":"Verified on"}]}',
  '{"line":10,"role":"user","ts":"2026-10-05T09:01:08Z"}',
  '{"line":11,"role":"assistant","blocks":[{"type":"tool-call","name":"Bash"}]}',
  '{"line":12,"role":"user","blocks":[{"type":"tool-result","bytes":2210}]}',
  '{"line":13,"role":"assistant","blocks":[{"type":"text","text":"The tail page"}]}',
];
const BLOCK_BYTES = 412 * 1024;

/** A source file of `lines` lines, the way `Write` stores it. */
function source(lines: number): string {
  const body = [
    'export async function tail(store: Store, row: ChunkRow, bytes: Uint8Array) {',
    '  const at = row.byteSize; // the byte size is the cursor: append first',
    '  await store.write(row.id, bytes);',
    '  return at + bytes.length;',
    '}',
    '',
  ];
  return Array.from({ length: lines }, (_, i) => body[i % body.length] ?? '').join('\n');
}

/** What `Read` returns for the first lines of a file: `cat -n` output. */
function numbered(lines: number): string {
  const code = source(lines).split('\n');
  return code.map((line, i) => `${String(i + 1).padStart(6)}→${line}`).join('\n');
}

const entries: Entry[] = [
  {
    lineNo: 18_240,
    role: 'user',
    ts: at('09:00:00'),
    blocks: [
      { type: 'text', text: 'The 80 MB session is slow to open. Can we stream only new bytes?' },
    ],
  },
  {
    lineNo: 18_241,
    role: 'assistant',
    ts: at('09:00:05'),
    blocks: [{ type: 'thinking', text: 'Byte cursor vs whole-file read. The file only appends…' }],
  },
  {
    lineNo: 18_242,
    role: 'assistant',
    ts: at('09:00:09'),
    blocks: [
      {
        type: 'text',
        text: 'Yes. The file only appends, so the byte offset of the old EOF is the cursor. I’ll read the chunk and split the items.',
      },
    ],
  },
  {
    lineNo: 18_243,
    role: 'assistant',
    ts: at('09:00:12'),
    blocks: [
      {
        type: 'tool-call',
        id: 'tu1',
        name: 'Read',
        kind: 'read',
        input: { file_path: `${root}/c-3.ndjson`, offset: 1, limit: 500 },
      },
    ],
  },
  {
    lineNo: 18_244,
    role: 'user',
    ts: at('09:00:14'),
    blocks: [
      {
        elided: true,
        type: 'tool-result',
        bytes: BLOCK_BYTES,
        preview: ndjson.slice(0, 3).join('\n'),
        ref: '18244/0',
      },
    ],
  },
  {
    lineNo: 18_245,
    role: 'assistant',
    ts: at('09:00:20'),
    blocks: [
      {
        type: 'tool-call',
        id: 'tuw',
        name: 'Write',
        kind: 'write',
        input: { file_path: `${root}/core/src/ingest/tail.ts`, content: writeContent },
      },
    ],
  },
  {
    lineNo: 18_246,
    role: 'user',
    ts: at('09:00:21'),
    blocks: [
      {
        type: 'tool-result',
        id: 'tuw',
        content: 'File created successfully at: core/src/ingest/tail.ts',
      },
    ],
  },
  {
    lineNo: 18_247,
    role: 'assistant',
    ts: at('09:00:30'),
    blocks: [
      {
        type: 'tool-call',
        id: 'tu2',
        name: 'Edit',
        kind: 'edit',
        input: {
          file_path: `${root}/web/src/screens/cockpit/items.ts`,
          old_string:
            'export function toItems(entry: Entry): Item[] {\n  const items: Item[] = [];\n  const ts = fmtTime(entry.ts);',
          new_string:
            'export function toItems(entry: Entry): Item[] {\n  const acts: Item[] = [];\n  const texts: Item[] = [];\n  const ts = fmtTime(entry.ts);',
        },
      },
    ],
  },
  {
    lineNo: 18_248,
    role: 'user',
    ts: at('09:00:31'),
    blocks: [{ type: 'tool-result', id: 'tu2', content: 'The file has been updated.' }],
  },
  {
    lineNo: 18_249,
    role: 'assistant',
    ts: at('09:00:40.000'),
    blocks: [
      { type: 'tool-call', id: 'tu3', name: 'Bash', kind: 'run', input: { command: 'ls core' } },
    ],
  },
  {
    lineNo: 18_250,
    role: 'user',
    ts: at('09:00:40.100'),
    blocks: [
      {
        type: 'tool-result',
        id: 'tu3',
        content: 'migrations  package.json  src  tsconfig.json',
      },
    ],
  },
  {
    lineNo: 18_251,
    role: 'assistant',
    ts: at('09:00:46.000'),
    blocks: [
      {
        type: 'tool-call',
        id: 'tu4',
        name: 'mcp__github__list_prs',
        kind: 'call',
        mcp: { server: 'github', tool: 'list_prs' },
        input: { owner: 'yangflow' },
      },
    ],
  },
  {
    lineNo: 18_252,
    role: 'user',
    ts: at('09:00:46.600'),
    blocks: [
      {
        type: 'tool-result',
        id: 'tu4',
        content: JSON.stringify([
          { number: 147, title: 'Build the Hoplet cockpit screen', state: 'merged' },
          { number: 146, title: 'Start sessions from the web UI', state: 'merged' },
          { number: 138, title: 'Redesign the Hoplet screen', state: 'merged' },
        ]),
      },
    ],
  },
  {
    lineNo: 18_253,
    role: 'assistant',
    ts: at('09:00:50'),
    blocks: [
      {
        type: 'tool-call',
        id: 'tu5',
        name: 'WebFetch',
        kind: 'fetch',
        input: { url: 'https://example.com' },
      },
    ],
  },
  {
    lineNo: 18_254,
    role: 'user',
    ts: at('09:00:52'),
    blocks: [
      {
        type: 'tool-result',
        id: 'tu5',
        content: `# Example Domain\n\nThis domain is for use in documentation examples.\n${'.'.repeat(18 * 1024 - 80)}`,
      },
    ],
  },
  {
    lineNo: 18_255,
    role: 'assistant',
    ts: at('09:00:55'),
    blocks: [
      { type: 'image', mediaType: 'image/png', url: 'screenshot.png', data: 'A'.repeat(251_000) },
    ],
  },
  {
    lineNo: 18_301,
    role: 'assistant',
    ts: at('09:01:08'),
    blocks: [
      {
        type: 'text',
        text: 'Verified on a 23 MB fixture: tail page in about a second. Next the 256 MB fixture.',
      },
    ],
  },
];

const PROMPT = 'Run pnpm check and fix what fails.';
/** The live turn starts at 14:06 local time, as in the design. */
const LIVE_START = new Date(2026, 9, 8, 14, 6, 0).getTime();

function liveTurn(over: Partial<LiveTurn>): LiveTurn {
  return {
    id: 'live-fixture',
    prompt: PROMPT,
    startedAt: LIVE_START,
    boundary: 18_302,
    joined: false,
    reachedRunning: true,
    rows: [],
    phase: 'live',
    ...over,
  };
}

const CHECK_OUT = [
  '> biome check . && tsc -b && vitest run',
  ' ✓ core/src/ingest/tail.test.ts (12 tests) 84 ms',
  ' × core/src/store/chunks.test.ts (9 tests | 1 failed) 112 ms',
  '   × appendChunk > renames to .gz after the index commit',
  '     AssertionError: expected "rename" to be called after "commit"',
  ' Test Files  1 failed | 23 passed (24)',
  '      Tests  1 failed | 211 passed (212)',
].join('\n');

const SEARCH_OUT = [
  'core/src/store/chunks.ts:30:export async function appendChunk(',
  'core/src/store/chunks.test.ts:41:describe("appendChunk", () => {',
  'core/src/ingest/tail.ts:88:  const at = await appendChunk(store, row, bytes);',
].join('\n');

const runningRows: LiveTurn['rows'] = [
  { id: 0, kind: 'thinking', tokens: 1204 },
  {
    id: 1,
    kind: 'tool',
    toolUseId: 'lt1',
    tool: 'Bash',
    toolKind: 'run',
    input: { command: 'pnpm check' },
    startedAt: LIVE_START + 1000,
    endedAt: LIVE_START + 43_000,
    result: { text: `Exit code 1\n${CHECK_OUT}`, failed: true, bytes: 38 * 1024 },
  },
  {
    id: 2,
    kind: 'text',
    text: 'One test fails: `chunks.test.ts` expects the gz rename after the index commit. The order in `appendChunk` is reversed, so I’ll swap it.',
  },
  {
    id: 3,
    kind: 'tool',
    toolUseId: 'lt2',
    tool: 'Grep',
    toolKind: 'search',
    input: { pattern: 'appendChunk', output_mode: 'content', '-n': true },
    startedAt: LIVE_START + 44_000,
    endedAt: LIVE_START + 44_500,
    result: { text: SEARCH_OUT, failed: false, bytes: SEARCH_OUT.length },
  },
  {
    id: 4,
    kind: 'tool',
    toolUseId: 'lt3',
    tool: 'Edit',
    toolKind: 'edit',
    input: {
      file_path: 'core/src/store/chunks.ts',
      old_string:
        '  await rename(row.tmp, join(store.dir, row.id + GZ));\n  await store.write(row.id, bytes);\n  await store.commit(row);',
      new_string:
        '  await fh.write(bytes);\n  await fh.close();\n  await commitIndex(store, row, at + bytes.length);\n  await rename(row.tmp, join(store.dir, row.id + GZ)); // after the commit',
    },
    startedAt: LIVE_START + 45_000,
    endedAt: LIVE_START + 45_100,
    result: { text: 'The file has been updated.', failed: false, bytes: 26 },
  },
  {
    id: 5,
    kind: 'tool',
    toolUseId: 'lt4',
    tool: 'Bash',
    toolKind: 'run',
    input: { command: 'pnpm vitest run chunks' },
    startedAt: LIVE_START + 36_000,
  },
];

const permissionRows: LiveTurn['rows'] = [
  { id: 0, kind: 'thinking', tokens: 812 },
  {
    id: 1,
    kind: 'tool',
    toolUseId: 'lp1',
    tool: 'Read',
    toolKind: 'read',
    input: { file_path: 'core/src/store/chunks.ts' },
    startedAt: LIVE_START + 4000,
    endedAt: LIVE_START + 5000,
    result: { text: numbered(120), failed: false, bytes: 6.1 * 1024 },
  },
];

const permission: OpenPermission = {
  requestId: 'a1f3c0de',
  tool: 'Bash',
  toolKind: 'run',
  input: { command: 'pnpm check 2>&1 | tail -40' },
  receivedAt: LIVE_START + 31_000 - 19_000,
};

const askScene = (request: OpenPermission, draft?: NonNullable<CardRules['open']>) => (
  <Scene
    variant={{
      runtime: { state: 'waiting-permission', capabilities },
      live: {
        turn: liveTurn({ rows: permissionRows }),
        elapsed: 31_000,
        permissions: [request],
        ...(draft ? { draft } : {}),
      },
    }}
  />
);

const ruleRequest = (over: Partial<OpenPermission>): OpenPermission => ({
  ...permission,
  ...over,
});

const PLAN_TEXT = `## Roll back the byte cursor after a failed commit

When a chunk commit fails, the cursor must return to the last stored offset.

1. Wrap \`appendChunk\` in a transaction that records the offset it started from.
2. On failure, restore \`byte_size\` from that offset and drop the partial chunk.
3. Add a test that fails the commit and checks the next append resumes at the old offset.
4. Run \`pnpm check\`.
`;

const LONG_PLAN_TEXT = `${PLAN_TEXT}\n${Array.from(
  { length: 60 },
  (_, i) => `- Step note ${i + 1}: keep the stored bytes untouched while the commit is retried.`,
).join('\n')}\n`;

const planExit = (plan: string, freshContext = false): NonNullable<OpenPermission['planExit']> => ({
  plan,
  option: 'mode',
  modes: ['acceptEdits', 'default', 'auto'],
  default: 'acceptEdits',
  ...(freshContext ? { freshContext: true } : {}),
});

const planPermission = (plan = PLAN_TEXT, freshContext = false): OpenPermission => ({
  requestId: 'p1a2b3c4',
  tool: 'ExitPlanMode',
  toolKind: 'other',
  input: { plan },
  receivedAt: LIVE_START + 31_000,
  planExit: planExit(plan, freshContext),
});

const planResolution = (over: Partial<PlanResolution>): PlanResolution => ({
  requestId: 'p1a2b3c4',
  title: 'Roll back the byte cursor after a failed commit',
  steps: 4,
  bytes: new TextEncoder().encode(PLAN_TEXT).length,
  plan: PLAN_TEXT,
  outcome: 'approved',
  mode: 'acceptEdits',
  at: LIVE_START + 40_000,
  afterId: 1,
  ...over,
});

const planScene = (permissions: OpenPermission[], plan?: PlanView, macbookOnline = true) => (
  <Scene
    macbookOnline={macbookOnline}
    variant={{
      runtime: { state: 'waiting-permission', capabilities },
      live: {
        turn: liveTurn({ rows: permissionRows }),
        elapsed: 31_000,
        permissions,
        ...(plan ? { plan } : {}),
      },
    }}
  />
);

const QUEUE_TEXTS = [
  'Then run the Go tests too: cd agent && go test ./...',
  'Update docs/architecture.md if it describes the rename order',
  'Open a draft pull request titled fix(core): commit the index before the gz rename',
];
const queueOf = (over: Partial<Queue> = {}, sending = false): Queue => ({
  items: QUEUE_TEXTS.map((text, i) => ({
    id: `q${i + 1}`,
    text,
    attachments: [],
    version: 1,
    state: sending && i === 0 ? ('sending' as const) : ('queued' as const),
  })),
  ...over,
});
const BUDGET_PROMPT = 'Document the notification kinds in docs/api.md';
const inDocs = (base: LiveSessionMeta[]) =>
  base.map((x) => (x.sessionId === catalogId ? { ...x, projectPath: DOCS_PATH } : x));
const STEER_NOW = 'Also check the same order in the compaction path.';
const STEER_PEND = 'Use the existing fixture; do not generate a new one.';

/** The session of a queue state: the running turn (or the state `runtime` names) with the queue in its tray. */
const queueScene = (
  variant: Variant,
  queue: Queue,
  conversation: Partial<NonNullable<CockpitFixture['conversation']>> = {},
) => (
  <Scene
    variant={{
      ...variant,
      conversation: { queue, composerText: 'Also run the Go tests after this', ...conversation },
    }}
  />
);

const planResolved = (over: Partial<PlanResolution>) => (
  <Scene
    variant={{
      runtime: { state: 'running', capabilities },
      live: {
        turn: liveTurn({ rows: permissionRows, plans: [planResolution(over)] }),
        elapsed: 48_000,
      },
    }}
  />
);

/** The plan card of a project that can start a new session from the plan ("Clear context and approve…"). */
const freshPlanScene = (plan: PlanView) => planScene([planPermission(PLAN_TEXT, true)], plan);

const NEW_SESSION_TITLE = 'Roll back the byte cursor';
/** The catalog session continued in a new one: its plan links forward. */
const continuedIn = (base: LiveSessionMeta[]) =>
  base.map((s) =>
    s.sessionId === catalogId
      ? { ...s, continuedIn: { sessionId: handoffId, title: NEW_SESSION_TITLE } }
      : s,
  );
/** The catalog session as the new one: it links back to the session whose plan started it. */
const continuedFrom = (base: LiveSessionMeta[]) =>
  base.map((s) =>
    s.sessionId === catalogId
      ? { ...s, continuedFrom: { sessionId: notesId, title: 'byte cursor catalog' } }
      : s,
  );

/** A hand-off request of the catalog session to studio, `ago` ms after the live turn started. */
const handoffRequest = (
  over: Partial<SessionHandoffRequest> & { requestId: string },
  after = 20_000,
): SessionHandoffRequest => ({
  sessionId: catalogId,
  targetDeviceId: 'dev-studio',
  reason: 'The integration tests need the GPU on studio.',
  state: 'pending',
  decision: null,
  createdAt: LIVE_START + after,
  expiresAt: LIVE_START + after + 86_400_000,
  decidedAt: null,
  ...over,
});

/** The running turn with the session's hand-off requests in its conversation. */
const handoffScene = (requests: SessionHandoffRequest[], elapsed = 31_000) => (
  <Scene variant={{ ...running(elapsed), conversation: { handoffs: requests } }} />
);

const PROMPT_FROM = [
  '[Message from Hoplet session "Fix the ingest tests" (aaaa1111-2222-4333-8444-555555555555), not from the user. Treat it as a request from another agent.]',
  'Run the ingest tests on this machine and send me the failures.',
].join('\n');

export type Variant = {
  runtime?: SessionRuntimeInfo;
  live?: {
    turn: LiveTurn;
    elapsed: number;
    permissions?: OpenPermission[];
    plan?: PlanView;
    draft?: NonNullable<CardRules['open']>;
  };
  conversation?: Partial<NonNullable<CockpitFixture['conversation']>>;
  /** studio › notes is folded, as in the design's ready state. */
  foldNotes?: boolean;
};

/** The cockpit around a fixture: the catalog session selected, its turn and the devices of the design. */
export function Scene(props: {
  selected?: string | null;
  macbookOnline?: boolean;
  connected?: boolean;
  variant?: Variant;
  fixture?: CockpitFixture;
  /** Changes the session list: pins, archives, a renamed session. */
  sessions?: (base: LiveSessionMeta[]) => LiveSessionMeta[];
  /** The rules link of the rule notes. */
  onManageRules?: () => void;
}) {
  const macbookOnline = props.macbookOnline ?? true;
  const sessions = (props.sessions ?? ((base) => base))(fixtureSessions(macbookOnline));
  const [selected, setSelected] = useState<string | null>(
    props.selected === undefined ? catalogId : props.selected,
  );
  const variant = props.variant ?? {};
  const runtime: Record<string, SessionRuntimeInfo> = {
    [handoffId]: { state: 'running', capabilities },
    [notesId]: { state: 'waiting-permission', capabilities },
  };
  runtime[catalogId] = variant.runtime ?? {
    state: macbookOnline ? 'idle' : 'offline',
    capabilities,
  };
  rememberBlock(catalogId, 18_244, 0, ndjson.join('\n'));
  const live = variant.live;
  return (
    <Cockpit
      sessions={sessions}
      selected={selected}
      onSelect={setSelected}
      live={props.connected ?? true}
      error={null}
      host="hoplet.local"
      runtime={runtime}
      {...(props.onManageRules ? { onManageRules: props.onManageRules } : {})}
      fixture={{
        conversation:
          selected === catalogId
            ? {
                entries,
                now: LIVE_START + (live?.elapsed ?? 0),
                ...(live
                  ? {
                      live: {
                        turn: live.turn,
                        permissions: live.permissions ?? [],
                        ...(live.plan ? { plan: live.plan } : {}),
                        ...(live.draft ? { draft: live.draft } : {}),
                      },
                    }
                  : {}),
                ...variant.conversation,
              }
            : { entries: [] },
        voiceAvailable: true,
        foldedDirs: [
          dirKey('office-mini', '/Users/mark/github/repopro/hoplet-app'),
          ...(variant.foldNotes ? [dirKey('studio', '/Users/mark/notes')] : []),
        ],
        devices: fixtureDevices(macbookOnline),
        workspace: { data: macbookOnline ? liveWorkspace() : { phase: 'snapshot', snapshot } },
        now: NOW,
        ...props.fixture,
      }}
    />
  );
}

export const running = (elapsed: number, over?: Partial<LiveTurn>): Variant => ({
  runtime: { state: 'running', capabilities },
  live: { turn: liveTurn({ rows: runningRows, ...over }), elapsed },
});

/** The catalog session with five children, the docs child blocked on `pnpm schema`. */
function childAskScene(variant: Variant) {
  return (
    <Scene
      variant={variant}
      sessions={(base) =>
        base.map((s) => (s.sessionId === catalogId ? { ...s, childCount: 5 } : s))
      }
      fixture={{
        children: {
          data: kidsTree(catalogId, blockedDocs),
          asks: { [KID_DOCS]: [DOCS_ASK] },
        },
      }}
    />
  );
}

/** What the owner answers about deleting a session (ADR 0023); the variants below change one part of it. */
const deletionOf = (over: Partial<DeletionPreview> = {}): DeletionPreview => ({
  owner: { deviceId: 'dev-macbook', name: 'MacBook Pro', online: true },
  running: false,
  vendorFile: { path: '~/.claude/projects/-Users-me-code-hoplet/3f9a1c2e-….jsonl' },
  worktree: null,
  previousOwner: null,
  ...over,
});
const studio = { deviceId: 'dev-studio', name: 'studio', online: true };
const handedOff = (dirty: { modified: number; untracked: number }, unpushed: number) => ({
  path: '~/code/hoplet-3f9a1c2e',
  branch: 'hoplet/3f9a1c2e',
  dirty,
  unpushed,
});
const deleteWorktree = deletionOf({
  owner: studio,
  worktree: handedOff({ modified: 0, untracked: 0 }, 0),
});
const deleteFixture = (delete_: DeleteFixture): CockpitFixture => ({ delete: delete_ });

/** The studio's note is pinned and the handoff session renamed. */
const pinned = (base: LiveSessionMeta[]) =>
  base.map((s) =>
    s.sessionId === notesId
      ? { ...s, pinnedAt: NOW - 86_400_000 }
      : s.sessionId === handoffId
        ? { ...s, titleOverride: 'Snapshot handoff', pinnedAt: NOW - 2 * 86_400_000 }
        : s,
  );
/** Two sessions of the MacBook Pro and the office-mini's one are archived. */
const archivedSessions = (base: LiveSessionMeta[]) =>
  base.map((s) =>
    s.sessionId === handoffId
      ? { ...s, archivedAt: NOW - 3_600_000 }
      : s.sessionId === appId
        ? { ...s, archivedAt: NOW - 2 * 86_400_000 }
        : s,
  );
const HOME = '/Users/mark';
const HOME_GITHUB = `${HOME}/github`;
const dir = (name: string, git = false) => ({ name, git });
/** What the browse root of MacBook Pro answers, and one directory below it. */
const browseFixture: AddProjectFixture = {
  start: HOME,
  candidates: [`${HOME_GITHUB}/website`, `${HOME}/Documents/thesis`, `${HOME}/scratch/api-sketch`],
  listings: {
    [HOME]: {
      path: HOME,
      parent: null,
      root: HOME,
      entries: [
        dir('Desktop'),
        dir('Documents'),
        dir('Downloads'),
        dir('github'),
        dir('notes'),
        dir('scratch'),
      ],
      truncated: false,
    },
    [HOME_GITHUB]: {
      path: HOME_GITHUB,
      parent: HOME,
      root: HOME,
      entries: [
        dir('dotfiles', true),
        dir('experiments'),
        dir('repopro'),
        dir('website', true),
        dir('zsh-config', true),
      ],
      truncated: false,
    },
  },
};

/** A prompt longer than the field grows to, so it scrolls. */
const COMPOSE_TEXT = [
  'The composer forgets its height after a reload. Keep the height per browser, not per session, and clamp it',
  'to the window so the conversation keeps at least 240 px.',
  '',
  'Then check both schemes:',
  '- the grip shows on hover and on focus',
  '- a double-click on the edge resets the height',
  '- the arrow keys move it a line at a time',
  '- Enter on the grip resets it as well',
  '- a reload keeps the height',
  '- a narrow window clamps it',
].join('\n');

/** MacBook Pro's watched projects as its catalog lists them; thesis is not a repository. */
const DRAFT_DIRS = [
  { path: '/Users/mark/github/repopro/hoplet', name: 'hoplet', branch: 'main' },
  { path: '/Users/mark/github/website', name: 'website', branch: 'redesign' },
  { path: '/Users/mark/Documents/thesis', name: 'thesis' },
];

/** The settings fixtures' templates, with the model ids this screen's machines offer. */
const TEMPLATES = templateList(
  { ...REVIEW, config: { model: 'opus-5.5', mode: 'default', thinking: true } },
  {
    ...FIX,
    deviceId: 'dev-macbook',
    projectPath: DRAFT_DIRS[0]?.path,
    config: { model: 'opus-4', mode: 'acceptEdits' },
  },
  { ...NOTES, config: { model: 'sonnet-5.5', mode: 'plan' } },
  {
    ...DEPS,
    deviceId: 'dev-macbook',
    projectPath: DRAFT_DIRS[0]?.path,
    config: { model: 'sonnet-5.5', mode: 'acceptEdits' },
  },
);

/** The templates of the one-click entries, as the design lists them: two on each machine. */
const ENTRY_TEMPLATES = templateList(
  { ...REVIEW, config: { model: 'opus-5.5', mode: 'default', thinking: true } },
  { ...FIX, config: { model: 'opus-5.5', mode: 'acceptEdits', thinking: true, fast: true } },
  { ...NOTES, config: { model: 'sonnet-5.5', mode: 'plan' } },
  { ...DEPS, config: { model: 'sonnet-5.5', mode: 'acceptEdits' } },
);
/** Two sessions whose titles the palette's "review" matches. */
const reviewing = (base: LiveSessionMeta[]) =>
  base.map((s) =>
    s.sessionId === catalogId
      ? { ...s, titleOverride: 'Review the sync protocol change' }
      : s.sessionId === notesId
        ? { ...s, titleOverride: 'Review notes for the 0.8 release' }
        : s,
  );

const cmd = (
  name: string,
  description: string,
  over: Partial<CommandsResult['commands'][number]> = {},
): CommandsResult['commands'][number] => ({
  name,
  description,
  kind: 'command',
  source: 'builtin',
  ...over,
});

const BUILTIN_COMMANDS: CommandsResult['commands'] = [
  cmd('compact', 'Clear conversation history but keep a summary in context', {
    argumentHint: '[focus instructions]',
  }),
  cmd('context', 'Show how the context window is being used'),
  cmd('usage', 'Show plan usage and rate limits'),
  cmd('init', 'Create a CLAUDE.md that documents this codebase'),
  cmd('review', 'Review a pull request', { argumentHint: '[pull request]' }),
];

const SESSION_COMMANDS: CommandsResult = {
  source: 'session',
  commands: [
    ...BUILTIN_COMMANDS,
    cmd('hello', 'Say hello to someone by name', { argumentHint: '[name]', source: 'project' }),
    cmd('greet', 'Greet the user in the language of their last message', {
      kind: 'skill',
      source: 'user',
    }),
    cmd('dodopayments:checkout-integration', 'Add Dodo Payments checkout to a web app', {
      kind: 'skill',
      source: 'plugin',
      plugin: 'dodopayments',
    }),
  ],
};

const MENTION_PATHS = [
  'core/src/config/index.ts',
  'core/src/control/index.ts',
  'web/src/screens/Conversation.tsx',
];
const MENTION_PREFIX = 'Compare the cursor reset with ';

/** The ready session with the composer completing: the text, the list's commands or paths, the rows lit. */
function completing(
  text: string,
  over: {
    paths?: PathsState;
    selected?: number;
    hovered?: number;
    offline?: boolean;
  } = {},
): ReactNode {
  const { offline, ...rest } = over;
  return (
    <Scene
      macbookOnline={!offline}
      variant={{
        runtime: { state: offline ? 'offline' : 'ready', capabilities },
        conversation: {
          composerText: text,
          completions: { commands: SESSION_COMMANDS, selected: 0, ...rest },
          ...(offline ? { composerOffline: true } : {}),
        },
      }}
    />
  );
}

/** Command turns of the design: the prompts, what the commands printed and the reply. */
const user = (lineNo: number, time: string, text: string): Entry => ({
  lineNo,
  role: 'user',
  ts: at(time),
  blocks: [{ type: 'text', text }],
});
const reply = (lineNo: number, time: string, text: string): Entry => ({
  lineNo,
  role: 'assistant',
  ts: at(time),
  blocks: [{ type: 'text', text }],
});
const printed = (lineNo: number, time: string, text: string): Entry =>
  user(lineNo, time, `<local-command-stdout>${text}</local-command-stdout>`);
const FOLLOW_UP = [
  user(8, '12:52:00', 'Now check the reconnect path again'),
  reply(
    9,
    '12:52:20',
    'The reconnect path reads `byte_size` from the store, so the cursor survives.',
  ),
];
const CONTEXT_TABLE = [
  '62.4k of 200k tokens used',
  '',
  '| Category | Tokens | Share |',
  '| --- | --- | --- |',
  '| System prompt | 3.1k | 2% |',
  '| Tools | 11.8k | 6% |',
  '| Memory files | 2.4k | 1% |',
  '| Messages | 45.1k | 23% |',
  '| Free space | 137.6k | 69% |',
].join('\n');
const commandTurns: Record<string, Entry[]> = {
  'command-sent': [
    user(1, '12:40:00', '/hello world'),
    reply(2, '12:40:05', 'Hello, world! Ready when you are.'),
    user(3, '12:44:00', '/review 147'),
    reply(
      4,
      '12:44:30',
      'Pull request 147 adds the cockpit screen. The composer and the sidebar match the mockups; the diff view has one open question about large files.',
    ),
    ...FOLLOW_UP,
  ],
  'command-output': [
    user(
      1,
      '12:44:00',
      '<command-name>/context</command-name>\n<command-message>context</command-message>\n<command-args></command-args>',
    ),
    printed(2, '12:44:01', CONTEXT_TABLE),
  ],
  'command-compact': [
    // Claude Code writes the boundary, with the sizes, before the lines of the command.
    {
      lineNo: 1,
      role: 'system',
      ts: at('12:51:08'),
      blocks: [
        {
          type: 'other',
          kind: 'compact_boundary',
          label: 'Conversation compacted',
          text: '{"trigger":"manual","preTokens":45100,"postTokens":3200}',
        },
      ],
    },
    user(2, '12:51:00', '/compact keep the cursor discussion'),
    printed(3, '12:51:08', '\u001b[2mCompacted \u001b[22m'),
    ...FOLLOW_UP,
  ],
};
const commandState = (state: string): ReactNode => (
  <Scene
    variant={{
      runtime: { state: 'ready', capabilities },
      conversation: { entries: commandTurns[state] ?? [] },
    }}
  />
);

/** Every state the design shows, rendered at /__states/cockpit/<state> in development builds. */
export const states: Record<string, () => ReactNode> = {
  'card-a': () => askScene(ruleRequest({ input: { command: 'pnpm test --filter @hoplet/core' } })),
  'card-a-rule': () =>
    askScene(
      ruleRequest({
        input: { command: 'git push --force-with-lease origin feat/98-rule-audit' },
        rule: {
          decision: 'ask',
          ruleId: 'default.force-push',
          name: 'Force push',
          scope: 'builtin',
        },
      }),
    ),
  'card-a-nodraft': () => askScene(permission),
  'card-a-other': () =>
    askScene(
      ruleRequest({
        tool: 'Task',
        toolKind: 'other',
        input: { description: 'Audit the rule store', prompt: 'Read the store and report.' },
      }),
    ),
  'draft-session': () =>
    askScene(ruleRequest({ input: { command: 'pnpm test --filter @hoplet/core' } }), {
      action: 'allow',
    }),
  'draft-project': () =>
    askScene(
      ruleRequest({
        tool: 'Write',
        toolKind: 'write',
        input: {
          file_path: '/Users/mark/work/hoplet/docs/api.md',
          content: '# API\n',
        },
      }),
      { action: 'allow', scope: 'project', note: 'Docs are safe to edit' },
    ),
  'draft-deny': () =>
    askScene(
      ruleRequest({
        tool: 'WebFetch',
        toolKind: 'fetch',
        input: { url: 'https://pastebin.com/raw/Xk3a9' },
      }),
      { action: 'deny', note: 'No paste sites' },
    ),
  'rule-lines': () => (
    <Scene
      variant={{
        ...running(48_000),
        conversation: {
          rules: [
            { toolUseId: 'lt2', decision: 'allow', ruleId: 'r1', scope: 'global' },
            { toolUseId: 'lt3', decision: 'allow', ruleId: 'r2', scope: 'project' },
            { toolUseId: 'lt1', decision: 'deny', ruleId: 'default.secret-files', scope: 'agent' },
          ],
        },
      }}
    />
  ),
  'conv-codex-prompts': () => (
    <Scene
      variant={{
        ...running(48_000),
        runtime: { state: 'running', capabilities: { ...capabilities, rules: 'prompts-only' } },
      }}
    />
  ),
  'conv-codex-nohook': () => (
    <Scene
      onManageRules={() => undefined}
      variant={{
        ...running(48_000),
        runtime: { state: 'running', capabilities },
        conversation: { unenforced: ['lt2'] },
      }}
    />
  ),
  'conv-auto': () => (
    <main className="col" style={{ padding: 24, maxWidth: 640 }}>
      <h1 className="meta" style={{ margin: 0, fontSize: 13 }}>
        Lines the conversation shows when a rule decided
      </h1>
      <RuleLine
        rule={{ decision: 'allow', ruleId: 'r1', name: 'Run the checks', scope: 'global' }}
        onView={() => {}}
      />
      <RuleLine
        rule={{
          decision: 'deny',
          ruleId: 'default.secret-files',
          name: 'Secret files (.env)',
          scope: 'builtin',
        }}
        onView={() => {}}
      />
      <RuleLine
        rule={{
          decision: 'ask',
          ruleId: 'default.force-push',
          name: 'Force push',
          scope: 'builtin',
        }}
        onView={() => {}}
      />
    </main>
  ),
  empty: () => <Scene selected={null} fixture={{ hoverDir: 'notes' }} />,
  loaded: () => <Scene />,
  draft: () => <Scene fixture={{ draft: { dirs: DRAFT_DIRS } }} />,
  'draft-directory': () => <Scene fixture={{ draft: { dirs: DRAFT_DIRS, open: 'directory' } }} />,
  machines: () => (
    <Scene fixture={{ hoverDevice: 'MacBook Pro', devices: fixtureDevices(true, true, true) }} />
  ),
  resources: () => <Scene fixture={{ devices: fixtureDevices(true, true, true, true) }} />,
  'resources-menu': () => (
    <Scene fixture={{ devices: fixtureDevices(true, true, true, true), menuDevice: 'studio' }} />
  ),
  'machines-draft': () => (
    <Scene
      fixture={{
        devices: fixtureDevices(true, true, true),
        draft: { dirs: DRAFT_DIRS, open: 'machine' },
      }}
    />
  ),
  'machines-add': () => (
    <Scene
      fixture={{
        hoverDevice: 'MacBook Pro',
        devices: fixtureDevices(true, true, true),
        addProject: {
          deviceName: 'MacBook Pro',
          browse: { ...browseFixture, start: HOME_GITHUB, machineOpen: true },
        },
      }}
    />
  ),
  'draft-configuration': () => (
    <Scene
      fixture={{
        devices: fixtureDevices(true, true, false, false, {
          'dev-macbook': [
            claudeConfiguration('claude', 'Claude Code'),
            claudeConfiguration('glm', 'GLM'),
            claudeConfiguration('deepseek', 'DeepSeek', { keySet: false }),
            claudeConfiguration('qwen', 'Qwen', { enabled: false }),
          ],
        }),
        draft: { dirs: DRAFT_DIRS, open: 'configuration' },
      }}
    />
  ),
  'session-configuration': () => (
    <Scene
      sessions={(base) =>
        base.map((s) =>
          s.sessionId === catalogId ? { ...s, configuration: { id: 'glm', name: 'GLM' } } : s,
        )
      }
      fixture={{
        devices: fixtureDevices(true, true, false, false, {
          'dev-macbook': [
            claudeConfiguration('claude', 'Claude Code'),
            claudeConfiguration('glm', 'GLM'),
          ],
        }),
      }}
    />
  ),
  'draft-model': () => (
    <Scene
      fixture={{
        draft: {
          dirs: DRAFT_DIRS,
          open: 'model',
          prompt: 'Use the smaller model for the docs pass',
        },
      }}
    />
  ),
  'draft-templates': () => (
    <Scene fixture={{ draft: { dirs: DRAFT_DIRS, templates: TEMPLATES, open: 'templates' } }} />
  ),
  'template-fill': () => (
    <Scene
      fixture={{
        draft: {
          dirs: DRAFT_DIRS,
          templates: TEMPLATES,
          applied: { id: 'tpl-review', values: { pr: '#312' }, dialog: 'fill' },
        },
      }}
    />
  ),
  'template-applied': () => (
    <Scene
      fixture={{
        draft: {
          dirs: DRAFT_DIRS,
          templates: TEMPLATES,
          applied: { id: 'tpl-notes', values: { version: 'v0.9.0' } },
        },
      }}
    />
  ),
  'template-mismatch': () => (
    <Scene
      fixture={{
        draft: { dirs: DRAFT_DIRS, templates: TEMPLATES, applied: { id: 'tpl-fix' } },
      }}
    />
  ),
  'template-save': () => (
    <Scene
      fixture={{
        draft: {
          dirs: DRAFT_DIRS,
          templates: TEMPLATES,
          prompt: 'Review pull request {{pr}} in {{repo:hoplet}}.',
          save: { name: 'Review a pull request' },
        },
      }}
    />
  ),
  'completions-commands': () => completing('/', { hovered: 3 }),
  'completions-filter': () => completing('/c'),
  'completions-draft': () => (
    <Scene
      fixture={{
        draft: {
          dirs: DRAFT_DIRS,
          prompt: '/',
          completions: { commands: { source: 'builtin', commands: BUILTIN_COMMANDS }, selected: 0 },
        },
      }}
    />
  ),
  'mention-files': () =>
    completing(`${MENTION_PREFIX}@con`, {
      paths: { phase: 'ready', paths: MENTION_PATHS },
      hovered: 3,
    }),
  'mention-loading': () => completing(`${MENTION_PREFIX}@co`, { paths: { phase: 'loading' } }),
  'mention-empty': () =>
    completing(`${MENTION_PREFIX}@zq`, { paths: { phase: 'ready', paths: MENTION_PATHS } }),
  'mention-offline': () =>
    completing(`${MENTION_PREFIX}@`, { paths: { phase: 'offline' }, offline: true }),
  'command-sent': () => commandState('command-sent'),
  'command-output': () => commandState('command-output'),
  'command-compact': () => commandState('command-compact'),
  'add-project': () => (
    <Scene
      fixture={{
        hoverDevice: 'MacBook Pro',
        addProject: {
          deviceName: 'MacBook Pro',
          browse: { ...browseFixture, start: HOME_GITHUB, selected: `${HOME_GITHUB}/website` },
        },
      }}
    />
  ),
  'add-project-recent': () => (
    <Scene
      fixture={{
        hoverDevice: 'MacBook Pro',
        addProject: { deviceName: 'MacBook Pro', browse: browseFixture },
      }}
    />
  ),
  'add-project-error': () => (
    <Scene
      fixture={{
        hoverDevice: 'MacBook Pro',
        addProject: {
          deviceName: 'MacBook Pro',
          browse: {
            ...browseFixture,
            typed: { text: '/etc/ssh', reason: 'outside the browse root' },
          },
        },
      }}
    />
  ),
  ready: () => <Scene variant={{ runtime: { state: 'ready', capabilities }, foldNotes: true }} />,
  compose: () => (
    <Scene
      variant={{
        runtime: { state: 'idle', capabilities },
        conversation: { composerText: COMPOSE_TEXT, composerImages: COMPOSE_IMAGES },
      }}
    />
  ),
  'model-menu': () => (
    <Scene
      variant={{
        runtime: {
          state: 'ready',
          capabilities: capabilitiesOn('opus-5.5'),
        },
        conversation: {
          composerText: 'Use the smaller model for the docs pass',
          modelMenuOpen: true,
        },
      }}
    />
  ),
  'mode-manual': () => (
    <Scene variant={{ runtime: { state: 'ready', capabilities: capabilitiesOn('opus-5.5') } }} />
  ),
  'mode-menu': () => (
    <Scene
      variant={{
        runtime: { state: 'ready', capabilities: capabilitiesOn('opus-5.5') },
        conversation: { modeMenuOpen: true },
      }}
    />
  ),
  'mode-bypass': () => (
    <Scene
      variant={{
        runtime: {
          state: 'ready',
          capabilities: capabilitiesOn('opus-5.5', 'bypassPermissions'),
        },
      }}
    />
  ),
  'mode-plan': () => (
    <Scene
      variant={{
        runtime: { state: 'ready', capabilities: capabilitiesOn('opus-5.5', 'plan') },
      }}
    />
  ),
  recording: () => (
    <Scene
      variant={{
        runtime: { state: 'ready', capabilities },
        conversation: { recording: true },
      }}
    />
  ),
  starting: () => (
    <Scene
      variant={{
        runtime: { state: 'starting', capabilities },
        live: { turn: liveTurn({ reachedRunning: false }), elapsed: 0 },
      }}
    />
  ),
  running: () => <Scene variant={running(48_000)} />,
  // A child the catalog session started waits for a permission: the strip in the turn answers it (ADR 0035).
  'child-ask': () => childAskScene(running(48_000)),
  'child-ask-idle': () => childAskScene({}),
  'waiting-permission': () => (
    <Scene
      variant={{
        runtime: { state: 'waiting-permission', capabilities },
        live: {
          turn: liveTurn({ rows: permissionRows }),
          elapsed: 31_000,
          permissions: [permission],
        },
      }}
    />
  ),
  'queue-menu': () => queueScene(running(72_000), { items: [] }, { sendMenuOpen: true }),
  'queue-queued': () =>
    queueScene(running(72_000), queueOf(), { tray: { hovered: 'q2' }, composerText: '' }),
  'queue-editing': () =>
    queueScene(running(72_000), queueOf({}, true), { tray: { editing: 'q2' }, composerText: '' }),
  'queue-dragging': () =>
    queueScene(running(72_000), queueOf({}, true), { tray: { dragging: 'q3' }, composerText: '' }),
  'queue-steered': () =>
    queueScene(
      running(72_000),
      { items: [] },
      {
        composerText: '',
        steering: [{ text: STEER_NOW, taken: true }, { text: STEER_PEND }],
      },
    ),
  'queue-paused-permission': () =>
    queueScene(
      {
        runtime: { state: 'waiting-permission', capabilities },
        live: {
          turn: liveTurn({ rows: permissionRows }),
          elapsed: 31_000,
          permissions: [permission],
        },
      },
      queueOf(),
      { composerText: '' },
    ),
  'queue-paused-error': () => {
    const error = 'claude exited with code 1\nENOENT /Users/mark/.local/bin/claude';
    return queueScene(
      {
        runtime: { state: 'failed', capabilities, error },
        live: {
          turn: liveTurn({ phase: 'failed', endedAt: LIVE_START + 2000, error }),
          elapsed: 2000,
        },
      },
      queueOf({ paused: 'error' }),
      { composerText: '' },
    );
  },
  'queue-paused-interrupted': () =>
    queueScene({ runtime: { state: 'ready', capabilities } }, queueOf({ paused: 'interrupted' }), {
      composerText: '',
    }),
  'handoff-request': () => handoffScene([handoffRequest({ requestId: 'h1' }, 20_000)]),
  'handoff-after': () =>
    handoffScene([
      handoffRequest(
        {
          requestId: 'h3',
          state: 'failed',
          decision: 'user',
          decidedAt: LIVE_START + 27_000,
          error: 'no-clone',
        },
        24_000,
      ),
      handoffRequest(
        { requestId: 'h2', state: 'approved', decision: 'auto', decidedAt: LIVE_START + 12_000 },
        12_000,
      ),
      handoffRequest(
        { requestId: 'h1', state: 'approved', decision: 'user', decidedAt: LIVE_START + 8_000 },
        4_000,
      ),
    ]),
  'prompt-from-session': () => <Scene variant={running(31_000, { prompt: PROMPT_FROM })} />,
  'plan-pending': () => planScene([planPermission()]),
  'plan-long': () => planScene([planPermission(LONG_PLAN_TEXT)]),
  'plan-modes': () => planScene([planPermission()], { menu: true }),
  'plan-feedback': () =>
    planScene([planPermission()], { feedback: 'Keep the migration out of this change.' }),
  'plan-offline': () => planScene([planPermission()], undefined, false),
  'plan-approved': () => planResolved({}),
  'plan-kept': () =>
    planResolved({
      outcome: 'kept',
      mode: 'plan',
      feedback: 'Keep the migration out of this change.',
    }),
  'plan-elsewhere': () => planResolved({ outcome: 'elsewhere' }),
  'plan-fresh-menu': () => freshPlanScene({ menu: true }),
  'plan-fresh-confirm': () => freshPlanScene({ fresh: { phase: 'confirm' } }),
  'plan-fresh-starting': () => freshPlanScene({ fresh: { phase: 'starting' } }),
  'plan-fresh-failed': () =>
    freshPlanScene({
      fresh: {
        phase: 'failed',
        error:
          'MacBook Pro did not start the new session: it is offline. The plan still waits here, nothing was changed.',
      },
    }),
  'plan-fresh-done': () => (
    <Scene
      sessions={continuedIn}
      variant={{
        runtime: { state: 'running', capabilities },
        live: {
          turn: liveTurn({
            rows: permissionRows,
            plans: [planResolution({ outcome: 'elsewhere', fresh: true })],
          }),
          elapsed: 48_000,
        },
      }}
    />
  ),
  'plan-fresh-new': () => (
    <Scene
      sessions={continuedFrom}
      variant={{
        ...running(12_000),
        // A session that starts from a plan has no earlier lines: its origin line is the first thing in it.
        conversation: { entries: entries.slice(0, 2).map((e, i) => ({ ...e, lineNo: i })) },
      }}
    />
  ),
  'failed-turn': () => {
    const error = 'claude exited with code 1\nENOENT /Users/mark/.local/bin/claude';
    return (
      <Scene
        variant={{
          runtime: { state: 'failed', capabilities, error },
          live: {
            turn: liveTurn({ phase: 'failed', endedAt: LIVE_START + 2000, error }),
            elapsed: 2000,
          },
        }}
      />
    );
  },
  'failed-start': () => (
    <Scene
      variant={{
        runtime: { state: 'failed', capabilities, error: 'claude not found in PATH' },
      }}
    />
  ),
  external: () => <Scene variant={{ runtime: { state: 'external', capabilities } }} />,
  offline: () => <Scene macbookOnline={false} />,
  scrolled: () => (
    <Scene
      variant={{
        ...running(48_000),
        conversation: { unread: 3, loadingOlder: true, scrollTo: 69 },
      }}
    />
  ),
  'elided-open': () => (
    <Scene variant={{ conversation: { openBlocks: ['blk:18244:0'], scrollTo: 59 } }} />
  ),
  search: () => (
    <Scene fixture={{ paletteOpen: true, query: 'byte cursor', search: searchFixture }} />
  ),
  'sidebar-folded': () => <Scene fixture={{ sidebarFolded: true }} />,
  'sidebar-flat': () => (
    <Scene fixture={{ machineViews: { 'dev-macbook': { organize: 'flat', sort: 'activity' } } }} />
  ),
  'sidebar-menu': () => <Scene fixture={{ menuDevice: 'MacBook Pro' }} />,
  'session-usage': () => <Scene fixture={{ usage: { data: sessionUsage() } }} />,
  'session-usage-open': () => <Scene fixture={{ usage: { data: sessionUsage(), open: true } }} />,
  'session-usage-unknown': () => (
    <Scene fixture={{ usage: { data: sessionUsage(true), open: true } }} />
  ),
  'session-usage-near': () => (
    <Scene fixture={{ usage: { data: sessionUsage(), open: true, budgets: [B1, B2] } }} />
  ),
  // The open session belongs to hoplet-docs, the project whose budget is over.
  'composer-over-budget': () => (
    <Scene
      sessions={inDocs}
      variant={{ conversation: { refusal: B4, composerText: BUDGET_PROMPT } }}
    />
  ),
  'composer-over-budget-running': () => (
    <Scene
      sessions={inDocs}
      variant={{
        ...running(48_000),
        conversation: {
          queue: {
            ...queueOf({ paused: 'error', error: 'over-budget' }),
            items: queueOf().items.slice(0, 2),
          },
          budgets: [B1, B4],
          composerText: '',
        },
      }}
    />
  ),
  'session-actions': () => <Scene fixture={{ actionsMenu: {} }} />,
  'save-menu': () => <Scene fixture={{ actionsMenu: { item: 'save-template' } }} />,
  'new-menu': () => <Scene fixture={{ templates: { templates: ENTRY_TEMPLATES, menu: true } }} />,
  palette: () => (
    <Scene
      sessions={reviewing}
      fixture={{
        paletteOpen: true,
        query: 'review',
        search: { hits: [], scanning: false },
        templates: { templates: ENTRY_TEMPLATES },
      }}
    />
  ),
  'palette-fill': () => (
    <Scene fixture={{ templates: { templates: ENTRY_TEMPLATES, fill: { id: 'tpl-review' } } }} />
  ),
  'save-session': () => (
    <Scene
      fixture={{
        templates: {
          templates: ENTRY_TEMPLATES,
          save: {
            name: 'Catalog the byte cursor',
            text: 'Catalog every place the byte cursor is read or written and list the invariants each one keeps.',
          },
        },
      }}
    />
  ),
  'session-actions-copy': () => <Scene fixture={{ actionsMenu: { sub: 'copy' } }} />,
  'rename-dialog': () => (
    <Scene sessions={pinned} fixture={{ rename: 'Snapshot handoff' }} selected={handoffId} />
  ),
  pinned: () => <Scene sessions={pinned} />,
  'pinned-flat': () => (
    <Scene
      sessions={pinned}
      fixture={{ machineViews: { 'dev-macbook': { organize: 'flat', sort: 'activity' } } }}
    />
  ),
  archived: () => <Scene sessions={archivedSessions} fixture={{ archived: true }} />,
  'archived-empty': () => <Scene fixture={{ archived: true }} />,
  'session-menu-delete': () => <Scene fixture={{ actionsMenu: { item: 'delete' } }} />,
  delete: () => <Scene fixture={deleteFixture({ preview: deletionOf() })} />,
  'delete-worktree': () => (
    <Scene
      fixture={deleteFixture({
        preview: deletionOf({
          owner: studio,
          worktree: handedOff({ modified: 0, untracked: 0 }, 0),
        }),
      })}
    />
  ),
  'delete-dirty': () => (
    <Scene
      fixture={deleteFixture({
        preview: deletionOf({
          owner: studio,
          worktree: handedOff({ modified: 3, untracked: 2 }, 2),
        }),
      })}
    />
  ),
  'delete-offline': () => (
    <Scene
      fixture={deleteFixture({
        preview: deletionOf({
          owner: { ...studio, online: false },
          vendorFile: null,
          previousOwner: { deviceId: 'dev-macbook', name: 'MacBook Pro' },
        }),
      })}
    />
  ),
  'delete-running': () => (
    <Scene fixture={deleteFixture({ preview: deletionOf({ running: true }) })} />
  ),
  'delete-progress': () => (
    <Scene
      fixture={deleteFixture({
        preview: deleteWorktree,
        phase: 'progress',
        rows: [
          { step: 'stop-syncing', state: 'ok' },
          { step: 'vendor-file', state: 'ok' },
          { step: 'worktree-branch', state: 'run' },
          { step: 'core-copy', state: 'wait' },
        ],
      })}
    />
  ),
  'delete-partial': () => (
    <Scene
      fixture={deleteFixture({
        preview: deleteWorktree,
        phase: 'partial',
        rows: [
          { step: 'stop-syncing', state: 'ok' },
          { step: 'vendor-file', state: 'ok' },
          { step: 'worktree', state: 'bad', reason: 'dirty' },
          { step: 'core-copy', state: 'skip' },
        ],
      })}
    />
  ),
  'copy-toast': () => <Scene fixture={{ toast: { text: 'Session ID copied' } }} />,
  disconnected: () => <Scene connected={false} variant={running(48_000)} />,
};
