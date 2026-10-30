package playback

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A chapter carries a thumbnail URL only when a current chapter image exists
// for this exact source revision. Everything else publishes no URL.
func TestChapterThumbnailURLTracksCurrentChapterImages(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, item, token := catalogFixture(t, db, "l", "/fixture", compactcatalog.Movie, "i", "I", compactcatalog.Asset{Path: "/fixture/a", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 1000})
	// A session accepts only a confirmed object under a confirmed root.
	_, e = db.Exec(`UPDATE library_sources SET generation=4,incarnation='inc',enabled=1,root_identity='root',identity_confirmed=1,health='ready' WHERE id='l';
INSERT INTO inventory_objects(id,source_id,asset_id,revision,state,retired,root_incarnation,relative_path,object_identity,evidence_json,size,modified_ns) VALUES('o','l',?, 'rev','available',0,'inc','a','object','{}',1,1)`, token)
	if e != nil {
		t.Fatal(e)
	}
	facts := assets.Facts{Duration: 1000, ChapterStatus: "known", Chapters: []assets.Chapter{
		{Title: "One", Start: 0, End: 10}, {Title: "Two", Start: 10, End: 20}, {Title: "Three", Start: 20, End: 30},
	}}
	tx, _ := db.Begin()
	if e = assets.PersistChapters(tx, token, 1, 1, facts); e != nil {
		t.Fatal(e)
	}
	tx.Commit()
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "account", ProfileID: "profile"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	s := New(db)
	session, e := s.Create(p, item, "automatic", "one")
	if e != nil {
		t.Fatal(e)
	}
	scope := OffersScope{"server", "l", item, "fence"}
	bare, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil || len(bare.Chapters) != 3 {
		t.Fatal(bare, e)
	}
	for _, c := range bare.Chapters {
		if c.ThumbnailURL != "" {
			t.Fatal("thumbnail published without a chapter image", c)
		}
	}
	publish := func(result, revision string, incarnation string, generation int64, ordinals ...int) {
		t.Helper()
		if _, e := db.Exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) VALUES(?,'o',?,?,?, ?,1,'chapter_images','alg','','tool',1000000000,'{}',1)`, result, token, revision, incarnation, generation); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`INSERT INTO analysis_heads(object_id,source_revision,stage,result_id) VALUES('o',?,'chapter_images',?) ON CONFLICT(object_id,source_revision,stage) DO UPDATE SET result_id=excluded.result_id`, revision, result); e != nil {
			t.Fatal(e)
		}
		for _, ordinal := range ordinals {
			if _, e := db.Exec(`INSERT INTO analysis_artifacts(id,result_id,digest,size,kind,mime,ordinal,start_us,end_us,width,height) VALUES(?,?,'digest',1,'chapter_images','image/jpeg',?,0,1,320,180)`, result+":"+string(rune('a'+ordinal)), result, ordinal); e != nil {
				t.Fatal(e)
			}
		}
	}
	// Chapter images are produced in chapter order from ordinal zero, so only the
	// first and third chapters here have a preview.
	publish("result-1", "rev", "inc", 4, 0, 2)
	withImages, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil || len(withImages.Chapters) != 3 {
		t.Fatal(withImages, e)
	}
	if withImages.Revision == bare.Revision {
		t.Fatal("gaining thumbnails did not change the projection revision")
	}
	want := []string{"/v1/items/" + item + "/chapters/" + token + ":1/image", "", "/v1/items/" + item + "/chapters/" + token + ":3/image"}
	for i, c := range withImages.Chapters {
		if c.ThumbnailURL != want[i] {
			t.Fatal("thumbnail URL", i, c.ThumbnailURL, want[i])
		}
	}
	// A held revision must not survive a thumbnail change.
	if _, e = s.Chapters(context.Background(), p, scope, session.ID, bare.Revision, "", 100); e == nil {
		t.Fatal("stale revision accepted after thumbnails appeared")
	}
	// A result for another source revision is not this file's preview.
	if _, e = db.Exec(`UPDATE analysis_results SET source_revision='other';UPDATE analysis_heads SET source_revision='other'`); e != nil {
		t.Fatal(e)
	}
	otherRevision, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range otherRevision.Chapters {
		if c.ThumbnailURL != "" {
			t.Fatal("a superseded revision published thumbnails", c)
		}
	}
	// Neither is a retired result nor a reconfigured root.
	if _, e = db.Exec(`UPDATE analysis_results SET source_revision='rev';UPDATE analysis_heads SET source_revision='rev';UPDATE analysis_results SET retired_ms=1`); e != nil {
		t.Fatal(e)
	}
	retired, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range retired.Chapters {
		if c.ThumbnailURL != "" {
			t.Fatal("a retired result published thumbnails", c)
		}
	}
	if _, e = db.Exec(`UPDATE analysis_results SET retired_ms=0,configuration_generation=9`); e != nil {
		t.Fatal(e)
	}
	reconfigured, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range reconfigured.Chapters {
		if c.ThumbnailURL != "" {
			t.Fatal("a reconfigured root published thumbnails", c)
		}
	}
	if reconfigured.Revision != bare.Revision {
		t.Fatal("removing every thumbnail did not restore the original revision")
	}
}
