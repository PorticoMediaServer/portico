package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

// Synthetic persistence test: no player, network or provider process is started.
func TestRevisionPublicationSelectionAndScope(t *testing.T) {
	c := catalogtest.Open(t)
	db := c.DB
	library := c.Library("library", "Music", "music", "/synthetic")
	artist := c.Artist(library, "Synthetic Artist")
	album := c.Album(artist, "Synthetic Album", 2024)
	song := c.Song(album, 1, "/synthetic/song.flac", "Synthetic song")
	c.Drain()
	_, e := db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('playback','bearer','owner','profile',?,?,1,'ready','grant-hash','grant',?,'request',120)`, song.ID, song.Token, time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('playback',?,1000,1)`, song.Token); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	svc := &Service{DB: db}
	access := Access{Actor: Actor{Authority: "local", AccountID: "owner", ProfileID: "profile", Owner: true, SessionHash: "bearer"}, ServerID: "server", LibraryID: "library", ItemID: song.Public, ViewerFence: "fence", Authorize: func(*sql.Tx) error { return nil }}
	view, e := svc.View(ctx, access, Target{})
	if e != nil || view.Source == nil {
		t.Fatal(view, e)
	}
	target := Target{SourceID: view.Source.ID, SourceVersion: view.Source.Version}
	input := Mutation{Target: target, Scope: "private", Language: "en", Format: "lrc", Text: "[00:01]First\n[00:10]Next"}
	first, e := svc.Publish(ctx, access, input, "replace")
	if e != nil {
		t.Fatal(e)
	}
	other := access
	other.Actor = Actor{Authority: "local", AccountID: "other", ProfileID: "other", SessionHash: "different"}
	if _, e = svc.Read(ctx, other, target, first.ID, 1); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("private lyric visible to other actor", e)
	}
	sessionTarget := target
	sessionTarget.SessionID = "playback"
	if _, e = svc.Choose(ctx, access, Choose{Target: sessionTarget, ResourceID: first.ID, Revision: 1}); e != nil {
		t.Fatal(e)
	}
	offset := int64(500)
	second, e := svc.Publish(ctx, access, Mutation{Target: target, ResourceID: first.ID, ExpectedRevision: 1, OffsetMS: &offset}, "offset")
	if e != nil || second.Revision != 2 || second.OffsetMS != 500 {
		t.Fatal(second, e)
	}
	if _, e = svc.Publish(ctx, access, Mutation{Target: target, ResourceID: first.ID, ExpectedRevision: 1, OffsetMS: &offset}, "offset"); !errors.Is(e, ErrConflict) {
		t.Fatal("stale offset accepted", e)
	}
	active, e := svc.View(ctx, access, sessionTarget)
	if e != nil || active.Selection.Resource == nil || active.Selection.Resource.Revision != 1 {
		t.Fatal("pin changed on offset", active, e)
	}
	if _, e = svc.Publish(ctx, access, Mutation{Target: target, ResourceID: first.ID, ExpectedRevision: 2}, "delete"); e != nil {
		t.Fatal(e)
	}
	active, e = svc.View(ctx, access, sessionTarget)
	if e != nil || len(active.Resources) != 0 || active.Selection.Resource == nil || active.Selection.Resource.Revision != 1 {
		t.Fatal("deleted head or pin incorrect", active, e)
	}
	if _, e = svc.Choose(ctx, access, Choose{Target: sessionTarget, ExpectedSelectionRevision: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Read(ctx, access, sessionTarget, first.ID, 1); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("old revision readable without active pin", e)
	}
	input.Scope = "library"
	shared, e := svc.Publish(ctx, access, input, "replace")
	if e != nil {
		t.Fatal(e)
	}
	public, e := svc.Read(ctx, other, target, shared.ID, 1)
	if e != nil || public.CanManage {
		t.Fatal("shared read/manage permissions", public, e)
	}
	if _, e = svc.Publish(ctx, other, Mutation{Target: target, ResourceID: shared.ID, ExpectedRevision: 1}, "delete"); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("non-owner deleted shared lyrics", e)
	}
	if _, e = db.Exec(`UPDATE lyric_revisions SET language='fr' WHERE resource_id=?`, shared.ID); e == nil {
		t.Fatal("immutable revision updated")
	}
	denied := access
	denied.Authorize = func(*sql.Tx) error { return identity.ErrUnauthorized }
	if _, e = svc.View(ctx, denied, target); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("authorization callback ignored", e)
	}
	if _, e = db.Exec(`UPDATE catalog_assets SET modified_ns=201 WHERE token=?`, song.Token); e != nil {
		t.Fatal(e)
	}
	c.Drain()
	if _, e = svc.View(ctx, access, sessionTarget); !errors.Is(e, ErrSource) {
		t.Fatal("source replacement reused old playback", e)
	}
}
