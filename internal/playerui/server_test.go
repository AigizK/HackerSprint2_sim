package playerui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

type fakeQuery struct {
	agentID string
	limit   int
	offset  int
}

func (f *fakeQuery) Runs(_ context.Context, agentID string, limit, offset int) ([]application.DebugRunSummary, error) {
	f.agentID, f.limit, f.offset = agentID, limit, offset
	now := time.Date(2032, 4, 1, 12, 0, 0, 0, time.UTC)
	uptime, passed := .997, true
	return []application.DebugRunSummary{{
		Run: simulation.RunRecord{RunID: "1234567890abcdefghijklmn", AgentID: agentID, CreatedAt: now}, Seed: 42, EventCount: 120,
		Overview: simulation.OverviewView{RunStatus: "completed", SiteStatus: "healthy", SimulationTime: now, SimulationEndsAt: now,
			Availability: simulation.AvailabilityView{UptimeRatio: &uptime, SLOPassed: &passed}, Costs: simulation.CostsView{Currency: "USD", TotalCostMinor: 1234}},
	}}, nil
}

func TestPlayerUIRoutesAndRunHistory(t *testing.T) {
	t.Parallel()
	query := &fakeQuery{}
	server := httptest.NewServer(New(query))
	t.Cleanup(server.Close)

	for _, path := range []string{"/ui/", "/ui/history", "/ui/runs/1234567890abcdefghijklmn", "/ui/runs/1234567890abcdefghijklmn/logs"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Simulation Operations") {
			t.Fatalf("GET %s: status=%d err=%v body=%s", path, response.StatusCode, readErr, body)
		}
	}

	response, err := http.Get(server.URL + "/ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	asset, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(asset), "loadWorkspace") {
		t.Fatalf("asset status=%d body=%s", response.StatusCode, asset)
	}

	response, err = http.Get(server.URL + "/ui/api/runs?limit=20&offset=3")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var history runsResponse
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || query.agentID != "human-ui" || query.limit != 20 || query.offset != 3 ||
		len(history.Runs) != 1 || history.Runs[0].Seed != 42 || history.Runs[0].UptimeRatio == nil {
		t.Fatalf("unexpected history response: status=%d query=%+v history=%+v", response.StatusCode, query, history)
	}
}
