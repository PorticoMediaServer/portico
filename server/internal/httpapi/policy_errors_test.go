package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
)

func TestPolicyErrorEnvelopes(t *testing.T) {
	for _, d := range policyErrorDefinitions() {
		t.Run(d.Code, func(t *testing.T) {
			w := httptest.NewRecorder()
			policyError(w, d.Code)
			if w.Code != d.Status || errorCode(t, w) != d.Code {
				t.Fatal(w.Code, w.Body.String())
			}
			if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(w.Body.String(), `"requestId":`) || !strings.Contains(w.Body.String(), `"retry":`) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestMetadataUnavailableUsesErrorEnvelope(t *testing.T) {
	d, viewer := logoutHTTPFixture(t)
	h := New(d)
	r := httptest.NewRequest("POST", "/v1/metadata/bulk", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || errorCode(t, w) != "metadata_unavailable" {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
}
