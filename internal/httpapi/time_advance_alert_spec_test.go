package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestAdvanceTimeStopsAtFirstNewLogError(t *testing.T) {
	api, closeStorage, startsAt := newTimeAdvanceAlertSpecServer(t)
	defer closeStorage()
	runID := startTimeAdvanceSpecRun(t, api, -71, "start-alert-stop")
	base := "/v2/runs/" + runID

	// An error that predates the wait must not trigger the new-log condition.
	callJSON(t, api, http.MethodPost, base+"/probes", map[string]any{
		"request_id": "existing-error", "page": "product_list",
	}, http.StatusOK)

	result := callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id":       "wait-for-first-new-error",
		"duration_seconds": 30 * 60,
		"stop_when":        map[string]any{"new_log_errors": 1},
	}, http.StatusOK)

	if stringField(t, result, "stop_reason") != "log_error" {
		t.Fatalf("advance result = %#v", result)
	}
	assertSimulationTime(t, result, startsAt.Add(10*time.Minute))
	if numberField(t, result, "requested_duration_seconds") != 30*60 {
		t.Fatalf("requested_duration_seconds = %#v, want the original requested interval", result["requested_duration_seconds"])
	}
	if numberField(t, result, "new_logs") != 1 {
		t.Fatalf("new_logs = %#v, want the triggering log only", result["new_logs"])
	}
}

func TestAdvanceTimeWithNoNewLogErrorReachesDurationLimit(t *testing.T) {
	api, closeStorage, _ := newTimeAdvanceAlertSpecServer(t)
	defer closeStorage()
	runID := startTimeAdvanceSpecRun(t, api, -72, "start-duration-stop")
	base := "/v2/runs/" + runID

	result := callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id":       "wait-without-error",
		"duration_seconds": 30 * 60,
		"stop_when":        map[string]any{"new_log_errors": 1},
	}, http.StatusOK)

	if stringField(t, result, "stop_reason") != "duration_elapsed" {
		t.Fatalf("advance result = %#v", result)
	}
	previous := parseSpecTime(t, stringField(t, result, "previous_simulation_time"))
	current := parseSpecTime(t, stringField(t, result["clock"].(map[string]any), "simulation_time"))
	if current.Sub(previous) != 30*time.Minute {
		t.Fatalf("advanced by %s, want 30m", current.Sub(previous))
	}
}

func TestAdvanceTimeFinishesEveryEventAtTriggerTimestamp(t *testing.T) {
	api, closeStorage, startsAt := newTimeAdvanceAlertSpecServer(t)
	defer closeStorage()
	runID := startTimeAdvanceSpecRun(t, api, -73, "start-atomic-alert-stop")
	base := "/v2/runs/" + runID

	result := callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id":       "wait-for-atomic-errors",
		"duration_seconds": 30 * 60,
		"stop_when":        map[string]any{"new_log_errors": 1},
	}, http.StatusOK)

	if stringField(t, result, "stop_reason") != "log_error" {
		t.Fatalf("advance result = %#v", result)
	}
	assertSimulationTime(t, result, startsAt.Add(10*time.Minute))
	if numberField(t, result, "new_logs") != 2 {
		t.Fatalf("new_logs = %#v, want both errors at the trigger timestamp", result["new_logs"])
	}
}

func TestAdvanceTimeByDurationRemainsAvailableWithoutStopCondition(t *testing.T) {
	api, closeStorage, _ := newTimeAdvanceAlertSpecServer(t)
	defer closeStorage()
	runID := startTimeAdvanceSpecRun(t, api, -72, "start-duration-only")
	base := "/v2/runs/" + runID

	result := callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id": "advance-five-minutes", "duration_seconds": 5 * 60,
	}, http.StatusOK)

	if stringField(t, result, "stop_reason") != "duration_elapsed" {
		t.Fatalf("advance result = %#v", result)
	}
	previous := parseSpecTime(t, stringField(t, result, "previous_simulation_time"))
	current := parseSpecTime(t, stringField(t, result["clock"].(map[string]any), "simulation_time"))
	if current.Sub(previous) != 5*time.Minute {
		t.Fatalf("advanced by %s, want 5m", current.Sub(previous))
	}
}

func TestAdvanceTimeReportsRunCompletionBeforeDurationLimit(t *testing.T) {
	api, closeStorage, startsAt := newTimeAdvanceAlertSpecServer(t)
	defer closeStorage()
	runID := startTimeAdvanceSpecRun(t, api, -72, "start-run-completion")
	base := "/v2/runs/" + runID

	result := callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id":       "wait-past-world-end",
		"duration_seconds": 2 * 60 * 60,
		"stop_when":        map[string]any{"new_log_errors": 1},
	}, http.StatusOK)

	if stringField(t, result, "stop_reason") != "run_completed" {
		t.Fatalf("advance result = %#v", result)
	}
	assertSimulationTime(t, result, startsAt.Add(time.Hour))
}

func TestAdvanceTimeRejectsUnsupportedLogErrorThreshold(t *testing.T) {
	api, closeStorage, _ := newTimeAdvanceAlertSpecServer(t)
	defer closeStorage()
	runID := startTimeAdvanceSpecRun(t, api, -72, "start-invalid-alert-threshold")

	result := callJSON(t, api, http.MethodPost, "/v2/runs/"+runID+"/time/advance", map[string]any{
		"request_id":       "invalid-alert-threshold",
		"duration_seconds": 30 * 60,
		"stop_when":        map[string]any{"new_log_errors": 2},
	}, http.StatusBadRequest)
	if stringField(t, result, "error") != "INVALID_REQUEST" {
		t.Fatalf("invalid threshold response = %#v", result)
	}
}

func TestAdvanceTimeStopConditionIsPartOfIdempotentPayload(t *testing.T) {
	api, closeStorage, _ := newTimeAdvanceAlertSpecServer(t)
	defer closeStorage()
	runID := startTimeAdvanceSpecRun(t, api, -72, "start-alert-idempotency")
	base := "/v2/runs/" + runID

	callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id":       "same-advance-id",
		"duration_seconds": 5 * 60,
		"stop_when":        map[string]any{"new_log_errors": 1},
	}, http.StatusOK)
	conflict := callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id": "same-advance-id", "duration_seconds": 5 * 60,
	}, http.StatusConflict)
	if stringField(t, conflict, "error") != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict = %#v", conflict)
	}
}

func newTimeAdvanceAlertSpecServer(t *testing.T) (*Server, func(), time.Time) {
	t.Helper()
	ctx := context.Background()
	storage, err := persistence.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	startsAt := time.Date(2032, 2, 2, 0, 0, 0, 0, time.UTC)
	bootstrap := []events.Event{
		events.PageConfigured{Page: model.PageProductList, LoadUnits: 10, HoldDuration: time.Minute, BaseLatency: 20 * time.Millisecond, ConfiguredAt: startsAt},
		events.ProductAdded{ProductID: "product-1", Name: "Product", PriceMinor: 1_000, ViewProbabilityPPM: 0, AddedAt: startsAt},
	}
	errorAt := startsAt.Add(10 * time.Minute)
	if _, err := application.RegisterManualWorld(ctx, storage.Catalog, application.ManualWorldInput{
		Seed: -71, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour), Bootstrap: bootstrap,
		Events: events.EventSchedule{{Sequence: 1, OccursAt: errorAt, Event: events.VisitorArrived{VisitorID: "visitor-error", ArrivedAt: errorAt}}},
	}); err != nil {
		_ = storage.Close()
		t.Fatal(err)
	}
	if _, err := application.RegisterManualWorld(ctx, storage.Catalog, application.ManualWorldInput{
		Seed: -72, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour),
	}); err != nil {
		_ = storage.Close()
		t.Fatal(err)
	}
	if _, err := application.RegisterManualWorld(ctx, storage.Catalog, application.ManualWorldInput{
		Seed: -73, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour), Bootstrap: bootstrap,
		Events: events.EventSchedule{
			{Sequence: 1, OccursAt: errorAt, Event: events.VisitorArrived{VisitorID: "visitor-error-1", ArrivedAt: errorAt}},
			{Sequence: 2, OccursAt: errorAt, Event: events.VisitorArrived{VisitorID: "visitor-error-2", ArrivedAt: errorAt}},
		},
	}); err != nil {
		_ = storage.Close()
		t.Fatal(err)
	}
	manager := application.NewRunManager(storage.Catalog, storage.Journal, storage.Journal, 2)
	api := New(application.NewStartRunService(storage.Catalog, storage.Journal, nil, storage.Journal), application.NewRunService(manager, storage.Catalog))
	return api, func() { _ = storage.Close() }, startsAt
}

func startTimeAdvanceSpecRun(t *testing.T, api *Server, seed int64, requestID string) string {
	t.Helper()
	started := callJSON(t, api, http.MethodPost, "/v2/start", map[string]any{
		"seed": seed, "agent_id": "time-advance-spec", "agent_version": "v1", "request_id": requestID,
	}, http.StatusCreated)
	return stringField(t, started, "run_id")
}

func assertSimulationTime(t *testing.T, response map[string]any, want time.Time) {
	t.Helper()
	clock, ok := response["clock"].(map[string]any)
	if !ok {
		t.Fatalf("clock = %#v", response["clock"])
	}
	if got := parseSpecTime(t, stringField(t, clock, "simulation_time")); !got.Equal(want) {
		t.Fatalf("simulation_time = %s, want %s", got, want)
	}
}

func parseSpecTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}
