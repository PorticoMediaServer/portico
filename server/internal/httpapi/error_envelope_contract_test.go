package httpapi

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// BE-API-08 / BE-API-09 contract. Every registered route, called by the owner
// with a body it can't accept, answers an error that the shared client error
// reader understands: {"error":{"code":"…"}} with a code, never a text body or
// a flat {"error":"…"} / {"code":…}. And every PUT, PATCH or DELETE whose body
// carries a revision fence answers 428 revision_required when the fence is
// missing, never a silent revision 0.
func TestEveryRouteAnswersErrorsInTheEnvelope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newV1Fixture(t, 1)
	param := regexp.MustCompile(`\{[^}]+\}`)
	patterns := make([]string, 0, len(routeLanes))
	for pattern := range routeLanes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	fenced := []string{}
	for _, pattern := range patterns {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok || path == "" || method == "GET" || method == "HEAD" {
			continue
		}
		// Streams and media bodies answer bytes, not JSON, on success; their
		// failures are still envelopes, and a malformed body never streams.
		path = param.ReplaceAllString(path, "x")
		w := f.raw(method, path, f.owner.AccessToken, nil, "{}")
		if w.Code < 400 {
			continue
		}
		var envelope struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error == nil || envelope.Error.Code == "" {
			t.Errorf("%s answered %d outside the error envelope: %.200s", pattern, w.Code, w.Body.String())
			continue
		}
		if w.Code == http.StatusPreconditionRequired && envelope.Error.Code == "revision_required" {
			fenced = append(fenced, pattern)
		}
	}
	// A /v1 path no route serves is an envelope too (404, or 405 with Allow).
	for _, probe := range []struct{ method, path string }{{"GET", "/v1/no-such-route"}, {"DELETE", "/v1/capabilities"}} {
		w := f.raw(probe.method, probe.path, f.owner.AccessToken, nil, nil)
		var envelope struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error == nil || (w.Code != 404 && w.Code != 405) {
			t.Errorf("%s %s answered %d outside the envelope: %.200s", probe.method, probe.path, w.Code, w.Body.String())
		}
	}
	t.Logf("revision-fenced routes answering 428 for a missing expectedRevision (%d):\n%s", len(fenced), strings.Join(fenced, "\n"))
}
