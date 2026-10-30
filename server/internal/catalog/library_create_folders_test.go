package catalog

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestCreateLibraryFoldersIsAtomicAndNeverTouchesMedia(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	ctx := context.Background()
	a, b := t.TempDir(), t.TempDir()
	fail := errors.New("refuse creation")
	if _, err = s.CreateFoldersAuthorizedThen(ctx, "Films", "movie", []string{a, b}, func(*sql.Tx) error { return fail }, nil); !errors.Is(err, fail) {
		t.Fatalf("authority: %v", err)
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM libraries`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("denied library persisted: %d %v", n, err)
	}
	if _, err = s.CreateFoldersAuthorizedThen(ctx, "Films", "movie", []string{a, filepath.Join(b, "missing")}, nil, nil); err == nil {
		t.Fatal("missing second folder admitted")
	}
	if _, err = s.CreateFoldersAuthorizedThen(ctx, "Films", "movie", []string{a, a}, nil, nil); !errors.Is(err, ErrSourceAlreadyConfigured) {
		t.Fatalf("duplicate folders: %v", err)
	}
	if _, err = s.CreateFoldersAuthorizedThen(ctx, "Films", "movie", []string{a, b}, nil, func(*sql.Tx, Library) error { return fail }); !errors.Is(err, fail) {
		t.Fatalf("callback: %v", err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM libraries`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial creation: %d %v", n, err)
	}
	library, err := s.CreateFoldersAuthorizedThen(ctx, "Films", "movie", []string{a, b}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM library_sources WHERE library_id=? AND enabled=1 AND identity_confirmed=1`, library.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("folders %d %v", n, err)
	}
	for _, path := range []string{a, b} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatalf("media modified: %s %v", path, err)
		}
	}
	for _, kind := range []string{"book", "comic", "audiobook"} {
		if _, err = s.CreateFoldersAuthorizedThen(ctx, kind, kind, []string{t.TempDir()}, nil, nil); err != nil {
			t.Fatalf("kind %s: %v", kind, err)
		}
	}
}
