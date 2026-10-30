package metadataprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const acousticFingerprint = "AQAAEAAAgAAAQAAAAAAAAAAA"

func acousticFixture(t *testing.T, h http.HandlerFunc) *AcoustID {
	p, e := NewAcoustID("application-secret")
	if e != nil {
		t.Fatal(e)
	}
	p.http = fixtureTransport(t, h)
	return p
}
func TestAcoustIDLookupUsesPrivateFormAndTypedRecordingIDs(t *testing.T) {
	p := acousticFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/lookup" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			t.Error("private POST contract", r.Method, r.URL.Path)
		}
		if e := r.ParseForm(); e != nil {
			t.Fatal(e)
		}
		if r.Form.Get("client") != "application-secret" || r.Form.Get("fingerprint") != acousticFingerprint || r.Form.Get("duration") != "210" || r.Form.Get("meta") != "recordingids" || r.Form.Get("user") != "" {
			t.Error("wrong lookup form")
		}
		w.Write([]byte(`{"status":"ok","results":[{"id":"` + idC + `","score":0.96,"recordings":[{"id":"` + idA + `"},{"id":"` + idB + `"}]},{"id":"` + idB + `","score":0.98,"recordings":[{"id":"` + idA + `"}]}]}`))
	})
	values, e := p.Lookup(context.Background(), acousticFingerprint, 210)
	if e != nil || len(values) != 2 || values[0].RecordingID != idA || values[0].Score != .98 {
		t.Fatal(values, e)
	}
}
func TestAcoustIDBoundsFailuresAndNoRedirects(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		code       string
	}{{"authentication", `{"status":"error","error":{"code":4,"message":"secret"}}`, 200, "authentication"}, {"rate", `{}`, 429, "rate_limited"}, {"missing", `{}`, 404, "request_rejected"}, {"malformed", `{"status":"ok","results":[{"id":"wrong","score":0.99}]}`, 200, "malformed"}, {"score", `{"status":"ok","results":[{"id":"` + idA + `","score":2}]}`, 200, "malformed"}, {"redirect", ``, 302, "request_rejected"}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p := acousticFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "3600")
				w.Header().Set("Location", "http://169.254.169.254/")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			_, e := p.Lookup(context.Background(), acousticFingerprint, 100)
			var v *Error
			if !errors.As(e, &v) || v.Code != tc.code || calls != 1 {
				t.Fatal(e, calls)
			}
			if strings.Contains(e.Error(), "secret") {
				t.Fatal("credential or provider diagnostic leaked")
			}
		})
	}
	p := acousticFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid fingerprint reached transport") })
	for _, fp := range []string{"short", strings.Repeat("A", 16385), "AAAAAAAAAAAAAAAA\n"} {
		if _, e := p.Lookup(context.Background(), fp, 100); e == nil {
			t.Fatal("invalid fingerprint accepted")
		}
	}
	if _, e := p.Lookup(context.Background(), acousticFingerprint, 0); e == nil {
		t.Fatal("missing duration accepted")
	}
}
func TestAcoustIDEmptyAndBoundedResult(t *testing.T) {
	p := acousticFixture(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok","results":[]}`)) })
	values, e := p.Lookup(context.Background(), acousticFingerprint, 100)
	if e != nil || values == nil || len(values) != 0 {
		t.Fatal(values, e)
	}
	p = acousticFixture(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "results": make([]int, 26)})
	})
	if _, e = p.Lookup(context.Background(), acousticFingerprint, 100); e == nil {
		t.Fatal("oversized result accepted")
	}
	if p, e := NewAcoustID(""); e != nil || p != nil {
		t.Fatal("missing credential should remain unconfigured", e)
	}
}
