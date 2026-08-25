package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

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
		SimulationTime: result.State.Clock.CurrentTime, SimulationEnds: result.State.Clock.EndsAt})
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
			SiteStatus: view.SiteStatus, CurrentDeploymentID: stringPointer(view.CurrentDeploymentID),
			ServerCount: view.ServerCount, CapacityUtilization: view.CapacityUtilization, ErrorRate: view.ErrorRate,
			SuccessfulPurchases: view.SuccessfulPurchases, RevenueMinor: view.RevenueMinor,
			ServerCostMinor: view.ServerCostMinor, BalanceMinor: view.BalanceMinor}, nil
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
			ProductID: stringPointer(item.Entry.ProductID), Status: item.Entry.StatusCode,
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
		result := resourcesResponse{Clock: clockFrom(response.Clock), DesiredInstances: view.DesiredInstances,
			ActiveInstances: view.ActiveInstances, TotalCapacityUnits: view.TotalCapacityUnits,
			UsedLoadUnits: view.UsedLoadUnits, TotalCostPerHourMinor: view.TotalCostPerHourMinor,
			Servers: make([]serverResourceResponse, 0, len(view.Servers))}
		for _, server := range view.Servers {
			result.Servers = append(result.Servers, serverResourceResponse{ServerID: string(server.ServerID), Status: string(server.Status),
				CapacityUnits: server.CapacityUnits, UsedLoadUnits: server.UsedLoadUnits, CostPerHourMinor: server.CostPerHourMinor})
		}
		return result, nil
	})
}

func (s *Server) handleScaleBackend(writer http.ResponseWriter, request *http.Request) {
	var input scaleBackendRequest
	s.handleRunCommand(writer, request, &input, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		if input.DesiredInstances == nil {
			return application.ApplicationResponse{}, application.ErrInvalidRequest
		}
		return s.runs.ScaleBackend(ctx, runID, agentRequest, *input.DesiredInstances)
	}, inputRequestID(&input), operationAcceptedFrom)
}

func (s *Server) handleApplyFix(writer http.ResponseWriter, request *http.Request) {
	var input applyFixRequest
	s.handleRunCommand(writer, request, &input, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		return s.runs.ApplyFix(ctx, runID, agentRequest, input.Message)
	}, inputRequestID(&input), func(response application.ApplicationResponse) (any, error) {
		view, err := requireValue[application.FixResult](response)
		if err != nil {
			return nil, err
		}
		return applyFixResponse{Clock: clockFrom(response.Clock), Applied: view.Applied,
			FixedBug: stringPointer(view.BugID), FixedAttack: stringPointer(view.AttackID), Message: view.Message}, nil
	})
}

func (s *Server) handleDeployments(writer http.ResponseWriter, request *http.Request) {
	s.handleRunRead(writer, request, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		return s.runs.Deployments(ctx, runID, agentRequest)
	}, func(response application.ApplicationResponse) (any, error) {
		view, err := requireValue[simulation.DeploymentsView](response)
		if err != nil {
			return nil, err
		}
		result := deploymentsResponse{Clock: clockFrom(response.Clock), Deployments: make([]deploymentResponse, 0, len(view.Deployments))}
		for _, deployment := range view.Deployments {
			result.Deployments = append(result.Deployments, deploymentResponse{DeploymentID: string(deployment.DeploymentID),
				Sequence: deployment.Sequence, Name: deployment.Name, Description: deployment.Description,
				Status: string(deployment.Status), OperationID: stringPointer(deployment.OperationID)})
		}
		return result, nil
	})
}

func (s *Server) handleStartDeployment(writer http.ResponseWriter, request *http.Request) {
	var input startDeploymentRequest
	s.handleRunCommand(writer, request, &input, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		return s.runs.StartDeployment(ctx, runID, agentRequest, model.DeploymentID(input.DeploymentID))
	}, inputRequestID(&input), operationAcceptedFrom)
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
	response, err := s.runs.Operation(request.Context(), runID, agentRequest, model.OperationID(operationID))
	if err != nil {
		s.writeError(writer, err)
		return
	}
	view, err := requireValue[simulation.OperationView](response)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	result := operationResponse{Clock: clockFrom(response.Clock), OperationID: string(view.OperationID), Type: string(view.Kind),
		Status: string(view.Status), Progress: view.Progress, SubmittedAt: view.SubmittedAt,
		StartedAt: timePointer(view.StartedAt), CompletedAt: timePointer(view.CompletedAt)}
	if view.ErrorCode != "" {
		result.Error = &errorResponse{Error: view.ErrorCode, Message: view.Message}
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
			ProductID: stringPointer(input.ProductID), Status: view.StatusCode, LatencyMS: durationMilliseconds(view.Latency),
			LoadUnits: view.LoadUnits, Error: stringPointer(view.ErrorCode), Message: stringPointer(view.Message)}, nil
	})
}

func (s *Server) handleEconomy(writer http.ResponseWriter, request *http.Request) {
	s.handleRunRead(writer, request, func(ctx context.Context, runID string, agentRequest application.AgentRequest) (application.ApplicationResponse, error) {
		return s.runs.Economy(ctx, runID, agentRequest)
	}, func(response application.ApplicationResponse) (any, error) {
		view, err := requireValue[simulation.EconomyView](response)
		if err != nil {
			return nil, err
		}
		return economyResponse{Clock: clockFrom(response.Clock), Currency: view.Currency,
			SuccessfulPurchases: view.SuccessfulPurchases, LostPurchases: view.LostPurchases,
			RevenueMinor: view.RevenueMinor, LostRevenueMinor: view.LostRevenueMinor,
			ServerCostMinor: view.ServerCostMinor, DeploymentCostMinor: view.DeploymentCostMinor,
			BalanceMinor: view.BalanceMinor}, nil
	})
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
		case *scaleBackendRequest:
			return value.RequestID
		case *applyFixRequest:
			return value.RequestID
		case *startDeploymentRequest:
			return value.RequestID
		case *probeRequest:
			return value.RequestID
		case *advanceTimeRequest:
			return value.RequestID
		default:
			return ""
		}
	}
}

func operationAcceptedFrom(response application.ApplicationResponse) (any, error) {
	view, err := requireValue[application.OperationAccepted](response)
	if err != nil {
		return nil, err
	}
	return operationAcceptedResponse{Clock: clockFrom(response.Clock), OperationID: string(view.Operation.OperationID),
		Status: string(view.Operation.Status), EstimatedCompleteAt: timePointer(view.EstimatedCompleteAt)}, nil
}

func metricSnapshotFrom(view simulation.MetricSnapshotView) metricSnapshotResponse {
	result := metricSnapshotResponse{ServerCount: view.ServerCount, CapacityUnits: view.CapacityUnits,
		UsedLoadUnits: view.UsedLoadUnits, CapacityUtilization: view.CapacityUtilization,
		ActiveRequests: view.ActiveRequests, Responses200: view.Responses200, Responses500: view.Responses500,
		ErrorRate: view.ErrorRate, LatencyP50MS: durationMilliseconds(view.LatencyP50),
		LatencyP95MS: durationMilliseconds(view.LatencyP95), SuccessfulPurchases: view.SuccessfulPurchases,
		RevenueMinor: view.RevenueMinor, LostRevenueMinor: view.LostRevenueMinor, ServerCostMinor: view.ServerCostMinor,
		ByPage: make([]pageMetricsResponse, 0, len(view.ByPage))}
	for _, page := range view.ByPage {
		result.ByPage = append(result.ByPage, pageMetricsResponse{Page: string(page.Page), ActiveRequests: page.ActiveRequests,
			UsedLoadUnits: page.UsedLoadUnits, Responses200: page.Responses200, Responses500: page.Responses500,
			ErrorRate: page.ErrorRate})
	}
	return result
}

func durationMilliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func publicRunStatus(state simulation.State) string {
	if state.Status == simulation.RunCompleted && state.EndReason == "negative_balance" {
		return "failed"
	}
	return string(state.Status)
}

func invalidApplicationResponse(name string) error {
	return fmt.Errorf("invalid %s application response", name)
}
