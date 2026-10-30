package httpapi

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFailureDoesNotDiscloseRawErrors(t *testing.T) {
	for _, detail := range []string{"secret /Users/private/db.sqlite", "sqlite constraint: sensitive_column", "unexpected key material"} {
		w := httptest.NewRecorder()
		failure(w, errors.New(detail))
		if strings.Contains(w.Body.String(), detail) || !strings.Contains(w.Body.String(), "The request could not be completed.") {
			t.Fatalf("unsafe response: %s", w.Body)
		}
	}
}

func TestLongPollRoutesUseRealtimeLane(t *testing.T) {
	for _, route := range []string{"GET /v1/events"} {
		if routeLanes[route] != laneRealtime {
			t.Errorf("%s has lane %s", route, routeLanes[route])
		}
	}
}

func TestHomeLimitClamps(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/home?limit=1000000", nil)
	n, err := homeQueryLimit(r)
	if err != nil || n <= 0 || n >= 1000000 {
		t.Fatalf("limit %d: %v", n, err)
	}
	r = httptest.NewRequest("GET", "/v1/home?limit=-1", nil)
	if _, err := homeQueryLimit(r); err == nil {
		t.Fatal("negative limit accepted")
	}
}
