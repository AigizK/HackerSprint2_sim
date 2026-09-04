package httpapi

import (
	"net/http"
	"testing"
)

func TestFirewallControlCommandsUseBasicAuthAndStrictSchemas(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	start := callJSON(t, api, http.MethodPost, "/v2/start", map[string]any{
		"seed": -1, "agent_id": "firewall-agent", "agent_version": "v1", "request_id": "firewall-start",
	}, http.StatusCreated)
	runID := stringField(t, start, "run_id")
	auth := start["control_panel_auth"].(map[string]any)
	username, password := stringField(t, auth, "username"), stringField(t, auth, "password")
	path := "/v2/runs/" + runID + "/control/commands"

	unauthorized := callJSON(t, api, http.MethodPost, path, map[string]any{
		"request_id": "list-unauthorized", "command": "firewall.rules.list", "params": map[string]any{},
	}, http.StatusUnauthorized)
	if stringField(t, unauthorized, "error") != "CONTROL_UNAUTHORIZED" {
		t.Fatalf("unauthorized response = %#v", unauthorized)
	}

	created := callJSONBasic(t, api, http.MethodPost, path, map[string]any{
		"request_id": "firewall-create", "command": "firewall.rules.upsert",
		"params": map[string]any{"rule_id": "deny-cameras", "priority": 10, "action": "deny", "enabled": true,
			"match": map[string]any{"user_agent": map[string]any{"operator": "contains", "value": "Camera"}}},
	}, http.StatusOK, username, password)
	createdResult := created["result"].(map[string]any)["rule"].(map[string]any)
	if createdResult["rule_id"] != "deny-cameras" || createdResult["revision"] != float64(1) {
		t.Fatalf("created rule = %#v", createdResult)
	}

	listed := callJSONBasic(t, api, http.MethodPost, path, map[string]any{
		"request_id": "firewall-list", "command": "firewall.rules.list", "params": map[string]any{},
	}, http.StatusOK, username, password)
	rules := listed["result"].(map[string]any)["rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["rule_id"] != "deny-cameras" {
		t.Fatalf("listed rules = %#v", rules)
	}

	invalid := callJSONBasic(t, api, http.MethodPost, path, map[string]any{
		"request_id": "firewall-invalid", "command": "firewall.rules.upsert",
		"params": map[string]any{"rule_id": "bad", "priority": 1, "action": "deny", "enabled": true,
			"match": map[string]any{"region_code": "RU", "unknown": true}},
	}, http.StatusBadRequest, username, password)
	if stringField(t, invalid, "error") != "INVALID_REQUEST" {
		t.Fatalf("strict schema response = %#v", invalid)
	}

	deleted := callJSONBasic(t, api, http.MethodPost, path, map[string]any{
		"request_id": "firewall-delete", "command": "firewall.rules.delete", "params": map[string]any{"rule_id": "deny-cameras"},
	}, http.StatusOK, username, password)
	if deleted["result"].(map[string]any)["deleted"] != true {
		t.Fatalf("delete response = %#v", deleted)
	}
	missing := callJSONBasic(t, api, http.MethodPost, path, map[string]any{
		"request_id": "firewall-delete-missing", "command": "firewall.rules.delete", "params": map[string]any{"rule_id": "missing"},
	}, http.StatusNotFound, username, password)
	if stringField(t, missing, "error") != "RESOURCE_NOT_FOUND" {
		t.Fatalf("missing response = %#v", missing)
	}
}
