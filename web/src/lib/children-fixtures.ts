// Fixtures of the child session views for /__states and the tests: the tree of design/screens/child-sessions.html.
// Nothing here is read by the real screens.
import type { ChildCollect } from '@hoplet/shared';
import type { ChildAsk, Kid, KidsData } from './children.ts';

export const KID_MIGRATION = 'c1000000-0000-4000-8000-000000000001';
export const KID_TESTS = 'c2000000-0000-4000-8000-000000000002';
export const KID_GPU = 'c2a00000-0000-4000-8000-00000000002a';
export const KID_DOCS = 'c3000000-0000-4000-8000-000000000003';
export const KID_NOTES = 'c4000000-0000-4000-8000-000000000004';
export const KID_FIXTURES = 'c5000000-0000-4000-8000-000000000005';

const kid = (over: Partial<Kid> & Pick<Kid, 'id' | 'title'>): Kid => ({
  parentId: null,
  depth: 1,
  machine: 'MacBook Pro',
  branch: null,
  state: 'running',
  elapsedSeconds: null,
  childCount: 0,
  ...over,
});

/** The five children of the parent and the grandchild one of them started; `parentId` is the viewed session's. */
export function kidsTree(parentId: string, over: { docs?: Partial<Kid> } = {}): KidsData {
  return {
    truncated: false,
    kids: [
      kid({
        id: KID_MIGRATION,
        parentId,
        title: 'Write migration 0042',
        branch: 'hoplet/3fa91c0d',
        elapsedSeconds: 252,
      }),
      kid({
        id: KID_TESTS,
        parentId,
        title: 'Port the store tests',
        machine: 'studio',
        branch: 'hoplet/a7c2e5b1',
        elapsedSeconds: 500,
        childCount: 1,
      }),
      kid({
        id: KID_GPU,
        parentId: KID_TESTS,
        depth: 2,
        title: 'Run the GPU suite',
        machine: 'studio',
        branch: 'hoplet/55d0be9a',
      }),
      kid({
        id: KID_DOCS,
        parentId,
        title: 'Update docs/api.md',
        branch: 'hoplet/9be1d4f2',
        elapsedSeconds: 363,
        ...over.docs,
      }),
      kid({
        id: KID_NOTES,
        parentId,
        title: 'Draft the upgrade notes',
        branch: 'hoplet/c61d08e4',
        state: 'timed-out',
        elapsedSeconds: 1800,
      }),
      kid({
        id: KID_FIXTURES,
        parentId,
        title: 'Add the offset fixtures',
        branch: 'hoplet/e4079a3b',
        state: 'done',
        elapsedSeconds: 468,
      }),
    ],
  };
}

/** The docs child blocked on a shell command. */
export const blockedDocs = { docs: { state: 'blocked' as const } };

/** What the docs child waits on: `pnpm schema`. */
export const DOCS_ASK: ChildAsk = {
  requestId: 'r-docs-1',
  tool: 'Bash',
  toolKind: 'run',
  input: { command: 'pnpm schema' },
  plan: false,
};

/** What collecting the done child answered: its report, its files and its cost. */
export const FIXTURES_REPORT: ChildCollect = {
  sessionId: KID_FIXTURES,
  state: 'done',
  summary:
    'Added fixtures for offsets below, at and above 2^32, and removed the 32-bit-only one. The store tests read them and pass.',
  summaryTruncated: false,
  changes: {
    status: 'ok',
    branch: 'hoplet/e4079a3b',
    base: 'main',
    total: 4,
    files: [
      { path: 'core/test/fixtures/offsets-64.ndjson', status: 'added', added: 412, removed: 0 },
      {
        path: 'core/test/fixtures/offsets-boundary.ndjson',
        status: 'added',
        added: 96,
        removed: 0,
      },
      { path: 'core/src/store/cursor.test.ts', status: 'modified', added: 38, removed: 6 },
      { path: 'core/test/fixtures/offsets-32.ndjson', status: 'deleted', added: 0, removed: 54 },
    ],
  },
  usage: {
    estimated: true,
    input: 21_400,
    output: 9_800,
    cacheWrite: 4_100,
    cacheRead: 35_700,
    messages: 41,
    estimatedCostUsd: 0.41,
    costComplete: true,
  },
  released: false,
};
