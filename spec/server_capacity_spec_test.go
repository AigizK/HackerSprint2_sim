package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

const (
	capacityServerID           model.ServerID = "server-1"
	capacityServerUnits                       = int64(100)
	capacityCostPerHour                       = int64(1_000)
	capacityHoldDuration                      = 10 * time.Minute
	serverProvisioningDuration                = 5 * time.Minute
)

func TestRequestsWithinServerCapacityAreSuccessful(t *testing.T) {
	s := newCapacityScenario(t, "run-capacity-within-limit", 50)
	firstRequestID := model.RequestID("request-capacity-first")
	secondRequestID := model.RequestID("request-capacity-second")

	s.When(
		s.User.OpensPage(firstRequestID, "visitor-1", model.PageProductList, ""),
		s.User.OpensPage(secondRequestID, "visitor-2", model.PageProductList, ""),
	)

	wantEvents := append(
		acceptedRequestEvents(firstRequestID, "visitor-1", 50, worldStartsAt),
		acceptedRequestEvents(secondRequestID, "visitor-2", 50, worldStartsAt)...,
	)
	wantEvents = append(wantEvents, events.BackendAvailabilityChanged{Available: false,
		UnavailablePages: []model.PageType{model.PageProductList}, ChangedAt: worldStartsAt})
	s.Then(
		s.Events.Exactly(wantEvents...),
		s.Logs.Exactly(
			capacityLog(firstRequestID, "visitor-1", worldStartsAt, 200, "", ""),
			capacityLog(secondRequestID, "visitor-2", worldStartsAt, 200, "", ""),
		),
	)
}

func TestRequestExceedingServerCapacityIsRejected(t *testing.T) {
	s := newCapacityScenario(t, "run-capacity-exceeded", 60)
	acceptedRequestID := model.RequestID("request-capacity-accepted")
	rejectedRequestID := model.RequestID("request-capacity-rejected")

	s.When(
		s.User.OpensPage(acceptedRequestID, "visitor-1", model.PageProductList, ""),
	)

	s.Then(
		s.Logs.Exactly(
			capacityLog(acceptedRequestID, "visitor-1", worldStartsAt, 200, "", ""),
		),
	)

	s.When(
		s.User.OpensPage(rejectedRequestID, "visitor-2", model.PageProductList, ""),
	)

	s.Then(
		s.Events.Exactly(
			events.PageRequestStarted{
				RequestID:  rejectedRequestID,
				Source:     model.RequestSourceVisitor,
				VisitorID:  "visitor-2",
				Page:       model.PageProductList,
				SourceIP:   simulation.VisitorClientProfile(42, "visitor-2").SourceIP,
				UserAgent:  simulation.VisitorClientProfile(42, "visitor-2").UserAgent,
				RegionCode: simulation.VisitorClientProfile(42, "visitor-2").RegionCode,
				LoadUnits:  60,
				StartedAt:  worldStartsAt,
			},
			events.FirewallRequestEvaluated{RequestID: rejectedRequestID,
				SourceIP:   simulation.VisitorClientProfile(42, "visitor-2").SourceIP,
				UserAgent:  simulation.VisitorClientProfile(42, "visitor-2").UserAgent,
				RegionCode: simulation.VisitorClientProfile(42, "visitor-2").RegionCode,
				Action:     model.FirewallAllow, EvaluatedAt: worldStartsAt},
			events.PageRequestRejected{
				RequestID:  rejectedRequestID,
				StatusCode: 500,
				ErrorCode:  model.FailureServerCapacityExceeded,
				Message:    "server capacity exceeded: required=60 available=40",
				RejectedAt: worldStartsAt,
			},
		),
		s.Logs.Exactly(
			capacityLog(acceptedRequestID, "visitor-1", worldStartsAt, 200, "", ""),
			capacityLog(
				rejectedRequestID,
				"visitor-2",
				worldStartsAt,
				500,
				model.FailureServerCapacityExceeded,
				"server capacity exceeded: required=60 available=40",
			),
		),
	)
}

func TestCapacityIsAvailableAfterRequestHoldDuration(t *testing.T) {
	s := newCapacityScenario(t, "run-capacity-released", capacityServerUnits)
	firstRequestID := model.RequestID("request-using-all-capacity")
	afterReleaseRequestID := model.RequestID("request-after-capacity-release")

	s.When(
		s.User.OpensPage(firstRequestID, "visitor-1", model.PageProductList, ""),
	)

	s.When(
		s.Time.Advance(0, capacityHoldDuration),
	)

	s.When(
		s.User.OpensPage(afterReleaseRequestID, "visitor-2", model.PageProductList, ""),
	)

	wantEvents := append(acceptedRequestEvents(afterReleaseRequestID, "visitor-2", capacityServerUnits, worldStartsAt.Add(capacityHoldDuration)),
		events.BackendAvailabilityChanged{Available: false, UnavailablePages: []model.PageType{model.PageProductList}, ChangedAt: worldStartsAt.Add(capacityHoldDuration)})
	s.Then(
		s.Events.Exactly(wantEvents...),
		s.Logs.Exactly(
			capacityLog(firstRequestID, "visitor-1", worldStartsAt, 200, "", ""),
			capacityLog(afterReleaseRequestID, "visitor-2", worldStartsAt.Add(capacityHoldDuration), 200, "", ""),
		),
	)
}

func newCapacityScenario(t *testing.T, runID string, pageLoadUnits int64) *spec.Scenario {
	t.Helper()
	s := spec.New(t, runID)
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.World.InfrastructureConfigured(serverProvisioningDuration),
		s.Page.Configured(model.PageProductList, pageLoadUnits, capacityHoldDuration),
		s.Server.Active(capacityServerID, capacityServerUnits, capacityCostPerHour),
	)
	return s
}

func acceptedRequestEvents(requestID model.RequestID, visitorID model.VisitorID, loadUnits int64, at time.Time) []events.Event {
	return acceptedRequestEventsOnServer(requestID, visitorID, loadUnits, at, capacityServerID)
}

func acceptedRequestEventsOnServer(requestID model.RequestID, visitorID model.VisitorID, loadUnits int64, at time.Time, serverID model.ServerID) []events.Event {
	profile := simulation.VisitorClientProfile(42, visitorID)
	return []events.Event{
		events.PageRequestStarted{
			RequestID:  requestID,
			Source:     model.RequestSourceVisitor,
			VisitorID:  visitorID,
			Page:       model.PageProductList,
			SourceIP:   profile.SourceIP,
			UserAgent:  profile.UserAgent,
			RegionCode: profile.RegionCode,
			LoadUnits:  loadUnits,
			StartedAt:  at,
		},
		events.FirewallRequestEvaluated{RequestID: requestID, SourceIP: profile.SourceIP, UserAgent: profile.UserAgent,
			RegionCode: profile.RegionCode, Action: model.FirewallAllow, EvaluatedAt: at},
		events.PageRequestAccepted{
			RequestID:  requestID,
			ServerID:   serverID,
			AcceptedAt: at,
			ReleasesAt: at.Add(capacityHoldDuration),
		},
		events.PageRequestCompleted{
			RequestID:   requestID,
			ServerID:    serverID,
			StatusCode:  200,
			CompletedAt: at,
		},
	}
}

func capacityLog(
	requestID model.RequestID,
	visitorID model.VisitorID,
	at time.Time,
	statusCode int,
	errorCode model.RequestFailureCode,
	message string,
) logs.Entry {
	profile := simulation.VisitorClientProfile(42, visitorID)
	return logs.Entry{
		Timestamp:  at,
		RequestID:  requestID,
		Source:     model.RequestSourceVisitor,
		VisitorID:  visitorID,
		Page:       model.PageProductList,
		SourceIP:   profile.SourceIP,
		UserAgent:  profile.UserAgent,
		RegionCode: profile.RegionCode,
		StatusCode: statusCode,
		ErrorCode:  errorCode,
		Message:    message,
	}
}
