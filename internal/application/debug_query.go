package application

import (
	"context"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

const MinimumAvailabilitySLO = 0.95

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
	Availability      float64
	SLOEvaluated      bool
	SLOPassed         bool
	ProfitMinor       int64
	LoadError         string
}

type DebugRunOverview struct {
	Run                     simulation.RunRecord
	Overview                simulation.OverviewView
	Economy                 simulation.EconomyView
	Evaluation              generator.WorldEvaluation
	HasBenchmark            bool
	Availability            float64
	MinimumAvailability     float64
	SLOEvaluated            bool
	SLOPassed               bool
	ProfitMinor             int64
	MaximumProfitMinor      int64
	ScoreRatio              float64
	ProfitGapMinor          int64
	ActualAgentRequestCount int
	ActualModeledRealTime   time.Duration
	ActualWallClockRealTime time.Duration
}

type debugCatalog interface {
	simulation.RunRepository
	GetWorld(context.Context, string) (generator.WorldDefinition, error)
}

// DebugQuery exposes durable run projections to the operator UI without going
// through RunManager, whose public API reads intentionally advance time.
type DebugQuery struct {
	catalog debugCatalog
	store   simulation.EventStore
	audit   AuditStore
}

func NewDebugQuery(catalog debugCatalog, store simulation.EventStore, audit AuditStore) *DebugQuery {
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
		summary.Availability, summary.SLOEvaluated, summary.SLOPassed, summary.ProfitMinor = runScoreFacts(summary.Overview, summary.Economy)
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

func (q *DebugQuery) Overview(ctx context.Context, runID string) (DebugRunOverview, error) {
	run, projection, err := q.projection(ctx, runID)
	if err != nil {
		return DebugRunOverview{}, err
	}
	result := DebugRunOverview{Run: run, Overview: projection.Overview(), Economy: projection.Economy(), MinimumAvailability: MinimumAvailabilitySLO}
	result.Availability, result.SLOEvaluated, result.SLOPassed, result.ProfitMinor = runScoreFacts(result.Overview, result.Economy)
	world, err := q.catalog.GetWorld(ctx, run.WorldID)
	if err != nil {
		return DebugRunOverview{}, err
	}
	result.Evaluation = world.Evaluation
	result.MaximumProfitMinor = world.Evaluation.MaximumRevenueMinor - world.Evaluation.MinimumServerCostMinor
	result.HasBenchmark = result.MaximumProfitMinor > 0
	if result.HasBenchmark && result.SLOPassed {
		result.ScoreRatio = float64(result.ProfitMinor) / float64(result.MaximumProfitMinor)
		result.ProfitGapMinor = result.MaximumProfitMinor - result.ProfitMinor
	}
	requests, err := q.audit.LoadAgentRequests(ctx, runID)
	if err != nil {
		return DebugRunOverview{}, err
	}
	result.ActualAgentRequestCount = len(requests)
	result.ActualModeledRealTime = time.Duration(len(requests)) * world.Evaluation.AgentRequestDuration
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

func runScoreFacts(overview simulation.OverviewView, economy simulation.EconomyView) (float64, bool, bool, int64) {
	availability := float64(0)
	if overview.VisitorRequestsTotal > 0 {
		availability = 1 - overview.VisitorErrorRate
	}
	evaluated := overview.RunStatus == string(simulation.RunCompleted) || overview.RunStatus == "failed"
	passed := overview.RunStatus == string(simulation.RunCompleted) && overview.VisitorRequestsTotal > 0 && availability >= MinimumAvailabilitySLO
	profit := economy.RevenueMinor - economy.ServerCostMinor - economy.DeploymentCostMinor
	return availability, evaluated, passed, profit
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
