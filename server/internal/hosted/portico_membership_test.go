package hosted

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

func identityNonce() string {
	b := make([]byte, 18)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// assertion signs an identity claim with the fixture's Hosted key, the way
// Hosted's POST /v1/servers/{id}/identity does.
func assertion(f *claimFixture, c IdentityClaims) Signed {
	raw, _ := json.Marshal(c)
	return Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.hostedKey, raw)), KeyID: "pin1", Certificate: testSigningCertificate(f.hostedKey, "pin1")}
}

const testInstallation = "installation-for-tests"

// freshClaims asks the server for a challenge, as a client does, and builds
// the assertion Hosted would sign for it.
func freshClaims(f *claimFixture, account string) IdentityClaims {
	challenge, _, e := f.service.IssueChallenge(context.Background(), testInstallation)
	if e != nil {
		panic(e)
	}
	now := time.Now().UTC()
	return IdentityClaims{Kind: identityKind, AccountID: account, Username: "member", DisplayName: "Member", ServerID: f.id.ID(), Challenge: challenge, Nonce: identityNonce(), IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(5 * time.Minute).Format(time.RFC3339Nano)}
}

func verify(f *claimFixture, c IdentityClaims) (identity.PorticoIdentity, error) {
	return f.service.VerifyIdentity(context.Background(), assertion(f, c), testInstallation)
}

func addLinkedMember(t *testing.T, f *claimFixture, local, hosted string) {
	t.Helper()
	if _, e := f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,x'',?)`, local, local, local+"-profile"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,?)`, local, hosted); e != nil {
		t.Fatal(e)
	}
}

// The server admits a Portico Account from its own table after checking the
// Hosted assertion offline, and nothing else: a stranger with a perfectly good
// assertion is refused, a replayed or misdirected assertion is not an identity.
func TestPorticoIdentityAdmitsOnlyMembers(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	addLinkedMember(t, f, "local-m", "acc_member")

	c := freshClaims(f, "acc_member")
	signed := assertion(f, c)
	who, e := f.service.VerifyIdentity(ctx, signed, testInstallation)
	if e != nil || who.AccountID != "acc_member" {
		t.Fatal("valid assertion refused", who, e)
	}
	if _, e = f.service.VerifyIdentity(ctx, signed, testInstallation); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("replayed assertion accepted", e)
	}
	for name, mutate := range map[string]func(*IdentityClaims){
		"other server":    func(c *IdentityClaims) { c.ServerID = "srv_other" },
		"too long":        func(c *IdentityClaims) { c.ExpiresAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano) },
		"wrong kind":      func(c *IdentityClaims) { c.Kind = "portico.profile-selection.v1" },
		"short nonce":     func(c *IdentityClaims) { c.Nonce = "abc" },
		"no challenge":    func(c *IdentityClaims) { c.Challenge = "" },
		"other challenge": func(c *IdentityClaims) { c.Challenge = identityNonce() + identityNonce() },
	} {
		bad := freshClaims(f, "acc_member")
		mutate(&bad)
		if _, e = f.service.VerifyIdentity(ctx, assertion(f, bad), testInstallation); !errors.Is(e, identity.ErrUnauthorized) {
			t.Fatal(name, "accepted", e)
		}
	}
	forged := assertion(f, freshClaims(f, "acc_member"))
	forged.Signature = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	if _, e = f.service.VerifyIdentity(ctx, forged, testInstallation); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("unsigned assertion accepted", e)
	}

	in, e := f.id.PorticoSignIn(ctx, who)
	if e != nil || in.Session == nil || in.Account.HostedAccountID != "acc_member" || in.Session.Viewer.Authority != "local" || in.Session.Viewer.Role != identity.TierMember {
		t.Fatalf("member sign-in %+v %v", in, e)
	}
	if _, e = f.id.Authenticate(in.Session.AccessToken); e != nil {
		t.Fatal("member session unusable", e)
	}
	if _, e = f.id.PorticoSignIn(ctx, identity.PorticoIdentity{AccountID: "acc_stranger"}); !errors.Is(e, identity.ErrAccessRefused) {
		t.Fatal("a non-member was admitted", e)
	}

	// Revocation is the server's own act and immediate: the owner disables the
	// member, its session stops working at once, and it cannot sign back in.
	var revision int64
	f.db.QueryRow(`SELECT revision FROM direct_memberships WHERE account_id='local-m'`).Scan(&revision)
	// Nor can anyone give a Portico member a password.
	f.db.QueryRow(`SELECT revision FROM direct_memberships WHERE account_id='local-m'`).Scan(&revision)
	if _, e = f.id.MakeMemberResetCode(ctx, f.owner.AccessToken, "local-m", identity.MemberResetRequest{ExpectedRevision: revision}); !errors.Is(e, identity.ErrSignInKindPortico) {
		t.Fatal("password set on a Portico member", e)
	}
	if e = f.id.UpdateDirectMember(ctx, f.owner.AccessToken, "local-m", identity.DirectMemberInput{ExpectedRevision: revision, Disabled: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.id.Authenticate(in.Session.AccessToken); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("a removed member's session still works", e)
	}
	again, _ := verify(f, freshClaims(f, "acc_member"))
	if _, e = f.id.PorticoSignIn(ctx, again); !errors.Is(e, identity.ErrAccessRefused) {
		t.Fatal("a removed member signed back in", e)
	}
	var journalled int
	f.db.QueryRow(`SELECT count(*) FROM hosted_membership_journal WHERE hosted_account_id='acc_member'`).Scan(&journalled)
	if journalled < 2 {
		t.Fatal("membership changes were not journalled for Hosted", journalled)
	}

}

// A session that migration 0120 ended refreshes to session_migrated, not to a
// plain refusal, so a client re-admits silently instead of showing a sign-out.
func TestMigratedFamilyRefreshSaysMigrated(t *testing.T) {
	f := newClaimFixture(t)
	if _, e := f.db.Exec(`INSERT INTO hosted_migrated_families(family_id,refresh_hash) SELECT family_id,token_hash FROM identity_refresh_credentials WHERE family_id=?; UPDATE authorization_session_families SET revoked=1,revoked_reason='membership_removed',revoked_at='2026-09-23T00:00:00Z' WHERE id=?`, f.owner.SessionFamilyID, f.owner.SessionFamilyID); e != nil {
		t.Fatal(e)
	}
	_, e := f.id.RefreshSession(context.Background(), f.owner.RefreshToken, f.owner.InstallationID, "request-1")
	if !errors.Is(e, identity.ErrSessionMigrated) {
		t.Fatal("migrated family did not say so", e)
	}
}

// fakeIndex is Hosted's side of the membership push, with Hosted's rules:
// apply on top of the held sequence, treat an exact replay as done, answer
// anything else with applied=false.
type fakeIndex struct {
	mu       sync.Mutex
	sequence int64
	members  map[string]string
	pushes   []MemberPush
	checkIns int
	// Hosted's consent rule, simplified: an account it has not listed yet
	// needs a consent; it is skipped otherwise.
	requireConsent bool
	departures     []Departure
	// failPushes answers that many member pushes 413; sizes records bodies.
	failPushes int
	sizes      []int
	// custodian is what Hosted says holds custody of the claim.
	custodian string
}

func (x *fakeIndex) RoundTrip(r *http.Request) (*http.Response, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	answer := func(v any) (*http.Response, error) {
		raw, _ := json.Marshal(v)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)), Request: r}, nil
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/members"):
		var p MemberPush
		if e := json.Unmarshal(body, &p); e != nil {
			return nil, e
		}
		x.sizes = append(x.sizes, len(body))
		if x.failPushes > 0 {
			x.failPushes--
			return &http.Response{StatusCode: 413, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"payload_too_large","message":"Too large.","retryable":false}}`)), Request: r}, nil
		}
		x.pushes = append(x.pushes, p)
		switch {
		case p.Full && p.Page == 0:
			x.members = map[string]string{}
		case p.Full:
		case p.After == x.sequence:
		case p.Through == x.sequence:
			return answer(MemberPushResult{Sequence: x.sequence, Applied: true, Custodian: x.custodian})
		default:
			return answer(MemberPushResult{Sequence: x.sequence})
		}
		var skipped []string
		for _, m := range p.Members {
			_, listed := x.members[m.AccountID]
			switch {
			case m.Active && x.requireConsent && !listed && len(m.Consent) == 0 && m.Role != identity.TierOwner:
				skipped = append(skipped, m.AccountID)
			case m.Active:
				x.members[m.AccountID] = m.Role
			default:
				delete(x.members, m.AccountID)
			}
		}
		if p.More {
			return answer(MemberPushResult{Sequence: x.sequence, Applied: true, Skipped: skipped, Custodian: x.custodian})
		}
		x.sequence = p.Through
		return answer(MemberPushResult{Sequence: x.sequence, Applied: true, Skipped: skipped, Custodian: x.custodian})
	case strings.HasSuffix(r.URL.Path, "/heartbeat"):
		x.checkIns++
		departures := x.departures
		x.departures = nil
		return answer(map[string]any{"heartbeatAfterMs": (7 * 24 * time.Hour).Milliseconds(), "presenceExpiresAt": time.Now().Add(8 * 24 * time.Hour), "memberSequence": x.sequence, "memberDigestMatches": true, "departures": departures})
	}
	return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
}

// The push keeps Hosted's index equal to this server's members without Hosted
// ever asking: the first push is the full list (the owner linked by the
// claim), later ones are deltas, and a gap is repaired by a full list.
func TestMembershipPushDeltaAndGapRecovery(t *testing.T) {
	f := newClaimFixture(t)
	index := &fakeIndex{members: map[string]string{}}
	f.service.current.transport.UseRoundTripper(index)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	step := func() {
		t.Helper()
		for range 4 {
			if _, e := controlStep(t, f); e != nil {
				t.Fatal(e)
			}
		}
	}
	step()
	if len(index.pushes) == 0 || !index.pushes[0].Full || index.members["account"] != identity.TierOwner {
		t.Fatalf("first push was not the full list with the claim owner: %+v %v", index.pushes, index.members)
	}
	pushes := len(index.pushes)

	addLinkedMember(t, f, "local-a", "acc_a")
	addLinkedMember(t, f, "local-b", "acc_b")
	step()
	delta := index.pushes[pushes]
	if delta.Full || delta.After == 0 || len(delta.Members) != 2 || index.members["acc_a"] != identity.TierMember || index.members["acc_b"] != identity.TierMember {
		t.Fatalf("delta %+v index %v", delta, index.members)
	}
	var acked, journal int64
	f.db.QueryRow(`SELECT acked,(SELECT count(*) FROM hosted_membership_journal) FROM hosted_membership_sync`).Scan(&acked, &journal)
	if acked != index.sequence || journal != 0 {
		t.Fatal("acknowledged push not recorded", acked, index.sequence, journal)
	}
	// Nothing to say, nothing said.
	pushes = len(index.pushes)
	step()
	if len(index.pushes) != pushes {
		t.Fatal("an idle server pushed")
	}

	// Hosted lost track (a restore on its side): the next delta is refused and
	// the full list replaces what it holds.
	index.mu.Lock()
	index.sequence = 999
	index.members = map[string]string{"acc_stale": "member"}
	index.mu.Unlock()
	if _, e := f.db.Exec(`DELETE FROM accounts WHERE id='local-b'`); e != nil {
		t.Fatal(e)
	}
	step()
	last := index.pushes[len(index.pushes)-1]
	if !last.Full || index.members["acc_b"] != "" || index.members["acc_stale"] != "" || index.members["acc_a"] != identity.TierMember || index.members["account"] != identity.TierOwner {
		t.Fatalf("gap not repaired: %+v %v", last, index.members)
	}
	if index.checkIns != 0 {
		t.Fatal("pushing membership ran a check-in", index.checkIns)
	}
}

// Hosted being down never blocks a membership change and never floods Hosted:
// the push backs off with the shared rule and the journal keeps collecting.
func TestMembershipPushBacksOffWhileHostedIsDown(t *testing.T) {
	f := newClaimFixture(t)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	if _, e := controlStep(t, f); e == nil {
		t.Fatal("an unreachable Hosted reported success")
	}
	var attempts int
	var next int64
	f.db.QueryRow(`SELECT attempts,next_at FROM hosted_membership_sync`).Scan(&attempts, &next)
	if attempts != 1 || time.UnixMilli(next).Before(time.Now().Add(4*time.Second)) {
		t.Fatal("no backoff recorded", attempts, next)
	}
	addLinkedMember(t, f, "local-a", "acc_a")
	f.service.MembershipChanged()
	for range 3 {
		if _, e := controlStep(t, f); e != nil {
			t.Fatal("a step inside the retry window called Hosted", e)
		}
	}
	f.db.QueryRow(`SELECT attempts FROM hosted_membership_sync`).Scan(&attempts)
	if attempts != 1 {
		t.Fatal("the retry deadline was bypassed", attempts)
	}
}

// An owner whose identity is a Portico Account transfers ownership with a
// fresh identity assertion (it has no password here), and the change reaches
// Hosted as an owner-change delta in the ordinary push stream.
func TestPorticoOwnerTransferReachesHosted(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	index := &fakeIndex{members: map[string]string{}}
	f.service.current.transport.UseRoundTripper(index)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	addLinkedMember(t, f, "local-heir", "acc_heir")
	for range 3 {
		if _, e := controlStep(t, f); e != nil {
			t.Fatal(e)
		}
	}
	if index.members["account"] != identity.TierOwner || index.members["acc_heir"] != identity.TierMember {
		t.Fatalf("before %v", index.members)
	}
	var revision int64
	f.db.QueryRow(`SELECT m.revision FROM direct_memberships m WHERE m.role='owner'`).Scan(&revision)
	// A stranger's assertion, or none, does not confirm the owner: a linked-account
	// mismatch is refused (403), never unauthorized (401, which would sign the
	// owner out).
	stranger, _ := verify(f, freshClaims(f, "acc_stranger"))
	if e := f.id.TransferPorticoOwnership(ctx, f.owner.AccessToken, "local-heir", stranger, revision); !errors.Is(e, identity.ErrForbidden) {
		t.Fatal("transfer confirmed by another account", e)
	} else if status, _, _ := identity.Refusal(e); status != 403 {
		t.Fatalf("linked-account mismatch answered %d, want 403", status)
	}
	who, e := verify(f, freshClaims(f, "account"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.id.TransferPorticoOwnership(ctx, f.owner.AccessToken, "local-heir", who, revision); e != nil {
		t.Fatal(e)
	}
	pushes := len(index.pushes)
	for range 3 {
		if _, e = controlStep(t, f); e != nil {
			t.Fatal(e)
		}
	}
	if len(index.pushes) != pushes+1 || index.pushes[pushes].Full {
		t.Fatalf("owner change was not one delta: %+v", index.pushes[pushes:])
	}
	if index.members["acc_heir"] != identity.TierOwner || index.members["account"] != identity.TierMember {
		t.Fatalf("after %v", index.members)
	}
}

// An assertion answers a challenge this server gave the installation that
// presents it; a clock that is off by minutes is tolerated and named when it
// is off by more (INT M7, M8). The accepted assertion is kept as consent.
func TestIdentityAssertionChallengeSkewAndConsent(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	addLinkedMember(t, f, "local-m", "acc_member")
	c := freshClaims(f, "acc_member")
	if _, e := f.service.VerifyIdentity(ctx, assertion(f, c), "another-installation"); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("a challenge answered from another installation", e)
	}
	// The failed attempt did not spend it: the right installation still can.
	skewed := freshClaims(f, "acc_member")
	now := time.Now().UTC()
	skewed.IssuedAt = now.Add(4 * time.Minute).Format(time.RFC3339Nano)
	skewed.ExpiresAt = now.Add(9 * time.Minute).Format(time.RFC3339Nano)
	who, e := verify(f, skewed)
	if e != nil {
		t.Fatal("four minutes of skew refused", e)
	}
	far := freshClaims(f, "acc_member")
	far.IssuedAt = now.Add(20 * time.Minute).Format(time.RFC3339Nano)
	far.ExpiresAt = now.Add(25 * time.Minute).Format(time.RFC3339Nano)
	if _, e = verify(f, far); !errors.Is(e, ErrClockSkew) {
		t.Fatal("a far-off clock not named", e)
	}
	if _, e = f.id.PorticoSignIn(ctx, who); e != nil {
		t.Fatal(e)
	}
	var consent string
	if e = f.db.QueryRow(`SELECT consent FROM account_portico_links WHERE hosted_account_id='acc_member'`).Scan(&consent); e != nil || !strings.Contains(consent, `"payload"`) {
		t.Fatal("consent not kept", consent, e)
	}
}

// An owner linked to a Portico Account who set up a second factor here keeps
// it; and only a session admitted through Portico escapes the recovery
// owner's private-network rule (INT M9).
func TestPorticoSignInKeepsLocalSecondFactor(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	owner := f.owner.Viewer.AccountID
	if _, e := f.db.Exec(`INSERT INTO identity_account_factors(account_id,secret,confirmed,enrolled_at) VALUES(?,'x',1,'t')`, owner); e != nil {
		t.Fatal(e)
	}
	who, e := verify(f, freshClaims(f, "account"))
	if e != nil {
		t.Fatal(e)
	}
	in, e := f.id.PorticoSignIn(ctx, who)
	if e != nil || in.Challenge == nil || in.Session != nil {
		t.Fatalf("second factor skipped: %+v %v", in, e)
	}
	var marked int
	f.db.QueryRow(`SELECT count(*) FROM portico_factor_challenges`).Scan(&marked)
	if marked != 1 {
		t.Fatal("challenge not marked as Portico")
	}
	if _, e = f.db.Exec(`DELETE FROM identity_account_factors`); e != nil {
		t.Fatal(e)
	}
	who, _ = verify(f, freshClaims(f, "account"))
	in, e = f.id.PorticoSignIn(ctx, who)
	if e != nil || in.Session == nil {
		t.Fatalf("Portico sign-in after local factor removed: %v", e)
	}
	if _, e = f.id.AuthenticateContext(ctx, in.Session.AccessToken); e != nil {
		t.Fatal(e)
	}

}

// Hosted lists a member only with its consent; one it skipped is left out of
// the digest until it signs in again. A departure recorded at Hosted ends the
// membership here at the next check-in. A full list goes in pages.
func TestMembershipPushConsentDeparturesAndPages(t *testing.T) {
	f := newClaimFixture(t)
	index := &fakeIndex{members: map[string]string{}, requireConsent: true}
	f.service.current.transport.UseRoundTripper(index)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	step := func() {
		t.Helper()
		for range 8 {
			if _, e := controlStep(t, f); e != nil {
				t.Fatal(e)
			}
		}
	}
	addLinkedMember(t, f, "local-quiet", "acc_quiet")
	step()
	if _, listed := index.members["acc_quiet"]; listed {
		t.Fatal("a member without consent was listed")
	}
	var unindexed int
	f.db.QueryRow(`SELECT unindexed FROM account_portico_links WHERE hosted_account_id='acc_quiet'`).Scan(&unindexed)
	if unindexed != 1 {
		t.Fatal("skipped member not marked")
	}
	digest, _ := f.service.currentMemberDigest(context.Background())
	if digest != MemberDigest([]MemberStanding{{AccountID: "account", Role: identity.TierOwner, Active: true}}) {
		t.Fatal("digest counts a member Hosted declined")
	}
	// It signs in: its consent is recorded and pushed; Hosted lists it.
	who, e := verify(f, freshClaims(f, "acc_quiet"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.id.PorticoSignIn(context.Background(), who); e != nil {
		t.Fatal(e)
	}
	step()
	if index.members["acc_quiet"] != identity.TierMember {
		t.Fatalf("consented member not listed: %v", index.members)
	}
	// It removes the server from its list at Hosted.
	index.mu.Lock()
	index.departures = []Departure{{AccountID: "acc_quiet", LeftAt: time.Now().Add(time.Second)}}
	index.mu.Unlock()
	if _, e = f.db.Exec(`UPDATE networking_control_schedule SET heartbeat_at=0,retry_at=0,attempts=0`); e != nil {
		t.Fatal(e)
	}
	step()
	var disabled int
	f.db.QueryRow(`SELECT m.disabled FROM direct_memberships m JOIN account_portico_links l ON l.account_id=m.account_id WHERE l.hosted_account_id='acc_quiet'`).Scan(&disabled)
	if disabled != 1 || index.members["acc_quiet"] != "" {
		t.Fatalf("departure not applied: disabled=%d index=%v", disabled, index.members)
	}
	// A list longer than a page goes in pages and ends complete.
	index.mu.Lock()
	index.requireConsent = false
	index.mu.Unlock()
	for i := range 450 {
		addLinkedMember(t, f, fmt.Sprintf("local-%03d", i), fmt.Sprintf("acc_%03d", i))
	}
	if e = f.runner.Do(context.Background(), func(ctx context.Context) error { return f.service.requestFullMemberPush(ctx, f.installed) }); e != nil {
		t.Fatal(e)
	}
	pushes := len(index.pushes)
	step()
	pages := index.pushes[pushes:]
	if len(pages) < 3 || !pages[0].Full || pages[0].Page != 0 || !pages[0].More || pages[len(pages)-1].More {
		t.Fatalf("not paged: %d pushes", len(pages))
	}
	if len(index.members) != 451 {
		t.Fatalf("paged list incomplete: %d", len(index.members))
	}
}

// Deltas stay under Hosted's body limit however many members change at once
// and however large their consents are, and a delta Hosted keeps refusing
// gives way to the full list (INT M14).
func TestMembershipDeltasArePagedAndFallBackToTheFullList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newClaimFixture(t)
	index := &fakeIndex{members: map[string]string{}}
	f.service.current.transport.UseRoundTripper(index)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	step := func() {
		t.Helper()
		for range 12 {
			if _, e := controlStep(t, f); e != nil {
				t.Fatal(e)
			}
		}
	}
	step()
	// 450 members join at once, each with a 6 KB consent: 2.7 MB in all.
	consent := `{"payload":"` + strings.Repeat("A", 6000) + `","signature":"s","keyId":"k"}`
	for i := range 450 {
		addLinkedMember(t, f, fmt.Sprintf("local-%03d", i), fmt.Sprintf("acc_%03d", i))
	}
	if _, e := f.db.Exec(`UPDATE account_portico_links SET consent=? WHERE hosted_account_id LIKE 'acc_%'`, consent); e != nil {
		t.Fatal(e)
	}
	pushes := len(index.pushes)
	step()
	deltas := index.pushes[pushes:]
	if len(deltas) < 3 {
		t.Fatalf("450 large members went in %d pushes", len(deltas))
	}
	for i, p := range deltas {
		if p.Full || len(p.Members) > memberPushPage {
			t.Fatalf("push %d: full=%v members=%d", i, p.Full, len(p.Members))
		}
	}
	for _, size := range index.sizes {
		if size >= 1<<20 {
			t.Fatalf("a push body of %d bytes", size)
		}
	}
	if len(index.members) != 451 {
		t.Fatalf("deltas incomplete: %d", len(index.members))
	}
	// Hosted refuses the next delta again and again: after memberPushFallback
	// failures the server sends the full list instead.
	index.mu.Lock()
	index.failPushes = memberPushFallback
	index.mu.Unlock()
	if _, e := f.db.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id='local-000'`); e != nil {
		t.Fatal(e)
	}
	for range memberPushFallback {
		if _, e := controlStep(t, f); e == nil {
			t.Fatal("a refused push reported success")
		}
		if _, e := f.db.Exec(`UPDATE hosted_membership_sync SET next_at=0`); e != nil {
			t.Fatal(e)
		}
	}
	var full int
	f.db.QueryRow(`SELECT full_pending FROM hosted_membership_sync`).Scan(&full)
	if full != 1 {
		t.Fatal("repeated failures did not switch to the full list")
	}
	pushes = len(index.pushes)
	step()
	if len(index.pushes) == pushes || !index.pushes[pushes].Full || index.members["acc_000"] != "admin" || len(index.members) != 451 {
		t.Fatalf("full list after fallback: %+v", index.members["acc_000"])
	}
}

// The challenge answer is signed with this server's identity key over the
// client's nonce, so a client can check it reached the server it chose before
// asking Hosted for anything (INT M7).
func TestChallengeProofIsSignedByTheServerIdentity(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	signer := networking.NewRouteIdentityHandler(f.keys, f.runner, nil, nil)
	challenge, expires, e := f.service.IssueChallenge(ctx, testInstallation)
	if e != nil {
		t.Fatal(e)
	}
	nonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	payload, signature, e := signer.SignChallenge(ctx, challenge, nonce, testInstallation, expires)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	sig, _ := base64.RawURLEncoding.DecodeString(signature)
	var p networking.ChallengeProof
	if e = json.Unmarshal(raw, &p); e != nil {
		t.Fatal(e)
	}
	pub, _ := base64.RawURLEncoding.DecodeString(p.PublicKey)
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, raw, sig) {
		t.Fatal("proof not signed by the key it names")
	}
	digest := sha256.Sum256(append([]byte("portico.server.identity.v1\x00"), pub...))
	if p.Kind != "portico.challenge-proof" || p.ServerID != f.id.ID() || p.ServerID != "srv_"+base64.RawURLEncoding.EncodeToString(digest[:]) || p.Challenge != challenge || p.Nonce != nonce || p.InstallationID != testInstallation {
		t.Fatalf("proof %+v", p)
	}
	for _, bad := range []string{"", "short", nonce + "A"} {
		if _, _, e = signer.SignChallenge(ctx, challenge, bad, testInstallation, expires); !errors.Is(e, networking.ErrInvalid) {
			t.Fatal("nonce accepted:", bad, e)
		}
	}
}

// A Portico owner whose custody Hosted has not moved to it is told so, and
// accepts custody with its own custody assertion, which the next push
// forwards with its standing. Custody and identity assertions are accepted
// only where each is meant (INT M6, M10).
func TestPorticoOwnerAcceptsCustody(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	index := &fakeIndex{members: map[string]string{}, custodian: "account"}
	f.service.current.transport.UseRoundTripper(index)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	step := func() {
		t.Helper()
		for range 4 {
			if _, e := controlStep(t, f); e != nil {
				t.Fatal(e)
			}
		}
	}
	pending := func() bool {
		t.Helper()
		snapshot, e := f.id.DirectMe(ctx, f.owner.AccessToken)
		if e != nil {
			t.Fatal(e)
		}
		return snapshot.CustodyPending
	}
	if pending() {
		t.Fatal("custody pending before Hosted said anything")
	}
	step()
	if index.members["account"] != identity.TierOwner || pending() {
		t.Fatal("custody pending for the custodian", index.members)
	}
	// Hosted releases custody (say, it was transferred on the server before).
	index.mu.Lock()
	index.custodian = ""
	index.mu.Unlock()
	addLinkedMember(t, f, "local-x", "acc_x")
	step()
	if !pending() {
		t.Fatal("owner not told to accept custody")
	}
	custody := freshClaims(f, "account")
	custody.Kind = custodyKind
	if _, e := f.service.VerifyIdentity(ctx, assertion(f, custody), testInstallation); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("a custody assertion signed someone in", e)
	}
	custody = freshClaims(f, "account")
	custody.Kind = custodyKind
	who, e := f.service.VerifyCustody(ctx, assertion(f, custody), testInstallation)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.VerifyCustody(ctx, assertion(f, freshClaims(f, "account")), testInstallation); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("an identity assertion accepted custody", e)
	}
	// Only the owner, for its own account.
	stranger := freshClaims(f, "acc_x")
	stranger.Kind = custodyKind
	other, _ := f.service.VerifyCustody(ctx, assertion(f, stranger), testInstallation)
	if e = f.id.AcceptPorticoCustody(ctx, f.owner.AccessToken, other); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("custody accepted for another account", e)
	}
	if e = f.id.AcceptPorticoCustody(ctx, f.owner.AccessToken, who); e != nil {
		t.Fatal(e)
	}
	index.mu.Lock()
	index.custodian = "account"
	index.mu.Unlock()
	pushes := len(index.pushes)
	step()
	if len(index.pushes) == pushes {
		t.Fatal("custody acceptance was not pushed")
	}
	last := index.pushes[len(index.pushes)-1]
	var carried bool
	for _, m := range last.Members {
		var env Signed
		var c IdentityClaims
		json.Unmarshal(m.Consent, &env)
		raw, _ := base64.RawURLEncoding.DecodeString(env.Payload)
		json.Unmarshal(raw, &c)
		if m.AccountID == "account" && m.Role == identity.TierOwner && c.Kind == custodyKind && c.Nonce == custody.Nonce {
			carried = true
		}
	}
	if !carried || pending() {
		t.Fatalf("custody assertion not forwarded: %+v", last.Members)
	}
}

// A departure is applied only to the membership it was about: an account that
// consented here again after leaving (it accepted a new invitation, or signed
// in) stays, and is pushed again so Hosted lists it (F-apple e2e, 23 Sep).
func TestADepartureOlderThanARejoinIsNotApplied(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newClaimFixture(t)
	ctx := context.Background()
	index := &fakeIndex{members: map[string]string{}}
	f.service.current.transport.UseRoundTripper(index)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Time{})
	addLinkedMember(t, f, "local-r", "acc_r")
	leftAt := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)
	// It signs in after leaving: its consent is newer than the departure.
	who, e := verify(f, freshClaims(f, "acc_r"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.id.PorticoSignIn(ctx, who); e != nil {
		t.Fatal(e)
	}
	for range 4 {
		if _, e = controlStep(t, f); e != nil {
			t.Fatal(e)
		}
	}
	index.mu.Lock()
	delete(index.members, "acc_r") // Hosted dropped it when it left
	index.departures = []Departure{{AccountID: "acc_r", LeftAt: leftAt}}
	index.mu.Unlock()
	if _, e = f.db.Exec(`UPDATE networking_control_schedule SET heartbeat_at=0,retry_at=0,attempts=0`); e != nil {
		t.Fatal(e)
	}
	for range 6 {
		if _, e = controlStep(t, f); e != nil {
			t.Fatal(e)
		}
	}
	var disabled int
	f.db.QueryRow(`SELECT disabled FROM direct_memberships WHERE account_id='local-r'`).Scan(&disabled)
	if disabled != 0 || index.members["acc_r"] != identity.TierMember {
		t.Fatalf("a departure older than the rejoin was applied: disabled=%d index=%v", disabled, index.members)
	}
}
