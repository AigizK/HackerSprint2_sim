CREATE TABLE IF NOT EXISTS run_control_credentials (
    run_id TEXT PRIMARY KEY REFERENCES runs(run_id) ON DELETE CASCADE,
    username TEXT NOT NULL UNIQUE,
    password TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (3, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
