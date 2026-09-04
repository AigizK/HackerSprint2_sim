package controlauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"
)

type ServerCredential struct {
	RunID        string
	CredentialID string
	ServerID     string
	Version      uint64
	Username     string
	Password     string
	ValidFrom    time.Time
	ExpiresAt    time.Time
	IssuedAt     time.Time
}

func (ServerCredential) String() string     { return "[REDACTED server credential]" }
func (c ServerCredential) GoString() string { return c.String() }

type ServerCredentialRepository interface {
	GetOrCreateServerCredential(context.Context, ServerCredential) (ServerCredential, error)
	GetServerCredential(context.Context, string, string) (ServerCredential, error)
}

func NewServerCredential(candidate ServerCredential) (ServerCredential, error) {
	var random [50]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ServerCredential{}, err
	}
	candidate.Username = "ops_" + base64.RawURLEncoding.EncodeToString(random[:18])
	candidate.Password = base64.RawURLEncoding.EncodeToString(random[18:])
	return candidate, nil
}
