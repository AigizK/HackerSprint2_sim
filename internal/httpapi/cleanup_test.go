package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRemovedRoutesAreNotRegistered(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	start := callJSON(t, api, http.MethodPost, "/v2/start", map[string]any{
		"seed": -1, "agent_id": "http-agent", "agent_version": "v1", "request_id": "start-cleanup",
	}, http.StatusCreated)
	base := "/v2/runs/" + stringField(t, start, "run_id")
	for _, route := range []struct{ method, path string }{
		{"POST", "/products/product-1"}, {"DELETE", "/products/product-1"},
		{"PUT", "/resources/backend"}, {"POST", "/fixes"},
		{"GET", "/deployments"}, {"POST", "/deployments"}, {"GET", "/economy"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			api.ServeHTTP(recorder, httptest.NewRequest(route.method, base+route.path, bytes.NewBufferString(`{}`)))
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("removed route: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestReadOnlyServiceHTTPFlow(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	input := map[string]any{"seed": -1, "agent_id": "http-agent", "agent_version": "v1", "request_id": "start-service"}
	start := callJSON(t, api, "POST", "/v2/start", input, 201)
	duplicate := callJSON(t, api, "POST", "/v2/start", input, 200)
	if start["run_id"] != duplicate["run_id"] {
		t.Fatal("start is not idempotent")
	}
	base := "/v2/runs/" + stringField(t, start, "run_id")
	for _, page := range []string{"product_list", "product_page"} {
		probe := map[string]any{"request_id": "probe-" + page, "page": page}
		if page == "product_page" {
			probe["product_id"] = "product-1"
		}
		result := callJSON(t, api, "POST", base+"/probes", probe, 200)
		if numberField(t, result, "status") != 200 {
			t.Fatalf("probe: %#v", result)
		}
		callJSON(t, api, "POST", base+"/probes", probe, 200)
	}
	callJSON(t, api, "POST", base+"/probes", map[string]any{
		"request_id": "purchase-probe", "page": "purchase", "product_id": "product-1",
	}, 400)
	for _, path := range []string{"/metrics?page=purchase", "/logs?page=purchase", "/metrics?names=" + url.QueryEscape("revenue_minor")} {
		callJSON(t, api, "GET", base+path, nil, 400)
	}
	for _, path := range []string{"/overview", "/metrics", "/logs", "/resources", "/inbox"} {
		result := callJSON(t, api, "GET", base+path, nil, 200)
		assertNoSalesFields(t, result)
	}
	callJSON(t, api, "GET", base+"/operations/1234567890abcdefghijklmn", nil, 404)
	advance := map[string]any{"request_id": "advance-cleanup", "duration_seconds": 300}
	callJSON(t, api, "POST", base+"/time/advance", advance, 200)
	callJSON(t, api, "POST", base+"/time/advance", advance, 200)
	overview := callJSON(t, api, "GET", base+"/overview", nil, 200)
	costs := overview["costs"].(map[string]any)
	if numberField(t, costs, "total_cost_minor") <= 0 || costs["currency"] != "USD" {
		t.Fatalf("costs: %#v", costs)
	}
}

func assertNoSalesFields(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch key {
			case "balance_minor", "revenue_minor", "lost_revenue_minor", "successful_purchases", "lost_purchases", "profit_minor", "deployment_cost_minor", "current_deployment_id", "desired_instances":
				t.Errorf("obsolete response field %s", key)
			}
			assertNoSalesFields(t, child)
		}
	case []any:
		for _, child := range value {
			assertNoSalesFields(t, child)
		}
	}
}
