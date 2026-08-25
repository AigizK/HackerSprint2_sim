package simulation

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

var ErrProjectionNotFound = errors.New("projection not found")

type OverviewView struct {
	RunID               string
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
	SuccessfulPurchases uint64
	RevenueMinor        int64
	ServerCostMinor     int64
}
type MetricSnapshotView struct {
	ServerCount                                     int
	CapacityUnits, UsedLoadUnits                    int64
	ActiveRequests                                  int
	RequestsTotal                                   uint64
	CapacityUtilization                             float64
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
	Currency                                                                           string
	SuccessfulPurchases, LostPurchases                                                 uint64
	RevenueMinor, LostRevenueMinor, ServerCostMinor, DeploymentCostMinor, BalanceMinor int64
}
type DeploymentView struct {
	DeploymentID model.DeploymentID
	Sequence     int
	Name         string
	Description  string
	Status       model.DeploymentLifecycleStatus
	OperationID  model.OperationID
}
type DeploymentsView struct{ Deployments []DeploymentView }
type OperationView struct {
	OperationID                         model.OperationID
	Kind                                model.OperationKind
	Status                              model.OperationLifecycleStatus
	Progress                            float64
	SubmittedAt, StartedAt, CompletedAt time.Time
	ErrorCode, Message                  string
}

type QueryService struct{ store EventStore }

// Projection serves read models from an already-open RunSession without
// replaying the run journal for every HTTP GET.
type Projection struct {
	records []StoredEvent
	state   State
}

func NewProjection(records []StoredEvent, state State) Projection {
	return Projection{records: records, state: state}
}

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
	return NewProjection(records, state).Overview(), nil
}

func (p Projection) Overview() OverviewView {
	records, state := p.records, p.state
	metrics := buildMetrics(records, state, MetricsQuery{}).Current
	remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
	if remaining < 0 {
		remaining = 0
	}
	site := "healthy"
	if state.Status == RunCompleted || state.ActiveDeployment != "" {
		site = "unavailable"
	} else if recentErrorRate(records, state.Clock.CurrentTime, 5*time.Minute) > 0 || len(state.ActiveAttacks) > 0 ||
		len(state.DegradedProviders) > 0 || (metrics.CapacityUnits > 0 && metrics.UsedLoadUnits >= metrics.CapacityUnits) {
		site = "degraded"
	}
	util := float64(0)
	if metrics.CapacityUnits > 0 {
		util = float64(metrics.UsedLoadUnits) / float64(metrics.CapacityUnits)
	}
	publicStatus := string(state.Status)
	if state.Status == RunCompleted && state.EndReason == "negative_balance" {
		publicStatus = "failed"
	}
	return OverviewView{RunID: state.RunID, SimulationTime: state.Clock.CurrentTime, SimulationEndsAt: state.Clock.EndsAt, Remaining: remaining,
		RunStatus: publicStatus, SiteStatus: site, CurrentDeploymentID: state.ActiveDeployment,
		ServerCount: metrics.ServerCount, CapacityUtilization: util, ErrorRate: metrics.ErrorRate,
		SuccessfulPurchases: state.Economy.SuccessfulPurchases, RevenueMinor: state.Economy.RevenueMinor,
		ServerCostMinor: state.Economy.ServerCostMinor,
		BalanceMinor:    state.Economy.InitialBalanceMinor + state.Economy.RevenueMinor - state.Economy.ServerCostMinor - state.Economy.DeploymentCostMinor}
}

func recentErrorRate(records []StoredEvent, now time.Time, window time.Duration) float64 {
	from := now.Add(-window)
	var total, failed uint64
	for _, record := range records {
		switch event := record.Event.(type) {
		case events.PageRequestCompleted:
			if event.CompletedAt.Before(from) || event.CompletedAt.After(now) {
				continue
			}
			total++
			if event.StatusCode >= 500 {
				failed++
			}
		case events.PageRequestRejected:
			if event.RejectedAt.Before(from) || event.RejectedAt.After(now) {
				continue
			}
			total++
			if event.StatusCode >= 500 {
				failed++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(failed) / float64(total)
}

func (q *QueryService) Metrics(runID string, query MetricsQuery) (MetricsView, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return MetricsView{}, err
	}
	return NewProjection(records, state).Metrics(query), nil
}

func (p Projection) Metrics(query MetricsQuery) MetricsView {
	return buildMetrics(p.records, p.state, query)
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
	for _, page := range []model.PageType{model.PageProductList, model.PageProduct, model.PagePurchase} {
		background := ddosLoadPerActiveServer(state, page) * int64(current.ServerCount)
		current.UsedLoadUnits += background
		if background > 0 {
			pageMetric(byPage, page).UsedLoadUnits += background
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
	type requestBucket struct {
		at                time.Time
		page              model.PageType
		total, ok, failed uint64
		latencies         []time.Duration
	}
	buckets := make(map[string]*requestBucket)
	points := make([]MetricPointView, 0)
	selected := make(map[string]bool, len(query.Names))
	for _, name := range query.Names {
		selected[name] = true
	}
	seriesRequested := !query.From.IsZero() || !query.To.IsZero()
	wants := func(name string) bool { return seriesRequested && (len(selected) == 0 || selected[name]) }
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
		current.RequestsTotal++
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
		at := bucket(event.CompletedAt, query.From, query.Step)
		key := at.Format(time.RFC3339Nano) + "\x00" + string(page)
		stat := buckets[key]
		if stat == nil {
			stat = &requestBucket{at: at, page: page}
			buckets[key] = stat
		}
		stat.total++
		if event.StatusCode >= 500 {
			stat.failed++
		} else if event.StatusCode >= 200 && event.StatusCode < 300 {
			stat.ok++
		}
		if event.Latency > 0 {
			stat.latencies = append(stat.latencies, event.Latency)
		}
	}
	for _, stat := range buckets {
		if wants("requests_total") {
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "requests_total", Page: stat.page, Value: float64(stat.total)})
		}
		if wants("responses_200") {
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "responses_200", Page: stat.page, Value: float64(stat.ok)})
		}
		if wants("responses_500") {
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "responses_500", Page: stat.page, Value: float64(stat.failed)})
		}
		if wants("error_rate") && stat.total > 0 {
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "error_rate", Page: stat.page, Value: float64(stat.failed) / float64(stat.total)})
		}
		sort.Slice(stat.latencies, func(i, j int) bool { return stat.latencies[i] < stat.latencies[j] })
		if len(stat.latencies) > 0 {
			if wants("latency_p50_ms") {
				points = append(points, MetricPointView{Timestamp: stat.at, Name: "latency_p50_ms", Page: stat.page, Value: float64(stat.latencies[(len(stat.latencies)-1)/2]) / float64(time.Millisecond)})
			}
			if wants("latency_p95_ms") {
				points = append(points, MetricPointView{Timestamp: stat.at, Name: "latency_p95_ms", Page: stat.page, Value: float64(stat.latencies[int(float64(len(stat.latencies)-1)*.95+.999999)]) / float64(time.Millisecond)})
			}
		}
	}
	if query.Page == "" {
		for _, record := range records {
			var at time.Time
			var name string
			var value float64
			switch event := record.Event.(type) {
			case events.ProductPurchased:
				at = event.PurchasedAt
				if (!query.From.IsZero() && at.Before(query.From)) || (!query.To.IsZero() && at.After(query.To)) {
					continue
				}
				if wants("successful_purchases") {
					points = append(points, MetricPointView{Timestamp: bucket(at, query.From, query.Step), Name: "successful_purchases", Value: 1})
				}
				if wants("revenue_minor") {
					points = append(points, MetricPointView{Timestamp: bucket(at, query.From, query.Step), Name: "revenue_minor", Value: float64(event.PriceMinor)})
				}
				continue
			case events.RevenueLost:
				at, name, value = event.LostAt, "lost_revenue_minor", float64(event.AmountMinor)
			case events.InfrastructureCostAccrued:
				at, name, value = event.To, "server_cost_minor", float64(event.AmountMinor)
			default:
				continue
			}
			if !wants(name) || (!query.From.IsZero() && at.Before(query.From)) || (!query.To.IsZero() && at.After(query.To)) {
				continue
			}
			points = append(points, MetricPointView{Timestamp: bucket(at, query.From, query.Step), Name: name, Value: value})
		}
	}
	total := current.Responses200 + current.Responses500
	if total > 0 {
		current.ErrorRate = float64(current.Responses500) / float64(total)
	}
	if current.CapacityUnits > 0 {
		current.CapacityUtilization = float64(current.UsedLoadUnits) / float64(current.CapacityUnits)
	}
	if seriesRequested {
		points = append(points, historicalGaugePoints(records, state, query, wants)...)
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

type historicalGaugeChange struct {
	at       time.Time
	order    uint64
	kind     uint8
	serverID model.ServerID
	attackID model.AttackID
	page     model.PageType
	capacity int64
	load     int64
	hold     time.Duration
	attack   historicalAttack
}

type historicalAttack struct {
	page                model.PageType
	requestsPerMinute   int64
	loadUnitsPerRequest int64
	resolution          model.AttackResolution
}

const (
	gaugeServerActivated uint8 = iota + 1
	gaugeServerDeactivated
	gaugeAllocationStarted
	gaugeAllocationEnded
	gaugePageConfigured
	gaugeAttackStarted
	gaugeAttackEnded
)

func historicalGaugePoints(records []StoredEvent, state State, query MetricsQuery, wants func(string) bool) []MetricPointView {
	wantsGauge := wants("server_count") || wants("capacity_units") || wants("used_load_units") ||
		wants("capacity_utilization") || wants("active_requests")
	if !wantsGauge || query.From.IsZero() || query.To.IsZero() || query.Step <= 0 || query.To.Before(query.From) {
		return nil
	}
	limit := query.To
	if state.Clock.CurrentTime.Before(limit) {
		limit = state.Clock.CurrentTime
	}
	if limit.Before(query.From) {
		return nil
	}

	type allocation struct {
		startedAt, releasesAt time.Time
		load                  int64
		order                 uint64
	}
	allocations := make(map[model.RequestID]allocation)
	requestLoads := make(map[model.RequestID]int64)
	serverCapacity := make(map[model.ServerID]int64)
	changes := make([]historicalGaugeChange, 0)
	for _, record := range records {
		order := record.Version * 2
		switch event := record.Event.(type) {
		case events.ServerActivated:
			changes = append(changes, historicalGaugeChange{at: event.ActivatedAt, order: order, kind: gaugeServerActivated, serverID: event.ServerID, capacity: serverCapacity[event.ServerID]})
		case events.ServerDrainingStarted:
			changes = append(changes, historicalGaugeChange{at: event.StartedAt, order: order, kind: gaugeServerDeactivated, serverID: event.ServerID})
		case events.ServerRemoved:
			changes = append(changes, historicalGaugeChange{at: event.RemovedAt, order: order, kind: gaugeServerDeactivated, serverID: event.ServerID})
		case events.ServerProvisioningStarted:
			serverCapacity[event.ServerID] = event.CapacityUnits
		case events.PageRequestStarted:
			requestLoads[event.RequestID] = event.LoadUnits
		case events.PageRequestAccepted:
			allocations[event.RequestID] = allocation{startedAt: event.AcceptedAt, releasesAt: event.ReleasesAt, load: requestLoads[event.RequestID], order: order}
		case events.CapacityAllocationReleased:
			allocation := allocations[event.RequestID]
			if !allocation.startedAt.IsZero() && event.ReleasedAt.Before(allocation.releasesAt) {
				allocation.releasesAt = event.ReleasedAt
				allocations[event.RequestID] = allocation
			}
		case events.PageConfigured:
			changes = append(changes, historicalGaugeChange{at: event.ConfiguredAt, order: order, kind: gaugePageConfigured, page: event.Page, hold: event.HoldDuration})
		case events.PageLoadChanged:
			changes = append(changes, historicalGaugeChange{at: event.ChangedAt, order: order, kind: gaugePageConfigured, page: event.Page, hold: event.NewHoldDuration})
		case events.TrafficAttackStarted:
			changes = append(changes, historicalGaugeChange{at: event.StartedAt, order: order, kind: gaugeAttackStarted, attackID: event.AttackID,
				attack: historicalAttack{page: event.TargetPage, requestsPerMinute: event.RequestsPerMinute, loadUnitsPerRequest: event.LoadUnitsPerRequest, resolution: event.Resolution}})
		case events.TrafficAttackEnded:
			changes = append(changes, historicalGaugeChange{at: event.EndedAt, order: order, kind: gaugeAttackEnded, attackID: event.AttackID})
		case events.TrafficAttackMitigated:
			changes = append(changes, historicalGaugeChange{at: event.MitigatedAt, order: order, kind: gaugeAttackEnded, attackID: event.AttackID})
		}
	}

	for _, allocation := range allocations {
		changes = append(changes,
			historicalGaugeChange{at: allocation.startedAt, order: allocation.order, kind: gaugeAllocationStarted, load: allocation.load},
			historicalGaugeChange{at: allocation.releasesAt, order: allocation.order + 1, kind: gaugeAllocationEnded, load: allocation.load},
		)
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].at.Equal(changes[j].at) {
			return changes[i].order < changes[j].order
		}
		return changes[i].at.Before(changes[j].at)
	})

	activeServers := make(map[model.ServerID]int64)
	pageHolds := make(map[model.PageType]time.Duration)
	activeAttacks := make(map[model.AttackID]historicalAttack)
	serverCount, activeRequests := 0, 0
	capacityUnits, allocationLoad := int64(0), int64(0)
	changeIndex := 0
	points := make([]MetricPointView, 0)
	for at := query.From; !at.After(limit); at = at.Add(query.Step) {
		for changeIndex < len(changes) && !changes[changeIndex].at.After(at) {
			change := changes[changeIndex]
			switch change.kind {
			case gaugeServerActivated:
				if _, exists := activeServers[change.serverID]; !exists {
					activeServers[change.serverID] = change.capacity
					serverCount++
					capacityUnits += change.capacity
				}
			case gaugeServerDeactivated:
				if capacity, exists := activeServers[change.serverID]; exists {
					delete(activeServers, change.serverID)
					serverCount--
					capacityUnits -= capacity
				}
			case gaugeAllocationStarted:
				activeRequests++
				allocationLoad += change.load
			case gaugeAllocationEnded:
				activeRequests--
				allocationLoad -= change.load
			case gaugePageConfigured:
				pageHolds[change.page] = change.hold
			case gaugeAttackStarted:
				activeAttacks[change.attackID] = change.attack
			case gaugeAttackEnded:
				delete(activeAttacks, change.attackID)
			}
			changeIndex++
		}
		usedLoad := allocationLoad + historicalDDoSLoad(serverCount, pageHolds, activeAttacks)
		utilization := float64(0)
		if capacityUnits > 0 {
			utilization = float64(usedLoad) / float64(capacityUnits)
		}
		gauges := []struct {
			name  string
			value float64
		}{
			{"server_count", float64(serverCount)}, {"capacity_units", float64(capacityUnits)},
			{"used_load_units", float64(usedLoad)}, {"capacity_utilization", utilization},
			{"active_requests", float64(activeRequests)},
		}
		for _, gauge := range gauges {
			if wants(gauge.name) {
				points = append(points, MetricPointView{Timestamp: at, Name: gauge.name, Value: gauge.value})
			}
		}
		if at.After(limit.Add(-query.Step)) {
			break
		}
	}
	return points
}

func historicalDDoSLoad(serverCount int, pageHolds map[model.PageType]time.Duration, attacks map[model.AttackID]historicalAttack) int64 {
	if serverCount <= 0 {
		return 0
	}
	totalByPage := make(map[model.PageType]int64)
	for _, attack := range attacks {
		if attack.resolution != model.AttackScaleOrExpiry {
			continue
		}
		hold := pageHolds[attack.page]
		concurrent := (attack.requestsPerMinute*int64(hold) + int64(time.Minute) - 1) / int64(time.Minute)
		totalByPage[attack.page] += concurrent * attack.loadUnitsPerRequest
	}
	servers := int64(serverCount)
	used := int64(0)
	for _, total := range totalByPage {
		used += ((total + servers - 1) / servers) * servers
	}
	return used
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
	type pointKey struct {
		at   int64
		name string
		page model.PageType
	}
	out := make([]MetricPointView, 0, len(in))
	indexes := make(map[pointKey]int, len(in))
	for _, p := range in {
		key := pointKey{at: p.Timestamp.UnixNano(), name: p.Name, page: p.Page}
		if index, exists := indexes[key]; exists {
			out[index].Value += p.Value
			continue
		}
		indexes[key] = len(out)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Timestamp.Equal(out[j].Timestamp) {
			if out[i].Name == out[j].Name {
				return out[i].Page < out[j].Page
			}
			return out[i].Name < out[j].Name
		}
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}

func (q *QueryService) Logs(runID string, query LogsQuery) (LogsView, error) {
	records, _, err := q.records(runID)
	if err != nil {
		return LogsView{}, err
	}
	return NewProjection(records, State{}).Logs(query)
}

func (p Projection) Logs(query LogsQuery) (LogsView, error) {
	records := p.records
	requestStates := make(map[model.RequestID]PageRequestState)
	requestOrder := make([]model.RequestID, 0)
	for _, record := range records {
		switch event := record.Event.(type) {
		case events.PageRequestStarted:
			requestStates[event.RequestID] = PageRequestState{ID: event.RequestID, Source: event.Source, VisitorID: event.VisitorID, Page: event.Page, ProductID: event.ProductID, LoadUnits: event.LoadUnits, StartedAt: event.StartedAt}
			requestOrder = append(requestOrder, event.RequestID)
		case events.PageRequestAccepted:
			request := requestStates[event.RequestID]
			request.ServerID, request.ReleasesAt = event.ServerID, event.ReleasesAt
			requestStates[event.RequestID] = request
		case events.PageRequestCompleted:
			request := requestStates[event.RequestID]
			request.StatusCode, request.ErrorCode, request.Message = event.StatusCode, event.ErrorCode, event.Message
			request.CompletedAt = event.CompletedAt
			request.Latency = event.Latency
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
		all = append(all, RequestLogView{Entry: logs.Entry{Timestamp: request.CompletedAt, RequestID: request.ID, Source: request.Source, VisitorID: request.VisitorID, Page: request.Page, ProductID: request.ProductID, StatusCode: request.StatusCode, ErrorCode: request.ErrorCode, Message: request.Message}, Latency: request.Latency, LoadUnits: request.LoadUnits, ServerID: request.ServerID})
	}
	start, err := decodeLogCursor(query.Cursor)
	if err != nil {
		return LogsView{}, err
	}
	result := LogsView{}
	for i, item := range all {
		if i < start {
			continue
		}
		if !query.From.IsZero() && item.Entry.Timestamp.Before(query.From) || !query.To.IsZero() && item.Entry.Timestamp.After(query.To) || query.Page != "" && item.Entry.Page != query.Page || query.StatusCode != 0 && item.Entry.StatusCode != query.StatusCode {
			continue
		}
		result.Logs = append(result.Logs, item)
		if query.Limit > 0 && len(result.Logs) >= query.Limit {
			if i < len(all)-1 {
				result.NextCursor = encodeLogCursor(i + 1)
			}
			break
		}
	}
	if len(result.Logs) == 0 {
		result.Logs = nil
	}
	return result, nil
}

func encodeLogCursor(offset int) string {
	if offset == 1 {
		return "opaque-next-cursor"
	}
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + strconv.Itoa(offset)))
}

// LogCursor returns an opaque cursor immediately after offset site-log entries.
func LogCursor(offset int) string {
	if offset <= 0 {
		return ""
	}
	return encodeLogCursor(offset)
}

func decodeLogCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if cursor == "opaque-next-cursor" {
		return 1, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(payload) < 4 || string(payload[:3]) != "v1:" {
		return 0, fmt.Errorf("%w: invalid logs cursor", ErrInvalidCommand)
	}
	offset, err := strconv.Atoi(string(payload[3:]))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%w: invalid logs cursor", ErrInvalidCommand)
	}
	return offset, nil
}

func (q *QueryService) Resources(runID string) (ResourcesView, error) {
	_, state, err := q.records(runID)
	if err != nil {
		return ResourcesView{}, err
	}
	return NewProjection(nil, state).Resources(), nil
}

func (p Projection) Resources() ResourcesView {
	state := p.state
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
		if s.Status == model.ServerActive {
			for _, page := range []model.PageType{model.PageProductList, model.PageProduct, model.PagePurchase} {
				sv.UsedLoadUnits += ddosLoadPerActiveServer(state, page)
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
	return v
}
func (q *QueryService) Economy(runID string) (EconomyView, error) {
	_, s, e := q.records(runID)
	if e != nil {
		return EconomyView{}, e
	}
	return NewProjection(nil, s).Economy(), nil
}

func (p Projection) Economy() EconomyView {
	s := p.state
	v := EconomyView{Currency: s.Economy.Currency, SuccessfulPurchases: s.Economy.SuccessfulPurchases, LostPurchases: s.Economy.LostPurchases, RevenueMinor: s.Economy.RevenueMinor, LostRevenueMinor: s.Economy.LostRevenueMinor, ServerCostMinor: s.Economy.ServerCostMinor, DeploymentCostMinor: s.Economy.DeploymentCostMinor}
	v.BalanceMinor = s.Economy.InitialBalanceMinor + v.RevenueMinor - v.ServerCostMinor - v.DeploymentCostMinor
	return v
}

func (q *QueryService) Deployments(runID string) (DeploymentsView, error) {
	_, state, err := q.records(runID)
	if err != nil {
		return DeploymentsView{}, err
	}
	return NewProjection(nil, state).Deployments(), nil
}

func (p Projection) Deployments() DeploymentsView {
	state := p.state
	result := DeploymentsView{}
	for _, deployment := range state.Deployments {
		status := deployment.Status
		if status == model.DeploymentStatusApplied {
			status = model.DeploymentStatusSucceeded
		}
		result.Deployments = append(result.Deployments, DeploymentView{DeploymentID: deployment.ID, Sequence: deployment.Sequence,
			Name: deployment.Name, Description: deployment.Description, Status: status, OperationID: deployment.OperationID})
	}
	sort.Slice(result.Deployments, func(i, j int) bool { return result.Deployments[i].Sequence < result.Deployments[j].Sequence })
	if len(result.Deployments) == 0 {
		result.Deployments = nil
	}
	return result
}
func (q *QueryService) Operation(runID string, id model.OperationID) (OperationView, error) {
	_, s, e := q.records(runID)
	if e != nil {
		return OperationView{}, e
	}
	return NewProjection(nil, s).Operation(id)
}

func (p Projection) Operation(id model.OperationID) (OperationView, error) {
	s := p.state
	o, ok := s.Operations[id]
	if !ok {
		return OperationView{}, fmt.Errorf("%w: operation %s", ErrProjectionNotFound, id)
	}
	progress := float64(o.ProgressPPM) / float64(ProbabilityScale)
	if o.Status == model.OperationStatusSucceeded || o.Status == model.OperationStatusFailed {
		progress = 1
	} else if o.Status == model.OperationStatusRunning {
		var expected time.Time
		for _, deployment := range s.Deployments {
			if deployment.OperationID == id && deployment.ExpectedCompletionAt.After(expected) {
				expected = deployment.ExpectedCompletionAt
			}
		}
		for _, server := range s.Servers {
			if server.OperationID == id && server.ReadyAt.After(expected) {
				expected = server.ReadyAt
			}
		}
		if !expected.IsZero() && expected.After(o.StartedAt) {
			progress = float64(s.Clock.CurrentTime.Sub(o.StartedAt)) / float64(expected.Sub(o.StartedAt))
			if progress < 0 {
				progress = 0
			}
			if progress > 1 {
				progress = 1
			}
		}
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
