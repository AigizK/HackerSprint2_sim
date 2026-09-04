// Package sqlite stores the small, queryable catalog. Large append-only event
// streams are deliberately kept in the journal package.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.sql
var initialSchema string

//go:embed migrations/002_http_readiness.sql
var httpReadinessMigration string

//go:embed migrations/003_control_credentials.sql
var controlCredentialsMigration string

//go:embed migrations/004_server_credentials.sql
var serverCredentialsMigration string

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;" + initialSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize sqlite catalog: %w", err)
	}
	var version2 int
	err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&version2)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect sqlite migrations: %w", err)
	}
	if version2 == 0 {
		if err := applyHTTPReadinessMigration(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply sqlite migration 2: %w", err)
		}
	}
	if _, err := db.Exec(controlCredentialsMigration); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply sqlite migration 3: %w", err)
	}
	if _, err := db.Exec(serverCredentialsMigration); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply sqlite migration 4: %w", err)
	}
	return &Store{db: db}, nil
}

func applyHTTPReadinessMigration(db *sql.DB) error {
	statements := make([]string, 0, 3)
	for _, column := range []struct{ table, name, definition string }{
		{"worlds", "definition_json", "definition_json BLOB"},
		{"runs", "last_real_request_at", "last_real_request_at TEXT NOT NULL DEFAULT ''"},
		{"runs", "updated_at", "updated_at TEXT NOT NULL DEFAULT ''"},
	} {
		exists, err := columnExists(db, column.table, column.name)
		if err != nil {
			return err
		}
		if !exists {
			statements = append(statements, "ALTER TABLE "+column.table+" ADD COLUMN "+column.definition)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(httpReadinessMigration); err != nil {
		return err
	}
	return tx.Commit()
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) CreateWorld(ctx context.Context, world generator.WorldDefinition) error {
	if world.WorldID == "" || world.Key.ProfileHash == "" || world.Key.GeneratorVersion == "" ||
		world.ProfileVersion == "" || world.ScheduleHash == "" || !world.EndsAt.After(world.StartsAt) ||
		(world.Source != "generated" && world.Source != "manual") {
		return fmt.Errorf("%w: invalid world definition", generator.ErrInvalidProfile)
	}
	var err error
	var definition []byte
	if world.Source == "manual" {
		definition, err = encodeManualWorld(world)
		if err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO worlds(
        world_id, seed, profile_version, profile_hash, generator_version,
        schedule_hash, source, starts_at, ends_at, created_at, definition_json
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		world.WorldID, world.Key.Seed, world.ProfileVersion, world.Key.ProfileHash, world.Key.GeneratorVersion,
		world.ScheduleHash, world.Source, formatTime(world.StartsAt), formatTime(world.EndsAt), formatTime(world.CreatedAt), definition)
	if isUniqueViolation(err) {
		return generator.ErrWorldAlreadyExists
	}
	return err
}

func (s *Store) FindManualWorld(ctx context.Context, seed int64) (generator.WorldDefinition, error) {
	if seed >= 0 {
		return generator.WorldDefinition{}, generator.ErrWorldNotFound
	}
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT definition_json FROM worlds WHERE seed = ? AND source = 'manual'`, seed).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return generator.WorldDefinition{}, generator.ErrWorldNotFound
	}
	if err != nil {
		return generator.WorldDefinition{}, err
	}
	return decodeManualWorld(payload)
}

func (s *Store) GetWorld(ctx context.Context, worldID string) (generator.WorldDefinition, error) {
	var seed int64
	var profileHash, generatorVersion, source string
	err := s.db.QueryRowContext(ctx, `SELECT seed, profile_hash, generator_version, source FROM worlds WHERE world_id = ?`, worldID).
		Scan(&seed, &profileHash, &generatorVersion, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return generator.WorldDefinition{}, generator.ErrWorldNotFound
	}
	if err != nil {
		return generator.WorldDefinition{}, err
	}
	if source == "manual" {
		return s.FindManualWorld(ctx, seed)
	}
	return s.FindWorld(ctx, generator.WorldKey{Seed: seed, ProfileHash: profileHash, GeneratorVersion: generatorVersion})
}

func (s *Store) FindWorld(ctx context.Context, key generator.WorldKey) (generator.WorldDefinition, error) {
	world := generator.WorldDefinition{Key: key}
	var startsAt, endsAt, createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT world_id, profile_version, schedule_hash, source,
        starts_at, ends_at, created_at FROM worlds
        WHERE seed = ? AND profile_hash = ? AND generator_version = ?`,
		key.Seed, key.ProfileHash, key.GeneratorVersion).Scan(
		&world.WorldID, &world.ProfileVersion, &world.ScheduleHash, &world.Source,
		&startsAt, &endsAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return generator.WorldDefinition{}, generator.ErrWorldNotFound
	}
	if err != nil {
		return generator.WorldDefinition{}, err
	}
	if world.StartsAt, err = parseTime(startsAt); err != nil {
		return generator.WorldDefinition{}, err
	}
	if world.EndsAt, err = parseTime(endsAt); err != nil {
		return generator.WorldDefinition{}, err
	}
	if world.CreatedAt, err = parseTime(createdAt); err != nil {
		return generator.WorldDefinition{}, err
	}
	return world, nil
}

func (s *Store) CreateRun(ctx context.Context, run simulation.RunRecord) error {
	if run.RunID == "" || run.WorldID == "" || run.AgentID == "" || run.AgentVersion == "" ||
		run.StartRequestID == "" || !run.EndsAt.After(run.StartedAt) {
		return fmt.Errorf("invalid run record")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs(
		run_id, world_id, agent_id, agent_version, start_request_id,
		started_at, ends_at, created_at, last_real_request_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.WorldID, run.AgentID, run.AgentVersion, run.StartRequestID,
		formatTime(run.StartedAt), formatTime(run.EndsAt), formatTime(run.CreatedAt),
		formatTime(run.LastRealRequestAt), formatTime(run.UpdatedAt))
	if isUniqueViolation(err) {
		return simulation.ErrRunAlreadyExists
	}
	return err
}

func (s *Store) GetRun(ctx context.Context, runID string) (simulation.RunRecord, error) {
	return scanRun(s.db.QueryRowContext(ctx, runSelect+` WHERE run_id = ?`, runID))
}
func (s *Store) FindByStartRequest(ctx context.Context, agentID, agentVersion, requestID string) (simulation.RunRecord, error) {
	return scanRun(s.db.QueryRowContext(ctx, runSelect+` WHERE agent_id = ? AND agent_version = ? AND start_request_id = ?`, agentID, agentVersion, requestID))
}

func (s *Store) ListRuns(ctx context.Context, limit, offset int) ([]simulation.RunRecord, error) {
	if limit < 1 || limit > 1000 || offset < 0 {
		return nil, fmt.Errorf("invalid run list query")
	}
	return s.listRuns(ctx, runSelect+` ORDER BY created_at DESC, run_id LIMIT ? OFFSET ?`, limit, offset)
}

func (s *Store) ListRunsByAgent(ctx context.Context, agentID string, limit, offset int) ([]simulation.RunRecord, error) {
	if agentID == "" || limit < 1 || limit > 1000 || offset < 0 {
		return nil, fmt.Errorf("invalid run list query")
	}
	return s.listRuns(ctx, runSelect+` WHERE agent_id = ? ORDER BY created_at DESC, run_id LIMIT ? OFFSET ?`, agentID, limit, offset)
}

func (s *Store) listRuns(ctx context.Context, query string, arguments ...any) ([]simulation.RunRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]simulation.RunRecord, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

const runSelect = `SELECT run_id, world_id, agent_id, agent_version, start_request_id,
	started_at, ends_at, created_at, last_real_request_at, updated_at FROM runs`

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (simulation.RunRecord, error) {
	var run simulation.RunRecord
	var startedAt, endsAt, createdAt, lastRealRequestAt, updatedAt string
	err := row.Scan(&run.RunID, &run.WorldID, &run.AgentID, &run.AgentVersion, &run.StartRequestID,
		&startedAt, &endsAt, &createdAt, &lastRealRequestAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return simulation.RunRecord{}, simulation.ErrRunNotFound
	}
	if err != nil {
		return simulation.RunRecord{}, err
	}
	run.Status = simulation.RunNotCreated
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.EndsAt, err = parseTime(endsAt); err != nil {
		return simulation.RunRecord{}, err
	}
	run.CurrentTime = run.StartedAt
	if run.CreatedAt, err = parseTime(createdAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.LastRealRequestAt, err = parseOptionalTime(lastRealRequestAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.UpdatedAt, err = parseOptionalTime(updatedAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.CreatedAt
	}
	return run, err
}

func (s *Store) TouchRun(ctx context.Context, runID string, lastRealRequestAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE runs SET last_real_request_at = ?, updated_at = ? WHERE run_id = ?`,
		formatTime(lastRealRequestAt), formatTime(lastRealRequestAt), runID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return simulation.ErrRunNotFound
	}
	return nil
}

func formatTime(value time.Time) string         { return value.UTC().Format(time.RFC3339Nano) }
func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }
func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return parseTime(value)
}
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

var _ generator.WorldRepository = (*Store)(nil)
var _ simulation.RunRepository = (*Store)(nil)
