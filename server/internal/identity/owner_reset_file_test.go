package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func requestResetFileCode(t *testing.T, s *Service) string {
	t.Helper()
	location, e := s.RequestOwnerReset(context.Background(), OwnerResetRequest{Username: "unrelated-unknown"})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(location.FileDirectory, location.FileName))
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 4 || len(lines[2]) != 43 {
		t.Fatal("invalid private file")
	}
	return lines[2]
}
func TestResetFilePublicUniformPrivateHashAndLocalPasswordGate(t *testing.T) {
	s, db, old := directFixture(t)
	ctx := context.Background()
	code := requestResetFileCode(t, s)
	first, e := s.RequestOwnerReset(ctx, OwnerResetRequest{Username: "owner"})
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.RequestOwnerReset(ctx, OwnerResetRequest{Username: "missing"})
	if e != nil {
		t.Fatal(e)
	}
	first.RetryAfterSeconds = 0
	second.RetryAfterSeconds = 0
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) || strings.Contains(string(a), code) || strings.Contains(string(a), "owner\"") {
		t.Fatal("nonuniform public result")
	}
	info, e := os.Stat(filepath.Join(s.resetDir, resetFileName))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("nonprivate reset file", e)
	}
	var hash string
	var issued, expires int64
	if e = db.QueryRow(`SELECT code_hash,issued_at,expires_at FROM owner_reset_file`).Scan(&hash, &issued, &expires); e != nil || hash == code || hash != s.setupHash("owner-reset", code) || expires-issued != 900 {
		t.Fatal("invalid stored digest/expiry", e)
	}
	out, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code})
	if e != nil {
		t.Fatal(e)
	}
	if out.ResetFileOwnerSession || out.Session == nil || !out.PasswordChangeRequired || out.Session.Viewer.Role != "account" || out.Session.RefreshToken != "" {
		t.Fatal("wrong local reset authority")
	}
	if _, e = s.Authenticate(old.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("old session survived", e)
	}
	if _, e = s.DirectMe(ctx, out.Session.AccessToken); !errors.Is(e, ErrPasswordChangeRequired) {
		t.Fatal("password gate escaped", e)
	}
	if _, e = s.Authenticate(out.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("reset session authorized viewing", e)
	}
	if e = s.ChangeDirectPassword(ctx, out.Session.AccessToken, "", "Chosen-password1!", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DirectLogin(ctx, "owner", "Chosen-password1!"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("replay admitted", e)
	}
	if _, e = os.Stat(filepath.Join(s.resetDir, resetFileName)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("used reset file remains", e)
	}
}
func TestResetFileWrongTryCancellationExpiryReissueAndRollback(t *testing.T) {
	s, db, _ := directFixture(t)
	ctx := context.Background()
	code := requestResetFileCode(t, s)
	for i := 0; i < 5; i++ {
		_, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: "wrong"})
		if i < 4 {
			if !errors.Is(e, ErrUnauthorized) {
				t.Fatal(e)
			}
		} else {
			var limited *SetupProtocolError
			if !errors.As(e, &limited) || limited.Code != "rate_limited" || limited.Interval < 1 || limited.Interval > 60 {
				t.Fatal("missing fifth-attempt cooldown", e)
			}
		}
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("cancelled code usable", e)
	}
	if _, e := db.Exec(`UPDATE owner_reset_file SET issued_at=issued_at-61`); e != nil {
		t.Fatal(e)
	}
	replacement := requestResetFileCode(t, s)
	if replacement == code {
		t.Fatal("code not replaced")
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("replaced code usable", e)
	}
	if _, e := db.Exec(`UPDATE owner_reset_file SET expires_at=0`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: replacement}); !errors.Is(e, ErrOwnerResetExpired) {
		t.Fatal("exact expired proof did not report its terminal outcome", e)
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: replacement}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("retired expired proof disclosed a successor outcome", e)
	}
	// A transaction failure must not consume a valid code or revoke sessions.
	if _, e := db.Exec(`UPDATE owner_reset_file SET issued_at=0`); e != nil {
		t.Fatal(e)
	}
	replacement = requestResetFileCode(t, s)
	if _, e := db.Exec(`CREATE TRIGGER reset_fail BEFORE UPDATE OF epoch ON accounts BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: replacement}); e == nil {
		t.Fatal("injected failure ignored")
	}
	if _, e := db.Exec(`DROP TRIGGER reset_fail`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: replacement}); e != nil {
		t.Fatal("failed transaction consumed code", e)
	}
}
func TestResetFileConcurrentVerifyIsSingleUseAndOwnerEpochFenced(t *testing.T) {
	s, db, _ := directFixture(t)
	ctx := context.Background()
	code := requestResetFileCode(t, s)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code}); results <- e }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrUnauthorized) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("concurrent successes", success)
	}
	if _, e := db.Exec(`UPDATE owner_reset_file SET issued_at=0`); e != nil {
		t.Fatal(e)
	}
	code = requestResetFileCode(t, s)
	if _, e := db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id='account'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("stale epoch admitted", e)
	}
}
func TestResetFileRefusesUnsafeFilesAndParents(t *testing.T) {
	for _, kind := range []string{"symlink", "public", "directory", "parent"} {
		t.Run(kind, func(t *testing.T) {
			s, db, _ := directFixture(t)
			external := filepath.Join(t.TempDir(), "external")
			if e := os.WriteFile(external, []byte("preserved"), 0600); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(s.resetDir, resetFileName)
			switch kind {
			case "symlink":
				if e := os.Symlink(external, path); e != nil {
					t.Fatal(e)
				}
			case "public":
				if e := os.WriteFile(path, []byte("preserved"), 0644); e != nil {
					t.Fatal(e)
				}
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			case "directory":
				if e := os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			case "parent":
				// Keys also contains the real setup secret in the portable layout.
				// Preserve it while replacing the parent with the hostile symlink.
				if e := os.Rename(s.resetDir, s.resetDir+"-before-symlink"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(filepath.Dir(external), s.resetDir); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.RequestOwnerReset(context.Background(), OwnerResetRequest{}); !errors.Is(e, ErrOwnerResetFileWrite) {
				t.Fatal("unsafe target did not report the closed file-write outcome", e)
			}
			raw, e := os.ReadFile(external)
			if e != nil || string(raw) != "preserved" {
				t.Fatal("external file changed", e)
			}
			var n int
			if e := db.QueryRow(`SELECT count(*) FROM owner_reset_file`).Scan(&n); e != nil || n != 0 {
				t.Fatal("failure persisted authority", e)
			}
		})
	}
}
func TestResetFilePorticoSessionIsExplicitAndCannotMintDescendants(t *testing.T) {
	s, db, old := directFixture(t)
	ctx := context.Background()
	selectPorticoFixture(t, s)
	if _, e := db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('account','hosted-owner')`); e != nil {
		t.Fatal(e)
	}
	out, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: requestResetFileCode(t, s)})
	if e != nil {
		t.Fatal(e)
	}
	if !out.ResetFileOwnerSession || out.PasswordChangeRequired || out.Session == nil || out.Session.Viewer.Role != TierOwner || out.Session.RefreshToken != "" {
		t.Fatal("fake Hosted or wrong reset session")
	}
	if _, e = s.Authenticate(out.Session.AccessToken); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(old.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("old owner session remains", e)
	}
	var admissions int
	if e = db.QueryRow(`SELECT count(*) FROM portico_admitted_families WHERE family_id=?`, out.Session.SessionFamilyID).Scan(&admissions); e != nil || admissions != 0 {
		t.Fatal("reset forged Hosted admission", e)
	}
	if _, e = s.SelectDirectProfile(ctx, out.Session.AccessToken, "profile", DirectSelection{}); !errors.Is(e, ErrForbidden) {
		t.Fatal("reset made persistent descendant", e)
	}
	if _, e = s.RefreshSession(ctx, old.Session.RefreshToken, old.Session.InstallationID, Token()); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("old refresh survived", e)
	}
	if _, e = s.DirectLogin(ctx, "owner", "Testing1!"); !errors.Is(e, ErrSignInKindPortico) {
		t.Fatal("Portico owner local fallback", e)
	}
	if e = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error { return refuseResetDescendantTx(ctx, tx, out.Session.SessionFamilyID) }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	horizon, _ := time.Parse(time.RFC3339, out.Session.AuthorizationHorizon)
	if time.Until(horizon) > time.Hour {
		t.Fatal("session horizon extended")
	}
}

func TestResetFilePublicationFailureBoundariesNeverPublishPartialCredential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, boundary := range []string{"write", "short-write", "sync", "close", "rename", "directory-sync"} {
		t.Run(boundary, func(t *testing.T) {
			s, db, _ := directFixture(t)
			old := requestResetFileCode(t, s)
			ctx := context.Background()
			if _, e := db.Exec(`UPDATE owner_reset_file SET issued_at=0`); e != nil {
				t.Fatal(e)
			}
			ops := defaultResetFileOperations()
			failure := errors.New("fixture storage failure")
			switch boundary {
			case "write":
				ops.write = func(*os.File, []byte) (int, error) { return 0, failure }
			case "short-write":
				ops.write = func(f *os.File, b []byte) (int, error) { return f.Write(b[:len(b)-1]) }
			case "sync":
				ops.sync = func(*os.File) error { return failure }
			case "close":
				ops.close = func(f *os.File) error { f.Close(); return failure }
			case "rename":
				ops.rename = func(*os.Root, string, string) error { return failure }
			case "directory-sync":
				ops.syncDirectory = func(string) error { return failure }
			}
			s.resetIO = &ops
			if _, e := s.RequestOwnerReset(ctx, OwnerResetRequest{}); e == nil {
				t.Fatal("publication failure ignored")
			}
			var hash string
			if e := db.QueryRow(`SELECT code_hash FROM owner_reset_file`).Scan(&hash); e != nil || hash != s.setupHash("owner-reset", old) {
				t.Fatal("failed publication committed authority", e)
			}
			raw, e := os.ReadFile(filepath.Join(s.resetDir, resetFileName))
			if e != nil {
				t.Fatal(e)
			}
			lines := strings.Split(string(raw), "\n")
			if len(lines) != 6 || len(lines[2]) != 43 {
				t.Fatal("partial reset file published")
			}
			if boundary != "directory-sync" && lines[2] != old {
				t.Fatal("pre-publication failure replaced existing file")
			}
			entries, e := os.ReadDir(s.resetDir)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".reset-code-") {
					t.Fatal("owned temporary file leaked")
				}
			}
		})
	}
}
func TestMemberResetAuthorityReplacementOneHourAndLiveFences(t *testing.T) {
	s, db, owner := directFixture(t)
	ctx := context.Background()
	admin, e := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "admin", Name: "Admin", Password: "Admin-password1!", AllowedLibraries: []string{}})
	if e != nil {
		t.Fatal(e)
	}
	member, e := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Name: "Member", Password: "Member-password1!", AllowedLibraries: []string{}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id=?`, admin.ID); e != nil {
		t.Fatal(e)
	}
	a, e := s.DirectLogin(ctx, "admin", "Admin-password1!")
	if e != nil {
		t.Fatal(e)
	}
	for _, target := range []DirectAccount{owner.Account, admin} {
		if _, e = s.MakeMemberResetCode(ctx, a.AccountToken, target.ID, MemberResetRequest{ExpectedRevision: target.Revision}); !errors.Is(e, ErrForbidden) {
			t.Fatal("admin reset peer/owner/self", e)
		}
	}
	first, e := s.MakeMemberResetCode(ctx, a.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision})
	if e != nil {
		t.Fatal(e)
	}
	expiry, e := time.Parse(time.RFC3339, first.ExpiresAt)
	if e != nil || time.Until(expiry) > time.Hour || time.Until(expiry) < time.Hour-5*time.Second {
		t.Fatal("wrong member lifetime", e)
	}
	second, e := s.MakeMemberResetCode(ctx, a.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if first.Code == second.Code {
		t.Fatal("replacement reused reset code")
	}
	if _, e = s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: first.Code}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("replaced member code admitted", e)
	}
	// Promotion after an admin issues a member code invalidates the code rather
	// than letting the old lower-tier authority reset a now privileged account.
	if _, e = db.Exec(`UPDATE direct_memberships SET role='admin',revision=revision+1 WHERE account_id=?`, member.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: second.Code}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("stale tier code admitted", e)
	}
	if _, e = db.Exec(`UPDATE direct_memberships SET role='member',revision=revision+1 WHERE account_id=?`, member.ID); e != nil {
		t.Fatal(e)
	}
	member.Revision += 2
	current, e := s.MakeMemberResetCode(ctx, a.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE direct_memberships SET role='member' WHERE account_id=?`, admin.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: current.Code}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("retired issuer authority retained", e)
	}
}

func TestResetFileWorksRemotelyWithOrdinaryRemoteSignInOff(t *testing.T) {
	s, _, _ := directFixture(t)
	ctx := WithSignInAdmission(context.Background(), SignInAdmission{Remote: true, Policy: RemoteSignInOff})
	code := requestResetFileCode(t, s)
	if _, e := s.DirectLogin(ctx, "owner", "Testing1!"); !errors.Is(e, ErrRemoteSignIn) {
		t.Fatal("fixture did not close ordinary remote sign-in", e)
	}
	if _, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code}); e != nil {
		t.Fatal("computer proof restricted to home network", e)
	}
}
func TestResetFilePorticoOwnerCannotMintPersistentAPICredential(t *testing.T) {
	s, db, _ := directFixture(t)
	ctx := context.Background()
	selectPorticoFixture(t, s)
	// The old Local password is no longer a fallback on a Portico server.
	if _, e := db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('account','portico-owner')`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DirectLogin(ctx, "owner", "Testing1!"); !errors.Is(e, ErrSignInKindPortico) {
		t.Fatal("linked owner accepted retained old password", e)
	}
	out, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: requestResetFileCode(t, s)})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Authenticate(out.Session.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	e = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		_, err := s.IssueAPICredentialTx(ctx, tx, p, CredentialKindToken, Token(), "persistent", time.Time{})
		return err
	})
	if !errors.Is(e, ErrForbidden) {
		t.Fatal("session-only reset minted persistent credential", e)
	}
}

func TestResetFilePreservesAuthenticatorCodesProfilesMyListAndHistory(t *testing.T) {
	s, db, owner := directFixture(t)
	ctx := context.Background()
	enrol, e := s.EnrolTwoFactor(ctx, owner.AccountToken, "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyTwoFactor(ctx, owner.AccountToken, "Testing1!", currentTOTP(t, enrol.Secret)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('preserved','Preserved','movie','/fixture-media');INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) SELECT 999,randomblob(16),id,1,'Preserved title','preserved','p' FROM catalog_libraries WHERE library_id='preserved';INSERT INTO personal_items(profile_id,item_id,watchlisted,revision) VALUES('profile',999,1,7);INSERT INTO progress(profile_id,item_id,position,playback_id) VALUES('profile',999,123,'preserved-playback');INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,completed) VALUES('history','profile',999,'start','update',7,123,0)`); e != nil {
		t.Fatal(e)
	}
	var before int
	if e = db.QueryRow(`SELECT count(*) FROM identity_recovery_codes WHERE account_id='account'`).Scan(&before); e != nil || before == 0 {
		t.Fatal("authenticator fixture missing", e)
	}
	out, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: requestResetFileCode(t, s)})
	if e != nil {
		t.Fatal(e)
	}
	var codes, profiles, saved, position, history, libraries int
	if e = db.QueryRow(`SELECT (SELECT count(*) FROM identity_recovery_codes WHERE account_id='account'),(SELECT count(*) FROM direct_profiles WHERE account_id='account'),(SELECT watchlisted FROM personal_items WHERE profile_id='profile' AND item_id=999),(SELECT position FROM progress WHERE profile_id='profile' AND item_id=999),(SELECT count(*) FROM personal_history WHERE profile_id='profile' AND id='history'),(SELECT count(*) FROM libraries WHERE id='preserved')`).Scan(&codes, &profiles, &saved, &position, &history, &libraries); e != nil {
		t.Fatal(e)
	}
	if codes != before || profiles != 1 || saved != 1 || position != 123 || history != 1 || libraries != 1 {
		t.Fatal("reset damaged authenticator or personal state")
	}
	if e = s.ChangeDirectPassword(ctx, out.Session.AccessToken, "", "Chosen-password1!", enrol.RecoveryCodes[0]); e != nil {
		t.Fatal("retained authenticator cannot finish password change", e)
	}
}

func TestResetFilePorticoMethodSurvivesLostAccountLinkWithoutInventingIdentity(t *testing.T) {
	s, db, _ := directFixture(t)
	ctx := context.Background()
	selectPorticoFixture(t, s)
	if _, e := db.Exec(`INSERT INTO onboarding_state_v1(singleton,auth_mode,operation_id,request_hash,setup_hash,receipt_until,ready) VALUES(1,'local','fixture','hash','hash',0,1)`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DirectLogin(ctx, "owner", "Testing1!"); !errors.Is(e, ErrSignInKindPortico) {
		t.Fatal("Portico server fell back to retained local password", e)
	}
	out, e := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: requestResetFileCode(t, s)})
	if e != nil {
		t.Fatal(e)
	}
	if !out.ResetFileOwnerSession || out.PasswordChangeRequired || out.Account.HostedAccountID != "" || out.Session == nil || out.Session.Viewer.Role != TierOwner {
		t.Fatal("lost account link changed method or fabricated Hosted facts")
	}
	if _, e = s.DirectMe(ctx, out.Session.AccessToken); e != nil {
		t.Fatal("reset-file owner session depends on lost Hosted link", e)
	}
}

func TestResetFileLocalMethodIgnoresStaleHostedOnboarding(t *testing.T) {
	s, db, _ := directFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO onboarding_state_v1(singleton,auth_mode,operation_id,request_hash,setup_hash,receipt_until,ready) VALUES(1,'hosted','fixture','hash','hash',0,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DirectLogin(ctx, "owner", "Testing1!"); err != nil {
		t.Fatal("stale onboarding changed the selected Local sign-in authority", err)
	}
	out, err := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: requestResetFileCode(t, s)})
	if err != nil {
		t.Fatal(err)
	}
	if out.ResetFileOwnerSession || !out.PasswordChangeRequired || out.Session == nil || out.Account.HostedAccountID != "" {
		t.Fatal("stale onboarding changed Local reset authority or invented identity")
	}
}

func TestOwnerResetExpiryIsNotAnOracleForGuessedOrRetiredCode(t *testing.T) {
	for _, mode := range []string{"guessed", "owner-epoch", "used"} {
		t.Run(mode, func(t *testing.T) {
			s, db, _ := directFixture(t)
			proof := requestResetFileCode(t, s)
			if _, err := db.Exec(`UPDATE owner_reset_file SET expires_at=0`); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "guessed":
				proof = "not-the-private-code"
			case "owner-epoch":
				if _, err := db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id='account'`); err != nil {
					t.Fatal(err)
				}
			case "used":
				if _, err := db.Exec(`UPDATE owner_reset_file SET code_hash=''`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.VerifyOwnerReset(context.Background(), OwnerResetVerify{Code: proof}); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("non-current proof disclosed expiry", err)
			}
		})
	}
}
