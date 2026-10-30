package assets

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func playbackBindingDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE catalog_assets(id INTEGER PRIMARY KEY,token TEXT,path TEXT,size INTEGER,modified_ns INTEGER,available INTEGER,container TEXT NOT NULL DEFAULT '');
CREATE TABLE catalog_asset_links(entity_id INTEGER,asset_id INTEGER,available INTEGER);
CREATE TABLE catalog_entities(id INTEGER PRIMARY KEY,library_id INTEGER,retired INTEGER);
CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,library_id TEXT,retired INTEGER NOT NULL DEFAULT 0);
CREATE TABLE playback_sessions(id TEXT,asset_id TEXT,grant_hash TEXT,item_id INTEGER);
CREATE TABLE playback_source_pins(session_id TEXT,asset_id TEXT,size INTEGER,modified_ns INTEGER,binding TEXT NOT NULL DEFAULT 'none',source_id TEXT NOT NULL DEFAULT '',inventory_object_id TEXT NOT NULL DEFAULT '',root TEXT NOT NULL DEFAULT '',root_identity TEXT NOT NULL DEFAULT '',relative_path TEXT NOT NULL DEFAULT '',object_identity TEXT NOT NULL DEFAULT '',follow_symlinks INTEGER NOT NULL DEFAULT 0);
CREATE TABLE playback_physical_source_pins(session_id TEXT,source_id TEXT,incarnation TEXT,configuration_generation INTEGER);
CREATE TABLE library_sources(id TEXT,library_id TEXT,root TEXT,configured_root TEXT,root_identity TEXT,follow_symlinks INTEGER,kind TEXT,enabled INTEGER,identity_confirmed INTEGER,health TEXT,incarnation TEXT,generation INTEGER);
CREATE TABLE inventory_objects(id TEXT,source_id TEXT,asset_id TEXT,relative_path TEXT,object_identity TEXT,size INTEGER,modified_ns INTEGER,retired INTEGER,state TEXT,root_incarnation TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	// Match the cleaned root recorded by source inspection, even when TMPDIR
	// contains parent components.
	root := filepath.Clean(t.TempDir())
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,available) VALUES(1,'asset',?,12,34,1)`, []any{filepath.Join(root, "source.mp4")}},
		{`INSERT INTO catalog_libraries VALUES(1,'library',0);INSERT INTO catalog_entities VALUES(1,1,0);INSERT INTO catalog_asset_links VALUES(1,1,1);`, nil},
		{`INSERT INTO library_sources(id,library_id,root,configured_root,root_identity,follow_symlinks,kind,enabled,identity_confirmed,health,incarnation,generation) VALUES('source','library',?,?,'root-object',1,'local',1,1,'ready','incarnation',3)`, []any{root, root}},
		{`INSERT INTO playback_sessions VALUES('session','asset','grant',1);
INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('session','asset',12,34);
INSERT INTO playback_physical_source_pins VALUES('session','source','incarnation',3);
INSERT INTO inventory_objects VALUES('object','source','asset','source.mp4','ino:42',12,34,0,'available','incarnation');`, nil},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	pinTestPlaybackBinding(t, db)
	return db, root
}

func TestPlaybackResolverRetainsRecordedInventoryBinding(t *testing.T) {
	db, root := playbackBindingDatabase(t)
	// One inode can have more than one inventoried hardlink. Resolve the alias
	// actually recorded by the catalogue, not whichever object sorts first.
	if _, err := db.Exec(`INSERT INTO inventory_objects SELECT 'earlier-alias',source_id,asset_id,'another.mp4',object_identity,size,modified_ns,retired,state,root_incarnation FROM inventory_objects WHERE id='object'`); err != nil {
		t.Fatal(err)
	}
	for _, resolve := range []func() (PlaybackSource, error){
		func() (PlaybackSource, error) { return ResolvePlaybackSource(context.Background(), db, "session", "") },
		func() (PlaybackSource, error) { return ResolveCatalogPlaybackSource(context.Background(), db, "asset") },
	} {
		source, err := resolve()
		if err != nil || source.Binding == nil {
			t.Fatal(source, err)
		}
		if source.Binding.ObjectIdentity != "ino:42" || source.Binding.Inventory.Root != root || source.Binding.Inventory.RootIdentity != "root-object" || !source.Binding.Inventory.FollowSymlinks || source.Binding.Inventory.RelativePath != "source.mp4" {
			t.Fatalf("lost recorded binding: %+v", source.Binding)
		}
	}
}

func TestPlaybackResolverSeparatesAcceptedBindingFromCurrentInventory(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE inventory_objects SET object_identity=''`,
		`UPDATE inventory_objects SET size=13`,
		`UPDATE inventory_objects SET state='missing'`,
		`UPDATE inventory_objects SET relative_path='../outside.mp4'`,
		`UPDATE inventory_objects SET root_incarnation='replacement'`,
		`UPDATE library_sources SET root_identity=''`,
		`UPDATE library_sources SET enabled=0`,
		`UPDATE library_sources SET identity_confirmed=0`,
		`UPDATE library_sources SET health='root_changed'`,
		`UPDATE library_sources SET generation=4`,
		`DELETE FROM inventory_objects`,
	} {
		t.Run(mutation, func(t *testing.T) {
			db, _ := playbackBindingDatabase(t)
			if source, err := ResolvePlaybackSource(context.Background(), db, "session", ""); err != nil || source.Binding == nil {
				t.Fatalf("initial scan evidence was not admitted: %+v %v", source, err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			accepted, err := ResolvePlaybackSource(context.Background(), db, "session", "")
			if strings.HasPrefix(mutation, "UPDATE library_sources") {
				if err == nil {
					t.Fatalf("session bypassed source liveness: %+v", accepted)
				}
			} else if err != nil || accepted.Binding == nil || accepted.Binding.ObjectIdentity != "ino:42" || accepted.Binding.Inventory.RelativePath != "source.mp4" {
				t.Fatalf("session followed current inventory: %+v %v", accepted, err)
			}
			// Session-less reads still require today's inventory. A generation-only
			// edit has no session fence, and no row retains the source-less fallback.
			current, err := ResolveCatalogPlaybackSource(context.Background(), db, "asset")
			switch mutation {
			case `UPDATE library_sources SET generation=4`:
				if err != nil || current.Binding == nil {
					t.Fatal(current, err)
				}
			case `DELETE FROM inventory_objects`:
				if err != nil || current.Binding != nil {
					t.Fatal(current, err)
				}
			default:
				if err == nil {
					t.Fatalf("stale catalog evidence downgraded to pathname: %+v", current)
				}
			}
		})
	}
}

func pinTestPlaybackBinding(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := PinPlaybackBindingTx(context.Background(), tx, "session"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
