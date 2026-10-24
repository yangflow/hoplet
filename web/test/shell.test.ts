import { JSDOM } from 'jsdom';
import { act, createElement } from 'react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

// The shell's keyboard behaviour in a DOM: Dialog traps and returns focus, Select picks with the arrow keys
// (the palette's keys are in palette.test.ts).

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
