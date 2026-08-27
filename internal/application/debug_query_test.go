package application

import (
	"context"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

func TestDebugQueryListsAllAgentsWithoutAdvancingRuns(t *testing.T) {
	ctx := context.Background()
	storage, err := persistence.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	startsAt := time.Date(2032, 3, 1, 0, 0, 0, 0, time.UTC)
	evaluation := generator.WorldEvaluation{MaximumBalanceMinor: 1_000_000, MaximumRevenueMinor: 900_000, MinimumServerCostMinor: 100_000, AgentRequestCount: 4,
		AgentRequestDuration: 10 * time.Second, MinimumRealTime: 40 * time.Second}
	if _, err := RegisterManualWorld(ctx, storage.Catalog, ManualWorldInput{Seed: -1, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour), Evaluation: evaluation}); err != nil {
		t.Fatal(err)
	}
	start := NewStartRunService(storage.Catalog, storage.Journal, nil, storage.Journal)
	ids := []string{"1234567890abcdefghijklmn", "abcdefghijklmn1234567890"}
	start.newRunID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	for index, agentID := range []string{"agent-one", "agent-two"} {
		_, err := start.Start(ctx, StartRunRequest{Seed: -1, AgentID: agentID, AgentVersion: "v1", RequestID: "start-" + agentID})
		if err != nil {
			t.Fatalf("start run %d: %v", index, err)
		}
	}

	query := NewDebugQuery(storage.Catalog, storage.Journal, storage.Journal)
	runs, err := query.Runs(ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Overview.RunStatus != "running" || runs[1].Overview.RunStatus != "running" {
		t.Fatalf("runs = %#v", runs)
	}
	runID := runs[0].Run.RunID
	versionBefore := runs[0].EventCount
	requestStarted := startsAt.Add(-time.Hour)
	if err := storage.Journal.RecordAgentRequest(ctx, runID, journal.AgentRequestReceived{RequestID: "audit-1", Method: "GET", Path: "/overview", ReceivedAt: requestStarted}); err != nil {
		t.Fatal(err)
	}
	if err := storage.Journal.CompleteAgentRequest(ctx, runID, journal.AgentRequestCompleted{RequestID: "audit-1", StatusCode: 200, CompletedAt: requestStarted.Add(12 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	recordBefore, err := storage.Catalog.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	debugOverview, err := query.Overview(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !debugOverview.HasBenchmark || debugOverview.Evaluation.MaximumBalanceMinor != 1_000_000 || debugOverview.MaximumProfitMinor != 800_000 ||
		debugOverview.MinimumAvailability != MinimumAvailabilitySLO || debugOverview.SLOPassed ||
		debugOverview.ActualAgentRequestCount != 1 || debugOverview.ActualModeledRealTime != 10*time.Second ||
		debugOverview.ActualWallClockRealTime != 12*time.Second {
		t.Fatalf("debug benchmark = %#v", debugOverview)
	}
	if _, _, err := query.Logs(ctx, runID, simulation.LogsQuery{Limit: 200}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := query.Economy(ctx, runID); err != nil {
		t.Fatal(err)
	}
	reopened, err := simulation.OpenRunSession(ctx, storage.Journal, runID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Version() != versionBefore || !reopened.State().Clock.CurrentTime.Equal(startsAt) {
		t.Fatalf("debug reads changed run: version=%d time=%s", reopened.Version(), reopened.State().Clock.CurrentTime)
	}
	record, err := storage.Catalog.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !record.LastRealRequestAt.Equal(recordBefore.LastRealRequestAt) || !record.UpdatedAt.Equal(recordBefore.UpdatedAt) {
		t.Fatalf("debug read touched catalog: before=%#v after=%#v", recordBefore, record)
	}
}

func TestRunScoreRequiresCompletedSLOAndUsesOperatingProfit(t *testing.T) {
	economy := simulation.EconomyView{RevenueMinor: 1_000, ServerCostMinor: 200, DeploymentCostMinor: 50}
	tests := []struct {
		name         string
		overview     simulation.OverviewView
		availability float64
		evaluated    bool
		passed       bool
	}{
		{name: "passed", overview: simulation.OverviewView{RunStatus: "completed", VisitorRequestsTotal: 100, VisitorErrorRate: .04}, availability: .96, evaluated: true, passed: true},
		{name: "below SLO", overview: simulation.OverviewView{RunStatus: "completed", VisitorRequestsTotal: 100, VisitorErrorRate: .06}, availability: .94, evaluated: true},
		{name: "still running", overview: simulation.OverviewView{RunStatus: "running", VisitorRequestsTotal: 100, VisitorErrorRate: .01}, availability: .99},
		{name: "no requests", overview: simulation.OverviewView{RunStatus: "completed"}, evaluated: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			availability, evaluated, passed, profit := runScoreFacts(test.overview, economy)
			if availability != test.availability || evaluated != test.evaluated || passed != test.passed || profit != 750 {
				t.Fatalf("score facts = availability=%v evaluated=%v passed=%v profit=%d", availability, evaluated, passed, profit)
			}
		})
	}
}
