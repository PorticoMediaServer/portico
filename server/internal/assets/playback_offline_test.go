package assets

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// A source that is only offline keeps every accepted identity, lifetime, root
// and file witness. The resolver names the storage state instead of calling the
// source changed, and the same evidence works again the moment it is healthy —
// no per-item write or new acceptance is needed.
func TestPlaybackResolverNamesOfflineStorageAndRecovers(t *testing.T) {
	db, _ := playbackBindingDatabase(t)
	if _, err := db.Exec(`UPDATE library_sources SET health='offline'`); err != nil {
		t.Fatal(err)
	}
	resolvers := map[string]func() (PlaybackSource, error){
		"pinned session": func() (PlaybackSource, error) {
			return ResolvePlaybackSource(context.Background(), db, "session", "")
		},
		"catalogue read": func() (PlaybackSource, error) {
			return ResolveCatalogPlaybackSource(context.Background(), db, "asset")
		},
	}
	for name, resolve := range resolvers {
		t.Run(name+" offline", func(t *testing.T) {
			_, err := resolve()
			if !errors.Is(err, storage.ErrStorageOffline) {
				t.Fatalf("offline storage: %v", err)
			}
			if errors.Is(err, storage.ErrPlaybackSource) {
				t.Fatal("offline storage was reported as a changed or unavailable source")
			}
		})
	}
	if _, err := db.Exec(`UPDATE library_sources SET health='ready'`); err != nil {
		t.Fatal(err)
	}
	for name, resolve := range resolvers {
		t.Run(name+" reconnected", func(t *testing.T) {
			source, err := resolve()
			if err != nil || source.Binding == nil {
				t.Fatalf("reconnected storage: %+v %v", source, err)
			}
			if source.Binding.ObjectIdentity != "ino:42" || source.Binding.Inventory.RelativePath != "source.mp4" {
				t.Fatalf("reconnected binding: %+v", source.Binding)
			}
		})
	}
}

// Offline is named only after every other witness holds. A disabled source, a
// replaced incarnation, a stale generation, a missing or resized object, or an
// absent object identity keeps the generic refusal, and root_changed/removing
// keep their existing meaning. A pinned read keeps its persisted file witness,
// so object changes are checked by the session-less catalogue read.
func TestPlaybackResolverKeepsGenericRefusalWhenBindingIsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name, health, mutation string
		pinned                 bool
	}{
		{name: "disabled", health: "offline", mutation: `UPDATE library_sources SET enabled=0`, pinned: true},
		{name: "unconfirmed identity", health: "offline", mutation: `UPDATE library_sources SET identity_confirmed=0`, pinned: true},
		{name: "configuration generation", health: "offline", mutation: `UPDATE library_sources SET generation=4`, pinned: true},
		{name: "empty root identity", health: "offline", mutation: `UPDATE library_sources SET root_identity=''`, pinned: true},
		{name: "replaced incarnation", health: "offline", mutation: `UPDATE library_sources SET incarnation='replacement'`, pinned: true},
		{name: "missing object", health: "offline", mutation: `UPDATE inventory_objects SET state='missing'`},
		{name: "resized object", health: "offline", mutation: `UPDATE inventory_objects SET size=13`},
		{name: "empty object identity", health: "offline", mutation: `UPDATE inventory_objects SET object_identity=''`},
		{name: "root changed", health: "root_changed", pinned: true},
		{name: "removing", health: "removing", pinned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := playbackBindingDatabase(t)
			if _, err := db.Exec(`UPDATE library_sources SET health=?`, tc.health); err != nil {
				t.Fatal(err)
			}
			if tc.mutation != "" {
				if _, err := db.Exec(tc.mutation); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if tc.pinned {
				_, err = ResolvePlaybackSource(context.Background(), db, "session", "")
			} else {
				_, err = ResolveCatalogPlaybackSource(context.Background(), db, "asset")
			}
			if !errors.Is(err, storage.ErrPlaybackSource) {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if errors.Is(err, storage.ErrStorageOffline) {
				t.Fatalf("%s leaked the offline state past an invalid binding", tc.name)
			}
		})
	}
}

// Acceptance pins the exact physical source inside the session transaction.
// Offline refuses with the typed state and writes no binding; reconnecting pins
// the same source again.
func TestPlaybackAcceptanceNamesOfflineStorageAndRecovers(t *testing.T) {
	db, _ := playbackBindingDatabase(t)
	for _, statement := range []string{
		`UPDATE playback_source_pins SET binding='none',source_id='',inventory_object_id='',root='',root_identity='',relative_path='',object_identity='',follow_symlinks=0`,
		`DELETE FROM playback_physical_source_pins`,
		`UPDATE library_sources SET health='offline'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	pin := func() (string, string, error) {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			return "", "", err
		}
		if err = PinPlaybackBindingTx(context.Background(), tx, "session"); err != nil {
			_ = tx.Rollback()
			return "", "", err
		}
		if err = tx.Commit(); err != nil {
			return "", "", err
		}
		var binding, source string
		err = db.QueryRow(`SELECT binding,source_id FROM playback_source_pins WHERE session_id='session'`).Scan(&binding, &source)
		return binding, source, err
	}

	if _, _, err := pin(); !errors.Is(err, storage.ErrStorageOffline) {
		t.Fatalf("offline acceptance: %v", err)
	}
	var binding string
	if err := db.QueryRow(`SELECT binding FROM playback_source_pins WHERE session_id='session'`).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if binding != "none" {
		t.Fatalf("offline acceptance wrote binding %q", binding)
	}

	// A completed scan on the healthy source supplies the physical pin again.
	if _, err := db.Exec(`INSERT INTO playback_physical_source_pins VALUES('session','source','incarnation',3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE library_sources SET health='ready'`); err != nil {
		t.Fatal(err)
	}
	binding, source, err := pin()
	if err != nil || binding != "inventory" || source != "source" {
		t.Fatalf("reconnected acceptance: %q %q %v", binding, source, err)
	}
}

// playbackFallbackDatabase is the same accepted session with no inventory
// object at all: the catalogue advertises the file from the item's folder
// fallback, which is exactly the source-less shape playback admits as
// size/mtime.
func playbackFallbackDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, root := playbackBindingDatabase(t)
	for _, statement := range []string{
		`UPDATE playback_source_pins SET binding='none',source_id='',inventory_object_id='',root='',root_identity='',relative_path='',object_identity='',follow_symlinks=0`,
		`DELETE FROM playback_physical_source_pins`,
		`DELETE FROM inventory_objects`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db, root
}

// pinFallbackPlayback runs acceptance for the source-less session and reports
// the binding the transaction left behind.
func pinFallbackPlayback(t *testing.T, db *sql.DB) (string, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return "", err
	}
	if err = PinPlaybackBindingTx(context.Background(), tx, "session"); err != nil {
		_ = tx.Rollback()
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	var binding string
	err = db.QueryRow(`SELECT binding FROM playback_source_pins WHERE session_id='session'`).Scan(&binding)
	return binding, err
}

// A file with no inventory object is admitted through the same folder fallback
// the catalogue advertises. A matching source that is only offline is named at
// acceptance and at both reads; an explicitly refused source keeps the generic
// answer; a ready or unassigned file keeps today's source-less acquisition.
func TestPlaybackFallbackNamesOfflineSource(t *testing.T) {
	for _, tc := range []struct {
		name    string
		health  string
		enabled int
		want    error
	}{
		{name: "ready", health: "ready", enabled: 1},
		{name: "offline", health: "offline", enabled: 1, want: storage.ErrStorageOffline},
		{name: "root changed", health: "root_changed", enabled: 1, want: storage.ErrPlaybackSource},
		{name: "removing", health: "removing", enabled: 1, want: storage.ErrPlaybackSource},
		{name: "disabled", health: "offline", enabled: 0, want: storage.ErrPlaybackSource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, dir := playbackFallbackDatabase(t)
			if _, err := db.Exec(`UPDATE library_sources SET health=?,enabled=? WHERE id='source'`, tc.health, tc.enabled); err != nil {
				t.Fatal(err)
			}
			check := func(step string, err error) {
				t.Helper()
				if tc.want == nil {
					if err != nil {
						t.Fatalf("%s: %v", step, err)
					}
					return
				}
				if !errors.Is(err, tc.want) {
					t.Fatalf("%s: %v", step, err)
				}
				if tc.want == storage.ErrStorageOffline && errors.Is(err, storage.ErrPlaybackSource) {
					t.Fatalf("%s leaked offline as a changed source", step)
				}
			}
			binding, err := pinFallbackPlayback(t, db)
			check("acceptance", err)
			if err == nil && binding != "none" {
				t.Fatalf("acceptance wrote binding %q for a fallback file", binding)
			}
			source, err := ResolvePlaybackSource(context.Background(), db, "session", "")
			check("pinned read", err)
			if err == nil && (source.Binding != nil || source.Path != filepath.Join(dir, "source.mp4") || source.Size != 12 || source.ModifiedNS != 34) {
				t.Fatalf("source-less pinned read changed: %+v", source)
			}
			current, err := ResolveCatalogPlaybackSource(context.Background(), db, "asset")
			check("catalogue read", err)
			if err == nil && current.Binding != nil {
				t.Fatalf("catalogue read claimed custody: %+v", current.Binding)
			}
		})
	}
}

// Offline is named only inside the item's own library: a configured folder in
// another library, a file no folder covers, a remote descriptor, or a root the
// owner already moved away does not turn into a storage state for this title.
func TestPlaybackFallbackKeepsOtherAssociationsAndRecovers(t *testing.T) {
	t.Run("reconnect is immediate", func(t *testing.T) {
		db, _ := playbackFallbackDatabase(t)
		if _, err := db.Exec(`UPDATE library_sources SET health='offline'`); err != nil {
			t.Fatal(err)
		}
		if _, err := pinFallbackPlayback(t, db); !errors.Is(err, storage.ErrStorageOffline) {
			t.Fatalf("offline acceptance: %v", err)
		}
		if _, err := db.Exec(`UPDATE library_sources SET health='ready'`); err != nil {
			t.Fatal(err)
		}
		binding, err := pinFallbackPlayback(t, db)
		if err != nil || binding != "none" {
			t.Fatalf("reconnected acceptance: %q %v", binding, err)
		}
		if _, err = ResolvePlaybackSource(context.Background(), db, "session", ""); err != nil {
			t.Fatalf("reconnected pinned read: %v", err)
		}
		if _, err = ResolveCatalogPlaybackSource(context.Background(), db, "asset"); err != nil {
			t.Fatalf("reconnected catalogue read: %v", err)
		}
	})

	t.Run("other library does not refuse", func(t *testing.T) {
		db, _ := playbackFallbackDatabase(t)
		if _, err := db.Exec(`INSERT INTO library_sources(id,library_id,root,configured_root,root_identity,follow_symlinks,kind,enabled,identity_confirmed,health,incarnation,generation)
 SELECT 'other','other',root,configured_root,root_identity,follow_symlinks,kind,enabled,identity_confirmed,'offline',incarnation,generation FROM library_sources WHERE id='source'`); err != nil {
			t.Fatal(err)
		}
		for _, open := range []struct {
			name string
			run  func() error
		}{
			{name: "acceptance", run: func() error { _, err := pinFallbackPlayback(t, db); return err }},
			{name: "pinned read", run: func() error { _, err := ResolvePlaybackSource(context.Background(), db, "session", ""); return err }},
			{name: "catalogue read", run: func() error { _, err := ResolveCatalogPlaybackSource(context.Background(), db, "asset"); return err }},
		} {
			if err := open.run(); err != nil {
				t.Fatalf("%s: %v", open.name, err)
			}
		}
		// The item's own source is the one that decides.
		if _, err := db.Exec(`UPDATE library_sources SET health='offline' WHERE id='source'`); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveCatalogPlaybackSource(context.Background(), db, "asset"); !errors.Is(err, storage.ErrStorageOffline) {
			t.Fatalf("own source offline: %v", err)
		}
	})

	t.Run("unassigned keeps size and mtime", func(t *testing.T) {
		db, root := playbackFallbackDatabase(t)
		if _, err := db.Exec(`UPDATE library_sources SET root=?,configured_root=?`, filepath.Join(root, "elsewhere"), filepath.Join(root, "elsewhere")); err != nil {
			t.Fatal(err)
		}
		binding, err := pinFallbackPlayback(t, db)
		if err != nil || binding != "none" {
			t.Fatalf("unassigned acceptance: %q %v", binding, err)
		}
		source, err := ResolvePlaybackSource(context.Background(), db, "session", "")
		if err != nil || source.Binding != nil || source.Path != filepath.Join(root, "source.mp4") || source.Size != 12 || source.ModifiedNS != 34 {
			t.Fatalf("unassigned pinned read: %+v %v", source, err)
		}
		if _, err = ResolveCatalogPlaybackSource(context.Background(), db, "asset"); err != nil {
			t.Fatalf("unassigned catalogue read: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM library_sources`); err != nil {
			t.Fatal(err)
		}
		if _, err = ResolvePlaybackSource(context.Background(), db, "session", ""); err != nil {
			t.Fatalf("library without sources: %v", err)
		}
	})

	t.Run("configured root still covers", func(t *testing.T) {
		db, root := playbackFallbackDatabase(t)
		if _, err := db.Exec(`UPDATE library_sources SET root=?,health='offline' WHERE id='source'`, filepath.Join(root, "renamed")); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolvePlaybackSource(context.Background(), db, "session", ""); !errors.Is(err, storage.ErrStorageOffline) {
			t.Fatalf("configured root offline: %v", err)
		}
	})

	t.Run("remote descriptor is not storage", func(t *testing.T) {
		db, _ := playbackFallbackDatabase(t)
		if _, err := db.Exec(`UPDATE catalog_assets SET container='strm';UPDATE library_sources SET health='offline'`); err != nil {
			t.Fatal(err)
		}
		if _, err := pinFallbackPlayback(t, db); err != nil {
			t.Fatalf("strm acceptance: %v", err)
		}
		if _, err := ResolvePlaybackSource(context.Background(), db, "session", ""); err != nil {
			t.Fatalf("strm pinned read: %v", err)
		}
		if _, err := ResolveCatalogPlaybackSource(context.Background(), db, "asset"); err != nil {
			t.Fatalf("strm catalogue read: %v", err)
		}
	})

	t.Run("ready match keeps file playable", func(t *testing.T) {
		db, _ := playbackFallbackDatabase(t)
		if _, err := db.Exec(`INSERT INTO library_sources(id,library_id,root,configured_root,root_identity,follow_symlinks,kind,enabled,identity_confirmed,health,incarnation,generation)
 SELECT 'second','library',root,configured_root,root_identity,follow_symlinks,kind,enabled,identity_confirmed,'ready',incarnation,generation FROM library_sources WHERE id='source';
 UPDATE library_sources SET health='offline' WHERE id='source'`); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolvePlaybackSource(context.Background(), db, "session", ""); err != nil {
			t.Fatalf("one ready match was refused: %v", err)
		}
	})

	t.Run("explicit refusal outranks offline", func(t *testing.T) {
		db, _ := playbackFallbackDatabase(t)
		if _, err := db.Exec(`INSERT INTO library_sources(id,library_id,root,configured_root,root_identity,follow_symlinks,kind,enabled,identity_confirmed,health,incarnation,generation)
 SELECT 'removing','library',root,configured_root,root_identity,follow_symlinks,kind,enabled,identity_confirmed,'removing',incarnation,generation FROM library_sources WHERE id='source';
 UPDATE library_sources SET health='offline' WHERE id='source'`); err != nil {
			t.Fatal(err)
		}
		_, err := ResolvePlaybackSource(context.Background(), db, "session", "")
		if !errors.Is(err, storage.ErrPlaybackSource) || errors.Is(err, storage.ErrStorageOffline) {
			t.Fatalf("explicit refusal was masked: %v", err)
		}
	})
}
