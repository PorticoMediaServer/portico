package writer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaEpisodeTestLibraryID      int64 = 7
	iaEpisodeTestLibraryKey           = "episode-library"
	iaEpisodeTestSourceID             = "episode-source"
	iaEpisodeTestRunID                = "episode-run"
	iaEpisodeTestAssetID        int64 = 41
	iaEpisodeTestRoot                 = "/media/episode"
	iaEpisodeTestObjectID             = "object:episode:1"
	iaEpisodeTestObjectRevision       = "object-revision:1"
)

// One private W frame, the frozen clock and the exact group C draft subset. The
// fixture seeds only prepared source/run/asset/search witnesses; it is not a
// qualified population, admission profile or product wiring.
func iaEpisodeAcceptFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-episode-accept-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',0,1,1,1,0),(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('episode-library','Episode','tv','/media/episode',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'episode-library','/media/episode')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('episode-source','episode-library','Episode','/media/episode','/media/episode','local','episode-root-identity',0,0,'',1,'episode-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('episode-run','episode-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('episode-run',7,'episode-library','/media/episode',0,?,?,'episode-source',3,'episode-incarnation','episode-root-identity',4,'inventory','first-import',11,13,?,5,?,1,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('episode-run','.','episode-directory-identity','episode-directory-revision','','pending',2,0)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(41,'episode-token','/media/episode/Show S01E01.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'raw-active',?,?,?,'active',1000)`,
			[]any{searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}},
		{`UPDATE search_revision SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0 WHERE singleton=1`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	registry, err := stmttest.New(t, iaEpisodeDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaEpisodeBytes(first byte) []byte {
	b := make([]byte, 16)
	b[0] = first
	return b
}

func iaEpisodeAcceptRequest(w *executor) iaEpisodeInventoryRequest {
	return iaEpisodeInventoryRequest{
		scope:  inventoryScope{runID: iaEpisodeTestRunID, libraryKey: iaEpisodeTestLibraryKey, libraryID: iaEpisodeTestLibraryID},
		source: inventorySource{id: iaEpisodeTestSourceID, incarnation: "episode-incarnation", rootIdentity: "episode-root-identity", resolvedRoot: []byte(iaEpisodeTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
			leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: ".", identity: "episode-directory-identity", revision: "episode-directory-revision",
			cursorKind: inventoryLinuxCursor, expectedCursor: "", nextCursor: "42", pages: 2, skipped: 0, rawNames: 1, complete: true},
		files: []inventoryFile{{sourceKey: "asset-source-key", relativePath: "Show S01E01.mkv", assetPath: iaEpisodeTestRoot + "/Show S01E01.mkv",
			objectIdentity: iaEpisodeTestObjectID, revision: iaEpisodeTestObjectRevision, size: 42, modifiedNS: 7, title: "Show S01E01",
			sortTitle: "", sortKey: "show s01e01", sortHead: "s", sortKeyKeepArticles: "show s01e01", sortHeadKeepArticles: "s",
			metadataLanguage: "en-US", year: 2020}},
		naming: naming.Reading{
			Titles:   []naming.Evidence[string]{{Value: "Show", Source: naming.ParentFolder, Path: "Show"}},
			Seasons:  []naming.Evidence[int]{{Value: 1, Source: naming.FileName}},
			Episodes: []naming.Evidence[[]int]{{Value: []int{1}, Source: naming.FileName}},
		},
		complete: true,
		showKey:  "show-key",
		episodes: []iaEpisodeLink{{season: 1, episode: 1, part: 0, start: 0, seasonKey: "season-1-key", episodeKey: "episode-1-key"}},
	}
}

func iaEpisodeAcceptReference(t *testing.T, r iaEpisodeInventoryRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAEpisodeInventoryCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaEpisodeAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaEpisodeInventoryRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAAcceptEpisodeInventory, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaEpisodeAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaEpisodeInventoryRequest, ref ReceiptReference) (*commandTurn, iaEpisodeAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaEpisodeAcceptTurn(t, w, r, q)
	out, err := acceptIAEpisodeInventory(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

func iaEpisodeCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func iaEpisodeNoEffects(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"catalog_identities", "catalog_entities", "catalog_asset_links", "catalog_asset_contributions",
		"scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaEpisodeCount(t, w, `SELECT last_command_sequence FROM inventory_runs WHERE job_id=?`, iaEpisodeTestRunID); got != 1 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT input_sequence FROM search_revision WHERE singleton=1`); got != 0 {
		t.Fatal("refused request advanced search input", got)
	}
}

// Fresh birth writes one complete unit: show/season/episode identities, the
// hierarchy, both asset links, contributions, search intent, the directory/run
// advance and the typed receipt/child cohort. Replay returns the saved result
// after live source/lease changes without any new effect.
func TestIAEpisodeAcceptFreshReplayAndLiveCohort(t *testing.T) {
	w, reg, p := iaEpisodeAcceptFixture(t)
	q := iaEpisodeAcceptRequest(w)
	ref := iaEpisodeAcceptReference(t, q, 2, 2)
	turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh episode accept", out, err)
	}
	if out.result.facts.acceptedRevision != 6 || out.result.facts.appliedMS != w.now().UnixMilli() ||
		out.result.facts.resultCount != 4 || out.result.facts.memberBytes <= 0 ||
		out.result.facts.objectID != iaEpisodeTestObjectID || string(out.result.facts.nextPayload) != "42" {
		t.Fatal("fresh episode facts", out.result.facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM catalog_identities WHERE library_id=7`, nil, 3},
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7`, nil, 3},
		{`SELECT count(*) FROM catalog_entities WHERE kind=2 AND show_id IS NULL AND parent_id IS NULL`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind=3 AND show_id=(SELECT id FROM catalog_identities WHERE source_key='show-key') AND season_number=1`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind=4 AND episode_number=1 AND numbering='seasonal' AND tv_hierarchy_revision=1`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41 AND part_index=0 AND start_seconds=0 AND end_seconds IS NULL`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_contributions WHERE asset_id=41 AND membership_refs=1 AND file_size=42`, nil, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='episode_inventory_accept' AND result_count=4 AND accepted_revision=6`, nil, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='episode_inventory_accept' AND ordinal BETWEEN 0 AND 3`, nil, 4},
		{`SELECT count(*) FROM search_publish_jobs`, nil, 3},
		{`SELECT count(*) FROM search_generations WHERE pending_subjects=3`, nil, 1},
		{`SELECT count(*) FROM search_revision WHERE input_sequence=1`, nil, 1},
		{`SELECT count(*) FROM inventory_directories WHERE cursor='42' AND pages=3`, nil, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1`, nil, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live source/lease changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='episode-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='episode-run'`)
	turn, replay, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.resultCount != 4 ||
		replay.result.facts.acceptedRevision != out.result.facts.acceptedRevision {
		t.Fatal("saved episode replay", replay, err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 1 {
		t.Fatal("replay minted a receipt", got)
	}
	turn.transaction.rollback(nil)
}

// A second accepted file reuses the exact show/season subjects, adds only its
// own episode, and never bumps a reused entity revision.
func TestIAEpisodeAcceptReusedIdentity(t *testing.T) {
	w, reg, p := iaEpisodeAcceptFixture(t)
	first := iaEpisodeAcceptRequest(w)
	ref := iaEpisodeAcceptReference(t, first, 2, 2)
	turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, first, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	showID := iaEpisodeCount(t, w, `SELECT subject_id FROM catalog_identities WHERE source_key='show-key'`)
	seasonID := iaEpisodeCount(t, w, `SELECT subject_id FROM catalog_identities WHERE source_key='season-1-key'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='episode-run'`)
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(42,'episode-token-2','/media/episode/Show S01E02.mkv',43,8,'','','',0,0,0,7)`)
	second := first
	second.run = inventoryRun{revision: 6, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
		leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 2, acknowledgedSequence: 2, phase: "inventory", scanMode: "first-import"}
	second.directory.expectedCursor, second.directory.nextCursor, second.directory.pages = "42", "43", 3
	second.files = []inventoryFile{{sourceKey: "asset-source-key-2", relativePath: "Show S01E02.mkv", assetPath: iaEpisodeTestRoot + "/Show S01E02.mkv",
		objectIdentity: "object:episode:2", revision: "object-revision:2", size: 43, modifiedNS: 8, title: "Show S01E02",
		sortTitle: "", sortKey: "show s01e02", sortHead: "s", sortKeyKeepArticles: "show s01e02", sortHeadKeepArticles: "s",
		metadataLanguage: "en-US", year: 2020}}
	second.episodes = []iaEpisodeLink{{season: 1, episode: 2, part: 0, start: 0, seasonKey: "season-1-key", episodeKey: "episode-2-key"}}
	ref2 := iaEpisodeAcceptReference(t, second, 3, 3)
	turn, out, err = iaEpisodeAcceptRun(t, w, reg, p, second, ref2)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 4 {
		t.Fatal("second episode accept", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT subject_id FROM catalog_identities WHERE source_key='show-key'`); got != showID {
		t.Fatal("show identity not reused", got, showID)
	}
	if got := iaEpisodeCount(t, w, `SELECT subject_id FROM catalog_identities WHERE source_key='season-1-key'`); got != seasonID {
		t.Fatal("season identity not reused", got, seasonID)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities`); got != 4 {
		t.Fatal("entity count", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id IN (?,?) AND revision=1`, showID, seasonID); got != 2 {
		t.Fatal("reused entity revision bumped", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 2 {
		t.Fatal("link count", got)
	}
	// The first original is now at or below the durable ACK floor: it is Retired,
	// never decoded as Applied, while its physical rows remain for the audit.
	turn, replay, err := iaEpisodeAcceptRun(t, w, reg, p, first, ref)
	if err != nil || replay.decision != receiptRetired || replay.digest != (ResultDigest{}) || replay.result.facts.resultCount != 0 {
		t.Fatal("acknowledged original after a second accept", replay, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_media_result_members`); got != 8 {
		t.Fatal("retired original changed its saved cohort", got)
	}
}

// Eight episode links are one complete accepted unit; a ninth refuses the whole
// request before any effect, and a seasonless episode keeps its catalogue
// hierarchy while its typed member carries no invented parent edge.
func TestIAEpisodeAcceptFanout(t *testing.T) {
	t.Run("eight", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		q := iaEpisodeAcceptRequest(w)
		q.episodes = make([]iaEpisodeLink, naming.MaxEpisodeLinks)
		for i := range q.episodes {
			q.episodes[i] = iaEpisodeLink{season: 1, episode: int64(i + 1), part: 0, start: float64(i * 60),
				seasonKey: "season-1-key", episodeKey: "episode-" + string(rune('a'+i))}
		}
		ref := iaEpisodeAcceptReference(t, q, 2, 2)
		turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 18 {
			t.Fatal("eight links", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		for _, check := range []struct {
			query string
			want  int64
		}{
			{`SELECT count(*) FROM catalog_asset_links`, 8},
			{`SELECT count(*) FROM catalog_asset_contributions`, 8},
			{`SELECT count(*) FROM catalog_entities WHERE kind=4`, 8},
			{`SELECT count(*) FROM scan_media_result_members`, 18},
			{`SELECT count(*) FROM search_publish_jobs`, 10},
		} {
			if got := iaEpisodeCount(t, w, check.query); got != check.want {
				t.Fatal(check.query, got, check.want)
			}
		}
	})
	t.Run("distinct_seasons", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		q := iaEpisodeAcceptRequest(w)
		q.episodes = make([]iaEpisodeLink, naming.MaxEpisodeLinks)
		for i := range q.episodes {
			season := int64(i + 1)
			q.episodes[i] = iaEpisodeLink{season: season, episode: 1, part: 0, start: 0,
				seasonKey: "season-key-" + string(rune('a'+i)), episodeKey: "episode-key-" + string(rune('a'+i))}
		}
		ref := iaEpisodeAcceptReference(t, q, 2, 2)
		turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 25 {
			t.Fatal("distinct seasons", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		for _, check := range []struct {
			query string
			want  int64
		}{
			{`SELECT count(*) FROM catalog_entities`, 17},
			{`SELECT count(*) FROM catalog_entities WHERE kind=3`, 8},
			{`SELECT count(*) FROM catalog_asset_links`, 8},
			{`SELECT count(*) FROM scan_media_result_members`, 25},
			{`SELECT count(*) FROM search_publish_jobs`, 17},
		} {
			if got := iaEpisodeCount(t, w, check.query); got != check.want {
				t.Fatal(check.query, got, check.want)
			}
		}
	})
	t.Run("nine", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		q := iaEpisodeAcceptRequest(w)
		q.episodes = make([]iaEpisodeLink, 9)
		for i := range q.episodes {
			q.episodes[i] = iaEpisodeLink{season: 1, episode: int64(i + 1), part: 0, start: 0,
				seasonKey: "season-1-key", episodeKey: "episode-" + string(rune('a'+i))}
		}
		ref := ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
		turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrInvalid) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
			t.Fatal("nine links", out, err)
		}
		turn.transaction.rollback(nil)
		iaEpisodeNoEffects(t, w)
	})
	t.Run("seasonless", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		q := iaEpisodeAcceptRequest(w)
		q.episodes = []iaEpisodeLink{{season: 0, episode: 5, part: 0, start: 0, episodeKey: "episode-absolute-key"}}
		ref := iaEpisodeAcceptReference(t, q, 2, 2)
		turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 3 {
			t.Fatal("seasonless episode", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		showID := iaEpisodeCount(t, w, `SELECT subject_id FROM catalog_identities WHERE source_key='show-key'`)
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=4 AND show_id=? AND parent_id=? AND season_id IS NULL AND numbering='absolute'`, showID, showID); got != 1 {
			t.Fatal("seasonless hierarchy", got)
		}
	})
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed
// library, source, directory, asset or parent refuses the complete unit before
// any catalogue/receipt row exists.
func TestIAEpisodeAcceptFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaEpisodeInventoryRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaEpisodeInventoryRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source":    func(_ *testing.T, _ *executor, q *iaEpisodeInventoryRequest) { q.source.generation++ },
		"stale_directory": func(_ *testing.T, _ *executor, q *iaEpisodeInventoryRequest) { q.directory.expectedCursor = "9" },
		"changed_asset":   func(_ *testing.T, _ *executor, q *iaEpisodeInventoryRequest) { q.files[0].size++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaEpisodeInventoryRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaEpisodeInventoryRequest) { q.run.revision++ },
		"foreign_parent": func(t *testing.T, w *executor, _ *iaEpisodeInventoryRequest) {
			wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(99,X'99000000000000000000000000000000',7,2),(100,X'9a000000000000000000000000000000',7,3)`)
			wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_number) VALUES(99,X'99000000000000000000000000000000',7,2,'Other','other','o','other','o',1,zeroblob(16),zeroblob(16),NULL,NULL),(100,X'9a000000000000000000000000000000',7,3,'Season 1','season 1','s','season 1','s',1,zeroblob(16),zeroblob(16),99,1)`)
			wc008NativeExec(t, w, `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(7,'season-1-key',100)`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaEpisodeAcceptFixture(t)
			q := iaEpisodeAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaEpisodeAcceptReference(t, q, 2, 2)
			turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=4`); got != 0 {
				t.Fatal("gate wrote an episode entity", got)
			}
			if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 0 {
				t.Fatal("gate wrote a link", got)
			}
			if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
				t.Fatal("gate wrote a receipt", got)
			}
		})
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAEpisodeAcceptAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		q := iaEpisodeAcceptRequest(w)
		ref := iaEpisodeAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaEpisodeAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaEpisodeNoEffects(t, w)
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
				w, reg, p := iaEpisodeAcceptFixture(t)
				wc008NativeExec(t, w, mutation)
				q := iaEpisodeAcceptRequest(w)
				ref := iaEpisodeAcceptReference(t, q, 2, 2)
				turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaEpisodeNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		q := iaEpisodeAcceptRequest(w)
		ref := iaEpisodeAcceptReference(t, q, 2, 2)
		turn, budget := iaEpisodeAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAEpisodeInventory(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaEpisodeNoEffects(t, w)
	})
}

// Current authority is re-observed after the fresh gates and again after the
// writes. A same-turn revocation injected by the last read or by a catalogue
// write must deny the fresh result and leave no committed effect.
func TestIAEpisodeAcceptLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		state := installIAEntryQueryRevocation(t, w, "FROM catalog_assets")
		q := iaEpisodeAcceptRequest(w)
		ref := iaEpisodeAcceptReference(t, q, 2, 2)
		turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("asset-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaEpisodeNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaEpisodeAcceptFixture(t)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_episode_late_revocation AFTER INSERT ON catalog_asset_links BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaEpisodeAcceptRequest(w)
		ref := iaEpisodeAcceptReference(t, q, 2, 2)
		turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var links int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_asset_links`).Scan(&links); err != nil || links != 1 {
			t.Fatal("link write not reached before the final recheck", links, err)
		}
		turn.transaction.rollback(nil)
		iaEpisodeNoEffects(t, w)
	})
}

// A saved cohort must be complete and digest-exact before it is served. A
// missing member, a wrong retained-byte ledger and a changed result digest all
// refuse the replay with zero public output.
func TestIAEpisodeAcceptReplayRejectsTamperedCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"missing_member": `DELETE FROM scan_media_result_members WHERE ordinal=3`,
		"member_bytes":   `UPDATE scan_command_receipts SET ia_media_member_bytes=1`,
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaEpisodeAcceptFixture(t)
			q := iaEpisodeAcceptRequest(w)
			ref := iaEpisodeAcceptReference(t, q, 2, 2)
			turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered cohort served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A stored search bank/profile that cannot be honored refuses the whole unit
// instead of accepting a catalogue cohort with no search intent.
func TestIAEpisodeAcceptRefusesUnusableSearchBank(t *testing.T) {
	w, reg, p := iaEpisodeAcceptFixture(t)
	wc008NativeExec(t, w, `UPDATE search_generations SET unicode_fingerprint='bad'`)
	q := iaEpisodeAcceptRequest(w)
	ref := iaEpisodeAcceptReference(t, q, 2, 2)
	turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
	if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("unusable search bank", out, err)
	}
	turn.transaction.rollback(nil)
	iaEpisodeNoEffects(t, w)
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAEpisodeAcceptUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaEpisodeAcceptFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaEpisodeAcceptRequest(w)
	ref := iaEpisodeAcceptReference(t, q, 2, 2)
	turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
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
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7`, 3},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41`, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='episode_inventory_accept' AND result_count=4`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='episode_inventory_accept'`, 4},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAAcceptEpisodeInventory, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

func iaEpisodePath(t *testing.T, w *executor) string {
	t.Helper()
	var sequence int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical rows still exist.
func TestIAEpisodeAcceptRetiredAfterAck(t *testing.T) {
	w, reg, p := iaEpisodeAcceptFixture(t)
	q := iaEpisodeAcceptRequest(w)
	ref := iaEpisodeAcceptReference(t, q, 2, 2)
	turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='episode-run'`)
	before := map[string]int64{}
	for _, table := range []string{"catalog_entities", "catalog_asset_links", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		before[table] = iaEpisodeCount(t, w, `SELECT count(*) FROM `+table)
	}
	turn, out, err = iaEpisodeAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	for table, want := range before {
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM `+table); got != want {
			t.Fatal("retired replay wrote", table, got, want)
		}
	}
}

// A conflicting digest and a non-contiguous sequence are conflicts, never a
// fresh admission or a new receipt.
func TestIAEpisodeAcceptRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaEpisodeAcceptFixture(t)
	q := iaEpisodeAcceptRequest(w)
	ref := iaEpisodeAcceptReference(t, q, 2, 2)
	changed := q
	changed.files[0].revision += "x"
	turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	// The request is internally consistent for sequence 3, but the durable owner
	// is still at sequence 1: a gap, never a fresh admission.
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaEpisodeAcceptReference(t, gap, 3, 4)
	turn, out, err = iaEpisodeAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	iaEpisodeNoEffects(t, w)
}

func TestIAEpisodeAcceptRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaEpisodeAcceptFixture(t)
	q := iaEpisodeAcceptRequest(w)
	ref := iaEpisodeAcceptReference(t, q, 2, 2)
	turn, out, err := iaEpisodeAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaEpisodeNoEffects(t, w)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_directories WHERE cursor='' AND pages=2`); got != 1 {
		t.Fatal("rollback advanced the directory", got)
	}
}
