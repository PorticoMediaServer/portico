package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A swap that fails partway (not a stop: an error, such as a file another program holds open
// on Windows, a disk fault, or a disk that fills) must not leave the data folder without its
// database, because the start then goes on to open "main.db", which makes a new empty one:
// a server anyone on the network can claim through first-run setup, with the owner's
// database left in restore-previous and never put back.
//
// Here the failure is a main.db-shm that cannot be moved (a folder its owner may not write
// to; not as root). Any error between "the original moved aside" and "the backup's database
// moved in" does the same.
func TestASwapThatFailsPartwayLeavesTheOwnersDatabaseInPlace(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can move anything")
	}
	dir := stagedServer(t)
	stuck := filepath.Join(dir, "main.db-shm")
	os.Mkdir(stuck, 0o500)
	t.Cleanup(func() { os.Chmod(stuck, 0o700); os.Chmod(filepath.Join(dir, previousDir, "main.db-shm"), 0o700) })

	_, note, err := Prepare(dir, time.Now())
	if err == nil && !strings.Contains(note, "could not be made") {
		t.Skip("the swap did not fail on this system, so there is nothing to test")
	}
	// The start stops on an error; a swap that could not begin has put the original back.
	if read(dir, "main.db") != "the database in use" {
		t.Errorf("after a failed swap the data folder's main.db reads %q, the original is in restore-previous: %v, and the start opens a new empty database",
			read(dir, "main.db"), exists(filepath.Join(dir, previousDir, "main.db")))
	}

	// And it stays that way: once the start has made its new empty database, the next start
	// "undoes" the journal without putting the original back, because a main.db is in place.
	os.Chmod(stuck, 0o700)
	os.Remove(stuck)
	if !exists(filepath.Join(dir, "main.db")) {
		d, err := db.Open(context.Background(), filepath.Join(dir, "main.db")) // as the start does
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
	if _, _, err := Prepare(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if read(dir, "main.db") != "the database in use" {
		t.Errorf("at the next start the owner's database is still not back (it is in restore-previous: %v) and the server is a new empty one",
			exists(filepath.Join(dir, previousDir, "main.db")))
	}
}

// A restore staged "after" the start (the clock was wrong at one end: a machine with no
// clock battery, a container) has an age nobody can know, so it is not made.
func TestAStagedRestoreFromTheFutureIsNotMade(t *testing.T) {
	dir := stagedServer(t)
	restored, _, err := Prepare(dir, time.Now().Add(-30*24*time.Hour))
	if err != nil || restored != "" || read(dir, "main.db") != "the database in use" {
		t.Errorf("a restore staged a month after this start's clock was made: %q %v", restored, err)
	}
}
