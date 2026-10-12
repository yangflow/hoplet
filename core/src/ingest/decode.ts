import { gunzipSync } from 'node:zlib';
import {
  type ClientMessage,
  ClientMessageSchema,
  type ErrorCode,
  isSessionId,
  PROTOCOL_VERSION,
  safeParse,
  WIRE_MESSAGES,
  wireTypes,
} from '@hoplet/shared';

export function decodeLines(msg: { encoding: string; lines: string }): string[] {
  let text: string;
  if (msg.encoding === 'gzip+base64') {
    text = gunzipSync(Buffer.from(msg.lines, 'base64')).toString('utf8');
  } else {
    text = msg.lines;
  }
  const lines = text.split('\n');
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return lines;
}

export type Frame =
  | { ok: true; msg: ClientMessage }
  | { ok: false; code: ErrorCode; message: string; sessionId?: string; close?: boolean };

/**
 * Message types that name a session or could (an unknown type under a session prefix): the ID is checked
 * before the payload is read. A reply that carries no session ID, such as `runtime.start.rejected`, is not
 * session scoped.
 */
const SESSION_SCOPED = /^(?:session|runtime|workspace|handoff)\./;
const WITHOUT_SESSION: ReadonlySet<string> = new Set(
  wireTypes('device-to-core').filter(
    (type) => !('sessionId' in WIRE_MESSAGES[type].schema.entries),
  ),
);

const CLIENT_TYPES: ReadonlySet<string> = new Set(wireTypes('device-to-core'));

/**
 * Turns one text frame from an agent into a typed message, or the error code to answer with. Checks run
 * in this order: JSON, envelope, protocol version, exact session ID, known type, full schema. The ID is
 * checked before the payload is decoded (ADR 0006). A version mismatch on `device.hello` closes the
 * connection.
 */
export function parseFrame(text: string): Frame {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return { ok: false, code: 'invalid-json', message: 'invalid json' };
  }
  if (typeof raw !== 'object' || raw === null) {
    return { ok: false, code: 'invalid-message', message: 'a message is a json object' };
  }
  const head = raw as { v?: unknown; type?: unknown; sessionId?: unknown };
  if (typeof head.type !== 'string' || typeof head.v !== 'number') {
    return { ok: false, code: 'invalid-message', message: 'a message has a type and a version v' };
  }
  if (head.v !== PROTOCOL_VERSION) {
    return {
      ok: false,
      code: 'protocol-version-mismatch',
      message: `protocol version ${head.v} is not supported; this core speaks ${PROTOCOL_VERSION}`,
      close: head.type === 'device.hello',
    };
  }
  if (
    SESSION_SCOPED.test(head.type) &&
    !WITHOUT_SESSION.has(head.type) &&
    !isSessionId(head.sessionId)
  ) {
    return { ok: false, code: 'invalid-session-id', message: 'invalid session identifier' };
  }
  if (!CLIENT_TYPES.has(head.type)) {
    return { ok: false, code: 'unknown-type', message: 'unsupported message' };
  }
  const parsed = safeParse(ClientMessageSchema, raw);
  if (!parsed.success) {
    return { ok: false, code: 'invalid-message', message: `invalid ${head.type} message` };
  }
  return { ok: true, msg: parsed.output };
}
