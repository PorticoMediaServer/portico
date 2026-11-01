package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
)

// sourceRemote is a request from one public address. Home is false by
// default, exactly as the HTTP edge marks an unknown client remote.
func sourceRemote(peer string) context.Context {
	return WithSignInSource(context.Background(), peer)
}

// sourceHome is the same request seen from the owner's own network.
func sourceHome(peer string) context.Context {
	return networkpolicy.WithHome(WithSignInSource(context.Background(), peer), true)
}

func sourceAttempts(t *testing.T, db *sql.DB, bucket string) (attempts int, last, until int64, blocks int) {
	t.Helper()
	err := db.QueryRow(`SELECT failed_attempts,last_failure,blocked_until,block_count FROM identity_source_attempts WHERE source_bucket=?`, bucket).Scan(&attempts, &last, &until, &blocks)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return attempts, last, until, blocks
}

func noticeCount(t *testing.T, db *sql.DB, category, audience string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM notification_records WHERE category=? AND audience=?`, category, audience).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// stuffingAccounts inserts real accounts whose passwords cost the test almost
// nothing to check, so one guess against each of many real names exercises the
// real sign-in path rather than the unknown-name ledger.
func stuffingAccounts(t *testing.T, db *sql.DB, count int) []string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("Testing2!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, count)
	for n := 0; n < count; n++ {
		name := fmt.Sprintf("stuff%02d", n)
		if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,?,?,1)`, Token(), name, hash, Token()); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

func TestSourceBlockStopsCredentialStuffingAcrossAccounts(t *testing.T) {
	s, db, _ := directFixture(t)
	names := stuffingAccounts(t, db, 30)
	ctx := sourceRemote("203.0.113.9:443")
	for _, name := range names {
		if _, err := s.DirectLogin(ctx, name, "wrong"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	attempts, _, until, blocks := sourceAttempts(t, db, "203.0.113.9")
	if attempts < 30 || until <= time.Now().Unix() || blocks != 1 {
		t.Fatalf("stuffing did not block the source: attempts=%d until=%d blocks=%d", attempts, until, blocks)
	}
	var failedAccounts int
	if err := db.QueryRow(`SELECT count(*) FROM identity_credential_attempts WHERE failed_attempts=1`).Scan(&failedAccounts); err != nil || failedAccounts != 30 {
		t.Fatalf("each account saw its own single failure: %d %v", failedAccounts, err)
	}
	// The block is checked before any account is looked up: the right password
	// from the blocked source is refused, with the wait.
	started := time.Now()
	_, err := s.DirectLogin(ctx, names[0], "Testing2!")
	var blocked *SourceBlockError
	if !errors.As(err, &blocked) || blocked.RetryAfter < 1 || blocked.RetryAfter > 900 {
		t.Fatalf("blocked source got %v, want SourceBlockError", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("a blocked source paid for a password check")
	}
	// The owner alert names the network, never the address.
	var body, arguments string
	if err = db.QueryRow(`SELECT body,arguments FROM notification_records WHERE category='security.source-blocked'`).Scan(&body, &arguments); err != nil {
		t.Fatalf("no source-blocked alert: %v", err)
	}
	if strings.Contains(body, "203.0.113.9") || strings.Contains(arguments, "203.0.113.9") {
		t.Fatalf("alert disclosed the exact address: %s %s", body, arguments)
	}
	if !strings.Contains(body, "203.0.113.0/24") {
		t.Fatalf("alert does not name the /24: %s", body)
	}
	if got := noticeCount(t, db, "security.source-blocked", "account-admin"); got != 1 {
		t.Fatalf("source-blocked alerts = %d, want 1", got)
	}
	// Ending the block and guessing again doubles it, and the day's alert
	// stays a single notice.
	now := time.Now().Unix()
	if _, err = db.Exec(`UPDATE identity_source_attempts SET blocked_until=?,failed_attempts=29,last_failure=? WHERE source_bucket='203.0.113.9'`, now-1, now); err != nil {
		t.Fatal(err)
	}
	var raisedBefore int64
	if err = db.QueryRow(`SELECT raised_at FROM identity_security_alerts WHERE alert_key='source-blocked:203.0.113.0_24'`).Scan(&raisedBefore); err != nil {
		t.Fatal(err)
	}
	_, _ = s.DirectLogin(ctx, "nobody-again", "wrong")
	attempts, _, until, blocks = sourceAttempts(t, db, "203.0.113.9")
	if blocks != 2 || attempts < 30 {
		t.Fatalf("re-block did not double: attempts=%d blocks=%d", attempts, blocks)
	}
	if wait := until - time.Now().Unix(); wait < 1790 || wait > 1800 {
		t.Fatalf("second block lasts %ds, want about 1800", wait)
	}
	var raisedAfter int64
	if err = db.QueryRow(`SELECT raised_at FROM identity_security_alerts WHERE alert_key='source-blocked:203.0.113.0_24'`).Scan(&raisedAfter); err != nil {
		t.Fatal(err)
	}
	if raisedAfter != raisedBefore {
		t.Fatalf("a second block inside the day re-alerted the owner: %d -> %d", raisedBefore, raisedAfter)
	}
	if got := noticeCount(t, db, "security.source-blocked", "account-admin"); got != 1 {
		t.Fatalf("source-blocked alerts after re-block = %d, want 1", got)
	}
}

func TestHomeSourceIsCountedButNeverBlocked(t *testing.T) {
	s, db, _ := directFixture(t)
	ctx := sourceHome("192.0.2.44:443")
	for n := 0; n < 40; n++ {
		_, _ = s.DirectLogin(ctx, fmt.Sprintf("home-%02d", n), "wrong")
	}
	attempts, _, until, blocks := sourceAttempts(t, db, "192.0.2.44")
	if attempts < 40 || until != 0 || blocks != 0 {
		t.Fatalf("home source state: attempts=%d until=%d blocks=%d", attempts, until, blocks)
	}
	if _, err := s.DirectLogin(ctx, "owner", "Testing1!"); err != nil {
		t.Fatalf("a home source was blocked: %v", err)
	}
	if got := noticeCount(t, db, "security.source-blocked", "account-admin"); got != 0 {
		t.Fatalf("a home source raised a block alert: %d", got)
	}
}

func TestEveryCredentialFailureKindChargesTheSource(t *testing.T) {
	t.Run("password", func(t *testing.T) {
		s, db, _ := directFixture(t)
		_, _ = s.DirectLogin(sourceRemote("198.51.100.1:1"), "owner", "wrong")
		if attempts, _, _, _ := sourceAttempts(t, db, "198.51.100.1"); attempts != 1 {
			t.Fatalf("password failure charged %d", attempts)
		}
	})
	t.Run("unknown username", func(t *testing.T) {
		s, db, _ := directFixture(t)
		_, _ = s.DirectLogin(sourceRemote("198.51.100.2:1"), "no-such-name", "wrong")
		if attempts, _, _, _ := sourceAttempts(t, db, "198.51.100.2"); attempts != 1 {
			t.Fatalf("unknown-name failure charged %d", attempts)
		}
	})
	t.Run("two-step code", func(t *testing.T) {
		s, db, owner := directFixture(t)
		confirmedFactor(t, s, owner.AccountToken, "Testing1!")
		login, err := s.DirectLogin(context.Background(), "owner", "Testing1!")
		if err != nil || login.Challenge == nil {
			t.Fatalf("no challenge: %+v %v", login, err)
		}
		_, err = s.CompleteSignInChallenge(sourceRemote("198.51.100.3:1"), login.Challenge.Token, "000000", true)
		if !errors.Is(err, ErrFactorInvalid) {
			t.Fatalf("wrong code: %v", err)
		}
		if attempts, _, _, _ := sourceAttempts(t, db, "198.51.100.3"); attempts != 1 {
			t.Fatalf("two-step failure charged %d", attempts)
		}
	})
	t.Run("profile PIN", func(t *testing.T) {
		s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchMain: "4821"})
		_, err := s.SelectDirectProfileFrom(sourceRemote("198.51.100.4:1"), login.AccountToken, profiles[switchMain].ID, DirectSelection{PIN: "0000"}, false)
		if !errors.Is(err, ErrProfilePIN) {
			t.Fatalf("wrong PIN: %v", err)
		}
		if attempts, _, _, _ := sourceAttempts(t, db, "198.51.100.4"); attempts != 1 {
			t.Fatalf("PIN failure charged %d", attempts)
		}
	})
	t.Run("quick connect code", func(t *testing.T) {
		s, db, _ := directFixture(t)
		if _, err := s.ReviewQuick(sourceRemote("198.51.100.5:1"), "2222-2222"); err == nil {
			t.Fatal("a guessed quick code was accepted")
		}
		if attempts, _, _, _ := sourceAttempts(t, db, "198.51.100.5"); attempts != 1 {
			t.Fatalf("quick-code failure charged %d", attempts)
		}
	})
	t.Run("two-step management code", func(t *testing.T) {
		s, db, owner := directFixture(t)
		if _, err := s.EnrolTwoFactor(context.Background(), owner.AccountToken, "Testing1!"); err != nil {
			t.Fatal(err)
		}
		// A pending enrolment is confirmed by a TOTP code; a wrong one is an
		// evaluated factor guess and must be charged.
		_, err := s.VerifyTwoFactor(sourceRemote("198.51.100.7:1"), owner.AccountToken, "Testing1!", "000000")
		if !errors.Is(err, ErrFactorInvalid) {
			t.Fatalf("wrong factor code: %v", err)
		}
		if attempts, _, _, _ := sourceAttempts(t, db, "198.51.100.7"); attempts != 1 {
			t.Fatalf("factor failure charged %d", attempts)
		}
	})
	t.Run("member reset code", func(t *testing.T) {
		ctx := context.Background()
		s, db, owner := directFixture(t)
		member, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Name: "Member", Password: "Testing2!", AllowedLibraries: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision}); err != nil {
			t.Fatal(err)
		}
		_, err = s.VerifyMemberReset(sourceRemote("198.51.100.6:1"), MemberResetVerify{Username: "member", Code: "not-the-code"})
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("wrong reset code: %v", err)
		}
		if attempts, _, _, _ := sourceAttempts(t, db, "198.51.100.6"); attempts != 1 {
			t.Fatalf("reset-code failure charged %d", attempts)
		}
	})
}

func TestNewDeviceNoticesFollowTheSourceOfTheSignIn(t *testing.T) {
	s, db, _ := directFixture(t)
	// The fixture's own sign-in created the first device from a remote
	// (unclassified) peer: the holder and the administrators hear about it.
	if got := noticeCount(t, db, "security.new-device", "profile"); got != 1 {
		t.Fatalf("holder notices after the first sign-in = %d, want 1", got)
	}
	if got := noticeCount(t, db, "security.new-device", "account-admin"); got != 1 {
		t.Fatalf("admin notices after the first sign-in = %d, want 1", got)
	}
	// A new device signing in at home is the holder's business only.
	homeCtx, err := WithIssuingDevice(sourceHome("192.0.2.10:1"), registration(Token()), "192.0.2.10:1")
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.DirectLoginFrom(homeCtx, "owner", "Testing1!", true)
	if err != nil {
		t.Fatal(err)
	}
	if home.DeviceID == "" {
		t.Fatal("home sign-in has no device")
	}
	if got := noticeCount(t, db, "security.new-device", "profile"); got != 2 {
		t.Fatalf("holder notices after a home device = %d, want 2", got)
	}
	if got := noticeCount(t, db, "security.new-device", "account-admin"); got != 1 {
		t.Fatalf("a home device alerted the owner: %d", got)
	}
	// The same installation signing in again is the same device: no notice.
	againCtx, err := WithIssuingDevice(context.Background(), registration(home.InstallationID), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLoginFrom(againCtx, "owner", "Testing1!", true); err != nil {
		t.Fatal(err)
	}
	if got := noticeCount(t, db, "security.new-device", "profile"); got != 2 {
		t.Fatalf("a known device raised another holder notice: %d", got)
	}
	if got := noticeCount(t, db, "security.new-device", "account-admin"); got != 1 {
		t.Fatalf("a known device raised another owner notice: %d", got)
	}
	// A remote new device names only the /24 in the owner's copy.
	remoteCtx, err := WithIssuingDevice(sourceRemote("203.0.113.9:443"), registration(Token()), "203.0.113.9:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLoginFrom(remoteCtx, "owner", "Testing1!", true); err != nil {
		t.Fatal(err)
	}
	if got := noticeCount(t, db, "security.new-device", "account-admin"); got != 2 {
		t.Fatalf("a remote device did not alert the owner: %d", got)
	}
	var arguments string
	if err = db.QueryRow(`SELECT arguments FROM notification_records WHERE category='security.new-device' AND audience='account-admin' AND arguments LIKE '%203.0.113.0/24%'`).Scan(&arguments); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(arguments, `203.0.113.9`) {
		t.Fatalf("owner alert disclosed the exact address: %s", arguments)
	}
}

func TestSignInBurstAlertRaisesOnceAnHour(t *testing.T) {
	s, db, _ := directFixture(t)
	// Fifty failures from fifty places: they are a server-wide burst without
	// blocking any one source.
	for n := 0; n < 50; n++ {
		ctx := sourceRemote(fmt.Sprintf("198.18.%d.%d:1", n/250, n%250+1))
		_, _ = s.DirectLogin(ctx, fmt.Sprintf("burst-%02d", n), "wrong")
	}
	if got := noticeCount(t, db, "security.sign-in-burst", "account-admin"); got != 1 {
		t.Fatalf("burst alerts = %d, want 1", got)
	}
	var raised int64
	if err := db.QueryRow(`SELECT raised_at FROM identity_security_alerts WHERE alert_key='sign-in-burst'`).Scan(&raised); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		ctx := sourceRemote(fmt.Sprintf("198.19.0.%d:1", n+1))
		_, _ = s.DirectLogin(ctx, fmt.Sprintf("burst-more-%d", n), "wrong")
	}
	var again int64
	if err := db.QueryRow(`SELECT raised_at FROM identity_security_alerts WHERE alert_key='sign-in-burst'`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != raised {
		t.Fatal("a second burst alert was raised inside the hour")
	}
	if got := noticeCount(t, db, "security.sign-in-burst", "account-admin"); got != 1 {
		t.Fatalf("burst alerts after more failures = %d, want 1", got)
	}
}

func TestSecuritySummaryAndUnblock(t *testing.T) {
	s, db, _ := directFixture(t)
	ctx := sourceRemote("203.0.113.9:443")
	for n := 0; n < 30; n++ {
		_, _ = s.DirectLogin(ctx, fmt.Sprintf("summary-%02d", n), "wrong")
	}
	summary, err := s.SecuritySummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one", summary.Blocks)
	}
	block := summary.Blocks[0]
	if block.Network != "203.0.113.0/24" || block.ID == "" || block.BlockCount != 1 {
		t.Fatalf("block = %+v", block)
	}
	if summary.Counts.Last24Hours.FailedSignIns < 30 || summary.Counts.Last7Days.FailedSignIns < 30 {
		t.Fatalf("failed sign-ins = %+v", summary.Counts)
	}
	if summary.Counts.Last24Hours.BlockedSources != 1 || summary.Counts.Last7Days.BlockedSources != 1 {
		t.Fatalf("blocked sources = %+v", summary.Counts)
	}
	if err = s.UnblockSource(context.Background(), block.ID); err != nil {
		t.Fatal(err)
	}
	summary, err = s.SecuritySummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Blocks) != 0 {
		t.Fatalf("unblock left %+v", summary.Blocks)
	}
	if attempts, _, _, _ := sourceAttempts(t, db, "203.0.113.9"); attempts != 0 {
		t.Fatalf("unblock left %d failures", attempts)
	}
	if err = s.UnblockSource(context.Background(), block.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second unblock: %v", err)
	}
	if err = s.UnblockSource(context.Background(), "not an id"); !errors.Is(err, ErrDirectInput) {
		t.Fatalf("bad id: %v", err)
	}
	// The source is admitted again at once.
	if _, err = s.DirectLogin(ctx, "owner", "Testing1!"); err != nil {
		t.Fatalf("unblocked source was still refused: %v", err)
	}
}

func TestSourceThresholdScalesWithPasswordPolicy(t *testing.T) {
	for policy, want := range map[AttemptPolicy]int{"relaxed": 60, "standard": 30, "strict": 15} {
		if got := sourceThreshold(policy); got != want {
			t.Errorf("%s threshold = %d, want %d", policy, got, want)
		}
	}
	if got := sourceBlockDuration(1); got != 900 {
		t.Errorf("first block = %ds, want 900", got)
	}
	if got := sourceBlockDuration(2); got != 1800 {
		t.Errorf("second block = %ds, want 1800", got)
	}
	if got := sourceBlockDuration(64); got != sourceBlockCeiling {
		t.Errorf("repeated blocks = %ds, want the day cap", got)
	}
}

func TestDecayedSourceRowsArePrunedOnWrite(t *testing.T) {
	s, db, _ := directFixture(t)
	now := time.Now().Unix()
	// A block that ended long ago and failures that have halved away.
	if _, err := db.Exec(`INSERT INTO identity_source_attempts(source_bucket,source_id,failed_attempts,last_failure,blocked_until,block_count) VALUES('198.51.100.40',?,2,?,0,1)`, strings.Repeat("d", 43), now-4*3600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO identity_security_minutes(minute,count) VALUES(?,5)`, now/60-24*60); err != nil {
		t.Fatal(err)
	}
	_, _ = s.DirectLogin(sourceRemote("198.51.100.41:1"), "prune-trigger", "wrong")
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM identity_source_attempts WHERE source_bucket='198.51.100.40'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("decayed source row survived pruning: %d %v", remaining, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM identity_security_minutes WHERE minute=?`, now/60-24*60).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("stale minute counter survived pruning: %d %v", remaining, err)
	}
}

func TestSourceNetworkNamesOnlyTheLargerBlock(t *testing.T) {
	for bucket, want := range map[string]string{
		"203.0.113.9":             "203.0.113.0/24",
		"2001:db8:ab12:cd34::/64": "2001:db8:ab12::/48",
	} {
		if got := sourceNetwork(bucket); got != want {
			t.Errorf("%s network = %q, want %q", bucket, got, want)
		}
	}
	ctx := sourceRemote("[2001:db8:ab12:cd34::9]:443")
	bucket, ok := sourceBucket(ctx)
	if !ok || bucket != "2001:db8:ab12:cd34::/64" {
		t.Fatalf("IPv6 source bucket = %q, %v", bucket, ok)
	}
}

func TestQuickConnectRaisesTheNewDeviceNotices(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	principal, err := s.Authenticate(owner.AccountToken)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.StartQuick(ctx, QuickStart{RequestID: "33333333-3333-4333-8333-333333333333", DeviceName: "Living room TV", Platform: "tvos", AppVersion: "1.0"}, true)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.ReviewQuick(ctx, code.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideQuick(ctx, principal, preview.RequestID, code.UserCode, "approve"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE quick_connect_v1 SET next_poll=0 WHERE id=?`, preview.RequestID); err != nil {
		t.Fatal(err)
	}
	pollCtx, err := WithIssuingDevice(sourceRemote("203.0.113.30:1"), registration(Token()), "203.0.113.30:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollQuick(pollCtx, code.DeviceCode); err != nil {
		t.Fatal(err)
	}
	// The fixture's own sign-in was the first remote device; the TV is the
	// second, and it is remote, so both inboxes hear about it.
	if got := noticeCount(t, db, "security.new-device", "profile"); got != 2 {
		t.Fatalf("holder notices after Quick Connect = %d, want 2", got)
	}
	if got := noticeCount(t, db, "security.new-device", "account-admin"); got != 2 {
		t.Fatalf("owner notices after Quick Connect = %d, want 2", got)
	}
	var arguments string
	if err = db.QueryRow(`SELECT arguments FROM notification_records WHERE category='security.new-device' AND audience='profile' AND arguments LIKE '%Living room TV%'`).Scan(&arguments); err != nil {
		t.Fatalf("the TV's notice does not name it: %v", err)
	}
}
