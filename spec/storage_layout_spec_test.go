package spec_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

func TestLocalStorageContainsCatalogAndPerRunJournalButNoSnapshotTables(t *testing.T) {
	root := t.TempDir()
	storage, err := persistence.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()

	if _, err := os.Stat(filepath.Join(root, "catalog.db")); err != nil {
		t.Fatal(err)
	}
	var forbidden int
	if err := storage.Catalog.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name IN ('world_events', 'run_events', 'snapshots', 'run_snapshots')`).Scan(&forbidden); err != nil {
		t.Fatal(err)
	}
	if forbidden != 0 {
		t.Fatalf("catalog contains %d event/snapshot tables", forbidden)
	}

	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := storage.Journal.Append(context.Background(), "layout-run", 0, []events.Event{
		events.WorldCreated{RunID: "layout-run", Seed: 1, StartedAt: at, EndsAt: at.AddDate(1, 0, 0)},
	}); err != nil {
		t.Fatal(err)
	}
	segments, err := filepath.Glob(filepath.Join(root, "runs", "*", "*", "journal", "*.log"))
	if err != nil || len(segments) != 1 {
		t.Fatalf("segments = %v, err = %v", segments, err)
	}
}
