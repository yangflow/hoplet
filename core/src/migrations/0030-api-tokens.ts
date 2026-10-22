import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * API tokens (#71, ADR 0033): credentials weaker than the host token. `token_hash` is the SHA-256 of the secret,
 * which is never stored; `grant` is the JSON of what the token may do and where. `kind` is `scoped` (minted by the
 * host) or `session` (#67, minted by the core for a running session); both share this table and its lookup.
 * `session_queue.actor_id` is the token that queued a message ('' for the host), so the prompt the queue sends
 * later is recorded under whoever wrote it.
 */
export const apiTokens: Migration = {
  id: 30,
  name: 'api-tokens',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE api_tokens(
        id TEXT PRIMARY KEY,
        kind TEXT NOT NULL,
        name TEXT NOT NULL,
        token_hash TEXT NOT NULL UNIQUE,
        grant TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        revoked_at INTEGER,
        last_used_at INTEGER
      );
      ALTER TABLE session_queue ADD COLUMN actor_id TEXT NOT NULL DEFAULT '';
    `);
  },
};
