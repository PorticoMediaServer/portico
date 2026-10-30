package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
)

func artworkCounter(t *testing.T, db *sql.DB, slot, format, outcome string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COALESCE((SELECT count FROM artwork_encoding_counters WHERE slot=? AND format=? AND outcome=?),0)`, slot, format, outcome).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// One decode makes both slots and the ambient effect. The same accepted bytes
// are the same source: publishing them again at the same policy decodes
// nothing, writes no file and counts nothing twice.
func TestArtworkMaterialisationIsOncePerAcceptedImage(t *testing.T) {
	s, db, _ := repairFixture(t)
	raw := editorJPEG(t, 1200, 1800)
	first := publishTestArtwork(t, s, raw, "2026-10-28T00:00:00Z")
	if first.reused || first.acceptedWidth != 1200 || first.acceptedHeight != 1800 || first.acceptedMime != "image/jpeg" {
		t.Fatal("first arrival", first.reused, first.acceptedWidth, first.acceptedHeight, first.acceptedMime)
	}
	if max(first.small.width, first.small.height) != artworkSmallEdge || max(first.large.width, first.large.height) != 1800 || max(first.ambient.width, first.ambient.height) != ArtworkAmbientEdge {
		t.Fatal("slot edges", first.small, first.large, first.ambient)
	}
	if artworkCounter(t, db, "small", "jpeg", "within") != 1 || artworkCounter(t, db, "large", "jpeg", "within") != 1 {
		t.Fatal("publication was not counted")
	}
	entries, err := os.ReadDir(s.cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	again := publishTestArtwork(t, s, raw, "2026-10-28T00:00:01Z")
	after, err := os.ReadDir(s.cacheRoot)
	if err != nil || !again.reused || again.source != first.source || len(after) != len(entries) {
		t.Fatal("the same image was materialised twice", again.reused, len(entries), len(after), err)
	}
	if artworkCounter(t, db, "small", "jpeg", "within") != 1 {
		t.Fatal("a reused source was counted again")
	}
	// A file gone from disk means the source is no longer reusable as is.
	if err = os.Remove(filepath.Join(s.cacheRoot, first.small.digest+".img")); err != nil {
		t.Fatal(err)
	}
	if third := publishTestArtwork(t, s, raw, "2026-10-28T00:00:02Z"); third.reused {
		t.Fatal("a source missing a file was reused")
	}
	if _, err = os.Stat(filepath.Join(s.cacheRoot, first.small.digest+".img")); err != nil {
		t.Fatal("re-materialisation did not restore the missing file", err)
	}
}

// An upload past its cap at the quality floor is refused with its own typed
// error and counted; nothing is published and the previous choice stays.
func TestArtworkUploadRefusesAnUnreachableCap(t *testing.T) {
	s, db, target := repairFixture(t)
	ctx := context.Background()
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	noise := image.NewNRGBA(image.Rect(0, 0, 533, 800))
	random := rand.New(rand.NewSource(7))
	for i := range noise.Pix {
		noise.Pix[i] = byte(random.Intn(256))
		if i%4 == 3 {
			noise.Pix[i] = 255
		}
	}
	if _, err = s.UploadArtwork(ctx, target, "poster", "", state.Revision, encodeTestPNG(t, noise), editorActor(), allowAll); !errors.Is(err, ErrArtworkCapUnreachable) {
		t.Fatal("unreachable cap was not refused with its own error", err)
	}
	var sources, selections int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM artwork_sources),(SELECT count(*) FROM artwork_selections)`).Scan(&sources, &selections); err != nil || sources != 0 || selections != 0 {
		t.Fatal("a refused picture was published", sources, selections, err)
	}
	if artworkCounter(t, db, "small", "jpeg", "cap_unreachable") != 1 {
		t.Fatal("the refusal was not counted")
	}
	counts, err := s.ArtworkEncodingCounts(ctx)
	if err != nil || len(counts) != 1 || counts[0] != (ArtworkEncodingCounter{"small", "jpeg", "cap_unreachable", 1}) {
		t.Fatal("reported counters", counts, err)
	}
}

// Collection runs sources first, then files: an unnamed source goes, and with
// it every file no other source names; a file another source shares stays.
// An owner-history pin keeps a source, and pins never starve later candidates.
func TestArtworkCollectionPassesAndPins(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	old := "2020-01-01T00:00:00Z"
	insert := func(n int, small string) string {
		digest := fmt.Sprintf("%064x", n)
		for _, blob := range []string{digest, small} {
			integrationExec(t, db, fixtureArtworkBlobSQL, blob)
			integrationExec(t, db, `UPDATE artwork_blobs SET created_at=? WHERE digest=?`, old, blob)
			if err := os.WriteFile(filepath.Join(s.cacheRoot, blob+".img"), []byte(blob), 0600); err != nil {
				t.Fatal(err)
			}
		}
		integrationExec(t, db, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/jpeg',1,1,?,?,?,1920,0,0,1,?)`, digest, small, digest, digest, old)
		return digest
	}
	shared := fmt.Sprintf("%064x", 9999)
	// 64 pinned sources come first in collection order; the 65th is free.
	for n := 1; n <= 64; n++ {
		pinned := insert(n, shared)
		integrationExec(t, db, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('item',?,'a','b','upload','owner',?,?,'{}')`, n, old, `{"digest":"`+pinned+`"}`)
	}
	free := insert(65, shared)
	exists := func(table, digest string) bool {
		var ok bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE digest=?)`, digest).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for pass := 0; pass < 3 && exists("artwork_sources", free); pass++ {
		if err := s.CleanupArtwork(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if exists("artwork_sources", free) {
		t.Fatal("pinned sources starved a later candidate")
	}
	for pass := 0; pass < 3 && exists("artwork_blobs", free); pass++ {
		if err := s.CleanupArtwork(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if exists("artwork_blobs", free) || !exists("artwork_blobs", shared) || !exists("artwork_sources", fmt.Sprintf("%064x", 1)) {
		t.Fatal("collection removed a named file or kept an unnamed one")
	}
	if _, err := os.Stat(filepath.Join(s.cacheRoot, free+".img")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("collected file stayed on disk", err)
	}
	if _, err := os.Stat(filepath.Join(s.cacheRoot, shared+".img")); err != nil {
		t.Fatal("a shared file lost its bytes", err)
	}
}

// HIST1: the destructive delete refuses when an owner-history row was written
// after the pass read its high-water mark, so a new undo point can never lose
// its source between the history scan and the delete.
func TestArtworkSourceDeleteRefusesAfterANewHistoryRow(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	source := fmt.Sprintf("%064x", 1)
	insertFixtureArtworkSource(t, db, source)
	var highWater int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM metadata_owner_history`).Scan(&highWater); err != nil {
		t.Fatal(err)
	}
	integrationExec(t, db, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('item',1,'a','b','upload','owner','now',?,'{}')`, `{"digest":"`+source+`"}`)
	if deleted, err := s.deleteUnreferencedArtworkSource(ctx, source, highWater); err != nil || deleted {
		t.Fatal("deleted a source pinned after the history scan", deleted, err)
	}
	if err := db.QueryRow(`SELECT MAX(id) FROM metadata_owner_history`).Scan(&highWater); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.deleteUnreferencedArtworkSource(ctx, source, highWater); err != nil || !deleted {
		t.Fatal("the relational recheck alone did not delete an unnamed source", deleted, err)
	}
}

// The batched descriptor resolver every grid uses names exactly the source
// the item art route serves, including inherited album, book, season and show
// art, and describes both of its slots.
func TestItemArtworkSourcesMatchTheServingRoute(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	ctx := context.Background()
	for n, owner := range []struct{ kind, name, role string }{
		{"item", "movie", "poster"}, {"show", "show", "poster"}, {"season", "season", "poster"}, {"show", "show", "backdrop"},
		{"album", "album", "cover"}, {"book", "book", "cover"},
	} {
		source := fmt.Sprintf("%064x", n+1)
		insertFixtureArtworkSource(t, db, source)
		entity := names[owner.name].ID
		candidate := fmt.Sprintf("parity-%d", n)
		integrationExec(t, db, `INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,?,?,?,'','fixture',?,'local','fixture','fence','now')`, candidate, owner.kind, entity, owner.role, candidate)
		integrationExec(t, db, `INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) VALUES(?,?,?,'',?,?,0,1,'fixture','now')`, owner.kind, entity, owner.role, candidate, source)
	}
	ids := []string{}
	for _, name := range []string{"movie", "episode", "song", "part"} {
		ids = append(ids, names[name].Public)
	}
	for _, role := range []string{"poster", "backdrop", "cover"} {
		refs, err := artworkref.ItemSources(ctx, db, ids, role)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			want, _, err := s.EntityArtworkIdentity(ctx, RepairTarget{"item", id}, role, "", false)
			if err != nil {
				t.Fatal(err)
			}
			ref, ok := refs[id]
			if ok != (want != "") || ref.Source != want {
				t.Fatal("batched source differs from the route", role, id, ref.Source, want)
			}
			if ok && (ref.Slots.Small.Blob != want || ref.Slots.Large.Width != 1) {
				t.Fatal("descriptor does not describe the slots", ref)
			}
		}
	}
	// A bare route on a page is rewritten to its described, cacheable form.
	movie := "/v1/items/" + names["movie"].Public + "/art/poster"
	part := "/v1/items/" + names["part"].Public + "/art/backdrop"
	if err := artworkref.DescribeItemURLs(ctx, db, &movie, &part); err != nil {
		t.Fatal(err)
	}
	if movie != "/v1/items/"+names["movie"].Public+"/art/poster?v="+fmt.Sprintf("%064x", 1)+"&sd=1x1.1."+fmt.Sprintf("%016x", 0)+"&ld=1x1.1."+fmt.Sprintf("%016x", 0) || part != "/v1/items/"+names["part"].Public+"/art/backdrop" {
		t.Fatal("described routes", movie, part)
	}
}

// Re-publishing a source under a new large edge replaces only its large file
// and moves every selection of it to a new revision.
func TestArtworkRepublicationRetiresCachedDescriptors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, target := repairFixture(t)
	ctx := context.Background()
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	raw := editorJPEG(t, 2700, 1080)
	if _, err = s.UploadArtwork(ctx, target, "poster", "", state.Revision, raw, editorActor(), allowAll); err != nil {
		t.Fatal(err)
	}
	var source, small, large string
	var revision int64
	if err = db.QueryRow(`SELECT a.digest,a.revision,o.small_blob,o.large_blob FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.role='poster'`).Scan(&source, &revision, &small, &large); err != nil {
		t.Fatal(err)
	}
	setArtworkPolicy(t, s, 2560)
	m := publishTestArtwork(t, s, raw, time.Now().UTC().Format(time.RFC3339Nano))
	var after int64
	var newSmall, newLarge string
	var edge int
	if err = db.QueryRow(`SELECT a.revision,o.small_blob,o.large_blob,o.large_edge FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.role='poster'`).Scan(&after, &newSmall, &newLarge, &edge); err != nil {
		t.Fatal(err)
	}
	if m.source != source || m.reused || newSmall != small || newLarge == large || edge != 2560 || after != revision+1 {
		t.Fatal("republication", m.reused, newSmall == small, newLarge == large, edge, after, revision)
	}
}

// While a backup holds artwork (backup_blob_holds), the collector deletes no
// source and no file, even unnamed old ones; once the hold ends it does.
func TestArtworkCollectionWaitsForABackupHold(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	old := "2020-01-01T00:00:00Z"
	digest := fmt.Sprintf("%064x", 4242)
	integrationExec(t, db, fixtureArtworkBlobSQL, digest)
	integrationExec(t, db, `UPDATE artwork_blobs SET created_at=? WHERE digest=?`, old, digest)
	if err := os.WriteFile(filepath.Join(s.cacheRoot, digest+".img"), []byte(digest), 0600); err != nil {
		t.Fatal(err)
	}
	integrationExec(t, db, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/jpeg',1,1,?,?,?,1920,0,0,1,?)`, digest, digest, digest, digest, old)
	integrationExec(t, db, `INSERT INTO backup_blob_holds(singleton,backup_id,process_epoch,scratch_token,created_ms,state) VALUES(1,'backup','epoch','scratch',0,'copying')`)
	exists := func(table string) bool {
		var ok bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE digest=?)`, digest).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for pass := 0; pass < 3; pass++ {
		if err := s.CleanupArtwork(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !exists("artwork_sources") || !exists("artwork_blobs") {
		t.Fatal("the collector deleted artwork while a backup held it")
	}
	if _, err := os.Stat(filepath.Join(s.cacheRoot, digest+".img")); err != nil {
		t.Fatal("a held file left the disk", err)
	}
	// The blob pass also refuses a hold taken after its candidate was marked.
	integrationExec(t, db, `DELETE FROM backup_blob_holds`)
	for pass := 0; pass < 6 && (exists("artwork_sources") || exists("artwork_blobs")); pass++ {
		if err := s.CleanupArtwork(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if exists("artwork_sources") || exists("artwork_blobs") {
		t.Fatal("the collector kept unnamed artwork after the hold ended")
	}
}
