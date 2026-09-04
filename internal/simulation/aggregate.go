package simulation

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func Rehydrate(records []StoredEvent) (State, error) {
	state := NewState()
	for _, record := range records {
		if record.Version != state.Version+1 {
			return State{}, fmt.Errorf("%w: expected version %d, got %d", ErrInvalidEvent, state.Version+1, record.Version)
		}
		if err := state.Apply(record.Event); err != nil {
			return State{}, err
		}
		state.Version = record.Version
	}
	state.pruneDerivedState(state.Clock.CurrentTime)
	return state, nil
}

func Decide(runID string, state State, command Command) ([]events.Event, error) {
	if commandID, payload := commandReceipt(command); commandID != "" {
		if previous, exists := state.CommandPayloads[commandID]; exists {
			if previous != payload {
				return nil, ErrIdempotencyConflict
			}
			return nil, nil
		}
	}
	switch command := command.(type) {
	case CreateWorld:
		if state.Status != RunNotCreated {
			return nil, ErrWorldAlreadyCreated
		}
		if runID == "" || command.Seed == 0 || command.StartedAt.IsZero() || !command.EndsAt.After(command.StartedAt) {
			return nil, fmt.Errorf("%w: invalid world parameters", ErrInvalidCommand)
		}
		return []events.Event{events.WorldCreated{
			RunID:     runID,
			Seed:      command.Seed,
			StartedAt: command.StartedAt,
			EndsAt:    command.EndsAt,
		}}, nil

	case AddProduct:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.ProductID == "" || strings.TrimSpace(command.Name) == "" || command.PriceMinor <= 0 {
			return nil, fmt.Errorf("%w: invalid product", ErrInvalidCommand)
		}
		if command.ViewProbabilityPPM > ProbabilityScale {
			return nil, fmt.Errorf("%w: probability is outside [0, %d]", ErrInvalidCommand, ProbabilityScale)
		}
		var totalViewProbability uint64
		for _, product := range state.Products {
			totalViewProbability += uint64(product.ViewProbabilityPPM)
		}
		totalViewProbability += uint64(command.ViewProbabilityPPM)
		if totalViewProbability > uint64(ProbabilityScale) {
			return nil, fmt.Errorf("%w: total product view probability exceeds %d", ErrInvalidCommand, ProbabilityScale)
		}
		if _, exists := state.Products[command.ProductID]; exists {
			return nil, ErrProductAlreadyExists
		}
		return []events.Event{events.ProductAdded{
			ProductID:          command.ProductID,
			Name:               command.Name,
			PriceMinor:         command.PriceMinor,
			ViewProbabilityPPM: command.ViewProbabilityPPM,
			AddedAt:            state.Clock.CurrentTime,
		}}, nil

	case AdvanceTime:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.RealElapsed < 0 || command.RequestedDuration < 0 ||
			(command.RequestedDuration > 0 && command.RequestedDuration < MinExplicitAdvance) ||
			(command.StopOnLogError && command.RequestedDuration < MinExplicitAdvance) {
			return nil, fmt.Errorf("%w: explicit duration must be zero or at least %s; durations cannot be negative", ErrInvalidCommand, MinExplicitAdvance)
		}
		applied := max(command.RequestedDuration, command.RealElapsed)
		if applied == 0 {
			return nil, nil
		}
		remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
		if applied > remaining {
			applied = remaining
		}
		to := state.Clock.CurrentTime.Add(applied)
		if command.StopOnLogError && command.stopAt.IsZero() {
			previewCommand := command
			previewCommand.CommandID = ""
			previewCommand.StopOnLogError = false
			previewCommand.previewLogErrors = true
			preview, err := Decide(runID, state, previewCommand)
			if err != nil {
				return nil, err
			}
			if stopAt, found := firstLogErrorTime(preview); found {
				command.stopAt = stopAt
			}
		}
		if !command.stopAt.IsZero() {
			if !command.stopAt.After(state.Clock.CurrentTime) || command.stopAt.After(to) {
				return nil, fmt.Errorf("%w: log-error stop time is outside the advance interval", ErrInvalidCommand)
			}
			to = command.stopAt
			applied = to.Sub(state.Clock.CurrentTime)
		}
		working := cloneState(state)
		result := make([]events.Event, 0)
		appendEvents := func(items ...events.Event) error {
			for _, event := range items {
				if err := working.Apply(event); err != nil {
					return err
				}
				result = append(result, event)
			}
			return nil
		}
		if err := appendEvents(events.TimeAdvanced{
			CommandID:         command.CommandID,
			From:              state.Clock.CurrentTime,
			To:                to,
			RealElapsed:       command.RealElapsed,
			RequestedDuration: command.RequestedDuration,
			AppliedDuration:   applied,
			StopOnLogError:    command.StopOnLogError,
		}); err != nil {
			return nil, err
		}
		firewallExpiries := firewallExpiryEvents(state, state.Clock.CurrentTime, to)
		firewallExpiryIndex := 0
		firewallBoundaries := firewallExpiryBoundaries(state, state.Clock.CurrentTime, to)
		firewallBoundaryIndex := 0
		availabilityCursor := state.Clock.CurrentTime
		recordBackendAvailability := func(at time.Time) error {
			items, err := appendBackendAvailabilityChangeToWorking(nil, &working, at)
			if err != nil {
				return err
			}
			result = append(result, items...)
			return nil
		}
		processAvailabilityBoundaries := func(until time.Time) error {
			for {
				next := time.Time{}
				if firewallBoundaryIndex < len(firewallBoundaries) {
					boundary := firewallBoundaries[firewallBoundaryIndex]
					if !boundary.After(until) {
						next = boundary
					}
				}
				for _, allocation := range working.Capacity {
					if !allocation.ReleasesAt.After(availabilityCursor) || allocation.ReleasesAt.After(until) {
						continue
					}
					if next.IsZero() || allocation.ReleasesAt.Before(next) {
						next = allocation.ReleasesAt
					}
				}
				if next.IsZero() {
					break
				}
				for firewallExpiryIndex < len(firewallExpiries) {
					expiry := firewallExpiries[firewallExpiryIndex].(events.FirewallAvailabilityChanged)
					if !expiry.ChangedAt.Equal(next) {
						break
					}
					if err := appendEvents(expiry); err != nil {
						return err
					}
					firewallExpiryIndex++
				}
				for firewallBoundaryIndex < len(firewallBoundaries) && firewallBoundaries[firewallBoundaryIndex].Equal(next) {
					firewallBoundaryIndex++
				}
				if err := recordBackendAvailability(next); err != nil {
					return err
				}
				availabilityCursor = next
			}
			if until.After(availabilityCursor) {
				availabilityCursor = until
			}
			return nil
		}
		releaseConnections := func(until time.Time) error {
			expiredConnections := make([]DatabaseConnectionState, 0)
			for _, connection := range working.DatabaseConnections {
				if !connection.ReleasesAt.After(until) {
					expiredConnections = append(expiredConnections, connection)
				}
			}
			sort.Slice(expiredConnections, func(i, j int) bool {
				if expiredConnections[i].ReleasesAt.Equal(expiredConnections[j].ReleasesAt) {
					return expiredConnections[i].RequestID < expiredConnections[j].RequestID
				}
				return expiredConnections[i].ReleasesAt.Before(expiredConnections[j].ReleasesAt)
			})
			lastReleasedAt := time.Time{}
			for _, connection := range expiredConnections {
				database := working.Databases[connection.DatabaseID]
				_, wasFull := database.AvailabilityReasons[model.DatabaseUnavailableConnectionLimit]
				if err := appendEvents(events.DatabaseConnectionReleased{RequestID: connection.RequestID, DatabaseID: connection.DatabaseID,
					ServerID: connection.ServerID, ReleasedAt: connection.ReleasesAt}); err != nil {
					return err
				}
				lastReleasedAt = connection.ReleasesAt
				if wasFull {
					database = working.Databases[connection.DatabaseID]
					reasons := reasonsWith(database, model.DatabaseUnavailableConnectionLimit, false)
					if err := appendEvents(events.DatabaseAvailabilityChanged{DatabaseID: database.ID, ServerID: database.ServerID,
						Available: len(reasons) == 0, Reasons: reasons, ChangedAt: connection.ReleasesAt}); err != nil {
						return err
					}
				}
			}
			if working.Site.Status == model.SiteStopping && activeDatabaseConnections(working) == 0 {
				stoppedAt := until
				if !lastReleasedAt.IsZero() {
					stoppedAt = lastReleasedAt
				}
				operationID := working.Site.OperationID
				if err := appendEvents(events.SiteStopped{OperationID: operationID, StoppedAt: stoppedAt},
					events.OperationSucceeded{OperationID: operationID, CompletedAt: to}); err != nil {
					return err
				}
			}
			return nil
		}
		if err := appendEvents(infrastructureCostEvents(working, state.Clock.CurrentTime, to)...); err != nil {
			return nil, err
		}
		if err := appendEvents(backupStorageCostEvents(working, state.Clock.CurrentTime, to)...); err != nil {
			return nil, err
		}
		// Events inside the interval use the existing infrastructure. Server
		// lifecycle transitions becoming ready are committed at its end.
		pending := pendingScheduledEvents(state, to)
		logErrorSeen := false
		for index, scheduled := range pending {
			if err := processAvailabilityBoundaries(scheduled.OccursAt); err != nil {
				return nil, err
			}
			if err := releaseConnections(scheduled.OccursAt); err != nil {
				return nil, err
			}
			if err := appendEvents(scheduled.Event); err != nil {
				return nil, err
			}
			if growth, ok := scheduled.Event.(events.DatabaseGrowthRequested); ok && working.Site.Status == model.SiteRunning {
				if err := appendEvents(decidePendingGrowth(working, growth.GrowthID, growth.RequestedAt)...); err != nil {
					return nil, err
				}
			}
			if growth, ok := scheduled.Event.(events.DiskLogsGrowthRequested); ok {
				if err := appendEvents(decideDiskLogsGrowth(working, growth)...); err != nil {
					return nil, err
				}
			}
			if rotation, ok := scheduled.Event.(events.ServerCredentialRotationRequested); ok {
				if err := appendEvents(decideScheduledServerCredentialRotation(working, rotation)...); err != nil {
					return nil, err
				}
			}
			if arrival, ok := scheduled.Event.(events.VisitorArrived); ok && len(working.Products) > 0 {
				journey, err := decideVisitorMutable(&working, arrival.VisitorID, arrival.ArrivedAt, false)
				if err != nil {
					return nil, err
				}
				result = append(result, journey...)
				if _, found := firstLogErrorTime(journey); found {
					logErrorSeen = true
				}
			}
			if err := recordBackendAvailability(scheduled.OccursAt); err != nil {
				return nil, err
			}
			nextHasSameTime := index+1 < len(pending) && pending[index+1].OccursAt.Equal(scheduled.OccursAt)
			if command.previewLogErrors && logErrorSeen && !nextHasSameTime {
				return result, nil
			}
		}
		if err := processAvailabilityBoundaries(to); err != nil {
			return nil, err
		}
		if err := releaseConnections(to); err != nil {
			return nil, err
		}
		completedOperations := make(map[model.OperationID]bool)
		for _, server := range sortedProvisioningServers(working) {
			if server.ReadyAt.After(to) {
				continue
			}
			if err := appendEvents(events.ServerActivated{
				OperationID: server.OperationID,
				ServerID:    server.ID,
				ActivatedAt: to,
			}); err != nil {
				return nil, err
			}
			completedOperations[server.OperationID] = true
		}
		for operationID := range completedOperations {
			if err := appendEvents(events.OperationSucceeded{OperationID: operationID, CompletedAt: to}); err != nil {
				return nil, err
			}
		}
		if err := recordBackendAvailability(to); err != nil {
			return nil, err
		}
		removedOperations := make(map[model.OperationID]bool)
		for _, server := range sortedDrainingServers(working) {
			if serverHasLoad(working, server.ID, to) {
				continue
			}
			if database, hosted := databaseOnServer(working, server.ID); hosted {
				if err := appendEvents(events.DatabaseDeleted{DatabaseID: database.ID, ServerID: server.ID, DeletedAt: to}); err != nil {
					return nil, err
				}
			}
			if err := appendEvents(events.ServerRemoved{
				OperationID: server.OperationID,
				ServerID:    server.ID,
				RemovedAt:   to,
			}); err != nil {
				return nil, err
			}
			removedOperations[server.OperationID] = true
		}
		for operationID := range removedOperations {
			allRemoved := true
			for _, server := range working.Servers {
				if server.OperationID == operationID && server.Status == model.ServerDraining && serverHasLoad(working, server.ID, to) {
					allRemoved = false
				}
			}
			if allRemoved {
				if err := appendEvents(events.OperationSucceeded{OperationID: operationID, CompletedAt: to}); err != nil {
					return nil, err
				}
			}
		}
		if err := recordBackendAvailability(to); err != nil {
			return nil, err
		}
		if to.Equal(state.Clock.EndsAt) {
			if err := appendEvents(events.RunEnded{CompletedAt: to, Reason: "world_completed"}); err != nil {
				return nil, err
			}
		}
		return result, nil

	case SynchronizeRealTime:
		return Decide(runID, state, AdvanceTime{CommandID: command.CommandID, RealElapsed: command.RealElapsed})

	case OpenPage:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.RequestID == "" || command.VisitorID == "" || !validPage(command.Page) {
			return nil, fmt.Errorf("%w: invalid page request", ErrInvalidCommand)
		}
		if _, exists := state.SeenRequests[command.RequestID]; exists {
			return nil, fmt.Errorf("%w: request %q already exists", ErrInvalidCommand, command.RequestID)
		}
		if command.Page != model.PageProductList {
			if _, exists := state.Products[command.ProductID]; !exists {
				return nil, ErrProductNotFound
			}
		}
		pageConfig := state.Pages[command.Page]
		profile, err := requestClientProfile(state, command)
		if err != nil {
			return nil, err
		}

		result := []events.Event{events.PageRequestStarted{
			RequestID: command.RequestID, Source: model.RequestSourceVisitor, VisitorID: command.VisitorID,
			Page: command.Page, ProductID: command.ProductID, SourceIP: profile.SourceIP,
			UserAgent: profile.UserAgent, RegionCode: profile.RegionCode,
			LoadUnits: pageConfig.LoadUnits, StartedAt: state.Clock.CurrentTime,
		}}
		decision := evaluateFirewall(state, profile, state.Clock.CurrentTime)
		result = append(result, events.FirewallRequestEvaluated{RequestID: command.RequestID, SourceIP: profile.SourceIP,
			UserAgent: profile.UserAgent, RegionCode: profile.RegionCode, MatchedRuleID: decision.RuleID,
			MatchedRuleRevision: decision.Revision, Action: decision.Action, EvaluatedAt: state.Clock.CurrentTime})
		if decision.Action == model.FirewallDeny {
			return append(result, events.PageRequestRejected{RequestID: command.RequestID, FirewallRuleID: decision.RuleID,
				StatusCode: 403, ErrorCode: model.FailureFirewallDenied, Message: "request denied by firewall",
				RejectedAt: state.Clock.CurrentTime}), nil
		}
		databaseID := state.Site.DatabaseID
		var database DatabaseState
		var databaseServer ServerState
		if databaseID != "" {
			if state.Site.Status != model.SiteRunning {
				return append(result, events.PageRequestRejected{RequestID: command.RequestID, DatabaseID: databaseID, FirewallRuleID: decision.RuleID, StatusCode: 503,
					ErrorCode: model.FailureSiteUnavailable, Message: "site is stopped", RejectedAt: state.Clock.CurrentTime}), nil
			}
			var exists bool
			database, exists = state.Databases[databaseID]
			databaseServer, _ = state.Servers[database.ServerID]
			if !exists || !database.Ready {
				return append(result, events.PageRequestRejected{RequestID: command.RequestID, DatabaseID: databaseID, FirewallRuleID: decision.RuleID, StatusCode: 503,
					ErrorCode: model.FailureDatabaseUnavailable, Message: "database is unavailable", RejectedAt: state.Clock.CurrentTime}), nil
			}
			if _, full := database.AvailabilityReasons[model.DatabaseUnavailableDiskFull]; full {
				return append(result, events.PageRequestRejected{RequestID: command.RequestID, DatabaseID: databaseID, ServerID: database.ServerID, FirewallRuleID: decision.RuleID, StatusCode: 500,
					ErrorCode: model.FailureDiskFull, Message: "database disk is full", RejectedAt: state.Clock.CurrentTime}), nil
			}
			active := state.DatabaseConnectionCounts[databaseID]
			if active >= databaseServer.ConnectionLimit {
				return append(result,
					events.DatabaseConnectionRejected{RequestID: command.RequestID, DatabaseID: databaseID, ServerID: database.ServerID, ActiveConnections: active,
						ConnectionLimit: databaseServer.ConnectionLimit, RejectedAt: state.Clock.CurrentTime},
					events.PageRequestRejected{RequestID: command.RequestID, DatabaseID: databaseID, ServerID: database.ServerID, FirewallRuleID: decision.RuleID, StatusCode: 500,
						ErrorCode: model.FailureDBConnectionLimit, Message: fmt.Sprintf("database connection limit exceeded: active=%d limit=%d", active, databaseServer.ConnectionLimit), RejectedAt: state.Clock.CurrentTime}), nil
			}
		}
		serverID := model.ServerID("")
		if pageConfig.LoadUnits > 0 {
			server, available, accepted := selectServer(state, command.Page, pageConfig.LoadUnits, state.Clock.CurrentTime)
			if !accepted {
				if allActiveBackendsDiskFull(state) {
					return append(result, events.PageRequestRejected{
						RequestID: command.RequestID, FirewallRuleID: decision.RuleID, StatusCode: 500,
						ErrorCode: model.FailureDiskFull, Message: "all active backend disks are full", RejectedAt: state.Clock.CurrentTime,
					}), nil
				}
				message := fmt.Sprintf("server capacity exceeded: required=%d available=%d", pageConfig.LoadUnits, available)
				return append(result, events.PageRequestRejected{
					RequestID: command.RequestID, FirewallRuleID: decision.RuleID, StatusCode: 500,
					ErrorCode: model.FailureServerCapacityExceeded, Message: message, RejectedAt: state.Clock.CurrentTime,
				}), nil
			}
			serverID = server.ID
			result = append(result, events.PageRequestAccepted{
				RequestID: command.RequestID, ServerID: server.ID, DatabaseID: databaseID, FirewallRuleID: decision.RuleID,
				AcceptedAt: state.Clock.CurrentTime, ReleasesAt: state.Clock.CurrentTime.Add(pageConfig.HoldDuration),
			})
		}
		if databaseID != "" {
			result = append(result, events.DatabaseConnectionOpened{RequestID: command.RequestID, DatabaseID: databaseID, ServerID: database.ServerID,
				OpenedAt: state.Clock.CurrentTime, ReleasesAt: state.Clock.CurrentTime.Add(databaseServer.ConnectionHold)})
			if state.DatabaseConnectionCounts[databaseID]+1 == databaseServer.ConnectionLimit {
				reasons := reasonsWith(database, model.DatabaseUnavailableConnectionLimit, true)
				result = append(result, events.DatabaseAvailabilityChanged{DatabaseID: databaseID, ServerID: database.ServerID, Available: false, Reasons: reasons, ChangedAt: state.Clock.CurrentTime})
			}
		}

		result = append(result, events.PageRequestCompleted{
			RequestID: command.RequestID, ServerID: serverID, DatabaseID: databaseID, FirewallRuleID: decision.RuleID,
			StatusCode: 200, Latency: pageConfig.BaseLatency, CompletedAt: state.Clock.CurrentTime,
		})
		return appendBackendAvailabilityChange(state, result, state.Clock.CurrentTime)

	case AddServer:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.CommandID == "" || command.OperationID == "" || command.ServerID == "" ||
			command.CapacityUnits <= 0 || command.CostPerHourMinor < 0 {
			return nil, fmt.Errorf("%w: invalid add server command", ErrInvalidCommand)
		}
		if err := ensureNewOperationIdentifiers(state, command.CommandID, command.OperationID); err != nil {
			return nil, err
		}
		if _, exists := state.Servers[command.ServerID]; exists {
			return nil, fmt.Errorf("%w: server %q already exists", ErrInvalidCommand, command.ServerID)
		}
		provisioningDuration := state.Infrastructure.ServerProvisioningDuration
		if provisioningDuration <= 0 {
			return nil, fmt.Errorf("%w: server provisioning duration is not configured", ErrInvalidCommand)
		}
		return []events.Event{
			events.ServerCommandAccepted{CommandID: command.CommandID, OperationID: command.OperationID, Payload: serverCommandPayload(command), AcceptedAt: state.Clock.CurrentTime},
			events.OperationQueued{
				OperationID: command.OperationID,
				Kind:        model.OperationControlCommand,
				QueuedAt:    state.Clock.CurrentTime,
			},
			events.OperationStarted{
				OperationID: command.OperationID,
				StartedAt:   state.Clock.CurrentTime,
			},
			events.ServerProvisioningStarted{
				OperationID:      command.OperationID,
				ServerID:         command.ServerID,
				CapacityUnits:    command.CapacityUnits,
				CostPerHourMinor: command.CostPerHourMinor,
				StartedAt:        state.Clock.CurrentTime,
				ReadyAt:          state.Clock.CurrentTime.Add(provisioningDuration),
			},
		}, nil

	case RemoveServer:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.CommandID == "" || command.OperationID == "" || command.ServerID == "" {
			return nil, fmt.Errorf("%w: invalid remove server command", ErrInvalidCommand)
		}
		if err := ensureNewOperationIdentifiers(state, command.CommandID, command.OperationID); err != nil {
			return nil, err
		}
		server, exists := state.Servers[command.ServerID]
		if !exists {
			return nil, fmt.Errorf("%w: server %q", ErrResourceNotFound, command.ServerID)
		}
		if server.Status != model.ServerActive {
			return nil, fmt.Errorf("%w: server %q is not active", ErrServerInUse, command.ServerID)
		}
		if server.Role == model.ServerRoleDatabase {
			if database, hosted := databaseOnServer(state, server.ID); hosted {
				if state.Site.DatabaseID == database.ID {
					return nil, fmt.Errorf("%w: database server is used by site", ErrServerInUse)
				}
				if state.DatabaseConnectionCounts[database.ID] > 0 {
					return nil, fmt.Errorf("%w: database server has active connections", ErrServerInUse)
				}
				if databaseOperationInProgress(state, database.ID) {
					return nil, ErrOperationInProgress
				}
			}
		}
		managedServers := 0
		for _, candidate := range state.Servers {
			if (candidate.Role == "" || candidate.Role == model.ServerRoleBackend) &&
				(candidate.Status == model.ServerProvisioning || candidate.Status == model.ServerActive || candidate.Status == model.ServerDraining) {
				managedServers++
			}
		}
		if (server.Role == "" || server.Role == model.ServerRoleBackend) && managedServers <= MinimumBackendInstances {
			return nil, fmt.Errorf("%w: at least %d backend server must remain", ErrLastBackendRequired, MinimumBackendInstances)
		}
		result := []events.Event{
			events.ServerCommandAccepted{CommandID: command.CommandID, OperationID: command.OperationID, Payload: serverCommandPayload(command), AcceptedAt: state.Clock.CurrentTime},
			events.OperationQueued{
				OperationID: command.OperationID,
				Kind:        model.OperationControlCommand,
				QueuedAt:    state.Clock.CurrentTime,
			},
			events.OperationStarted{
				OperationID: command.OperationID,
				StartedAt:   state.Clock.CurrentTime,
			},
			events.ServerDrainingStarted{
				OperationID: command.OperationID,
				ServerID:    command.ServerID,
				StartedAt:   state.Clock.CurrentTime,
			},
		}
		if serverHasLoad(state, command.ServerID, state.Clock.CurrentTime) {
			return appendBackendAvailabilityChange(state, result, state.Clock.CurrentTime)
		}
		if database, hosted := databaseOnServer(state, command.ServerID); hosted {
			result = append(result, events.DatabaseDeleted{DatabaseID: database.ID, ServerID: command.ServerID, DeletedAt: state.Clock.CurrentTime})
		}
		result = append(result,
			events.ServerRemoved{
				OperationID: command.OperationID,
				ServerID:    command.ServerID,
				RemovedAt:   state.Clock.CurrentTime,
			},
			events.OperationSucceeded{
				OperationID: command.OperationID,
				CompletedAt: state.Clock.CurrentTime,
			},
		)
		return appendBackendAvailabilityChange(state, result, state.Clock.CurrentTime)

	case ProbePage:
		generated, err := Decide(runID, state, OpenPage{
			RequestID: command.RequestID, VisitorID: "__probe__", Page: command.Page, ProductID: command.ProductID,
		})
		if err != nil {
			return nil, err
		}
		result := make([]events.Event, 0, len(generated))
		for _, event := range generated {
			switch event := event.(type) {
			case events.PageRequestStarted:
				event.Source = model.RequestSourceProbe
				event.VisitorID = ""
				result = append(result, event)
			default:
				result = append(result, event)
			}
		}
		return result, nil

	case SimulateVisitor:
		return decideVisitor(state, command.VisitorID, state.Clock.CurrentTime, true)

	case ConfigureServerCatalog:
		return decideConfigureServerCatalog(state, command)
	case AddTypedServer:
		return decideAddTypedServer(state, command)
	case CreateDatabase:
		return decideCreateDatabase(state, command)
	case GrowDatabase:
		return decideGrowDatabase(state, command)
	case CleanupDatabaseLogs:
		return decideCleanupDatabaseLogs(state, command)
	case BackupDatabase:
		return decideBackupDatabase(state, command)
	case RestoreDatabase:
		return decideRestoreDatabase(state, command)
	case StopSite:
		return decideStopSite(state, command)
	case StartSite:
		return decideStartSite(state, command)
	case SetSiteDatabase:
		return decideSetSiteDatabase(state, command)
	case UpsertFirewallRule:
		return decideUpsertFirewallRule(state, command)
	case DeleteFirewallRule:
		return decideDeleteFirewallRule(state, command)

	case RecordControlCommandResponse:
		return decideRecordControlCommandResponse(state, command)

	case RecordControlOperationResult:
		return decideRecordControlOperationResult(state, command)

	case IssueServerCredential:
		return decideIssueServerCredential(state, command)

	default:
		return nil, fmt.Errorf("%w: unsupported command %T", ErrInvalidCommand, command)
	}
}

func validPage(page model.PageType) bool {
	return page == model.PageProductList || page == model.PageProduct
}

func commandReceipt(command Command) (model.CommandID, string) {
	switch command := command.(type) {
	case AdvanceTime:
		return command.CommandID, timeCommandPayload(command.RealElapsed, command.RequestedDuration, command.StopOnLogError)
	case SynchronizeRealTime:
		return command.CommandID, timeCommandPayload(command.RealElapsed, 0, false)
	case ProbePage:
		return model.CommandID(command.RequestID), fmt.Sprintf("probe:%s:%s", command.Page, command.ProductID)
	default:
		if commandID, payload := serverCommandReceipt(command); commandID != "" {
			return commandID, payload
		}
		if commandID, payload := firewallCommandReceipt(command); commandID != "" {
			return commandID, payload
		}
		return databaseCommandReceipt(command)
	}
}

func timeCommandPayload(realElapsed, requested time.Duration, stopOnLogError bool) string {
	if requested > 0 {
		if stopOnLogError {
			return fmt.Sprintf("advance:%d:new-log-errors=1", requested)
		}
		return fmt.Sprintf("advance:%d", requested)
	}
	return fmt.Sprintf("time:%d:0", realElapsed)
}

func firstLogErrorTime(items []events.Event) (time.Time, bool) {
	for _, item := range items {
		switch event := item.(type) {
		case events.PageRequestRejected:
			if event.ErrorCode != "" {
				return event.RejectedAt, true
			}
		case events.PageRequestCompleted:
			if event.ErrorCode != "" {
				return event.CompletedAt, true
			}
		}
	}
	return time.Time{}, false
}

func selectServer(state State, page model.PageType, required int64, at time.Time) (ServerState, int64, bool) {
	serverIDs := make([]string, 0, len(state.Servers))
	for id, server := range state.Servers {
		if server.Status == model.ServerActive && (server.Role == "" || server.Role == model.ServerRoleBackend) {
			serverIDs = append(serverIDs, string(id))
		}
	}
	sort.Strings(serverIDs)
	maxAvailable := int64(0)
	for _, rawID := range serverIDs {
		server := state.Servers[model.ServerID(rawID)]
		available := serverAvailableCapacity(state, server.ID, page, at)
		if available > maxAvailable {
			maxAvailable = available
		}
		if available >= required {
			return server, available, true
		}
	}
	return ServerState{}, maxAvailable, false
}

func sortedDrainingServers(state State) []ServerState {
	serverIDs := make([]string, 0, len(state.Servers))
	for id, server := range state.Servers {
		if server.Status == model.ServerDraining {
			serverIDs = append(serverIDs, string(id))
		}
	}
	sort.Strings(serverIDs)
	servers := make([]ServerState, 0, len(serverIDs))
	for _, rawID := range serverIDs {
		servers = append(servers, state.Servers[model.ServerID(rawID)])
	}
	return servers
}

func sortedProvisioningServers(state State) []ServerState {
	serverIDs := make([]string, 0, len(state.Servers))
	for id, server := range state.Servers {
		if server.Status == model.ServerProvisioning {
			serverIDs = append(serverIDs, string(id))
		}
	}
	sort.Strings(serverIDs)
	servers := make([]ServerState, 0, len(serverIDs))
	for _, rawID := range serverIDs {
		servers = append(servers, state.Servers[model.ServerID(rawID)])
	}
	return servers
}

func ensureNewOperationIdentifiers(state State, commandID model.CommandID, operationID model.OperationID) error {
	if _, exists := state.Commands[commandID]; exists {
		return fmt.Errorf("%w: command %q already exists", ErrInvalidCommand, commandID)
	}
	if _, exists := state.Operations[operationID]; exists {
		return fmt.Errorf("%w: operation %q already exists", ErrInvalidCommand, operationID)
	}
	return nil
}

func deterministicRoll(seed int64, parts ...string) uint32 {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, strings.Join(parts, ":"))))
	return uint32(binary.BigEndian.Uint64(digest[:8]) % uint64(ProbabilityScale))
}

func decideVisitor(state State, visitorID model.VisitorID, at time.Time, includeArrival bool) ([]events.Event, error) {
	working := cloneState(state)
	return decideVisitorMutable(&working, visitorID, at, includeArrival)
}

func decideVisitorMutable(working *State, visitorID model.VisitorID, at time.Time, includeArrival bool) ([]events.Event, error) {
	if err := ensureRunning(*working); err != nil {
		return nil, err
	}
	if visitorID == "" {
		return nil, fmt.Errorf("%w: visitor id is required", ErrInvalidCommand)
	}
	currentTime := working.Clock.CurrentTime
	working.Clock.CurrentTime = at
	defer func() { working.Clock.CurrentTime = currentTime }()
	result := make([]events.Event, 0, 16)
	appendEvents := func(items ...events.Event) error {
		for _, event := range items {
			if err := working.Apply(event); err != nil {
				return err
			}
			result = append(result, event)
		}
		return nil
	}
	if includeArrival {
		if err := appendEvents(events.VisitorArrived{VisitorID: visitorID, ArrivedAt: at}); err != nil {
			return nil, err
		}
	}
	listEvents, err := Decide("", *working, OpenPage{
		RequestID: model.RequestID(string(visitorID) + ":product_list"), VisitorID: visitorID, Page: model.PageProductList,
	})
	if err != nil {
		return nil, err
	}
	if err := appendEvents(listEvents...); err != nil {
		return nil, err
	}
	if requestFailed(listEvents) {
		failure := []events.Event{events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorLeftAfterPageError, CompletedAt: at}}
		if err := appendEvents(failure...); err != nil {
			return nil, err
		}
		return result, nil
	}

	products := make([]ProductState, 0, len(working.Products))
	for _, product := range working.Products {
		products = append(products, product)
	}
	sort.Slice(products, func(i, j int) bool { return products[i].ID < products[j].ID })
	roll := deterministicRoll(working.Seed, string(visitorID), "product")
	var selected ProductState
	var cumulative uint64
	for _, product := range products {
		cumulative += uint64(product.ViewProbabilityPPM)
		if uint64(roll) < cumulative {
			selected = product
			break
		}
	}
	if selected.ID == "" {
		completion := events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorLeftAfterProductList, CompletedAt: at}
		_ = appendEvents(completion)
		return result, nil
	}
	if err := appendEvents(events.ProductSelected{VisitorID: visitorID, ProductID: selected.ID, SelectedAt: at}); err != nil {
		return nil, err
	}
	productRequestID := model.RequestID(string(visitorID) + ":product_page")
	productEvents, err := Decide("", *working, OpenPage{RequestID: productRequestID, VisitorID: visitorID, Page: model.PageProduct, ProductID: selected.ID})
	if err != nil {
		return nil, err
	}
	if err := appendEvents(productEvents...); err != nil {
		return nil, err
	}
	if requestFailed(productEvents) {
		failure := []events.Event{events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorLeftAfterPageError, CompletedAt: at}}
		if err := appendEvents(failure...); err != nil {
			return nil, err
		}
		return result, nil
	}
	_ = appendEvents(events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorLeftAfterProductPage, CompletedAt: at})
	return result, nil
}

func cloneState(state State) State {
	clone := state
	clone.Products = cloneMap(state.Products)
	clone.InboxMessages = cloneMap(state.InboxMessages)
	clone.Pages = cloneMap(state.Pages)
	clone.Servers = cloneMap(state.Servers)
	clone.ServerTypes = cloneMap(state.ServerTypes)
	clone.Databases = cloneDatabases(state.Databases)
	clone.DatabaseConnections = cloneMap(state.DatabaseConnections)
	clone.DatabaseConnectionCounts = cloneMap(state.DatabaseConnectionCounts)
	clone.PendingDatabaseGrowth = cloneMap(state.PendingDatabaseGrowth)
	clone.Backups = cloneMap(state.Backups)
	clone.Capacity = cloneMap(state.Capacity)
	clone.UsedCapacityByServer = cloneMap(state.UsedCapacityByServer)
	clone.Operations = cloneMap(state.Operations)
	clone.Commands = cloneMap(state.Commands)
	clone.CommandPayloads = cloneMap(state.CommandPayloads)
	clone.ControlCommandReceipts = cloneMap(state.ControlCommandReceipts)
	clone.ControlOperationResults = cloneMap(state.ControlOperationResults)
	clone.ServerCredentials = cloneMap(state.ServerCredentials)
	clone.CredentialRotationIDs = cloneMap(state.CredentialRotationIDs)
	clone.Requests = cloneMap(state.Requests)
	clone.SeenRequests = cloneMap(state.SeenRequests)
	clone.Visitors = cloneMap(state.Visitors)
	clone.SeenVisitors = cloneMap(state.SeenVisitors)
	clone.ActiveAttacks = cloneMap(state.ActiveAttacks)
	clone.ResolvedAttacks = cloneMap(state.ResolvedAttacks)
	clone.FirewallRules = cloneMap(state.FirewallRules)
	// World schedule is immutable after bootstrap and therefore safe to share.
	clone.Schedule = state.Schedule
	return clone
}

func cloneDatabases(source map[model.DatabaseID]DatabaseState) map[model.DatabaseID]DatabaseState {
	clone := make(map[model.DatabaseID]DatabaseState, len(source))
	for id, database := range source {
		database.AvailabilityReasons = cloneMap(database.AvailabilityReasons)
		clone[id] = database
	}
	return clone
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	clone := make(map[K]V, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func requestFailed(items []events.Event) bool {
	for _, item := range items {
		switch event := item.(type) {
		case events.PageRequestRejected:
			return event.StatusCode >= 400
		case events.PageRequestCompleted:
			return event.StatusCode >= 400
		}
	}
	return false
}

func pendingScheduledEvents(state State, to time.Time) events.EventSchedule {
	start := state.ScheduleCursor
	start += sort.Search(len(state.Schedule)-start, func(i int) bool {
		return state.Schedule[start+i].OccursAt.After(state.Clock.CurrentTime)
	})
	endOffset := sort.Search(len(state.Schedule)-start, func(i int) bool {
		return state.Schedule[start+i].OccursAt.After(to)
	})
	return state.Schedule[start : start+endOffset]
}

func infrastructureCostEvents(state State, from, to time.Time) []events.Event {
	result := make([]events.Event, 0)
	for _, server := range state.Servers {
		if server.Status != model.ServerActive && server.Status != model.ServerDraining {
			continue
		}
		activeFrom := server.ActivatedAt
		if activeFrom.IsZero() || !activeFrom.Before(to) {
			continue
		}
		if server.CostPerMonthMinor > 0 {
			total := proratedMonthlyCost(activeFrom, to, server.CostPerMonthMinor)
			if total > server.AccruedCostMinor {
				result = append(result, events.InfrastructureCostAccrued{ServerID: server.ID, From: from, To: to,
					TotalAmountMinor: total, AmountMinor: total - server.AccruedCostMinor})
			}
			continue
		}
		duration := to.Sub(activeFrom)
		hours := int64((duration + time.Hour - 1) / time.Hour)
		if hours <= server.BilledHours {
			continue
		}
		amount := (hours - server.BilledHours) * server.CostPerHourMinor
		result = append(result, events.InfrastructureCostAccrued{
			ServerID: server.ID, From: from, To: to, BilledHours: hours, AmountMinor: amount,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(events.InfrastructureCostAccrued).ServerID < result[j].(events.InfrastructureCostAccrued).ServerID
	})
	return result
}

func backupStorageCostEvents(state State, from, to time.Time) []events.Event {
	result := make([]events.Event, 0)
	for _, backup := range state.Backups {
		if backup.Status != model.BackupReady || backup.CompletedAt.IsZero() || !backup.CompletedAt.Before(to) {
			continue
		}
		hours := int64((to.Sub(backup.CompletedAt) + time.Hour - 1) / time.Hour)
		if hours <= backup.BilledHours {
			continue
		}
		result = append(result, events.BackupStorageCostAccrued{BackupID: backup.ID, From: from, To: to, BilledHours: hours,
			AmountMinor: (hours - backup.BilledHours) * backup.StorageCostPerHourMinor})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(events.BackupStorageCostAccrued).BackupID < result[j].(events.BackupStorageCostAccrued).BackupID
	})
	return result
}

func proratedMonthlyCost(from, to time.Time, monthly int64) int64 {
	if !to.After(from) || monthly <= 0 {
		return 0
	}
	var total int64
	for cursor := from; cursor.Before(to); {
		monthStart := time.Date(cursor.Year(), cursor.Month(), 1, 0, 0, 0, 0, cursor.Location())
		nextMonth := monthStart.AddDate(0, 1, 0)
		segmentEnd := to
		if nextMonth.Before(segmentEnd) {
			segmentEnd = nextMonth
		}
		// Millisecond precision is more than sufficient for a monthly tariff and
		// keeps price*duration safely inside int64 for realistic run lengths.
		segmentMillis := segmentEnd.Sub(cursor).Milliseconds()
		monthMillis := nextMonth.Sub(monthStart).Milliseconds()
		total += monthly * segmentMillis / monthMillis
		cursor = segmentEnd
	}
	return total
}

func ensureRunning(state State) error {
	switch state.Status {
	case RunNotCreated:
		return ErrWorldNotCreated
	case RunCompleted:
		return ErrRunCompleted
	case RunRunning:
		return nil
	default:
		return fmt.Errorf("%w: unknown run status %q", ErrInvalidCommand, state.Status)
	}
}

func max(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
