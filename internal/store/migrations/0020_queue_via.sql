-- via names the channel a queued message came through, such as a voice
-- call; empty for everything else.
ALTER TABLE queue ADD COLUMN via TEXT NOT NULL DEFAULT '';
