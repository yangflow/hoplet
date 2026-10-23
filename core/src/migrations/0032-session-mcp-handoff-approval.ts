import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Automatic hand-off approval in the session MCP settings (#67, ADR 0034): whether a session's hand-off requests
 * pass without the user's confirmation, and the JSON list of machines they may go to.
 */
export const sessionMcpHandoffApproval: Migration = {
  id: 32,
  name: 'session-mcp-handoff-approval',
  up: (db: DatabaseSync) => {
    db.exec(`
      ALTER TABLE session_mcp_settings ADD COLUMN auto_approve_handoff INTEGER NOT NULL DEFAULT 0;
      ALTER TABLE session_mcp_settings ADD COLUMN auto_approve_machines TEXT NOT NULL DEFAULT '[]';
      ALTER TABLE session_mcp_defaults ADD COLUMN auto_approve_handoff INTEGER NOT NULL DEFAULT 0;
      ALTER TABLE session_mcp_defaults ADD COLUMN auto_approve_machines TEXT NOT NULL DEFAULT '[]';
    `);
  },
};
