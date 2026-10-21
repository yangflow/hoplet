/**
 * Structured logging: one JSON object per line on standard output, nothing else writes there. Logs carry
 * IDs, sizes and codes, never content or tokens (docs/threat-model.md), and every line is redacted on the
 * way out: registered secrets, `Bearer` credentials, `token=` and `ticket=` values, and the fields that
 * hold content or credentials.
 */

import { scrubCredentials } from '../scrub/index.ts';

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

/** Shortest secret that is registered; shorter strings would redact ordinary words. */
const MIN_SECRET_LENGTH = 8;
const REDACTED = '[redacted]';

const secrets = new Set<string>();

/** Registers a credential so that it is replaced wherever it appears in a log line. */
export function registerSecret(secret: string): void {
  if (secret.length >= MIN_SECRET_LENGTH) secrets.add(secret);
}

/** Field names whose values are content or credentials, wherever they nest. */
const SENSITIVE_FIELDS = new Set([
  'token',
  'ticket',
  'authorization',
  'secret',
  'password',
  'text',
  'content',
  'prompt',
  'lines',
  'bundle',
  'jsonl',
]);

function scrub(value: unknown, depth = 0): unknown {
  if (Array.isArray(value)) return depth > 6 ? REDACTED : value.map((v) => scrub(v, depth + 1));
  if (value instanceof Error) return scrub({ name: value.name, message: value.message }, depth);
  if (typeof value === 'object' && value !== null) {
    if (depth > 6) return REDACTED;
    return Object.fromEntries(
      Object.entries(value).map(([k, v]) => [
        k,
        SENSITIVE_FIELDS.has(k.toLowerCase()) ? REDACTED : scrub(v, depth + 1),
      ]),
    );
  }
  return value;
}

/** Replaces credentials in an already serialized line. */
export function redact(line: string): string {
  let out = scrubCredentials(line, REDACTED);
  for (const secret of secrets) {
    // A secret may sit inside a JSON string, where it appears escaped.
    const escaped = JSON.stringify(secret).slice(1, -1);
    out = out.split(secret).join(REDACTED);
    if (escaped !== secret) out = out.split(escaped).join(REDACTED);
  }
  return out;
}

export function log(
  level: LogLevel,
  module: string,
  message: string,
  fields: Record<string, unknown> = {},
): void {
  const entry = {
    time: new Date().toISOString(),
    level,
    module,
    message,
    ...(scrub(fields) as Record<string, unknown>),
  };
  console.log(redact(JSON.stringify(entry)));
}
