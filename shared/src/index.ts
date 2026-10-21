/**
 * The contract between the core, the web UI and the agent: schemas, the types derived from them, and the
 * stable error codes. The Go mirror of `wire/` is `agent/internal/sync/protocol.go`.
 */

/** The one valibot every consumer parses with, so the pinned version is the version in use. */
export { type GenericSchema, type InferOutput, is, parse, safeParse } from 'valibot';
export * from './catalog.ts';
export * from './clone.ts';
export * from './commands.ts';
export * from './conversation.ts';
export * from './deletion.ts';
export * from './errors.ts';
export * from './ids.ts';
export * from './rest/index.ts';
export * from './runtime.ts';
export * from './session.ts';
export * from './templates.ts';
export * from './version.ts';
export * from './wire/index.ts';
