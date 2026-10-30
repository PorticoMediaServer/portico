package networking

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// SetupStartAuthority is captured from the current setup-token proof. It is not
// a Local owner or session; request/revision come from the original server.
type SetupStartAuthority struct {
	RequestID string
	Revision  int64
	CaptureTx func(context.Context, *sql.Tx, string, claimweb.Signed, claimweb.Signed) error
	// RenewTx replaces an expired, unconsumed start of this same installed setup.
	RenewTx func(context.Context, *sql.Tx, string, claimweb.Signed, claimweb.Signed) error
	BindTx  func(context.Context, *sql.Tx, string, string) error
}
type setupStartSigner interface {
	SignSetupStart(context.Context, Binding, claimweb.Signed, claimweb.SetupStart) (claimweb.Signed, error)
	SignRenewedSetupStart(context.Context, Binding, claimweb.Signed, claimweb.SetupStart) (claimweb.Signed, error)
}

// beginSetupWebClaim is the private initiation producer. Its public action
// binding is composed separately and must enforce the secure-origin and
// protected-state prerequisites. Signed state is never a status GET fact.
func (h *ClaimHandler) beginSetupWebClaim(ctx context.Context, v Intent, address, state, codeChallenge, installation string, authority *SetupStartAuthority) (localWebClaim, error) {
	var out localWebClaim
	transport, ok := h.transport.(*HTTPTransport)
	signer, canSign := h.signer.(setupStartSigner)
	if !ok || !canSign || authority == nil || authority.CaptureTx == nil || authority.BindTx == nil || !validID(authority.RequestID) || authority.Revision < 1 {
		return out, ErrUnavailable
	}
	if v.Purpose != "setup" || v.CurrentOwnerAccountID != "" || address == "" || !ownerRandom32(state) || !ownerRandom32(codeChallenge) || len(installation) < 32 || !validID(installation) {
		return out, ErrInvalid
	}
	if err := checkClaimRequest(ctx); err != nil {
		return out, err
	}
	var claimPayload, claimSignature, startPayload, startSignature []byte
	var phase, attempt, transaction string
	var expiresMS int64
	err := h.store.db.QueryRowContext(ctx, `SELECT claim_payload,claim_signature,start_payload,start_signature,phase,expires_ms,attempt_id,transaction_id FROM identity_setup_completions WHERE operation_id=?`, v.OperationID).Scan(&claimPayload, &claimSignature, &startPayload, &startSignature, &phase, &expiresMS, &attempt, &transaction)
	var start claimweb.SetupStart
	var original claimweb.Request
	// A slow installation (certificate issuance) can outlive the five-minute start. The same
	// approved, now installed setup may then renew it: a fresh claim identical but for its time
	// bounds and a fresh start for this browser. Owner admission is unchanged.
	renewal := err == nil && v.Stage == Installed && v.Purpose == "setup" && attempt == "" && (phase == "prepared" || phase == "installed") && expiresMS <= time.Now().UnixMilli()
	if renewal && authority.RenewTx == nil {
		return out, ErrUnavailable
	}
	if err == nil && !renewal {
		// A renewed start whose Hosted call or bind failed replays its own stored pair, as a
		// prepared start does, instead of waiting out its five minutes.
		if phase != "prepared" && !(phase == "installed" && transaction == "" && attempt == "") {
			return out, stepFailure("setup start phase", ErrStale)
		}
		out.Signed = claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(claimPayload), Signature: base64.RawURLEncoding.EncodeToString(claimSignature)}
		proof := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(startPayload), Signature: base64.RawURLEncoding.EncodeToString(startSignature)}
		original, start, err = claimweb.VerifySetupStart(out.Signed, proof, transport.origin, time.Now())
		if err != nil || original.Address != address || start.CodeChallenge != codeChallenge || start.InstallationID != installation || start.SetupRequestID != authority.RequestID || start.SetupRevision != authority.Revision || subtle.ConstantTimeCompare([]byte(start.State), []byte(state)) != 1 {
			return localWebClaim{}, stepFailure("setup start replay", ErrStale)
		}
		out.SetupStart = &proof

	} else if renewal || errors.Is(err, sql.ErrNoRows) {
		if !renewal && (v.Stage != Prepared || v.AccountID != UnboundAccount) {
			return out, stepFailure("setup start stage", ErrStale)
		}
		now := time.Now().UTC()
		expires := now.Add(claimweb.SetupLifetime())
		if !expires.After(now) {
			return out, ErrStale
		}
		if renewal {
			// The same claim the account approved, with fresh time bounds only.
			if err = json.Unmarshal(claimPayload, &original); err != nil || original.OperationID != v.OperationID || original.ServerID != v.ServerID || original.Purpose != "setup" || original.Address != address || original.AccountID != "" {
				return out, stepFailure("setup start renewal claim", ErrStale)
			}
			original.IssuedAt, original.ExpiresAt = now, now.Add(claimweb.SetupLifetime())
		} else {
			original = claimweb.Request{Kind: "portico.claim.web.v2", Audience: transport.origin, OperationID: v.OperationID, ServerID: v.ServerID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), LocalGeneration: v.LocalGeneration, Name: h.name(), Address: address, Purpose: "setup", IssuedAt: now, ExpiresAt: now.Add(claimweb.SetupLifetime())}
		}
		claimPayload, err = json.Marshal(original)
		if err != nil {
			return out, ErrInvalid
		}
		// The claim and its start are signed with the claim's original, unbound binding; a
		// renewal's intent is now bound to its approver (the claim itself never names one).
		signing := v.Binding
		if renewal {
			signing.AccountID = UnboundAccount
		}
		claimSignature, err = h.signer.Sign(ctx, signing, claimPayload)
		if err != nil {
			return out, err
		}
		out.Signed = claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(claimPayload), Signature: base64.RawURLEncoding.EncodeToString(claimSignature)}
		challenge := make([]byte, 24)
		if _, err = rand.Read(challenge); err != nil {
			return out, err
		}
		digest := sha256.Sum256(claimPayload)
		start = claimweb.SetupStart{SetupRequestID: authority.RequestID, SetupRevision: authority.Revision, Kind: claimweb.SetupStartKind, Audience: transport.origin, ClaimHash: base64.RawURLEncoding.EncodeToString(digest[:]), OperationID: v.OperationID, ServerID: v.ServerID, PublicKey: original.PublicKey, LocalGeneration: v.LocalGeneration, Address: address, Purpose: "setup", State: state, CodeChallenge: codeChallenge, InstallationID: installation, ServerChallenge: base64.RawURLEncoding.EncodeToString(challenge), IssuedAt: now, ExpiresAt: expires}
		sign := signer.SignSetupStart
		if renewal {
			sign = signer.SignRenewedSetupStart
		}
		proof, err := sign(ctx, signing, out.Signed, start)
		if err != nil {
			return out, stepFailure("setup start signing", err)
		}
		out.SetupStart = &proof
	} else {
		return out, err
	}
	// Persist the exact full proof and fresh challenge before any Hosted call.
	g, err := h.store.tx(ctx)
	if err != nil {
		return localWebClaim{}, err
	}
	if renewal {
		err = authority.RenewTx(ctx, g.Tx(), transport.origin, out.Signed, *out.SetupStart)
	} else {
		err = authority.CaptureTx(ctx, g.Tx(), transport.origin, out.Signed, *out.SetupStart)
	}
	if err == nil {
		err = commitClaimDatabaseTx(ctx, g)
	}
	g.Rollback()
	if err != nil {
		if renewal {
			return localWebClaim{}, stepFailure("setup start renewal", err)
		}
		return localWebClaim{}, stepFailure("setup start capture", err)
	}
	// A failed pending POST may never create networking_web_claims/watch. The
	// typed capture must still wake expiry reconciliation and survive restart.
	h.wakeClaimRecovery()
	var pending claimweb.Pending
	if err = transport.call(ctx, "pending", claimweb.PendingRequest{Payload: out.Signed.Payload, Signature: out.Signed.Signature, SetupStart: out.SetupStart}, &pending, nil); err != nil {
		return localWebClaim{}, stepFailure("hosted pending", err)
	}
	if !validID(pending.SetupTransactionID) || pending.Code == "" || len(pending.Code) > 128 || pending.ServerID != v.ServerID || pending.Purpose != "setup" || pending.Address != address || !pending.ExpiresAt.After(time.Now()) || pending.ExpiresAt.After(original.ExpiresAt) {
		return localWebClaim{}, ErrInvalid
	}
	out.SetupTransactionID = pending.SetupTransactionID
	out.Code = pending.Code
	out.URL = transport.origin + "/claim#" + url.Values{"code": {pending.Code}, "server": {original.Name}, "operation": {v.OperationID}}.Encode()
	out.ExpiresAt = start.ExpiresAt
	encoded, err := json.Marshal(out)
	if err != nil {
		return localWebClaim{}, err
	}
	g, err = h.store.tx(ctx)
	if err != nil {
		return localWebClaim{}, err
	}
	defer g.Rollback()
	if err = authority.BindTx(ctx, g.Tx(), v.OperationID, pending.SetupTransactionID); err != nil {
		return localWebClaim{}, stepFailure("setup start bind", err)
	}
	if _, err = g.Tx().ExecContext(ctx, `INSERT INTO networking_web_claims(operation_id,request) VALUES(?,?) ON CONFLICT(operation_id) DO UPDATE SET request=excluded.request`, v.OperationID, string(encoded)); err != nil {
		return localWebClaim{}, err
	}
	if err = commitClaimDatabaseTx(ctx, g); err != nil {
		return localWebClaim{}, err
	}
	h.wakeClaimRecovery()
	return out, nil
}
