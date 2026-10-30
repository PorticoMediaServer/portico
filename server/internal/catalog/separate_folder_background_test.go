package catalog

// P2-59 / O-022 SeparateFolder accepted-actor background continuation: source
// tests.
//
// These tests drive the real service seam, the real catalog_entity_mutations
// rows, the real console_operations admission and the real MutationAdapter
// dispatch. A typed local-actor descriptor is captured from the actual
// direct_memberships/direct_profiles rows; the tests never write the owner
// descriptor, the file cursor, the retirement cursor or a console progress row
// by hand. The two exceptions are explicitly the existing fixtures from
// separate_folder_retirement_test.go (the literal prepared-media cancel and
// downloads receipt statements) and the worker finishing a prepared job, which
// is exactly what the production worker does after cancellation.
//
// The operations scheduler is not run here. The adapter step returns the
// JobObservation the scheduler persists; the durable console_operations row and
// its operation.updated activity event are asserted where the actual admission
// writes them, and this file must never fabricate the scheduler's update.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

type separateAsyncFixture struct {
	c         *catalogtest.Catalog
	account   string
	profile   string
	principal identity.Principal
	source    string
	artist    int64
	album     int64
	songs     []int64
	objects   []string
	tokens    []string
}

// newSeparateAsyncFixture builds the real owner account/profile before any
// catalogue row, then the reported shape: every track has one file outside the
// selected folder (flac/) and one file inside it (mp3/). No owner descriptor or
// command row is created here.
func newSeparateAsyncFixture(t *testing.T, files int) *separateAsyncFixture {
	t.Helper()
	c := catalogtest.Open(t)
	f := &separateAsyncFixture{c: c, account: "owner", profile: "owner-profile"}
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,X'00',?)`, f.account, f.account, f.profile)
	f.principal = identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: f.account, ProfileID: f.profile, Role: identity.TierOwner}}
	music := c.Library("music", "Music", "music", "/music")
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='music'`).Scan(&f.source); err != nil {
		t.Fatal(err)
	}
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Release", 2020)
	f.artist = artist.ID
	f.album = album.ID
	for n := 1; n <= files; n++ {
		song := c.Song(album, n, fmt.Sprintf("/music/flac/%02d.flac", n), fmt.Sprintf("Song %d", n))
		inventoryFile(t, c, f.source, song, fmt.Sprintf("flac/%02d.flac", n), "flac")
		_, token := c.File(song.ID, fmt.Sprintf("/music/mp3/%02d.mp3", n), 5400)
		object := inventoryFile(t, c, f.source, catalogtest.Item{Token: token}, fmt.Sprintf("mp3/%02d.mp3", n), "mp3")
		f.songs = append(f.songs, song.ID)
		f.objects = append(f.objects, object)
		f.tokens = append(f.tokens, token)
	}
	c.Drain()
	return f
}

// separateAsyncSibling creates one unrelated album/song outside the separating
// folder so "sibling rows" name a real item this command must never touch.
func separateAsyncSibling(t *testing.T, f *separateAsyncFixture) (int64, string) {
	t.Helper()
	album := f.c.Album(catalogtest.Item{ID: f.artist}, "Other", 2021)
	song := f.c.Song(album, 1, "/music/other/01.flac", "Other")
	_, token := f.c.File(song.ID, "/music/other/01.mp3", 100)
	f.c.Drain()
	return song.ID, token
}

func separateAsyncText(t *testing.T, c *catalogtest.Catalog, query string, args ...any) string {
	t.Helper()
	var out string
	if err := c.DB.QueryRow(query, args...).Scan(&out); err != nil {
		t.Fatalf("%v: %s", err, query)
	}
	return out
}

func separateAsyncInt(t *testing.T, c *catalogtest.Catalog, query string, args ...any) int64 {
	t.Helper()
	var out int64
	if err := c.DB.QueryRow(query, args...).Scan(&out); err != nil {
		t.Fatalf("%v: %s", err, query)
	}
	return out
}

func separateAsyncRoot(t *testing.T, c *catalogtest.Catalog) string {
	t.Helper()
	return separateAsyncText(t, c, `SELECT mutation_id FROM catalog_entity_mutations
 WHERE owner_kind='separate' AND parent_mutation_id IS NULL ORDER BY owner_generation DESC,mutation_id LIMIT 1`)
}

func separateAsyncChild(t *testing.T, c *catalogtest.Catalog, root string) string {
	t.Helper()
	return separateAsyncText(t, c, `SELECT COALESCE(current_child,'') FROM catalog_entity_mutations WHERE mutation_id=?`, root)
}

func separateAsyncCursor(t *testing.T, c *catalogtest.Catalog, child string) string {
	t.Helper()
	var cursor string
	err := c.DB.QueryRow(`SELECT cursor FROM catalog_purge_jobs WHERE mutation_id=?`, child).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

// separateAsyncAdapterStep is one actual scheduler dispatch with a fresh
// process context: a new Service and a new adapter, only the durable rows are
// shared.
func separateAsyncAdapterStep(t *testing.T, db *sql.DB, retirement SeparateFolderHooks, id string) operations.JobObservation {
	t.Helper()
	s := New(db)
	s.SeparateFolderRetirement = retirement
	out, err := s.MutationAdapter().Step(context.Background(), id)
	if err != nil {
		t.Fatalf("operations adapter step %s: %v", id, err)
	}
	return out
}

// separateAsyncEffect is the catalogue/claim state a revoked or quiescent turn
// must not change. Command rows are deliberately excluded: abandoning private
// custody is allowed to advance its own child/root state.
type separateAsyncEffect struct {
	albums, originalFolder, separatedFolder int
	links, assets                           int
	liveGrants, livePreparations            int
}

func separateAsyncTarget(t *testing.T, f *separateAsyncFixture) int64 {
	t.Helper()
	var target int64
	if err := f.c.DB.QueryRow(`SELECT COALESCE(target_id,target_subject_id,0) FROM catalog_entity_mutations WHERE mutation_id=?`, separateAsyncRoot(t, f.c)).Scan(&target); err != nil {
		t.Fatal(err)
	}
	return target
}

func separateAsyncEffects(t *testing.T, f *separateAsyncFixture) separateAsyncEffect {
	t.Helper()
	target := separateAsyncTarget(t, f)
	return separateAsyncEffect{
		albums:           rows(t, f.c, `SELECT count(*) FROM catalog_albums`),
		originalFolder:   rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, f.album),
		separatedFolder:  rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, target),
		links:            rows(t, f.c, `SELECT count(*) FROM catalog_asset_links`),
		assets:           rows(t, f.c, `SELECT count(*) FROM catalog_assets`),
		liveGrants:       rows(t, f.c, `SELECT count(*) FROM download_grants WHERE revoked=0`),
		livePreparations: rows(t, f.c, `SELECT count(*) FROM download_preparations WHERE state IN('queued','running','ready','paused','failed')`),
	}
}

func separateAsyncRetirement(t *testing.T, f *separateAsyncFixture) (*separateFolderRetirementFixture, SeparateFolderHooks) {
	t.Helper()
	h := &separateFolderRetirementFixture{c: f.c, now: 1_700_000_000_000}
	rh := h.hooks()
	return h, SeparateFolderHooks{CancelJob: rh.CancelJob, Interrupt: rh.Interrupt, RevokeReceipt: rh.RevokeReceipt}
}

func separateAsyncReopen(t *testing.T, c *catalogtest.Catalog) *catalogtest.Catalog {
	t.Helper()
	var sequence int
	var name, path string
	if err := c.DB.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return catalogtest.New(t, db)
}

func separateAsyncOwner(t *testing.T, c *catalogtest.Catalog, root string) string {
	t.Helper()
	return separateAsyncText(t, c, `SELECT owner_id FROM catalog_entity_mutations WHERE mutation_id=?`, root)
}

func separateAsyncLocalActor(t *testing.T, f *separateAsyncFixture, owner string) {
	t.Helper()
	a, typed, err := separateFolderDecodeActor(owner)
	if err != nil || !typed {
		t.Fatalf("accepted owner is not the typed local actor: typed=%v err=%v", typed, err)
	}
	if a.Account != f.account || a.Profile != f.profile || a.Tier != identity.TierOwner {
		t.Fatalf("accepted owner is not this exact local actor: %+v", a)
	}
	// The descriptor must carry the current durable rows, not values this test
	// supplied. A removed lifetime/membership capture fails here.
	var membership, lifetime string
	if err := f.c.DB.QueryRow(`SELECT m.incarnation,p.lifetime FROM direct_profiles p
 JOIN direct_memberships m ON m.account_id=p.account_id WHERE p.account_id=? AND p.id=?`, f.account, f.profile).Scan(&membership, &lifetime); err != nil {
		t.Fatal(err)
	}
	if a.Membership != membership || a.Lifetime != lifetime {
		t.Fatal("owner descriptor does not carry the live membership incarnation and profile lifetime")
	}
}

// TestSeparateFolderBackgroundAdapterFinishesAfterRequestEnds proves the large
// claim: the request is honest pending backpressure with no id, its context
// ending stops it, and the actual operations adapter resumes the same typed
// owner to one public id using the durable console admission.
func TestSeparateFolderBackgroundAdapterFinishesAfterRequestEnds(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.35s")
	}
	f := newSeparateAsyncFixture(t, 40)
	ctx, cancel := context.WithCancel(WithSeparateFolderActor(context.Background(), f.principal))
	calls := 0
	authorize := func(*sql.Tx) error { calls++; return nil }
	separated, err := New(f.c.DB).SeparateFolder(ctx, "music", f.objects[0], "r1", authorize)
	if !errors.Is(err, ErrSeparateFolderPending) {
		t.Fatalf("a 40-file folder did not return bounded pending: %v", err)
	}
	if separated != "" {
		t.Fatalf("pending request reported a completed public id %q", separated)
	}
	if calls == 0 {
		t.Fatal("the accepted owner callback did not run in the foreground")
	}
	root := separateAsyncRoot(t, f.c)
	owner := separateAsyncOwner(t, f.c, root)
	separateAsyncLocalActor(t, f, owner)

	if want := operations.Hash(identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: f.account, ProfileID: f.profile})); separateAsyncText(t, f.c, `SELECT actor FROM console_operations WHERE id=?`, "cm-"+root) != want {
		t.Fatal("console actor is not the local accepted actor")
	}
	var state, phase, resource string
	if err := f.c.DB.QueryRow(`SELECT state,phase,resource FROM console_operations WHERE id=?`, "cm-"+root).Scan(&state, &phase, &resource); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || phase != "catalog mutation" || resource != root {
		t.Fatalf("durable console admission state=%q phase=%q resource=%q", state, phase, resource)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM api_events WHERE audience='admin' AND type='operation.updated' AND resource_kind='operation' AND resource_id=?`, "cm-"+root); n != 1 {
		t.Fatalf("console Activity event was not published by the actual admission: %d", n)
	}

	// The HTTP request context now ends. It must not advance or publish anything.
	before := separateAsyncEffects(t, f)
	cancel()
	again, err := New(f.c.DB).SeparateFolder(ctx, "music", f.objects[0], "r1", authorize)
	if again != "" || err == nil {
		t.Fatalf("cancelled request reported id=%q err=%v", again, err)
	}
	if after := separateAsyncEffects(t, f); after != before {
		t.Fatalf("cancelled request changed catalogue or claim rows: %+v -> %+v", before, after)
	}

	dbwork.ResetPlans()
	progressed, done := false, false
	finalPhase := ""
	for turn := 0; turn < 220; turn++ {
		out := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
		switch out.State {
		case "running":
			if out.Phase == "separating folder" {
				progressed = true
			}
		case "succeeded":
			done = true
			finalPhase = out.Phase
		default:
			t.Fatalf("adapter turn %d state=%q phase=%q code=%q", turn, out.State, out.Phase, out.ErrorCode)
		}
		if done {
			break
		}
	}
	if !done || !progressed {
		t.Fatalf("adapter did not finish the accepted command: progressed=%v done=%v", progressed, done)
	}
	if finalPhase != "folder separated" {
		t.Fatalf("adapter completed with phase %q", finalPhase)
	}
	if got := separateAsyncOwner(t, f.c, root); got != owner {
		t.Fatalf("adapter rebound the owner descriptor %q -> %q", owner, got)
	}
	target := separateAsyncText(t, f.c, `SELECT target_public_id FROM catalog_entity_mutations WHERE mutation_id=?`, root)
	if target == "" {
		t.Fatal("finished command published no separated public id")
	}
	retried, err := New(f.c.DB).SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil || retried != target {
		t.Fatalf("retry returned %q, %v; finalize published %q", retried, err, target)
	}
	if got := separateAsyncEffects(t, f).separatedFolder; got != 40 {
		t.Fatalf("separated folder holds %d files", got)
	}
	// The same shared writer bounds must remain in force.
	if dbwork.DefaultPlan(dbwork.ClassInteractive).Rows != 160 || phasedpurge.EffectLimit != 24 || phasedpurge.ReadLimit != 128 {
		t.Fatal("shared interactive 160 / retirement 24/128 bounds changed")
	}
	stats := dbwork.Plans()
	if stats.Measured == 0 || stats.Largest.Rows > dbwork.DefaultPlan(dbwork.ClassInteractive).Rows || stats.Overruns != 0 {
		t.Fatalf("background writers exceeded the interactive plan: %+v", stats)
	}
}

// TestSeparateFolderBackgroundReopenResumesSameActor closes the database after a
// committed quantum and resumes from the durable owner descriptor. The live
// lifetime/membership still authorize; no file or claim is duplicated.
func TestSeparateFolderBackgroundReopenResumesSameActor(t *testing.T) {
	f := newSeparateAsyncFixture(t, 20)
	_, retirement := separateAsyncRetirement(t, f)
	s := New(f.c.DB)
	s.SeparateFolderRetirement = retirement
	separated, err := s.SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if !errors.Is(err, ErrSeparateFolderPending) || separated != "" {
		t.Fatalf("large folder did not return pending/no id: id=%q err=%v", separated, err)
	}
	root := separateAsyncRoot(t, f.c)
	owner := separateAsyncOwner(t, f.c, root)
	// The last folder file is still ahead of the cursor, so its real claim is
	// retired only after the reopen.
	claimed := f.songs[len(f.songs)-1]
	f.c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,source_asset_id,created_ms,updated_ms)
 VALUES('prep-reopen','p','owner','account','p',?,'music','original','item','queued',?,1,1)`, claimed, f.tokens[len(f.tokens)-1])
	f.c.Exec(`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms)
 VALUES('grant-reopen','prep-reopen','p',?,1,9999999999999)`, claimed)

	f.c = separateAsyncReopen(t, f.c)
	if got := separateAsyncOwner(t, f.c, root); got != owner {
		t.Fatalf("reopen changed the owner descriptor %q -> %q", owner, got)
	}
	separateAsyncLocalActor(t, f, owner)
	beforeLinks := separateAsyncInt(t, f.c, `SELECT count(*) FROM catalog_asset_links`)
	done := false
	for turn := 0; turn < 220; turn++ {
		out := separateAsyncAdapterStep(t, f.c.DB, retirement, root)
		if out.State == "failed" {
			t.Fatalf("reopened adapter turn %d failed: %s", turn, out.ErrorCode)
		}
		if out.State == "succeeded" {
			done = true
			break
		}
	}
	if !done {
		t.Fatal("reopened adapter did not finish the same command")
	}
	effect := separateAsyncEffects(t, f)
	if effect.albums != 2 || effect.originalFolder != 0 || effect.separatedFolder != 20 {
		t.Fatalf("reopened split shape: %+v", effect)
	}
	if afterLinks := separateAsyncInt(t, f.c, `SELECT count(*) FROM catalog_asset_links`); afterLinks != beforeLinks {
		t.Fatalf("reopen duplicated/copied file links: %d -> %d", beforeLinks, afterLinks)
	}
	for _, token := range f.tokens {
		if n := rows(t, f.c, `SELECT count(*) FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, token); n != 1 {
			t.Fatalf("asset %s has %d links after reopen", token, n)
		}
	}
	if n := rows(t, f.c, `SELECT count(*) FROM download_grants WHERE hash='grant-reopen' AND item_id=? AND revoked=1`, claimed); n != 1 {
		t.Fatalf("claim was not retired once in place: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM download_preparations WHERE id='prep-reopen' AND item_id=? AND state='unavailable' AND reason='source_changed'`, claimed); n != 1 {
		t.Fatalf("preparation was not retired once in place: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM download_grants`); n != 1 {
		t.Fatalf("claim rows were duplicated: %d", n)
	}
}

// TestSeparateFolderBackgroundRevocationAbandonsWithoutEffect rotates the real
// membership role or the real lifetime/incarnation between committed turns. The
// next adapter turn may release only this command's private custody: no
// catalogue file or claim changes, the root becomes blocked/failed, and the
// revoked command never rebinds or publishes an id.
func TestSeparateFolderBackgroundRevocationAbandonsWithoutEffect(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 4.24s")
	}
	cases := []struct {
		name   string
		rotate func(t *testing.T, f *separateAsyncFixture)
	}{
		{"administrator role revoked", func(t *testing.T, f *separateAsyncFixture) {
			f.c.Exec(`UPDATE direct_memberships SET role='member',revision=revision+1 WHERE account_id=?`, f.account)
		}},
		{"profile lifetime rotated", func(t *testing.T, f *separateAsyncFixture) {
			f.c.Exec(`UPDATE direct_profiles SET lifetime=lower(hex(randomblob(16))) WHERE account_id=? AND id=?`, f.account, f.profile)
		}},
		{"membership incarnation rotated", func(t *testing.T, f *separateAsyncFixture) {
			f.c.Exec(`UPDATE direct_memberships SET incarnation=lower(hex(randomblob(16))),revision=revision+1 WHERE account_id=?`, f.account)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSeparateAsyncFixture(t, 20)
			_, retirement := separateAsyncRetirement(t, f)
			s := New(f.c.DB)
			s.SeparateFolderRetirement = retirement
			separated, err := s.SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if !errors.Is(err, ErrSeparateFolderPending) || separated != "" {
				t.Fatalf("large folder did not return pending/no id: id=%q err=%v", separated, err)
			}
			root := separateAsyncRoot(t, f.c)
			owner := separateAsyncOwner(t, f.c, root)
			target := separateAsyncText(t, f.c, `SELECT target_public_id FROM catalog_entity_mutations WHERE mutation_id=?`, root)
			for turn := 0; turn < 4 && separateAsyncChild(t, f.c, root) == ""; turn++ {
				if out := separateAsyncAdapterStep(t, f.c.DB, retirement, root); out.State != "running" {
					t.Fatalf("pre-revocation turn %d state=%q", turn, out.State)
				}
			}
			if separateAsyncChild(t, f.c, root) == "" {
				t.Fatal("no actual child quantum committed before revocation")
			}
			before := separateAsyncEffects(t, f)
			c.rotate(t, f)
			out := separateAsyncAdapterStep(t, f.c.DB, retirement, root)
			if out.State != "running" || out.ErrorCode != "authority_revoked" || out.Phase != "authority revoked; releasing custody" {
				t.Fatalf("revoked turn did not start bounded abandonment: %+v", out)
			}
			if after := separateAsyncEffects(t, f); after != before {
				t.Fatalf("revoked turn changed catalogue or claim rows: %+v -> %+v", before, after)
			}
			out = separateAsyncAdapterStep(t, f.c.DB, retirement, root)
			if out.State != "failed" || out.ErrorCode != "authority_revoked" {
				t.Fatalf("second bounded turn did not block the root: %+v", out)
			}
			if after := separateAsyncEffects(t, f); after != before {
				t.Fatalf("root abandonment changed catalogue or claim rows: %+v -> %+v", before, after)
			}
			if got := separateAsyncText(t, f.c, `SELECT status||'/'||outcome FROM catalog_entity_mutations WHERE mutation_id=?`, root); got != "terminal/blocked" {
				t.Fatalf("root state=%q", got)
			}
			if got := separateAsyncOwner(t, f.c, root); got != owner {
				t.Fatal("revoked command rebound its owner descriptor")
			}
			if got := separateAsyncText(t, f.c, `SELECT target_public_id FROM catalog_entity_mutations WHERE mutation_id=?`, root); got != target {
				t.Fatal("revoked command rebound its target")
			}
			// Retrying the accepted command retains its exact accepting scope.
			// A no-op management callback would instead model a fresh owner
			// admission under the newly rotated lifetime, not this old command.
			accepted, typed, decodeErr := separateFolderDecodeActor(owner)
			if decodeErr != nil || !typed {
				t.Fatal("accepted actor", decodeErr)
			}
			retry, err := New(f.c.DB).SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(tx *sql.Tx) error {
				_, err := accepted.authorizeTx(context.Background(), tx, "music")
				return err
			})
			if retry != "" || !errors.Is(err, identity.ErrUnauthorized) {
				t.Fatalf("revoked owner was not refused: id=%q err=%v", retry, err)
			}
			if n := rows(t, f.c, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='separate' AND parent_mutation_id IS NULL`); n != 1 {
				t.Fatalf("revocation created a new root command: %d", n)
			}
		})
	}
}

// TestSeparateFolderBackgroundCancellingJobHoldsThenResumes seeds a real
// running prepared job on the file the command next reaches. The real cancel
// hook moves it to cancelling; the engine yields without advancing the durable
// cursor and without moving the file; once the worker's cleanup finishes, the
// same walk resumes. Sibling rows and the playback pin survive untouched.
func TestSeparateFolderBackgroundCancellingJobHoldsThenResumes(t *testing.T) {
	f := newSeparateAsyncFixture(t, 20)
	claimed := len(f.songs) - 2
	siblingID, siblingToken := separateAsyncSibling(t, f)
	f.c.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES
 ('job-claimed',?,?,'music','profile','target','{}','revision','{}','running','prepare',1,2),
 ('job-sibling',?,?,'music','profile','target','{}','revision','{}','queued','waiting',1,2)`,
		f.songs[claimed], f.tokens[claimed], siblingID, siblingToken)
	f.c.Exec(`INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES
 ('version-claimed',?,?,'music','profile','target','revision','{}',0,'evidence','transform','digest',100,'{}','published',1,'job-claimed'),
 ('version-sibling',?,?,'music','profile','target','revision','{}',0,'evidence','transform','digest',100,'{}','published',1,'job-sibling')`,
		f.songs[claimed], f.tokens[claimed], siblingID, siblingToken)
	f.c.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration)
 VALUES('session-pin','hash','account','profile',?,?,1,'playing','session-grant','token','2099-01-01T00:00:00Z','request',100)`, f.songs[claimed], f.tokens[claimed])
	f.c.Exec(`INSERT INTO prepared_media_session_pins(session_id,version_id,digest,size) VALUES('session-pin','version-claimed','digest',100)`)

	h, retirement := separateAsyncRetirement(t, f)
	s := New(f.c.DB)
	s.SeparateFolderRetirement = retirement
	separated, err := s.SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if !errors.Is(err, ErrSeparateFolderPending) || separated != "" {
		t.Fatalf("large folder did not return pending/no id: id=%q err=%v", separated, err)
	}
	root := separateAsyncRoot(t, f.c)

	var held, cursorBefore string
	waiting := false
	for turn := 0; turn < 120; turn++ {
		child := separateAsyncChild(t, f.c, root)
		at := separateAsyncCursor(t, f.c, child)
		out := separateAsyncAdapterStep(t, f.c.DB, retirement, root)
		if out.Phase == "waiting for prepared cancellation" {
			held, cursorBefore, waiting = child, at, true
			break
		}
		if out.State != "running" {
			t.Fatalf("turn %d state=%q phase=%q code=%q", turn, out.State, out.Phase, out.ErrorCode)
		}
	}
	if !waiting {
		t.Fatal("the prepared job never reached cancellation quiescence")
	}
	if got := separateAsyncText(t, f.c, `SELECT state FROM prepared_media_jobs WHERE id='job-claimed'`); got != "cancelling" {
		t.Fatalf("real cancel hook did not leave the job cancelling: %q", got)
	}
	if len(h.cancelled) != 1 || h.cancelled[0] != "job-claimed" {
		t.Fatalf("real cancel hook calls: %v", h.cancelled)
	}
	before := separateAsyncEffects(t, f)
	out := separateAsyncAdapterStep(t, f.c.DB, retirement, root)
	if out.Phase != "waiting for prepared cancellation" {
		t.Fatalf("quiescence turn advanced: %+v", out)
	}
	if after := separateAsyncCursor(t, f.c, held); after != cursorBefore {
		t.Fatalf("quiescence advanced the durable cursor %q -> %q", cursorBefore, after)
	}
	if after := separateAsyncEffects(t, f); after != before {
		t.Fatalf("quiescence moved a file or claim: %+v -> %+v", before, after)
	}

	// This is the existing worker's own generation cleanup, not command progress.
	f.c.Exec(`UPDATE prepared_media_jobs SET state='cancelled' WHERE id='job-claimed'`)
	done := false
	for turn := 0; turn < 160; turn++ {
		out = separateAsyncAdapterStep(t, f.c.DB, retirement, root)
		if out.State == "failed" {
			t.Fatalf("resume turn %d failed: %s", turn, out.ErrorCode)
		}
		if out.State == "succeeded" {
			done = true
			break
		}
	}
	if !done {
		t.Fatal("quiescence cleanup did not resume the same command")
	}
	if len(h.interrupt) != 1 || h.interrupt[0] != "job-claimed" {
		t.Fatalf("worker interrupt was not exactly once: %v", h.interrupt)
	}
	if got := separateAsyncText(t, f.c, `SELECT state FROM prepared_media_jobs WHERE id='job-claimed'`); got != "cancelled" {
		t.Fatalf("claimed job=%q", got)
	}
	if got := separateAsyncText(t, f.c, `SELECT state FROM prepared_media_versions WHERE id='version-claimed'`); got != "deleting" {
		t.Fatalf("claimed version=%q", got)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM prepared_media_session_pins WHERE session_id='session-pin' AND version_id='version-claimed'`); n != 1 {
		t.Fatalf("session pin was mutated: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM playback_sessions WHERE id='session-pin'`); n != 1 {
		t.Fatalf("playback session was mutated: %d", n)
	}
	if got := separateAsyncText(t, f.c, `SELECT state FROM prepared_media_jobs WHERE id='job-sibling'`); got != "queued" {
		t.Fatalf("sibling job=%q", got)
	}
	if got := separateAsyncText(t, f.c, `SELECT state FROM prepared_media_versions WHERE id='version-sibling'`); got != "published" {
		t.Fatalf("sibling version=%q", got)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM prepared_media_jobs WHERE id='job-claimed' AND item_id=?`, f.songs[claimed]); n != 1 {
		t.Fatalf("claimed job was re-pointed: %d", n)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM prepared_media_versions WHERE id='version-claimed' AND item_id=?`, f.songs[claimed]); n != 1 {
		t.Fatalf("claimed version was re-pointed: %d", n)
	}
	effect := separateAsyncEffects(t, f)
	if effect.originalFolder != 0 || effect.separatedFolder != 20 {
		t.Fatalf("final split shape: %+v", effect)
	}
}

// TestSeparateFolderBackgroundPorticoAdmittedLocalMembership proves a membership
// created by Portico Account admission administers the same local rows: the
// command completes with the local accepted actor and no Hosted binding, while
// a genuinely non-local descriptor is refused instead of being inferred local.
func TestSeparateFolderBackgroundPorticoAdmittedLocalMembership(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.89s")
	}
	f := newSeparateAsyncFixture(t, 20)
	f.c.Exec(`UPDATE direct_memberships SET joined_via='portico',joined_ms=1 WHERE account_id=?`, f.account)
	hosted := func(c *catalogtest.Catalog) int64 {
		return separateAsyncInt(t, c, `SELECT (SELECT count(*) FROM portico_admitted_families)+(SELECT count(*) FROM hosted_migrated_families)+(SELECT count(*) FROM authorization_session_families)`)
	}
	if before := hosted(f.c); before != 0 {
		t.Fatalf("fixture already had Hosted binding rows: %d", before)
	}
	s := New(f.c.DB)
	separated, err := s.SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if !errors.Is(err, ErrSeparateFolderPending) || separated != "" {
		t.Fatalf("large folder did not return pending/no id: id=%q err=%v", separated, err)
	}
	root := separateAsyncRoot(t, f.c)
	separateAsyncLocalActor(t, f, separateAsyncOwner(t, f.c, root))
	done := false
	for turn := 0; turn < 220; turn++ {
		out := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
		if out.State == "failed" {
			t.Fatalf("Portico-admitted local turn %d failed: %s", turn, out.ErrorCode)
		}
		if out.State == "succeeded" {
			done = true
			break
		}
	}
	if !done {
		t.Fatal("Portico-admitted local membership did not finish the same flow")
	}
	if after := hosted(f.c); after != 0 {
		t.Fatalf("command created Hosted source binding rows: %d", after)
	}
	if got := separateAsyncText(t, f.c, `SELECT joined_via FROM direct_memberships WHERE account_id=?`, f.account); got != "portico" {
		t.Fatalf("the real membership marker changed: %q", got)
	}
	if want := operations.Hash(identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: f.account, ProfileID: f.profile})); separateAsyncText(t, f.c, `SELECT actor FROM console_operations WHERE id=?`, "cm-"+root) != want {
		t.Fatal("Portico-admitted membership did not use the local accepted actor")
	}

	g := newSeparateAsyncFixture(t, 3)
	nonlocalPrincipal := identity.Principal{Viewer: identity.Viewer{Authority: "hosted", AccountID: g.account, ProfileID: g.profile, Role: identity.TierOwner}}
	id, err := New(g.c.DB).SeparateFolder(WithSeparateFolderActor(context.Background(), nonlocalPrincipal), "music", g.objects[0], "r1", func(*sql.Tx) error { return nil })
	if id != "" || !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("non-local descriptor was not refused: id=%q err=%v", id, err)
	}
	if n := rows(t, g.c, `SELECT count(*) FROM catalog_entity_mutations`); n != 0 {
		t.Fatalf("non-local descriptor created command rows: %d", n)
	}
	if n := rows(t, g.c, `SELECT count(*) FROM console_operations`); n != 0 {
		t.Fatalf("non-local descriptor created console rows: %d", n)
	}
	if n := rows(t, g.c, `SELECT count(*) FROM catalog_albums`); n != 1 {
		t.Fatalf("non-local descriptor created a separated container: %d", n)
	}
}

// Exercise removal admission with the actual separate owner holding custody.
func TestSeparateFolderActiveRemovalAdmission(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 18.79s")
	}
	for _, library := range []bool{false, true} {
		t.Run(fmt.Sprintf("library=%v", library), func(t *testing.T) {
			f := newSeparateAsyncFixture(t, 20)
			s := New(f.c.DB)
			id, err := s.SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if id != "" || !errors.Is(err, ErrSeparateFolderPending) {
				t.Fatalf("admission %q %v", id, err)
			}
			root := separateAsyncRoot(t, f.c)
			var revision int64
			if err := f.c.DB.QueryRow(`SELECT revision FROM library_configuration WHERE library_id='music'`).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			authorize := func(*sql.Tx) error { return nil }
			enqueue := func(context.Context, *sql.Tx, string, string) error { return nil }
			if library {
				err = s.RemoveLibrary(context.Background(), "music", "Music", revision, authorize, enqueue)
			} else {
				err = s.RemoveSource(context.Background(), "music", f.source, revision, authorize, enqueue)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := separateAsyncEffects(t, f)
			out := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
			if out.State == "succeeded" {
				t.Fatalf("removed source published completion: %+v", out)
			}
			if after := separateAsyncEffects(t, f); after != before {
				t.Fatalf("removed source changed links/claims: %+v -> %+v", before, after)
			}
			if n := rows(t, f.c, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='separate' AND parent_mutation_id IS NULL`); n != 1 {
				t.Fatalf("duplicate roots: %d", n)
			}
		})
	}
}
