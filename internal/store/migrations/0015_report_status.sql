-- report is how things stood in from_session when it reported: done,
-- error or approval; empty for the user's own messages and for reports
-- queued before it existed.
ALTER TABLE queue ADD COLUMN report TEXT NOT NULL DEFAULT '';
