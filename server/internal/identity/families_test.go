package identity

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func familyFixture(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := persistence.Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err = db.Exec(`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES('srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY',X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f','link-test-key','2026-10-23T00:00:00Z');
 INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,'srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY',X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f',0);
 INSERT INTO configuration(key,value) VALUES('id','srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY')`); err != nil {
		t.Fatal(err)
	}

	service, err := New(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	return service, db, dir
}
func issueFamily(t *testing.T, s *Service) Envelope {
	t.Helper()
	value, err := s.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func familyBinding(id string) RenewalBinding { return RenewalBinding{"controller", "epoch", id} }
func localFamilyProof(_ context.Context, _ *sql.Tx, p Principal, b RenewalBinding) (time.Time, error) {
	if p.AccountID != "account" || p.ProfileID != "profile" || b.ControllerID != "controller" || b.ControllerEpoch != "epoch" {
		return time.Time{}, ErrUnauthorized
	}
	return time.Time{}, nil
}
func renewFamily(t *testing.T, s *Service, token, id string) AuthorizationEnvelope {
	t.Helper()
	v, e := s.RenewAuthorization(context.Background(), token, familyBinding(id), localFamilyProof)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestAuthorizationFamiliesRotationExactReplayAndIndependentLogin(t *testing.T) {
	s, db, _ := familyFixture(t)
	first, other := issueFamily(t, s), issueFamily(t, s)
	if first.SessionFamilyID == other.SessionFamilyID || first.TokenGeneration != 1 {
		t.Fatal("independent logins merged")
	}
	second := renewFamily(t, s, first.AccessToken, "renewal1")
	if second.SessionFamilyID != first.SessionFamilyID || second.TokenGeneration != 2 || second.AccessToken == first.AccessToken {
		t.Fatal("rotation identity incorrect")
	}
	if _, e := s.Authenticate(first.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("retired predecessor authenticated")
	}
	if _, e := s.Authenticate(second.AccessToken); e != nil {
		t.Fatal(e)
	}
	third := renewFamily(t, s, second.AccessToken, "renewal2")
	replay := renewFamily(t, s, first.AccessToken, "renewal1")
	if replay != second {
		t.Fatal("exact historical response changed after successor rotation")
	}
	if _, e := s.Authenticate(replay.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("historical replay restored retired authority")
	}
	if _, e := s.Authenticate(third.AccessToken); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Authenticate(other.AccessToken); e != nil {
		t.Fatal("independent family affected", e)
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM authorization_family_tokens WHERE family_id=?`, first.SessionFamilyID).Scan(&count)
	if count != 3 {
		t.Fatal("replay created another token")
	}
}

func TestBearerOnlyPlaybackRenewalDoesNotExtendFamilyHorizon(t *testing.T) {
	s, db, _ := familyFixture(t)
	first := issueFamily(t, s)
	horizon := time.Now().UTC().Add(20 * time.Minute).Truncate(time.Second).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, horizon, first.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	second := renewFamily(t, s, first.AccessToken, "bearer-only")
	if second.AuthorizationHorizon != horizon {
		t.Fatalf("bearer renewal extended family horizon: got %q want %q", second.AuthorizationHorizon, horizon)
	}
	var persisted string
	if err := db.QueryRow(`SELECT authorization_horizon FROM authorization_session_families WHERE id=?`, first.SessionFamilyID).Scan(&persisted); err != nil || persisted != horizon {
		t.Fatal("bearer renewal changed durable horizon", persisted, err)
	}
}
func TestAuthorizationFamiliesRenewalProofConflictExpiryAndRollback(t *testing.T) {
	s, db, _ := familyFixture(t)
	first := issueFamily(t, s)
	denied := func(context.Context, *sql.Tx, Principal, RenewalBinding) (time.Time, error) {
		return time.Time{}, ErrUnauthorized
	}
	if _, e := s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("denied"), denied); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("bad controller proof accepted")
	}
	if _, e := s.Authenticate(first.AccessToken); e != nil {
		t.Fatal("denied proof retired bearer")
	}
	second := renewFamily(t, s, first.AccessToken, "once")
	if _, e := s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("different"), localFamilyProof); !errors.Is(e, ErrRenewalRetired) {
		t.Fatal("retired bearer created new request")
	}
	if _, e := s.RenewAuthorization(context.Background(), second.AccessToken, familyBinding("once"), localFamilyProof); !errors.Is(e, ErrRenewalConflict) {
		t.Fatal("request ID reused with another predecessor")
	}
	// Injection occurs after proof inside the same transaction: any later failure
	// must roll back both family retirement and the current session record.
	if _, e := db.Exec(`CREATE TRIGGER deny_family_successor BEFORE INSERT ON authorization_family_tokens WHEN NEW.generation=3 BEGIN SELECT RAISE(ABORT,'controlled successor failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RenewAuthorization(context.Background(), second.AccessToken, familyBinding("rollback"), localFamilyProof); e == nil {
		t.Fatal("expected controlled failure")
	}
	if _, e := s.Authenticate(second.AccessToken); e != nil {
		t.Fatal("rollback lost working authority", e)
	}
	if _, e := db.Exec(`UPDATE authorization_family_renewals SET expires_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("once"), localFamilyProof); !errors.Is(e, ErrRenewalRetired) {
		t.Fatal("expired replay accepted")
	}
	if n, e := s.PruneAuthorizationRenewals(context.Background()); e != nil || n != 1 {
		t.Fatal("expired reply pruning", e)
	}
	if _, e := s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("once"), localFamilyProof); !errors.Is(e, ErrRenewalRetired) {
		t.Fatal("pruned retired reply recreated")
	}
	if _, e := db.Exec(`UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, Digest(second.AccessToken)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RenewAuthorization(context.Background(), second.AccessToken, familyBinding("expired"), localFamilyProof); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("expired login renewed")
	}
}
func TestAuthorizationFamiliesLogoutAnyGenerationAndRevokedRebind(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "retired", true: "expired-retired"}[expired], func(t *testing.T) {
			s, db, _ := familyFixture(t)
			first, other := issueFamily(t, s), issueFamily(t, s)
			second := renewFamily(t, s, first.AccessToken, "once")
			if expired {
				db.Exec(`UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, Digest(first.AccessToken))
			}
			callbacks := 0
			s.OnFamilyRevokedTx = func(ctx context.Context, tx *sql.Tx, f FamilyState) error {
				callbacks++
				var revoked int
				if e := tx.QueryRowContext(ctx, `SELECT revoked FROM authorization_session_families WHERE id=?`, f.ID).Scan(&revoked); e != nil {
					return e
				}
				if revoked != 1 {
					return errors.New("callback saw live family")
				}
				return nil
			}
			if e := s.LogoutToken(context.Background(), first.AccessToken); e != nil {
				t.Fatal(e)
			}
			if _, e := s.Authenticate(second.AccessToken); !errors.Is(e, ErrUnauthorized) {
				t.Fatal("successor survived logout")
			}
			if _, e := s.Authenticate(other.AccessToken); e != nil {
				t.Fatal("other login revoked", e)
			}
			if _, e := s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("once"), localFamilyProof); !errors.Is(e, ErrUnauthorized) {
				t.Fatal("revoked family replayed")
			}
			p, e := s.Authenticate(other.AccessToken)
			if e != nil {
				t.Fatal(e)
			}
			tx, e := db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.FamilyAuthorityTx(context.Background(), tx, first.SessionFamilyID, p); !errors.Is(e, ErrUnauthorized) {
				t.Fatal("revoked source escaped via another family")
			}
			tx.Rollback()
			if e = s.LogoutToken(context.Background(), first.AccessToken); e != nil || callbacks != 2 {
				t.Fatal("logout not safely idempotent")
			}
		})
	}
}
func TestAuthorizationFamiliesAccountAndProjectionRevocation(t *testing.T) {
	for name, statement := range map[string]string{"account epoch": `UPDATE accounts SET epoch=2`, "account profile": `UPDATE accounts SET profile_id='other'`, "account delete": `DELETE FROM accounts`, "family revoked": `UPDATE authorization_session_families SET revoked=1 WHERE id=?`} {
		t.Run(name, func(t *testing.T) {
			s, db, _ := familyFixture(t)
			first := issueFamily(t, s)
			second := renewFamily(t, s, first.AccessToken, "once")
			var e error
			if name == "family revoked" {
				_, e = db.Exec(statement, first.SessionFamilyID)
			} else {
				_, e = db.Exec(statement)
			}
			if e != nil {
				t.Fatal(e)
			}
			var revoked int
			if e = db.QueryRow(`SELECT revoked FROM authorization_session_families WHERE id=?`, first.SessionFamilyID).Scan(&revoked); e != nil || revoked != 1 {
				t.Fatal("revocation did not reach family", e)
			}
			if _, e = s.Authenticate(second.AccessToken); !errors.Is(e, ErrUnauthorized) {
				t.Fatal("revoked token accepted")
			}
			if _, e = s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("once"), localFamilyProof); !errors.Is(e, ErrUnauthorized) {
				t.Fatal("revoked replay accepted")
			}
		})
	}
}

func TestAuthorizationFamiliesPlainReplaySurvivesRestart(t *testing.T) {
	s, db, dir := familyFixture(t)
	first := issueFamily(t, s)
	second := renewFamily(t, s, first.AccessToken, "once")
	var stored, nonce []byte
	if e := db.QueryRow(`SELECT ciphertext,nonce FROM authorization_family_renewals`).Scan(&stored, &nonce); e != nil {
		t.Fatal(e)
	}
	// Plain storage: the response token sits in the row as-is, with no key.
	if string(stored) != second.AccessToken || len(nonce) != 0 {
		t.Fatal("renewal response not stored plainly")
	}
	restarted, e := New(db, dir)
	if e != nil {
		t.Fatal("restart failed", e)
	}
	if replay := renewFamily(t, restarted, first.AccessToken, "once"); replay != second {
		t.Fatal("persisted replay changed after service restart")
	}
	// A truncated row cannot open.
	if _, e := db.Exec(`UPDATE authorization_family_renewals SET ciphertext=CAST('short' AS BLOB)`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("once"), localFamilyProof); !errors.Is(e, ErrFamilyKey) {
		t.Fatal("truncated reply accepted")
	}
	if _, e := db.Exec(`UPDATE authorization_family_renewals SET ciphertext=?`, []byte(second.AccessToken)); e != nil {
		t.Fatal(e)
	}
	// A flipped byte replays different bytes: plaintext carries no
	// authentication tag by design, and the flipped token is not a bearer.
	stored = []byte(second.AccessToken)
	stored[0] ^= 1
	if _, e := db.Exec(`UPDATE authorization_family_renewals SET ciphertext=?`, stored); e != nil {
		t.Fatal(e)
	}
	replay, e := s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("once"), localFamilyProof)
	if e != nil || replay.AccessToken == second.AccessToken {
		t.Fatal("flipped reply not echoed as stored")
	}
	if _, e = s.RenewAuthorization(context.Background(), string(stored), familyBinding("twice"), localFamilyProof); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("flipped token accepted as a bearer")
	}
	// No key file exists, and none is created.
	if _, e := os.Stat(storagepaths.Path(dir, "authorization-replay.key")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("key file present")
	}
}
func TestAuthorizationFamiliesContextAndConcurrentRenewLogout(t *testing.T) {
	s, _, _ := familyFixture(t)
	first := issueFamily(t, s)
	// Writes have their own pool. Hold its write gate to exercise cancellation
	// while renewal waits for admission, independent of reader saturation.
	var e error
	release, e := dbwork.WriteGate().Acquire(context.Background(), dbwork.ClassSecurityFence)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := s.RenewAuthorization(ctx, first.AccessToken, familyBinding("blocked"), localFamilyProof); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("connection wait ignored context", e)
	}
	release()
	var wg sync.WaitGroup
	start := make(chan struct{})
	var renewed AuthorizationEnvelope
	var renewErr, logoutErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		renewed, renewErr = s.RenewAuthorization(context.Background(), first.AccessToken, familyBinding("race"), localFamilyProof)
	}()
	go func() { defer wg.Done(); <-start; logoutErr = s.LogoutToken(context.Background(), first.AccessToken) }()
	close(start)
	wg.Wait()
	if logoutErr != nil {
		t.Fatal(logoutErr)
	}
	if renewErr == nil {
		if _, e = s.Authenticate(renewed.AccessToken); !errors.Is(e, ErrUnauthorized) {
			t.Fatal("concurrent successor escaped family logout")
		}
	} else if !errors.Is(renewErr, ErrUnauthorized) {
		t.Fatal(renewErr)
	}
}
func TestAuthorizationFamiliesCurrentFormatAndRotationTriggerOrder(t *testing.T) {
	s, db, _ := familyFixture(t)
	token := Token()
	var legacy int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='sessions'`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatal("legacy dual-written sessions table remains", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM authorization_session_families`).Scan(&count); err != nil || count != 0 {
		t.Fatal("installer adopted an unbound token", count, err)
	}
	if _, err := s.Authenticate(token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("unbound token authorized", err)
	}
	for _, mode := range []string{"OFF", "ON"} {
		if _, err := db.Exec(`PRAGMA recursive_triggers=` + mode); err != nil {
			t.Fatal(err)
		}
		first := issueFamily(t, s)
		next := renewFamily(t, s, first.AccessToken, "current-"+mode)
		if _, err := s.Authenticate(next.AccessToken); err != nil {
			t.Fatal("rotation revoked current family", err)
		}
		if err := s.LogoutToken(context.Background(), first.AccessToken); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authenticate(next.AccessToken); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("retired authentic token did not end current family", err)
		}
	}
}
