package application

import (
	"context"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

func TestHistoryDurationIncludesStartInitializationAndStopsAtCompletion(t *testing.T) {
	ctx := context.Background()
	storage, err := persistence.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	simStart := time.Date(2032, 3, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RegisterManualWorld(ctx, storage.Catalog, ManualWorldInput{Seed: -1, StartsAt: simStart, EndsAt: simStart.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	start := NewStartRunService(storage.Catalog, storage.Journal, nil, storage.Journal)
	realStart := time.Now().UTC().Add(-time.Hour - 2*time.Minute - 3*time.Second)
	calls := 0
	start.now = func() time.Time {
		calls++
		if calls == 1 {
			return realStart
		}
		return realStart.Add(10 * time.Second)
	}
	request := StartRunRequest{Seed: -1, AgentID: "timed-agent", AgentVersion: "v1", RequestID: "start"}
	started, err := start.StartAudited(ctx, request, AgentRequest{AuditID: "start", Method: "POST", Path: "/v2/start"})
	if err != nil {
		t.Fatal(err)
	}
	query := NewDebugQuery(storage.Catalog, storage.Journal, storage.Journal)
	runs, err := query.Runs(ctx, "", 50, 0)
	if err != nil || len(runs) != 1 || runs[0].RealDurationSeconds != nil {
		t.Fatalf("running duration must be absent: %+v, %v", runs, err)
	}
	session, err := simulation.OpenRunSession(ctx, storage.Journal, started.Run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, simulation.AdvanceTime{RequestedDuration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	view, err := storage.Journal.RunSummary(ctx, started.Run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	want := int64(view.RealCompletedAt.Sub(realStart) / time.Second)
	if want < 3723 || !view.RealStartedAt.Equal(realStart) || !started.Run.CreatedAt.After(realStart) {
		t.Fatalf("invalid timing fixture: %+v", view)
	}
	// Later report requests and idempotent starts must not increase the duration.
	if err := storage.Catalog.TouchRun(ctx, started.Run.RunID, realStart.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	start.now = func() time.Time { return realStart.Add(48 * time.Hour) }
	if _, err := start.StartAudited(ctx, request, AgentRequest{AuditID: "retry", Method: "POST", Path: "/v2/start"}); err != nil {
		t.Fatal(err)
	}
	runs, err = query.Runs(ctx, "", 50, 0)
	if err != nil || len(runs) != 1 || runs[0].RealDurationSeconds == nil || *runs[0].RealDurationSeconds != want {
		t.Fatalf("expected fixed duration %d, got %+v, %v", want, runs, err)
	}
}
