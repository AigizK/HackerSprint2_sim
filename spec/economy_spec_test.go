package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestServerUsageIsRoundedUpToWholeHours(t *testing.T) {
	testCases := []struct {
		name        string
		duration    time.Duration
		billedHours int64
		amountMinor int64
	}{
		{name: "one minute", duration: time.Minute, billedHours: 1, amountMinor: 1_000},
		{name: "fifty nine minutes", duration: 59 * time.Minute, billedHours: 1, amountMinor: 1_000},
		{name: "exactly one hour", duration: time.Hour, billedHours: 1, amountMinor: 1_000},
		{name: "one hour and one second", duration: time.Hour + time.Second, billedHours: 2, amountMinor: 2_000},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			s := newCapacityScenario(t, "run-cost-"+testCase.name, 50)
			s.When(s.Time.Advance(testCase.duration, 0))
			s.Then(
				s.Events.Contains(events.InfrastructureCostAccrued{
					ServerID: capacityServerID, From: worldStartsAt, To: worldStartsAt.Add(testCase.duration),
					BilledHours: testCase.billedHours, AmountMinor: testCase.amountMinor,
				}),
				s.Future.Economy(spec.EconomyView{
					ServerCostMinor: testCase.amountMinor,
					BalanceMinor:    -testCase.amountMinor,
				}),
			)
		})
	}
}

func TestRunStopsAsSoonAsBalanceBecomesNegative(t *testing.T) {
	s := newCapacityScenario(t, "run-negative-balance", 50)
	s.Given(s.World.EconomyConfigured(1_000, time.Hour))

	s.When(s.Time.Advance(time.Hour+time.Second, 0))

	s.Then(
		s.Events.Contains(events.RunEnded{
			CompletedAt: worldStartsAt.Add(time.Hour + time.Second), Reason: "negative_balance",
		}),
		s.State.IsCompleted(),
		s.Future.Economy(spec.EconomyView{ServerCostMinor: 2_000, BalanceMinor: -1_000}),
	)
}

func TestSplitTimeAdvancesDoNotDoubleBillServerHour(t *testing.T) {
	s := newCapacityScenario(t, "run-cost-split-advance", 50)
	s.When(s.Time.Advance(30*time.Minute, 0))
	s.Then(s.Events.Contains(events.InfrastructureCostAccrued{
		ServerID: capacityServerID, From: worldStartsAt, To: worldStartsAt.Add(30 * time.Minute),
		BilledHours: 1, AmountMinor: 1_000,
	}))

	s.When(s.Time.Advance(30*time.Minute, 0))
	s.Then(
		s.Events.HasNoType("InfrastructureCostAccrued"),
		s.Future.Economy(spec.EconomyView{ServerCostMinor: 1_000, BalanceMinor: -1_000}),
	)
}

func TestProvisioningServerIsNotBilled(t *testing.T) {
	s := newCapacityScenario(t, "run-cost-server-lifecycle", 50)
	s.When(s.Server.Add("cost-add", "cost-add-operation", secondServerID, 100, 1_000))
	s.When(s.Time.Advance(serverProvisioningDuration, 0))

	s.Then(s.Events.Contains(
		events.InfrastructureCostAccrued{
			ServerID: capacityServerID, From: worldStartsAt, To: worldStartsAt.Add(serverProvisioningDuration),
			BilledHours: 1, AmountMinor: 1_000,
		},
	))
}

func TestDrainingServerContinuesToBeBilled(t *testing.T) {
	s := newCapacityScenario(t, "run-cost-draining-server", 50)
	s.Given(s.Server.Active(secondServerID, 100, 1_000))
	s.When(s.User.OpensPage("request-keeping-cost-server-busy", "visitor-1", model.PageProductList, ""))
	s.When(s.Server.Remove("cost-remove", "cost-remove-operation", capacityServerID))
	s.When(s.Time.Advance(5*time.Minute, 0))

	s.Then(s.Events.Contains(events.InfrastructureCostAccrued{
		ServerID: capacityServerID, From: worldStartsAt, To: worldStartsAt.Add(5 * time.Minute),
		BilledHours: 1, AmountMinor: 1_000,
	}))
}

func TestDeploymentCostIsChargedOnceEvenWhenDeploymentFails(t *testing.T) {
	s := newCapacityScenario(t, "run-deployment-cost", 50)
	s.Given(
		s.Deployment.DefinedWithCost(firstDeploymentID, 1, "Paid deployment", 2_500, deploymentDuration, 1_000_000),
		s.Deployment.Unlocked(firstDeploymentID),
	)

	s.When(s.Deployment.Start("paid-deployment", firstDeploymentID, "paid-deployment-operation"))
	s.Then(s.Events.Contains(events.DeploymentCostAccrued{
		DeploymentID: firstDeploymentID, AmountMinor: 2_500, AccruedAt: worldStartsAt,
	}))

	s.When(s.Time.Advance(0, deploymentDuration))
	s.Then(s.Future.Economy(spec.EconomyView{
		DeploymentCostMinor: 2_500,
		ServerCostMinor:     1_000,
		BalanceMinor:        -3_500,
	}))
}
