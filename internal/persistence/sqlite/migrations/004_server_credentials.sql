CREATE TABLE IF NOT EXISTS server_credentials (
    run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    credential_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    username TEXT NOT NULL,
    password TEXT NOT NULL,
    valid_from TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    issued_at TEXT NOT NULL,
    PRIMARY KEY (run_id, credential_id),
    UNIQUE (run_id, server_id, version)
);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (4, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
