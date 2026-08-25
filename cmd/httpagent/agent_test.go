package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPAgentUsesPublicAPIForDailyActions(t *testing.T) {
	t.Parallel()
	startsAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	endsAt := startsAt.Add(48 * time.Hour)
	var mu sync.Mutex
	overviewCalls, logCalls := 0, 0
	called := make(map[string]int)

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := request.Method + " " + request.URL.Path
		called[key]++
		writer.Header().Set("Content-Type", "application/json")
		switch key {
		case "POST /v1/start":
			writeTestJSON(t, writer, http.StatusCreated, map[string]any{"run_id": "abcdefghijklmnop", "status": "running", "simulation_time": startsAt, "simulation_ends_at": endsAt})
		case "GET /v1/runs/abcdefghijklmnop/overview":
			overviewCalls++
			status, now := "running", startsAt
			if overviewCalls > 1 {
				status, now = "completed", endsAt
			}
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"status": status, "clock": testClock(now, endsAt)})
		case "GET /v1/runs/abcdefghijklmnop/logs":
			logCalls++
			entries := []map[string]any{}
			if logCalls == 1 {
				entries = []map[string]any{
					{"timestamp": startsAt, "status": 500, "error": pageBugError, "message": fixMessagePrefix + "fix-key"},
					{"timestamp": startsAt, "status": 500, "error": capacityError},
				}
			}
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"logs": entries, "next_cursor": nil})
		case "POST /v1/runs/abcdefghijklmnop/fixes":
			var body map[string]any
			decodeTestJSON(t, request, &body)
			if body["message"] != "fix-key" {
				t.Errorf("fix message = %v", body["message"])
			}
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"applied": true})
		case "GET /v1/runs/abcdefghijklmnop/deployments":
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"deployments": []map[string]any{{"deployment_id": "deploy-1", "sequence": 1, "status": deploymentAvailable}}})
		case "POST /v1/runs/abcdefghijklmnop/deployments":
			writeTestJSON(t, writer, http.StatusAccepted, map[string]any{"operation_id": "abcdefghijklmnop", "status": "pending"})
		case "GET /v1/runs/abcdefghijklmnop/resources":
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"desired_instances": 1, "active_instances": 1, "used_load_units": 0})
		case "GET /v1/runs/abcdefghijklmnop/metrics":
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"current": map[string]any{"capacity_utilization": 0}})
		case "PUT /v1/runs/abcdefghijklmnop/resources/backend":
			var body map[string]any
			decodeTestJSON(t, request, &body)
			if body["desired_instances"] != float64(2) {
				t.Errorf("desired_instances = %v", body["desired_instances"])
			}
			writeTestJSON(t, writer, http.StatusAccepted, map[string]any{"operation_id": "abcdefghijklmnop", "status": "pending"})
		case "POST /v1/runs/abcdefghijklmnop/time/advance":
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"clock": testClock(startsAt.Add(24*time.Hour), endsAt), "processed_events": 10, "new_logs": 2})
		case "GET /v1/runs/abcdefghijklmnop/economy":
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"successful_purchases": 3, "revenue_minor": 3000, "server_cost_minor": 100, "deployment_cost_minor": 50, "balance_minor": 2850})
		default:
			http.Error(writer, fmt.Sprintf("unexpected request %s", key), http.StatusNotFound)
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := newAPIClient(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := newHTTPAgent(client, agentConfig{seed: 1, agentID: "test-agent", agentVersion: "v1", advance: 24 * time.Hour, maxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.run(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"POST /v1/start", "POST /v1/runs/abcdefghijklmnop/fixes", "POST /v1/runs/abcdefghijklmnop/deployments",
		"PUT /v1/runs/abcdefghijklmnop/resources/backend", "POST /v1/runs/abcdefghijklmnop/time/advance",
	} {
		if called[key] != 1 {
			t.Errorf("%s calls = %d, want 1", key, called[key])
		}
	}
}

func testClock(now, end time.Time) map[string]any {
	return map[string]any{"simulation_time": now, "simulation_ends_at": end}
}

func writeTestJSON(t *testing.T, writer http.ResponseWriter, status int, value any) {
	t.Helper()
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func decodeTestJSON(t *testing.T, request *http.Request, target any) {
	t.Helper()
	if err := json.NewDecoder(request.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}
