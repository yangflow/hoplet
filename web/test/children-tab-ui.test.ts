// @vitest-environment jsdom
import type { LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({
  postChildCollect: vi.fn(),
  postChildCancel: vi.fn(),
  postPermission: vi.fn(),
}));
vi.mock('../src/lib/api.ts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/lib/api.ts')>()),
  ...api,
}));

import {
  blockedDocs,
  DOCS_ASK,
  FIXTURES_REPORT,
  KID_DOCS,
  KID_FIXTURES,
  KID_GPU,
  KID_MIGRATION,
  KID_TESTS,
  kidsTree,
} from '../src/lib/children-fixtures.ts';
import { ApiError } from '../src/lib/errors.ts';
import type { ChildrenView } from '../src/lib/use-children.ts';
import { ChildAsks } from '../src/screens/cockpit/conversation/child-asks.tsx';
import { ChildrenTab } from '../src/screens/workspace/children-tab.tsx';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const PARENT = '8f1c2d3e-4a5b-4c6d-8e7f-90a1b2c3d4e5';
const session = {
  sessionId: PARENT,
  parentId: null,
  title: 'Move the cursor to 64-bit offsets',
  childCount: 5,
} as unknown as LiveSessionMeta;

let root: Root;
let host: HTMLDivElement;
let shown = 0;

const q = (id: string) => document.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const row = (id: string) => q(`workspace.children.row.${id}`);
const inRow = (id: string, test: string) =>
  row(id)?.querySelector<HTMLElement>(`[data-testid="${test}"]`) ?? null;
const click = async (el: Element | null | undefined) => {
  expect(el).toBeTruthy();
  await act(async () => {
    (el as HTMLElement).click();
  });
};

function viewOf(over: Partial<ChildrenView> = {}): ChildrenView {
  return {
    phase: 'live',
    data: kidsTree(PARENT),
    error: null,
    asks: {},
    refresh: vi.fn(),
    answered: vi.fn(),
    ...over,
  };
}

const showTab = async (view: ChildrenView, onOpenSession = vi.fn()) => {
  await act(async () => {
    root.render(createElement(ChildrenTab, { session, view, onOpenSession, key: ++shown }));
  });
  return onOpenSession;
};

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  for (const fn of Object.values(api)) fn.mockReset();
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe('Inspector > Children', () => {
  it('lists the tree with a roll-up, the indent of a grandchild and the live count of the tree', async () => {
    await showTab(viewOf());
    const rows = document.querySelectorAll('[data-testid^="workspace.children.row."]');
    expect(rows).toHaveLength(6);
    expect(row(KID_GPU)?.dataset.depth).toBe('2');
    expect(q('workspace.children.roll')?.textContent).toContain('4 running');
    expect(q('workspace.children.live')?.textContent).toBe('4 live of 8 in this tree');
    // a done child has nothing to cancel
    expect(inRow(KID_FIXTURES, 'workspace.children.cancel')).toBeNull();
    expect(inRow(KID_MIGRATION, 'workspace.children.cancel')).not.toBeNull();
  });

  it('says so when the tree is empty, loading or could not be read', async () => {
    await showTab(viewOf({ data: { kids: [], truncated: false } }));
    expect(q('workspace.children.empty')).not.toBeNull();
    expect(q('workspace.children.cancel-all')).toBeNull();
    await showTab(viewOf({ phase: 'loading', data: { kids: [], truncated: false } }));
    expect(q('workspace.children.loading')).not.toBeNull();
    const view = viewOf({
      phase: 'error',
      error: 'Could not reach the core.',
      data: { kids: [], truncated: false },
    });
    await showTab(view);
    expect(q('workspace.children.error')?.textContent).toContain('Could not reach the core.');
    await click(q('workspace.children.retry'));
    expect(view.refresh).toHaveBeenCalled();
  });

  it('shows the request of a blocked child and opens it to answer', async () => {
    const open = await showTab(
      viewOf({ data: kidsTree(PARENT, blockedDocs), asks: { [KID_DOCS]: [DOCS_ASK] } }),
    );
    expect(inRow(KID_DOCS, 'workspace.children.ask')?.textContent).toContain('pnpm schema');
    await click(inRow(KID_DOCS, 'workspace.children.answer'));
    expect(open).toHaveBeenCalledWith(KID_DOCS);
  });

  it('opens a row in place and collects the report through the child’s own parent', async () => {
    api.postChildCollect.mockResolvedValue(FIXTURES_REPORT);
    await showTab(viewOf());
    await click(inRow(KID_FIXTURES, 'workspace.children.toggle'));
    expect(api.postChildCollect).toHaveBeenCalledWith(PARENT, KID_FIXTURES);
    expect(inRow(KID_FIXTURES, 'workspace.children.summary')?.textContent).toContain('2^32');
    expect(inRow(KID_FIXTURES, 'workspace.children.tokens')?.textContent).toContain('tokens');
    expect(inRow(KID_FIXTURES, 'workspace.children.files')?.querySelectorAll('li')).toHaveLength(4);
    expect(inRow(KID_FIXTURES, 'workspace.children.toggle')?.getAttribute('aria-expanded')).toBe(
      'true',
    );
    await click(inRow(KID_FIXTURES, 'workspace.children.toggle'));
    expect(inRow(KID_FIXTURES, 'workspace.children.detail')).toBeNull();
    // opened again, the report is not asked for twice
    await click(inRow(KID_FIXTURES, 'workspace.children.toggle'));
    expect(api.postChildCollect).toHaveBeenCalledTimes(1);

    api.postChildCollect.mockResolvedValue({ ...FIXTURES_REPORT, sessionId: KID_GPU });
    await click(inRow(KID_GPU, 'workspace.children.toggle'));
    expect(api.postChildCollect).toHaveBeenLastCalledWith(KID_TESTS, KID_GPU);
  });

  it('offers to try a failed collect again', async () => {
    api.postChildCollect.mockRejectedValue(new Error('boom'));
    await showTab(viewOf());
    await click(inRow(KID_FIXTURES, 'workspace.children.toggle'));
    api.postChildCollect.mockResolvedValue(FIXTURES_REPORT);
    await click(inRow(KID_FIXTURES, 'workspace.children.collect-retry'));
    expect(inRow(KID_FIXTURES, 'workspace.children.summary')).not.toBeNull();
  });

  it('says what a cancel stops and keeps, and keeps running when told to', async () => {
    await showTab(viewOf());
    await click(inRow(KID_MIGRATION, 'workspace.children.cancel'));
    const dialog = q('workspace.children.cancel-dialog');
    expect(dialog?.textContent).toContain('Cancel “Write migration 0042”?');
    expect(dialog?.textContent).toContain('hoplet/3fa91c0d');
    await click(q('workspace.children.keep'));
    expect(q('workspace.children.cancel-dialog')).toBeNull();
    expect(api.postChildCancel).not.toHaveBeenCalled();
  });

  it('cancels a child with its subtree', async () => {
    api.postChildCancel.mockResolvedValue(undefined);
    const view = viewOf();
    await showTab(view);
    await click(inRow(KID_TESTS, 'workspace.children.cancel'));
    expect(q('workspace.children.cancel-dialog')?.textContent).toContain('Cancel 2 sessions?');
    await click(q('workspace.children.confirm'));
    expect(api.postChildCancel).toHaveBeenCalledWith(PARENT, KID_TESTS, true);
    expect(view.refresh).toHaveBeenCalled();
    expect(q('workspace.children.cancel-dialog')).toBeNull();
  });

  it('cancels all through each direct child, and shows a failure in the dialog', async () => {
    api.postChildCancel.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('boom'));
    await showTab(viewOf());
    await click(q('workspace.children.cancel-all'));
    expect(q('workspace.children.cancel-dialog')?.textContent).toContain('Cancel 4 sessions?');
    await click(q('workspace.children.confirm'));
    expect(api.postChildCancel.mock.calls).toEqual([
      [PARENT, KID_MIGRATION, true],
      [PARENT, KID_TESTS, true],
    ]);
    expect(q('workspace.children.cancel-dialog')?.querySelector('[role="alert"]')).not.toBeNull();
  });
});

describe('Conversation > child permission strip', () => {
  const showAsks = async (view: ChildrenView, decidable = true, onOpenSession = vi.fn()) => {
    await act(async () => {
      root.render(createElement(ChildAsks, { view, decidable, onOpenSession, key: ++shown }));
    });
    return onOpenSession;
  };
  const blocked = (asks: ChildrenView['asks'] = { [KID_DOCS]: [DOCS_ASK] }) =>
    viewOf({ data: kidsTree(PARENT, blockedDocs), asks });

  it('shows nothing while no child is blocked', async () => {
    await showAsks(viewOf());
    expect(q('cockpit.child-asks')).toBeNull();
  });

  it('allows a blocked child’s request with the call its own conversation makes', async () => {
    api.postPermission.mockResolvedValue(undefined);
    const view = blocked();
    await showAsks(view);
    expect(q('cockpit.child-ask')?.textContent).toContain('pnpm schema');
    await click(q('cockpit.child-ask.allow'));
    expect(api.postPermission).toHaveBeenCalledWith(KID_DOCS, 'r-docs-1', 'allow');
    expect(view.answered).toHaveBeenCalledWith(KID_DOCS, 'r-docs-1');
    expect(view.refresh).toHaveBeenCalled();
  });

  it('takes a request answered elsewhere as answered, and shows any other failure', async () => {
    api.postPermission.mockRejectedValueOnce(new ApiError(409, 'request-closed'));
    const view = blocked();
    await showAsks(view);
    await click(q('cockpit.child-ask.deny'));
    expect(api.postPermission).toHaveBeenCalledWith(KID_DOCS, 'r-docs-1', 'deny');
    expect(view.answered).toHaveBeenCalled();

    api.postPermission.mockRejectedValueOnce(new Error('boom'));
    const other = blocked();
    await showAsks(other);
    await click(q('cockpit.child-ask.allow'));
    expect(other.answered).not.toHaveBeenCalled();
    expect(q('cockpit.child-ask')?.querySelector('[role="alert"]')).not.toBeNull();
  });

  it('only opens the child when it cannot be answered here: a plan, no request yet, or no live turn', async () => {
    const open = await showAsks(blocked({ [KID_DOCS]: [{ ...DOCS_ASK, plan: true }] }));
    expect(q('cockpit.child-ask')?.textContent).toContain('Asks you to approve its plan');
    expect(q('cockpit.child-ask.allow')).toBeNull();
    await click(q('cockpit.child-ask.open'));
    expect(open).toHaveBeenCalledWith(KID_DOCS);

    await showAsks(blocked({}));
    expect(q('cockpit.child-ask')?.textContent).toContain('Waits for a permission');
    expect(q('cockpit.child-ask.allow')).toBeNull();

    await showAsks(blocked(), false);
    expect(q('cockpit.child-ask.allow')).toBeNull();
    expect(q('cockpit.child-ask.open')?.textContent).toContain('Open to answer');
  });
});
