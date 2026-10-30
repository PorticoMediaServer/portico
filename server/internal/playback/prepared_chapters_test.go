package playback

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPreparedChaptersNeverProjectOriginalClock(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "chapters.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A prepared pin must not consult or project the original chapter clock:
	// the session resolves, the prepared pin short-circuits, and no chapter
	// fact is read. Items are catalogue entities, assets tokens.
	entity, item, token := catalogFixture(t, db, "l", "/x", compactcatalog.Movie, "fixture", "T", compactcatalog.Asset{Path: "/x", Size: 200, ModifiedNS: 2, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 60})
	if _, err = db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('session','current','account','profile',?,?,3,'playing','g','grant','2100-01-01T00:00:00Z','req',60)`, entity, token); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('session',?,100,1)`, token); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms,version_id) VALUES('job',?,?,'l','profile','target','{}','source','{}','succeeded','complete',1,1,'prepared-version')`, entity, token); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,edition_id,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES('prepared-version',?,?,'l','profile','target','source','{}',0,NULL,'{}','transform','digest',1,'{}','published',1,'job')`, entity, token); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO prepared_media_session_pins VALUES('session','prepared-version','d',1)`); err != nil {
		t.Fatal(err)
	}
	s := &Service{db: db}
	p := identity.Principal{Hash: "current", Viewer: identity.Viewer{AccountID: "account", ProfileID: "profile"}}
	scope := OffersScope{ServerID: "server", LibraryID: "l", ItemID: item, ViewerFence: "viewer"}
	out, err := s.Chapters(context.Background(), p, scope, "session", "", "", 100)
	if err != nil || out.Status != "unavailable" || out.Reason == nil || *out.Reason != "prepared_chapter_mapping_unavailable" || len(out.Chapters) != 0 || out.TotalCount != 0 {
		t.Fatal("original seek clock exposed", out, err)
	}
	if _, err = s.Chapters(context.Background(), p, scope, "session", out.Revision, "", 100); err != nil {
		t.Fatal("stable unavailable projection", err)
	}
	if _, err = s.Chapters(context.Background(), p, scope, "session", out.Revision, "old-cursor", 100); !errors.Is(err, ErrStaleChapter) {
		t.Fatal("stale original cursor admitted", err)
	}
	p.ProfileID = "other"
	if _, err = s.Chapters(context.Background(), p, scope, "session", "", "", 100); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign profile admitted", err)
	}
}
