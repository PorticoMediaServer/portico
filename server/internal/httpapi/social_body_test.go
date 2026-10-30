package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/social"
)

func TestSocialBodyRejectsMalformedDocumentsBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name, media, raw string
		status           int
	}{
		{"valid", "application/json", `{"name":"Movie night"}`, http.StatusNoContent},
		{"utf8", "application/json; charset=utf-8", `{"name":"Movie night"}`, http.StatusNoContent},
		{"missing media", "", `{"name":"Movie night"}`, http.StatusNoContent},
		{"uppercase charset", "application/json; charset=UTF-8", `{"name":"Movie night"}`, http.StatusNoContent},
		{"plain text", "text/plain", `{"name":"Movie night"}`, http.StatusUnsupportedMediaType},
		{"unknown parameter", "application/json; version=1", `{"name":"Movie night"}`, http.StatusUnsupportedMediaType},
		{"invalid utf8", "application/json", "{\"name\":\"\xff\"}", http.StatusBadRequest},
		{"media prefix", "application/json-extra", `{"name":"Movie night"}`, http.StatusUnsupportedMediaType},
		{"wrong charset", "application/json; charset=latin1", `{"name":"Movie night"}`, http.StatusUnsupportedMediaType},
		{"trailing object", "application/json", `{"name":"Movie night"}{}`, http.StatusBadRequest},
		{"trailing garbage", "application/json", `{"name":"Movie night"}x`, http.StatusBadRequest},
		{"duplicate field", "application/json", `{"name":"one","name":"two"}`, http.StatusBadRequest},
		{"escaped duplicate", "application/json", `{"name":"one","na\u006de":"two"}`, http.StatusBadRequest},
		{"unknown field", "application/json", `{"name":"one","extra":true}`, http.StatusBadRequest},
		{"case alias", "application/json", `{"Name":"one"}`, http.StatusBadRequest},
		{"oversized", "application/json", `{"name":"` + strings.Repeat("x", 256<<10) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/groups", strings.NewReader(test.raw))
			r.Header.Set("Content-Type", test.media)
			w := httptest.NewRecorder()
			mutated := false
			var body social.CreateGroupRequest
			if socialBody(w, r, &body) {
				mutated = true
				w.WriteHeader(http.StatusNoContent)
			}
			if w.Code != test.status || mutated != (test.status == http.StatusNoContent) {
				t.Fatalf("status=%d mutation=%v: %s", w.Code, mutated, w.Body.String())
			}
			if !mutated {
				var failure struct {
					ProtocolVersion string `json:"protocolVersion"`
					Error           struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || failure.ProtocolVersion != social.Protocol || failure.Error.Code != "invalid_request" {
					t.Fatalf("social error envelope changed: %s (%v)", w.Body.String(), err)
				}
			}
		})
	}
}
