package simulation

import (
	"fmt"
	"sort"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func (s *State) applyDatabaseEvent(event events.Event) (bool, error) {
	switch event := event.(type) {
	case events.ServerTypeDefined:
		if err := ensureRunning(*s); err != nil {
			return true, err
		}
		if event.InstanceType == "" || (event.Role != model.ServerRoleBackend && event.Role != model.ServerRoleDatabase) ||
			!event.DefinedAt.Equal(s.Clock.CurrentTime) || event.CostPerMonthMinor < 0 {
			return true, fmt.Errorf("%w: invalid server type", ErrInvalidEvent)
		}
		if event.Role == model.ServerRoleDatabase && (event.DiskBytes <= 0 || event.ConnectionLimit <= 0 || event.ConnectionHold <= 0 || event.BackendCapacityUnits != 0) {
			return true, fmt.Errorf("%w: invalid database server type", ErrInvalidEvent)
		}
		if event.Role == model.ServerRoleBackend && (event.BackendCapacityUnits <= 0 || event.DiskBytes < 0 || event.ConnectionLimit != 0) {
			return true, fmt.Errorf("%w: invalid backend server type", ErrInvalidEvent)
		}
		if _, exists := s.ServerTypes[event.InstanceType]; exists {
			return true, fmt.Errorf("%w: server type already exists", ErrInvalidEvent)
		}
		s.ServerTypes[event.InstanceType] = ServerTypeState{InstanceType: event.InstanceType, Role: event.Role, DiskBytes: event.DiskBytes,
			ConnectionLimit: event.ConnectionLimit, ConnectionHold: event.ConnectionHold, BackendCapacityUnits: event.BackendCapacityUnits,
			CostPerHourMinor: event.CostPerHourMinor, CostPerMonthMinor: event.CostPerMonthMinor}
		return true, nil

	case events.DatabaseCreated:
		if err := ensureRunning(*s); err != nil {
			return true, err
		}
		server, ok := s.Servers[event.ServerID]
		if event.DatabaseID == "" || event.Name == "" || !ok || server.Status != model.ServerActive || server.Role != model.ServerRoleDatabase || !event.CreatedAt.Equal(s.Clock.CurrentTime) {
			return true, fmt.Errorf("%w: database requires an active database server", ErrInvalidEvent)
		}
		if _, exists := s.Databases[event.DatabaseID]; exists {
			return true, fmt.Errorf("%w: database already exists", ErrInvalidEvent)
		}
		for _, database := range s.Databases {
			if database.ServerID == event.ServerID {
				return true, fmt.Errorf("%w: server already hosts a database", ErrInvalidEvent)
			}
		}
		s.Databases[event.DatabaseID] = DatabaseState{ID: event.DatabaseID, ServerID: event.ServerID, Name: event.Name, Ready: true,
			AvailabilityReasons: make(map[model.DatabaseAvailabilityReason]struct{})}
		return true, nil

	case events.DatabaseDeleted:
		database, exists := s.Databases[event.DatabaseID]
		if !exists || database.ServerID != event.ServerID || s.Site.DatabaseID == event.DatabaseID ||
			!validFactTime(*s, event.DeletedAt) {
			return true, fmt.Errorf("%w: database cannot be deleted", ErrInvalidEvent)
		}
		delete(s.Databases, event.DatabaseID)
		for growthID, growth := range s.PendingDatabaseGrowth {
			if growth.DatabaseID == event.DatabaseID {
				delete(s.PendingDatabaseGrowth, growthID)
			}
		}
		return true, nil

	case events.DatabaseConnectionOpened:
		database, ok := s.Databases[event.DatabaseID]
		server, serverOK := s.Servers[event.ServerID]
		if !ok || !serverOK || database.ServerID != event.ServerID || !database.Available() || event.RequestID == "" ||
			!validFactTime(*s, event.OpenedAt) || event.ReleasesAt != event.OpenedAt.Add(server.ConnectionHold) || s.DatabaseConnectionCounts[event.DatabaseID] >= server.ConnectionLimit {
			return true, fmt.Errorf("%w: database connection cannot be opened", ErrInvalidEvent)
		}
		if _, exists := s.DatabaseConnections[event.RequestID]; exists {
			return true, fmt.Errorf("%w: database connection already exists", ErrInvalidEvent)
		}
		s.DatabaseConnections[event.RequestID] = DatabaseConnectionState{RequestID: event.RequestID, DatabaseID: event.DatabaseID, ServerID: event.ServerID, OpenedAt: event.OpenedAt, ReleasesAt: event.ReleasesAt}
		s.DatabaseConnectionCounts[event.DatabaseID]++
		return true, nil

	case events.DatabaseConnectionReleased:
		connection, ok := s.DatabaseConnections[event.RequestID]
		if !ok || connection.DatabaseID != event.DatabaseID || connection.ServerID != event.ServerID || event.ReleasedAt.Before(connection.ReleasesAt) || !validFactTime(*s, event.ReleasedAt) {
			return true, fmt.Errorf("%w: database connection cannot be released", ErrInvalidEvent)
		}
		delete(s.DatabaseConnections, event.RequestID)
		s.DatabaseConnectionCounts[event.DatabaseID]--
		return true, nil

	case events.DatabaseConnectionRejected:
		database, ok := s.Databases[event.DatabaseID]
		server, serverOK := s.Servers[event.ServerID]
		if !ok || !serverOK || database.ServerID != event.ServerID || event.ActiveConnections != s.DatabaseConnectionCounts[event.DatabaseID] ||
			event.ConnectionLimit != server.ConnectionLimit || event.ActiveConnections < event.ConnectionLimit || !validFactTime(*s, event.RejectedAt) {
			return true, fmt.Errorf("%w: invalid rejected database connection", ErrInvalidEvent)
		}
		return true, nil

	case events.DatabaseAvailabilityChanged:
		database, ok := s.Databases[event.DatabaseID]
		if !ok || database.ServerID != event.ServerID || !validFactTime(*s, event.ChangedAt) || event.Available != (len(event.Reasons) == 0) {
			return true, fmt.Errorf("%w: invalid database availability", ErrInvalidEvent)
		}
		reasons := make(map[model.DatabaseAvailabilityReason]struct{}, len(event.Reasons))
		for _, reason := range event.Reasons {
			if reason != model.DatabaseUnavailableDiskFull && reason != model.DatabaseUnavailableConnectionLimit {
				return true, fmt.Errorf("%w: unknown availability reason", ErrInvalidEvent)
			}
			reasons[reason] = struct{}{}
		}
		database.AvailabilityReasons = reasons
		s.Databases[event.DatabaseID] = database
		if s.Site.DatabaseID == event.DatabaseID && s.Site.Status == model.SiteRunning {
			if !event.Available && s.Site.UnavailableSince.IsZero() {
				s.Site.UnavailableSince = event.ChangedAt
			}
			if event.Available && siteDependenciesAvailable(*s) && !s.Site.UnavailableSince.IsZero() {
				s.Site.DowntimeDuration += event.ChangedAt.Sub(s.Site.UnavailableSince)
				s.Site.UnavailableSince = time.Time{}
			}
		}
		return true, nil

	case events.DatabaseGrowthRequested:
		if event.GrowthID == "" || event.DataDeltaBytes < 0 || event.LogsDeltaBytes < 0 || event.DataDeltaBytes+event.LogsDeltaBytes <= 0 || !validFactTime(*s, event.RequestedAt) {
			return true, fmt.Errorf("%w: invalid database growth", ErrInvalidEvent)
		}
		if _, exists := s.PendingDatabaseGrowth[event.GrowthID]; exists {
			return true, fmt.Errorf("%w: growth already exists", ErrInvalidEvent)
		}
		s.PendingDatabaseGrowth[event.GrowthID] = DatabaseGrowthState{ID: event.GrowthID, DataDeltaBytes: event.DataDeltaBytes, LogsDeltaBytes: event.LogsDeltaBytes, RequestedAt: event.RequestedAt}
		s.markScheduled(event, event.RequestedAt)
		return true, nil

	case events.DatabaseStorageIncreased:
		growth, exists := s.PendingDatabaseGrowth[event.GrowthID]
		database, dbExists := s.Databases[event.DatabaseID]
		server, serverExists := s.Servers[event.ServerID]
		wantVersion := database.DataVersion
		if growth.DataDeltaBytes > 0 {
			wantVersion++
		}
		if !exists || !dbExists || !serverExists || database.ServerID != event.ServerID || growth.DataDeltaBytes != event.DataBytesAdded || growth.LogsDeltaBytes != event.LogsBytesAdded ||
			event.DataVersion != wantVersion || database.UsedBytes()+event.DataBytesAdded+event.LogsBytesAdded > server.DiskBytes || !validFactTime(*s, event.IncreasedAt) {
			return true, fmt.Errorf("%w: invalid database storage increase", ErrInvalidEvent)
		}
		database.DataBytes += event.DataBytesAdded
		database.LogsBytes += event.LogsBytesAdded
		database.DataVersion = event.DataVersion
		database.Ready = true
		s.Databases[event.DatabaseID] = database
		delete(s.PendingDatabaseGrowth, event.GrowthID)
		return true, nil

	case events.DatabaseGrowthBlocked:
		growth, exists := s.PendingDatabaseGrowth[event.GrowthID]
		database, dbExists := s.Databases[event.DatabaseID]
		server, serverExists := s.Servers[event.ServerID]
		if !exists || !dbExists || !serverExists || database.ServerID != event.ServerID || growth.DataDeltaBytes != event.PendingDataBytes || growth.LogsDeltaBytes != event.PendingLogsBytes ||
			event.RequiredBytes != growth.DataDeltaBytes+growth.LogsDeltaBytes || event.FreeBytes != server.DiskBytes-database.UsedBytes() || event.RequiredBytes <= event.FreeBytes || !validFactTime(*s, event.BlockedAt) {
			return true, fmt.Errorf("%w: invalid blocked database growth", ErrInvalidEvent)
		}
		growth.DatabaseID = event.DatabaseID
		growth.Blocked = true
		s.PendingDatabaseGrowth[event.GrowthID] = growth
		return true, nil

	case events.DiskLogsCleaned:
		server, ok := s.Servers[event.ServerID]
		if !ok || event.FreedBytes < 0 || !validFactTime(*s, event.CleanedAt) {
			return true, fmt.Errorf("%w: invalid disk cleanup", ErrInvalidEvent)
		}
		database, hasDatabase := databaseOnServer(*s, event.ServerID)
		totalLogs := server.LogsBytes
		if hasDatabase {
			totalLogs += database.LogsBytes
		}
		if event.FreedBytes > totalLogs {
			return true, fmt.Errorf("%w: cleanup exceeds stored logs", ErrInvalidEvent)
		}
		remaining := event.FreedBytes
		fromServer := min(remaining, server.LogsBytes)
		server.LogsBytes -= fromServer
		remaining -= fromServer
		s.Servers[event.ServerID] = server
		if hasDatabase && remaining > 0 {
			database.LogsBytes -= remaining
			s.Databases[database.ID] = database
		}
		return true, nil

	case events.DiskLogsGrowthRequested:
		if _, ok := s.Servers[event.ServerID]; !ok || event.GrowthID == "" || event.DeltaBytes <= 0 || !validFactTime(*s, event.RequestedAt) {
			return true, fmt.Errorf("%w: invalid disk log growth", ErrInvalidEvent)
		}
		s.markScheduled(event, event.RequestedAt)
		return true, nil

	case events.DiskLogsIncreased:
		server, ok := s.Servers[event.ServerID]
		usage := diskUsage(*s, event.ServerID)
		if !ok || event.GrowthID == "" || event.BytesAdded <= 0 || event.BytesAdded > usage.FreeBytes || !validFactTime(*s, event.IncreasedAt) {
			return true, fmt.Errorf("%w: invalid disk log increase", ErrInvalidEvent)
		}
		server.LogsBytes += event.BytesAdded
		s.Servers[event.ServerID] = server
		return true, nil

	case events.DiskLogsGrowthBlocked:
		_, ok := s.Servers[event.ServerID]
		usage := diskUsage(*s, event.ServerID)
		if !ok || event.GrowthID == "" || event.RequiredBytes <= event.FreeBytes || event.FreeBytes != usage.FreeBytes || !validFactTime(*s, event.BlockedAt) {
			return true, fmt.Errorf("%w: invalid blocked disk log growth", ErrInvalidEvent)
		}
		return true, nil

	case events.DatabaseBackupStarted:
		database, ok := s.Databases[event.DatabaseID]
		if !ok || database.ServerID != event.ServerID || event.BackupID == "" || !validFactTime(*s, event.StartedAt) {
			return true, fmt.Errorf("%w: invalid backup start", ErrInvalidEvent)
		}
		if _, exists := s.Backups[event.BackupID]; exists {
			return true, fmt.Errorf("%w: backup already exists", ErrInvalidEvent)
		}
		s.Backups[event.BackupID] = BackupState{ID: event.BackupID, DatabaseID: event.DatabaseID, ServerID: event.ServerID, OperationID: event.OperationID, Status: model.BackupCreating, CreatedAt: event.StartedAt}
		return true, nil

	case events.DatabaseBackupCompleted:
		backup, ok := s.Backups[event.BackupID]
		database, dbOK := s.Databases[event.DatabaseID]
		if !ok || !dbOK || backup.Status != model.BackupCreating || backup.OperationID != event.OperationID || backup.DatabaseID != event.DatabaseID ||
			event.DataBytes != database.DataBytes || event.DataVersion != database.DataVersion || event.StorageCostPerHourMinor != backupStorageHourlyCost(event.DataBytes) || !validFactTime(*s, event.CompletedAt) {
			return true, fmt.Errorf("%w: invalid backup completion", ErrInvalidEvent)
		}
		backup.Status = model.BackupReady
		backup.DataBytes = event.DataBytes
		backup.DataVersion = event.DataVersion
		backup.CompletedAt = event.CompletedAt
		backup.StorageCostPerHourMinor = event.StorageCostPerHourMinor
		s.Backups[event.BackupID] = backup
		return true, nil

	case events.BackupStorageCostAccrued:
		backup, ok := s.Backups[event.BackupID]
		if !ok || backup.Status != model.BackupReady || event.AmountMinor < 0 || event.BilledHours <= backup.BilledHours ||
			event.AmountMinor != (event.BilledHours-backup.BilledHours)*backup.StorageCostPerHourMinor || !event.To.Equal(s.Clock.CurrentTime) {
			return true, fmt.Errorf("%w: invalid backup storage cost", ErrInvalidEvent)
		}
		backup.BilledHours = event.BilledHours
		s.Backups[event.BackupID] = backup
		s.Costs.BackupStorageCostMinor += event.AmountMinor
		return true, nil

	case events.DatabaseBackupFailed:
		backup, ok := s.Backups[event.BackupID]
		if !ok || backup.Status != model.BackupCreating || backup.OperationID != event.OperationID || event.ErrorCode == "" || event.Message == "" || !validFactTime(*s, event.FailedAt) {
			return true, fmt.Errorf("%w: invalid backup failure", ErrInvalidEvent)
		}
		backup.Status = model.BackupFailed
		backup.ErrorCode = event.ErrorCode
		backup.Message = event.Message
		s.Backups[event.BackupID] = backup
		return true, nil

	case events.DatabaseRestoreStarted:
		database, dbOK := s.Databases[event.DatabaseID]
		backup, backupOK := s.Backups[event.BackupID]
		if !dbOK || !backupOK || backup.Status != model.BackupReady || database.ServerID != event.ServerID || database.DataBytes != 0 || database.LogsBytes != 0 || !validFactTime(*s, event.StartedAt) {
			return true, fmt.Errorf("%w: invalid restore start", ErrInvalidEvent)
		}
		database.Ready = false
		s.Databases[event.DatabaseID] = database
		return true, nil

	case events.DatabaseRestoreCompleted:
		database, dbOK := s.Databases[event.DatabaseID]
		backup, backupOK := s.Backups[event.BackupID]
		if !dbOK || !backupOK || backup.Status != model.BackupReady || database.ServerID != event.ServerID || event.DataBytes != backup.DataBytes || event.DataVersion != backup.DataVersion || !validFactTime(*s, event.CompletedAt) {
			return true, fmt.Errorf("%w: invalid restore completion", ErrInvalidEvent)
		}
		database.DataBytes = event.DataBytes
		database.DataVersion = event.DataVersion
		database.Ready = true
		database.RestoredBackupID = event.BackupID
		database.RestoredFromDatabaseID = backup.DatabaseID
		s.Databases[event.DatabaseID] = database
		return true, nil

	case events.DatabaseRestoreFailed:
		database, ok := s.Databases[event.DatabaseID]
		if !ok || event.ErrorCode == "" || event.Message == "" || !validFactTime(*s, event.FailedAt) {
			return true, fmt.Errorf("%w: invalid restore failure", ErrInvalidEvent)
		}
		database.Ready = false
		s.Databases[event.DatabaseID] = database
		return true, nil

	case events.SiteStopStarted:
		if s.Site.Status != model.SiteRunning || !event.StartedAt.Equal(s.Clock.CurrentTime) {
			return true, fmt.Errorf("%w: site cannot stop", ErrInvalidEvent)
		}
		s.Site.Status = model.SiteStopping
		s.Site.OperationID = event.OperationID
		if s.Site.UnavailableSince.IsZero() {
			s.Site.UnavailableSince = event.StartedAt
		}
		return true, nil

	case events.SiteStopped:
		if s.Site.Status != model.SiteStopping || s.Site.OperationID != event.OperationID || activeDatabaseConnections(*s) != 0 || !validFactTime(*s, event.StoppedAt) {
			return true, fmt.Errorf("%w: site cannot finish stopping", ErrInvalidEvent)
		}
		s.Site.Status = model.SiteStopped
		return true, nil

	case events.SiteDatabaseChanged:
		database, ok := s.Databases[event.DatabaseID]
		if s.Site.Status != model.SiteStopped || !ok || !database.Ready || s.Site.DatabaseID != event.PreviousDatabaseID || !validFactTime(*s, event.ChangedAt) {
			return true, fmt.Errorf("%w: invalid site database switch", ErrInvalidEvent)
		}
		s.Site.DatabaseID = event.DatabaseID
		return true, nil

	case events.SiteStarted:
		database, ok := s.Databases[event.DatabaseID]
		if s.Site.Status != model.SiteStopped || !ok || !database.Available() || s.Site.DatabaseID != event.DatabaseID || !event.StartedAt.Equal(s.Clock.CurrentTime) {
			return true, fmt.Errorf("%w: site cannot start", ErrInvalidEvent)
		}
		s.Site.Status = model.SiteRunning
		s.Site.OperationID = ""
		if siteDependenciesAvailable(*s) && !s.Site.UnavailableSince.IsZero() {
			s.Site.DowntimeDuration += event.StartedAt.Sub(s.Site.UnavailableSince)
			s.Site.UnavailableSince = time.Time{}
		}
		return true, nil
	default:
		return false, nil
	}
}

func databaseOnServer(state State, serverID model.ServerID) (DatabaseState, bool) {
	for _, database := range state.Databases {
		if database.ServerID == serverID {
			return database, true
		}
	}
	return DatabaseState{}, false
}

func activeDatabaseConnections(state State) int { return len(state.DatabaseConnections) }

func databaseReasons(database DatabaseState) []model.DatabaseAvailabilityReason {
	result := make([]model.DatabaseAvailabilityReason, 0, len(database.AvailabilityReasons))
	for reason := range database.AvailabilityReasons {
		result = append(result, reason)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func reasonsWith(database DatabaseState, reason model.DatabaseAvailabilityReason, present bool) []model.DatabaseAvailabilityReason {
	reasons := cloneMap(database.AvailabilityReasons)
	if present {
		reasons[reason] = struct{}{}
	} else {
		delete(reasons, reason)
	}
	copyDB := database
	copyDB.AvailabilityReasons = reasons
	return databaseReasons(copyDB)
}
