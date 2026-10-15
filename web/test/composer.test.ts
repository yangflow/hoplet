import { sniffImageType } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { refusal, sizeOf, toBase64 } from '../src/screens/cockpit/composer/attachments.ts';
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
