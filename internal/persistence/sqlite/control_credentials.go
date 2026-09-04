package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aigizk/hackersprint2-sim/internal/controlauth"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

// GetOrCreateControlPanelCredentials persists one access pair per run. The
// INSERT's conflict handler ensures concurrent starts return the same winner.
// Secrets stay outside RunRecord, world definitions and the event/audit journal.
func (s *Store) GetOrCreateControlPanelCredentials(ctx context.Context, runID string) (controlauth.Credentials, error) {
	const query = `SELECT username, password FROM run_control_credentials WHERE run_id = ?`
	var credentials controlauth.Credentials
	err := s.db.QueryRowContext(ctx, query, runID).Scan(&credentials.Username, &credentials.Password)
	if err == nil {
		return credentials, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return controlauth.Credentials{}, err
	}
	candidate, err := controlauth.NewCredentials()
	if err != nil {
		return controlauth.Credentials{}, err
	}
	// The SELECT also prevents orphan credentials when foreign keys are disabled.
	_, err = s.db.ExecContext(ctx, `INSERT INTO run_control_credentials(run_id, username, password)
		SELECT run_id, ?, ? FROM runs WHERE run_id = ?
		ON CONFLICT(run_id) DO NOTHING`, candidate.Username, candidate.Password, runID)
	if err != nil {
		return controlauth.Credentials{}, err
	}
	err = s.db.QueryRowContext(ctx, query, runID).Scan(&credentials.Username, &credentials.Password)
	if errors.Is(err, sql.ErrNoRows) {
		return controlauth.Credentials{}, simulation.ErrRunNotFound
	}
	return credentials, err
}
