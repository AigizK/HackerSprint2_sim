package spec_test

import (
	"os"
	"strings"
	"testing"
)

func TestOpenAPIStartV2IncludesGuideAndControlPanelAccess(t *testing.T) {
	document := loadOpenAPI(t)
	paths := objectField(t, document, "paths")
	for path := range paths {
		if !strings.HasPrefix(path, "/v2/") {
			t.Fatalf("non-v2 endpoint: %s", path)
		}
	}
	start := objectField(t, objectField(t, paths, "/v2/start"), "post")
	if len(arrayField(t, start, "security")) != 0 {
		t.Fatal("start must issue credentials without requiring existing panel access")
	}
	for _, code := range []string{"200", "201"} {
		response := objectField(t, objectField(t, start, "responses"), code)
		cache := objectField(t, objectField(t, objectField(t, response, "headers"), "Cache-Control"), "schema")
		if cache["const"] != "no-store" {
			t.Fatalf("start %s must prohibit response caching", code)
		}
	}
	schemas := objectField(t, objectField(t, document, "components"), "schemas")
	response := objectField(t, schemas, "StartRunResponse")
	assertRequiredFields(t, response, []string{
		"run_id", "seed", "agent_id", "agent_version", "status", "simulation_time", "simulation_ends_at",
		"commands_markdown", "control_panel_auth",
	})
	properties := objectField(t, response, "properties")
	guide := objectField(t, properties, "commands_markdown")
	if guide["type"] != "string" || guide["contentMediaType"] != "text/markdown" {
		t.Fatal("commands_markdown must contain Markdown text")
	}
	if objectField(t, properties, "control_panel_auth")["$ref"] != "#/components/schemas/ControlPanelAuth" {
		t.Fatal("start must return the control panel auth schema")
	}
	auth := objectField(t, schemas, "ControlPanelAuth")
	assertRequiredFields(t, auth, []string{"scheme", "username", "password", "instructions"})
	authProperties := objectField(t, auth, "properties")
	if objectField(t, authProperties, "scheme")["const"] != "basic" {
		t.Fatal("panel access must use HTTP Basic")
	}
	if objectField(t, authProperties, "password")["writeOnly"] == true {
		t.Fatal("start must return the password, not hide it as a request-only field")
	}
}

func TestCommandsGuideCoversEveryCommandAndParameter(t *testing.T) {
	data, err := os.ReadFile("../COMMANDS.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(data)
	document := loadOpenAPI(t)
	schemas := objectField(t, objectField(t, document, "components"), "schemas")
	mapping := objectField(t, objectField(t, objectField(t, schemas, "ControlCommandRequest"), "discriminator"), "mapping")
	for command, ref := range mapping {
		t.Run(command, func(t *testing.T) {
			heading := "### " + command + "\n"
			if strings.Count(guide, heading) != 1 {
				t.Fatal("each command must have exactly one detailed section")
			}
			_, section, _ := strings.Cut(guide, heading)
			section, _, _ = strings.Cut(section, "\n#")
			request := resolveOpenAPIReference(t, document, ref.(string))
			props := objectField(t, request, "properties")
			params := resolveOpenAPIReference(t, document, objectField(t, props, "params")["$ref"].(string))
			if fields, ok := params["properties"].(map[string]any); ok {
				for field := range fields {
					if !strings.Contains(section, "`"+field+"`") && !strings.Contains(section, "\""+field+"\"") {
						t.Fatalf("missing parameter %s", field)
					}
				}
			}
			if !strings.Contains(section, "target_auth") {
				t.Fatal("command must explain server credential requirements")
			}
			if (request["x-execution"] == "asynchronous") != strings.Contains(section, "Асинхронно") {
				t.Fatal("guide execution mode differs from OpenAPI")
			}
		})
	}
}
