package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestOpenAPIEndpointReturnsEmbeddedContract(t *testing.T) {
	want, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	api, closeStorage, _ := newTestServer(t)
	defer closeStorage()
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/yaml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string(want) {
		t.Fatal("served OpenAPI contract differs from openapi.yaml")
	}
}
