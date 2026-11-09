package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Real bundled canonical F bootstrap, FK=1 and DQS=0/0. No overlay, legacy
// persistence opener or substitute parent. Synthetic custody is not authority.
func uoReadingCustodyTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	return wc008NativeTurn(t, w, SetPersonalFlags, uoReadingCustodyDefinitions())
}

func uoReadingReceiptFixture(t *testing.T, kind uoReadingPruneKind) (*executor, uoReadingPruneScope) {
	t.Helper()
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('reader','reader',X'','profile'),('other','other',X'','other')`)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_global(singleton,retired_before_ms) VALUES(1,36)`)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_accounts(id,authority,account_id,owner_account_id,charged_stop,retired_before_ms) VALUES(1,'local','reader','reader',0,36),(2,'local','other','other',0,36)`)
	for n := 1; n <= 37; n++ {
		issued, scope := n, 1
		if n == 36 {
			issued = 100
		}
		if n == 37 {
			issued = 101
			scope = 2
		}
		key := fmt.Sprintf("p251_%013d_%032x", issued, n)
		wc008NativeExec(t, w, `INSERT INTO user_mutation_receipts(key,scope_id,profile_id,profile_incarnation,family_id,operation,digest,is_stop,issued_ms,status) VALUES(?,?,'profile',?,'family','reading',?,0,?,'applied')`, key, scope, strings.Repeat("b", 32), strings.Repeat("a", 64), issued)
		wc008NativeExec(t, w, `INSERT INTO user_mutation_reading_results(receipt_key,command_kind,result_version,item_public_id,reading_revision,personal_revision,observed_ms,accepted,stale,cleared,reason) VALUES(?,771,1,?,1,1,1,1,0,1,'original')`, key, []byte("1234567890123456"))
	}
	q := uoReadingPruneScope{kind: kind, scopeID: 1, authority: "local", account: "reader", family: "family", profile: "profile", lifetime: strings.Repeat("b", 32)}
	if kind == uoReadingPruneOrphan {
		wc008NativeExec(t, w, `UPDATE user_mutation_accounts SET owner_account_id=NULL,account_id=NULL WHERE id=1`)
	}
	return w, q
}

func uoReadingCount(t *testing.T, w *executor, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func uoReadingRequireFK(t *testing.T, err error) {
	t.Helper()
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code() != 787 {
		t.Fatalf("want FK787, got %v", err)
	}
}

func TestUOReadingCustodyFourSelectedBatches(t *testing.T) {
	for _, kind := range []uoReadingPruneKind{uoReadingPruneAccount, uoReadingPruneFamily, uoReadingPruneOrphan, uoReadingPruneGlobal} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			w, q := uoReadingReceiptFixture(t, kind)
			_, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_receipts WHERE issued_ms=1`)
			uoReadingRequireFK(t, err)
			turn := uoReadingCustodyTurn(t, w)
			e, err := pruneUOReadingReceipts(t.Context(), turn, q)
			if err != nil || e.receipts != 32 || e.readingResults != 32 || e.ordinary != 32 || e.stops != 0 || e.variableBytes <= 0 {
				t.Fatal(e, err)
			}
			uoFlagsCommit(t, turn)
			if n := uoReadingCount(t, w, `SELECT count(*) FROM user_mutation_receipts`); n != 5 {
				t.Fatal(n)
			}
			if n := uoReadingCount(t, w, `SELECT count(*) FROM user_mutation_reading_results`); n != 5 {
				t.Fatal(n)
			}
			if n := uoReadingCount(t, w, `SELECT count(*) FROM user_mutation_receipts WHERE issued_ms IN(33,34,35,100,101)`); n != 5 {
				t.Fatal("wrong selected parents", n)
			}
		})
	}
}

func TestUOReadingCustodyChildPrefixRollback(t *testing.T) {
	for _, kind := range []uoReadingPruneKind{uoReadingPruneAccount, uoReadingPruneFamily, uoReadingPruneOrphan, uoReadingPruneGlobal} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			w, q := uoReadingReceiptFixture(t, kind)
			turn := uoReadingCustodyTurn(t, w)
			delete(turn.steps, uoReadingReceiptDelete) // after a real child DELETE
			e, err := pruneUOReadingReceipts(t.Context(), turn, q)
			if err == nil || e != (uoReadingPruneEffect{}) {
				t.Fatal(e, err)
			}
			var n int64
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM user_mutation_reading_results`).Scan(&n); err != nil || n != 36 {
				t.Fatal("no child prefix", n, err)
			}
			if _, err = turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if n = uoReadingCount(t, w, `SELECT count(*) FROM user_mutation_reading_results`); n != 37 {
				t.Fatal("prefix survived", n)
			}
			if n = uoReadingCount(t, w, `SELECT count(*) FROM user_mutation_receipts`); n != 37 {
				t.Fatal(n)
			}
		})
	}
}

func uoReadingPersonalFixture(t *testing.T) (*executor, uoReadingPersonalScope, uoReadingPersonalScope) {
	t.Helper()
	w, q := uoFlagsFixture(t)
	from := uoReadingPersonalScope{personalID: 1, itemID: q.subjectID, libraryID: q.libraryID, profileKey: q.profileKey, publicID: q.publicID}
	to := from
	to.personalID = 2
	to.itemID++
	to.publicID[0]++
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,1)`, to.itemID, to.publicID[:], to.libraryID)
	for _, s := range []uoReadingPersonalScope{from, to} {
		wc008NativeExec(t, w, `INSERT INTO personal_items(id,profile_id,item_id,library_id,item_public_id,reading_revision,watchlisted) VALUES(?,?,?,?,?,7,1)`, s.personalID, s.profileKey, s.itemID, s.libraryID, s.publicID[:])
	}
	return w, from, to
}

func uoReadingAddResume(t *testing.T, w *executor, personal, asset, observed, revision int64) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO personal_reading_resumes(personal_item_id,asset_id,source_token,source_revision,format,page_index,progression,revision,observed_ms) VALUES(?,?,?,?,'pdf',12,0.5,?,?)`, personal, asset, fmt.Sprint("stale-source-", asset), strings.Repeat("a", 64), revision, observed)
}

func TestUOReadingCustodyErasureDrainsBeforeParent(t *testing.T) {
	w, from, to := uoReadingPersonalFixture(t)
	uoReadingAddResume(t, w, from.personalID, 21, 1, 1)
	uoReadingAddResume(t, w, from.personalID, 22, 2, 2)
	uoReadingAddResume(t, w, to.personalID, 21, 1, 1)
	_, err := w.db.ExecContext(t.Context(), `DELETE FROM personal_items WHERE id=1`)
	uoReadingRequireFK(t, err)
	turn := uoReadingCustodyTurn(t, w)
	for n := 0; n < 2; n++ {
		e, err := drainUOReadingResume(t.Context(), turn, from)
		if err != nil || e.removed != 1 || e.variableBytes <= 0 || e.hasMore != (n == 0) {
			t.Fatal(e, err)
		}
		if n == 0 {
			_, err = turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM personal_items WHERE id=1`)
			uoReadingRequireFK(t, err)
		}
	}
	e, err := drainUOReadingResume(t.Context(), turn, from)
	if err != nil || e != (uoReadingResumeDrainEffect{}) {
		t.Fatal(e, err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM personal_items WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	if n := uoReadingCount(t, w, `SELECT count(*) FROM personal_reading_resumes WHERE personal_item_id=2`); n != 1 {
		t.Fatal("foreign parent lost", n)
	}
}

func TestUOReadingCustodyResumeRollbackAndBinding(t *testing.T) {
	for _, mode := range []string{"prefix", "profile", "public", "item", "library", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			w, from, _ := uoReadingPersonalFixture(t)
			uoReadingAddResume(t, w, from.personalID, 21, 1, 1)
			turn := uoReadingCustodyTurn(t, w)
			ctx := t.Context()
			switch mode {
			case "prefix":
				delete(turn.steps, uoReadingResumeRemaining)
			case "profile":
				from.profileKey = "foreign"
			case "public":
				from.publicID[0]++
			case "item":
				from.itemID++
			case "library":
				from.libraryID++
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			e, err := drainUOReadingResume(ctx, turn, from)
			if err == nil || e != (uoReadingResumeDrainEffect{}) {
				t.Fatal(e, err)
			}
			_, _ = turn.transaction.rollback(err)
			if n := uoReadingCount(t, w, `SELECT count(*) FROM personal_reading_resumes`); n != 1 {
				t.Fatal(n)
			}
		})
	}
}

func TestUOReadingCustodyTitleMergePreservesSources(t *testing.T) {
	w, from, to := uoReadingPersonalFixture(t)
	uoReadingAddResume(t, w, 1, 21, 20, 2)
	uoReadingAddResume(t, w, 2, 21, 10, 1) // source wins
	uoReadingAddResume(t, w, 1, 22, 10, 1)
	uoReadingAddResume(t, w, 2, 22, 20, 2) // target wins
	uoReadingAddResume(t, w, 1, 23, 30, 3) // stale asset absent from catalogue remains
	turn := uoReadingCustodyTurn(t, w)
	for n := 0; n < 3; n++ {
		e, err := mergeUOReadingResume(t.Context(), turn, from, to)
		if err != nil || e.removed != 1 || e.variableBytes != 0 || e.hasMore != (n < 2) {
			t.Fatal(e, err)
		}
	}
	// The owning merge co-commits the reading/personal head CAS; the participant
	// does not purge parent flags or reconstruct state from a catalogue asset.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE personal_items SET reading_revision=8,revision=revision+1 WHERE id=2 AND reading_revision=7`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM personal_items WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	if n := uoReadingCount(t, w, `SELECT count(*) FROM personal_reading_resumes WHERE personal_item_id=2 AND ((asset_id IN(21,22) AND observed_ms=20) OR (asset_id=23 AND observed_ms=30))`); n != 3 {
		t.Fatal(n)
	}
	if n := uoReadingCount(t, w, `SELECT watchlisted+reading_revision FROM personal_items WHERE id=2`); n != 9 {
		t.Fatal(n)
	}
}

func TestUOReadingCustodyAssetReplacementPreservesStaleResume(t *testing.T) {
	w, from, _ := uoReadingPersonalFixture(t)
	uoReadingAddResume(t, w, from.personalID, 21, 1, 1)
	for _, token := range []string{"old-source", "new-source"} {
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(21,?,7,'/fixture',1,1,'pdf','','',0,0,0)`, token)
		wc008NativeExec(t, w, `DELETE FROM catalog_assets WHERE id=21`)
	}
	if n := uoReadingCount(t, w, `SELECT count(*) FROM personal_reading_resumes WHERE asset_id=21 AND source_token='stale-source-21'`); n != 1 {
		t.Fatal(n)
	}
}

func TestUSReadingErasureExactProfileAndFull(t *testing.T) {
	w, q := uoReadingReceiptFixture(t, uoReadingPruneProfile)
	turn := uoReadingCustodyTurn(t, w)
	if _, err := pruneUSReadingProfileReceipts(t.Context(), turn, q); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	_, _ = turn.transaction.rollback(nil)
	turn = wc008NativeTurn(t, w, USMemberLibraries, uoReadingCustodyDefinitions())
	turn.transaction.descriptor.ID = 539
	q.lifetime = strings.Repeat("c", 32)
	e, err := pruneUSReadingProfileReceipts(t.Context(), turn, q)
	if err != nil || e.receipts != 0 {
		t.Fatal(e, err)
	}
	q.lifetime = strings.Repeat("b", 32)
	e, err = pruneUSReadingProfileReceipts(t.Context(), turn, q)
	if err != nil || e.receipts != 32 {
		t.Fatal(e, err)
	}
	uoFlagsCommit(t, turn)
}

func TestUOReadingCustodyStatementsFullF(t *testing.T) {
	w := wc008NativeExecutor(t)
	if n := uoReadingCount(t, w, `PRAGMA foreign_keys`); n != 1 {
		t.Fatal(n)
	}
	if n := uoReadingCount(t, w, `SELECT count(*) FROM sqlite_schema WHERE type='trigger'`); n != 0 {
		t.Fatal("substitute/legacy schema", n)
	}
	for _, d := range uoReadingCustodyDefinitions() {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	wc008NativePrepare(t, w, uoReadingCustodyDefinitions())
	for _, d := range uoReadingCustodyDefinitions() {
		args := make([]any, len(d.Params))
		for n, p := range d.Params {
			switch p.Type {
			case "text":
				args[n] = "fixture"
			case "blob":
				args[n] = []byte{1}
			default:
				args[n] = int64(1)
			}
		}
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, args...)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatal(d.ID, detail)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUOReadingCustodyMergePrefixRollback(t *testing.T) {
	w, from, to := uoReadingPersonalFixture(t)
	uoReadingAddResume(t, w, 1, 21, 20, 2)
	uoReadingAddResume(t, w, 2, 21, 10, 1)
	turn := uoReadingCustodyTurn(t, w)
	delete(turn.steps, uoReadingResumeMerge)
	e, err := mergeUOReadingResume(t.Context(), turn, from, to)
	if err == nil || e != (uoReadingResumeDrainEffect{}) {
		t.Fatal(e, err)
	}
	var n int
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM personal_reading_resumes`).Scan(&n); e != nil || n != 1 {
		t.Fatal("destination-delete prefix not exercised", n, e)
	}
	_, _ = turn.transaction.rollback(nil)
	if n := uoReadingCount(t, w, `SELECT count(*) FROM personal_reading_resumes`); n != 2 {
		t.Fatal("merge prefix survived", n)
	}
}

func TestUSReadingErasureResumeParticipant(t *testing.T) {
	w, from, _ := uoReadingPersonalFixture(t)
	uoReadingAddResume(t, w, 1, 21, 10, 1)
	turn := wc008NativeTurn(t, w, USMemberLibraries, uoReadingCustodyDefinitions())
	turn.transaction.descriptor.ID = 539
	e, err := drainUSReadingPersonal(t.Context(), turn, from)
	if err != nil || e.removed != 1 || e.hasMore {
		t.Fatal(e, err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM personal_items WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
}
