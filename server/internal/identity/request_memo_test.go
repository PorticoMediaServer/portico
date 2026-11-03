package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A request proves its authority once per moment across its read snapshots, but
// any transaction that writes proves it again in that transaction, so a
// revocation is never missed by a change.
func TestARequestsWritesSeeARevocationItsReadsRemember(t *testing.T) {
	db, service, principal := transactionAuthorityFixture(t)
	request := dbwork.WithRequestMemo(context.Background())
	readFamily := func() error {
		return dbwork.WithReadSnapshot(request, db, func(snapshot context.Context) error {
			_, err := service.SessionFamilyTx(snapshot, dbwork.Snapshot(snapshot), principal)
			return err
		})
	}
	if err := readFamily(); err != nil {
		t.Fatal(err)
	}
	before := dbwork.RequestMemoHits()
	if err := readFamily(); err != nil {
		t.Fatal(err)
	}
	if dbwork.RequestMemoHits() == before {
		t.Fatal("a second read snapshot of the same request proved its authority again")
	}
	// Another request revokes this session.
	if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassSecurityFence, `UPDATE authorization_session_families SET revoked=1 WHERE id IN (SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, principal.Hash); err != nil {
		t.Fatal(err)
	}
	// This request's reads may reuse their answer for at most
	// dbwork.RequestMemoLifetime; its next change proves authority afresh.
	if err := readFamily(); err != nil {
		t.Fatalf("the remembered answer was not reused: %v", err)
	}
	err := dbwork.WithWriteTx(request, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := service.ReauthorizeTx(request, tx, principal)
		return err
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a write after the revocation was authorized: %v", err)
	}
	// And having written, the request's reads prove afresh too.
	if err := readFamily(); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a read after the request wrote reused a revoked answer: %v", err)
	}
}
