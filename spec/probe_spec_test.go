package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestProbeCreatesNormalLoadAndProbeLog(t *testing.T) {
	s := newCapacityScenario(t, "run-probe-load", 60)
	s.When(s.User.ProbesPage("probe-list", model.PageProductList, ""))

	s.Then(
		s.Events.Exactly(
			events.PageRequestStarted{
				RequestID: "probe-list", Source: model.RequestSourceProbe,
				Page: model.PageProductList, LoadUnits: 60, StartedAt: worldStartsAt,
			},
			events.PageRequestAccepted{
				RequestID: "probe-list", ServerID: capacityServerID,
				AcceptedAt: worldStartsAt, ReleasesAt: worldStartsAt.Add(capacityHoldDuration),
			},
			events.PageRequestCompleted{
				RequestID: "probe-list", ServerID: capacityServerID, StatusCode: 200, CompletedAt: worldStartsAt,
			},
		),
		s.Future.Logs(spec.LogsQuery{}, spec.LogsView{Logs: []spec.RequestLogView{{
			Entry:     logsEntryForProbe("probe-list", model.PageProductList, 200, ""),
			LoadUnits: 60, ServerID: capacityServerID,
		}}}),
	)
}

func TestProbeObservesCapacityBugAndDeploymentErrors(t *testing.T) {
	t.Run("capacity", func(t *testing.T) {
		s := newCapacityScenario(t, "run-probe-capacity-error", 100)
		s.When(s.User.OpensPage("visitor-using-capacity", "visitor-1", model.PageProductList, ""))
		s.When(s.User.ProbesPage("probe-capacity-error", model.PageProductList, ""))
		probeError := logsEntryForProbe("probe-capacity-error", model.PageProductList, 500, model.FailureServerCapacityExceeded)
		probeError.Message = "server capacity exceeded: required=100 available=0"
		s.Then(s.Future.Logs(spec.LogsQuery{}, spec.LogsView{Logs: []spec.RequestLogView{
			{Entry: logs.Entry{Timestamp: worldStartsAt, RequestID: "visitor-using-capacity", Source: model.RequestSourceVisitor,
				VisitorID: "visitor-1", Page: model.PageProductList, StatusCode: 200}, LoadUnits: 100, ServerID: capacityServerID},
			{Entry: probeError, LoadUnits: 100},
		}}))
	})

	t.Run("bug", func(t *testing.T) {
		s := spec.New(t, "run-probe-bug-error")
		s.Given(
			s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
			s.Product.Added("product-1", "Coffee machine", 12_990, 850_000, 200_000),
			s.Bug.Activated(pageBugID, model.PageProduct, "product-1", 1_000_000, correctFixText),
		)
		s.When(s.User.ProbesPage("probe-bug-error", model.PageProduct, "product-1"))
		expected := logsEntryForProbe("probe-bug-error", model.PageProduct, 500, model.FailurePageBug)
		expected.ProductID = "product-1"
		expected.Message = "чтоб этот баг пропал полностью, надо сделать фикс с текстом " + correctFixText
		s.Then(s.Future.Logs(spec.LogsQuery{}, spec.LogsView{Logs: []spec.RequestLogView{{Entry: expected}}}))
	})

	t.Run("deployment", func(t *testing.T) {
		s := newDeploymentScenario(t, "run-probe-deployment-error", 50, 0)
		s.When(s.Deployment.Start("probe-deployment", firstDeploymentID, "probe-deployment-operation"))
		s.When(s.User.ProbesPage("probe-deployment-error", model.PageProductList, ""))
		expected := logsEntryForProbe("probe-deployment-error", model.PageProductList, 500, model.FailureDeployment)
		expected.Message = "deployment in progress: " + string(firstDeploymentID)
		s.Then(s.Future.Logs(spec.LogsQuery{}, spec.LogsView{Logs: []spec.RequestLogView{{Entry: expected, LoadUnits: 50}}}))
	})
}

func TestPurchaseProbeDoesNotCreatePurchaseOrRevenue(t *testing.T) {
	s := spec.New(t, "run-purchase-probe")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 850_000, 200_000),
	)

	s.When(s.User.ProbesPage("probe-purchase", model.PagePurchase, "product-1"))
	s.Then(
		s.Events.HasNoType("ProductPurchased"),
		s.State.Economy(0, 0),
	)
}

func logsEntryForProbe(
	requestID model.RequestID,
	page model.PageType,
	status int,
	errorCode model.RequestFailureCode,
) logs.Entry {
	return logs.Entry{
		Timestamp: worldStartsAt, RequestID: requestID, Source: model.RequestSourceProbe,
		Page: page, StatusCode: status, ErrorCode: errorCode,
	}
}
