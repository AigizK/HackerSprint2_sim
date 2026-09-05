// Package persistence assembles the local durable storage layout.
package persistence

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
)

type Storage struct {
	Catalog *sqlite.Store
	Journal *journal.Store
}

// Open creates this layout beneath root:
//
//	catalog.db
//	runs/<hash-prefix>/<run-id-hash>/journal/<segment>.log[.zst]
func Open(root string, journalOptions ...journal.Option) (*Storage, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	catalog, err := sqlite.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		return nil, err
	}
	options := append([]journal.Option{journal.WithRunSummaries(catalog)}, journalOptions...)
	runJournal, err := journal.Open(root, options...)
	if err != nil {
		catalog.Close()
		return nil, fmt.Errorf("open run journal: %w", err)
	}
	return &Storage{Catalog: catalog, Journal: runJournal}, nil
}

func (s *Storage) Close() error { return s.Catalog.Close() }
