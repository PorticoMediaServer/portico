package access

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func issueShortFixture(t *testing.T) (*Store, *sql.DB, Invitation, InvitationShortResponse) {
	t.Helper()
	s, db, _ := localFixture(t)
	invitation, err := s.Invite(context.Background(), nil, owner(), InvitationRequest{OperationID: "short-invite", Role: identity.TierMember})
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.IssueShortInvitation(context.Background(), nil, owner(), invitation.ID, InvitationShortRequest{ExpectedRevision: invitation.Revision, OperationID: "short-issue"})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, invitation, code
}
func TestShortInvitationOneUseSharesTheActualLongInvitation(t *testing.T) {
	for _, firstShort := range []bool{false, true} {
		t.Run(map[bool]string{false: "long first", true: "short first"}[firstShort], func(t *testing.T) {
			s, db, inv, short := issueShortFixture(t)
			ctx := context.Background()
			if _, err := s.PreviewCapability(ctx, InvitationPreviewRequest{ShortCode: strings.ToLower(short.ShortCode)}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Preview(ctx, inv.Code); err != nil {
				t.Fatal(err)
			}
			hash, err := HashPassword("Correct-Horse-9")
			if err != nil {
				t.Fatal(err)
			}
			first, second := Acceptance{Code: inv.Code}, Acceptance{ShortCode: short.ShortCode}
			if firstShort {
				first, second = second, first
			}
			first.Username = "guest"
			first.Password = "Correct-Horse-9"
			if _, err = s.Accept(ctx, first, hash); err != nil {
				t.Fatal(err)
			}
			second.Username = "another_guest"
			second.Password = "Correct-Horse-9"
			if _, err = s.Accept(ctx, second, hash); !errors.Is(err, ErrExpired) {
				t.Fatalf("second capability accepted: %v", err)
			}
			if _, err = s.PreviewCapability(ctx, InvitationPreviewRequest{ShortCode: short.ShortCode}); !errors.Is(err, ErrInvitationNotFound) {
				t.Fatal(err)
			}
			var n int
			if err = db.QueryRow(`SELECT count(*) FROM access_invitation_short_codes`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("consumed short row: %d %v", n, err)
			}
		})
	}
}
func TestShortInvitationReplayDoesNotRevealOrRotateAndNewCodeRevokes(t *testing.T) {
	s, db, inv, first := issueShortFixture(t)
	ctx := context.Background()
	replay, err := s.IssueShortInvitation(ctx, nil, owner(), inv.ID, InvitationShortRequest{ExpectedRevision: inv.Revision, OperationID: "short-issue"})
	if err != nil || replay.ShortCode != "" {
		t.Fatalf("receipt replay: %+v %v", replay, err)
	}
	if _, err = s.PreviewCapability(ctx, InvitationPreviewRequest{ShortCode: first.ShortCode}); err != nil {
		t.Fatal(err)
	}
	second, err := s.IssueShortInvitation(ctx, nil, owner(), inv.ID, InvitationShortRequest{ExpectedRevision: inv.Revision, OperationID: "short-new"})
	if err != nil || second.ShortCode == first.ShortCode {
		t.Fatal("new code did not rotate", err)
	}
	if _, err = s.PreviewCapability(ctx, InvitationPreviewRequest{ShortCode: first.ShortCode}); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatal("old code retained", err)
	}
	if _, err = s.Preview(ctx, inv.Code); err != nil {
		t.Fatal("short rotation changed long capability", err)
	}
	var body, lookup, binding string
	if err = db.QueryRow(`SELECT body FROM access_receipts WHERE scope=? AND operation_id='short-new'`, "invite-short:"+inv.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT lookup_hash,binding_hash FROM access_invitation_short_codes WHERE invitation_id=?`, inv.ID).Scan(&lookup, &binding); err != nil {
		t.Fatal(err)
	}
	canonical, _ := canonicalShortInvitationCode(second.ShortCode)
	if strings.Contains(body, canonical) || strings.Contains(body, second.ShortCode) || len(lookup) != 64 || len(binding) != 64 || lookup == binding {
		t.Fatal("plaintext or unbound hash stored")
	}
	expiry, _ := time.Parse(time.RFC3339Nano, second.ExpiresAt)
	if expiry.After(s.Now().Add(10 * time.Minute)) {
		t.Fatal("short expiry too long")
	}
}
func TestShortInvitationRefusesChangedAuthorityAndBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	mutations := map[string]string{
		"edited":          `UPDATE access_invitations SET revision=revision+1`,
		"revoked":         `UPDATE access_invitations SET state='revoked'`,
		"expired":         `UPDATE access_invitation_short_codes SET expires_ms=1`,
		"binding":         `UPDATE access_invitation_short_codes SET binding_hash=printf('%064d',0)`,
		"wrong server":    `UPDATE access_invitation_short_codes SET server_id='srv_foreign'`,
		"wrong kind":      `UPDATE access_invitation_short_codes SET sign_in_kind='portico'`,
		"wrong revision":  `UPDATE access_invitation_short_codes SET invitation_revision=invitation_revision+1`,
		"server reset":    `UPDATE networking_claim_identity SET reset_generation=reset_generation+1`,
		"kind round trip": `UPDATE accounts SET password_hash=x'';UPDATE identity_state SET sign_in_kind='portico';UPDATE identity_state SET sign_in_kind='local'`,
	}
	for name, statement := range mutations {
		t.Run(name, func(t *testing.T) {
			s, db, _, short := issueShortFixture(t)
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PreviewCapability(context.Background(), InvitationPreviewRequest{ShortCode: short.ShortCode}); !errors.Is(err, ErrInvitationNotFound) {
				t.Fatalf("stale capability: %v", err)
			}
		})
	}
}
func TestShortInvitationPorticoGrantRollsBackWithItsActualSignIn(t *testing.T) {
	s, db, _ := localFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE accounts SET password_hash=x'';UPDATE identity_state SET sign_in_kind='portico'`); err != nil {
		t.Fatal(err)
	}
	inv, err := s.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "portico-invite", Role: identity.TierMember, PorticoUsername: "portico_guest"})
	if err != nil {
		t.Fatal(err)
	}
	short, err := s.IssueShortInvitation(ctx, nil, owner(), inv.ID, InvitationShortRequest{ExpectedRevision: inv.Revision, OperationID: "portico-short"})
	if err != nil || short.ServerID == "" {
		t.Fatal("Portico server binding missing", err)
	}
	fail := errors.New("actual sign-in refused")
	q := PorticoAcceptance{ShortCode: short.ShortCode, AccountID: "hosted_guest", Username: "portico_guest", DisplayName: "Guest"}
	if _, err = s.AcceptPortico(ctx, q, func(context.Context, *sql.Tx) (identity.DirectSignIn, error) { return identity.DirectSignIn{}, fail }); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if _, err = s.PreviewCapability(ctx, InvitationPreviewRequest{ShortCode: short.ShortCode}); err != nil {
		t.Fatal("failed admission consumed capability", err)
	}
	if _, err = s.AcceptPortico(ctx, q, func(context.Context, *sql.Tx) (identity.DirectSignIn, error) { return identity.DirectSignIn{}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Preview(ctx, inv.Code); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatal("Portico consume left long code live", err)
	}
}
func TestShortInvitationWireCapabilityIsDisjointByPresence(t *testing.T) {
	for _, raw := range []string{`{}`, `{"shortCode":"0123456789ABCDEF","code":""}`, `{"shortCode":"0123456789ABCDEF","code":null}`, `{"code":"` + strings.Repeat("a", 43) + `","shortCode":""}`, `{"code":"` + strings.Repeat("a", 43) + `","shortCode":null}`, `{"shortCode":null}`} {
		var q InvitationPreviewRequest
		if json.Unmarshal([]byte(raw), &q) == nil {
			t.Fatalf("accepted mixed/invalid form: %s", raw)
		}
	}
	for _, raw := range []string{`{"shortCode":"0123-4567-89AB-CDEF"}`, `{"code":"` + strings.Repeat("a", 43) + `"}`} {
		var q InvitationPreviewRequest
		if err := json.Unmarshal([]byte(raw), &q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShortInvitationExpiryNeverOutlivesTheInvitation(t *testing.T) {
	s, db, inv, _ := issueShortFixture(t)
	ctx := context.Background()
	expires := s.now() + 30_000
	if _, err := db.Exec(`UPDATE access_invitations SET expires_ms=? WHERE id=?`, expires, inv.ID); err != nil {
		t.Fatal(err)
	}
	short, err := s.IssueShortInvitation(ctx, nil, owner(), inv.ID, InvitationShortRequest{ExpectedRevision: inv.Revision, OperationID: "short-expiry"})
	if err != nil {
		t.Fatal(err)
	}
	until, err := time.Parse(time.RFC3339Nano, short.ExpiresAt)
	if err != nil || until.UnixMilli() > expires {
		t.Fatal("short expiry exceeded invitation", err)
	}
	s.Now = func() time.Time { return time.UnixMilli(expires + 1) }
	if _, err = s.PreviewCapability(ctx, InvitationPreviewRequest{ShortCode: short.ShortCode}); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatal("expired code accepted", err)
	}
}
