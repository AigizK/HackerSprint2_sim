package journal

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

func summaryTestStore(t *testing.T, root string) *Store {
	t.Helper()
	catalog, err := sqlite.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	store, err := Open(root, WithRunSummaries(catalog), WithMaxCachedRuns(1), WithSegmentSize(1024))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func summaryTestWorld(t *testing.T, store *Store, runID string) time.Time {
	t.Helper()
	at := time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := store.Append(context.Background(), runID, 0, []events.Event{
		events.WorldCreated{RunID: runID, Seed: 42, StartedAt: at, EndsAt: at.Add(24 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestRunSummariesSurviveRestartWithoutLoadingOrEvictingJournals(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	store := summaryTestStore(t, root)
	for i := range 12 {
		summaryTestWorld(t, store, fmt.Sprintf("run-%d", i))
	}
	store = summaryTestStore(t, root)
	if _, err := store.Load(ctx, "run-0"); err != nil {
		t.Fatal(err)
	}
	activeStream := store.runs["run-0"]
	for range 2 {
		for i := range 12 {
			view, err := store.RunSummary(ctx, fmt.Sprintf("run-%d", i))
			if err != nil || view.Seed != 42 || view.EventCount != 1 || view.Overview.RunStatus != "running" {
				t.Fatalf("summary=%+v err=%v", view, err)
			}
		}
	}
	if len(store.runs) != 1 || store.runs["run-0"] != activeStream {
		t.Fatal("listing summaries opened journals or evicted the active run")
	}
}

func TestRunSummaryRebuildsAfterUnpublishedUpdatesAndSegmentRotation(t *testing.T) {
	ctx, root, runID := context.Background(), t.TempDir(), "run"
	store := summaryTestStore(t, root)
	at := summaryTestWorld(t, store, runID)
	// Simulate an older writer, or a crash before publishing the summary.
	writer, err := Open(root, WithSegmentSize(1024))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		from := at.Add(time.Duration(i) * 5 * time.Minute)
		if _, err := writer.Append(ctx, runID, uint64(i+1), []events.Event{events.TimeAdvanced{
			From: from, To: from.Add(5 * time.Minute), RequestedDuration: 5 * time.Minute, AppliedDuration: 5 * time.Minute,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.RecordAgentRequest(ctx, runID, AgentRequestReceived{RequestID: "audit", Method: "GET", Path: "/overview", ReceivedAt: at}); err != nil {
		t.Fatal(err)
	}
	reader := summaryTestStore(t, root)
	view, err := reader.RunSummary(ctx, runID)
	if err != nil || view.EventCount != 21 || view.AgentRequestCount != 1 || !view.Overview.SimulationTime.Equal(at.Add(100*time.Minute)) {
		t.Fatalf("rebuilt summary=%+v err=%v", view, err)
	}
	segments, err := listSegments(reader.runDirectory(runID))
	if err != nil || len(segments) < 2 || !segments[0].compressed {
		t.Fatalf("expected compressed history, segments=%v err=%v", segments, err)
	}
	restarted := summaryTestStore(t, root)
	if got, err := restarted.RunSummary(ctx, runID); err != nil || !reflect.DeepEqual(got, view) || len(restarted.runs) != 0 {
		t.Fatalf("persisted summary was not reused: %+v, %v", got, err)
	}
	if err := restarted.CompleteAgentRequest(ctx, runID, AgentRequestCompleted{RequestID: "audit", StatusCode: 200, CompletedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	finalReader := summaryTestStore(t, root)
	if got, err := finalReader.RunSummary(ctx, runID); err != nil || !reflect.DeepEqual(got, view) || len(finalReader.runs) != 0 {
		t.Fatalf("audit completion did not preserve the fast path: %+v, %v", got, err)
	}
}

func TestRunSummaryExcludesUncommittedDomainBatch(t *testing.T) {
	ctx, root, runID := context.Background(), t.TempDir(), "partial"
	store := summaryTestStore(t, root)
	at := summaryTestWorld(t, store, runID)
	stream := store.runs[runID]
	begin := make([]byte, 16)
	binary.BigEndian.PutUint64(begin[:8], 2)
	binary.BigEndian.PutUint64(begin[8:], 1)
	if err := store.appendFrame(ctx, stream, kindDomainBegin, at, begin); err != nil {
		t.Fatal(err)
	}
	part, err := encodeDomainBatch(2, []events.Event{events.RunEnded{Reason: "world_completed", CompletedAt: at}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.appendFrame(ctx, stream, kindDomainPart, at, part); err != nil {
		t.Fatal(err)
	}
	reader := summaryTestStore(t, root)
	view, err := reader.RunSummary(ctx, runID)
	if err != nil || view.EventCount != 1 || view.Overview.RunStatus != "running" {
		t.Fatalf("uncommitted event included: %+v, %v", view, err)
	}
}

func TestRunSummaryConcurrentReadsAndAppends(t *testing.T) {
	ctx, root, runID := context.Background(), t.TempDir(), "concurrent"
	store := summaryTestStore(t, root)
	at := summaryTestWorld(t, store, runID)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for range 30 {
				view, err := store.RunSummary(ctx, runID)
				if err != nil || view.EventCount < 1 || view.EventCount > 31 {
					t.Errorf("concurrent summary=%+v err=%v", view, err)
					return
				}
			}
		})
	}
	for i := range 30 {
		from := at.Add(time.Duration(i) * 5 * time.Minute)
		if _, err := store.Append(ctx, runID, uint64(i+1), []events.Event{events.TimeAdvanced{
			From: from, To: from.Add(5 * time.Minute), RequestedDuration: 5 * time.Minute, AppliedDuration: 5 * time.Minute,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	got, err := store.RunSummary(ctx, runID)
	if err != nil || got.EventCount != 31 {
		t.Fatalf("final summary=%+v err=%v", got, err)
	}
}

var _ simulation.RunSummaryRepository = (*sqlite.Store)(nil)
