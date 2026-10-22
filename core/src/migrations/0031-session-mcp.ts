import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Sessions that direct sessions (#67, ADR 0034). `api_tokens.session_id` names the session a `session` token was
 * minted for ('' for a scoped token). `session_mcp_settings` is what one session may do to others, resolved from a
 * default when the session first needs it; `session_mcp_defaults` holds those defaults (`device_id` and
 * `project_path` both '' for the global one). `session_provenance` records the chain of sessions that led to the
 * prompt a session is running, so a loop is cut at the next prompt.
 */
export const sessionMcp: Migration = {
  id: 31,
  name: 'session-mcp',
  up: (db: DatabaseSync) => {
    db.exec(`
      ALTER TABLE api_tokens ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
      CREATE INDEX api_tokens_session ON api_tokens(session_id) WHERE session_id != '';
      CREATE TABLE session_mcp_settings(
        session_id TEXT PRIMARY KEY,
        send INTEGER NOT NULL,
        create_sessions INTEGER NOT NULL,
        request_handoff INTEGER NOT NULL,
        visibility TEXT NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE session_mcp_defaults(
        device_id TEXT NOT NULL,
        project_path TEXT NOT NULL,
        send INTEGER NOT NULL,
        create_sessions INTEGER NOT NULL,
        request_handoff INTEGER NOT NULL,
        visibility TEXT NOT NULL,
        updated_at INTEGER NOT NULL,
        PRIMARY KEY(device_id, project_path)
      );
      CREATE TABLE session_provenance(
        session_id TEXT PRIMARY KEY,
        chain TEXT NOT NULL
      );
    `);
  },
};
