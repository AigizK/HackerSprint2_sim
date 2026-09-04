package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	agentdocs "github.com/aigizk/hackersprint2-sim"
)

func TestV2StartReturnsFullGuideAndStableBasicCredentials(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	guide, err := os.ReadFile("../../COMMANDS.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(guide) != agentdocs.CommandsMarkdown() {
		t.Fatal("embedded guide differs from COMMANDS.md")
	}
	body := `{"seed":-1,"agent_id":"http-agent","agent_version":"1.0","request_id":"start-guide"}`
	var previous startRunResponse
	for _, status := range []int{http.StatusCreated, http.StatusOK} {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v2/start", strings.NewReader(body)))
		if recorder.Code != status {
			t.Fatalf("status = %d, want %d", recorder.Code, status)
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("start response must not cache credentials")
		}
		var response startRunResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.CommandsMarkdown != string(guide) {
			t.Fatal("start must return the exact full guide")
		}
		auth := response.ControlPanelAuth
		if auth.Scheme != "basic" || auth.Username == "" || auth.Password == "" {
			t.Fatal("missing Basic Auth access pair")
		}
		for _, instruction := range []string{"HTTP Basic Auth", "Authorization: Basic", "/v2/runs/" + response.RunID + "/control/commands", "/credentials/{credential_id}", "target_auth"} {
			if !strings.Contains(auth.Instructions, instruction) {
				t.Fatalf("missing authorization instruction: %s", instruction)
			}
		}
		if status == http.StatusOK && (response.RunID != previous.RunID || !reflect.DeepEqual(auth, previous.ControlPanelAuth)) {
			t.Fatal("idempotent start must preserve credentials")
		}
		previous = response
	}
	// Existing observation routes remain public and never expose panel credentials.
	for _, suffix := range []string{"overview", "resources", "inbox"} {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v2/runs/"+previous.RunID+"/"+suffix, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("unauthenticated %s = %d", suffix, recorder.Code)
		}
		if bytes.Contains(recorder.Body.Bytes(), []byte(previous.ControlPanelAuth.Password)) ||
			bytes.Contains(recorder.Body.Bytes(), []byte(previous.ControlPanelAuth.Username)) {
			t.Fatalf("%s leaked control panel access", suffix)
		}
	}
}

func TestV1RoutesAreNotAvailable(t *testing.T) {
	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodPost, "/v1/start"},
		{http.MethodGet, "/v1/runs/1234567890abcdefghijklmn/overview"},
		{http.MethodGet, "/v1/runs/1234567890abcdefghijklmn/metrics"},
		{http.MethodGet, "/v1/runs/1234567890abcdefghijklmn/logs"},
		{http.MethodGet, "/v1/runs/1234567890abcdefghijklmn/resources"},
		{http.MethodGet, "/v1/runs/1234567890abcdefghijklmn/inbox"},
		{http.MethodGet, "/v1/runs/1234567890abcdefghijklmn/operations/1234567890abcdef"},
		{http.MethodPost, "/v1/runs/1234567890abcdefghijklmn/probes"},
		{http.MethodPost, "/v1/runs/1234567890abcdefghijklmn/time/advance"},
	} {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("old route %s: %d, want 404", endpoint.path, recorder.Code)
		}
	}
}
