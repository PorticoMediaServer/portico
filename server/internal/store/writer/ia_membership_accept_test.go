package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaMembershipTestLibraryID      int64 = 7
	iaMembershipTestLibraryKey           = "membership-library"
	iaMembershipTestSourceID             = "membership-source"
	iaMembershipTestRunID                = "membership-run"
	iaMembershipTestAssetID        int64 = 41
	iaMembershipTestRoot                 = "/media/membership"
	iaMembershipTestObjectID             = "object:membership:1"
	iaMembershipTestObjectRevision       = "object-revision:1"

	iaMembershipTestEpisodeID int64 = 31
	iaMembershipTestMovieID   int64 = 60
	iaMembershipTestSeason1ID int64 = 71
	iaMembershipTestSeason2ID int64 = 72
	iaMembershipTestAlbumID   int64 = 81
	iaMembershipTestSongID    int64 = 92
)

// One private W frame, the frozen clock and the exact group C draft subset. The
// seeded entities are retained evidence: the writer must re-read each one and
// keep its ID while changing only the destination membership.
func iaMembershipAcceptFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	requireC019Fixture(t)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',0,1,1,1,0),(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,0),(6,'album',0,1,1,1,0),(7,'song',1,0,1,1,1)`, nil},
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(8,'book',0,1,1,1,0),(9,'audiobook_file',1,0,1,1,1)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('membership-library','Membership','tv','/media/membership',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'membership-library','/media/membership')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('membership-source','membership-library','Membership','/media/membership','/media/membership','local','membership-root-identity',0,0,'',1,'membership-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('membership-run','membership-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('membership-run',7,'membership-library','/media/membership',0,?,?,'membership-source',3,'membership-incarnation','membership-root-identity',4,'inventory','first-import',11,13,?,5,?,1,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('membership-run','Show/Season 1','membership-directory-identity','membership-directory-revision','','pending',2,0)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(41,'membership-token','/media/membership/Show/Season 1/Episode 1.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(70,X'70000000000000000000000000000000',7,2),(71,X'71000000000000000000000000000000',7,3),(72,X'72000000000000000000000000000000',7,3),(31,X'31000000000000000000000000000000',7,4),(60,X'60000000000000000000000000000000',7,1),(80,X'80000000000000000000000000000000',7,5),(81,X'81000000000000000000000000000000',7,6),(92,X'92000000000000000000000000000000',7,7)`, nil},
		{`INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,year,added_at,metadata_language,show_id,season_id,season_number,numbering,episode_number,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,artist_id,album_id) VALUES(70,X'70000000000000000000000000000000',7,2,NULL,'Show','','show','s','show','s',1,0,'2026-11-01T00:00:00.000000000Z','en-US',NULL,NULL,NULL,NULL,NULL,zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(71,X'71000000000000000000000000000000',7,3,70,'Season 1','','season 1','s','season 1','s',1,0,'2026-11-01T00:00:00.000000000Z','en-US',70,NULL,1,'seasonal',NULL,zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(72,X'72000000000000000000000000000000',7,3,70,'Season 2','','season 2','s','season 2','s',1,0,'2026-11-01T00:00:00.000000000Z','en-US',70,NULL,2,'seasonal',NULL,zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(31,X'31000000000000000000000000000000',7,4,71,'Episode 1','','episode 1','e','episode 1','e',1,0,'2026-11-01T00:00:00.000000000Z','en-US',70,71,1,'seasonal',1,zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(60,X'60000000000000000000000000000000',7,1,NULL,'Movie','','movie','m','movie','m',1,2020,'2026-11-01T00:00:00.000000000Z','en-US',NULL,NULL,NULL,NULL,NULL,zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(80,X'80000000000000000000000000000000',7,5,NULL,'Artist','','artist','a','artist','a',1,0,'2026-11-01T00:00:00.000000000Z','en-US',NULL,NULL,NULL,NULL,NULL,zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(81,X'81000000000000000000000000000000',7,6,80,'Album','','album','a','album','a',1,0,'2026-11-01T00:00:00.000000000Z','en-US',NULL,NULL,NULL,NULL,NULL,zeroblob(16),zeroblob(16),zeroblob(16),1,80,NULL),(92,X'92000000000000000000000000000000',7,7,81,'Song','','song','s','song','s',1,0,'2026-11-01T00:00:00.000000000Z','en-US',NULL,NULL,NULL,NULL,NULL,zeroblob(16),zeroblob(16),zeroblob(16),1,80,81)`, nil},
		{`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(73,X'73000000000000000000000000000000',7,2),(82,X'82000000000000000000000000000000',7,8),(83,X'83000000000000000000000000000000',7,8),(84,X'84000000000000000000000000000000',7,5),(85,X'85000000000000000000000000000000',7,6),(93,X'93000000000000000000000000000000',7,9)`, nil},
		{`INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,year,added_at,metadata_language,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,artist_id,book_id) VALUES(73,X'73000000000000000000000000000000',7,2,NULL,'Show 2','','show 2','s','show 2','s',1,0,'2026-11-01T00:00:00.000000000Z','en-US',zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(84,X'84000000000000000000000000000000',7,5,NULL,'Artist 2','','artist 2','a','artist 2','a',1,0,'2026-11-01T00:00:00.000000000Z','en-US',zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(82,X'82000000000000000000000000000000',7,8,NULL,'Book','','book','b','book','b',1,0,'2026-11-01T00:00:00.000000000Z','en-US',zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(83,X'83000000000000000000000000000000',7,8,NULL,'Book 2','','book 2','b','book 2','b',1,0,'2026-11-01T00:00:00.000000000Z','en-US',zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,NULL),(85,X'85000000000000000000000000000000',7,6,84,'Album 2','','album 2','a','album 2','a',1,0,'2026-11-01T00:00:00.000000000Z','en-US',zeroblob(16),zeroblob(16),zeroblob(16),1,84,NULL),(93,X'93000000000000000000000000000000',7,9,82,'Audiobook File','','audiobook file','a','audiobook file','a',1,0,'2026-11-01T00:00:00.000000000Z','en-US',zeroblob(16),zeroblob(16),zeroblob(16),1,NULL,82)`, nil},
		{`INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(7,'membership-episode-key',31),(7,'membership-season-key',72),(7,'membership-movie-key',60),(7,'membership-album-key',81),(7,'membership-song-key',92),(7,'membership-season-one-key',71),(7,'membership-show-two-key',73),(7,'membership-artist-key',80),(7,'membership-artist-two-key',84),(7,'membership-album-two-key',85),(7,'membership-book-two-key',83),(7,'membership-file-key',93)`, nil},
		{`INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision,library_id) VALUES(31,41,0,1,0,NULL,'',X'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',1,7)`, nil},
		{`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(7,31,41,1,0,42,1)`, nil},
		{`INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(31,'membership-token','whole_source')`, nil},
		{`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'raw-active',?,?,?,'active',1000)`,
			[]any{searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}},
		{`UPDATE search_revision SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0 WHERE singleton=1`, nil},
	} {
		if _, err := w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	registry, err := stmttest.New(t, iaMembershipDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func newIAMembershipAcceptRequest(w *executor) iaMembershipAcceptRequest {
	return iaMembershipAcceptRequest{
		scope:  inventoryScope{runID: iaMembershipTestRunID, libraryKey: iaMembershipTestLibraryKey, libraryID: iaMembershipTestLibraryID},
		source: inventorySource{id: iaMembershipTestSourceID, incarnation: "membership-incarnation", rootIdentity: "membership-root-identity", resolvedRoot: []byte(iaMembershipTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
			leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: "Show/Season 1", identity: "membership-directory-identity", revision: "membership-directory-revision",
			cursorKind: inventoryLinuxCursor, expectedCursor: "", nextCursor: "42", pages: 2, skipped: 0, rawNames: 1, complete: true},
		files: []inventoryFile{{sourceKey: "membership-asset-source-key", relativePath: "Show/Season 1/Episode 1.mkv", assetPath: iaMembershipTestRoot + "/Show/Season 1/Episode 1.mkv",
			objectIdentity: iaMembershipTestObjectID, revision: iaMembershipTestObjectRevision, size: 42, modifiedNS: 7, title: "Episode 1",
			sortKey: "episode 1", sortHead: "e", sortKeyKeepArticles: "episode 1", sortHeadKeepArticles: "e",
			metadataLanguage: "en-US", year: 2020}},
		complete: true,
		changes:  []iaMembershipChange{{subjectKey: "membership-episode-key", subjectKind: 4, subjectRevision: 1, parentKey: "membership-season-key"}},
	}
}

func iaMembershipAcceptReference(t *testing.T, r iaMembershipAcceptRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAMembershipAcceptCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaMembershipAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaMembershipAcceptRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAAcceptMembership, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaMembershipAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaMembershipAcceptRequest, ref ReceiptReference) (*commandTurn, iaMembershipAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaMembershipAcceptTurn(t, w, r, q)
	out, err := acceptIAMembership(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

// iaMembershipRefused proves no receipt, member, search or catalogue mutation
// survived one refused unit.
func iaMembershipRefused(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaEpisodeCount(t, w, `SELECT revision FROM catalog_entities WHERE id=31`); got != 1 {
		t.Fatal("refused request bumped the retained revision", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 1 {
		t.Fatal("refused request changed links", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM episode_asset_boundaries`); got != 1 {
		t.Fatal("refused request changed boundaries", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT last_command_sequence FROM inventory_runs WHERE job_id=?`, iaMembershipTestRunID); got != 1 {
		t.Fatal("refused request advanced the run", got)
	}
}

// A reparent retains the subject ID and writes only the new destination parent;
// a present link writes the new destination membership. The old parent and the
// entity's own title/art are never rewritten, and no search/name fanout occurs.
func TestIAMembershipAcceptReparentAndPresent(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	q.changes = append(q.changes, iaMembershipChange{subjectKey: "membership-movie-key", subjectKind: 1, subjectRevision: 1, effect: "present", edition: "Extended", part: 0, start: 0})
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 3 {
		t.Fatal("fresh membership accept", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_entities WHERE id=31 AND revision=2 AND parent_id=72 AND season_id=72 AND show_id=70 AND title='Episode 1'`, 1},
		{`SELECT count(*) FROM catalog_entities WHERE id=71 AND revision=1 AND parent_id=70`, 1},
		{`SELECT count(*) FROM catalog_entities WHERE id=72 AND revision=1`, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE entity_id=60 AND asset_id=41 AND library_id=7 AND edition='Extended'`, 1},
		{`SELECT count(*) FROM catalog_asset_contributions WHERE parent_id=60 AND asset_id=41`, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='membership_accept' AND result_count=3 AND accepted_revision=6`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='membership_accept' AND member_kind='identity'`, 2},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='membership_accept' AND member_kind='membership' AND link_effect='present'`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE subject_id=31 AND parent_id=72 AND parent_kind=3`, 1},
		{`SELECT count(*) FROM search_publish_jobs`, 0},
		{`SELECT count(*) FROM inventory_directories WHERE cursor='42' AND pages=3`, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_revision=6 AND last_command_sequence=2`, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live rows no longer describe the accepted original; replay still returns
	// the exact saved cohort without a new effect.
	wc008NativeExec(t, w, `UPDATE catalog_entities SET parent_id=70,season_id=NULL,revision=9 WHERE id=31`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='membership-run'`)
	turn, replay, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.resultCount != 3 {
		t.Fatal("saved membership replay", replay, err)
	}
	// The replay frame still holds the single writer connection, so the
	// no-new-receipt check reads the open transaction itself.
	var receipts int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("replay minted a receipt", receipts, err)
	}
	turn.transaction.rollback(nil)
}

// A removed link reports its prior edition/part/range and deletes the exact link,
// contribution and episode boundary in the same complete unit.
func TestIAMembershipAcceptRemovedLinkAndBoundary(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	q.changes = []iaMembershipChange{{subjectKey: "membership-episode-key", subjectKind: 4, subjectRevision: 1, effect: "removed", boundaryStatus: "whole_source", part: 0, start: 0}}
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 2 {
		t.Fatal("removed link accept", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_asset_links`, 0},
		{`SELECT count(*) FROM catalog_asset_contributions`, 0},
		{`SELECT count(*) FROM episode_asset_boundaries`, 0},
		{`SELECT count(*) FROM catalog_entities WHERE id=31 AND revision=1`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE link_effect='removed' AND part_index=0 AND start_seconds=0 AND episode_boundary_status='whole_source'`, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// A second removal cannot re-describe the deleted link from live rows.
	turn, replay, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.result.facts.resultCount != 2 {
		t.Fatal("removed replay", replay, err)
	}
	turn.transaction.rollback(nil)
}

// A removed link's boundary status is an exact CAS: a different stored status is
// a conflict, so the delete can never silently remove unproven boundary evidence.
func TestIAMembershipAcceptStaleBoundaryRefuses(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	q.changes = []iaMembershipChange{{subjectKey: "membership-episode-key", subjectKind: 4, subjectRevision: 1, effect: "removed", boundaryStatus: "unknown_multi_episode", part: 0, start: 0}}
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("stale boundary status", out, err)
	}
	turn.transaction.rollback(nil)
	iaMembershipRefused(t, w)
}

// Edition, part and CUE are explicit membership facts: an existing link is
// updated in place (no second row, no new identity) and the boundary status is
// the exact CAS.
func TestIAMembershipAcceptEditionPartUpdate(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	q.changes = []iaMembershipChange{{subjectKey: "membership-episode-key", subjectKind: 4, subjectRevision: 1, effect: "present",
		edition: "Extended", part: 2, start: 0, boundaryStatus: "unknown_multi_episode"}}
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41`, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE entity_id=31 AND part_index=2 AND edition='Extended'`, 1},
		{`SELECT count(*) FROM episode_asset_boundaries WHERE item_id=31 AND status='unknown_multi_episode'`, 1},
		{`SELECT count(*) FROM catalog_entities WHERE id=31 AND revision=1`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE member_kind='membership' AND edition='Extended' AND part_index=2 AND episode_boundary_status='unknown_multi_episode'`, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
}

// The distinct total cap is enforced before IO: more than 64 changes, or the
// same (subject,effect) twice, refuses the whole request without an effect.
func TestIAMembershipAcceptDistinctTotalCap(t *testing.T) {
	t.Run("too_many", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = make([]iaMembershipChange, iaMembershipMaxChanges+1)
		for i := range q.changes {
			q.changes[i] = iaMembershipChange{subjectKey: "membership-missing-" + string(rune('a'+i%26)), subjectKind: 4, subjectRevision: 1, effect: "present", start: 0}
		}
		ref := ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrInvalid) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("too many changes", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
	})
	t.Run("duplicate", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = append(q.changes, q.changes[0])
		ref := ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrInvalid) || out.decision != 0 {
			t.Fatal("duplicate change", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
	})
}

// The caller's observed retained revision is an exact witness: a stale one is a
// conflict before the first write, so a concurrent reparent cannot be overwritten.
func TestIAMembershipAcceptStaleRetainedID(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	wc008NativeExec(t, w, `UPDATE catalog_entities SET revision=2 WHERE id=31`)
	q := newIAMembershipAcceptRequest(w)
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("stale retained revision", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=31 AND parent_id=71 AND season_id=71 AND revision=2`); got != 1 {
		t.Fatal("stale request reparented the subject", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
		t.Fatal("stale request wrote a receipt", got)
	}
}

// A malformed existing parent chain that reaches the subject is refused as a
// cycle; the destination uniqueness check never permits an ancestry loop.
func TestIAMembershipAcceptNoAncestryCycle(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	// album -> song -> album would be created by reparenting the song under the
	// album; seed the malformed back edge first to model the attempted cycle.
	wc008NativeExec(t, w, `UPDATE catalog_entities SET parent_id=92 WHERE id=81`)
	q := newIAMembershipAcceptRequest(w)
	q.changes = []iaMembershipChange{{subjectKey: "membership-song-key", subjectKind: 7, subjectRevision: 1, parentKey: "membership-album-key"}}
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("ancestry cycle", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
		t.Fatal("cycle wrote a receipt", got)
	}
}

// A reparent writes only the new destination membership: no old-parent row is
// re-read or saved, no membership row appears for an identity-only change, and
// no descendant art/name/search row is fanned out.
func TestIAMembershipAcceptDestinationOnlyChronology(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 {
		t.Fatal("identity-only reparent", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='membership_accept' AND member_kind='identity' AND parent_id=72 AND subject_id=31`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE member_kind='membership'`, 0},
		{`SELECT count(*) FROM scan_media_result_members WHERE parent_id=71`, 0},
		{`SELECT count(*) FROM catalog_entities WHERE id=71 AND revision=1 AND parent_id=70`, 1},
		{`SELECT count(*) FROM catalog_entities WHERE id=31 AND title='Episode 1' AND sort_key='episode 1'`, 1},
		{`SELECT count(*) FROM search_publish_jobs`, 0},
	} {
		if got := iaEpisodeCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// A plain episode-only reparent does not touch the asset link.
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_asset_links WHERE entity_id=31 AND part_index=0`); got != 1 {
		t.Fatal("reparent rewrote the link", got)
	}
}

// A reparent into another season is a genuinely new child membership at the
// destination: the destination season and its show advance to the moved
// episode's own retained added time (not this command's clock), each exactly
// once, while the source season keeps its chronology untouched.
func TestIAMembershipAcceptReparentAdvancesDestinationChronology(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 {
		t.Fatal("chronology reparent", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_entities WHERE id=72 AND last_content_added_at='2026-11-01T00:00:00.000000000Z'`, 1},
		{`SELECT count(*) FROM catalog_entities WHERE id=70 AND last_content_added_at='2026-11-01T00:00:00.000000000Z'`, 1},
		{`SELECT count(*) FROM catalog_entities WHERE id=71 AND last_content_added_at IS NULL`, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Replay decodes the saved cohort and writes nothing: a newer destination
	// value installed after the accept survives an exact replay.
	wc008NativeExec(t, w, `UPDATE catalog_entities SET last_content_added_at='2030-01-01T00:00:00.000000000Z' WHERE id IN(70,72)`)
	turn, replay, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest {
		t.Fatal("saved chronology replay", replay, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id IN(70,72) AND last_content_added_at='2030-01-01T00:00:00.000000000Z'`); got != 2 {
		t.Fatal("replay rewrote destination chronology", got)
	}
}

// An older moved episode never pulls the destination season or show backwards:
// the monotonic advance leaves an equal or newer destination value alone.
func TestIAMembershipAcceptReparentOlderEpisodeKeepsDestinationChronology(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	wc008NativeExec(t, w, `UPDATE catalog_entities SET last_content_added_at='2026-11-05T00:00:00.000000000Z' WHERE id IN(70,72)`)
	q := newIAMembershipAcceptRequest(w)
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("older reparent", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id IN(70,72) AND last_content_added_at='2026-11-05T00:00:00.000000000Z'`); got != 2 {
		t.Fatal("older episode moved destination chronology backwards", got)
	}
}

// The denormalized hierarchy columns are pinned to one accepted parent. A season
// reparent rewrites only the season's show_id, so it refuses while any episode
// child exists; an album reparent rewrites only the album's artist_id, so it
// refuses while any song child exists unless the destination artist is already
// the album's artist; a destination that is itself moved in the same request is
// refused before IO; a song's artist_id follows its new album. A removal also
// never deletes a contribution that still carries authored references.
func TestIAMembershipAcceptHierarchyGuards(t *testing.T) {
	manualRef := func() ReceiptReference {
		return ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
	}
	t.Run("season_with_episodes_refused", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-season-one-key", subjectKind: 3, subjectRevision: 1, parentKey: "membership-show-two-key"}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("season with episodes", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=71 AND parent_id=70 AND show_id=70 AND revision=1`); got != 1 {
			t.Fatal("refused season reparent moved the season", got)
		}
	})
	t.Run("season_without_episodes_reparents", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-season-key", subjectKind: 3, subjectRevision: 1, parentKey: "membership-show-two-key"}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 {
			t.Fatal("childless season reparent", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		for _, check := range []struct {
			query string
			want  int64
		}{
			{`SELECT count(*) FROM catalog_entities WHERE id=72 AND parent_id=73 AND show_id=73 AND season_number=2 AND revision=2`, 1},
			{`SELECT count(*) FROM catalog_entities WHERE id=70 AND revision=1`, 1},
			{`SELECT count(*) FROM catalog_entities WHERE id=31 AND parent_id=71 AND show_id=70 AND revision=1`, 1},
			{`SELECT count(*) FROM scan_media_result_members WHERE member_kind='identity' AND subject_id=72 AND parent_id=73 AND parent_kind=2`, 1},
			{`SELECT count(*) FROM scan_media_result_members WHERE member_kind='membership'`, 0},
		} {
			if got := iaEpisodeCount(t, w, check.query); got != check.want {
				t.Fatal(check.query, got, check.want)
			}
		}
	})
	t.Run("destination_moved_same_request_refused", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{
			{subjectKey: "membership-episode-key", subjectKind: 4, subjectRevision: 1, parentKey: "membership-season-key"},
			{subjectKey: "membership-season-key", subjectKind: 3, subjectRevision: 1, parentKey: "membership-show-two-key"},
		}
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, manualRef())
		if !errors.Is(err, ErrInvalid) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("same-request destination move", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
		// The same episode move alone is accepted: only the interdependency was
		// refused, not the destination season.
		q.changes = q.changes[:1]
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err = iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh {
			t.Fatal("episode move to an unmoved season", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("album_with_songs_reparent_refused", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-album-key", subjectKind: 6, subjectRevision: 1, parentKey: "membership-artist-two-key"}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("album with songs reparented", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=81 AND parent_id=80 AND artist_id=80 AND revision=1`); got != 1 {
			t.Fatal("refused album reparent moved the album", got)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=92 AND parent_id=81 AND album_id=81 AND artist_id=80 AND revision=1`); got != 1 {
			t.Fatal("refused album reparent moved the song", got)
		}
	})
	t.Run("album_without_songs_reparents", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-album-two-key", subjectKind: 6, subjectRevision: 1, parentKey: "membership-artist-key"}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 {
			t.Fatal("childless album reparent", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=85 AND parent_id=80 AND artist_id=80 AND revision=2`); got != 1 {
			t.Fatal("childless album did not reparent", got)
		}
	})
	t.Run("album_same_artist_move_allowed", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-album-key", subjectKind: 6, subjectRevision: 1, parentKey: "membership-artist-key"}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 {
			t.Fatal("same-artist album move", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=81 AND parent_id=80 AND artist_id=80 AND revision=2`); got != 1 {
			t.Fatal("same-artist album move changed artist_id", got)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=92 AND parent_id=81 AND album_id=81 AND artist_id=80 AND revision=1`); got != 1 {
			t.Fatal("same-artist album move touched the song", got)
		}
	})
	t.Run("song_reparent_follows_album_artist", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-song-key", subjectKind: 7, subjectRevision: 1, parentKey: "membership-album-two-key"}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh {
			t.Fatal("song reparent", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=92 AND parent_id=85 AND album_id=85 AND artist_id=84 AND revision=2`); got != 1 {
			t.Fatal("song artist did not follow the destination album", got)
		}
	})
	t.Run("audiobook_file_reparent", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-file-key", subjectKind: 9, subjectRevision: 1, parentKey: "membership-book-two-key"}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh {
			t.Fatal("audiobook file reparent", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=93 AND parent_id=83 AND book_id=83 AND revision=2`); got != 1 {
			t.Fatal("file book did not follow the destination book", got)
		}
	})
	t.Run("authored_contribution_refuses_removal", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		wc008NativeExec(t, w, `UPDATE catalog_asset_contributions SET membership_refs=2,authored_refs=1 WHERE parent_id=31 AND asset_id=41`)
		q := newIAMembershipAcceptRequest(w)
		q.changes = []iaMembershipChange{{subjectKey: "membership-episode-key", subjectKind: 4, subjectRevision: 1, effect: "removed", boundaryStatus: "whole_source", part: 0, start: 0}}
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 {
			t.Fatal("authored contribution removal", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_asset_contributions WHERE parent_id=31 AND asset_id=41 AND membership_refs=2 AND authored_refs=1`); got != 1 {
			t.Fatal("refused removal lost the authored contribution", got)
		}
	})
	t.Run("reading_item_kinds_refused", func(t *testing.T) {
		for _, kind := range []int64{20, 21} {
			w, reg, p := iaMembershipAcceptFixture(t)
			q := newIAMembershipAcceptRequest(w)
			q.changes = []iaMembershipChange{{subjectKey: "membership-reading-key", subjectKind: kind, subjectRevision: 1, effect: "present", start: 0}}
			turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, manualRef())
			if !errors.Is(err, ErrInvalid) || out.decision != 0 {
				t.Fatal("reading item link change", kind, out, err)
			}
			turn.transaction.rollback(nil)
			iaMembershipRefused(t, w)
		}
	})
}

// Every fresh witness is re-proven after the receipt is Fresh.
func TestIAMembershipAcceptFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaMembershipAcceptRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaMembershipAcceptRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source":    func(_ *testing.T, _ *executor, q *iaMembershipAcceptRequest) { q.source.generation++ },
		"stale_directory": func(_ *testing.T, _ *executor, q *iaMembershipAcceptRequest) { q.directory.expectedCursor = "9" },
		"changed_asset":   func(_ *testing.T, _ *executor, q *iaMembershipAcceptRequest) { q.files[0].size++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaMembershipAcceptRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaMembershipAcceptRequest) { q.run.revision++ },
		"wrong_destination": func(_ *testing.T, _ *executor, q *iaMembershipAcceptRequest) {
			q.changes[0].parentKey = "membership-movie-key"
		},
		"missing_subject": func(_ *testing.T, _ *executor, q *iaMembershipAcceptRequest) {
			q.changes[0].subjectKey = "membership-missing-key"
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaMembershipAcceptFixture(t)
			q := newIAMembershipAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaMembershipAcceptReference(t, q, 2, 2)
			turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			iaMembershipRefused(t, w)
		})
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAMembershipAcceptAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaMembershipAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
	})
	t.Run("stale_authority", func(t *testing.T) {
		for name, mutation := range map[string]string{
			"demoted_owner":  `UPDATE direct_memberships SET role='member'`,
			"retired_token":  `UPDATE authorization_family_tokens SET retired=1`,
			"denied_device":  `UPDATE identity_security_fences SET denied=1`,
			"profile_reset":  `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
			"reset_session":  `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`,
			"narrowed_grant": `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
		} {
			t.Run(name, func(t *testing.T) {
				w, reg, p := iaMembershipAcceptFixture(t)
				wc008NativeExec(t, w, mutation)
				q := newIAMembershipAcceptRequest(w)
				ref := iaMembershipAcceptReference(t, q, 2, 2)
				turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaMembershipRefused(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, budget := iaMembershipAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAMembership(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
	})
}

// Current authority is re-observed after the fresh gates and again after the
// writes.
func TestIAMembershipAcceptLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		state := installIAEntryQueryRevocation(t, w, "FROM catalog_assets")
		q := newIAMembershipAcceptRequest(w)
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("asset-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_membership_late_revocation AFTER UPDATE ON catalog_entities BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := newIAMembershipAcceptRequest(w)
		ref := iaMembershipAcceptReference(t, q, 2, 2)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var revision int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT revision FROM catalog_entities WHERE id=31`).Scan(&revision); err != nil || revision != 2 {
			t.Fatal("reparent not reached before the final recheck", revision, err)
		}
		turn.transaction.rollback(nil)
		iaMembershipRefused(t, w)
	})
}

func TestIAMembershipAcceptReplayRejectsTamperedCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"missing_member": `DELETE FROM scan_media_result_members WHERE ordinal=0`,
		"member_bytes":   `UPDATE scan_command_receipts SET ia_media_member_bytes=1`,
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaMembershipAcceptFixture(t)
			q := newIAMembershipAcceptRequest(w)
			ref := iaMembershipAcceptReference(t, q, 2, 2)
			turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered cohort served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAMembershipAcceptUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := newIAMembershipAcceptRequest(w)
	q.changes = append(q.changes, iaMembershipChange{subjectKey: "membership-movie-key", subjectKind: 1, subjectRevision: 1, effect: "present", start: 0})
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("fresh before commit", out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_entities WHERE id=31 AND revision=2 AND parent_id=72 AND season_id=72`, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE entity_id=60 AND asset_id=41`, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='membership_accept' AND result_count=3`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='membership_accept'`, 3},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAAcceptMembership, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical rows still exist.
func TestIAMembershipAcceptRetiredAfterAck(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='membership-run'`)
	before := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_media_result_members`)
	turn, out, err = iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_media_result_members`); got != before {
		t.Fatal("retired replay wrote", got, before)
	}
	if got := iaEpisodeCount(t, w, `SELECT revision FROM catalog_entities WHERE id=31`); got != 2 {
		t.Fatal("retired replay reparented", got)
	}
}

// A conflicting digest and a non-contiguous sequence are conflicts, never a
// fresh admission or a new receipt.
func TestIAMembershipAcceptRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	changed := q
	changed.changes[0].part = 9
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaMembershipAcceptReference(t, gap, 3, 4)
	turn, out, err = iaMembershipAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	iaMembershipRefused(t, w)
}

func TestIAMembershipAcceptRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaMembershipAcceptFixture(t)
	q := newIAMembershipAcceptRequest(w)
	ref := iaMembershipAcceptReference(t, q, 2, 2)
	turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaMembershipRefused(t, w)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_directories WHERE cursor='' AND pages=2`); got != 1 {
		t.Fatal("rollback advanced the directory", got)
	}
}
