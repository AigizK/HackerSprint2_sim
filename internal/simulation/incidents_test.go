package simulation

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func incidentState(t *testing.T, attack events.Event, servers int) State {
	t.Helper()
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	stream := []events.Event{events.WorldCreated{RunID: "incident-run", Seed: 1, StartedAt: at, EndsAt: at.Add(time.Hour)},
		events.PageConfigured{Page: model.PageProductList, LoadUnits: 60, HoldDuration: time.Minute, BaseLatency: 10 * time.Millisecond, ConfiguredAt: at},
		events.PageConfigured{Page: model.PagePurchase, LoadUnits: 10, HoldDuration: time.Minute, BaseLatency: 10 * time.Millisecond, ConfiguredAt: at},
		events.InfrastructureConfigured{ServerProvisioningDuration: time.Minute, ConfiguredAt: at}}
	for index := 1; index <= servers; index++ {
		id := model.ServerID(fmt.Sprintf("server-%d", index))
		op := model.OperationID(fmt.Sprintf("initial-%d", index))
		stream = append(stream, events.ServerProvisioningStarted{OperationID: op, ServerID: id, CapacityUnits: 100, StartedAt: at, ReadyAt: at}, events.ServerActivated{OperationID: op, ServerID: id, ActivatedAt: at})
	}
	if attack != nil {
		stream = append(stream, attack)
	}
	records := make([]StoredEvent, len(stream))
	for i, event := range stream {
		records[i] = StoredEvent{Version: uint64(i + 1), Event: event}
	}
	state, err := Rehydrate(records)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestScaleDDoSConsumesCapacityAndAdditionalServerAbsorbsIt(t *testing.T) {
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	attack := events.TrafficAttackStarted{AttackID: "ddos", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
		RequestsPerMinute: 60, LoadUnitsPerRequest: 1, Resolution: model.AttackScaleOrExpiry, StartedAt: at, ExpectedEndAt: at.Add(time.Hour)}
	one := incidentState(t, attack, 1)
	decided, err := Decide("incident-run", one, OpenPage{RequestID: "one", VisitorID: "v1", Page: model.PageProductList})
	if err != nil {
		t.Fatal(err)
	}
	if rejected, ok := decided[len(decided)-1].(events.PageRequestRejected); !ok || rejected.ErrorCode != model.FailureServerCapacityExceeded {
		t.Fatalf("one server = %#v", decided)
	}
	two := incidentState(t, attack, 2)
	decided, err = Decide("incident-run", two, OpenPage{RequestID: "two", VisitorID: "v2", Page: model.PageProductList})
	if err != nil {
		t.Fatal(err)
	}
	if completed, ok := decided[len(decided)-1].(events.PageRequestCompleted); !ok || completed.StatusCode != 200 {
		t.Fatalf("two servers = %#v", decided)
	}
}

func TestFixableDDoSStopsBlockingAfterCorrectFix(t *testing.T) {
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	message := "FIX-DDOS"
	digest := sha256.Sum256([]byte(message))
	attack := events.TrafficAttackStarted{AttackID: "ddos", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
		RequestsPerMinute: 60, LoadUnitsPerRequest: 1, Resolution: model.AttackFixOrExpiry, FixMessage: message,
		FixMessageHash: fmt.Sprintf("%x", digest), StartedAt: at, ExpectedEndAt: at.Add(time.Hour)}
	state := incidentState(t, attack, 1)
	fixEvents, err := Decide("incident-run", state, ApplyFix{CommandID: "fix", Message: message})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range fixEvents {
		if err := state.Apply(event); err != nil {
			t.Fatal(err)
		}
		state.Version++
	}
	decided, err := Decide("incident-run", state, OpenPage{RequestID: "after", VisitorID: "v", Page: model.PageProductList})
	if err != nil {
		t.Fatal(err)
	}
	if completed, ok := decided[len(decided)-1].(events.PageRequestCompleted); !ok || completed.StatusCode != 200 {
		t.Fatalf("after fix = %#v", decided)
	}
}
