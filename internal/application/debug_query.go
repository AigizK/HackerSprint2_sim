package application

import (
	"context"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

// DebugRunSummary uses a compact journal-derived read model. Listing runs never
// advances simulation time or writes an agent request audit record.
type DebugRunSummary struct {
	Run                 simulation.RunRecord
	Seed                int64
	EventCount          uint64
	AgentRequestCount   int
	RealDurationSeconds *int64
	Overview            simulation.RunListOverview
	LoadError           string
}

type DebugRunOverview struct {
	Run                     simulation.RunRecord
	Overview                simulation.OverviewView
	ActualAgentRequestCount int
	ActualWallClockRealTime time.Duration
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
		view, loadErr := q.runSummary(ctx, run.RunID)
		if loadErr != nil {
			summary.LoadError = loadErr.Error()
			result = append(result, summary)
			continue
		}
		summary.Seed, summary.EventCount = view.Seed, view.EventCount
		summary.Overview, summary.AgentRequestCount = view.Overview, view.AgentRequestCount
		startedAt := view.RealStartedAt
		if startedAt.IsZero() {
			startedAt = run.CreatedAt
		}
		if view.Overview.RunStatus == string(simulation.RunCompleted) && !startedAt.IsZero() &&
			!view.RealCompletedAt.IsZero() && !view.RealCompletedAt.Before(startedAt) {
			seconds := int64(view.RealCompletedAt.Sub(startedAt) / time.Second)
			summary.RealDurationSeconds = &seconds
		}
		result = append(result, summary)
	}
	return result, nil
}

func (q *DebugQuery) runSummary(ctx context.Context, runID string) (simulation.RunSummary, error) {
	if store, ok := q.store.(interface {
		RunSummary(context.Context, string) (simulation.RunSummary, error)
	}); ok {
		return store.RunSummary(ctx, runID)
	}
	// Non-journal stores (e.g. the in-memory test store) can use the same reducer.
	records, err := q.store.Load(ctx, runID)
	if err != nil {
		return simulation.RunSummary{}, err
	}
	projection := simulation.NewRunSummaryProjection()
	for _, record := range records {
		projection.Apply(record)
	}
	requests, err := q.audit.LoadAgentRequests(ctx, runID)
	if err != nil {
		return simulation.RunSummary{}, err
	}
	return projection.View(len(requests)), nil
}

func (q *DebugQuery) Overview(ctx context.Context, runID string) (DebugRunOverview, error) {
	run, projection, err := q.projection(ctx, runID)
	if err != nil {
		return DebugRunOverview{}, err
	}
	result := DebugRunOverview{Run: run, Overview: projection.Overview()}
	requests, err := q.audit.LoadAgentRequests(ctx, runID)
	if err != nil {
		return DebugRunOverview{}, err
	}
	result.ActualAgentRequestCount = len(requests)
	if len(requests) > 0 {
		first, last := requests[0].Received.ReceivedAt, requests[0].Received.ReceivedAt
		for _, request := range requests {
			if request.Received.ReceivedAt.Before(first) {
				first = request.Received.ReceivedAt
			}
			completedAt := request.Received.ReceivedAt
			if request.Completed != nil {
				completedAt = request.Completed.CompletedAt
			}
			if completedAt.After(last) {
				last = completedAt
			}
		}
		result.ActualWallClockRealTime = last.Sub(first)
	}
	return result, nil
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
