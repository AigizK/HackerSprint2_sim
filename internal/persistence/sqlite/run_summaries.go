package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

func (s *Store) GetRunSummary(ctx context.Context, runID string) (simulation.CachedRunSummary, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT summary_json FROM run_summaries WHERE run_id = ?`, runID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return simulation.CachedRunSummary{}, false, nil
	}
	if err != nil {
		return simulation.CachedRunSummary{}, false, err
	}
	var cached simulation.CachedRunSummary
	if err := json.Unmarshal(data, &cached); err != nil {
		// A damaged cache can always be rebuilt from the authoritative journal.
		return simulation.CachedRunSummary{}, false, nil
	}
	return cached, true, nil
}

func (s *Store) PutRunSummary(ctx context.Context, runID string, summary simulation.CachedRunSummary) error {
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO run_summaries(run_id, summary_json) VALUES (?, ?)
		ON CONFLICT(run_id) DO UPDATE SET summary_json = excluded.summary_json`, runID, data)
	return err
}
