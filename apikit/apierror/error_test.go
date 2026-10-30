package apierror

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageLintAndSafeWriter(t *testing.T) {
	for _, message := range []string{"This was cancelled.", "The SQL mutex failed.", "A message\nwith lines.", " "} {
		if LintMessage(message) == nil {
			t.Errorf("accepted %q", message)
		}
	}
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{errors.New("database-password-secret"), &Error{Code: "not_registered", Message: "database-password-secret"}} {
		w := httptest.NewRecorder()
		r.Write(w, "request-1", cause)
		if w.Code != 500 || strings.Contains(w.Body.String(), "database-password-secret") || !strings.Contains(w.Body.String(), "request-1") {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	r.Write(w, "request-2", &Error{Code: "request_in_progress", Message: "unsafe override", RetryAfterSeconds: 2})
	if w.Code != 409 || w.Header().Get("Retry-After") != "2" || strings.Contains(w.Body.String(), "unsafe override") || !strings.Contains(w.Body.String(), `"retryAfterMs":2000`) || strings.Contains(w.Body.String(), "retryAfterSeconds") {
		t.Fatal(w.Code, w.Body)
	}
}
