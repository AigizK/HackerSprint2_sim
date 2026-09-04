package spec_test

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestControlOpenAPIReplacesProductAndScalingOperations(t *testing.T) {
	document := loadOpenAPI(t)
	paths := objectField(t, document, "paths")
	for _, removed := range []string{
		"/v2/runs/{run_id}/products/{product_id}",
		"/v2/runs/{run_id}/resources/backend",
		"/v2/runs/{run_id}/economy",
		"/v2/runs/{run_id}/fixes",
		"/v2/runs/{run_id}/deployments",
	} {
		if _, exists := paths[removed]; exists {
			t.Fatalf("removed endpoint remains: %s", removed)
		}
	}

	for _, operation := range []struct{ path, method, id string }{
		{"/v2/runs/{run_id}/control/commands", "get", "listControlCommands"},
		{"/v2/runs/{run_id}/control/commands", "post", "executeControlCommand"},
		{"/v2/runs/{run_id}/credentials/{credential_id}", "get", "getServerCredentials"},
	} {
		value := objectField(t, objectField(t, paths, operation.path), operation.method)
		if stringFieldFromMap(t, value, "operationId") != operation.id {
			t.Fatalf("unexpected operationId for %s %s", operation.method, operation.path)
		}
		security := arrayField(t, value, "security")
		if len(security) != 1 {
			t.Fatalf("%s security = %#v", operation.id, security)
		}
		entry, ok := security[0].(map[string]any)
		if !ok {
			t.Fatalf("%s security entry is not an object", operation.id)
		}
		if _, exists := entry["ControlPanelBasicAuth"]; !exists {
			t.Fatalf("%s does not require panel access", operation.id)
		}
	}

	inbox := objectField(t, objectField(t, paths, "/v2/runs/{run_id}/inbox"), "get")
	if len(arrayField(t, inbox, "security")) != 0 {
		t.Fatal("bootstrap inbox must remain accessible without credentials")
	}
	components := objectField(t, document, "components")
	security := objectField(t, components, "securitySchemes")
	if _, exists := security["StoreBasicAuth"]; exists {
		t.Fatal("obsolete store authentication remains")
	}
	schemas := objectField(t, components, "schemas")
	for _, removed := range []string{
		"ScaleBackendRequest", "UpsertProductRequest", "DeleteProductRequest",
		"ProductMutationResponse", "DeleteProductResponse", "EconomyResponse",
		"ApplyFixRequest", "ApplyFixResponse", "Deployment", "DeploymentStatus",
		"DeploymentsResponse", "StartDeploymentRequest", "OperationAcceptedResponse",
	} {
		if _, exists := schemas[removed]; exists {
			t.Fatalf("obsolete request/response schema remains: %s", removed)
		}
	}
}

func TestControlOpenAPIUsesTimeBasedUptimeAndCostsWithoutSales(t *testing.T) {
	document := loadOpenAPI(t)
	schemas := objectField(t, objectField(t, document, "components"), "schemas")
	if !reflect.DeepEqual(arrayField(t, objectField(t, schemas, "PageType"), "enum"), []any{"product_list", "product_page"}) {
		t.Fatal("only the two read-only page types must remain")
	}
	availability := objectField(t, schemas, "AvailabilitySummary")
	assertRequiredFields(t, availability, []string{
		"uptime_target", "observed_seconds", "available_seconds", "downtime_seconds", "uptime_ratio", "slo_passed",
	})
	properties := objectField(t, availability, "properties")
	if objectField(t, properties, "uptime_target")["const"] != 0.99 {
		t.Fatal("uptime SLO must be exactly 99%")
	}
	assertRequiredFields(t, objectField(t, schemas, "CostSummary"), []string{
		"currency", "server_cost_minor", "backup_storage_cost_minor", "total_cost_minor", "current_cost_per_hour_minor",
	})
	assertRequiredFields(t, objectField(t, schemas, "OverviewResponse"), []string{
		"clock", "run_id", "status", "site_status", "server_count", "capacity_utilization", "error_rate", "availability", "costs",
	})
	metricNames := arrayField(t, objectField(t, schemas, "MetricName"), "enum")
	snapshot := objectField(t, objectField(t, schemas, "MetricSnapshot"), "properties")
	for _, required := range []string{
		"server_cost_minor", "backup_storage_cost_minor", "total_cost_minor", "current_cost_per_hour_minor",
		"observed_seconds", "available_seconds", "downtime_seconds", "uptime_ratio",
	} {
		found := false
		for _, name := range metricNames {
			found = found || name == required
		}
		if !found {
			t.Fatalf("metric %s is missing from MetricName", required)
		}
		if _, exists := snapshot[required]; !exists {
			t.Fatalf("metric %s is missing from MetricSnapshot", required)
		}
	}
	removedFields := map[string]bool{
		"purchase": true, "successful_purchases": true, "lost_purchases": true,
		"revenue_minor": true, "lost_revenue_minor": true, "balance_minor": true,
		"profit_minor": true, "deployment_cost_minor": true, "current_deployment_id": true,
	}
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for field, child := range node {
				if removedFields[field] {
					t.Fatalf("obsolete sales/deployment field remains: %s", field)
				}
				visit(child)
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		case string:
			if removedFields[node] {
				t.Fatalf("obsolete sales/deployment enum value remains: %s", node)
			}
		}
	}
	visit(schemas)
	operation := objectField(t, objectField(t, schemas, "OperationResponse"), "properties")
	if objectField(t, operation, "type")["const"] != "control_command" {
		t.Fatal("operations must describe only infrastructure commands")
	}
}

func TestControlOpenAPIDescribesAllCommandParametersAndResults(t *testing.T) {
	document := loadOpenAPI(t)
	schemas := objectField(t, objectField(t, document, "components"), "schemas")
	cases := []struct {
		command    string
		params     string
		required   []string
		optional   []string
		targetAuth bool
	}{
		{"firewall.rules.list", "EmptyParams", nil, nil, false},
		{"firewall.rules.upsert", "FirewallRule", []string{"rule_id", "priority", "action", "match", "enabled"}, []string{"expires_at"}, false},
		{"firewall.rules.delete", "FirewallRuleIdParams", []string{"rule_id"}, nil, false},
		{"server.types.list", "ServerTypesListParams", nil, []string{"role"}, false},
		{"server.create", "ServerCreateParams", []string{"name", "role", "instance_type"}, nil, false},
		{"server.inspect", "ServerIdParams", []string{"server_id"}, nil, false},
		{"server.delete", "ServerIdParams", []string{"server_id"}, nil, false},
		{"database.create", "DatabaseCreateParams", []string{"server_id", "name"}, nil, true},
		{"database.inspect", "DatabaseIdParams", []string{"database_id"}, nil, true},
		{"database.backup", "DatabaseIdParams", []string{"database_id"}, nil, true},
		{"database.backups.list", "DatabaseBackupsListParams", nil, []string{"database_id"}, false},
		{"database.restore", "DatabaseRestoreParams", []string{"database_id", "backup_id"}, nil, true},
		{"site.config.get", "EmptyParams", nil, nil, false},
		{"site.stop", "EmptyParams", nil, nil, false},
		{"site.start", "EmptyParams", nil, nil, false},
		{"site.database.set", "SiteDatabaseSetParams", []string{"database_id", "expected_current_database_id"}, nil, false},
		{"disk.usage", "ServerIdParams", []string{"server_id"}, nil, true},
		{"disk.cleanup", "ServerIdParams", []string{"server_id"}, nil, true},
	}
	if len(arrayField(t, objectField(t, schemas, "ControlCommandName"), "enum")) != len(cases) {
		t.Fatal("command enum must contain exactly 18 commands")
	}
	requestUnion := objectField(t, schemas, "ControlCommandRequest")
	responseUnion := objectField(t, schemas, "ControlCommandResponse")
	for _, union := range []map[string]any{requestUnion, responseUnion} {
		if len(arrayField(t, union, "oneOf")) != len(cases) {
			t.Fatal("each command must have a typed oneOf variant")
		}
		discriminator := objectField(t, union, "discriminator")
		if stringFieldFromMap(t, discriminator, "propertyName") != "command" {
			t.Fatal("command must be the discriminator")
		}
		if len(objectField(t, discriminator, "mapping")) != len(cases) {
			t.Fatal("each command must have a discriminator mapping")
		}
	}
	requestMapping := objectField(t, objectField(t, requestUnion, "discriminator"), "mapping")
	responseMapping := objectField(t, objectField(t, responseUnion, "discriminator"), "mapping")
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			requestRef := stringFieldFromMap(t, requestMapping, tc.command)
			request := resolveOpenAPIReference(t, document, requestRef)
			if request["additionalProperties"] != false {
				t.Fatal("command envelope must reject unknown fields")
			}
			required := []string{"request_id", "command", "params"}
			if tc.targetAuth {
				required = append(required, "target_auth")
			}
			assertRequiredFields(t, request, required)
			properties := objectField(t, request, "properties")
			if objectField(t, properties, "command")["const"] != tc.command {
				t.Fatal("variant must constrain command with const")
			}
			if _, exists := properties["target_auth"]; exists != tc.targetAuth {
				t.Fatal("target_auth presence differs from command authorization")
			}
			if request["x-target-auth-required"] != tc.targetAuth {
				t.Fatal("catalog authorization metadata differs from schema")
			}
			if stringFieldFromMap(t, objectField(t, properties, "params"), "$ref") != "#/components/schemas/"+tc.params {
				t.Fatal("unexpected parameter schema")
			}
			params := objectField(t, schemas, tc.params)
			if params["additionalProperties"] != false {
				t.Fatal("params must reject unknown fields")
			}
			assertRequiredFields(t, params, tc.required)
			paramProperties := map[string]any{}
			if _, exists := params["properties"]; exists {
				paramProperties = objectField(t, params, "properties")
			}
			wantFields := append(append([]string{}, tc.required...), tc.optional...)
			if len(paramProperties) != len(wantFields) {
				t.Fatalf("parameter count = %d, want %d", len(paramProperties), len(wantFields))
			}
			for _, field := range wantFields {
				if _, exists := paramProperties[field]; !exists {
					t.Fatalf("missing parameter %s", field)
				}
			}
			examples := arrayField(t, request, "examples")
			if len(examples) == 0 {
				t.Fatal("each command must have a runnable example")
			}
			response := resolveOpenAPIReference(t, document, stringFieldFromMap(t, responseMapping, tc.command))
			assertRequiredFields(t, response, []string{"clock", "request_id", "command", "result"})
			resultProperties := objectField(t, response, "properties")
			if objectField(t, resultProperties, "command")["const"] != tc.command {
				t.Fatal("response discriminator does not match request")
			}
			result := resolveOpenAPIReference(t, document, stringFieldFromMap(t, objectField(t, resultProperties, "result"), "$ref"))
			if result["additionalProperties"] != false {
				t.Fatal("command result must have a concrete closed schema")
			}
		})
	}
}

func TestControlOpenAPICoversInfrastructureAndTrafficFields(t *testing.T) {
	document := loadOpenAPI(t)
	schemas := objectField(t, objectField(t, document, "components"), "schemas")
	assertRequiredFields(t, objectField(t, schemas, "InboxMessage"), []string{"description", "message_id", "sender_email", "sent_at", "subject"})
	assertRequiredFields(t, objectField(t, schemas, "TargetAuth"), []string{"username", "password"})
	assertRequiredFields(t, objectField(t, schemas, "CredentialRecord"), []string{"credential_id", "resource_id", "version", "username", "password", "valid_from", "expires_at"})
	assertRequiredFields(t, objectField(t, schemas, "SiteConfiguration"), []string{"state", "database_id"})
	assertRequiredFields(t, objectField(t, schemas, "DiskUsage"), []string{"server_id", "total_bytes", "system_bytes", "database_bytes", "logs_bytes", "used_bytes", "free_bytes", "cleanable_bytes"})
	assertRequiredFields(t, objectField(t, schemas, "DiskCleanupResult"), []string{"server_id", "freed_bytes", "usage"})
	if !reflect.DeepEqual(arrayField(t, objectField(t, schemas, "ServerRole"), "enum"), []any{"backend", "database"}) {
		t.Fatal("server.create must support both roles")
	}
	for _, schemaName := range []string{"RequestLog", "ProbeResponse"} {
		schema := objectField(t, schemas, schemaName)
		required := arrayField(t, schema, "required")
		for _, field := range []string{"source_ip", "user_agent", "region_code", "firewall_rule_id"} {
			found := false
			for _, actual := range required {
				found = found || actual == field
			}
			if !found {
				t.Fatalf("%s must require %s", schemaName, field)
			}
		}
		if _, exists := objectField(t, schema, "properties")["is_attack"]; exists {
			t.Fatal("public logs must not reveal attacker labels")
		}
	}
	match := objectField(t, schemas, "FirewallMatch")
	if match["minProperties"] != 1 || match["additionalProperties"] != false {
		t.Fatal("firewall match must require at least one known condition")
	}
	if !reflect.DeepEqual(arrayField(t, objectField(t, schemas, "PageRequestStatus"), "enum"), []any{200, 403, 500, 503}) {
		t.Fatal("page status schema must cover firewall, stopped site and failures")
	}
}

func TestControlOpenAPIAsyncCommandMetadataMatchesAcceptedResponse(t *testing.T) {
	document := loadOpenAPI(t)
	schemas := objectField(t, objectField(t, document, "components"), "schemas")
	want := []string{"database.backup", "database.restore", "server.create", "server.delete", "site.stop"}
	var got []string
	mapping := objectField(t, objectField(t, objectField(t, schemas, "ControlCommandRequest"), "discriminator"), "mapping")
	for command, ref := range mapping {
		request := resolveOpenAPIReference(t, document, ref.(string))
		mode := stringFieldFromMap(t, request, "x-execution")
		switch mode {
		case "asynchronous":
			got = append(got, command)
		case "synchronous":
		default:
			t.Fatalf("unexpected execution mode %q", mode)
		}
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("asynchronous commands = %v, want %v", got, want)
	}
	accepted := objectField(t, objectField(t, schemas, "ControlOperationAcceptedResponse"), "properties")
	enum := arrayField(t, objectField(t, accepted, "command"), "enum")
	var acceptedCommands []string
	for _, value := range enum {
		acceptedCommands = append(acceptedCommands, value.(string))
	}
	sort.Strings(acceptedCommands)
	if !reflect.DeepEqual(acceptedCommands, want) {
		t.Fatal("accepted response command enum differs from asynchronous commands")
	}
}

func TestControlOpenAPIReferencesResolveAndOperationIDsAreUnique(t *testing.T) {
	document := loadOpenAPI(t)
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if reference, exists := node["$ref"]; exists {
				resolveOpenAPIReference(t, document, reference.(string))
			}
			for _, child := range node {
				visit(child)
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(document)
	ids := map[string]bool{}
	for path, value := range objectField(t, document, "paths") {
		pathItem := value.(map[string]any)
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			if value, exists := pathItem[method]; exists {
				id := stringFieldFromMap(t, value.(map[string]any), "operationId")
				if ids[id] {
					t.Fatalf("duplicate operationId %s at %s", id, path)
				}
				ids[id] = true
			}
		}
	}
}

func TestOpenAPIContainsExactlyTheExecutableV2EndpointInventory(t *testing.T) {
	document := loadOpenAPI(t)
	want := map[string]bool{
		"POST /v2/start":                                    true,
		"GET /v2/runs/{run_id}/overview":                    true,
		"GET /v2/runs/{run_id}/metrics":                     true,
		"GET /v2/runs/{run_id}/logs":                        true,
		"GET /v2/runs/{run_id}/resources":                   true,
		"GET /v2/runs/{run_id}/operations/{operation_id}":   true,
		"POST /v2/runs/{run_id}/probes":                     true,
		"GET /v2/runs/{run_id}/inbox":                       true,
		"GET /v2/runs/{run_id}/control/commands":            true,
		"POST /v2/runs/{run_id}/control/commands":           true,
		"GET /v2/runs/{run_id}/credentials/{credential_id}": true,
		"POST /v2/runs/{run_id}/time/advance":               true,
	}
	got := map[string]bool{}
	for path, raw := range objectField(t, document, "paths") {
		item := raw.(map[string]any)
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			if _, exists := item[method]; exists {
				got[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OpenAPI endpoint inventory = %#v, want %#v", got, want)
	}
}

func resolveOpenAPIReference(t *testing.T, document map[string]any, reference string) map[string]any {
	t.Helper()
	if !strings.HasPrefix(reference, "#/") {
		t.Fatalf("unexpected external reference %q", reference)
	}
	node := document
	for _, field := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		field = strings.ReplaceAll(strings.ReplaceAll(field, "~1", "/"), "~0", "~")
		node = objectField(t, node, field)
	}
	return node
}

func loadOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	content, err := os.ReadFile("../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("decode openapi.yaml: %v", err)
	}
	return document
}

func objectField(t *testing.T, value map[string]any, name string) map[string]any {
	t.Helper()
	result, ok := value[name].(map[string]any)
	if !ok {
		t.Fatalf("field %q is not an object in %#v", name, value)
	}
	return result
}

func arrayField(t *testing.T, value map[string]any, name string) []any {
	t.Helper()
	result, ok := value[name].([]any)
	if !ok {
		t.Fatalf("field %q is not an array in %#v", name, value)
	}
	return result
}

func stringFieldFromMap(t *testing.T, value map[string]any, name string) string {
	t.Helper()
	result, ok := value[name].(string)
	if !ok {
		t.Fatalf("field %q is not a string in %#v", name, value)
	}
	return result
}

func assertRequiredFields(t *testing.T, schema map[string]any, want []string) {
	t.Helper()
	values := []any{}
	if _, exists := schema["required"]; exists {
		values = arrayField(t, schema, "required")
	}
	want = append([]string{}, want...)
	got := make([]string, 0, len(values))
	for _, value := range values {
		field, ok := value.(string)
		if !ok {
			t.Fatalf("required field is not a string: %#v", value)
		}
		got = append(got, field)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required fields = %v, want %v", got, want)
	}
}
