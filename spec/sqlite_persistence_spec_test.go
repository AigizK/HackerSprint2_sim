package spec_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

type sqlitePersistenceScenario struct {
	t           *testing.T
	ctx         context.Context
	path        string
	store       *sqlite.Store
	events      *journal.Store
	journalPath string
	world       generator.WorldDefinition
	loaded      generator.WorldDefinition
}

func newSQLitePersistenceScenario(t *testing.T) *sqlitePersistenceScenario {
	root := t.TempDir()
	return &sqlitePersistenceScenario{t: t, ctx: context.Background(), path: filepath.Join(root, "catalog.db"), journalPath: filepath.Join(root, "streams")}
}

func (s *sqlitePersistenceScenario) GivenOpenDatabase() {
	s.t.Helper()
	store, err := sqlite.Open(s.path)
	if err != nil {
		s.t.Fatal(err)
	}
	s.store = store
	s.events, err = journal.Open(s.journalPath)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() {
		if s.store != nil {
			_ = s.store.Close()
		}
	})
}

func (s *sqlitePersistenceScenario) GivenGeneratedWorld() {
	s.world = generatedWorldDefinition()
}

func (s *sqlitePersistenceScenario) WhenWorldIsSaved() {
	s.t.Helper()
	if err := s.store.CreateWorld(s.ctx, s.world); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sqlitePersistenceScenario) WhenDatabaseIsRestarted() {
	s.t.Helper()
	if err := s.store.Close(); err != nil {
		s.t.Fatal(err)
	}
	s.store = nil
	s.GivenOpenDatabase()
}

func (s *sqlitePersistenceScenario) WhenWorldIsLoaded() {
	s.t.Helper()
	world, err := s.store.FindWorld(s.ctx, s.world.Key)
	if err != nil {
		s.t.Fatal(err)
	}
	s.loaded = world
}

func (s *sqlitePersistenceScenario) ThenSameWorldIsReturned() {
	s.t.Helper()
	if s.loaded.WorldID != s.world.WorldID || s.loaded.ScheduleHash != s.world.ScheduleHash || len(s.loaded.Events) != 0 || len(s.loaded.Bootstrap) != 0 {
		s.t.Fatalf("catalog world = %#v", s.loaded)
	}
}

func TestSQLiteCatalogStoresWorldMetadataButNotGeneratedEvents(t *testing.T) {
	s := newSQLitePersistenceScenario(t)
	s.GivenOpenDatabase()
	s.GivenGeneratedWorld()
	s.WhenWorldIsSaved()
	s.WhenDatabaseIsRestarted()
	s.WhenWorldIsLoaded()
	s.ThenSameWorldIsReturned()
}

func TestWorldKeyCannotBeGeneratedTwice(t *testing.T) {
	s := newSQLitePersistenceScenario(t)
	s.GivenOpenDatabase()
	s.GivenGeneratedWorld()
	s.WhenWorldIsSaved()
	if err := s.store.CreateWorld(s.ctx, s.world); !errors.Is(err, generator.ErrWorldAlreadyExists) {
		t.Fatalf("second create error = %v", err)
	}
}

func TestRunContinuesFromSQLiteEventStreamAfterRestart(t *testing.T) {
	s := newSQLitePersistenceScenario(t)
	s.GivenOpenDatabase()
	s.GivenGeneratedWorld()
	s.WhenWorldIsSaved()
	createPersistentRun(t, s.store, "run-persistent", s.world)

	handler := simulation.NewHandler(s.events)
	if _, err := handler.Execute(s.ctx, "run-persistent", simulation.CreateWorld{
		Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Execute(s.ctx, "run-persistent", simulation.AddProduct{
		ProductID: "persisted-product", Name: "Persisted product", PriceMinor: 12_990,
		ViewProbabilityPPM: 1_000_000, PurchaseProbabilityPPM: 1_000_000,
	}); err != nil {
		t.Fatal(err)
	}

	s.WhenDatabaseIsRestarted()
	restartedHandler := simulation.NewHandler(s.events)
	if _, err := restartedHandler.Execute(s.ctx, "run-persistent", simulation.AdvanceTime{RequestedDuration: 5 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	state, err := restartedHandler.State(s.ctx, "run-persistent")
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 3 || !state.Clock.CurrentTime.Equal(s.world.StartsAt.Add(5*time.Minute)) {
		t.Fatalf("restored state version/time = %d/%s", state.Version, state.Clock.CurrentTime)
	}
	if _, exists := state.Products["persisted-product"]; !exists {
		t.Fatal("persisted product was lost after restart")
	}
}

func TestRunsOfSameStoredWorldHaveIndependentStreams(t *testing.T) {
	s := newSQLitePersistenceScenario(t)
	s.GivenOpenDatabase()
	s.GivenGeneratedWorld()
	s.WhenWorldIsSaved()
	createPersistentRun(t, s.store, "run-one", s.world)
	createPersistentRun(t, s.store, "run-two", s.world)
	handler := simulation.NewHandler(s.events)
	for _, runID := range []string{"run-one", "run-two"} {
		if _, err := handler.Execute(s.ctx, runID, simulation.CreateWorld{Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := handler.Execute(s.ctx, "run-one", simulation.AddProduct{
		ProductID: "only-first", Name: "Only first", PriceMinor: 1000,
		ViewProbabilityPPM: 100_000, PurchaseProbabilityPPM: 100_000,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := handler.State(s.ctx, "run-two")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := second.Products["only-first"]; exists {
		t.Fatal("run events leaked into another run")
	}
}

func TestEmptyRunCacheCanBeRebuiltFromSQLite(t *testing.T) {
	s := newSQLitePersistenceScenario(t)
	s.GivenOpenDatabase()
	s.GivenGeneratedWorld()
	s.WhenWorldIsSaved()
	createPersistentRun(t, s.store, "run-cached", s.world)
	firstCache := simulation.NewCachedEventStore(s.events, 10)
	firstHandler := simulation.NewHandler(firstCache)
	if _, err := firstHandler.Execute(s.ctx, "run-cached", simulation.CreateWorld{Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}); err != nil {
		t.Fatal(err)
	}

	// Simulates process cache loss without touching the persistent SQLite stream.
	secondCache := simulation.NewCachedEventStore(s.events, 10)
	state, err := simulation.NewHandler(secondCache).State(s.ctx, "run-cached")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != simulation.RunRunning || state.Version != 1 {
		t.Fatalf("state was not rebuilt: %#v", state)
	}
}

func generatedWorldDefinition() generator.WorldDefinition {
	startsAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	arrivesAt := startsAt.Add(5 * time.Minute)
	return generator.WorldDefinition{
		WorldID:        "world-seed-42-v1",
		Key:            generator.WorldKey{Seed: 42, ProfileHash: "profile-hash-v1", GeneratorVersion: "generator-v1"},
		ProfileVersion: "world-generation.v1", ScheduleHash: "schedule-hash-v1", Source: "generated",
		StartsAt: startsAt, EndsAt: startsAt.AddDate(1, 0, 0), CreatedAt: startsAt,
		Evaluation: generator.WorldEvaluation{
			EvaluatorVersion: generator.EvaluatorVersion, MaximumBalanceMinor: 42_000,
			AgentRequestCount: 2, AgentRequestDuration: 10 * time.Second, MinimumRealTime: 20 * time.Second,
			OptimalPlan: []generator.OracleAction{{Kind: "start", At: startsAt}, {Kind: "advance_time", At: startsAt}},
		},
		Events: events.EventSchedule{{Sequence: 1, OccursAt: arrivesAt, Event: events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: arrivesAt}}},
	}
}

func createPersistentRun(t *testing.T, store *sqlite.Store, runID string, world generator.WorldDefinition) {
	t.Helper()
	err := store.CreateRun(context.Background(), simulation.RunRecord{
		RunID: runID, WorldID: world.WorldID, AgentID: "agent-" + runID, AgentVersion: "v1",
		StartRequestID: "start-" + runID, Status: simulation.RunNotCreated,
		StartedAt: world.StartsAt, EndsAt: world.EndsAt, CurrentTime: world.StartsAt,
		CreatedAt: world.CreatedAt, UpdatedAt: world.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
}
