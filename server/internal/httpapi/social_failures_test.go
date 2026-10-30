package httpapi

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
)

func TestSocialFailureMatchesDeclaredCanonicalCodes(t *testing.T) {
	declared := map[string]bool{}
	for _, code := range socialErrorCodes() {
		declared[code] = true
	}
	for _, tc := range []struct {
		err       error
		status    int
		code      string
		retryable bool
	}{
		{identity.ErrForbidden, 403, "not_permitted", false},
		{errors.New("storage unavailable"), 500, "internal", true},
		{identity.ErrUnauthorized, 401, "unauthorized", false},
		{identity.ErrContentRestricted, 404, "not_found", false},
	} {
		recorder := httptest.NewRecorder()
		socialFailure(recorder, tc.err)
		var body struct {
			Error struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		if e := json.Unmarshal(recorder.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		if recorder.Code != tc.status || body.Error.Code != tc.code || body.Error.Retryable != tc.retryable || !declared[body.Error.Code] {
			t.Fatalf("status/code/retry mismatch: %d %s %v", recorder.Code, body.Error.Code, body.Error.Retryable)
		}
	}
}
