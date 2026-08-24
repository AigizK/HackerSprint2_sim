package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
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
	// SQLite has one writer. A single pooled connection also keeps :memory:
	// databases consistent and makes transaction ordering explicit.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;" + initialSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize sqlite: %w", err)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO worlds(
        world_id, seed, profile_version, profile_hash, generator_version,
        schedule_hash, source, starts_at, ends_at, created_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		world.WorldID, world.Key.Seed, world.ProfileVersion, world.Key.ProfileHash, world.Key.GeneratorVersion,
		world.ScheduleHash, world.Source, formatTime(world.StartsAt), formatTime(world.EndsAt), formatTime(world.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return generator.ErrWorldAlreadyExists
		}
		return err
	}
	for index, event := range world.Bootstrap {
		eventType, payload, err := simulation.EncodeEvent(event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO world_events(
            world_id, phase, sequence, occurs_at, event_type, payload_json
        ) VALUES (?, 'bootstrap', ?, ?, ?, ?)`, world.WorldID, index+1, formatTime(world.StartsAt), eventType, payload); err != nil {
			return err
		}
	}
	schedule := append(events.EventSchedule(nil), world.Events...)
	sort.SliceStable(schedule, func(i, j int) bool {
		if schedule[i].OccursAt.Equal(schedule[j].OccursAt) {
			return schedule[i].Sequence < schedule[j].Sequence
		}
		return schedule[i].OccursAt.Before(schedule[j].OccursAt)
	})
	for _, scheduled := range schedule {
		eventType, payload, err := simulation.EncodeEvent(scheduled.Event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO world_events(
            world_id, phase, sequence, occurs_at, event_type, payload_json
        ) VALUES (?, 'scheduled', ?, ?, ?, ?)`, world.WorldID, scheduled.Sequence, formatTime(scheduled.OccursAt), eventType, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) FindWorld(ctx context.Context, key generator.WorldKey) (generator.WorldDefinition, error) {
	var world generator.WorldDefinition
	world.Key = key
	var startsAt, endsAt, createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT world_id, profile_version, schedule_hash, source,
        starts_at, ends_at, created_at FROM worlds
        WHERE seed = ? AND profile_hash = ? AND generator_version = ?`,
		key.Seed, key.ProfileHash, key.GeneratorVersion).Scan(
		&world.WorldID, &world.ProfileVersion, &world.ScheduleHash, &world.Source, &startsAt, &endsAt, &createdAt)
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
	rows, err := s.db.QueryContext(ctx, `SELECT phase, sequence, occurs_at, event_type, payload_json
        FROM world_events WHERE world_id = ? ORDER BY phase, occurs_at, sequence`, world.WorldID)
	if err != nil {
		return generator.WorldDefinition{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var scheduled events.ScheduledWorldEvent
		var phase, occursAt, eventType string
		var payload []byte
		if err := rows.Scan(&phase, &scheduled.Sequence, &occursAt, &eventType, &payload); err != nil {
			return generator.WorldDefinition{}, err
		}
		if scheduled.OccursAt, err = parseTime(occursAt); err != nil {
			return generator.WorldDefinition{}, err
		}
		if scheduled.Event, err = simulation.DecodeEvent(eventType, payload); err != nil {
			return generator.WorldDefinition{}, err
		}
		if phase == "bootstrap" {
			world.Bootstrap = append(world.Bootstrap, scheduled.Event)
		} else {
			world.Events = append(world.Events, scheduled)
		}
	}
	return world, rows.Err()
}

func (s *Store) CreateRun(ctx context.Context, run simulation.RunRecord) error {
	if run.RunID == "" || run.WorldID == "" || run.AgentID == "" || run.AgentVersion == "" ||
		run.StartRequestID == "" || !run.EndsAt.After(run.StartedAt) {
		return fmt.Errorf("invalid run record")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs(
        run_id, world_id, agent_id, agent_version, start_request_id, status,
		started_at, ends_at, simulation_time, last_real_request_at, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.WorldID, run.AgentID, run.AgentVersion, run.StartRequestID, run.Status,
		formatTime(run.StartedAt), formatTime(run.EndsAt), formatTime(run.CurrentTime), nullableTime(run.LastRealRequestAt),
		formatTime(run.CreatedAt), formatTime(run.UpdatedAt))
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

const runSelect = `SELECT run_id, world_id, agent_id, agent_version, start_request_id, status,
    started_at, ends_at, simulation_time, last_real_request_at, created_at, updated_at FROM runs`

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (simulation.RunRecord, error) {
	var run simulation.RunRecord
	var status, startedAt, endsAt, currentTime, createdAt, updatedAt string
	var lastReal sql.NullString
	err := row.Scan(&run.RunID, &run.WorldID, &run.AgentID, &run.AgentVersion, &run.StartRequestID, &status,
		&startedAt, &endsAt, &currentTime, &lastReal, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return simulation.RunRecord{}, simulation.ErrRunNotFound
	}
	if err != nil {
		return simulation.RunRecord{}, err
	}
	run.Status = simulation.RunStatus(status)
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.EndsAt, err = parseTime(endsAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.CurrentTime, err = parseTime(currentTime); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.CreatedAt, err = parseTime(createdAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if run.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return simulation.RunRecord{}, err
	}
	if lastReal.Valid {
		run.LastRealRequestAt, err = parseTime(lastReal.String)
	}
	return run, err
}

func (s *Store) Load(ctx context.Context, runID string) ([]simulation.StoredEvent, error) {
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version, event_type, payload_json
        FROM run_events WHERE run_id = ? ORDER BY version`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]simulation.StoredEvent, 0)
	for rows.Next() {
		var record simulation.StoredEvent
		var eventType string
		var payload []byte
		if err := rows.Scan(&record.Version, &eventType, &payload); err != nil {
			return nil, err
		}
		if record.Event, err = simulation.DecodeEvent(eventType, payload); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *Store) Append(ctx context.Context, runID string, expectedVersion uint64, newEvents []events.Event) ([]simulation.StoredEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var endsAtRaw string
	if err := tx.QueryRowContext(ctx, `SELECT ends_at FROM runs WHERE run_id = ?`, runID).Scan(&endsAtRaw); errors.Is(err, sql.ErrNoRows) {
		return nil, simulation.ErrRunNotFound
	} else if err != nil {
		return nil, err
	}
	var current uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM run_events WHERE run_id = ?`, runID).Scan(&current); err != nil {
		return nil, err
	}
	if current != expectedVersion {
		return nil, fmt.Errorf("%w: expected %d, actual %d", simulation.ErrVersionConflict, expectedVersion, current)
	}
	appended := make([]simulation.StoredEvent, 0, len(newEvents))
	status := ""
	currentTime := ""
	endsAt, err := parseTime(endsAtRaw)
	if err != nil {
		return nil, err
	}
	for _, event := range newEvents {
		eventType, payload, err := simulation.EncodeEvent(event)
		if err != nil {
			return nil, err
		}
		current++
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_events(
            run_id, version, event_type, payload_json, persisted_at
        ) VALUES (?, ?, ?, ?, ?)`, runID, current, eventType, payload, formatTime(time.Now().UTC())); err != nil {
			return nil, err
		}
		appended = append(appended, simulation.StoredEvent{Version: current, Event: event})
		switch event := event.(type) {
		case events.WorldCreated:
			status, currentTime = string(simulation.RunRunning), formatTime(event.StartedAt)
		case events.TimeAdvanced:
			currentTime = formatTime(event.To)
			if event.To.Equal(endsAt) {
				status = string(simulation.RunCompleted)
			}
		case events.RunEnded:
			status, currentTime = string(simulation.RunCompleted), formatTime(event.CompletedAt)
		}
	}
	if len(newEvents) > 0 {
		if status != "" {
			_, err = tx.ExecContext(ctx, `UPDATE runs SET status = ?, simulation_time = COALESCE(NULLIF(?, ''), simulation_time), updated_at = ? WHERE run_id = ?`, status, currentTime, formatTime(time.Now().UTC()), runID)
		} else if currentTime != "" {
			_, err = tx.ExecContext(ctx, `UPDATE runs SET simulation_time = ?, updated_at = ? WHERE run_id = ?`, currentTime, formatTime(time.Now().UTC()), runID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE runs SET updated_at = ? WHERE run_id = ?`, formatTime(time.Now().UTC()), runID)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return appended, nil
}

func formatTime(value time.Time) string         { return value.UTC().Format(time.RFC3339Nano) }
func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

var _ generator.WorldRepository = (*Store)(nil)
var _ simulation.RunRepository = (*Store)(nil)
var _ simulation.EventStore = (*Store)(nil)
