package httpapi

import (
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

type RecordingRootInput struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	OperationID      string `json:"operationId"`
	Path             string `json:"path"`
}

func (d Dependencies) recordingRootRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/dvr/recording-root", func(w http.ResponseWriter, r *http.Request) {
		p, err := d.owner(r)
		if err != nil {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), recordingRootError(err))
			return
		}
		if d.Console == nil {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "administration_unavailable"})
			return
		}
		out, err := d.Console.RecordingRootStatus(r.Context(), p, d.consoleAuthority(p, true))
		if err != nil {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), recordingRootError(err))
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PATCH /v1/admin/dvr/recording-root", func(w http.ResponseWriter, r *http.Request) {
		p, err := d.owner(r)
		if err != nil {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), recordingRootError(err))
			return
		}
		if d.Console == nil {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "administration_unavailable"})
			return
		}
		var body RecordingRootInput
		if err = decodeLegacyRevision(w, r, &body); err != nil {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), recordingRootError(err))
			return
		}
		out, err := d.Console.ApplyRecordingRoot(r.Context(), p, d.consoleAuthority(p, true), operations.RecordingRootChange{ExpectedRevision: body.ExpectedRevision, IdempotencyKey: body.OperationID, Path: body.Path})
		if err != nil {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), recordingRootError(err))
			return
		}
		write(w, 200, out)
	})
}

// The recording-root routes use registered outcomes instead of the legacy
// console envelope. Access, transaction and storage admission remain unchanged.
func recordingRootError(err error) error {
	switch {
	case errors.Is(err, errRevisionRequired):
		return &apierror.Error{Code: "revision_required"}
	case errors.Is(err, errStrictJSON):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, operations.ErrIdempotencyKeyReused):
		return &apierror.Error{Code: "idempotency_key_reused"}
	case errors.Is(err, operations.ErrExpired):
		return &apierror.Error{Code: "recording_root_request_expired"}
	case errors.Is(err, identity.ErrSignInKindLocal):
		return &apierror.Error{Code: "sign_in_kind_local"}
	case errors.Is(err, identity.ErrSignInKindPortico):
		return &apierror.Error{Code: "sign_in_kind_portico"}
	case errors.Is(err, identity.ErrProfileChanged), errors.Is(err, identity.ErrRestrictionChanged):
		return &apierror.Error{Code: "revision_mismatch"}
	case errors.Is(err, operations.ErrCapacity):
		return &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 60}
	}
	var public *apierror.Error
	if errors.As(v1AuthError(err), &public) {
		return public
	}
	return connectionPolicyError(err)
}

func (RecordingRootInput) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"expectedRevision", "operationId", "path"},
		"properties": map[string]any{
			"expectedRevision": map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991},
			"operationId":      map[string]any{"type": "string", "minLength": 1, "maxLength": 160, "pattern": "^[A-Za-z0-9_.-]+$"},
			"path":             operations.RecordingRootPathSchema(false),
		},
	}
}
