package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestRestrictionWritersHideMemberCeilingRefusals(t *testing.T) {
	for _, writer := range []struct {
		name string
		call func(*httptest.ResponseRecorder, error)
	}{
		{"common", func(w *httptest.ResponseRecorder, err error) { failure(w, err) }},
		{"access", func(w *httptest.ResponseRecorder, err error) { accessFailure(w, err) }},
	} {
		for _, refusal := range []error{identity.ErrContentRestricted} {
			w := httptest.NewRecorder()
			writer.call(w, refusal)
			if w.Code != 404 || !strings.Contains(w.Body.String(), `"not_found"`) {
				t.Fatalf("%s %v: %d %s", writer.name, refusal, w.Code, w.Body.String())
			}
		}
	}
}
