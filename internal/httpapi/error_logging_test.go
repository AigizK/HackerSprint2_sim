package httpapi

import (
	"bytes"
	"errors"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aigizk/hackersprint2-sim/internal/application"
)

func TestWriteErrorLogsUnderlyingInternalError(t *testing.T) {
	var output bytes.Buffer
	server := &Server{logger: log.New(&output, "", 0)}
	recorder := httptest.NewRecorder()

	server.writeError(recorder, errors.New("database constraint failed"))

	if recorder.Code != 500 {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	logLine := output.String()
	for _, want := range []string{"status=500", "code=INTERNAL_ERROR", "database constraint failed"} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("log %q does not contain %q", logLine, want)
		}
	}
}

func TestWriteErrorDoesNotLogExpectedClientError(t *testing.T) {
	var output bytes.Buffer
	server := &Server{logger: log.New(&output, "", 0)}

	server.writeError(httptest.NewRecorder(), application.ErrInvalidRequest)

	if output.Len() != 0 {
		t.Fatalf("client error was logged: %q", output.String())
	}
}
