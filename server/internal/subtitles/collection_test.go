package subtitles

import (
	"context"
	"strings"
	"testing"
)

func (f *subtitleFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if e := f.db.QueryRow(query, args...).Scan(&n); e != nil {
		t.Fatal(query, e)
	}
	return n
}

func (f *subtitleFixture) collectDue(t *testing.T) {
	t.Helper()
	if _, e := f.db.Exec(`UPDATE subtitle_obsolete_revisions SET due_ms=0`); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Cleanup(context.Background()); e != nil {
		t.Fatal(e)
	}
}

// C1: a superseded revision is collected once due, with its render and
// original rows, and the current revision is never touched.
func TestCollectionRemovesSupersededRevisions(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	first, e := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("upload", plainSubtitle, f.item.Token))
	if e != nil {
		t.Fatal(e)
	}
	update := uploadMutation("replace", strings.ReplaceAll(plainSubtitle, "Hello", "Goodbye"), f.item.Token)
	update.ResourceID, update.ExpectedRevision = first.ResourceID, 1
	if _, e = f.s.Mutate(ctx, f.p, f.item.Public, update); e != nil {
		t.Fatal(e)
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_obsolete_revisions WHERE resource_id=? AND revision=1`, first.ResourceID); n != 1 {
		t.Fatalf("superseded revision queued %d times", n)
	}
	// Not yet due: nothing goes.
	if e = f.s.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_revisions WHERE resource_id=?`, first.ResourceID); n != 2 {
		t.Fatalf("collected before due: %d revisions", n)
	}
	f.collectDue(t)
	if n := f.count(t, `SELECT count(*) FROM subtitle_revisions WHERE resource_id=? AND revision=1`, first.ResourceID); n != 0 {
		t.Fatal("superseded revision kept")
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_render_revisions WHERE resource_id=? AND revision=1`, first.ResourceID); n != 0 {
		t.Fatal("superseded render revision kept")
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_revisions WHERE resource_id=? AND revision=2`, first.ResourceID); n != 1 {
		t.Fatal("current revision collected")
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_obsolete_revisions`); n != 0 {
		t.Fatalf("%d queue entries left", n)
	}
}

// C1: a deleted resource leaves entirely, so the row ceiling counts live
// tracks; a manifest reference postpones collection instead of failing it.
func TestCollectionRemovesDeletedResourcesAndWaitsForManifests(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	first, e := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("upload", plainSubtitle, f.item.Token))
	if e != nil {
		t.Fatal(e)
	}
	bound := `SELECT rows FROM maintenance_table_bounds WHERE table_name='subtitle_resources'`
	before := f.count(t, bound)
	if _, e = f.s.Delete(ctx, f.p, f.item.Public, first.ResourceID, DeleteMutation{"delete", 1}); e != nil {
		t.Fatal(e)
	}
	// A manifest naming the revision holds it (the foreign key would otherwise
	// refuse the delete and stall every later entry).
	if _, e = f.db.Exec(`INSERT INTO playback_manifest_subtitles(session_id,resource_id,resource_revision) VALUES('session',?,1)`, first.ResourceID); e != nil {
		t.Fatal(e)
	}
	f.collectDue(t)
	if n := f.count(t, `SELECT count(*) FROM subtitle_revisions WHERE resource_id=?`, first.ResourceID); n != 1 {
		t.Fatal("held revision collected")
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_obsolete_revisions WHERE resource_id=? AND revision=1 AND due_ms>0`, first.ResourceID); n != 1 {
		t.Fatal("held revision was not postponed")
	}
	if _, e = f.db.Exec(`DELETE FROM playback_manifest_subtitles WHERE session_id='session'`); e != nil {
		t.Fatal(e)
	}
	f.collectDue(t)
	if n := f.count(t, `SELECT count(*) FROM subtitle_resources WHERE id=?`, first.ResourceID); n != 0 {
		t.Fatal("deleted resource kept")
	}
	if after := f.count(t, bound); after != before-1 {
		t.Fatalf("resource bound counts %d, want %d", after, before-1)
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_obsolete_revisions`); n != 0 {
		t.Fatalf("%d queue entries left", n)
	}
}

// An entry whose revision became current again is forgotten, not collected.
func TestCollectionForgetsRevisionsThatAreCurrentAgain(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	first, e := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("upload", plainSubtitle, f.item.Token))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`INSERT INTO subtitle_obsolete_revisions(resource_id,revision,due_ms) VALUES(?,1,0)`, first.ResourceID); e != nil {
		t.Fatal(e)
	}
	f.collectDue(t)
	if n := f.count(t, `SELECT count(*) FROM subtitle_revisions WHERE resource_id=? AND revision=1`, first.ResourceID); n != 1 {
		t.Fatal("current revision collected")
	}
	if n := f.count(t, `SELECT count(*) FROM subtitle_obsolete_revisions`); n != 0 {
		t.Fatal("stale entry kept")
	}
}

// A3: a pass reads only due entries in index order, whatever the queue holds.
func TestCollectionDueQueryUsesItsIndexWithoutSorting(t *testing.T) {
	f := newSubtitleFixture(t)
	rows, e := f.db.Query(`EXPLAIN QUERY PLAN SELECT resource_id,revision FROM subtitle_obsolete_revisions INDEXED BY subtitle_obsolete_revisions_due WHERE due_ms<=? ORDER BY due_ms,revision DESC LIMIT ?`, 0, collectionPage)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
			t.Fatal(e)
		}
		plan = append(plan, detail)
	}
	if text := strings.Join(plan, "; "); strings.Contains(text, "TEMP B-TREE") || !strings.Contains(text, "subtitle_obsolete_revisions_due") {
		t.Fatal(text)
	}
}
