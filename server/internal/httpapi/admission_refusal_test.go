package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

type refusalDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline          time.Time
	changes           []time.Time
	wroteWithBudget   bool
	flushedWithBudget bool
}

func (w *refusalDeadlineWriter) SetWriteDeadline(value time.Time) error {
	w.deadline = value
	w.changes = append(w.changes, value)
	return nil
}

func (w *refusalDeadlineWriter) Write(body []byte) (int, error) {
	w.wroteWithBudget = !w.deadline.IsZero() && time.Now().Before(w.deadline)
	return w.ResponseRecorder.Write(body)
}

func (w *refusalDeadlineWriter) Flush() {
	w.flushedWithBudget = !w.deadline.IsZero() && time.Now().Before(w.deadline)
	w.ResponseRecorder.Flush()
}

func TestAdmissionRefusalHasOnlyATemporaryWriteBudget(t *testing.T) {
	a := newAdmission()
	w := &refusalDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	a.refuse(w, httptest.NewRequest("GET", "/v1/home", nil), a.lanes[laneBrowsing])
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("overload contract changed")
	}
	if !w.wroteWithBudget || !w.flushedWithBudget || len(w.changes) != 2 || !w.changes[1].IsZero() {
		t.Fatal("refusal did not bound its write and restore the connection")
	}
	if wait := w.changes[0].Sub(start); wait < 1900*time.Millisecond || wait > 2100*time.Millisecond {
		t.Fatalf("unexpected refusal write budget %s", wait)
	}
	if !w.deadline.IsZero() {
		t.Fatal("refusal write deadline leaked into subsequent requests")
	}
}
