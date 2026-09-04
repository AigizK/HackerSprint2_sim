package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type EmptyControlParams struct{}
type FirewallDeleteParams struct {
	RuleID model.FirewallRuleID `json:"rule_id"`
}
type ServerTypesListParams struct {
	Role model.ServerRole `json:"role,omitempty"`
}
type ServerCreateParams struct {
	Name         string             `json:"name"`
	Role         model.ServerRole   `json:"role"`
	InstanceType model.InstanceType `json:"instance_type"`
}
type ServerIDParams struct {
	ServerID model.ServerID `json:"server_id"`
}
type DatabaseCreateParams struct {
	ServerID model.ServerID `json:"server_id"`
	Name     string         `json:"name"`
}
type DatabaseIDParams struct {
	DatabaseID model.DatabaseID `json:"database_id"`
}
type DatabaseBackupsListParams struct {
	DatabaseID model.DatabaseID `json:"database_id,omitempty"`
}
type DatabaseRestoreParams struct {
	DatabaseID model.DatabaseID `json:"database_id"`
	BackupID   model.BackupID   `json:"backup_id"`
}
type SiteDatabaseSetParams struct {
	DatabaseID                model.DatabaseID `json:"database_id"`
	ExpectedCurrentDatabaseID model.DatabaseID `json:"expected_current_database_id"`
}

type ControlCommandInput struct {
	RequestID  model.CommandID
	Command    string
	Params     any
	TargetAuth *TargetAuth
}

type ControlExecutionResult struct {
	RequestID           model.CommandID
	Command             string
	Result              json.RawMessage
	OperationID         model.OperationID
	EstimatedCompleteAt time.Time
}

type ServerTypeView struct {
	InstanceType        model.InstanceType `json:"instance_type"`
	Role                model.ServerRole   `json:"role"`
	CapacityUnits       int64              `json:"capacity_units"`
	DiskBytes           int64              `json:"disk_bytes"`
	ConnectionLimit     int                `json:"connection_limit"`
	ConnectionHoldMS    int64              `json:"connection_hold_ms"`
	CostPerHourMinor    int64              `json:"cost_per_hour_minor"`
	CostPerMonthMinor   int64              `json:"cost_per_month_minor"`
	ProvisioningSeconds float64            `json:"provisioning_seconds"`
}
type DiskUsageView struct {
	ServerID       model.ServerID `json:"server_id"`
	TotalBytes     int64          `json:"total_bytes"`
	SystemBytes    int64          `json:"system_bytes"`
	DatabaseBytes  int64          `json:"database_bytes"`
	LogsBytes      int64          `json:"logs_bytes"`
	UsedBytes      int64          `json:"used_bytes"`
	FreeBytes      int64          `json:"free_bytes"`
	CleanableBytes int64          `json:"cleanable_bytes"`
}
type ServerView struct {
	ServerID         model.ServerID              `json:"server_id"`
	Name             string                      `json:"name"`
	Role             model.ServerRole            `json:"role"`
	InstanceType     model.InstanceType          `json:"instance_type"`
	Status           model.ServerLifecycleStatus `json:"status"`
	CapacityUnits    int64                       `json:"capacity_units"`
	UsedLoadUnits    int64                       `json:"used_load_units"`
	CostPerHourMinor int64                       `json:"cost_per_hour_minor"`
	Disk             DiskUsageView               `json:"disk"`
	DatabaseIDs      []model.DatabaseID          `json:"database_ids"`
	CredentialID     model.CredentialID          `json:"credential_id"`
}
type DatabaseView struct {
	DatabaseID           model.DatabaseID `json:"database_id"`
	ServerID             model.ServerID   `json:"server_id"`
	Name                 string           `json:"name"`
	Status               string           `json:"status"`
	SizeBytes            int64            `json:"size_bytes"`
	DataVersion          uint64           `json:"data_version"`
	CapacityUnits        int64            `json:"capacity_units"`
	UsedLoadUnits        int64            `json:"used_load_units"`
	LastRestoredBackupID *model.BackupID  `json:"last_restored_backup_id"`
}
type BackupView struct {
	BackupID                model.BackupID              `json:"backup_id"`
	DatabaseID              model.DatabaseID            `json:"database_id"`
	SourceServerID          model.ServerID              `json:"source_server_id"`
	Status                  model.BackupLifecycleStatus `json:"status"`
	DataVersion             uint64                      `json:"data_version"`
	SizeBytes               int64                       `json:"size_bytes"`
	CreatedAt               time.Time                   `json:"created_at"`
	CompletedAt             *time.Time                  `json:"completed_at"`
	StorageCostPerHourMinor int64                       `json:"storage_cost_per_hour_minor"`
}
type SiteView struct {
	State      model.SiteLifecycleStatus `json:"state"`
	DatabaseID model.DatabaseID          `json:"database_id"`
}
type DiskCleanupView struct {
	ServerID   model.ServerID `json:"server_id"`
	FreedBytes int64          `json:"freed_bytes"`
	Usage      DiskUsageView  `json:"usage"`
}

type ControlOperationView struct {
	Operation simulation.OperationView
	Command   string
	RequestID model.CommandID
	Result    json.RawMessage
}

func (s *RunService) ExecuteControlCommand(ctx context.Context, runID string, request AgentRequest, input ControlCommandInput) (ApplicationResponse, error) {
	if !ValidRequestID(string(input.RequestID)) || input.Command == "" || input.Params == nil {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	paramsJSON, err := json.Marshal(input.Params)
	if err != nil {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	fingerprint := controlFingerprint(input.Command, paramsJSON)
	request.CommandID = input.RequestID
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		if err := ensureServerCredentials(ctx, s.credentials, run.Session); err != nil {
			return ApplicationResponse{}, err
		}
		state := run.Session.State()
		if receipt, exists := state.ControlCommandReceipts[input.RequestID]; exists {
			if receipt.Command != input.Command || receipt.PayloadSHA256 != fingerprint {
				return ApplicationResponse{}, ErrIdempotencyConflict
			}
			if err := s.authorizeControlTarget(ctx, state, input); err != nil {
				return ApplicationResponse{}, err
			}
			return ApplicationResponse{StatusCode: receipt.StatusCode, Value: ControlExecutionResult{RequestID: input.RequestID,
				Command: input.Command, Result: append(json.RawMessage(nil), receipt.Result...), OperationID: receipt.OperationID}}, nil
		}
		if err := s.authorizeControlTarget(ctx, state, input); err != nil {
			return ApplicationResponse{}, err
		}
		result, operationID, estimatedAt, err := s.executeNewControlCommand(ctx, run.Session, input)
		if err != nil {
			return ApplicationResponse{}, err
		}
		status := 200
		if operationID != "" {
			status = 202
		}
		if _, err := run.Session.Execute(ctx, simulation.RecordControlCommandResponse{CommandID: input.RequestID, Command: input.Command,
			PayloadSHA256: fingerprint, Params: paramsJSON, StatusCode: status, OperationID: operationID, Result: result}); err != nil {
			return ApplicationResponse{}, err
		}
		if operationID != "" {
			if err := s.recordTerminalOperationResult(ctx, run.Session, operationID); err != nil {
				return ApplicationResponse{}, err
			}
		}
		return ApplicationResponse{StatusCode: status, Value: ControlExecutionResult{RequestID: input.RequestID,
			Command: input.Command, Result: result, OperationID: operationID, EstimatedCompleteAt: estimatedAt}}, nil
	})
}

func (s *RunService) executeNewControlCommand(ctx context.Context, session *simulation.RunSession, input ControlCommandInput) (json.RawMessage, model.OperationID, time.Time, error) {
	state := session.State()
	switch params := input.Params.(type) {
	case model.FirewallRule:
		if input.Command != "firewall.rules.upsert" {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		if _, err := session.Execute(ctx, simulation.UpsertFirewallRule{CommandID: input.RequestID, Rule: params}); err != nil {
			return nil, "", time.Time{}, err
		}
		stored := session.State().FirewallRules[params.ID]
		return marshalControlResult(map[string]any{"rule": struct {
			model.FirewallRule
			Revision uint64 `json:"revision"`
		}{stored.Rule, stored.Revision}}), "", time.Time{}, nil
	case FirewallDeleteParams:
		if input.Command != "firewall.rules.delete" {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		if _, err := session.Execute(ctx, simulation.DeleteFirewallRule{CommandID: input.RequestID, RuleID: params.RuleID}); err != nil {
			return nil, "", time.Time{}, err
		}
		return marshalControlResult(map[string]any{"rule_id": params.RuleID, "deleted": true}), "", time.Time{}, nil
	case ServerTypesListParams:
		if input.Command != "server.types.list" || (params.Role != "" && params.Role != model.ServerRoleBackend && params.Role != model.ServerRoleDatabase) {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		return marshalControlResult(map[string]any{"types": serverTypesView(state, params.Role)}), "", time.Time{}, nil
	case ServerCreateParams:
		if input.Command != "server.create" || strings.TrimSpace(params.Name) == "" ||
			(params.Role != model.ServerRoleBackend && params.Role != model.ServerRoleDatabase) {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		typeState, ok := state.ServerTypes[params.InstanceType]
		if !ok || typeState.Role != params.Role {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		serverToken, err := NewRunID()
		if err != nil {
			return nil, "", time.Time{}, err
		}
		operationToken, err := NewRunID()
		if err != nil {
			return nil, "", time.Time{}, err
		}
		serverID := model.ServerID("server-" + serverToken)
		operationID := model.OperationID(operationToken)
		credentialID := simulation.ServerCredentialID(state.RunID, serverID, 1)
		if _, err := session.Execute(ctx, simulation.AddTypedServer{CommandID: input.RequestID, OperationID: operationID,
			ServerID: serverID, Name: strings.TrimSpace(params.Name), CredentialID: credentialID, InstanceType: params.InstanceType}); err != nil {
			return nil, "", time.Time{}, err
		}
		if err := ensureServerCredentials(ctx, s.credentials, session); err != nil {
			return nil, "", time.Time{}, err
		}
		server := session.State().Servers[serverID]
		return nil, operationID, server.ReadyAt, nil
	case ServerIDParams:
		switch input.Command {
		case "server.inspect":
			server, ok := state.Servers[params.ServerID]
			if !ok {
				return nil, "", time.Time{}, simulation.ErrResourceNotFound
			}
			return marshalControlResult(map[string]any{"server": buildServerView(state, server)}), "", time.Time{}, nil
		case "server.delete":
			operationToken, err := NewRunID()
			if err != nil {
				return nil, "", time.Time{}, err
			}
			operationID := model.OperationID(operationToken)
			if _, err := session.Execute(ctx, simulation.RemoveServer{CommandID: input.RequestID, OperationID: operationID, ServerID: params.ServerID}); err != nil {
				return nil, "", time.Time{}, err
			}
			return nil, operationID, time.Time{}, nil
		case "disk.usage":
			usage, err := simulation.NewProjection(nil, state).DiskUsage(params.ServerID)
			if err != nil {
				return nil, "", time.Time{}, err
			}
			return marshalControlResult(diskUsageView(usage)), "", time.Time{}, nil
		case "disk.cleanup":
			before, err := simulation.NewProjection(nil, state).DiskUsage(params.ServerID)
			if err != nil {
				return nil, "", time.Time{}, err
			}
			if _, err := session.Execute(ctx, simulation.CleanupDatabaseLogs{CommandID: input.RequestID, ServerID: params.ServerID}); err != nil {
				return nil, "", time.Time{}, err
			}
			after, _ := session.Projection().DiskUsage(params.ServerID)
			return marshalControlResult(DiskCleanupView{ServerID: params.ServerID, FreedBytes: before.LogsBytes, Usage: diskUsageView(after)}), "", time.Time{}, nil
		default:
			return nil, "", time.Time{}, ErrInvalidRequest
		}
	case DatabaseCreateParams:
		if input.Command != "database.create" || strings.TrimSpace(params.Name) == "" {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		token, err := NewRunID()
		if err != nil {
			return nil, "", time.Time{}, err
		}
		databaseID := model.DatabaseID("db-" + token)
		if _, err := session.Execute(ctx, simulation.CreateDatabase{CommandID: input.RequestID, DatabaseID: databaseID,
			ServerID: params.ServerID, Name: strings.TrimSpace(params.Name)}); err != nil {
			return nil, "", time.Time{}, err
		}
		return marshalControlResult(map[string]any{"database": buildDatabaseView(session.State(), session.State().Databases[databaseID])}), "", time.Time{}, nil
	case DatabaseIDParams:
		database, ok := state.Databases[params.DatabaseID]
		if !ok {
			return nil, "", time.Time{}, simulation.ErrResourceNotFound
		}
		switch input.Command {
		case "database.inspect":
			return marshalControlResult(map[string]any{"database": buildDatabaseView(state, database)}), "", time.Time{}, nil
		case "database.backup":
			backupToken, err := NewRunID()
			if err != nil {
				return nil, "", time.Time{}, err
			}
			operationToken, err := NewRunID()
			if err != nil {
				return nil, "", time.Time{}, err
			}
			operationID := model.OperationID(operationToken)
			if _, err := session.Execute(ctx, simulation.BackupDatabase{CommandID: input.RequestID, OperationID: operationID,
				BackupID: model.BackupID("backup-" + backupToken), DatabaseID: params.DatabaseID}); err != nil {
				return nil, "", time.Time{}, err
			}
			return nil, operationID, time.Time{}, nil
		default:
			return nil, "", time.Time{}, ErrInvalidRequest
		}
	case DatabaseBackupsListParams:
		if input.Command != "database.backups.list" {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		backups := simulation.NewProjection(nil, state).DatabaseBackups(params.DatabaseID)
		views := make([]BackupView, 0, len(backups))
		for _, backup := range backups {
			views = append(views, buildBackupView(backup))
		}
		return marshalControlResult(map[string]any{"backups": views}), "", time.Time{}, nil
	case DatabaseRestoreParams:
		if input.Command != "database.restore" {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		operationToken, err := NewRunID()
		if err != nil {
			return nil, "", time.Time{}, err
		}
		operationID := model.OperationID(operationToken)
		if _, err := session.Execute(ctx, simulation.RestoreDatabase{CommandID: input.RequestID, OperationID: operationID,
			BackupID: params.BackupID, DatabaseID: params.DatabaseID}); err != nil {
			return nil, "", time.Time{}, err
		}
		return nil, operationID, time.Time{}, nil
	case EmptyControlParams:
		switch input.Command {
		case "firewall.rules.list":
			rules := session.Projection().FirewallRules()
			views := make([]any, 0, len(rules))
			for _, stored := range rules {
				views = append(views, struct {
					model.FirewallRule
					Revision uint64 `json:"revision"`
				}{stored.Rule, stored.Revision})
			}
			return marshalControlResult(map[string]any{"rules": views}), "", time.Time{}, nil
		case "site.config.get":
			return marshalControlResult(map[string]any{"site": buildSiteView(state)}), "", time.Time{}, nil
		case "site.stop":
			operationToken, err := NewRunID()
			if err != nil {
				return nil, "", time.Time{}, err
			}
			operationID := model.OperationID(operationToken)
			if _, err := session.Execute(ctx, simulation.StopSite{CommandID: input.RequestID, OperationID: operationID}); err != nil {
				return nil, "", time.Time{}, err
			}
			return nil, operationID, time.Time{}, nil
		case "site.start":
			if _, err := session.Execute(ctx, simulation.StartSite{CommandID: input.RequestID}); err != nil {
				return nil, "", time.Time{}, err
			}
			return marshalControlResult(map[string]any{"site": buildSiteView(session.State())}), "", time.Time{}, nil
		default:
			return nil, "", time.Time{}, ErrInvalidRequest
		}
	case SiteDatabaseSetParams:
		if input.Command != "site.database.set" {
			return nil, "", time.Time{}, ErrInvalidRequest
		}
		if _, err := session.Execute(ctx, simulation.SetSiteDatabase{CommandID: input.RequestID, DatabaseID: params.DatabaseID,
			ExpectedCurrentDatabaseID: params.ExpectedCurrentDatabaseID}); err != nil {
			return nil, "", time.Time{}, err
		}
		return marshalControlResult(map[string]any{"site": buildSiteView(session.State())}), "", time.Time{}, nil
	default:
		return nil, "", time.Time{}, ErrInvalidRequest
	}
}

func (s *RunService) authorizeControlTarget(ctx context.Context, state simulation.State, input ControlCommandInput) error {
	serverID, protected, err := controlTargetServer(state, input)
	if err != nil {
		return err
	}
	if !protected {
		if input.TargetAuth != nil {
			return ErrInvalidRequest
		}
		return nil
	}
	if input.TargetAuth == nil {
		return ErrTargetUnauthorized
	}
	return authenticateTarget(ctx, s.credentials, state, serverID, *input.TargetAuth)
}

func controlTargetServer(state simulation.State, input ControlCommandInput) (model.ServerID, bool, error) {
	switch input.Command {
	case "database.create":
		return input.Params.(DatabaseCreateParams).ServerID, true, nil
	case "database.inspect", "database.backup":
		id := input.Params.(DatabaseIDParams).DatabaseID
		database, ok := state.Databases[id]
		if !ok {
			return "", true, simulation.ErrResourceNotFound
		}
		return database.ServerID, true, nil
	case "database.restore":
		id := input.Params.(DatabaseRestoreParams).DatabaseID
		database, ok := state.Databases[id]
		if !ok {
			return "", true, simulation.ErrResourceNotFound
		}
		return database.ServerID, true, nil
	case "disk.usage", "disk.cleanup":
		return input.Params.(ServerIDParams).ServerID, true, nil
	default:
		return "", false, nil
	}
}

func controlFingerprint(command string, params []byte) string {
	digest := sha256.Sum256(append(append([]byte(command), 0), params...))
	return hex.EncodeToString(digest[:])
}

func marshalControlResult(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func serverTypesView(state simulation.State, role model.ServerRole) []ServerTypeView {
	types := simulation.NewProjection(nil, state).ServerTypes(role)
	result := make([]ServerTypeView, 0, len(types))
	for _, item := range types {
		capacity := item.BackendCapacityUnits
		if item.Role == model.ServerRoleDatabase {
			capacity = int64(item.ConnectionLimit)
		}
		result = append(result, ServerTypeView{InstanceType: item.InstanceType, Role: item.Role, CapacityUnits: capacity,
			DiskBytes: item.DiskBytes, ConnectionLimit: item.ConnectionLimit, ConnectionHoldMS: item.ConnectionHold.Milliseconds(),
			CostPerHourMinor: hourlyCost(item.CostPerHourMinor, item.CostPerMonthMinor), CostPerMonthMinor: item.CostPerMonthMinor,
			ProvisioningSeconds: state.Infrastructure.ServerProvisioningDuration.Seconds()})
	}
	return result
}

func buildServerView(state simulation.State, server simulation.ServerState) ServerView {
	usage, _ := simulation.NewProjection(nil, state).DiskUsage(server.ID)
	ids := make([]model.DatabaseID, 0)
	for id, database := range state.Databases {
		if database.ServerID == server.ID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	used := state.UsedCapacityByServer[server.ID]
	if server.Role == model.ServerRoleDatabase {
		server.CapacityUnits = int64(server.ConnectionLimit)
		for id, database := range state.Databases {
			if database.ServerID == server.ID {
				used += int64(state.DatabaseConnectionCounts[id])
			}
		}
	}
	return ServerView{ServerID: server.ID, Name: server.Name, Role: server.Role, InstanceType: server.InstanceType,
		Status: server.Status, CapacityUnits: server.CapacityUnits, UsedLoadUnits: used,
		CostPerHourMinor: hourlyCost(server.CostPerHourMinor, server.CostPerMonthMinor), Disk: diskUsageView(usage),
		DatabaseIDs: ids, CredentialID: server.CredentialID}
}

func buildDatabaseView(state simulation.State, database simulation.DatabaseState) DatabaseView {
	status := "ready"
	if !database.Ready {
		status = "restoring"
	} else if database.DataBytes == 0 && database.DataVersion == 0 {
		status = "empty"
	} else if len(database.AvailabilityReasons) > 0 {
		status = "unavailable"
	}
	for _, backup := range state.Backups {
		if backup.DatabaseID == database.ID && backup.Status == model.BackupCreating {
			status = "backing_up"
		}
	}
	var restored *model.BackupID
	if database.RestoredBackupID != "" {
		value := database.RestoredBackupID
		restored = &value
	}
	server := state.Servers[database.ServerID]
	return DatabaseView{DatabaseID: database.ID, ServerID: database.ServerID, Name: database.Name, Status: status,
		SizeBytes: database.DataBytes, DataVersion: database.DataVersion, CapacityUnits: int64(server.ConnectionLimit),
		UsedLoadUnits: int64(state.DatabaseConnectionCounts[database.ID]), LastRestoredBackupID: restored}
}

func buildBackupView(backup simulation.BackupState) BackupView {
	return BackupView{BackupID: backup.ID, DatabaseID: backup.DatabaseID, SourceServerID: backup.ServerID,
		Status: backup.Status, DataVersion: backup.DataVersion, SizeBytes: backup.DataBytes, CreatedAt: backup.CreatedAt,
		CompletedAt: timePointerValue(backup.CompletedAt), StorageCostPerHourMinor: backup.StorageCostPerHourMinor}
}

func buildSiteView(state simulation.State) SiteView {
	return SiteView{State: state.Site.Status, DatabaseID: state.Site.DatabaseID}
}

func diskUsageView(usage simulation.DiskUsageView) DiskUsageView {
	return DiskUsageView{ServerID: usage.ServerID, TotalBytes: usage.TotalBytes, SystemBytes: usage.SystemBytes,
		DatabaseBytes: usage.DatabaseBytes, LogsBytes: usage.LogsBytes, UsedBytes: usage.UsedBytes,
		FreeBytes: usage.FreeBytes, CleanableBytes: usage.CleanableBytes}
}

func hourlyCost(hourly, monthly int64) int64 {
	if hourly > 0 {
		return hourly
	}
	if monthly <= 0 {
		return 0
	}
	return (monthly + 30*24 - 1) / (30 * 24)
}

func timePointerValue(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func (s *RunService) recordTerminalOperationResult(ctx context.Context, session *simulation.RunSession, operationID model.OperationID) error {
	state := session.State()
	operation, exists := state.Operations[operationID]
	if !exists || (operation.Status != model.OperationStatusSucceeded && operation.Status != model.OperationStatusFailed) {
		return nil
	}
	if _, exists := state.ControlOperationResults[operationID]; exists {
		return nil
	}
	var receipt simulation.ControlCommandReceiptState
	for _, candidate := range state.ControlCommandReceipts {
		if candidate.OperationID == operationID {
			receipt = candidate
			break
		}
	}
	if receipt.CommandID == "" {
		return nil
	}
	result, err := operationResult(state, receipt)
	if err != nil {
		return err
	}
	errorCode, message := "", ""
	if operation.Status == model.OperationStatusFailed {
		result, errorCode, message = json.RawMessage("null"), operation.ErrorCode, operation.Message
	}
	_, err = session.Execute(ctx, simulation.RecordControlOperationResult{OperationID: operationID, CommandID: receipt.CommandID,
		Command: receipt.Command, Result: result, ErrorCode: errorCode, Message: message})
	return err
}

func operationResult(state simulation.State, receipt simulation.ControlCommandReceiptState) (json.RawMessage, error) {
	switch receipt.Command {
	case "server.create":
		for _, server := range state.Servers {
			if server.OperationID == receipt.OperationID {
				return marshalControlResult(map[string]any{"server": buildServerView(state, server)}), nil
			}
		}
		return nil, simulation.ErrResourceNotFound
	case "server.delete":
		var params ServerIDParams
		if err := json.Unmarshal(receipt.Params, &params); err != nil {
			return nil, err
		}
		return marshalControlResult(map[string]any{"server_id": params.ServerID, "deleted": true}), nil
	case "database.backup":
		for _, backup := range state.Backups {
			if backup.OperationID == receipt.OperationID {
				return marshalControlResult(map[string]any{"backup": buildBackupView(backup)}), nil
			}
		}
		return nil, simulation.ErrResourceNotFound
	case "database.restore":
		var params DatabaseRestoreParams
		if err := json.Unmarshal(receipt.Params, &params); err != nil {
			return nil, err
		}
		database, exists := state.Databases[params.DatabaseID]
		if !exists {
			return nil, simulation.ErrResourceNotFound
		}
		return marshalControlResult(map[string]any{"database": buildDatabaseView(state, database)}), nil
	case "site.stop":
		return marshalControlResult(map[string]any{"site": buildSiteView(state)}), nil
	default:
		return nil, ErrInvalidRequest
	}
}

func (s *RunService) ServerCredential(ctx context.Context, runID string, request AgentRequest, credentialID model.CredentialID) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		if err := ensureServerCredentials(ctx, s.credentials, run.Session); err != nil {
			return ApplicationResponse{}, err
		}
		state := run.Session.State()
		metadata, exists := state.ServerCredentials[credentialID]
		if !exists || metadata.IssuedAt.After(state.Clock.CurrentTime) {
			return ApplicationResponse{}, simulation.ErrResourceNotFound
		}
		credential, err := s.credentials.GetServerCredential(ctx, runID, string(credentialID))
		if err != nil {
			return ApplicationResponse{}, err
		}
		credential.ValidFrom, credential.ExpiresAt, credential.IssuedAt = metadata.ValidFrom, metadata.ExpiresAt, metadata.IssuedAt
		return ApplicationResponse{StatusCode: 200, Value: credential}, nil
	})
}

func (s *RunService) EnrichedOperation(ctx context.Context, runID string, request AgentRequest, operationID model.OperationID) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		if err := s.recordTerminalOperationResult(ctx, run.Session, operationID); err != nil && !errors.Is(err, simulation.ErrResourceNotFound) {
			return ApplicationResponse{}, err
		}
		state := run.Session.State()
		operation, err := simulation.NewProjection(nil, state).Operation(operationID)
		if err != nil {
			return ApplicationResponse{}, err
		}
		view := ControlOperationView{Operation: operation}
		for _, receipt := range state.ControlCommandReceipts {
			if receipt.OperationID == operationID {
				view.Command, view.RequestID = receipt.Command, receipt.CommandID
				break
			}
		}
		if terminal, exists := state.ControlOperationResults[operationID]; exists {
			view.Result = append(json.RawMessage(nil), terminal.Result...)
		}
		return ApplicationResponse{StatusCode: 200, Value: view}, nil
	})
}
