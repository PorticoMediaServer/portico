package playback

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestChapterPageContinuesBeyondFourThousand(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, item, token := catalogFixture(t, db, "library", "/fixture", compactcatalog.Movie, "movie", "Movie", compactcatalog.Asset{Path: "/fixture/movie.mp4", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", Duration: 5000})
	if _, err = db.Exec(`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,1,1,1,'known','test')`, token); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4097; i++ {
		_, err = tx.Exec(`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,'Chapter',?,?)`, token, i, i-1, i)
		if err != nil {
			break
		}
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	s := New(db)
	session, err := s.Create(p, item, "automatic", "chapter-limit")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Chapters(context.Background(), p, OffersScope{"server", "library", item, "fence"}, session.ID, "", "", 100)
	if err != nil || page.TotalCount != 4097 || len(page.Chapters) != 100 || page.NextCursor == "" {
		t.Fatalf("total=%d page=%d cursor=%t: %v", page.TotalCount, len(page.Chapters), page.NextCursor != "", err)
	}
}
