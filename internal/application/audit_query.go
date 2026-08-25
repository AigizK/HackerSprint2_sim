package application

import (
	"context"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

// AuditQuery is for the operator debug UI. Reading it deliberately does not
// advance simulation time and is not part of the public agent API.
type AuditQuery struct {
	catalog simulation.RunRepository
	audit   AuditStore
}

func NewAuditQuery(catalog simulation.RunRepository, audit AuditStore) *AuditQuery {
	return &AuditQuery{catalog: catalog, audit: audit}
}

func (q *AuditQuery) List(ctx context.Context, runID, agentID string) ([]journal.AgentRequestAudit, error) {
	run, err := q.catalog.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if agentID != "" && run.AgentID != agentID {
		return nil, ErrUnauthorized
	}
	return q.audit.LoadAgentRequests(ctx, runID)
}

func (q *AuditQuery) Runs(ctx context.Context, agentID string, limit, offset int) ([]simulation.RunRecord, error) {
	if !ValidAgentID(agentID) || limit < 1 || limit > 1000 || offset < 0 {
		return nil, ErrInvalidRequest
	}
	return q.catalog.ListRunsByAgent(ctx, agentID, limit, offset)
}
