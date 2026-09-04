package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const maxRequestBodyBytes int64 = 1 << 20

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

type Server struct {
	start      *application.StartRunService
	runs       *application.RunService
	mux        *http.ServeMux
	newAuditID func() (string, error)
	logger     *log.Logger
}

func New(start *application.StartRunService, runs *application.RunService) *Server {
	server := &Server{start: start, runs: runs, mux: http.NewServeMux(), newAuditID: application.NewRunID, logger: log.Default()}
	server.routes()
	return server
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mux.ServeHTTP(writer, request)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /openapi.yaml", handleOpenAPI)
	s.mux.HandleFunc("POST /v2/start", s.handleStartRun)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/overview", s.handleOverview)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/logs", s.handleLogs)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/resources", s.handleResources)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/operations/{operation_id}", s.handleOperation)
	s.mux.HandleFunc("POST /v2/runs/{run_id}/probes", s.handleProbe)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/inbox", s.handleInbox)
	s.mux.HandleFunc("POST /v2/runs/{run_id}/time/advance", s.handleAdvanceTime)
	s.mux.HandleFunc("POST /v2/runs/{run_id}/control/commands", s.handleControlCommand)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/control/commands", s.handleControlCommands)
	s.mux.HandleFunc("GET /v2/runs/{run_id}/credentials/{credential_id}", s.handleServerCredential)
}

func (s *Server) runID(request *http.Request) (string, error) {
	runID := request.PathValue("run_id")
	if !application.ValidRunID(runID) {
		return "", application.ErrInvalidRequest
	}
	return runID, nil
}

func (s *Server) applicationRequest(request *http.Request, body []byte, commandID string) (application.AgentRequest, error) {
	auditID, err := s.newAuditID()
	if err != nil {
		return application.AgentRequest{}, err
	}
	return application.AgentRequest{
		AuditID: auditID, CommandID: model.CommandID(commandID), Method: request.Method,
		Path: request.URL.RequestURI(), Headers: request.Header.Clone(), Body: append([]byte(nil), body...),
	}, nil
}

func decodeRequestBody(writer http.ResponseWriter, request *http.Request, target any) ([]byte, error) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return nil, application.ErrInvalidRequest
	}
	if err := decodeStrictJSON(body, target); err != nil {
		return nil, application.ErrInvalidRequest
	}
	return body, nil
}

func decodeStrictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return application.ErrInvalidRequest
	}
	return nil
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, application.ErrInvalidRequest
	}
	return parsed, nil
}

func parseOptionalInt(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, application.ErrInvalidRequest
	}
	return parsed, nil
}

func parseMetricNames(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				result = append(result, name)
			}
		}
	}
	return result
}

func (s *Server) writeError(writer http.ResponseWriter, err error) {
	apiError := application.ClassifyError(err)
	if apiError.Status >= http.StatusInternalServerError {
		s.logger.Printf("httpapi: status=%d code=%s error=%v", apiError.Status, apiError.Code, err)
	}
	writeJSON(writer, apiError.Status, errorResponse{Error: apiError.Code, Message: apiError.Message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func clockFrom(value application.ClockEnvelope) clockResponse {
	return clockResponse{SimulationTime: value.SimulationTime, SimulationEndsAt: value.SimulationEndsAt,
		RemainingSeconds: value.Remaining.Seconds(), RealElapsedSeconds: value.RealElapsed.Seconds(),
		AppliedAdvanceSeconds: value.AppliedAdvance.Seconds()}
}

func stringPointer[T ~string](value T) *string {
	if value == "" {
		return nil
	}
	converted := string(value)
	return &converted
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func requireValue[T any](response application.ApplicationResponse) (T, error) {
	value, ok := response.Value.(T)
	if !ok {
		return value, fmt.Errorf("unexpected application response %T", response.Value)
	}
	return value, nil
}

func validPage(value string) bool {
	page := model.PageType(value)
	return page == model.PageProductList || page == model.PageProduct
}

func metricQuery(request *http.Request) (simulation.MetricsQuery, error) {
	query := request.URL.Query()
	from, err := parseOptionalTime(query.Get("from"))
	if err != nil {
		return simulation.MetricsQuery{}, err
	}
	to, err := parseOptionalTime(query.Get("to"))
	if err != nil {
		return simulation.MetricsQuery{}, err
	}
	step, err := parseOptionalInt(query.Get("step_seconds"), 60)
	if err != nil || step < 1 {
		return simulation.MetricsQuery{}, application.ErrInvalidRequest
	}
	page := model.PageType(query.Get("page"))
	if page != "" && !validPage(string(page)) {
		return simulation.MetricsQuery{}, application.ErrInvalidRequest
	}
	return simulation.MetricsQuery{From: from, To: to, Step: time.Duration(step) * time.Second,
		Names: parseMetricNames(query["names"]), Page: page}, nil
}

func logsQuery(request *http.Request) (simulation.LogsQuery, error) {
	query := request.URL.Query()
	from, err := parseOptionalTime(query.Get("from"))
	if err != nil {
		return simulation.LogsQuery{}, err
	}
	to, err := parseOptionalTime(query.Get("to"))
	if err != nil {
		return simulation.LogsQuery{}, err
	}
	status, err := parseOptionalInt(query.Get("status"), 0)
	if err != nil {
		return simulation.LogsQuery{}, err
	}
	limit, err := parseOptionalInt(query.Get("limit"), 100)
	if err != nil {
		return simulation.LogsQuery{}, err
	}
	page := model.PageType(query.Get("page"))
	if page != "" && !validPage(string(page)) {
		return simulation.LogsQuery{}, application.ErrInvalidRequest
	}
	var hasError *bool
	if value := query.Get("has_error"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return simulation.LogsQuery{}, application.ErrInvalidRequest
		}
		hasError = &parsed
	}
	return simulation.LogsQuery{From: from, To: to, Page: page, StatusCode: status,
		HasError: hasError, ErrorCode: model.RequestFailureCode(query.Get("error")),
		Cursor: query.Get("cursor"), Limit: limit}, nil
}

func inboxQuery(request *http.Request) (simulation.InboxQuery, error) {
	limit, err := parseOptionalInt(request.URL.Query().Get("limit"), 100)
	if err != nil || limit < 1 || limit > 1000 {
		return simulation.InboxQuery{}, application.ErrInvalidRequest
	}
	return simulation.InboxQuery{Cursor: request.URL.Query().Get("cursor"), Limit: limit}, nil
}
