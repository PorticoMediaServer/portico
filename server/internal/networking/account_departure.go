package networking

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// DepartureIntent reads an installed tuple without granting positive claim
// authority. Its sole use is a signed deny-only leave notice.
func (s *SQLiteStore) DepartureIntent(ctx context.Context) (Intent, error) {
	var id string
	e := s.db.QueryRowContext(ctx, `SELECT installed_operation_id FROM networking_claim_identity WHERE singleton=1 AND installed_operation_id IS NOT NULL`).Scan(&id)
	if e != nil {
		return Intent{}, e
	}
	g, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return Intent{}, e
	}
	defer g.Rollback()
	v, e := loadIntentTx(ctx, g.Tx(), id)
	return v.Intent, e
}
func (s *SQLiteStore) checkServiceRequest(ctx context.Context, path string, in any) error {
	if path == "/v1/servers/leave" || path == "/v1/server-claims/cancel" {
		proof, ok := in.(proofEnvelope)
		if !ok {
			return ErrInvalid
		}
		payload, e := base64.RawURLEncoding.Strict().DecodeString(proof.Payload)
		if e != nil || base64.RawURLEncoding.EncodeToString(payload) != proof.Payload {
			return ErrInvalid
		}
		signature, e := base64.RawURLEncoding.Strict().DecodeString(proof.Signature)
		if e != nil || base64.RawURLEncoding.EncodeToString(signature) != proof.Signature {
			return ErrInvalid
		}
		if path == "/v1/server-claims/cancel" {
			queued, err := s.queuedPreinstallCancellation(ctx, payload, signature)
			if err != nil {
				return err
			}
			if queued {
				return nil
			}
		} else {
			var queued bool
			e = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_service_outbox WHERE payload=? AND signature=? AND delivered_ms=0)`, payload, signature).Scan(&queued)
			if e != nil {
				return e
			}
			if !queued {
				return ErrInvalid
			}
			return nil
		}
	}
	var kind string
	e := s.db.QueryRowContext(ctx, `SELECT sign_in_kind FROM identity_state WHERE singleton=1`).Scan(&kind)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if kind == "portico" {
		return nil
	}
	// Only the owner's explicitly confirmed move may start the new identity
	// service, and only its interactive claim endpoints; background work stays off.
	var move bool
	if e = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_moves WHERE to_kind='portico' AND stage='preview' AND owner_confirmed=1)`).Scan(&move); e != nil {
		return e
	}
	if move && (len(path) > 18 && path[:18] == "/v1/server-claims/") {
		return nil
	}
	return ErrLocalServices
}

var ErrLocalServices = errors.New("A local server does not contact Portico Services.")

func (t *HTTPTransport) SendIdentityDeparture(ctx context.Context, p SignedProof) error {
	// Only a store with the durable deny-only outbox can authorize this call.
	store, ok := t.credentials.(*SQLiteStore)
	if !ok {
		return ErrInvalid
	}
	envelope := proofEnvelope{Payload: base64.RawURLEncoding.EncodeToString(p.Payload), Signature: base64.RawURLEncoding.EncodeToString(p.Signature)}
	if e := store.checkServiceRequest(ctx, "/v1/servers/leave", envelope); e != nil {
		return e
	}
	return t.controlRequest(ctx, "POST", "/v1/servers/leave", envelope, nil, nil, claimHTTPBound)
}

func validDepartureSigningPayload(raw []byte, b Binding) bool {
	var wire struct {
		Kind                 string `json:"kind"`
		Version              string `json:"version"`
		Audience             string `json:"audience"`
		OperationID          string `json:"operationId"`
		ServerID             string `json:"serverId"`
		PublicKey            string `json:"publicKey"`
		ClaimGeneration      string `json:"claimGeneration"`
		CredentialGeneration string `json:"credentialGeneration"`
		NoticeID             string `json:"noticeId"`
		Reason               string `json:"reason"`
	}
	if canonicalApprovalJSON(raw, &wire) != nil || wire.Kind != "portico.server.leave" || wire.Version != "1" || !validAudience(wire.Audience) || wire.OperationID != b.OperationID || wire.ServerID != b.ServerID || wire.PublicKey != base64.RawURLEncoding.EncodeToString(b.PublicKey) || wire.NoticeID == "" || (wire.Reason != "moving_to_local" && wire.Reason != "start_over_local") {
		return false
	}
	canonical, _ := json.Marshal(wire)
	return bytes.Equal(canonical, raw)
}
