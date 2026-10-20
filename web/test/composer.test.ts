// @vitest-environment jsdom
import { sniffImageType } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  MAX_EDGE,
  readImage,
  refusal,
  shrunkSize,
  sizeOf,
  toBase64,
} from '../src/screens/cockpit/composer/attachments.ts';
import {
  AUTO_MAX_PX,
  CONVERSATION_MIN_PX,
  clampHeight,
  HEIGHT_KEY,
  LINE_PX,
  loadHeight,
  maxFieldHeight,
  saveHeight,
} from '../src/screens/cockpit/composer/height.ts';
import { hasChoices, menuConfig, withValues } from '../src/screens/cockpit/composer/model-menu.tsx';
import { folderOf } from '../src/screens/cockpit/draft.tsx';

function memory() {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    removeItem: (k: string) => void data.delete(k),
  };
}

describe('the composer height', () => {
  it('grows to 8 lines by itself', () => {
    expect(AUTO_MAX_PX).toBe(8 * LINE_PX);
  });

  it('leaves the conversation its minimum and never goes below a line', () => {
    expect(maxFieldHeight(800, 160)).toBe(800 - CONVERSATION_MIN_PX - 160);
    expect(maxFieldHeight(300, 160)).toBe(LINE_PX);
    expect(clampHeight(5, 400)).toBe(LINE_PX);
    expect(clampHeight(900, 400)).toBe(400);
    expect(clampHeight(123.6, 400)).toBe(124);
  });

  it('is remembered by the browser until it is reset', () => {
    const store = memory();
    expect(loadHeight(store)).toBeNull();
    saveHeight(312, store);
    expect(store.data.get(HEIGHT_KEY)).toBe('312');
    expect(loadHeight(store)).toBe(312);
    saveHeight(null, store);
    expect(loadHeight(store)).toBeNull();
  });

  it('ignores a stored value that is not a height', () => {
    const store = memory();
    store.setItem(HEIGHT_KEY, 'tall');
    expect(loadHeight(store)).toBeNull();
    store.setItem(HEIGHT_KEY, '3');
    expect(loadHeight(store)).toBeNull();
  });
});

describe('the draft directory picker', () => {
  it('names the folder a directory is in, with the home directory as ~', () => {
    expect(folderOf('/Users/mark/github/repopro/hoplet')).toBe('~/github/repopro');
    expect(folderOf('/home/mark/notes')).toBe('~');
    expect(folderOf('/srv/app')).toBe('/srv');
    expect(folderOf('/app')).toBe('/');
  });
});

describe('images for a prompt', () => {
  const head = (...bytes: number[]) => {
    const out = new Uint8Array(16);
    out.set(bytes);
    return out;
  };

  it('recognizes the four types by their first bytes, as the agent does', () => {
    expect(sniffImageType(head(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a))).toBe('image/png');
    expect(sniffImageType(head(0xff, 0xd8, 0xff, 0xe0))).toBe('image/jpeg');
    expect(sniffImageType(head(...[...'GIF89a'].map((c) => c.charCodeAt(0))))).toBe('image/gif');
    expect(sniffImageType(head(...[...'RIFF\0\0\0\0WEBPVP8 '].map((c) => c.charCodeAt(0))))).toBe(
      'image/webp',
    );
    expect(sniffImageType(head(...[...'GIF8xx'].map((c) => c.charCodeAt(0))))).toBeNull();
    expect(sniffImageType(head(...[...'<svg'].map((c) => c.charCodeAt(0))))).toBeNull();
  });

  it('counts base64 sizes and refuses a fifth image or more than 8 MiB together', () => {
    expect(sizeOf(toBase64(new Uint8Array(5)))).toBe(5);
    expect(sizeOf(toBase64(new Uint8Array(6)))).toBe(6);
    const one = (size: number) => ({
      id: 'x',
      name: 'x',
      mediaType: 'image/png' as const,
      data: '',
      size,
    });
    expect(refusal([one(1), one(1), one(1)], one(1))).toBeNull();
    expect(refusal([one(1), one(1), one(1), one(1)], one(1))).toBe('At most 4 images');
    expect(refusal([one(4 * 1024 * 1024)], one(4 * 1024 * 1024 + 1))).toBe(
      'The images are larger than 8 MiB together',
    );
  });
});

describe('shrinking before upload', () => {
  it('draws an image larger than the long edge at the long edge, proportions kept', () => {
    expect(MAX_EDGE).toBe(1568);
    expect(shrunkSize(3136, 1000)).toEqual({ width: 1568, height: 500 });
    expect(shrunkSize(1000, 4704)).toEqual({ width: 333, height: 1568 });
    expect(shrunkSize(1568, 1200)).toBeNull();
    expect(shrunkSize(640, 480)).toBeNull();
  });
});

describe('reading an image', () => {
  it('says so when the browser cannot decode a file with an image signature', async () => {
    const restore = { bitmap: globalThis.createImageBitmap, canvas: globalThis.OffscreenCanvas };
    Object.assign(globalThis, {
      createImageBitmap: () => Promise.reject(new Error('InvalidStateError')),
      OffscreenCanvas: class {},
    });
    try {
      const bytes = new Uint8Array(64);
      bytes.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
      const read = await readImage(new File([bytes], 'broken.png', { type: 'image/png' }));
      expect(read).toBe('broken.png cannot be read as an image');
    } finally {
      Object.assign(globalThis, {
        createImageBitmap: restore.bitmap,
        OffscreenCanvas: restore.canvas,
      });
    }
  });
});

describe('the config options of the menu', () => {
  const model = {
    type: 'select' as const,
    id: 'model',
    name: 'Model',
    category: 'model',
    currentValue: 'default',
    options: [
      { value: 'default', name: 'Default model' },
      { value: 'opus', name: 'opus' },
    ],
  };
  const thinking = {
    type: 'boolean' as const,
    id: 'thinking',
    name: 'Thinking',
    currentValue: true,
  };
  const mode = { ...model, id: 'mode', name: 'Mode', category: 'mode' };
  const caps = { permissions: true, interrupt: true };

  it('lists the driver options in its order, without the mode category', () => {
    expect(menuConfig([thinking, mode, model]).map((o) => o.id)).toEqual(['thinking', 'model']);
    expect(menuConfig(undefined)).toEqual([]);
  });

  it('lays picked values over the current ones and ignores a value of the wrong type', () => {
    const got = withValues([model, thinking], { model: 'opus', thinking: 'no' });
    expect(got.map((o) => o.currentValue)).toEqual(['opus', true]);
    expect(withValues([model], {})).toEqual([model]);
  });

  it('offers a menu for an option, and otherwise none', () => {
    const one = { provider: 'claude' as const, capabilities: { ...caps, config: [] } };
    expect(hasChoices(one)).toBe(false);
    expect(hasChoices(undefined)).toBe(false);
    const withOption = { ...one, capabilities: { ...caps, config: [thinking] } };
    expect(hasChoices(withOption)).toBe(true);
    const modeOnly = { ...one, capabilities: { ...caps, config: [mode] } };
    expect(hasChoices(modeOnly)).toBe(false);
  });
});
