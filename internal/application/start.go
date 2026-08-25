package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

var (
	ErrInvalidRequest      = errors.New("invalid application request")
	ErrInvalidSeed         = errors.New("seed must be a non-zero integer")
	ErrMinimumAdvance      = errors.New("duration_seconds must be at least 300")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrManualWorldNotFound = errors.New("manual world not found")
)

type Catalog interface {
	generator.WorldRepository
	simulation.RunRepository
	FindManualWorld(ctx context.Context, seed int64) (generator.WorldDefinition, error)
	GetWorld(ctx context.Context, worldID string) (generator.WorldDefinition, error)
}

type StartRunRequest struct {
	Seed         int64
	AgentID      string
	AgentVersion string
	RequestID    string
}

type StartRunResult struct {
	Run     simulation.RunRecord
	State   simulation.State
	Created bool
}

type StartRunService struct {
	catalog   Catalog
	store     simulation.EventStore
	generator *generator.Generator
	now       func() time.Time
	newRunID  func() (string, error)
	audit     AuditStore
}

func NewStartRunService(catalog Catalog, store simulation.EventStore, worldGenerator *generator.Generator, audit ...AuditStore) *StartRunService {
	service := &StartRunService{catalog: catalog, store: store, generator: worldGenerator, now: time.Now, newRunID: NewRunID}
	if len(audit) > 0 {
		service.audit = audit[0]
	}
	return service
}

func (s *StartRunService) StartAudited(ctx context.Context, request StartRunRequest, agentRequest AgentRequest) (StartRunResult, error) {
	receivedAt := s.now().UTC()
	result, err := s.Start(ctx, request)
	if err != nil || s.audit == nil {
		return result, err
	}
	if agentRequest.AuditID == "" || agentRequest.Method == "" || agentRequest.Path == "" {
		return StartRunResult{}, ErrInvalidRequest
	}
	received := journal.AgentRequestReceived{RequestID: agentRequest.AuditID, AgentID: request.AgentID,
		Method: agentRequest.Method, Path: agentRequest.Path, Headers: redactHeaders(agentRequest.Headers),
		Body: append([]byte(nil), agentRequest.Body...), ReceivedAt: receivedAt}
	if err := s.audit.RecordAgentRequest(ctx, result.Run.RunID, received); err != nil {
		return StartRunResult{}, err
	}
	status := 200
	if result.Created {
		status = 201
	}
	if err := s.audit.CompleteAgentRequest(ctx, result.Run.RunID, journal.AgentRequestCompleted{
		RequestID: agentRequest.AuditID, StatusCode: status, CompletedAt: s.now().UTC(),
	}); err != nil {
		return StartRunResult{}, err
	}
	return result, nil
}

func (s *StartRunService) Start(ctx context.Context, request StartRunRequest) (StartRunResult, error) {
	if request.Seed == 0 {
		return StartRunResult{}, ErrInvalidSeed
	}
	if !ValidAgentID(request.AgentID) || !ValidAgentVersion(request.AgentVersion) || !ValidRequestID(request.RequestID) {
		return StartRunResult{}, ErrInvalidRequest
	}
	if existing, err := s.catalog.FindByStartRequest(ctx, request.AgentID, request.AgentVersion, request.RequestID); err == nil {
		world, worldErr := s.catalog.GetWorld(ctx, existing.WorldID)
		if worldErr != nil {
			return StartRunResult{}, worldErr
		}
		if world.Key.Seed != request.Seed {
			return StartRunResult{}, ErrIdempotencyConflict
		}
		state, err := s.initialize(ctx, existing, world)
		return StartRunResult{Run: existing, State: state, Created: false}, err
	} else if !errors.Is(err, simulation.ErrRunNotFound) {
		return StartRunResult{}, err
	}

	world, err := s.world(ctx, request.Seed)
	if err != nil {
		return StartRunResult{}, err
	}
	now := s.now().UTC()
	for attempt := 0; attempt < 8; attempt++ {
		runID, idErr := s.newRunID()
		if idErr != nil {
			return StartRunResult{}, idErr
		}
		run := simulation.RunRecord{RunID: runID, WorldID: world.WorldID, AgentID: request.AgentID,
			AgentVersion: request.AgentVersion, StartRequestID: request.RequestID, Status: simulation.RunNotCreated,
			StartedAt: world.StartsAt, EndsAt: world.EndsAt, CurrentTime: world.StartsAt,
			LastRealRequestAt: now, CreatedAt: now, UpdatedAt: now}
		if err := s.catalog.CreateRun(ctx, run); err != nil {
			if errors.Is(err, simulation.ErrRunAlreadyExists) {
				if existing, findErr := s.catalog.FindByStartRequest(ctx, request.AgentID, request.AgentVersion, request.RequestID); findErr == nil {
					state, initErr := s.initialize(ctx, existing, world)
					return StartRunResult{Run: existing, State: state}, initErr
				}
				continue
			}
			return StartRunResult{}, err
		}
		state, err := s.initialize(ctx, run, world)
		return StartRunResult{Run: run, State: state, Created: true}, err
	}
	return StartRunResult{}, fmt.Errorf("allocate run id: %w", simulation.ErrRunAlreadyExists)
}

func (s *StartRunService) world(ctx context.Context, seed int64) (generator.WorldDefinition, error) {
	if seed < 0 {
		world, err := s.catalog.FindManualWorld(ctx, seed)
		if errors.Is(err, generator.ErrWorldNotFound) {
			return generator.WorldDefinition{}, ErrManualWorldNotFound
		}
		return world, err
	}
	if s.generator == nil {
		return generator.WorldDefinition{}, ErrInvalidRequest
	}
	world, _, err := s.generator.GetOrCreate(ctx, s.catalog, seed)
	return world, err
}

func (s *StartRunService) initialize(ctx context.Context, run simulation.RunRecord, world generator.WorldDefinition) (simulation.State, error) {
	var state simulation.State
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		state, err = s.initializeOnce(ctx, run, world)
		if !errors.Is(err, simulation.ErrVersionConflict) {
			return state, err
		}
	}
	return state, err
}

func (s *StartRunService) initializeOnce(ctx context.Context, run simulation.RunRecord, world generator.WorldDefinition) (simulation.State, error) {
	records, err := s.store.Load(ctx, run.RunID)
	if err != nil {
		return simulation.State{}, err
	}
	state, err := simulation.Rehydrate(records)
	if err != nil {
		return simulation.State{}, err
	}
	if state.Version > 0 && (state.Seed != world.Key.Seed || !state.Clock.StartedAt.Equal(world.StartsAt) || !state.Clock.EndsAt.Equal(world.EndsAt) || len(state.Schedule) > len(world.Events)) {
		return simulation.State{}, fmt.Errorf("run journal does not match world %s", world.WorldID)
	}
	for index := range state.Schedule {
		if !reflect.DeepEqual(state.Schedule[index], world.Events[index]) {
			return simulation.State{}, fmt.Errorf("run schedule does not match world %s at index %d", world.WorldID, index)
		}
	}
	if state.Version == 0 {
		initial := make([]events.Event, 0, 1+len(world.Bootstrap))
		initial = append(initial, events.WorldCreated{RunID: run.RunID, Seed: world.Key.Seed, StartedAt: world.StartsAt, EndsAt: world.EndsAt})
		initial = append(initial, world.Bootstrap...)
		appended, err := s.store.Append(ctx, run.RunID, 0, initial)
		if err != nil {
			return simulation.State{}, err
		}
		state, err = simulation.Rehydrate(appended)
		if err != nil {
			return simulation.State{}, err
		}
	}
	const chunkSize = 10_000
	for offset := len(state.Schedule); offset < len(world.Events); offset += chunkSize {
		end := offset + chunkSize
		if end > len(world.Events) {
			end = len(world.Events)
		}
		appended, err := s.store.Append(ctx, run.RunID, state.Version, []events.Event{
			events.WorldScheduleCreated{Schedule: world.Events[offset:end], CreatedAt: world.StartsAt},
		})
		if err != nil {
			return simulation.State{}, err
		}
		for _, record := range appended {
			if err := state.Apply(record.Event); err != nil {
				return simulation.State{}, err
			}
			state.Version = record.Version
		}
	}
	return state, nil
}

const runIDAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func NewRunID() (string, error) {
	result := make([]byte, 24)
	buffer := make([]byte, 32)
	for offset := 0; offset < len(result); {
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		for _, value := range buffer {
			if value >= 248 {
				continue
			}
			result[offset] = runIDAlphabet[int(value)%len(runIDAlphabet)]
			offset++
			if offset == len(result) {
				break
			}
		}
	}
	return string(result), nil
}
