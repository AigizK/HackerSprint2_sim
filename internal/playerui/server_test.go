package playerui

import (
	"bytes"
	"compress/gzip"
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
		Run: simulation.RunRecord{RunID: "1234567890abcdefghijklmn", AgentID: "sre-agent", AgentVersion: "2.1", CreatedAt: now}, Seed: 42, EventCount: 120,
		Overview: simulation.RunListOverview{RunStatus: "completed", SiteStatus: "healthy", SimulationTime: now, SimulationEndsAt: now,
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
	if response.StatusCode != http.StatusOK || query.agentID != "" || query.limit != 20 || query.offset != 3 ||
		len(history.Runs) != 1 || history.Runs[0].AgentID != "sre-agent" || history.Runs[0].AgentVersion != "2.1" ||
		history.Runs[0].Seed != 42 || history.Runs[0].UptimeRatio == nil {
		t.Fatalf("unexpected history response: status=%d query=%+v history=%+v", response.StatusCode, query, history)
	}
}

func TestPlayerUICompressesAssetsWhenClientAcceptsGzip(t *testing.T) {
	t.Parallel()
	server := New(&fakeQuery{})
	request := httptest.NewRequest(http.MethodGet, "/ui/assets/app.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "gzip" ||
		!strings.Contains(response.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("unexpected compressed response: status=%d headers=%v", response.StatusCode, response.Header)
	}
	compressed, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	uncompressed, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !strings.Contains(string(uncompressed), "loadWorkspace") || len(compressed) >= len(uncompressed) {
		t.Fatalf("invalid gzip asset: compressed=%d uncompressed=%d err=%v", len(compressed), len(uncompressed), err)
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/ui/assets/app.js", nil)
	rangeRequest.Header.Set("Accept-Encoding", "gzip")
	rangeRequest.Header.Set("Range", "bytes=0-99")
	rangeRecorder := httptest.NewRecorder()
	server.ServeHTTP(rangeRecorder, rangeRequest)
	if rangeRecorder.Code != http.StatusPartialContent || rangeRecorder.Header().Get("Content-Encoding") != "" {
		t.Fatalf("range response must remain uncompressed: status=%d headers=%v", rangeRecorder.Code, rangeRecorder.Header())
	}
}
