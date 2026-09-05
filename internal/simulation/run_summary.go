package simulation

import (
	"context"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// RunSummary contains only the public values needed by the run list. It is a
// disposable read model, never an aggregate snapshot or a replay checkpoint.
type RunSummary struct {
	Seed              int64
	EventCount        uint64
	AgentRequestCount int
	RealStartedAt     time.Time
	RealCompletedAt   time.Time
	Overview          RunListOverview
}

type RunListOverview struct {
	RunStatus        string
	SiteStatus       string
	SimulationTime   time.Time
	SimulationEndsAt time.Time
	Costs            CostsView
	Availability     AvailabilityView
}

type CachedRunSummary struct {
	FormatVersion int
	JournalStamp  string
	Summary       RunSummary
}

type RunSummaryRepository interface {
	GetRunSummary(context.Context, string) (CachedRunSummary, bool, error)
	PutRunSummary(context.Context, string, CachedRunSummary) error
}

// RunSummaryProjection folds committed events into a small list projection.
// It retains infrastructure and outstanding allocations, but no visitor
// history, schedules, credentials, command receipts, or completed requests.
// Only View() is persisted; the reducer itself is reconstructed from events.
type RunSummaryProjection struct {
	seed                int64
	version             uint64
	status              RunStatus
	clock               ClockState
	site                SiteState
	costs               CostsState
	servers             map[model.ServerID]ServerState
	databases           map[model.DatabaseID]summaryDatabase
	backupRate          int64
	attacks             map[model.AttackID]struct{}
	requestLoads        map[model.RequestID]int64
	capacity            map[model.RequestID]CapacityAllocationState
	lastErrorAt         time.Time
	backendUnavailable  bool
	firewallUnavailable bool
}

type summaryDatabase struct {
	serverID    model.ServerID
	ready       bool
	unavailable bool
}

func NewRunSummaryProjection() *RunSummaryProjection {
	return &RunSummaryProjection{
		status: RunNotCreated, site: SiteState{Status: model.SiteRunning},
		servers: make(map[model.ServerID]ServerState), databases: make(map[model.DatabaseID]summaryDatabase),
		attacks: make(map[model.AttackID]struct{}), requestLoads: make(map[model.RequestID]int64),
		capacity: make(map[model.RequestID]CapacityAllocationState),
	}
}

// Apply consumes facts already accepted into the journal. Aggregate validation
// remains the responsibility of the simulation, not this public read model.
func (p *RunSummaryProjection) Apply(record StoredEvent) {
	p.version = record.Version
	switch e := record.Event.(type) {
	case events.WorldCreated:
		p.seed, p.status = e.Seed, RunRunning
		p.clock = ClockState{StartedAt: e.StartedAt, CurrentTime: e.StartedAt, EndsAt: e.EndsAt}
	case events.TimeAdvanced:
		p.pruneCapacity(e.From)
		p.clock.CurrentTime = e.To
	case events.RunEnded:
		p.status = RunCompleted
	case events.CostsConfigured:
		p.costs.Currency = e.Currency
	case events.InfrastructureCostAccrued:
		p.costs.ServerCostMinor += e.AmountMinor
	case events.BackupStorageCostAccrued:
		p.costs.BackupStorageCostMinor += e.AmountMinor
	case events.DatabaseBackupCompleted:
		p.backupRate += e.StorageCostPerHourMinor
	case events.ServerProvisioningStarted:
		p.servers[e.ServerID] = ServerState{Role: e.Role, Status: model.ServerProvisioning,
			CapacityUnits: e.CapacityUnits, CostPerHourMinor: e.CostPerHourMinor, CostPerMonthMinor: e.CostPerMonthMinor}
	case events.ServerActivated:
		server := p.servers[e.ServerID]
		server.Status = model.ServerActive
		p.servers[e.ServerID] = server
	case events.ServerDrainingStarted:
		server := p.servers[e.ServerID]
		server.Status = model.ServerDraining
		p.servers[e.ServerID] = server
	case events.ServerRemoved:
		delete(p.servers, e.ServerID)
		for id, database := range p.databases {
			if database.serverID == e.ServerID {
				delete(p.databases, id)
			}
		}
	case events.ServerProvisioningFailed:
		delete(p.servers, e.ServerID)
	case events.DatabaseCreated:
		p.databases[e.DatabaseID] = summaryDatabase{serverID: e.ServerID, ready: true}
	case events.DatabaseDeleted:
		delete(p.databases, e.DatabaseID)
	case events.DatabaseRestoreStarted:
		database := p.databases[e.DatabaseID]
		database.ready = false
		p.databases[e.DatabaseID] = database
	case events.DatabaseRestoreFailed:
		database := p.databases[e.DatabaseID]
		database.ready = false
		p.databases[e.DatabaseID] = database
	case events.DatabaseRestoreCompleted:
		database := p.databases[e.DatabaseID]
		database.ready = true
		p.databases[e.DatabaseID] = database
	case events.DatabaseStorageIncreased:
		database := p.databases[e.DatabaseID]
		database.ready = true
		p.databases[e.DatabaseID] = database
	case events.DatabaseAvailabilityChanged:
		database := p.databases[e.DatabaseID]
		database.unavailable = !e.Available
		p.databases[e.DatabaseID] = database
		if p.site.DatabaseID == e.DatabaseID && p.site.Status == model.SiteRunning {
			p.availabilityChanged(e.Available, e.ChangedAt)
		}
	case events.BackendAvailabilityChanged:
		p.backendUnavailable = !e.Available
		if p.site.Status == model.SiteRunning {
			p.availabilityChanged(e.Available, e.ChangedAt)
		}
	case events.FirewallAvailabilityChanged:
		p.firewallUnavailable = !e.Available
		if p.site.Status == model.SiteRunning {
			p.availabilityChanged(e.Available, e.ChangedAt)
		}
	case events.SiteStopStarted:
		p.site.Status = model.SiteStopping
		p.availabilityChanged(false, e.StartedAt)
	case events.SiteStopped:
		p.site.Status = model.SiteStopped
	case events.SiteDatabaseChanged:
		p.site.DatabaseID = e.DatabaseID
	case events.SiteStarted:
		p.site.Status = model.SiteRunning
		p.availabilityChanged(true, e.StartedAt)
	case events.TrafficAttackStarted:
		p.attacks[e.AttackID] = struct{}{}
	case events.TrafficAttackEnded:
		delete(p.attacks, e.AttackID)
	case events.PageRequestStarted:
		p.pruneCapacity(e.StartedAt)
		p.requestLoads[e.RequestID] = e.LoadUnits
	case events.PageRequestAccepted:
		p.capacity[e.RequestID] = CapacityAllocationState{LoadUnits: p.requestLoads[e.RequestID], ReleasesAt: e.ReleasesAt}
		delete(p.requestLoads, e.RequestID)
	case events.PageRequestCompleted:
		delete(p.requestLoads, e.RequestID)
		p.recordError(e.StatusCode, e.CompletedAt)
	case events.PageRequestRejected:
		delete(p.requestLoads, e.RequestID)
		p.recordError(e.StatusCode, e.RejectedAt)
	}
}

func (p *RunSummaryProjection) recordError(status int, at time.Time) {
	if status >= 400 && at.After(p.lastErrorAt) {
		p.lastErrorAt = at
	}
}

func (p *RunSummaryProjection) pruneCapacity(at time.Time) {
	for id, allocation := range p.capacity {
		if !allocation.ReleasesAt.After(at) {
			delete(p.capacity, id)
		}
	}
}

func (p *RunSummaryProjection) dependenciesAvailable() bool {
	if p.backendUnavailable || p.firewallUnavailable {
		return false
	}
	if p.site.DatabaseID == "" {
		return true
	}
	database, exists := p.databases[p.site.DatabaseID]
	return exists && database.ready && !database.unavailable
}

func (p *RunSummaryProjection) availabilityChanged(available bool, at time.Time) {
	if !available && p.site.UnavailableSince.IsZero() {
		p.site.UnavailableSince = at
	}
	if available && p.dependenciesAvailable() && !p.site.UnavailableSince.IsZero() {
		p.site.DowntimeDuration += at.Sub(p.site.UnavailableSince)
		p.site.UnavailableSince = time.Time{}
	}
}

func (p *RunSummaryProjection) View(agentRequests int) RunSummary {
	var capacity, used, hourlyCost int64
	var serverCount int
	for _, server := range p.servers {
		if server.Status == model.ServerActive && server.Role != model.ServerRoleDatabase {
			serverCount++
			capacity += server.CapacityUnits
		}
		if server.Status == model.ServerActive || server.Status == model.ServerDraining {
			hourlyCost += serverCostPerHour(server)
		}
	}
	for _, allocation := range p.capacity {
		if allocation.ReleasesAt.After(p.clock.CurrentTime) {
			used += allocation.LoadUnits
		}
	}
	siteStatus := "healthy"
	if p.status == RunCompleted || p.site.Status != model.SiteRunning || !p.dependenciesAvailable() {
		siteStatus = "unavailable"
	} else if (!p.lastErrorAt.IsZero() && !p.lastErrorAt.Before(p.clock.CurrentTime.Add(-5*time.Minute)) && !p.lastErrorAt.After(p.clock.CurrentTime)) ||
		len(p.attacks) > 0 || serverCount == 0 || (capacity > 0 && used >= capacity) {
		siteStatus = "degraded"
	}
	return RunSummary{Seed: p.seed, EventCount: p.version, AgentRequestCount: agentRequests,
		Overview: RunListOverview{RunStatus: string(p.status), SiteStatus: siteStatus,
			SimulationTime: p.clock.CurrentTime, SimulationEndsAt: p.clock.EndsAt,
			Costs: CostsView{Currency: p.costs.Currency, ServerCostMinor: p.costs.ServerCostMinor,
				BackupStorageCostMinor:  p.costs.BackupStorageCostMinor,
				TotalCostMinor:          p.costs.ServerCostMinor + p.costs.BackupStorageCostMinor,
				CurrentCostPerHourMinor: hourlyCost + p.backupRate},
			Availability: availabilityView(State{Status: p.status, Clock: p.clock, Site: p.site})}}
}
