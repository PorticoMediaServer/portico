//go:build linux || darwin

package playback

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type sourceBindingFixture struct {
	db                                                    *sql.DB
	service                                               *Service
	principal                                             identity.Principal
	item, asset, root, path, rootIdentity, objectIdentity string
	info                                                  os.FileInfo
}

func sourceBindingIdentity(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id, err := storage.FileObjectIdentity(f)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func acceptedSourceFixture(t *testing.T, inventory bool) *sourceBindingFixture {
	t.Helper()
	root := filepath.Clean(t.TempDir())
	path := filepath.Join(root, "movie.mp4")
	if err := os.WriteFile(path, []byte("original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, item, asset := catalogFixture(t, db, "binding-library", root, compactcatalog.Movie, "binding-movie", "Movie", compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 100})
	f := &sourceBindingFixture{db: db, service: New(db), item: item, asset: asset, root: root, path: path, info: info}
	f.principal = identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, "owner", "profile")
	insertFixtureSession(t, db, "session", "owner", "profile", "owner", "2999-01-01T00:00:00Z")
	f.rootIdentity = sourceBindingIdentity(t, root)
	f.objectIdentity = storage.StableObjectIdentity(sourceBindingIdentity(t, path), f.rootIdentity)
	if inventory {
		// Catalog fixtures create their own initial library source; use it rather
		// than inventing a separate source-selection path for acceptance.
		if _, err := db.Exec(`UPDATE library_sources SET root_identity=?,identity_confirmed=1,health='ready',follow_symlinks=0 WHERE library_id='binding-library'`, f.rootIdentity); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns) SELECT 'accepted-object',id,?,incarnation,'movie.mp4',?,'r1','{}',?,? FROM library_sources WHERE library_id='binding-library'`, asset, f.objectIdentity, info.Size(), info.ModTime().UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *sourceBindingFixture) create(t *testing.T, request string) Session {
	t.Helper()
	session, err := f.service.Create(f.principal, f.item, "automatic", request)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (f *sourceBindingFixture) read(t *testing.T, session Session, want string) {
	t.Helper()
	reader, err := assets.OpenPlayback(f.db, session.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != want {
		t.Fatalf("bytes=%q err=%v", data, err)
	}
}

func (f *sourceBindingFixture) refuse(t *testing.T, session Session) {
	t.Helper()
	reader, err := assets.OpenPlayback(f.db, session.ID, "")
	if reader != nil {
		reader.Close()
	}
	if !errors.Is(err, storage.ErrPlaybackSource) {
		t.Fatalf("expected source refusal, got %v", err)
	}
}

func (f *sourceBindingFixture) replace(t *testing.T) {
	t.Helper()
	replacement := filepath.Join(f.root, "replacement")
	if err := os.WriteFile(replacement, []byte("replaced bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, f.info.ModTime(), f.info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, f.path); err != nil {
		t.Fatal(err)
	}
}

func TestSessionAcceptedBindingRejectsRescannedReplacement(t *testing.T) {
	f := acceptedSourceFixture(t, true)
	old := f.create(t, "old")
	f.read(t, old, "original bytes")
	f.replace(t)
	replacement := storage.StableObjectIdentity(sourceBindingIdentity(t, f.path), f.rootIdentity)
	if replacement == f.objectIdentity {
		t.Fatal("fixture reused accepted inode")
	}
	// Model the rescan's unchanged size/mtime inventory update, including the
	// new identity. The catalogue token, size and mtime remain unchanged.
	if _, err := f.db.Exec(`UPDATE inventory_objects SET object_identity=?,revision='rescan' WHERE id='accepted-object'`, replacement); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, old)
	fresh := f.create(t, "fresh")
	f.read(t, fresh, "replaced bytes")
	var oldObject, newObject, mode, source, physical string
	if err := f.db.QueryRow(`SELECT object_identity FROM playback_source_pins WHERE session_id=?`, old.ID).Scan(&oldObject); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT p.object_identity,p.binding,p.source_id,x.source_id FROM playback_source_pins p JOIN playback_physical_source_pins x ON x.session_id=p.session_id WHERE p.session_id=?`, fresh.ID).Scan(&newObject, &mode, &source, &physical); err != nil {
		t.Fatal(err)
	}
	if oldObject != f.objectIdentity || newObject != replacement || mode != "inventory" || source != physical {
		t.Fatal("acceptance did not freeze the physical tuple", oldObject, newObject, mode, source, physical)
	}
}

func TestSessionAcceptedBindingSourceFences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mutation := range []string{
		`UPDATE library_sources SET enabled=0`,
		`UPDATE library_sources SET identity_confirmed=0`,
		`UPDATE library_sources SET health='removing'`,
		`UPDATE library_sources SET incarnation='new-incarnation'`,
		`UPDATE library_sources SET generation=generation+1`,
		`DELETE FROM inventory_objects; DELETE FROM library_sources`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f := acceptedSourceFixture(t, true)
			session := f.create(t, "accepted")
			f.read(t, session, "original bytes")
			if _, err := f.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			f.refuse(t, session)
		})
	}
}

func TestSessionAcceptedBindingRejectsReplacedRoot(t *testing.T) {
	f := acceptedSourceFixture(t, true)
	old := f.create(t, "old")
	retained := f.root + "-retained"
	if err := os.Rename(f.root, retained); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(retained); err != nil {
			t.Error(err)
		}
	})
	if err := os.Mkdir(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	// Even retaining the exact file via a hardlink cannot authorize a new root.
	if err := os.Link(filepath.Join(retained, "movie.mp4"), f.path); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, old) // The filesystem check also fences a root not yet rescanned.
	if _, err := f.db.Exec(`UPDATE library_sources SET root_identity=?`, sourceBindingIdentity(t, f.root)); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, old)
}

func TestSessionAcceptedBindingSurvivesUnrelatedRescanAndHardlink(t *testing.T) {
	f := acceptedSourceFixture(t, true)
	session := f.create(t, "accepted")
	if err := os.Link(f.path, filepath.Join(f.root, "alias.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE inventory_objects SET revision='unrelated-rescan'; INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns) SELECT 'earlier-alias',source_id,asset_id,root_incarnation,'alias.mp4',object_identity,revision,evidence_json,size,modified_ns FROM inventory_objects WHERE id='accepted-object'`); err != nil {
		t.Fatal(err)
	}
	f.read(t, session, "original bytes")
	// Inventory retirement does not substitute a new object for accepted bytes.
	if _, err := f.db.Exec(`DELETE FROM inventory_objects`); err != nil {
		t.Fatal(err)
	}
	f.read(t, session, "original bytes")
}

func TestSessionAcceptedBindingRejectsSubstitutedSymlink(t *testing.T) {
	f := acceptedSourceFixture(t, true)
	session := f.create(t, "accepted")
	if err := os.Rename(f.path, f.path+".target"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("movie.mp4.target", f.path); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, session)
}

func TestSessionAcceptedBindingNonePreservesSizeMtimeAndNoFollow(t *testing.T) {
	f := acceptedSourceFixture(t, false)
	session := f.create(t, "accepted")
	var mode, root, object string
	if err := f.db.QueryRow(`SELECT binding,root,object_identity FROM playback_source_pins WHERE session_id=?`, session.ID).Scan(&mode, &root, &object); err != nil {
		t.Fatal(err)
	}
	if mode != "none" || root != "" || object != "" {
		t.Fatal("fabricated custody", mode, root, object)
	}
	f.replace(t)
	f.read(t, session, "replaced bytes")
	if err := os.Rename(f.path, f.path+".target"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("movie.mp4.target", f.path); err != nil {
		t.Fatal(err)
	}
	f.refuse(t, session)
}

func TestSessionAcceptedBindingInvalidEvidenceRollsBack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mutation := range []string{
		`UPDATE inventory_objects SET object_identity=''`,
		`UPDATE inventory_objects SET size=size+1`,
		`UPDATE inventory_objects SET modified_ns=modified_ns+1`,
		`UPDATE inventory_objects SET relative_path='other.mp4'`,
		`UPDATE inventory_objects SET retired=1`,
		`UPDATE inventory_objects SET state='missing'`,
		`UPDATE inventory_objects SET root_incarnation='old-root'`,
		`UPDATE library_sources SET root_identity=''`,
		`UPDATE library_sources SET health='offline'`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f := acceptedSourceFixture(t, true)
			if _, err := f.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Create(f.principal, f.item, "automatic", "invalid"); err == nil {
				t.Fatal("invalid acceptance succeeded")
			}
			var count int
			if err := f.db.QueryRowContext(context.Background(), `SELECT count(*) FROM playback_sessions`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed acceptance committed session", count, err)
			}
		})
	}
}

func TestSessionAcceptedBindingMountedPreservesProviderIdentity(t *testing.T) {
	f := acceptedSourceFixture(t, true)
	if _, err := f.db.Exec(`UPDATE library_sources SET kind='remote',root_identity='provider-root'; UPDATE inventory_objects SET object_identity='provider-object'`); err != nil {
		t.Fatal(err)
	}
	session := f.create(t, "mounted")
	accepted, err := assets.ResolvePlaybackSource(context.Background(), f.db, session.ID, "")
	if err != nil || accepted.Binding == nil || !accepted.Binding.Mounted || accepted.Binding.ObjectIdentity != "provider-object" || accepted.Binding.Inventory.RootIdentity != "provider-root" {
		t.Fatal("mounted provider evidence lost", accepted, err)
	}
	// Provider IDs remain opaque, pinned mount evidence after a rescan.
	if _, err := f.db.Exec(`UPDATE inventory_objects SET object_identity='new-provider-object',relative_path='new.mp4'`); err != nil {
		t.Fatal(err)
	}
	accepted, err = assets.ResolvePlaybackSource(context.Background(), f.db, session.ID, "")
	if err != nil || accepted.Path != f.path || accepted.Binding.ObjectIdentity != "provider-object" {
		t.Fatal("mounted session followed inventory", accepted, err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := storage.New(binary)
	if input, err := assets.OpenPlaybackInput(context.Background(), f.db, client, session.ID); !errors.Is(err, storage.ErrPlaybackSource) {
		if input != nil {
			input.Close()
		}
		t.Fatal("unmanaged remote binding accepted", err)
	}
}

func TestSessionAcceptedBindingSelectsMatchingPhysicalSource(t *testing.T) {
	f := acceptedSourceFixture(t, true)
	// A lexically earlier source for the same asset must not win over the
	// source whose root and relative path actually name the catalogue file.
	if _, err := f.db.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root,root_identity,identity_confirmed) VALUES('a-other','binding-library','/other','/other','other-root',1);
 INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns) SELECT 'other-object','a-other',?,incarnation,'movie.mp4','other-object','r1','{}',?,? FROM library_sources WHERE id='a-other'`, f.asset, f.info.Size(), f.info.ModTime().UnixNano()); err != nil {
		t.Fatal(err)
	}
	session := f.create(t, "matching-source")
	f.read(t, session, "original bytes")
	var pinned, physical string
	if err := f.db.QueryRow(`SELECT p.source_id,x.source_id FROM playback_source_pins p JOIN playback_physical_source_pins x ON x.session_id=p.session_id WHERE p.session_id=?`, session.ID).Scan(&pinned, &physical); err != nil {
		t.Fatal(err)
	}
	if pinned != "binding-library" || physical != pinned {
		t.Fatal("physical selection did not match accepted binding", pinned, physical)
	}
}
