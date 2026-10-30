package identity

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A device refreshes, the server commits the rotation, and the answer is lost
// because the server restarts. Hours later the device retries with the same
// refresh credential and the same request ID it persisted before sending. The
// restarted server must replay the committed answer, and the family must stay
// live: a lost response is not refresh-credential reuse (demo, 23 Sep: iOS and
// tvOS families revoked with refresh_reuse across server restarts before L4).
func TestLostRefreshAnswerAcrossRestartReplaysAndKeepsTheFamily(t *testing.T) {
	s, db, dir := familyFixture(t)
	ctx, err := WithIssuingDevice(context.Background(), registration(Token()), "198.51.100.21:9999")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.IssueTx(ctx, tx, "account", "profile", "local", "owner", 1, time.Time{})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var installation string
	if err = db.QueryRow(`SELECT installation_id FROM identity_devices WHERE id=?`, first.DeviceID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	request := Token()
	committed, err := s.RefreshSession(context.Background(), first.RefreshToken, installation, request)
	if err != nil {
		t.Fatal(err)
	}
	// The answer never reaches the device: the server restarts.
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	restart := func() (*Service, func()) {
		t.Helper()
		reopened, err := persistence.Open(filepath.Join(dir, "server.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		service, err := New(reopened, dir)
		if err != nil {
			reopened.Close()
			t.Fatal(err)
		}
		db = reopened
		return service, func() { reopened.Close() }
	}
	s, closeDB := restart()
	// Hours pass: every access token the device ever held has expired and the
	// maintenance pass has pruned what it prunes.
	if _, err = db.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE family_id=?`, time.Now().UTC().Add(-3*time.Hour).Format(time.RFC3339), first.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PruneSessionHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Several more restarts while the device is idle.
	closeDB()
	s, closeDB = restart()
	closeDB()
	s, closeDB = restart()
	defer closeDB()
	replayed, err := s.RefreshSession(context.Background(), first.RefreshToken, installation, request)
	if err != nil {
		t.Fatal("lost refresh answer was not replayed after restarts:", err)
	}
	if replayed.RefreshToken != committed.RefreshToken || replayed.SessionFamilyID != first.SessionFamilyID {
		t.Fatal("replay did not return the committed credentials")
	}
	var revoked int
	var reason string
	if err = db.QueryRow(`SELECT revoked,revoked_reason FROM authorization_session_families WHERE id=?`, first.SessionFamilyID).Scan(&revoked, &reason); err != nil || revoked != 0 || reason != "" {
		t.Fatalf("family revoked by a lost answer: revoked=%d reason=%q err=%v", revoked, reason, err)
	}
	// The device carries on with the successor it finally received.
	next, err := s.RefreshSession(context.Background(), replayed.RefreshToken, installation, Token())
	if err != nil {
		t.Fatal("successor refresh refused after the replay:", err)
	}
	if _, err = s.Authenticate(next.AccessToken); err != nil {
		t.Fatal("family unusable after the replay:", err)
	}
	if err = db.QueryRow(`SELECT revoked FROM authorization_session_families WHERE id=?`, first.SessionFamilyID).Scan(&revoked); err != nil || revoked != 0 {
		t.Fatalf("family revoked after the successor was used: %d %v", revoked, err)
	}
}
