ALTER TABLE sessions ADD COLUMN plan INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN effort TEXT NOT NULL DEFAULT '';
-- read_only was a permission mode; it is now plan mode over the strictest mode.
UPDATE sessions SET plan = 1, permission = 'confirm' WHERE permission = 'read_only';
