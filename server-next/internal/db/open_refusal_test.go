package db

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// When the version cannot be read, the refusal says what failed. It does not tell the
// person to delete their database.
func TestAFailureToReadTheVersionIsReportedAsItself(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "reading the database's version failed") || strings.Contains(err.Error(), "delete") {
		t.Fatalf("Open says %v", err)
	}
}
