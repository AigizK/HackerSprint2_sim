package spec_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type worldGeneratorScenario struct {
	t         *testing.T
	generator *generator.Generator
	world     generator.WorldDefinition
}

func newWorldGeneratorScenario(t *testing.T) *worldGeneratorScenario {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	profile, err := generator.LoadProfileFile(filepath.Join(filepath.Dir(file), "..", "config", "world-generation.v2.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	profile.Traffic.BaseArrivalsPerHour = 1
	profile.Traffic.TargetScheduledEvents = 100
	for hour := range profile.Traffic.HourlyMultipliers {
		profile.Traffic.HourlyMultipliers[hour] = 0
	}
	profile.Traffic.HourlyMultipliers[12] = generator.ProbabilityScale
	for day := range profile.Traffic.WeekdayMultipliers {
		profile.Traffic.WeekdayMultipliers[day] = generator.ProbabilityScale
	}
	profile.Traffic.SpecialDays = nil
	profile.Traffic.NoiseMultiplier = generator.PPMRange{Min: generator.ProbabilityScale, Max: generator.ProbabilityScale}
	profile.Traffic.MaxArrivalsPerHour = 100
	profile.Catalog.ProductCount = generator.IntRange{Min: 3, Max: 3}
	profile.DDoS.AttackCount = generator.IntRange{Min: 3, Max: 3}
	profile.Infrastructure.BackendSurgeVisitors = 0
	profile.Infrastructure.DatabaseConnectionSurgeVisitors = 0
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
	if len(state.Products) != 3 || len(state.Pages) != 2 || len(state.InboxMessages) != 1 || state.Costs.Currency != "RUB" ||
		len(state.ServerTypes) != 4 || state.Site.DatabaseID != "db-main" || state.Site.Status != model.SiteRunning {
		s.t.Fatalf("unexpected bootstrap: %#v", state)
	}

	for _, product := range state.Products {
		if product.Version != 1 || !product.Available || product.Manufacturer == "" || product.Description == "" {
			s.t.Fatalf("generated product %q = version:%d available:%v manufacturer:%q description:%q",
				product.ID, product.Version, product.Available, product.Manufacturer, product.Description)
		}
	}
}

func (s *worldGeneratorScenario) ThenScheduleContainsTrafficAndDDoS() {
	s.t.Helper()
	counts := map[string]int{}
	for index, scheduled := range s.world.Events {
		counts[scheduled.Event.EventType()]++
		switch event := scheduled.Event.(type) {
		case events.VisitorArrived:
			if event.SourceIP == "" || event.UserAgent == "" || event.RegionCode == "" {
				s.t.Fatalf("visitor %s has no firewall identity", event.VisitorID)
			}
		case events.TrafficAttackStarted:
			if event.SourceCIDR == "" || event.UserAgent == "" || event.RegionCode == "" {
				s.t.Fatalf("attack %s has no firewall identity", event.AttackID)
			}
		}
		if scheduled.Sequence != uint64(index+1) {
			s.t.Fatalf("sequence[%d] = %d", index, scheduled.Sequence)
		}
		if index > 0 && scheduled.OccursAt.Before(s.world.Events[index-1].OccursAt) {
			s.t.Fatal("schedule is not chronological")
		}
	}
	if counts["VisitorArrived"] == 0 || counts["TrafficAttackStarted"] != 3 || counts["TrafficAttackEnded"] != 3 ||
		counts["DatabaseGrowthRequested"] != 24 || counts["DiskLogsGrowthRequested"] != 10 ||
		counts["ServerCredentialRotationRequested"] != int(s.generator.Profile().Credentials.RotationEvents) {
		s.t.Fatalf("schedule event counts = %#v", counts)
	}
	if len(s.world.Events) != 100 {
		s.t.Fatalf("scheduled events = %d, want 100", len(s.world.Events))
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

func TestGeneratedWorldContainsReplayableBootstrapAndWeeklySchedule(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	location, err := time.LoadLocation(s.generator.Profile().Clock.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	if !s.world.EndsAt.Equal(s.world.StartsAt.Add(7*24*time.Hour)) || s.world.StartsAt.In(location).Weekday() != time.Monday {
		t.Fatalf("world clock = %s..%s", s.world.StartsAt, s.world.EndsAt)
	}
	s.ThenBootstrapCanBeReplayed()
	s.ThenScheduleContainsTrafficAndDDoS()
}

func TestGeneratedDDoSOverloadsOneBackendAndMatchingFirewallRemovesItsLoad(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	var attack events.TrafficAttackStarted
	for _, scheduled := range s.world.Events {
		if candidate, ok := scheduled.Event.(events.TrafficAttackStarted); ok {
			attack = candidate
			break
		}
	}
	if attack.AttackID == "" {
		t.Fatal("generated week has no DDoS attack")
	}
	stream := []events.Event{events.WorldCreated{RunID: "generated-ddos", Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}}
	stream = append(stream, s.world.Bootstrap...)
	stream = append(stream,
		events.TimeAdvanced{From: s.world.StartsAt, To: attack.StartedAt, RequestedDuration: attack.StartedAt.Sub(s.world.StartsAt), AppliedDuration: attack.StartedAt.Sub(s.world.StartsAt)},
		attack,
	)
	state := rehydrateEvents(t, stream)
	request := simulation.OpenPage{RequestID: "generated-ddos-unblocked", VisitorID: "generated-ddos-visitor", Page: attack.TargetPage}
	if attack.TargetPage == model.PageProduct {
		request.ProductID = "product-001"
	}
	decided, err := simulation.Decide("generated-ddos", state, request)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRequestFailure(decided, model.FailureServerCapacityExceeded) {
		t.Fatalf("generated attack does not overload one backend: %#v", decided)
	}

	ruleEvents, err := simulation.Decide("generated-ddos", state, simulation.UpsertFirewallRule{CommandID: "block-generated-ddos", Rule: model.FirewallRule{
		ID: "block-generated-ddos", Priority: 1, Action: model.FirewallDeny, Enabled: true,
		Match: model.FirewallMatch{SourceCIDR: attack.SourceCIDR},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range ruleEvents {
		if err := state.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	request.RequestID = "generated-ddos-blocked"
	decided, err = simulation.Decide("generated-ddos", state, request)
	if err != nil {
		t.Fatal(err)
	}
	if containsRequestFailure(decided, model.FailureServerCapacityExceeded) || !containsSuccessfulRequest(decided) {
		t.Fatalf("matching firewall did not remove DDoS load: %#v", decided)
	}
}

func TestGeneratedOrganicSurgeNeedsTwoBackendsWithoutDDoS(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	profile, err := generator.LoadProfileFile(filepath.Join(filepath.Dir(file), "..", "config", "world-generation.v2.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	worldGenerator, err := generator.New(profile)
	if err != nil {
		t.Fatal(err)
	}
	world, err := worldGenerator.Generate(42)
	if err != nil {
		t.Fatal(err)
	}

	groups := make(map[time.Time]events.EventSchedule)
	for _, scheduled := range world.Events {
		if _, visitor := scheduled.Event.(events.VisitorArrived); visitor && scheduled.OccursAt.Before(world.StartsAt.Add(12*time.Hour)) {
			groups[scheduled.OccursAt] = append(groups[scheduled.OccursAt], scheduled)
		}
	}
	var surge events.EventSchedule
	for _, group := range groups {
		if len(group) > 1 && int64(len(group)) <= profile.Infrastructure.BackendSurgeVisitors {
			surge = append(surge, group...)
			break
		}
	}
	if len(surge) == 0 {
		t.Fatalf("generated surge is missing (configured maximum %d visitors)", profile.Infrastructure.BackendSurgeVisitors)
	}
	for index := range surge {
		surge[index].Sequence = uint64(index + 1)
	}

	run := func(runID string, backendCount int) int {
		t.Helper()
		ctx := context.Background()
		store := simulation.NewMemoryEventStore()
		stream := []events.Event{events.WorldCreated{RunID: runID, Seed: world.Key.Seed, StartedAt: world.StartsAt, EndsAt: world.EndsAt}}
		stream = append(stream, world.Bootstrap...)
		if backendCount == 2 {
			stream = append(stream,
				events.ServerProvisioningStarted{OperationID: "surge-second-backend", ServerID: "server-surge-2", Name: "server-surge-2",
					InstanceType: model.InstanceBackendStandard, Role: model.ServerRoleBackend,
					CapacityUnits: profile.Infrastructure.ServerCapacityUnits, DiskBytes: profile.Infrastructure.DatabaseDiskXBytes,
					CostPerHourMinor: profile.Infrastructure.ServerCostPerHourMinor, StartedAt: world.StartsAt, ReadyAt: world.StartsAt},
				events.ServerActivated{OperationID: "surge-second-backend", ServerID: "server-surge-2", ActivatedAt: world.StartsAt},
			)
		}
		stream = append(stream, events.WorldScheduleCreated{Schedule: surge, CreatedAt: world.StartsAt})
		if _, appendErr := store.Append(ctx, runID, 0, stream); appendErr != nil {
			t.Fatal(appendErr)
		}
		session, openErr := simulation.OpenRunSession(ctx, store, runID)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, executeErr := session.Execute(ctx, simulation.AdvanceTime{CommandID: "reach-organic-surge", RequestedDuration: surge[0].OccursAt.Sub(world.StartsAt)}); executeErr != nil {
			t.Fatal(executeErr)
		}
		failures := 0
		for _, record := range session.Records() {
			if rejected, ok := record.Event.(events.PageRequestRejected); ok && rejected.RejectedAt.Equal(surge[0].OccursAt) && rejected.ErrorCode == model.FailureServerCapacityExceeded {
				failures++
			}
		}
		return failures
	}

	if failures := run("generated-organic-surge-one", 1); failures == 0 {
		t.Fatal("one backend handled the generated organic surge")
	}
	if failures := run("generated-organic-surge-two", 2); failures != 0 {
		t.Fatalf("two backends still rejected %d surge requests", failures)
	}
}

func TestGeneratedConnectionSurgeCanSaturateSmallDatabase(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	profile, err := generator.LoadProfileFile(filepath.Join(filepath.Dir(file), "..", "config", "world-generation.v2.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	worldGenerator, err := generator.New(profile)
	if err != nil {
		t.Fatal(err)
	}
	world, err := worldGenerator.Generate(42)
	if err != nil {
		t.Fatal(err)
	}

	groups := make(map[time.Time]events.EventSchedule)
	for _, scheduled := range world.Events {
		if _, visitor := scheduled.Event.(events.VisitorArrived); visitor && scheduled.OccursAt.Before(world.StartsAt.Add(12*time.Hour)) {
			groups[scheduled.OccursAt] = append(groups[scheduled.OccursAt], scheduled)
		}
	}
	var surge events.EventSchedule
	for _, group := range groups {
		if len(group) >= model.DBSmallConnections+1 {
			surge = append(surge, group...)
			break
		}
	}
	if len(surge) == 0 {
		t.Fatal("generated database connection surge is missing")
	}
	for index := range surge {
		surge[index].Sequence = uint64(index + 1)
	}

	state := rehydrateEvents(t, append([]events.Event{events.WorldCreated{
		RunID: "generated-db-connection-bootstrap", Seed: world.Key.Seed, StartedAt: world.StartsAt, EndsAt: world.EndsAt,
	}}, world.Bootstrap...))
	perVisitorLoad := state.Pages[model.PageProductList].LoadUnits + state.Pages[model.PageProduct].LoadUnits
	backendCount := int((int64(len(surge))*perVisitorLoad+profile.Infrastructure.ServerCapacityUnits-1)/profile.Infrastructure.ServerCapacityUnits) + 1
	if backendCount > profile.Infrastructure.MaxBackendInstances {
		t.Fatalf("connection surge needs %d backends, configured maximum is %d", backendCount, profile.Infrastructure.MaxBackendInstances)
	}

	ctx := context.Background()
	store := simulation.NewMemoryEventStore()
	const runID = "generated-db-connection-surge"
	stream := []events.Event{events.WorldCreated{RunID: runID, Seed: world.Key.Seed, StartedAt: world.StartsAt, EndsAt: world.EndsAt}}
	stream = append(stream, world.Bootstrap...)
	for index := 2; index <= backendCount; index++ {
		serverID := model.ServerID(fmt.Sprintf("connection-surge-backend-%d", index))
		operationID := model.OperationID("initial-" + string(serverID))
		stream = append(stream,
			events.ServerProvisioningStarted{OperationID: operationID, ServerID: serverID, Name: string(serverID),
				InstanceType: model.InstanceBackendStandard, Role: model.ServerRoleBackend,
				CapacityUnits: profile.Infrastructure.ServerCapacityUnits, DiskBytes: profile.Infrastructure.DatabaseDiskXBytes,
				CostPerHourMinor: profile.Infrastructure.ServerCostPerHourMinor, StartedAt: world.StartsAt, ReadyAt: world.StartsAt},
			events.ServerActivated{OperationID: operationID, ServerID: serverID, ActivatedAt: world.StartsAt},
		)
	}
	stream = append(stream, events.WorldScheduleCreated{Schedule: surge, CreatedAt: world.StartsAt})
	if _, err := store.Append(ctx, runID, 0, stream); err != nil {
		t.Fatal(err)
	}
	session, err := simulation.OpenRunSession(ctx, store, runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, simulation.AdvanceTime{CommandID: "reach-db-connection-surge", RequestedDuration: surge[0].OccursAt.Sub(world.StartsAt)}); err != nil {
		t.Fatal(err)
	}
	connectionFailures := 0
	for _, record := range session.Records() {
		if rejected, ok := record.Event.(events.PageRequestRejected); ok && rejected.RejectedAt.Equal(surge[0].OccursAt) &&
			rejected.ErrorCode == model.FailureDBConnectionLimit {
			connectionFailures++
		}
	}
	if connectionFailures == 0 {
		t.Fatal("generated connection surge did not saturate db.small")
	}
}

func TestGeneratedRealDataGrowthExhaustsSmallDiskEvenAfterLogCleanup(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	ctx := context.Background()
	store := simulation.NewMemoryEventStore()
	stream := []events.Event{events.WorldCreated{RunID: "generated-disk", Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}}
	stream = append(stream, s.world.Bootstrap...)
	stream = append(stream, events.WorldScheduleCreated{Schedule: s.world.Events, CreatedAt: s.world.StartsAt})
	if _, err := store.Append(ctx, "generated-disk", 0, stream); err != nil {
		t.Fatal(err)
	}
	session, err := simulation.OpenRunSession(ctx, store, "generated-disk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, simulation.AdvanceTime{CommandID: "reach-disk-pressure", RequestedDuration: 4 * 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, simulation.CleanupDatabaseLogs{CommandID: "clean-generated-db", ServerID: "db-server-initial"}); err != nil {
		t.Fatal(err)
	}
	state := session.State()
	database := state.Databases["db-main"]
	if _, full := database.AvailabilityReasons[model.DatabaseUnavailableDiskFull]; !full {
		t.Fatalf("db.small became healthy after log cleanup: %#v", database)
	}
	if len(state.PendingDatabaseGrowth) == 0 {
		t.Fatal("all real-data growth fit db.small; migration scenario is not guaranteed")
	}
}

func TestGeneratedBackendLogGrowthFillsDiskAndCleanupFreesIt(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	ctx := context.Background()
	store := simulation.NewMemoryEventStore()
	const runID = "generated-backend-disk"
	stream := []events.Event{events.WorldCreated{RunID: runID, Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}}
	stream = append(stream, s.world.Bootstrap...)
	stream = append(stream, events.WorldScheduleCreated{Schedule: s.world.Events, CreatedAt: s.world.StartsAt})
	if _, err := store.Append(ctx, runID, 0, stream); err != nil {
		t.Fatal(err)
	}
	session, err := simulation.OpenRunSession(ctx, store, runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, simulation.AdvanceTime{CommandID: "reach-backend-disk-pressure", RequestedDuration: 4 * 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	usage, err := simulation.NewProjection(session.Records(), session.State()).DiskUsage("server-initial-1")
	if err != nil {
		t.Fatal(err)
	}
	if usage.FreeBytes != 0 || usage.LogsBytes != usage.TotalBytes {
		t.Fatalf("generated backend disk usage = %#v, want full disk made only of logs", usage)
	}
	if _, err := session.Execute(ctx, simulation.CleanupDatabaseLogs{CommandID: "clean-generated-backend", ServerID: "server-initial-1"}); err != nil {
		t.Fatal(err)
	}
	usage, err = simulation.NewProjection(session.Records(), session.State()).DiskUsage("server-initial-1")
	if err != nil {
		t.Fatal(err)
	}
	if usage.FreeBytes != usage.TotalBytes || usage.LogsBytes != 0 {
		t.Fatalf("cleaned generated backend disk usage = %#v", usage)
	}
}

func TestGeneratedCredentialRotationProducesIssuanceAndInboxAtScheduledTime(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	ctx := context.Background()
	store := simulation.NewMemoryEventStore()
	stream := []events.Event{events.WorldCreated{RunID: "generated-rotation", Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}}
	stream = append(stream, s.world.Bootstrap...)
	stream = append(stream, events.WorldScheduleCreated{Schedule: s.world.Events, CreatedAt: s.world.StartsAt})
	if _, err := store.Append(ctx, "generated-rotation", 0, stream); err != nil {
		t.Fatal(err)
	}
	session, err := simulation.OpenRunSession(ctx, store, "generated-rotation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, simulation.AdvanceTime{CommandID: "reach-all-rotations", RequestedDuration: 6 * 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	state := session.State()
	rotationMessages := 0
	for _, message := range state.InboxMessages {
		if strings.Contains(message.Subject, "Новые credentials сервера") {
			rotationMessages++
		}
	}
	if rotationMessages != int(s.generator.Profile().Credentials.RotationEvents) || len(state.ServerCredentials) < rotationMessages {
		t.Fatalf("rotation messages=%d credentials=%d", rotationMessages, len(state.ServerCredentials))
	}
}

func rehydrateEvents(t *testing.T, stream []events.Event) simulation.State {
	t.Helper()
	records := make([]simulation.StoredEvent, len(stream))
	for index, event := range stream {
		records[index] = simulation.StoredEvent{Version: uint64(index + 1), Event: event}
	}
	state, err := simulation.Rehydrate(records)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func containsRequestFailure(items []events.Event, code model.RequestFailureCode) bool {
	for _, item := range items {
		if rejected, ok := item.(events.PageRequestRejected); ok && rejected.ErrorCode == code {
			return true
		}
	}
	return false
}

func containsSuccessfulRequest(items []events.Event) bool {
	for _, item := range items {
		if completed, ok := item.(events.PageRequestCompleted); ok && completed.StatusCode == 200 {
			return true
		}
	}
	return false
}

func TestGeneratedReadOnlyWorldCompletesAndReplays(t *testing.T) {
	s := newWorldGeneratorScenario(t)
	s.WhenWorldIsGenerated(42)
	ctx := context.Background()
	store := simulation.NewMemoryEventStore()
	stream := []events.Event{events.WorldCreated{RunID: "service-week", Seed: s.world.Key.Seed, StartedAt: s.world.StartsAt, EndsAt: s.world.EndsAt}}
	stream = append(stream, s.world.Bootstrap...)
	stream = append(stream, events.WorldScheduleCreated{Schedule: s.world.Events, CreatedAt: s.world.StartsAt})
	if _, err := store.Append(ctx, "service-week", 0, stream); err != nil {
		t.Fatal(err)
	}
	session, err := simulation.OpenRunSession(ctx, store, "service-week")
	if err != nil {
		t.Fatal(err)
	}
	products := session.State().Products
	if _, err := session.Execute(ctx, simulation.AdvanceTime{RequestedDuration: s.world.EndsAt.Sub(s.world.StartsAt)}); err != nil {
		t.Fatal(err)
	}
	replayed, err := simulation.Rehydrate(session.Records())
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != simulation.RunCompleted || !replayed.Clock.CurrentTime.Equal(s.world.EndsAt) || replayed.Costs.ServerCostMinor <= 0 {
		t.Fatalf("world did not complete: %#v", replayed.Clock)
	}
	if !reflect.DeepEqual(products, replayed.Products) {
		t.Fatal("catalog changed during the week")
	}
	if replayed.ScheduleCursor != len(s.world.Events) {
		t.Fatalf("unprocessed scheduled events: %d/%d", replayed.ScheduleCursor, len(s.world.Events))
	}
	metrics := session.Projection().Metrics(simulation.MetricsQuery{}).Current
	if metrics.VisitorRequestsTotal == 0 {
		t.Fatal("no read-only visitor requests were served")
	}
}

func TestDifferentSeedsCanSelectDifferentWeeksFromConfiguredYear(t *testing.T) {
	weeks := make(map[string]bool)
	for seed := int64(1); seed <= 24; seed++ {
		s := newWorldGeneratorScenario(t)
		s.WhenWorldIsGenerated(seed)
		year, week := s.world.StartsAt.ISOWeek()
		weeks[fmt.Sprintf("%d-%02d", year, week)] = true
	}
	if len(weeks) < 2 {
		t.Fatalf("selected weeks = %v", weeks)
	}
}

func TestRandomWeekSelectionCanIncludeConfiguredHolidays(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	profile, err := generator.LoadProfileFile(filepath.Join(filepath.Dir(file), "..", "config", "world-generation.v2.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	profile.Traffic.TargetScheduledEvents = 100
	profile.DDoS.AttackCount = generator.IntRange{Min: 3, Max: 3}
	profile.Infrastructure.BackendSurgeVisitors = 0
	profile.Infrastructure.DatabaseConnectionSurgeVisitors = 0
	profile.Limits.MaxVisitors = 1000
	profile.Limits.MaxScheduledEvents = 2000
	worldGenerator, err := generator.New(profile)
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation(profile.Clock.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	foundHolidayWeek := false
	for seed := int64(1); seed <= 256 && !foundHolidayWeek; seed++ {
		world, generationErr := worldGenerator.Generate(seed)
		if generationErr != nil {
			t.Fatal(generationErr)
		}
		for day := world.StartsAt.In(location); day.Before(world.EndsAt.In(location)); day = day.AddDate(0, 0, 1) {
			fixed := day.Month() == time.March && day.Day() == 8 || day.Month() == time.May && day.Day() == 1 ||
				day.Month() == time.September && day.Day() == 1
			blackFriday := day.Month() == time.November && day.Weekday() == time.Friday && day.Day() >= 23 && day.Day() <= 29
			if fixed || blackFriday {
				foundHolidayWeek = true
				break
			}
		}
	}
	if !foundHolidayWeek {
		t.Fatal("no seed selected a week containing a configured holiday")
	}
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
