package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestSetBackendDesiredInstancesAddsAndRemovesRequiredServers(t *testing.T) {
	s := newCapacityScenario(t, "run-set-desired-instances", 50)

	s.When(s.Server.SetDesired("scale-to-three", "scale-to-three-operation", 3))
	s.Then(s.Future.Resources(spec.ResourcesView{
		DesiredInstances: 3, ActiveInstances: 1, TotalCapacityUnits: 100, TotalCostPerHourMinor: 1_000,
		Servers: []spec.ServerResourceView{
			{ServerID: capacityServerID, Status: model.ServerActive, CapacityUnits: 100, CostPerHourMinor: 1_000},
			{ServerID: "server-scale-to-three-1", Status: model.ServerProvisioning, CapacityUnits: 100, CostPerHourMinor: 1_000},
			{ServerID: "server-scale-to-three-2", Status: model.ServerProvisioning, CapacityUnits: 100, CostPerHourMinor: 1_000},
		},
	}))

	s.When(s.Time.Advance(0, serverProvisioningDuration))
	s.Then(s.Future.Resources(spec.ResourcesView{
		DesiredInstances:   3,
		ActiveInstances:    3,
		TotalCapacityUnits: 300, TotalCostPerHourMinor: 3_000,
		Servers: []spec.ServerResourceView{
			{ServerID: capacityServerID, Status: model.ServerActive, CapacityUnits: 100, CostPerHourMinor: 1_000},
			{ServerID: "server-scale-to-three-1", Status: model.ServerActive, CapacityUnits: 100, CostPerHourMinor: 1_000},
			{ServerID: "server-scale-to-three-2", Status: model.ServerActive, CapacityUnits: 100, CostPerHourMinor: 1_000},
		},
	}))

	s.When(s.Server.SetDesired("scale-to-zero", "scale-to-zero-operation", 0))
	s.Then(s.Future.Resources(spec.ResourcesView{
		DesiredInstances: 0, ActiveInstances: 0, TotalCapacityUnits: 0,
	}))
}

func TestOperationProjectionTracksRunningAndSucceededScaling(t *testing.T) {
	s := newCapacityScenario(t, "run-operation-scaling", 50)
	s.When(s.Server.Add("operation-add", "operation-add-server", secondServerID, 100, 1_000))

	s.Then(s.Future.Operation("operation-add-server", spec.OperationView{
		OperationID: "operation-add-server",
		Kind:        model.OperationScaleBackend,
		Status:      model.OperationStatusRunning,
		Progress:    0,
		SubmittedAt: worldStartsAt,
		StartedAt:   worldStartsAt,
	}))

	s.When(s.Time.Advance(0, serverProvisioningDuration))
	s.Then(s.Future.Operation("operation-add-server", spec.OperationView{
		OperationID: "operation-add-server",
		Kind:        model.OperationScaleBackend,
		Status:      model.OperationStatusSucceeded,
		Progress:    1,
		SubmittedAt: worldStartsAt,
		StartedAt:   worldStartsAt,
		CompletedAt: worldStartsAt.Add(serverProvisioningDuration),
	}))
}

func TestOperationProjectionContainsDeploymentFailure(t *testing.T) {
	s := newDeploymentScenario(t, "run-operation-failure", 50, 1_000_000)
	s.When(s.Deployment.Start("failed-deployment", firstDeploymentID, "failed-deployment-operation"))
	s.When(s.Time.Advance(0, deploymentDuration))

	s.Then(s.Future.Operation("failed-deployment-operation", spec.OperationView{
		OperationID: "failed-deployment-operation",
		Kind:        model.OperationDeployment,
		Status:      model.OperationStatusFailed,
		Progress:    1,
		SubmittedAt: worldStartsAt,
		StartedAt:   worldStartsAt,
		CompletedAt: worldStartsAt.Add(deploymentDuration),
		ErrorCode:   "DEPLOYMENT_FAILED",
		Message:     "deployment failed",
	}))
}

func TestUnknownOperationIsNotFound(t *testing.T) {
	s := spec.New(t, "run-operation-missing")
	s.Given(s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))
	s.Then(s.Future.OperationMissing("unknown-operation"))
}

func TestFixCommandIsIdempotent(t *testing.T) {
	s := spec.New(t, "run-idempotent-fix")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 850_000, 200_000),
		s.Bug.Activated(pageBugID, model.PageProduct, "product-1", 1_000_000, correctFixText),
	)

	s.When(s.Bug.Fix("same-fix-request", correctFixText))
	s.When(s.Bug.Fix("same-fix-request", correctFixText))
	s.Then(s.Events.None())

	s.WhenFails(
		simulation.ErrIdempotencyConflict,
		s.Bug.Fix("same-fix-request", "different-payload"),
	)
}

func TestDeploymentCommandIsIdempotent(t *testing.T) {
	s := newDeploymentScenario(t, "run-idempotent-deployment", 50, 0)
	s.When(s.Deployment.Start("same-deployment-request", firstDeploymentID, "same-deployment-operation"))
	s.When(s.Deployment.Start("same-deployment-request", firstDeploymentID, "same-deployment-operation"))
	s.Then(s.Events.None())
}

func TestScaleCommandIsIdempotent(t *testing.T) {
	s := newCapacityScenario(t, "run-idempotent-scale", 50)
	s.When(s.Server.SetDesired("same-scale-request", "same-scale-operation", 3))
	s.When(s.Server.SetDesired("same-scale-request", "same-scale-operation", 3))
	s.Then(s.Events.None())

	s.WhenFails(
		simulation.ErrIdempotencyConflict,
		s.Server.SetDesired("same-scale-request", "different-operation", 4),
	)
}

func TestAdvanceTimeCommandIsIdempotent(t *testing.T) {
	s := spec.New(t, "run-idempotent-time")
	s.Given(s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))

	s.When(s.Time.AdvanceRequest("same-time-request", 0, 5*time.Minute))
	s.When(s.Time.AdvanceRequest("same-time-request", 0, 5*time.Minute))
	s.Then(
		s.Events.None(),
		s.State.CurrentTime(worldStartsAt.Add(5*time.Minute)),
	)
}
