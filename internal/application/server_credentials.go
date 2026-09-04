package application

import (
	"context"
	"crypto/subtle"
	"fmt"
	"sort"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/controlauth"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const serverCredentialLifetime = 7 * 24 * time.Hour

var (
	ErrControlUnauthorized = fmt.Errorf("control panel credentials are invalid")
	ErrTargetUnauthorized  = fmt.Errorf("target server credentials are invalid")
	ErrCredentialsExpired  = fmt.Errorf("target server credentials have expired")
)

type TargetAuth struct {
	Username string
	Password string
}

func ensureServerCredentials(ctx context.Context, repository controlauth.ServerCredentialRepository, session *simulation.RunSession) error {
	if repository == nil {
		return fmt.Errorf("server credential repository is not configured")
	}
	state := session.State()
	if state.Status != simulation.RunRunning {
		return nil
	}
	ids := make([]string, 0, len(state.Servers))
	for id := range state.Servers {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, rawID := range ids {
		serverID := model.ServerID(rawID)
		state = session.State()
		server := state.Servers[serverID]
		var previous simulation.ServerCredentialState
		if server.CredentialID != "" {
			previous = state.ServerCredentials[server.CredentialID]
			if previous.CredentialID != "" && state.Clock.CurrentTime.Before(previous.ExpiresAt) {
				if _, err := repository.GetOrCreateServerCredential(ctx, controlauth.ServerCredential{RunID: state.RunID,
					CredentialID: string(previous.CredentialID), ServerID: string(serverID), Version: previous.Version,
					ValidFrom: previous.ValidFrom, ExpiresAt: previous.ExpiresAt, IssuedAt: previous.IssuedAt}); err != nil {
					return err
				}
				continue
			}
		}
		version := uint64(1)
		if previous.Version > 0 {
			version = previous.Version + 1
		}
		validFrom := state.Clock.CurrentTime
		expiresAt := validFrom.Add(serverCredentialLifetime)
		if state.Clock.EndsAt.Before(expiresAt) {
			expiresAt = state.Clock.EndsAt
		}
		if !expiresAt.After(validFrom) {
			continue
		}
		credentialID := simulation.ServerCredentialID(state.RunID, serverID, version)
		credential, err := repository.GetOrCreateServerCredential(ctx, controlauth.ServerCredential{RunID: state.RunID,
			CredentialID: string(credentialID), ServerID: string(serverID), Version: version, ValidFrom: validFrom,
			ExpiresAt: expiresAt, IssuedAt: state.Clock.CurrentTime})
		if err != nil {
			return err
		}
		if _, err := session.Execute(ctx, simulation.IssueServerCredential{CredentialID: model.CredentialID(credential.CredentialID),
			ServerID: serverID, Version: version, SupersedesCredentialID: previous.CredentialID, ValidFrom: validFrom,
			ExpiresAt: expiresAt, MessageID: model.MessageID("message-" + credential.CredentialID)}); err != nil {
			return err
		}
	}
	return nil
}

func authenticateTarget(ctx context.Context, repository controlauth.ServerCredentialRepository, state simulation.State, serverID model.ServerID, auth TargetAuth) error {
	server, exists := state.Servers[serverID]
	if !exists {
		return simulation.ErrResourceNotFound
	}
	if auth.Username == "" || auth.Password == "" || server.CredentialID == "" {
		return ErrTargetUnauthorized
	}
	now := state.Clock.CurrentTime
	for _, metadata := range state.ServerCredentials {
		if metadata.ServerID != serverID || metadata.IssuedAt.After(now) {
			continue
		}
		credential, err := repository.GetServerCredential(ctx, state.RunID, string(metadata.CredentialID))
		if err != nil {
			return err
		}
		usernameOK := subtle.ConstantTimeCompare([]byte(auth.Username), []byte(credential.Username))
		passwordOK := subtle.ConstantTimeCompare([]byte(auth.Password), []byte(credential.Password))
		if usernameOK&passwordOK != 1 {
			continue
		}
		if !now.Before(metadata.ExpiresAt) {
			return ErrCredentialsExpired
		}
		if now.Before(metadata.ValidFrom) || metadata.CredentialID != server.CredentialID {
			return ErrTargetUnauthorized
		}
		return nil
	}
	return ErrTargetUnauthorized
}
