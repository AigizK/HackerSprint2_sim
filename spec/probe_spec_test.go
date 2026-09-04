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

func TestProbeCreatesNormalLoadAndProbeLog(t *testing.T) {
	s := newCapacityScenario(t, "run-probe-load", 60)
	profile := simulation.ProbeClientProfile(42)
	s.When(s.User.ProbesPage("probe-list", model.PageProductList, ""))

	s.Then(
		s.Events.Exactly(
			events.PageRequestStarted{
				RequestID: "probe-list", Source: model.RequestSourceProbe,
				Page: model.PageProductList, SourceIP: profile.SourceIP, UserAgent: profile.UserAgent,
				RegionCode: profile.RegionCode, LoadUnits: 60, StartedAt: worldStartsAt,
			},
			events.FirewallRequestEvaluated{RequestID: "probe-list", SourceIP: profile.SourceIP,
				UserAgent: profile.UserAgent, RegionCode: profile.RegionCode, Action: model.FirewallAllow, EvaluatedAt: worldStartsAt},
			events.PageRequestAccepted{
				RequestID: "probe-list", ServerID: capacityServerID,
				AcceptedAt: worldStartsAt, ReleasesAt: worldStartsAt.Add(capacityHoldDuration),
			},
			events.PageRequestCompleted{
				RequestID: "probe-list", ServerID: capacityServerID, StatusCode: 200, CompletedAt: worldStartsAt,
			},
			events.BackendAvailabilityChanged{Available: false,
				UnavailablePages: []model.PageType{model.PageProductList}, ChangedAt: worldStartsAt},
		),
		s.Future.Logs(spec.LogsQuery{}, spec.LogsView{Logs: []spec.RequestLogView{{
			Entry:     logsEntryForProbe("probe-list", model.PageProductList, 200, ""),
			LoadUnits: 60, ServerID: capacityServerID,
		}}}),
		s.Future.Overview(spec.OverviewView{
			SimulationTime: worldStartsAt, SimulationEndsAt: worldStartsAt.Add(24 * time.Hour), Remaining: 24 * time.Hour,
			RunStatus: "running", SiteStatus: "unavailable", ServerCount: 1, CapacityUtilization: .6, Uptime: 1,
		}),
	)
}

func logsEntryForProbe(
	requestID model.RequestID,
	page model.PageType,
	status int,
	errorCode model.RequestFailureCode,
) logs.Entry {
	profile := simulation.ProbeClientProfile(42)
	return logs.Entry{
		Timestamp: worldStartsAt, RequestID: requestID, Source: model.RequestSourceProbe,
		Page: page, SourceIP: profile.SourceIP, UserAgent: profile.UserAgent, RegionCode: profile.RegionCode,
		StatusCode: status, ErrorCode: errorCode,
	}
}
