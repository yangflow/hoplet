ALTER TABLE events ADD COLUMN agent TEXT;
CREATE INDEX events_agent ON events (session_id, agent, seq) WHERE agent IS NOT NULL;
