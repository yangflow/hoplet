import type {
  Annotation,
  HandoffRule,
  HandoffRulesPreview,
  SnapshotChanges,
  WorkspaceChange,
  WorkspaceDiff,
  WorkspaceFile,
  WorkspaceListing,
} from '@hoplet/shared';
import type { WorkspaceData } from './use-workspace.ts';

// Fixtures for /__states/workspace/<state>, built for the design's states (design/screens/workspace.html), not
// copied from a session. Nothing here is read by the real screen.

export const SESSION = '11111111-2222-4333-8444-555555555555';
const COMMIT = '56d5a0e3b9c14f7a8d2e6b0c1f4a9e7d3c5b8a21';
export const ORIGIN = 'git@github.com:yangflow/hoplet.git';

export const CHANGES: WorkspaceChange[] = [
  { path: 'core/src/store/chunks.ts', status: 'modified', added: 9, removed: 3 },
  { path: 'docs/hosting.md', status: 'modified', added: 9214, removed: 1 },
  { path: 'notes.txt', status: 'untracked', added: 5, removed: 0 },
  { path: 'design/icon.png', status: 'modified', binary: true },
];

const PATHS = [
  'core/src/index.ts',
  'core/src/store/chunks.ts',
  'design/hoplet.sketch',
  'design/icon.png',
  'docs/hosting.md',
  'web/index.html',
  'web/src/main.tsx',
  'package.json',
  'README.md',
  'notes.txt',
];

export function listing(over: Partial<WorkspaceListing> = {}): WorkspaceListing {
  return {
    sessionId: SESSION,
    git: true,
    commit: COMMIT,
    origin: ORIGIN,
    paths: PATHS,
    truncated: false,
    changes: CHANGES,
    ...over,
  };
}

export const live = (over: Partial<WorkspaceListing> = {}): WorkspaceData => ({
  phase: 'live',
  listing: listing(over),
});

/** A working tree past the listing limit: 400 paths, most of them under web/. */
export function truncatedListing(): WorkspaceData {
  const web = Array.from({ length: 400 - PATHS.length }, (_, i) => `web/src/gen/part-${i}.ts`);
  return live({ paths: [...PATHS, ...web].slice(0, 400), truncated: true });
}

/** The last snapshot, taken at 14:02 local time. */
export const snapshot: SnapshotChanges = {
  sessionId: SESSION,
  snapshotAt: new Date(2026, 9, 8, 14, 2).getTime(),
  baseCommit: COMMIT,
  origin: ORIGIN,
  byteSize: 1_258_291,
  changes: CHANGES,
};

/** core/src/store/chunks.ts as it is now: the index row is committed before the rename. */
export const CHUNKS_NOW = [
  'import { open, rename } from "node:fs/promises";',
  'import { join } from "node:path";',
  'import { commitIndex } from "./index-rows.ts";',
  'import type { ChunkRow, Store } from "./types.ts";',
  '',
  'const GZ = ".ndjson.gz";',
  'const MAX_CHUNK = 8_388_608;',
  '',
  '// A chunk holds session bytes verbatim. It is appended',
  '// as .tmp and renamed to .gz once its index row is',
  '// committed, so byte_size never drifts from the bytes.',
  '',
  'export async function readChunk(',
  '  store: Store,',
  '  row: ChunkRow,',
  '): Promise<Uint8Array> {',
  '  const fh = await open(join(store.dir, row.id + GZ));',
  '  try {',
  '    return await fh.readFile();',
  '  } finally {',
  '    await fh.close();',
  '  }',
  '}',
  '',
  'export function chunkPath(store: Store, row: ChunkRow): string {',
  '  return join(store.dir, row.id + GZ);',
  '}',
  '',
  'export const isFull = (row: ChunkRow) => row.byteSize >= MAX_CHUNK;',
  '',
  'export async function appendChunk(',
  '  store: Store,',
  '  row: ChunkRow,',
  '  bytes: Uint8Array,',
  '): Promise<number> {',
  '  const at = row.byteSize;',
  '  // The byte size is the cursor: append the bytes,',
  '  // commit the index row, only then rename to .gz.',
  '  const fh = await open(row.tmp, "a");',
  '  await fh.write(bytes);',
  '  await fh.close();',
  '  await commitIndex(store, row, at + bytes.length);',
  '  await rename(row.tmp, join(store.dir, row.id + GZ));',
  '  // A crash before the rename leaves the .tmp.',
  '  return at + bytes.length;',
  '}',
].join('\n');

const CHUNKS_DIFF = [
  '@@ -1,5 +1,6 @@',
  ' import { open, rename } from "node:fs/promises";',
  ' import { join } from "node:path";',
  '+import { commitIndex } from "./index-rows.ts";',
  ' import type { ChunkRow, Store } from "./types.ts";',
  ' ',
  ' const GZ = ".ndjson.gz";',
  '@@ -30,11 +31,16 @@ appendChunk',
  ' export async function appendChunk(',
  '   store: Store,',
  '   row: ChunkRow,',
  '   bytes: Uint8Array,',
  ' ): Promise<number> {',
  '   const at = row.byteSize;',
  '-  await rename(row.tmp, join(store.dir, row.id + GZ));',
  '-  await store.write(row.id, bytes);',
  '-  await store.commit(row);',
  '+  // The byte size is the cursor: append the bytes,',
  '+  // commit the index row, only then rename to .gz.',
  '+  const fh = await open(row.tmp, "a");',
  '+  await fh.write(bytes);',
  '+  await fh.close();',
  '+  await commitIndex(store, row, at + bytes.length);',
  '+  await rename(row.tmp, join(store.dir, row.id + GZ));',
  '+  // A crash before the rename leaves the .tmp.',
  '   return at + bytes.length;',
  ' }',
  '',
].join('\n');

const HOSTING_DIFF = [
  '@@ -88,8 +88,11 @@ Resources',
  ' ## Resources',
  ' ',
  ' | Resource | Default | Limit |',
  ' | --- | --- | --- |',
  ' | Session store | none | the disk |',
  '-| Snapshot | 64 MiB | per session |',
  '+| Snapshot | 64 MiB | per turn |',
  '+| Snapshot store | 2 GiB | per device |',
  '+| Bundle | 256 MiB | per handoff |',
  '+',
  ' Snapshots older than the last handoff are pruned when the next snapshot of the same session is stored; `hoplet prune --dry-run` lists what would go.',
  ' ',
  '',
].join('\n');

export const chunksDiff: WorkspaceDiff = {
  kind: 'text',
  sessionId: SESSION,
  path: 'core/src/store/chunks.ts',
  status: 'modified',
  diff: CHUNKS_DIFF,
  truncated: false,
  size: CHUNKS_DIFF.length,
};

export const hostingDiff: WorkspaceDiff = {
  kind: 'text',
  sessionId: SESSION,
  path: 'docs/hosting.md',
  status: 'modified',
  diff: HOSTING_DIFF,
  truncated: true,
  size: 1_468_006,
};

const NOTES = [
  'Byte cursor checks',
  '- tail page of the 23 MB fixture: about a second',
  '- 256 MB fixture next',
  '- restart resumes at the index row',
  '- handoff keeps the cursor',
];

export const notesDiff: WorkspaceDiff = {
  kind: 'text',
  sessionId: SESSION,
  path: 'notes.txt',
  status: 'untracked',
  diff: `@@ -0,0 +1,5 @@\n${NOTES.map((l) => `+${l}`).join('\n')}\n`,
  truncated: false,
  size: 140,
};

export const notesFile: WorkspaceFile = {
  kind: 'text',
  sessionId: SESSION,
  path: 'notes.txt',
  size: 140,
  text: `${NOTES.join('\n')}\n`,
  truncated: false,
};

export const iconDiff: WorkspaceDiff = {
  kind: 'binary',
  sessionId: SESSION,
  path: 'design/icon.png',
  status: 'modified',
  oldSize: 18_842,
  newSize: 21_504,
};

export const chunksFile: WorkspaceFile = {
  kind: 'text',
  sessionId: SESSION,
  path: 'core/src/store/chunks.ts',
  size: 2150,
  text: `${CHUNKS_NOW}\n`,
  truncated: false,
};

/** The app icon drawn as it is now, and as HEAD has it: a 512 px SVG standing in for the PNG. */
function iconArt(now: boolean): string {
  const svg = now
    ? '<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 64 64"><rect x="4" y="4" width="56" height="56" rx="13" fill="#ff7a1a"/><path d="M21 43L43 21" stroke="#1c0d02" stroke-width="6" stroke-linecap="round"/><circle cx="43" cy="21" r="3.6" fill="#fff4e6"/></svg>'
    : '<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 64 64"><rect x="4" y="4" width="56" height="56" rx="10" fill="#d9620d"/><path d="M21 43L43 21" stroke="#1c0d02" stroke-width="4.5" stroke-linecap="round"/></svg>';
  return btoa(svg);
}

export const iconNow: WorkspaceFile = {
  kind: 'image',
  sessionId: SESSION,
  path: 'design/icon.png',
  size: 21_504,
  mediaType: 'image/svg+xml',
  data: iconArt(true),
};

export const iconHead: WorkspaceFile = { ...iconNow, size: 18_842, data: iconArt(false) };

export const sketchFile: WorkspaceFile = {
  kind: 'binary',
  sessionId: SESSION,
  path: 'design/hoplet.sketch',
  size: 1_887_437,
};

/** The catalog session's project rules on MacBook Pro against studio's clone (design/screens/handoff-rules.html). */
const STUDIO_CLONE = '/Users/mark/github/hoplet';
const RULE_TERRAFORM: Omit<HandoffRule, 'state'> = {
  action: 'deny',
  tool: 'run',
  match: { command: 'terraform apply' },
  note: '',
};
const RULE_PUBLISH: Omit<HandoffRule, 'state'> = {
  action: 'ask',
  tool: 'run',
  match: { command: 'pnpm publish' },
  note: '',
};
const RULE_MIGRATIONS: Omit<HandoffRule, 'state'> = {
  action: 'deny',
  tool: 'write',
  match: { paths: [`${STUDIO_CLONE}/core/src/migrations/**`] },
  note: '',
};
const RULE_TEST: Omit<HandoffRule, 'state'> = {
  action: 'allow',
  tool: 'run',
  match: { command: 'pnpm test' },
  note: '',
};
const RULE_NPM: Omit<HandoffRule, 'state'> = {
  action: 'allow',
  tool: 'fetch',
  match: { domains: ['registry.npmjs.org'] },
  note: '',
};

/** studio has one of the four and one of its own (`differ`), none of them (`none`) or all four (`same`). */
export function handoffRules(kind: 'differ' | 'none' | 'same'): HandoffRulesPreview {
  const four = [RULE_TERRAFORM, RULE_PUBLISH, RULE_MIGRATIONS, RULE_TEST];
  const rules: HandoffRule[] =
    kind === 'same'
      ? four.map((r) => ({ ...r, state: 'same' }))
      : kind === 'none'
        ? four.map((r) => ({ ...r, state: 'copy' }))
        : [
            ...four.map(
              (r): HandoffRule => ({ ...r, state: r === RULE_PUBLISH ? 'same' : 'copy' }),
            ),
            { ...RULE_NPM, state: 'target' },
          ];
  return {
    sourceDeviceId: 'dev-macbook',
    sourcePath: '/Users/mark/github/repopro/hoplet',
    targetDeviceId: 'dev-studio',
    targetPath: STUDIO_CLONE,
    rules,
  };
}

// Review comments on the diff (#84, design/screens/diff-annotations.html). Line numbers are the working tree's
// lines of chunksDiff and hostingDiff.
const AT = Date.UTC(2026, 9, 21, 9, 30);
const comment = (
  id: number,
  path: string,
  startLine: number,
  endLine: number,
  snippet: string,
  body: string,
  state: Annotation['state'] = 'pending',
): Annotation => ({
  id: `aaaaaaaa-0000-4000-8000-${String(id).padStart(12, '0')}`,
  path,
  side: 'new',
  startLine,
  endLine,
  snippet,
  body,
  state,
  createdAt: AT + id * 60_000,
  updatedAt: AT + id * 60_000,
  ...(state === 'pending'
    ? {}
    : { sent: { review: 2, at: AT + 600_000, delivery: 'prompt', messageId: 'msg-review-2' } }),
});
const OPEN_THREE = [
  '  const fh = await open(row.tmp, "a");',
  '  await fh.write(bytes);',
  '  await fh.close();',
].join('\n');
const COMMIT_LINE = '  await commitIndex(store, row, at + bytes.length);';
const C_OPEN =
  'Open the .tmp once per session and keep the handle; opening it for every chunk costs a syscall per append.';
const C_COMMIT =
  'If the commit throws, the bytes are already in the .tmp but byte_size is not: say what the retry does.';
const C_HOSTING = 'Per turn or per session? The spec still says per session.';

/** Two pending comments in chunks.ts and one in hosting.md. */
export const pendingComments: Annotation[] = [
  comment(1, 'core/src/store/chunks.ts', 39, 41, OPEN_THREE, C_OPEN),
  comment(2, 'core/src/store/chunks.ts', 42, 42, COMMIT_LINE, C_COMMIT),
  comment(3, 'docs/hosting.md', 93, 93, '| Snapshot | 64 MiB | per turn |', C_HOSTING),
];

/** Sent comments: one on lines that still read as sent, one on lines that have changed since, one resolved. */
export const sentComments: Annotation[] = [
  comment(1, 'core/src/store/chunks.ts', 39, 41, OPEN_THREE, C_OPEN, 'resolved'),
  comment(2, 'core/src/store/chunks.ts', 42, 42, COMMIT_LINE, C_COMMIT, 'sent'),
  comment(
    4,
    'core/src/store/chunks.ts',
    44,
    44,
    '  store.setByteSize(row.id, at + bytes.length);',
    'Set the byte size in the same transaction as the index row.',
    'sent',
  ),
];

/** The review message as the core composes it, for the send dialog's preview. */
export function reviewText(
  list: readonly Annotation[],
  ids: readonly string[],
  note: string,
): string {
  const picked = list.filter((a) => ids.includes(a.id));
  const items = picked.map((a, i) => {
    const lines = a.startLine === a.endLine ? `${a.startLine}` : `${a.startLine}-${a.endLine}`;
    const lang = a.path.endsWith('.ts') ? 'ts' : a.path.endsWith('.md') ? 'md' : '';
    return `${i + 1}. ${a.path}:${lines}\n\`\`\`${lang}\n${a.snippet}\n\`\`\`\n${a.body}`;
  });
  const heading = `Review comments (${picked.length}) on the working tree.`;
  return [heading, ...(note ? [note] : []), ...items].join('\n\n');
}
