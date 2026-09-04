package simulation

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestLogsCanFilterByErrorPresenceAndType(t *testing.T) {
	t.Parallel()
	at := time.Date(2032, 1, 1, 12, 0, 0, 0, time.UTC)
	records := []StoredEvent{
		{Version: 1, Event: events.PageRequestStarted{RequestID: "ok", Source: model.RequestSourceVisitor, Page: model.PageProductList, StartedAt: at}},
		{Version: 2, Event: events.PageRequestCompleted{RequestID: "ok", StatusCode: 200, CompletedAt: at}},
		{Version: 3, Event: events.PageRequestStarted{RequestID: "failed", Source: model.RequestSourceVisitor, Page: model.PageProductList, StartedAt: at}},
		{Version: 4, Event: events.PageRequestRejected{RequestID: "failed", StatusCode: 503, ErrorCode: model.FailureDiskFull, Message: "disk full", RejectedAt: at}},
	}
	hasError := true
	view, err := NewProjection(records, NewState()).Logs(LogsQuery{HasError: &hasError, ErrorCode: model.FailureDiskFull, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Logs) != 1 || view.Logs[0].Entry.RequestID != "failed" || view.Logs[0].Entry.ErrorCode != model.FailureDiskFull {
		t.Fatalf("filtered logs = %#v", view.Logs)
	}

	hasError = false
	view, err = NewProjection(records, NewState()).Logs(LogsQuery{HasError: &hasError, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Logs) != 1 || view.Logs[0].Entry.RequestID != "ok" {
		t.Fatalf("successful logs = %#v", view.Logs)
	}
}

func TestInfrastructureSeriesExposeConnectionsAndDiskParts(t *testing.T) {
	t.Parallel()
	at := time.Date(2032, 1, 1, 12, 0, 0, 0, time.UTC)
	records := []StoredEvent{
		{Version: 1, Event: events.ServerProvisioningStarted{OperationID: "db-operation", ServerID: "db-server", Role: model.ServerRoleDatabase, DiskBytes: 10 << 30, ConnectionLimit: 100, StartedAt: at, ReadyAt: at}},
		{Version: 2, Event: events.DatabaseCreated{DatabaseID: "db-main", ServerID: "db-server", Name: "main", CreatedAt: at}},
		{Version: 3, Event: events.DatabaseStorageIncreased{GrowthID: "growth", DatabaseID: "db-main", ServerID: "db-server", DataBytesAdded: 2 << 30, LogsBytesAdded: 1 << 30, DataVersion: 1, IncreasedAt: at}},
		{Version: 4, Event: events.SiteDatabaseChanged{DatabaseID: "db-main", ChangedAt: at}},
		{Version: 5, Event: events.DatabaseConnectionOpened{RequestID: "request", DatabaseID: "db-main", ServerID: "db-server", OpenedAt: at.Add(time.Minute), ReleasesAt: at.Add(2 * time.Minute)}},
		{Version: 6, Event: events.DatabaseConnectionReleased{RequestID: "request", DatabaseID: "db-main", ServerID: "db-server", ReleasedAt: at.Add(2 * time.Minute)}},
	}
	state := NewState()
	state.Clock = ClockState{StartedAt: at, CurrentTime: at.Add(2 * time.Minute), EndsAt: at.Add(time.Hour)}
	wanted := map[string]bool{"database_active_connections": true, "database_connection_limit": true, "disk_database_bytes": true, "disk_logs_bytes": true, "disk_free_bytes": true}
	points := historicalInfrastructurePoints(records, state, MetricsQuery{From: at, To: at.Add(2 * time.Minute), Step: time.Minute}, func(name string) bool { return wanted[name] })

	lookup := make(map[string]map[time.Time]float64)
	for _, point := range points {
		if lookup[point.Name] == nil {
			lookup[point.Name] = make(map[time.Time]float64)
		}
		lookup[point.Name][point.Timestamp] = point.Value
	}
	if lookup["database_active_connections"][at.Add(time.Minute)] != 1 || lookup["database_active_connections"][at.Add(2*time.Minute)] != 0 ||
		lookup["database_connection_limit"][at] != 100 || lookup["disk_database_bytes"][at] != 2<<30 ||
		lookup["disk_logs_bytes"][at] != 1<<30 || lookup["disk_free_bytes"][at] != 7<<30 {
		t.Fatalf("unexpected infrastructure points: %#v", lookup)
	}
}
