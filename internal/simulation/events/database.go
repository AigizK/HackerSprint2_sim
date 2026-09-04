package events

import "time"

type ServerTypeDefined struct {
	InstanceType         InstanceType
	Role                 ServerRole
	DiskBytes            int64
	ConnectionLimit      int
	ConnectionHold       time.Duration
	BackendCapacityUnits int64
	CostPerHourMinor     int64
	CostPerMonthMinor    int64
	DefinedAt            time.Time
}

func (ServerTypeDefined) EventType() string { return "ServerTypeDefined" }

type DatabaseCreated struct {
	DatabaseID DatabaseID
	ServerID   ServerID
	Name       string
	CreatedAt  time.Time
}

func (DatabaseCreated) EventType() string { return "DatabaseCreated" }

type DatabaseDeleted struct {
	DatabaseID DatabaseID
	ServerID   ServerID
	DeletedAt  time.Time
}

func (DatabaseDeleted) EventType() string { return "DatabaseDeleted" }

type DatabaseConnectionOpened struct {
	RequestID  RequestID
	DatabaseID DatabaseID
	ServerID   ServerID
	OpenedAt   time.Time
	ReleasesAt time.Time
}

func (DatabaseConnectionOpened) EventType() string { return "DatabaseConnectionOpened" }

type DatabaseConnectionReleased struct {
	RequestID  RequestID
	DatabaseID DatabaseID
	ServerID   ServerID
	ReleasedAt time.Time
}

func (DatabaseConnectionReleased) EventType() string { return "DatabaseConnectionReleased" }

type DatabaseConnectionRejected struct {
	RequestID         RequestID
	DatabaseID        DatabaseID
	ServerID          ServerID
	ActiveConnections int
	ConnectionLimit   int
	RejectedAt        time.Time
}

func (DatabaseConnectionRejected) EventType() string { return "DatabaseConnectionRejected" }

type DatabaseAvailabilityChanged struct {
	DatabaseID DatabaseID
	ServerID   ServerID
	Available  bool
	Reasons    []DatabaseAvailabilityReason
	ChangedAt  time.Time
}

func (DatabaseAvailabilityChanged) EventType() string { return "DatabaseAvailabilityChanged" }

type DatabaseGrowthRequested struct {
	GrowthID       GrowthID
	DataDeltaBytes int64
	LogsDeltaBytes int64
	RequestedAt    time.Time
}

func (DatabaseGrowthRequested) EventType() string { return "DatabaseGrowthRequested" }

type DatabaseStorageIncreased struct {
	GrowthID       GrowthID
	DatabaseID     DatabaseID
	ServerID       ServerID
	DataBytesAdded int64
	LogsBytesAdded int64
	DataVersion    uint64
	IncreasedAt    time.Time
}

func (DatabaseStorageIncreased) EventType() string { return "DatabaseStorageIncreased" }

type DatabaseGrowthBlocked struct {
	GrowthID         GrowthID
	DatabaseID       DatabaseID
	ServerID         ServerID
	PendingDataBytes int64
	PendingLogsBytes int64
	RequiredBytes    int64
	FreeBytes        int64
	BlockedAt        time.Time
}

func (DatabaseGrowthBlocked) EventType() string { return "DatabaseGrowthBlocked" }

type DiskLogsCleaned struct {
	ServerID   ServerID
	FreedBytes int64
	CleanedAt  time.Time
}

func (DiskLogsCleaned) EventType() string { return "DiskLogsCleaned" }

// DiskLogsGrowthRequested is a scheduled log-growth occurrence for either a
// backend or database server. The following outcome is exactly one of
// DiskLogsIncreased or DiskLogsGrowthBlocked.
type DiskLogsGrowthRequested struct {
	GrowthID    GrowthID
	ServerID    ServerID
	DeltaBytes  int64
	RequestedAt time.Time
}

func (DiskLogsGrowthRequested) EventType() string { return "DiskLogsGrowthRequested" }

type DiskLogsIncreased struct {
	GrowthID    GrowthID
	ServerID    ServerID
	BytesAdded  int64
	IncreasedAt time.Time
}

func (DiskLogsIncreased) EventType() string { return "DiskLogsIncreased" }

type DiskLogsGrowthBlocked struct {
	GrowthID      GrowthID
	ServerID      ServerID
	RequiredBytes int64
	FreeBytes     int64
	BlockedAt     time.Time
}

func (DiskLogsGrowthBlocked) EventType() string { return "DiskLogsGrowthBlocked" }

type DatabaseBackupStarted struct {
	OperationID OperationID
	BackupID    BackupID
	DatabaseID  DatabaseID
	ServerID    ServerID
	StartedAt   time.Time
}

func (DatabaseBackupStarted) EventType() string { return "DatabaseBackupStarted" }

type DatabaseBackupCompleted struct {
	OperationID             OperationID
	BackupID                BackupID
	DatabaseID              DatabaseID
	ServerID                ServerID
	DataBytes               int64
	DataVersion             uint64
	StorageCostPerHourMinor int64
	CompletedAt             time.Time
}

func (DatabaseBackupCompleted) EventType() string { return "DatabaseBackupCompleted" }

type DatabaseBackupFailed struct {
	OperationID OperationID
	BackupID    BackupID
	DatabaseID  DatabaseID
	ErrorCode   string
	Message     string
	FailedAt    time.Time
}

func (DatabaseBackupFailed) EventType() string { return "DatabaseBackupFailed" }

type BackupStorageCostAccrued struct {
	BackupID    BackupID
	From        time.Time
	To          time.Time
	BilledHours int64
	AmountMinor int64
}

func (BackupStorageCostAccrued) EventType() string { return "BackupStorageCostAccrued" }

type DatabaseRestoreStarted struct {
	OperationID OperationID
	BackupID    BackupID
	DatabaseID  DatabaseID
	ServerID    ServerID
	StartedAt   time.Time
}

func (DatabaseRestoreStarted) EventType() string { return "DatabaseRestoreStarted" }

type DatabaseRestoreCompleted struct {
	OperationID OperationID
	BackupID    BackupID
	DatabaseID  DatabaseID
	ServerID    ServerID
	DataBytes   int64
	DataVersion uint64
	CompletedAt time.Time
}

func (DatabaseRestoreCompleted) EventType() string { return "DatabaseRestoreCompleted" }

type DatabaseRestoreFailed struct {
	OperationID OperationID
	BackupID    BackupID
	DatabaseID  DatabaseID
	ErrorCode   string
	Message     string
	FailedAt    time.Time
}

func (DatabaseRestoreFailed) EventType() string { return "DatabaseRestoreFailed" }

type SiteStopStarted struct {
	OperationID OperationID
	StartedAt   time.Time
}

func (SiteStopStarted) EventType() string { return "SiteStopStarted" }

type SiteStopped struct {
	OperationID OperationID
	StoppedAt   time.Time
}

func (SiteStopped) EventType() string { return "SiteStopped" }

type SiteDatabaseChanged struct {
	PreviousDatabaseID DatabaseID
	DatabaseID         DatabaseID
	BackupID           BackupID
	DataVersion        uint64
	ChangedAt          time.Time
}

func (SiteDatabaseChanged) EventType() string { return "SiteDatabaseChanged" }

type SiteStarted struct {
	DatabaseID DatabaseID
	StartedAt  time.Time
}

func (SiteStarted) EventType() string { return "SiteStarted" }
