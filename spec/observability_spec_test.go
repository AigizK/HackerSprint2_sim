package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestOverviewIsDerivedFromStateAndRequestHistory(t *testing.T) {
	s := newCapacityScenario(t, "run-overview", 60)
	s.When(
		s.User.OpensPage("overview-success", "visitor-1", model.PageProductList, ""),
		s.User.OpensPage("overview-error", "visitor-2", model.PageProductList, ""),
	)

	s.Then(s.Future.Overview(spec.OverviewView{
		SimulationTime:       worldStartsAt,
		SimulationEndsAt:     worldStartsAt.Add(24 * time.Hour),
		Remaining:            24 * time.Hour,
		RunStatus:            "running",
		SiteStatus:           "unavailable",
		ServerCount:          1,
		CapacityUtilization:  0.6,
		ErrorRate:            0.5,
		VisitorRequestsTotal: 2,
		VisitorErrorRate:     0.5,
		Uptime:               1,
	}))
}

func TestOverviewForCompletedRunHasNoRemainingTime(t *testing.T) {
	s := spec.New(t, "run-overview-completed")
	s.Given(s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))
	s.When(s.Time.Advance(0, 24*time.Hour))

	s.Then(s.Future.Overview(spec.OverviewView{
		SimulationTime:   worldStartsAt.Add(24 * time.Hour),
		SimulationEndsAt: worldStartsAt.Add(24 * time.Hour),
		RunStatus:        "completed",
		SiteStatus:       "unavailable",
		Uptime:           1,
	}))
}

func TestMetricsSnapshotUsesStateAndAccumulatedRequestEvents(t *testing.T) {
	s := newCapacityScenario(t, "run-metrics-snapshot", 60)
	s.When(
		s.User.OpensPage("metrics-success", "visitor-1", model.PageProductList, ""),
		s.User.OpensPage("metrics-error", "visitor-2", model.PageProductList, ""),
	)

	s.Then(s.Future.Metrics(spec.MetricsQuery{}, spec.MetricsView{Current: spec.MetricSnapshotView{
		ServerCount:    1,
		CapacityUnits:  100,
		UsedLoadUnits:  60,
		ActiveRequests: 1,
		Responses200:   1,
		Responses500:   1,
		ErrorRate:      0.5,
		ByPage: []spec.PageMetricView{{
			Page: model.PageProductList, ActiveRequests: 1, UsedLoadUnits: 60,
			Responses200: 1, Responses500: 1, ErrorRate: 0.5,
		}},
	}}))
}

func TestMetricsBuildTimeBucketsAndLatencyPercentiles(t *testing.T) {
	s := spec.New(t, "run-metrics-series")
	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.User.RecordedResponse("latency-1", "visitor-1", model.PageProductList, 200, 100*time.Millisecond),
		s.User.RecordedResponse("latency-2", "visitor-2", model.PageProductList, 500, 900*time.Millisecond),
	)

	s.Then(s.Future.Metrics(
		spec.MetricsQuery{
			From: worldStartsAt, To: worldStartsAt.Add(time.Minute), Step: time.Minute,
			Names: []string{"responses_200", "responses_500", "latency_p50_ms", "latency_p95_ms"},
			Page:  model.PageProductList,
		},
		spec.MetricsView{
			Current: spec.MetricSnapshotView{
				Responses200: 1, Responses500: 1, ErrorRate: 0.5,
				LatencyP50: 100 * time.Millisecond, LatencyP95: 900 * time.Millisecond,
			},
			Series: []spec.MetricPointView{
				{Timestamp: worldStartsAt, Name: "latency_p50_ms", Page: model.PageProductList, Value: 100},
				{Timestamp: worldStartsAt, Name: "latency_p95_ms", Page: model.PageProductList, Value: 900},
				{Timestamp: worldStartsAt, Name: "responses_200", Page: model.PageProductList, Value: 1},
				{Timestamp: worldStartsAt, Name: "responses_500", Page: model.PageProductList, Value: 1},
			},
		},
	))
}

func TestMetricsBuildHistoricalInfrastructureAndLoadGauges(t *testing.T) {
	s := newCapacityScenario(t, "run-metrics-gauge-history", 60)
	s.When(
		s.User.OpensPage("gauge-load", "visitor-1", model.PageProductList, ""),
		s.Time.Advance(0, 5*time.Minute),
		s.Server.Add("gauge-scale", "gauge-operation", secondServerID, 100, 1_000),
		s.Time.Advance(0, 5*time.Minute),
	)

	s.Then(s.Future.Metrics(
		spec.MetricsQuery{
			From: worldStartsAt, To: worldStartsAt.Add(10 * time.Minute), Step: 5 * time.Minute,
			Names: []string{"server_count", "capacity_units", "used_load_units", "capacity_utilization", "active_requests"},
		},
		spec.MetricsView{
			Current: spec.MetricSnapshotView{
				ServerCount: 2, CapacityUnits: 200, Responses200: 1, ServerCostMinor: 1_000,
				ByPage: []spec.PageMetricView{{Page: model.PageProductList, Responses200: 1}},
			},
			Series: []spec.MetricPointView{
				{Timestamp: worldStartsAt, Name: "active_requests", Value: 1},
				{Timestamp: worldStartsAt, Name: "capacity_units", Value: 100},
				{Timestamp: worldStartsAt, Name: "capacity_utilization", Value: 0.6},
				{Timestamp: worldStartsAt, Name: "server_count", Value: 1},
				{Timestamp: worldStartsAt, Name: "used_load_units", Value: 60},
				{Timestamp: worldStartsAt.Add(5 * time.Minute), Name: "active_requests", Value: 1},
				{Timestamp: worldStartsAt.Add(5 * time.Minute), Name: "capacity_units", Value: 100},
				{Timestamp: worldStartsAt.Add(5 * time.Minute), Name: "capacity_utilization", Value: 0.6},
				{Timestamp: worldStartsAt.Add(5 * time.Minute), Name: "server_count", Value: 1},
				{Timestamp: worldStartsAt.Add(5 * time.Minute), Name: "used_load_units", Value: 60},
				{Timestamp: worldStartsAt.Add(10 * time.Minute), Name: "active_requests", Value: 0},
				{Timestamp: worldStartsAt.Add(10 * time.Minute), Name: "capacity_units", Value: 200},
				{Timestamp: worldStartsAt.Add(10 * time.Minute), Name: "capacity_utilization", Value: 0},
				{Timestamp: worldStartsAt.Add(10 * time.Minute), Name: "server_count", Value: 2},
				{Timestamp: worldStartsAt.Add(10 * time.Minute), Name: "used_load_units", Value: 0},
			},
		},
	))
}

func TestMetricsBuildCumulativeUptimeAndCostSeries(t *testing.T) {
	s := newCapacityScenario(t, "run-metrics-slo-cost-history", capacityServerUnits)
	s.When(
		s.User.OpensPage("slo-load", "visitor-1", model.PageProductList, ""),
		s.Time.Advance(capacityHoldDuration, 0),
		s.Time.Advance(capacityHoldDuration, 0),
	)

	s.Then(s.Future.Metrics(
		spec.MetricsQuery{From: worldStartsAt, To: worldStartsAt.Add(2 * capacityHoldDuration), Step: capacityHoldDuration,
			Names: []string{"downtime_seconds", "uptime_ratio", "server_cost_minor", "total_cost_minor"}},
		spec.MetricsView{
			Current: spec.MetricSnapshotView{ServerCount: 1, CapacityUnits: capacityServerUnits,
				Responses200: 1, ServerCostMinor: capacityCostPerHour,
				ByPage: []spec.PageMetricView{{Page: model.PageProductList, Responses200: 1}}},
			Series: []spec.MetricPointView{
				{Timestamp: worldStartsAt, Name: "downtime_seconds", Value: 0},
				{Timestamp: worldStartsAt, Name: "server_cost_minor", Value: 0},
				{Timestamp: worldStartsAt, Name: "total_cost_minor", Value: 0},
				{Timestamp: worldStartsAt.Add(capacityHoldDuration), Name: "downtime_seconds", Value: capacityHoldDuration.Seconds()},
				{Timestamp: worldStartsAt.Add(capacityHoldDuration), Name: "server_cost_minor", Value: float64(capacityCostPerHour)},
				{Timestamp: worldStartsAt.Add(capacityHoldDuration), Name: "total_cost_minor", Value: float64(capacityCostPerHour)},
				{Timestamp: worldStartsAt.Add(capacityHoldDuration), Name: "uptime_ratio", Value: 0},
				{Timestamp: worldStartsAt.Add(2 * capacityHoldDuration), Name: "downtime_seconds", Value: capacityHoldDuration.Seconds()},
				{Timestamp: worldStartsAt.Add(2 * capacityHoldDuration), Name: "server_cost_minor", Value: float64(capacityCostPerHour)},
				{Timestamp: worldStartsAt.Add(2 * capacityHoldDuration), Name: "total_cost_minor", Value: float64(capacityCostPerHour)},
				{Timestamp: worldStartsAt.Add(2 * capacityHoldDuration), Name: "uptime_ratio", Value: 0.5},
			},
		},
	))
}

func TestLogsSupportEnrichedFieldsFiltersAndCursorPagination(t *testing.T) {
	s := newCapacityScenario(t, "run-logs-query", 60)
	s.When(
		s.User.OpensPage("logs-success", "visitor-1", model.PageProductList, ""),
		s.User.OpensPage("logs-error", "visitor-2", model.PageProductList, ""),
	)

	s.Then(s.Future.Logs(
		spec.LogsQuery{
			From: worldStartsAt, To: worldStartsAt, Page: model.PageProductList,
			StatusCode: 200, Limit: 1,
		},
		spec.LogsView{
			Logs: []spec.RequestLogView{{
				Entry: logs.Entry{
					Timestamp: worldStartsAt, RequestID: "logs-success", Source: model.RequestSourceVisitor,
					VisitorID: "visitor-1", Page: model.PageProductList,
					SourceIP:   simulation.VisitorClientProfile(42, "visitor-1").SourceIP,
					UserAgent:  simulation.VisitorClientProfile(42, "visitor-1").UserAgent,
					RegionCode: simulation.VisitorClientProfile(42, "visitor-1").RegionCode, StatusCode: 200,
				},
				LoadUnits: 60, ServerID: capacityServerID,
			}},
			NextCursor: "opaque-next-cursor",
		},
	))

	s.Then(s.Future.Logs(
		spec.LogsQuery{
			From: worldStartsAt, To: worldStartsAt, Page: model.PageProductList,
			StatusCode: 500, Cursor: "opaque-next-cursor", Limit: 1,
		},
		spec.LogsView{Logs: []spec.RequestLogView{{
			Entry: logs.Entry{
				Timestamp: worldStartsAt, RequestID: "logs-error", Source: model.RequestSourceVisitor,
				VisitorID: "visitor-2", Page: model.PageProductList,
				SourceIP:   simulation.VisitorClientProfile(42, "visitor-2").SourceIP,
				UserAgent:  simulation.VisitorClientProfile(42, "visitor-2").UserAgent,
				RegionCode: simulation.VisitorClientProfile(42, "visitor-2").RegionCode, StatusCode: 500,
				ErrorCode: model.FailureServerCapacityExceeded,
				Message:   "server capacity exceeded: required=60 available=40",
			},
			LoadUnits: 60,
		}}},
	))
}

func TestResourcesAreDerivedFromServerAndAllocationState(t *testing.T) {
	s := newCapacityScenario(t, "run-resources", 60)
	s.When(s.User.OpensPage("resources-request", "visitor-1", model.PageProductList, ""))
	s.When(s.Server.Add("resources-add", "resources-operation", secondServerID, 100, 1_000))

	s.Then(s.Future.Resources(spec.ResourcesView{
		ActiveInstances:       1,
		TotalCapacityUnits:    100,
		UsedLoadUnits:         60,
		TotalCostPerHourMinor: 1_000,
		Servers: []spec.ServerResourceView{
			{ServerID: capacityServerID, Status: model.ServerActive, CapacityUnits: 100, UsedLoadUnits: 60, CostPerHourMinor: 1_000},
			{ServerID: secondServerID, Status: model.ServerProvisioning, CapacityUnits: 100, CostPerHourMinor: 1_000},
		},
	}))
}

func TestResourcesExcludeExpiredAllocationsAndDrainingCapacity(t *testing.T) {
	t.Run("expired allocation", func(t *testing.T) {
		s := newCapacityScenario(t, "run-resources-expired", 100)
		s.When(s.User.OpensPage("resource-expiring", "visitor-1", model.PageProductList, ""))
		s.When(s.Time.Advance(0, capacityHoldDuration))
		s.Then(s.Future.Resources(spec.ResourcesView{
			ActiveInstances: 1, TotalCapacityUnits: 100,
			UsedLoadUnits: 0, TotalCostPerHourMinor: 1_000,
			Servers: []spec.ServerResourceView{{
				ServerID: capacityServerID, Status: model.ServerActive,
				CapacityUnits: 100, UsedLoadUnits: 0, CostPerHourMinor: 1_000,
			}},
		}))
	})

	t.Run("draining server", func(t *testing.T) {
		s := newCapacityScenario(t, "run-resources-draining", 100)
		s.Given(s.Server.Active(secondServerID, 100, 1_000))
		s.When(s.User.OpensPage("resource-draining", "visitor-1", model.PageProductList, ""))
		s.When(s.Server.Remove("remove-draining", "remove-draining-operation", capacityServerID))
		s.Then(s.Future.Resources(spec.ResourcesView{
			ActiveInstances: 1, TotalCapacityUnits: 100,
			UsedLoadUnits: 100, TotalCostPerHourMinor: 2_000,
			Servers: []spec.ServerResourceView{
				{ServerID: capacityServerID, Status: model.ServerDraining, CapacityUnits: 100, UsedLoadUnits: 100, CostPerHourMinor: 1_000},
				{ServerID: secondServerID, Status: model.ServerActive, CapacityUnits: 100, CostPerHourMinor: 1_000},
			},
		}))
	})
}
