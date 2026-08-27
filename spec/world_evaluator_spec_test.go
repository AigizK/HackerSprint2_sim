package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type worldEvaluatorScenario struct {
	t          *testing.T
	world      generator.WorldDefinition
	config     generator.EvaluationConfig
	evaluation generator.WorldEvaluation
}

func newWorldEvaluatorScenario(t *testing.T) *worldEvaluatorScenario {
	startsAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	return &worldEvaluatorScenario{
		t: t,
		config: generator.EvaluationConfig{
			InitialBackendInstances: 1, ServerCapacityUnits: 100, ServerCostPerHourMinor: 100,
			ServerProvisioningTime: 30 * time.Minute, AgentRequestDuration: 10 * time.Second,
		},
		world: generator.WorldDefinition{
			WorldID: "world-evaluator", Key: generator.WorldKey{Seed: 42}, StartsAt: startsAt, EndsAt: startsAt.Add(2 * time.Hour),
			Bootstrap: []events.Event{
				events.EconomyConfigured{InitialBalanceMinor: 10_000, StopRunOnNegativeBalance: true, ServerBillingPeriod: time.Hour, ConfiguredAt: startsAt},
				events.PageConfigured{Page: model.PageProductList, LoadUnits: 10, HoldDuration: time.Minute, ConfiguredAt: startsAt},
				events.PageConfigured{Page: model.PageProduct, LoadUnits: 20, HoldDuration: time.Minute, ConfiguredAt: startsAt},
				events.PageConfigured{Page: model.PagePurchase, LoadUnits: 30, HoldDuration: time.Minute, ConfiguredAt: startsAt},
				events.ProductAdded{ProductID: "product-1", Name: "Product", PriceMinor: 1_000, ViewProbabilityPPM: generator.ProbabilityScale, PurchaseProbabilityPPM: generator.ProbabilityScale, AddedAt: startsAt},
				events.PageBugActivated{BugID: "bug-1", Page: model.PagePurchase, FailureProbabilityPPM: generator.ProbabilityScale, FixMessage: "FIX-ONE", FixMessageHash: "hash", ActivatedAt: startsAt},
			},
			Events: events.EventSchedule{
				{Sequence: 1, OccursAt: startsAt.Add(15 * time.Minute), Event: events.VisitorArrived{VisitorID: "visitor-1", ArrivedAt: startsAt.Add(15 * time.Minute)}},
				{Sequence: 2, OccursAt: startsAt.Add(45 * time.Minute), Event: events.VisitorArrived{VisitorID: "visitor-2", ArrivedAt: startsAt.Add(45 * time.Minute)}},
			},
		},
	}
}

func (s *worldEvaluatorScenario) WhenWorldIsEvaluated() {
	s.t.Helper()
	evaluator, err := generator.NewWorldEvaluator(s.config)
	if err != nil {
		s.t.Fatal(err)
	}
	s.evaluation, err = evaluator.Evaluate(s.world)
	if err != nil {
		s.t.Fatal(err)
	}
}

func (s *worldEvaluatorScenario) ThenMaximumEconomyIs(revenue, serverCost, balance int64, purchases uint64) {
	s.t.Helper()
	got := s.evaluation
	if got.MaximumRevenueMinor != revenue || got.MinimumServerCostMinor != serverCost ||
		got.MaximumBalanceMinor != balance || got.MaximumPurchases != purchases {
		s.t.Fatalf("evaluation economy = %#v", got)
	}
}

func (s *worldEvaluatorScenario) ThenMinimumAgentTimeIs(requests int, duration time.Duration) {
	s.t.Helper()
	if s.evaluation.AgentRequestCount != requests || s.evaluation.MinimumRealTime != duration {
		s.t.Fatalf("requests/time = %d/%s, want %d/%s", s.evaluation.AgentRequestCount, s.evaluation.MinimumRealTime, requests, duration)
	}
}

func TestWorldEvaluatorCalculatesMaximumBalanceAndMinimumAgentTime(t *testing.T) {
	s := newWorldEvaluatorScenario(t)
	s.WhenWorldIsEvaluated()
	s.ThenMaximumEconomyIs(2_000, 200, 11_800, 2)
	s.ThenMinimumAgentTimeIs(3, 30*time.Second) // start + fix + final time advance
}

func TestWorldEvaluatorAccountsForProvisioningBeforeRevenueCanBeEarned(t *testing.T) {
	s := newWorldEvaluatorScenario(t)
	for index, event := range s.world.Bootstrap {
		if page, ok := event.(events.PageConfigured); ok {
			page.LoadUnits = 40
			s.world.Bootstrap[index] = page
		}
	}
	s.world.Bootstrap = s.world.Bootstrap[:len(s.world.Bootstrap)-1] // no bug in this scenario
	s.WhenWorldIsEvaluated()
	s.ThenMaximumEconomyIs(1_000, 400, 10_600, 1)
	s.ThenMinimumAgentTimeIs(4, 40*time.Second) // start + scale + provisioning advance + final advance
}

func TestWorldEvaluatorKeepsOneBackendServerWhenWorldIsUnprofitable(t *testing.T) {
	s := newWorldEvaluatorScenario(t)
	s.config.ServerCostPerHourMinor = 2_000
	s.WhenWorldIsEvaluated()
	s.ThenMaximumEconomyIs(2_000, 4_000, 8_000, 2)
	if s.evaluation.RequiredServerCount != 1 {
		t.Fatalf("required servers = %d, want 1", s.evaluation.RequiredServerCount)
	}
}
