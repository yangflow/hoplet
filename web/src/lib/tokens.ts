import type {
  TokenAllowEntry,
  TokenCapability,
  TokenCreateRequest,
  TokenGrant,
  TokenInfo,
} from '@hoplet/shared';
import { DEFAULT_TOKEN_DAYS, MAX_TOKEN_ALLOW, MAX_TOKEN_DAYS } from '@hoplet/shared';
import { currentLanguage, t } from './i18n.ts';

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** The most characters of a token's name (the core's limit). */
export const MAX_TOKEN_NAME = 80;

/** The three levels the form offers; `answer` is a separate switch (ADR 0033). */
export const PRESETS = ['read', 'send', 'create'] as const;
export type Preset = (typeof PRESETS)[number];

const PRESET_CAPABILITIES: Record<Preset, readonly TokenCapability[]> = {
  read: ['read'],
  send: ['read', 'send'],
  create: ['read', 'send', 'create'],
};

export function presetName(preset: Preset): string {
  switch (preset) {
    case 'read':
      return t('Read only');
    case 'send':
      return t('May send');
    case 'create':
      return t('May create');
  }
}

export function presetHint(preset: Preset): string {
  switch (preset) {
    case 'read':
      return t('Sees sessions, machines and projects. Cannot change anything.');
    case 'send':
      return t('Reads, and adds to a session: prompts, the queue, interrupt.');
    case 'create':
      return t('Reads, sends, and starts new sessions. Never in Bypass permissions.');
  }
}

export function capabilityText(capability: TokenCapability): string {
  switch (capability) {
    case 'read':
      return t('See sessions, machines and projects, and receive the live stream');
    case 'send':
      return t('Add to a session: prompts, the queue, interrupt');
    case 'create':
      return t('Start sessions, never in Bypass permissions');
    case 'answer':
      return t('Answer permission requests');
  }
}

/** The preset a grant's capabilities amount to: the widest of read, send and create it holds. */
export function presetOf(grant: TokenGrant): Preset {
  if (grant.capabilities.includes('create')) return 'create';
  if (grant.capabilities.includes('send')) return 'send';
  return 'read';
}

export function answersOf(grant: TokenGrant): boolean {
  return grant.capabilities.includes('answer');
}

// -- The form -------------------------------------------------------------------------------------------------------

export const EXPIRY_CHOICES = ['1', '7', '30', '90', 'custom'] as const;
export type ExpiryChoice = (typeof EXPIRY_CHOICES)[number];

export type TokenForm = {
  name: string;
  preset: Preset;
  answer: boolean;
  scope: 'all' | 'only';
  /** The places of "Only these": a machine alone is the whole machine. */
  allow: TokenAllowEntry[];
  expiry: ExpiryChoice;
  /** The days typed under "Custom". */
  customDays: string;
};

export function emptyForm(): TokenForm {
  return {
    name: '',
    preset: 'read',
    answer: false,
    scope: 'all',
    allow: [],
    expiry: String(DEFAULT_TOKEN_DAYS) as ExpiryChoice,
    customDays: '',
  };
}

/** The life in days the form asks for, or null while "Custom" holds something that is not 1 to 365. */
export function daysOf(form: Pick<TokenForm, 'expiry' | 'customDays'>): number | null {
  if (form.expiry !== 'custom') return Number(form.expiry);
  const text = form.customDays.trim();
  if (!/^\d+$/.test(text)) return null;
  const n = Number(text);
  return n >= 1 && n <= MAX_TOKEN_DAYS ? n : null;
}

export type FormCheck = {
  ok: boolean;
  errors: { name?: true; allow?: true; days?: true };
};

export function checkForm(form: TokenForm): FormCheck {
  const errors: FormCheck['errors'] = {};
  const name = form.name.trim();
  if (name === '' || name.length > MAX_TOKEN_NAME) errors.name = true;
  if (form.scope === 'only' && (form.allow.length === 0 || form.allow.length > MAX_TOKEN_ALLOW)) {
    errors.allow = true;
  }
  if (daysOf(form) === null) errors.days = true;
  return { ok: Object.keys(errors).length === 0, errors };
}

/** The capabilities of a form: the preset's, and `answer` only when it is switched on. */
export function capabilitiesOf(form: Pick<TokenForm, 'preset' | 'answer'>): TokenCapability[] {
  return [...PRESET_CAPABILITIES[form.preset], ...(form.answer ? (['answer'] as const) : [])];
}

/** The request a valid form makes; null while the form is not valid. */
export function toRequest(form: TokenForm): TokenCreateRequest | null {
  if (!checkForm(form).ok) return null;
  const days = daysOf(form);
  if (days === null) return null;
  return {
    name: form.name.trim(),
    grant: {
      capabilities: capabilitiesOf(form),
      ...(form.scope === 'only' ? { allow: form.allow.map((e) => ({ ...e })) } : {}),
    },
    expiresInDays: days,
  };
}

// -- The places a token reaches ---------------------------------------------------------------------------------------

/** Whether the list holds the whole machine. */
export function holdsMachine(allow: readonly TokenAllowEntry[], deviceId: string): boolean {
  return allow.some((e) => e.deviceId === deviceId && e.path === undefined);
}

/** Whether the list holds the project (on its own, or through the whole machine). */
export function holdsProject(
  allow: readonly TokenAllowEntry[],
  deviceId: string,
  path: string,
): boolean {
  return allow.some((e) => e.deviceId === deviceId && (e.path === undefined || e.path === path));
}

/** Whether the list holds some of the machine, not all of it. */
export function holdsPartOf(allow: readonly TokenAllowEntry[], deviceId: string): boolean {
  return (
    !holdsMachine(allow, deviceId) &&
    allow.some((e) => e.deviceId === deviceId && e.path !== undefined)
  );
}

/** Ticks a machine whole, or takes all of it out. */
export function toggleMachine(
  allow: readonly TokenAllowEntry[],
  deviceId: string,
): TokenAllowEntry[] {
  const rest = allow.filter((e) => e.deviceId !== deviceId);
  return holdsMachine(allow, deviceId) || holdsPartOf(allow, deviceId)
    ? rest
    : [...rest, { deviceId }];
}

/**
 * Ticks one project or takes it out. Taking a project out of a whole machine keeps the machine's other
 * projects, which `siblings` lists (every project the machine offers).
 */
export function toggleProject(
  allow: readonly TokenAllowEntry[],
  deviceId: string,
  path: string,
  siblings: readonly string[],
): TokenAllowEntry[] {
  if (holdsMachine(allow, deviceId)) {
    return [
      ...allow.filter((e) => e.deviceId !== deviceId),
      ...siblings.filter((p) => p !== path).map((p) => ({ deviceId, path: p })),
    ];
  }
  if (allow.some((e) => e.deviceId === deviceId && e.path === path)) {
    return allow.filter((e) => !(e.deviceId === deviceId && e.path === path));
  }
  return [...allow, { deviceId, path }];
}

export function removeEntry(
  allow: readonly TokenAllowEntry[],
  entry: TokenAllowEntry,
): TokenAllowEntry[] {
  return allow.filter((e) => !(e.deviceId === entry.deviceId && e.path === entry.path));
}

export function baseName(path: string): string {
  const parts = path.split(/[/\\]/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

// -- Times ------------------------------------------------------------------------------------------------------------

function locale(): string {
  return currentLanguage() === 'zh-Hans' ? 'zh-CN' : 'en-GB';
}

/** A date for a person: "22 Dec 2026", "2026年12月22日". */
export function dateText(ms: number): string {
  return new Intl.DateTimeFormat(locale(), {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  }).format(new Date(ms));
}

/** A date with its weekday: "Wed 20 Jan 2027". */
export function dayText(ms: number): string {
  return new Intl.DateTimeFormat(locale(), {
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  }).format(new Date(ms));
}

const put = (text: string, n: number) => t(text).replace('{n}', String(n));

/** How far a moment is from `now`: "4 min ago", "2 h ago", "yesterday", "in 61 days", "tomorrow". */
export function relativeText(ms: number, now: number): string {
  const diff = ms - now;
  const past = diff <= 0;
  const span = Math.abs(diff);
  if (span < MINUTE) return t('just now');
  if (span < HOUR) return put(past ? '{n} min ago' : 'in {n} min', Math.floor(span / MINUTE));
  if (span < DAY) return put(past ? '{n} h ago' : 'in {n} h', Math.floor(span / HOUR));
  const days = Math.floor(span / DAY);
  if (days === 1) return t(past ? 'yesterday' : 'tomorrow');
  return put(past ? '{n} days ago' : 'in {n} days', days);
}

/** A token that expires within this many days is marked. */
export const EXPIRING_DAYS = 7;

export function expiringSoon(token: Pick<TokenInfo, 'state' | 'expiresAt'>, now: number): boolean {
  return token.state === 'active' && token.expiresAt - now <= EXPIRING_DAYS * DAY;
}

/** When a token issued now for `days` days expires. */
export function expiryOf(days: number, now: number): number {
  return now + days * DAY;
}
