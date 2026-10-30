package identity

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func countFixture(t *testing.T) *sql.DB {
	t.Helper()
	// This tests the bounded relational reader, not caller authentication. The
	// production caller must supply its already-authenticated preview snapshot.
	db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "count.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE accounts(id TEXT PRIMARY KEY, state TEXT); INSERT INTO accounts VALUES('m_owner','active')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func snapshotCount(t *testing.T, db *sql.DB) *int {
	t.Helper()
	snapshot, err := dbwork.BeginSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	count, err := captureOwnerSwitchCountTx(context.Background(), snapshot.Tx(), "m_owner")
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestOwnerSwitchCountKeepsDisabledAndWaitingAccountsAndExcludesOtherObjects(t *testing.T) {
	db := countFixture(t)
	if count := snapshotCount(t, db); count == nil || *count != 0 {
		t.Fatalf("empty preview count: %v", count)
	}
	if _, err := db.Exec(`INSERT INTO accounts VALUES('a_disabled','disabled'),('z_waiting','waiting'),('gone','active'); DELETE FROM accounts WHERE id='gone'; CREATE TABLE profiles(id TEXT PRIMARY KEY); INSERT INTO profiles VALUES('first'),('second'),('third'); CREATE TABLE invitations(id TEXT PRIMARY KEY); INSERT INTO invitations VALUES('unused')`); err != nil {
		t.Fatal(err)
	}
	if count := snapshotCount(t, db); count == nil || *count != 2 {
		t.Fatalf("retained account count: %v", count)
	}
}

func TestOwnerSwitchCountEmitsExactTenThousandAndOmitsOverflow(t *testing.T) {
	db := countFixture(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`INSERT INTO accounts VALUES(?,'active')`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for i := 0; i < 10000; i++ {
		if _, err = statement.Exec(fmt.Sprintf("person_%05d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count := snapshotCount(t, db); count == nil || *count != 10000 {
		t.Fatalf("bounded exact count: %v", count)
	}
	if _, err = db.Exec(`INSERT INTO accounts VALUES('one_more','waiting')`); err != nil {
		t.Fatal(err)
	}
	if count := snapshotCount(t, db); count != nil {
		t.Fatalf("overflow must be omitted, got %d", *count)
	}
}

func TestOwnerSwitchCountUsesBoundedPrimaryKeyRanges(t *testing.T) {
	db := countFixture(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+ownerSwitchCountSQL, "m_owner")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ranges := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SEARCH accounts USING COVERING INDEX") {
			ranges++
		}
		if strings.Contains(detail, "SCAN accounts") {
			t.Fatal("count scanned the full accounts table")
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if ranges != 2 {
		t.Fatalf("expected both primary-key ranges, got %d", ranges)
	}
}
