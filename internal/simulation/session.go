package simulation

import (
	"context"
	"fmt"
	"sync"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// RunSession owns one active run in memory. It replays once when opened and
// applies every durably appended event to the cached State. Restart recovery
// still uses the complete journal, so this cache is not a snapshot.
type RunSession struct {
	mu       sync.Mutex
	runID    string
	store    EventStore
	state    State
	records  []StoredEvent
	logCount int
}

func OpenRunSession(ctx context.Context, store EventStore, runID string) (*RunSession, error) {
	records, err := store.Load(ctx, runID)
	if err != nil {
		return nil, err
	}
	state, err := Rehydrate(records)
	if err != nil {
		return nil, err
	}
	return &RunSession{runID: runID, store: store, state: state, records: records, logCount: countSiteLogRecords(records)}, nil
}

func (s *RunSession) Execute(ctx context.Context, command Command) ([]events.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	decided, err := DecideWithRunPolicies(s.runID, s.state, command)
	if err != nil {
		return nil, err
	}
	appended, err := s.store.Append(ctx, s.runID, s.state.Version, decided)
	if err != nil {
		return nil, err
	}
	for _, record := range appended {
		if err := s.state.Apply(record.Event); err != nil {
			return nil, fmt.Errorf("apply durably appended event %d: %w", record.Version, err)
		}
		s.state.Version = record.Version
		if isSiteLogEvent(record.Event) {
			s.logCount++
		}
	}
	s.state.pruneDerivedState(s.state.Clock.CurrentTime)
	s.records = append(s.records, appended...)
	return decided, nil
}

// State is a read-only view. Callers must not mutate its maps or slices.
func (s *RunSession) State() State { s.mu.Lock(); defer s.mu.Unlock(); return s.state }

func (s *RunSession) EventsAfter(version uint64) ([]StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version > s.state.Version {
		return nil, fmt.Errorf("version %d is ahead of run version %d", version, s.state.Version)
	}
	return append([]StoredEvent(nil), s.records[version:]...), nil
}

func (s *RunSession) Version() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.state.Version }

func (s *RunSession) LogCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.logCount }

func (s *RunSession) Records() []StoredEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StoredEvent(nil), s.records...)
}

// Projection borrows the immutable event prefix and current state for the
// duration of a serialized run action. Callers must not retain or mutate it.
func (s *RunSession) Projection() Projection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return NewProjection(s.records, s.state)
}

func (s *RunSession) RequestEvents(requestID model.RequestID) []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]events.Event, 0, 4)
	for _, record := range s.records {
		switch event := record.Event.(type) {
		case events.PageRequestStarted:
			if event.RequestID == requestID {
				result = append(result, event)
			}
		case events.PageRequestAccepted:
			if event.RequestID == requestID {
				result = append(result, event)
			}
		case events.FirewallRequestEvaluated:
			if event.RequestID == requestID {
				result = append(result, event)
			}
		case events.PageRequestCompleted:
			if event.RequestID == requestID {
				result = append(result, event)
			}
		case events.PageRequestRejected:
			if event.RequestID == requestID {
				result = append(result, event)
			}
		}
	}
	return result
}

func countSiteLogRecords(records []StoredEvent) int {
	count := 0
	for _, record := range records {
		if isSiteLogEvent(record.Event) {
			count++
		}
	}
	return count
}

func isSiteLogEvent(event events.Event) bool {
	switch event.(type) {
	case events.PageRequestCompleted, events.PageRequestRejected:
		return true
	default:
		return false
	}
}
