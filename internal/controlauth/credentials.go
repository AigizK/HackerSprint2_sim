// Package controlauth defines run-scoped access to the simulated control panel.
// These credentials are independent of both the deterministic world and server credentials.
package controlauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
)

type Credentials struct {
	Username string
	Password string
}

// String and GoString keep accidental diagnostic formatting from leaking secrets.
func (Credentials) String() string     { return "[REDACTED control panel credentials]" }
func (c Credentials) GoString() string { return c.String() }

type Repository interface {
	GetOrCreateControlPanelCredentials(context.Context, string) (Credentials, error)
}

func NewCredentials() (Credentials, error) {
	var random [50]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Credentials{}, err
	}
	return Credentials{
		Username: "panel_" + base64.RawURLEncoding.EncodeToString(random[:18]),
		Password: base64.RawURLEncoding.EncodeToString(random[18:]),
	}, nil
}
