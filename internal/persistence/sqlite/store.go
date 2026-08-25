// Package sqlite stores the small, queryable catalog. Large append-only event
// streams are deliberately kept in the journal package.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
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
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) CreateWorld(ctx context.Context, world generator.WorldDefinition) error {
	if world.WorldID == "" || world.Key.ProfileHash == "" || world.Key.GeneratorVersion == "" ||
		world.ProfileVersion == "" || world.ScheduleHash == "" || !world.EndsAt.After(world.StartsAt) ||
		(world.Source != "generated" && world.Source != "manual") {
		return fmt.Errorf("%w: invalid world definition", generator.ErrInvalidProfile)
	}
	evaluation, err := json.Marshal(world.Evaluation)
	if err != nil {
		return fmt.Errorf("encode world evaluation: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO worlds(
        world_id, seed, profile_version, profile_hash, generator_version,
        schedule_hash, source, starts_at, ends_at, created_at, evaluation_json
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		world.WorldID, world.Key.Seed, world.ProfileVersion, world.Key.ProfileHash, world.Key.GeneratorVersion,
		world.ScheduleHash, world.Source, formatTime(world.StartsAt), formatTime(world.EndsAt), formatTime(world.CreatedAt), evaluation)
	if isUniqueViolation(err) {
		return generator.ErrWorldAlreadyExists
	}
	return err
}

func (s *Store) FindWorld(ctx context.Context, key generator.WorldKey) (generator.WorldDefinition, error) {
	world := generator.WorldDefinition{Key: key}
	var startsAt, endsAt, createdAt string
	var evaluation []byte
	err := s.db.QueryRowContext(ctx, `SELECT world_id, profile_version, schedule_hash, source,
        starts_at, ends_at, created_at, evaluation_json FROM worlds
        WHERE seed = ? AND profile_hash = ? AND generator_version = ?`,
		key.Seed, key.ProfileHash, key.GeneratorVersion).Scan(
		&world.WorldID, &world.ProfileVersion, &world.ScheduleHash, &world.Source,
		&startsAt, &endsAt, &createdAt, &evaluation)
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
	if err := json.Unmarshal(evaluation, &world.Evaluation); err != nil {
		return generator.WorldDefinition{}, fmt.Errorf("decode world evaluation: %w", err)
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
		started_at, ends_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.WorldID, run.AgentID, run.AgentVersion, run.StartRequestID,
		formatTime(run.StartedAt), formatTime(run.EndsAt), formatTime(run.CreatedAt))
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

const runSelect = `SELECT run_id, world_id, agent_id, agent_version, start_request_id,
	started_at, ends_at, created_at FROM runs`

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (simulation.RunRecord, error) {
	var run simulation.RunRecord
	var startedAt, endsAt, createdAt string
	err := row.Scan(&run.RunID, &run.WorldID, &run.AgentID, &run.AgentVersion, &run.StartRequestID,
		&startedAt, &endsAt, &createdAt)
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
	run.UpdatedAt = run.CreatedAt
	return run, err
}

func formatTime(value time.Time) string         { return value.UTC().Format(time.RFC3339Nano) }
func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

var _ generator.WorldRepository = (*Store)(nil)
var _ simulation.RunRepository = (*Store)(nil)
