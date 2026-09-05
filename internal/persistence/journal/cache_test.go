package journal

import (
	"context"
	"testing"
)

func TestStoreEvictsLeastRecentlyUsedIdleRun(t *testing.T) {
	store, err := Open(t.TempDir(), WithMaxCachedRuns(2))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, runID := range []string{"run-1", "run-2", "run-1", "run-3"} {
		if _, err := store.Load(ctx, runID); err != nil {
			t.Fatal(err)
		}
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.runs) != 2 {
		t.Fatalf("cached runs = %d, want 2", len(store.runs))
	}
	if store.runs["run-1"] == nil || store.runs["run-3"] == nil {
		t.Fatalf("unexpected cached runs: %#v", store.runs)
	}
	if store.runs["run-2"] != nil {
		t.Fatal("least recently used run-2 was not evicted")
	}
}
