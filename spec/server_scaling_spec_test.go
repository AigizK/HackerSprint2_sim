package spec_test

import (
	"fmt"
	"testing"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
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
	wantEvents := append(
		acceptedRequestEventsOnServer(afterScaleFirstRequestID, "visitor-4", 60, afterScaleAt, capacityServerID),
		acceptedRequestEventsOnServer(afterScaleSecondRequestID, "visitor-5", 60, afterScaleAt, secondServerID)...,
	)
	wantEvents = append(wantEvents, events.BackendAvailabilityChanged{Available: false,
		UnavailablePages: []model.PageType{model.PageProductList}, ChangedAt: afterScaleAt})
	s.Then(
		s.Events.Exactly(wantEvents...),
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
			events.InfrastructureCostAccrued{
				ServerID: capacityServerID, From: worldStartsAt, To: worldStartsAt.Add(serverProvisioningDuration),
				BilledHours: 1, AmountMinor: capacityCostPerHour,
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
			events.ServerCommandAccepted{CommandID: removeCommandID, OperationID: removeOperationID, Payload: fmt.Sprintf("server.delete:%s:%s", removeOperationID, secondServerID), AcceptedAt: worldStartsAt},
			events.OperationQueued{
				OperationID: removeOperationID,
				Kind:        model.OperationControlCommand,
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
	s.Given(s.Server.Active(secondServerID, capacityServerUnits, capacityCostPerHour))

	s.When(
		s.User.OpensPage(requestID, "visitor-1", model.PageProductList, ""),
	)

	s.When(
		s.Server.Remove(removeCommandID, removeOperationID, capacityServerID),
	)

	s.Then(
		s.Events.Exactly(
			events.ServerCommandAccepted{CommandID: removeCommandID, OperationID: removeOperationID, Payload: fmt.Sprintf("server.delete:%s:%s", removeOperationID, capacityServerID), AcceptedAt: worldStartsAt},
			events.OperationQueued{
				OperationID: removeOperationID,
				Kind:        model.OperationControlCommand,
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
			events.InfrastructureCostAccrued{
				ServerID: capacityServerID, From: worldStartsAt, To: worldStartsAt.Add(capacityHoldDuration),
				BilledHours: 1, AmountMinor: capacityCostPerHour,
			},
			events.InfrastructureCostAccrued{
				ServerID: secondServerID, From: worldStartsAt, To: worldStartsAt.Add(capacityHoldDuration),
				BilledHours: 1, AmountMinor: capacityCostPerHour,
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

func TestLastBackendServerCannotBeRemoved(t *testing.T) {
	s := newCapacityScenario(t, "run-keep-last-server", capacityServerUnits)
	s.WhenFails(simulation.ErrInvalidCommand, s.Server.Remove(removeCommandID, removeOperationID, capacityServerID))
}

func addServerEvents() []events.Event {
	return []events.Event{
		events.ServerCommandAccepted{CommandID: addServerCommandID, OperationID: addServerOperationID, Payload: fmt.Sprintf("server.create:%s:%s:%d:%d", addServerOperationID, secondServerID, capacityServerUnits, capacityCostPerHour), AcceptedAt: worldStartsAt},
		events.OperationQueued{
			OperationID: addServerOperationID,
			Kind:        model.OperationControlCommand,
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
