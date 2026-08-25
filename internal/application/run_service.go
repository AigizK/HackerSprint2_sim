package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type RunService struct{ manager *RunManager }

func NewRunService(manager *RunManager) *RunService { return &RunService{manager: manager} }

type OperationAccepted struct {
	Operation           simulation.OperationView
	EstimatedCompleteAt time.Time
}

type FixResult struct {
	Applied  bool
	BugID    model.BugID
	AttackID model.AttackID
	Message  string
}

type ProbeResult struct {
	StatusCode int
	Latency    time.Duration
	LoadUnits  int64
	ErrorCode  model.RequestFailureCode
	Message    string
}

type AdvanceTimeResult struct {
	Clock                  ClockEnvelope
	PreviousSimulationTime time.Time
	RequestedDuration      time.Duration
	ProcessedEvents        int
	NewLogs                int
	LogsCursor             string
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
			(query.StatusCode != 0 && query.StatusCode != 200 && query.StatusCode != 500) || !validOptionalPage(query.Page) {
			return ApplicationResponse{}, ErrInvalidRequest
		}
		value, err := run.Session.Projection().Logs(query)
		return ApplicationResponse{StatusCode: 200, Value: value}, err
	})
}

func validateMetricsQuery(query simulation.MetricsQuery) error {
	if query.From.IsZero() != query.To.IsZero() || (!query.From.IsZero() && (query.To.Before(query.From) || query.Step <= 0)) || !validOptionalPage(query.Page) {
		return ErrInvalidRequest
	}
	valid := map[string]bool{"server_count": true, "capacity_units": true, "used_load_units": true, "capacity_utilization": true,
		"active_requests": true, "requests_total": true, "responses_200": true, "responses_500": true, "error_rate": true,
		"latency_p50_ms": true, "latency_p95_ms": true, "successful_purchases": true, "revenue_minor": true,
		"lost_revenue_minor": true, "server_cost_minor": true}
	for _, name := range query.Names {
		if !valid[name] {
			return ErrInvalidRequest
		}
	}
	return nil
}

func validOptionalPage(page model.PageType) bool {
	return page == "" || page == model.PageProductList || page == model.PageProduct || page == model.PagePurchase
}
func (s *RunService) Resources(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	return s.observe(ctx, runID, request, false, func(projection simulation.Projection) any { return projection.Resources() })
}
func (s *RunService) Deployments(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	return s.observe(ctx, runID, request, false, func(projection simulation.Projection) any { return projection.Deployments() })
}
func (s *RunService) Economy(ctx context.Context, runID string, request AgentRequest) (ApplicationResponse, error) {
	return s.observe(ctx, runID, request, false, func(projection simulation.Projection) any { return projection.Economy() })
}
func (s *RunService) Operation(ctx context.Context, runID string, request AgentRequest, operationID model.OperationID) (ApplicationResponse, error) {
	return s.manager.Handle(ctx, runID, request, func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
		value, err := simulation.NewProjection(nil, run.State).Operation(operationID)
		return ApplicationResponse{StatusCode: 200, Value: value}, err
	})
}

func (s *RunService) ScaleBackend(ctx context.Context, runID string, request AgentRequest, desired int) (ApplicationResponse, error) {
	if desired < 0 || desired > 1000 || !ValidRequestID(string(request.CommandID)) {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	operationID := StableOperationID(runID, request.CommandID, "scale")
	request.ExpectedCommandPayload = fmt.Sprintf("scale:%s:%d", operationID, desired)
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		operationID := run.State.Commands[request.CommandID]
		if operationID == "" {
			operationID = StableOperationID(runID, request.CommandID, "scale")
			if _, err := run.Session.Execute(ctx, simulation.SetBackendDesiredInstances{CommandID: request.CommandID, OperationID: operationID, DesiredInstances: desired}); err != nil {
				return ApplicationResponse{}, err
			}
		}
		state := run.Session.State()
		operation, err := simulation.NewProjection(nil, state).Operation(operationID)
		if err != nil {
			return ApplicationResponse{}, err
		}
		status := 202
		if operation.Status == model.OperationStatusSucceeded || run.Duplicate {
			status = 200
		}
		return ApplicationResponse{StatusCode: status, Value: OperationAccepted{Operation: operation, EstimatedCompleteAt: latestScaleReadyAt(state, operationID)}}, nil
	})
}

func (s *RunService) ApplyFix(ctx context.Context, runID string, request AgentRequest, message string) (ApplicationResponse, error) {
	if !ValidRequestID(string(request.CommandID)) || len(message) == 0 || len(message) > 4096 {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	request.ExpectedCommandPayload = "fix:" + message
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		if !run.Duplicate {
			if _, err := run.Session.Execute(ctx, simulation.ApplyFix{CommandID: request.CommandID, Message: message}); err != nil {
				return ApplicationResponse{}, err
			}
		}
		fix := run.Session.State().Fixes[request.CommandID]
		return ApplicationResponse{StatusCode: 200, Value: FixResult{Applied: fix.Status == model.FixAccepted, BugID: fix.BugID, AttackID: fix.AttackID, Message: string(fix.Status)}}, nil
	})
}

func (s *RunService) StartDeployment(ctx context.Context, runID string, request AgentRequest, deploymentID model.DeploymentID) (ApplicationResponse, error) {
	if !ValidRequestID(string(request.CommandID)) || deploymentID == "" || len(deploymentID) > 128 {
		return ApplicationResponse{}, ErrInvalidRequest
	}
	operationID := StableOperationID(runID, request.CommandID, "deployment")
	request.ExpectedCommandPayload = fmt.Sprintf("deployment:%s:%s", deploymentID, operationID)
	return s.manager.Handle(ctx, runID, request, func(ctx context.Context, run *RunContext) (ApplicationResponse, error) {
		operationID := run.State.Commands[request.CommandID]
		if operationID == "" {
			operationID = StableOperationID(runID, request.CommandID, "deployment")
			if _, err := run.Session.Execute(ctx, simulation.StartDeployment{CommandID: request.CommandID, DeploymentID: deploymentID, OperationID: operationID}); err != nil {
				return ApplicationResponse{}, err
			}
		}
		state := run.Session.State()
		operation, err := simulation.NewProjection(nil, state).Operation(operationID)
		if err != nil {
			return ApplicationResponse{}, err
		}
		status := 202
		if run.Duplicate {
			status = 200
		}
		return ApplicationResponse{StatusCode: status, Value: OperationAccepted{Operation: operation, EstimatedCompleteAt: state.Deployments[deploymentID].ExpectedCompletionAt}}, nil
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
			case events.PageRequestCompleted:
				result.StatusCode, result.Latency, result.ErrorCode, result.Message = event.StatusCode, event.Latency, event.ErrorCode, event.Message
			case events.PageRequestRejected:
				result.StatusCode, result.ErrorCode, result.Message = event.StatusCode, event.ErrorCode, event.Message
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
	return s.manager.Handle(ctx, runID, request, func(_ context.Context, run *RunContext) (ApplicationResponse, error) {
		return ApplicationResponse{StatusCode: 200, Value: AdvanceTimeResult{
			Clock: run.Clock, PreviousSimulationTime: run.PreviousSimulationTime, RequestedDuration: run.RequestedAdvance,
			ProcessedEvents: run.ProcessedEvents, NewLogs: run.NewLogs, LogsCursor: run.LogsCursor,
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

func StableOperationID(runID string, commandID model.CommandID, kind string) model.OperationID {
	digest := sha256.Sum256([]byte(runID + "\x00" + string(commandID) + "\x00" + kind))
	result := make([]byte, 24)
	for index := range result {
		result[index] = runIDAlphabet[int(digest[index])%len(runIDAlphabet)]
	}
	return model.OperationID(result)
}

func latestScaleReadyAt(state simulation.State, operationID model.OperationID) time.Time {
	var latest time.Time
	for _, server := range state.Servers {
		if server.OperationID == operationID && server.ReadyAt.After(latest) {
			latest = server.ReadyAt
		}
	}
	return latest
}
