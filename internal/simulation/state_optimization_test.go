package simulation

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestPruneDerivedStateKeepsIDsAndDropsCompletedDetails(t *testing.T) {
	now := time.Date(2030, 3, 1, 12, 0, 0, 0, time.UTC)
	state := NewState()
	state.Requests["request-1"] = PageRequestState{ID: "request-1", Status: model.PageRequestSucceeded, CompletedAt: now}
	state.SeenRequests["request-1"] = struct{}{}
	state.Visitors["visitor-1"] = VisitorState{ID: "visitor-1", Outcome: model.VisitorPurchased, CompletedAt: now}
	state.SeenVisitors["visitor-1"] = struct{}{}
	state.Capacity["request-1"] = CapacityAllocationState{RequestID: "request-1", ServerID: "server-1", LoadUnits: 60, ReleasesAt: now}
	state.UsedCapacityByServer["server-1"] = 60

	state.pruneDerivedState(now)

	if len(state.Requests) != 0 || len(state.Visitors) != 0 || len(state.Capacity) != 0 {
		t.Fatalf("completed details were not pruned: requests=%d visitors=%d capacity=%d", len(state.Requests), len(state.Visitors), len(state.Capacity))
	}
	if _, exists := state.SeenRequests["request-1"]; !exists {
		t.Fatal("request id must remain in the seen set")
	}
	if _, exists := state.SeenVisitors["visitor-1"]; !exists {
		t.Fatal("visitor id must remain in the seen set")
	}
	if state.UsedCapacityByServer["server-1"] != 0 {
		t.Fatalf("used capacity = %d", state.UsedCapacityByServer["server-1"])
	}
}

func TestCapacityIndexAndScheduleCursorAvoidHistoricalScans(t *testing.T) {
	now := time.Date(2030, 3, 1, 12, 0, 0, 0, time.UTC)
	state := NewState()
	state.Servers["server-1"] = ServerState{ID: "server-1", Status: model.ServerActive, CapacityUnits: 100}
	state.CapacityIndexedAt = now
	state.UsedCapacityByServer["server-1"] = 60
	if available := serverAvailableCapacity(state, "server-1", now); available != 40 {
		t.Fatalf("available capacity = %d", available)
	}

	state.Clock.CurrentTime = now
	state.Schedule = events.EventSchedule{
		{Sequence: 1, OccursAt: now, Event: events.VisitorArrived{VisitorID: "old", ArrivedAt: now}},
		{Sequence: 2, OccursAt: now.Add(time.Hour), Event: events.VisitorArrived{VisitorID: "next", ArrivedAt: now.Add(time.Hour)}},
		{Sequence: 3, OccursAt: now.Add(2 * time.Hour), Event: events.VisitorArrived{VisitorID: "later", ArrivedAt: now.Add(2 * time.Hour)}},
	}
	state.ScheduleCursor = 1
	pending := pendingScheduledEvents(state, now.Add(time.Hour))
	if len(pending) != 1 || pending[0].Sequence != 2 {
		t.Fatalf("pending schedule = %#v", pending)
	}

	clone := cloneState(state)
	if &clone.Schedule[0] != &state.Schedule[0] {
		t.Fatal("immutable schedule backing array was copied")
	}
}
