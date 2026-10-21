// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionUsageChip } from '../src/screens/cockpit/session-usage.tsx';
import { UsageSection } from '../src/screens/settings/usage.tsx';
import {
  backfillingUsage,
  emptyUsage,
  sessionUsage,
  usageFixture,
} from '../src/screens/settings/usage-fixtures.ts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const all = (id: string) => [...host.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`)];
const click = async (el: Element | null | undefined) => {
  await act(async () => {
    (el as HTMLElement).click();
  });
};
const radio = (label: string) =>
  [...host.querySelectorAll<HTMLElement>('[role="radio"]')].find((b) => b.textContent === label);

const onTokensOnly = vi.fn();
const render = async (el: ReturnType<typeof createElement>) => {
  await act(async () => {
    root.render(el);
  });
};
const section = (over: Parameters<typeof usageFixture>[0] = {}, tokensOnly = false) =>
  render(createElement(UsageSection, { fixture: usageFixture(over), tokensOnly, onTokensOnly }));

beforeEach(() => {
  onTokensOnly.mockReset();
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe('Settings > Usage', () => {
  it('shows the totals, the chart, the legend and the ranked rows', async () => {
    await section();
    expect(q('settings.usage.total')?.textContent).toContain('$');
    expect(q('settings.usage.chart')).not.toBeNull();
    expect(q('settings.usage.legend')).not.toBeNull();
    expect(all('settings.usage.row').length).toBeGreaterThan(0);
    expect(q('settings.usage.footnote')).not.toBeNull();
  });

  it('switches the grouping', async () => {
    await section();
    expect(q('settings.usage.table.project')).not.toBeNull();
    await click(radio('Model'));
    expect(q('settings.usage.table.model')).not.toBeNull();
    expect(q('settings.usage.table.project')).toBeNull();
  });

  it('moves the range with previous and next, keeping the fixture answer', async () => {
    await section();
    const before = q('settings.usage.label')?.textContent;
    await click(q('settings.usage.prev'));
    expect(q('settings.usage.label')?.textContent).not.toBe(before);
    await click(q('settings.usage.next'));
    expect(q('settings.usage.label')?.textContent).toBe(before);
    expect((q('settings.usage.next') as HTMLButtonElement).disabled).toBe(true);
  });

  it('shows two date fields for a custom range', async () => {
    await section();
    await click(radio('Custom'));
    expect(q('settings.usage.first')).not.toBeNull();
    expect(q('settings.usage.last')).not.toBeNull();
  });

  it('hides the cost for tokens only', async () => {
    await section({}, true);
    expect(q('settings.usage.total')?.textContent).not.toContain('$');
    expect(q('settings.usage.footnote')?.textContent).toContain('tokens only');
  });

  it('says what the unpriced model costs the estimate', async () => {
    await section({ group: 'model' });
    expect(q('settings.usage.unknown')).not.toBeNull();
    expect(q('settings.usage.add-price')).not.toBeNull();
    await click(q('settings.usage.add-price'));
    expect(q('settings.prices.table')).not.toBeNull();
  });

  it('has an empty state', async () => {
    await section(emptyUsage());
    expect(q('settings.usage.empty')).not.toBeNull();
    expect(q('settings.usage.chart')).toBeNull();
  });

  it('shows the backfill note', async () => {
    await section(backfillingUsage());
    expect(q('settings.usage.backfill')).not.toBeNull();
  });

  it('switches to Prices with the arrow keys, through Budgets', async () => {
    await section();
    for (const to of ['settings.budgets.list', 'settings.prices.table']) {
      const tab = host.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
      await act(async () => {
        tab?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
      });
      if (to === 'settings.budgets.list') expect(q('settings.usage.chart')).toBeNull();
    }
    expect(q('settings.prices.table')).not.toBeNull();
    expect(q('settings.usage.chart')).toBeNull();
  });
});

describe('Settings > Usage > Prices', () => {
  const prices = (tokensOnly = false) =>
    render(
      createElement(UsageSection, {
        fixture: usageFixture({ tab: 'prices' }),
        tokensOnly,
        onTokensOnly,
      }),
    );

  it('lists the built-in prices and the unpriced model', async () => {
    await prices();
    const rows = all('settings.prices.row');
    expect(rows.some((r) => r.textContent?.includes('claude-opus-4-5'))).toBe(true);
    expect(rows.some((r) => r.textContent?.includes('glm-5'))).toBe(true);
  });

  it('reports the tokens-only switch', async () => {
    await prices();
    await click(q('settings.prices.tokens-only'));
    expect(onTokensOnly).toHaveBeenCalledWith(true);
  });

  it('saves an override from the editor into the table', async () => {
    await prices();
    await click(q('settings.usage.tab.prices'));
    const add = q('settings.prices.add');
    await click(add);
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
    const type = async (id: string, value: string) => {
      const input = q(id) as HTMLInputElement;
      await act(async () => {
        set?.call(input, value);
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
    };
    await type('settings.prices.model', 'my-model');
    await type('settings.prices.field.input', '2');
    await type('settings.prices.field.output', '8');
    await click(q('settings.prices.save'));
    expect(q('settings.prices.editor')).toBeNull();
    expect(all('settings.prices.row').some((r) => r.textContent?.includes('my-model'))).toBe(true);
  });
});

describe('the session usage chip', () => {
  const chip = (over: { unknown?: boolean; tokensOnly?: boolean; open?: boolean } = {}) =>
    render(
      createElement(SessionUsageChip, {
        sessionId: 'ses-usage',
        refresh: 1,
        tokensOnly: over.tokensOnly ?? false,
        fixture: sessionUsage(over.unknown),
        open: over.open,
      }),
    );

  it('shows tokens and the estimated cost', async () => {
    await chip();
    const text = q('cockpit.usage-chip')?.textContent ?? '';
    expect(text).toContain('tokens');
    expect(text).toContain('≈ $');
  });

  it('drops the cost for tokens only and for a model without a price', async () => {
    await chip({ tokensOnly: true });
    expect(q('cockpit.usage-chip')?.textContent).not.toContain('$');
    await chip({ unknown: true });
    expect(q('cockpit.usage-chip')?.textContent).not.toContain('$');
  });

  it('opens the popover with a click', async () => {
    await chip();
    expect(q('cockpit.usage-popover')).toBeNull();
    await click(q('cockpit.usage-chip'));
    expect(q('cockpit.usage-popover')?.textContent).toContain('claude-opus-4-5');
  });

  it('points to Prices for a model without a price', async () => {
    await chip({ unknown: true, open: true });
    expect(q('cockpit.usage-popover')?.textContent).toContain('glm-5 has no price');
  });
});
