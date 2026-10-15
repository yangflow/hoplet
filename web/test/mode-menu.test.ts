// @vitest-environment jsdom
import type { ConfigOption } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  isRisky,
  ModeMenu,
  modeOption,
  nextMode,
} from '../src/screens/cockpit/composer/mode-menu.tsx';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function option(current: string): ConfigOption & { type: 'select' } {
  return {
    type: 'select',
    id: 'mode',
    name: 'Permission mode',
    category: 'mode',
    currentValue: current,
    options: [
      { value: 'a', name: 'A', description: 'first' },
      { value: 'b', name: 'B' },
      { value: 'c', name: 'C', risk: 'high' },
    ],
  };
}

describe('the permission-mode helpers', () => {
  it('finds the first select of the mode category and nothing else', () => {
    const model: ConfigOption = { ...option('a'), id: 'model', category: 'model' };
    const flag: ConfigOption = {
      type: 'boolean',
      id: 'x',
      name: 'X',
      category: 'mode',
      currentValue: true,
    };
    expect(modeOption([model, flag, option('a')])?.id).toBe('mode');
    expect(modeOption([model, flag])).toBeUndefined();
    expect(modeOption(undefined)).toBeUndefined();
  });

  it('cycles in the declared order and wraps', () => {
    expect(nextMode(option('a'))).toBe('b');
    expect(nextMode(option('c'))).toBe('a');
    expect(nextMode({ ...option('a'), options: [{ value: 'a', name: 'A' }] })).toBeUndefined();
  });

  it('marks a high-risk current value', () => {
    expect(isRisky(option('c'))).toBe(true);
    expect(isRisky(option('a'))).toBe(false);
  });
});

describe('the mode menu', () => {
  let root: Root | undefined;
  let host: HTMLElement | undefined;
  afterEach(() => {
    act(() => root?.unmount());
    host?.remove();
  });

  function mount(current: string, onChange = vi.fn(), initialOpen = false) {
    host = document.createElement('div');
    document.body.append(host);
    root = createRoot(host);
    act(() =>
      root?.render(createElement(ModeMenu, { option: option(current), onChange, initialOpen })),
    );
    return onChange;
  }

  it('names the current mode on the pill and turns amber only for a high-risk one', () => {
    mount('a');
    const pill = host?.querySelector('[data-testid="cockpit.mode"]');
    expect(pill?.textContent).toBe('A');
    expect(pill?.classList.contains('warn')).toBe(false);
    act(() => root?.render(createElement(ModeMenu, { option: option('c'), onChange: vi.fn() })));
    expect(host?.querySelector('[data-testid="cockpit.mode"]')?.classList.contains('warn')).toBe(
      true,
    );
    expect(host?.querySelector('[data-testid="cockpit.mode-live"]')?.textContent).toContain('C');
  });

  it('lists every mode, the high-risk one last with its badge, and applies a pick at once', () => {
    const onChange = mount('a', vi.fn(), true);
    const rows = [
      ...(host?.querySelectorAll<HTMLElement>('[data-testid="cockpit.mode-option"]') ?? []),
    ];
    expect(rows.map((r) => r.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false']);
    expect(rows[2]?.textContent).toContain('High risk');
    expect(rows[0]?.textContent).toContain('first');
    act(() => rows[1]?.click());
    expect(onChange).toHaveBeenCalledWith('mode', 'b');
  });
});
