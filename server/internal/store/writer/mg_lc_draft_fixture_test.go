package writer

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const mgLCDraftSHA = "4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262"

var mgLCDraftTemplate struct {
	sync.Once
	image []byte
	err   error
}

// No canonical schema edits, copied DDL packet or absolute owner checkout in
// source. Parent runners supply this exact candidate as a pinned input. All DB
// scratch uses testing's TMPDIR and is closed/removed before task completion.
func mgLCDraftExecutor(t *testing.T) (*executor, string) {
	t.Helper()
	candidate := os.Getenv("MGLC_DRAFT_CANDIDATE")
	if candidate == "" {
		t.Skip("set MGLC_DRAFT_CANDIDATE to the exact MG-001 owner candidate " + mgLCDraftSHA)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	open := func(path string) (*executor, error) {
		connector, err := sqliteconn.Connector(path, cfg)
		if err != nil {
			return nil, err
		}
		w := &executor{config: cfg, now: time.Now}
		w.openPool(connector, nil)
		return w, nil
	}
	mgLCDraftTemplate.Do(func() {
		data, err := os.ReadFile(candidate)
		if err != nil {
			mgLCDraftTemplate.err = err
			return
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != mgLCDraftSHA {
			mgLCDraftTemplate.err = fmt.Errorf("LC candidate SHA mismatch")
			return
		}
		dir, err := os.MkdirTemp(os.Getenv("TMPDIR"), "mg-lc-template-")
		if err != nil {
			mgLCDraftTemplate.err = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "draft.db")
		w, err := open(path)
		if err != nil {
			mgLCDraftTemplate.err = err
			return
		}
		if _, err = w.db.ExecContext(t.Context(), string(data)); err == nil {
			_, err = w.db.ExecContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`)
		}
		closeErr := w.close()
		if err != nil {
			mgLCDraftTemplate.err = err
			return
		}
		if closeErr != nil {
			mgLCDraftTemplate.err = closeErr
			return
		}
		mgLCDraftTemplate.image, mgLCDraftTemplate.err = os.ReadFile(path)
	})
	if mgLCDraftTemplate.err != nil {
		t.Fatal(mgLCDraftTemplate.err)
	}
	path := filepath.Join(t.TempDir(), "lc-draft.db")
	if err := os.WriteFile(path, mgLCDraftTemplate.image, 0600); err != nil {
		t.Fatal(err)
	}
	w, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !w.closed {
			if err := w.close(); err != nil {
				t.Error(err)
			}
		}
	})
	return w, path
}
func mgLCFixture(t *testing.T) (*executor, mgLCBuild) {
	t.Helper()
	w, _ := mgLCDraftExecutor(t)
	b := mgLCBuild{channel: "lc-channel", generation: "lc-new", base: "lc-old", config: `{"id":"lc-channel"}`, seed: "fixed-seed", lease: "lease-original", revision: 7, start: 1000, end: 61000, leaseUntil: time.Now().Add(time.Hour).UnixMilli(), incarnation: [16]byte{11}, create: [16]byte{12}, birth: RequestDigest{13}, libraries: []mgLCLibraryWitness{{id: "lc-lib", revision: 11, order: 1, metadata: 1, origin: 1, catalogue: 101, incarnation: [16]byte{7}}}}
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root,data_revision,playback_origin_incarnation) VALUES('lc-lib','Library','movies','/fixture/lc',11,X'07000000000000000000000000000000')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(101,'lc-lib','/fixture/lc')`,
		`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('lc-channel',7,'{"id":"lc-channel"}',1,0,'Channel','healthy')`,
		`INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms,command_incarnation,create_operation_id,birth_request_digest,entry_digest,verify_end_ms,verified_entry_digest) VALUES('lc-old','lc-channel',7,'{}','old-fence','old','published','published','',1000,61000,1000,61000,1,zeroblob(16),zeroblob(16),zeroblob(32),zeroblob(32),1000,zeroblob(32))`,
		`UPDATE lc_channels SET active_generation='lc-old',generated_through_ms=61000 WHERE id='lc-channel'`,
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
	// Unqualified unit admission, not registry aggregation or a command profile.
	registry, err := stmttest.New(t, mgLCDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{10000, 10000000, 10000, 64 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), InventoryAccept, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_, _ = tx.rollback(nil)
		}
	})
	var steps []statementStep
	for _, d := range mgLCDefinitions() {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 64, 1 << 20, time.Microsecond}})
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
func mgLCNext(b mgLCBuild, p mgLCProgress) mgLCCommand {
	if p.last == 0 {
		return mgLCCommand{CommandIdentity{b.incarnation, 1, b.create}, b.birth}
	}
	return mgLCCommand{CommandIdentity{b.incarnation, p.last + 1, [16]byte{99, byte(p.last + 1)}}, RequestDigest{88, byte(p.last + 1)}}
}
func mgLCTestSave(t *testing.T, turn *commandTurn, b mgLCBuild, c mgLCCommand, k mgLCKind, p mgLCProgress) mgLCSaved {
	t.Helper()
	digest := ResultDigest(sha256.Sum256([]byte(fmt.Sprintf("fixture-codec-only:%#v", mgLCResult(p)))))
	s, err := saveMGLCReceipt(t.Context(), turn, b, c, k, p, digest)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func mgLCTestAck(t *testing.T, w *executor, b mgLCBuild, s mgLCSaved, p mgLCProgress) mgLCProgress {
	t.Helper()
	turn := mgLCTurn(t, w)
	ack, err := acknowledgeMGLC(t.Context(), turn, b, s.command, s.kind, s.digest)
	if err != nil {
		t.Fatal(err)
	}
	mgLCCommit(t, turn)
	p.ack = ack
	return p
}
func mgLCBeginFixture(t *testing.T, w *executor, b mgLCBuild) mgLCProgress {
	t.Helper()
	turn := mgLCTurn(t, w)
	c := mgLCNext(b, mgLCProgress{})
	p, err := beginMGLC(t.Context(), turn, b, c)
	if err != nil {
		t.Fatal(err)
	}
	s := mgLCTestSave(t, turn, b, c, mgLCBeginKind, p)
	mgLCCommit(t, turn)
	return mgLCTestAck(t, w, b, s, p)
}
func mgLCCandidateFixture() mgLCCandidate {
	return mgLCCandidate{rule: "rule", item: 101, ordinal: 0, library: "lc-lib", asset: "asset-101", source: "source-observation", title: "Immutable title", duration: 30000, weight: 1, sort: "title:101"}
}
func mgLCStartFixture(t *testing.T, w *executor, b mgLCBuild, media bool) mgLCProgress {
	t.Helper()
	p := mgLCBeginFixture(t, w, b)
	if media {
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, p)
		next, err := stageMGLCCandidates(t.Context(), turn, b, c, p, []mgLCCandidate{mgLCCandidateFixture()}, "id:101", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		s := mgLCTestSave(t, turn, b, c, mgLCCandidatesKind, next)
		mgLCCommit(t, turn)
		p = mgLCTestAck(t, w, b, s, next)
	}
	turn := mgLCTurn(t, w)
	c := mgLCNext(b, p)
	next, err := finishMGLCCandidates(t.Context(), turn, b, c, p)
	if err != nil {
		t.Fatal(err)
	}
	s := mgLCTestSave(t, turn, b, c, mgLCCandidatesKind, next)
	mgLCCommit(t, turn)
	return mgLCTestAck(t, w, b, s, next)
}
func mgLCEntriesFixture(t *testing.T, w *executor, b mgLCBuild, media bool) mgLCProgress {
	t.Helper()
	p := mgLCStartFixture(t, w, b, media)
	turn := mgLCTurn(t, w)
	c := mgLCNext(b, p)
	item := int64(0)
	if media {
		item = 101
	}
	next, err := stageMGLCEntries(t.Context(), turn, b, c, p, []mgLCSlot{{"first", "rule", "default", item, 1000, 31000}, {"second", "rule", "default", 0, 31000, 61000}})
	if err != nil {
		t.Fatal(err)
	}
	s := mgLCTestSave(t, turn, b, c, mgLCEntriesKind, next)
	mgLCCommit(t, turn)
	return mgLCTestAck(t, w, b, s, next)
}
func mgLCVerifiedFixture(t *testing.T, w *executor, b mgLCBuild, p mgLCProgress) mgLCProgress {
	t.Helper()
	for n := 0; n < 4 && p.phase == mgLCCompletePhase; n++ {
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, p)
		next, err := verifyMGLCEntries(t.Context(), turn, b, c, p)
		if err != nil {
			t.Fatal(err)
		}
		s := mgLCTestSave(t, turn, b, c, mgLCVerifyKind, next)
		mgLCCommit(t, turn)
		p = mgLCTestAck(t, w, b, s, next)
	}
	if p.phase != mgLCVerifiedPhase {
		t.Fatal("verification incomplete", p)
	}
	return p
}
func mgLCSealedFixture(t *testing.T, w *executor, b mgLCBuild, media bool) mgLCProgress {
	t.Helper()
	p := mgLCVerifiedFixture(t, w, b, mgLCEntriesFixture(t, w, b, media))
	turn := mgLCTurn(t, w)
	c := mgLCNext(b, p)
	next, err := sealMGLC(t.Context(), turn, b, c, p)
	if err != nil {
		t.Fatal(err)
	}
	s := mgLCTestSave(t, turn, b, c, mgLCSealKind, next)
	mgLCCommit(t, turn)
	return mgLCTestAck(t, w, b, s, next)
}
