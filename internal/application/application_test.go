package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestManualStartIsDurableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := sqlite.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	journalStore, err := journal.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	startsAt := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, seed := range []int64{-1, -2} {
		if _, err := RegisterManualWorld(ctx, catalog, ManualWorldInput{Seed: seed, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	service := NewStartRunService(catalog, journalStore, nil)
	service.now = func() time.Time { return startsAt.Add(-time.Hour) }
	service.newRunID = func() (string, error) { return "1234567890abcdefghijklmn", nil }
	request := StartRunRequest{Seed: -1, AgentID: "agent", AgentVersion: "v1", RequestID: "start-1"}
	first, err := service.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.State.Status != simulation.RunRunning || first.State.Seed != -1 {
		t.Fatalf("first = %#v", first)
	}
	second, err := service.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Run.RunID != first.Run.RunID {
		t.Fatalf("second = %#v", second)
	}
	request.Seed = -2
	if _, err := service.Start(ctx, request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict = %v", err)
	}

	reopened, err := simulation.OpenRunSession(ctx, journalStore, first.Run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State().Seed != -1 {
		t.Fatalf("reopened seed = %d", reopened.State().Seed)
	}
}

func TestRunServiceCoversEveryOpenAPIOperationAfterStart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := sqlite.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	journalStore, err := journal.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	simAt := time.Date(2031, 2, 1, 0, 0, 0, 0, time.UTC)
	realAt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	fixText := "FIX-BUG"
	fixHash := sha256.Sum256([]byte(fixText))
	bootstrap := []events.Event{
		events.PageConfigured{Page: model.PageProductList, LoadUnits: 10, HoldDuration: time.Minute, BaseLatency: 20 * time.Millisecond, ConfiguredAt: simAt},
		events.PageConfigured{Page: model.PageProduct, LoadUnits: 10, HoldDuration: time.Minute, BaseLatency: 30 * time.Millisecond, ConfiguredAt: simAt},
		events.PageConfigured{Page: model.PagePurchase, LoadUnits: 10, HoldDuration: time.Minute, BaseLatency: 40 * time.Millisecond, ConfiguredAt: simAt},
		events.InfrastructureConfigured{ServerProvisioningDuration: time.Minute, ConfiguredAt: simAt},
		events.EconomyConfigured{Currency: "USD", InitialBalanceMinor: 1_000_000, StopRunOnNegativeBalance: true, ServerBillingPeriod: time.Hour, ConfiguredAt: simAt},
		events.ServerProvisioningStarted{OperationID: "initial", ServerID: "server-1", CapacityUnits: 100, CostPerHourMinor: 100, StartedAt: simAt, ReadyAt: simAt},
		events.ServerActivated{OperationID: "initial", ServerID: "server-1", ActivatedAt: simAt},
		events.ProductAdded{ProductID: "product-1", Name: "Product", PriceMinor: 1000, ViewProbabilityPPM: 1_000_000, PurchaseProbabilityPPM: 1_000_000, AddedAt: simAt},
		events.PageBugActivated{BugID: "bug-1", Page: model.PageProduct, ProductID: "product-1", FailureProbabilityPPM: 1_000_000, FixMessage: fixText, FixMessageHash: fmt.Sprintf("%x", fixHash), ActivatedAt: simAt},
		events.DeploymentDefined{DeploymentID: "deployment-1", Sequence: 1, Name: "Cache", Description: "Enable cache", Duration: 5 * time.Minute, DefinedAt: simAt},
		events.DeploymentUnlocked{DeploymentID: "deployment-1", UnlockedAt: simAt},
	}
	worldEvents := events.EventSchedule{{Sequence: 1, OccursAt: simAt.Add(time.Minute), Event: events.VisitorArrived{VisitorID: "scheduled-visitor", ArrivedAt: simAt.Add(time.Minute)}}}
	if _, err := RegisterManualWorld(ctx, catalog, ManualWorldInput{Seed: -1, StartsAt: simAt, EndsAt: simAt.Add(time.Hour), Bootstrap: bootstrap, Events: worldEvents}); err != nil {
		t.Fatal(err)
	}
	start := NewStartRunService(catalog, journalStore, nil, journalStore)
	start.now = func() time.Time { return realAt }
	start.newRunID = func() (string, error) { return "1234567890abcdefghijklmn", nil }
	started, err := start.StartAudited(ctx, StartRunRequest{Seed: -1, AgentID: "agent", AgentVersion: "v1", RequestID: "start"}, AgentRequest{AuditID: "audit-start", Method: "POST", Path: "/v1/start"})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := catalog.ListRunsByAgent(ctx, "agent", 100, 0)
	if err != nil || len(runs) != 1 || runs[0].RunID != started.Run.RunID {
		t.Fatalf("agent runs=%#v err=%v", runs, err)
	}
	manager := NewRunManager(catalog, journalStore, journalStore, 2)
	manager.now = func() time.Time { return realAt }
	service := NewRunService(manager)
	runID := started.Run.RunID
	auditIndex := 0
	req := func(method, path string, command model.CommandID) AgentRequest {
		auditIndex++
		return AgentRequest{AuditID: fmt.Sprintf("audit-%d", auditIndex), AgentID: "agent", Method: method, Path: path, CommandID: command}
	}
	assert := func(name string, response ApplicationResponse, err error) {
		t.Helper()
		if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("%s: response=%#v err=%v", name, response, err)
		}
	}
	response, err := service.Overview(ctx, runID, req("GET", "/overview", ""))
	assert("overview", response, err)
	response, err = service.Metrics(ctx, runID, req("GET", "/metrics", ""), simulation.MetricsQuery{})
	assert("metrics", response, err)
	response, err = service.Resources(ctx, runID, req("GET", "/resources", ""))
	assert("resources", response, err)
	if _, err = service.ScaleBackend(ctx, runID, req("PUT", "/resources/backend", "scale-zero"), 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("scale to zero error = %v, want %v", err, ErrInvalidRequest)
	}
	response, err = service.ScaleBackend(ctx, runID, req("PUT", "/resources/backend", "scale-1"), 1)
	assert("scale", response, err)
	response, err = service.ApplyFix(ctx, runID, req("POST", "/fixes", "fix-1"), fixText)
	assert("fix", response, err)
	response, err = service.Deployments(ctx, runID, req("GET", "/deployments", ""))
	assert("deployments", response, err)
	response, err = service.StartDeployment(ctx, runID, req("POST", "/deployments", "deploy-1"), "deployment-1")
	assert("start deployment", response, err)
	deploymentOperation := StableOperationID(runID, "deploy-1", "deployment")
	response, err = service.Operation(ctx, runID, req("GET", "/operations", ""), deploymentOperation)
	assert("operation", response, err)
	advance := req("POST", "/time/advance", "advance-1")
	advance.RequestedAdvance = 5 * time.Minute
	response, err = service.AdvanceTime(ctx, runID, advance)
	assert("advance", response, err)
	advanceResult, ok := response.Value.(AdvanceTimeResult)
	if !ok || !advanceResult.PreviousSimulationTime.Equal(simAt) || advanceResult.RequestedDuration != 5*time.Minute ||
		advanceResult.ProcessedEvents == 0 || advanceResult.NewLogs == 0 || advanceResult.LogsCursor == "" ||
		!advanceResult.Clock.SimulationTime.Equal(simAt.Add(5*time.Minute)) {
		t.Fatalf("advance result = %#v", response.Value)
	}
	duplicateAdvance := advance
	duplicateAdvance.AuditID = "audit-advance-duplicate"
	response, err = service.AdvanceTime(ctx, runID, duplicateAdvance)
	assert("duplicate advance", response, err)
	duplicateResult, ok := response.Value.(AdvanceTimeResult)
	if !ok || duplicateResult.ProcessedEvents != 0 || duplicateResult.NewLogs != 0 ||
		!duplicateResult.PreviousSimulationTime.Equal(simAt.Add(5*time.Minute)) {
		t.Fatalf("duplicate advance result = %#v", response.Value)
	}
	response, err = service.Probe(ctx, runID, req("POST", "/probes", "probe-1"), model.PageProduct, "product-1")
	assert("probe", response, err)
	response, err = service.Logs(ctx, runID, req("GET", "/logs", ""), simulation.LogsQuery{})
	assert("logs", response, err)
	response, err = service.Economy(ctx, runID, req("GET", "/economy", ""))
	assert("economy", response, err)
}

func TestOpenAPIValidationErrorsHaveSpecificCodes(t *testing.T) {
	start := NewStartRunService(nil, nil, nil)
	if _, err := start.Start(context.Background(), StartRunRequest{Seed: 0, AgentID: "agent", AgentVersion: "v1", RequestID: "start"}); !errors.Is(err, ErrInvalidSeed) {
		t.Fatalf("zero seed error = %v", err)
	}
	if got := ClassifyError(ErrInvalidSeed); got.Status != 400 || got.Code != "INVALID_SEED" {
		t.Fatalf("seed API error = %#v", got)
	}

	service := NewRunService(nil)
	_, err := service.AdvanceTime(context.Background(), "run", AgentRequest{CommandID: "advance", RequestedAdvance: time.Minute})
	if !errors.Is(err, ErrMinimumAdvance) {
		t.Fatalf("minimum advance error = %v", err)
	}
	if got := ClassifyError(ErrMinimumAdvance); got.Status != 400 || got.Code != "MINIMUM_ADVANCE_IS_300_SECONDS" {
		t.Fatalf("advance API error = %#v", got)
	}
}

func TestRunManagerAdvancesClockAuditsAndChecksOwnership(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := sqlite.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	journalStore, err := journal.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	startsAt := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	world, err := RegisterManualWorld(ctx, catalog, ManualWorldInput{Seed: -1, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour),
		Events: events.EventSchedule{{Sequence: 1, OccursAt: startsAt.Add(4 * time.Minute), Event: events.VisitorArrived{VisitorID: "visitor", ArrivedAt: startsAt.Add(4 * time.Minute)}}}})
	if err != nil {
		t.Fatal(err)
	}
	realStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	run := simulation.RunRecord{RunID: "1234567890abcdefghijklmn", WorldID: world.WorldID, AgentID: "agent", AgentVersion: "v1", StartRequestID: "start",
		StartedAt: startsAt, EndsAt: startsAt.Add(time.Hour), CurrentTime: startsAt, LastRealRequestAt: realStart, CreatedAt: realStart, UpdatedAt: realStart}
	if err := catalog.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := journalStore.Append(ctx, run.RunID, 0, []events.Event{events.WorldCreated{RunID: run.RunID, Seed: -1, StartedAt: startsAt, EndsAt: startsAt.Add(time.Hour)}, events.WorldScheduleCreated{Schedule: world.Events, CreatedAt: startsAt}}); err != nil {
		t.Fatal(err)
	}

	manager := NewRunManager(catalog, journalStore, journalStore, 1)
	manager.now = func() time.Time { return realStart.Add(5 * time.Minute) }
	response, err := manager.Handle(ctx, run.RunID, AgentRequest{AuditID: "audit-1", AgentID: "agent", Method: "GET", Path: "/overview"},
		func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
			if !run.State.Clock.CurrentTime.Equal(startsAt.Add(5*time.Minute)) || run.Clock.AppliedAdvance != 5*time.Minute {
				t.Fatalf("clock = %#v", run.Clock)
			}
			return ApplicationResponse{StatusCode: 200}, nil
		})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	audits, err := journalStore.LoadAgentRequests(ctx, run.RunID)
	if err != nil || len(audits) != 1 || audits[0].Completed == nil {
		t.Fatalf("audits=%#v err=%v", audits, err)
	}
	if _, err := manager.Handle(ctx, run.RunID, AgentRequest{AuditID: "audit-2", AgentID: "other", Method: "GET", Path: "/overview"}, func(context.Context, *RunContext) (ApplicationResponse, error) { return ApplicationResponse{}, nil }); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ownership = %v", err)
	}
}
