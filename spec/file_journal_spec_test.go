package spec_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

type fileJournalScenario struct {
	t              *testing.T
	ctx            context.Context
	root           string
	store          *journal.Store
	runID          string
	loadedRequests []journal.AgentRequestAudit
}

func newFileJournalScenario(t *testing.T, segmentSize int64) *fileJournalScenario {
	s := &fileJournalScenario{t: t, ctx: context.Background(), root: t.TempDir(), runID: "run/with unsafe characters"}
	store, err := journal.Open(s.root, journal.WithSegmentSize(segmentSize))
	if err != nil {
		t.Fatal(err)
	}
	s.store = store
	return s
}

func (s *fileJournalScenario) GivenManyDomainEventBatches() {
	s.t.Helper()
	for index := 0; index < 30; index++ {
		at := time.Date(2030, 1, 1, 0, index, 0, 0, time.UTC)
		if _, err := s.store.Append(s.ctx, s.runID, uint64(index), []events.Event{
			events.TimeAdvanced{From: at, To: at.Add(time.Minute), RequestedDuration: time.Minute, AppliedDuration: time.Minute},
		}); err != nil {
			s.t.Fatal(err)
		}
	}
}

func (s *fileJournalScenario) GivenAgentRequestIsRecordedAndCompleted() {
	s.t.Helper()
	receivedAt := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := s.store.RecordAgentRequest(s.ctx, s.runID, journal.AgentRequestReceived{
		RequestID: "request-1", AgentID: "agent-7", Method: "POST", Path: "/v2/runs/x/time:advance",
		Body: []byte(`{"minutes":5}`), ReceivedAt: receivedAt,
	}); err != nil {
		s.t.Fatal(err)
	}
	if err := s.store.CompleteAgentRequest(s.ctx, s.runID, journal.AgentRequestCompleted{
		RequestID: "request-1", StatusCode: 200, ResponseBody: []byte(`{"ok":true}`), CompletedAt: receivedAt.Add(time.Second),
	}); err != nil {
		s.t.Fatal(err)
	}
}

func (s *fileJournalScenario) WhenProcessRestarts() {
	s.t.Helper()
	store, err := journal.Open(s.root, journal.WithSegmentSize(512))
	if err != nil {
		s.t.Fatal(err)
	}
	s.store = store
}

func (s *fileJournalScenario) WhenRequestsAreLoaded() {
	var err error
	s.loadedRequests, err = s.store.LoadAgentRequests(s.ctx, s.runID)
	if err != nil {
		s.t.Fatal(err)
	}
}

func (s *fileJournalScenario) ThenClosedSegmentsAreZstdAndAllEventsReplay() {
	s.t.Helper()
	compressed, err := filepath.Glob(filepath.Join(s.root, "runs", "*", "*", "journal", "*.log.zst"))
	if err != nil || len(compressed) == 0 {
		s.t.Fatalf("compressed segments = %v, err = %v", compressed, err)
	}
	active, _ := filepath.Glob(filepath.Join(s.root, "runs", "*", "*", "journal", "*.log"))
	if len(active) != 1 {
		s.t.Fatalf("active segments = %v", active)
	}
	records, err := s.store.Load(s.ctx, s.runID)
	if err != nil {
		s.t.Fatal(err)
	}
	if len(records) != 30 || records[len(records)-1].Version != 30 {
		s.t.Fatalf("records = %d", len(records))
	}
}

func TestJournalRotatesCompressesAndReplaysWithoutSnapshots(t *testing.T) {
	s := newFileJournalScenario(t, 512)
	s.GivenManyDomainEventBatches()
	s.WhenProcessRestarts()
	s.ThenClosedSegmentsAreZstdAndAllEventsReplay()
}

func TestAgentRequestsShareRunJournalAndSurviveRestart(t *testing.T) {
	s := newFileJournalScenario(t, 512)
	s.GivenAgentRequestIsRecordedAndCompleted()
	s.WhenProcessRestarts()
	s.WhenRequestsAreLoaded()
	if len(s.loadedRequests) != 1 || s.loadedRequests[0].Completed == nil || s.loadedRequests[0].Completed.StatusCode != 200 {
		t.Fatalf("loaded requests = %#v", s.loadedRequests)
	}
}

func TestIncompleteActiveSegmentTailIsDiscardedOnRecovery(t *testing.T) {
	s := newFileJournalScenario(t, 4096)
	s.GivenManyDomainEventBatches()
	active, _ := filepath.Glob(filepath.Join(s.root, "runs", "*", "*", "journal", "*.log"))
	if len(active) != 1 {
		t.Fatalf("active segment = %v", active)
	}
	before, _ := os.Stat(active[0])
	file, err := os.OpenFile(active[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("torn-frame"))
	_ = file.Close()
	s.WhenProcessRestarts()
	records, err := s.store.Load(s.ctx, s.runID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(active[0])
	if len(records) != 30 || after.Size() != before.Size() {
		t.Fatalf("records=%d size=%d want=%d", len(records), after.Size(), before.Size())
	}
}
