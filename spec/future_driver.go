package spec

import (
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type projectionDriver struct{ query *simulation.QueryService }

func newProjectionDriver(store simulation.EventStore) FutureDriver {
	return &projectionDriver{query: simulation.NewQueryService(store)}
}
func (*projectionDriver) GenerateWorld(seed int64) (events.EventSchedule, error) {
	return simulation.GenerateWorld(seed)
}
func (*projectionDriver) LoadManualWorld(seed int64) (events.EventSchedule, error) {
	return simulation.LoadManualWorld(seed)
}
func (d *projectionDriver) Overview(id string) (OverviewView, error) {
	v, e := d.query.Overview(id)
	return OverviewView{SimulationTime: v.SimulationTime, SimulationEndsAt: v.SimulationEndsAt, Remaining: v.Remaining,
		RunStatus: v.RunStatus, SiteStatus: v.SiteStatus, CurrentDeploymentID: v.CurrentDeploymentID,
		ServerCount: v.ServerCount, CapacityUtilization: v.CapacityUtilization, ErrorRate: v.ErrorRate, BalanceMinor: v.BalanceMinor}, e
}
func (d *projectionDriver) Metrics(id string, q MetricsQuery) (MetricsView, error) {
	v, e := d.query.Metrics(id, simulation.MetricsQuery{From: q.From, To: q.To, Step: q.Step, Names: q.Names, Page: q.Page})
	if e != nil {
		return MetricsView{}, e
	}
	out := MetricsView{Current: metricSnapshot(v.Current)}
	for _, p := range v.Series {
		out.Series = append(out.Series, MetricPointView(p))
	}
	if len(out.Series) == 0 {
		out.Series = nil
	}
	return out, nil
}
func metricSnapshot(v simulation.MetricSnapshotView) MetricSnapshotView {
	out := MetricSnapshotView{ServerCount: v.ServerCount, CapacityUnits: v.CapacityUnits, UsedLoadUnits: v.UsedLoadUnits, ActiveRequests: v.ActiveRequests, Responses200: v.Responses200, Responses500: v.Responses500, ErrorRate: v.ErrorRate, LatencyP50: v.LatencyP50, LatencyP95: v.LatencyP95, SuccessfulPurchases: v.SuccessfulPurchases, RevenueMinor: v.RevenueMinor, LostRevenueMinor: v.LostRevenueMinor, ServerCostMinor: v.ServerCostMinor}
	for _, p := range v.ByPage {
		out.ByPage = append(out.ByPage, PageMetricView(p))
	}
	if len(out.ByPage) == 0 {
		out.ByPage = nil
	}
	return out
}
func (d *projectionDriver) Logs(id string, q LogsQuery) (LogsView, error) {
	v, e := d.query.Logs(id, simulation.LogsQuery{From: q.From, To: q.To, Page: q.Page, StatusCode: q.StatusCode, Cursor: q.Cursor, Limit: q.Limit})
	if e != nil {
		return LogsView{}, e
	}
	out := LogsView{NextCursor: v.NextCursor}
	for _, l := range v.Logs {
		out.Logs = append(out.Logs, RequestLogView{Entry: l.Entry, Latency: l.Latency, LoadUnits: l.LoadUnits, ServerID: l.ServerID})
	}
	if len(out.Logs) == 0 {
		out.Logs = nil
	}
	return out, nil
}
func (d *projectionDriver) Resources(id string) (ResourcesView, error) {
	v, e := d.query.Resources(id)
	if e != nil {
		return ResourcesView{}, e
	}
	out := ResourcesView{DesiredInstances: v.DesiredInstances, ActiveInstances: v.ActiveInstances, TotalCapacityUnits: v.TotalCapacityUnits, UsedLoadUnits: v.UsedLoadUnits, TotalCostPerHourMinor: v.TotalCostPerHourMinor}
	for _, s := range v.Servers {
		out.Servers = append(out.Servers, ServerResourceView(s))
	}
	if len(out.Servers) == 0 {
		out.Servers = nil
	}
	return out, nil
}
func (d *projectionDriver) Economy(id string) (EconomyView, error) {
	v, e := d.query.Economy(id)
	return EconomyView{SuccessfulPurchases: v.SuccessfulPurchases, LostPurchases: v.LostPurchases,
		RevenueMinor: v.RevenueMinor, LostRevenueMinor: v.LostRevenueMinor, ServerCostMinor: v.ServerCostMinor,
		DeploymentCostMinor: v.DeploymentCostMinor, BalanceMinor: v.BalanceMinor}, e
}
func (d *projectionDriver) Operation(id string, operationID model.OperationID) (OperationView, error) {
	v, e := d.query.Operation(id, operationID)
	return OperationView(v), e
}
