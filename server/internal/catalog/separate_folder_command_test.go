package catalog

// P2-59 / O-022 SeparateFolder actual command: source tests.
//
// These are source-only regression tests for the bounded coordinator. They
// drive the real service method, the real mutation rows, the real per-file
// unit and the real retirement units. They do not fabricate a successful split:
// a pending call must return no public id, and a completed call must return the
// same separated id on retry.
//
// The claim hooks reproduce the existing cancellation/revocation statements
// literally. The catalogue cannot import downloads or preparedmedia
// (downloads -> playback -> catalog), so the production wrappers are not used
// here.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

type separateFolderCommandFixture struct {
	c       *catalogtest.Catalog
	source  string
	objects []string
	tokens  []string
	album   int64
}

// newSeparateFolderCommandFixture builds the reported 409-row shape: every
// track has one file in the selected folder and one elsewhere. The admission
// guard therefore sees here=files, total=2*files.
func newSeparateFolderCommandFixture(t *testing.T, files int) separateFolderCommandFixture {
	t.Helper()
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	var source string
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='music'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Release", 2020)
	f := separateFolderCommandFixture{c: c, source: source, album: album.ID}
	for n := 1; n <= files; n++ {
		song := c.Song(album, n, fmt.Sprintf("/music/flac/%02d.flac", n), fmt.Sprintf("Song %d", n))
		inventoryFile(t, c, source, song, fmt.Sprintf("flac/%02d.flac", n), "flac")
		_, token := c.File(song.ID, fmt.Sprintf("/music/mp3/%02d.mp3", n), 5400)
		object := inventoryFile(t, c, source, catalogtest.Item{Token: token}, fmt.Sprintf("mp3/%02d.mp3", n), "mp3")
		f.objects = append(f.objects, object)
		f.tokens = append(f.tokens, token)
	}
	c.Drain()
	return f
}

// TestSeparateFolderCommandActualThreeFileFolder is the original red-main
// shape through the real service method: three folder files, three originals
// left elsewhere, one separated public id, and an idempotent second call.
func TestSeparateFolderCommandActualThreeFileFolder(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 3)
	s := New(f.c.DB)
	ctx := WithSeparateFolderHooks(context.Background(), SeparateFolderHooks{})
	separated, err := s.SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if separated == "" {
		t.Fatal("separated folder returned no public id after finalize")
	}
	if n := rows(t, f.c, `SELECT count(*) FROM catalog_albums`); n != 2 {
		t.Fatalf("albums after separating a folder: %d", n)
	}
	id := f.c.ID(separated)
	if n := rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, id); n != 3 {
		t.Fatalf("folder files on the separated album: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, f.album); n != 0 {
		t.Fatalf("folder files left on the original album: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/flac/%'`, f.album); n != 3 {
		t.Fatalf("original elsewhere files were not preserved: %d", n)
	}
	again, err := s.SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if again != separated {
		t.Fatalf("retry returned a different separated id: %q then %q", separated, again)
	}
}

// TestSeparateFolderCommandPendingCarriesNoId proves a call that exhausts its
// bounded foreground quanta is honest backpressure: no public id is returned,
// the same command resumes, and only finalize produces the id.
func TestSeparateFolderCommandPendingCarriesNoId(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 12)
	s := New(f.c.DB)
	ctx := WithSeparateFolderHooks(context.Background(), SeparateFolderHooks{})
	separated, err := s.SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err == nil {
		if separated == "" {
			t.Fatal("completed split returned no public id")
		}
		return
	}
	if !errors.Is(err, ErrSeparateFolderPending) {
		t.Fatalf("first call did not report bounded pending: %v", err)
	}
	if separated != "" {
		t.Fatalf("pending call reported a completed public id %q", separated)
	}
	for i := 0; i < 4 && err != nil; i++ {
		separated, err = s.SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	}
	if err != nil {
		t.Fatal(err)
	}
	if separated == "" {
		t.Fatal("resumed split reported no public id")
	}
	again, err := s.SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil || again != separated {
		t.Fatalf("resumed split was not idempotent: %q, %v", again, err)
	}
}

// TestSeparateFolderCommandRetiresOriginalClaimsBeforeMovingTheFile seeds one
// real preparation and grant on the original child. The file must move, the
// grant must be revoked, the preparation must become unavailable/source_changed,
// and neither row may be re-pointed at the new child.
func TestSeparateFolderCommandRetiresOriginalClaimsBeforeMovingTheFile(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 1)
	var item int64
	if err := f.c.DB.QueryRow(`SELECT l.entity_id FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN inventory_objects o ON o.asset_id=a.token WHERE o.id=?`, f.objects[0]).Scan(&item); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,created_ms,updated_ms)
 VALUES('prep-1','p','owner','account','p',?, 'music','original','item','queued',1,1)`, item)
	f.c.Exec(`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms)
 VALUES('grant-1','prep-1','p',?,1,9999999999999)`, item)
	h := &separateFolderRetirementFixture{c: f.c, now: 1_700_000_000_000}
	rh := h.hooks()
	ctx := WithSeparateFolderHooks(context.Background(), SeparateFolderHooks{CancelJob: rh.CancelJob, Interrupt: rh.Interrupt, RevokeReceipt: rh.RevokeReceipt})
	s := New(f.c.DB)
	separated, err := s.SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM download_grants WHERE hash='grant-1' AND item_id=? AND revoked=1`, item); n != 1 {
		t.Fatalf("original grant was not revoked in place: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM download_preparations WHERE id='prep-1' AND item_id=? AND state='unavailable' AND reason='source_changed'`, item); n != 1 {
		t.Fatalf("original preparation was not retired in place: %d", n)
	}
	id := f.c.ID(separated)
	if n := rows(t, f.c, `SELECT count(*) FROM download_grants g JOIN download_preparations p ON p.id=g.preparation_id WHERE p.item_id=?`, id); n != 0 {
		t.Fatalf("claim rows were re-pointed at the new child: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, id); n != 1 {
		t.Fatalf("folder file did not move: %d", n)
	}
}

// TestSeparateFolderCommandInteractiveRowsEnvelope is the source envelope
// check: every measured writer transaction in the actual command (admission,
// retirement turns, per-file units and finalize) must stay within the unchanged
// interactive 160-row plan. It is an assertion in source, not a measured run
// claim by the reviewer.
func TestSeparateFolderCommandInteractiveRowsEnvelope(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 3)
	s := New(f.c.DB)
	ctx := WithSeparateFolderHooks(context.Background(), SeparateFolderHooks{})
	dbwork.ResetPlans()
	separated, err := s.SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if separated == "" {
		t.Fatal("no separated id")
	}
	stats := dbwork.Plans()
	if stats.Measured == 0 {
		t.Fatal("no measured writer transaction")
	}
	if stats.Largest.Rows > dbwork.DefaultPlan(dbwork.ClassInteractive).Rows {
		t.Fatalf("a separate-folder writer used %d rows against the interactive %d", stats.Largest.Rows, dbwork.DefaultPlan(dbwork.ClassInteractive).Rows)
	}
	if stats.Overruns != 0 {
		t.Fatalf("separate-folder writers overran their plan: %+v", stats.Recent)
	}
}

func TestSeparateFolderCommandRefusesMissingClaimHooks(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 1)
	var item int64
	if err := f.c.DB.QueryRow(`SELECT l.entity_id FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN inventory_objects o ON o.asset_id=a.token WHERE o.id=?`, f.objects[0]).Scan(&item); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,created_ms,updated_ms)
 VALUES('prep-1','p','owner','account','p',?, 'music','original','item','queued',1,1)`, item)
	f.c.Exec(`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms)
 VALUES('grant-1','prep-1','p',?,1,9999999999999)`, item)
	s := New(f.c.DB)
	separated, err := s.SeparateFolder(WithSeparateFolderHooks(context.Background(), SeparateFolderHooks{}), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if !errors.Is(err, ErrSeparateFolderHooks) {
		t.Fatalf("missing claim hooks did not fail closed: %v", err)
	}
	if separated != "" {
		t.Fatalf("missing claim hooks reported a public id %q", separated)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, f.album); n != 1 {
		t.Fatalf("file moved without retiring its claims: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM download_grants WHERE hash='grant-1' AND revoked=0`); n != 1 {
		t.Fatalf("claim changed without its owner hook: %d", n)
	}
}

// TestSeparateFolderCommandRetirementCursorKeepsVerifyAndPlanDigest proves the
// designated retirement store carries the engine's full cursor. A cursor that
// has finished the units but is still verifying must survive a restart with its
// Verify ordinal, and a plan digest that is not this compiled plan must block.
func TestSeparateFolderCommandRetirementCursorKeepsVerifyAndPlanDigest(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("books", "Books", "audiobook", "/books")
	book := c.Book(library, "Cursor", "Author")
	part := c.BookFile(book, 1, "/books/cursor/01.m4b")
	var source string
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='books'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	item := part.ID
	var child compactcatalog.EntityMutation
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		ref, err := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{
			Entity: item, OwnerKind: "separate", OwnerID: "cursor-fixture", OwnerGeneration: 1, Mode: "separate",
			SplitSourceID: source, SplitDirectory: "cursor", SplitObjectID: "object", SplitObjectRevision: "revision",
		})
		if err != nil {
			return err
		}
		child, err = compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		return err
	})
	plan, err := SeparateFolderRetirementUnits(item, SeparateFolderRetirementHooks{
		CancelJob:     func(context.Context, *sql.Tx, string) error { return nil },
		Interrupt:     func(string) {},
		RevokeReceipt: func(context.Context, *sql.Tx, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := SeparateFolderRetirementPlanDigest(item)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		cursor, err := separateFolderRetirementCursorTx(ctx, tx, child, digest, plan)
		if err != nil {
			return err
		}
		if cursor.Unit != 0 || cursor.Verify != 0 {
			return fmt.Errorf("new retirement cursor is not zero: %+v", cursor)
		}
		return separateFolderSaveRetirementCursorTx(ctx, tx, child, digest, phasedpurge.Cursor{Unit: len(plan), Verify: 3})
	})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		cursor, err := separateFolderRetirementCursorTx(ctx, tx, child, digest, plan)
		if err != nil {
			return err
		}
		if cursor.Unit != len(plan) || cursor.Verify != 3 {
			return fmt.Errorf("retirement cursor lost Verify: %+v", cursor)
		}
		return nil
	})
	c.Exec(`UPDATE catalog_purge_jobs SET plan_digest=? WHERE mutation_id=?`, strings.Repeat("c", 64), child.Ref.ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := separateFolderRetirementCursorTx(ctx, tx, child, digest, plan); !errors.Is(err, ErrSeparateFolderCursor) {
			return fmt.Errorf("changed plan digest was not refused: %v", err)
		}
		return nil
	})
}

func TestSeparateFolderRawOwnerAndChildHeadsSeek(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 1)
	for _, probe := range []struct {
		name, query, index string
		args               []any
	}{
		{"owner", separateFolderLatestOwnerSQL, "catalog_entity_mutations_owner", []any{"absent-owner"}},
		{"child", separateFolderNextChildSQL, "catalog_entities_parent", []any{f.album, int64(0)}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			plan := retirementExplain(t, f.c, probe.query, probe.args...)
			if !strings.Contains(plan, "SEARCH") || !strings.Contains(plan, probe.index) || strings.Contains(plan, "SCAN") || strings.Contains(plan, "TEMP B-TREE") {
				t.Fatal(plan)
			}
		})
	}
}

func TestSeparateFolderAcceptedUnavailableRawObjectIsRefused(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 1)
	// The earliest object remains part of the accepted folder even if a later
	// duplicate is available. Never skip the raw head to claim a complete split.
	f.c.Exec(`UPDATE inventory_objects SET state='missing',missing_since='2026-01-01T00:00:00Z' WHERE id=?`, f.objects[0])
	f.c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,directory_path,revision,evidence_json,size,modified_ns,state) SELECT 'zz-later',source_id,asset_id,root_incarnation,'later.mp3',directory_path,'r2','{}',size,modified_ns,'available' FROM inventory_objects WHERE id=?`, f.objects[0])
	var asset, original int
	if err := f.c.DB.QueryRow(`SELECT count(*),count(*) FROM catalog_asset_links`).Scan(&asset, &original); err != nil {
		t.Fatal(err)
	}
	id, err := New(f.c.DB).SeparateFolder(context.Background(), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if id != "" || !errors.Is(err, compactcatalog.ErrMutationStale) {
		t.Fatalf("raw unavailable head was skipped: %q %v", id, err)
	}
	if got := rows(t, f.c, `SELECT count(*) FROM catalog_entity_mutations WHERE mode='separate'`); got != 0 {
		t.Fatalf("refusal admitted %d mutations", got)
	}
	if got := rows(t, f.c, `SELECT count(*) FROM catalog_asset_links`); got != asset {
		t.Fatalf("refusal changed links: %d -> %d", original, got)
	}
}
