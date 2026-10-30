//go:build linux || darwin

package subtitlevideo

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSubtitleSessionUsesAcceptedPlaybackBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	c := catalogtest.Open(t)
	root := filepath.Clean(t.TempDir())
	path := filepath.Join(root, "movie.mp4")
	if err := os.WriteFile(path, []byte("original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	object := func(path string) string {
		t.Helper()
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		id, err := storage.FileObjectIdentity(file)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	library := c.Library("library", "Movies", "movie", root)
	item := c.Movie(library, path, "Movie", 2024)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"size": info.Size(), "modified_ns": info.ModTime().UnixNano()})
	})
	c.Drain()
	rootIdentity := object(root)
	identity := storage.StableObjectIdentity(object(path), rootIdentity)
	c.Exec(`UPDATE library_sources SET root_identity=?,identity_confirmed=1,follow_symlinks=0 WHERE id='library'`, rootIdentity)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns) SELECT 'object',id,?,incarnation,'movie.mp4',?,'revision','{}',?,? FROM library_sources WHERE id='library'`, item.Token, identity, info.Size(), info.ModTime().UnixNano())
	c.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('session','login','account','profile',?,?,1,'playing','grant-hash','grant','2100-01-01T00:00:00Z','request',60)`, item.ID, item.Token)
	c.Exec(`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('session',?,?,?)`, item.Token, info.Size(), info.ModTime().UnixNano())
	c.Exec(`INSERT INTO playback_physical_source_pins SELECT 'session',id,incarnation,generation FROM library_sources WHERE id='library'`)
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := assets.PinPlaybackBindingTx(ctx, tx, "session"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := storage.New(binary)
	life, cancel := context.WithCancel(ctx)
	runtime := &Runtime{db: c.DB, storage: client, roots: sourceaccess.New(client, nil), life: life}
	t.Cleanup(func() {
		cancel()
		stop, done := context.WithTimeout(ctx, 3*time.Second)
		defer done()
		if err := runtime.roots.Shutdown(stop); err != nil {
			t.Error(err)
		}
	})
	read := func() {
		t.Helper()
		input, err := runtime.OpenSubtitleInput(ctx, item.Public, item.Token, "session")
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		data, err := input.ReadExtent(ctx, 0, info.Size())
		if err != nil || string(data) != "original bytes" {
			t.Fatal(string(data), err)
		}
	}
	read()
	// An unrelated rescan keeps the accepted object readable.
	c.Exec(`UPDATE inventory_objects SET revision='rescan'`)
	read()
	if err := os.Rename(path, path+".accepted"); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement")
	if err := os.WriteFile(replacement, []byte("replaced bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE inventory_objects SET object_identity=?`, storage.StableObjectIdentity(object(path), rootIdentity))
	if input, err := runtime.OpenSubtitleInput(ctx, item.Public, item.Token, "session"); err == nil {
		input.Close()
		t.Fatal("subtitle session followed replacement")
	}
	c.Exec(`DELETE FROM inventory_objects`)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".accepted", path); err != nil {
		t.Fatal(err)
	}
	read()
	// Replacing inventory metadata alone must never authorize a different path.
	if source, err := runtime.selection(ctx, item.Public, item.Token, "session"); err != nil || source.path != path || source.binding.ObjectIdentity != identity {
		t.Fatal(source, err)
	}
}
