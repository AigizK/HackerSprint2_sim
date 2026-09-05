package debugui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const testRunID = "1234567890abcdefghijklmn"

type fakeQuery struct {
	run      simulation.RunRecord
	overview simulation.OverviewView
}

func (f fakeQuery) Runs(context.Context, string, int, int) ([]application.DebugRunSummary, error) {
	return []application.DebugRunSummary{{Run: f.run, Seed: 1, EventCount: 42, AgentRequestCount: 7,
		Overview: simulation.RunListOverview{RunStatus: f.overview.RunStatus, SiteStatus: f.overview.SiteStatus,
			SimulationTime: f.overview.SimulationTime, SimulationEndsAt: f.overview.SimulationEndsAt,
			Costs: f.overview.Costs, Availability: f.overview.Availability}}}, nil
}
func (f fakeQuery) Overview(context.Context, string) (application.DebugRunOverview, error) {
	return application.DebugRunOverview{Run: f.run, Overview: f.overview, ActualAgentRequestCount: 8, ActualWallClockRealTime: 3 * time.Second}, nil
}
func (f fakeQuery) Logs(context.Context, string, simulation.LogsQuery) (simulation.RunRecord, simulation.LogsView, error) {
	view := simulation.RequestLogView{Entry: logs.Entry{
		Timestamp: f.overview.SimulationTime, RequestID: "request-1", Source: model.RequestSourceVisitor,
		Page: model.PageProductList, StatusCode: 500, ErrorCode: model.FailureServerCapacityExceeded,
	}}
	return f.run, simulation.LogsView{Logs: []simulation.RequestLogView{view}}, nil
}

func TestDebugPagesLinkRunProjections(t *testing.T) {
	t.Parallel()
	now := time.Date(2032, 4, 1, 12, 0, 0, 0, time.UTC)
	query := fakeQuery{
		run: simulation.RunRecord{RunID: testRunID, AgentID: "agent-one", AgentVersion: "v1", CreatedAt: now},
		overview: simulation.OverviewView{RunID: testRunID, RunStatus: "completed", SiteStatus: "degraded", SimulationTime: now,
			SimulationEndsAt: now, ServerCount: 1, RequestsTotal: 100, ErrorRate: .04, VisitorRequestsTotal: 100, VisitorErrorRate: .04},
	}
	server := httptest.NewServer(New(query))
	t.Cleanup(server.Close)

	checks := map[string][]string{
		"/debug/":                                {testRunID, "agent-one", "infrastructure cost", "/overview"},
		"/debug/runs/" + testRunID + "/overview": {"Run status", "degraded", "Infrastructure cost", "Uptime is calculated"},
		"/debug/runs/" + testRunID + "/logs":     {"SERVER_CAPACITY_EXCEEDED", "request-1"},
	}
	for path, fragments := range checks {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status=%d err=%v body=%s", path, response.StatusCode, readErr, body)
		}
		for _, fragment := range fragments {
			if !strings.Contains(string(body), fragment) {
				t.Errorf("GET %s does not contain %q", path, fragment)
			}
		}
	}
}
