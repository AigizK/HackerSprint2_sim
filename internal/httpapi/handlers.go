package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	agentdocs "github.com/aigizk/hackersprint2-sim"
	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/controlauth"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func handleOpenAPI(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(agentdocs.OpenAPIYAML()))
}

func (s *Server) handleStartRun(writer http.ResponseWriter, request *http.Request) {
	var input startRunRequest
	body, err := decodeRequestBody(writer, request, &input)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	if input.Seed == nil {
		s.writeError(writer, application.ErrInvalidRequest)
		return
	}
	agentRequest, err := s.applicationRequest(request, body, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result, err := s.start.StartAudited(request.Context(), application.StartRunRequest{
		Seed: *input.Seed, AgentID: input.AgentID, AgentVersion: input.AgentVersion, RequestID: input.RequestID,
	}, agentRequest)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, startRunResponse{RunID: result.Run.RunID, Seed: result.State.Seed,
		AgentID: result.Run.AgentID, AgentVersion: result.Run.AgentVersion, Status: publicRunStatus(result.State),
		SimulationTime: result.State.Clock.CurrentTime, SimulationEnds: result.State.Clock.EndsAt,
		CommandsMarkdown: agentdocs.CommandsMarkdown(),
		ControlPanelAuth: controlPanelAuthResponse{
			Scheme: "basic", Username: result.ControlPanelAuth.Username, Password: result.ControlPanelAuth.Password,
			Instructions: "Используйте эти username и password для HTTP Basic Auth " +
				"(Authorization: Basic <base64(username:password)>) при GET/POST /v2/runs/" + result.Run.RunID +
				"/control/commands и GET /v2/runs/" + result.Run.RunID + "/credentials/{credential_id}. " +
				"Это доступ к панели текущего run, не серверный target_auth. Все 18 типизированных команд, каталог и источник " +
				"серверных credentials доступны; подробности находятся в commands_markdown.",
		}})
}

func (s *Server) handleControlCommand(writer http.ResponseWriter, request *http.Request) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	if err := s.authenticateControlPanel(writer, request, runID); err != nil {
		s.writeError(writer, err)
		return
	}
	var input controlCommandRequest
	body, err := decodeRequestBody(writer, request, &input)
	if err != nil || !application.ValidRequestID(input.RequestID) || len(input.Params) == 0 || string(input.Params) == "null" {
		s.writeError(writer, application.ErrInvalidRequest)
		return
	}
	params, err := decodeControlCommandParams(input.Command, input.Params)
	if err != nil {
		s.writeError(writer, application.ErrInvalidRequest)
		return
	}
	if input.TargetAuth != nil && (input.TargetAuth.Username == "" || len(input.TargetAuth.Username) > 128 ||
		input.TargetAuth.Password == "" || len(input.TargetAuth.Password) > 1024) {
		s.writeError(writer, application.ErrInvalidRequest)
		return
	}
	redactedBody := redactControlCommandBody(body)
	agentRequest, err := s.applicationRequest(request, redactedBody, input.RequestID)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	var targetAuth *application.TargetAuth
	if input.TargetAuth != nil {
		targetAuth = &application.TargetAuth{Username: input.TargetAuth.Username, Password: input.TargetAuth.Password}
	}
	response, err := s.runs.ExecuteControlCommand(request.Context(), runID, agentRequest, application.ControlCommandInput{
		RequestID: model.CommandID(input.RequestID), Command: input.Command, Params: params, TargetAuth: targetAuth,
	})
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result, err := requireValue[application.ControlExecutionResult](response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	if response.StatusCode == http.StatusAccepted {
		writeJSON(writer, response.StatusCode, controlOperationAcceptedResponse{Clock: clockFrom(response.Clock), RequestID: input.RequestID,
			Command: input.Command, OperationID: string(result.OperationID), Status: string(model.OperationStatusRunning),
			EstimatedCompleteAt: timePointer(result.EstimatedCompleteAt)})
		return
	}
	writeJSON(writer, response.StatusCode, controlCommandResponse{Clock: clockFrom(response.Clock), RequestID: input.RequestID,
		Command: input.Command, Result: result.Result})
}

func (s *Server) authenticateControlPanel(writer http.ResponseWriter, request *http.Request, runID string) error {
	username, password, ok := request.BasicAuth()
	if !ok {
		writer.Header().Set("WWW-Authenticate", `Basic realm="control"`)
		return application.ErrControlUnauthorized
	}
	if err := s.start.AuthenticateControlPanel(request.Context(), runID, username, password); err != nil {
		if errors.Is(err, application.ErrControlUnauthorized) {
			writer.Header().Set("WWW-Authenticate", `Basic realm="control"`)
		}
		return err
	}
	return nil
}

func decodeControlCommandParams(command string, raw json.RawMessage) (any, error) {
	var target any
	switch command {
	case "firewall.rules.list", "site.config.get", "site.stop", "site.start":
		target = &application.EmptyControlParams{}
	case "firewall.rules.upsert":
		target = &model.FirewallRule{}
	case "firewall.rules.delete":
		target = &application.FirewallDeleteParams{}
	case "server.types.list":
		target = &application.ServerTypesListParams{}
	case "server.create":
		target = &application.ServerCreateParams{}
	case "server.inspect", "server.delete", "disk.usage", "disk.cleanup":
		target = &application.ServerIDParams{}
	case "database.create":
		target = &application.DatabaseCreateParams{}
	case "database.inspect", "database.backup":
		target = &application.DatabaseIDParams{}
	case "database.backups.list":
		target = &application.DatabaseBackupsListParams{}
	case "database.restore":
		target = &application.DatabaseRestoreParams{}
	case "site.database.set":
		target = &application.SiteDatabaseSetParams{}
	default:
		return nil, application.ErrInvalidRequest
	}
	if err := decodeStrictJSON(raw, target); err != nil {
		return nil, err
	}
	if _, empty := target.(*application.EmptyControlParams); empty {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || len(object) != 0 {
			return nil, application.ErrInvalidRequest
		}
	}
	value := dereferenceControlParams(target)
	if !validControlCommandParams(value) {
		return nil, application.ErrInvalidRequest
	}
	return value, nil
}

func validControlCommandParams(value any) bool {
	validName := func(name string) bool {
		trimmed := strings.TrimSpace(name)
		return trimmed != "" && len(trimmed) <= 128
	}
	switch value := value.(type) {
	case application.EmptyControlParams:
		return true
	case model.FirewallRule:
		return true // canonical CIDR, match and expiry rules are checked by the domain.
	case application.FirewallDeleteParams:
		return application.ValidResourceID(string(value.RuleID))
	case application.ServerTypesListParams:
		return value.Role == "" || value.Role == model.ServerRoleBackend || value.Role == model.ServerRoleDatabase
	case application.ServerCreateParams:
		return validName(value.Name) && (value.Role == model.ServerRoleBackend || value.Role == model.ServerRoleDatabase) &&
			application.ValidResourceID(string(value.InstanceType))
	case application.ServerIDParams:
		return application.ValidResourceID(string(value.ServerID))
	case application.DatabaseCreateParams:
		return application.ValidResourceID(string(value.ServerID)) && validName(value.Name)
	case application.DatabaseIDParams:
		return application.ValidResourceID(string(value.DatabaseID))
	case application.DatabaseBackupsListParams:
		return value.DatabaseID == "" || application.ValidResourceID(string(value.DatabaseID))
	case application.DatabaseRestoreParams:
		return application.ValidResourceID(string(value.DatabaseID)) && application.ValidResourceID(string(value.BackupID))
	case application.SiteDatabaseSetParams:
		return application.ValidResourceID(string(value.DatabaseID)) && application.ValidResourceID(string(value.ExpectedCurrentDatabaseID))
	default:
		return false
	}
}

func dereferenceControlParams(value any) any {
	switch value := value.(type) {
	case *application.EmptyControlParams:
		return *value
	case *model.FirewallRule:
		return *value
	case *application.FirewallDeleteParams:
		return *value
	case *application.ServerTypesListParams:
		return *value
	case *application.ServerCreateParams:
		return *value
	case *application.ServerIDParams:
		return *value
	case *application.DatabaseCreateParams:
		return *value
	case *application.DatabaseIDParams:
		return *value
	case *application.DatabaseBackupsListParams:
		return *value
	case *application.DatabaseRestoreParams:
		return *value
	case *application.SiteDatabaseSetParams:
		return *value
	default:
		return value
	}
}

func redactControlCommandBody(body []byte) []byte {
	var object map[string]any
	if json.Unmarshal(body, &object) != nil {
		return nil
	}
	if auth, ok := object["target_auth"].(map[string]any); ok {
		if _, exists := auth["username"]; exists {
			auth["username"] = "[REDACTED]"
		}
		if _, exists := auth["password"]; exists {
			auth["password"] = "[REDACTED]"
		}
	}
	redacted, _ := json.Marshal(object)
	return redacted
}

func (s *Server) handleControlCommands(writer http.ResponseWriter, request *http.Request) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	if err := s.authenticateControlPanel(writer, request, runID); err != nil {
		s.writeError(writer, err)
		return
	}
	agentRequest, err := s.applicationRequest(request, nil, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := s.runs.ControlClock(request.Context(), runID, agentRequest)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, controlCommandsResponse{Clock: clockFrom(response.Clock), Commands: controlCommandCatalog()})
}

func (s *Server) handleServerCredential(writer http.ResponseWriter, request *http.Request) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	if err := s.authenticateControlPanel(writer, request, runID); err != nil {
		s.writeError(writer, err)
		return
	}
	credentialID := request.PathValue("credential_id")
	if !application.ValidResourceID(credentialID) {
		s.writeError(writer, application.ErrInvalidRequest)
		return
	}
	agentRequest, err := s.applicationRequest(request, nil, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := s.runs.ServerCredential(request.Context(), runID, agentRequest, model.CredentialID(credentialID))
	if err != nil {
		s.writeError(writer, err)
		return
	}
	credential, err := requireValue[controlauth.ServerCredential](response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, credentialsResponse{Clock: clockFrom(response.Clock), Credential: credentialRecordResponse{
		CredentialID: credential.CredentialID, ResourceID: credential.ServerID, Version: credential.Version,
		Username: credential.Username, Password: credential.Password, ValidFrom: credential.ValidFrom, ExpiresAt: credential.ExpiresAt,
	}})
}

func (s *Server) handleOverview(writer http.ResponseWriter, request *http.Request) {
	s.handleRunRead(writer, request, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		return s.runs.Overview(ctx, runID, agentRequest)
	}, func(response application.ApplicationResponse) (any, error) {
		view, err := requireValue[simulation.OverviewView](response)
		if err != nil {
			return nil, err
		}
		return overviewResponse{Clock: clockFrom(response.Clock), RunID: view.RunID, Status: view.RunStatus,
			SiteStatus:  view.SiteStatus,
			ServerCount: view.ServerCount, CapacityUtilization: view.CapacityUtilization, ErrorRate: view.ErrorRate,
			Availability: availabilityResponseFrom(view.Availability), Costs: costsResponseFrom(view.Costs)}, nil
	})
}

func (s *Server) handleMetrics(writer http.ResponseWriter, request *http.Request) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	query, err := metricQuery(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	agentRequest, err := s.applicationRequest(request, nil, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := s.runs.Metrics(request.Context(), runID, agentRequest, query)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	view, err := requireValue[simulation.MetricsView](response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result := metricsResponse{Clock: clockFrom(response.Clock), Current: metricSnapshotFrom(view.Current),
		Series: make([]metricPointResponse, 0, len(view.Series))}
	if !query.From.IsZero() {
		result.Window = &timeWindowResponse{From: query.From, To: query.To}
	}
	for _, point := range view.Series {
		result.Series = append(result.Series, metricPointResponse{Timestamp: point.Timestamp, Name: point.Name,
			Page: stringPointer(point.Page), Value: point.Value})
	}
	writeJSON(writer, response.StatusCode, result)
}

func (s *Server) handleLogs(writer http.ResponseWriter, request *http.Request) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	query, err := logsQuery(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	agentRequest, err := s.applicationRequest(request, nil, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := s.runs.Logs(request.Context(), runID, agentRequest, query)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	view, err := requireValue[simulation.LogsView](response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result := logsResponse{Clock: clockFrom(response.Clock), Logs: make([]requestLogResponse, 0, len(view.Logs)), NextCursor: stringPointer(view.NextCursor)}
	for _, item := range view.Logs {
		result.Logs = append(result.Logs, requestLogResponse{Timestamp: item.Entry.Timestamp, RequestID: string(item.Entry.RequestID),
			Source: string(item.Entry.Source), VisitorID: stringPointer(item.Entry.VisitorID), Page: string(item.Entry.Page),
			ProductID: stringPointer(item.Entry.ProductID), SourceIP: item.Entry.SourceIP, UserAgent: item.Entry.UserAgent,
			RegionCode: string(item.Entry.RegionCode), FirewallRuleID: stringPointer(item.Entry.FirewallRuleID), Status: item.Entry.StatusCode,
			LatencyMS: durationMilliseconds(item.Latency), LoadUnits: item.LoadUnits, ServerID: stringPointer(item.ServerID),
			Error: stringPointer(item.Entry.ErrorCode), Message: stringPointer(item.Entry.Message)})
	}
	writeJSON(writer, response.StatusCode, result)
}

func (s *Server) handleResources(writer http.ResponseWriter, request *http.Request) {
	s.handleRunRead(writer, request, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		return s.runs.Resources(ctx, runID, agentRequest)
	}, func(response application.ApplicationResponse) (any, error) {
		view, err := requireValue[simulation.ResourcesView](response)
		if err != nil {
			return nil, err
		}
		result := resourcesResponse{Clock: clockFrom(response.Clock),
			ActiveInstances: view.ActiveInstances, TotalCapacityUnits: view.TotalCapacityUnits,
			UsedLoadUnits: view.UsedLoadUnits, TotalCostPerHourMinor: view.TotalCostPerHourMinor,
			Servers: make([]serverResourceResponse, 0, len(view.Servers))}
		for _, server := range view.Servers {
			result.Servers = append(result.Servers, serverResourceResponse{ServerID: string(server.ServerID), Status: string(server.Status),
				Name: server.Name, Role: string(server.Role), InstanceType: string(server.InstanceType),
				CapacityUnits: server.CapacityUnits, UsedLoadUnits: server.UsedLoadUnits, CostPerHourMinor: server.CostPerHourMinor,
				Disk: diskUsageResponseFrom(server.Disk), DatabaseIDs: databaseIDStrings(server.DatabaseIDs), CredentialID: string(server.CredentialID)})
		}
		return result, nil
	})
}

func diskUsageResponseFrom(value simulation.DiskUsageView) diskUsageResponse {
	return diskUsageResponse{ServerID: string(value.ServerID), TotalBytes: value.TotalBytes, SystemBytes: value.SystemBytes,
		DatabaseBytes: value.DatabaseBytes, LogsBytes: value.LogsBytes, UsedBytes: value.UsedBytes,
		FreeBytes: value.FreeBytes, CleanableBytes: value.CleanableBytes}
}

func databaseIDStrings(values []model.DatabaseID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func (s *Server) handleOperation(writer http.ResponseWriter, request *http.Request) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	operationID := request.PathValue("operation_id")
	if !operationIDPattern.MatchString(operationID) {
		s.writeError(writer, application.ErrInvalidRequest)
		return
	}
	agentRequest, err := s.applicationRequest(request, nil, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := s.runs.EnrichedOperation(request.Context(), runID, agentRequest, model.OperationID(operationID))
	if err != nil {
		s.writeError(writer, err)
		return
	}
	view, err := requireValue[application.ControlOperationView](response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	operation := view.Operation
	result := operationResponse{Clock: clockFrom(response.Clock), OperationID: string(operation.OperationID), Type: string(operation.Kind),
		Command: view.Command, RequestID: string(view.RequestID), Status: string(operation.Status), Progress: operation.Progress,
		SubmittedAt: operation.SubmittedAt, StartedAt: timePointer(operation.StartedAt), CompletedAt: timePointer(operation.CompletedAt), Result: view.Result}
	if len(view.Result) != 0 && operation.Status == model.OperationStatusSucceeded {
		result.Result = controlCommandResponse{Clock: clockFrom(response.Clock), RequestID: string(view.RequestID),
			Command: view.Command, Result: view.Result}
	} else {
		result.Result = nil
	}
	if operation.ErrorCode != "" {
		result.Error = &errorResponse{Error: operation.ErrorCode, Message: operation.Message}
	}
	writeJSON(writer, response.StatusCode, result)
}

func (s *Server) handleProbe(writer http.ResponseWriter, request *http.Request) {
	var input probeRequest
	s.handleRunCommand(writer, request, &input, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		return s.runs.Probe(ctx, runID, agentRequest, model.PageType(input.Page), model.ProductID(input.ProductID))
	}, inputRequestID(&input), func(response application.ApplicationResponse) (any, error) {
		view, err := requireValue[application.ProbeResult](response)
		if err != nil {
			return nil, err
		}
		return probeResponse{Clock: clockFrom(response.Clock), RequestID: input.RequestID, Page: input.Page,
			ProductID: stringPointer(input.ProductID), SourceIP: view.SourceIP, UserAgent: view.UserAgent,
			RegionCode: string(view.RegionCode), FirewallRuleID: stringPointer(view.FirewallRuleID),
			Status: view.StatusCode, LatencyMS: durationMilliseconds(view.Latency),
			LoadUnits: view.LoadUnits, Error: stringPointer(view.ErrorCode), Message: stringPointer(view.Message)}, nil
	})
}

func (s *Server) handleInbox(writer http.ResponseWriter, request *http.Request) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	query, err := inboxQuery(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	agentRequest, err := s.applicationRequest(request, nil, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := s.runs.Inbox(request.Context(), runID, agentRequest, query)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	view, err := requireValue[simulation.InboxView](response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result := inboxResponse{Clock: clockFrom(response.Clock), Messages: make([]inboxMessageResponse, 0, len(view.Messages)),
		NextCursor: stringPointer(view.NextCursor)}
	for _, message := range view.Messages {
		result.Messages = append(result.Messages, inboxMessageResponse{MessageID: string(message.MessageID), SenderEmail: message.SenderEmail,
			SentAt: message.SentAt, Subject: message.Subject, Description: message.Description})
	}
	writeJSON(writer, response.StatusCode, result)
}

func (s *Server) handleAdvanceTime(writer http.ResponseWriter, request *http.Request) {
	var input advanceTimeRequest
	s.handleRunCommand(writer, request, &input, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		if input.DurationSeconds == nil || *input.DurationSeconds > int64((time.Duration(1<<63-1))/time.Second) {
			return application.ApplicationResponse{}, application.ErrInvalidRequest
		}
		agentRequest.RequestedAdvance = time.Duration(*input.DurationSeconds) * time.Second
		return s.runs.AdvanceTime(ctx, runID, agentRequest)
	}, inputRequestID(&input), func(response application.ApplicationResponse) (any, error) {
		view, err := requireValue[application.AdvanceTimeResult](response)
		if err != nil {
			return nil, err
		}
		return advanceTimeResponse{Clock: clockFrom(response.Clock), PreviousSimulationTime: view.PreviousSimulationTime,
			RequestedDurationSeconds: int64(view.RequestedDuration / time.Second), ProcessedEvents: view.ProcessedEvents,
			NewLogs: view.NewLogs, LogsCursor: stringPointer(view.LogsCursor)}, nil
	})
}

type runCall func(context.Context, string, application.AgentRequest) (application.ApplicationResponse, error)
type responseMapper func(application.ApplicationResponse) (any, error)

func (s *Server) handleRunRead(writer http.ResponseWriter, request *http.Request, call runCall, mapper responseMapper) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	agentRequest, err := s.applicationRequest(request, nil, "")
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := call(request.Context(), runID, agentRequest)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result, err := mapper(response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	writeJSON(writer, response.StatusCode, result)
}

func (s *Server) handleRunCommand(writer http.ResponseWriter, request *http.Request, input any, call runCall, requestID func() string, mapper responseMapper) {
	runID, err := s.runID(request)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	body, err := decodeRequestBody(writer, request, input)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	agentRequest, err := s.applicationRequest(request, body, requestID())
	if err != nil {
		s.writeError(writer, err)
		return
	}
	response, err := call(request.Context(), runID, agentRequest)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result, err := mapper(response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	writeJSON(writer, response.StatusCode, result)
}

func inputRequestID(input any) func() string {
	return func() string {
		switch value := input.(type) {
		case *probeRequest:
			return value.RequestID
		case *advanceTimeRequest:
			return value.RequestID
		default:
			return ""
		}
	}
}

func metricSnapshotFrom(view simulation.MetricSnapshotView) metricSnapshotResponse {
	result := metricSnapshotResponse{ServerCount: view.ServerCount, CapacityUnits: view.CapacityUnits,
		UsedLoadUnits: view.UsedLoadUnits, CapacityUtilization: view.CapacityUtilization,
		ActiveRequests: view.ActiveRequests, DatabaseActiveConnections: view.DatabaseActiveConnections,
		DatabaseConnectionLimit: view.DatabaseConnectionLimit, DiskTotalBytes: view.DiskTotalBytes,
		DiskSystemBytes: view.DiskSystemBytes, DiskDatabaseBytes: view.DiskDatabaseBytes,
		DiskLogsBytes: view.DiskLogsBytes, DiskFreeBytes: view.DiskFreeBytes,
		Responses200: view.Responses200, Responses403: view.Responses403,
		Responses500: view.Responses500, Responses503: view.Responses503,
		ErrorRate: view.ErrorRate, LatencyP50MS: durationMilliseconds(view.LatencyP50),
		LatencyP95MS: durationMilliseconds(view.LatencyP95), ServerCostMinor: view.ServerCostMinor,
		BackupStorageCostMinor: view.BackupStorageCostMinor, TotalCostMinor: view.TotalCostMinor,
		CurrentCostPerHourMinor: view.CurrentCostPerHourMinor, ObservedSeconds: view.ObservedDuration.Seconds(),
		AvailableSeconds: view.AvailableDuration.Seconds(), DowntimeSeconds: view.DowntimeDuration.Seconds(), UptimeRatio: view.UptimeRatio,
		ByPage: make([]pageMetricsResponse, 0, len(view.ByPage))}
	for _, page := range view.ByPage {
		result.ByPage = append(result.ByPage, pageMetricsResponse{Page: string(page.Page), ActiveRequests: page.ActiveRequests,
			UsedLoadUnits: page.UsedLoadUnits, Responses200: page.Responses200, Responses403: page.Responses403,
			Responses500: page.Responses500, Responses503: page.Responses503,
			ErrorRate: page.ErrorRate})
	}
	return result
}

func availabilityResponseFrom(view simulation.AvailabilityView) availabilityResponse {
	return availabilityResponse{UptimeTarget: view.UptimeTarget, ObservedSeconds: view.ObservedDuration.Seconds(),
		AvailableSeconds: view.AvailableDuration.Seconds(), DowntimeSeconds: view.DowntimeDuration.Seconds(),
		UptimeRatio: view.UptimeRatio, SLOPassed: view.SLOPassed}
}

func durationMilliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func publicRunStatus(state simulation.State) string {
	return string(state.Status)
}
