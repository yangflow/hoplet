import { describe, expect, it } from 'vitest';
import {
  APPEARANCE_KEY,
  applyScheme,
  applyText,
  DEFAULTS,
  loadAppearance,
  saveAppearance,
  textTokens,
} from '../src/lib/prefs.ts';
import { sectionOf } from '../src/screens/settings/index.tsx';
import { lastSeenText } from '../src/screens/settings/last-seen.ts';

function memory(initial?: string) {
  const data = new Map<string, string>();
  if (initial !== undefined) data.set(APPEARANCE_KEY, initial);
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
}

function root() {
  const style = new Map<string, string>();
  return {
    dataset: {} as Record<string, string | undefined>,
    style: {
      setProperty: (k: string, v: string) => void style.set(k, v),
      removeProperty: (k: string) => void style.delete(k),
    },
    values: style,
  };
}

describe('appearance storage', () => {
  it('defaults when nothing is saved', () => {
    expect(loadAppearance(memory())).toEqual(DEFAULTS);
    expect(loadAppearance(null)).toEqual(DEFAULTS);
  });

  it('round-trips a saved choice', () => {
    const store = memory();
    const a = {
      ...DEFAULTS,
      scheme: 'dark',
      language: 'zh-Hans',
      size: 'large',
      wide: true,
      sendMode: 'steer',
    } as const;
    saveAppearance(a, store);
    expect(loadAppearance(store)).toEqual(a);
  });

  it('keeps the valid fields and defaults the rest of a damaged value', () => {
    const store = memory(JSON.stringify({ scheme: 'dark', uiFont: 'comic', size: 3, wide: 'yes' }));
    expect(loadAppearance(store)).toEqual({ ...DEFAULTS, scheme: 'dark' });
    expect(loadAppearance(memory('not json'))).toEqual(DEFAULTS);
    expect(loadAppearance(memory('[1]'))).toEqual(DEFAULTS);
  });

  it('survives a store that throws', () => {
    const broken = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => {
        throw new Error('denied');
      },
    };
    expect(loadAppearance(broken)).toEqual(DEFAULTS);
    expect(() => saveAppearance(DEFAULTS, broken)).not.toThrow();
  });
});

describe('send mode', () => {
  it('defaults to queue and ignores an unknown value', () => {
    expect(DEFAULTS.sendMode).toBe('queue');
    expect(loadAppearance(memory(JSON.stringify({ sendMode: 'shout' }))).sendMode).toBe('queue');
  });
});

describe('text tokens', () => {
  it('overrides nothing at the defaults', () => {
    expect(textTokens(DEFAULTS)).toEqual({});
  });

  it('maps the sizes and keeps the title on the body size', () => {
    expect(textTokens({ ...DEFAULTS, size: 'small' })).toEqual({
      '--text-meta': '10px',
      '--text-ui': '12px',
      '--text-body': '14px',
      '--text-title': '14px',
      '--text-code': '11.5px',
    });
    expect(textTokens({ ...DEFAULTS, size: 'large' })['--text-code']).toBe('13.5px');
  });

  it('maps the faces', () => {
    const tokens = textTokens({ ...DEFAULTS, uiFont: 'geist', codeFont: 'geist-mono' });
    expect(tokens['--font-ui']).toContain('Geist Variable');
    expect(tokens['--font-mono']).toContain('Geist Mono Variable');
    expect(textTokens({ ...DEFAULTS, uiFont: 'system' })['--font-ui']).toContain('system-ui');
  });
});

describe('applying', () => {
  it('sets and clears the text tokens and attributes', () => {
    const r = root();
    applyText(
      { ...DEFAULTS, size: 'large', wide: true, ligatures: false },
      r as unknown as HTMLElement,
    );
    expect(r.values.get('--text-ui')).toBe('14px');
    expect(r.dataset.wide).toBe('');
    expect(r.dataset.ligatures).toBe('off');
    applyText(DEFAULTS, r as unknown as HTMLElement);
    expect(r.values.size).toBe(0);
    expect(r.dataset.wide).toBeUndefined();
    expect(r.dataset.ligatures).toBeUndefined();
  });

  it('forces a scheme or leaves it to the system', () => {
    const r = root();
    applyScheme('light', r as unknown as HTMLElement);
    expect(r.dataset.scheme).toBe('light');
    applyScheme('system', r as unknown as HTMLElement);
    expect(r.dataset.scheme).toBeUndefined();
  });
});

describe('sections', () => {
  it('falls back to the first section', () => {
    expect(sectionOf('machines')).toBe('machines');
    expect(sectionOf('nope')).toBe('appearance');
    expect(sectionOf(null)).toBe('appearance');
  });
});

describe('last seen', () => {
  const now = Date.UTC(2026, 0, 1, 12);
  it('reads as a relative time', () => {
    expect(lastSeenText({ connected: true }, now)).toBe('now');
    expect(lastSeenText({ connected: false }, now)).toBe('—');
    expect(lastSeenText({ connected: false, lastSeen: now - 5 * 60_000 }, now)).toBe('5 min ago');
    expect(lastSeenText({ connected: false, lastSeen: now - 6 * 3_600_000 }, now)).toBe('6 h ago');
    expect(lastSeenText({ connected: false, lastSeen: now - 3 * 86_400_000 }, now)).toBe('3 d ago');
  });
});
