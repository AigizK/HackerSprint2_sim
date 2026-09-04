package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aigizk/hackersprint2-sim/internal/controlauth"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

func (s *Store) GetOrCreateServerCredential(ctx context.Context, wanted controlauth.ServerCredential) (controlauth.ServerCredential, error) {
	if wanted.RunID == "" || wanted.CredentialID == "" || wanted.ServerID == "" || wanted.Version == 0 ||
		wanted.ValidFrom.IsZero() || !wanted.ExpiresAt.After(wanted.ValidFrom) || wanted.IssuedAt.IsZero() {
		return controlauth.ServerCredential{}, fmt.Errorf("invalid server credential metadata")
	}
	if _, err := s.GetRun(ctx, wanted.RunID); err != nil {
		return controlauth.ServerCredential{}, err
	}
	candidate, err := controlauth.NewServerCredential(wanted)
	if err != nil {
		return controlauth.ServerCredential{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO server_credentials(
        run_id, credential_id, server_id, version, username, password, valid_from, expires_at, issued_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.RunID, candidate.CredentialID, candidate.ServerID, candidate.Version,
		candidate.Username, candidate.Password, formatTime(candidate.ValidFrom), formatTime(candidate.ExpiresAt), formatTime(candidate.IssuedAt))
	if err != nil {
		return controlauth.ServerCredential{}, err
	}
	stored, err := s.GetServerCredential(ctx, wanted.RunID, wanted.CredentialID)
	if err != nil {
		return controlauth.ServerCredential{}, err
	}
	if stored.ServerID != wanted.ServerID || stored.Version != wanted.Version || !stored.ValidFrom.Equal(wanted.ValidFrom) ||
		!stored.ExpiresAt.Equal(wanted.ExpiresAt) || !stored.IssuedAt.Equal(wanted.IssuedAt) {
		return controlauth.ServerCredential{}, fmt.Errorf("server credential identity conflict")
	}
	return stored, nil
}

func (s *Store) GetServerCredential(ctx context.Context, runID, credentialID string) (controlauth.ServerCredential, error) {
	var result controlauth.ServerCredential
	var validFrom, expiresAt, issuedAt string
	err := s.db.QueryRowContext(ctx, `SELECT run_id, credential_id, server_id, version, username, password,
        valid_from, expires_at, issued_at FROM server_credentials WHERE run_id = ? AND credential_id = ?`, runID, credentialID).
		Scan(&result.RunID, &result.CredentialID, &result.ServerID, &result.Version, &result.Username, &result.Password,
			&validFrom, &expiresAt, &issuedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return controlauth.ServerCredential{}, simulation.ErrResourceNotFound
	}
	if err != nil {
		return controlauth.ServerCredential{}, err
	}
	result.ValidFrom, err = parseTime(validFrom)
	if err == nil {
		result.ExpiresAt, err = parseTime(expiresAt)
	}
	if err == nil {
		result.IssuedAt, err = parseTime(issuedAt)
	}
	return result, err
}

var _ controlauth.ServerCredentialRepository = (*Store)(nil)
