// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  linesOf,
  OutputWell,
  WELL_LINES,
} from '../src/screens/cockpit/conversation/OutputWell.tsx';

let root: Root;
let host: HTMLDivElement;
const show = (props: Parameters<typeof OutputWell>[0]) =>
  act(async () => root.render(createElement(OutputWell, props)));

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe('OutputWell', () => {
  it('splits a text into lines and drops the final newline', () => {
    expect(linesOf('a\nb\n').map((l) => l.text)).toEqual(['a', 'b']);
    expect(linesOf('')).toEqual([{ text: '' }]);
  });

  it('shows the command line first and numbers the lines when asked', async () => {
    await show({ lines: [{ text: 'ls core', kind: 'cmd' }, { text: 'src' }], numbered: false });
    expect(host.querySelector('.wl.cmd')?.textContent).toBe('ls core');
    expect(host.querySelector('.well.ln')).toBeNull();
    await show({
      lines: [
        { text: 'a', no: 30 },
        { text: 'b', no: 31 },
      ],
      numbered: true,
    });
    expect([...host.querySelectorAll('.wl .n')].map((n) => n.textContent)).toEqual(['30', '31']);
    expect(host.querySelector('.well.ln')).not.toBeNull();
  });

  it('fades past the maximum height and not before', async () => {
    await show({
      lines: linesOf(Array.from({ length: WELL_LINES }, (_, i) => `l${i}`).join('\n')),
    });
    expect(host.querySelector('.well.fade')).toBeNull();
    await show({
      lines: linesOf(Array.from({ length: WELL_LINES + 1 }, (_, i) => `l${i}`).join('\n')),
    });
    expect(host.querySelector('.well.fade')).not.toBeNull();
    expect(host.querySelector('.wb')?.getAttribute('tabindex')).toBe('0');
  });

  it('mounts only the lines in view of a large block', async () => {
    const lines = linesOf(Array.from({ length: 5000 }, (_, i) => `line ${i}`).join('\n'));
    await show({ lines, numbered: true, scrollTestId: 'full' });
    expect(host.querySelectorAll('.wl').length).toBeLessThan(80);
    expect(host.querySelector('[data-testid="full"]')?.textContent).toContain('line 0');
    expect(host.querySelector('.well.big')).not.toBeNull();
  });

  it('copies the text of the lines, or the text it is given', async () => {
    const write = vi.fn();
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: write },
    });
    await show({ lines: linesOf('a\nb') });
    (host.querySelector('[data-testid="cockpit.well-copy"]') as HTMLElement).click();
    expect(write).toHaveBeenLastCalledWith('a\nb');
    await show({ lines: linesOf('a'), copyText: 'whole block' });
    (host.querySelector('[data-testid="cockpit.well-copy"]') as HTMLElement).click();
    expect(write).toHaveBeenLastCalledWith('whole block');
  });

  it('tints a failure and sits flush left when solo', async () => {
    await show({ lines: linesOf('boom'), error: true, solo: true });
    expect(host.querySelector('.well.err.solo')).not.toBeNull();
  });
});
