package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

const (
	firstDeploymentID  model.DeploymentID = "deployment-1"
	secondDeploymentID model.DeploymentID = "deployment-2"
	deploymentDuration                    = 5 * time.Minute
)

func TestDeploymentListKeepsAppliedDeploymentAndUnlocksNext(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-list", 50, 0)

	s.Then(
		s.Deployments.Statuses(
			deploymentStatus(firstDeploymentID, 1, model.DeploymentStatusAvailable),
			deploymentStatus(secondDeploymentID, 2, model.DeploymentStatusLocked),
		),
	)

	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))
	s.When(s.Time.Advance(0, deploymentDuration))

	s.Then(
		s.Deployments.Statuses(
			deploymentStatus(firstDeploymentID, 1, model.DeploymentStatusApplied),
			deploymentStatus(secondDeploymentID, 2, model.DeploymentStatusAvailable),
		),
	)
}

func TestDeploymentStartSchedulesConfiguredDuration(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-duration", 50, 0)

	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))

	s.Then(
		s.Events.Contains(
			events.OperationQueued{
				OperationID: "operation-deployment-1",
				Kind:        model.OperationDeployment,
				QueuedAt:    worldStartsAt,
			},
			events.OperationStarted{
				OperationID: "operation-deployment-1",
				StartedAt:   worldStartsAt,
			},
			events.DeploymentStarted{
				CommandID:            "start-deployment-1",
				DeploymentID:         firstDeploymentID,
				OperationID:          "operation-deployment-1",
				StartedAt:            worldStartsAt,
				ExpectedCompletionAt: worldStartsAt.Add(deploymentDuration),
			},
		),
		s.Deployments.Statuses(
			deploymentStatus(firstDeploymentID, 1, model.DeploymentStatusRunning),
			deploymentStatus(secondDeploymentID, 2, model.DeploymentStatusLocked),
		),
	)
}

func TestDeploymentsMustRunInSequence(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-sequence", 50, 0)

	s.WhenFails(
		simulation.ErrDeploymentLocked,
		s.Deployment.Start("start-deployment-2", secondDeploymentID, "operation-deployment-2"),
	)
}

func TestOnlyOneDeploymentCanRunAtATime(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-concurrent", 50, 0)

	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))

	s.WhenFails(
		simulation.ErrDeploymentInProgress,
		s.Deployment.Start("start-deployment-2", secondDeploymentID, "operation-deployment-2"),
	)
}

func TestAppliedDeploymentCannotRunAgain(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-reapply", 50, 0)

	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))
	s.When(s.Time.Advance(0, deploymentDuration))

	s.WhenFails(
		simulation.ErrDeploymentApplied,
		s.Deployment.Start("start-deployment-1-again", firstDeploymentID, "operation-deployment-1-again"),
	)
}

func TestRequestsFailDuringDeploymentAndRecoverAfterCompletion(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-downtime", 50, 0)
	duringRequestID := model.RequestID("request-during-deployment")
	afterRequestID := model.RequestID("request-after-deployment")

	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))
	s.When(s.User.OpensPage(duringRequestID, "visitor-during", model.PageProductList, ""))

	s.Then(
		s.Logs.Exactly(capacityLog(
			duringRequestID,
			"visitor-during",
			worldStartsAt,
			500,
			model.FailureDeployment,
			"deployment in progress: deployment-1",
		)),
	)

	s.When(s.Time.Advance(0, deploymentDuration))
	s.When(s.User.OpensPage(afterRequestID, "visitor-after", model.PageProductList, ""))

	s.Then(
		s.Logs.Exactly(
			capacityLog(
				duringRequestID,
				"visitor-during",
				worldStartsAt,
				500,
				model.FailureDeployment,
				"deployment in progress: deployment-1",
			),
			capacityLog(afterRequestID, "visitor-after", worldStartsAt.Add(deploymentDuration), 200, "", ""),
		),
	)
}

func TestSuccessfulDeploymentChangesPageLoad(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-load-effect", 60, 0)
	s.Given(s.Deployment.PageLoadEffect(firstDeploymentID, model.PageProductList, 40, capacityHoldDuration))
	beforeFirstID := model.RequestID("load-before-deployment-first")
	beforeSecondID := model.RequestID("load-before-deployment-second")
	afterFirstID := model.RequestID("load-after-deployment-first")
	afterSecondID := model.RequestID("load-after-deployment-second")

	s.When(
		s.User.OpensPage(beforeFirstID, "visitor-1", model.PageProductList, ""),
		s.User.OpensPage(beforeSecondID, "visitor-2", model.PageProductList, ""),
	)

	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))
	s.When(s.Time.Advance(0, deploymentDuration))

	s.Then(
		s.Events.Contains(events.PageLoadChanged{
			DeploymentID:    firstDeploymentID,
			Page:            model.PageProductList,
			OldLoadUnits:    60,
			NewLoadUnits:    40,
			OldHoldDuration: capacityHoldDuration,
			NewHoldDuration: capacityHoldDuration,
			ChangedAt:       worldStartsAt.Add(deploymentDuration),
		}),
	)

	s.When(
		s.User.OpensPage(afterFirstID, "visitor-3", model.PageProductList, ""),
		s.User.OpensPage(afterSecondID, "visitor-4", model.PageProductList, ""),
	)

	s.Then(
		s.Logs.Exactly(
			capacityLog(beforeFirstID, "visitor-1", worldStartsAt, 200, "", ""),
			capacityLog(beforeSecondID, "visitor-2", worldStartsAt, 500, model.FailureServerCapacityExceeded, "server capacity exceeded: required=60 available=40"),
			capacityLog(afterFirstID, "visitor-3", worldStartsAt.Add(deploymentDuration), 200, "", ""),
			capacityLog(afterSecondID, "visitor-4", worldStartsAt.Add(deploymentDuration), 200, "", ""),
		),
	)
}

func TestSuccessfulDeploymentChangesBugProbability(t *testing.T) {
	s := spec.New(t, "run-deployment-bug-effect")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 850_000, 200_000),
		s.Bug.Activated(pageBugID, model.PageProduct, "product-1", 1_000_000, correctFixText),
		s.Deployment.Defined(firstDeploymentID, 1, "Stabilize product page", deploymentDuration, 0),
		s.Deployment.BugProbabilityEffect(firstDeploymentID, pageBugID, 0),
		s.Deployment.Unlocked(firstDeploymentID),
	)
	beforeRequestID := model.RequestID("bug-before-deployment")
	afterRequestID := model.RequestID("bug-after-deployment")

	s.When(s.User.OpensPage(beforeRequestID, "visitor-before", model.PageProduct, "product-1"))
	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))
	s.When(s.Time.Advance(0, deploymentDuration))

	s.Then(
		s.Events.Contains(events.PageBugProbabilityChanged{
			DeploymentID:      firstDeploymentID,
			BugID:             pageBugID,
			OldProbabilityPPM: 1_000_000,
			NewProbabilityPPM: 0,
			ChangedAt:         worldStartsAt.Add(deploymentDuration),
		}),
	)

	s.When(s.User.OpensPage(afterRequestID, "visitor-after", model.PageProduct, "product-1"))

	s.Then(
		s.Logs.Exactly(
			pageBugLog(beforeRequestID, "visitor-before"),
			successPageLogAt(afterRequestID, "visitor-after", worldStartsAt.Add(deploymentDuration)),
		),
	)
}

func TestStartingDeploymentReleasesExistingCapacityAllocations(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-releases-capacity", capacityServerUnits, 0)
	requestID := model.RequestID("request-before-deployment-release")

	s.When(s.User.OpensPage(requestID, "visitor-1", model.PageProductList, ""))
	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))

	s.Then(
		s.Events.Contains(events.CapacityAllocationReleased{
			DeploymentID: firstDeploymentID,
			RequestID:    requestID,
			ServerID:     capacityServerID,
			ReleasedAt:   worldStartsAt,
		}),
	)
}

func TestFailedDeploymentDoesNotApplyEffectsAndUnlocksNext(t *testing.T) {
	s := newDeploymentScenario(t, "run-deployment-failure", 60, 1_000_000)
	s.Given(s.Deployment.PageLoadEffect(firstDeploymentID, model.PageProductList, 40, capacityHoldDuration))
	duringRequestID := model.RequestID("request-during-failed-deployment")
	afterFirstID := model.RequestID("request-after-failed-deployment-first")
	afterSecondID := model.RequestID("request-after-failed-deployment-second")

	s.When(s.Deployment.Start("start-deployment-1", firstDeploymentID, "operation-deployment-1"))
	s.When(s.User.OpensPage(duringRequestID, "visitor-during", model.PageProductList, ""))
	s.When(s.Time.Advance(0, deploymentDuration))

	s.Then(
		s.Events.Contains(
			events.DeploymentFailed{
				DeploymentID: firstDeploymentID,
				OperationID:  "operation-deployment-1",
				ErrorCode:    "DEPLOYMENT_FAILED",
				Message:      "deployment failed",
				FailedAt:     worldStartsAt.Add(deploymentDuration),
			},
			events.OperationFailed{
				OperationID: "operation-deployment-1",
				ErrorCode:   "DEPLOYMENT_FAILED",
				Message:     "deployment failed",
				FailedAt:    worldStartsAt.Add(deploymentDuration),
			},
			events.DeploymentUnlocked{
				DeploymentID: secondDeploymentID,
				UnlockedAt:   worldStartsAt.Add(deploymentDuration),
			},
		),
		s.Events.HasNoType("PageLoadChanged"),
		s.Deployments.Statuses(
			deploymentStatus(firstDeploymentID, 1, model.DeploymentStatusFailed),
			deploymentStatus(secondDeploymentID, 2, model.DeploymentStatusAvailable),
		),
	)

	s.When(
		s.User.OpensPage(afterFirstID, "visitor-1", model.PageProductList, ""),
		s.User.OpensPage(afterSecondID, "visitor-2", model.PageProductList, ""),
	)

	s.Then(
		s.Logs.Exactly(
			capacityLog(duringRequestID, "visitor-during", worldStartsAt, 500, model.FailureDeployment, "deployment in progress: deployment-1"),
			capacityLog(afterFirstID, "visitor-1", worldStartsAt.Add(deploymentDuration), 200, "", ""),
			capacityLog(afterSecondID, "visitor-2", worldStartsAt.Add(deploymentDuration), 500, model.FailureServerCapacityExceeded, "server capacity exceeded: required=60 available=40"),
		),
	)
}

func newDeploymentScenario(t *testing.T, runID string, pageLoad int64, firstFailurePPM uint32) *spec.Scenario {
	t.Helper()
	s := newCapacityScenario(t, runID, pageLoad)
	s.Given(
		s.Deployment.Defined(firstDeploymentID, 1, "First deployment", deploymentDuration, firstFailurePPM),
		s.Deployment.Defined(secondDeploymentID, 2, "Second deployment", deploymentDuration, 0),
		s.Deployment.Unlocked(firstDeploymentID),
	)
	return s
}

func deploymentStatus(
	id model.DeploymentID,
	sequence int,
	status model.DeploymentLifecycleStatus,
) spec.DeploymentStatusExpectation {
	return spec.DeploymentStatusExpectation{ID: id, Sequence: sequence, Status: status}
}
