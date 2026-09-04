package playerui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
)

//go:embed static/*
var staticFiles embed.FS

type queryService interface {
	Runs(context.Context, string, int, int) ([]application.DebugRunSummary, error)
}

type Server struct {
	query queryService
	mux   *http.ServeMux
}

func New(query queryService) *Server {
	server := &Server{query: query, mux: http.NewServeMux()}
	assets, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	server.mux.Handle("GET /ui/assets/", http.StripPrefix("/ui/assets/", cacheAssets(http.FileServer(http.FS(assets)))))
	server.mux.HandleFunc("GET /ui/api/runs", server.handleRuns)
	server.mux.HandleFunc("GET /ui", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/ui/", http.StatusSeeOther)
	})
	server.mux.HandleFunc("GET /ui/", server.handleApp)
	return server
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mux.ServeHTTP(writer, request)
}

type runSummaryResponse struct {
	RunID             string    `json:"run_id"`
	Seed              int64     `json:"seed"`
	Status            string    `json:"status"`
	SiteStatus        string    `json:"site_status"`
	SimulationTime    time.Time `json:"simulation_time"`
	SimulationEndsAt  time.Time `json:"simulation_ends_at"`
	CreatedAt         time.Time `json:"created_at"`
	TotalCostMinor    int64     `json:"total_cost_minor"`
	Currency          string    `json:"currency"`
	UptimeRatio       *float64  `json:"uptime_ratio"`
	SLOPassed         *bool     `json:"slo_passed"`
	EventCount        uint64    `json:"event_count"`
	AgentRequestCount int       `json:"agent_request_count"`
	LoadError         string    `json:"load_error,omitempty"`
}

type runsResponse struct {
	Runs       []runSummaryResponse `json:"runs"`
	NextOffset *int                 `json:"next_offset"`
}

func (s *Server) handleRuns(writer http.ResponseWriter, request *http.Request) {
	limit, err := positiveInt(request.URL.Query().Get("limit"), 50)
	if err != nil || limit > 100 {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	offset, err := nonNegativeInt(request.URL.Query().Get("offset"), 0)
	if err != nil {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	summaries, err := s.query.Runs(request.Context(), "human-ui", limit, offset)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, application.ErrInvalidRequest) {
			status = http.StatusBadRequest
		}
		http.Error(writer, http.StatusText(status), status)
		return
	}
	result := runsResponse{Runs: make([]runSummaryResponse, 0, len(summaries))}
	for _, summary := range summaries {
		result.Runs = append(result.Runs, runSummaryResponse{
			RunID: summary.Run.RunID, Seed: summary.Seed, Status: summary.Overview.RunStatus,
			SiteStatus: summary.Overview.SiteStatus, SimulationTime: summary.Overview.SimulationTime,
			SimulationEndsAt: summary.Overview.SimulationEndsAt, CreatedAt: summary.Run.CreatedAt,
			TotalCostMinor: summary.Overview.Costs.TotalCostMinor, Currency: summary.Overview.Costs.Currency,
			UptimeRatio: summary.Overview.Availability.UptimeRatio, SLOPassed: summary.Overview.Availability.SLOPassed,
			EventCount: summary.EventCount, AgentRequestCount: summary.AgentRequestCount, LoadError: summary.LoadError,
		})
	}
	if len(summaries) == limit {
		next := offset + limit
		result.NextOffset = &next
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(result)
}

func (s *Server) handleApp(writer http.ResponseWriter, request *http.Request) {
	content, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	_, _ = writer.Write(content)
}

func cacheAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "public, max-age=300")
		next.ServeHTTP(writer, request)
	})
}

func positiveInt(value string, fallback int) (int, error) {
	parsed, err := nonNegativeInt(value, fallback)
	if err != nil || parsed < 1 {
		return 0, application.ErrInvalidRequest
	}
	return parsed, nil
}

func nonNegativeInt(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, application.ErrInvalidRequest
	}
	return parsed, nil
}
