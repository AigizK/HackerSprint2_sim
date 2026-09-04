package httpapi

import (
	"net/http"
	"testing"
)

type panelTestAccess struct{ username, password string }

func startControlTestRun(t *testing.T, api *Server, requestID string) (string, panelTestAccess) {
	t.Helper()
	started := callJSON(t, api, http.MethodPost, "/v2/start", map[string]any{
		"seed": -1, "agent_id": "control-agent", "agent_version": "v2", "request_id": requestID,
	}, http.StatusCreated)
	auth := started["control_panel_auth"].(map[string]any)
	return stringField(t, started, "run_id"), panelTestAccess{stringField(t, auth, "username"), stringField(t, auth, "password")}
}

func controlCommand(t *testing.T, api *Server, runID string, access panelTestAccess, status int, requestID, command string, params map[string]any, targetAuth map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"request_id": requestID, "command": command, "params": params}
	if targetAuth != nil {
		body["target_auth"] = targetAuth
	}
	return callJSONBasic(t, api, http.MethodPost, "/v2/runs/"+runID+"/control/commands", body, status, access.username, access.password)
}

func serverCredentialFor(t *testing.T, api *Server, runID string, access panelTestAccess, serverID, requestID string) (string, map[string]any) {
	t.Helper()
	inspected := controlCommand(t, api, runID, access, http.StatusOK, requestID, "server.inspect", map[string]any{"server_id": serverID}, nil)
	server := inspected["result"].(map[string]any)["server"].(map[string]any)
	credentialID := stringField(t, server, "credential_id")
	response := callJSONBasic(t, api, http.MethodGet, "/v2/runs/"+runID+"/credentials/"+credentialID, nil, http.StatusOK, access.username, access.password)
	credential := response["credential"].(map[string]any)
	return credentialID, map[string]any{"username": credential["username"], "password": credential["password"]}
}

func TestControlCatalogCredentialsAndStrictAuthorization(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	runID, access := startControlTestRun(t, api, "start-control-catalog")
	base := "/v2/runs/" + runID

	unauthorized := callJSON(t, api, http.MethodGet, base+"/control/commands", nil, http.StatusUnauthorized)
	if stringField(t, unauthorized, "error") != "CONTROL_UNAUTHORIZED" {
		t.Fatalf("unauthorized = %#v", unauthorized)
	}
	catalog := callJSONBasic(t, api, http.MethodGet, base+"/control/commands", nil, http.StatusOK, access.username, access.password)
	commands := catalog["commands"].([]any)
	if len(commands) != 18 {
		t.Fatalf("catalog command count = %d", len(commands))
	}
	seen := map[string]bool{}
	for _, raw := range commands {
		definition := raw.(map[string]any)
		name := stringField(t, definition, "command")
		if seen[name] || definition["params_schema"] == nil || definition["result_schema"] == nil {
			t.Fatalf("invalid command definition %#v", definition)
		}
		seen[name] = true
	}

	_, databaseAuth := serverCredentialFor(t, api, runID, access, "db-server-1", "inspect-db-server-auth")
	bad := controlCommand(t, api, runID, access, http.StatusForbidden, "inspect-db-auth-retry", "database.inspect", map[string]any{"database_id": "db-main"}, map[string]any{"username": "bad", "password": "bad"})
	if stringField(t, bad, "error") != "TARGET_UNAUTHORIZED" {
		t.Fatalf("bad target auth = %#v", bad)
	}
	controlCommand(t, api, runID, access, http.StatusOK, "inspect-db-auth-retry", "database.inspect", map[string]any{"database_id": "db-main"}, databaseAuth)

	forbidden := controlCommand(t, api, runID, access, http.StatusBadRequest, "target-auth-forbidden", "site.config.get", map[string]any{}, databaseAuth)
	if stringField(t, forbidden, "error") != "INVALID_REQUEST" {
		t.Fatalf("unexpected target_auth response = %#v", forbidden)
	}
	invalid := controlCommand(t, api, runID, access, http.StatusBadRequest, "strict-params", "server.inspect", map[string]any{"server_id": "server-1", "extra": true}, nil)
	if stringField(t, invalid, "error") != "INVALID_REQUEST" {
		t.Fatalf("strict params = %#v", invalid)
	}
}

func TestAllInfrastructureControlCommandsEndToEnd(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	runID, access := startControlTestRun(t, api, "start-control-e2e")
	base := "/v2/runs/" + runID
	controlCommand(t, api, runID, access, http.StatusOK, "firewall-list-e2e", "firewall.rules.list", map[string]any{}, nil)
	controlCommand(t, api, runID, access, http.StatusOK, "firewall-upsert-e2e", "firewall.rules.upsert", map[string]any{
		"rule_id": "temporary-deny-e2e", "priority": 100, "action": "deny", "enabled": true,
		"match": map[string]any{"user_agent": map[string]any{"operator": "contains", "value": "GeneratedBot"}},
	}, nil)
	controlCommand(t, api, runID, access, http.StatusOK, "firewall-delete-e2e", "firewall.rules.delete", map[string]any{"rule_id": "temporary-deny-e2e"}, nil)

	types := controlCommand(t, api, runID, access, http.StatusOK, "types-database", "server.types.list", map[string]any{"role": "database"}, nil)
	if len(types["result"].(map[string]any)["types"].([]any)) != 3 {
		t.Fatalf("database types = %#v", types)
	}
	_, oldDatabaseAuth := serverCredentialFor(t, api, runID, access, "db-server-1", "inspect-old-db-server")
	usage := controlCommand(t, api, runID, access, http.StatusOK, "usage-old-db", "disk.usage", map[string]any{"server_id": "db-server-1"}, oldDatabaseAuth)
	if numberField(t, usage["result"].(map[string]any), "database_bytes") <= 0 || numberField(t, usage["result"].(map[string]any), "logs_bytes") <= 0 {
		t.Fatalf("disk usage = %#v", usage)
	}
	cleaned := controlCommand(t, api, runID, access, http.StatusOK, "cleanup-old-db", "disk.cleanup", map[string]any{"server_id": "db-server-1"}, oldDatabaseAuth)
	if numberField(t, cleaned["result"].(map[string]any), "freed_bytes") <= 0 {
		t.Fatalf("cleanup = %#v", cleaned)
	}
	controlCommand(t, api, runID, access, http.StatusOK, "inspect-old-db", "database.inspect", map[string]any{"database_id": "db-main"}, oldDatabaseAuth)
	controlCommand(t, api, runID, access, http.StatusOK, "site-before-stop", "site.config.get", map[string]any{}, nil)

	stopped := controlCommand(t, api, runID, access, http.StatusAccepted, "stop-for-migration", "site.stop", map[string]any{}, nil)
	stopOperationID := stringField(t, stopped, "operation_id")
	stopOperation := callJSON(t, api, http.MethodGet, base+"/operations/"+stopOperationID, nil, http.StatusOK)
	if stopOperation["status"] != "succeeded" || stopOperation["result"] == nil {
		t.Fatalf("stop operation = %#v", stopOperation)
	}

	backupAccepted := controlCommand(t, api, runID, access, http.StatusAccepted, "backup-main", "database.backup", map[string]any{"database_id": "db-main"}, oldDatabaseAuth)
	backupOperation := callJSON(t, api, http.MethodGet, base+"/operations/"+stringField(t, backupAccepted, "operation_id"), nil, http.StatusOK)
	backupResult := backupOperation["result"].(map[string]any)["result"].(map[string]any)["backup"].(map[string]any)
	backupID := stringField(t, backupResult, "backup_id")

	createServer := controlCommand(t, api, runID, access, http.StatusAccepted, "create-large-db-server", "server.create", map[string]any{
		"name": "database-large", "role": "database", "instance_type": "db.large",
	}, nil)
	createOperationID := stringField(t, createServer, "operation_id")
	callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{"request_id": "wait-provisioning", "duration_seconds": 300}, http.StatusOK)
	createOperation := callJSON(t, api, http.MethodGet, base+"/operations/"+createOperationID, nil, http.StatusOK)
	createdServer := createOperation["result"].(map[string]any)["result"].(map[string]any)["server"].(map[string]any)
	newServerID := stringField(t, createdServer, "server_id")
	credentialID := stringField(t, createdServer, "credential_id")
	credentialResponse := callJSONBasic(t, api, http.MethodGet, base+"/credentials/"+credentialID, nil, http.StatusOK, access.username, access.password)
	credential := credentialResponse["credential"].(map[string]any)
	newDatabaseAuth := map[string]any{"username": credential["username"], "password": credential["password"]}

	createdDatabase := controlCommand(t, api, runID, access, http.StatusOK, "create-new-db", "database.create", map[string]any{
		"server_id": newServerID, "name": "service-new",
	}, newDatabaseAuth)
	newDatabaseID := stringField(t, createdDatabase["result"].(map[string]any)["database"].(map[string]any), "database_id")
	restoreAccepted := controlCommand(t, api, runID, access, http.StatusAccepted, "restore-new-db", "database.restore", map[string]any{
		"database_id": newDatabaseID, "backup_id": backupID,
	}, newDatabaseAuth)
	restoreOperation := callJSON(t, api, http.MethodGet, base+"/operations/"+stringField(t, restoreAccepted, "operation_id"), nil, http.StatusOK)
	if restoreOperation["status"] != "succeeded" {
		t.Fatalf("restore operation = %#v", restoreOperation)
	}

	controlCommand(t, api, runID, access, http.StatusOK, "list-main-backups", "database.backups.list", map[string]any{"database_id": "db-main"}, nil)
	controlCommand(t, api, runID, access, http.StatusOK, "switch-new-db", "site.database.set", map[string]any{
		"database_id": newDatabaseID, "expected_current_database_id": "db-main",
	}, nil)
	controlCommand(t, api, runID, access, http.StatusOK, "start-after-migration", "site.start", map[string]any{}, nil)

	deleteOld := controlCommand(t, api, runID, access, http.StatusAccepted, "delete-old-db-server", "server.delete", map[string]any{"server_id": "db-server-1"}, nil)
	deleteOperation := callJSON(t, api, http.MethodGet, base+"/operations/"+stringField(t, deleteOld, "operation_id"), nil, http.StatusOK)
	if deleteOperation["status"] != "succeeded" {
		t.Fatalf("delete operation = %#v", deleteOperation)
	}
	controlCommand(t, api, runID, access, http.StatusNotFound, "inspect-deleted-server", "server.inspect", map[string]any{"server_id": "db-server-1"}, nil)
}

func TestControlCommandIdempotencyUsesCommandAndParams(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	runID, access := startControlTestRun(t, api, "start-control-idempotency")
	first := controlCommand(t, api, runID, access, http.StatusOK, "site-config-stable", "site.config.get", map[string]any{}, nil)
	repeated := controlCommand(t, api, runID, access, http.StatusOK, "site-config-stable", "site.config.get", map[string]any{}, nil)
	if first["result"].(map[string]any)["site"].(map[string]any)["state"] != repeated["result"].(map[string]any)["site"].(map[string]any)["state"] {
		t.Fatal("saved result was not replayed")
	}
	conflict := controlCommand(t, api, runID, access, http.StatusConflict, "site-config-stable", "server.types.list", map[string]any{}, nil)
	if stringField(t, conflict, "error") != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict = %#v", conflict)
	}
}

func TestScheduledCredentialRotationArrivesInInboxAndExpiresOldSecret(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	runID, access := startControlTestRun(t, api, "start-credential-rotation")
	base := "/v2/runs/" + runID
	oldCredentialID, oldAuth := serverCredentialFor(t, api, runID, access, "db-server-1", "inspect-before-rotation")

	callJSON(t, api, http.MethodPost, base+"/time/advance", map[string]any{
		"request_id": "reach-credential-rotation", "duration_seconds": 1800,
	}, http.StatusOK)
	inbox := callJSON(t, api, http.MethodGet, base+"/inbox", nil, http.StatusOK)
	messages := inbox["messages"].([]any)
	rotationMessage := messages[len(messages)-1].(map[string]any)
	if rotationMessage["subject"] != "Новые credentials сервера db-server-1" {
		t.Fatalf("rotation message = %#v", rotationMessage)
	}

	inspected := controlCommand(t, api, runID, access, http.StatusOK, "inspect-after-rotation", "server.inspect", map[string]any{"server_id": "db-server-1"}, nil)
	newCredentialID := stringField(t, inspected["result"].(map[string]any)["server"].(map[string]any), "credential_id")
	if newCredentialID == oldCredentialID {
		t.Fatal("credential id did not rotate")
	}
	newRecord := callJSONBasic(t, api, http.MethodGet, base+"/credentials/"+newCredentialID, nil, http.StatusOK, access.username, access.password)
	newCredential := newRecord["credential"].(map[string]any)
	newAuth := map[string]any{"username": newCredential["username"], "password": newCredential["password"]}

	expired := controlCommand(t, api, runID, access, http.StatusForbidden, "usage-after-rotation", "disk.usage", map[string]any{"server_id": "db-server-1"}, oldAuth)
	if stringField(t, expired, "error") != "CREDENTIALS_EXPIRED" {
		t.Fatalf("old credential response = %#v", expired)
	}
	controlCommand(t, api, runID, access, http.StatusOK, "usage-after-rotation", "disk.usage", map[string]any{"server_id": "db-server-1"}, newAuth)
}
