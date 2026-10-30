package networking

import (
	"bytes"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"github.com/PorticoMediaServer/portico/server/internal/wirejson"
)

// ClaimOwnerStartRequest contains browser protected-state bindings only. The
// server chooses the fresh challenge, original signed Address and expiry.
type ClaimOwnerStartRequest struct {
	State          string `json:"state"`
	CodeChallenge  string `json:"codeChallenge"`
	InstallationID string `json:"installationId"`
}

func (q *ClaimOwnerStartRequest) UnmarshalJSON(raw []byte) error {
	*q = ClaimOwnerStartRequest{}
	fields, err := wirejson.Object(raw, "state", "codeChallenge", "installationId")
	type plain ClaimOwnerStartRequest
	var next plain
	if err != nil || len(fields) != 3 || json.Unmarshal(raw, &next) != nil || !ownerRandom32(next.State) || !ownerRandom32(next.CodeChallenge) || len(next.InstallationID) < 32 || !validID(next.InstallationID) {
		return ErrInvalid
	}
	*q = ClaimOwnerStartRequest(next)
	return nil
}

type ClaimActionRequest struct {
	Expected         ClaimExpected           `json:"expected"`
	ApprovalEnvelope string                  `json:"approvalEnvelope,omitempty"`
	SetupStart       *ClaimSetupStartRequest `json:"setupStart,omitempty"`
	OwnerStart       *ClaimOwnerStartRequest `json:"ownerStart,omitempty"`
}

func (q *ClaimActionRequest) UnmarshalJSON(raw []byte) error {
	*q = ClaimActionRequest{}
	fields, err := wirejson.Object(raw, "expected", "approvalEnvelope", "ownerStart", "setupStart")
	if err != nil {
		return ErrInvalid
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	expected, err := wirejson.Object(fields["expected"], "operationId", "localGeneration", "revision")
	if err != nil || len(expected) != 3 {
		return ErrInvalid
	}
	type plain ClaimActionRequest
	var next plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&next) != nil {
		return ErrInvalid
	}
	_, hasApproval := fields["approvalEnvelope"]
	if next.SetupStart != nil && (next.OwnerStart != nil || hasApproval) {
		return ErrInvalid
	}
	*q = ClaimActionRequest(next)
	return nil
}

// OwnerContinuation exposes only normalized progress on GET. Full proof is
// attached solely to the initiating authenticated POST response.
type OwnerContinuation struct {
	TransactionID      string                     `json:"transactionId"`
	Phase              string                     `json:"phase"`
	ExpiresAt          string                     `json:"expiresAt"`
	ReturnRegistration *serverreturn.Registration `json:"returnRegistration,omitempty"`
	Claim              *claimweb.Signed           `json:"claim,omitempty"`
	Start              *claimweb.Signed           `json:"start,omitempty"`
}

func (OwnerContinuation) String() string   { return "[owner continuation redacted]" }
func (OwnerContinuation) GoString() string { return "[owner continuation redacted]" }

// Setup uses the same protected-state scalar grammar in a separate purpose.
type ClaimSetupStartRequest ClaimOwnerStartRequest

func (q *ClaimSetupStartRequest) UnmarshalJSON(raw []byte) error {
	*q = ClaimSetupStartRequest{}
	var value ClaimOwnerStartRequest
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*q = ClaimSetupStartRequest(value)
	return nil
}
func (ClaimSetupStartRequest) JSONSchema() map[string]any {
	return (ClaimOwnerStartRequest{}).JSONSchema()
}

// SetupContinuation is the setup's browser continuation. It never carries a return
// registration: setup finishes at claim install over its signed original address (O-163).
type SetupContinuation struct {
	TransactionID string           `json:"transactionId"`
	Phase         string           `json:"phase"`
	ExpiresAt     string           `json:"expiresAt"`
	Claim         *claimweb.Signed `json:"claim,omitempty"`
	Start         *claimweb.Signed `json:"start,omitempty"`
}

func (SetupContinuation) String() string   { return "[setup continuation redacted]" }
func (SetupContinuation) GoString() string { return "[setup continuation redacted]" }
