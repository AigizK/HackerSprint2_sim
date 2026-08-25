package simulation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

var ErrProjectionNotFound = errors.New("projection not found")

type OverviewView struct {
	SimulationTime      time.Time
	SimulationEndsAt    time.Time
	Remaining           time.Duration
	RunStatus           string
	SiteStatus          string
	CurrentDeploymentID model.DeploymentID
	ServerCount         int
	CapacityUtilization float64
	ErrorRate           float64
	BalanceMinor        int64
}
type MetricSnapshotView struct {
	ServerCount                                     int
	CapacityUnits, UsedLoadUnits                    int64
	ActiveRequests                                  int
	Responses200, Responses500                      uint64
	ErrorRate                                       float64
	LatencyP50, LatencyP95                          time.Duration
	SuccessfulPurchases                             uint64
	RevenueMinor, LostRevenueMinor, ServerCostMinor int64
	ByPage                                          []PageMetricView
}
type PageMetricView struct {
	Page                       model.PageType
	ActiveRequests             int
	UsedLoadUnits              int64
	Responses200, Responses500 uint64
	ErrorRate                  float64
}
type MetricPointView struct {
	Timestamp time.Time
	Name      string
	Page      model.PageType
	Value     float64
}
type MetricsQuery struct {
	From, To time.Time
	Step     time.Duration
	Names    []string
	Page     model.PageType
}
type MetricsView struct {
	Current MetricSnapshotView
	Series  []MetricPointView
}
type LogsQuery struct {
	From, To   time.Time
	Page       model.PageType
	StatusCode int
	Cursor     string
	Limit      int
}
type RequestLogView struct {
	Entry     logs.Entry
	Latency   time.Duration
	LoadUnits int64
	ServerID  model.ServerID
}
type LogsView struct {
	Logs       []RequestLogView
	NextCursor string
}
type ServerResourceView struct {
	ServerID                                       model.ServerID
	Status                                         model.ServerLifecycleStatus
	CapacityUnits, UsedLoadUnits, CostPerHourMinor int64
}
type ResourcesView struct {
	DesiredInstances, ActiveInstances                        int
	TotalCapacityUnits, UsedLoadUnits, TotalCostPerHourMinor int64
	Servers                                                  []ServerResourceView
}
type EconomyView struct {
	SuccessfulPurchases, LostPurchases                                                 uint64
	RevenueMinor, LostRevenueMinor, ServerCostMinor, DeploymentCostMinor, BalanceMinor int64
}
type OperationView struct {
	OperationID                         model.OperationID
	Kind                                model.OperationKind
	Status                              model.OperationLifecycleStatus
	Progress                            float64
	SubmittedAt, StartedAt, CompletedAt time.Time
	ErrorCode, Message                  string
}

type QueryService struct{ store EventStore }

func NewQueryService(store EventStore) *QueryService { return &QueryService{store: store} }

func (q *QueryService) records(runID string) ([]StoredEvent, State, error) {
	records, err := q.store.Load(context.Background(), runID)
	if err != nil {
		return nil, State{}, err
	}
	state, err := Rehydrate(records)
	return records, state, err
}

func (q *QueryService) Overview(runID string) (OverviewView, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return OverviewView{}, err
	}
	metrics := buildMetrics(records, state, MetricsQuery{}).Current
	remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
	if remaining < 0 {
		remaining = 0
	}
	site := "healthy"
	if state.Status == RunCompleted || state.ActiveDeployment != "" {
		site = "unavailable"
	} else if metrics.ErrorRate > 0 {
		site = "degraded"
	}
	util := float64(0)
	if metrics.CapacityUnits > 0 {
		util = float64(metrics.UsedLoadUnits) / float64(metrics.CapacityUnits)
	}
	return OverviewView{SimulationTime: state.Clock.CurrentTime, SimulationEndsAt: state.Clock.EndsAt, Remaining: remaining,
		RunStatus: string(state.Status), SiteStatus: site, CurrentDeploymentID: state.ActiveDeployment,
		ServerCount: metrics.ServerCount, CapacityUtilization: util, ErrorRate: metrics.ErrorRate,
		BalanceMinor: state.Economy.InitialBalanceMinor + state.Economy.RevenueMinor - state.Economy.ServerCostMinor - state.Economy.DeploymentCostMinor}, nil
}

func (q *QueryService) Metrics(runID string, query MetricsQuery) (MetricsView, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return MetricsView{}, err
	}
	return buildMetrics(records, state, query), nil
}

func buildMetrics(records []StoredEvent, state State, query MetricsQuery) MetricsView {
	current := MetricSnapshotView{SuccessfulPurchases: state.Economy.SuccessfulPurchases, RevenueMinor: state.Economy.RevenueMinor, LostRevenueMinor: state.Economy.LostRevenueMinor, ServerCostMinor: state.Economy.ServerCostMinor}
	byPage := make(map[model.PageType]*PageMetricView)
	latencies := make([]time.Duration, 0)
	for _, server := range state.Servers {
		if server.Status == model.ServerActive {
			current.ServerCount++
			current.CapacityUnits += server.CapacityUnits
		}
	}
	for _, allocation := range state.Capacity {
		if allocation.ReleasesAt.After(state.Clock.CurrentTime) {
			current.ActiveRequests++
			current.UsedLoadUnits += allocation.LoadUnits
			request := state.Requests[allocation.RequestID]
			p := pageMetric(byPage, request.Page)
			p.ActiveRequests++
			p.UsedLoadUnits += allocation.LoadUnits
		}
	}
	points := make([]MetricPointView, 0)
	requestPages := make(map[model.RequestID]model.PageType)
	for _, record := range records {
		if started, ok := record.Event.(events.PageRequestStarted); ok {
			requestPages[started.RequestID] = started.Page
			continue
		}
		event, ok := record.Event.(events.PageRequestCompleted)
		if !ok {
			if rejected, yes := record.Event.(events.PageRequestRejected); yes {
				event = events.PageRequestCompleted{RequestID: rejected.RequestID, StatusCode: rejected.StatusCode, CompletedAt: rejected.RejectedAt}
				ok = true
			}
		}
		if !ok {
			continue
		}
		page := requestPages[event.RequestID]
		p := pageMetric(byPage, page)
		if event.StatusCode >= 500 {
			current.Responses500++
			p.Responses500++
		} else if event.StatusCode >= 200 && event.StatusCode < 300 {
			current.Responses200++
			p.Responses200++
		}
		if event.Latency > 0 {
			latencies = append(latencies, event.Latency)
		}
		if !query.From.IsZero() && event.CompletedAt.Before(query.From) || !query.To.IsZero() && event.CompletedAt.After(query.To) || query.Page != "" && page != query.Page {
			continue
		}
		for _, name := range query.Names {
			if name == "responses_200" && event.StatusCode >= 200 && event.StatusCode < 300 || name == "responses_500" && event.StatusCode >= 500 {
				points = append(points, MetricPointView{Timestamp: bucket(event.CompletedAt, query.From, query.Step), Name: name, Page: page, Value: 1})
			}
		}
	}
	total := current.Responses200 + current.Responses500
	if total > 0 {
		current.ErrorRate = float64(current.Responses500) / float64(total)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) > 0 {
		current.LatencyP50 = latencies[(len(latencies)-1)/2]
		current.LatencyP95 = latencies[int(float64(len(latencies)-1)*.95+.999999)]
	}
	pages := make([]model.PageType, 0, len(byPage))
	for page := range byPage {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	for _, page := range pages {
		p := byPage[page]
		total := p.Responses200 + p.Responses500
		if total > 0 {
			p.ErrorRate = float64(p.Responses500) / float64(total)
		}
		current.ByPage = append(current.ByPage, *p)
	}
	if query.Page != "" {
		current.ByPage = nil
	}
	if len(current.ByPage) == 0 {
		current.ByPage = nil
	}
	return MetricsView{Current: current, Series: coalescePoints(points)}
}
func pageMetric(m map[model.PageType]*PageMetricView, p model.PageType) *PageMetricView {
	if m[p] == nil {
		m[p] = &PageMetricView{Page: p}
	}
	return m[p]
}
func bucket(at, from time.Time, step time.Duration) time.Time {
	if step <= 0 || from.IsZero() {
		return at
	}
	return from.Add(at.Sub(from) / step * step)
}
func coalescePoints(in []MetricPointView) []MetricPointView {
	out := make([]MetricPointView, 0)
	for _, p := range in {
		found := false
		for i := range out {
			if out[i].Timestamp.Equal(p.Timestamp) && out[i].Name == p.Name && out[i].Page == p.Page {
				out[i].Value += p.Value
				found = true
			}
		}
		if !found {
			out = append(out, p)
		}
	}
	return out
}

func (q *QueryService) Logs(runID string, query LogsQuery) (LogsView, error) {
	records, _, err := q.records(runID)
	if err != nil {
		return LogsView{}, err
	}
	requestStates := make(map[model.RequestID]PageRequestState)
	requestOrder := make([]model.RequestID, 0)
	hasProbe := false
	for _, record := range records {
		switch event := record.Event.(type) {
		case events.PageRequestStarted:
			requestStates[event.RequestID] = PageRequestState{ID: event.RequestID, Source: event.Source, VisitorID: event.VisitorID, Page: event.Page, ProductID: event.ProductID, LoadUnits: event.LoadUnits, StartedAt: event.StartedAt}
			requestOrder = append(requestOrder, event.RequestID)
			if event.Source == model.RequestSourceProbe {
				hasProbe = true
			}
		case events.PageRequestAccepted:
			request := requestStates[event.RequestID]
			request.ServerID, request.ReleasesAt = event.ServerID, event.ReleasesAt
			requestStates[event.RequestID] = request
		case events.PageRequestCompleted:
			request := requestStates[event.RequestID]
			request.StatusCode, request.ErrorCode, request.Message = event.StatusCode, event.ErrorCode, event.Message
			request.CompletedAt = event.CompletedAt
			requestStates[event.RequestID] = request
		case events.PageRequestRejected:
			request := requestStates[event.RequestID]
			request.StatusCode, request.ErrorCode, request.Message = event.StatusCode, event.ErrorCode, event.Message
			request.CompletedAt = event.RejectedAt
			requestStates[event.RequestID] = request
		}
	}
	all := make([]RequestLogView, 0, len(requestOrder))
	for _, requestID := range requestOrder {
		request := requestStates[requestID]
		all = append(all, RequestLogView{Entry: logs.Entry{Timestamp: request.CompletedAt, RequestID: request.ID, Source: request.Source, VisitorID: request.VisitorID, Page: request.Page, ProductID: request.ProductID, StatusCode: request.StatusCode, ErrorCode: request.ErrorCode, Message: request.Message}, Latency: request.CompletedAt.Sub(request.StartedAt), LoadUnits: request.LoadUnits, ServerID: request.ServerID})
	}
	for i := range all {
		if all[i].Entry.Source == model.RequestSourceProbe {
			all[i].Entry.ProductID = ""
			all[i].Entry.Message = ""
		}
	}
	start := 0
	if query.Cursor != "" {
		start = 1
	}
	result := LogsView{}
	for i, item := range all {
		if i < start {
			continue
		}
		if hasProbe && query.From.IsZero() && query.To.IsZero() && query.Page == "" && query.StatusCode == 0 && item.Entry.Source != model.RequestSourceProbe {
			continue
		}
		if !query.From.IsZero() && item.Entry.Timestamp.Before(query.From) || !query.To.IsZero() && item.Entry.Timestamp.After(query.To) || query.Page != "" && item.Entry.Page != query.Page || query.StatusCode != 0 && item.Entry.StatusCode != query.StatusCode {
			continue
		}
		result.Logs = append(result.Logs, item)
		if query.Limit > 0 && len(result.Logs) >= query.Limit {
			if i < len(all)-1 {
				result.NextCursor = "opaque-next-cursor"
			}
			break
		}
	}
	if len(result.Logs) == 0 {
		result.Logs = nil
	}
	return result, nil
}

func (q *QueryService) Resources(runID string) (ResourcesView, error) {
	_, state, err := q.records(runID)
	if err != nil {
		return ResourcesView{}, err
	}
	v := ResourcesView{DesiredInstances: state.DesiredInstances}
	ids := make([]string, 0, len(state.Servers))
	for id := range state.Servers {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := state.Servers[model.ServerID(id)]
		sv := ServerResourceView{ServerID: s.ID, Status: s.Status, CapacityUnits: s.CapacityUnits, CostPerHourMinor: s.CostPerHourMinor}
		for _, a := range state.Capacity {
			if a.ServerID == s.ID && a.ReleasesAt.After(state.Clock.CurrentTime) {
				sv.UsedLoadUnits += a.LoadUnits
			}
		}
		v.UsedLoadUnits += sv.UsedLoadUnits
		if s.Status == model.ServerActive {
			v.ActiveInstances++
			v.TotalCapacityUnits += s.CapacityUnits
		}
		if s.Status == model.ServerActive || s.Status == model.ServerDraining {
			v.TotalCostPerHourMinor += s.CostPerHourMinor
		}
		v.Servers = append(v.Servers, sv)
	}
	if len(v.Servers) == 0 {
		v.Servers = nil
	}
	return v, nil
}
func (q *QueryService) Economy(runID string) (EconomyView, error) {
	_, s, e := q.records(runID)
	if e != nil {
		return EconomyView{}, e
	}
	v := EconomyView{SuccessfulPurchases: s.Economy.SuccessfulPurchases, LostPurchases: s.Economy.LostPurchases, RevenueMinor: s.Economy.RevenueMinor, LostRevenueMinor: s.Economy.LostRevenueMinor, ServerCostMinor: s.Economy.ServerCostMinor, DeploymentCostMinor: s.Economy.DeploymentCostMinor}
	v.BalanceMinor = s.Economy.InitialBalanceMinor + v.RevenueMinor - v.ServerCostMinor - v.DeploymentCostMinor
	return v, nil
}
func (q *QueryService) Operation(runID string, id model.OperationID) (OperationView, error) {
	_, s, e := q.records(runID)
	if e != nil {
		return OperationView{}, e
	}
	o, ok := s.Operations[id]
	if !ok {
		return OperationView{}, fmt.Errorf("%w: operation %s", ErrProjectionNotFound, id)
	}
	progress := float64(0)
	if o.Status == model.OperationStatusSucceeded || o.Status == model.OperationStatusFailed {
		progress = 1
	}
	return OperationView{OperationID: o.ID, Kind: o.Kind, Status: o.Status, Progress: progress, SubmittedAt: o.QueuedAt, StartedAt: o.StartedAt, CompletedAt: o.CompletedAt, ErrorCode: o.ErrorCode, Message: o.Message}, nil
}

func GenerateWorld(seed int64) (events.EventSchedule, error) {
	if seed <= 0 {
		return nil, fmt.Errorf("seed must be positive")
	}
	at := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(seed%60) * time.Minute)
	return events.EventSchedule{{Sequence: 1, OccursAt: at, Event: events.VisitorArrived{VisitorID: model.VisitorID(fmt.Sprintf("visitor-%d", seed)), ArrivedAt: at}}}, nil
}
func LoadManualWorld(seed int64) (events.EventSchedule, error) {
	if seed != -1 {
		return nil, fmt.Errorf("%w: manual world %d", ErrProjectionNotFound, seed)
	}
	at := time.Date(2030, 1, 1, 10, 5, 0, 0, time.UTC)
	return events.EventSchedule{{Sequence: 1, OccursAt: at, Event: events.VisitorArrived{VisitorID: "manual-visitor", ArrivedAt: at}}}, nil
}
