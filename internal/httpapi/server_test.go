package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestHTTPValidationAndErrorsFollowOpenAPI(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()

	invalidSeed := callJSON(t, api, http.MethodPost, "/v2/start", map[string]any{
		"seed": 0, "agent_id": "http-agent", "agent_version": "v1", "request_id": "invalid-seed",
	}, http.StatusBadRequest)
	if stringField(t, invalidSeed, "error") != "INVALID_SEED" {
		t.Fatalf("invalid seed = %#v", invalidSeed)
	}

	start := callJSON(t, api, http.MethodPost, "/v2/start", map[string]any{
		"seed": -1, "agent_id": "http-agent", "agent_version": "v1", "request_id": "start-errors",
	}, http.StatusCreated)
	runID := stringField(t, start, "run_id")
	tooShort := callJSON(t, api, http.MethodPost, "/v2/runs/"+runID+"/time/advance", map[string]any{
		"request_id": "short", "duration_seconds": 60,
	}, http.StatusBadRequest)
	if stringField(t, tooShort, "error") != "MINIMUM_ADVANCE_IS_300_SECONDS" {
		t.Fatalf("minimum advance = %#v", tooShort)
	}

	unknown := callJSON(t, api, http.MethodGet, "/v2/runs/1234567890abcdefghijklmn/overview", nil, http.StatusNotFound)
	if stringField(t, unknown, "error") != "RUN_NOT_FOUND" {
		t.Fatalf("unknown run = %#v", unknown)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/start", bytes.NewBufferString(`{"seed":-1,"unknown":true}`))
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

}

func newTestServer(t *testing.T) (*Server, func(), time.Time) {
	t.Helper()
	ctx := context.Background()
	storage, err := persistence.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	startsAt := time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	bootstrap := []events.Event{
		events.InboxMessageDelivered{MessageID: "message-1", SenderEmail: "director@example.com", SentAt: startsAt,
			Subject: "Service brief", Description: "Keep the service available with minimal infrastructure cost."},
		events.PageConfigured{Page: model.PageProductList, LoadUnits: 10, HoldDuration: time.Minute, BaseLatency: 20 * time.Millisecond, ConfiguredAt: startsAt},
		events.PageConfigured{Page: model.PageProduct, LoadUnits: 20, HoldDuration: time.Minute, BaseLatency: 30 * time.Millisecond, ConfiguredAt: startsAt},
		events.InfrastructureConfigured{ServerProvisioningDuration: 5 * time.Minute, ConfiguredAt: startsAt},
		events.CostsConfigured{Currency: "USD", ConfiguredAt: startsAt},
		events.ServerTypeDefined{InstanceType: model.InstanceBackendStandard, Role: model.ServerRoleBackend, DiskBytes: 10 << 30, BackendCapacityUnits: 100, CostPerHourMinor: 100, DefinedAt: startsAt},
		events.ServerTypeDefined{InstanceType: model.InstanceDBSmall, Role: model.ServerRoleDatabase, DiskBytes: 10 << 30, ConnectionLimit: model.DBSmallConnections, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBSmallCostPerMonthMinor, DefinedAt: startsAt},
		events.ServerTypeDefined{InstanceType: model.InstanceDBMedium, Role: model.ServerRoleDatabase, DiskBytes: 20 << 30, ConnectionLimit: model.DBMediumConnections, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBMediumCostPerMonthMinor, DefinedAt: startsAt},
		events.ServerTypeDefined{InstanceType: model.InstanceDBLarge, Role: model.ServerRoleDatabase, DiskBytes: 40 << 30, ConnectionLimit: model.DBLargeConnections, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBLargeCostPerMonthMinor, DefinedAt: startsAt},
		events.ServerProvisioningStarted{OperationID: "initial-server", ServerID: "server-1", Name: "backend-main", InstanceType: model.InstanceBackendStandard, Role: model.ServerRoleBackend, CapacityUnits: 100, DiskBytes: 10 << 30, CostPerHourMinor: 100, StartedAt: startsAt, ReadyAt: startsAt},
		events.ServerActivated{OperationID: "initial-server", ServerID: "server-1", ActivatedAt: startsAt},
		events.ServerProvisioningStarted{OperationID: "initial-database", ServerID: "db-server-1", Name: "database-main", InstanceType: model.InstanceDBSmall, Role: model.ServerRoleDatabase, DiskBytes: 10 << 30, ConnectionLimit: model.DBSmallConnections, ConnectionHold: model.DBConnectionHold, CostPerMonthMinor: model.DBSmallCostPerMonthMinor, StartedAt: startsAt, ReadyAt: startsAt},
		events.ServerActivated{OperationID: "initial-database", ServerID: "db-server-1", ActivatedAt: startsAt},
		events.DatabaseCreated{DatabaseID: "db-main", ServerID: "db-server-1", Name: "service-main", CreatedAt: startsAt},
		events.DatabaseGrowthRequested{GrowthID: "initial-growth", DataDeltaBytes: 1 << 30, LogsDeltaBytes: 1 << 29, RequestedAt: startsAt},
		events.DatabaseStorageIncreased{GrowthID: "initial-growth", DatabaseID: "db-main", ServerID: "db-server-1", DataBytesAdded: 1 << 30, LogsBytesAdded: 1 << 29, DataVersion: 1, IncreasedAt: startsAt},
		events.SiteStopStarted{OperationID: "initial-site-stop", StartedAt: startsAt},
		events.SiteStopped{OperationID: "initial-site-stop", StoppedAt: startsAt},
		events.SiteDatabaseChanged{DatabaseID: "db-main", DataVersion: 1, ChangedAt: startsAt},
		events.SiteStarted{DatabaseID: "db-main", StartedAt: startsAt},
		events.ProductAdded{ProductID: "product-1", Name: "Product", PriceMinor: 1_000, ViewProbabilityPPM: 1_000_000, AddedAt: startsAt},
	}
	rotationAt := startsAt.Add(30 * time.Minute)
	worldEvents := events.EventSchedule{{Sequence: 1, OccursAt: rotationAt, Event: events.ServerCredentialRotationRequested{
		RotationID: "rotate-initial-database", ServerID: "db-server-1", RequestedAt: rotationAt,
	}}}
	if _, err := application.RegisterManualWorld(ctx, storage.Catalog, application.ManualWorldInput{
		Seed: -1, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour), Bootstrap: bootstrap, Events: worldEvents,
	}); err != nil {
		storage.Close()
		t.Fatal(err)
	}
	manager := application.NewRunManager(storage.Catalog, storage.Journal, storage.Journal, 4)
	server := New(application.NewStartRunService(storage.Catalog, storage.Journal, nil, storage.Journal), application.NewRunService(manager, storage.Catalog))
	auditNumber := 0
	server.newAuditID = func() (string, error) {
		auditNumber++
		return fmt.Sprintf("audit-%d", auditNumber), nil
	}
	return server, func() { _ = storage.Close() }, startsAt
}

func callJSON(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	return callJSONBasic(t, handler, method, path, body, wantStatus, "", "")
}

func callJSONBasic(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int, username, password string) map[string]any {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	if username != "" || password != "" {
		request.SetBasicAuth(username, password)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, recorder.Code, wantStatus, recorder.Body.String())
	}
	result := make(map[string]any)
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s %s: decode response: %v; body=%s", method, path, err, recorder.Body.String())
	}
	return result
}

func stringField(t *testing.T, value map[string]any, name string) string {
	t.Helper()
	result, ok := value[name].(string)
	if !ok {
		t.Fatalf("field %s is not a string in %#v", name, value)
	}
	return result
}

func numberField(t *testing.T, value map[string]any, name string) float64 {
	t.Helper()
	result, ok := value[name].(float64)
	if !ok {
		t.Fatalf("field %s is not a number in %#v", name, value)
	}
	return result
}
