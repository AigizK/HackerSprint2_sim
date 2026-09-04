package simulation

import (
	"reflect"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestDatabaseInfrastructureEventsRoundTripThroughCodec(t *testing.T) {
	at := time.Date(2032, 1, 2, 3, 4, 5, 6, time.UTC)
	all := []events.Event{
		events.ServerTypeDefined{InstanceType: model.InstanceDBSmall, Role: model.ServerRoleDatabase, DiskBytes: 10 << 30, ConnectionLimit: 100, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBSmallCostPerMonthMinor, DefinedAt: at},
		events.DatabaseCreated{DatabaseID: "db", ServerID: "server", Name: "main", CreatedAt: at},
		events.DatabaseConnectionOpened{RequestID: "request", DatabaseID: "db", ServerID: "server", OpenedAt: at, ReleasesAt: at.Add(model.DBConnectionHold)},
		events.DatabaseConnectionReleased{RequestID: "request", DatabaseID: "db", ServerID: "server", ReleasedAt: at},
		events.DatabaseConnectionRejected{RequestID: "request-2", DatabaseID: "db", ServerID: "server", ActiveConnections: 100, ConnectionLimit: 100, RejectedAt: at},
		events.DatabaseAvailabilityChanged{DatabaseID: "db", ServerID: "server", Reasons: []model.DatabaseAvailabilityReason{model.DatabaseUnavailableDiskFull}, ChangedAt: at},
		events.DatabaseGrowthRequested{GrowthID: "growth", DataDeltaBytes: 1, LogsDeltaBytes: 2, RequestedAt: at},
		events.DatabaseStorageIncreased{GrowthID: "growth", DatabaseID: "db", ServerID: "server", DataBytesAdded: 1, LogsBytesAdded: 2, DataVersion: 3, IncreasedAt: at},
		events.DatabaseGrowthBlocked{GrowthID: "growth", DatabaseID: "db", ServerID: "server", PendingDataBytes: 1, PendingLogsBytes: 2, RequiredBytes: 3, FreeBytes: 0, BlockedAt: at},
		events.DiskLogsCleaned{ServerID: "server", FreedBytes: 2, CleanedAt: at},
		events.DatabaseBackupStarted{OperationID: "operation", BackupID: "backup", DatabaseID: "db", ServerID: "server", StartedAt: at},
		events.DatabaseBackupCompleted{OperationID: "operation", BackupID: "backup", DatabaseID: "db", ServerID: "server", DataBytes: 4, DataVersion: 3, StorageCostPerHourMinor: 100, CompletedAt: at},
		events.DatabaseBackupFailed{OperationID: "operation", BackupID: "backup", DatabaseID: "db", ErrorCode: "FAILED", Message: "failed", FailedAt: at},
		events.BackupStorageCostAccrued{BackupID: "backup", From: at, To: at.Add(time.Hour), BilledHours: 1, AmountMinor: 100},
		events.DatabaseRestoreStarted{OperationID: "operation", BackupID: "backup", DatabaseID: "db", ServerID: "server", StartedAt: at},
		events.DatabaseRestoreCompleted{OperationID: "operation", BackupID: "backup", DatabaseID: "db", ServerID: "server", DataBytes: 4, DataVersion: 3, CompletedAt: at},
		events.DatabaseRestoreFailed{OperationID: "operation", BackupID: "backup", DatabaseID: "db", ErrorCode: "FAILED", Message: "failed", FailedAt: at},
		events.SiteStopStarted{OperationID: "operation", StartedAt: at},
		events.SiteStopped{OperationID: "operation", StoppedAt: at},
		events.SiteDatabaseChanged{PreviousDatabaseID: "old", DatabaseID: "db", BackupID: "backup", DataVersion: 3, ChangedAt: at},
		events.SiteStarted{DatabaseID: "db", StartedAt: at},
	}
	for _, original := range all {
		t.Run(original.EventType(), func(t *testing.T) {
			eventType, payload, err := EncodeEvent(original)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeEvent(eventType, payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, original) {
				t.Fatalf("decoded = %#v, want %#v", decoded, original)
			}
		})
	}
}

func TestDatabaseServerTypeConstants(t *testing.T) {
	if model.InstanceDBSmall != "db.small" || model.InstanceDBMedium != "db.medium" || model.InstanceDBLarge != "db.large" ||
		model.DBSmallConnections != 100 || model.DBMediumConnections != 200 || model.DBLargeConnections != 400 ||
		model.DBConnectionHold != 100*time.Millisecond || model.DBSmallCostPerMonthMinor != 500_000 ||
		model.DBMediumCostPerMonthMinor != 1_000_000 || model.DBLargeCostPerMonthMinor != 2_000_000 {
		t.Fatal("database server catalog constants differ from the agreed contract")
	}
}
