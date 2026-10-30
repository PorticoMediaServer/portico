package networking

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func identityStoreFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "current.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyNetworkingClaimsForTest(t, context.Background(), db); e != nil {
		t.Fatal(e)
	}
	return db
}
func identityCandidate(t *testing.T, incarnation string) DurableIdentity {
	t.Helper()
	pub, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	id, e := ServerIdentity(pub)
	if e != nil {
		t.Fatal(e)
	}
	return DurableIdentity{ServerID: id, PublicKey: pub, KeyIncarnation: incarnation}
}
func stageIdentityFixture(t *testing.T, db *sql.DB, expected *IdentitySnapshot, next DurableIdentity) IdentitySnapshot {
	t.Helper()
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	v, e := StageIdentityTx(context.Background(), tx, expected, next)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestClaimIdentityStoreFreshRestartAndImmutability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := identityStoreFixture(t)
	key := identityCandidate(t, "key1")
	v := stageIdentityFixture(t, db, nil, key)
	if v.ResetGeneration != 0 || !sameIdentity(v.DurableIdentity, key) {
		t.Fatal("wrong initial pointer")
	}
	again := stageIdentityFixture(t, db, &v, key)
	if again.ResetGeneration != 0 {
		t.Fatal("restart changed reset generation")
	}
	if e := verifyNetworkingClaimsForTest(t, context.Background(), db); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE networking_server_identities SET key_incarnation='different'`); e == nil {
		t.Fatal("immutable key mutated")
	}
	if _, e := db.Exec(`DELETE FROM networking_server_identities`); e == nil {
		t.Fatal("immutable history deleted")
	}
	if _, e := db.Exec(`UPDATE networking_claim_identity SET public_key=zeroblob(32)`); e == nil {
		t.Fatal("pointer/key FK missing")
	}
}
func TestClaimIdentityStoreReplacementRetainsHistory(t *testing.T) {
	db := identityStoreFixture(t)
	a := stageIdentityFixture(t, db, nil, identityCandidate(t, "key1"))
	// An acknowledged terminal operation is immutable history. Crypto proof
	// validation belongs to the accepted Store, not this pointer transaction test.
	if _, e := db.Exec(`INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,created_at,updated_at) VALUES('old',?,'account',?,0,3,'cancelled','owner','profile',1,'now','now')`, a.ServerID, a.PublicKey); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO networking_claim_cancellations(operation_id,request_id,mode,claim_generation,payload,signature,state,created_at,acknowledged_at) VALUES('old','cancel1','intent','',x'01',zeroblob(64),'acknowledged','now','now')`); e != nil {
		t.Fatal(e)
	}
	b := stageIdentityFixture(t, db, &a, identityCandidate(t, "key2"))
	if b.ResetGeneration != 1 || b.ServerID == a.ServerID {
		t.Fatal("replacement did not change identity")
	}
	var oldServer string
	if e := db.QueryRow(`SELECT server_id FROM networking_claim_intents WHERE operation_id='old'`).Scan(&oldServer); e != nil || oldServer != a.ServerID {
		t.Fatal("old operation lost original identity")
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM networking_claim_cancellations WHERE operation_id='old'`).Scan(&n); e != nil || n != 1 {
		t.Fatal("old cancellation disappeared")
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = StageIdentityTx(context.Background(), tx, &b, a.DurableIdentity); e == nil {
		t.Fatal("historical key reused")
	}
}
func TestClaimIdentityStoreRejectsWrongPointerAndChosenID(t *testing.T) {
	db := identityStoreFixture(t)
	a := stageIdentityFixture(t, db, nil, identityCandidate(t, "key1"))
	next := identityCandidate(t, "key2")
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	wrong := a
	wrong.ResetGeneration++
	if _, e = StageIdentityTx(context.Background(), tx, &wrong, next); !errors.Is(e, ErrStale) {
		t.Fatal("stale generation accepted")
	}
	bad := next
	bad.ServerID = a.ServerID
	if _, e = StageIdentityTx(context.Background(), tx, &a, bad); !errors.Is(e, ErrInvalid) {
		t.Fatal("chosen ID with different key accepted")
	}
	if _, e = StageIdentityTx(context.Background(), tx, nil, next); !errors.Is(e, ErrStale) {
		t.Fatal("fresh publication overwrote current identity")
	}
	var n int
	if e = tx.QueryRow(`SELECT count(*) FROM networking_server_identities`).Scan(&n); e != nil || n != 1 {
		t.Fatal("failed publication wrote a key")
	}
}
func TestClaimIdentityStorePendingAndHostileOperation(t *testing.T) {
	db := identityStoreFixture(t)
	a := stageIdentityFixture(t, db, nil, identityCandidate(t, "key1"))
	next := identityCandidate(t, "key2")
	if _, e := db.Exec(`UPDATE networking_claim_identity SET active_operation_id='missing'`); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = StageIdentityTx(context.Background(), tx, &a, a.DurableIdentity); !errors.Is(e, ErrInvalid) {
		t.Fatal("hostile active reference accepted")
	}
	tx.Rollback()
	if _, e = db.Exec(`UPDATE networking_claim_identity SET active_operation_id=NULL`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,created_at,updated_at) VALUES('pending',?,'account',?,0,1,'prepared','owner','profile',1,'now','now')`, a.ServerID, a.PublicKey); e != nil {
		t.Fatal(e)
	}
	tx, e = db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = StageIdentityTx(context.Background(), tx, &a, next); !errors.Is(e, ErrStale) {
		t.Fatal("retained nonterminal operation permitted replacement")
	}
}
func TestClaimIdentityStoreOverflowAndCancellation(t *testing.T) {
	db := identityStoreFixture(t)
	a := stageIdentityFixture(t, db, nil, identityCandidate(t, "key1"))
	next := identityCandidate(t, "key2")
	if _, e := db.Exec(`UPDATE networking_claim_identity SET reset_generation=?`, int64(math.MaxInt64)); e != nil {
		t.Fatal(e)
	}
	a.ResetGeneration = math.MaxInt64
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = StageIdentityTx(context.Background(), tx, &a, next); !errors.Is(e, ErrStale) {
		t.Fatal("reset overflow accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = StageIdentityTx(ctx, tx, &a, next); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled staging succeeded")
	}
	tx.Rollback()
}

func TestClaimIdentityStoreRefusesDamagedMarkedSchema(t *testing.T) {
	for name, damage := range map[string]string{
		"table":       `DROP TABLE networking_claim_cancellations`,
		"trigger":     `DROP TRIGGER networking_identity_no_update`,
		"index":       `DROP INDEX networking_claim_one_active; CREATE INDEX networking_claim_one_active ON networking_claim_intents(account_id)`,
		"foreign_key": `DROP TABLE networking_claim_credentials; CREATE TABLE networking_claim_credentials(operation_id TEXT PRIMARY KEY,server_id TEXT NOT NULL,local_generation INTEGER NOT NULL,claim_generation TEXT NOT NULL,credential_generation TEXT NOT NULL,nonce BLOB NOT NULL,ciphertext BLOB NOT NULL,installed_at TEXT NOT NULL)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := identityStoreFixture(t)
			if _, e := db.Exec(damage); e != nil {
				t.Fatal(e)
			}
			if e := verifyNetworkingClaimsForTest(t, context.Background(), db); !errors.Is(e, persistence.ErrNetworkingClaimSchema) {
				t.Fatalf("damaged marked schema accepted: %v", e)
			}
		})
	}
}
func TestClaimIdentityStoreReplaceCannotRewriteHistory(t *testing.T) {
	for _, recursive := range []string{"OFF", "ON"} {
		t.Run(recursive, func(t *testing.T) {
			db := identityStoreFixture(t)
			v := stageIdentityFixture(t, db, nil, identityCandidate(t, "key1"))
			if _, e := db.Exec(`PRAGMA recursive_triggers=` + recursive); e != nil {
				t.Fatal(e)
			}
			if _, e := db.Exec(`INSERT OR REPLACE INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,'replacement','now')`, v.ServerID, v.PublicKey); e == nil {
				t.Fatal("REPLACE rewrote immutable identity")
			}
			var incarnation string
			if e := db.QueryRow(`SELECT key_incarnation FROM networking_server_identities WHERE server_id=?`, v.ServerID).Scan(&incarnation); e != nil || incarnation != "key1" {
				t.Fatal("identity history changed")
			}
		})
	}
}

func TestClaimIdentityStoreRefusesHostileFreshNamedObject(t *testing.T) {
	db := openFixtureDB(t, filepath.Join(t.TempDir(), "hostile.sqlite"))
	if _, e := db.Exec(`CREATE TABLE unrelated(id INTEGER); CREATE TRIGGER networking_identity_no_update BEFORE UPDATE ON unrelated BEGIN SELECT 1; END;`); e != nil {
		t.Fatal(e)
	}
	// Migration 0042 adopts objects by name, so a same-named object of another
	// shape survives it; verification at startup must then refuse the database.
	if e := installNetworkingClaimsForTest(t, context.Background(), db); !errors.Is(e, persistence.ErrNetworkingClaimSchema) {
		t.Fatalf("hostile preexisting trigger accepted: %v", e)
	}
	if e := persistence.VerifyNetworkingClaims(context.Background(), db); !errors.Is(e, persistence.ErrNetworkingClaimSchema) {
		t.Fatalf("startup verification accepted a hostile trigger: %v", e)
	}
}

func TestIdentityCodeTxReadsCurrentPublicationWithoutStoredCode(t *testing.T) {
	db := identityStoreFixture(t)
	defer db.Close()
	ctx := context.Background()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	server, e := ServerIdentity(raw)
	if e != nil {
		t.Fatal(e)
	}
	stageIdentityFixture(t, db, nil, DurableIdentity{ServerID: server, PublicKey: raw, KeyIncarnation: "identity-code-vector"})
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	code, e := IdentityCodeTx(ctx, tx)
	if e != nil || code != "JJWY-C8J8-J26F" {
		t.Fatal("current identity-code vector mismatch", e)
	}
	if _, e = IdentityCodeTx(ctx, nil); e == nil {
		t.Fatal("nil identity transaction accepted")
	}
}
