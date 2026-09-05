package journal

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

func TestRunTimingSurvivesRetriesRestartAndCacheUpgrade(t *testing.T) {
	ctx, root, runID := context.Background(), t.TempDir(), "timed"
	store := summaryTestStore(t, root)
	simulationStart := summaryTestWorld(t, store, runID)
	realStart := time.Now().UTC().Add(-27*time.Hour - 2*time.Minute - 3*time.Second)
	if err := store.RecordAgentRequest(ctx, runID, AgentRequestReceived{
		RequestID: "start", Method: "POST", Path: "/v2/start", ReceivedAt: realStart,
	}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if _, err := store.Append(ctx, runID, 1, []events.Event{
		events.RunEnded{Reason: "world_completed", CompletedAt: simulationStart.Add(24 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	view, err := store.RunSummary(ctx, runID)
	if err != nil || !view.RealStartedAt.Equal(realStart) || view.RealCompletedAt.Before(before) || view.RealCompletedAt.After(time.Now()) {
		t.Fatalf("expected real timestamps, got %+v, %v", view, err)
	}
	for _, request := range []AgentRequestReceived{
		{RequestID: "retry", Method: "POST", Path: "/v2/start", ReceivedAt: realStart.Add(48 * time.Hour)},
		{RequestID: "report", Method: "GET", Path: "/v2/runs/timed/overview", ReceivedAt: realStart.Add(49 * time.Hour)},
	} {
		if err := store.RecordAgentRequest(ctx, runID, request); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteAgentRequest(ctx, runID, AgentRequestCompleted{
			RequestID: request.RequestID, StatusCode: 200, CompletedAt: request.ReceivedAt.Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Upgrade an unchanged v1 cache: timing must be recovered from the old journal.
	cached, _, err := store.summaries.GetRunSummary(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	cached.FormatVersion = 1
	cached.Summary.RealStartedAt, cached.Summary.RealCompletedAt = time.Time{}, time.Time{}
	if err := store.summaries.PutRunSummary(ctx, runID, cached); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store = summaryTestStore(t, root)
		got, err := store.RunSummary(ctx, runID)
		if err != nil || !got.RealStartedAt.Equal(view.RealStartedAt) || !got.RealCompletedAt.Equal(view.RealCompletedAt) {
			t.Fatalf("timing changed after restart/retry: %+v, %v", got, err)
		}
	}
	if len(store.runs) != 0 {
		t.Fatal("upgraded timing should be served from the persisted summary")
	}
}

func TestRunTimingUsesCommittedMultipartTimestamp(t *testing.T) {
	ctx, root, runID := context.Background(), t.TempDir(), "multipart-timed"
	store := summaryTestStore(t, root)
	simTime := summaryTestWorld(t, store, runID)
	stream := store.runs[runID]
	realStart := time.Now().UTC().Add(-time.Hour)
	begin := make([]byte, 16)
	binary.BigEndian.PutUint64(begin[:8], 2)
	binary.BigEndian.PutUint64(begin[8:], 1)
	part, err := encodeDomainBatch(2, []events.Event{events.RunEnded{Reason: "world_completed", CompletedAt: simTime}})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []frame{
		{kind: kindDomainBegin, recordedAt: realStart, payload: begin},
		{kind: kindDomainPart, recordedAt: realStart.Add(time.Minute), payload: part},
	} {
		if err := store.appendFrame(ctx, stream, value.kind, value.recordedAt, value.payload); err != nil {
			t.Fatal(err)
		}
	}
	reader := summaryTestStore(t, root)
	view, err := reader.RunSummary(ctx, runID)
	if err != nil || !view.RealCompletedAt.IsZero() {
		t.Fatalf("uncommitted completion affected timing: %+v, %v", view, err)
	}
	completedAt := realStart.Add(2 * time.Minute)
	if err := store.appendFrame(ctx, stream, kindDomainCommit, completedAt, nil); err != nil {
		t.Fatal(err)
	}
	reader = summaryTestStore(t, root)
	view, err = reader.RunSummary(ctx, runID)
	if err != nil || !view.RealCompletedAt.Equal(completedAt) {
		t.Fatalf("expected commit wall time, got %+v, %v", view, err)
	}
}
