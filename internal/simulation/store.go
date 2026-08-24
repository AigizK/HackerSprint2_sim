package simulation

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrVersionConflict = errors.New("event stream version conflict")

type EventStore interface {
	Load(ctx context.Context, runID string) ([]StoredEvent, error)
	Append(ctx context.Context, runID string, expectedVersion uint64, events []Event) ([]StoredEvent, error)
}

type MemoryEventStore struct {
	mu      sync.RWMutex
	streams map[string][]StoredEvent
}

func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{streams: make(map[string][]StoredEvent)}
}

func (s *MemoryEventStore) Load(_ context.Context, runID string) ([]StoredEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := s.streams[runID]
	return append([]StoredEvent(nil), records...), nil
}

func (s *MemoryEventStore) Append(_ context.Context, runID string, expectedVersion uint64, events []Event) ([]StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stream := s.streams[runID]
	if uint64(len(stream)) != expectedVersion {
		return nil, fmt.Errorf("%w: expected %d, actual %d", ErrVersionConflict, expectedVersion, len(stream))
	}

	appended := make([]StoredEvent, 0, len(events))
	for _, event := range events {
		record := StoredEvent{
			Version: uint64(len(stream)) + 1,
			Event:   event,
		}
		stream = append(stream, record)
		appended = append(appended, record)
	}
	s.streams[runID] = stream
	return appended, nil
}
