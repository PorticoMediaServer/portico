package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

func TestAdminLogEventsResumeResetAndLifetime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	d, viewer := logoutHTTPFixture(t)
	logs := servicelog.New(servicelog.Options{Capacity: 3})
	defer logs.Close()
	for i := 0; i < 4; i++ {
		logs.Record("info", "server", "entry")
	}
	streams := &adminLogStreams{lifetime: time.Second}
	h := d.adminLogEvents(logs, streams)
	for _, tc := range []struct {
		after int64
		reset bool
		ids   []int64
	}{{2, false, []int64{3, 4}}, {0, true, []int64{1, 2, 3, 4}}} {
		r := httptest.NewRequest("GET", "/v1/admin/logs/events", nil)
		r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
		r.Header.Set("Last-Event-ID", logs.EventID(tc.after))
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		w := httptest.NewRecorder()
		h(w, r)
		if ctx.Err() != nil {
			t.Fatal("stream lifetime did not close connection")
		}
		if w.Code != 200 || strings.Contains(w.Body.String(), "event: reset") != tc.reset {
			t.Fatal(w.Code, w.Body.String())
		}
		previous := -1
		for _, id := range tc.ids {
			at := strings.Index(w.Body.String(), "id: "+logs.EventID(id)+"\n")
			if at <= previous {
				t.Fatal("nonmonotonic/missing ID", id, w.Body.String())
			}
			previous = at
		}
	}
}

func TestAdminLogEventsPerViewerLimit(t *testing.T) {
	d, viewer := logoutHTTPFixture(t)
	logs := servicelog.New(servicelog.Options{})
	defer logs.Close()
	streams := &adminLogStreams{lifetime: time.Second}
	key := viewer.Viewer.Authority + ":" + viewer.Viewer.AccountID + ":" + viewer.Viewer.ProfileID
	if !streams.admit(key) || !streams.admit(key) {
		t.Fatal("first two denied")
	}
	r := httptest.NewRequest("GET", "/v1/admin/logs/events", nil)
	r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	w := httptest.NewRecorder()
	d.adminLogEvents(logs, streams)(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	streams.release(key)
	w = httptest.NewRecorder()
	d.adminLogEvents(logs, streams)(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	streams.release(key)
}
