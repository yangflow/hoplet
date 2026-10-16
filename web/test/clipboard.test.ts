// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { copyLater } from '../src/lib/clipboard.ts';

class FakeItem {
  constructor(readonly items: Record<string, Promise<Blob>>) {}
}

afterEach(() => {
  vi.unstubAllGlobals();
});

function clipboard(write: (items: FakeItem[]) => Promise<void>, writeText = vi.fn()) {
  vi.stubGlobal('ClipboardItem', FakeItem);
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { write, writeText },
  });
  return writeText;
}

describe('copyLater', () => {
  it('starts the write before the text has arrived, with a promise of it', async () => {
    let started = false;
    let text = '';
    clipboard(async ([item]) => {
      started = true;
      text = await (await item?.items['text/plain'])?.text();
    });
    let resolve: (v: string) => void = () => {};
    const done = copyLater(() => new Promise<string>((r) => (resolve = r)));
    expect(started).toBe(true);
    resolve('# answer');
    expect(await done).toBe('copied');
    expect(text).toBe('# answer');
  });

  it('says there was nothing to copy', async () => {
    clipboard(async ([item]) => {
      await item?.items['text/plain'];
    });
    expect(await copyLater(async () => null)).toBe('empty');
  });

  it('falls back to writeText when the write is refused', async () => {
    const writeText = clipboard(async () => {
      throw new Error('refused');
    });
    writeText.mockResolvedValue(undefined);
    expect(await copyLater(async () => 'text')).toBe('copied');
    expect(writeText).toHaveBeenCalledWith('text');
  });

  it('uses writeText when there is no ClipboardItem, and reports a failed fetch', async () => {
    vi.stubGlobal('ClipboardItem', undefined);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    expect(await copyLater(async () => 'plain')).toBe('copied');
    expect(writeText).toHaveBeenCalledWith('plain');
    expect(await copyLater(() => Promise.reject(new Error('net')))).toBe('failed');
  });
});
