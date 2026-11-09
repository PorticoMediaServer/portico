package writer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// IA23's finish half reuses IA20's adopted C019 fixture and current-owner
// security producers, and registers the composed family registries plus its own
// finish statements so one turn sees the journal, the canonical rows and the
// recovery proof on the same snapshot. No new identity/session producer.
func iaRefreshFinishFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRefreshRequest) {
	t.Helper()
	w, r, p, q := iaRefreshFixture(t)
	registry, err := stmttest.New(t, iaRefreshFinishDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	r.Registry = registry
	return w, r, p, q
}

func iaRefreshFinalizeRequest(t *testing.T, q iaRefreshRequest, last iaRefreshOutcome) iaRefreshRequest {
	t.Helper()
	q.purpose = IARefreshFinalize
	q.action = iaEntryApply
	q.key = iaRefreshKey{}
	q.row = nil
	q.expectedRevision = last.result.revision
	q.reference.Identity.Sequence++
	q.reference.Identity.OperationID[0]++
	iaRefreshSign(t, &q)
	return q
}

func iaRefreshFinishRejects(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaRefreshRequest, life context.Context) error {
	t.Helper()
	turn, b := iaRefreshTurn(t, w, r, q)
	out, err := runIARefresh(t.Context(), turn, q, p, life, b)
	if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
		t.Fatalf("refused finish produced output: %+v %v", out, err)
	}
	turn.transaction.rollback(err)
	return err
}

func iaRefreshFinishSeededJournal(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRefreshRequest, iaRefreshOutcome) {
	t.Helper()
	w, r, p, q := iaRefreshFinishFixture(t)
	iaRefreshSeed(t, w, iaRefreshFact(10))
	iaRefreshSeed(t, w, iaRefreshStreamFixtureRow(1, "h264"))
	begin := iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	q = iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{1, "refresh-asset", 0}, iaRefreshFact(11))
	step := iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, step.result.refreshID)
	return w, r, p, q, step
}

// A complete new epoch is accepted exactly once: the first turn publishes and
// writes a kind-127 receipt, the same original replays it, and after ACK it is
// Retired with no payload and no repeated effect.
func TestIARefreshFinishAcceptsCompleteEpochAndRetires(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	fin := iaRefreshFinalizeRequest(t, q, last)
	out := iaRefreshApply(t, w, r, p, fin)
	if out.decision != receiptFresh || out.result.state != "accepted" || out.result.family != 1 ||
		out.result.key != (iaRefreshKey{1, "refresh-asset", 0}).cursor() {
		t.Fatal(out)
	}
	if got := iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`); got != 0 {
		t.Fatal("fence not cleared", got)
	}
	if got := iaRefreshCount(t, w, `SELECT info_epoch FROM catalog_assets WHERE id=41`); got != 2 {
		t.Fatal("accepted epoch changed", got)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE command_kind='refresh_finalize' AND result_state='accepted'`); got != 1 {
		t.Fatal("finalize receipt", got)
	}
	if got := iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`); got != iaRefreshCount(t, w, `SELECT retained_bytes FROM catalog_info_refresh_undo WHERE refresh_id=?`, out.result.refreshID) {
		t.Fatal("custody charge is not the retained debt", got)
	}
	replay := iaRefreshApply(t, w, r, p, fin)
	if replay.decision != receiptReplay || replay.result != out.result || replay.digest != out.digest {
		t.Fatal("original finalize replay changed", replay)
	}
	iaRefreshAck(t, w, out.result.refreshID)
	retired := iaRefreshApply(t, w, r, p, fin)
	if retired.decision != receiptRetired || retired.result != (iaRefreshResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal("retired finalize disclosed a payload", retired)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE command_kind='refresh_finalize'`); got != 1 {
		t.Fatal("retired replay repeated the effect", got)
	}
}

// Accept never relabels a later publication as this epoch, and the failed turn
// leaves the fence and the journal exactly as they were.
func TestIARefreshFinishAcceptRefusesLaterPublication(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	wc008NativeExec(t, w, `UPDATE asset_stream_facts SET fingerprint='later' WHERE asset_id='refresh-asset'`)
	fin := iaRefreshFinalizeRequest(t, q, last)
	if err := iaRefreshFinishRejects(t, w, r, p, fin, t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 1 ||
		iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE command_kind='refresh_finalize'`) != 0 {
		t.Fatal("refused accept moved the fence/receipt")
	}
	// A later publication is never clobbered by the rollback either: the journal
	// refuses and stays pending rather than overwriting the newer accepted row.
	q = iaRefreshNext(t, w, q, last, IARefreshRollback, iaRefreshKey{1, "refresh-asset", 0}, nil)
	if err := iaRefreshFinishRejects(t, w, r, p, q, t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var got string
	if err := w.db.QueryRow(`SELECT fingerprint FROM asset_stream_facts WHERE asset_id='refresh-asset'`).Scan(&got); err != nil || got != "later" {
		t.Fatal(got, err)
	}
	if iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 1 {
		t.Fatal("deferred rollback cleared the fence")
	}
}

// A source that changes after copying starts can never be accepted, but the old
// projection still restores: the retry after that is a NEW input, not a splice.
func TestIARefreshFinishSourceChangeRestoresOldProjection(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	wc008NativeExec(t, w, `UPDATE catalog_assets SET size=99,modified_ns=88 WHERE id=41`)
	fin := iaRefreshFinalizeRequest(t, q, last)
	if err := iaRefreshFinishRejects(t, w, r, p, fin, t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 1 {
		t.Fatal("refused accept cleared the fence")
	}
	q = iaRefreshNext(t, w, q, last, IARefreshRollback, iaRefreshKey{1, "refresh-asset", 0}, nil)
	restored := iaRefreshApply(t, w, r, p, q)
	if iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts`) != 10 {
		t.Fatal("old projection not restored")
	}
	iaRefreshAck(t, w, restored.result.refreshID)
	fin = iaRefreshFinalizeRequest(t, q, restored)
	out := iaRefreshApply(t, w, r, p, fin)
	if out.result.state != "restored" {
		t.Fatal(out)
	}
	if iaRefreshCount(t, w, `SELECT size FROM catalog_assets WHERE id=41`) != 99 {
		t.Fatal("restore rewrote the source witness")
	}
}

// The restore half is refused until EVERY journalled key is the old image, so a
// partial rollback can never clear the publication fence.
func TestIARefreshFinishRestoreRequiresCompleteOldProjection(t *testing.T) {
	w, r, p, q := iaRefreshFinishFixture(t)
	iaRefreshSeed(t, w, iaRefreshFact(10))
	iaRefreshSeed(t, w, iaRefreshStreamFixtureRow(1, "h264"))
	begin := iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	last := begin
	for _, s := range []struct {
		key iaRefreshKey
		row iaRefreshRecord
	}{
		{iaRefreshKey{1, "refresh-asset", 0}, iaRefreshFact(11)},
		{iaRefreshKey{2, "refresh-asset", 1}, iaRefreshStreamFixtureRow(1, "hevc")},
	} {
		q = iaRefreshNext(t, w, q, last, IARefreshStep, s.key, s.row)
		last = iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, last.result.refreshID)
	}
	q = iaRefreshNext(t, w, q, last, IARefreshRollback, iaRefreshKey{2, "refresh-asset", 1}, nil)
	last = iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, last.result.refreshID)
	fin := iaRefreshFinalizeRequest(t, q, last)
	if err := iaRefreshFinishRejects(t, w, r, p, fin, t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 1 {
		t.Fatal("partial restore cleared the fence")
	}
	q = iaRefreshNext(t, w, q, last, IARefreshRollback, iaRefreshKey{1, "refresh-asset", 0}, nil)
	last = iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, last.result.refreshID)
	fin = iaRefreshFinalizeRequest(t, q, last)
	if out := iaRefreshApply(t, w, r, p, fin); out.result.state != "restored" {
		t.Fatal(out)
	}
	if iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 0 ||
		iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts`) != 10 {
		t.Fatal("complete restore was not exact")
	}
	var codec string
	if err := w.db.QueryRow(`SELECT codec FROM asset_streams WHERE stream_index=1`).Scan(&codec); err != nil || codec != "h264" {
		t.Fatal(codec, err)
	}
}

// Every required authority cell for the accepting finalize command: forged
// issuer, stale lease, denied current source, expired authority and a cancelled
// lifetime all refuse with zero output and zero effects.
func TestIARefreshFinishAuthorityCells(t *testing.T) {
	for _, mode := range []string{"forged_issuer", "stale_lease", "source_denied", "expired", "cancelled_lifetime"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q, last := iaRefreshFinishSeededJournal(t)
			fin := iaRefreshFinalizeRequest(t, q, last)
			switch mode {
			case "forged_issuer":
				p.witness.issuer.memberIncarnation = strings.Repeat("x", 16)
			case "stale_lease":
				wc008NativeExec(t, w, `UPDATE catalog_info_changes SET job_lease_generation=2 WHERE id=51`)
			case "source_denied":
				wc008NativeExec(t, w, `UPDATE library_sources SET enabled=0 WHERE id='refresh-source'`)
			case "expired":
				p.tokenExpiry = w.now().Add(-time.Second)
			}
			life := t.Context()
			if mode == "cancelled_lifetime" {
				var cancel context.CancelFunc
				life, cancel = context.WithCancel(t.Context())
				cancel()
			}
			err := iaRefreshFinishRejects(t, w, r, p, fin, life)
			if mode == "cancelled_lifetime" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled lifetime was not observed", err)
			}
			if iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 1 ||
				iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE command_kind='refresh_finalize'`) != 0 ||
				iaRefreshCount(t, w, `SELECT used_bytes FROM catalog_info_input_budget`) != iaRefreshCount(t, w, `SELECT retained_bytes FROM catalog_info_refresh_undo`) {
				t.Fatal("denied finalize left effects")
			}
		})
	}
}

// A journal with no captured family is still a complete (empty) new epoch, and
// a finish turn never accepts a caller-named row or a reused sequence.
func TestIARefreshFinishEmptyCohortAndMisboundTurns(t *testing.T) {
	w, r, p, q := iaRefreshFinishFixture(t)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	fin := iaRefreshFinalizeRequest(t, q, begin)
	out := iaRefreshApply(t, w, r, p, fin)
	if out.result.state != "accepted" || out.result.rows != 2 || out.result.family != 0 || out.result.key != "" {
		t.Fatal(out)
	}
	// A different request at the same sequence can never be replayed as Applied.
	wrong := fin
	wrong.expectedRevision--
	iaRefreshSign(t, &wrong)
	if err := iaRefreshFinishRejects(t, w, r, p, wrong, t.Context()); err == nil {
		t.Fatal("stale expected revision was accepted")
	}
	misbound := fin
	misbound.key = iaRefreshKey{1, "refresh-asset", 0}
	misbound.reference.Identity.Sequence++
	misbound.reference.Identity.OperationID[0]++
	iaRefreshSign(t, &misbound)
	if err := iaRefreshFinishRejects(t, w, r, p, misbound, t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("caller-named finish row was accepted", err)
	}
	// A fresh finalize on the already-retired journal is refused, never reopened.
	iaRefreshAck(t, w, out.result.refreshID)
	cleanup := iaRefreshFinalizeRequest(t, fin, out)
	first := iaRefreshApply(t, w, r, p, cleanup)
	if first.result.state != "retired" {
		t.Fatal(first)
	}
	iaRefreshAck(t, w, first.result.refreshID)
	reopen := iaRefreshFinalizeRequest(t, cleanup, first)
	if err := iaRefreshFinishRejects(t, w, r, p, reopen, t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal("retired journal was reopened", err)
	}
}

// An uncertain COMMIT is never success: the accepted epoch and its receipt are
// read back through a FRESH read-only connection, never the quarantined pool.
func TestIARefreshFinishUnknownCommitReadsBackFresh(t *testing.T) {
	w, r, p, q, last := iaRefreshFinishSeededJournal(t)
	fin := iaRefreshFinalizeRequest(t, q, last)
	path := iaConfigureLibraryPath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, b := iaRefreshTurn(t, w, r, fin)
	out, err := runIARefresh(t.Context(), turn, fin, p, t.Context(), b)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown COMMIT was reported as success", result, err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var receipts, pending int
	if err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_refresh_receipts WHERE command_kind='refresh_finalize'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("fresh read lost the finalize receipt", receipts, err)
	}
	if err = db.QueryRowContext(t.Context(), `SELECT info_publication_pending FROM catalog_assets WHERE id=41`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("fresh read lost the published fence", pending, err)
	}
	if _, err = w.begin(t.Context(), IARefreshFinalize, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown commit was reused", err)
	}
}

// IA21's walk had ONE cursor for the whole refresh, so a chapter step after the
// stream step (and a stream restore after chapters) must be admitted as "this
// pair has not started". IA23 relies on that fixed semantic and proves a
// streams+chapters+subtitles round trip both when accepting and when restoring.
func TestIARefreshFinishComposesEveryFamilyPair(t *testing.T) {
	for _, mode := range []string{"accept", "restore"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaRefreshFinishFixture(t)
			iaRefreshSeed(t, w, iaRefreshFact(10))
			iaRefreshSeed(t, w, iaRefreshStreamFixtureRow(1, "h264"))
			iaRefreshSeed(t, w, iaRefreshChapterFactRow{AssetID: "refresh-asset", Revision: 10, Size: 42, ModifiedNS: 11, Status: "ready"})
			iaRefreshSeed(t, w, iaRefreshChapterRow{AssetID: "refresh-asset", ChapterIndex: 1, Title: "one", EndSeconds: 2})
			iaRefreshSeed(t, w, iaRefreshSubtitleFactRow{AssetID: "refresh-asset", Revision: 10, Size: 42, ModifiedNS: 11, Status: "ready"})
			iaRefreshSeed(t, w, iaRefreshSubtitleRow{ID: "sub", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt", Language: "en", Title: "old"})
			begin := iaRefreshApply(t, w, r, p, q)
			if begin.result.state != "copying" {
				t.Fatal(begin)
			}
			q.refreshID = begin.result.refreshID
			iaRefreshAck(t, w, begin.result.refreshID)
			last := begin
			streamDesired := iaRefreshStreamDesired{
				Fact: &iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 11, Fingerprint: "new", DetailVersion: 1},
				Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(1, "hevc"), iaRefreshStreamFixtureRow(2, "av1")},
			}
			for {
				step, err := iaRefreshRawStreamStep(t, w, r, q, streamDesired)
				if err != nil {
					t.Fatal(err)
				}
				if !step.more {
					break
				}
				q = iaRefreshStepRequest(t, q, IARefreshStep, step)
				last = iaRefreshApply(t, w, r, p, q)
				iaRefreshAck(t, w, last.result.refreshID)
			}
			chapterDesired := iaRefreshChapterDesired{
				Fact: &iaRefreshChapterFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 11, Status: "ready"},
				Rows: []iaRefreshChapterRow{{AssetID: "refresh-asset", ChapterIndex: 2, Title: "two", EndSeconds: 3}},
			}
			for {
				step, err := iaRefreshRawChapterStep(t, w, r, q, chapterDesired)
				if err != nil {
					t.Fatal(err)
				}
				if !step.more {
					break
				}
				q = iaRefreshStepRequest(t, q, IARefreshStep, step)
				last = iaRefreshApply(t, w, r, p, q)
				iaRefreshAck(t, w, last.result.refreshID)
			}
			newSub := iaRefreshSubtitleRow{ID: "sub", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt", Language: "en", Title: "new"}
			for _, s := range []struct {
				key iaRefreshKey
				row iaRefreshRecord
			}{
				{iaRefreshKey{5, "refresh-asset", 0}, iaRefreshSubtitleFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 11, Status: "ready"}},
				{iaRefreshKey{7, "sub", 0}, newSub},
			} {
				q = iaRefreshNext(t, w, q, last, IARefreshStep, s.key, s.row)
				last = iaRefreshApply(t, w, r, p, q)
				iaRefreshAck(t, w, last.result.refreshID)
			}
			if mode == "accept" {
				fin := iaRefreshFinalizeRequest(t, q, last)
				out := iaRefreshApply(t, w, r, p, fin)
				if out.result.state != "accepted" || out.result.family != 7 || iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 0 {
					t.Fatal(out)
				}
				return
			}
			// Restore in reverse dependency order: subtitles, chapters, streams.
			for _, key := range []iaRefreshKey{{7, "sub", 0}, {5, "refresh-asset", 0}} {
				q = iaRefreshNext(t, w, q, last, IARefreshRollback, key, nil)
				last = iaRefreshApply(t, w, r, p, q)
				iaRefreshAck(t, w, last.result.refreshID)
			}
			for {
				step, err := iaRefreshRawChapterRestore(t, w, r, q)
				if err != nil {
					t.Fatal(err)
				}
				if !step.more {
					break
				}
				q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
				last = iaRefreshApply(t, w, r, p, q)
				iaRefreshAck(t, w, last.result.refreshID)
			}
			for {
				step, err := iaRefreshRawStreamRestore(t, w, r, q)
				if err != nil {
					t.Fatal(err)
				}
				if !step.more {
					break
				}
				q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
				last = iaRefreshApply(t, w, r, p, q)
				iaRefreshAck(t, w, last.result.refreshID)
			}
			fin := iaRefreshFinalizeRequest(t, q, last)
			out := iaRefreshApply(t, w, r, p, fin)
			if out.result.state != "restored" || iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`) != 0 {
				t.Fatal(out)
			}
			var title string
			if err := w.db.QueryRow(`SELECT title FROM asset_subtitles WHERE id='sub'`).Scan(&title); err != nil || title != "old" {
				t.Fatal(title, err)
			}
			if err := w.db.QueryRow(`SELECT title FROM asset_chapters WHERE chapter_index=1`).Scan(&title); err != nil || title != "one" {
				t.Fatal(title, err)
			}
			var codec string
			if err := w.db.QueryRow(`SELECT codec FROM asset_streams WHERE stream_index=1`).Scan(&codec); err != nil || codec != "h264" {
				t.Fatal(codec, err)
			}
		})
	}
}
