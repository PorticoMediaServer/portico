package hosted

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

// This is the Server transport/SQLite boundary, not a Hosted PostgreSQL pair.
// Old erasure directives have no consumer; the strict authenticated check-in
// decoder refuses their retired fields before changing local state.
type retiredCleanupCheckIn struct {
	t     *testing.T
	calls int
}

func (x *retiredCleanupCheckIn) RoundTrip(r *http.Request) (*http.Response, error) {
	x.calls++
	if !strings.HasSuffix(r.URL.Path, "/heartbeat") {
		x.t.Fatalf("retired protocol request: %s", r.URL.Path)
	}
	raw, _ := json.Marshal(map[string]any{
		"heartbeatAfterMs": (7 * 24 * time.Hour).Milliseconds(), "presenceExpiresAt": time.Now().Add(8 * 24 * time.Hour),
		"memberSequence": 0, "memberDigestMatches": true, "cleanupPending": true, "retired": true,
		"jobs": []any{map[string]any{"kind": "portico.account-erasure.v1", "accountId": "account", "profileIds": []string{"private-profile"}, "disposition": "delete_owned_personal_resources"}},
	})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw))), ContentLength: int64(len(raw)), Request: r}, nil
}
func seedDepartureResources(t *testing.T, f *claimFixture, account, hosted string) identity.Envelope {
	t.Helper()
	addLinkedMember(t, f, account, hosted)
	if _, err := f.db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position) VALUES(?,?,'Personal',0,1)`, account+"-personal", account); err != nil {
		t.Fatal(err)
	}
	key := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: account, ProfileID: account + "-personal"})
	if _, err := f.db.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES(?,'collection',?,'local',?,?,'Keep this collection','now')`, account+"-saved", key, account, account+"-personal"); err != nil {
		t.Fatal(err)
	}
	env, err := f.id.Issue(account, account+"-profile", "local", identity.TierMember, 1)
	if err != nil {
		t.Fatal(err)
	}
	return env
}
func departureRetentionState(t *testing.T, f *claimFixture) string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT printf('%d:%d:%d',count(*),(SELECT count(*) FROM direct_profiles),(SELECT count(*) FROM saved_resources)) FROM accounts`,
		`SELECT group_concat(account_id||':'||hosted_account_id,',') FROM (SELECT account_id,hosted_account_id FROM account_portico_links ORDER BY account_id)`,
		`SELECT COALESCE(installed_operation_id,'') FROM networking_claim_identity WHERE singleton=1`,
	} {
		var s string
		if err := f.db.QueryRow(q).Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, "|")
}
func TestRetiredCleanupCheckInCannotEraseOrRetireServerData(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	seedDepartureResources(t, f, "private-member", "acc_private")
	before := departureRetentionState(t, f)
	if err := f.service.ConfigureIdentityDepartures(f.keys); err != nil {
		t.Fatal(err)
	}
	// Preparing an explicit leave stays offline and keeps the exact installed
	// server-key/claim/credential tuple; removal of cleanup cannot break it.
	notice, err := f.service.PrepareIdentityDeparture(ctx, "moving_to_local")
	if err != nil || notice == nil || notice.Wire.ServerID != f.installed.ServerID || notice.Wire.ClaimGeneration != f.installed.ClaimGeneration || notice.Wire.CredentialGeneration != f.installed.CredentialGeneration || !ed25519.Verify(f.installed.PublicKey, notice.Payload, notice.Signature) {
		t.Fatalf("actual fixed departure proof: %v", err)
	}
	transport := &retiredCleanupCheckIn{t: t}
	f.service.current.transport.UseRoundTripper(transport)
	seedSchedule(t, f, time.Time{}, time.Time{})
	// No member push is needed to exercise the actual authenticated heartbeat.
	if _, err := f.db.Exec(`INSERT INTO hosted_membership_sync(singleton,acked,full_pending) VALUES(1,0,0) ON CONFLICT(singleton) DO UPDATE SET full_pending=0`); err != nil {
		t.Fatal(err)
	}
	// The retired cleanup fields are unknown to this server. Hosted's plain responses decode open
	// (R-09b), so they are ignored like any added field: the check-in succeeds and they never act.
	err = f.runner.Do(ctx, func(ctx context.Context) error { _, err := f.service.ordinaryControlStep(ctx); return err })
	if err != nil || transport.calls != 1 {
		t.Fatalf("a check-in carrying retired fields: calls=%d, error=%v", transport.calls, err)
	}
	if after := departureRetentionState(t, f); after != before {
		t.Fatalf("retired directives changed data: %s -> %s", before, after)
	}
	if err = f.runner.Do(ctx, func(ctx context.Context) error {
		_, err := f.keys.Sign(ctx, f.installed.Binding, []byte(`{"kind":"portico.claim.cleanup"}`))
		return err
	}); !errors.Is(err, networking.ErrInvalid) {
		t.Fatalf("retired erasure protocol acquired a signature: %v", err)
	}
}
func TestDepartureMarksOnlyMatchingMemberLeftAndRetainsData(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	member := seedDepartureResources(t, f, "left-member", "acc_left")
	other := seedDepartureResources(t, f, "other-member", "acc_other")
	before := departureRetentionState(t, f)
	var retired []string
	f.id.OnFamilyRevokedTx = func(_ context.Context, _ *sql.Tx, family identity.FamilyState) error {
		retired = append(retired, family.ID)
		return nil
	}
	departure := []Departure{{AccountID: "acc_left", LeftAt: time.Now().UTC()}, {AccountID: "account", LeftAt: time.Now().UTC()}}
	apply := func() {
		t.Helper()
		if err := f.runner.Do(ctx, func(ctx context.Context) error { return f.service.applyDepartures(ctx, f.installed, departure) }); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	if len(retired) != 1 || retired[0] != member.SessionFamilyID {
		t.Fatalf("departure omitted/mis-scoped existing retirement hook: %v", retired)
	}
	var disabled, epoch int
	var reason string
	var revision int64
	if err := f.db.QueryRow(`SELECT m.disabled,m.disabled_reason,m.revision,a.epoch FROM direct_memberships m JOIN accounts a ON a.id=m.account_id WHERE account_id='left-member'`).Scan(&disabled, &reason, &revision, &epoch); err != nil || disabled != 1 || reason != "left" || epoch != 2 {
		t.Fatalf("Left state disabled=%d reason=%s revision=%d epoch=%d: %v", disabled, reason, revision, epoch, err)
	}
	if _, err := f.id.Authenticate(member.AccessToken); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("Left member kept a session: %v", err)
	}
	for name, token := range map[string]string{"other member": other.AccessToken, "owner": f.owner.AccessToken} {
		if _, err := f.id.Authenticate(token); err != nil {
			t.Fatalf("departure ended %s: %v", name, err)
		}
	}
	if after := departureRetentionState(t, f); after != before {
		t.Fatalf("departure erased data/unlinked owner/retired installed claim: %s -> %s", before, after)
	}
	apply()
	if len(retired) != 1 {
		t.Fatal("departure replay repeated family retirement")
	}
	var next string
	if err := f.db.QueryRow(`SELECT printf('%d:%d',a.epoch,m.revision) FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.id='left-member'`).Scan(&next); err != nil || next != fmt.Sprintf("%d:%d", epoch, revision) {
		t.Fatalf("duplicate advanced authority: %s %v", next, err)
	}
}

func TestDepartureRetirementHookFailureRollsBackLeftAndRevocation(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	member := seedDepartureResources(t, f, "left-member", "acc_left")
	before := departureRetentionState(t, f)
	failed := errors.New("reader retirement refused")
	f.id.OnFamilyRevokedTx = func(_ context.Context, _ *sql.Tx, family identity.FamilyState) error {
		if family.ID != member.SessionFamilyID {
			t.Fatal("wrong retirement family")
		}
		return failed
	}
	err := f.runner.Do(ctx, func(ctx context.Context) error {
		return f.service.applyDepartures(ctx, f.installed, []Departure{{AccountID: "acc_left", LeftAt: time.Now().UTC()}})
	})
	if !errors.Is(err, failed) {
		t.Fatalf("callback error did not abort departure: %v", err)
	}
	var disabled, epoch int
	if err = f.db.QueryRow(`SELECT m.disabled,a.epoch FROM direct_memberships m JOIN accounts a ON a.id=m.account_id WHERE a.id='left-member'`).Scan(&disabled, &epoch); err != nil || disabled != 0 || epoch != 1 {
		t.Fatalf("failed retirement published Left/epoch: %d/%d %v", disabled, epoch, err)
	}
	if _, err = f.id.Authenticate(member.AccessToken); err != nil {
		t.Fatalf("failed retirement revoked the family: %v", err)
	}
	if after := departureRetentionState(t, f); after != before {
		t.Fatal("failed retirement changed retained data")
	}
}
