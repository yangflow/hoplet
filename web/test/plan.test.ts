import { PROTOCOL_VERSION } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  addPermission,
  expirePermissions,
  type LiveTurn,
  liveItems,
  type OpenPermission,
  PERMISSION_OPEN_MS,
  planBytes,
  planModes,
  planSteps,
  planTitle,
  resolvePlan,
  withPlans,
} from '../src/screens/cockpit/conversation/live-turn.ts';

const PLAN =
  '## Roll back the cursor\n\n1. Wrap it\n2. Restore it\n```\n1. not a step\n```\n3. Test it\n';
const exit = {
  plan: PLAN,
  option: 'mode',
  modes: ['acceptEdits', 'default', 'auto'],
  default: 'acceptEdits',
};
const request: OpenPermission = {
  requestId: 'r1',
  tool: 'ExitPlanMode',
  toolKind: 'other',
  input: { plan: PLAN },
  receivedAt: 0,
  planExit: exit,
};
const turn = (over: Partial<LiveTurn> = {}): LiveTurn => ({
  id: 't',
  prompt: 'p',
  startedAt: 0,
  boundary: 0,
  joined: false,
  reachedRunning: true,
  rows: [
    { id: 0, kind: 'text', text: 'a' },
    { id: 1, kind: 'text', text: 'b' },
  ] as LiveTurn['rows'],
  phase: 'live',
  ...over,
});

describe('plan helpers', () => {
  it('takes the title from the first heading, else the first line', () => {
    expect(planTitle(PLAN)).toBe('Roll back the cursor');
    expect(planTitle('\n**Do** `it`\nmore')).toBe('Do it');
  });

  it('counts numbered steps outside code fences', () => {
    expect(planSteps(PLAN)).toBe(3);
    expect(planSteps('no steps')).toBe(0);
  });

  it('measures bytes, not characters', () => {
    expect(planBytes('é')).toBe(2);
  });

  it('orders the offered modes with the default first and drops unknown ones', () => {
    const option = { options: [{ value: 'default' }, { value: 'auto' }, { value: 'acceptEdits' }] };
    expect(planModes({ ...exit, default: 'default' }, option)).toEqual([
      'default',
      'acceptEdits',
      'auto',
    ]);
    expect(planModes(exit, { options: [{ value: 'auto' }] })).toEqual(['auto']);
    expect(planModes(exit, undefined)).toEqual(['acceptEdits', 'default', 'auto']);
  });
});

describe('plan requests', () => {
  it('carries planExit from the wire message and never expires', () => {
    const list = addPermission(
      [],
      {
        v: PROTOCOL_VERSION,
        type: 'session.permission',
        sessionId: 's',
        requestId: 'r1',
        tool: 'ExitPlanMode',
        input: {},
        planExit: exit,
      } as never,
      0,
    );
    expect(list[0]?.planExit).toEqual(exit);
    expect(expirePermissions(list, PERMISSION_OPEN_MS * 10)).toBe(list);
    const plain = [{ ...request, planExit: undefined }];
    expect(expirePermissions(plain, PERMISSION_OPEN_MS * 10)).toEqual([]);
  });

  it('resolves with the intent of this window, else as answered elsewhere', () => {
    expect(resolvePlan(request, { outcome: 'approved', mode: 'auto' }, 5)).toMatchObject({
      outcome: 'approved',
      mode: 'auto',
      steps: 3,
      title: 'Roll back the cursor',
    });
    expect(resolvePlan(request, undefined, 5)?.outcome).toBe('elsewhere');
    expect(resolvePlan({ ...request, planExit: undefined }, undefined, 5)).toBeNull();
  });

  it('places a resolution after the last row and does not repeat it', () => {
    const r = resolvePlan(request, { outcome: 'kept', feedback: 'no' }, 5);
    if (!r) throw new Error('no resolution');
    const once = withPlans(turn(), [r]);
    expect(once.plans?.[0]?.afterId).toBe(1);
    expect(withPlans(once, [r])).toBe(once);
    expect(
      liveItems(once).map((x) => (x.plan ? `plan:${x.plan.requestId}` : `row:${x.row.id}`)),
    ).toEqual(['row:0', 'row:1', 'plan:r1']);
  });
});
