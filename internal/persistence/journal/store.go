// Package journal implements a per-run append-only file store. A single
// ordered journal contains both domain event batches and agent request audit
// records, so their relative order survives a process restart.
package journal

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/klauspost/compress/zstd"
)

const DefaultSegmentSize int64 = 32 << 20

var ErrRecordTooLarge = errors.New("journal record exceeds segment size")

type Option func(*Store) error

func WithSegmentSize(size int64) Option {
	return func(store *Store) error {
		if size <= headerSize+trailerSize {
			return fmt.Errorf("journal segment size must exceed frame overhead")
		}
		store.segmentSize = size
		return nil
	}
}

func WithMaxCachedRuns(maxRuns int) Option {
	return func(store *Store) error {
		if maxRuns < 1 {
			return fmt.Errorf("journal cache size must be positive")
		}
		store.maxRuns = maxRuns
		return nil
	}
}

type Store struct {
	root        string
	segmentSize int64
	maxRuns     int
	mu          sync.Mutex
	access      uint64
	runs        map[string]*runStream
	summaries   simulation.RunSummaryRepository
}

type runStream struct {
	mu                  sync.Mutex
	initialized         bool
	directory           string
	nextSequence        uint64
	domainVersion       uint64
	activeSegment       uint64
	activeSize          int64
	events              []simulation.StoredEvent
	requests            []AgentRequestAudit
	requestIndexes      map[string]int
	pendingEvents       []simulation.StoredEvent
	pendingFirstVersion uint64
	pendingEventCount   uint64
	users               int
	lastAccess          uint64
	summary             *simulation.RunSummaryProjection
	realStartedAt       time.Time
	realCompletedAt     time.Time
}

func Open(root string, options ...Option) (*Store, error) {
	store := &Store{root: root, segmentSize: DefaultSegmentSize, maxRuns: 8, runs: make(map[string]*runStream)}
	for _, option := range options {
		if err := option(store); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o750); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Load(ctx context.Context, runID string) ([]simulation.StoredEvent, error) {
	stream, err := s.acquireStream(runID)
	if err != nil {
		return nil, err
	}
	defer s.releaseStream(runID, stream)
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := s.initialize(ctx, runID, stream); err != nil {
		return nil, err
	}
	return append([]simulation.StoredEvent(nil), stream.events...), nil
}

func (s *Store) Append(ctx context.Context, runID string, expectedVersion uint64, newEvents []events.Event) ([]simulation.StoredEvent, error) {
	stream, err := s.acquireStream(runID)
	if err != nil {
		return nil, err
	}
	defer s.releaseStream(runID, stream)
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := s.initialize(ctx, runID, stream); err != nil {
		return nil, err
	}
	if stream.domainVersion != expectedVersion {
		return nil, fmt.Errorf("%w: expected %d, actual %d", simulation.ErrVersionConflict, expectedVersion, stream.domainVersion)
	}
	if len(newEvents) == 0 {
		return nil, nil
	}
	const eventsPerPart = 10_000
	recordedAt := time.Now().UTC()
	if len(newEvents) <= eventsPerPart {
		payload, err := encodeDomainBatch(expectedVersion+1, newEvents)
		if err != nil {
			return nil, err
		}
		recordedAt = time.Now().UTC()
		if err := s.appendFrame(ctx, stream, kindDomainEvents, recordedAt, payload); err != nil {
			return nil, err
		}
	} else {
		beginPayload := make([]byte, 16)
		binary.BigEndian.PutUint64(beginPayload[:8], expectedVersion+1)
		binary.BigEndian.PutUint64(beginPayload[8:], uint64(len(newEvents)))
		if err := s.appendFrame(ctx, stream, kindDomainBegin, recordedAt, beginPayload); err != nil {
			return nil, err
		}
		for offset := 0; offset < len(newEvents); offset += eventsPerPart {
			end := offset + eventsPerPart
			if end > len(newEvents) {
				end = len(newEvents)
			}
			payload, err := encodeDomainBatch(expectedVersion+1+uint64(offset), newEvents[offset:end])
			if err != nil {
				return nil, err
			}
			if err := s.appendFrame(ctx, stream, kindDomainPart, recordedAt, payload); err != nil {
				return nil, err
			}
		}
		recordedAt = time.Now().UTC()
		if err := s.appendFrame(ctx, stream, kindDomainCommit, recordedAt, nil); err != nil {
			return nil, err
		}
	}
	appended := make([]simulation.StoredEvent, 0, len(newEvents))
	for _, event := range newEvents {
		stream.domainVersion++
		record := simulation.StoredEvent{Version: stream.domainVersion, Event: event}
		stream.events = append(stream.events, record)
		stream.applySummaryEvent(record, recordedAt)
		appended = append(appended, record)
	}
	s.persistSummary(ctx, runID, stream)
	return appended, nil
}

func (s *Store) RecordAgentRequest(ctx context.Context, runID string, request AgentRequestReceived) error {
	if request.RequestID == "" || request.Method == "" || request.Path == "" || request.ReceivedAt.IsZero() {
		return fmt.Errorf("invalid received agent request")
	}
	stream, err := s.acquireStream(runID)
	if err != nil {
		return err
	}
	defer s.releaseStream(runID, stream)
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := s.initialize(ctx, runID, stream); err != nil {
		return err
	}
	if _, exists := stream.requestIndexes[request.RequestID]; exists {
		return fmt.Errorf("agent request %q already exists", request.RequestID)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := s.appendFrame(ctx, stream, kindRequestReceived, request.ReceivedAt, payload); err != nil {
		return err
	}
	stream.requestIndexes[request.RequestID] = len(stream.requests)
	stream.requests = append(stream.requests, AgentRequestAudit{Received: request})
	stream.applySummaryRequest(request)
	s.persistSummary(ctx, runID, stream)
	return nil
}

func (s *Store) CompleteAgentRequest(ctx context.Context, runID string, completion AgentRequestCompleted) error {
	if completion.RequestID == "" || completion.CompletedAt.IsZero() {
		return fmt.Errorf("invalid completed agent request")
	}
	stream, err := s.acquireStream(runID)
	if err != nil {
		return err
	}
	defer s.releaseStream(runID, stream)
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := s.initialize(ctx, runID, stream); err != nil {
		return err
	}
	index, exists := stream.requestIndexes[completion.RequestID]
	if !exists {
		return fmt.Errorf("agent request %q was not received", completion.RequestID)
	}
	if stream.requests[index].Completed != nil {
		return fmt.Errorf("agent request %q already completed", completion.RequestID)
	}
	payload, err := json.Marshal(completion)
	if err != nil {
		return err
	}
	if err := s.appendFrame(ctx, stream, kindRequestCompleted, completion.CompletedAt, payload); err != nil {
		return err
	}
	copy := completion
	stream.requests[index].Completed = &copy
	s.persistSummary(ctx, runID, stream)
	return nil
}

func (s *Store) LoadAgentRequests(ctx context.Context, runID string) ([]AgentRequestAudit, error) {
	stream, err := s.acquireStream(runID)
	if err != nil {
		return nil, err
	}
	defer s.releaseStream(runID, stream)
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := s.initialize(ctx, runID, stream); err != nil {
		return nil, err
	}
	result := make([]AgentRequestAudit, len(stream.requests))
	copy(result, stream.requests)
	return result, nil
}

func (s *Store) acquireStream(runID string) (*runStream, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id is required")
	}
	directory := s.runDirectory(runID)
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := s.runs[runID]
	if stream == nil {
		stream = &runStream{directory: directory, activeSegment: 1, requestIndexes: make(map[string]int)}
		s.runs[runID] = stream
	}
	s.access++
	stream.lastAccess = s.access
	stream.users++
	return stream, nil
}

func (s *Store) releaseStream(runID string, stream *runStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runs[runID] == stream && stream.users > 0 {
		stream.users--
	}
	for len(s.runs) > s.maxRuns {
		var candidateID string
		oldest := ^uint64(0)
		for id, candidate := range s.runs {
			if candidate.users == 0 && candidate.lastAccess < oldest {
				candidateID = id
				oldest = candidate.lastAccess
			}
		}
		if candidateID == "" {
			return
		}
		delete(s.runs, candidateID)
	}
}

func (s *Store) initialize(ctx context.Context, runID string, stream *runStream) error {
	if stream.initialized {
		return nil
	}
	// A canceled/failed first read may have consumed only part of the journal.
	// Retry from a clean projection; never apply the same prefix twice.
	stream.nextSequence, stream.domainVersion = 0, 0
	stream.activeSegment, stream.activeSize = 1, 0
	stream.events, stream.requests, stream.pendingEvents = nil, nil, nil
	stream.pendingFirstVersion, stream.pendingEventCount = 0, 0
	stream.requestIndexes = make(map[string]int)
	stream.summary = simulation.NewRunSummaryProjection()
	stream.realStartedAt, stream.realCompletedAt = time.Time{}, time.Time{}
	if err := os.MkdirAll(stream.directory, 0o750); err != nil {
		return err
	}
	identityPath := filepath.Join(filepath.Dir(stream.directory), "run_id")
	if contents, err := os.ReadFile(identityPath); err == nil {
		if string(contents) != runID {
			return fmt.Errorf("run journal identity mismatch")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(identityPath, []byte(runID), 0o640); err != nil {
			return err
		}
	} else {
		return err
	}
	segments, err := listSegments(stream.directory)
	if err != nil {
		return err
	}
	for _, segment := range segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.readSegment(segment, stream); err != nil {
			return err
		}
		if segment.number > stream.activeSegment {
			stream.activeSegment = segment.number
		}
		if !segment.compressed {
			info, err := os.Stat(segment.path)
			if err != nil {
				return err
			}
			stream.activeSize = info.Size()
		}
	}
	if len(segments) > 0 && segments[len(segments)-1].compressed {
		stream.activeSegment = segments[len(segments)-1].number + 1
		stream.activeSize = 0
	}
	stream.initialized = true
	return nil
}

type segmentFile struct {
	number     uint64
	path       string
	compressed bool
}

func listSegments(directory string) ([]segmentFile, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var result []segmentFile
	for _, entry := range entries {
		name := entry.Name()
		compressed := strings.HasSuffix(name, ".log.zst")
		if !compressed && !strings.HasSuffix(name, ".log") {
			continue
		}
		numberText := strings.TrimSuffix(strings.TrimSuffix(name, ".zst"), ".log")
		number, err := strconv.ParseUint(numberText, 10, 64)
		if err != nil {
			continue
		}
		result = append(result, segmentFile{number: number, path: filepath.Join(directory, name), compressed: compressed})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].number == result[j].number {
			return result[i].compressed
		}
		return result[i].number < result[j].number
	})
	// A crash after atomically publishing .zst but before deleting .log can
	// leave both names behind. The compressed file is already committed and is
	// authoritative; ignore the duplicate source rather than replaying twice.
	deduplicated := result[:0]
	for _, segment := range result {
		if len(deduplicated) > 0 && deduplicated[len(deduplicated)-1].number == segment.number {
			continue
		}
		deduplicated = append(deduplicated, segment)
	}
	return deduplicated, nil
}

func (s *Store) readSegment(segment segmentFile, stream *runStream) error {
	file, err := os.OpenFile(segment.path, func() int {
		if segment.compressed {
			return os.O_RDONLY
		}
		return os.O_RDWR
	}(), 0)
	if err != nil {
		return err
	}
	defer file.Close()
	var reader io.Reader = file
	var decoder *zstd.Decoder
	if segment.compressed {
		decoder, err = zstd.NewReader(file)
		if err != nil {
			return err
		}
		defer decoder.Close()
		reader = decoder
	}
	var validOffset int64
	for {
		value, size, readErr := readFrame(reader)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if segment.compressed {
				return fmt.Errorf("read compressed segment %s: %w", segment.path, readErr)
			}
			position, seekErr := file.Seek(0, io.SeekCurrent)
			info, statErr := file.Stat()
			if seekErr != nil || statErr != nil {
				return fmt.Errorf("inspect active segment after read error: %w", readErr)
			}
			if position < info.Size() {
				return fmt.Errorf("corrupt active segment %s before tail: %w", segment.path, readErr)
			}
			if err := file.Truncate(validOffset); err != nil {
				return err
			}
			break
		}
		if value.sequence != stream.nextSequence+1 {
			return fmt.Errorf("journal sequence: got %d, want %d", value.sequence, stream.nextSequence+1)
		}
		if err := applyFrame(value, stream); err != nil {
			return fmt.Errorf("apply journal frame %d: %w", value.sequence, err)
		}
		stream.nextSequence = value.sequence
		validOffset += size
	}
	return nil
}

func applyFrame(value frame, stream *runStream) error {
	switch value.kind {
	case kindDomainEvents:
		records, err := decodeDomainBatch(value.payload)
		if err != nil {
			return err
		}
		if len(records) > 0 && records[0].Version != stream.domainVersion+1 {
			return fmt.Errorf("domain version discontinuity")
		}
		stream.events = append(stream.events, records...)
		for _, record := range records {
			stream.applySummaryEvent(record, value.recordedAt)
		}
		if len(records) > 0 {
			stream.domainVersion = records[len(records)-1].Version
		}
	case kindDomainBegin:
		if len(value.payload) != 16 {
			return fmt.Errorf("invalid domain transaction begin")
		}
		stream.pendingFirstVersion = binary.BigEndian.Uint64(value.payload[:8])
		stream.pendingEventCount = binary.BigEndian.Uint64(value.payload[8:])
		stream.pendingEvents = nil
		if stream.pendingFirstVersion != stream.domainVersion+1 {
			return fmt.Errorf("domain transaction version discontinuity")
		}
	case kindDomainPart:
		if stream.pendingEventCount == 0 {
			return fmt.Errorf("domain transaction part without begin")
		}
		records, err := decodeDomainBatch(value.payload)
		if err != nil {
			return err
		}
		expected := stream.pendingFirstVersion + uint64(len(stream.pendingEvents))
		if len(records) == 0 || records[0].Version != expected {
			return fmt.Errorf("domain transaction part version discontinuity")
		}
		stream.pendingEvents = append(stream.pendingEvents, records...)
	case kindDomainCommit:
		if stream.pendingEventCount == 0 || uint64(len(stream.pendingEvents)) != stream.pendingEventCount {
			return fmt.Errorf("incomplete domain transaction")
		}
		stream.events = append(stream.events, stream.pendingEvents...)
		for _, record := range stream.pendingEvents {
			stream.applySummaryEvent(record, value.recordedAt)
		}
		stream.domainVersion = stream.pendingEvents[len(stream.pendingEvents)-1].Version
		stream.pendingEvents = nil
		stream.pendingFirstVersion = 0
		stream.pendingEventCount = 0
	case kindRequestReceived:
		var request AgentRequestReceived
		if err := json.Unmarshal(value.payload, &request); err != nil {
			return err
		}
		if _, exists := stream.requestIndexes[request.RequestID]; exists {
			return fmt.Errorf("duplicate request %q", request.RequestID)
		}
		stream.requestIndexes[request.RequestID] = len(stream.requests)
		stream.requests = append(stream.requests, AgentRequestAudit{Received: request})
		stream.applySummaryRequest(request)
	case kindRequestCompleted:
		var completion AgentRequestCompleted
		if err := json.Unmarshal(value.payload, &completion); err != nil {
			return err
		}
		index, exists := stream.requestIndexes[completion.RequestID]
		if !exists {
			return fmt.Errorf("completion without request %q", completion.RequestID)
		}
		copy := completion
		stream.requests[index].Completed = &copy
	default:
		return fmt.Errorf("unknown journal record kind %d", value.kind)
	}
	return nil
}

func (s *Store) appendFrame(ctx context.Context, stream *runStream, kind recordKind, at time.Time, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if uint64(len(payload)) > uint64(^uint32(0)) {
		return fmt.Errorf("journal payload is too large")
	}
	value := encodeFrame(frame{kind: kind, sequence: stream.nextSequence + 1, recordedAt: at.UTC(), payload: payload})
	if int64(len(value)) > s.segmentSize {
		return fmt.Errorf("%w: record=%d segment=%d", ErrRecordTooLarge, len(value), s.segmentSize)
	}
	if stream.activeSize > 0 && stream.activeSize+int64(len(value)) > s.segmentSize {
		if err := s.closeSegment(stream); err != nil {
			return err
		}
		stream.activeSegment++
		stream.activeSize = 0
	}
	path := filepath.Join(stream.directory, fmt.Sprintf("%06d.log", stream.activeSegment))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	if _, err := file.Write(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	stream.activeSize += int64(len(value))
	stream.nextSequence++
	return nil
}

func (s *Store) closeSegment(stream *runStream) error {
	source := filepath.Join(stream.directory, fmt.Sprintf("%06d.log", stream.activeSegment))
	target := source + ".zst"
	temporary := target + ".tmp"
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		input.Close()
		return err
	}
	encoder, err := zstd.NewWriter(output, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err == nil {
		_, err = io.Copy(encoder, input)
		if closeErr := encoder.Close(); err == nil {
			err = closeErr
		}
	}
	if syncErr := output.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if closeErr := input.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Remove(source)
}

var _ simulation.EventStore = (*Store)(nil)
