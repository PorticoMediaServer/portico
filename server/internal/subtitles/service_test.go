package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type subtitleFixture struct {
	s       *Service
	db      *sql.DB
	catalog *catalogtest.Catalog
	item    catalogtest.Item
	p       identity.Principal
	allowed bool
}

func newSubtitleFixture(t *testing.T) *subtitleFixture {
	t.Helper()
	dir := canonicalFixtureDir(t)
	db, e := persistence.Open(filepath.Join(dir, "database.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	catalog := catalogtest.New(t, db)
	library := catalog.Library("library", "Movies", "movie", dir)
	item := catalog.Movie(library, filepath.Join(dir, "movie.mp4"), "Movie", 2024)
	catalog.Drain()
	f := &subtitleFixture{db: db, catalog: catalog, item: item, allowed: true, p: identity.Principal{Viewer: identity.Viewer{AccountID: "account", ProfileID: "profile", ServerID: "server", Authority: "local", Role: "owner"}, Hash: "login", Epoch: 1}}
	if _, e = db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('session','login','account','profile',?,?,1,'playing','hash','grant',?,'request',60)`, item.ID, item.Token, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('session',?,1000,1)`, item.Token); e != nil {
		t.Fatal(e)
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	f.s, e = New(Options{DB: db, Directory: filepath.Join(dir, "subtitles"), Storage: storage.New(binary), HelperBinary: binary, Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
		if !f.allowed || item != f.item.Public {
			return p, identity.ErrUnauthorized
		}
		return p, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.s.Close() })
	return f
}
func uploadMutation(op, body, source string) Mutation {
	return Mutation{OperationID: op, SourceID: source, Scope: "personal", Format: "srt", Language: "en", Title: "English", Rights: "Owner transcript", OffsetUS: "0", Content: &body}
}

const plainSubtitle = "1\n00:00:00,000 --> 00:00:02,000\nHello\n"

func TestPublicationCASIdempotencyAndScope(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	m := uploadMutation("upload", plainSubtitle, f.item.Token)
	first, e := f.s.Mutate(ctx, f.p, f.item.Public, m)
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.s.Mutate(ctx, f.p, f.item.Public, m)
	if e != nil || again != first {
		t.Fatalf("duplicate receipt: %+v %v", again, e)
	}
	changed := m
	changed.Title = "different"
	if _, e = f.s.Mutate(ctx, f.p, f.item.Public, changed); !errors.Is(e, ErrOperation) {
		t.Fatalf("operation collision: %v", e)
	}
	update := uploadMutation("replace", strings.ReplaceAll(plainSubtitle, "Hello", "Replaced"), f.item.Token)
	update.ResourceID = first.ResourceID
	update.ExpectedRevision = 1
	second, e := f.s.Mutate(ctx, f.p, f.item.Public, update)
	if e != nil || second.Revision != 2 {
		t.Fatal(second, e)
	}
	update.OperationID = "stale"
	if _, e = f.s.Mutate(ctx, f.p, f.item.Public, update); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale CAS: %v", e)
	}
	other := f.p
	other.ProfileID = "other"
	other.Role = "viewer"
	catalog, e := f.s.List(ctx, other, f.item.Public)
	if e != nil || len(catalog.Resources) != 0 {
		t.Fatal(catalog, e)
	}
	if _, e = f.s.Delete(ctx, other, f.item.Public, first.ResourceID, DeleteMutation{"delete", 2}); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatalf("private delete: %v", e)
	}
	shared := uploadMutation("shared", plainSubtitle, f.item.Token)
	shared.Scope = "shared"
	if _, e = f.s.Mutate(ctx, other, f.item.Public, shared); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatalf("nonowner sharing: %v", e)
	}
	if _, e = f.s.Mutate(ctx, f.p, f.item.Public, shared); e != nil {
		t.Fatal(e)
	}
	catalog, e = f.s.List(ctx, other, f.item.Public)
	if e != nil || len(catalog.Resources) != 1 || catalog.Resources[0].CanManage {
		t.Fatal(catalog, e)
	}
}
func TestPinnedReplacementDeleteOffAndRevocation(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	first, e := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("upload", plainSubtitle, f.item.Token))
	if e != nil {
		t.Fatal(e)
	}
	selectRequest := SelectRequest{OperationID: "select", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: first.ResourceID, ResourceRevision: 1, OffsetUS: "500000"}
	selected, e := f.s.Select(ctx, f.p, f.item.Public, "session", selectRequest)
	if e != nil || selected.OffsetUS != "500000" {
		t.Fatal(selected, e)
	}
	reader, e := f.s.OpenDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	update := uploadMutation("replace", strings.ReplaceAll(plainSubtitle, "Hello", "Goodbye"), f.item.Token)
	update.ResourceID = first.ResourceID
	update.ExpectedRevision = 1
	if _, e = f.s.Mutate(ctx, f.p, f.item.Public, update); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Delete(ctx, f.p, f.item.Public, first.ResourceID, DeleteMutation{"delete", 2}); e != nil {
		t.Fatal(e)
	}
	pinned, e := f.s.Plan(ctx, f.p, f.item.Public, "session")
	if e != nil || pinned.Selected == nil || pinned.Selected.Revision != 1 || !pinned.Selected.Retired || pinned.DocumentURL == "" || len(pinned.Resources) != 0 {
		t.Fatal(pinned, e)
	}
	bytes, e := io.ReadAll(reader)
	if e != nil || !strings.Contains(string(bytes), "Hello") || strings.Contains(string(bytes), "Goodbye") {
		t.Fatal(string(bytes), e)
	}
	if _, e = f.db.Exec(`UPDATE subtitle_obsolete_revisions SET due_ms=0`); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if e = f.s.CheckDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1); e != nil {
		t.Fatal("active pin collected", e)
	}
	off, e := f.s.Select(ctx, f.p, f.item.Public, "session", SelectRequest{OperationID: "off", Generation: 1, ExpectedRevision: pinned.Revision, Mode: "off", OffsetUS: "0"})
	if e != nil || off.Mode != "off" || off.DocumentURL != "" {
		t.Fatal(off, e)
	}
	if _, e = f.s.OpenDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatalf("Off retained an access grant: %v", e)
	}
	replay, e := f.s.Select(ctx, f.p, f.item.Public, "session", selectRequest)
	if e != nil || replay.Mode != "off" || replay.AppliedRevision != 2 {
		t.Fatal("old receipt resurrected track", replay, e)
	}
	f.allowed = false
	if e = f.s.CheckDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal(e)
	}
	if _, e = f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("upload", plainSubtitle, f.item.Token)); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatalf("receipt bypassed revocation: %v", e)
	}
}
func TestSourceEvidenceAndSelectionGenerationFence(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	r, e := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("upload", plainSubtitle, f.item.Token))
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Select(ctx, f.p, f.item.Public, "session", SelectRequest{OperationID: "badgeneration", Generation: 2, ExpectedRevision: 1, Mode: "track", ResourceID: r.ResourceID, ResourceRevision: 1, OffsetUS: "0"})
	if !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE catalog_assets SET modified_ns=21 WHERE token=?`, f.item.Token); e != nil {
		t.Fatal(e)
	}
	m := uploadMutation("metadata", plainSubtitle, f.item.Token)
	m.Content = nil
	m.ResourceID = r.ResourceID
	m.ExpectedRevision = 1
	if _, e = f.s.Mutate(ctx, f.p, f.item.Public, m); !errors.Is(e, ErrConflict) {
		t.Fatalf("metadata relinked stale bytes: %v", e)
	}
	if _, e = f.s.Plan(ctx, f.p, f.item.Public, "session"); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatalf("stale session source: %v", e)
	}
}
