package writer

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgLCFixture(t *testing.T) (*executor, mgLCBuild) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	b := mgLCBuild{channel: "lc-channel", generation: "lc-new", base: "lc-old", config: `{"id":"lc-channel"}`, seed: "fixed-seed", lease: "lease-original", revision: 7, start: 1000, end: 61000, leaseUntil: time.Now().Add(time.Hour).UnixMilli(), libraries: []mgLCLibraryWitness{{id: "lc-lib", revision: 11, order: 1, metadata: 1, origin: 1, catalogue: 101, incarnation: [16]byte{7}}}}
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root,data_revision,playback_origin_incarnation) VALUES('lc-lib','Library','movies','/fixture/lc',11,X'07000000000000000000000000000000')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(101,'lc-lib','/fixture/lc')`,
		`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,active_generation,state,generated_through_ms) VALUES('lc-channel',7,'{"id":"lc-channel"}',1,0,'Channel','lc-old','healthy',61000)`,
		`INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES('lc-old','lc-channel',7,'{}','lc-lib:10','old','published','published','',1000,61000,1000,61000,1)`,
		`INSERT INTO lc_entries VALUES('lc-old','old-occurrence','lc-channel',1000,61000,0,'','','','Old slate','rule','default',0,'no-eligible-media')`,
		`INSERT INTO lc_playback_refs VALUES('active-player','lc-channel','lc-old','old-occurrence',61000,1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	return w, b
}
func mgLCTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	// Synthetic admission only. Reserved MG descriptors are intentionally NOT
	// installed in the shared dispatcher; this fixture uses an existing frame.
	recipe := turnRecipe{limit: Work{10000, 10000000, 10000, 64 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), InventoryAccept, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	registry, err := stmttest.New(t, mgLCDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range mgLCDefinitions() {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func mgLCCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func mgLCCandidateFixture() mgLCCandidate {
	return mgLCCandidate{rule: "rule", item: 101, ordinal: 0, library: "lc-lib", asset: "asset-101", source: "source-observation", title: "Immutable title", duration: 30000, weight: 1, sort: "title:101"}
}
func mgLCStartFixture(t *testing.T, w *executor, b mgLCBuild, withCandidate bool) mgLCProgress {
	t.Helper()
	turn := mgLCTurn(t, w)
	p, err := beginMGLC(t.Context(), turn, b)
	if err != nil {
		t.Fatal(err)
	}
	if withCandidate {
		p, err = stageMGLCCandidates(t.Context(), turn, b, p, []mgLCCandidate{mgLCCandidateFixture()}, "id:101", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	mgLCCommit(t, turn)
	return p
}
func mgLCCompleteFixture(t *testing.T, w *executor, b mgLCBuild, withCandidate bool) mgLCProgress {
	t.Helper()
	p := mgLCStartFixture(t, w, b, withCandidate)
	turn := mgLCTurn(t, w)
	p, err := finishMGLCCandidates(t.Context(), turn, b, p, p.candidates)
	if err != nil {
		t.Fatal(err)
	}
	item := int64(0)
	if withCandidate {
		item = 101
	}
	p, err = stageMGLCEntries(t.Context(), turn, b, p, []mgLCSlot{{"first", "rule", "default", item, 1000, 31000}, {"second", "rule", "default", 0, 31000, 61000}})
	if err != nil {
		t.Fatal(err)
	}
	mgLCCommit(t, turn)
	return p
}
func TestMGLCNativeStagingPublicationRetainsOldGeneration(t *testing.T) {
	w, b := mgLCFixture(t)
	p := mgLCStartFixture(t, w, b, true)
	var head string
	if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation FROM lc_channels WHERE id=?`, b.channel).Scan(&head); err != nil || head != b.base {
		t.Fatal(head, err)
	}
	turn := mgLCTurn(t, w)
	p, err := finishMGLCCandidates(t.Context(), turn, b, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err = stageMGLCEntries(t.Context(), turn, b, p, []mgLCSlot{{"first", "rule", "default", 101, 1000, 31000}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishMGLC(t.Context(), turn, b, p); !errors.Is(err, ErrConflict) {
		t.Fatal("partial timeline published", err)
	}
	// Refusal above is read-only: the actual accepting frame would roll back its
	// command. Keep this fixture's committed prefix in a separate transaction.
	mgLCCommit(t, turn)
	turn = mgLCTurn(t, w)
	p, err = stageMGLCEntries(t.Context(), turn, b, p, []mgLCSlot{{"second", "rule", "default", 0, 31000, 61000}})
	if err != nil {
		t.Fatal(err)
	}
	mgLCCommit(t, turn)
	if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation FROM lc_channels WHERE id=?`, b.channel).Scan(&head); err != nil || head != b.base {
		t.Fatal("head changed before publication", head, err)
	}
	turn = mgLCTurn(t, w)
	got, err := publishMGLC(t.Context(), turn, b, p)
	if err != nil || got.generation != b.generation || got.through != 61000 || got.candidates != 1 {
		t.Fatal(got, err)
	}
	mgLCCommit(t, turn)
	var oldRows, pins, entries int
	var status, token, state, title, source, asset string
	var until, revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,revision,state,(SELECT count(*) FROM lc_entries WHERE generation_id='lc-old'),(SELECT count(*) FROM lc_playback_refs WHERE generation_id='lc-old'),(SELECT count(*) FROM lc_entries WHERE generation_id='lc-new') FROM lc_channels WHERE id=?`, b.channel).Scan(&head, &revision, &state, &oldRows, &pins, &entries); err != nil {
		t.Fatal(err)
	}
	if head != b.generation || revision != 7 || state != "healthy" || oldRows != 1 || pins != 1 || entries != 2 {
		t.Fatal(head, revision, state, oldRows, pins, entries)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT status,lease_token,lease_until_ms FROM lc_generations WHERE id=?`, b.generation).Scan(&status, &token, &until); err != nil || status != "published" || token != "" || until != 0 {
		t.Fatal(status, token, until, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT title,source_fence,asset_id FROM lc_entries WHERE generation_id=? AND occurrence_id='first'`, b.generation).Scan(&title, &source, &asset); err != nil || title != "Immutable title" || source != "source-observation" || asset != "asset-101" {
		t.Fatal(title, source, asset, err)
	}
	turn = mgLCTurn(t, w)
	if _, err := stageMGLCCandidates(t.Context(), turn, b, p, []mgLCCandidate{mgLCCandidateFixture()}, "later", 1, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("published candidates mutated", err)
	}
}
func TestMGLCPublicationFences(t *testing.T) {
	for name, q := range map[string]string{
		"config":             `UPDATE lc_channels SET revision=8 WHERE id='lc-channel'`,
		"same-revision-base": `UPDATE lc_channels SET active_generation='other-published' WHERE id='lc-channel'`,
		"lease-token":        `UPDATE lc_generations SET lease_token='successor' WHERE id='lc-new'`,
		"lease-deadline":     `UPDATE lc_generations SET lease_until_ms=lease_until_ms+1 WHERE id='lc-new'`,
		"catalogue":          `UPDATE libraries SET data_revision=12 WHERE id='lc-lib'`,
		"library-retired":    `UPDATE catalog_libraries SET retired=1 WHERE id=101`,
		"history":            `UPDATE lc_generations SET play_history_fence=1 WHERE id='lc-new'`,
		"metadata":           `UPDATE libraries SET data_metadata_revision=2 WHERE id='lc-lib'`,
		"order":              `UPDATE libraries SET data_order_revision=2 WHERE id='lc-lib'`,
		"origin-incarnation": `UPDATE libraries SET playback_origin_incarnation=zeroblob(16) WHERE id='lc-lib'`,
		"disabled":           `UPDATE lc_channels SET enabled=0 WHERE id='lc-channel'`,
		"removed":            `UPDATE lc_channels SET removed=1 WHERE id='lc-channel'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, b := mgLCFixture(t)
			p := mgLCCompleteFixture(t, w, b, true)
			if _, err := w.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			turn := mgLCTurn(t, w)
			got, err := publishMGLC(t.Context(), turn, b, p)
			if !errors.Is(err, ErrConflict) || got != (mgLCPublication{}) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestMGLCPartialPublicationRequiresWholeRollback(t *testing.T) {
	w, b := mgLCFixture(t)
	p := mgLCCompleteFixture(t, w, b, true)
	turn := mgLCTurn(t, w)
	// Fail the second publication statement's resource reservation AFTER the
	// native head update, exercising the real partial-effect rollback boundary.
	step := turn.steps[mgLCGenerationPublish]
	step.work.Rows = math.MaxUint64
	turn.steps[mgLCGenerationPublish] = step
	if got, err := publishMGLC(t.Context(), turn, b, p); !errors.Is(err, ErrQuantum) || got != (mgLCPublication{}) {
		t.Fatal(got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var head, status string
	if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,(SELECT status FROM lc_generations WHERE id='lc-new') FROM lc_channels WHERE id='lc-channel'`).Scan(&head, &status); err != nil || head != b.base || status != "pending" {
		t.Fatal("partial publish survived", head, status, err)
	}
}
func TestMGLCStageRejectsHolesAndRollsBackDuplicates(t *testing.T) {
	w, b := mgLCFixture(t)
	p := mgLCStartFixture(t, w, b, true)
	turn := mgLCTurn(t, w)
	second := mgLCCandidateFixture()
	second.item = 102
	second.ordinal = 1
	duplicate := second
	duplicate.ordinal = 2
	duplicate.title = "changed duplicate"
	if _, err := stageMGLCCandidates(t.Context(), turn, b, p, []mgLCCandidate{second, duplicate}, "id:102", 2, 0); err == nil {
		t.Fatal("duplicate body accepted")
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count, total int
	if err := w.db.QueryRowContext(t.Context(), `SELECT candidate_count,(SELECT count(*) FROM lc_candidates WHERE generation_id='lc-new') FROM lc_generations WHERE id='lc-new'`).Scan(&count, &total); err != nil || count != 1 || total != 1 {
		t.Fatal(count, total, err)
	}
	turn = mgLCTurn(t, w)
	second.ordinal = 3
	if _, err := stageMGLCCandidates(t.Context(), turn, b, p, []mgLCCandidate{second}, "id:102", 1, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("ordinal hole accepted", err)
	}
	if _, err := finishMGLCCandidates(t.Context(), turn, b, p, 2); !errors.Is(err, ErrConflict) {
		t.Fatal("incomplete candidate count accepted", err)
	}
	p, err := finishMGLCCandidates(t.Context(), turn, b, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []mgLCSlot{{"gap", "rule", "default", 101, 2000, 31000}, {"too-long", "rule", "default", 101, 1000, 40000}, {"missing", "rule", "default", 999, 1000, 31000}} {
		if _, err := stageMGLCEntries(t.Context(), turn, b, p, []mgLCSlot{s}); err == nil {
			t.Fatal("bad slot accepted", s)
		}
	}
}
func TestMGLCSlateAndLeaseExpiry(t *testing.T) {
	t.Run("slate", func(t *testing.T) {
		w, b := mgLCFixture(t)
		p := mgLCCompleteFixture(t, w, b, false)
		turn := mgLCTurn(t, w)
		if _, err := publishMGLC(t.Context(), turn, b, p); err != nil {
			t.Fatal(err)
		}
		mgLCCommit(t, turn)
		var state, health string
		if err := w.db.QueryRowContext(t.Context(), `SELECT state,health_code FROM lc_channels WHERE id=?`, b.channel).Scan(&state, &health); err != nil || state != "no-eligible-media" || health != state {
			t.Fatal(state, health, err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		w, b := mgLCFixture(t)
		p := mgLCCompleteFixture(t, w, b, false)
		turn := mgLCTurn(t, w)
		w.now = func() time.Time { return time.UnixMilli(b.leaseUntil) }
		if got, err := publishMGLC(t.Context(), turn, b, p); !errors.Is(err, ErrConflict) || got != (mgLCPublication{}) {
			t.Fatal("expired lease published", got, err)
		}
		// The synthetic one-hour clock jump also exceeds this turn's hold. Its
		// rollback must retire/fence the frame; never reuse that writer afterward.
		if _, err := turn.transaction.rollback(nil); !errors.Is(err, ErrOverrun) {
			t.Fatal(err)
		}
	})
}
func TestMGLCCanceledAndBoundedInputs(t *testing.T) {
	w, b := mgLCFixture(t)
	turn := mgLCTurn(t, w)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := beginMGLC(ctx, turn, b); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	p := mgLCStartFixture(t, w, b, false)
	turn = mgLCTurn(t, w)
	c := mgLCCandidateFixture()
	c.title = strings.Repeat("x", mgLCBytes)
	if _, err := stageMGLCCandidates(t.Context(), turn, b, p, []mgLCCandidate{c}, "next", 1, 0); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	// Empty eligible page still advances the inspected-row keyset.
	p, err := stageMGLCCandidates(t.Context(), turn, b, p, nil, "filtered:8", 8, 0)
	if err != nil || p.candidates != 0 {
		t.Fatal(p, err)
	}
	if len(mgLCDescriptors()) != 5 {
		t.Fatal("descriptor inventory")
	}
	if _, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: mgLCDescriptors()}); err != nil {
		t.Fatal(err)
	}
}

func TestMGLCCivilWindowAndImmutableHistoryCutoff(t *testing.T) {
	w, b := mgLCFixture(t)
	window := b
	window.end = window.start + 169*60*60*1000
	if !window.valid() {
		t.Fatal("seven civil days across DST fallback rejected")
	}
	p := mgLCCompleteFixture(t, w, b, true)
	// A later real play must not move the generation's frozen least-played
	// cutoff. Fixture-only catalogue writes satisfy the retained history FK.
	for _, q := range []string{

		`INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(101,zeroblob(16),101,1)`,
		`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(101,zeroblob(16),101,1,'Item','item','i','item','i',0,zeroblob(16),zeroblob(16))`,
		`INSERT INTO lc_play_events(channel_id,occurrence_start_ms,item_id) VALUES('lc-channel',1000,101)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	turn := mgLCTurn(t, w)
	if _, err := publishMGLC(t.Context(), turn, b, p); err != nil {
		t.Fatal("later play changed immutable cutoff", err)
	}
	mgLCCommit(t, turn)
}
