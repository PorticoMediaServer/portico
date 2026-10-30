package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// accessRoutes registers the people and access surface.
//
// Tier: every /v1/admin/access route needs at least the admin tier, except the
// API key routes, which are owner-only because a full-scope key is a second
// credential for the whole server and belongs with the other security controls.
// Invitation acceptance is unauthenticated: the invitee has no account yet, so
// the invitation code is the whole authority and the route is rate-limited like
// the sign-in routes.
func (d Dependencies) accessRoutes(mux *http.ServeMux) {
	d.memberProfilePictureRoutes(mux)
	store := d.Access.Access
	if store == nil || d.Identity == nil {
		return
	}
	d.httpHandle(mux, "POST /v1/admin/access/members/bulk", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var q access.MemberBulkRequest
		if e = decode(w, r, &q); e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.BulkMembers(r.Context(), d.accessAuthority(p, identity.TierAdmin), d.Identity, p, q)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/admin/access/members", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.Members(r.Context(), d.accessReadAuthority(&p, identity.TierAdmin), access.MemberQuery{Cursor: r.URL.Query().Get("cursor"), Limit: r.URL.Query().Get("limit"), Search: r.URL.Query().Get("q"), Role: r.URL.Query().Get("role"), Status: r.URL.Query().Get("status"), Sort: r.URL.Query().Get("sort"), Direction: r.URL.Query().Get("direction")})
		if e != nil {
			accessFailure(w, e)
			return
		}
		for i := range out.Items {
			if p.Role != identity.TierOwner && !identity.ManagesTier(p.Role, out.Items[i].Role) {
				out.Items[i].Notes = nil
			}
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/admin/access/members/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.Member(r.Context(), d.accessReadAuthority(&p, identity.TierAdmin), r.PathValue("id"))
		if e != nil {
			accessFailure(w, e)
			return
		}
		if p.Role != identity.TierOwner && !identity.ManagesTier(p.Role, out.Role) {
			out.Notes = nil
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PUT /v1/admin/access/members/{id}/role", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body access.RoleChange
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := store.SetRole(r.Context(), d.accessAuthority(p, identity.TierAdmin), p, r.PathValue("id"), body)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/admin/access/members/{id}/limits", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.Limits(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), r.PathValue("id"))
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PUT /v1/admin/access/members/{id}/limits", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body access.LimitsChange
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := store.SetLimits(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), r.PathValue("id"), body)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	// Another person's profiles, as People's Manage edits them. Every route
	// proves, inside its own transaction, that the caller's tier manages the
	// person's; the owner's own profiles are managed from Profile & account.
	d.httpHandle(mux, "GET /v1/admin/access/members/{id}/profiles", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.MemberProfiles(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), r.PathValue("id"))
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PATCH /v1/admin/access/members/{id}/profiles/{profileId}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body identity.MemberProfileEdit
		if e = decode(w, r, &body); e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.EditMemberProfile(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), r.PathValue("id"), r.PathValue("profileId"), body)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PUT /v1/admin/access/members/{id}/profiles/{profileId}/restrictions", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body identity.ProfileRestrictionEdit
		if e = decode(w, r, &body); e != nil {
			accessFailure(w, e)
			return
		}
		// Who can record is the owner's: an administrator managing this person can
		// switch Recordings off but never on.
		out, e := store.SetMemberProfileRestrictions(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), r.PathValue("id"), r.PathValue("profileId"), body, p.Authority == "local" && p.Role == "owner")
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/admin/access/members/{id}/profiles/{profileId}/pin-reset", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body identity.MemberProfilePINReset
		if e = decode(w, r, &body); e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.ClearMemberProfilePIN(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), d.Identity, r.PathValue("id"), r.PathValue("profileId"), body)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/admin/access/invitations", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		query := r.URL.Query()
		out, total, matched, e := store.InvitationTable(r.Context(), d.accessReadAuthority(&p, identity.TierAdmin), p, access.InvitationQuery{Cursor: query.Get("cursor"), Limit: query.Get("limit"), State: query.Get("state")}, query.Get("q"), query.Get("sort"), query.Get("direction"))
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, access.InvitationTablePage{InvitationPage: out, TotalCount: total, MatchedCount: matched})
	})
	d.httpHandle(mux, "GET /v1/admin/access/invitations/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.Invitation(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), true), r.PathValue("id"))
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/admin/access/invitations", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body access.InvitationRequest
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := store.Invite(r.Context(), d.accessAuthority(p, identity.TierAdmin), p, body)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 201, out)
	})
	d.httpHandle(mux, "POST /v1/admin/access/invitations/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body access.InvitationRevoke
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := store.RevokeInvitation(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), true), r.PathValue("id"), body)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})

	d.httpHandle(mux, "PUT /v1/admin/access/members/{id}/notes", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var q access.MemberNotesChange
		if e = decodeLegacyRevision(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := store.SetMemberNotes(r.Context(), d.accessAuthority(p, identity.TierAdmin), p, r.PathValue("id"), q)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PUT /v1/admin/access/invitations/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var q access.InvitationChange
		if e = decodeLegacyRevision(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := store.EditInvitation(r.Context(), d.accessAuthority(p, identity.TierAdmin), p, r.PathValue("id"), q)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/admin/access/invitations/{id}/link", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var q access.InvitationLinkRequest
		if e = decodeLegacyRevision(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := store.PendingInvitationLink(r.Context(), d.accessAuthority(p, identity.TierAdmin), p, r.PathValue("id"), q)
		if e != nil {
			accessFailure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/admin/access/invitations/{id}/short-code", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var q access.InvitationShortRequest
		if e = decodeLegacyRevision(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := store.IssueShortInvitation(r.Context(), d.accessAuthority(p, identity.TierAdmin), p, r.PathValue("id"), q)
		if e != nil {
			switch {
			case errors.Is(e, access.ErrExpired):
				policyError(w, "invitation_not_found")
			case errors.Is(e, access.ErrConflict):
				policyError(w, "revision_mismatch")
			case errors.Is(e, access.ErrIdempotencyKeyReused):
				policyError(w, "idempotency_key_reused")
			default:
				accessFailure(w, e)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/access/invitations/accept", func(w http.ResponseWriter, r *http.Request) {
		var body InvitationAcceptRequest
		if e := decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		// The same limiter the sign-in routes use: a code is a secret, and an
		// unauthenticated route that creates accounts must not be brute-forced.
		if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "invitation", 10); e != nil {
			setupFailure(w, e)
			return
		}
		if body.PorticoIdentity != nil {
			if d.Hosted == nil || body.Password != "" {
				failure(w, identity.ErrUnauthorized)
				return
			}
			who, e := d.Hosted.VerifyIdentity(r.Context(), *body.PorticoIdentity, r.Header.Get("X-Portico-Installation-Id"))
			if e != nil {
				failure(w, e)
				return
			}
			out, e := store.AcceptPortico(r.Context(), access.PorticoAcceptance{Code: body.Code, ShortCode: body.ShortCode, AccountID: who.AccountID, Username: who.Username, DisplayName: who.DisplayName, Consent: who.Consent}, func(ctx context.Context, tx *sql.Tx) (identity.DirectSignIn, error) {
				return d.Identity.PorticoSignInTx(ctx, tx, who)
			})
			if e != nil {
				if errors.Is(e, access.ErrExpired) || errors.Is(e, access.ErrInvalid) || errors.Is(e, access.ErrInvitationRecipient) {
					accessFailure(w, e)
				} else {
					failure(w, e)
				}
				return
			}
			writeAuth(w, r, 201, porticoAcceptanceBody(out))
			return
		}
		hash, e := access.HashPassword(body.Password)
		if e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.Accept(r.Context(), body.Acceptance, hash)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 201, taggedBody{"variant", "accepted", out})
	})
}

// porticoAcceptanceBody tags a Portico Account acceptance as a signed-in session
// or a second-factor challenge.
func porticoAcceptanceBody(out identity.DirectSignIn) taggedBody {
	kind := "signed-in"
	if out.Challenge != nil {
		kind = "challenge"
	}
	return taggedBody{"variant", kind, out}
}

// The live caller and target tiers are re-read in the transaction performing the operation.
func (d Dependencies) accessTargetAuthority(p identity.Principal, id string, invitation bool) access.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		live, e := d.tierAuthorityTx(ctx, tx, p, identity.TierAdmin)
		if e != nil {
			return e
		}
		var role string
		if invitation {
			e = tx.QueryRowContext(ctx, `SELECT role FROM access_invitations WHERE id=?`, id).Scan(&role)
		} else {
			e = tx.QueryRowContext(ctx, `SELECT role FROM direct_memberships WHERE account_id=?`, id).Scan(&role)
		}
		if e != nil {
			return e
		}
		if !identity.ManagesTier(live.Role, role) {
			return identity.ErrForbidden
		}
		return nil
	}
}

// InvitationAcceptRequest publishes the two actual authentication methods.
type InvitationAcceptRequest struct {
	access.Acceptance
	PorticoIdentity *hosted.Signed `json:"porticoIdentity,omitempty"`
}

// The disjoint wire forms reject a second capability by presence, including
// empty or null, and reject local credential fields on a Portico assertion.
func (q *InvitationAcceptRequest) UnmarshalJSON(raw []byte) error {
	fields, err := access.InvitationCapabilityFields(raw)
	if err != nil {
		return err
	}
	_, portico := fields["porticoIdentity"]
	for field := range fields {
		if field == "code" || field == "shortCode" || portico && field == "porticoIdentity" || !portico && (field == "username" || field == "password" || field == "name") {
			continue
		}
		return errors.New("invalid invitation acceptance field")
	}
	type wire InvitationAcceptRequest
	var value wire
	if err = json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if portico && value.PorticoIdentity == nil {
		return errors.New("invalid invitation identity")
	}
	*q = InvitationAcceptRequest(value)
	return nil
}
func (InvitationAcceptRequest) JSONSchema() map[string]any {
	local := (access.Acceptance{}).JSONSchema()["oneOf"].([]any)
	porticoLong := access.InvitationCapabilityShape(reflect.TypeFor[struct {
		Code            string        `json:"code"`
		PorticoIdentity hosted.Signed `json:"porticoIdentity"`
	}]())
	porticoShort := access.InvitationCapabilityShape(reflect.TypeFor[struct {
		ShortCode       string        `json:"shortCode"`
		PorticoIdentity hosted.Signed `json:"porticoIdentity"`
	}]())
	return map[string]any{"oneOf": append(local, porticoLong, porticoShort)}
}

type InvitationAcceptanceResponse json.RawMessage

// A populated snapshot is a successful sign-in only with its session. The
// challenge and pending-device producers leave snapshot slices nil; their
// explicit shapes describe only the authentication state clients may consume.
type invitationSignedIn struct {
	identity.DirectSnapshot
	Session                identity.Envelope `json:"session"`
	PasswordChangeRequired bool              `json:"passwordChangeRequired,omitempty"`
	DeviceID               string            `json:"deviceId,omitempty"`
	InstallationID         string            `json:"installationId,omitempty"`
}
type invitationChallenge struct {
	identity.DirectSnapshot
	Challenge identity.SignInChallenge `json:"challenge"`
}

func (InvitationAcceptanceResponse) JSONSchema() map[string]any {
	variants := []reflect.Type{reflect.TypeFor[access.AcceptanceResult](), reflect.TypeFor[invitationSignedIn](), reflect.TypeFor[invitationChallenge]()}
	kinds := []string{"accepted", "signed-in", "challenge"}
	shapes := make([]any, 0, len(variants))
	for i, typ := range variants {
		shape, e := apikit.Schema(typ)
		if e != nil {
			panic(e)
		}
		withTag(shape, "variant", kinds[i])
		if typ == reflect.TypeFor[invitationChallenge]() {
			// These producers marshal a zero snapshot, including nil slices.
			// Keep those fields explicit without allowing null in signed-in data.
			properties := shape["properties"].(map[string]any)
			properties["profiles"] = map[string]any{"type": "null"}
			account := properties["account"].(map[string]any)["properties"].(map[string]any)
			account["allowedLibraries"] = map[string]any{"type": "null"}
		}
		shapes = append(shapes, shape)
	}
	// Presentation documents allow additive fields, so a future response can
	// carry both a challenge and pending-device facts. Deliberate anyOf avoids
	// rejecting that overlap while retaining each complete producer shape.
	return map[string]any{"anyOf": shapes}
}

// Keep response redaction tied to the exact read snapshot's live authority.
func (d Dependencies) accessReadAuthority(p *identity.Principal, needed string) access.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		live, e := d.tierAuthorityTx(ctx, tx, *p, needed)
		if e == nil {
			*p = live
		}
		return e
	}
}
