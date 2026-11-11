package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// A book's address lives for hours, longer than the server remembers that a sign-in ended. Its
// check reads the sign-in itself, so a signed-out sign-in's address stays dead after the
// server has forgotten the sign-out.
func TestABookAddressStaysDeadAfterItsEndedSignInIsForgotten(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := seed.Import(ctx, d, seed.Config{Seed: 3, Items: 300}, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	session, err := d.CreateOwner(ctx, db.NewOwner{Username: "owner", PasswordHash: "x", ProfileName: "Owner", Now: now,
		Session: db.NewSession{ID: db.NewSessionID(), RefreshHash: "r", ExpiresAt: now.Add(24 * time.Hour),
			Device: db.DeviceInfo{InstallationID: "test", Name: "Test", Platform: "web", App: "web", AppVersion: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := d.Profiles(ctx, session.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if session, err = d.SelectProfile(ctx, db.ProfileSelection{UserID: session.UserID, SessionID: session.ID, ProfileID: profiles[0].ID, Now: now}); err != nil {
		t.Fatal(err)
	}
	v := db.Viewer{UserID: session.UserID, ProfileID: profiles[0].ID}
	var book db.Book
	libraries, _ := d.AllLibraries(ctx)
	for _, lib := range libraries {
		page, err := d.Items(ctx, v, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindBook}, Limit: 1})
		if err == nil && len(page.Items) == 1 {
			if book, err = d.Book(ctx, v, page.Items[0].PublicID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(book.Versions) == 0 {
		t.Fatal("the seeded library has no book with a file")
	}
	open := func() bool {
		visible, _, err := d.BookAccess(ctx, v, session.ID, book.ID, book.Versions[0].FileID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return visible
	}
	if !open() {
		t.Fatal("a live sign-in's address was refused")
	}
	if err := d.RevokeSession(ctx, session.ID, "signed_out", time.Now()); err != nil {
		t.Fatal(err)
	}
	db.ForgetEndedSignIns(d)
	if d.TokenRefused(session.ID, session.Stamp) {
		t.Fatal("the hook did not make the server forget the sign-out")
	}
	if open() {
		t.Fatal("a signed-out sign-in's address works once the server has forgotten the sign-out")
	}
}
