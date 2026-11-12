package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAnOldTestFolderIsRefusedBeforeServing(t *testing.T) {
	ctx := context.Background()
	for _, change := range []string{"", `DROP TABLE track_embeddings`, `ALTER TABLE items RENAME COLUMN tagline TO older_name`} {
		path := filepath.Join(t.TempDir(), "main.db")
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
		conn, err := open(path, "rw", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if change != "" {
			if _, err := conn.Exec(change); err != nil {
				t.Fatal(err)
			}
		}
		conn.Close()
		d, err = Open(ctx, path)
		if d != nil {
			d.Close()
		}
		if change == "" && err != nil || change != "" && !errors.Is(err, ErrOldTestFolder) {
			t.Fatalf("after %q: %v", change, err)
		}
	}
}
