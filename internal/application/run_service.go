package application

import (
	"context"
	"fmt"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/controlauth"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type RunService struct {
	manager     *RunManager
	credentials controlauth.ServerCredentialRepository
}

func NewRunService(manager *RunManager, credentials ...controlauth.ServerCredentialRepository) *RunService {
	service := &RunService{manager: manager}
	if len(credentials) > 0 {
		service.credentials = credentials[0]
	}
	return service
}

type ProbeResult struct {
	StatusCode     int
	Latency        time.Duration
	LoadUnits      int64
	SourceIP       string
	UserAgent      string
	RegionCode     model.RegionCode
	FirewallRuleID model.FirewallRuleID
	ErrorCode      model.RequestFailureCode
	Message        string
}

type AdvanceTimeResult struct {
	Clock                  ClockEnvelope
	PreviousSimulationTime time.Time
	RequestedDuration      time.Duration
	ProcessedEvents        int
	NewLogs                int
	LogsCursor             string
	StopReason             string
}

type FirewallRuleResult struct {
	Rule     model.FirewallRule
	Revision uint64
}

func (s *RunService) FirewallRules(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	return s.observe(ctx, runID, request, false, func(projection simulation.Projection) any {
		return projection.FirewallRules()
	})
}

func (s *RunService) UpsertFirewallRule(ctx context.Context, runID string, request AgentRequest, rule model.FirewallRule) (ApplicationResponse, error) {
	if !ValidRequestID(string(request.CommandID)) {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		if _, err := run.Session.Execute(ctx, simulation.UpsertFirewallRule{CommandID: request.CommandID, Rule: rule}); err != nil {
			return ApplicationResponse{}, err
		}
		stored := run.Session.State().FirewallRules[rule.ID]
		return ApplicationResponse{StatusCode: 200, Value: FirewallRuleResult{Rule: stored.Rule, Revision: stored.Revision}}, nil
	})
}

func (s *RunService) DeleteFirewallRule(ctx context.Context, runID string, request AgentRequest, ruleID model.FirewallRuleID) (ApplicationResponse, error) {
	if !ValidRequestID(string(request.CommandID)) {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		if _, err := run.Session.Execute(ctx, simulation.DeleteFirewallRule{CommandID: request.CommandID, RuleID: ruleID}); err != nil {
			return ApplicationResponse{}, err
		}
		return ApplicationResponse{StatusCode: 200, Value: ruleID}, nil
	})
}

func (s *RunService) Overview(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	return s.observe(ctx, runID, request, true, func(projection simulation.Projection) any { return projection.Overview() })
}
func (s *RunService) Metrics(ctx context.Context, runID string, request AgentRequest, query simulation.MetricsQuery) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
		if !query.From.IsZero() && query.Step == 0 {
			query.Step = time.Minute
		}
		if err := validateMetricsQuery(query); err != nil {
			return ApplicationResponse{}, err
		}
		return ApplicationResponse{StatusCode: 200, Value: run.Session.Projection().Metrics(query)}, nil
	})
}
func (s *RunService) Logs(ctx context.Context, runID string, request AgentRequest, query simulation.LogsQuery) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
		if query.Limit == 0 {
			query.Limit = 100
		}
		if query.Limit < 1 || query.Limit > 1000 || (!query.From.IsZero() && !query.To.IsZero() && query.To.Before(query.From)) ||
			(query.StatusCode != 0 && query.StatusCode != 200 && query.StatusCode != 403 && query.StatusCode != 500 && query.StatusCode != 503) ||
			!validOptionalPage(query.Page) || !validOptionalRequestFailure(query.ErrorCode) ||
			(query.HasError != nil && !*query.HasError && query.ErrorCode != "") {
			return ApplicationResponse{}, ErrInvalidRequest
		}
		value, err := run.Session.Projection().Logs(query)
		return ApplicationResponse{StatusCode: 200, Value: value}, err
	})
}

func (s *RunService) Inbox(ctx context.Context, runID string, request AgentRequest, query simulation.InboxQuery) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		if s.credentials != nil {
			if err := ensureServerCredentials(ctx, s.credentials, run.Session); err != nil {
				return ApplicationResponse{}, err
			}
		}
		if query.Limit == 0 {
			query.Limit = 100
		}
		if query.Limit < 1 || query.Limit > 1000 || len(query.Cursor) > 512 {
			return ApplicationResponse{}, ErrInvalidRequest
		}
		value, err := simulation.NewProjection(nil, run.Session.State()).Inbox(query)
		return ApplicationResponse{StatusCode: 200, Value: value}, err
	})
}

func validateMetricsQuery(query simulation.MetricsQuery) error {
	if query.From.IsZero() != query.To.IsZero() || (!query.From.IsZero() && (query.To.Before(query.From) || query.Step <= 0)) || !validOptionalPage(query.Page) {
		return ErrInvalidRequest
	}
	valid := map[string]bool{"server_count": true, "capacity_units": true, "used_load_units": true, "capacity_utilization": true,
		"active_requests": true, "requests_total": true, "responses_200": true, "responses_403": true, "responses_500": true, "responses_503": true, "error_rate": true,
		"latency_p50_ms": true, "latency_p95_ms": true, "server_cost_minor": true, "backup_storage_cost_minor": true,
		"total_cost_minor": true, "current_cost_per_hour_minor": true, "observed_seconds": true,
		"available_seconds": true, "downtime_seconds": true, "uptime_ratio": true,
		"database_active_connections": true, "database_connection_limit": true, "disk_total_bytes": true,
		"disk_system_bytes": true, "disk_database_bytes": true, "disk_logs_bytes": true, "disk_free_bytes": true}
	for _, name := range query.Names {
		if !valid[name] {
			return ErrInvalidRequest
		}
	}
	return nil
}

func validOptionalPage(page model.PageType) bool {
	return page == "" || page == model.PageProductList || page == model.PageProduct
}

func validOptionalRequestFailure(code model.RequestFailureCode) bool {
	switch code {
	case "", model.FailureServerCapacityExceeded, model.FailureDBConnectionLimit, model.FailureDiskFull,
		model.FailureSiteUnavailable, model.FailureDatabaseUnavailable, model.FailureFirewallDenied:
		return true
	default:
		return false
	}
}
func (s *RunService) Resources(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	return s.observe(ctx, runID, request, false, func(projection simulation.Projection) any { return projection.Resources() })
}

func (s *RunService) ControlClock(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	return s.observe(ctx, runID, request, false, func(simulation.Projection) any { return nil })
}
func (s *RunService) Operation(ctx context.Context, runID string, request AgentRequest, operationID model.OperationID) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
		value, err := simulation.NewProjection(nil, run.State).Operation(operationID)
		return ApplicationResponse{StatusCode: 200, Value: value}, err
	})
}

func (s *RunService) Probe(ctx context.Context, runID string, request AgentRequest, page model.PageType, productID model.ProductID) (ApplicationResponse, error) {
	if !ValidRequestID(string(request.CommandID)) || !validOptionalPage(page) || page == "" ||
		(page == model.PageProductList && productID != "") || (page != model.PageProductList && productID == "") {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	request.ExpectedCommandPayload = fmt.Sprintf("probe:%s:%s", page, productID)
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		var decided []events.Event
		var err error
		if !run.Duplicate {
			decided, err = run.Session.Execute(ctx, simulation.ProbePage{RequestID: model.RequestID(request.CommandID), Page: page, ProductID: productID})
			if err != nil {
				return ApplicationResponse{}, err
			}
		} else {
			decided = run.Session.RequestEvents(model.RequestID(request.CommandID))
		}
		result := ProbeResult{}
		for _, event := range decided {
			switch event := event.(type) {
			case events.PageRequestStarted:
				result.LoadUnits = event.LoadUnits
				result.SourceIP, result.UserAgent, result.RegionCode = event.SourceIP, event.UserAgent, event.RegionCode
			case events.FirewallRequestEvaluated:
				result.FirewallRuleID = event.MatchedRuleID
			case events.PageRequestCompleted:
				result.StatusCode, result.Latency, result.ErrorCode, result.Message = event.StatusCode, event.Latency, event.ErrorCode, event.Message
				result.FirewallRuleID = event.FirewallRuleID
			case events.PageRequestRejected:
				result.StatusCode, result.ErrorCode, result.Message = event.StatusCode, event.ErrorCode, event.Message
				result.FirewallRuleID = event.FirewallRuleID
			}
		}
		return ApplicationResponse{StatusCode: 200, Value: result}, nil
	})
}

func (s *RunService) AdvanceTime(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	if !ValidRequestID(string(request.CommandID)) {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	if request.RequestedAdvance < simulation.MinExplicitAdvance {
		return ApplicationResponse{}, ErrMinimumAdvance
	}
	request.ExpectedCommandPayload = fmt.Sprintf("advance:%d", request.RequestedAdvance)
	if request.StopOnLogError {
		request.ExpectedCommandPayload += ":new-log-errors=1"
	}
	return s.manager.Handle(ctx, runID, request, func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
		stopReason := "duration_elapsed"
		if run.State.Status != simulation.RunRunning {
			stopReason = "run_completed"
		} else if request.StopOnLogError && run.NewLogErrors > 0 {
			stopReason = "log_error"
		}
		return ApplicationResponse{StatusCode: 200, Value: AdvanceTimeResult{
			Clock: run.Clock, PreviousSimulationTime: run.PreviousSimulationTime, RequestedDuration: run.RequestedAdvance,
			ProcessedEvents: run.ProcessedEvents, NewLogs: run.NewLogs, LogsCursor: run.LogsCursor, StopReason: stopReason,
		}}, nil
	})
}

func (s *RunService) observe(ctx context.Context, runID string, request AgentRequest, needsRecords bool, project func(simulation.Projection) any) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
		projection := simulation.NewProjection(nil, run.State)
		if needsRecords {
			projection = run.Session.Projection()
		}
		return ApplicationResponse{StatusCode: 200, Value: project(projection)}, nil
	})
}
