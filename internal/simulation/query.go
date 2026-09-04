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
	Costs                CostsView
	Availability         AvailabilityView
	RunID                string
	SimulationTime       time.Time
	SimulationEndsAt     time.Time
	Remaining            time.Duration
	RunStatus            string
	SiteStatus           string
	ServerCount          int
	CapacityUtilization  float64
	ErrorRate            float64
	RequestsTotal        uint64
	VisitorErrorRate     float64
	VisitorRequestsTotal uint64
	ServerCostMinor      int64
	Uptime               float64
}
type AvailabilityView struct {
	UptimeTarget      float64
	ObservedDuration  time.Duration
	AvailableDuration time.Duration
	DowntimeDuration  time.Duration
	UptimeRatio       *float64
	SLOPassed         *bool
}
type MetricSnapshotView struct {
	ServerCostMinor, BackupStorageCostMinor, TotalCostMinor, CurrentCostPerHourMinor   int64
	ObservedDuration, AvailableDuration, DowntimeDuration                              time.Duration
	UptimeRatio                                                                        *float64
	ServerCount                                                                        int
	CapacityUnits, UsedLoadUnits                                                       int64
	ActiveRequests                                                                     int
	DatabaseActiveConnections, DatabaseConnectionLimit                                 int
	DiskTotalBytes, DiskSystemBytes, DiskDatabaseBytes, DiskLogsBytes, DiskFreeBytes   int64
	RequestsTotal                                                                      uint64
	CapacityUtilization                                                                float64
	Responses200, Responses403, Responses500, Responses503                             uint64
	ErrorRate                                                                          float64
	VisitorRequestsTotal                                                               uint64
	VisitorResponses200, VisitorResponses403, VisitorResponses500, VisitorResponses503 uint64
	VisitorErrorRate                                                                   float64
	LatencyP50, LatencyP95                                                             time.Duration
	ByPage                                                                             []PageMetricView
}
type PageMetricView struct {
	Page                                                   model.PageType
	ActiveRequests                                         int
	UsedLoadUnits                                          int64
	Responses200, Responses403, Responses500, Responses503 uint64
	ErrorRate                                              float64
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
	HasError   *bool
	ErrorCode  model.RequestFailureCode
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
type InboxQuery struct {
	Cursor string
	Limit  int
}
type InboxMessageView struct {
	MessageID   model.MessageID
	SenderEmail string
	SentAt      time.Time
	Subject     string
	Description string
}
type InboxView struct {
	Messages   []InboxMessageView
	NextCursor string
}
type ServerResourceView struct {
	ServerID         model.ServerID
	Name             string
	Role             model.ServerRole
	InstanceType     model.InstanceType
	Status           model.ServerLifecycleStatus
	CapacityUnits    int64
	UsedLoadUnits    int64
	CostPerHourMinor int64
	Disk             DiskUsageView
	DatabaseIDs      []model.DatabaseID
	CredentialID     model.CredentialID
}
type ResourcesView struct {
	ActiveInstances                                          int
	TotalCapacityUnits, UsedLoadUnits, TotalCostPerHourMinor int64
	Servers                                                  []ServerResourceView
}
type OperationView struct {
	OperationID                         model.OperationID
	Kind                                model.OperationKind
	Status                              model.OperationLifecycleStatus
	Progress                            float64
	SubmittedAt, StartedAt, CompletedAt time.Time
	ErrorCode, Message                  string
}

type DiskUsageView struct {
	ServerID       model.ServerID
	TotalBytes     int64
	SystemBytes    int64
	DatabaseBytes  int64
	LogsBytes      int64
	UsedBytes      int64
	FreeBytes      int64
	CleanableBytes int64
}

// FirewallRules returns the complete current rule set in evaluation order.
// Disabled and expired rules remain visible until explicitly deleted.
func (p Projection) FirewallRules() []FirewallRuleState {
	result := make([]FirewallRuleState, 0, len(p.state.FirewallRules))
	for _, rule := range p.state.FirewallRules {
		result = append(result, rule)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Rule.Priority == result[j].Rule.Priority {
			return result[i].Rule.ID < result[j].Rule.ID
		}
		return result[i].Rule.Priority < result[j].Rule.Priority
	})
	return result
}

// ServerTypes returns a stable, optionally role-filtered catalog projection.
func (p Projection) ServerTypes(role model.ServerRole) []ServerTypeState {
	result := make([]ServerTypeState, 0, len(p.state.ServerTypes))
	for _, serverType := range p.state.ServerTypes {
		if role == "" || serverType.Role == role {
			result = append(result, serverType)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].InstanceType < result[j].InstanceType })
	return result
}

func (p Projection) Database(databaseID model.DatabaseID) (DatabaseState, error) {
	database, ok := p.state.Databases[databaseID]
	if !ok {
		return DatabaseState{}, ErrProjectionNotFound
	}
	return database, nil
}

func (p Projection) DiskUsage(serverID model.ServerID) (DiskUsageView, error) {
	if _, ok := p.state.Servers[serverID]; !ok {
		return DiskUsageView{}, ErrProjectionNotFound
	}
	return diskUsage(p.state, serverID), nil
}

func diskUsage(state State, serverID model.ServerID) DiskUsageView {
	server := state.Servers[serverID]
	view := DiskUsageView{ServerID: serverID, TotalBytes: server.DiskBytes, SystemBytes: server.SystemBytes, LogsBytes: server.LogsBytes}
	if database, found := databaseOnServer(state, serverID); found {
		view.DatabaseBytes, view.LogsBytes = database.DataBytes, view.LogsBytes+database.LogsBytes
	}
	view.UsedBytes = view.SystemBytes + view.DatabaseBytes + view.LogsBytes
	view.FreeBytes = view.TotalBytes - view.UsedBytes
	if view.FreeBytes < 0 {
		view.FreeBytes = 0
	}
	view.CleanableBytes = view.LogsBytes
	return view
}

func (p Projection) DatabaseBackups(databaseID model.DatabaseID) []BackupState {
	result := make([]BackupState, 0, len(p.state.Backups))
	for _, backup := range p.state.Backups {
		if databaseID == "" || backup.DatabaseID == databaseID {
			result = append(result, backup)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
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

func (q *QueryService) ServerTypes(runID string, role model.ServerRole) ([]ServerTypeState, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return nil, err
	}
	return NewProjection(records, state).ServerTypes(role), nil
}

func (q *QueryService) Database(runID string, databaseID model.DatabaseID) (DatabaseState, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return DatabaseState{}, err
	}
	return NewProjection(records, state).Database(databaseID)
}

func (q *QueryService) DiskUsage(runID string, serverID model.ServerID) (DiskUsageView, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return DiskUsageView{}, err
	}
	return NewProjection(records, state).DiskUsage(serverID)
}

func (q *QueryService) DatabaseBackups(runID string, databaseID model.DatabaseID) ([]BackupState, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return nil, err
	}
	return NewProjection(records, state).DatabaseBackups(databaseID), nil
}

func (q *QueryService) FirewallRules(runID string) ([]FirewallRuleState, error) {
	records, state, err := q.records(runID)
	if err != nil {
		return nil, err
	}
	return NewProjection(records, state).FirewallRules(), nil
}

func (p Projection) Overview() OverviewView {
	records, state := p.records, p.state
	metrics := buildMetrics(records, state, MetricsQuery{}).Current
	remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
	if remaining < 0 {
		remaining = 0
	}
	site := "healthy"
	if state.Status == RunCompleted || state.Site.Status != model.SiteRunning || state.FirewallUnavailable || !siteDependenciesAvailable(state) {
		site = "unavailable"
	} else if recentErrorRate(records, state.Clock.CurrentTime, 5*time.Minute) > 0 || len(state.ActiveAttacks) > 0 ||
		metrics.ServerCount == 0 || (metrics.CapacityUnits > 0 && metrics.UsedLoadUnits >= metrics.CapacityUnits) {
		site = "degraded"
	}
	util := float64(0)
	if metrics.CapacityUnits > 0 {
		util = float64(metrics.UsedLoadUnits) / float64(metrics.CapacityUnits)
	}
	publicStatus := string(state.Status)
	availability := availabilityView(state)
	uptime := float64(1)
	if availability.UptimeRatio != nil {
		uptime = *availability.UptimeRatio
	}
	return OverviewView{RunID: state.RunID, SimulationTime: state.Clock.CurrentTime, SimulationEndsAt: state.Clock.EndsAt, Remaining: remaining,
		RunStatus: publicStatus, SiteStatus: site,
		ServerCount: metrics.ServerCount, CapacityUtilization: util, ErrorRate: metrics.ErrorRate, RequestsTotal: metrics.RequestsTotal,
		VisitorErrorRate: metrics.VisitorErrorRate, VisitorRequestsTotal: metrics.VisitorRequestsTotal,
		ServerCostMinor: state.Costs.ServerCostMinor, Costs: p.Costs(), Availability: availability, Uptime: uptime}
}

func availabilityView(state State) AvailabilityView {
	const target = 0.99
	observed := state.Clock.CurrentTime.Sub(state.Clock.StartedAt)
	if observed < 0 {
		observed = 0
	}
	downtime := state.Site.DowntimeAt(state.Clock.CurrentTime)
	if downtime < 0 {
		downtime = 0
	}
	if downtime > observed {
		downtime = observed
	}
	result := AvailabilityView{UptimeTarget: target, ObservedDuration: observed,
		AvailableDuration: observed - downtime, DowntimeDuration: downtime}
	if observed > 0 {
		ratio := float64(result.AvailableDuration) / float64(observed)
		result.UptimeRatio = &ratio
	}
	if state.Status == RunCompleted {
		passed := result.UptimeRatio != nil && *result.UptimeRatio >= target
		result.SLOPassed = &passed
	}
	return result
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
			if event.StatusCode >= 400 {
				failed++
			}
		case events.PageRequestRejected:
			if event.RejectedAt.Before(from) || event.RejectedAt.After(now) {
				continue
			}
			total++
			if event.StatusCode >= 400 {
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
	costs := NewProjection(nil, state).Costs()
	availability := availabilityView(state)
	current := MetricSnapshotView{ServerCostMinor: costs.ServerCostMinor, BackupStorageCostMinor: costs.BackupStorageCostMinor,
		TotalCostMinor: costs.TotalCostMinor, CurrentCostPerHourMinor: costs.CurrentCostPerHourMinor,
		ObservedDuration: availability.ObservedDuration, AvailableDuration: availability.AvailableDuration,
		DowntimeDuration: availability.DowntimeDuration, UptimeRatio: availability.UptimeRatio}
	byPage := make(map[model.PageType]*PageMetricView)
	latencies := make([]time.Duration, 0)
	current.DatabaseActiveConnections = state.DatabaseConnectionCounts[state.Site.DatabaseID]
	if database, ok := state.Databases[state.Site.DatabaseID]; ok {
		current.DatabaseConnectionLimit = state.Servers[database.ServerID].ConnectionLimit
	}
	for serverID := range state.Servers {
		usage := diskUsage(state, serverID)
		current.DiskTotalBytes += usage.TotalBytes
		current.DiskSystemBytes += usage.SystemBytes
		current.DiskDatabaseBytes += usage.DatabaseBytes
		current.DiskLogsBytes += usage.LogsBytes
		current.DiskFreeBytes += usage.FreeBytes
	}
	for _, server := range state.Servers {
		if server.Status == model.ServerActive && server.Role != model.ServerRoleDatabase {
			current.ServerCount++
			current.CapacityUnits += server.CapacityUnits
		}
	}
	for _, page := range []model.PageType{model.PageProductList, model.PageProduct} {
		usableBackends := int64(0)
		for _, server := range state.Servers {
			if server.Status == model.ServerActive && server.Role != model.ServerRoleDatabase && !serverDiskFull(state, server) {
				usableBackends++
			}
		}
		background := ddosLoadPerActiveServer(state, page, state.Clock.CurrentTime) * usableBackends
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
		at                                               time.Time
		page                                             model.PageType
		total, ok, forbidden, internalError, unavailable uint64
		latencies                                        []time.Duration
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
	requestSources := make(map[model.RequestID]model.RequestSource)
	for _, record := range records {
		if started, ok := record.Event.(events.PageRequestStarted); ok {
			requestPages[started.RequestID] = started.Page
			requestSources[started.RequestID] = started.Source
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
		switch event.StatusCode {
		case 403:
			current.Responses403++
			p.Responses403++
			if requestSources[event.RequestID] == model.RequestSourceVisitor {
				current.VisitorRequestsTotal++
				current.VisitorResponses403++
			}
		case 500:
			current.Responses500++
			p.Responses500++
			if requestSources[event.RequestID] == model.RequestSourceVisitor {
				current.VisitorRequestsTotal++
				current.VisitorResponses500++
			}
		case 503:
			current.Responses503++
			p.Responses503++
			if requestSources[event.RequestID] == model.RequestSourceVisitor {
				current.VisitorRequestsTotal++
				current.VisitorResponses503++
			}
		default:
			if event.StatusCode < 200 || event.StatusCode >= 300 {
				break
			}
			current.Responses200++
			p.Responses200++
			if requestSources[event.RequestID] == model.RequestSourceVisitor {
				current.VisitorRequestsTotal++
				current.VisitorResponses200++
			}
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
		switch event.StatusCode {
		case 403:
			stat.forbidden++
		case 500:
			stat.internalError++
		case 503:
			stat.unavailable++
		default:
			if event.StatusCode >= 200 && event.StatusCode < 300 {
				stat.ok++
			}
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
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "responses_500", Page: stat.page, Value: float64(stat.internalError)})
		}
		if wants("responses_403") {
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "responses_403", Page: stat.page, Value: float64(stat.forbidden)})
		}
		if wants("responses_503") {
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "responses_503", Page: stat.page, Value: float64(stat.unavailable)})
		}
		if wants("error_rate") && stat.total > 0 {
			failed := stat.forbidden + stat.internalError + stat.unavailable
			points = append(points, MetricPointView{Timestamp: stat.at, Name: "error_rate", Page: stat.page, Value: float64(failed) / float64(stat.total)})
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
	total := current.Responses200 + current.Responses403 + current.Responses500 + current.Responses503
	if total > 0 {
		failed := current.Responses403 + current.Responses500 + current.Responses503
		current.ErrorRate = float64(failed) / float64(total)
	}
	if current.VisitorRequestsTotal > 0 {
		failed := current.VisitorResponses403 + current.VisitorResponses500 + current.VisitorResponses503
		current.VisitorErrorRate = float64(failed) / float64(current.VisitorRequestsTotal)
	}
	if current.CapacityUnits > 0 {
		current.CapacityUtilization = float64(current.UsedLoadUnits) / float64(current.CapacityUnits)
	}
	if seriesRequested {
		points = append(points, historicalGaugePoints(records, state, query, wants)...)
		points = append(points, historicalSLOAndCostPoints(records, state, query, wants)...)
		points = append(points, historicalInfrastructurePoints(records, state, query, wants)...)
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
		total := p.Responses200 + p.Responses403 + p.Responses500 + p.Responses503
		if total > 0 {
			p.ErrorRate = float64(p.Responses403+p.Responses500+p.Responses503) / float64(total)
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

type historicalMetricChange struct {
	at    time.Time
	order uint64
	event events.Event
}

func historicalSLOAndCostPoints(records []StoredEvent, state State, query MetricsQuery, wants func(string) bool) []MetricPointView {
	wantsAny := wants("server_cost_minor") || wants("backup_storage_cost_minor") || wants("total_cost_minor") ||
		wants("current_cost_per_hour_minor") || wants("observed_seconds") || wants("available_seconds") ||
		wants("downtime_seconds") || wants("uptime_ratio")
	if !wantsAny || query.From.IsZero() || query.To.IsZero() || query.Step <= 0 || query.To.Before(query.From) {
		return nil
	}
	limit := query.To
	if state.Clock.CurrentTime.Before(limit) {
		limit = state.Clock.CurrentTime
	}
	if limit.Before(query.From) {
		return nil
	}

	changes := make([]historicalMetricChange, 0)
	for _, record := range records {
		at, relevant := historicalMetricEventTime(record.Event)
		if relevant && !at.After(limit) {
			changes = append(changes, historicalMetricChange{at: at, order: record.Version, event: record.Event})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].at.Equal(changes[j].at) {
			return changes[i].order < changes[j].order
		}
		return changes[i].at.Before(changes[j].at)
	})

	siteRunning, backendAvailable, firewallAvailable := true, true, true
	connectedDatabase := model.DatabaseID("")
	databaseAvailable := make(map[model.DatabaseID]bool)
	serverRates := make(map[model.ServerID]int64)
	activeServers := make(map[model.ServerID]bool)
	backupRates := make(map[model.BackupID]int64)
	serverCost, backupCost := int64(0), int64(0)
	downtime := time.Duration(0)
	availabilityCursor := state.Clock.StartedAt
	available := func() bool {
		return siteRunning && backendAvailable && firewallAvailable &&
			(connectedDatabase == "" || databaseAvailable[connectedDatabase])
	}
	apply := func(change historicalMetricChange) {
		at := change.at
		if at.Before(state.Clock.StartedAt) {
			at = state.Clock.StartedAt
		}
		if !available() && at.After(availabilityCursor) {
			downtime += at.Sub(availabilityCursor)
		}
		availabilityCursor = at
		switch event := change.event.(type) {
		case events.ServerProvisioningStarted:
			serverRates[event.ServerID] = historicalServerRate(event)
		case events.ServerActivated:
			activeServers[event.ServerID] = true
		case events.ServerRemoved:
			delete(activeServers, event.ServerID)
		case events.InfrastructureCostAccrued:
			serverCost += event.AmountMinor
		case events.DatabaseBackupCompleted:
			backupRates[event.BackupID] = event.StorageCostPerHourMinor
		case events.BackupStorageCostAccrued:
			backupCost += event.AmountMinor
		case events.DatabaseCreated:
			databaseAvailable[event.DatabaseID] = true
		case events.DatabaseAvailabilityChanged:
			databaseAvailable[event.DatabaseID] = event.Available
		case events.SiteStopStarted:
			siteRunning = false
		case events.SiteStopped:
			siteRunning = false
		case events.SiteDatabaseChanged:
			connectedDatabase = event.DatabaseID
		case events.SiteStarted:
			siteRunning = true
			connectedDatabase = event.DatabaseID
		case events.BackendAvailabilityChanged:
			backendAvailable = event.Available
		case events.FirewallAvailabilityChanged:
			firewallAvailable = event.Available
		}
	}

	changeIndex := 0
	points := make([]MetricPointView, 0)
	for at := query.From; !at.After(limit); at = at.Add(query.Step) {
		for changeIndex < len(changes) && !changes[changeIndex].at.After(at) {
			apply(changes[changeIndex])
			changeIndex++
		}
		observedAt := at
		if observedAt.Before(state.Clock.StartedAt) {
			observedAt = state.Clock.StartedAt
		}
		observed := observedAt.Sub(state.Clock.StartedAt)
		atDowntime := downtime
		if !available() && observedAt.After(availabilityCursor) {
			atDowntime += observedAt.Sub(availabilityCursor)
		}
		if atDowntime > observed {
			atDowntime = observed
		}
		availableDuration := observed - atDowntime
		currentRate := int64(0)
		for serverID := range activeServers {
			currentRate += serverRates[serverID]
		}
		for _, rate := range backupRates {
			currentRate += rate
		}
		values := []struct {
			name  string
			value float64
		}{
			{"server_cost_minor", float64(serverCost)}, {"backup_storage_cost_minor", float64(backupCost)},
			{"total_cost_minor", float64(serverCost + backupCost)}, {"current_cost_per_hour_minor", float64(currentRate)},
			{"observed_seconds", observed.Seconds()}, {"available_seconds", availableDuration.Seconds()},
			{"downtime_seconds", atDowntime.Seconds()},
		}
		for _, value := range values {
			if wants(value.name) {
				points = append(points, MetricPointView{Timestamp: at, Name: value.name, Value: value.value})
			}
		}
		if observed > 0 && wants("uptime_ratio") {
			points = append(points, MetricPointView{Timestamp: at, Name: "uptime_ratio", Value: float64(availableDuration) / float64(observed)})
		}
		if at.After(limit.Add(-query.Step)) {
			break
		}
	}
	return points
}

func historicalMetricEventTime(event events.Event) (time.Time, bool) {
	switch event := event.(type) {
	case events.ServerProvisioningStarted:
		return event.StartedAt, true
	case events.ServerActivated:
		return event.ActivatedAt, true
	case events.ServerRemoved:
		return event.RemovedAt, true
	case events.InfrastructureCostAccrued:
		return event.To, true
	case events.DatabaseBackupCompleted:
		return event.CompletedAt, true
	case events.BackupStorageCostAccrued:
		return event.To, true
	case events.DatabaseCreated:
		return event.CreatedAt, true
	case events.DatabaseAvailabilityChanged:
		return event.ChangedAt, true
	case events.SiteStopStarted:
		return event.StartedAt, true
	case events.SiteStopped:
		return event.StoppedAt, true
	case events.SiteDatabaseChanged:
		return event.ChangedAt, true
	case events.SiteStarted:
		return event.StartedAt, true
	case events.BackendAvailabilityChanged:
		return event.ChangedAt, true
	case events.FirewallAvailabilityChanged:
		return event.ChangedAt, true
	default:
		return time.Time{}, false
	}
}

func historicalServerRate(event events.ServerProvisioningStarted) int64 {
	if event.CostPerHourMinor > 0 {
		return event.CostPerHourMinor
	}
	if event.CostPerMonthMinor <= 0 {
		return 0
	}
	return (event.CostPerMonthMinor + 30*24 - 1) / (30 * 24)
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
			if capacity, backend := serverCapacity[event.ServerID]; backend {
				changes = append(changes, historicalGaugeChange{at: event.ActivatedAt, order: order, kind: gaugeServerActivated, serverID: event.ServerID, capacity: capacity})
			}
		case events.ServerDrainingStarted:
			changes = append(changes, historicalGaugeChange{at: event.StartedAt, order: order, kind: gaugeServerDeactivated, serverID: event.ServerID})
		case events.ServerRemoved:
			changes = append(changes, historicalGaugeChange{at: event.RemovedAt, order: order, kind: gaugeServerDeactivated, serverID: event.ServerID})
		case events.ServerProvisioningStarted:
			// Database connection capacity is intentionally separate from the
			// backend capacity gauges exposed by /metrics.
			if event.Role != model.ServerRoleDatabase && event.CapacityUnits > 0 {
				serverCapacity[event.ServerID] = event.CapacityUnits
			}
		case events.PageRequestStarted:
			requestLoads[event.RequestID] = event.LoadUnits
		case events.PageRequestAccepted:
			allocations[event.RequestID] = allocation{startedAt: event.AcceptedAt, releasesAt: event.ReleasesAt, load: requestLoads[event.RequestID], order: order}
		case events.PageConfigured:
			changes = append(changes, historicalGaugeChange{at: event.ConfiguredAt, order: order, kind: gaugePageConfigured, page: event.Page, hold: event.HoldDuration})
		case events.TrafficAttackStarted:
			changes = append(changes, historicalGaugeChange{at: event.StartedAt, order: order, kind: gaugeAttackStarted, attackID: event.AttackID,
				attack: historicalAttack{page: event.TargetPage, requestsPerMinute: event.RequestsPerMinute, loadUnitsPerRequest: event.LoadUnitsPerRequest, resolution: event.Resolution}})
		case events.TrafficAttackEnded:
			changes = append(changes, historicalGaugeChange{at: event.EndedAt, order: order, kind: gaugeAttackEnded, attackID: event.AttackID})
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

type historicalInfrastructureChange struct {
	at    time.Time
	order uint64
	event events.Event
}

type historicalDiskState struct {
	total, system, database, logs int64
}

func historicalInfrastructurePoints(records []StoredEvent, state State, query MetricsQuery, wants func(string) bool) []MetricPointView {
	wantsAny := wants("database_active_connections") || wants("database_connection_limit") ||
		wants("disk_total_bytes") || wants("disk_system_bytes") || wants("disk_database_bytes") ||
		wants("disk_logs_bytes") || wants("disk_free_bytes")
	if !wantsAny || query.From.IsZero() || query.To.IsZero() || query.Step <= 0 || query.To.Before(query.From) {
		return nil
	}
	limit := query.To
	if state.Clock.CurrentTime.Before(limit) {
		limit = state.Clock.CurrentTime
	}
	if limit.Before(query.From) {
		return nil
	}

	changes := make([]historicalInfrastructureChange, 0)
	for _, record := range records {
		at, ok := historicalInfrastructureEventTime(record.Event)
		if ok && !at.After(limit) {
			changes = append(changes, historicalInfrastructureChange{at: at, order: record.Version, event: record.Event})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].at.Equal(changes[j].at) {
			return changes[i].order < changes[j].order
		}
		return changes[i].at.Before(changes[j].at)
	})

	serverLimits := make(map[model.ServerID]int)
	databaseServers := make(map[model.DatabaseID]model.ServerID)
	connectionCounts := make(map[model.DatabaseID]int)
	disks := make(map[model.ServerID]historicalDiskState)
	connectedDatabase := model.DatabaseID("")
	apply := func(change historicalInfrastructureChange) {
		switch event := change.event.(type) {
		case events.ServerProvisioningStarted:
			serverLimits[event.ServerID] = event.ConnectionLimit
			disks[event.ServerID] = historicalDiskState{total: event.DiskBytes}
		case events.ServerRemoved:
			delete(serverLimits, event.ServerID)
			delete(disks, event.ServerID)
			for databaseID, serverID := range databaseServers {
				if serverID == event.ServerID {
					delete(databaseServers, databaseID)
					delete(connectionCounts, databaseID)
				}
			}
		case events.DatabaseCreated:
			databaseServers[event.DatabaseID] = event.ServerID
		case events.DatabaseDeleted:
			serverID := databaseServers[event.DatabaseID]
			disk := disks[serverID]
			disk.database = 0
			disks[serverID] = disk
			delete(databaseServers, event.DatabaseID)
			delete(connectionCounts, event.DatabaseID)
		case events.DatabaseConnectionOpened:
			connectionCounts[event.DatabaseID]++
		case events.DatabaseConnectionReleased:
			if connectionCounts[event.DatabaseID] > 0 {
				connectionCounts[event.DatabaseID]--
			}
		case events.SiteDatabaseChanged:
			connectedDatabase = event.DatabaseID
		case events.SiteStarted:
			connectedDatabase = event.DatabaseID
		case events.DatabaseStorageIncreased:
			disk := disks[event.ServerID]
			disk.database += event.DataBytesAdded
			disk.logs += event.LogsBytesAdded
			disks[event.ServerID] = disk
		case events.DatabaseRestoreCompleted:
			disk := disks[event.ServerID]
			disk.database = event.DataBytes
			disks[event.ServerID] = disk
		case events.DiskLogsIncreased:
			disk := disks[event.ServerID]
			disk.logs += event.BytesAdded
			disks[event.ServerID] = disk
		case events.DiskLogsCleaned:
			disk := disks[event.ServerID]
			disk.logs -= event.FreedBytes
			if disk.logs < 0 {
				disk.logs = 0
			}
			disks[event.ServerID] = disk
		}
	}

	changeIndex := 0
	points := make([]MetricPointView, 0)
	for at := query.From; !at.After(limit); at = at.Add(query.Step) {
		for changeIndex < len(changes) && !changes[changeIndex].at.After(at) {
			apply(changes[changeIndex])
			changeIndex++
		}
		activeConnections := connectionCounts[connectedDatabase]
		connectionLimit := serverLimits[databaseServers[connectedDatabase]]
		total, system, database, logsBytes := int64(0), int64(0), int64(0), int64(0)
		for _, disk := range disks {
			total += disk.total
			system += disk.system
			database += disk.database
			logsBytes += disk.logs
		}
		free := total - system - database - logsBytes
		if free < 0 {
			free = 0
		}
		values := []struct {
			name  string
			value float64
		}{
			{"database_active_connections", float64(activeConnections)},
			{"database_connection_limit", float64(connectionLimit)},
			{"disk_total_bytes", float64(total)},
			{"disk_system_bytes", float64(system)},
			{"disk_database_bytes", float64(database)},
			{"disk_logs_bytes", float64(logsBytes)},
			{"disk_free_bytes", float64(free)},
		}
		for _, value := range values {
			if wants(value.name) {
				points = append(points, MetricPointView{Timestamp: at, Name: value.name, Value: value.value})
			}
		}
		if at.After(limit.Add(-query.Step)) {
			break
		}
	}
	return points
}

func historicalInfrastructureEventTime(event events.Event) (time.Time, bool) {
	switch event := event.(type) {
	case events.ServerProvisioningStarted:
		return event.StartedAt, true
	case events.ServerRemoved:
		return event.RemovedAt, true
	case events.DatabaseCreated:
		return event.CreatedAt, true
	case events.DatabaseDeleted:
		return event.DeletedAt, true
	case events.DatabaseConnectionOpened:
		return event.OpenedAt, true
	case events.DatabaseConnectionReleased:
		return event.ReleasedAt, true
	case events.SiteDatabaseChanged:
		return event.ChangedAt, true
	case events.SiteStarted:
		return event.StartedAt, true
	case events.DatabaseStorageIncreased:
		return event.IncreasedAt, true
	case events.DatabaseRestoreCompleted:
		return event.CompletedAt, true
	case events.DiskLogsIncreased:
		return event.IncreasedAt, true
	case events.DiskLogsCleaned:
		return event.CleanedAt, true
	default:
		return time.Time{}, false
	}
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
			requestStates[event.RequestID] = PageRequestState{ID: event.RequestID, Source: event.Source, VisitorID: event.VisitorID,
				Page: event.Page, ProductID: event.ProductID, SourceIP: event.SourceIP, UserAgent: event.UserAgent,
				RegionCode: event.RegionCode, LoadUnits: event.LoadUnits, StartedAt: event.StartedAt}
			requestOrder = append(requestOrder, event.RequestID)
		case events.FirewallRequestEvaluated:
			request := requestStates[event.RequestID]
			request.FirewallAction, request.FirewallRuleID = event.Action, event.MatchedRuleID
			requestStates[event.RequestID] = request
		case events.PageRequestAccepted:
			request := requestStates[event.RequestID]
			request.ServerID, request.ReleasesAt, request.FirewallRuleID = event.ServerID, event.ReleasesAt, event.FirewallRuleID
			requestStates[event.RequestID] = request
		case events.PageRequestCompleted:
			request := requestStates[event.RequestID]
			request.StatusCode, request.ErrorCode, request.Message = event.StatusCode, event.ErrorCode, event.Message
			request.CompletedAt = event.CompletedAt
			request.Latency = event.Latency
			request.FirewallRuleID = event.FirewallRuleID
			requestStates[event.RequestID] = request
		case events.PageRequestRejected:
			request := requestStates[event.RequestID]
			request.StatusCode, request.ErrorCode, request.Message = event.StatusCode, event.ErrorCode, event.Message
			request.CompletedAt = event.RejectedAt
			request.FirewallRuleID = event.FirewallRuleID
			requestStates[event.RequestID] = request
		}
	}
	all := make([]RequestLogView, 0, len(requestOrder))
	for _, requestID := range requestOrder {
		request := requestStates[requestID]
		all = append(all, RequestLogView{Entry: logs.Entry{Timestamp: request.CompletedAt, RequestID: request.ID,
			Source: request.Source, VisitorID: request.VisitorID, Page: request.Page, ProductID: request.ProductID,
			SourceIP: request.SourceIP, UserAgent: request.UserAgent, RegionCode: request.RegionCode,
			FirewallRuleID: request.FirewallRuleID, StatusCode: request.StatusCode, ErrorCode: request.ErrorCode,
			Message: request.Message}, Latency: request.Latency, LoadUnits: request.LoadUnits, ServerID: request.ServerID})
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
		hasError := item.Entry.ErrorCode != ""
		if !query.From.IsZero() && item.Entry.Timestamp.Before(query.From) || !query.To.IsZero() && item.Entry.Timestamp.After(query.To) ||
			query.Page != "" && item.Entry.Page != query.Page || query.StatusCode != 0 && item.Entry.StatusCode != query.StatusCode ||
			query.HasError != nil && hasError != *query.HasError || query.ErrorCode != "" && item.Entry.ErrorCode != query.ErrorCode {
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

func (q *QueryService) Inbox(runID string, query InboxQuery) (InboxView, error) {
	_, state, err := q.records(runID)
	if err != nil {
		return InboxView{}, err
	}
	return NewProjection(nil, state).Inbox(query)
}

func (p Projection) Inbox(query InboxQuery) (InboxView, error) {
	start, err := decodeInboxCursor(query.Cursor)
	if err != nil {
		return InboxView{}, err
	}
	messages := make([]InboxMessageView, 0, len(p.state.InboxMessages))
	for _, message := range p.state.InboxMessages {
		messages = append(messages, InboxMessageView{MessageID: message.ID, SenderEmail: message.SenderEmail,
			SentAt: message.SentAt, Subject: message.Subject, Description: message.Description})
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].SentAt.Equal(messages[j].SentAt) {
			return messages[i].MessageID < messages[j].MessageID
		}
		return messages[i].SentAt.Before(messages[j].SentAt)
	})
	if start > len(messages) {
		return InboxView{}, fmt.Errorf("%w: invalid inbox cursor", ErrInvalidCommand)
	}
	end := len(messages)
	if query.Limit > 0 && start+query.Limit < end {
		end = start + query.Limit
	}
	result := InboxView{Messages: append([]InboxMessageView(nil), messages[start:end]...)}
	if end < len(messages) {
		result.NextCursor = encodeInboxCursor(end)
	}
	if len(result.Messages) == 0 {
		result.Messages = nil
	}
	return result, nil
}

func encodeInboxCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("inbox-v1:" + strconv.Itoa(offset)))
}

func decodeInboxCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	const prefix = "inbox-v1:"
	if err != nil || len(payload) <= len(prefix) || string(payload[:len(prefix)]) != prefix {
		return 0, fmt.Errorf("%w: invalid inbox cursor", ErrInvalidCommand)
	}
	offset, err := strconv.Atoi(string(payload[len(prefix):]))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%w: invalid inbox cursor", ErrInvalidCommand)
	}
	return offset, nil
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
	v := ResourcesView{}
	ids := make([]string, 0, len(state.Servers))
	for id := range state.Servers {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := state.Servers[model.ServerID(id)]
		capacity := s.CapacityUnits
		if s.Role == model.ServerRoleDatabase {
			capacity = int64(s.ConnectionLimit)
		}
		databaseIDs := make([]model.DatabaseID, 0)
		for databaseID, database := range state.Databases {
			if database.ServerID == s.ID {
				databaseIDs = append(databaseIDs, databaseID)
			}
		}
		sort.Slice(databaseIDs, func(i, j int) bool { return databaseIDs[i] < databaseIDs[j] })
		sv := ServerResourceView{ServerID: s.ID, Name: s.Name, Role: s.Role, InstanceType: s.InstanceType,
			Status: s.Status, CapacityUnits: capacity, CostPerHourMinor: serverCostPerHour(s), Disk: diskUsage(state, s.ID),
			DatabaseIDs: databaseIDs, CredentialID: s.CredentialID}
		if s.Role == model.ServerRoleDatabase {
			for _, databaseID := range databaseIDs {
				sv.UsedLoadUnits += int64(state.DatabaseConnectionCounts[databaseID])
			}
		} else {
			for _, a := range state.Capacity {
				if a.ServerID == s.ID && a.ReleasesAt.After(state.Clock.CurrentTime) {
					sv.UsedLoadUnits += a.LoadUnits
				}
			}
		}
		if s.Status == model.ServerActive && s.Role != model.ServerRoleDatabase && !serverDiskFull(state, s) {
			for _, page := range []model.PageType{model.PageProductList, model.PageProduct} {
				sv.UsedLoadUnits += ddosLoadPerActiveServer(state, page, state.Clock.CurrentTime)
			}
		}
		if s.Role != model.ServerRoleDatabase {
			v.UsedLoadUnits += sv.UsedLoadUnits
		}
		if s.Status == model.ServerActive && s.Role != model.ServerRoleDatabase {
			v.ActiveInstances++
			v.TotalCapacityUnits += s.CapacityUnits
		}
		if s.Status == model.ServerActive || s.Status == model.ServerDraining {
			v.TotalCostPerHourMinor += sv.CostPerHourMinor
		}
		v.Servers = append(v.Servers, sv)
	}
	if len(v.Servers) == 0 {
		v.Servers = nil
	}
	return v
}

func serverCostPerHour(server ServerState) int64 {
	if server.CostPerHourMinor > 0 {
		return server.CostPerHourMinor
	}
	if server.CostPerMonthMinor <= 0 {
		return 0
	}
	return (server.CostPerMonthMinor + 30*24 - 1) / (30 * 24)
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
