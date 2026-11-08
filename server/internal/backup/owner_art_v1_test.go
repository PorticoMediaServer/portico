package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// v1Fixture installs the exact schema-v1 DDL on a disposable database opened
// through the pinned pure-Go SQLite driver, with foreign keys enforced for
// every fixture row. It is never a canonical, product or live database.
func v1Fixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	ctx := context.Background()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err = db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = schema.InitializeEmpty(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return db, path
}

// finalizeFixture leaves the fixture as the snapshot boundary: one database
// file, no WAL, closed before the copy is read.
func finalizeFixture(t *testing.T, db *sql.DB, path, dest string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture kept a WAL at the snapshot boundary: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err = target.Sync(); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err = target.Close(); err != nil {
		t.Fatal(err)
	}
}

// v1ArtworkFile writes real owner artwork bytes and names them by their own
// SHA-256, which is the digest the database stores and the backup verifies.
func v1ArtworkFile(t *testing.T, state, seed string) string {
	t.Helper()
	raw := []byte("owner artwork " + seed)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	dir := filepath.Join(state, "Metadata", "artwork")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, digest+".img"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return digest
}

func v1Exec(t *testing.T, db *sql.DB, query string, args ...any) sql.Result {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func v1Blob(t *testing.T, db *sql.DB, digest string, bytes int) {
	t.Helper()
	v1Exec(t, db, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,?,'now')`, digest, bytes)
}

func v1Source(t *testing.T, db *sql.DB, digest, small, large, ambient string) {
	t.Helper()
	v1Exec(t, db, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/jpeg',1,1,?,?,?,1920,0,0,1,'now')`, digest, small, large, ambient)
}

// v1Locked adds an owner selection. An empty digest is the locked-empty choice
// the schema allows: an explicit absence, not a file reference.
func v1Locked(t *testing.T, db *sql.DB, entity int, role, digest string) {
	t.Helper()
	if digest == "" {
		v1Exec(t, db, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,revision,actor,observed_at) VALUES('entity',?,?,'locked',NULL,'','','','','','','fence',1,'owner','now')`, entity, role)
		return
	}
	v1Exec(t, db, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,revision,actor,observed_at) VALUES('entity',?,?,'locked',?,'owner','owner-image','local','owner','','','fence',1,'owner','now')`, entity, role, digest)
}

func v1Matched(t *testing.T, db *sql.DB, entity int, role, digest string) {
	t.Helper()
	v1Exec(t, db, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,revision,actor,observed_at) VALUES('entity',?,?,'matched',?,'tmdb','provider-image','remote','provider','','','fence',1,'system','now')`, entity, role, digest)
}

// v1Pin is one normalized undo pin, as a schema-v1 owner edit leaves it.
func v1Pin(t *testing.T, db *sql.DB, digest string) {
	t.Helper()
	result := v1Exec(t, db, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,"trigger",actor,observed_at,before_json,after_json) VALUES('entity',1,'r1','r2','artwork','owner','now','{}','{}')`)
	history, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	v1Exec(t, db, `INSERT INTO metadata_owner_history_artwork(history_id,role,choice,digest) VALUES(?,'poster','locked',?)`, history, digest)
}

// At the finalized snapshot boundary, a schema-v1 database names owner art in
// artwork_selections.choice and metadata_owner_history_artwork. The backup
// carries exactly those blobs (once each), never provider-matched art, and the
// locked-empty choice never becomes a file reference.
func TestOwnerArtworkClosureV1CarriesOwnerSelectionsAndPins(t *testing.T) {
	ctx := context.Background()
	db, path := v1Fixture(t)
	state := t.TempDir()
	lockedSmall := v1ArtworkFile(t, state, "locked small")
	lockedLarge := v1ArtworkFile(t, state, "locked large")
	shared := v1ArtworkFile(t, state, "shared ambient and pinned small")
	pinnedLarge := v1ArtworkFile(t, state, "pinned large")
	pinnedAmbient := v1ArtworkFile(t, state, "pinned ambient")
	matchedSmall := v1ArtworkFile(t, state, "provider small")
	matchedLarge := v1ArtworkFile(t, state, "provider large")
	matchedAmbient := v1ArtworkFile(t, state, "provider ambient")
	for _, digest := range []string{lockedSmall, lockedLarge, shared, pinnedLarge, pinnedAmbient, matchedSmall, matchedLarge, matchedAmbient} {
		v1Blob(t, db, digest, 1)
	}
	v1Source(t, db, lockedSmall, lockedSmall, lockedLarge, shared)
	v1Source(t, db, shared, shared, pinnedLarge, pinnedAmbient)
	v1Source(t, db, matchedSmall, matchedSmall, matchedLarge, matchedAmbient)
	v1Locked(t, db, 1, "poster", lockedSmall)
	v1Locked(t, db, 2, "poster", "") // locked empty: an absence, not a file
	v1Matched(t, db, 3, "poster", matchedSmall)
	v1Pin(t, db, shared)

	partial := t.TempDir()
	finalizeFixture(t, db, path, filepath.Join(partial, DatabaseName))

	if _, err := copyOwnerArtwork(ctx, state, partial, nil); err != nil {
		t.Fatalf("v1 owner artwork closure: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(partial, "Metadata", "artwork"))
	if err != nil {
		t.Fatal(err)
	}
	carried := map[string]bool{}
	for _, entry := range entries {
		digest, ok := cutImgSuffix(entry.Name())
		if !ok {
			t.Fatalf("unexpected carried file %q", entry.Name())
		}
		carried[digest] = true
	}
	want := map[string]bool{lockedSmall: true, lockedLarge: true, shared: true, pinnedLarge: true, pinnedAmbient: true}
	for digest := range want {
		if !carried[digest] {
			t.Fatalf("owner artwork %s was not carried: %v", digest, carried)
		}
	}
	for _, digest := range []string{matchedSmall, matchedLarge, matchedAmbient} {
		if carried[digest] {
			t.Fatalf("provider-matched artwork %s was carried: %v", digest, carried)
		}
	}
	if len(carried) != len(want) {
		t.Fatalf("carried %d owner files, want %d: %v", len(carried), len(want), carried)
	}

	// The legacy statement names tables this shape does not have. It must not
	// be run against it, which is why the closure is resolved first.
	legacy, err := sql.Open("sqlite", "file:"+filepath.Join(partial, DatabaseName)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err = legacy.QueryContext(ctx, ownerArtworkReferences); err == nil {
		t.Fatal("legacy artwork closure unexpectedly fits the schema-v1 database")
	}
}

func cutImgSuffix(name string) (string, bool) {
	const suffix = ".img"
	if len(name) != 64+len(suffix) || name[len(name)-len(suffix):] != suffix {
		return "", false
	}
	return name[:64], true
}

// The size estimate resolves the same closure from the live database shape:
// owner bytes only, and the provider-matched source isn't counted.
func TestOwnerArtworkEstimateUsesTheV1Closure(t *testing.T) {
	db, _ := v1Fixture(t)
	locked := []string{fmt.Sprintf("%064x", 1), fmt.Sprintf("%064x", 2), fmt.Sprintf("%064x", 3)}
	matched := []string{fmt.Sprintf("%064x", 11), fmt.Sprintf("%064x", 12), fmt.Sprintf("%064x", 13)}
	for i, digest := range locked {
		v1Blob(t, db, digest, []int{500000, 400000, 300000}[i])
	}
	for _, digest := range matched {
		v1Blob(t, db, digest, 1000000)
	}
	v1Source(t, db, locked[0], locked[0], locked[1], locked[2])
	v1Source(t, db, matched[0], matched[0], matched[1], matched[2])
	v1Locked(t, db, 1, "poster", locked[0])
	v1Matched(t, db, 3, "poster", matched[0])
	v1Pin(t, db, locked[0])

	s := New(t.TempDir(), db, "srv-test", "test-version", 0)
	defer s.stop()
	total, err := s.estimate(context.Background(), false)
	if err != nil {
		t.Fatalf("v1 size estimate: %v", err)
	}
	if total != 1200000 {
		t.Fatalf("owner artwork estimate = %d, want 1200000 (provider art counted?)", total)
	}
}

// An unrecognised ownership shape stops the backup instead of guessing at a
// closure that could silently omit owner files.
func TestOwnerArtworkClosureRefusesUnsupportedSelectionShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = ownerArtworkClosure(ctx, db); !errors.Is(err, ErrArtworkSchemaUnsupported) {
		t.Fatalf("missing selections table: %v", err)
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE artwork_selections(digest TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = ownerArtworkClosure(ctx, db); !errors.Is(err, ErrArtworkSchemaUnsupported) {
		t.Fatalf("table without an ownership column: %v", err)
	}
}

// Pre-restore preservation and stream apply use the v1 closure too: the old
// database names the owner files to keep, and a directory at an owner path is
// still refused.
func TestPreRestoreOwnerArtworkNamesUseV1Closure(t *testing.T) {
	ctx := context.Background()
	db, path := v1Fixture(t)
	state := t.TempDir()
	locked := v1ArtworkFile(t, state, "locked")
	pinned := v1ArtworkFile(t, state, "pinned")
	matched := v1ArtworkFile(t, state, "matched")
	for _, digest := range []string{locked, pinned, matched} {
		v1Blob(t, db, digest, 1)
		v1Source(t, db, digest, digest, digest, digest)
	}
	v1Locked(t, db, 1, "poster", locked)
	v1Matched(t, db, 3, "poster", matched)
	v1Pin(t, db, pinned)

	pre := t.TempDir()
	finalizeFixture(t, db, path, filepath.Join(pre, DatabaseName))
	names, err := currentOwnerArtworkNames(ctx, pre)
	if err != nil {
		t.Fatalf("v1 previous artwork names: %v", err)
	}
	got := map[string]bool{}
	for _, name := range names {
		got[name] = true
	}
	for _, digest := range []string{locked, pinned} {
		want := "Metadata/artwork/" + digest + ".img"
		if !got[want] {
			t.Fatalf("owner artwork %s was not preserved: %v", want, names)
		}
	}
	if got["Metadata/artwork/"+matched+".img"] {
		t.Fatalf("provider-matched artwork was preserved: %v", names)
	}
	if len(got) != 2 {
		t.Fatalf("preserved %d owner files, want 2: %v", len(got), names)
	}
	if err = checkPreviousArtworkPaths(ctx, state, pre); err != nil {
		t.Fatalf("regular v1 owner files were refused: %v", err)
	}
	name := filepath.Join(state, "Metadata", "artwork", pinned+".img")
	if err = os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	if err = checkPreviousArtworkPaths(ctx, state, pre); !errors.Is(err, ErrSourceInvalid) {
		t.Fatalf("a directory at an owner artwork path: %v", err)
	}
}
