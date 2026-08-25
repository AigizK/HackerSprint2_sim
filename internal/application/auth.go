package application

import (
	"crypto/subtle"
	"errors"
)

var ErrUnauthorized = errors.New("unauthorized agent")

type AgentAuthorizer interface {
	Authorize(apiKey, agentID string) error
}

type StaticAgentAuthorizer struct{ keys map[string]string }

func NewStaticAgentAuthorizer(agentKeys map[string]string) *StaticAgentAuthorizer {
	copy := make(map[string]string, len(agentKeys))
	for agentID, key := range agentKeys {
		copy[agentID] = key
	}
	return &StaticAgentAuthorizer{keys: copy}
}

func (a *StaticAgentAuthorizer) Authorize(apiKey, agentID string) error {
	expected, exists := a.keys[agentID]
	if !exists || len(apiKey) != len(expected) || subtle.ConstantTimeCompare([]byte(apiKey), []byte(expected)) != 1 {
		return ErrUnauthorized
	}
	return nil
}
