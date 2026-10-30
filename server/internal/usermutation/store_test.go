package usermutation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func init() { persistence.UseSchemaTemplate() }

const testNow int64 = 1793196000000

var testBinding = Binding{Authority: "local", AccountID: "account", ProfileID: "profile", FamilyID: "family"}

func key(now int64, n int) string { return fmt.Sprintf("p251_%013d_%032x", now, n) }
func envelope(n int) Envelope {
	return Envelope{key(testNow, n), "update_personal_item", strings.Repeat("a", 64), false}
}
func fixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'00','profile')`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
func write(t *testing.T, db *sql.DB, fn func(context.Context, *sql.Tx) error) error {
	t.Helper()
	return dbwork.WithWriteTxContext(dbwork.WithDurableUser(context.Background()), db, dbwork.ClassInteractive, fn)
}
func apply(t *testing.T, db *sql.DB, b Binding, e Envelope, r Result, now int64) error {
	t.Helper()
	return write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		old, err := Check(ctx, tx, b, e, now)
		if err != nil || old != nil {
			return err
		}
		return Save(ctx, tx, b, e, r, now)
	})
}
func count(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestReplayConflictIsolationAndConcurrentDuplicate(t *testing.T) {
	db := fixture(t)
	e := envelope(1)
	result := Result{Status: "applied", TargetID: "item", Revision: 7}
	var effects atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
				r, err := Check(ctx, tx, testBinding, e, testNow)
				if err != nil || r != nil {
					return err
				}
				effects.Add(1)
				return Save(ctx, tx, testBinding, e, result, testNow)
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if effects.Load() != 1 {
		t.Fatalf("effects=%d", effects.Load())
	}
	r, err := Lookup(context.Background(), db, testBinding, e.Key, testNow)
	if err != nil || r == nil || r.Result != result {
		t.Fatalf("lookup %v %v", r, err)
	}
	raw, _ := json.Marshal(r)
	for _, private := range []string{"digest", "authority", "accountId", "profileId", "familyId"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("private field %s", raw)
		}
	}
	for _, change := range []string{"digest", "operation", "stop", "profile", "family"} {
		t.Run(change, func(t *testing.T) {
			b, other := testBinding, e
			switch change {
			case "digest":
				other.Digest = strings.Repeat("b", 64)
			case "operation":
				other.Operation = "delete_personal_item"
			case "stop":
				other.Stop = true
			case "profile":
				b.ProfileID = "other"
			case "family":
				b.FamilyID = "other"
			}
			if err := apply(t, db, b, other, result, testNow); !errors.Is(err, ErrConflict) {
				t.Fatalf("got %v", err)
			}
		})
	}
	for _, change := range []string{"profile", "family", "account"} {
		b := testBinding
		switch change {
		case "profile":
			b.ProfileID = "other"
		case "family":
			b.FamilyID = "other"
		case "account":
			b.AccountID = "other"
		}
		if r, err = Lookup(context.Background(), db, b, e.Key, testNow); err != nil || r != nil {
			t.Fatalf("scope disclosure %v %v", r, err)
		}
	}
	if got := count(t, db, `SELECT ordinary_count FROM user_mutation_global`); got != 1 {
		t.Fatalf("usage %d", got)
	}
}
func TestRollbackAndUnfinalizedReservationCannotCommit(t *testing.T) {
	db := fixture(t)
	e := envelope(2)
	sentinel := errors.New("rollback")
	for _, early := range []bool{true, false} {
		err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := Check(ctx, tx, testBinding, e, testNow); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE accounts SET display_name='changed' WHERE id='account'`); err != nil {
				return err
			}
			if early {
				return sentinel
			}
			return nil
		})
		want := ErrConflict
		if early {
			want = sentinel
		}
		if !errors.Is(err, want) {
			t.Fatalf("unfinished committed: %v", err)
		}
	}
	for _, query := range []string{`SELECT count(*) FROM user_mutation_receipts`, `SELECT count(*) FROM user_mutation_accounts`, `SELECT ordinary_count FROM user_mutation_global`, `SELECT count(*) FROM accounts WHERE display_name='changed'`} {
		if n := count(t, db, query); n != 0 {
			t.Fatalf("leak %s=%d", query, n)
		}
	}
	if n := count(t, db, `SELECT ordinary_bytes FROM user_mutation_global`); n != BaseChargeBytes {
		t.Fatalf("charge leaked %d", n)
	}
	if err := apply(t, db, testBinding, e, Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
}
func TestGrammarAndClosedResultBounds(t *testing.T) {
	for _, k := range []string{"", key(testNow, 1) + "a", strings.Replace(key(testNow, 1), "p251_", "p252_", 1), "p251_1793196000000_" + strings.Repeat("A", 32), key(testNow+FutureSkewMs+1, 1)} {
		if _, err := IssuedMs(k, testNow); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if _, err := IssuedMs(key(testNow-RetentionMs-1, 1), testNow); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	db := fixture(t)
	for i, r := range []Result{{Status: "response body"}, {Status: "applied", TargetID: strings.Repeat("x", 129)}, {Status: "applied", PositionStatus: "anything"}, {Status: "applied", Revision: -1}, {Status: "applied", Generation: 2147483648}} {
		if err := apply(t, db, testBinding, envelope(100+i), r, testNow); !errors.Is(err, ErrConflict) {
			t.Fatalf("%+v: %v", r, err)
		}
	}
	if err := apply(t, db, testBinding, Envelope{key(testNow, 200), "op", "wrong", false}, Result{Status: "applied"}, testNow); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}
}
func TestDurableCutoffAndClockRollback(t *testing.T) {
	db := fixture(t)
	e := envelope(3)
	if err := apply(t, db, testBinding, e, Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	future := testNow + RetentionMs + 1
	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		n, err := Prune(ctx, tx, future)
		if n != 1 {
			t.Fatalf("pruned %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	db = reopened

	for _, now := range []int64{testNow, testNow - 24*60*60*1000, future} {
		if err := apply(t, db, testBinding, e, Result{Status: "applied"}, now); !errors.Is(err, ErrExpired) {
			t.Fatalf("now=%d %v", now, err)
		}
		if _, err := Lookup(context.Background(), db, testBinding, e.Key, now); !errors.Is(err, ErrExpired) {
			t.Fatal(err)
		}
	}
	cutoff, err := Policy(context.Background(), db, testBinding, testNow)
	if err != nil || cutoff != testNow+1 {
		t.Fatalf("cutoff=%d %v", cutoff, err)
	}
	if _, err := db.Exec(`UPDATE user_mutation_global SET retired_before_ms=0`); err == nil {
		t.Fatal("SQL allowed cutoff rollback")
	}
}
func TestOwnerRecoveryBoundedAndNoUnknownAccountRegistration(t *testing.T) {
	db := fixture(t)
	for i := 1; i <= 40; i++ {
		if err := apply(t, db, testBinding, envelope(i), Result{Status: "applied"}, testNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return RetireAccount(ctx, tx, "local", "account", testNow+1)
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_receipts`); n != 8 {
		t.Fatalf("remaining=%d", n)
	}
	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		n, err := Prune(ctx, tx, testNow)
		if n != 8 {
			t.Fatalf("retired recovery drain=%d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT cleanup_pending FROM user_mutation_accounts`); n != 0 {
		t.Fatal("recovery never settles")
	}

	if err := apply(t, db, testBinding, envelope(1), Result{Status: "applied"}, testNow); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return RetireAccount(ctx, tx, "local", "missing", testNow+1)
	}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('fresh','fresh',X'00','p')`); err != nil {
		t.Fatal(err)
	}
	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return RetireAccount(ctx, tx, "local", "fresh", testNow+1)
	}); err != nil {
		t.Fatal(err)
	}
	fresh := testBinding
	fresh.AccountID = "fresh"
	fresh.ProfileID = "p"
	if err := apply(t, db, fresh, envelope(90), Result{Status: "applied"}, testNow); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT retired_before_ms FROM user_mutation_global`); n != testNow-RetentionMs {
		t.Fatalf("global recovery changed unrelated scope %d", n)
	}
}
func TestCountQuotaStopByteReservationAndBorrowing(t *testing.T) {
	db := fixture(t)
	if err := apply(t, db, testBinding, envelope(1), Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []string{"account_ordinary", "account_stop", "global_ordinary", "global_stop", "ordinary_bytes", "all_bytes"} {
		t.Run(boundary, func(t *testing.T) {
			rollback := errors.New("fixture rollback")
			err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
				e := envelope(2)
				query := ""
				switch boundary {
				case "account_ordinary":
					query = `UPDATE user_mutation_accounts SET ordinary_count=8192`
				case "account_stop":
					e.Stop = true
					query = `UPDATE user_mutation_accounts SET stop_count=512`
				case "global_ordinary":
					query = `UPDATE user_mutation_global SET ordinary_count=65536`
				case "global_stop":
					e.Stop = true
					query = `UPDATE user_mutation_global SET stop_count=4096`
				case "ordinary_bytes":
					query = `UPDATE user_mutation_global SET ordinary_bytes=31457280`
				case "all_bytes":
					e.Stop = true
					query = `UPDATE user_mutation_global SET ordinary_bytes=31457280,stop_bytes=2097152`
				}
				if _, err := tx.ExecContext(ctx, query); err != nil {
					return err
				}
				if _, err := Check(ctx, tx, testBinding, e, testNow); !errors.Is(err, ErrCapacity) {
					t.Fatalf("boundary %s %v", boundary, err)
				}
				if boundary == "ordinary_bytes" || boundary == "account_ordinary" || boundary == "global_ordinary" {
					e.Stop = true
					if _, err := Check(ctx, tx, testBinding, e, testNow); err != nil {
						t.Fatalf("Stop reservation %v", err)
					}
					if err := Save(ctx, tx, testBinding, e, Result{Status: "applied"}, testNow); err != nil {
						return err
					}
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE user_mutation_global SET stop_bytes=2097152`); err != nil {
			return err
		}
		e := envelope(99)
		e.Stop = true
		if _, err := Check(ctx, tx, testBinding, e, testNow); err != nil {
			return err
		}
		return Save(ctx, tx, testBinding, e, Result{Status: "applied"}, testNow)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestFamilyRetirementAndAccountErasureAreBounded(t *testing.T) {
	db := fixture(t)
	for i := 1; i <= 40; i++ {
		if err := apply(t, db, testBinding, envelope(i), Result{Status: "applied"}, testNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error { return RetireFamily(ctx, tx, testBinding) }); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_receipts`); n != 40 {
		t.Fatal("live family receipts pruned without retirement fence")
	}
	if _, err := db.Exec(`UPDATE user_mutation_accounts SET retired_before_ms=?`, testNow+1); err != nil {
		t.Fatal(err)
	}

	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error { return RetireFamily(ctx, tx, testBinding) }); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_receipts`); n != 8 {
		t.Fatalf("remaining=%d", n)
	}
	if _, err := db.Exec(`DELETE FROM accounts WHERE id='account'`); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_accounts WHERE account_id IS NOT NULL OR owner_account_id IS NOT NULL`); n != 0 {
		t.Fatal("identity retained")
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_receipts`); n != 8 {
		t.Fatal("unbounded erasure cascade")
	}
	if err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		n, err := Prune(ctx, tx, testNow)
		if n != 9 {
			t.Fatalf("removed=%d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT ordinary_bytes+stop_bytes FROM user_mutation_global`); n != BaseChargeBytes {
		t.Fatalf("leaked bytes=%d", n)
	}
}
func TestLinkedPorticoAccountSharesLocalQuota(t *testing.T) {
	db := fixture(t)
	if _, err := db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('account','portico-account')`); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, db, testBinding, envelope(1), Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	b := testBinding
	b.FamilyID = "portico-family"
	b.ProfileID = "portico-profile"
	if _, err := db.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES(?,'account','Portico profile')`, b.ProfileID); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, db, b, envelope(2), Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_accounts`); n != 1 {
		t.Fatal(n)
	}
	if n := count(t, db, `SELECT ordinary_count FROM user_mutation_accounts`); n != 2 {
		t.Fatal(n)
	}
}
func TestReceiptPhysicalBytesAndMaxRecord(t *testing.T) {
	for _, worst := range []bool{false, true} {
		t.Run(fmt.Sprint(worst), func(t *testing.T) {
			db := fixture(t)
			const n = 256
			b := testBinding
			if worst {
				b.ProfileID = strings.Repeat("p", 128)
				b.FamilyID = strings.Repeat("f", 128)
				if _, err := db.Exec(`INSERT INTO direct_profiles(id,account_id,name) VALUES(?,'account','Max profile')`, b.ProfileID); err != nil {
					t.Fatal(err)
				}
			}
			err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
				for i := 1; i <= n; i++ {
					e := envelope(i)
					r := Result{Status: "applied", TargetID: "session_0123456789abcdef", Revision: 1234, Generation: 3, PositionMs: 1800000, PositionStatus: "applied"}
					if worst {
						e.Operation = strings.Repeat("o", 96)
						r.TargetID = strings.Repeat("t", 128)
						r.Revision = 9007199254740991
						r.Generation = 2147483647
						r.PositionMs = 9007199254740991
						r.Status = "already_stopped"
						r.PositionStatus = "already_stopped"
					}
					if _, err := Check(ctx, tx, b, e, testNow); err != nil {
						return err
					}
					if err := Save(ctx, tx, b, e, r, testNow); err != nil {
						return err
					}
					if worst {
						if _, err := tx.ExecContext(ctx, `UPDATE user_mutation_receipts SET stop_item_id=? WHERE key=?`, strings.Repeat("i", 128), e.Key); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var bytes, maxRecord, payload int64
			err = db.QueryRow(`SELECT coalesce(sum(pgsize),0),coalesce(max(CASE WHEN name='user_mutation_receipts' THEN mx_payload ELSE 0 END),0),coalesce(sum(payload),0) FROM dbstat WHERE name LIKE 'user_mutation_%' OR name LIKE 'sqlite_autoindex_user_mutation_%'`).Scan(&bytes, &maxRecord, &payload)
			if err != nil {
				t.Fatalf("physical accounting unavailable: %v", err)
			}
			if maxRecord > MaxReceiptRecordBytes {
				t.Fatalf("max record=%d > %d", maxRecord, MaxReceiptRecordBytes)
			}
			charged := count(t, db, `SELECT ordinary_bytes+stop_bytes FROM user_mutation_global`)
			if bytes > charged || charged > GlobalByteLimit {
				t.Fatalf("physical=%d charged=%d", bytes, charged)
			}
			t.Logf("worst=%t receipts=%d physical table/index/account bytes=%d (%.1f/receipt), record bound=%d measured max=%d payload=%d charged=%d", worst, n, bytes, float64(bytes)/n, MaxReceiptRecordBytes, maxRecord, payload, charged)
		})
	}
}

func TestSnapshotReadJoinsActualMutationTransaction(t *testing.T) {
	db := fixture(t)
	e := envelope(400)
	err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := Check(ctx, tx, testBinding, e, testNow); err != nil {
			return err
		}
		if err := Save(ctx, tx, testBinding, e, Result{Status: "applied", Revision: 4}, testNow); err != nil {
			return err
		}
		r, err := Lookup(ctx, db, testBinding, e.Key, testNow)
		if err != nil {
			return err
		}
		if r == nil || r.Revision != 4 {
			t.Fatal("lookup escaped carried transaction")
		}
		_, err = Policy(ctx, db, testBinding, testNow)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestRolledBackSavepointReservationDoesNotBlockCommit(t *testing.T) {
	db := fixture(t)
	err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.Exec(`SAVEPOINT domain_attempt`); err != nil {
			return err
		}
		if _, err := Check(ctx, tx, testBinding, envelope(401), testNow); err != nil {
			return err
		}
		if _, err := tx.Exec(`ROLLBACK TO domain_attempt`); err != nil {
			return err
		}
		if _, err := tx.Exec(`RELEASE domain_attempt`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE accounts SET display_name='retained' WHERE id='account'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_receipts`); n != 0 {
		t.Fatal(n)
	}
	if n := count(t, db, `SELECT count(*) FROM accounts WHERE display_name='retained'`); n != 1 {
		t.Fatal(n)
	}
}
func TestSQLHardBoundsAndIndexedCleanup(t *testing.T) {
	db := fixture(t)
	if err := apply(t, db, testBinding, envelope(402), Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{`status='body'`, `position_status='body'`, `target_id=replace(hex(zeroblob(65)),'0','x')`, `stop_item_id=replace(hex(zeroblob(65)),'0','x')`, `stop_item_id=char(0)`, `operation=replace(hex(zeroblob(49)),'0','x')`, `profile_id=char(0)`, `generation=-1`, `revision=9007199254740992`, `digest='x'`} {
		if _, err := db.Exec(`UPDATE user_mutation_receipts SET ` + set); err == nil {
			t.Fatalf("unbounded SQL accepted %s", set)
		}
	}
	for _, query := range []string{
		`SELECT key FROM user_mutation_receipts WHERE issued_ms<1793196000001 ORDER BY issued_ms,key LIMIT 32`,
		`SELECT key FROM user_mutation_receipts WHERE scope_id=1 AND issued_ms<1793196000001 ORDER BY issued_ms,key LIMIT 32`,
		`SELECT key FROM user_mutation_receipts WHERE scope_id=1 AND family_id='family' AND issued_ms<1793196000001 ORDER BY issued_ms,key LIMIT 32`,
		`SELECT id FROM user_mutation_accounts WHERE owner_account_id IS NULL LIMIT 1`,
	} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN ` + query)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, "INDEX") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("cleanup lost indexed bound: %s", plan)
		}
	}
}
func TestSparseMaxAccountPhysicalAccounting(t *testing.T) {
	db := fixture(t)
	const n = 64
	err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for i := 1; i <= n; i++ {
			account := fmt.Sprintf("%0128x", i)
			if _, err := tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,X'00',?)`, account, account, account); err != nil {
				return err
			}
			b := Binding{Authority: "local", AccountID: account, ProfileID: account, FamilyID: strings.Repeat("f", 128)}
			e := envelope(i)
			e.Operation = strings.Repeat("o", 96)
			if _, err := Check(ctx, tx, b, e, testNow); err != nil {
				return err
			}
			if err := Save(ctx, tx, b, e, Result{Status: "already_stopped", TargetID: strings.Repeat("t", 128), Revision: 9007199254740991, Generation: 2147483647, PositionMs: 9007199254740991, PositionStatus: "already_stopped"}, testNow); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE user_mutation_receipts SET stop_item_id=? WHERE key=?`, strings.Repeat("i", 128), e.Key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var physical, maxRecord int64
	err = db.QueryRow(`SELECT sum(pgsize),max(CASE WHEN name='user_mutation_receipts' THEN mx_payload ELSE 0 END) FROM dbstat WHERE name LIKE 'user_mutation_%' OR name LIKE 'sqlite_autoindex_user_mutation_%'`).Scan(&physical, &maxRecord)
	if err != nil {
		t.Fatal(err)
	}
	charged := count(t, db, `SELECT ordinary_bytes+stop_bytes FROM user_mutation_global`)
	if physical > charged || maxRecord > MaxReceiptRecordBytes {
		t.Fatalf("physical=%d charged=%d record=%d", physical, charged, maxRecord)
	}
	t.Logf("sparse-max accounts=%d receipts=%d physical ledger+all indexes=%d (%.1f/receipt+account), record=%d charged=%d", n, n, physical, float64(physical)/n, maxRecord, charged)
}

func TestCompactDomainStatuses(t *testing.T) {
	db := fixture(t)
	for i, status := range []string{"applied", "already_stopped", "conflict", "invalid", "terminal", "deleted", "removed", "partial"} {
		e := envelope(500 + i)
		if err := apply(t, db, testBinding, e, Result{Status: status}, testNow); err != nil {
			t.Fatalf("status %s: %v", status, err)
		}
		r, err := Lookup(context.Background(), db, testBinding, e.Key, testNow)
		if err != nil || r == nil || r.Status != status {
			t.Fatalf("status %s: %v %v", status, r, err)
		}
	}
}

func TestClosedReceiptWireSchema(t *testing.T) {
	schema := (Receipt{}).JSONSchema()
	if schema["additionalProperties"] != false {
		t.Fatal("receipt shape is open")
	}
	properties := schema["properties"].(map[string]any)
	if len(properties) != 10 {
		t.Fatalf("unexpected receipt fields: %v", properties)
	}
	for _, field := range []string{"digest", "binding", "authority", "accountId", "profileId", "familyId", "response", "body"} {
		if _, ok := properties[field]; ok {
			t.Fatalf("private/body field: %s", field)
		}
	}
	for _, status := range properties["status"].(map[string]any)["enum"].([]string) {
		if !validStatus(status) {
			t.Fatalf("wire status diverged: %s", status)
		}
	}
	for _, status := range properties["positionStatus"].(map[string]any)["enum"].([]string) {
		if !validPositionStatus(status) {
			t.Fatalf("wire position status diverged: %s", status)
		}
	}
}
