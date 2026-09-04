package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestVisitorJourneyIsDeterministicForSeed(t *testing.T) {
	first := spec.New(t, "run-visitor-deterministic-1")
	second := spec.New(t, "run-visitor-deterministic-2")
	for _, scenario := range []*spec.Scenario{first, second} {
		scenario.Given(
			scenario.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
			scenario.Product.Added("product-1", "Coffee machine", 12_990, 750_000),
		)
		scenario.When(scenario.Visitor.Arrives("same-visitor"))
	}
	first.Then(first.Events.EqualTo(second))
}

func TestVisitorLeavesAfterProductListWhenNoProductIsSelected(t *testing.T) {
	s := spec.New(t, "run-visitor-abandons-list")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 0),
	)

	s.When(s.Visitor.Arrives("visitor-1"))
	s.Then(
		s.Events.Contains(events.VisitorJourneyCompleted{
			VisitorID: "visitor-1", Outcome: model.VisitorLeftAfterProductList, CompletedAt: worldStartsAt,
		}),
		s.Events.HasNoType("ProductSelected"),
		s.Events.HasNoType("ProductPurchased"),
	)
}

func TestVisitorLeavesAfterReadOnlyProductPage(t *testing.T) {
	s := spec.New(t, "run-visitor-product-page")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 1_000_000),
	)

	s.When(s.Visitor.Arrives("visitor-1"))
	s.Then(
		s.Events.Contains(
			events.VisitorJourneyCompleted{
				VisitorID: "visitor-1", Outcome: model.VisitorLeftAfterProductPage, CompletedAt: worldStartsAt,
			},
		),
		s.Events.HasNoType("ProductPurchased"),
	)
}

func TestLargeAdvanceProcessesEveryScheduledVisitorJourney(t *testing.T) {
	s := spec.New(t, "run-many-scheduled-visitors")
	firstAt := worldStartsAt.Add(5 * time.Minute)
	secondAt := worldStartsAt.Add(10 * time.Minute)
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 1_000_000),
		s.World.Schedule(events.EventSchedule{
			{Sequence: 1, OccursAt: firstAt, Event: events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: firstAt}},
			{Sequence: 2, OccursAt: secondAt, Event: events.VisitorArrived{VisitorID: "visitor-2", ArrivedAt: secondAt}},
		}),
	)

	s.When(s.Time.Advance(0, time.Hour))
	s.Then(s.Events.Contains(
		events.VisitorJourneyCompleted{VisitorID: "visitor-1", Outcome: model.VisitorLeftAfterProductPage, CompletedAt: firstAt},
		events.VisitorJourneyCompleted{VisitorID: "visitor-2", Outcome: model.VisitorLeftAfterProductPage, CompletedAt: secondAt},
	))
}
