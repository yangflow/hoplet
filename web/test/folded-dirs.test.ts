// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  dirKey,
  FOLDED_KEY,
  loadFolded,
  saveFolded,
  toggled,
  useFoldedDirs,
} from '../src/screens/cockpit/folded-dirs.ts';

const memory = () => {
  const data = new Map<string, string>();
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
};

describe('folded directories', () => {
  it('remembers a choice per device and directory', () => {
    const store = memory();
    expect(loadFolded(store).size).toBe(0);
    const folded = toggled(new Set(), dirKey('office-mini', '/work/hoplet-app'));
    saveFolded(folded, store);
    const again = loadFolded(store);
    expect(again.has(dirKey('office-mini', '/work/hoplet-app'))).toBe(true);
    expect(again.has(dirKey('studio', '/work/hoplet-app'))).toBe(false);
    expect(toggled(again, dirKey('office-mini', '/work/hoplet-app')).size).toBe(0);
  });

  it('ignores a saved value that is not a list of strings', () => {
    const store = memory();
    store.setItem(FOLDED_KEY, '{"a":1}');
    expect(loadFolded(store).size).toBe(0);
    store.setItem(FOLDED_KEY, 'not json');
    expect(loadFolded(store).size).toBe(0);
    store.setItem(FOLDED_KEY, '["a", 3]');
    expect([...loadFolded(store)]).toEqual(['a']);
  });
});

describe('useFoldedDirs', () => {
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    localStorage.clear();
  });
  afterEach(() => localStorage.clear());

  it('folds, saves in the browser and starts from what was saved', async () => {
    let api: ReturnType<typeof useFoldedDirs> | undefined;
    const Probe = () => {
      api = useFoldedDirs();
      return null;
    };
    const mount = async () => {
      const host = document.createElement('div');
      const root = createRoot(host);
      await act(async () => root.render(createElement(Probe)));
      return root;
    };
    const first = await mount();
    expect(api?.isFolded('studio', '/notes')).toBe(false);
    await act(async () => api?.toggle('studio', '/notes'));
    expect(api?.isFolded('studio', '/notes')).toBe(true);
    expect(JSON.parse(localStorage.getItem(FOLDED_KEY) ?? '[]')).toEqual([
      dirKey('studio', '/notes'),
    ]);
    await act(async () => first.unmount());
    const second = await mount();
    expect(api?.isFolded('studio', '/notes')).toBe(true);
    await act(async () => api?.toggle('studio', '/notes'));
    expect(api?.isFolded('studio', '/notes')).toBe(false);
    await act(async () => second.unmount());
  });
});
