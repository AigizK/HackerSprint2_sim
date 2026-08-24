package simulation

import (
	"context"
	"sync"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

type cachedStream struct {
	records    []StoredEvent
	lastAccess uint64
}

// CachedEventStore caches only reconstructable run streams. The wrapped store
// remains authoritative; losing or evicting the cache never loses run data.
type CachedEventStore struct {
	delegate EventStore
	maxRuns  int

	mu      sync.Mutex
	clock   uint64
	epochs  map[string]uint64
	streams map[string]cachedStream
}

func NewCachedEventStore(delegate EventStore, maxRuns int) *CachedEventStore {
	if maxRuns < 1 {
		maxRuns = 1
	}
	return &CachedEventStore{
		delegate: delegate, maxRuns: maxRuns,
		epochs: make(map[string]uint64), streams: make(map[string]cachedStream),
	}
}

func (s *CachedEventStore) Load(ctx context.Context, runID string) ([]StoredEvent, error) {
	s.mu.Lock()
	if stream, exists := s.streams[runID]; exists {
		s.clock++
		stream.lastAccess = s.clock
		s.streams[runID] = stream
		result := append([]StoredEvent(nil), stream.records...)
		s.mu.Unlock()
		return result, nil
	}
	epoch := s.epochs[runID]
	s.mu.Unlock()

	records, err := s.delegate.Load(ctx, runID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.epochs[runID] == epoch {
		s.clock++
		s.streams[runID] = cachedStream{records: append([]StoredEvent(nil), records...), lastAccess: s.clock}
		s.evictLocked()
	}
	s.mu.Unlock()
	return append([]StoredEvent(nil), records...), nil
}

func (s *CachedEventStore) Append(ctx context.Context, runID string, expectedVersion uint64, newEvents []events.Event) ([]StoredEvent, error) {
	appended, err := s.delegate.Append(ctx, runID, expectedVersion, newEvents)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.epochs[runID]++
	if stream, exists := s.streams[runID]; exists && uint64(len(stream.records)) == expectedVersion {
		stream.records = append(stream.records, appended...)
		s.clock++
		stream.lastAccess = s.clock
		s.streams[runID] = stream
	} else {
		delete(s.streams, runID)
	}
	s.mu.Unlock()
	return appended, nil
}

func (s *CachedEventStore) Invalidate(runID string) {
	s.mu.Lock()
	s.epochs[runID]++
	delete(s.streams, runID)
	s.mu.Unlock()
}

func (s *CachedEventStore) evictLocked() {
	for len(s.streams) > s.maxRuns {
		var oldestID string
		var oldestAccess uint64
		first := true
		for runID, stream := range s.streams {
			if first || stream.lastAccess < oldestAccess {
				oldestID, oldestAccess, first = runID, stream.lastAccess, false
			}
		}
		delete(s.streams, oldestID)
	}
}

var _ EventStore = (*CachedEventStore)(nil)
