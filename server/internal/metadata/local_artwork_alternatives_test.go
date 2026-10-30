package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The scan keeps numbered sidecar images beside the primary one. They reach the
// editor as ordinary provider='local' candidates, seeded after the primary, so
// they can never replace a slot that is already held.
func TestSeedLocalArtworkAddsScannedAlternativesAfterThePrimary(t *testing.T) {
	db, movie := openAlternativesFixture(t, "alt1")
	primary := strings.Repeat("c", 64)
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	mustExec(t, db, `INSERT INTO local_artwork(library_id,kind,entity_id,role,art_key,observed_at) VALUES('alt1','item',?,'poster',?,'now')`, movie.ID, primary)
	seedLegacyAlternatives(t, db, "alt1", movie, "poster", first, second)
	fence := seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, fences := localCandidates(t, db, movie.ID, "poster")
	want := []string{"local:" + first, "local:" + second, "local:" + primary}
	if len(origins) != len(want) {
		t.Fatalf("candidates %v", origins)
	}
	for n, origin := range want {
		if origins[n] != origin {
			t.Fatalf("candidates %v, want %v", origins, want)
		}
		if fences[n] != fence {
			t.Fatalf("%s kept fence %q, want %q", origin, fences[n], fence)
		}
	}
	jobs := localArtworkJobs(t, db, movie.ID, "poster")
	if len(jobs) != 1 || jobs[0] != "local:"+primary {
		t.Fatalf("alternatives changed the queued slot: %v", jobs)
	}
}

// A head with no primary sidecar for a slot still gets its numbered images;
// the first accepted one (the reader's deterministic key order) queues the
// ordinary local fetch.
func TestSeedLocalArtworkQueuesTheFirstScannedAlternativeWhenNothingHoldsTheSlot(t *testing.T) {
	db, movie := openAlternativesFixture(t, "alt2")
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	seedScannedAlternatives(t, db, "alt2", movie, "poster", first, second)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != 2 {
		t.Fatalf("candidates %v", origins)
	}
	jobs := localArtworkJobs(t, db, movie.ID, "poster")
	if len(jobs) != 1 || jobs[0] != "local:"+first {
		t.Fatalf("queued slot %v, want the first accepted alternative", jobs)
	}
}

// An owner's locked choice stays the slot's authority: the alternative is
// listed for a later manual pick but nothing is queued or replaced.
func TestSeedLocalArtworkKeepsALockedOwnerChoice(t *testing.T) {
	db, movie := openAlternativesFixture(t, "alt3")
	digest := strings.Repeat("d", 64)
	mustExec(t, db, fixtureArtworkBlobSQL, digest)
	mustExec(t, db, fixtureArtworkSourceSQL, digest)
	mustExec(t, db, `INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('owner-pick','item',?,'poster','local','owner','local:owner-pick','Local file','older-fence','now')`, movie.ID)
	mustExec(t, db, `INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,locked,actor,observed_at) VALUES('item',?,'poster','owner-pick',?,1,'owner','now')`, movie.ID, digest)
	seedScannedAlternatives(t, db, "alt3", movie, "poster", strings.Repeat("a", 64))
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != 2 {
		t.Fatalf("candidates %v", origins)
	}
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 0 {
		t.Fatalf("an owner choice was queued over: %v", jobs)
	}
	var selected string
	if err := db.QueryRow(`SELECT candidate_id FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster'`, movie.ID).Scan(&selected); err != nil || selected != "owner-pick" {
		t.Fatalf("owner selection changed to %q (%v)", selected, err)
	}
}

// openAlternativesFixture opens a movie library with one live inventory object
// for the movie's asset, so catalogue candidate reads see captured evidence.
func openAlternativesFixture(t *testing.T, library string) (*sql.DB, catalogtest.Item) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), library+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	c.Library(library, "Movies", "movie", "/"+library)
	var source, incarnation string
	if err := db.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id=?`, library).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	movie := c.Movie(c.Handle(library), "/"+library+"/Film.mkv", "Film", 2000)
	mustExec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,?,?,?,'Film.mkv','r1','{}',1,1)`, "object-"+library, source, movie.Token, incarnation)
	return db, movie
}

// seedScannedAlternatives records accepted images with a deterministic source
// basename and rank in argument order, as the current collector does.
func seedScannedAlternatives(t *testing.T, db *sql.DB, library string, movie catalogtest.Item, role string, keys ...string) {
	t.Helper()
	for n, key := range keys {
		mustExec(t, db, `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank) VALUES(?,?,?,'item',?,?,?,?,?)`,
			"object-"+library, "r1", movie.Token, movie.ID, role, key, fmt.Sprintf("poster-%d.jpg", n+1), int64(1024+n+1))
	}
}

// seedLegacyAlternatives records pre-addendum6 rows: accepted choices without a
// named order fact, which the ranked default must not guess.
func seedLegacyAlternatives(t *testing.T, db *sql.DB, library string, movie catalogtest.Item, role string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		mustExec(t, db, `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key) VALUES(?,?,?,'item',?,?,?)`, "object-"+library, "r1", movie.Token, movie.ID, role, key)
	}
}

// seedLocalArtworkCall runs one real discovery call in its own transaction and
// reports whether the target's slots reached a short page.
func seedLocalArtworkCall(t *testing.T, db *sql.DB, kind string, target catalogtest.Item) (bool, string) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	repairTarget := RepairTarget{Kind: kind, ID: target.Public}
	fence, err := artworkFence(ctx, tx, repairTarget)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := seedLocalArtwork(ctx, tx, repairTarget, fence, "now")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return complete, fence
}

// seedLocalArtworkToCompletion runs bounded discovery calls until every slot
// reached a short page, as the worker would across its own invocations.
func seedLocalArtworkToCompletion(t *testing.T, db *sql.DB, kind string, target catalogtest.Item) string {
	t.Helper()
	for call := 0; call < 64; call++ {
		done, fence := seedLocalArtworkCall(t, db, kind, target)
		if done {
			return fence
		}
	}
	t.Fatal("accepted candidates did not converge")
	return ""
}

func localCandidates(t *testing.T, db *sql.DB, entity int64, role string) (origins, fences []string) {
	t.Helper()
	rows, err := db.Query(`SELECT origin,source_fence FROM artwork_candidates WHERE kind='item' AND entity_id=? AND role=? AND provider='local' ORDER BY origin`, entity, role)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var origin, fence string
		if err = rows.Scan(&origin, &fence); err != nil {
			t.Fatal(err)
		}
		origins = append(origins, origin)
		fences = append(fences, fence)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return origins, fences
}

func localArtworkJobs(t *testing.T, db *sql.DB, entity int64, role string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT origin FROM artwork_jobs WHERE kind='item' AND entity_id=? AND role=? ORDER BY created_at,id`, entity, role)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	jobs := []string{}
	for rows.Next() {
		var origin string
		if err = rows.Scan(&origin); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, origin)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return jobs
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// alternativesKeys returns n distinct accepted image keys in the reader's
// deterministic ascending order.
func alternativesKeys(n int) []string {
	keys := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		keys = append(keys, fmt.Sprintf("%064x", i))
	}
	return keys
}

func containsOrigin(origins []string, key string) bool {
	for _, origin := range origins {
		if origin == "local:"+key {
			return true
		}
	}
	return false
}

// Nine accepted images used to stop at the first eight-key page; every one of
// them must reach the editor and exactly the first queued pick stays.
func TestSeedLocalArtworkSeedsEveryAcceptedAlternativeAcrossPages(t *testing.T) {
	db, movie := openAlternativesFixture(t, "alt9")
	keys := alternativesKeys(9)
	seedScannedAlternatives(t, db, "alt9", movie, "poster", keys...)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != len(keys) {
		t.Fatalf("seeded %d of %d accepted images", len(origins), len(keys))
	}
	for _, key := range keys {
		if !containsOrigin(origins, key) {
			t.Fatalf("accepted image %s was skipped", key)
		}
	}
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 1 || jobs[0] != "local:"+keys[0] {
		t.Fatalf("queued slot %v, want the first accepted image", jobs)
	}
}

// The catalogue reader caps one call at 128; 130 accepted images must cross that
// bound through continuation instead of being capped, with at most one page of
// work committed per worker call.
func TestSeedLocalArtworkContinuationCrossesTheReaderBound(t *testing.T) {
	db, movie := openAlternativesFixture(t, "alt130")
	keys := alternativesKeys(130)
	seedScannedAlternatives(t, db, "alt130", movie, "poster", keys...)
	calls, committed := 0, 0
	for call := 0; call < 64; call++ {
		done, _ := seedLocalArtworkCall(t, db, "item", movie)
		calls++
		origins, _ := localCandidates(t, db, movie.ID, "poster")
		if grew := len(origins) - committed; grew > localArtworkCandidatePage || grew < 0 {
			t.Fatalf("call %d committed %d keys, want at most %d", calls, grew, localArtworkCandidatePage)
		} else if calls == 1 && grew != localArtworkCandidatePage {
			t.Fatalf("first call committed %d keys, want one full page", grew)
		}
		committed = len(origins)
		if done {
			break
		}
	}
	if calls < 2 {
		t.Fatal("130 accepted images were treated as one page")
	}
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != len(keys) {
		t.Fatalf("seeded %d of %d accepted images", len(origins), len(keys))
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM artwork_candidates WHERE kind='item' AND entity_id=? AND role='poster' AND provider='local'`, movie.ID).Scan(&rows); err != nil || rows != len(keys) {
		t.Fatalf("candidate rows %d of %d (%v)", rows, len(keys), err)
	}
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 1 || jobs[0] != "local:"+keys[0] {
		t.Fatalf("queued slot %v, want the first accepted image", jobs)
	}
}

// A call that commits only its first page is the required interruption case:
// the next call must resume from that committed progress and reach the same
// final set as an uninterrupted sequence, without duplicate or skipped keys.
func TestSeedLocalArtworkResumesCommittedPageProgress(t *testing.T) {
	cleanDB, cleanMovie := openAlternativesFixture(t, "altclean")
	cleanKeys := alternativesKeys(130)
	seedScannedAlternatives(t, cleanDB, "altclean", cleanMovie, "poster", cleanKeys...)
	seedLocalArtworkToCompletion(t, cleanDB, "item", cleanMovie)

	db, movie := openAlternativesFixture(t, "altrestart")
	keys := alternativesKeys(130)
	seedScannedAlternatives(t, db, "altrestart", movie, "poster", keys...)
	// One bounded call commits a single page and reports the target incomplete;
	// the dirty row would stay for the next invocation.
	if done, _ := seedLocalArtworkCall(t, db, "item", movie); done {
		t.Fatal("a 130-key slot completed in one bounded call")
	}
	committed, _ := localCandidates(t, db, movie.ID, "poster")
	if len(committed) != localArtworkCandidatePage {
		t.Fatalf("the first call committed %d keys, want one page", len(committed))
	}
	seedLocalArtworkToCompletion(t, db, "item", movie)

	restarted, _ := localCandidates(t, db, movie.ID, "poster")
	clean, _ := localCandidates(t, cleanDB, cleanMovie.ID, "poster")
	if len(restarted) != len(keys) || len(clean) != len(keys) {
		t.Fatalf("resumed %d, clean %d of %d", len(restarted), len(clean), len(keys))
	}
	for n := range clean {
		if restarted[n] != clean[n] {
			t.Fatalf("resumed set differs at %d: %q vs %q", n, restarted[n], clean[n])
		}
	}
	if got, want := localArtworkJobs(t, db, movie.ID, "poster"), localArtworkJobs(t, cleanDB, cleanMovie.ID, "poster"); len(got) != 1 || len(want) != 1 || got[0] != want[0] {
		t.Fatalf("resumed jobs %v, clean jobs %v", got, want)
	}
}

// A repeated dirty pass is stable: the same candidate rows and the same queued
// pick, with no duplicates.
func TestSeedLocalArtworkRepeatDirtyIsStable(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altrepeat")
	keys := alternativesKeys(20)
	seedScannedAlternatives(t, db, "altrepeat", movie, "poster", keys...)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	first, _ := localCandidates(t, db, movie.ID, "poster")
	firstJobs := localArtworkJobs(t, db, movie.ID, "poster")
	seedLocalArtworkToCompletion(t, db, "item", movie)
	second, _ := localCandidates(t, db, movie.ID, "poster")
	secondJobs := localArtworkJobs(t, db, movie.ID, "poster")
	if len(first) != len(second) {
		t.Fatalf("repeat dirty changed the set: %d then %d", len(first), len(second))
	}
	for n := range first {
		if first[n] != second[n] {
			t.Fatalf("repeat dirty differs at %d: %q vs %q", n, first[n], second[n])
		}
	}
	if len(firstJobs) != 1 || len(secondJobs) != 1 || firstJobs[0] != secondJobs[0] {
		t.Fatalf("repeat dirty changed the queued pick: %v then %v", firstJobs, secondJobs)
	}
}

// A source revision replaces the accepted evidence; the pass converges on the
// new keys, keeps candidate history, and the ranked default follows the new
// preferred accepted image unless an owner holds the slot.
func TestSeedLocalArtworkFollowsASourceRevision(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altrev")
	keys := alternativesKeys(3)
	seedScannedAlternatives(t, db, "altrev", movie, "poster", keys...)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	jobs := localArtworkJobs(t, db, movie.ID, "poster")
	if len(jobs) != 1 {
		t.Fatalf("initial queued slot %v", jobs)
	}
	fresh := []string{fmt.Sprintf("%064x", 10), fmt.Sprintf("%064x", 11)}
	mustExec(t, db, `UPDATE inventory_objects SET revision='r2' WHERE id='object-altrev'`)
	mustExec(t, db, `DELETE FROM inventory_artwork_candidates WHERE object_id='object-altrev'`)
	for n, key := range fresh {
		mustExec(t, db, `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank) VALUES('object-altrev','r2',?,'item',?,'poster',?,?,?)`, movie.Token, movie.ID, key, fmt.Sprintf("poster-%d.jpg", n+1), int64(1024+n+1))
	}
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	for _, key := range fresh {
		if !containsOrigin(origins, key) {
			t.Fatalf("revised image %s was skipped", key)
		}
	}
	if got := localArtworkJobs(t, db, movie.ID, "poster"); len(got) != 1 || got[0] != "local:"+fresh[0] {
		t.Fatalf("source revision did not queue the new preferred default: %v then %v", jobs, got)
	}
}

type namedCandidate struct {
	name string
	rank int64
	key  string
}

// seedNamedAlternatives records accepted images with their source basename and
// acceptance rank, as the collector does for addendum6.
func seedNamedAlternatives(t *testing.T, db *sql.DB, library string, movie catalogtest.Item, role string, candidates []namedCandidate) {
	t.Helper()
	for _, candidate := range candidates {
		mustExec(t, db, `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank) VALUES(?,?,?,'item',?,?,?,?,?)`,
			"object-"+library, "r1", movie.Token, movie.ID, role, candidate.key, candidate.name, candidate.rank)
	}
}

// The canonical basename is the default even when the numbered image was
// accepted first: the ordered reader, not acceptance order, decides.
func TestSeedLocalArtworkCanonicalNameDefaultSurvivesAcceptanceOrder(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altname")
	canonical, numbered := strings.Repeat("c", 64), strings.Repeat("7", 64)
	seedNamedAlternatives(t, db, "altname", movie, "poster", []namedCandidate{
		{"poster-7.png", 1031, numbered},
		{"poster.jpg", 16, canonical},
	})
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != 2 || !containsOrigin(origins, canonical) || !containsOrigin(origins, numbered) {
		t.Fatalf("materialized %v", origins)
	}
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 1 || jobs[0] != "local:"+canonical {
		t.Fatalf("default %v, want the canonical poster.jpg", jobs)
	}
}

// 130 named images cross the reader's 128-tuple locate page; the composite
// (rank, name, key) cursor keeps every one materialized in bounded calls.
func TestSeedLocalArtworkCompositeCursorAcrossReaderBound(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altbig")
	candidates := make([]namedCandidate, 0, 130)
	for n := 1; n <= 130; n++ {
		candidates = append(candidates, namedCandidate{fmt.Sprintf("poster-%d.jpg", n), int64(1024 + n), fmt.Sprintf("%064x", n)})
	}
	seedNamedAlternatives(t, db, "altbig", movie, "poster", candidates)
	calls := 0
	for call := 0; call < 64; call++ {
		done, _ := seedLocalArtworkCall(t, db, "item", movie)
		calls++
		if done {
			break
		}
	}
	if calls < 2 {
		t.Fatal("130 named images were treated as one page")
	}
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != len(candidates) {
		t.Fatalf("materialized %d of %d named images", len(origins), len(candidates))
	}
	for _, candidate := range candidates {
		if !containsOrigin(origins, candidate.key) {
			t.Fatalf("named image %s was skipped", candidate.name)
		}
	}
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 1 || jobs[0] != "local:"+candidates[0].key {
		t.Fatalf("default %v, want the lowest rank", jobs)
	}
}

// An owner choice counts even when it is unlocked: a selection with an actor, or
// a pending owner job, keeps the slot and is never replaced by a materialized
// alternative.
func TestSeedLocalArtworkNeverOverridesAnOwnerChoice(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altown")
	digest := strings.Repeat("d", 64)
	key := strings.Repeat("e", 64)
	mustExec(t, db, fixtureArtworkBlobSQL, digest)
	mustExec(t, db, fixtureArtworkSourceSQL, digest)
	mustExec(t, db, `INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('owner-unlocked','item',?,'poster','tmdb','remote','https://example.invalid/p.jpg','TMDB','older-fence','now')`, movie.ID)
	mustExec(t, db, `INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,locked,actor,observed_at) VALUES('item',?,'poster','owner-unlocked',?,0,'owner','now')`, movie.ID, digest)
	seedScannedAlternatives(t, db, "altown", movie, "poster", key)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 0 {
		t.Fatalf("an unlocked owner choice queued %v", jobs)
	}
	var selected string
	if err := db.QueryRow(`SELECT candidate_id FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster'`, movie.ID).Scan(&selected); err != nil || selected != "owner-unlocked" {
		t.Fatalf("owner selection changed to %q (%v)", selected, err)
	}
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if !containsOrigin(origins, key) {
		t.Fatalf("accepted image was not materialized: %v", origins)
	}

	// A pending owner job holds the slot just as firmly.
	otherCatalog := catalogtest.New(t, db)
	other := otherCatalog.Movie(otherCatalog.Handle("altown"), "/altown/Other.mkv", "Other", 2001)
	mustExec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT 'object-other',s.id,a.token,s.incarnation,'Other.mkv','r1','{}',1,1 FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN library_sources s ON s.library_id=cl.library_id WHERE a.token=? LIMIT 1`, other.Token)
	mustExec(t, db, `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key) VALUES('object-other','r1',?,'item',?,'poster',?)`, other.Token, other.ID, strings.Repeat("f", 64))
	mustExec(t, db, `INSERT INTO artwork_jobs(id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,status,actor,locked,created_at) VALUES('owner-job','item',?,'poster','','owner-unlocked','https://example.invalid/p.jpg','tmdb','older-fence','pending','owner',1,'now')`, other.ID)
	seedLocalArtworkToCompletion(t, db, "item", other)
	jobs, _ := localArtworkJobIDs(t, db, other.ID, "poster")
	if len(jobs) != 1 || jobs[0] != "owner-job" {
		t.Fatalf("a pending owner job was replaced: %v", jobs)
	}
}

// A newly accepted image that sorts before or in the middle of committed
// progress is still found: locating the first unseeded ordered tuple never
// assumes the previous position is the end of the prefix.
func TestSeedLocalArtworkFindsNewAcceptedImagesBelowAndInsideProgress(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altlate")
	early := make([]namedCandidate, 0, 10)
	for n := 1; n <= 10; n++ {
		early = append(early, namedCandidate{fmt.Sprintf("poster-%d.jpg", n), int64(1024 + n), fmt.Sprintf("%064x", n)})
	}
	seedNamedAlternatives(t, db, "altlate", movie, "poster", early[:8])
	seedLocalArtworkToCompletion(t, db, "item", movie)
	// New acceptance arrives under the same fence: one canonical image before
	// the whole prefix and one numbered image in its middle.
	late := []namedCandidate{
		{"poster.jpg", 16, strings.Repeat("a", 64)},
		{"poster-4b.jpg", 1029, strings.Repeat("b", 64)},
	}
	seedNamedAlternatives(t, db, "altlate", movie, "poster", late)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	for _, candidate := range late {
		if !containsOrigin(origins, candidate.key) {
			t.Fatalf("late accepted image %s was skipped", candidate.name)
		}
	}
	if len(origins) != 10 {
		t.Fatalf("materialized %d of 10 accepted images", len(origins))
	}
}

// The dirty row survives every call that has not completed all slots; the real
// discovery pass clears it only after the last page, and the whole run keeps the
// primary plus page materialization bounded per call.
func TestSeedLocalArtworkKeepsDirtyUntilEverySlotCompletes(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altdirty")
	primary := strings.Repeat("c", 64)
	mustExec(t, db, `INSERT INTO local_artwork(library_id,kind,entity_id,role,art_key,observed_at) VALUES('altdirty','item',?,'poster',?,'now')`, movie.ID, primary)
	candidates := make([]namedCandidate, 0, 20)
	for n := 1; n <= 20; n++ {
		candidates = append(candidates, namedCandidate{fmt.Sprintf("poster-%d.jpg", n), int64(1024 + n), fmt.Sprintf("%064x", n)})
	}
	seedNamedAlternatives(t, db, "altdirty", movie, "poster", candidates)
	mustExec(t, db, `INSERT OR IGNORE INTO artwork_dirty(kind,entity_id) VALUES('item',?)`, movie.ID)
	s := New(db, "test-token")
	dirtyRows := func() int {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM artwork_dirty WHERE kind='item' AND entity_id=?`, movie.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if err := s.seedArtwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dirtyRows() != 1 {
		t.Fatal("the dirty row was cleared before every slot completed")
	}
	// Every pass keeps its candidate writes, primary upserts included, inside
	// the one eight-write budget.
	dbwork.ProfileStatements(true)
	defer dbwork.ProfileStatements(false)
	inserts := func() int64 {
		var count int64
		for _, statement := range dbwork.StatementProfile() {
			if strings.HasPrefix(statement.Shape, "INSERT INTO artwork_candidates") {
				count += statement.Count
			}
		}
		return count
	}
	first := true
	for call := 0; call < 64; call++ {
		dbwork.ProfileStatements(true)
		if err := s.seedArtwork(context.Background()); err != nil {
			t.Fatal(err)
		}
		if wrote := inserts(); wrote > int64(localArtworkCandidatePage) {
			t.Fatalf("call %d wrote %d candidates including the primary", call+1, wrote)
		} else if first && wrote != int64(localArtworkCandidatePage) {
			t.Fatalf("first call wrote %d candidates, want the combined budget", wrote)
		}
		first = false
		if dirtyRows() == 0 {
			break
		}
	}
	if dirtyRows() != 0 {
		t.Fatal("the dirty row survived a completed continuation")
	}
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != len(candidates)+1 {
		t.Fatalf("materialized %d of %d with the primary", len(origins), len(candidates)+1)
	}
}

func localArtworkJobIDs(t *testing.T, db *sql.DB, entity int64, role string) ([]string, error) {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM artwork_jobs WHERE kind='item' AND entity_id=? AND role=? ORDER BY created_at,id`, entity, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// An accepted image already retained under an older source fence is refreshed
// under the current one by the ordinary bounded page, without a duplicate row.
func TestSeedLocalArtworkRefreshesAKeyRetainedUnderAnOldFence(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altfence")
	key := strings.Repeat("a", 64)
	seedScannedAlternatives(t, db, "altfence", movie, "poster", key)
	fence := seedLocalArtworkToCompletion(t, db, "item", movie)
	mustExec(t, db, `UPDATE artwork_candidates SET source_fence='stale-fence' WHERE kind='item' AND entity_id=? AND role='poster' AND origin=?`, movie.ID, "local:"+key)
	if refreshed := seedLocalArtworkToCompletion(t, db, "item", movie); refreshed != fence {
		t.Fatalf("fence changed across the refresh: %q then %q", fence, refreshed)
	}
	var stored string
	if err := db.QueryRow(`SELECT source_fence FROM artwork_candidates WHERE kind='item' AND entity_id=? AND role='poster' AND origin=?`, movie.ID, "local:"+key).Scan(&stored); err != nil || stored != fence {
		t.Fatalf("retained key kept fence %q, want %q (%v)", stored, fence, err)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM artwork_candidates WHERE kind='item' AND entity_id=? AND role='poster' AND origin=?`, movie.ID, "local:"+key).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("refresh duplicated the candidate: %d (%v)", rows, err)
	}
}

// A committed prefix longer than the reader's full page must not hide a missing
// tail: the domain reader excludes committed tuples, so the missing page holds
// exactly the tail.
func TestSeedLocalArtworkMaterializesAMissingTailPastACommittedPrefix(t *testing.T) {
	db, movie := openAlternativesFixture(t, "alttail")
	first := namedCandidate{"poster-first.jpg", 1, fmt.Sprintf("%064x", 9999)}
	seedNamedAlternatives(t, db, "alttail", movie, "poster", []namedCandidate{first})
	fence := seedLocalArtworkToCompletion(t, db, "item", movie)
	for n := 1; n <= 1105; n++ {
		key := fmt.Sprintf("%064x", n)
		mustExec(t, db, `INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,'item',?,'poster','','local',?,?,'Local artwork',?,'now')`,
			fmt.Sprintf("prefix-%d", n), movie.ID, key, "local:"+key, fence)
	}
	tail := make([]namedCandidate, 0, 10)
	for n := 1300; n < 1310; n++ {
		tail = append(tail, namedCandidate{fmt.Sprintf("poster-%d.jpg", n), int64(1024 + n), fmt.Sprintf("%064x", n)})
	}
	seedNamedAlternatives(t, db, "alttail", movie, "poster", tail)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	origins, _ := localCandidates(t, db, movie.ID, "poster")
	if len(origins) != 1105+1+len(tail) {
		t.Fatalf("materialized %d rows, want the committed prefix plus the missing tail", len(origins))
	}
	for _, candidate := range tail {
		if !containsOrigin(origins, candidate.key) {
			t.Fatalf("missing tail image %s was not materialized", candidate.name)
		}
	}
}

// A rank/name-only change among already-materialized keys moves the preferred
// default even though no candidate is missing, because the helper runs for
// every slot on every pass.
func TestSeedLocalArtworkRequeuesWhenOnlyTheRankOrderChanges(t *testing.T) {
	db, movie := openAlternativesFixture(t, "altrank")
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	seedNamedAlternatives(t, db, "altrank", movie, "poster", []namedCandidate{
		{"poster-a.jpg", 1025, a},
		{"poster-b.jpg", 1026, b},
	})
	seedLocalArtworkToCompletion(t, db, "item", movie)
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 1 || jobs[0] != "local:"+a {
		t.Fatalf("initial default %v, want poster-a", jobs)
	}
	// Rank-only change: b becomes preferred and nothing is missing.
	mustExec(t, db, `UPDATE inventory_artwork_candidates SET source_rank=1024 WHERE art_key=?`, b)
	mustExec(t, db, `UPDATE inventory_artwork_candidates SET source_rank=1027 WHERE art_key=?`, a)
	seedLocalArtworkToCompletion(t, db, "item", movie)
	if jobs := localArtworkJobs(t, db, movie.ID, "poster"); len(jobs) != 1 || jobs[0] != "local:"+b {
		t.Fatalf("rank change did not move the default: %v", jobs)
	}
}
