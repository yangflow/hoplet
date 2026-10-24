// @vitest-environment jsdom
import type { SessionConfigChange } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { configChange } from '../src/lib/config-change-fixtures.ts';
import {
  ConfigChangeCard,
  ConfigChangeResult,
  type ConfigChangeView,
} from '../src/screens/cockpit/conversation/config-change-card.tsx';
import { ConfigReview } from '../src/screens/workspace/config-review.tsx';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const SESSION = '8f1c2d3e-4a5b-4c6d-8e7f-90a1b2c3d4e5';
const NOW = Date.UTC(2026, 9, 24, 9, 0, 0);

let root: Root;
let host: HTMLDivElement;

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const all = (id: string) => host.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`);
const click = async (el: Element | null) => {
  expect(el).toBeTruthy();
  await act(async () => {
    (el as HTMLElement).click();
  });
};
const show = async (node: ReturnType<typeof createElement>) => {
  await act(async () => {
    root.render(node);
  });
};

function viewOf(
  change: SessionConfigChange,
  over: Partial<ConfigChangeView> = {},
): ConfigChangeView {
  return {
    changes: [change],
    touched: new Set([change.changeId]),
    dismissed: new Set(),
    busy: null,
    decidable: true,
    providerName: 'Claude Code',
    projectName: 'hoplet',
    deviceName: 'studio',
    onDecide: vi.fn(),
    onUndo: vi.fn(),
    onReview: vi.fn(),
    onDismiss: vi.fn(),
    ...over,
  };
}

const applied = (over: Partial<SessionConfigChange> = {}) =>
  configChange(SESSION, NOW, {
    state: 'applied',
    decidedAt: NOW + 60_000,
    appliedAt: NOW + 61_000,
    undoable: true,
    ...over,
  });

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe('the card in the turn', () => {
  it('lists the objects, opens the review and declines', async () => {
    const change = configChange(SESSION, NOW);
    const view = viewOf(change);
    await show(createElement(ConfigChangeCard, { change, view, now: NOW }));
    expect(q('cockpit.config-change')?.textContent).toContain(
      'Claude Code proposes {n} changes to this project'.replace('{n}', '4'),
    );
    expect(all('cockpit.config-change-object')).toHaveLength(4);
    expect(q('cockpit.config-change-reason')?.textContent).toBe(change.reason);
    expect(q('cockpit.config-change-expiry')?.textContent).toMatch(/Expires in/);
    await click(q('cockpit.config-change-review'));
    expect(view.onReview).toHaveBeenCalledWith(change.changeId);
    await click(q('cockpit.config-change-decline'));
    expect(view.onDecide).toHaveBeenCalledWith(change.changeId, false);
  });

  it('cannot decline while not connected', async () => {
    const change = configChange(SESSION, NOW);
    await show(
      createElement(ConfigChangeCard, {
        change,
        view: viewOf(change, { decidable: false }),
        now: NOW,
      }),
    );
    expect((q('cockpit.config-change-decline') as HTMLButtonElement).disabled).toBe(true);
    expect((q('cockpit.config-change-review') as HTMLButtonElement).disabled).toBe(false);
  });
});

describe('the outcome in the turn', () => {
  const stateOf = async (change: SessionConfigChange, over: Partial<ConfigChangeView> = {}) => {
    const view = viewOf(change, over);
    await show(createElement(ConfigChangeResult, { change, view }));
    return { view, state: q('cockpit.config-change-result')?.dataset.state };
  };

  it('shows each state', async () => {
    const base = configChange(SESSION, NOW);
    expect((await stateOf(base)).state).toBeUndefined();
    expect((await stateOf({ ...base, state: 'approved', decidedAt: NOW })).state).toBe('approved');
    expect((await stateOf({ ...base, state: 'declined', decidedAt: NOW })).state).toBe('declined');
    expect((await stateOf({ ...base, state: 'expired' })).state).toBe('expired');
    expect((await stateOf({ ...base, state: 'undone', undoneAt: NOW })).state).toBe('undone');
    const failed = await stateOf({
      ...base,
      state: 'failed',
      error: 'stale',
      errorReason: 'memory:project',
    });
    expect(failed.state).toBe('failed');
    expect(host.textContent).toContain('Changed since');
    expect(host.textContent).toContain('Unchanged');
  });

  it('undoes an applied change and opens its review', async () => {
    const change = applied();
    const { view, state } = await stateOf(change);
    expect(state).toBe('applied');
    await click(q('cockpit.config-change-undo'));
    expect(view.onUndo).toHaveBeenCalledWith(change.changeId);
    await click(q('cockpit.config-change-open'));
    expect(view.onReview).toHaveBeenCalledWith(change.changeId);
  });

  it('offers no undo once the earlier versions are gone', async () => {
    await stateOf(applied({ undoable: false }));
    expect(q('cockpit.config-change-undo')).toBeNull();
  });

  it('names the file that changed since when an undo is refused, and puts the refusal away', async () => {
    const change = applied({ undoable: false, error: 'stale', errorReason: 'memory:project' });
    const { view, state } = await stateOf(change);
    expect(state).toBe('undo-refused');
    expect(q('cockpit.config-change-result')?.querySelector('p b')?.textContent).toBe('CLAUDE.md');
    expect(q('cockpit.config-change-undo')).toBeNull();
    await click(q('cockpit.config-change-dismiss'));
    expect(view.onDismiss).toHaveBeenCalledWith(change.changeId);
    const put = await stateOf(change, { dismissed: new Set([change.changeId]) });
    expect(put.state).toBe('applied');
  });
});

describe('the review in the inspector', () => {
  const review = async (
    change: SessionConfigChange | null,
    over: { decidable?: boolean; busy?: boolean; initial?: string } = {},
  ) => {
    const onDecide = vi.fn(async () => null as string | null);
    const onUndo = vi.fn(async () => 'Nothing to undo' as string | null);
    await show(
      createElement(ConfigReview, {
        change,
        busy: over.busy ?? false,
        decidable: over.decidable ?? true,
        initial: over.initial,
        onDecide,
        onUndo,
      }),
    );
    return { onDecide, onUndo };
  };

  it('shows the objects and the selected diff, and approves or declines all of them', async () => {
    const { onDecide } = await review(configChange(SESSION, NOW));
    expect(all('config-review.object')).toHaveLength(4);
    expect(q('config-review.view')?.textContent).toContain('CLAUDE.md');
    expect(q('config-review.diff')).toBeTruthy();
    await click(all('config-review.object')[3] ?? null);
    expect(q('config-review.view')?.textContent).toContain('Effort');
    expect(q('config-review.diff')).toBeNull();
    expect(q('config-review.approve')?.textContent).toBe('Approve all 4');
    await click(q('config-review.approve'));
    expect(onDecide).toHaveBeenCalledWith(true);
    await click(q('config-review.decline'));
    expect(onDecide).toHaveBeenCalledWith(false);
  });

  it('opens on the object asked for', async () => {
    await review(configChange(SESSION, NOW), { initial: 'skill:ingest-fixtures' });
    expect(q('config-review.view')?.textContent).toContain('New skill');
    expect(host.querySelector('[aria-current="true"]')?.textContent).toContain('ingest-fixtures');
  });

  it('cannot decide while not connected or busy', async () => {
    await review(configChange(SESSION, NOW), { decidable: false });
    expect((q('config-review.approve') as HTMLButtonElement).disabled).toBe(true);
    await review(configChange(SESSION, NOW), { busy: true });
    expect((q('config-review.decline') as HTMLButtonElement).disabled).toBe(true);
  });

  it('undoes an applied change and shows a refusal', async () => {
    const { onUndo } = await review(applied());
    expect(q('config-review.approve')).toBeNull();
    await click(q('config-review.undo'));
    expect(onUndo).toHaveBeenCalled();
    expect(q('config-review.error')?.textContent).toBe('Nothing to undo');
  });

  it('says what became of a decided change', async () => {
    await review(configChange(SESSION, NOW, { state: 'declined', decidedAt: NOW }));
    expect(q('config-review.undo')).toBeNull();
    expect(q('config-review.state')?.textContent).toContain('Changes declined');
  });

  it('says when the request is gone', async () => {
    await review(null);
    expect(q('config-review.gone')).toBeTruthy();
  });
});
