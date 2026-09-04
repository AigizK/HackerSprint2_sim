package debugui

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

const runsPerPage = 50

type queryService interface {
	Runs(context.Context, string, int, int) ([]application.DebugRunSummary, error)
	Overview(context.Context, string) (application.DebugRunOverview, error)
	Logs(context.Context, string, simulation.LogsQuery) (simulation.RunRecord, simulation.LogsView, error)
}

type Server struct {
	query    queryService
	mux      *http.ServeMux
	runs     *template.Template
	overview *template.Template
	logs     *template.Template
}

func New(query queryService) *Server {
	functions := template.FuncMap{
		"timefmt": func(value time.Time) string {
			if value.IsZero() {
				return "—"
			}
			return value.UTC().Format("2006-01-02 15:04:05 UTC")
		},
		"duration": func(value time.Duration) string { return value.Round(time.Second).String() },
		"percent":  func(value float64) string { return fmt.Sprintf("%.2f%%", value*100) },
		"minor":    formatMinor,
		"path":     url.PathEscape,
		"query":    url.QueryEscape,
		"statusClass": func(value string) string {
			switch value {
			case "running":
				return "running"
			case "completed":
				return "completed"
			default:
				return "failed"
			}
		},
	}
	server := &Server{
		query:    query,
		mux:      http.NewServeMux(),
		runs:     template.Must(template.New("runs").Funcs(functions).Parse(commonTemplate + runsTemplate)),
		overview: template.Must(template.New("overview").Funcs(functions).Parse(commonTemplate + overviewTemplate)),
		logs:     template.Must(template.New("logs").Funcs(functions).Parse(commonTemplate + logsTemplate)),
	}
	server.mux.HandleFunc("GET /debug", server.redirectRoot)
	server.mux.HandleFunc("GET /debug/", server.handleRuns)
	server.mux.HandleFunc("GET /debug/runs/{run_id}/overview", server.handleOverview)
	server.mux.HandleFunc("GET /debug/runs/{run_id}/logs", server.handleLogs)
	return server
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mux.ServeHTTP(writer, request)
}

type runsPage struct {
	Runs                         []application.DebugRunSummary
	AgentID                      string
	Page, PreviousPage, NextPage int
	HasPrevious, HasNext         bool
}

func (s *Server) redirectRoot(writer http.ResponseWriter, request *http.Request) {
	http.Redirect(writer, request, "/debug/", http.StatusSeeOther)
}

func (s *Server) handleRuns(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/debug/" {
		http.NotFound(writer, request)
		return
	}
	page, err := optionalNonNegativeInt(request.URL.Query().Get("page"))
	if err != nil {
		s.writeError(writer, err)
		return
	}
	agentID := request.URL.Query().Get("agent_id")
	runs, err := s.query.Runs(request.Context(), agentID, runsPerPage, page*runsPerPage)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	data := runsPage{Runs: runs, AgentID: agentID, Page: page, PreviousPage: page - 1, NextPage: page + 1,
		HasPrevious: page > 0, HasNext: len(runs) == runsPerPage}
	s.render(writer, s.runs, data)
}

type runPage struct {
	RunID string
	Run   simulation.RunRecord
	View  application.DebugRunOverview
}

func (s *Server) handleOverview(writer http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("run_id")
	view, err := s.query.Overview(request.Context(), runID)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	s.render(writer, s.overview, runPage{RunID: runID, Run: view.Run, View: view})
}

type logsPage struct {
	RunID     string
	Run       simulation.RunRecord
	View      simulation.LogsView
	Status    int
	NextQuery string
}

func (s *Server) handleLogs(writer http.ResponseWriter, request *http.Request) {
	status, err := optionalNonNegativeInt(request.URL.Query().Get("status"))
	if err != nil || (status != 0 && status != 200 && status != 500) {
		s.writeError(writer, application.ErrInvalidRequest)
		return
	}
	query := simulation.LogsQuery{StatusCode: status, Cursor: request.URL.Query().Get("cursor"), Limit: 200}
	runID := request.PathValue("run_id")
	run, view, err := s.query.Logs(request.Context(), runID, query)
	if err != nil {
		s.writeError(writer, err)
		return
	}
	nextQuery := ""
	if view.NextCursor != "" {
		values := url.Values{"cursor": {view.NextCursor}}
		if status != 0 {
			values.Set("status", strconv.Itoa(status))
		}
		nextQuery = values.Encode()
	}
	s.render(writer, s.logs, logsPage{RunID: runID, Run: run, View: view, Status: status, NextQuery: nextQuery})
}

func (s *Server) render(writer http.ResponseWriter, page *template.Template, data any) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if err := page.Execute(writer, data); err != nil {
		http.Error(writer, "render debug page: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) writeError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, simulation.ErrRunNotFound) {
		status = http.StatusNotFound
	} else if errors.Is(err, application.ErrInvalidRequest) {
		status = http.StatusBadRequest
	}
	http.Error(writer, http.StatusText(status), status)
}

func optionalNonNegativeInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, application.ErrInvalidRequest
	}
	return parsed, nil
}

func formatMinor(value int64) string {
	sign := ""
	if value < 0 {
		sign, value = "-", -value
	}
	return fmt.Sprintf("%s%d.%02d", sign, value/100, value%100)
}
