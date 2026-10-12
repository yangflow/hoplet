import { JSDOM } from 'jsdom';
import { act, createElement } from 'react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

// The shell's keyboard behaviour in a DOM: Dialog traps and returns focus, the palette moves and opens
// with the keyboard, Select picks with the arrow keys.

let dom: JSDOM;
beforeAll(() => {
  dom = new JSDOM('<!DOCTYPE html><html lang="en"><body><div id="root"></div></body></html>', {
    pretendToBeVisual: true,
    url: 'http://localhost/',
  });
  const globals: Record<string, unknown> = {
    window: dom.window,
    document: dom.window.document,
    location: dom.window.location,
    HTMLElement: dom.window.HTMLElement,
    Node: dom.window.Node,
    KeyboardEvent: dom.window.KeyboardEvent,
    IS_REACT_ACT_ENVIRONMENT: true,
    requestAnimationFrame: (cb: () => void) => setTimeout(cb, 0),
  };
  for (const [name, value] of Object.entries(globals)) {
    Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
  }
});

afterEach(() => {
  document.body.innerHTML = '<div id="root"></div>';
});

async function mount(element: ReturnType<typeof createElement>) {
  const { createRoot } = await import('react-dom/client');
  const host = document.getElementById('root');
  if (!host) throw new Error('no root');
  const root = createRoot(host);
  await act(async () => root.render(element));
  return root;
}

function key(target: Element, k: string, init: KeyboardEventInit = {}) {
  target.dispatchEvent(
    new dom.window.KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }),
  );
}

describe('Dialog', () => {
  it('focuses in, closes on Esc and returns focus', async () => {
    const { Dialog } = await import('../src/components/Dialog.tsx');
    const trigger = document.createElement('button');
    document.body.appendChild(trigger);
    trigger.focus();
    const onClose = vi.fn();
    const root = await mount(
      createElement(
        Dialog,
        { label: 'Test', onClose },
        createElement('button', { type: 'button', 'data-autofocus': true }, 'first'),
        createElement('button', { type: 'button' }, 'last'),
      ),
    );
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.getAttribute('aria-modal')).toBe('true');
    expect(document.activeElement?.textContent).toBe('first');
    const [first, last] = [...document.querySelectorAll('[role="dialog"] button')];
    (last as HTMLElement).focus();
    await act(async () => key(last as Element, 'Tab'));
    expect(document.activeElement).toBe(first);
    await act(async () => key(first as Element, 'Escape'));
    expect(onClose).toHaveBeenCalledTimes(1);
    await act(async () => root.unmount());
    expect(document.activeElement).toBe(trigger);
  });
});

describe('Select', () => {
  it('opens with ArrowDown, moves and picks with the keyboard', async () => {
    const { Select } = await import('../src/components/Select.tsx');
    const onChange = vi.fn();
    await mount(
      createElement(Select, {
        label: 'Model',
        value: 'a',
        onChange,
        options: [
          { value: 'a', label: 'Alpha' },
          { value: 'b', label: 'Beta' },
        ],
      }),
    );
    const trigger = document.querySelector('button[aria-haspopup="listbox"]') as HTMLElement;
    // The trigger's name holds the field and the value it shows (the visible text is part of the name).
    expect(trigger.getAttribute('aria-label')).toBe('Model, Alpha');
    await act(async () => key(trigger, 'ArrowDown'));
    const list = document.querySelector('[role="listbox"]') as HTMLElement;
    expect(list).not.toBeNull();
    await act(async () => key(list, 'ArrowDown'));
    await act(async () => key(list, 'Enter'));
    expect(onChange).toHaveBeenCalledWith('b');
    expect(document.querySelector('[role="listbox"]')).toBeNull();
  });
});

describe('Palette', () => {
  it('lists sessions and actions and opens the active entry on Enter', async () => {
    const { Palette } = await import('../src/screens/cockpit/palette.tsx');
    const onSelectSession = vi.fn();
    const onClose = vi.fn();
    const session = {
      sessionId: 's1',
      projectPath: '/a/hoplet',
      projectDir: '',
      deviceName: 'MacBook Pro',
      title: 'byte cursor catalog',
      provider: 'claude' as const,
      ownerConnected: true,
      createdAt: 1,
      updatedAt: 2,
      lineCount: 1,
      byteSize: 1,
      ownerEpoch: 0,
    };
    await mount(
      createElement(Palette, {
        sessions: [session],
        searchFixture: { hits: [], scanning: false },
        onClose,
        onSelectSession,
        onSelectHit: vi.fn(),
        onNewSession: vi.fn(),
        onSettings: vi.fn(),
      }),
    );
    const options = [...document.querySelectorAll('[role="option"]')];
    expect(options.map((o) => o.textContent)).toEqual([
      expect.stringContaining('byte cursor catalog'),
      expect.stringContaining('New session'),
      expect.stringContaining('Settings'),
    ]);
    const input = document.querySelector('input[role="combobox"]') as HTMLElement;
    expect(document.activeElement).toBe(input);
    await act(async () => key(input, 'Enter'));
    expect(onSelectSession).toHaveBeenCalledWith('s1');
    expect(onClose).toHaveBeenCalled();
  });
});
