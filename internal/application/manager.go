package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

var ErrConcurrentRunRequest = errors.New("another request for this run is in progress")

type AuditStore interface {
	RecordAgentRequest(context.Context, string, journal.AgentRequestReceived) error
	CompleteAgentRequest(context.Context, string, journal.AgentRequestCompleted) error
	LoadAgentRequests(context.Context, string) ([]journal.AgentRequestAudit, error)
}

type AgentRequest struct {
	AuditID                string
	CommandID              model.CommandID
	ExpectedCommandPayload string
	AgentID                string
	Method                 string
	Path                   string
	Headers                map[string][]string
	Body                   []byte
	RequestedAdvance       time.Duration
}

type ClockEnvelope struct {
	SimulationTime   time.Time
	SimulationEndsAt time.Time
	Remaining        time.Duration
	RealElapsed      time.Duration
	AppliedAdvance   time.Duration
}

type RunContext struct {
	Run                    simulation.RunRecord
	Session                *simulation.RunSession
	State                  simulation.State
	Clock                  ClockEnvelope
	PreviousSimulationTime time.Time
	RequestedAdvance       time.Duration
	ProcessedEvents        int
	NewLogs                int
	LogsCursor             string
	Duplicate              bool
}

type ApplicationResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
	Value      any
	Clock      ClockEnvelope
}

type RunAction func(context.Context, *RunContext) (ApplicationResponse, error)

type managedRun struct {
	gate       sync.Mutex
	session    *simulation.RunSession
	lastAccess uint64
	users      int
}

type RunManager struct {
	catalog simulation.RunRepository
	store   simulation.EventStore
	audit   AuditStore
	now     func() time.Time
	maxRuns int

	mu       sync.Mutex
	openMu   sync.Mutex
	access   uint64
	sessions map[string]*managedRun
}

func NewRunManager(catalog simulation.RunRepository, store simulation.EventStore, audit AuditStore, maxCachedRuns int) *RunManager {
	if maxCachedRuns <= 0 {
		maxCachedRuns = 4
	}
	return &RunManager{catalog: catalog, store: store, audit: audit, now: time.Now, maxRuns: maxCachedRuns, sessions: make(map[string]*managedRun)}
}

func (m *RunManager) Handle(ctx context.Context, runID string, request AgentRequest, action RunAction) (response ApplicationResponse, err error) {
	if runID == "" || request.AuditID == "" || request.Method == "" || request.Path == "" || action == nil {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	run, err := m.catalog.GetRun(ctx, runID)
	if err != nil {
		return ApplicationResponse{}, err
	}
	if request.AgentID != "" && request.AgentID != run.AgentID {
		return ApplicationResponse{}, ErrUnauthorized
	}
	managed, err := m.open(ctx, runID)
	if err != nil {
		return ApplicationResponse{}, err
	}
	defer m.release(runID, managed)
	run, err = m.catalog.GetRun(ctx, runID)
	if err != nil {
		return ApplicationResponse{}, err
	}
	if !managed.gate.TryLock() {
		return ApplicationResponse{}, ErrConcurrentRunRequest
	}
	defer managed.gate.Unlock()

	receivedAt := m.now().UTC()
	received := journal.AgentRequestReceived{RequestID: request.AuditID, CommandID: string(request.CommandID), AgentID: request.AgentID, Method: request.Method,
		Path: request.Path, Headers: redactHeaders(request.Headers), Body: append([]byte(nil), request.Body...), ReceivedAt: receivedAt}
	if err := m.audit.RecordAgentRequest(ctx, runID, received); err != nil {
		return ApplicationResponse{}, err
	}
	defer func() {
		status := response.StatusCode
		if status == 0 {
			status = ClassifyError(err).Status
			if status == 0 {
				status = 500
			}
		}
		completion := journal.AgentRequestCompleted{RequestID: request.AuditID, StatusCode: status,
			ResponseHeaders: response.Headers, ResponseBody: response.Body, CompletedAt: m.now().UTC()}
		if err != nil {
			completion.Error = err.Error()
		}
		if auditErr := m.audit.CompleteAgentRequest(context.Background(), runID, completion); err == nil && auditErr != nil {
			err = auditErr
		}
	}()

	state := managed.session.State()
	previousSimulationTime := state.Clock.CurrentTime
	previousLogCount := managed.session.LogCount()
	realElapsed := time.Duration(0)
	if !run.LastRealRequestAt.IsZero() && receivedAt.After(run.LastRealRequestAt) {
		realElapsed = receivedAt.Sub(run.LastRealRequestAt)
	}
	touchAt := receivedAt
	if run.LastRealRequestAt.After(touchAt) {
		touchAt = run.LastRealRequestAt
	}
	applied := time.Duration(0)
	processedEvents := 0
	duplicate := false
	if request.CommandID != "" {
		previous, exists := state.CommandPayloads[request.CommandID]
		duplicate = exists
		if exists && request.ExpectedCommandPayload != "" && previous != request.ExpectedCommandPayload {
			return ApplicationResponse{}, ErrIdempotencyConflict
		}
	}
	if request.RequestedAdvance > 0 && !duplicate && state.Status != simulation.RunRunning {
		return ApplicationResponse{}, simulation.ErrRunCompleted
	}
	if state.Status == simulation.RunRunning && !(request.RequestedAdvance > 0 && duplicate) {
		requested := request.RequestedAdvance
		commandID := model.CommandID("realtime:" + request.AuditID)
		if requested > 0 {
			commandID = request.CommandID
		}
		before := state.Clock.CurrentTime
		decided, execErr := managed.session.Execute(ctx, simulation.AdvanceTime{CommandID: commandID, RealElapsed: realElapsed, RequestedDuration: requested})
		if execErr != nil {
			return ApplicationResponse{}, execErr
		}
		processedEvents = len(decided)
		state = managed.session.State()
		applied = state.Clock.CurrentTime.Sub(before)
	}
	if err := m.catalog.TouchRun(ctx, runID, touchAt); err != nil {
		return ApplicationResponse{}, err
	}
	run.LastRealRequestAt, run.UpdatedAt, run.CurrentTime, run.Status = touchAt, touchAt, state.Clock.CurrentTime, state.Status
	remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
	if remaining < 0 {
		remaining = 0
	}
	currentLogCount := managed.session.LogCount()
	runContext := &RunContext{Run: run, Session: managed.session, State: state, Duplicate: duplicate,
		PreviousSimulationTime: previousSimulationTime, RequestedAdvance: request.RequestedAdvance,
		ProcessedEvents: processedEvents, NewLogs: currentLogCount - previousLogCount, LogsCursor: simulation.LogCursor(currentLogCount),
		Clock: ClockEnvelope{SimulationTime: state.Clock.CurrentTime, SimulationEndsAt: state.Clock.EndsAt, Remaining: remaining,
			RealElapsed: realElapsed, AppliedAdvance: applied}}
	response, err = action(ctx, runContext)
	response.Clock = runContext.Clock
	return response, err
}

func (m *RunManager) open(ctx context.Context, runID string) (*managedRun, error) {
	m.openMu.Lock()
	defer m.openMu.Unlock()
	m.mu.Lock()
	if existing := m.sessions[runID]; existing != nil {
		m.access++
		existing.lastAccess = m.access
		existing.users++
		m.mu.Unlock()
		return existing, nil
	}
	m.mu.Unlock()

	session, err := simulation.OpenRunSession(ctx, m.store, runID)
	if err != nil {
		return nil, err
	}
	if err := m.recoverIncompleteAudits(ctx, runID, session); err != nil {
		return nil, err
	}
	managed := &managedRun{session: session, users: 1}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[runID]; existing != nil {
		existing.users++
		return existing, nil
	}
	m.access++
	managed.lastAccess = m.access
	m.sessions[runID] = managed
	m.evictUnlocked(runID)
	return managed, nil
}

func (m *RunManager) release(runID string, managed *managedRun) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.sessions[runID]; current == managed && managed.users > 0 {
		managed.users--
	}
	m.evictUnlocked("")
}

func (m *RunManager) evictUnlocked(protected string) {
	for len(m.sessions) > m.maxRuns {
		var candidate string
		oldest := ^uint64(0)
		for runID, session := range m.sessions {
			if runID != protected && session.users == 0 && session.lastAccess < oldest && session.gate.TryLock() {
				session.gate.Unlock()
				candidate, oldest = runID, session.lastAccess
			}
		}
		if candidate == "" {
			return
		}
		delete(m.sessions, candidate)
	}
}

func redactHeaders(headers map[string][]string) map[string][]string {
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		if strings.EqualFold(key, "X-Agent-API-Key") {
			result[key] = []string{"[REDACTED]"}
			continue
		}
		result[key] = append([]string(nil), values...)
	}
	return result
}

func (m *RunManager) RecoverIncompleteAudits(ctx context.Context, runID string) error {
	session, err := simulation.OpenRunSession(ctx, m.store, runID)
	if err != nil {
		return err
	}
	return m.recoverIncompleteAudits(ctx, runID, session)
}

func (m *RunManager) recoverIncompleteAudits(ctx context.Context, runID string, session *simulation.RunSession) error {
	audits, err := m.audit.LoadAgentRequests(ctx, runID)
	if err != nil {
		return err
	}
	for _, audit := range audits {
		if audit.Completed != nil {
			continue
		}
		state := session.State()
		commandID := model.CommandID(audit.Received.CommandID)
		_, commandApplied := state.CommandPayloads[commandID]
		_, realtimeApplied := state.CommandPayloads[model.CommandID("realtime:"+audit.Received.RequestID)]
		if commandApplied || realtimeApplied {
			run, err := m.catalog.GetRun(ctx, runID)
			if err != nil {
				return err
			}
			if run.LastRealRequestAt.Before(audit.Received.ReceivedAt) {
				if err := m.catalog.TouchRun(ctx, runID, audit.Received.ReceivedAt); err != nil {
					return err
				}
			}
		}
		completion := journal.AgentRequestCompleted{RequestID: audit.Received.RequestID, StatusCode: 500,
			Error: "request interrupted by simulator restart", CompletedAt: m.now().UTC()}
		if err := m.audit.CompleteAgentRequest(ctx, runID, completion); err != nil {
			return fmt.Errorf("recover request %s: %w", audit.Received.RequestID, err)
		}
	}
	return nil
}
