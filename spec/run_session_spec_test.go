package spec_test

import (
	"context"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

type countingEventStore struct {
	simulation.EventStore
	loads int
}

func (s *countingEventStore) Load(ctx context.Context, runID string) ([]simulation.StoredEvent, error) {
	s.loads++
	return s.EventStore.Load(ctx, runID)
}

func TestRunSessionReplaysOnceAndAppliesSubsequentCommandsInMemory(t *testing.T) {
	ctx := context.Background()
	base := simulation.NewMemoryEventStore()
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := base.Append(ctx, "session-run", 0, []events.Event{
		events.WorldCreated{RunID: "session-run", Seed: 1, StartedAt: at, EndsAt: at.AddDate(0, 1, 0)},
	}); err != nil {
		t.Fatal(err)
	}
	store := &countingEventStore{EventStore: base}
	session, err := simulation.OpenRunSession(ctx, store, "session-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, simulation.AddProduct{ProductID: "p1", Name: "Product", PriceMinor: 100,
		ViewProbabilityPPM: 1_000_000, PurchaseProbabilityPPM: 1_000_000}); err != nil {
		t.Fatal(err)
	}
	_ = session.State()
	_ = session.State()
	if store.loads != 1 {
		t.Fatalf("event stream loads = %d, want 1", store.loads)
	}
	if session.Version() != 2 {
		t.Fatalf("session version = %d", session.Version())
	}
}
