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

// OwnerStartAuthority is supplied only by the local HTTP identity edge. It
// closes over its exact current owner/main-profile/family/generation and token
// expiry; neither the browser nor a Hosted approval can construct this guard.
type OwnerSwitchCountSign func(context.Context, *sql.Tx, string, int64, int) (claimweb.Signed, error)

type OwnerStartAuthority struct {
	CaptureTx          func(context.Context, *sql.Tx, string, claimweb.Signed, claimweb.Signed) error
	CaptureWithCountTx func(context.Context, *sql.Tx, string, claimweb.Signed, claimweb.Signed, OwnerSwitchCountSign) (*claimweb.Signed, error)
	BindTx             func(context.Context, *sql.Tx, string, string) error
}

type ownerStartSigner interface {
	SignOwnerStart(context.Context, Binding, claimweb.Signed, claimweb.OwnerStart) (claimweb.Signed, error)
}

type ownerSwitchCountSigner interface {
	SignOwnerSwitchCountTx(context.Context, *sql.Tx, Binding, string, claimweb.Signed, claimweb.Signed, claimweb.OwnerSwitchCount) (claimweb.Signed, error)
}

func ownerRandom32(value string) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

// beginOwnerWebClaim is the private initiation producer. Its public action
// binding is composed separately and must enforce the secure-origin and
// protected-state prerequisites. Signed state is never a status GET fact.
func (h *ClaimHandler) beginOwnerWebClaim(ctx context.Context, v Intent, address, state, codeChallenge, installation string, authority *OwnerStartAuthority) (localWebClaim, error) {
	var out localWebClaim
	transport, ok := h.transport.(*HTTPTransport)
	signer, canSign := h.signer.(ownerStartSigner)
	if !ok || !canSign || authority == nil || authority.CaptureTx == nil || authority.BindTx == nil {
		return out, ErrUnavailable
	}
	if v.Purpose != "switch" || v.CurrentOwnerAccountID != "" || address == "" || !ownerRandom32(state) || !ownerRandom32(codeChallenge) || len(installation) < 32 || !validID(installation) {
		return out, ErrInvalid
	}
	if err := checkClaimRequest(ctx); err != nil {
		return out, err
	}
	var claimPayload, claimSignature, startPayload, startSignature, countPayload, countSignature []byte
	var phase string
	err := h.store.db.QueryRowContext(ctx, `SELECT claim_payload,claim_signature,start_payload,start_signature,phase,switch_count_payload,switch_count_signature FROM identity_owner_completions WHERE operation_id=?`, v.OperationID).Scan(&claimPayload, &claimSignature, &startPayload, &startSignature, &phase, &countPayload, &countSignature)
	var start claimweb.OwnerStart
	var original claimweb.Request
	if err == nil {
		if phase != "prepared" {
			return out, ErrStale
		}
		out.Signed = claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(claimPayload), Signature: base64.RawURLEncoding.EncodeToString(claimSignature)}
		proof := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(startPayload), Signature: base64.RawURLEncoding.EncodeToString(startSignature)}
		original, start, err = claimweb.VerifyOwnerStart(out.Signed, proof, transport.origin, time.Now())
		if err != nil || original.Address != address || start.CodeChallenge != codeChallenge || start.InstallationID != installation || subtle.ConstantTimeCompare([]byte(start.State), []byte(state)) != 1 {
			return localWebClaim{}, ErrStale
		}
		out.OwnerStart = &proof
		if len(countPayload) != 0 && len(countSignature) != 0 {
			out.ownerSwitchCount = &claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(countPayload), Signature: base64.RawURLEncoding.EncodeToString(countSignature)}
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if v.Stage != Prepared || v.AccountID != UnboundAccount {
			return out, ErrStale
		}
		now := time.Now().UTC()
		// The move is bound to the owner's session family, not its access token
		// (A5), so a start lasts a whole Portico Account step even though the
		// token that pressed it expires sooner.
		expires := now.Add(claimweb.OwnerStartLifetime)
		original = claimweb.Request{Kind: "portico.claim.web.v2", Audience: transport.origin, OperationID: v.OperationID, ServerID: v.ServerID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), LocalGeneration: v.LocalGeneration, Name: h.name(), Address: address, Purpose: "switch", IssuedAt: now, ExpiresAt: now.Add(claimweb.RequestLifetime("switch"))}
		claimPayload, err = json.Marshal(original)
		if err != nil {
			return out, ErrInvalid
		}
		claimSignature, err = h.signer.Sign(ctx, v.Binding, claimPayload)
		if err != nil {
			return out, err
		}
		out.Signed = claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(claimPayload), Signature: base64.RawURLEncoding.EncodeToString(claimSignature)}
		challenge := make([]byte, 24)
		if _, err = rand.Read(challenge); err != nil {
			return out, err
		}
		digest := sha256.Sum256(claimPayload)
		// The mode comes only from this server's own confirmed move. A start-over
		// emits the versioned start with its closed mode; every ordinary switch keeps
		// the legacy v1 document byte-for-byte.
		var startOver bool
		if err = h.store.db.QueryRowContext(ctx, `SELECT m.start_over
 FROM networking_claim_intents i JOIN identity_moves m ON m.owner_id=i.owner_id
 WHERE i.operation_id=? AND i.purpose='switch' AND i.current_owner_account_id=''
 AND m.from_kind='local' AND m.to_kind='portico' AND m.stage='preview' AND m.owner_confirmed=1`, v.OperationID).Scan(&startOver); err != nil {
			// Missing, changed or unreadable authoritative move state cannot be
			// turned into an ordinary switch. Capture below repeats this binding
			// in the accepting transaction before any Hosted request.
			return out, ErrStale
		}
		kind, mode := claimweb.OwnerStartKind, ""
		if startOver {
			kind, mode = claimweb.OwnerStartKindV2, "start-over"
		}
		start = claimweb.OwnerStart{Kind: kind, Audience: transport.origin, ClaimHash: base64.RawURLEncoding.EncodeToString(digest[:]), OperationID: v.OperationID, ServerID: v.ServerID, PublicKey: original.PublicKey, LocalGeneration: v.LocalGeneration, Address: address, Purpose: "switch", State: state, CodeChallenge: codeChallenge, InstallationID: installation, ServerChallenge: base64.RawURLEncoding.EncodeToString(challenge), IssuedAt: now, ExpiresAt: expires, TransitionMode: mode}
		proof, err := signer.SignOwnerStart(ctx, v.Binding, out.Signed, start)
		if err != nil {
			return out, err
		}
		out.OwnerStart = &proof
	} else {
		return out, err
	}
	// Persist the exact full proof and fresh challenge before any Hosted call.
	g, err := h.store.tx(ctx)
	if err != nil {
		return localWebClaim{}, err
	}
	if authority.CaptureWithCountTx != nil {
		var signCount OwnerSwitchCountSign
		if countSigner, ok := h.signer.(ownerSwitchCountSigner); ok {
			signCount = func(ctx context.Context, tx *sql.Tx, preview string, revision int64, people int) (claimweb.Signed, error) {
				now := time.Now().UTC()
				expires := now.Add(claimweb.OwnerStartLifetime)
				if original.ExpiresAt.Before(expires) {
					expires = original.ExpiresAt
				}
				if start.ExpiresAt.Before(expires) {
					expires = start.ExpiresAt
				}
				count := claimweb.OwnerSwitchCount{Kind: claimweb.OwnerSwitchCountKind, ServerID: original.ServerID, PublicKey: original.PublicKey, OperationID: original.OperationID, Purpose: "switch", PreviewID: preview, PreviewRevision: revision, LocalGeneration: original.LocalGeneration, ClaimHash: start.ClaimHash, OtherPeopleCount: people, IssuedAt: now, ExpiresAt: expires}
				return countSigner.SignOwnerSwitchCountTx(ctx, tx, v.Binding, transport.origin, out.Signed, *out.OwnerStart, count)
			}
		}
		out.ownerSwitchCount, err = authority.CaptureWithCountTx(ctx, g.Tx(), transport.origin, out.Signed, *out.OwnerStart, signCount)
	} else {
		err = authority.CaptureTx(ctx, g.Tx(), transport.origin, out.Signed, *out.OwnerStart)
	}
	if err == nil {
		err = commitClaimDatabaseTx(ctx, g)
	}
	g.Rollback()
	if err != nil {
		return localWebClaim{}, err
	}
	// A failed pending POST may never create networking_web_claims/watch. The
	// typed capture must still wake expiry reconciliation and survive restart.
	h.wakeClaimRecovery()
	var pending claimweb.Pending
	if err = transport.call(ctx, "pending", claimweb.PendingRequest{Payload: out.Signed.Payload, Signature: out.Signed.Signature, OwnerStart: out.OwnerStart, OwnerSwitchCount: out.ownerSwitchCount}, &pending, nil); err != nil {
		return localWebClaim{}, err
	}
	if !validID(pending.OwnerTransactionID) || pending.Code == "" || len(pending.Code) > 128 || pending.ServerID != v.ServerID || pending.Purpose != "switch" || pending.Address != address || !pending.ExpiresAt.After(time.Now()) || pending.ExpiresAt.After(original.ExpiresAt) {
		return localWebClaim{}, ErrInvalid
	}
	out.OwnerTransactionID = pending.OwnerTransactionID
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
	if err = authority.BindTx(ctx, g.Tx(), v.OperationID, pending.OwnerTransactionID); err != nil {
		return localWebClaim{}, err
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
