package spec_test

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

type worldGeneratorScenario struct {
	t         *testing.T
	generator *generator.Generator
	world     generator.WorldDefinition
}

func newWorldGeneratorScenario(t *testing.T) *worldGeneratorScenario {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	profile, err := generator.LoadProfileFile(filepath.Join(filepath.Dir(file), "..", "config", "world-generation.v1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	profile.Traffic.BaseArrivalsPerHour = 1
	for hour := range profile.Traffic.HourlyMultipliers {
		profile.Traffic.HourlyMultipliers[hour] = 0
	}
	profile.Traffic.HourlyMultipliers[12] = generator.ProbabilityScale
	for day := range profile.Traffic.WeekdayMultipliers {
		profile.Traffic.WeekdayMultipliers[day] = generator.ProbabilityScale
	}
	profile.Traffic.SpecialDays = nil
	profile.Traffic.NoiseMultiplier = generator.PPMRange{Min: generator.ProbabilityScale, Max: generator.ProbabilityScale}
	profile.Traffic.MaxArrivalsPerHour = 2
	profile.Catalog.ProductCount = generator.IntRange{Min: 3, Max: 3}
	profile.Bugs.InitialBugCount = generator.IntRange{Min: 1, Max: 1}
	profile.Deployments.Count = generator.IntRange{Min: 3, Max: 3}
	profile.Deployments.FailureProbability = generator.PPMRange{Min: 0, Max: 0}
	profile.Deployments.EffectsPerDeployment = generator.IntRange{Min: 3, Max: 3}
	profile.Deployments.NewBugProbabilityPPM = generator.ProbabilityScale
	profile.Deployments.NewBugCount = generator.IntRange{Min: 1, Max: 1}
	profile.DDoS.AttackCount = generator.IntRange{Min: 3, Max: 3}
	profile.Limits.MaxVisitors = 1000
	profile.Limits.MaxScheduledEvents = 2000
	worldGenerator, err := generator.New(profile)
	if err != nil {
		t.Fatal(err)
	}
	return &worldGeneratorScenario{t: t, generator: worldGenerator}
}

func (s *worldGeneratorScenario) WhenWorldIsGenerated(seed int64) {
	s.t.Helper()
	world, err := s.generator.Generate(seed)
	if err != nil {
		s.t.Fatal(err)
	}
	s.world = world
}

func (s *worldGeneratorScenario) ThenBootstrapCanBeReplayed() {
	s.t.Helper()
	stream := []events.Event{events.WorldCreated{RunID: "generated-run", Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}}
	stream = append(stream, s.world.Bootstrap...)
	stream = append(stream, events.WorldScheduleCreated{Schedule: s.world.Events, CreatedAt: s.world.StartsAt})
	records := make([]simulation.StoredEvent, 0, len(stream))
	for index, event := range stream {
		records = append(records, simulation.StoredEvent{Version: uint64(index + 1), Event: event})
	}
	state, err := simulation.Rehydrate(records)
	if err != nil {
		s.t.Fatalf("generated bootstrap cannot be replayed: %v", err)
	}
	if len(state.Products) != 3 || len(state.Deployments) != 3 || len(state.Bugs) != 1 {
		s.t.Fatalf("generated state has products/deployments/bugs = %d/%d/%d", len(state.Products), len(state.Deployments), len(state.Bugs))
	}
}

func (s *worldGeneratorScenario) ThenScheduleContainsTrafficAndDDoS() {
	s.t.Helper()
	counts := map[string]int{}
	for index, scheduled := range s.world.Events {
		counts[scheduled.Event.EventType()]++
		if scheduled.Sequence != uint64(index+1) {
			s.t.Fatalf("sequence[%d] = %d", index, scheduled.Sequence)
		}
		if index > 0 && scheduled.OccursAt.Before(s.world.Events[index-1].OccursAt) {
			s.t.Fatal("schedule is not chronological")
		}
	}
	if counts["VisitorArrived"] == 0 || counts["TrafficAttackStarted"] != 3 || counts["TrafficAttackEnded"] != 3 {
		s.t.Fatalf("schedule event counts = %#v", counts)
	}
}

func TestWorldGeneratorIsDeterministicForSeedAndProfile(t *testing.T) {
	first := newWorldGeneratorScenario(t)
	second := newWorldGeneratorScenario(t)
	first.WhenWorldIsGenerated(42)
	second.WhenWorldIsGenerated(42)
	if !reflect.DeepEqual(first.world, second.world) {
		t.Fatal("same seed and profile generated different worlds")
	}
}

func TestWorldGeneratorProducesDifferentWorldForDifferentSeed(t *testing.T) {
	first := newWorldGeneratorScenario(t)
	second := newWorldGeneratorScenario(t)
	first.WhenWorldIsGenerated(42)
	second.WhenWorldIsGenerated(43)
	if first.world.WorldID == second.world.WorldID || first.world.ScheduleHash == second.world.ScheduleHash {
		t.Fatal("different seeds generated the same world identity")
	}
}

func TestGeneratedWorldContainsReplayableBootstrapAndAnnualSchedule(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	s.ThenBootstrapCanBeReplayed()
	s.ThenScheduleContainsTrafficAndDDoS()
}

func TestGeneratedWorldIsReusedBySeedAndGeneratorKey(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	repository := generator.NewMemoryWorldRepository()
	first, created, err := s.generator.GetOrCreate(context.Background(), repository, 42)
	if err != nil || !created {
		t.Fatalf("first get-or-create = created:%v err:%v", created, err)
	}
	second, created, err := s.generator.GetOrCreate(context.Background(), repository, 42)
	if err != nil || created {
		t.Fatalf("second get-or-create = created:%v err:%v", created, err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("repository returned another world for the same key")
	}
}

func TestGeneratedWorldIsReusedFromSQLiteAfterRestart(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	path := filepath.Join(t.TempDir(), "worlds.db")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := s.generator.GetOrCreate(context.Background(), store, 42)
	if err != nil || !created {
		t.Fatalf("first generation = created:%v err:%v", created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	second, created, err := s.generator.GetOrCreate(context.Background(), restarted, 42)
	if err != nil || created {
		t.Fatalf("restarted generation = created:%v err:%v", created, err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("SQLite did not return the persisted generated world")
	}
}

func TestGeneratedDeploymentAcceleratesFutureDeploymentsAndCanCreateBug(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	store := simulation.NewMemoryEventStore()
	initial := []events.Event{events.WorldCreated{RunID: "generated-deployment-run", Seed: 42, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}}
	initial = append(initial, s.world.Bootstrap...)
	initial = append(initial, events.WorldScheduleCreated{Schedule: s.world.Events, CreatedAt: s.world.StartsAt})
	if _, err := store.Append(context.Background(), "generated-deployment-run", 0, initial); err != nil {
		t.Fatal(err)
	}
	handler := simulation.NewHandler(store)
	before, err := handler.State(context.Background(), "generated-deployment-run")
	if err != nil {
		t.Fatal(err)
	}
	secondDuration := before.Deployments["deployment-002"].Duration
	initialBugCount := len(before.Bugs)
	first := before.Deployments["deployment-001"]
	if _, err := handler.Execute(context.Background(), "generated-deployment-run", simulation.StartDeployment{
		CommandID: "generated-deploy-command", DeploymentID: first.ID, OperationID: "generated-deploy-operation",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Execute(context.Background(), "generated-deployment-run", simulation.AdvanceTime{RequestedDuration: first.Duration}); err != nil {
		t.Fatal(err)
	}
	after, err := handler.State(context.Background(), "generated-deployment-run")
	if err != nil {
		t.Fatal(err)
	}
	if after.Deployments["deployment-002"].Duration >= secondDuration {
		t.Fatal("future deployment duration was not reduced")
	}
	if len(after.Bugs) <= initialBugCount {
		t.Fatal("successful generated deployment did not activate its generated bug")
	}
}

func TestConcurrentGenerationOfSameSeedPersistsOneWorld(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "concurrent-worlds.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	type result struct {
		world   generator.WorldDefinition
		created bool
		err     error
	}
	results := make([]result, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index].world, results[index].created, results[index].err = s.generator.GetOrCreate(context.Background(), store, 42)
		}(index)
	}
	wait.Wait()
	for _, result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
	}
	if results[0].created == results[1].created {
		t.Fatalf("created flags = %v/%v, want exactly one", results[0].created, results[1].created)
	}
	if !reflect.DeepEqual(results[0].world, results[1].world) {
		t.Fatal("concurrent callers received different worlds")
	}
}
