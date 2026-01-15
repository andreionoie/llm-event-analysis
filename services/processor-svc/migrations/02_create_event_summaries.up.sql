-- 02_create_event_summaries.up.sql
-- Create event summaries table (aggregated and denormalized from the events table).

CREATE TABLE IF NOT EXISTS event_summaries (
    bucket_start  TIMESTAMPTZ NOT NULL PRIMARY KEY,
    bucket_end    TIMESTAMPTZ NOT NULL,
    total_count   INT NOT NULL,
    by_severity   JSONB NOT NULL DEFAULT '{}',
    by_type       JSONB NOT NULL DEFAULT '{}',
    sample_events JSONB NOT NULL DEFAULT '[]'
);
