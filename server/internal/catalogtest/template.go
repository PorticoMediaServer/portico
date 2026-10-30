package catalogtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Templates owns closed fixture snapshots for one test package. TestMain must
// create it after helper dispatch and Close it after m.Run. Builders may only
// populate database state: files, identities and running services belong to each
// caller. Every copy gets its own handle, worker, names map and signing keys.
type Templates struct {
	mu        sync.Mutex
	snapshots map[string]snapshot
	closed    bool
}
type snapshot struct {
	data  []byte
	names Names
}

func NewTemplates() *Templates { return &Templates{snapshots: make(map[string]snapshot)} }

func (s *Templates) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.snapshots = nil
}

// Open copies a lazily built immutable snapshot. The first caller's builder is
// closed before its bytes are captured; neither WAL files nor live workers are
// shared. Builders register their usual test cleanup; a second Close is harmless.
func (s *Templates) Open(t *testing.T, key string, build func(*testing.T) (*Catalog, Names)) (*Catalog, Names) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		t.Fatal("fixture templates already closed")
	}
	cached, ok := s.snapshots[key]
	if !ok {
		c, names := build(t)
		var path string
		var seq int
		var schema string
		rows, err := c.DB.Query(`PRAGMA database_list`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			if err = rows.Scan(&seq, &schema, &path); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if schema == "main" {
				break
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if path == "" {
			t.Fatal("fixture template requires a file database")
		}
		var busy, log, checkpointed int
		if err = c.DB.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 {
			t.Fatalf("checkpoint fixture: %d %v", busy, err)
		}
		if err = c.DB.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cached = snapshot{data: data, names: cloneNames(names)}
		s.snapshots[key] = cached
	}
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	if err := os.WriteFile(path, cached.data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Match fresh-install randomness rather than copying cursor capabilities.
	for _, name := range []string{"chapter_cursor_key", "activity_cursor_key", "catalog_cursor_key"} {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(context.Background(), `UPDATE configuration SET value=? WHERE key=?`, hex.EncodeToString(key), name); err != nil {
			t.Fatal(err)
		}
	}
	return New(t, db), cloneNames(cached.names)
}
func cloneNames(names Names) Names {
	out := make(Names, len(names))
	for k, v := range names {
		out[k] = v
	}
	return out
}
