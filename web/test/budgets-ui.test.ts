// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import { SessionUsageChip } from '../src/screens/cockpit/session-usage.tsx';
import { UsageSection } from '../src/screens/settings/usage.tsx';
import {
  B1,
  B2,
  B3,
  B4,
  B4_ALLOWED,
  B4_OK,
  BUDGET_DEVICES,
  BUDGET_PROJECTS,
  budgetList,
  HOPLET_PATH,
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
const page = async (list: ReturnType<typeof budgetList>, editing?: string) => {
  await act(async () => {
    root.render(
      createElement(UsageSection, {
        tokensOnly: false,
        onTokensOnly: () => undefined,
        fixture: usageFixture({
          tab: 'budgets',
          budgets: {
            list,
            projects: BUDGET_PROJECTS,
            devices: BUDGET_DEVICES,
            ...(editing ? { editing } : {}),
          },
        }),
      }),
    );
  });
};

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe('Settings > Usage > Budgets', () => {
  it('lists a row per budget with nothing to act on while none is over in pause mode', async () => {
    await page(budgetList(B1, B2, B3, B4_OK));
    expect(all('settings.budget.edit')).toHaveLength(4);
    expect(q('settings.budget.allow')).toBeNull();
    expect(q('settings.budget.stop-allowing')).toBeNull();
  });

  it('offers to allow a paused budget until the end of its period, and to take that back', async () => {
    await page(budgetList(B1, B4));
    expect(q('settings.budget.allow')?.textContent).toContain('Oct 31');
    await click(q('settings.budget.allow'));
    expect(q('settings.budget.allow')).toBeNull();
    expect(q('settings.budget.stop-allowing')).not.toBeNull();
    expect(host.textContent).toContain('hoplet-docs is allowed until Oct 31');
    await click(q('settings.budget.stop-allowing'));
    expect(q('settings.budget.allow')).not.toBeNull();
  });

  it('shows an allowed budget with its way back', async () => {
    await page(budgetList(B4_ALLOWED));
    expect(q('settings.budget.stop-allowing')).not.toBeNull();
    expect(q('settings.budget.allow')).toBeNull();
  });

  it('opens the budget being edited as a form in place of its row, with Delete', async () => {
    await page(budgetList(B1, B2, B3), B2.id);
    expect(q('settings.budget.form')).not.toBeNull();
    expect((q('settings.budget.limit') as HTMLInputElement).value).toBe('12');
    expect(all('settings.budget.edit')).toHaveLength(2);
    expect(q('settings.budget.delete')).not.toBeNull();
    await click(q('settings.budget.delete'));
    expect(q('settings.budget.form')).toBeNull();
    expect(host.textContent).not.toContain('Project · hoplet');
  });

  it('keeps the form open on Save while the limit is not a number, and has no Delete for a new budget', async () => {
    await page(budgetList(B1));
    await click(q('settings.budgets.add'));
    expect(q('settings.budget.delete')).toBeNull();
    await click(q('settings.budget.save'));
    expect(q('settings.budget.form')).not.toBeNull();
    expect(q('settings.budget.limit')?.getAttribute('aria-invalid')).toBe('true');
  });

  it('says what budgets are when there are none', async () => {
    await page(budgetList());
    expect(q('settings.budgets.empty')).not.toBeNull();
    await click(q('settings.budgets.add'));
    expect(q('settings.budget.form')).not.toBeNull();
  });
});

describe('the session chip', () => {
  const chip = async (budgets: (typeof B1)[]) => {
    await act(async () => {
      root.render(
        createElement(SessionUsageChip, {
          sessionId: 'ses-usage',
          refresh: 1,
          tokensOnly: false,
          projectPath: HOPLET_PATH,
          ownerDeviceId: 'dev-macbook',
          fixture: sessionUsage(),
          open: true,
          budgetsFixture: budgets,
        }),
      );
    });
  };

  it('lists the budgets that count the session, with an amber dot, once one is past its alert line', async () => {
    await chip([B1, B2, B3, B4]);
    const lines = q('cockpit.usage-budgets')?.textContent ?? '';
    expect(lines).toContain('hoplet · weekly');
    expect(lines).toContain('Global · monthly');
    expect(lines).not.toContain('hoplet-docs');
    expect(lines).not.toContain('studio');
    expect(host.querySelector('.adot')).not.toBeNull();
  });

  it('shows neither while every budget is under its alert line', async () => {
    await chip([B1]);
    expect(q('cockpit.usage-budgets')).toBeNull();
    expect(host.querySelector('.adot')).toBeNull();
  });
});

describe('a refusal by a budget', () => {
  it('carries the budget that refused', () => {
    const e = new ApiError(409, 'over-budget', undefined, undefined, undefined, {
      id: 'b1',
      name: 'hoplet-docs',
    });
    expect(e.budget).toEqual({ id: 'b1', name: 'hoplet-docs' });
  });
});

describe('the words and bars around a budget', () => {
  it('translates the allowed toast and its date', async () => {
    const { setLanguage } = await import('../src/lib/i18n.ts');
    const { states } = await import('../src/screens/settings/states.tsx');
    setLanguage('zh-Hans');
    try {
      const view = states['budgets-allowed'];
      await act(async () => {
        root.render(view?.());
      });
      expect(q('toast')?.textContent).toBe('hoplet-docs 已允许至 10月31日');
    } finally {
      setLanguage(null);
    }
  });

  it("draws the popover's bars with a fill and an alert tick", async () => {
    await act(async () => {
      root.render(
        createElement(SessionUsageChip, {
          sessionId: 'ses-usage',
          refresh: 1,
          tokensOnly: false,
          projectPath: HOPLET_PATH,
          ownerDeviceId: 'dev-macbook',
          fixture: sessionUsage(),
          open: true,
          budgetsFixture: [B1, B2],
        }),
      );
    });
    const bars = [
      ...host.querySelectorAll<HTMLElement>('[data-testid="cockpit.usage-budgets"] .pbar'),
    ];
    expect(bars).toHaveLength(2);
    for (const bar of bars) {
      expect(bar.querySelector('.pf')).not.toBeNull();
      expect(bar.querySelector('.pt')).not.toBeNull();
    }
    expect(bars[1]?.classList.contains('warn')).toBe(true);
    expect(bars[1]?.querySelector<HTMLElement>('.pf')?.style.width).toBe('86%');
  });
});
