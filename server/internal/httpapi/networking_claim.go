package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"strings"
)

// NetworkingClaimAuthorizer captures the actual current local owner token.
// Claiming stays with the server's own web app and the built-in Portico pages;
// native bearer requests may omit Origin. No cookies grant local ownership.
func NetworkingClaimAuthorizer(id *identity.Service, origins []string, policies ...Dependencies) networking.ClaimRequestAuthorizer {
	d := Dependencies{}
	if len(policies) > 0 {
		d = policies[0]
	}
	d.Origins = origins
	return func(r *http.Request) (networking.ClaimRequestOwner, error) {
		if id == nil || len(r.Header.Values("Authorization")) != 1 {
			return networking.ClaimRequestOwner{}, identity.ErrUnauthorized
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !d.firstPartyOrigin(r, origin) {
				return networking.ClaimRequestOwner{}, identity.ErrUnauthorized
			}
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || strings.ContainsAny(strings.TrimPrefix(header, "Bearer "), " ,\r\n") {
			return networking.ClaimRequestOwner{}, identity.ErrUnauthorized
		}

		secret := strings.TrimPrefix(header, "Bearer ")
		p, e := id.Authenticate(secret)
		// A narrowed owner token is recognized on exactly these operation
		// reads/continuation calls. It cannot prepare or approve another claim,
		// cancel the installed central claim, or configure routes/certificates.
		// The existing cancel path has a distinct captured-continuation branch.
		if e != nil && (r.Method == http.MethodGet && r.URL.Path == "/v1/networking/claim" || r.Method == http.MethodPost && (r.URL.Path == "/v1/networking/claim/continue" || r.URL.Path == "/v1/networking/claim/await-approval" || r.URL.Path == "/v1/networking/claim/cancel")) {
			p, e = id.OwnerClaimAuthority(r.Context(), secret)
		}
		if e != nil && (r.URL.Path == "/v1/networking/claim" || strings.HasPrefix(r.URL.Path, "/v1/networking/claim/") || r.URL.Path == "/v1/setup/approval") {
			pending, proof, setupErr := id.SetupClaimAuthority(r.Context(), secret)
			if setupErr == nil {
				resume, _ := json.Marshal(claimResume{SetupOperationID: pending.RequestID, SetupRevision: pending.Revision, SetupProof: proof})
				actor := networking.ClaimRequestOwner{Owner: networking.LocalOwner{SetupOperationID: pending.RequestID, Epoch: pending.Revision}, WebOrigin: d.setupWebOrigin(r), SetupStart: &networking.SetupStartAuthority{RequestID: pending.RequestID, Revision: pending.Revision, CaptureTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
					return networkingOwnerStartError(id.CaptureSetupClaimTx(ctx, tx, pending, proof, original, start, audience))
				}, RenewTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
					return networkingOwnerStartError(id.RenewSetupClaimTx(ctx, tx, pending, proof, original, start, audience))
				}, BindTx: func(ctx context.Context, tx *sql.Tx, operation, transaction string) error {
					return networkingOwnerStartError(id.BindSetupClaimTransactionTx(ctx, tx, pending, proof, operation, transaction))
				}}, Resume: string(resume), Guard: func(ctx context.Context, tx *sql.Tx) error {
					return id.CheckSetupClaimTx(ctx, tx, pending.RequestID, pending.Revision, proof)
				}}
				if d.Hosted != nil && d.Networking != nil && actor.WebOrigin != "" {
					actor.ResolveSetupReturn = func(ctx context.Context) error {
						err := d.Hosted.PrepareSetupCompletion(ctx, d.Networking, secret, actor.WebOrigin)
						return networkingOwnerStartError(err)
					}
				}
				return actor, nil
			}
		}

		if e != nil || p.Authority != "local" || p.Role != "owner" {
			return networking.ClaimRequestOwner{}, identity.ErrUnauthorized
		}
		resume, _ := json.Marshal(claimResume{Digest: p.Hash, AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: p.Epoch})
		actor := networking.ClaimRequestOwner{Owner: networking.LocalOwner{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: int64(p.Epoch)}, WebOrigin: d.ownerSwitchWebOrigin(r), Guard: func(ctx context.Context, tx *sql.Tx) error {
			e := id.CheckClaimRequestOwnerTx(ctx, tx, p)
			if errors.Is(e, identity.ErrUnauthorized) || errors.Is(e, identity.ErrForbidden) {
				return networking.ErrClaimOwnerRequired
			}
			return e
		}, Resume: string(resume), CancelOwnerReturnTx: func(ctx context.Context, tx *sql.Tx, operation string) error {
			return networkingOwnerStartError(id.CancelOwnerReturnTx(ctx, tx, p, operation))
		}, OwnerStart: &networking.OwnerStartAuthority{
			ExpiresAt: p.ExpiresAt,
			CaptureTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed) error {
				return networkingOwnerStartError(id.CaptureOwnerClaimTx(ctx, tx, p, original, start, audience))
			},
			CaptureWithCountTx: func(ctx context.Context, tx *sql.Tx, audience string, original, start claimweb.Signed, sign networking.OwnerSwitchCountSign) (*claimweb.Signed, error) {
				proof, err := id.CaptureOwnerClaimWithCountTx(ctx, tx, p, original, start, audience, identity.OwnerSwitchCountSigner(sign))
				return proof, networkingOwnerStartError(err)
			},
			BindTx: func(ctx context.Context, tx *sql.Tx, operation, transaction string) error {
				return networkingOwnerStartError(id.BindOwnerClaimTransactionTx(ctx, tx, p, operation, transaction))
			},
		}}
		if d.Hosted != nil && d.Networking != nil && actor.WebOrigin != "" {
			actor.ResolveOwnerReturn = func(ctx context.Context) error {
				_, err := d.Hosted.PrepareOwnerCompletion(ctx, d.Networking, secret, actor.WebOrigin)
				return networkingOwnerStartError(err)
			}
		}
		return actor, nil
	}
}

// NetworkingSetupReturnResolver rechecks an existing server-private resume
// note. No public caller can turn an attached boolean or operation ID into this
// capability, and only the captured setup registration is prepared.
func NetworkingSetupReturnResolver(id *identity.Service, control *hosted.Service, handler *networking.ClaimHandler) func(context.Context, string, string) error {
	return func(ctx context.Context, note, operation string) error {
		var saved claimResume
		if id == nil || control == nil || json.Unmarshal([]byte(note), &saved) != nil || saved.SetupOperationID == "" || saved.SetupRevision < 1 || saved.SetupProof == "" || saved.Digest != "" || saved.AccountID != "" || saved.ProfileID != "" || saved.Epoch != 0 {
			return networking.ErrClaimOwnerRequired
		}
		pending := identity.PendingAccountSetup{ServerID: id.ID(), RequestID: saved.SetupOperationID, Revision: saved.SetupRevision, AuthMode: "hosted", ClaimRequired: true}
		err := control.PrepareCapturedSetupCompletion(ctx, handler, pending, saved.SetupProof, operation)
		return networkingOwnerStartError(err)
	}
}

func networkingOwnerStartError(err error) error {
	switch {
	case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrSignInKindLocal), errors.Is(err, identity.ErrSignInKindPortico):
		return networking.ErrClaimOwnerRequired
	case errors.Is(err, identity.ErrConflict), errors.Is(err, identity.ErrProfileChanged):
		return networking.ErrStale
	case errors.Is(err, identity.ErrMoveCollision):
		return networking.ErrOwnerCollision
	default:
		return err
	}
}

// ownerSwitchWebOrigin is only a transport prerequisite. The browser must
// independently require its secure context and protected storage before any
// private state. A bearer or missing Origin is not native-context attestation.
func (d Dependencies) ownerSwitchWebOrigin(r *http.Request) string {
	origin := networkpolicy.Origin(r, d.trustedProxyPrefixes(r.Context()))
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if supplied := r.Header.Get("Origin"); supplied != "" && supplied != origin {
		return ""
	}
	if u.Scheme == "https" && d.secureTransport(r) {
		return origin
	}
	peer, ok := networkpolicy.Peer(r)
	if u.Scheme == "http" && u.Hostname() == "localhost" && ok && peer.IsLoopback() {
		return origin
	}
	return ""
}

// setupWebOrigin is the original address a first-run setup claim starts from and returns
// to (S1/S3/S5): the exact HTTPS origin on a secure transport, or plain HTTP from a local
// source to an address this server admits for setup (claimweb.SetupStartAddress).
func (d Dependencies) setupWebOrigin(r *http.Request) string {
	origin := networkpolicy.Origin(r, d.trustedProxyPrefixes(r.Context()))
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if supplied := r.Header.Get("Origin"); supplied != "" && supplied != origin {
		return ""
	}
	if u.Scheme == "https" && d.secureTransport(r) && d.browserSetupAuthorityAllowed(r) {
		return origin
	}
	if u.Scheme == "http" && d.setupSourceAllowed(r) && d.browserSetupAuthorityAllowed(r) && claimweb.SetupStartAddress(origin) {
		return origin
	}
	return ""
}

// claimResume notes the owner sign-in that asked for a web approval: the
// digest the session store already keeps for its current token, and who it
// belonged to.
type claimResume struct {
	SetupOperationID string `json:"setupOperationId,omitempty"`
	SetupRevision    int64  `json:"setupRevision,omitempty"`
	SetupProof       string `json:"setupProof,omitempty"`
	Digest           string `json:"digest"`
	AccountID        string `json:"accountId"`
	ProfileID        string `json:"profileId"`
	Epoch            int    `json:"epoch"`
}

// NetworkingClaimResume rebuilds, after a restart, the guard of the owner who
// asked for a pending web approval: it holds while that same sign-in is still
// current and still the local owner's, exactly as the guard it replaces.
func NetworkingClaimResume(id *identity.Service) func(string) networking.ClaimRequestGuard {
	return func(note string) networking.ClaimRequestGuard {
		var r claimResume
		if id == nil || json.Unmarshal([]byte(note), &r) != nil {
			return nil
		}
		if r.SetupOperationID != "" && r.SetupProof != "" && r.SetupRevision > 0 {
			return func(ctx context.Context, tx *sql.Tx) error {
				return id.CheckSetupClaimTx(ctx, tx, r.SetupOperationID, r.SetupRevision, r.SetupProof)
			}
		}
		if r.Digest == "" {
			return nil
		}
		return func(ctx context.Context, tx *sql.Tx) error {
			p, e := id.ClaimRequestOwnerByDigestTx(ctx, tx, r.Digest)
			if errors.Is(e, identity.ErrUnauthorized) || e == nil && (p.Authority != "local" || p.Role != "owner" || p.AccountID != r.AccountID || p.ProfileID != r.ProfileID || p.Epoch != r.Epoch) {
				return networking.ErrClaimOwnerRequired
			}
			if e != nil {
				return e
			}
			return id.CheckClaimRequestOwnerTx(ctx, tx, p)
		}
	}
}
