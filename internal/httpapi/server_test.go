package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestAllOpenAPIEndpoints(t *testing.T) {
	api, closeStorage, startsAt := newTestServer(t)
	defer closeStorage()

	seen := make(map[string]bool)
	startBody := map[string]any{
		"seed": -1, "agent_id": "http-agent", "agent_version": "v1", "request_id": "start-1",
	}
	start := callJSON(t, api, http.MethodPost, "/v1/start", startBody, http.StatusCreated)
	seen["startRun"] = true
	runID := stringField(t, start, "run_id")
	repeatedStart := callJSON(t, api, http.MethodPost, "/v1/start", startBody, http.StatusOK)
	if stringField(t, repeatedStart, "run_id") != runID {
		t.Fatalf("idempotent start returned another run: %#v", repeatedStart)
	}
	base := "/v1/runs/" + runID

	overview := callJSON(t, api, http.MethodGet, base+"/overview", nil, http.StatusOK)
	seen["getOverview"] = true
	if stringField(t, overview, "status") != "running" {
		t.Fatalf("overview = %#v", overview)
	}

	probe := callJSON(t, api, http.MethodPost, base+"/probes", map[string]any{
		"request_id": "probe-1", "page": "product_list",
	}, http.StatusOK)
	seen["probePage"] = true
	if numberField(t, probe, "status") != 200 {
		t.Fatalf("probe = %#v", probe)
	}

	logs := callJSON(t, api, http.MethodGet, base+"/logs?status=200&limit=100", nil, http.StatusOK)
	seen["getSiteLogs"] = true
	if items, ok := logs["logs"].([]any); !ok || len(items) == 0 {
		t.Fatalf("logs = %#v", logs)
	}

	metricsQuery := url.Values{}
	metricsQuery.Set("from", startsAt.Format(time.RFC3339))
	metricsQuery.Set("to", startsAt.Add(10*time.Minute).Format(time.RFC3339))
	metricsQuery.Set("step_seconds", "300")
	metricsQuery.Set("names", "server_count,used_load_units,responses_200")
	metrics := callJSON(t, api, http.MethodGet, base+"/metrics?"+metricsQuery.Encode(), nil, http.StatusOK)
	seen["getMetrics"] = true
	if _, ok := metrics["current"].(map[string]any); !ok {
		t.Fatalf("metrics = %#v", metrics)
	}

	resources := callJSON(t, api, http.MethodGet, base+"/resources", nil, http.StatusOK)
	seen["getResources"] = true
	if numberField(t, resources, "active_instances") != 1 {
		t.Fatalf("resources = %#v", resources)
	}

	scaleBody := map[string]any{
		"request_id": "scale-1", "desired_instances": 2,
	}
	scale := callJSON(t, api, http.MethodPut, base+"/resources/backend", scaleBody, http.StatusAccepted)
	seen["setBackendServerCount"] = true
	scaleOperationID := stringField(t, scale, "operation_id")
	repeatedScale := callJSON(t, api, http.MethodPut, base+"/resources/backend", scaleBody, http.StatusOK)
	if stringField(t, repeatedScale, "operation_id") != scaleOperationID {
		t.Fatalf("idempotent scale returned another operation: %#v", repeatedScale)
	}
	callJSON(t, api, http.MethodGet, base+"/operations/"+scaleOperationID, nil, http.StatusOK)
	seen["getOperation"] = true

	fix := callJSON(t, api, http.MethodPost, base+"/fixes", map[string]any{
		"request_id": "fix-1", "message": "FIX-HTTP-BUG",
	}, http.StatusOK)
	seen["applyFix"] = true
	if applied, _ := fix["applied"].(bool); !applied {
		t.Fatalf("fix = %#v", fix)
	}

	deployments := callJSON(t, api, http.MethodGet, base+"/deployments", nil, http.StatusOK)
	seen["getDeployments"] = true
	if items, ok := deployments["deployments"].([]any); !ok || len(items) != 1 {
		t.Fatalf("deployments = %#v", deployments)
	}
	deploymentBody := map[string]any{
		"request_id": "deployment-1-request", "deployment_id": "deployment-1",
	}
	deployment := callJSON(t, api, http.MethodPost, base+"/deployments", deploymentBody, http.StatusAccepted)
	seen["startDeployment"] = true
	deploymentOperationID := stringField(t, deployment, "operation_id")
	repeatedDeployment := callJSON(t, api, http.MethodPost, base+"/deployments", deploymentBody, http.StatusOK)
	if stringField(t, repeatedDeployment, "operation_id") != deploymentOperationID {
		t.Fatalf("idempotent deployment returned another operation: %#v", repeatedDeployment)
	}

	advanceBody := map[string]any{
		"request_id": "advance-1", "duration_seconds": 300,
	}
	advanced := callJSON(t, api, http.MethodPost, base+"/time/advance", advanceBody, http.StatusOK)
	seen["advanceTime"] = true
	if numberField(t, advanced, "requested_duration_seconds") != 300 || numberField(t, advanced, "processed_events") == 0 {
		t.Fatalf("advance = %#v", advanced)
	}
	repeatedAdvance := callJSON(t, api, http.MethodPost, base+"/time/advance", advanceBody, http.StatusOK)
	if numberField(t, repeatedAdvance, "processed_events") != 0 || numberField(t, repeatedAdvance, "new_logs") != 0 {
		t.Fatalf("idempotent advance produced new work: %#v", repeatedAdvance)
	}
	operation := callJSON(t, api, http.MethodGet, base+"/operations/"+deploymentOperationID, nil, http.StatusOK)
	if stringField(t, operation, "status") != "succeeded" {
		t.Fatalf("deployment operation = %#v", operation)
	}

	economy := callJSON(t, api, http.MethodGet, base+"/economy", nil, http.StatusOK)
	seen["getEconomy"] = true
	if stringField(t, economy, "currency") != "USD" {
		t.Fatalf("economy = %#v", economy)
	}

	wantOperations := []string{"startRun", "getOverview", "getMetrics", "getSiteLogs", "getResources",
		"setBackendServerCount", "applyFix", "getDeployments", "startDeployment", "getOperation",
		"probePage", "getEconomy", "advanceTime"}
	for _, operationID := range wantOperations {
		if !seen[operationID] {
			t.Errorf("OpenAPI operation %s was not exercised", operationID)
		}
	}
}

func TestHTTPValidationAndErrorsFollowOpenAPI(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()

	invalidSeed := callJSON(t, api, http.MethodPost, "/v1/start", map[string]any{
		"seed": 0, "agent_id": "http-agent", "agent_version": "v1", "request_id": "invalid-seed",
	}, http.StatusBadRequest)
	if stringField(t, invalidSeed, "error") != "INVALID_SEED" {
		t.Fatalf("invalid seed = %#v", invalidSeed)
	}

	start := callJSON(t, api, http.MethodPost, "/v1/start", map[string]any{
		"seed": -1, "agent_id": "http-agent", "agent_version": "v1", "request_id": "start-errors",
	}, http.StatusCreated)
	runID := stringField(t, start, "run_id")
	tooShort := callJSON(t, api, http.MethodPost, "/v1/runs/"+runID+"/time/advance", map[string]any{
		"request_id": "short", "duration_seconds": 60,
	}, http.StatusBadRequest)
	if stringField(t, tooShort, "error") != "MINIMUM_ADVANCE_IS_300_SECONDS" {
		t.Fatalf("minimum advance = %#v", tooShort)
	}

	unknown := callJSON(t, api, http.MethodGet, "/v1/runs/1234567890abcdefghijklmn/overview", nil, http.StatusNotFound)
	if stringField(t, unknown, "error") != "RUN_NOT_FOUND" {
		t.Fatalf("unknown run = %#v", unknown)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/start", bytes.NewBufferString(`{"seed":-1,"unknown":true}`))
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	missingRequired := callJSON(t, api, http.MethodPut, "/v1/runs/"+runID+"/resources/backend", map[string]any{
		"request_id": "missing-desired",
	}, http.StatusBadRequest)
	if stringField(t, missingRequired, "error") != "INVALID_REQUEST" {
		t.Fatalf("missing desired_instances = %#v", missingRequired)
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
	fixMessage := "FIX-HTTP-BUG"
	fixHash := sha256.Sum256([]byte(fixMessage))
	bootstrap := []events.Event{
		events.PageConfigured{Page: model.PageProductList, LoadUnits: 10, HoldDuration: time.Minute, BaseLatency: 20 * time.Millisecond, ConfiguredAt: startsAt},
		events.PageConfigured{Page: model.PageProduct, LoadUnits: 20, HoldDuration: time.Minute, BaseLatency: 30 * time.Millisecond, ConfiguredAt: startsAt},
		events.PageConfigured{Page: model.PagePurchase, LoadUnits: 30, HoldDuration: time.Minute, BaseLatency: 40 * time.Millisecond, ConfiguredAt: startsAt},
		events.InfrastructureConfigured{ServerProvisioningDuration: 5 * time.Minute, ConfiguredAt: startsAt},
		events.EconomyConfigured{Currency: "USD", InitialBalanceMinor: 1_000_000, StopRunOnNegativeBalance: true, ServerBillingPeriod: time.Hour, ConfiguredAt: startsAt},
		events.ServerProvisioningStarted{OperationID: "initial-server", ServerID: "server-1", CapacityUnits: 100, CostPerHourMinor: 100, StartedAt: startsAt, ReadyAt: startsAt},
		events.ServerActivated{OperationID: "initial-server", ServerID: "server-1", ActivatedAt: startsAt},
		events.ProductAdded{ProductID: "product-1", Name: "Product", PriceMinor: 1_000, ViewProbabilityPPM: 1_000_000, PurchaseProbabilityPPM: 1_000_000, AddedAt: startsAt},
		events.PageBugActivated{BugID: "bug-1", Page: model.PageProduct, ProductID: "product-1", FailureProbabilityPPM: 1_000_000,
			FixMessage: fixMessage, FixMessageHash: fmt.Sprintf("%x", fixHash), ActivatedAt: startsAt},
		events.DeploymentDefined{DeploymentID: "deployment-1", Sequence: 1, Name: "Cache", Description: "Enable cache", Duration: 5 * time.Minute, DefinedAt: startsAt},
		events.DeploymentUnlocked{DeploymentID: "deployment-1", UnlockedAt: startsAt},
	}
	if _, err := application.RegisterManualWorld(ctx, storage.Catalog, application.ManualWorldInput{
		Seed: -1, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour), Bootstrap: bootstrap,
	}); err != nil {
		storage.Close()
		t.Fatal(err)
	}
	manager := application.NewRunManager(storage.Catalog, storage.Journal, storage.Journal, 4)
	server := New(application.NewStartRunService(storage.Catalog, storage.Journal, nil, storage.Journal), application.NewRunService(manager))
	auditNumber := 0
	server.newAuditID = func() (string, error) {
		auditNumber++
		return fmt.Sprintf("audit-%d", auditNumber), nil
	}
	return server, func() { _ = storage.Close() }, startsAt
}

func callJSON(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
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
