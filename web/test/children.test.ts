import type { ChildStatus, SessionDescendant } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  addAsk,
  askLine,
  buildKids,
  cancelAllRoots,
  cancelPlan,
  dropAsk,
  liveKids,
  pruneAsks,
  rollTags,
  rollup,
  stateOfKid,
  usedTokens,
} from '../src/lib/children.ts';
import {
  blockedDocs,
  KID_DOCS,
  KID_FIXTURES,
  KID_GPU,
  KID_MIGRATION,
  KID_NOTES,
  KID_TESTS,
  kidsTree,
} from '../src/lib/children-fixtures.ts';

const PARENT = 'p0000000-0000-4000-8000-000000000000';

const descendant = (over: Partial<SessionDescendant>): SessionDescendant =>
  ({
    sessionId: KID_MIGRATION,
    parentId: PARENT,
    depth: 1,
    title: 'Write migration 0042',
    deviceName: 'MacBook Pro',
    gitBranch: 'hoplet/3fa91c0d',
    childCount: 0,
    runtime: { state: 'running' },
    ...over,
  }) as SessionDescendant;

describe('stateOfKid', () => {
  it('takes the state the core states for a direct child', () => {
    const status: ChildStatus = { sessionId: KID_NOTES, state: 'timed-out', elapsedSeconds: 1800 };
    expect(stateOfKid('running', status)).toBe('timed-out');
  });

  it('reads a deeper session from its runtime state', () => {
    expect(stateOfKid('starting')).toBe('running');
    expect(stateOfKid('running')).toBe('running');
    expect(stateOfKid('waiting-permission')).toBe('blocked');
    expect(stateOfKid('failed')).toBe('failed');
    expect(stateOfKid('offline')).toBe('ended');
    expect(stateOfKid('idle')).toBe('done');
  });
});

describe('buildKids', () => {
  it('keeps the pre-order and joins the statuses of the direct children', () => {
    const data = buildKids(
      {
        truncated: true,
        descendants: [
          descendant({}),
          descendant({
            sessionId: KID_GPU,
            parentId: KID_MIGRATION,
            depth: 2,
            title: '',
            titleOverride: '  Run the GPU suite ',
            gitBranch: undefined,
            runtime: { state: 'waiting-permission' } as SessionDescendant['runtime'],
          }),
        ],
      },
      [{ sessionId: KID_MIGRATION, state: 'running', elapsedSeconds: 252 }],
    );
    expect(data.truncated).toBe(true);
    expect(data.kids.map((k) => [k.id, k.depth, k.state, k.elapsedSeconds])).toEqual([
      [KID_MIGRATION, 1, 'running', 252],
      [KID_GPU, 2, 'blocked', null],
    ]);
    expect(data.kids[1]?.title).toBe('Run the GPU suite');
    expect(data.kids[1]?.branch).toBeNull();
    expect(data.kids[0]?.machine).toBe('MacBook Pro');
  });
});

describe('rollTags', () => {
  it('names what needs you first, then what runs, then how the rest ended, leaving out zeros', () => {
    const { kids } = kidsTree(PARENT, blockedDocs);
    expect(rollTags(rollup(kids)).map((t) => t.text)).toEqual([
      '1 needs you',
      '3 running',
      '1 done',
      '1 timed out',
    ]);
  });
});

describe('cancelling', () => {
  const { kids } = kidsTree(PARENT);

  it('cancels a child without children on its own', () => {
    const plan = cancelPlan(kids, KID_MIGRATION);
    expect(plan.targets.map((k) => k.id)).toEqual([KID_MIGRATION]);
    expect(plan.subtree).toBe(false);
  });

  it('cancels a child with its live subtree', () => {
    const plan = cancelPlan(kids, KID_TESTS);
    expect(plan.targets.map((k) => k.id)).toEqual([KID_TESTS, KID_GPU]);
    expect(plan.subtree).toBe(true);
  });

  it('reaches a live grandchild below a child that is no longer live', () => {
    const tree = kids.map((k) => (k.id === KID_TESTS ? { ...k, state: 'done' as const } : k));
    expect(cancelPlan(tree, KID_TESTS).targets.map((k) => k.id)).toEqual([KID_TESTS, KID_GPU]);
    expect(cancelAllRoots(tree, PARENT).map((k) => k.id)).toContain(KID_TESTS);
  });

  it('knows nothing of an unknown id', () => {
    expect(cancelPlan(kids, 'nope').targets).toEqual([]);
  });

  it('cancels all through the direct children that have something live', () => {
    expect(cancelAllRoots(kids, PARENT).map((k) => k.id)).toEqual([
      KID_MIGRATION,
      KID_TESTS,
      KID_DOCS,
    ]);
    expect(liveKids(kids).map((k) => k.id)).toEqual([KID_MIGRATION, KID_TESTS, KID_GPU, KID_DOCS]);
    expect(cancelAllRoots(kids, PARENT).map((k) => k.id)).not.toContain(KID_FIXTURES);
  });
});

describe('usedTokens', () => {
  it('adds what was read and what was written', () => {
    expect(usedTokens({ input: 1, output: 2, cacheWrite: 3, cacheRead: 4 })).toBe(10);
  });
});

describe('asks', () => {
  const ask = {
    sessionId: KID_DOCS,
    requestId: 'r1',
    tool: 'Bash',
    toolKind: 'run' as const,
    input: { command: 'pnpm schema\necho done' },
  };

  it('shows the first line of a command, else the tool', () => {
    expect(askLine(ask)).toEqual({ text: 'pnpm schema', command: true });
    expect(askLine({ ...ask, toolKind: 'edit', input: { path: 'a' } })).toEqual({
      text: 'Bash',
      command: false,
    });
    expect(askLine({ ...ask, input: { command: '  ' } }).command).toBe(false);
    const long = askLine({ ...ask, input: { command: 'x'.repeat(300) } });
    expect(long.text).toHaveLength(120);
    expect(long.text.endsWith('…')).toBe(true);
  });

  it('adds a request once, however often the core replays it', () => {
    const once = addAsk({}, ask);
    expect(addAsk(once, ask)).toBe(once);
    expect(once[KID_DOCS]?.[0]).toMatchObject({ requestId: 'r1', plan: false });
    const plan = addAsk(once, { ...ask, requestId: 'r2', planExit: {} as never });
    expect(plan[KID_DOCS]?.map((a) => a.plan)).toEqual([false, true]);
  });

  it('keeps the requests of the blocked children only', () => {
    const asks = addAsk(addAsk({}, ask), { ...ask, sessionId: KID_GPU });
    expect(pruneAsks(asks, new Set([KID_DOCS, KID_GPU]))).toBe(asks);
    expect(Object.keys(pruneAsks(asks, new Set([KID_GPU])))).toEqual([KID_GPU]);
  });

  it('drops an answered request, and the child when it was the last', () => {
    const asks = addAsk(addAsk({}, ask), { ...ask, requestId: 'r2' });
    expect(dropAsk(asks, KID_DOCS, 'nope')).toBe(asks);
    expect(dropAsk(asks, KID_DOCS, 'r1')[KID_DOCS]?.map((a) => a.requestId)).toEqual(['r2']);
    expect(dropAsk(dropAsk(asks, KID_DOCS, 'r1'), KID_DOCS, 'r2')).toEqual({});
  });
});
