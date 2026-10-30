package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestFoundationDirectReadErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		status int
		code   string
		retry  apierror.Retry
		after  int
	}{
		{"deadline", context.DeadlineExceeded, 503, "timeout", apierror.SameRequest, 2},
		{"read pressure", dbwork.ErrReadPressure, 503, "database_busy", apierror.SameRequest, 1},
		{"long read busy", dbwork.ErrLongReadBusy, 503, "database_busy", apierror.SameRequest, 5},
		{"writer capacity", dbwork.ErrWriterCapacity, 503, "writer_capacity", apierror.SameRequest, dbwork.WriteGate().RetryAfterSeconds()},
		{"unexpected", errors.New("unexpected database failure"), 500, "internal", apierror.Never, 0},
		{"unexplained transaction end", sql.ErrTxDone, 500, "internal", apierror.Never, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := fmt.Errorf("private direct read: %w", tc.cause)
			mapped := foundationAuthError(cause)
			if !errors.Is(mapped, cause) {
				t.Fatalf("lost original cause: %v", mapped)
			}
			w := httptest.NewRecorder()
			v1Catalogue().Write(w, "direct-read", mapped)
			var body struct {
				Error apierror.Error `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			after := ""
			if tc.after > 0 {
				after = strconv.Itoa(tc.after)
			}
			if w.Code != tc.status || body.Error.Code != tc.code || body.Error.Retry != tc.retry || w.Header().Get("Retry-After") != after || body.Error.RetryAfterMs != int64(tc.after)*1000 {
				t.Fatalf("status=%d error=%+v retry-after=%q", w.Code, body.Error, w.Header().Get("Retry-After"))
			}
			if strings.Contains(w.Body.String(), "private direct read") || body.Error.Cause != nil {
				t.Fatal("private cause exposed")
			}
		})
	}
	if err := foundationAuthError(nil); err != nil {
		t.Fatalf("nil error became %v", err)
	}
}

func TestFoundationDirectReadIdentityErrorsTakePrecedence(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  string
		after int
	}{
		{identity.ErrNotVisible, "not_found", 0},
		{identity.ErrForbidden, "not_permitted", 0},
		{identity.ErrUnauthorized, "unauthorized", 0},
		{identity.ErrPasswordChangeRequired, "password_change_required", 0},
		{identity.ErrDirectInput, "invalid_request", 0},
		{identity.ErrDeviceInput, "invalid_request", 0},
		{identity.ErrDeviceUnknown, "not_found", 0},
		{identity.ErrRestrictionInput, "invalid_request", 0},
		{identity.ErrRestrictionChanged, "revision_mismatch", 0},
		{identity.ErrProfileChanged, "revision_mismatch", 0},
		{identity.ErrBusy, "request_in_progress", 2},
	} {
		t.Run(tc.cause.Error(), func(t *testing.T) {
			cause := errors.Join(tc.cause, context.DeadlineExceeded, dbwork.ErrReadPressure)
			mapped := foundationAuthError(cause)
			var public *apierror.Error
			if !errors.As(mapped, &public) || public.Code != tc.code || public.RetryAfterSeconds != tc.after || public.Cause != cause || !errors.Is(mapped, tc.cause) {
				t.Fatalf("identity mapping or cause changed: %+v", mapped)
			}
			w := httptest.NewRecorder()
			v1Catalogue().Write(w, "direct-identity", mapped)
			if w.Code >= 500 || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("identity refusal became %d %s", w.Code, w.Body.String())
			}
		})
	}
}
