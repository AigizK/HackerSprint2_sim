package application

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

func TestAuditHeadersRedactAgentAndAuthorizationCredentials(t *testing.T) {
	headers := http.Header{"Authorization": {"Basic secret"}, "X-Agent-Api-Key": {"agent-secret"}, "X-Trace-ID": {"trace-1"}}
	redacted := http.Header(redactHeaders(headers))
	if redacted["Authorization"][0] != "[REDACTED]" || redacted["X-Agent-Api-Key"][0] != "[REDACTED]" ||
		redacted["X-Trace-ID"][0] != "trace-1" {
		t.Fatalf("redacted headers = %#v", redacted)
	}
}

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
