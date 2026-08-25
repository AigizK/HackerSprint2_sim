package simulation

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRunNotFound      = errors.New("run not found")
	ErrRunAlreadyExists = errors.New("run already exists")
)

type RunRecord struct {
	RunID             string
	WorldID           string
	AgentID           string
	AgentVersion      string
	StartRequestID    string
	Status            RunStatus
	StartedAt         time.Time
	EndsAt            time.Time
	CurrentTime       time.Time
	LastRealRequestAt time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type RunRepository interface {
	CreateRun(ctx context.Context, run RunRecord) error
	GetRun(ctx context.Context, runID string) (RunRecord, error)
	FindByStartRequest(ctx context.Context, agentID, agentVersion, requestID string) (RunRecord, error)
	TouchRun(ctx context.Context, runID string, lastRealRequestAt time.Time) error
	ListRuns(ctx context.Context, limit, offset int) ([]RunRecord, error)
	ListRunsByAgent(ctx context.Context, agentID string, limit, offset int) ([]RunRecord, error)
}
