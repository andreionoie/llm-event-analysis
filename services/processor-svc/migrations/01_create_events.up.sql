-- 01_create_events.up.sql
-- Create events table and indexes.

CREATE TABLE IF NOT EXISTS events (
    id          TEXT PRIMARY KEY,
    timestamp   TIMESTAMPTZ NOT NULL,
    source      TEXT NOT NULL,
    severity    SMALLINT NOT NULL,
    event_type  TEXT NOT NULL,
    payload     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_events_severity ON events(severity) WHERE severity >= 2;
CREATE INDEX IF NOT EXISTS idx_events_type ON events(event_type);
