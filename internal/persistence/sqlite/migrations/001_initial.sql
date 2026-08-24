PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

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

CREATE TABLE IF NOT EXISTS world_events (
    world_id TEXT NOT NULL REFERENCES worlds(world_id) ON DELETE CASCADE,
    phase TEXT NOT NULL CHECK (phase IN ('bootstrap', 'scheduled')),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    occurs_at TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload_json BLOB NOT NULL,
    PRIMARY KEY (world_id, phase, sequence)
);

CREATE INDEX IF NOT EXISTS idx_world_events_time
    ON world_events(world_id, phase, occurs_at, sequence);

CREATE TABLE IF NOT EXISTS runs (
    run_id TEXT PRIMARY KEY,
    world_id TEXT NOT NULL REFERENCES worlds(world_id),
    agent_id TEXT NOT NULL,
    agent_version TEXT NOT NULL,
    start_request_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('not_created', 'running', 'completed')),
    started_at TEXT NOT NULL,
    ends_at TEXT NOT NULL,
    simulation_time TEXT NOT NULL,
    last_real_request_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (agent_id, agent_version, start_request_id)
);

CREATE INDEX IF NOT EXISTS idx_runs_agent_created
    ON runs(agent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_runs_status_updated
    ON runs(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS run_events (
    run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    event_type TEXT NOT NULL,
    payload_json BLOB NOT NULL,
    persisted_at TEXT NOT NULL,
    PRIMARY KEY (run_id, version)
);

CREATE INDEX IF NOT EXISTS idx_run_events_type
    ON run_events(run_id, event_type, version);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
