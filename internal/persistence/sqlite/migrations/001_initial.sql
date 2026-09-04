PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

-- catalog.db contains identities and searchable metadata only. Generated world
-- events are reproduced from seed; mutable streams live in file journals.
CREATE TABLE IF NOT EXISTS worlds (
    world_id TEXT PRIMARY KEY,
    seed INTEGER NOT NULL,
    profile_version TEXT NOT NULL,
    profile_hash TEXT NOT NULL,
    generator_version TEXT NOT NULL,
    schedule_hash TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('generated', 'manual')),
    starts_at TEXT NOT NULL,
    ends_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (seed, profile_hash, generator_version)
);

CREATE TABLE IF NOT EXISTS runs (
    run_id TEXT PRIMARY KEY,
    world_id TEXT NOT NULL REFERENCES worlds(world_id),
    agent_id TEXT NOT NULL,
    agent_version TEXT NOT NULL,
    start_request_id TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ends_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (agent_id, agent_version, start_request_id)
);

CREATE INDEX IF NOT EXISTS idx_runs_agent_created ON runs(agent_id, created_at DESC);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
