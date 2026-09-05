package journal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

// Increment when the list projection's semantics change so persisted views
// are rebuilt even when their journals have not changed.
const runSummaryFormatVersion = 2

func (stream *runStream) summaryView() simulation.RunSummary {
	view := stream.summary.View(len(stream.requests))
	view.RealStartedAt, view.RealCompletedAt = stream.realStartedAt, stream.realCompletedAt
	return view
}

func (stream *runStream) applySummaryEvent(record simulation.StoredEvent, recordedAt time.Time) {
	stream.summary.Apply(record)
	if _, completed := record.Event.(events.RunEnded); completed && stream.realCompletedAt.IsZero() {
		// The journal frame records wall time; RunEnded.CompletedAt is simulation time.
		stream.realCompletedAt = recordedAt
	}
}

func (stream *runStream) applySummaryRequest(request AgentRequestReceived) {
	if request.Method == "POST" && request.Path == "/v2/start" &&
		(stream.realStartedAt.IsZero() || request.ReceivedAt.Before(stream.realStartedAt)) {
		// Start is audited after initialization, but ReceivedAt includes world generation.
		// Idempotent retries must never move the start forward.
		stream.realStartedAt = request.ReceivedAt
	}
}

func WithRunSummaries(repository simulation.RunSummaryRepository) Option {
	return func(store *Store) error {
		store.summaries = repository
		return nil
	}
}

func (s *Store) runDirectory(runID string) string {
	digest := sha256.Sum256([]byte(runID))
	hexDigest := hex.EncodeToString(digest[:])
	return filepath.Join(s.root, "runs", hexDigest[:2], hexDigest, "journal")
}

// RunSummary's fast path reads only the compact catalog row and segment file
// metadata. It does not open a journal stream or evict active runs from the LRU.
func (s *Store) RunSummary(ctx context.Context, runID string) (simulation.RunSummary, error) {
	if err := ctx.Err(); err != nil {
		return simulation.RunSummary{}, err
	}
	if runID == "" {
		return simulation.RunSummary{}, fmt.Errorf("run id is required")
	}
	if s.summaries != nil {
		cached, found, err := s.summaries.GetRunSummary(ctx, runID)
		if err != nil {
			return simulation.RunSummary{}, err
		}
		if found && cached.FormatVersion == runSummaryFormatVersion {
			stamp, err := journalStamp(s.runDirectory(runID))
			if err == nil && cached.JournalStamp == stamp {
				return cached.Summary, nil
			}
		}
	}
	stream, err := s.acquireStream(runID)
	if err != nil {
		return simulation.RunSummary{}, err
	}
	defer s.releaseStream(runID, stream)
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := s.initialize(ctx, runID, stream); err != nil {
		return simulation.RunSummary{}, err
	}
	s.persistSummary(ctx, runID, stream)
	return stream.summaryView(), nil
}

// The cache and journal are separate durable stores. Publishing the cache only
// after a successful journal append, with a stamp of the actual segment files,
// makes a crash between the two writes recoverable on the next read.
func (s *Store) persistSummary(ctx context.Context, runID string, stream *runStream) {
	if s.summaries == nil {
		return
	}
	stamp, err := journalStamp(stream.directory)
	if err == nil {
		err = s.summaries.PutRunSummary(ctx, runID, simulation.CachedRunSummary{
			FormatVersion: runSummaryFormatVersion, JournalStamp: stamp,
			Summary: stream.summaryView(),
		})
	}
	// The domain/audit append has already committed. A failed disposable cache
	// update must not turn it into a failed command; its old stamp cannot match.
	if err != nil {
		log.Printf("update run summary %s: %v", runID, err)
	}
}

func journalStamp(directory string) (string, error) {
	segments, err := listSegments(directory)
	if err != nil {
		return "", err
	}
	var stamp strings.Builder
	for _, segment := range segments {
		info, err := os.Stat(segment.path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&stamp, "%d:%t:%d:%d;", segment.number, segment.compressed, info.Size(), info.ModTime().UnixNano())
	}
	return stamp.String(), nil
}
