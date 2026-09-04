package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/spec"
)

var worldStartsAt = time.Date(2030, time.January, 1, 10, 0, 0, 0, time.UTC)

func TestWorldCreation(t *testing.T) {
	s := spec.New(t, "run-create-world")
	endsAt := worldStartsAt.Add(24 * time.Hour)

	s.Given()

	s.When(
		s.World.Create(42, worldStartsAt, endsAt),
	)

	s.Then(
		s.Events.Exactly(events.WorldCreated{
			RunID:     "run-create-world",
			Seed:      42,
			StartedAt: worldStartsAt,
			EndsAt:    endsAt,
		}),
		s.State.IsRunning(),
		s.State.CurrentTime(worldStartsAt),
		s.State.Version(1),
	)
}

func TestProductAddition(t *testing.T) {
	s := spec.New(t, "run-add-product")
	endsAt := worldStartsAt.Add(24 * time.Hour)

	s.Given(
		s.World.Created(42, worldStartsAt, endsAt),
	)

	s.When(
		s.Product.Add("coffee-1", "Coffee machine", 12_990, 850_000),
	)

	s.Then(
		s.Events.Exactly(events.ProductAdded{
			ProductID:          "coffee-1",
			Name:               "Coffee machine",
			PriceMinor:         12_990,
			ViewProbabilityPPM: 850_000,
			AddedAt:            worldStartsAt,
		}),
		s.State.HasProduct(simulation.ProductState{
			ID:                 "coffee-1",
			Name:               "Coffee machine",
			Manufacturer:       "Unknown",
			PriceMinor:         12_990,
			Version:            1,
			ViewProbabilityPPM: 850_000,
			AddedAt:            worldStartsAt,
			UpdatedAt:          worldStartsAt,
		}),
		s.State.Version(2),
	)
}

func TestTimeAdvanceUsesMaximumOfRealAndRequestedTime(t *testing.T) {
	s := spec.New(t, "run-advance-time")
	endsAt := worldStartsAt.Add(24 * time.Hour)

	s.Given(
		s.World.Created(42, worldStartsAt, endsAt),
	)

	s.When(
		s.Time.Advance(7*time.Minute, 5*time.Minute),
	)

	s.Then(
		s.Events.Exactly(events.TimeAdvanced{
			From:              worldStartsAt,
			To:                worldStartsAt.Add(7 * time.Minute),
			RealElapsed:       7 * time.Minute,
			RequestedDuration: 5 * time.Minute,
			AppliedDuration:   7 * time.Minute,
		}),
		s.State.CurrentTime(worldStartsAt.Add(7*time.Minute)),
		s.State.IsRunning(),
		s.State.Version(2),
	)
}

func TestTimeAdvanceStopsAtEndOfWorld(t *testing.T) {
	s := spec.New(t, "run-complete-world")
	endsAt := worldStartsAt.Add(30 * time.Minute)

	s.Given(
		s.World.Created(42, worldStartsAt, endsAt),
	)

	s.When(
		s.Time.Advance(5*time.Minute, 24*time.Hour),
	)

	s.Then(
		s.Events.Exactly(
			events.TimeAdvanced{
				From:              worldStartsAt,
				To:                endsAt,
				RealElapsed:       5 * time.Minute,
				RequestedDuration: 24 * time.Hour,
				AppliedDuration:   30 * time.Minute,
			},
			events.RunEnded{CompletedAt: endsAt, Reason: "world_completed"},
		),
		s.State.CurrentTime(endsAt),
		s.State.IsCompleted(),
	)
}

func TestAutomaticTimeAdvanceMayBeLessThanFiveMinutes(t *testing.T) {
	s := spec.New(t, "run-automatic-time")

	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
	)

	s.When(
		s.Time.Advance(2*time.Minute, 0),
	)

	s.Then(
		s.Events.Exactly(events.TimeAdvanced{
			From:              worldStartsAt,
			To:                worldStartsAt.Add(2 * time.Minute),
			RealElapsed:       2 * time.Minute,
			RequestedDuration: 0,
			AppliedDuration:   2 * time.Minute,
		}),
		s.State.CurrentTime(worldStartsAt.Add(2*time.Minute)),
	)
}
