package simulation

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestDecideContextHonorsCanceledRequest(t *testing.T) {
	state := largeHistoryAdvanceState(10, 10, 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := decideContext(ctx, state.RunID, state, AdvanceTime{RequestedDuration: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("advance error = %v, want context.Canceled", err)
	}
}

func BenchmarkAdvanceTimeLargeHistory(b *testing.B) {
	state := largeHistoryAdvanceState(100_000, 50_000, 600)
	command := AdvanceTime{
		CommandID:         "large-history-hour",
		RequestedDuration: time.Hour,
		StopOnLogError:    true,
		LogErrorCodes:     []model.RequestFailureCode{model.FailureDiskFull},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := Decide(state.RunID, state, command); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRealtimeAdvanceLargeHistoryWithoutVisitors(b *testing.B) {
	state := largeHistoryAdvanceState(100_000, 50_000, 0)
	command := AdvanceTime{CommandID: "realtime-read", RealElapsed: time.Second}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := Decide(state.RunID, state, command); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAdvanceDoesNotMutateSeenSets(t *testing.T) {
	for _, arrivals := range []int{0, 1} {
		state := largeHistoryAdvanceState(3, 2, arrivals)
		beforeRequests, beforeVisitors := len(state.SeenRequests), len(state.SeenVisitors)
		if _, err := Decide(state.RunID, state, AdvanceTime{RequestedDuration: time.Hour}); err != nil {
			t.Fatalf("arrivals=%d: %v", arrivals, err)
		}
		if len(state.SeenRequests) != beforeRequests || len(state.SeenVisitors) != beforeVisitors {
			t.Fatalf("arrivals=%d mutated input seen sets: requests=%d visitors=%d", arrivals, len(state.SeenRequests), len(state.SeenVisitors))
		}
	}
}

func largeHistoryAdvanceState(seenRequests, seenVisitors, arrivals int) State {
	startsAt := time.Date(2030, 1, 14, 0, 0, 0, 0, time.UTC)
	state := NewState()
	state.RunID = "large-history"
	state.Seed = 42
	state.Status = RunRunning
	state.Clock = ClockState{StartedAt: startsAt, CurrentTime: startsAt, EndsAt: startsAt.Add(7 * 24 * time.Hour)}
	state.Site = SiteState{Status: model.SiteRunning}
	state.Pages[model.PageProductList] = PageConfigState{Page: model.PageProductList, LoadUnits: 1, HoldDuration: time.Second, BaseLatency: time.Millisecond}
	state.Pages[model.PageProduct] = PageConfigState{Page: model.PageProduct, LoadUnits: 1, HoldDuration: time.Second, BaseLatency: time.Millisecond}
	state.Products["product"] = ProductState{ID: "product", Name: "Product", PriceMinor: 100, ViewProbabilityPPM: ProbabilityScale}
	state.Servers["backend"] = ServerState{ID: "backend", Role: model.ServerRoleBackend, Status: model.ServerActive, CapacityUnits: 1_000_000, ActivatedAt: startsAt}
	state.CapacityIndexedAt = startsAt
	for index := 0; index < seenRequests; index++ {
		state.SeenRequests[model.RequestID(fmt.Sprintf("seen-request-%06d", index))] = struct{}{}
	}
	for index := 0; index < seenVisitors; index++ {
		state.SeenVisitors[model.VisitorID(fmt.Sprintf("seen-visitor-%06d", index))] = struct{}{}
	}
	state.Schedule = make(events.EventSchedule, 0, arrivals)
	for index := 0; index < arrivals; index++ {
		at := startsAt.Add(time.Duration(index+1) * time.Hour / time.Duration(arrivals+1))
		visitorID := model.VisitorID(fmt.Sprintf("new-visitor-%06d", index))
		profile := VisitorClientProfile(state.Seed, visitorID)
		state.Schedule = append(state.Schedule, events.ScheduledWorldEvent{
			Sequence: uint64(index + 1), OccursAt: at,
			Event: events.VisitorArrived{VisitorID: visitorID, SourceIP: profile.SourceIP, UserAgent: profile.UserAgent, RegionCode: profile.RegionCode, ArrivedAt: at},
		})
	}
	return state
}
