package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestVisitorJourneyOpensPagesInOrderAndPurchases(t *testing.T) {
	s := spec.New(t, "run-visitor-purchase")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 1_000_000, 1_000_000),
	)

	s.When(s.Visitor.Arrives("visitor-1"))

	s.Then(s.Events.Contains(
		events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: worldStartsAt},
		events.PageRequestStarted{
			RequestID: "visitor-1:product_list", Source: model.RequestSourceVisitor,
			VisitorID: "visitor-1", Page: model.PageProductList, StartedAt: worldStartsAt,
		},
		events.ProductSelected{VisitorID: "visitor-1", ProductID: "product-1", SelectedAt: worldStartsAt},
		events.PageRequestStarted{
			RequestID: "visitor-1:product_page", Source: model.RequestSourceVisitor,
			VisitorID: "visitor-1", Page: model.PageProduct, ProductID: "product-1", StartedAt: worldStartsAt,
		},
		events.PurchaseIntentCreated{
			PurchaseID: "visitor-1:purchase", VisitorID: "visitor-1", ProductID: "product-1", CreatedAt: worldStartsAt,
		},
		events.PageRequestStarted{
			RequestID: "visitor-1:purchase", Source: model.RequestSourceVisitor,
			VisitorID: "visitor-1", Page: model.PagePurchase, ProductID: "product-1", StartedAt: worldStartsAt,
		},
		events.ProductPurchased{
			PurchaseID: "visitor-1:purchase", ProductID: "product-1", PriceMinor: 12_990, PurchasedAt: worldStartsAt,
		},
		events.VisitorJourneyCompleted{
			VisitorID: "visitor-1", Outcome: model.VisitorPurchased, CompletedAt: worldStartsAt,
		},
	))
}

func TestVisitorJourneyIsDeterministicForSeed(t *testing.T) {
	first := spec.New(t, "run-visitor-deterministic-1")
	second := spec.New(t, "run-visitor-deterministic-2")
	for _, scenario := range []*spec.Scenario{first, second} {
		scenario.Given(
			scenario.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
			scenario.Product.Added("product-1", "Coffee machine", 12_990, 750_000, 400_000),
		)
		scenario.When(scenario.Visitor.Arrives("same-visitor"))
	}
	first.Then(first.Events.EqualTo(second))
}

func TestVisitorLeavesAfterProductListWhenNoProductIsSelected(t *testing.T) {
	s := spec.New(t, "run-visitor-abandons-list")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 0, 1_000_000),
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

func TestVisitorLeavesAfterProductPageWhenPurchaseProbabilityMisses(t *testing.T) {
	s := spec.New(t, "run-visitor-no-purchase")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 1_000_000, 0),
	)

	s.When(s.Visitor.Arrives("visitor-1"))
	s.Then(
		s.Events.Contains(
			events.RevenueLost{
				VisitorID: "visitor-1", ProductID: "product-1", AmountMinor: 12_990,
				Reason: model.RevenueLostToAbandonment, LostAt: worldStartsAt,
			},
			events.VisitorJourneyCompleted{
				VisitorID: "visitor-1", Outcome: model.VisitorLeftAfterProductPage, CompletedAt: worldStartsAt,
			},
		),
		s.Events.HasNoType("ProductPurchased"),
	)
}

func TestPageErrorStopsVisitorJourneyAndRecordsLostRevenue(t *testing.T) {
	s := spec.New(t, "run-visitor-lost-revenue")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 1_000_000, 1_000_000),
		s.Bug.Activated(pageBugID, model.PageProduct, "product-1", 1_000_000, correctFixText),
	)

	s.When(s.Visitor.Arrives("visitor-1"))
	s.Then(
		s.Events.Contains(
			events.RevenueLost{
				VisitorID: "visitor-1", ProductID: "product-1", RequestID: "visitor-1:product_page",
				AmountMinor: 12_990, Reason: model.RevenueLostToPageBug, LostAt: worldStartsAt,
			},
			events.VisitorJourneyCompleted{
				VisitorID: "visitor-1", Outcome: model.VisitorLeftAfterPageError, CompletedAt: worldStartsAt,
			},
		),
		s.Future.Economy(spec.EconomyView{LostPurchases: 1, LostRevenueMinor: 12_990}),
	)
}

func TestLargeAdvanceProcessesEveryScheduledVisitorJourney(t *testing.T) {
	s := spec.New(t, "run-many-scheduled-visitors")
	firstAt := worldStartsAt.Add(5 * time.Minute)
	secondAt := worldStartsAt.Add(10 * time.Minute)
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 1_000_000, 1_000_000),
		s.World.Schedule(events.EventSchedule{
			{Sequence: 1, OccursAt: firstAt, Event: events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: firstAt}},
			{Sequence: 2, OccursAt: secondAt, Event: events.VisitorArrived{VisitorID: "visitor-2", ArrivedAt: secondAt}},
		}),
	)

	s.When(s.Time.Advance(0, time.Hour))
	s.Then(s.Events.Contains(
		events.VisitorJourneyCompleted{VisitorID: "visitor-1", Outcome: model.VisitorPurchased, CompletedAt: firstAt},
		events.VisitorJourneyCompleted{VisitorID: "visitor-2", Outcome: model.VisitorPurchased, CompletedAt: secondAt},
	))
}
