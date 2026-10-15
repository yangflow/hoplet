import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The git branch a session works on, as its vendor last recorded it in the session lines (Claude Code's
 * `gitBranch`); empty until a line names one.
 */
export const sessionGitBranch: Migration = {
  id: 10,
  name: 'session-git-branch',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE sessions ADD COLUMN git_branch TEXT NOT NULL DEFAULT ''`);
  },
};
