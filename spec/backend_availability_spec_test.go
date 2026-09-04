package spec_test

import (
	"math"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestBackendOutageIsIntegratedIntoTimeBasedUptime(t *testing.T) {
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	startedAt := h.state().Clock.CurrentTime.Add(time.Minute)
	endedAt := startedAt.Add(5 * time.Minute)
	h.append(events.WorldScheduleCreated{CreatedAt: h.state().Clock.CurrentTime, Schedule: events.EventSchedule{
		{Sequence: 1, OccursAt: startedAt, Event: events.TrafficAttackStarted{
			AttackID: "uptime-ddos", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
			RequestsPerMinute: 60_000, LoadUnitsPerRequest: 2, SourceCIDR: "203.0.113.0/24",
			UserAgent: "LoadBot/1.0", RegionCode: "ZZ", Resolution: model.AttackScaleOrExpiry,
			ExpectedEndAt: endedAt, StartedAt: startedAt,
		}},
		{Sequence: 2, OccursAt: endedAt, Event: events.TrafficAttackEnded{AttackID: "uptime-ddos", EndedAt: endedAt}},
	}})

	started := h.execute(simulation.AdvanceTime{RealElapsed: time.Minute})
	assertBackendAvailabilityEvent(t, started, false, startedAt, model.PageProductList)
	recovered := h.execute(simulation.AdvanceTime{RealElapsed: 9 * time.Minute})
	assertBackendAvailabilityEvent(t, recovered, true, endedAt)

	overview := h.projection().Overview()
	if overview.Uptime != 0.5 {
		t.Fatalf("uptime = %f, want 0.5 after five unavailable minutes out of ten", overview.Uptime)
	}
}

func TestSecondBackendRecoversAvailabilityDuringDDoS(t *testing.T) {
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	startedAt := h.state().Clock.CurrentTime.Add(time.Minute)
	endedAt := startedAt.Add(20 * time.Minute)
	h.append(events.WorldScheduleCreated{CreatedAt: h.state().Clock.CurrentTime, Schedule: events.EventSchedule{
		{Sequence: 1, OccursAt: startedAt, Event: events.TrafficAttackStarted{
			AttackID: "scale-ddos", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
			RequestsPerMinute: 45_000, LoadUnitsPerRequest: 2, SourceCIDR: "203.0.113.0/24",
			UserAgent: "LoadBot/1.0", RegionCode: "ZZ", Resolution: model.AttackScaleOrExpiry,
			ExpectedEndAt: endedAt, StartedAt: startedAt,
		}},
		{Sequence: 2, OccursAt: endedAt, Event: events.TrafficAttackEnded{AttackID: "scale-ddos", EndedAt: endedAt}},
	}})
	h.execute(simulation.AdvanceTime{RealElapsed: time.Minute})
	h.execute(simulation.AddTypedServer{CommandID: h.id("scale"), OperationID: h.operation("scale-op"), ServerID: "backend-2", InstanceType: model.InstanceBackendStandard})

	recovered := h.execute(simulation.AdvanceTime{RealElapsed: 5 * time.Minute})
	assertBackendAvailabilityEvent(t, recovered, true, h.state().Clock.CurrentTime)
}

func TestFirewallBlockingDDoSRecoversBackendAvailability(t *testing.T) {
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	startedAt := h.state().Clock.CurrentTime.Add(time.Minute)
	endedAt := startedAt.Add(20 * time.Minute)
	h.append(events.WorldScheduleCreated{CreatedAt: h.state().Clock.CurrentTime, Schedule: events.EventSchedule{
		{Sequence: 1, OccursAt: startedAt, Event: events.TrafficAttackStarted{
			AttackID: "firewall-ddos", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
			RequestsPerMinute: 60_000, LoadUnitsPerRequest: 2, SourceCIDR: "203.0.113.0/24",
			UserAgent: "LoadBot/1.0", RegionCode: "ZZ", Resolution: model.AttackScaleOrExpiry,
			ExpectedEndAt: endedAt, StartedAt: startedAt,
		}},
		{Sequence: 2, OccursAt: endedAt, Event: events.TrafficAttackEnded{AttackID: "firewall-ddos", EndedAt: endedAt}},
	}})
	h.execute(simulation.AdvanceTime{RealElapsed: time.Minute})

	items := h.execute(simulation.UpsertFirewallRule{CommandID: h.id("block-ddos"), Rule: model.FirewallRule{
		ID: "block-ddos", Priority: 1, Action: model.FirewallDeny, Enabled: true,
		Match: model.FirewallMatch{SourceCIDR: "203.0.113.0/24"},
	}})
	assertBackendAvailabilityEvent(t, items, true, h.state().Clock.CurrentTime)
}

func TestRequestCapacityReleaseClosesBackendOutageAtExactBoundary(t *testing.T) {
	s := newCapacityScenario(t, "backend-capacity-boundary", capacityServerUnits)
	s.When(s.User.OpensPage("uses-all-backend", "visitor-1", model.PageProductList, ""))
	s.Then(s.Events.Contains(events.BackendAvailabilityChanged{Available: false,
		UnavailablePages: []model.PageType{model.PageProductList}, ChangedAt: worldStartsAt}))

	s.When(s.Time.Advance(capacityHoldDuration, 0))
	s.Then(s.Events.Contains(events.BackendAvailabilityChanged{Available: true,
		UnavailablePages: []model.PageType{}, ChangedAt: worldStartsAt.Add(capacityHoldDuration)}))

	s.When(s.Time.Advance(capacityHoldDuration, 0))
	s.Then(s.Future.Overview(spec.OverviewView{
		SimulationTime: worldStartsAt.Add(2 * capacityHoldDuration), SimulationEndsAt: worldStartsAt.Add(24 * time.Hour),
		Remaining: 24*time.Hour - 2*capacityHoldDuration, RunStatus: "running", SiteStatus: "healthy",
		ServerCount: 1, VisitorRequestsTotal: 1, Uptime: 0.5,
	}))
}

func TestFirewallExpiryAndAttackEndKeepBackendUptimeBoundariesChronological(t *testing.T) {
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	startedAt := h.state().Clock.CurrentTime.Add(time.Minute)
	endedAt := startedAt.Add(9 * time.Minute)
	h.append(events.WorldScheduleCreated{CreatedAt: h.state().Clock.CurrentTime, Schedule: events.EventSchedule{
		{Sequence: 1, OccursAt: startedAt, Event: events.TrafficAttackStarted{
			AttackID: "expiring-rule-ddos", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
			RequestsPerMinute: 60_000, LoadUnitsPerRequest: 2, SourceCIDR: "203.0.113.0/24",
			UserAgent: "LoadBot/1.0", RegionCode: "ZZ", Resolution: model.AttackScaleOrExpiry,
			ExpectedEndAt: endedAt, StartedAt: startedAt,
		}},
		{Sequence: 2, OccursAt: endedAt, Event: events.TrafficAttackEnded{AttackID: "expiring-rule-ddos", EndedAt: endedAt}},
	}})
	h.execute(simulation.AdvanceTime{RealElapsed: time.Minute})
	expiresAt := startedAt.Add(4 * time.Minute)
	h.execute(simulation.UpsertFirewallRule{CommandID: h.id("temporary-block"), Rule: model.FirewallRule{
		ID: "temporary-block", Priority: 1, Action: model.FirewallDeny, Enabled: true, ExpiresAt: expiresAt,
		Match: model.FirewallMatch{SourceCIDR: "203.0.113.0/24"},
	}})

	items := h.execute(simulation.AdvanceTime{RealElapsed: 11 * time.Minute})
	changes := backendAvailabilityEvents(items)
	if len(changes) != 2 || changes[0].Available || !changes[0].ChangedAt.Equal(expiresAt) ||
		!changes[1].Available || !changes[1].ChangedAt.Equal(endedAt) {
		t.Fatalf("backend transitions = %#v", changes)
	}
	wantUptime := 1 - float64(endedAt.Sub(expiresAt))/float64(12*time.Minute)
	if got := h.projection().Overview().Uptime; math.Abs(got-wantUptime) > 1e-12 {
		t.Fatalf("uptime = %f, want %f", got, wantUptime)
	}
}

func TestFullBackendLogDiskRejectsRequestsUntilCleanup(t *testing.T) {
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	filledAt := h.state().Clock.CurrentTime.Add(time.Minute)
	h.append(events.WorldScheduleCreated{CreatedAt: h.state().Clock.CurrentTime, Schedule: events.EventSchedule{{
		Sequence: 1, OccursAt: filledAt, Event: events.DiskLogsGrowthRequested{
			GrowthID: "backend-logs-fill", ServerID: "backend-1", DeltaBytes: 10 * gib, RequestedAt: filledAt,
		},
	}}})

	filled := h.execute(simulation.AdvanceTime{RealElapsed: time.Minute})
	requireEventTypes(t, filled, "DiskLogsIncreased", "BackendAvailabilityChanged")
	assertBackendAvailabilityEvent(t, filled, false, filledAt, model.PageProductList)
	if rejected := lastPageRejection(h.open()); rejected.ErrorCode != model.FailureDiskFull {
		t.Fatalf("full backend rejection = %#v, want DISK_FULL", rejected)
	}

	cleaned := h.execute(simulation.CleanupDatabaseLogs{CommandID: h.id("clean-backend"), ServerID: "backend-1"})
	requireEventTypes(t, cleaned, "DiskLogsCleaned", "BackendAvailabilityChanged")
	assertBackendAvailabilityEvent(t, cleaned, true, h.state().Clock.CurrentTime)
	if !containsSuccessfulRequest(h.open()) {
		t.Fatal("backend request did not recover after cleaning server logs")
	}
}

func TestFullBackendLogDiskIsSkippedWhenAnotherBackendIsAvailable(t *testing.T) {
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	h.activeServer("backend-2", model.InstanceBackendStandard)
	filledAt := h.state().Clock.CurrentTime.Add(time.Minute)
	h.append(events.WorldScheduleCreated{CreatedAt: h.state().Clock.CurrentTime, Schedule: events.EventSchedule{{
		Sequence: 1, OccursAt: filledAt, Event: events.DiskLogsGrowthRequested{
			GrowthID: "backend-1-logs-fill", ServerID: "backend-1", DeltaBytes: 10 * gib, RequestedAt: filledAt,
		},
	}}})

	items := h.execute(simulation.AdvanceTime{RealElapsed: time.Minute})
	if changes := backendAvailabilityEvents(items); len(changes) != 0 {
		t.Fatalf("second backend should keep service available, transitions = %#v", changes)
	}
	request := h.open()
	for _, item := range request {
		if accepted, ok := item.(events.PageRequestAccepted); ok {
			if accepted.ServerID != "backend-2" {
				t.Fatalf("request routed to full backend %q", accepted.ServerID)
			}
			return
		}
	}
	t.Fatalf("request was not accepted: %v", eventTypes(request))
}

func lastPageRejection(items []events.Event) events.PageRequestRejected {
	for index := len(items) - 1; index >= 0; index-- {
		if rejected, ok := items[index].(events.PageRequestRejected); ok {
			return rejected
		}
	}
	return events.PageRequestRejected{}
}

func assertBackendAvailabilityEvent(t *testing.T, items []events.Event, available bool, at time.Time, unavailablePages ...model.PageType) {
	t.Helper()
	for _, item := range items {
		if changed, ok := item.(events.BackendAvailabilityChanged); ok {
			if changed.Available != available || !changed.ChangedAt.Equal(at) || !equalPages(changed.UnavailablePages, unavailablePages) {
				t.Fatalf("backend availability = %#v, want available=%t at=%s pages=%v", changed, available, at, unavailablePages)
			}
			return
		}
	}
	t.Fatalf("events %v do not contain BackendAvailabilityChanged", eventTypes(items))
}

func equalPages(left, right []model.PageType) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func backendAvailabilityEvents(items []events.Event) []events.BackendAvailabilityChanged {
	result := make([]events.BackendAvailabilityChanged, 0)
	for _, item := range items {
		if changed, ok := item.(events.BackendAvailabilityChanged); ok {
			result = append(result, changed)
		}
	}
	return result
}
