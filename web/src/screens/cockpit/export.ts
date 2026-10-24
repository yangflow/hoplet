import {
  EXPORT_MASK_MAX_BYTES,
  EXPORT_MASK_MAX_ENTRIES,
  EXPORT_MASK_MIN_CHARS,
  EXPORT_SUMMARY_MAX_BYTES,
  type ExportConversation,
} from '@hoplet/shared';
import { t } from '../../lib/i18n.ts';

/** What the export dialog asks the core for (docs/api.md#export). */
export type ExportOptions = {
  conversation: ExportConversation;
  /** The summary's text, or null without one. */
  summary: string | null;
  changes: boolean;
  commands: boolean;
  machines: boolean;
  /** The user's own strings to mask, one per line as typed. */
  mask: string;
};

export const DEFAULT_EXPORT_OPTIONS: ExportOptions = {
  conversation: 'all',
  summary: '',
  changes: true,
  commands: true,
  machines: true,
  mask: '',
};

const encoder = new TextEncoder();

/** `text` cut to at most `max` bytes of UTF-8, at a character. */
export function cutBytes(text: string, max: number): string {
  if (encoder.encode(text).length <= max) return text;
  let out = '';
  let used = 0;
  for (const ch of text) {
    const n = encoder.encode(ch).length;
    if (used + n > max) break;
    out += ch;
    used += n;
  }
  return out;
}

/** The strings to mask: the non-empty lines, each long enough, at most as many as the core takes. */
export function maskList(mask: string): string[] {
  return mask
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => [...s].length >= EXPORT_MASK_MIN_CHARS)
    .slice(0, EXPORT_MASK_MAX_ENTRIES);
}

/** The query of `GET /api/sessions/:id/export` for a set of options. */
export function exportQuery(o: ExportOptions, tz?: string): Record<string, string> {
  const q: Record<string, string> = { conversation: o.conversation };
  if (o.summary !== null && o.summary.trim() !== '') {
    q.summary = cutBytes(o.summary.trim(), EXPORT_SUMMARY_MAX_BYTES);
  }
  if (o.changes) q.changes = 'true';
  if (o.commands) q.commands = 'true';
  if (o.machines) q.machines = 'true';
  const mask = maskList(o.mask);
  if (mask.length > 0) q.mask = cutBytes(mask.join('\n'), EXPORT_MASK_MAX_BYTES);
  if (tz) q.tz = tz;
  return q;
}

/** One kind of masked value: how many, and in which turns (0 for the parts outside the conversation). */
export type MaskedKind = { kind: string; count: number; turns: number[] };

/** What an export's text so far says: its turns, where it is, and what it masked and elided. */
export type ExportScan = {
  /** The turns the file holds, from its front matter; null before the front matter is complete. */
  total: number | null;
  /** The last turn written so far. */
  turn: number;
  bytes: number;
  masked: number;
  kinds: MaskedKind[];
  elided: number;
};

const MARKER = /\[redacted:([a-z-]+)\]/g;

export function scanExport(text: string): ExportScan {
  const total = /^turns: (\d+)$/m.exec(text.slice(0, 4096));
  const byKind = new Map<string, MaskedKind>();
  let turn = 0;
  let masked = 0;
  let elided = 0;
  // The review warning names the marker's form; it is not a masked value.
  const body = text.replace('`[redacted:<kind>]`', '');
  for (const line of body.split('\n')) {
    const head = /^### Turn (\d+)/.exec(line);
    if (head) turn = Number(head[1]);
    if (line.includes('- Output elided: ')) elided++;
    for (const m of line.matchAll(MARKER)) {
      const kind = m[1] as string;
      const entry = byKind.get(kind) ?? { kind, count: 0, turns: [] };
      entry.count++;
      if (turn > 0 && !entry.turns.includes(turn)) entry.turns.push(turn);
      byKind.set(kind, entry);
      masked++;
    }
  }
  return {
    total: total ? Number(total[1]) : null,
    turn,
    bytes: encoder.encode(text).length,
    masked,
    kinds: [...byKind.values()].sort((a, b) => b.count - a.count),
    elided,
  };
}

/** The name of a kind of masked value. */
export function kindLabel(kind: string): string {
  switch (kind) {
    case 'private-key':
      return t('Private key');
    case 'url-credential':
      return t('Credential in a URL');
    case 'bearer-token':
      return t('Bearer token');
    case 'basic-auth':
      return t('Basic credential');
    case 'query-credential':
      return t('Credential in a query');
    case 'api-key':
      return t('API key');
    case 'github-token':
      return t('GitHub token');
    case 'hoplet-token':
      return t('Hoplet token');
    case 'slack-token':
      return t('Slack token');
    case 'aws-key':
      return t('AWS key');
    case 'jwt':
      return t('JSON Web Token');
    case 'named-secret':
      return t('Named secret');
    case 'custom':
      return t('Your strings');
    default:
      return kind;
  }
}

export function sizeText(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`;
}

/** The time zone the browser shows times in, for the file's times. */
export function localTimeZone(): string | undefined {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;
  } catch {
    return undefined;
  }
}
