// What Settings keeps in the browser (docs/spec.md#settings): the theme, the language and how text looks. Every
// choice only swaps design tokens and root attributes; the faces are bundled with the web UI and never fetched.
import { currentLanguage, type Language, setLanguage } from './i18n.ts';

export const APPEARANCE_KEY = 'hoplet.appearance';

export const SCHEMES = ['system', 'light', 'dark'] as const;
export type Scheme = (typeof SCHEMES)[number];

export const LANGUAGES = ['en', 'zh-Hans'] as const;

export const UI_FONTS = ['inter', 'system', 'geist'] as const;
export type UiFont = (typeof UI_FONTS)[number];

export const CODE_FONTS = ['jetbrains-mono', 'system', 'geist-mono'] as const;
export type CodeFont = (typeof CODE_FONTS)[number];

export const SIZES = ['small', 'default', 'large'] as const;
export type Size = (typeof SIZES)[number];

export const SEND_MODES = ['queue', 'steer'] as const;
export type SendMode = (typeof SEND_MODES)[number];

export type Appearance = {
  scheme: Scheme;
  /** `null` follows the browser's language. */
  language: Language | null;
  uiFont: UiFont;
  codeFont: CodeFont;
  size: Size;
  /** The conversation column fills the main area instead of stopping at `--layout-column`. */
  wide: boolean;
  /** Ligatures in code and tool output. */
  ligatures: boolean;
  /** What Enter does while a turn runs: queue the message for after the turn, or steer it into the turn. */
  sendMode: SendMode;
  /** Cost is hidden everywhere (docs/spec.md#settings-usage): for plans that are not billed per token. */
  tokensOnly: boolean;
};

export const DEFAULTS: Appearance = {
  scheme: 'system',
  language: null,
  uiFont: 'inter',
  codeFont: 'jetbrains-mono',
  size: 'default',
  wide: false,
  ligatures: true,
  sendMode: 'queue',
  tokensOnly: false,
};

const SYSTEM_UI = '-apple-system, system-ui, "PingFang SC", sans-serif';
const SYSTEM_MONO = 'ui-monospace, "SF Mono", Menlo, monospace';

const UI_STACK: Record<UiFont, string> = {
  inter: `"Inter Variable", ${SYSTEM_UI}`,
  system: SYSTEM_UI,
  geist: `"Geist Variable", ${SYSTEM_UI}`,
};
const MONO_STACK: Record<CodeFont, string> = {
  'jetbrains-mono': `"JetBrains Mono Variable", ${SYSTEM_MONO}`,
  system: SYSTEM_MONO,
  'geist-mono': `"Geist Mono Variable", ${SYSTEM_MONO}`,
};

/** The four sizes scale together; every line height stays as it is. The page title follows the body size. */
const SIZE_PX: Record<Size, { meta: number; ui: number; body: number; code: number }> = {
  small: { meta: 10, ui: 12, body: 14, code: 11.5 },
  default: { meta: 11, ui: 13, body: 15, code: 12.5 },
  large: { meta: 12, ui: 14, body: 16, code: 13.5 },
};

/** The tokens the text choices own. */
export const TEXT_TOKENS = [
  '--font-ui',
  '--font-mono',
  '--text-meta',
  '--text-ui',
  '--text-body',
  '--text-title',
  '--text-code',
] as const;

/**
 * The tokens the text choices override, by name. A choice at its default overrides nothing, so
 * `design/tokens.css` stays the one place the default values live; `--font-prose` follows `--font-ui` there.
 */
export function textTokens(
  a: Pick<Appearance, 'uiFont' | 'codeFont' | 'size'>,
): Record<string, string> {
  const out: Record<string, string> = {};
  if (a.uiFont !== DEFAULTS.uiFont) out['--font-ui'] = UI_STACK[a.uiFont];
  if (a.codeFont !== DEFAULTS.codeFont) out['--font-mono'] = MONO_STACK[a.codeFont];
  if (a.size !== DEFAULTS.size) {
    const z = SIZE_PX[a.size];
    out['--text-meta'] = `${z.meta}px`;
    out['--text-ui'] = `${z.ui}px`;
    out['--text-body'] = `${z.body}px`;
    out['--text-title'] = `${z.body}px`;
    out['--text-code'] = `${z.code}px`;
  }
  return out;
}

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function storage(): Store | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

function oneOf<T extends string>(list: readonly T[], value: unknown, fallback: T): T {
  return list.find((item) => item === value) ?? fallback;
}

/** The saved choices; each one that is missing or not a known value is its default. */
export function loadAppearance(store: Store | null = storage()): Appearance {
  try {
    const raw = store?.getItem(APPEARANCE_KEY);
    if (!raw) return { ...DEFAULTS };
    const saved: unknown = JSON.parse(raw);
    if (typeof saved !== 'object' || saved === null || Array.isArray(saved)) return { ...DEFAULTS };
    const o = saved as Record<string, unknown>;
    return {
      scheme: oneOf(SCHEMES, o.scheme, DEFAULTS.scheme),
      language: LANGUAGES.find((l) => l === o.language) ?? null,
      uiFont: oneOf(UI_FONTS, o.uiFont, DEFAULTS.uiFont),
      codeFont: oneOf(CODE_FONTS, o.codeFont, DEFAULTS.codeFont),
      size: oneOf(SIZES, o.size, DEFAULTS.size),
      wide: typeof o.wide === 'boolean' ? o.wide : DEFAULTS.wide,
      ligatures: typeof o.ligatures === 'boolean' ? o.ligatures : DEFAULTS.ligatures,
      sendMode: oneOf(SEND_MODES, o.sendMode, DEFAULTS.sendMode),
      tokensOnly: typeof o.tokensOnly === 'boolean' ? o.tokensOnly : DEFAULTS.tokensOnly,
    };
  } catch {
    return { ...DEFAULTS };
  }
}

export function saveAppearance(a: Appearance, store: Store | null = storage()): void {
  try {
    store?.setItem(APPEARANCE_KEY, JSON.stringify(a));
  } catch {
    /* a browser that refuses storage forgets the choices on reload */
  }
}

/** The theme: `system` leaves the choice to the browser's colour scheme, the others force one. */
export function applyScheme(scheme: Scheme, root: HTMLElement = document.documentElement): void {
  if (scheme === 'system') delete root.dataset.scheme;
  else root.dataset.scheme = scheme;
}

/** The text choices, full width and ligatures, as tokens and attributes on the root. */
export function applyText(
  a: Pick<Appearance, 'uiFont' | 'codeFont' | 'size' | 'wide' | 'ligatures'>,
  root: HTMLElement = document.documentElement,
): void {
  for (const name of TEXT_TOKENS) root.style.removeProperty(name);
  for (const [name, value] of Object.entries(textTokens(a))) root.style.setProperty(name, value);
  if (a.wide) root.dataset.wide = '';
  else delete root.dataset.wide;
  if (a.ligatures) delete root.dataset.ligatures;
  else root.dataset.ligatures = 'off';
}

/** Puts every saved choice into effect. */
export function applyAppearance(a: Appearance, root: HTMLElement = document.documentElement): void {
  applyScheme(a.scheme, root);
  applyText(a, root);
  setLanguage(a.language);
  root.lang = currentLanguage();
}
