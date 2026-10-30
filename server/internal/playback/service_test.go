package playback

import (
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestSingleOfferAndStaleSessionProgress(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	id, item, _ := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "item", "Film", compactcatalog.Asset{Path: "/test/file.mp4", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 100})
	s := New(db)
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	insertFixtureSession(t, db, p.Hash, p.AccountID, p.ProfileID, p.Role, "2999-01-01T00:00:00Z")
	a, e := s.Create(p, item, "automatic", "one")
	if e != nil || a.Mode != "direct" {
		t.Fatalf("single offer %+v %v", a, e)
	}
	again, e := s.Create(p, item, "automatic", "one")
	if e != nil || again.ID != a.ID {
		t.Fatal("creation was not idempotent", e)
	}
	if e = s.Progress(p, a.ID, a.Generation, 10, 30, "playing"); e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(p, item, "automatic", "two")
	if e != nil {
		t.Fatal(e)
	}
	// Separate controllers/devices sharing a profile do not implicitly hand off.
	stillActive, err := s.Get(p, a.ID)
	if err != nil || stillActive.State == "stopped" {
		t.Fatal("another device stopped existing playback", stillActive, err)
	}
	if b.ResumeSeconds != 30 || b.Generation <= a.Generation {
		t.Fatalf("new generation %+v", b)
	}
	if e = s.Progress(p, a.ID, a.Generation, 100, 80, "playing"); e != nil {
		t.Fatal(e)
	}
	if e = s.Progress(p, b.ID, b.Generation, 2, 40, "paused"); e != nil {
		t.Fatal(e)
	}
	if e = s.Progress(p, b.ID, b.Generation, 1, 90, "playing"); e != nil {
		t.Fatal(e)
	}
	var pos int64
	if e = db.QueryRow(`SELECT position FROM progress WHERE item_id=?`, id).Scan(&pos); e != nil || pos != 40000 {
		t.Fatalf("stale write changed progress %v %v", pos, e)
	}
	if e = s.Stop(p, b.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Stop(p, b.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Progress(p, b.ID, b.Generation, 3, 95, "ended"); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow(`SELECT position FROM progress WHERE item_id=?`, id).Scan(&pos)
	if pos != 40000 {
		t.Fatal("stop did not fence progress")
	}
	if _, e = db.Exec(`UPDATE playback_sessions SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, b.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Cleanup(); e != nil {
		t.Fatal(e)
	}
	// Renewal keeps this controller's family. A bare unrelated token must not
	// replay another device's receipt merely because the profile matches.
	if _, e = db.Exec(`UPDATE authorization_session_families SET current_generation=2 WHERE id='fixture-family-session';
 INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('new-login-session','fixture-family-session',2,'2999-01-01T00:00:00Z')`); e != nil {
		t.Fatal(e)
	}
	p.Hash = "new-login-session"
	replayed, e := s.Create(p, item, "automatic", "two")
	if e != nil || replayed.ID != b.ID || replayed.State != "stopped" {
		t.Fatalf("durable stopped receipt lost after cleanup %+v %v", replayed, e)
	}

}

func TestSharedEpisodeBoundaryAndIndependentWholeSourceProgress(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	catalogLibrary(t, db, "tv", "TV", "tv", "/tv")
	id1, ep1, token := catalogFixture(t, db, "tv", "/tv", compactcatalog.Episode, "episode1", "Episode 1", compactcatalog.Asset{Path: "/tv/show.mp4", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 120})
	id2, ep2, _ := catalogFixture(t, db, "tv", "/tv", compactcatalog.Episode, "episode2", "Episode 2", compactcatalog.Asset{Path: "/tv/show.mp4", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 120})
	// Both episodes share the one file (same path links the same asset).
	if _, err = db.Exec(`INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(? ,?,'unknown_multi_episode'),(? ,?,'unknown_multi_episode')`, id1, token, id2, token); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	first, err := s.Create(p, ep1, "automatic", "first")
	if err != nil || first.SourceBoundary != "unknown_multi_episode" || first.Duration != 120 {
		t.Fatal(first, err)
	}
	if err = s.Progress(p, first.ID, first.Generation, 1, 90, "paused"); err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(p, ep2, "automatic", "second")
	if err != nil || second.ResumeSeconds != 0 || second.Duration != 120 {
		t.Fatal(second, err)
	}
	if err = s.Progress(p, second.ID, second.Generation, 1, 35, "paused"); err != nil {
		t.Fatal(err)
	}
	var a, b int64
	if err = db.QueryRow(`SELECT position FROM progress WHERE item_id=?`, id1).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT position FROM progress WHERE item_id=?`, id2).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if a != 90000 || b != 35000 {
		t.Fatal("logical progress mixed", a, b)
	}
	current, err := s.Get(p, second.ID)
	if err != nil || current.SourceBoundary != "unknown_multi_episode" {
		t.Fatal(current, err)
	}
	repeated, err := s.Create(p, ep1, "automatic", "first")
	if err != nil || repeated.SourceBoundary != "unknown_multi_episode" || repeated.ID != first.ID {
		t.Fatal(repeated, err)
	}
}
