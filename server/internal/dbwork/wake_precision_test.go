package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// These cases pin the exact-wake precision amendment: a committed plain SELECT
// no longer counts as a write for WakeOnTableWrites subscribers, while every
// general root, unknown broadcast, trigger/FK edge and real write keeps exactly
// the behaviour it had. The read that is exempted is the one the bounded head
// parser can prove read-only; everything else stays conservative.

func precisionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenHandle(filepath.Join(t.TempDir(), "precision.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	const schema = `
CREATE TABLE wp_read(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_write(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_source(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_unrelated(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_parent(id INTEGER PRIMARY KEY);
CREATE TABLE wp_child(id INTEGER PRIMARY KEY, parent INTEGER REFERENCES wp_parent(id) ON DELETE CASCADE);
CREATE TABLE wp_jobs(id INTEGER PRIMARY KEY);
CREATE TRIGGER wp_enqueue AFTER INSERT ON wp_child BEGIN INSERT INTO wp_jobs VALUES(NEW.id); END;
CREATE TABLE wp_inserted(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_replace_target(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_select_source(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_deleted(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_update_target(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_returning_target(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE wp_accounts(id INTEGER PRIMARY KEY, epoch INTEGER NOT NULL);
INSERT INTO wp_read VALUES(1,'one');
INSERT INTO wp_write VALUES(1,'one');
INSERT INTO wp_source VALUES(1,'one');
INSERT INTO wp_unrelated VALUES(1,'one');
INSERT INTO wp_select_source VALUES(7,'seven');
INSERT INTO wp_deleted VALUES(1,'one');
INSERT INTO wp_update_target VALUES(1,'one');
INSERT INTO wp_parent VALUES(1);
INSERT INTO wp_accounts VALUES(1,0);`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if err = InstallWakeDependencies(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestPlainSelectIsTheOnlyExactExemption pins the classifier itself. The
// accepted shape is deliberately narrow: a literal `select ` head after the
// existing trim/lower-casing, no CTE, at most the one terminal semicolon, and no
// comment marker anywhere. Everything else keeps the old false wake.
func TestPlainSelectIsTheOnlyExactExemption(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sql   string
		plain bool
	}{
		{"plain", `SELECT kind FROM libraries WHERE id=?`, true},
		{"join", `SELECT s.id,s.root FROM library_sources s JOIN libraries l ON l.id=s.library_id`, true},
		{"union", `SELECT id FROM libraries UNION SELECT id FROM library_sources`, true},
		{"subquery", `SELECT id,(SELECT 1 FROM library_sources) FROM libraries`, true},
		{"terminal semicolon", `SELECT id FROM libraries;`, true},
		{"padded", "  SELECT id FROM libraries  ", true},
		{"leading comment", "-- comment\nSELECT id FROM libraries", false},
		{"block comment", "/* comment */ SELECT id FROM libraries", false},
		{"internal comment", `SELECT id/* why */ FROM library_sources`, false},
		{"dash marker in literal", `SELECT '--x' FROM library_sources`, false},
		{"script", `SELECT 1; DELETE FROM library_sources`, false},
		{"cte read", `WITH x AS (SELECT 1) SELECT id FROM libraries`, false},
		{"cte write", `WITH x AS (SELECT 1) UPDATE accounts SET epoch=epoch+1`, false},
		{"insert select", `INSERT INTO libraries(id) SELECT id FROM library_sources`, false},
		{"tab head", "SELECT\tid FROM library_sources", false},
		{"compact head", `SELECT(id) FROM library_sources`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := headOf(tc.sql)
			if head.plainSelect != tc.plain {
				t.Fatalf("plainSelect(%q) = %v, want %v", tc.sql, head.plainSelect, tc.plain)
			}
			// The amendment adds one field; the old head facts stay as they were.
			switch tc.name {
			case "cte read", "cte write", "script":
				if !head.unknown {
					t.Fatalf("%q stopped being unknown", tc.sql)
				}
			case "terminal semicolon", "padded", "plain":
				if head.unknown {
					t.Fatalf("%q became unknown", tc.sql)
				}
			}
		})
	}
	// The memo is keyed by raw text; classifying twice must agree.
	for i := 0; i < 3; i++ {
		if head := headOf(`SELECT id FROM library_sources WHERE id=?`); !head.plainSelect || !head.named || head.table != "library_sources" {
			t.Fatalf("cached head %+v lost the read classification", head)
		}
	}
}

func precisionRead(t *testing.T, tx *sql.Tx, table string) {
	t.Helper()
	rows, err := tx.Query(`SELECT id,value FROM ` + table)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var value string
		if err = rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
}

func precisionWrite(t *testing.T, tx *sql.Tx, table string) {
	t.Helper()
	if _, err := tx.Exec(`UPDATE ` + table + ` SET value=value WHERE id=1`); err != nil {
		t.Fatal(err)
	}
}

// TestReadRootsStayGeneralWhileExactWakesNeedAWrite is the separate-set
// counterexample: SELECT + unrelated write. The read root must keep waking
// WakeOnTables subscribers and must not wake a WakeOnTableWrites subscriber.
// Both statement orders are exercised for a root that is read and written.
func TestReadRootsStayGeneralWhileExactWakesNeedAWrite(t *testing.T) {
	db := precisionDB(t)
	ctx := context.Background()
	generalRead, exactRead := worker.NewSignal(), worker.NewSignal()
	generalWrite, exactWrite := worker.NewSignal(), worker.NewSignal()
	for _, stop := range []func(){
		WakeOnTables(generalRead, "wp_read"),
		WakeOnTableWrites(exactRead, "wp_read"),
		WakeOnTables(generalWrite, "wp_write"),
		WakeOnTableWrites(exactWrite, "wp_write"),
	} {
		defer stop()
	}

	// Read, then an unrelated write: the exact read subscription stays silent,
	// the general one still hears its root.
	if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
		precisionRead(t, tx, "wp_read")
		precisionWrite(t, tx, "wp_unrelated")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !generalRead.Take() {
		t.Fatal("a committed read lost its general root wake")
	}
	if exactRead.Take() {
		t.Fatal("a committed read woke an exact subscriber")
	}
	if generalWrite.Take() || exactWrite.Take() {
		t.Fatal("an unrelated write woke a table it never named")
	}

	// Real write to a root that is also read, in both orders: exact must fire,
	// and a later read must not clear it.
	for _, order := range []string{"write-then-read", "read-then-write"} {
		t.Run(order, func(t *testing.T) {
			if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
				if order == "write-then-read" {
					precisionWrite(t, tx, "wp_source")
					precisionRead(t, tx, "wp_source")
					return nil
				}
				precisionRead(t, tx, "wp_source")
				precisionWrite(t, tx, "wp_source")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			exact := worker.NewSignal()
			general := worker.NewSignal()
			stopExact, stopGeneral := WakeOnTableWrites(exact, "wp_source"), WakeOnTables(general, "wp_source")
			defer stopExact()
			defer stopGeneral()
			if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
				if order == "write-then-read" {
					precisionWrite(t, tx, "wp_source")
					precisionRead(t, tx, "wp_source")
					return nil
				}
				precisionRead(t, tx, "wp_source")
				precisionWrite(t, tx, "wp_source")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			exactHeard, generalHeard := exact.Take(), general.Take()
			if !exactHeard || !generalHeard {
				t.Fatalf("%s: a real write alongside a read lost its wake (exact=%v general=%v)", order, exactHeard, generalHeard)
			}
		})
	}
}

// TestFalseValuedRootsStillExpandDependencies proves the value change does not
// reach the general set: a read-only root keeps its trigger and foreign-key
// expansion. Nothing writes wp_jobs here; only wp_child was read, and the
// trigger edge from wp_child is what wakes it.
func TestFalseValuedRootsStillExpandDependencies(t *testing.T) {
	db := precisionDB(t)
	ctx := context.Background()
	jobs, child := worker.NewSignal(), worker.NewSignal()
	for _, stop := range []func(){WakeOnTables(jobs, "wp_jobs"), WakeOnTables(child, "wp_child")} {
		defer stop()
	}
	if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id,parent FROM wp_child`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, parent sql.NullInt64
			if err = rows.Scan(&id, &parent); err != nil {
				return err
			}
		}
		rows.Close()
		_, err = tx.Exec(`UPDATE wp_unrelated SET value=value WHERE id=1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !child.Take() {
		t.Fatal("a read root stopped waking its own general subscriber")
	}
	if !jobs.Take() {
		t.Fatal("a read root stopped expanding its trigger dependency for general subscribers")
	}
	// Directly: the key survives with a false value, and roots() reports it.
	changes := &changeSet{}
	changes.note(`SELECT id,parent FROM wp_child`)
	changes.note(`UPDATE wp_unrelated SET value=value WHERE id=1`)
	if known := changes.known(); known["wp_child"] {
		t.Fatal("the read root entered the exact set")
	}
	roots := changes.roots()
	if !roots["wp_child"] || !roots["wp_unrelated"] {
		t.Fatalf("general roots lost a named table: %v", roots)
	}
}

// TestTrueMutationsNameOnlyTheirDirectTarget covers the write shapes the exact
// wake relies on, through both connection and prepared executions, and through
// Exec and Query (RETURNING). An INSERT … SELECT names its target, never the
// table it reads.
func TestTrueMutationsNameOnlyTheirDirectTarget(t *testing.T) {
	db := precisionDB(t)
	ctx := context.Background()
	inserted, replaced, deleted, updated, returned := worker.NewSignal(), worker.NewSignal(), worker.NewSignal(), worker.NewSignal(), worker.NewSignal()
	selectSource := worker.NewSignal()
	for _, stop := range []func(){
		WakeOnTableWrites(inserted, "wp_inserted"),
		WakeOnTableWrites(replaced, "wp_replace_target"),
		WakeOnTableWrites(deleted, "wp_deleted"),
		WakeOnTableWrites(updated, "wp_update_target"),
		WakeOnTableWrites(returned, "wp_returning_target"),
		WakeOnTableWrites(selectSource, "wp_select_source"),
		WakeOnTables(selectSource, "wp_select_source"),
	} {
		defer stop()
	}
	for _, tc := range []struct {
		name   string
		run    func(tx *sql.Tx) error
		signal *worker.Signal
	}{
		{"insert", func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO wp_inserted(id,value) VALUES(1,'a')`)
			return err
		}, inserted},
		{"prepared insert", func(tx *sql.Tx) error {
			stmt, err := tx.Prepare(`INSERT INTO wp_inserted(id,value) VALUES(2,'b')`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			_, err = stmt.Exec()
			return err
		}, inserted},
		{"insert select", func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO wp_inserted(id,value) SELECT id+100,value FROM wp_select_source WHERE id=7`)
			return err
		}, inserted},
		{"replace", func(tx *sql.Tx) error {
			_, err := tx.Exec(`REPLACE INTO wp_replace_target(id,value) VALUES(1,'a')`)
			return err
		}, replaced},
		{"delete", func(tx *sql.Tx) error {
			_, err := tx.Exec(`DELETE FROM wp_deleted WHERE id=1`)
			return err
		}, deleted},
		{"update", func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE wp_update_target SET value='b' WHERE id=1`)
			return err
		}, updated},
		{"returning", func(tx *sql.Tx) error {
			if _, err := tx.Exec(`INSERT INTO wp_returning_target(id,value) VALUES(1,'a')`); err != nil {
				return err
			}
			rows, err := tx.Query(`UPDATE wp_returning_target SET value='b' WHERE id=1 RETURNING id`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					return err
				}
			}
			rows.Close()
			return rows.Err()
		}, returned},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := WithWriteTx(ctx, db, ClassInteractive, tc.run); err != nil {
				t.Fatal(err)
			}
			if !tc.signal.Take() {
				t.Fatalf("%s named its target but did not wake it", tc.name)
			}
		})
	}
	if selectSource.Take() {
		t.Fatal("a write woke the table it only read")
	}
	// Prepared Query on the connection path names the target too.
	preparedReturning := worker.NewSignal()
	stopPrepared := WakeOnTableWrites(preparedReturning, "wp_returning_target")
	defer stopPrepared()
	if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`UPDATE wp_returning_target SET value=? WHERE id=1 RETURNING id`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		rows, err := stmt.Query("c")
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				return err
			}
		}
		rows.Close()
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if !preparedReturning.Take() {
		t.Fatal("a prepared UPDATE … RETURNING did not wake its exact target")
	}
}

// TestUnknownAndCommentedShapesKeepTheOldExactBehaviour pins the conservative
// half: an unknown statement that names a root still wakes it exactly (the
// pre-existing behaviour the old comment overstated), an unknown statement that
// names nothing still broadcasts generally, and comment/short forms never gain
// the read exemption.
func TestUnknownAndCommentedShapesKeepTheOldExactBehaviour(t *testing.T) {
	db := precisionDB(t)
	ctx := context.Background()
	exact, general, unrelated := worker.NewSignal(), worker.NewSignal(), worker.NewSignal()
	for _, stop := range []func(){
		WakeOnTableWrites(exact, "wp_accounts", "wp_source"),
		WakeOnTables(general, "wp_accounts", "wp_source"),
		WakeOnTables(unrelated, "wp_unrelated"),
	} {
		defer stop()
	}
	// A CTE that names a root: unknown, still exact for the root it named.
	if _, err := ExecWrite(ctx, db, ClassInteractive, `WITH x AS (SELECT 1) UPDATE wp_accounts SET epoch=epoch+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if !exact.Take() || !general.Take() {
		t.Fatal("a CTE naming a root stopped waking that root")
	}
	for _, tc := range []struct {
		name      string
		statement string
		exact     bool
		broadcast bool
	}{
		{"script first", `SELECT 1; UPDATE wp_source SET value='script' WHERE id=1`, true, true},
		{"script read names root", `SELECT id FROM wp_source; UPDATE wp_unrelated SET value='script' WHERE id=1`, true, true},
	} {
		if _, err := ExecWrite(ctx, db, ClassInteractive, tc.statement); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := exact.Take(); got != tc.exact {
			t.Fatalf("%s: exact wake %v, want %v", tc.name, got, tc.exact)
		}
		if got := general.Take(); got != tc.exact {
			t.Fatalf("%s: named general wake %v, want %v", tc.name, got, tc.exact)
		}
		if got := unrelated.Take(); got != tc.broadcast {
			t.Fatalf("%s: unrelated broadcast %v, want %v", tc.name, got, tc.broadcast)
		}
	}
	// A read alone changes no row, so it can never notify; a comment-bearing
	// read is only observable beside a real write in the same transaction. There
	// it keeps the old exact wake for the root it named, while the plain
	// spelling of the same read is the one shape that declines it.
	for _, tc := range []struct {
		name  string
		read  string
		exact bool
	}{
		{"leading comment", "-- leading\nSELECT id FROM wp_source", true},
		{"block comment", "/* note */ SELECT id FROM wp_source", true},
		{"internal comment", `SELECT id /* note */ FROM wp_source`, true},
		{"literal comment marker", `SELECT id FROM wp_source WHERE value<>'--'`, true},
		{"plain read", `SELECT id FROM wp_source`, false},
	} {
		if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
			rows, err := tx.Query(tc.read)
			if err != nil {
				return err
			}
			for rows.Next() {
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return err
			}
			_, err = tx.Exec(`UPDATE wp_unrelated SET value=? WHERE id=1`, tc.name)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := exact.Take(); got != tc.exact {
			t.Fatalf("%s beside a write: exact wake %v, want %v", tc.name, got, tc.exact)
		}
		if !general.Take() {
			t.Fatalf("%s beside a write: the named root lost its general wake", tc.name)
		}
		if !unrelated.Take() {
			t.Fatalf("%s beside a write: the real write did not wake its table", tc.name)
		}
	}
}

// TestRollbackAndNoChangeStaySilent keeps the transaction boundaries: a rolled
// back read plus write announces nothing, and a no-row statement never notifies.
func TestRollbackAndNoChangeStaySilent(t *testing.T) {
	db := precisionDB(t)
	ctx := context.Background()
	exact, general := worker.NewSignal(), worker.NewSignal()
	for _, stop := range []func(){WakeOnTableWrites(exact, "wp_source"), WakeOnTables(general, "wp_source")} {
		defer stop()
	}
	if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE wp_source SET value='rolled back' WHERE id=1`); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT id FROM wp_source`)
		if err != nil {
			return err
		}
		rows.Close()
		return context.Canceled
	}); err == nil {
		t.Fatal("the rolled-back transaction committed")
	}
	if exact.Take() || general.Take() {
		t.Fatal("a rolled-back transaction announced a change")
	}
	if _, err := ExecWrite(ctx, db, ClassInteractive, `UPDATE wp_source SET value='absent' WHERE id=-1`); err != nil {
		t.Fatal(err)
	}
	if exact.Take() || general.Take() {
		t.Fatal("a no-row statement announced a change")
	}
	// A read-only transaction has no error and no notification either.
	if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM wp_source`)
		if err != nil {
			return err
		}
		rows.Close()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if exact.Take() || general.Take() {
		t.Fatal("a read-only transaction announced a change")
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM wp_source WHERE id=1`).Scan(&value); err != nil || value != "one" {
		t.Fatalf("rollback did not restore the committed row: %q %v", value, err)
	}
}

// TestExactRegistrationsStayConservativeOnPostCountFailure keeps the ExecWrite
// post-count-failure contract: the statement already committed, so general and
// direct subscribers are woken conservatively and the statement is never
// replayed. Unchanged by the precision amendment.
func TestExactRegistrationsStayConservativeOnPostCountFailure(t *testing.T) {
	conn := &postCountFailureConn{}
	db := sql.OpenDB(postCountFailureConnector{conn})
	defer db.Close()
	general, direct := worker.NewSignal(), worker.NewSignal()
	for _, stop := range []func(){WakeOnTables(general, "wp_rows"), WakeOnDirectWrites(direct, "wp_rows")} {
		defer stop()
	}
	result, err := ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO wp_rows(value) VALUES('committed')`)
	if err != nil || result == nil || conn.executed != 1 || conn.counts != 2 {
		t.Fatalf("committed statement was failed or replayed: err %v, executions %d, counts %d", err, conn.executed, conn.counts)
	}
	if !general.Take() || !direct.Take() {
		t.Fatal("a committed write with unknown post-count stopped broadcasting conservatively")
	}
}
