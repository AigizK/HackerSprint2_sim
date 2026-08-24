package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestGeneratedWorldIsDeterministicForSeed(t *testing.T) {
	s := spec.New(t, "run-world-determinism")
	s.Then(s.Future.GeneratedWorldsEqual(42))
}

func TestDifferentSeedsGenerateDifferentWorlds(t *testing.T) {
	s := spec.New(t, "run-world-different-seeds")
	s.Then(s.Future.GeneratedWorldsDiffer(42, 43))
}

func TestRunsOfSameWorldHaveIndependentEventStreams(t *testing.T) {
	first := spec.New(t, "run-independent-1")
	second := spec.New(t, "run-independent-2")
	first.Given(first.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))
	second.Given(second.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))

	first.When(first.Product.Add("product-only-in-first", "First product", 1_000, 100_000, 100_000))
	second.Then(second.State.HasNoProduct("product-only-in-first"))
}

func TestManualWorldMustExistForNegativeSeed(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		s := spec.New(t, "run-manual-world-existing")
		s.Then(s.Future.ManualWorldAvailable(-1))
	})
	t.Run("missing", func(t *testing.T) {
		s := spec.New(t, "run-manual-world-missing")
		s.Then(s.Future.ManualWorldMissing(-999))
	})
}

func TestAdvanceAppliesScheduledEventsChronologicallyAndOnce(t *testing.T) {
	s := spec.New(t, "run-world-schedule")
	atFive := worldStartsAt.Add(5 * time.Minute)
	atTen := worldStartsAt.Add(10 * time.Minute)
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.World.Schedule(events.EventSchedule{
			{Sequence: 2, OccursAt: atTen, Event: events.VisitorArrived{VisitorID: "visitor-2", ArrivedAt: atTen}},
			{Sequence: 1, OccursAt: atFive, Event: events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: atFive}},
		}),
	)

	s.When(s.Time.Advance(0, 10*time.Minute))
	s.Then(s.Events.Exactly(
		events.TimeAdvanced{
			From: worldStartsAt, To: atTen,
			RequestedDuration: 10 * time.Minute, AppliedDuration: 10 * time.Minute,
		},
		events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: atFive},
		events.VisitorArrived{VisitorID: "visitor-2", ArrivedAt: atTen},
	))

	s.When(s.Time.Advance(0, 5*time.Minute))
	s.Then(s.Events.Exactly(events.TimeAdvanced{
		From: atTen, To: atTen.Add(5 * time.Minute),
		RequestedDuration: 5 * time.Minute, AppliedDuration: 5 * time.Minute,
	}))
}

func TestScheduledEventsAtSameTimeUseStableSequence(t *testing.T) {
	s := spec.New(t, "run-world-same-time-sequence")
	at := worldStartsAt.Add(5 * time.Minute)
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.World.Schedule(events.EventSchedule{
			{Sequence: 2, OccursAt: at, Event: events.VisitorArrived{VisitorID: model.VisitorID("visitor-2"), ArrivedAt: at}},
			{Sequence: 1, OccursAt: at, Event: events.VisitorArrived{VisitorID: model.VisitorID("visitor-1"), ArrivedAt: at}},
		}),
	)

	s.When(s.Time.Advance(0, 5*time.Minute))
	s.Then(s.Events.Exactly(
		events.TimeAdvanced{From: worldStartsAt, To: at, RequestedDuration: 5 * time.Minute, AppliedDuration: 5 * time.Minute},
		events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: at},
		events.VisitorArrived{VisitorID: "visitor-2", ArrivedAt: at},
	))
}

func TestEveryApplicationRequestCanSynchronizeRealElapsedTime(t *testing.T) {
	s := spec.New(t, "run-real-time-synchronization")
	s.Given(s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))

	s.When(s.Time.SynchronizeRequest("agent-request-1", 7*time.Minute))
	s.Then(
		s.Events.Exactly(events.TimeAdvanced{
			CommandID: "agent-request-1",
			From:      worldStartsAt, To: worldStartsAt.Add(7 * time.Minute),
			RealElapsed: 7 * time.Minute, AppliedDuration: 7 * time.Minute,
		}),
		s.State.CurrentTime(worldStartsAt.Add(7*time.Minute)),
	)
}
