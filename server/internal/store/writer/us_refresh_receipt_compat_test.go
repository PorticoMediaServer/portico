package writer

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	usRefreshReceiptFamilyA = "us-refresh-family-a"
	usRefreshReceiptFamilyB = "us-refresh-family-b"
)

// usRefreshReceiptFixture starts from the ordinary canonical fixture and
// creates two independent ordinary session families so the receipt constraint
// can be exercised without the legacy identity package.
func usRefreshReceiptFixture(t *testing.T) *executor {
	t.Helper()
	w, _, _ := testExecutor(t)
	for i, family := range []string{usRefreshReceiptFamilyA, usRefreshReceiptFamilyB} {
		marker := strings.Repeat(string(rune('a'+i)), 32)
		_, err := w.db.ExecContext(t.Context(), `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,0)`,
			family, "us-refresh-server", "us-refresh-account", "us-refresh-profile", "local", "owner", 1, "2030-01-01T00:00:00Z", 1, marker, marker, []byte(marker[:16]))
		if err != nil {
			t.Fatalf("insert %s: %v", family, err)
		}
	}
	return w
}

func usRefreshReceiptTurn(t *testing.T, w *executor) *transaction {
	t.Helper()
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), USMemberLibraries, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	return tx
}

func usRefreshReceiptExec(t *testing.T, tx *transaction, query string, args ...any) error {
	t.Helper()
	if err := tx.before(Work{1, 100, 1, 4096, time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	_, err := tx.conn.ExecContext(tx.ctx, query, args...)
	return err
}

func usRefreshReceiptInsert(t *testing.T, tx *transaction, family, predecessor, answer string) error {
	t.Helper()
	return usRefreshReceiptExec(t, tx, `INSERT INTO identity_refresh_receipts(predecessor_hash,family_id,request_id,ciphertext,expires_at) VALUES(?,?,?,?,?)`,
		predecessor, family, "request-"+predecessor, []byte(answer), "2030-01-01T00:00:00Z")
}

func usRefreshReceiptDelete(t *testing.T, tx *transaction, family, predecessor string) error {
	t.Helper()
	return usRefreshReceiptExec(t, tx, `DELETE FROM identity_refresh_receipts WHERE family_id=? AND predecessor_hash=?`, family, predecessor)
}

func usRefreshReceiptStored(t *testing.T, w *executor, predecessor string) (string, bool) {
	t.Helper()
	var family, answer string
	err := w.db.QueryRowContext(t.Context(), `SELECT family_id,ciphertext FROM identity_refresh_receipts WHERE predecessor_hash=?`, predecessor).Scan(&family, &answer)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false
		}
		t.Fatal(err)
	}
	return family + "|" + answer, true
}

func usRefreshReceiptCount(t *testing.T, w *executor, family string) int {
	t.Helper()
	var count int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM identity_refresh_receipts WHERE family_id=?`, family).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestUSRefreshReceiptSingleSuccessor proves the group B refresh-receipt
// constraint before it is embedded: a family retains at most one recoverable
// answer, a successor replaces it only by retiring the old exact source in the
// same turn, unrelated families stay independent, a rolled-back prefix leaves
// the old source intact, and a lost answer stays replayable until the successor
// is used. This is a native store control, not an activation of the legacy
// refresh producer and not an extension of the US-01 authority stamp.
func TestUSRefreshReceiptSingleSuccessor(t *testing.T) {
	t.Run("SecondRetainedReceiptRefused", func(t *testing.T) {
		w := usRefreshReceiptFixture(t)
		tx := usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-0", "sealed-0"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}

		tx = usRefreshReceiptTurn(t, w)
		err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-1", "sealed-1")
		if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
			t.Fatalf("second retained receipt for one family was not refused by the unique index: %v", err)
		}
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}
		if count := usRefreshReceiptCount(t, w, usRefreshReceiptFamilyA); count != 1 {
			t.Fatalf("refused insert changed the retained receipt count: %d", count)
		}
		if _, ok := usRefreshReceiptStored(t, w, "predecessor-0"); !ok {
			t.Fatal("refused insert displaced the old exact source")
		}
	})

	t.Run("SuccessorRetiresOldSourceInOneTurn", func(t *testing.T) {
		w := usRefreshReceiptFixture(t)
		tx := usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-0", "sealed-0"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}

		// The producer ordering is delete-then-insert in the same turn
		// (identity/refresh.go:127-130). Nothing may delete the old receipt in a
		// separate turn and leave a gap in which no answer is recoverable.
		tx = usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptDelete(t, tx, usRefreshReceiptFamilyA, "predecessor-0"); err != nil {
			t.Fatal(err)
		}
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-1", "sealed-1"); err != nil {
			t.Fatalf("same-turn successor insert refused: %v", err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}
		if _, ok := usRefreshReceiptStored(t, w, "predecessor-0"); ok {
			t.Fatal("retired predecessor remained replayable")
		}
		if stored, ok := usRefreshReceiptStored(t, w, "predecessor-1"); !ok || stored != usRefreshReceiptFamilyA+"|sealed-1" {
			t.Fatalf("successor receipt: %q %v", stored, ok)
		}
		if count := usRefreshReceiptCount(t, w, usRefreshReceiptFamilyA); count != 1 {
			t.Fatalf("family holds %d retained receipts", count)
		}
	})

	t.Run("IndependentFamiliesStayIndependent", func(t *testing.T) {
		w := usRefreshReceiptFixture(t)
		for _, family := range []string{usRefreshReceiptFamilyA, usRefreshReceiptFamilyB} {
			tx := usRefreshReceiptTurn(t, w)
			if err := usRefreshReceiptInsert(t, tx, family, "predecessor-"+family, "sealed-"+family); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.commit(); err != nil {
				t.Fatal(err)
			}
		}

		tx := usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptDelete(t, tx, usRefreshReceiptFamilyA, "predecessor-"+usRefreshReceiptFamilyA); err != nil {
			t.Fatal(err)
		}
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "successor-"+usRefreshReceiptFamilyA, "sealed-successor-a"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}

		if stored, ok := usRefreshReceiptStored(t, w, "predecessor-"+usRefreshReceiptFamilyB); !ok || stored != usRefreshReceiptFamilyB+"|sealed-"+usRefreshReceiptFamilyB {
			t.Fatalf("other family's retained receipt changed: %q %v", stored, ok)
		}
		if count := usRefreshReceiptCount(t, w, usRefreshReceiptFamilyB); count != 1 {
			t.Fatalf("other family holds %d retained receipts", count)
		}
		if _, ok := usRefreshReceiptStored(t, w, "successor-"+usRefreshReceiptFamilyA); !ok {
			t.Fatal("family A's own successor was not committed")
		}
	})

	t.Run("RollbackAfterPrefixKeepsOldReceipt", func(t *testing.T) {
		w := usRefreshReceiptFixture(t)
		tx := usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-0", "sealed-0"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}

		tx = usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptDelete(t, tx, usRefreshReceiptFamilyA, "predecessor-0"); err != nil {
			t.Fatal(err)
		}
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-1", "sealed-1"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}

		if stored, ok := usRefreshReceiptStored(t, w, "predecessor-0"); !ok || stored != usRefreshReceiptFamilyA+"|sealed-0" {
			t.Fatalf("rollback lost the old source: %q %v", stored, ok)
		}
		if _, ok := usRefreshReceiptStored(t, w, "predecessor-1"); ok {
			t.Fatal("rolled-back successor remained")
		}
		if count := usRefreshReceiptCount(t, w, usRefreshReceiptFamilyA); count != 1 {
			t.Fatalf("family holds %d retained receipts after rollback", count)
		}

		// The intact old source can still be retired by a later successor turn.
		tx = usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptDelete(t, tx, usRefreshReceiptFamilyA, "predecessor-0"); err != nil {
			t.Fatal(err)
		}
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-2", "sealed-2"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("LostAnswerSurvivesUntilSuccessorIsUsed", func(t *testing.T) {
		w := usRefreshReceiptFixture(t)
		tx := usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-1", "sealed-1"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}

		// The device never received that answer. An exact retry after any outage
		// reads the same retained answer and adds no effect.
		stored, ok := usRefreshReceiptStored(t, w, "predecessor-1")
		if !ok || stored != usRefreshReceiptFamilyA+"|sealed-1" {
			t.Fatalf("lost answer was not retained: %q %v", stored, ok)
		}
		if count := usRefreshReceiptCount(t, w, usRefreshReceiptFamilyA); count != 1 {
			t.Fatalf("replay of a lost answer added a receipt: %d", count)
		}

		// Once the legitimate device uses the successor, the predecessor source is
		// retired in the same turn as the next answer is retained.
		tx = usRefreshReceiptTurn(t, w)
		if err := usRefreshReceiptDelete(t, tx, usRefreshReceiptFamilyA, "predecessor-1"); err != nil {
			t.Fatal(err)
		}
		if err := usRefreshReceiptInsert(t, tx, usRefreshReceiptFamilyA, "predecessor-2", "sealed-2"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.commit(); err != nil {
			t.Fatal(err)
		}

		// The old source no longer resolves, so reuse is detectable rather than a
		// stale replay, and only the current successor answer remains recoverable.
		if _, ok := usRefreshReceiptStored(t, w, "predecessor-1"); ok {
			t.Fatal("predecessor answer survived the successor turn")
		}
		if stored, ok := usRefreshReceiptStored(t, w, "predecessor-2"); !ok || stored != usRefreshReceiptFamilyA+"|sealed-2" {
			t.Fatalf("successor answer: %q %v", stored, ok)
		}
		if count := usRefreshReceiptCount(t, w, usRefreshReceiptFamilyA); count != 1 {
			t.Fatalf("family holds %d retained receipts", count)
		}
	})
}
