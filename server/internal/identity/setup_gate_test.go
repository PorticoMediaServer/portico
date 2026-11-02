package identity_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The remote gate's "still unset?" answer closes on any committed write that creates an
// owner, by whichever path and with nothing reloaded: local setup, and a raw insert
// standing in for a path added later.
func TestUnconfiguredClosesOnAnyOwnerWrite(t *testing.T) {
	open := func(t *testing.T) (*sql.DB, *identity.Service) {
		root := t.TempDir()
		db, err := persistence.Open(filepath.Join(root, "db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		s, err := identity.New(db, root)
		if err != nil {
			t.Fatal(err)
		}
		operations.WireSetupRemote(s)
		if !s.Unconfigured() || !s.Unconfigured() {
			t.Fatal("a fresh server is unconfigured")
		}
		return db, s
	}
	t.Run("local setup", func(t *testing.T) {
		_, s := open(t)
		token, err := s.BrowserSetupToken(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Setup(token, "owner", "A-long-local-password", "Den"); err != nil {
			t.Fatal(err)
		}
		if s.Unconfigured() {
			t.Fatal("still unconfigured right after local setup")
		}
	})
	t.Run("any committed accounts write", func(t *testing.T) {
		db, s := open(t)
		// Another table's write leaves it unset.
		if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO configuration VALUES('fixture','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if !s.Unconfigured() {
			t.Fatal("another table's write closed the gate")
		}
		if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('a-gate','owner',X'00','p-gate')`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if s.Unconfigured() {
			t.Fatal("still unconfigured right after an owner was written")
		}
	})
}
