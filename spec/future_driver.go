package spec

import (
	"context"
	"sort"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type projectionDriver struct{ query *simulation.QueryService }
type projectionInboxDriver struct{ store simulation.EventStore }

func newProjectionDriver(store simulation.EventStore) FutureDriver {
	return &projectionDriver{query: simulation.NewQueryService(store)}
}
func newProjectionInboxDriver(store simulation.EventStore) InboxFutureDriver {
	return &projectionInboxDriver{store: store}
}
func (d *projectionInboxDriver) InboxScenario(runID string, input InboxScenarioInput) (InboxScenarioResult, error) {
	ctx := context.Background()
	records, err := d.store.Load(ctx, runID)
	if err != nil {
		return InboxScenarioResult{}, err
	}
	state, err := simulation.Rehydrate(records)
	if err != nil {
		return InboxScenarioResult{}, err
	}
	messages := append([]InboxMessageView(nil), input.Messages...)
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].SentAt.Equal(messages[j].SentAt) {
			return messages[i].MessageID < messages[j].MessageID
		}
		return messages[i].SentAt.Before(messages[j].SentAt)
	})
	schedule := make(events.EventSchedule, 0, len(messages))
	for index, message := range messages {
		delivered := events.InboxMessageDelivered{MessageID: model.MessageID(message.MessageID), SenderEmail: message.SenderEmail,
			SentAt: message.SentAt, Subject: message.Subject, Description: message.Description}
		if message.SentAt.Equal(state.Clock.CurrentTime) {
			appended, appendErr := d.store.Append(ctx, runID, state.Version, []events.Event{delivered})
			if appendErr != nil {
				return InboxScenarioResult{}, appendErr
			}
			state, err = simulation.Rehydrate(append(records, appended...))
			if err != nil {
				return InboxScenarioResult{}, err
			}
			records = append(records, appended...)
			continue
		}
		schedule = append(schedule, events.ScheduledWorldEvent{Sequence: uint64(index + 1), OccursAt: message.SentAt, Event: delivered})
	}
	if len(schedule) > 0 {
		for index := range schedule {
			schedule[index].Sequence = uint64(index + 1)
		}
		appended, appendErr := d.store.Append(ctx, runID, state.Version, []events.Event{
			events.WorldScheduleCreated{Schedule: schedule, CreatedAt: state.Clock.StartedAt},
		})
		if appendErr != nil {
			return InboxScenarioResult{}, appendErr
		}
		records = append(records, appended...)
	}
	session, err := simulation.OpenRunSession(ctx, d.store, runID)
	if err != nil {
		return InboxScenarioResult{}, err
	}
	result := InboxScenarioResult{Reads: make([][]InboxPageView, 0, len(input.ReadTimes))}
	for readIndex, readAt := range input.ReadTimes {
		current := session.State().Clock.CurrentTime
		if readAt.After(current) {
			if _, err := session.Execute(ctx, simulation.AdvanceTime{CommandID: model.CommandID("inbox-read-" + time.Duration(readIndex).String()), RequestedDuration: readAt.Sub(current)}); err != nil {
				return InboxScenarioResult{}, err
			}
		}
		pages := make([]InboxPageView, 0)
		cursor := ""
		for {
			view, err := session.Projection().Inbox(simulation.InboxQuery{Cursor: cursor, Limit: input.Limit})
			if err != nil {
				return InboxScenarioResult{}, err
			}
			page := InboxPageView{HasNext: view.NextCursor != "", Messages: make([]InboxMessageView, 0, len(view.Messages))}
			for _, message := range view.Messages {
				page.Messages = append(page.Messages, InboxMessageView{MessageID: string(message.MessageID), SenderEmail: message.SenderEmail,
					SentAt: message.SentAt, Subject: message.Subject, Description: message.Description})
			}
			if len(page.Messages) == 0 {
				page.Messages = nil
			}
			pages = append(pages, page)
			if view.NextCursor == "" {
				break
			}
			cursor = view.NextCursor
		}
		result.Reads = append(result.Reads, pages)
	}
	return result, nil
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
		RunStatus: v.RunStatus, SiteStatus: v.SiteStatus,
		ServerCount: v.ServerCount, CapacityUtilization: v.CapacityUtilization, ErrorRate: v.ErrorRate,
		VisitorRequestsTotal: v.VisitorRequestsTotal, VisitorErrorRate: v.VisitorErrorRate, Uptime: v.Uptime}, e
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
	out := MetricSnapshotView{ServerCount: v.ServerCount, CapacityUnits: v.CapacityUnits, UsedLoadUnits: v.UsedLoadUnits, ActiveRequests: v.ActiveRequests,
		Responses200: v.Responses200, Responses403: v.Responses403, Responses500: v.Responses500, Responses503: v.Responses503,
		ErrorRate: v.ErrorRate, LatencyP50: v.LatencyP50, LatencyP95: v.LatencyP95, ServerCostMinor: v.ServerCostMinor}
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
	out := ResourcesView{ActiveInstances: v.ActiveInstances, TotalCapacityUnits: v.TotalCapacityUnits, UsedLoadUnits: v.UsedLoadUnits, TotalCostPerHourMinor: v.TotalCostPerHourMinor}
	for _, s := range v.Servers {
		out.Servers = append(out.Servers, ServerResourceView{ServerID: s.ServerID, Status: s.Status,
			CapacityUnits: s.CapacityUnits, UsedLoadUnits: s.UsedLoadUnits, CostPerHourMinor: s.CostPerHourMinor})
	}
	if len(out.Servers) == 0 {
		out.Servers = nil
	}
	return out, nil
}
func (d *projectionDriver) Operation(id string, operationID model.OperationID) (OperationView, error) {
	v, e := d.query.Operation(id, operationID)
	return OperationView(v), e
}
