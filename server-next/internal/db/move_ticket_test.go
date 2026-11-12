package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A move ticket keeps a mark of the password that made it (db.PasswordMark): a password changed
// since, by any path, makes the ticket stand for nothing at the peek and at the move.
func TestAPasswordChangedSinceVoidsAMoveTicketsMark(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Now()
	session, err := d.CreateOwner(ctx, db.NewOwner{Username: "owner", PasswordHash: "hash-one", ProfileName: "Owner", Now: now,
		Session: db.NewSession{ID: db.NewSessionID(), RefreshHash: "r", ExpiresAt: now.Add(24 * time.Hour),
			Device: db.DeviceInfo{InstallationID: "test", Name: "Test", Platform: "web", App: "web", AppVersion: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	mark := db.PasswordMark("hash-one")
	if stands, err := d.PasswordStands(ctx, session.UserID, mark); err != nil || !stands {
		t.Fatalf("the password a ticket was made with does not stand: %v %v", stands, err)
	}
	if err := d.ExecForTest(ctx, `UPDATE users SET password_hash = 'hash-two' WHERE id = ?`, session.UserID); err != nil {
		t.Fatal(err)
	}
	if stands, _ := d.PasswordStands(ctx, session.UserID, mark); stands {
		t.Fatal("a ticket still stands after its account's password changed")
	}
	if stands, _ := d.PasswordStands(ctx, session.UserID+999, mark); stands {
		t.Fatal("a ticket stands for an account that does not exist")
	}
}
