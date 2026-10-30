package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func retirementFixture(t *testing.T) (*Service, *sql.DB, DirectSignIn, DirectProfile, ProfileDeletionPreview) {
	t.Helper()
	s, db, login := directFixture(t)
	ctx := context.Background()
	doc, err := s.CreateDirectProfile(ctx, login.AccountToken, "Kids", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var kid DirectProfile
	for _, p := range doc.Profiles {
		if !p.Primary {
			kid = p
		}
	}
	preview, err := s.PreviewDirectProfileDeletion(ctx, login.AccountToken, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, login, kid, preview
}
func TestProfileRetirementAtomicDenialAndLostResponseReplay(t *testing.T) {
	s, db, login, kid, preview := retirementFixture(t)
	ctx := context.Background()
	selected, err := s.SelectDirectProfile(ctx, login.AccountToken, kid.ID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	q := ProfileRetirementRequest{ExpectedRevision: preview.Revision, Lifetime: preview.Lifetime, DeleteSavedResources: true}
	q.Lifetime = "00000000000000000000000000000000"
	if _, err = s.RetireDirectProfile(ctx, login.AccountToken, kid.ID, q); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("cross-lifetime: %v", err)
	}
	q.Lifetime = preview.Lifetime
	out, err := s.RetireDirectProfile(ctx, login.AccountToken, kid.ID, q)
	if err != nil || out.State != "removing" {
		t.Fatalf("retire: %+v %v", out, err)
	}
	// Cache and an already captured principal must both lose authority at commit.
	if _, err = s.Authenticate(selected.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old bearer survived: %v", err)
	}
	err = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error { _, e := s.ReauthorizeTx(ctx, tx, before); return e })
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("late transaction survived: %v", err)
	}
	replay, err := s.RetireDirectProfile(ctx, login.AccountToken, kid.ID, q)
	if err != nil || replay != out {
		t.Fatalf("lost response changed promise: %+v %v", replay, err)
	}
	var jobs int
	if err = db.QueryRow(`SELECT count(*) FROM security_purge_jobs WHERE profile_id=?`, kid.ID).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("duplicate purge: %d %v", jobs, err)
	}
	var name string
	var deleted bool
	var pin sql.NullString
	if err = db.QueryRow(`SELECT name,deleted,pin_hash FROM direct_profiles WHERE id=?`, kid.ID).Scan(&name, &deleted, &pin); err != nil || !deleted || pin.Valid || name != "Kids" {
		t.Fatalf("retained label/retirement: %q %v %v %v", name, deleted, pin, err)
	}
	status, err := s.DirectProfileRetirement(ctx, login.AccountToken, kid.ID, preview.Lifetime)
	if err != nil || status.State != "removing" {
		t.Fatalf("status: %+v %v", status, err)
	}
	if _, err = s.DirectProfileRetirement(ctx, selected.Session.AccessToken, kid.ID, preview.Lifetime); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retired viewer read status: %v", err)
	}
}
func TestProfileRetirementEnqueueFailureRollsBackDenial(t *testing.T) {
	s, db, login, kid, preview := retirementFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TRIGGER fail_purge_install BEFORE INSERT ON security_purge_jobs BEGIN SELECT RAISE(ABORT,'install failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetireDirectProfile(ctx, login.AccountToken, kid.ID, ProfileRetirementRequest{ExpectedRevision: preview.Revision, Lifetime: preview.Lifetime, DeleteSavedResources: true}); err == nil {
		t.Fatal("false acceptance")
	}
	var deleted bool
	var revision int64
	if err := db.QueryRow(`SELECT deleted,revision FROM direct_profiles WHERE id=?`, kid.ID).Scan(&deleted, &revision); err != nil || deleted || revision != preview.Revision {
		t.Fatalf("partial retirement %v %d %v", deleted, revision, err)
	}
}

// Explicitly consume the real bounded worker in legacy physical-erasure tests.
func retireProfileAndDrainForTest(t *testing.T, s *Service, ctx context.Context, bearer, id string, expected int64, deleteSaved bool) error {
	t.Helper()
	var lifetime string
	if err := s.db.QueryRow(`SELECT lifetime FROM direct_profiles WHERE id=?`, id).Scan(&lifetime); err != nil {
		return err
	}
	if _, err := s.RetireDirectProfile(ctx, bearer, id, ProfileRetirementRequest{ExpectedRevision: expected, Lifetime: lifetime, DeleteSavedResources: deleteSaved}); err != nil {
		return err
	}
	for i := 0; i < 20000; i++ {
		worked, err := s.PurgeProfileStep(ctx)
		if err != nil {
			return err
		}
		if !worked {
			return nil
		}
	}
	t.Fatal("purge failed to converge")
	return nil
}

func TestProfileRetirementImmediatelyDeniesAnAlreadyPairedCastTarget(t *testing.T) {
	s, _, login, kid, preview := retirementFixture(t)
	selected, err := s.SelectDirectProfile(context.Background(), login.AccountToken, kid.ID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	screen := issueCastTarget(t, s, castScreen(t, "removed-child", "Kids TV"), login.Account.ID, kid.ID, selected.Session.SessionFamilyID)
	playback := WithPlaybackRequest(context.Background())
	if _, err = s.AuthenticateContext(playback, screen.AccessToken); err != nil {
		t.Fatalf("live Cast control: %v", err)
	}
	if _, err = s.RetireDirectProfile(context.Background(), login.AccountToken, kid.ID, ProfileRetirementRequest{ExpectedRevision: preview.Revision, Lifetime: preview.Lifetime, DeleteSavedResources: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateContext(playback, screen.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retired Cast target retained authority: %v", err)
	}
}
