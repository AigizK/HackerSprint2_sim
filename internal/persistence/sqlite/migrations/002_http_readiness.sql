CREATE UNIQUE INDEX IF NOT EXISTS idx_worlds_manual_seed
ON worlds(seed) WHERE source = 'manual';

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (2, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
