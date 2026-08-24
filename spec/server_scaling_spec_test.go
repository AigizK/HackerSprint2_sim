package spec_test

import (
	"testing"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const (
	secondServerID       model.ServerID    = "server-2"
	addServerCommandID   model.CommandID   = "add-server-2"
	addServerOperationID model.OperationID = "operation-add-server-2"
	removeCommandID      model.CommandID   = "remove-server-2"
	removeOperationID    model.OperationID = "operation-remove-server-2"
)

func TestAddingServerMakesCapacityAvailableAfterOverload(t *testing.T) {
	s := newCapacityScenario(t, "run-scale-after-overload", 60)
	firstRequestID := model.RequestID("request-before-scale-accepted")
	overloadedRequestID := model.RequestID("request-before-scale-rejected")
	duringProvisioningRequestID := model.RequestID("request-during-provisioning")
	afterScaleFirstRequestID := model.RequestID("request-after-scale-first")
	afterScaleSecondRequestID := model.RequestID("request-after-scale-second")

	s.When(
		s.User.OpensPage(firstRequestID, "visitor-1", model.PageProductList, ""),
		s.User.OpensPage(overloadedRequestID, "visitor-2", model.PageProductList, ""),
	)

	s.Then(
		s.Logs.Exactly(
			capacityLog(firstRequestID, "visitor-1", worldStartsAt, 200, "", ""),
			capacityLog(
				overloadedRequestID,
				"visitor-2",
				worldStartsAt,
				500,
				model.FailureServerCapacityExceeded,
				"server capacity exceeded: required=60 available=40",
			),
		),
	)

	s.When(
		s.Server.Add(addServerCommandID, addServerOperationID, secondServerID, capacityServerUnits, capacityCostPerHour),
	)

	s.When(
		s.User.OpensPage(duringProvisioningRequestID, "visitor-3", model.PageProductList, ""),
	)

	s.Then(
		s.Logs.Exactly(
			capacityLog(firstRequestID, "visitor-1", worldStartsAt, 200, "", ""),
			capacityLog(
				overloadedRequestID,
				"visitor-2",
				worldStartsAt,
				500,
				model.FailureServerCapacityExceeded,
				"server capacity exceeded: required=60 available=40",
			),
			capacityLog(
				duringProvisioningRequestID,
				"visitor-3",
				worldStartsAt,
				500,
				model.FailureServerCapacityExceeded,
				"server capacity exceeded: required=60 available=40",
			),
		),
	)

	s.When(
		s.Time.Advance(0, serverProvisioningDuration),
	)

	s.Then(
		s.State.ServerStatus(secondServerID, model.ServerActive),
	)

	s.When(
		s.Time.Advance(0, capacityHoldDuration-serverProvisioningDuration),
	)

	s.When(
		s.User.OpensPage(afterScaleFirstRequestID, "visitor-4", model.PageProductList, ""),
		s.User.OpensPage(afterScaleSecondRequestID, "visitor-5", model.PageProductList, ""),
	)

	afterScaleAt := worldStartsAt.Add(capacityHoldDuration)
	s.Then(
		s.Events.Exactly(
			events.PageRequestStarted{
				RequestID: afterScaleFirstRequestID,
				Source:    model.RequestSourceVisitor,
				VisitorID: "visitor-4",
				Page:      model.PageProductList,
				LoadUnits: 60,
				StartedAt: afterScaleAt,
			},
			events.PageRequestAccepted{
				RequestID:  afterScaleFirstRequestID,
				ServerID:   capacityServerID,
				AcceptedAt: afterScaleAt,
				ReleasesAt: afterScaleAt.Add(capacityHoldDuration),
			},
			events.PageRequestCompleted{
				RequestID:   afterScaleFirstRequestID,
				ServerID:    capacityServerID,
				StatusCode:  200,
				CompletedAt: afterScaleAt,
			},
			events.PageRequestStarted{
				RequestID: afterScaleSecondRequestID,
				Source:    model.RequestSourceVisitor,
				VisitorID: "visitor-5",
				Page:      model.PageProductList,
				LoadUnits: 60,
				StartedAt: afterScaleAt,
			},
			events.PageRequestAccepted{
				RequestID:  afterScaleSecondRequestID,
				ServerID:   secondServerID,
				AcceptedAt: afterScaleAt,
				ReleasesAt: afterScaleAt.Add(capacityHoldDuration),
			},
			events.PageRequestCompleted{
				RequestID:   afterScaleSecondRequestID,
				ServerID:    secondServerID,
				StatusCode:  200,
				CompletedAt: afterScaleAt,
			},
		),
		s.Logs.Exactly(
			capacityLog(firstRequestID, "visitor-1", worldStartsAt, 200, "", ""),
			capacityLog(
				overloadedRequestID,
				"visitor-2",
				worldStartsAt,
				500,
				model.FailureServerCapacityExceeded,
				"server capacity exceeded: required=60 available=40",
			),
			capacityLog(
				duringProvisioningRequestID,
				"visitor-3",
				worldStartsAt,
				500,
				model.FailureServerCapacityExceeded,
				"server capacity exceeded: required=60 available=40",
			),
			capacityLog(afterScaleFirstRequestID, "visitor-4", afterScaleAt, 200, "", ""),
			capacityLog(afterScaleSecondRequestID, "visitor-5", afterScaleAt, 200, "", ""),
		),
	)
}

func TestServerCanBeAdded(t *testing.T) {
	s := newCapacityScenario(t, "run-add-server", 50)

	s.When(
		s.Server.Add(addServerCommandID, addServerOperationID, secondServerID, capacityServerUnits, capacityCostPerHour),
	)

	s.Then(
		s.Events.Exactly(addServerEvents()...),
		s.State.ServerStatus(secondServerID, model.ServerProvisioning),
	)

	s.When(
		s.Time.Advance(0, serverProvisioningDuration),
	)

	s.Then(
		s.Events.Exactly(
			events.TimeAdvanced{
				From:              worldStartsAt,
				To:                worldStartsAt.Add(serverProvisioningDuration),
				RequestedDuration: serverProvisioningDuration,
				AppliedDuration:   serverProvisioningDuration,
			},
			events.ServerActivated{
				OperationID: addServerOperationID,
				ServerID:    secondServerID,
				ActivatedAt: worldStartsAt.Add(serverProvisioningDuration),
			},
			events.OperationSucceeded{
				OperationID: addServerOperationID,
				CompletedAt: worldStartsAt.Add(serverProvisioningDuration),
			},
		),
		s.State.ServerStatus(secondServerID, model.ServerActive),
	)
}

func TestIdleServerIsRemovedImmediately(t *testing.T) {
	s := newCapacityScenario(t, "run-remove-idle-server", 50)
	s.Given(
		s.Server.Active(secondServerID, capacityServerUnits, capacityCostPerHour),
	)

	s.When(
		s.Server.Remove(removeCommandID, removeOperationID, secondServerID),
	)

	s.Then(
		s.Events.Exactly(
			events.BackendScaleRequested{
				CommandID:        removeCommandID,
				OperationID:      removeOperationID,
				DesiredInstances: 1,
				RequestedAt:      worldStartsAt,
			},
			events.OperationQueued{
				OperationID: removeOperationID,
				Kind:        model.OperationScaleBackend,
				QueuedAt:    worldStartsAt,
			},
			events.OperationStarted{OperationID: removeOperationID, StartedAt: worldStartsAt},
			events.ServerDrainingStarted{OperationID: removeOperationID, ServerID: secondServerID, StartedAt: worldStartsAt},
			events.ServerRemoved{OperationID: removeOperationID, ServerID: secondServerID, RemovedAt: worldStartsAt},
			events.OperationSucceeded{OperationID: removeOperationID, CompletedAt: worldStartsAt},
		),
		s.State.HasNoServer(secondServerID),
	)
}

func TestBusyServerIsRemovedOnlyAfterItsLoadIsReleased(t *testing.T) {
	s := newCapacityScenario(t, "run-remove-busy-server", capacityServerUnits)
	requestID := model.RequestID("request-keeping-server-busy")

	s.When(
		s.User.OpensPage(requestID, "visitor-1", model.PageProductList, ""),
	)

	s.When(
		s.Server.Remove(removeCommandID, removeOperationID, capacityServerID),
	)

	s.Then(
		s.Events.Exactly(
			events.BackendScaleRequested{
				CommandID:        removeCommandID,
				OperationID:      removeOperationID,
				DesiredInstances: 0,
				RequestedAt:      worldStartsAt,
			},
			events.OperationQueued{
				OperationID: removeOperationID,
				Kind:        model.OperationScaleBackend,
				QueuedAt:    worldStartsAt,
			},
			events.OperationStarted{OperationID: removeOperationID, StartedAt: worldStartsAt},
			events.ServerDrainingStarted{OperationID: removeOperationID, ServerID: capacityServerID, StartedAt: worldStartsAt},
		),
		s.State.ServerStatus(capacityServerID, model.ServerDraining),
	)

	s.When(
		s.Time.Advance(0, capacityHoldDuration),
	)

	s.Then(
		s.Events.Exactly(
			events.TimeAdvanced{
				From:              worldStartsAt,
				To:                worldStartsAt.Add(capacityHoldDuration),
				RequestedDuration: capacityHoldDuration,
				AppliedDuration:   capacityHoldDuration,
			},
			events.ServerRemoved{
				OperationID: removeOperationID,
				ServerID:    capacityServerID,
				RemovedAt:   worldStartsAt.Add(capacityHoldDuration),
			},
			events.OperationSucceeded{
				OperationID: removeOperationID,
				CompletedAt: worldStartsAt.Add(capacityHoldDuration),
			},
		),
		s.State.HasNoServer(capacityServerID),
	)
}

func addServerEvents() []events.Event {
	return []events.Event{
		events.BackendScaleRequested{
			CommandID:        addServerCommandID,
			OperationID:      addServerOperationID,
			DesiredInstances: 2,
			RequestedAt:      worldStartsAt,
		},
		events.OperationQueued{
			OperationID: addServerOperationID,
			Kind:        model.OperationScaleBackend,
			QueuedAt:    worldStartsAt,
		},
		events.OperationStarted{OperationID: addServerOperationID, StartedAt: worldStartsAt},
		events.ServerProvisioningStarted{
			OperationID:      addServerOperationID,
			ServerID:         secondServerID,
			CapacityUnits:    capacityServerUnits,
			CostPerHourMinor: capacityCostPerHour,
			StartedAt:        worldStartsAt,
			ReadyAt:          worldStartsAt.Add(serverProvisioningDuration),
		},
	}
}
