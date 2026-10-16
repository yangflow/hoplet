import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Notifications (ADR 0021). `notify_prefs` holds the one row of preferences as JSON. A push subscription is
 * bound to the VAPID key it was made with (`vapid_key`), so a rotated key drops the subscriptions of the old one.
 * `webhooks` holds the event kinds each one receives as a JSON array and no secret: signing secrets live in the secrets file of the data directory. `action_tokens`
 * remembers the IDs of used action links until they would have expired anyway.
 */
export const notifications: Migration = {
  id: 14,
  name: 'notifications',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE notify_prefs(
        id   INTEGER PRIMARY KEY CHECK (id = 1),
        json TEXT NOT NULL
      );
      CREATE TABLE push_subscriptions(
        id         TEXT PRIMARY KEY,
        endpoint   TEXT NOT NULL UNIQUE,
        p256dh     TEXT NOT NULL,
        auth       TEXT NOT NULL,
        label      TEXT NOT NULL,
        vapid_key  TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        last_at    INTEGER,
        last_ok    INTEGER,
        last_error TEXT
      ) WITHOUT ROWID;
      CREATE TABLE webhooks(
        id         TEXT PRIMARY KEY,
        url        TEXT NOT NULL,
        label      TEXT NOT NULL,
        enabled    INTEGER NOT NULL,
        events     TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        last_at    INTEGER,
        last_ok    INTEGER,
        last_error TEXT
      ) WITHOUT ROWID;
      CREATE TABLE action_tokens(
        id         TEXT PRIMARY KEY,
        expires_at INTEGER NOT NULL
      ) WITHOUT ROWID;
      CREATE INDEX action_tokens_by_expiry ON action_tokens(expires_at);
    `);
  },
};
