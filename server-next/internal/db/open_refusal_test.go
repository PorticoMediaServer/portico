package db

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A file in the data folder that Portico did not make is refused and left exactly as it
// was: not switched to WAL mode, and with no new files beside it.
func TestAFileThatIsNotOursIsRefusedUnchanged(t *testing.T) {
	ctx := context.Background()
	for name, want := range map[string]string{"another program's database": "Portico did not make", "not a database": "could not be read as a database"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "main.db")
		if name == "not a database" {
			if err := os.WriteFile(path, bytes.Repeat([]byte("holiday photographs "), 400), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			other, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := other.Exec(`CREATE TABLE recipes (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO recipes (name) VALUES ('soup')`); err != nil {
				t.Fatal(err)
			}
			other.Close()
		}
		before, _ := os.ReadFile(path)
		_, err := Open(ctx, path)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "nothing in it has been changed") {
			t.Errorf("%s: Open says %v, want it to say %q", name, err, want)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Errorf("%s: the file changed (bytes 18 and 19 were %v, are %v)", name, before[18:20], after[18:20])
		}
		if left, _ := os.ReadDir(dir); len(left) != 1 {
			t.Errorf("%s: %d files in the folder afterwards, want only the one that was there", name, len(left))
		}
	}
}

// A version table from another test schema is refused before any version-dependent read.
func TestAnOldVersionTableIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	other, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(`CREATE TABLE schema_migrations (release TEXT)`); err != nil {
		t.Fatal(err)
	}
	other.Close()
	_, err = Open(context.Background(), path)
	if err != ErrOldTestFolder {
		t.Fatalf("Open says %v", err)
	}
}

// holdExclusively opens the database the way a tool that wants it to itself does, so that
// nothing else can even read it, and returns what lets go of it.
func holdExclusively(t *testing.T, path string) func() {
	t.Helper()
	other, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	other.SetMaxOpenConns(1)
	if _, err := other.Exec(`PRAGMA locking_mode = EXCLUSIVE; BEGIN IMMEDIATE; UPDATE libraries SET revision = revision; COMMIT`); err != nil {
		t.Fatal(err)
	}
	return func() { other.Close() }
}

// A server started while the one before it is still letting go of the database waits for
// it, as every other connection does, and starts.
func TestOpenWaitsOutADatabaseThatIsBrieflyBusy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	release := holdExclusively(t, path)
	began := time.Now()
	time.AfterFunc(400*time.Millisecond, release)
	d, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("opening a database that was busy for a moment: %v", err)
	}
	d.Close()
	if waited := time.Since(began); waited < 300*time.Millisecond {
		t.Fatalf("Open returned after %s, before the database was let go: the test held nothing", waited)
	}
}

// A database something else keeps hold of is refused for what it is: in use. It is never
// called unreadable, and it is not touched.
func TestADatabaseHeldBySomethingElseIsRefusedAsInUse(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	usual := busyTimeoutMs
	busyTimeoutMs = 300
	t.Cleanup(func() { busyTimeoutMs = usual })
	release := holdExclusively(t, path)
	defer release()
	_, err = Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "another Portico server or tool has the database") || !strings.Contains(err.Error(), "nothing in it has been changed") ||
		strings.Contains(err.Error(), "could not be read as a database") {
		t.Fatalf("opening a database held by something else: %v", err)
	}
	release()
	if d, err = Open(ctx, path); err != nil {
		t.Fatalf("opening it once it was let go: %v", err)
	}
	d.Close()
}
