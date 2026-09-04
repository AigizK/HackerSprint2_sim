package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

// This is the executable public-API inventory. Adding an OpenAPI operation
// requires adding a successful (or deliberately not-found) call here.
func TestEveryV2EndpointHasAnExecutableContract(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	runID, access := startControlTestRun(t, api, "endpoint-inventory-start")
	base := "/v2/runs/" + runID

	for _, path := range []string{"/overview", "/metrics", "/logs", "/inbox"} {
		callJSON(t, api, http.MethodGet, base+path, nil, http.StatusOK)
	}
	resources := callJSON(t, api, http.MethodGet, base+"/resources", nil, http.StatusOK)
	if numberField(t, resources, "active_instances") != 1 {
		t.Fatalf("active_instances must count only backend servers: %#v", resources)
	}
	servers := resources["servers"].([]any)
	for _, raw := range servers {
		server := raw.(map[string]any)
		for _, field := range []string{"server_id", "name", "role", "instance_type", "status", "capacity_units", "used_load_units", "cost_per_hour_minor", "disk", "database_ids", "credential_id"} {
			if _, exists := server[field]; !exists {
				t.Fatalf("resource server misses %s: %#v", field, server)
			}
		}
	}
	callJSON(t, api, http.MethodPost, base+"/probes", map[string]any{
		"request_id": "endpoint-inventory-probe", "page": "product_list",
	}, http.StatusOK)
	callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id": "endpoint-inventory-advance", "duration_seconds": 300,
	}, http.StatusOK)
	callJSON(t, api, http.MethodGet, base+"/operations/1234567890abcdefghijklmn", nil, http.StatusNotFound)
	callJSONBasic(t, api, http.MethodGet, base+"/control/commands", nil, http.StatusOK, access.username, access.password)
	controlCommand(t, api, runID, access, http.StatusOK, "endpoint-inventory-command", "server.inspect", map[string]any{"server_id": "db-server-1"}, nil)
	credentialID, _ := serverCredentialFor(t, api, runID, access, "db-server-1", "endpoint-inventory-credential-id")
	callJSONBasic(t, api, http.MethodGet, base+"/credentials/"+credentialID, nil, http.StatusOK, access.username, access.password)
}

func TestOverviewAndMetricsExposeTimeBasedSLOAndAllCosts(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	runID, _ := startControlTestRun(t, api, "slo-cost-contract-start")
	base := "/v2/runs/" + runID

	overview := callJSON(t, api, http.MethodGet, base+"/overview", nil, http.StatusOK)
	availability, ok := overview["availability"].(map[string]any)
	if !ok {
		t.Fatalf("overview has no availability summary: %#v", overview)
	}
	if numberField(t, availability, "uptime_target") != 0.99 {
		t.Fatalf("uptime_target = %#v", availability["uptime_target"])
	}
	observed := numberField(t, availability, "observed_seconds")
	available := numberField(t, availability, "available_seconds")
	downtime := numberField(t, availability, "downtime_seconds")
	if observed != available+downtime || availability["slo_passed"] != nil {
		t.Fatalf("invalid running availability summary: %#v", availability)
	}
	for _, field := range []string{"currency", "server_cost_minor", "backup_storage_cost_minor", "total_cost_minor", "current_cost_per_hour_minor"} {
		if _, exists := overview["costs"].(map[string]any)[field]; !exists {
			t.Fatalf("overview costs miss %s", field)
		}
	}

	metrics := callJSON(t, api, http.MethodGet, base+"/metrics", nil, http.StatusOK)
	current := metrics["current"].(map[string]any)
	for _, field := range []string{"server_cost_minor", "backup_storage_cost_minor", "total_cost_minor", "current_cost_per_hour_minor",
		"observed_seconds", "available_seconds", "downtime_seconds", "uptime_ratio"} {
		if _, exists := current[field]; !exists {
			t.Fatalf("metrics.current misses %s: %#v", field, current)
		}
	}
	series := callJSON(t, api, http.MethodGet, base+"/metrics?from="+overview["clock"].(map[string]any)["simulation_time"].(string)+
		"&to="+overview["clock"].(map[string]any)["simulation_time"].(string)+"&names=uptime_ratio,total_cost_minor", nil, http.StatusOK)
	if _, ok := series["series"].([]any); !ok {
		t.Fatalf("SLO/cost series response = %#v", series)
	}
}

func TestControlCatalogContainsExactlyEveryImplementedCommand(t *testing.T) {
	want := map[string]bool{
		"firewall.rules.list": true, "firewall.rules.upsert": true, "firewall.rules.delete": true,
		"server.types.list": true, "server.create": true, "server.inspect": true, "server.delete": true,
		"database.create": true, "database.inspect": true, "database.backup": true,
		"database.backups.list": true, "database.restore": true,
		"site.config.get": true, "site.stop": true, "site.start": true, "site.database.set": true,
		"disk.usage": true, "disk.cleanup": true,
	}
	got := make(map[string]bool)
	for _, definition := range controlCommandCatalog() {
		if got[definition.Command] {
			t.Fatalf("duplicate command %q", definition.Command)
		}
		got[definition.Command] = true
	}
	if len(got) != len(want) {
		t.Fatalf("command catalog size=%d want=%d; got=%v", len(got), len(want), got)
	}
	for command := range want {
		if !got[command] {
			t.Errorf("implemented command %q is missing from catalog", command)
		}
	}
}

func TestControlCatalogResultSchemasDescribeEveryReturnedObject(t *testing.T) {
	for _, definition := range controlCommandCatalog() {
		t.Run(definition.Command, func(t *testing.T) {
			if err := validateConcreteCatalogSchema(definition.ResultSchema, "result"); err != nil {
				t.Fatal(err)
			}
		})
	}

	var upsert commandDefinitionResponse
	for _, definition := range controlCommandCatalog() {
		if definition.Command == "firewall.rules.upsert" {
			upsert = definition
		}
	}
	match := upsert.ParamsSchema["properties"].(map[string]any)["match"].(map[string]any)
	if match["minProperties"] != 1 {
		t.Fatal("firewall match catalog schema must require at least one condition")
	}
	userAgent := match["properties"].(map[string]any)["user_agent"].(map[string]any)
	value := userAgent["properties"].(map[string]any)["value"].(map[string]any)
	if value["maxLength"] != 2048 {
		t.Fatal("firewall user-agent catalog schema must expose the actual length limit")
	}
}

func validateConcreteCatalogSchema(schema map[string]any, path string) error {
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		if schema["additionalProperties"] != false {
			return fmt.Errorf("%s must reject unknown properties", path)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok || len(properties) == 0 {
			return fmt.Errorf("%s is an object without described properties", path)
		}
		for name, raw := range properties {
			child, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s is not a schema", path, name)
			}
			if err := validateConcreteCatalogSchema(child, path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s array has no item schema", path)
		}
		return validateConcreteCatalogSchema(items, path+"[]")
	case "string", "integer", "number", "boolean":
		return nil
	default:
		if _, nullable := schema["type"].([]any); nullable {
			return nil
		}
		return fmt.Errorf("%s has unsupported or missing type %#v", path, schema["type"])
	}
	return nil
}
