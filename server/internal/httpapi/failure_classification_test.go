package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

func TestFailureDistinguishesRequestValidationFromUnknownFaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"input", requestinput.New("invalid value"), 400, "invalid_request"},
		{"wrapped input", fmt.Errorf("operation: %w", requestinput.New("invalid value")), 400, "invalid_request"},
		{"query", catalog.ErrAdminQuery, 400, "invalid_request"},
		{"unknown", errors.New("private internal cause"), 500, "internal"},
		{"internal decoder", errors.New("invalid stored provider document"), 500, "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			failure(w, tc.err)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), tc.err.Error()) {
				t.Fatal("cause exposed to caller")
			}
		})
	}
}

func TestFailureLogsUnknownFault(t *testing.T) {
	var output bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(prior)
	httpFailureMu.Lock()
	httpFailureWindows = map[string]*httpFailureWindow{}
	httpFailureMu.Unlock()
	w := httptest.NewRecorder()
	failure(w, errors.New("dependency failed"))
	if w.Code != 500 || !strings.Contains(output.String(), "dependency failed") {
		t.Fatalf("fault was not logged: %d %q", w.Code, output.String())
	}
}

func TestAdministrationTimeoutIsRetryable(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled} {
		w := httptest.NewRecorder()
		administrationFailure(w, err)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"administration_unavailable"`) || !strings.Contains(w.Body.String(), `"retryable":true`) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}
