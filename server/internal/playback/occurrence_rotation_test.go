package playback

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// R-03: an admitted transcode stays live across a routine token refresh in
// the same session family, and still ends when that family is revoked.
func TestOccurrenceStaysLiveAcrossTokenRotation(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, item, _ := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "item", "Film", compactcatalog.Asset{Path: "/test/file.mp4", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 100})
	s := New(db)
	p := identity.Principal{Hash: "started", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	insertFixtureSession(t, db, p.Hash, p.AccountID, p.ProfileID, p.Role, "2999-01-01T00:00:00Z")
	a, err := s.Create(p, item, "automatic", "one")
	if err != nil {
		t.Fatal(err)
	}
	if !occurrenceLive(db, a.ID, a.Generation) || !occurrenceLive(db, a.ID, 0) {
		t.Fatal("a fresh occurrence is not live")
	}
	// A refresh: the family moves to generation 2 and the starting token is no
	// longer current. No progress report has rebound the occurrence yet.
	if _, err = db.Exec(`UPDATE authorization_session_families SET current_generation=2 WHERE id='fixture-family-started';
 INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('refreshed','fixture-family-started',2,'2999-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if !occurrenceLive(db, a.ID, a.Generation) || !occurrenceLive(db, a.ID, 0) {
		t.Fatal("a token refresh in the same family ended the transcode")
	}
	if occurrenceLive(db, a.ID, a.Generation+1) {
		t.Fatal("another occurrence generation was accepted")
	}
	if _, err = db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id='fixture-family-started'`); err != nil {
		t.Fatal(err)
	}
	if occurrenceLive(db, a.ID, a.Generation) {
		t.Fatal("a revoked family kept the transcode live")
	}
}
