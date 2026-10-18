import * as v from 'valibot';

/**
 * The session-ID rule (ADR 0006): a nonempty ASCII string of letters, digits, underscore and hyphen.
 * IDs are exact keys; no component trims, decodes or normalizes one. The agent mirrors the rule in
 * `provider.ValidSessionID`; `shared/session-id-cases.json` holds the cases both sides are tested with.
 */
export const SESSION_ID_PATTERN = /^[A-Za-z0-9_-]+$/;

export const SessionIdSchema = v.pipe(v.string(), v.regex(SESSION_ID_PATTERN));

export function isSessionId(value: unknown): value is string {
  return v.is(SessionIdSchema, value);
}

/** A workspace snapshot or handoff identifier: short, URL- and path-safe, chosen by the machine that makes it. */
export const OPAQUE_ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;

export const SnapshotIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));
/** An image uploaded to a machine for a prompt, chosen by the core (ADR 0017). */
export const AttachmentIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));
export const HandoffIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));
/** A terminal opened on a machine, chosen by the core (ADR 0026). */
export const TerminalIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));

export function isOpaqueId(value: unknown): value is string {
  return v.is(SnapshotIdSchema, value);
}
