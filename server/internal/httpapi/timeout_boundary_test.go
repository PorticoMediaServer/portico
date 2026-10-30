package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func TestV1TimeoutBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
		retry  apierror.Retry
		after  string
	}{
		{"deadline", context.DeadlineExceeded, 503, "timeout", apierror.SameRequest, "2"},
		{"wrapped deadline", fmt.Errorf("private detail: %w", context.DeadlineExceeded), 503, "timeout", apierror.SameRequest, "2"},
		{"unknown", errors.New("private detail"), 500, "internal", apierror.Never, ""},
		{"canceled", context.Canceled, 500, "internal", apierror.Never, ""},
		{"authorization before deadline", errors.Join(identity.ErrForbidden, context.DeadlineExceeded), 403, "not_permitted", apierror.Never, ""},
		{"revision before deadline", errors.Join(&playbackv1.RevisionError{Current: "2"}, context.DeadlineExceeded), 412, "revision_mismatch", apierror.AfterRefresh, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			v1Catalogue().Write(w, "test", v1Error(tc.err))
			var body struct {
				Error apierror.Error `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || body.Error.Code != tc.code || body.Error.Retry != tc.retry || w.Header().Get("Retry-After") != tc.after {
				t.Fatalf("status=%d error=%+v retry-after=%q", w.Code, body.Error, w.Header().Get("Retry-After"))
			}
			if strings.Contains(w.Body.String(), "private detail") {
				t.Fatal("private cause exposed")
			}
			if tc.after != "" && body.Error.RetryAfterMs != 2000 {
				t.Fatalf("retryAfterMs=%d", body.Error.RetryAfterMs)
			}
		})
	}
}

// The transport observes a deadline after the real handler committed the start.
func TestV1TimedOutStartReplyReplaysOriginalSession(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	key := map[string]string{"Idempotency-Key": "timeout-start-key-0001"}
	raw, err := json.Marshal(startBody(f.items[0], map[string]any{"startFrom": "beginning"}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	handler := f.handler
	var first playbackv1.SessionView
	replaced := false
	f.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !replaced && r.Method == http.MethodPost && r.URL.Path == "/v1/playback/sessions" {
			replaced = true
			committed := httptest.NewRecorder()
			handler.ServeHTTP(committed, r)
			if committed.Code != 201 {
				t.Fatalf("committed start %d %s", committed.Code, committed.Body.String())
			}
			if err := json.Unmarshal(committed.Body.Bytes(), &first); err != nil {
				t.Fatal(err)
			}
			v1Catalogue().Write(w, "lost-reply", v1Error(context.DeadlineExceeded))
			return
		}
		handler.ServeHTTP(w, r)
	})
	timedOut := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, key, body)
	if timedOut.Code != 503 || v1Code(timedOut) != "timeout" || timedOut.Header().Get("Retry-After") != "2" {
		t.Fatalf("timeout %d %s", timedOut.Code, timedOut.Body.String())
	}
	var replay playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", key, body, 201, &replay)
	if first.ID == "" || replay.ID != first.ID || first.Presentation.Generation < 1 || replay.Presentation.Generation != first.Presentation.Generation {
		t.Fatalf("first %+v replay %+v", first, replay)
	}
	for _, table := range []string{"playback_v1_sessions", "playback_sessions"} {
		var count int
		if err := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s: created %d sessions", table, count)
		}
	}
}
