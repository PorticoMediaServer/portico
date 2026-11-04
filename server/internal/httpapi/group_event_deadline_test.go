package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

type groupStreamConnector struct {
	driver.Connector
	connect func(context.Context) (driver.Conn, error)
}

func (c groupStreamConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.connect(ctx)
}

func TestGroupStreamAuthenticationHonorsRequestEnd(t *testing.T) {
	for _, kind := range []string{"canceled", "deadline", "live-error", "live-canceled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "deadline" {
				var end context.CancelFunc
				ctx, end = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer end()
			}
			var stream http.HandlerFunc
			viewer := func(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
				if kind == "canceled" {
					cancel()
				}
				err := context.DeadlineExceeded
				if kind == "live-canceled" {
					err = context.Canceled
				}
				socialFailure(w, err)
				return identity.Principal{}, false
			}
			Dependencies{}.groupRoutes(func(pattern string, h http.HandlerFunc) {
				if pattern == "GET /v1/groups/{id}/events" {
					stream = h
				}
			}, viewer, nil)
			w := httptest.NewRecorder()
			stream(w, httptest.NewRequest("GET", "/v1/groups/hostile-id/events", nil).WithContext(ctx))
			if strings.HasPrefix(kind, "live-") {
				if w.Code != 500 {
					t.Fatalf("live operation failure was hidden: %d %s", w.Code, w.Body)
				}
				return
			}
			if w.Code >= 500 || w.Body.Len() != 0 {
				t.Fatalf("ended stream got an error response: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestGroupStreamInitialReadHonorsRequestEnd(t *testing.T) {
	for _, kind := range []string{"canceled", "deadline", "live-error"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "deadline" {
				var end context.CancelFunc
				ctx, end = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer end()
			}
			base, err := sqlite.NewConnector(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			stopped := errors.New("live storage failure")
			calls := 0
			db := sql.OpenDB(groupStreamConnector{base, func(context.Context) (driver.Conn, error) {
				calls++
				if kind == "canceled" {
					cancel()
					return nil, context.Canceled
				}
				return nil, stopped
			}})
			defer db.Close()
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/v1/groups/hostile-id/events", nil).WithContext(ctx)
			Dependencies{}.groupEventStream(w, r, social.New(db), identity.Principal{}, "hostile-id")
			if kind == "live-error" {
				if w.Code != 500 || calls != 1 {
					t.Fatalf("live storage failure was hidden: %d %s opens=%d", w.Code, w.Body, calls)
				}
				return
			}
			if w.Code >= 500 || w.Body.Len() != 0 {
				t.Fatalf("ended stream got an error response: %d %s", w.Code, w.Body)
			}
		})
	}
}

// The body on this GET is ignored, IDs are opaque lookup keys, and a live
// request still gets the established not-found or malformed-resume response.
func TestGroupStreamLiveInputErrorsKeepTheirStatus(t *testing.T) {
	f := socialHTTP(t)
	group := f.group("host-only")
	for _, tc := range []struct {
		id, resume string
		status     int
	}{
		{"hostile-id", "", http.StatusNotFound},
		{group.Group.ID, "1e1000", http.StatusBadRequest},
	} {
		r := httptest.NewRequest("GET", "/v1/groups/"+tc.id+"/events", strings.NewReader(`{"id":1e1000,"revision":-1e1000}`))
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Last-Event-ID", tc.resume)
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("id=%s resume=%q: got %d %s", tc.id, tc.resume, w.Code, w.Body)
		}
	}
}

func TestGroupStreamRequiresTheOriginalWriterToFlush(t *testing.T) {
	w := httptest.NewRecorder()
	// Deliberately expose only ResponseWriter, although the recorder underneath
	// can flush. The prelude guard must not invent streaming support.
	d := Dependencies{}
	d.groupEventStream(struct{ http.ResponseWriter }{w}, httptest.NewRequest("GET", "/v1/groups/id/events", nil), nil, identity.Principal{}, "id")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "streaming_unsupported") {
		t.Fatalf("unsupported stream writer: %d %s", w.Code, w.Body)
	}
}
