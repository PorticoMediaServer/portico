package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/bookseries"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// The accepted local pair publishes with the accepted audiobook source: one
// exact book/source/info-epoch/policy head and the complete ordered cohort.
func TestIABookSeriesAcceptPublishesAcceptedPairs(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	bookID := iaBookSeriesBookID(t, w, "book-key")
	head := iaBookSeriesHeadRow(t, w, bookID)
	wantCount, wantBytes, wantDigest, err := bookseries.Cohort([]bookseries.Pair{{Name: "Saga", Position: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if head.libraryID != iaBookTestLibraryID || head.bookKind != 8 || head.assetID != iaBookTestAssetID ||
		head.infoEpoch != 1 || head.sourceRevision != "book-token" || head.policyRevision != 4 ||
		head.revision != 1 || head.count != wantCount || head.bytes != wantBytes || head.digest != wantDigest {
		t.Fatal("published head", head)
	}
	var name, position string
	var ordinal int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT ordinal,series_name,series_position FROM catalog_book_series_members WHERE book_id=? ORDER BY ordinal`, bookID).Scan(&ordinal, &name, &position); err != nil {
		t.Fatal(err)
	}
	if ordinal != 0 || name != "Saga" || position != "1" {
		t.Fatal("published member", ordinal, name, position)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_book_series_heads`); got != 1 {
		t.Fatal("head count", got)
	}
}

// Only accepted pairs publish. A book accepted without a series writes no head,
// and a raw scalar on the entity row is never a fallback projection.
func TestIABookSeriesAcceptUnacceptedTagsInvisible(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	q.series, q.seriesPosition = "", ""
	iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_book_series_heads`); got != 0 {
		t.Fatal("accepted empty cohort minted a head", got)
	}
	bookID := iaBookSeriesBookID(t, w, "book-key")
	wc008NativeExec(t, w, `UPDATE catalog_entities SET series='Raw Tag',series_position='7' WHERE id=?`, bookID)
	turn, _ := iaBookAcceptTurn(t, w, reg, q)
	if err := iaBookSeriesPublish(t.Context(), turn, iaBookTestLibraryID, bookID,
		iaBookAssetFacts{id: iaBookTestAssetID, infoEpoch: 1, token: "book-token"}, 4, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_book_series_heads`); got != 0 {
		t.Fatal("raw entity tag became a projection", got)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_book_series_members`); got != 0 {
		t.Fatal("raw entity tag minted members", got)
	}
}

// A newer accepted info epoch or policy replaces the complete old cohort,
// including the accepted empty cohort, and a stale witness is refused.
func TestIABookSeriesAcceptReplacesCompleteCohortOnNewEpoch(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	bookID := iaBookSeriesBookID(t, w, "book-key")
	pairs := []bookseries.Pair{{Name: "Saga", Position: "1"}, {Name: "Émile", Position: ""}, {Name: "Ünïcode", Position: "2.5"}}
	wc008NativeExec(t, w, `UPDATE catalog_assets SET info_epoch=2 WHERE id=?`, iaBookTestAssetID)
	iaBookSeriesDirectPublish(t, w, reg, q, bookID, 2, 4, pairs)
	count, bytes, digest, err := bookseries.Cohort(pairs)
	if err != nil {
		t.Fatal(err)
	}
	head := iaBookSeriesHeadRow(t, w, bookID)
	if head.revision != 2 || head.infoEpoch != 2 || head.count != count || head.bytes != bytes || head.digest != digest {
		t.Fatal("replaced head", head)
	}
	rows, err := w.db.QueryContext(t.Context(), `SELECT ordinal,series_name,series_position FROM catalog_book_series_members WHERE book_id=? ORDER BY ordinal`, bookID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []bookseries.Pair
	for rows.Next() {
		var ordinal int64
		var pair bookseries.Pair
		if err = rows.Scan(&ordinal, &pair.Name, &pair.Position); err != nil {
			t.Fatal(err)
		}
		if ordinal != int64(len(got)) {
			t.Fatal("non-contiguous ordinal", ordinal)
		}
		got = append(got, pair)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(pairs) {
		t.Fatal("member cohort", got)
	}
	for i := range pairs {
		if got[i] != pairs[i] {
			t.Fatal("member pair", i, got[i], pairs[i])
		}
	}
	// The accepted empty cohort invalidates the old projection without losing
	// the head's source witness.
	wc008NativeExec(t, w, `UPDATE catalog_assets SET info_epoch=3 WHERE id=?`, iaBookTestAssetID)
	iaBookSeriesDirectPublish(t, w, reg, q, bookID, 3, 4, nil)
	head = iaBookSeriesHeadRow(t, w, bookID)
	if head.revision != 3 || head.infoEpoch != 3 || head.count != 0 || head.bytes != 0 || head.digest != bookseries.EmptyDigest() {
		t.Fatal("empty accepted cohort", head)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_book_series_members WHERE book_id=?`, bookID); got != 0 {
		t.Fatal("empty cohort kept members", got)
	}
	// A stale accepted witness never rewrites the newer projection.
	turn, _ := iaBookAcceptTurn(t, w, reg, q)
	err = iaBookSeriesPublish(t.Context(), turn, iaBookTestLibraryID, bookID,
		iaBookAssetFacts{id: iaBookTestAssetID, infoEpoch: 2, token: "book-token"}, 4, pairs)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale epoch", err)
	}
	turn.transaction.rollback(nil)
	head = iaBookSeriesHeadRow(t, w, bookID)
	if head.revision != 3 || head.infoEpoch != 3 {
		t.Fatal("stale witness rewrote the head", head)
	}
}

// A divergent cohort in the same accepted epoch is contradictory evidence, not
// an update; a newer policy is required to change the projection.
func TestIABookSeriesAcceptRefusesDivergentSameEpoch(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	bookID := iaBookSeriesBookID(t, w, "book-key")
	turn, _ := iaBookAcceptTurn(t, w, reg, q)
	err := iaBookSeriesPublish(t.Context(), turn, iaBookTestLibraryID, bookID,
		iaBookAssetFacts{id: iaBookTestAssetID, infoEpoch: 1, token: "book-token"}, 4,
		[]bookseries.Pair{{Name: "Other", Position: "2"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("divergent same-epoch cohort", err)
	}
	turn.transaction.rollback(nil)
	// An older local policy is a stale witness even with the exact cohort.
	turn, _ = iaBookAcceptTurn(t, w, reg, q)
	err = iaBookSeriesPublish(t.Context(), turn, iaBookTestLibraryID, bookID,
		iaBookAssetFacts{id: iaBookTestAssetID, infoEpoch: 1, token: "book-token"}, 3,
		[]bookseries.Pair{{Name: "Saga", Position: "1"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale policy", err)
	}
	turn.transaction.rollback(nil)
	head := iaBookSeriesHeadRow(t, w, bookID)
	if head.revision != 1 || head.policyRevision != 4 || head.count != 1 {
		t.Fatal("refused publish changed the head", head)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_book_series_members WHERE book_id=?`, bookID); got != 1 {
		t.Fatal("refused publish changed members", got)
	}
}

// The exact complete cohort already published is idempotent: a retry or a
// second accepted file for the same book does not bump the head revision.
func TestIABookSeriesAcceptIdempotentSameCohort(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	bookID := iaBookSeriesBookID(t, w, "book-key")
	iaBookSeriesDirectPublish(t, w, reg, q, bookID, 1, 4, []bookseries.Pair{{Name: "Saga", Position: "1"}})
	head := iaBookSeriesHeadRow(t, w, bookID)
	if head.revision != 1 || head.count != 1 {
		t.Fatal("idempotent publish bumped the head", head)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_book_series_members WHERE book_id=?`, bookID); got != 1 {
		t.Fatal("idempotent publish duplicated members", got)
	}
}

// An empty position is exact: it is retained as an empty string, not NULL and
// not a synthesized zero.
func TestIABookSeriesAcceptEmptyPositionExact(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	q.series, q.seriesPosition = "Saga", ""
	iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	bookID := iaBookSeriesBookID(t, w, "book-key")
	var position string
	var nulls int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT series_position,series_position IS NULL FROM catalog_book_series_members WHERE book_id=?`, bookID).Scan(&position, &nulls); err != nil {
		t.Fatal(err)
	}
	if position != "" || nulls != 0 {
		t.Fatal("empty position", position, nulls)
	}
}

// Every projection bound refuses the complete unit before the first write.
func TestIABookSeriesAcceptBounds(t *testing.T) {
	pairs := make([]bookseries.Pair, bookseries.MaxPairs+1)
	for i := range pairs {
		pairs[i] = bookseries.Pair{Name: "series-" + string(rune('a'+i))}
	}
	for name, tc := range map[string]struct {
		pairs []bookseries.Pair
		cap   bool
	}{
		"too_many":      {pairs, true},
		"blank_name":    {[]bookseries.Pair{{Name: "  "}}, false},
		"nul_name":      {[]bookseries.Pair{{Name: "Sa\x00ga"}}, false},
		"invalid_utf8":  {[]bookseries.Pair{{Name: "Sa\xffga"}}, false},
		"overlong_name": {[]bookseries.Pair{{Name: repeatByte('a', bookseries.MaxNameRunes+1)}}, true},
		"duplicate":     {[]bookseries.Pair{{Name: "Saga"}, {Name: "Saga", Position: "2"}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaBookAcceptFixture(t)
			q := iaBookAcceptRequest(w)
			iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
			bookID := iaBookSeriesBookID(t, w, "book-key")
			turn, _ := iaBookAcceptTurn(t, w, reg, q)
			err := iaBookSeriesPublish(t.Context(), turn, iaBookTestLibraryID, bookID,
				iaBookAssetFacts{id: iaBookTestAssetID, infoEpoch: 2, token: "book-token"}, 4, tc.pairs)
			var refusal *PolicyError
			if !errors.As(err, &refusal) {
				t.Fatal("bound refusal", err)
			}
			if tc.cap != errors.Is(err, ErrCap) {
				t.Fatal("bound cause", err)
			}
			turn.transaction.rollback(nil)
			head := iaBookSeriesHeadRow(t, w, bookID)
			if head.revision != 1 || head.count != 1 {
				t.Fatal("refused bound changed the accepted head", head)
			}
		})
	}
}

// A later failure in the same accept rolls the projection back with the rest of
// the unit.
func TestIABookSeriesAcceptRollbackLeavesNoProjection(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaBookNoEffects(t, w)

	w, reg, p = iaBookAcceptFixture(t)
	wc008NativeExec(t, w, `CREATE TRIGGER ia_book_series_late_failure BEFORE INSERT ON scan_command_receipts BEGIN SELECT RAISE(ABORT,'late'); END`)
	q = iaBookAcceptRequest(w)
	ref = iaBookAcceptReference(t, q, 2, 2)
	turn, out, err = iaBookAcceptRun(t, w, reg, p, q, ref)
	if err == nil || out.decision != 0 {
		t.Fatal("late failure was released", out, err)
	}
	turn.transaction.rollback(nil)
	iaBookNoEffects(t, w)
}

// Replay and Retired return the saved decision without republishing or bumping
// the head, even after the live source changed.
func TestIABookSeriesAcceptReplayAndRetiredLeaveHead(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	out := iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	bookID := iaBookSeriesBookID(t, w, "book-key")
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='books-source'`)
	turn, replay, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest {
		t.Fatal("replay", replay, err)
	}
	turn.transaction.rollback(nil)
	head := iaBookSeriesHeadRow(t, w, bookID)
	if head.revision != 1 || head.count != 1 {
		t.Fatal("replay republished", head)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='books-run'`)
	turn, retired, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || retired.decision != receiptRetired || len(retired.result.facts.members) != 0 {
		t.Fatal("retired", retired, err)
	}
	turn.transaction.rollback(nil)
	head = iaBookSeriesHeadRow(t, w, bookID)
	if head.revision != 1 || head.count != 1 {
		t.Fatal("retired republished", head)
	}
}

// A lost COMMIT reply is never success; the committed head is read back through
// a FRESH read-only connection, never the quarantined writer pool.
func TestIABookSeriesAcceptUnknownCommitReadsBackHead(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
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
	wantCount, wantBytes, wantDigest, err := bookseries.Cohort([]bookseries.Pair{{Name: "Saga", Position: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	var libraryID, assetID, infoEpoch, policyRevision, revision, count, bytes int64
	var sourceRevision string
	var digest []byte
	if err = db.QueryRowContext(t.Context(), `SELECT library_id,source_asset_id,source_info_epoch,source_revision,local_policy_revision,revision,member_count,member_bytes,member_digest FROM catalog_book_series_heads`).Scan(
		&libraryID, &assetID, &infoEpoch, &sourceRevision, &policyRevision, &revision, &count, &bytes, &digest); err != nil {
		t.Fatal(err)
	}
	if libraryID != iaBookTestLibraryID || assetID != iaBookTestAssetID || infoEpoch != 1 || sourceRevision != "book-token" ||
		policyRevision != 4 || revision != 1 || count != wantCount || bytes != wantBytes || !equalBytes(digest, wantDigest[:]) {
		t.Fatal("fresh read back head", libraryID, assetID, infoEpoch, sourceRevision, policyRevision, revision, count, bytes)
	}
	var name, position string
	if err = db.QueryRowContext(t.Context(), `SELECT series_name,series_position FROM catalog_book_series_members`).Scan(&name, &position); err != nil {
		t.Fatal(err)
	}
	if name != "Saga" || position != "1" {
		t.Fatal("fresh read back member", name, position)
	}
}

// Current authority precedes the projection write: a forged issuer denies with
// no head and no member row.
func TestIABookSeriesAcceptForgedIssuerWritesNothing(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	fake := *p
	fake.witness.issuer.family = "forged"
	turn, out, err := iaBookAcceptRun(t, w, reg, &fake, q, ref)
	if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 {
		t.Fatal("forged issuer", out, err)
	}
	turn.transaction.rollback(nil)
	iaBookNoEffects(t, w)
}

type iaBookSeriesHeadTestRow struct {
	libraryID, bookKind, assetID, infoEpoch, policyRevision, revision, count, bytes int64
	sourceRevision                                                                  string
	digest                                                                          [32]byte
}

func iaBookSeriesBookID(t *testing.T, w *executor, key string) int64 {
	t.Helper()
	var id int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?`, iaBookTestLibraryID, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func iaBookSeriesHeadRow(t *testing.T, w *executor, bookID int64) iaBookSeriesHeadTestRow {
	t.Helper()
	var row iaBookSeriesHeadTestRow
	var digest []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT library_id,book_kind,source_asset_id,source_info_epoch,source_revision,local_policy_revision,revision,member_count,member_bytes,member_digest FROM catalog_book_series_heads WHERE book_id=?`, bookID).Scan(
		&row.libraryID, &row.bookKind, &row.assetID, &row.infoEpoch, &row.sourceRevision, &row.policyRevision, &row.revision, &row.count, &row.bytes, &digest); err != nil {
		t.Fatal(err)
	}
	if len(digest) != 32 {
		t.Fatal("head digest length", len(digest))
	}
	copy(row.digest[:], digest)
	return row
}

func iaBookSeriesDirectPublish(t *testing.T, w *executor, reg *iaEntryRegistry, q iaBookInventoryRequest, bookID, infoEpoch, policyRevision int64, pairs []bookseries.Pair) {
	t.Helper()
	turn, _ := iaBookAcceptTurn(t, w, reg, q)
	if err := iaBookSeriesPublish(t.Context(), turn, iaBookTestLibraryID, bookID,
		iaBookAssetFacts{id: iaBookTestAssetID, infoEpoch: infoEpoch, token: "book-token"}, policyRevision, pairs); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func repeatByte(b byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return string(out)
}
