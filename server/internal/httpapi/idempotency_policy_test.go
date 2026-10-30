package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
)

func TestPolicyIdempotencyKeyReuseIsConflict(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(http.ResponseWriter, error)
		err   error
	}{{"access", accessFailure, access.ErrIdempotencyKeyReused}, {"administration", administrationFailure, administration.ErrIdempotencyKeyReused}} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.write(w, tc.err)
			if w.Code != 409 || errorCode(t, w) != "idempotency_key_reused" {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
