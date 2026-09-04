package spec_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const gib int64 = 1 << 30

type databaseHarness struct {
	t       *testing.T
	ctx     context.Context
	runID   string
	store   *simulation.MemoryEventStore
	engine  *simulation.Engine
	handler *simulation.Handler
	nextID  int
}

func newDatabaseHarness(t *testing.T, instanceType model.InstanceType) *databaseHarness {
	t.Helper()
	store := simulation.NewMemoryEventStore()
	h := &databaseHarness{t: t, ctx: context.Background(), runID: "db-spec-" + t.Name(), store: store,
		engine: simulation.NewEngine(store), handler: simulation.NewHandler(store)}
	t.Cleanup(h.engine.Close)
	h.execute(simulation.CreateWorld{Seed: 42, StartedAt: worldStartsAt, EndsAt: worldStartsAt.AddDate(0, 1, 0)})
	h.append(events.InfrastructureConfigured{ServerProvisioningDuration: 5 * time.Minute, ConfiguredAt: worldStartsAt})
	h.execute(simulation.ConfigureServerCatalog{XBytes: 10 * gib, BackendCapacityUnits: 1_000})
	h.activeServer("backend-1", model.InstanceBackendStandard)
	h.append(events.PageConfigured{Page: model.PageProductList, LoadUnits: 1, HoldDuration: time.Second, BaseLatency: 10 * time.Millisecond, ConfiguredAt: worldStartsAt})
	h.activeServer("db-server-1", instanceType)
	h.execute(simulation.CreateDatabase{CommandID: h.id("create-db"), DatabaseID: "db-main", ServerID: "db-server-1", Name: "main"})
	h.connectInitial("db-main")
	return h
}

func (h *databaseHarness) id(prefix string) model.CommandID {
	h.nextID++
	return model.CommandID(fmt.Sprintf("%s-%d", prefix, h.nextID))
}

func (h *databaseHarness) operation(prefix string) model.OperationID {
	return model.OperationID(h.id(prefix))
}

func (h *databaseHarness) execute(command simulation.Command) []events.Event {
	h.t.Helper()
	result, err := h.engine.Execute(h.ctx, h.runID, command)
	if err != nil {
		h.t.Fatalf("execute %T: %v", command, err)
	}
	return result
}

func (h *databaseHarness) executeError(want error, command simulation.Command) {
	h.t.Helper()
	_, err := h.engine.Execute(h.ctx, h.runID, command)
	if !errors.Is(err, want) {
		h.t.Fatalf("execute %T error = %v, want %v", command, err, want)
	}
}

func (h *databaseHarness) append(items ...events.Event) {
	h.t.Helper()
	records, err := h.store.Load(h.ctx, h.runID)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err = h.store.Append(h.ctx, h.runID, uint64(len(records)), items); err != nil {
		h.t.Fatal(err)
	}
	if _, err = h.handler.State(h.ctx, h.runID); err != nil {
		h.t.Fatalf("replay given events: %v", err)
	}
}

func (h *databaseHarness) state() simulation.State {
	h.t.Helper()
	state, err := h.handler.State(h.ctx, h.runID)
	if err != nil {
		h.t.Fatal(err)
	}
	return state
}

func (h *databaseHarness) projection() simulation.Projection {
	h.t.Helper()
	records, err := h.store.Load(h.ctx, h.runID)
	if err != nil {
		h.t.Fatal(err)
	}
	return simulation.NewProjection(records, h.state())
}

func (h *databaseHarness) activeServer(id model.ServerID, instanceType model.InstanceType) {
	h.t.Helper()
	typeState := h.state().ServerTypes[instanceType]
	op := model.OperationID("initial-" + string(id))
	h.append(events.ServerProvisioningStarted{OperationID: op, ServerID: id, InstanceType: instanceType, Role: typeState.Role,
		CapacityUnits: typeState.BackendCapacityUnits, DiskBytes: typeState.DiskBytes, ConnectionLimit: typeState.ConnectionLimit,
		ConnectionHold: typeState.ConnectionHold, CostPerMonthMinor: typeState.CostPerMonthMinor, StartedAt: h.state().Clock.CurrentTime, ReadyAt: h.state().Clock.CurrentTime},
		events.ServerActivated{OperationID: op, ServerID: id, ActivatedAt: h.state().Clock.CurrentTime})
}

func (h *databaseHarness) connectInitial(databaseID model.DatabaseID) {
	h.t.Helper()
	stopCommand, stopOperation := h.id("stop"), h.operation("stop-op")
	h.execute(simulation.StopSite{CommandID: stopCommand, OperationID: stopOperation})
	h.execute(simulation.SetSiteDatabase{CommandID: h.id("set-db"), DatabaseID: databaseID})
	h.execute(simulation.StartSite{CommandID: h.id("start")})
}

func (h *databaseHarness) grow(dataBytes, logsBytes int64) []events.Event {
	h.t.Helper()
	return h.execute(simulation.GrowDatabase{CommandID: h.id("grow"), GrowthID: model.GrowthID(h.id("growth")), DataDeltaBytes: dataBytes, LogsDeltaBytes: logsBytes})
}

func (h *databaseHarness) open() []events.Event {
	h.t.Helper()
	id := h.id("request")
	return h.execute(simulation.OpenPage{RequestID: model.RequestID(id), VisitorID: model.VisitorID(id), Page: model.PageProductList})
}

func (h *databaseHarness) stop() []events.Event {
	h.t.Helper()
	return h.execute(simulation.StopSite{CommandID: h.id("stop"), OperationID: h.operation("stop-op")})
}

func eventTypes(items []events.Event) map[string]int {
	result := make(map[string]int)
	for _, item := range items {
		result[item.EventType()]++
	}
	return result
}

func requireEventTypes(t *testing.T, items []events.Event, types ...string) {
	t.Helper()
	counts := eventTypes(items)
	for _, eventType := range types {
		if counts[eventType] == 0 {
			t.Fatalf("events %v do not contain %s", counts, eventType)
		}
	}
}

func requireEqual[T comparable](t *testing.T, got, want T, name string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestDatabaseServerCatalogAndCreation(t *testing.T) {
	t.Run("catalog_has_exactly_three_database_types", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		types := h.projection().ServerTypes(model.ServerRoleDatabase)
		requireEqual(t, len(types), 3, "database type count")
		want := map[model.InstanceType]struct {
			disk, cost  int64
			connections int
		}{
			model.InstanceDBSmall: {10 * gib, 500_000, 100}, model.InstanceDBMedium: {20 * gib, 1_000_000, 200}, model.InstanceDBLarge: {40 * gib, 2_000_000, 400},
		}
		for _, got := range types {
			expected := want[got.InstanceType]
			requireEqual(t, got.DiskBytes, expected.disk, "disk")
			requireEqual(t, got.CostPerMonthMinor, expected.cost, "cost")
			requireEqual(t, got.ConnectionLimit, expected.connections, "connections")
			requireEqual(t, got.ConnectionHold, 100*time.Millisecond, "hold")
		}
	})
	t.Run("catalog_has_one_repeatable_backend_type", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		types := h.projection().ServerTypes(model.ServerRoleBackend)
		requireEqual(t, len(types), 1, "backend type count")
		requireEqual(t, types[0].BackendCapacityUnits, int64(1_000), "Y")
		h.activeServer("backend-2", model.InstanceBackendStandard)
		requireEqual(t, len(h.state().Servers), 3, "server count")
	})
	t.Run("created_database_server_uses_selected_type", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		items := h.execute(simulation.AddTypedServer{CommandID: h.id("add"), OperationID: h.operation("add-op"), ServerID: "db-medium", InstanceType: model.InstanceDBMedium})
		requireEventTypes(t, items, "ServerProvisioningStarted")
		server := h.state().Servers["db-medium"]
		requireEqual(t, server.DiskBytes, 20*gib, "disk")
		requireEqual(t, server.ConnectionLimit, 200, "connection limit")
		requireEqual(t, server.CostPerMonthMinor, int64(1_000_000), "monthly cost")
	})
	t.Run("database_can_only_be_created_on_active_database_server", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.executeError(simulation.ErrInvalidCommand, simulation.CreateDatabase{CommandID: h.id("bad"), DatabaseID: "bad", ServerID: "backend-1", Name: "bad"})
		h.execute(simulation.AddTypedServer{CommandID: h.id("add"), OperationID: h.operation("add-op"), ServerID: "provisioning", InstanceType: model.InstanceDBSmall})
		h.executeError(simulation.ErrInvalidCommand, simulation.CreateDatabase{CommandID: h.id("bad"), DatabaseID: "bad", ServerID: "provisioning", Name: "bad"})
		h.activeServer("db-server-2", model.InstanceDBSmall)
		items := h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
		requireEventTypes(t, items, "DatabaseCreated")
	})
	t.Run("database_server_cost_is_prorated_by_simulation_time", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.execute(simulation.AdvanceTime{RealElapsed: 15*24*time.Hour + 12*time.Hour})
		requireEqual(t, h.state().Costs.ServerCostMinor, int64(250_000), "half-month cost")
	})
}

func TestDatabaseConnections(t *testing.T) {
	t.Run("request_inside_connection_limit_is_accepted", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for range 99 {
			h.open()
		}
		items := h.open()
		requireEventTypes(t, items, "DatabaseConnectionOpened", "DatabaseAvailabilityChanged")
		requireEqual(t, h.state().DatabaseConnectionCounts["db-main"], 100, "active connections")
	})
	t.Run("request_over_small_connection_limit_is_rejected", func(t *testing.T) { connectionLimitContract(t, model.InstanceDBSmall, 100) })
	t.Run("all_database_type_limits_are_enforced", func(t *testing.T) {
		for _, tc := range []struct {
			typ   model.InstanceType
			limit int
		}{{model.InstanceDBSmall, 100}, {model.InstanceDBMedium, 200}, {model.InstanceDBLarge, 400}} {
			t.Run(string(tc.typ), func(t *testing.T) { connectionLimitContract(t, tc.typ, tc.limit) })
		}
	})
	t.Run("connection_is_still_busy_before_100ms", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for range 100 {
			h.open()
		}
		h.execute(simulation.AdvanceTime{RealElapsed: 99 * time.Millisecond})
		requireEventTypes(t, h.open(), "DatabaseConnectionRejected", "PageRequestRejected")
	})
	t.Run("connection_is_released_at_100ms_boundary", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for range 100 {
			h.open()
		}
		advanced := h.execute(simulation.AdvanceTime{RealElapsed: 100 * time.Millisecond})
		requireEventTypes(t, advanced, "DatabaseConnectionReleased", "DatabaseAvailabilityChanged")
		requireEventTypes(t, h.open(), "DatabaseConnectionOpened")
	})
	t.Run("backend_servers_share_one_database_pool", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.activeServer("backend-2", model.InstanceBackendStandard)
		for range 100 {
			h.open()
		}
		requireEventTypes(t, h.open(), "DatabaseConnectionRejected", "PageRequestRejected")
	})
	t.Run("rejected_request_does_not_leak_resources", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for range 100 {
			h.open()
		}
		before := h.state().UsedCapacityByServer["backend-1"]
		var rejected []events.Event
		for range 5 {
			rejected = append(rejected, h.open()...)
		}
		requireEqual(t, h.state().DatabaseConnectionCounts["db-main"], 100, "connections")
		requireEqual(t, h.state().UsedCapacityByServer["backend-1"], before, "backend load")
		entries, err := logs.Project(rejected)
		if err != nil {
			t.Fatal(err)
		}
		requireEqual(t, len(entries), 5, "failure logs")
	})
	t.Run("accepted_requests_survive_pool_saturation", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for range 100 {
			h.open()
		}
		requireEventTypes(t, h.open(), "PageRequestRejected")
		h.execute(simulation.AdvanceTime{RealElapsed: 100 * time.Millisecond})
		requireEventTypes(t, h.open(), "PageRequestCompleted")
	})
}

func connectionLimitContract(t *testing.T, instanceType model.InstanceType, limit int) {
	t.Helper()
	h := newDatabaseHarness(t, instanceType)
	for range limit {
		h.open()
	}
	before := h.state().UsedCapacityByServer["backend-1"]
	items := h.open()
	requireEventTypes(t, items, "DatabaseConnectionRejected", "PageRequestRejected")
	rejected := items[len(items)-1].(events.PageRequestRejected)
	requireEqual(t, rejected.ErrorCode, model.FailureDBConnectionLimit, "error code")
	requireEqual(t, h.state().UsedCapacityByServer["backend-1"], before, "backend load")
}

func TestDatabaseGrowthAndDiskCleanup(t *testing.T) {
	t.Run("growth_increases_data_and_logs_atomically", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		items := h.grow(2*gib, gib)
		requireEventTypes(t, items, "DatabaseGrowthRequested", "DatabaseStorageIncreased")
		db := h.state().Databases["db-main"]
		requireEqual(t, db.DataBytes, 2*gib, "data")
		requireEqual(t, db.LogsBytes, gib, "logs")
		requireEqual(t, db.DataVersion, uint64(1), "version")
	})
	t.Run("log_only_growth_keeps_data_version", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(gib, 0)
		version := h.state().Databases["db-main"].DataVersion
		h.grow(0, gib)
		requireEqual(t, h.state().Databases["db-main"].DataVersion, version, "version")
	})
	t.Run("growth_that_does_not_fit_is_blocked", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(8*gib, gib)
		items := h.grow(2*gib, gib)
		requireEventTypes(t, items, "DatabaseGrowthRequested", "DatabaseGrowthBlocked", "DatabaseAvailabilityChanged")
		db := h.state().Databases["db-main"]
		requireEqual(t, db.UsedBytes(), 9*gib, "used")
		requireEqual(t, len(h.state().PendingDatabaseGrowth), 1, "pending growth")
	})
	t.Run("full_disk_rejects_site_requests", func(t *testing.T) {
		h := blockedDiskHarness(t)
		items := h.open()
		requireEventTypes(t, items, "PageRequestRejected")
		requireEqual(t, items[len(items)-1].(events.PageRequestRejected).ErrorCode, model.FailureDiskFull, "error code")
	})
	t.Run("disk_usage_reports_consistent_counters", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(6*gib, 3*gib)
		usage, err := h.projection().DiskUsage("db-server-1")
		if err != nil {
			t.Fatal(err)
		}
		requireEqual(t, usage.UsedBytes, 9*gib, "used")
		requireEqual(t, usage.FreeBytes, gib, "free")
		requireEqual(t, usage.CleanableBytes, 3*gib, "cleanable")
	})
	t.Run("cleanup_removes_only_logs", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(6*gib, 4*gib)
		version := h.state().Databases["db-main"].DataVersion
		items := h.execute(simulation.CleanupDatabaseLogs{CommandID: h.id("cleanup"), ServerID: "db-server-1"})
		requireEventTypes(t, items, "DiskLogsCleaned")
		db := h.state().Databases["db-main"]
		requireEqual(t, db.DataBytes, 6*gib, "data")
		requireEqual(t, db.LogsBytes, int64(0), "logs")
		requireEqual(t, db.DataVersion, version, "version")
	})
	t.Run("cleanup_retries_pending_growth_once", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(6*gib, 3*gib)
		h.grow(2*gib, 0)
		items := h.execute(simulation.CleanupDatabaseLogs{CommandID: h.id("cleanup"), ServerID: "db-server-1"})
		requireEventTypes(t, items, "DiskLogsCleaned", "DatabaseStorageIncreased", "DatabaseAvailabilityChanged")
		requireEqual(t, len(h.state().PendingDatabaseGrowth), 0, "pending")
	})
	t.Run("cleanup_cannot_fix_disk_filled_by_data", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(10*gib, 0)
		h.grow(gib, 0)
		items := h.execute(simulation.CleanupDatabaseLogs{CommandID: h.id("cleanup"), ServerID: "db-server-1"})
		cleaned := items[1].(events.DiskLogsCleaned)
		requireEqual(t, cleaned.FreedBytes, int64(0), "freed")
		if _, full := h.state().Databases["db-main"].AvailabilityReasons[model.DatabaseUnavailableDiskFull]; !full {
			t.Fatal("disk_full was cleared")
		}
	})
}

func blockedDiskHarness(t *testing.T) *databaseHarness {
	t.Helper()
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	h.grow(9*gib, gib)
	h.grow(gib, 0)
	return h
}

func TestDatabaseBackup(t *testing.T) {
	t.Run("connected_database_backup_requires_stopped_site", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.executeError(simulation.ErrSiteNotStopped, simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
		h.open()
		h.stop()
		h.executeError(simulation.ErrSiteNotStopped, simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
	})
	t.Run("site_stop_drains_database_connections", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.open()
		requireEventTypes(t, h.stop(), "SiteStopStarted")
		items := h.execute(simulation.AdvanceTime{RealElapsed: 100 * time.Millisecond})
		requireEventTypes(t, items, "DatabaseConnectionReleased", "SiteStopped")
		requireEqual(t, h.state().Site.Status, model.SiteStopped, "site")
	})
	t.Run("backup_copies_data_without_logs", func(t *testing.T) { backupContentsContract(t, false) })
	t.Run("backup_is_possible_on_full_source_disk", func(t *testing.T) { backupContentsContract(t, true) })
	t.Run("failed_backup_does_not_change_source", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(7*gib, 2*gib)
		h.stop()
		before := h.state().Databases["db-main"]
		items := h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "failed", DatabaseID: "db-main", Fail: true})
		requireEventTypes(t, items, "DatabaseBackupStarted", "DatabaseBackupFailed")
		after := h.state().Databases["db-main"]
		if before.DataBytes != after.DataBytes || before.DataVersion != after.DataVersion {
			t.Fatal("failed backup changed source")
		}
		h.activeServer("db-server-2", model.InstanceDBMedium)
		h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
		h.executeError(simulation.ErrBackupNotReady, simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "failed", DatabaseID: "db-new"})
	})
	t.Run("backup_survives_source_server_deletion", func(t *testing.T) {
		h := migratedHarness(t, 5*gib, 0)
		beforeCost := h.state().Costs.BackupStorageCostMinor
		h.execute(simulation.RemoveServer{CommandID: h.id("delete"), OperationID: h.operation("delete-op"), ServerID: "db-server-1"})
		h.execute(simulation.AdvanceTime{RealElapsed: time.Hour})
		requireEqual(t, len(h.projection().DatabaseBackups("db-main")), 1, "backups")
		if h.state().Costs.BackupStorageCostMinor <= beforeCost {
			t.Fatal("backup storage cost did not continue")
		}
	})
}

func backupContentsContract(t *testing.T, full bool) {
	t.Helper()
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	logsBytes := int64(2 * gib)
	dataBytes := int64(7 * gib)
	if full {
		dataBytes = 8 * gib
	}
	h.grow(dataBytes, logsBytes)
	h.stop()
	items := h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
	requireEventTypes(t, items, "DatabaseBackupStarted", "DatabaseBackupCompleted")
	backup := h.state().Backups["backup"]
	requireEqual(t, backup.DataBytes, dataBytes, "backup data")
	requireEqual(t, backup.DataVersion, uint64(1), "backup version")
}

func TestDatabaseRestoreAndSwitch(t *testing.T) {
	t.Run("restore_ready_backup_into_empty_database", func(t *testing.T) {
		h := migratedHarness(t, 7*gib, 2*gib)
		db := h.state().Databases["db-new"]
		requireEqual(t, db.DataBytes, 7*gib, "restored data")
		requireEqual(t, db.LogsBytes, int64(0), "restored logs")
		requireEqual(t, db.DataVersion, uint64(1), "version")
	})
	t.Run("restore_rejects_nonempty_database", func(t *testing.T) {
		h := backupAndTargetHarness(t, 2*gib)
		h.append(events.DatabaseGrowthRequested{GrowthID: "target-growth", DataDeltaBytes: gib, RequestedAt: h.state().Clock.CurrentTime}, events.DatabaseStorageIncreased{GrowthID: "target-growth", DatabaseID: "db-new", ServerID: "db-server-2", DataBytesAdded: gib, DataVersion: 1, IncreasedAt: h.state().Clock.CurrentTime})
		h.executeError(simulation.ErrDatabaseNotEmpty, simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new"})
	})
	t.Run("restore_rejects_unready_backup", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.stop()
		h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main", Fail: true})
		h.activeServer("db-server-2", model.InstanceDBMedium)
		h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
		h.executeError(simulation.ErrBackupNotReady, simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new"})
	})
	t.Run("restore_rejects_insufficient_target_disk", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBLarge)
		h.grow(15*gib, 0)
		h.stop()
		h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
		h.activeServer("db-server-2", model.InstanceDBSmall)
		h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
		h.executeError(simulation.ErrInsufficientDisk, simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new"})
	})
	t.Run("failed_restore_never_makes_database_ready", func(t *testing.T) {
		h := backupAndTargetHarness(t, 2*gib)
		h.execute(simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new", Fail: true})
		if h.state().Databases["db-new"].Ready {
			t.Fatal("failed target is ready")
		}
		h.executeError(simulation.ErrDatabaseNotReady, simulation.SetSiteDatabase{CommandID: h.id("set"), DatabaseID: "db-new", ExpectedCurrentDatabaseID: "db-main"})
	})
	t.Run("database_switch_requires_stopped_site_and_expected_current_id", func(t *testing.T) {
		h := backupAndTargetHarness(t, 2*gib)
		h.execute(simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new"})
		h.executeError(simulation.ErrSiteConfigConflict, simulation.SetSiteDatabase{CommandID: h.id("set"), DatabaseID: "db-new", ExpectedCurrentDatabaseID: "wrong"})
		items := h.execute(simulation.SetSiteDatabase{CommandID: h.id("set"), DatabaseID: "db-new", ExpectedCurrentDatabaseID: "db-main"})
		requireEventTypes(t, items, "SiteDatabaseChanged")
	})
	t.Run("database_switch_rejects_stale_backup", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(gib, 0)
		h.stop()
		h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "old", DatabaseID: "db-main"})
		h.execute(simulation.StartSite{CommandID: h.id("start")})
		h.grow(gib, 0)
		h.stop()
		h.activeServer("db-server-2", model.InstanceDBMedium)
		h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
		h.execute(simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "old", DatabaseID: "db-new"})
		h.executeError(simulation.ErrDatabaseBackupStale, simulation.SetSiteDatabase{CommandID: h.id("set"), DatabaseID: "db-new", ExpectedCurrentDatabaseID: "db-main"})
	})
	t.Run("switch_does_not_start_site_automatically", func(t *testing.T) {
		h := migratedHarnessStopped(t, 2*gib, 0)
		requireEqual(t, h.state().Site.Status, model.SiteStopped, "site status")
		h.execute(simulation.StartSite{CommandID: h.id("start")})
		h.grow(gib, 0)
		requireEqual(t, h.state().Databases["db-new"].DataBytes, 3*gib, "new database data")
	})
}

func backupAndTargetHarness(t *testing.T, dataBytes int64) *databaseHarness {
	t.Helper()
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	h.grow(dataBytes, 0)
	h.stop()
	h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
	h.activeServer("db-server-2", model.InstanceDBMedium)
	h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
	return h
}

func migratedHarnessStopped(t *testing.T, dataBytes, logsBytes int64) *databaseHarness {
	t.Helper()
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	h.grow(dataBytes, logsBytes)
	h.stop()
	h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
	h.activeServer("db-server-2", model.InstanceDBMedium)
	h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
	h.execute(simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new"})
	h.execute(simulation.SetSiteDatabase{CommandID: h.id("set"), DatabaseID: "db-new", ExpectedCurrentDatabaseID: "db-main"})
	return h
}

func migratedHarness(t *testing.T, dataBytes, logsBytes int64) *databaseHarness {
	t.Helper()
	h := migratedHarnessStopped(t, dataBytes, logsBytes)
	h.execute(simulation.StartSite{CommandID: h.id("start")})
	return h
}

func TestDatabaseEndToEndAndDurability(t *testing.T) {
	t.Run("connection_overload_is_fixed_by_migration", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for range 100 {
			h.open()
		}
		requireEventTypes(t, h.open(), "PageRequestRejected")
		h.stop()
		h.execute(simulation.AdvanceTime{RealElapsed: 100 * time.Millisecond})
		h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
		h.activeServer("db-server-2", model.InstanceDBMedium)
		h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
		h.execute(simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new"})
		h.execute(simulation.SetSiteDatabase{CommandID: h.id("set"), DatabaseID: "db-new", ExpectedCurrentDatabaseID: "db-main"})
		h.execute(simulation.StartSite{CommandID: h.id("start")})
		for range 200 {
			h.open()
		}
		requireEventTypes(t, h.open(), "PageRequestRejected")
	})
	t.Run("log_filled_disk_is_fixed_by_cleanup", func(t *testing.T) {
		h := blockedDiskHarness(t)
		requireEventTypes(t, h.open(), "PageRequestRejected")
		h.execute(simulation.CleanupDatabaseLogs{CommandID: h.id("cleanup"), ServerID: "db-server-1"})
		requireEventTypes(t, h.open(), "PageRequestCompleted")
	})
	t.Run("data_filled_disk_requires_lossless_migration", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(10*gib, 0)
		h.grow(gib, 0)
		h.execute(simulation.CleanupDatabaseLogs{CommandID: h.id("cleanup"), ServerID: "db-server-1"})
		h.stop()
		h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup-op"), BackupID: "backup", DatabaseID: "db-main"})
		h.activeServer("db-server-2", model.InstanceDBMedium)
		h.execute(simulation.CreateDatabase{CommandID: h.id("create"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
		h.execute(simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore-op"), BackupID: "backup", DatabaseID: "db-new"})
		requireEqual(t, h.state().Databases["db-new"].DataBytes, 10*gib, "migrated data")
	})
	t.Run("command_retries_do_not_duplicate_database_effects", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		command := simulation.GrowDatabase{CommandID: "retry-growth", GrowthID: "retry-growth", DataDeltaBytes: gib}
		first := h.execute(command)
		version := h.state().Version
		second := h.execute(command)
		if len(first) == 0 || len(second) != 0 {
			t.Fatalf("retry events: first=%d second=%d", len(first), len(second))
		}
		requireEqual(t, h.state().Version, version, "journal version")
	})
	t.Run("journal_replay_restores_database_infrastructure", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.grow(9*gib, 0)
		h.grow(2*gib, 0)
		h.open()
		h.stop()
		first := h.state()
		second := h.state()
		if !reflect.DeepEqual(first, second) {
			t.Fatal("replay is not deterministic")
		}
		h.execute(simulation.AdvanceTime{RealElapsed: time.Second})
		overview := h.projection().Overview()
		if overview.Uptime >= 1 {
			t.Fatalf("uptime = %f, want downtime included", overview.Uptime)
		}
	})
}
