package model

import "time"

type ServerRole string
type InstanceType string
type DatabaseID string
type BackupID string
type GrowthID string
type DatabaseAvailabilityReason string
type SiteLifecycleStatus string
type BackupLifecycleStatus string

const (
	ServerRoleBackend  ServerRole = "backend"
	ServerRoleDatabase ServerRole = "database"

	InstanceBackendStandard InstanceType = "backend.standard"
	InstanceDBSmall         InstanceType = "db.small"
	InstanceDBMedium        InstanceType = "db.medium"
	InstanceDBLarge         InstanceType = "db.large"

	DBSmallConnections                     = 100
	DBMediumConnections                    = 200
	DBLargeConnections                     = 400
	DBConnectionHold                       = 100 * time.Millisecond
	DBSmallCostPerMonthMinor         int64 = 500_000
	DBMediumCostPerMonthMinor        int64 = 1_000_000
	DBLargeCostPerMonthMinor         int64 = 2_000_000
	BackupStorageCostPerGiBHourMinor int64 = 100

	DatabaseUnavailableDiskFull        DatabaseAvailabilityReason = "disk_full"
	DatabaseUnavailableConnectionLimit DatabaseAvailabilityReason = "connection_limit"

	SiteRunning  SiteLifecycleStatus = "running"
	SiteStopping SiteLifecycleStatus = "stopping"
	SiteStopped  SiteLifecycleStatus = "stopped"

	BackupCreating BackupLifecycleStatus = "creating"
	BackupReady    BackupLifecycleStatus = "ready"
	BackupFailed   BackupLifecycleStatus = "failed"
)
