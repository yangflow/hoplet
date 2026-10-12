import type {
  DeviceInfo,
  Entry,
  LiveSessionMeta,
  RuntimeCapabilities,
  SessionRuntimeInfo,
} from '@hoplet/shared';
import { type ReactNode, useState } from 'react';
import { live as liveWorkspace, snapshot } from '../workspace/fixtures.ts';
import type { AddProjectFixture } from './add-project.tsx';
import { rememberBlock } from './blocks.tsx';
import { dirKey } from './folded-dirs.ts';
import { Cockpit, type CockpitFixture } from './index.tsx';
import type { LiveTurn, OpenPermission } from './live-turn.ts';
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
    ownerConnected: true,
    createdAt: NOW - 7_200_000,
    updatedAt: NOW - MINUTE,
    lineCount: 18_302,
    byteSize: 12_000,
    ownerEpoch: 0,
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

const capabilities: RuntimeCapabilities = {
  permissions: true,
  interrupt: true,
  thinking: true,
  fast: true,
  models: ['opus-5.5', 'sonnet-5.5', 'haiku-5'],
};

function fixtureDevices(macbookOnline: boolean): DeviceInfo[] {
  const providers = [{ provider: 'claude' as const, capabilities }];
  return [
    {
      deviceId: 'dev-macbook',
      name: 'MacBook Pro',
      connected: macbookOnline,
      providers: macbookOnline ? providers : [],
    },
    { deviceId: 'dev-studio', name: 'studio', connected: true, providers },
    { deviceId: 'dev-office', name: 'office-mini', connected: false, providers: [] },
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

export type Variant = {
  runtime?: SessionRuntimeInfo;
  live?: { turn: LiveTurn; elapsed: number; permissions?: OpenPermission[] };
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
}) {
  const macbookOnline = props.macbookOnline ?? true;
  const sessions = fixtureSessions(macbookOnline);
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
      fixture={{
        conversation:
          selected === catalogId
            ? {
                entries,
                now: LIVE_START + (live?.elapsed ?? 0),
                ...(live ? { live: { turn: live.turn, permissions: live.permissions ?? [] } } : {}),
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

/** Every state the design shows, rendered at /__states/cockpit/<state> in development builds. */
export const states: Record<string, () => ReactNode> = {
  empty: () => <Scene selected={null} fixture={{ hoverDir: 'notes' }} />,
  loaded: () => <Scene />,
  'new-session': () => <Scene fixture={{ newSessionOpen: true }} />,
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
  disconnected: () => <Scene connected={false} variant={running(48_000)} />,
};
