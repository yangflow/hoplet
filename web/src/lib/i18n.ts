import en from '../i18n/en.json';
import zhHans from '../i18n/zh-Hans.json';

const dictionaries: Record<string, Record<string, string>> = {
  en,
  'zh-Hans': zhHans,
};

export type Language = 'en' | 'zh-Hans';

/** The language the user chose in Settings; `null` follows the browser. */
let chosen: Language | null = null;

export function setLanguage(language: Language | null): void {
  chosen = language;
}

function language(): string {
  // Development builds take ?lang=en|zh-Hans so every state can be captured in both languages.
  const forced =
    import.meta.env.DEV && typeof location !== 'undefined'
      ? new URLSearchParams(location.search).get('lang')
      : null;
  const lang = forced ?? chosen ?? (typeof navigator === 'undefined' ? 'en' : navigator.language);
  return lang.startsWith('zh') ? 'zh-Hans' : 'en';
}

/** The language the interface is in now. */
export function currentLanguage(): Language {
  return language() as Language;
}

/** The English literal is the key; en.json is the source of truth; a missing translation falls back to it. */
export function t(key: string): string {
  return dictionaries[language()]?.[key] ?? key;
}
