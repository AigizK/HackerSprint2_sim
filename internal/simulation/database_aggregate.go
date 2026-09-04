package simulation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func decideConfigureServerCatalog(state State, command ConfigureServerCatalog) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.XBytes <= 0 || command.BackendCapacityUnits <= 0 || command.BackendCostPerMonthMinor < 0 || len(state.ServerTypes) != 0 {
		return nil, fmt.Errorf("%w: invalid or already configured server catalog", ErrInvalidCommand)
	}
	at := state.Clock.CurrentTime
	return []events.Event{
		events.ServerTypeDefined{InstanceType: model.InstanceBackendStandard, Role: model.ServerRoleBackend, DiskBytes: command.XBytes, BackendCapacityUnits: command.BackendCapacityUnits, CostPerMonthMinor: command.BackendCostPerMonthMinor, DefinedAt: at},
		events.ServerTypeDefined{InstanceType: model.InstanceDBSmall, Role: model.ServerRoleDatabase, DiskBytes: command.XBytes, ConnectionLimit: model.DBSmallConnections, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBSmallCostPerMonthMinor, DefinedAt: at},
		events.ServerTypeDefined{InstanceType: model.InstanceDBMedium, Role: model.ServerRoleDatabase, DiskBytes: 2 * command.XBytes, ConnectionLimit: model.DBMediumConnections, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBMediumCostPerMonthMinor, DefinedAt: at},
		events.ServerTypeDefined{InstanceType: model.InstanceDBLarge, Role: model.ServerRoleDatabase, DiskBytes: 4 * command.XBytes, ConnectionLimit: model.DBLargeConnections, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBLargeCostPerMonthMinor, DefinedAt: at},
	}, nil
}

func decideAddTypedServer(state State, command AddTypedServer) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	typeState, ok := state.ServerTypes[command.InstanceType]
	if command.CommandID == "" || command.OperationID == "" || command.ServerID == "" || !ok || state.Infrastructure.ServerProvisioningDuration <= 0 {
		return nil, fmt.Errorf("%w: invalid typed server command", ErrInvalidCommand)
	}
	if err := ensureNewOperationIdentifiers(state, command.CommandID, command.OperationID); err != nil {
		return nil, err
	}
	if _, exists := state.Servers[command.ServerID]; exists {
		return nil, fmt.Errorf("%w: server already exists", ErrInvalidCommand)
	}
	at := state.Clock.CurrentTime
	name := strings.TrimSpace(command.Name)
	if name == "" {
		name = string(command.ServerID)
	}
	return []events.Event{
		commandAccepted(command.CommandID, command.OperationID, databaseCommandPayload(command), at),
		events.OperationQueued{OperationID: command.OperationID, Kind: model.OperationControlCommand, QueuedAt: at},
		events.OperationStarted{OperationID: command.OperationID, StartedAt: at},
		events.ServerProvisioningStarted{OperationID: command.OperationID, ServerID: command.ServerID, Name: name, CredentialID: command.CredentialID, InstanceType: command.InstanceType, Role: typeState.Role,
			CapacityUnits: typeState.BackendCapacityUnits, DiskBytes: typeState.DiskBytes, ConnectionLimit: typeState.ConnectionLimit,
			ConnectionHold: typeState.ConnectionHold, CostPerHourMinor: typeState.CostPerHourMinor, CostPerMonthMinor: typeState.CostPerMonthMinor, StartedAt: at, ReadyAt: at.Add(state.Infrastructure.ServerProvisioningDuration)},
	}, nil
}

func decideCreateDatabase(state State, command CreateDatabase) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	server, ok := state.Servers[command.ServerID]
	if command.CommandID == "" || command.DatabaseID == "" || strings.TrimSpace(command.Name) == "" {
		return nil, fmt.Errorf("%w: invalid database command", ErrInvalidCommand)
	}
	if !ok {
		return nil, fmt.Errorf("%w: server %q", ErrResourceNotFound, command.ServerID)
	}
	if server.Status != model.ServerActive || server.Role != model.ServerRoleDatabase {
		return nil, fmt.Errorf("%w: database requires an active database server", ErrInvalidCommand)
	}
	if _, exists := state.Databases[command.DatabaseID]; exists {
		return nil, fmt.Errorf("%w: database already exists", ErrInvalidCommand)
	}
	if _, exists := databaseOnServer(state, command.ServerID); exists {
		return nil, fmt.Errorf("%w: server already hosts a database", ErrServerInUse)
	}
	if diskUsage(state, command.ServerID).FreeBytes <= 0 {
		return nil, ErrInsufficientDisk
	}
	at := state.Clock.CurrentTime
	return []events.Event{commandAccepted(command.CommandID, model.OperationID(command.CommandID), databaseCommandPayload(command), at),
		events.DatabaseCreated{DatabaseID: command.DatabaseID, ServerID: command.ServerID, Name: strings.TrimSpace(command.Name), CreatedAt: at}}, nil
}

func decideGrowDatabase(state State, command GrowDatabase) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.CommandID == "" || command.GrowthID == "" || command.DataDeltaBytes < 0 || command.LogsDeltaBytes < 0 || command.DataDeltaBytes+command.LogsDeltaBytes <= 0 || state.Site.DatabaseID == "" {
		return nil, fmt.Errorf("%w: invalid database growth", ErrInvalidCommand)
	}
	if _, exists := state.PendingDatabaseGrowth[command.GrowthID]; exists {
		return nil, fmt.Errorf("%w: growth already exists", ErrInvalidCommand)
	}
	at := state.Clock.CurrentTime
	result := []events.Event{commandAccepted(command.CommandID, model.OperationID(command.CommandID), databaseCommandPayload(command), at),
		events.DatabaseGrowthRequested{GrowthID: command.GrowthID, DataDeltaBytes: command.DataDeltaBytes, LogsDeltaBytes: command.LogsDeltaBytes, RequestedAt: at}}
	working := cloneState(state)
	for _, event := range result {
		if err := working.Apply(event); err != nil {
			return nil, err
		}
	}
	if working.Site.Status == model.SiteRunning {
		result = append(result, decidePendingGrowth(working, command.GrowthID, at)...)
	}
	return result, nil
}

func decidePendingGrowth(state State, growthID model.GrowthID, at time.Time) []events.Event {
	growth, exists := state.PendingDatabaseGrowth[growthID]
	if !exists {
		return nil
	}
	databaseID := growth.DatabaseID
	if databaseID == "" {
		databaseID = state.Site.DatabaseID
	}
	database, ok := state.Databases[databaseID]
	if !ok {
		return nil
	}
	server, ok := state.Servers[database.ServerID]
	if !ok {
		return nil
	}
	required, free := growth.DataDeltaBytes+growth.LogsDeltaBytes, server.DiskBytes-database.UsedBytes()
	if required > free {
		result := []events.Event{events.DatabaseGrowthBlocked{GrowthID: growth.ID, DatabaseID: database.ID, ServerID: server.ID, PendingDataBytes: growth.DataDeltaBytes,
			PendingLogsBytes: growth.LogsDeltaBytes, RequiredBytes: required, FreeBytes: free, BlockedAt: at}}
		reasons := reasonsWith(database, model.DatabaseUnavailableDiskFull, true)
		if _, already := database.AvailabilityReasons[model.DatabaseUnavailableDiskFull]; !already {
			result = append(result, events.DatabaseAvailabilityChanged{DatabaseID: database.ID, ServerID: server.ID, Available: false, Reasons: reasons, ChangedAt: at})
		}
		return result
	}
	version := database.DataVersion
	if growth.DataDeltaBytes > 0 {
		version++
	}
	result := []events.Event{events.DatabaseStorageIncreased{GrowthID: growth.ID, DatabaseID: database.ID, ServerID: server.ID, DataBytesAdded: growth.DataDeltaBytes,
		LogsBytesAdded: growth.LogsDeltaBytes, DataVersion: version, IncreasedAt: at}}
	if _, full := database.AvailabilityReasons[model.DatabaseUnavailableDiskFull]; full {
		reasons := reasonsWith(database, model.DatabaseUnavailableDiskFull, false)
		result = append(result, events.DatabaseAvailabilityChanged{DatabaseID: database.ID, ServerID: server.ID, Available: len(reasons) == 0, Reasons: reasons, ChangedAt: at})
	}
	return result
}

func decideCleanupDatabaseLogs(state State, command CleanupDatabaseLogs) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.CommandID == "" {
		return nil, fmt.Errorf("%w: disk cleanup request is invalid", ErrInvalidCommand)
	}
	if _, ok := state.Servers[command.ServerID]; !ok {
		return nil, fmt.Errorf("%w: server disk not found", ErrResourceNotFound)
	}
	at := state.Clock.CurrentTime
	usage := diskUsage(state, command.ServerID)
	result := []events.Event{commandAccepted(command.CommandID, model.OperationID(command.CommandID), databaseCommandPayload(command), at),
		events.DiskLogsCleaned{ServerID: command.ServerID, FreedBytes: usage.LogsBytes, CleanedAt: at}}
	working := cloneState(state)
	for _, event := range result {
		if err := working.Apply(event); err != nil {
			return nil, err
		}
	}
	database, hasDatabase := databaseOnServer(state, command.ServerID)
	ids := make([]string, 0)
	for id, growth := range working.PendingDatabaseGrowth {
		if hasDatabase && growth.Blocked && growth.DatabaseID == database.ID {
			ids = append(ids, string(id))
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		generated := decidePendingGrowth(working, model.GrowthID(id), at)
		for _, event := range generated {
			if err := working.Apply(event); err != nil {
				return nil, err
			}
			result = append(result, event)
		}
	}
	return appendBackendAvailabilityChange(state, result, at)
}

func decideDiskLogsGrowth(state State, growth events.DiskLogsGrowthRequested) []events.Event {
	usage := diskUsage(state, growth.ServerID)
	if growth.DeltaBytes > usage.FreeBytes {
		return []events.Event{events.DiskLogsGrowthBlocked{GrowthID: growth.GrowthID, ServerID: growth.ServerID,
			RequiredBytes: growth.DeltaBytes, FreeBytes: usage.FreeBytes, BlockedAt: growth.RequestedAt}}
	}
	return []events.Event{events.DiskLogsIncreased{GrowthID: growth.GrowthID, ServerID: growth.ServerID,
		BytesAdded: growth.DeltaBytes, IncreasedAt: growth.RequestedAt}}
}

func decideBackupDatabase(state State, command BackupDatabase) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	database, ok := state.Databases[command.DatabaseID]
	if command.CommandID == "" || command.OperationID == "" || command.BackupID == "" || !ok {
		return nil, fmt.Errorf("%w: invalid backup command", ErrInvalidCommand)
	}
	if command.DatabaseID == state.Site.DatabaseID && state.Site.Status != model.SiteStopped {
		return nil, ErrSiteNotStopped
	}
	if err := ensureNewOperationIdentifiers(state, command.CommandID, command.OperationID); err != nil {
		return nil, err
	}
	if _, exists := state.Backups[command.BackupID]; exists {
		return nil, fmt.Errorf("%w: backup already exists", ErrInvalidCommand)
	}
	at := state.Clock.CurrentTime
	result := []events.Event{commandAccepted(command.CommandID, command.OperationID, databaseCommandPayload(command), at),
		events.OperationQueued{OperationID: command.OperationID, Kind: model.OperationControlCommand, QueuedAt: at}, events.OperationStarted{OperationID: command.OperationID, StartedAt: at},
		events.DatabaseBackupStarted{OperationID: command.OperationID, BackupID: command.BackupID, DatabaseID: database.ID, ServerID: database.ServerID, StartedAt: at}}
	if command.Fail {
		return append(result, events.DatabaseBackupFailed{OperationID: command.OperationID, BackupID: command.BackupID, DatabaseID: database.ID, ErrorCode: "BACKUP_FAILED", Message: "backup failed", FailedAt: at},
			events.OperationFailed{OperationID: command.OperationID, ErrorCode: "BACKUP_FAILED", Message: "backup failed", FailedAt: at}), nil
	}
	return append(result, events.DatabaseBackupCompleted{OperationID: command.OperationID, BackupID: command.BackupID, DatabaseID: database.ID, ServerID: database.ServerID,
		DataBytes: database.DataBytes, DataVersion: database.DataVersion, StorageCostPerHourMinor: backupStorageHourlyCost(database.DataBytes), CompletedAt: at}, events.OperationSucceeded{OperationID: command.OperationID, CompletedAt: at}), nil
}

func backupStorageHourlyCost(dataBytes int64) int64 {
	if dataBytes <= 0 {
		return 0
	}
	gibibytes := (dataBytes + (1 << 30) - 1) / (1 << 30)
	return gibibytes * model.BackupStorageCostPerGiBHourMinor
}

func decideRestoreDatabase(state State, command RestoreDatabase) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	database, dbOK := state.Databases[command.DatabaseID]
	backup, backupOK := state.Backups[command.BackupID]
	if command.CommandID == "" || command.OperationID == "" || !dbOK {
		return nil, fmt.Errorf("%w: invalid restore command", ErrInvalidCommand)
	}
	if !backupOK || backup.Status != model.BackupReady {
		return nil, ErrBackupNotReady
	}
	if command.DatabaseID == state.Site.DatabaseID && state.Site.Status != model.SiteStopped {
		return nil, ErrSiteNotStopped
	}
	if database.DataBytes != 0 || database.LogsBytes != 0 {
		return nil, ErrDatabaseNotEmpty
	}
	server, ok := state.Servers[database.ServerID]
	if !ok {
		return nil, ErrResourceNotFound
	}
	if backup.DataBytes > server.DiskBytes {
		return nil, ErrInsufficientDisk
	}
	if err := ensureNewOperationIdentifiers(state, command.CommandID, command.OperationID); err != nil {
		return nil, err
	}
	at := state.Clock.CurrentTime
	result := []events.Event{commandAccepted(command.CommandID, command.OperationID, databaseCommandPayload(command), at),
		events.OperationQueued{OperationID: command.OperationID, Kind: model.OperationControlCommand, QueuedAt: at}, events.OperationStarted{OperationID: command.OperationID, StartedAt: at},
		events.DatabaseRestoreStarted{OperationID: command.OperationID, BackupID: backup.ID, DatabaseID: database.ID, ServerID: database.ServerID, StartedAt: at}}
	if command.Fail {
		return append(result, events.DatabaseRestoreFailed{OperationID: command.OperationID, BackupID: backup.ID, DatabaseID: database.ID, ErrorCode: "RESTORE_FAILED", Message: "restore failed", FailedAt: at},
			events.OperationFailed{OperationID: command.OperationID, ErrorCode: "RESTORE_FAILED", Message: "restore failed", FailedAt: at}), nil
	}
	return append(result, events.DatabaseRestoreCompleted{OperationID: command.OperationID, BackupID: backup.ID, DatabaseID: database.ID, ServerID: database.ServerID,
		DataBytes: backup.DataBytes, DataVersion: backup.DataVersion, CompletedAt: at}, events.OperationSucceeded{OperationID: command.OperationID, CompletedAt: at}), nil
}

func decideStopSite(state State, command StopSite) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.CommandID == "" || command.OperationID == "" {
		return nil, ErrSiteNotRunning
	}
	if err := ensureNewOperationIdentifiers(state, command.CommandID, command.OperationID); err != nil {
		return nil, err
	}
	at := state.Clock.CurrentTime
	if state.Site.Status == model.SiteStopped {
		return []events.Event{commandAccepted(command.CommandID, command.OperationID, databaseCommandPayload(command), at),
			events.OperationQueued{OperationID: command.OperationID, Kind: model.OperationControlCommand, QueuedAt: at},
			events.OperationStarted{OperationID: command.OperationID, StartedAt: at},
			events.OperationSucceeded{OperationID: command.OperationID, CompletedAt: at}}, nil
	}
	if state.Site.Status != model.SiteRunning {
		return nil, ErrSiteNotRunning
	}
	result := []events.Event{commandAccepted(command.CommandID, command.OperationID, databaseCommandPayload(command), at),
		events.OperationQueued{OperationID: command.OperationID, Kind: model.OperationControlCommand, QueuedAt: at}, events.OperationStarted{OperationID: command.OperationID, StartedAt: at},
		events.SiteStopStarted{OperationID: command.OperationID, StartedAt: at}}
	if activeDatabaseConnections(state) == 0 {
		result = append(result, events.SiteStopped{OperationID: command.OperationID, StoppedAt: at}, events.OperationSucceeded{OperationID: command.OperationID, CompletedAt: at})
	}
	return result, nil
}

func decideStartSite(state State, command StartSite) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	database, ok := state.Databases[state.Site.DatabaseID]
	if command.CommandID == "" {
		return nil, ErrSiteNotStopped
	}
	if state.Site.Status == model.SiteRunning {
		return []events.Event{commandAccepted(command.CommandID, model.OperationID(command.CommandID), databaseCommandPayload(command), state.Clock.CurrentTime)}, nil
	}
	if state.Site.Status != model.SiteStopped {
		return nil, ErrSiteNotStopped
	}
	if !ok || !database.Available() {
		return nil, ErrDatabaseNotReady
	}
	server, serverExists := state.Servers[database.ServerID]
	if !serverExists || server.Status != model.ServerActive {
		return nil, ErrDatabaseNotReady
	}
	activeBackends := 0
	for _, candidate := range state.Servers {
		if candidate.Status == model.ServerActive && (candidate.Role == "" || candidate.Role == model.ServerRoleBackend) {
			activeBackends++
		}
	}
	if activeBackends == 0 || len(backendUnavailablePages(state, state.Clock.CurrentTime)) > 0 {
		return nil, ErrBackendUnavailable
	}
	if databaseOperationInProgress(state, database.ID) {
		return nil, ErrOperationInProgress
	}
	at := state.Clock.CurrentTime
	result := []events.Event{commandAccepted(command.CommandID, model.OperationID(command.CommandID), databaseCommandPayload(command), at), events.SiteStarted{DatabaseID: database.ID, StartedAt: at}}
	working := cloneState(state)
	for _, event := range result {
		if err := working.Apply(event); err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0)
	for id, growth := range working.PendingDatabaseGrowth {
		if !growth.Blocked {
			ids = append(ids, string(id))
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		generated := decidePendingGrowth(working, model.GrowthID(id), at)
		for _, event := range generated {
			if err := working.Apply(event); err != nil {
				return nil, err
			}
			result = append(result, event)
		}
	}
	return result, nil
}

func databaseOperationInProgress(state State, databaseID model.DatabaseID) bool {
	for _, backup := range state.Backups {
		if backup.DatabaseID == databaseID && backup.Status == model.BackupCreating {
			return true
		}
	}
	database, exists := state.Databases[databaseID]
	return exists && !database.Ready
}

func decideSetSiteDatabase(state State, command SetSiteDatabase) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.CommandID == "" || state.Site.Status != model.SiteStopped {
		return nil, ErrSiteNotStopped
	}
	if state.Site.DatabaseID != command.ExpectedCurrentDatabaseID {
		return nil, ErrSiteConfigConflict
	}
	target, ok := state.Databases[command.DatabaseID]
	if !ok || !target.Ready {
		return nil, ErrDatabaseNotReady
	}
	if state.Site.DatabaseID != "" {
		current := state.Databases[state.Site.DatabaseID]
		if target.RestoredFromDatabaseID != current.ID || target.DataVersion < current.DataVersion {
			return nil, ErrDatabaseBackupStale
		}
	}
	at := state.Clock.CurrentTime
	return []events.Event{commandAccepted(command.CommandID, model.OperationID(command.CommandID), databaseCommandPayload(command), at), events.SiteDatabaseChanged{PreviousDatabaseID: state.Site.DatabaseID,
		DatabaseID: target.ID, BackupID: target.RestoredBackupID, DataVersion: target.DataVersion, ChangedAt: at}}, nil
}

func commandAccepted(commandID model.CommandID, operationID model.OperationID, payload string, at time.Time) events.ServerCommandAccepted {
	return events.ServerCommandAccepted{CommandID: commandID, OperationID: operationID, Payload: payload, AcceptedAt: at}
}
