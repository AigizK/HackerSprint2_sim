package application

import (
	"context"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

// DebugRunSummary is an operator-only, read-only view of one run. Building it
// replays the durable journal but never advances simulation time and never
// writes an agent request audit record.
type DebugRunSummary struct {
	Run               simulation.RunRecord
	Seed              int64
	EventCount        uint64
	AgentRequestCount int
	Overview          simulation.OverviewView
	Economy           simulation.EconomyView
	LoadError         string
}

// DebugQuery exposes durable run projections to the operator UI without going
// through RunManager, whose public API reads intentionally advance time.
type DebugQuery struct {
	catalog simulation.RunRepository
	store   simulation.EventStore
	audit   AuditStore
}

func NewDebugQuery(catalog simulation.RunRepository, store simulation.EventStore, audit AuditStore) *DebugQuery {
	return &DebugQuery{catalog: catalog, store: store, audit: audit}
}

func (q *DebugQuery) Runs(ctx context.Context, agentID string, limit, offset int) ([]DebugRunSummary, error) {
	if limit < 1 || limit > 100 || offset < 0 || (agentID != "" && !ValidAgentID(agentID)) {
		return nil, ErrInvalidRequest
	}
	var (
		runs []simulation.RunRecord
		err  error
	)
	if agentID == "" {
		runs, err = q.catalog.ListRuns(ctx, limit, offset)
	} else {
		runs, err = q.catalog.ListRunsByAgent(ctx, agentID, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	result := make([]DebugRunSummary, 0, len(runs))
	for _, run := range runs {
		summary := DebugRunSummary{Run: run}
		session, loadErr := simulation.OpenRunSession(ctx, q.store, run.RunID)
		if loadErr != nil {
			summary.LoadError = loadErr.Error()
			result = append(result, summary)
			continue
		}
		state := session.State()
		summary.Seed = state.Seed
		summary.EventCount = session.Version()
		projection := session.Projection()
		summary.Overview = projection.Overview()
		summary.Economy = projection.Economy()
		requests, auditErr := q.audit.LoadAgentRequests(ctx, run.RunID)
		if auditErr != nil {
			summary.LoadError = auditErr.Error()
		} else {
			summary.AgentRequestCount = len(requests)
		}
		result = append(result, summary)
	}
	return result, nil
}

func (q *DebugQuery) Overview(ctx context.Context, runID string) (simulation.RunRecord, simulation.OverviewView, error) {
	run, projection, err := q.projection(ctx, runID)
	if err != nil {
		return simulation.RunRecord{}, simulation.OverviewView{}, err
	}
	return run, projection.Overview(), nil
}

func (q *DebugQuery) Logs(ctx context.Context, runID string, query simulation.LogsQuery) (simulation.RunRecord, simulation.LogsView, error) {
	if query.Limit < 1 || query.Limit > 1000 || (query.StatusCode != 0 && query.StatusCode != 200 && query.StatusCode != 500) {
		return simulation.RunRecord{}, simulation.LogsView{}, ErrInvalidRequest
	}
	run, projection, err := q.projection(ctx, runID)
	if err != nil {
		return simulation.RunRecord{}, simulation.LogsView{}, err
	}
	logs, err := projection.Logs(query)
	return run, logs, err
}

func (q *DebugQuery) Economy(ctx context.Context, runID string) (simulation.RunRecord, simulation.EconomyView, error) {
	run, projection, err := q.projection(ctx, runID)
	if err != nil {
		return simulation.RunRecord{}, simulation.EconomyView{}, err
	}
	return run, projection.Economy(), nil
}

func (q *DebugQuery) projection(ctx context.Context, runID string) (simulation.RunRecord, simulation.Projection, error) {
	run, err := q.catalog.GetRun(ctx, runID)
	if err != nil {
		return simulation.RunRecord{}, simulation.Projection{}, err
	}
	session, err := simulation.OpenRunSession(ctx, q.store, runID)
	if err != nil {
		return simulation.RunRecord{}, simulation.Projection{}, err
	}
	return run, session.Projection(), nil
}
