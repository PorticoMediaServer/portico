// Package titledatatest makes small title data files by hand, in the shape of the data set's
// contract (recommendations/schema/transport.sql in the private repository), signs a list for
// them with a throwaway key and serves them without a network. It is for tests only: the real
// file and its signature come from the data set's release, which a server never builds.
//
// It writes the file with SQL itself, outside internal/db, because it stands in for the data
// set's builder, not for the server.
package titledatatest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

// schema is the contract's schema, copied as published.
const schema = `
PRAGMA user_version = 3;
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;
CREATE TABLE kinds(id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE) STRICT;
CREATE TABLE providers(id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, UNIQUE(name, kind)) STRICT;
CREATE TABLE works(
  qid INTEGER PRIMARY KEY,
  kind INTEGER NOT NULL REFERENCES kinds(id),
  anime INTEGER NOT NULL DEFAULT 0 CHECK(anime IN (0,1)),
  audiobook INTEGER NOT NULL DEFAULT 0 CHECK(audiobook IN (0,1)),
  tagged INTEGER NOT NULL CHECK(tagged IN (0,1)),
  confidence INTEGER CHECK(confidence IN (1,2)),
  year INTEGER,
  sitelinks INTEGER NOT NULL,
  title TEXT NOT NULL
) STRICT;
CREATE TABLE work_ids(
  provider INTEGER NOT NULL REFERENCES providers(id),
  provider_id ANY NOT NULL,
  qid INTEGER NOT NULL,
  PRIMARY KEY(provider, provider_id, qid)
) STRICT, WITHOUT ROWID;
CREATE TABLE vocabulary(
  id INTEGER PRIMARY KEY,
  tag TEXT NOT NULL UNIQUE,
  family TEXT NOT NULL,
  label TEXT NOT NULL,
  description TEXT NOT NULL,
  applies_to TEXT NOT NULL,
  added_in INTEGER NOT NULL,
  retired_in INTEGER,
  row_label TEXT
) STRICT;
CREATE TABLE work_tags(
  qid INTEGER NOT NULL,
  tag INTEGER NOT NULL REFERENCES vocabulary(id),
  strength INTEGER NOT NULL CHECK(strength BETWEEN 1 AND 3),
  PRIMARY KEY(qid, tag)
) STRICT, WITHOUT ROWID;
CREATE TABLE work_similar(
  qid INTEGER NOT NULL,
  rank INTEGER NOT NULL CHECK(rank BETWEEN 1 AND 15),
  similar INTEGER NOT NULL,
  relation INTEGER NOT NULL CHECK(relation BETWEEN 0 AND 5),
  PRIMARY KEY(qid, rank),
  CHECK(qid <> similar)
) STRICT, WITHOUT ROWID;
CREATE TABLE work_sources(
  qid INTEGER NOT NULL,
  source INTEGER NOT NULL CHECK(source IN (1,2,3)),
  site TEXT NOT NULL,
  revision INTEGER NOT NULL,
  PRIMARY KEY(qid, source, site)
) STRICT, WITHOUT ROWID;
CREATE TABLE fandom_wikis(site TEXT PRIMARY KEY, license TEXT NOT NULL) STRICT;
CREATE TABLE redirects(old_qid INTEGER PRIMARY KEY, qid INTEGER NOT NULL) STRICT;
CREATE TABLE removed(qid INTEGER PRIMARY KEY) STRICT;
INSERT INTO kinds VALUES (1, 'movie'), (2, 'show'), (3, 'artist'), (4, 'album'), (5, 'book');
INSERT INTO providers VALUES (1, 'tmdb', 'movie'), (2, 'tmdb', 'tv'), (3, 'tvdb', 'series'), (4, 'tvdb', 'movie'),
  (5, 'anilist', 'anime'), (6, 'imdb', 'title');
`

// Provider codes, as the contract numbers them.
const (
	TMDBMovie  = 1
	TMDBShow   = 2
	TVDBSeries = 3
	TVDBMovie  = 4
	AniList    = 5
	IMDb       = 6
)

// Tag is one vocabulary entry. RowLabel is empty for a family that makes no row.
type Tag struct {
	ID       int
	Key      string // "mood:cozy"
	RowLabel string // "cozy stories"
	Retired  bool
}

// Work is one title in the file.
type Work struct {
	QID     int64
	Show    bool
	IDs     map[int]string // provider code to id
	Tags    map[int]int    // vocabulary id to strength
	Similar []int64        // qids, best first
}

// File is a whole title data file.
type File struct {
	Version    string // dataset_version; "2026.45" when empty
	Domain     string // "screen" when empty
	Kind       string // "full" when empty
	Vocabulary []Tag
	Works      []Work
}

// Write writes f as an SQLite file at path.
func Write(t testing.TB, path string, f File) {
	t.Helper()
	if f.Version == "" {
		f.Version = "2026.45"
	}
	if f.Domain == "" {
		f.Domain = "screen"
	}
	if f.Kind == "" {
		f.Kind = "full"
	}
	conn, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	statements := []string{schema}
	add := func(format string, args ...any) { statements = append(statements, fmt.Sprintf(format, args...)) }
	for key, value := range map[string]string{"schema_version": "3", "domain": f.Domain, "dataset_version": f.Version,
		"kind": f.Kind, "vocabulary_version": "3", "built_at": "2026-11-10T00:00:00Z", "model": "test",
		"license": "CC-BY-SA-4.0", "attribution": "test", "source_repo": "test"} {
		add(`INSERT INTO meta VALUES ('%s', '%s')`, key, value)
	}
	for _, tag := range f.Vocabulary {
		retired, label := "NULL", "NULL"
		if tag.Retired {
			retired = "3"
		}
		if tag.RowLabel != "" {
			label = "'" + tag.RowLabel + "'"
		}
		family, _, _ := strings.Cut(tag.Key, ":")
		add(`INSERT INTO vocabulary VALUES (%d, '%s', '%s', '%s', '', '["movie","show"]', 1, %s, %s)`,
			tag.ID, tag.Key, family, tag.Key, retired, label)
	}
	for _, w := range f.Works {
		kind := 1
		if w.Show {
			kind = 2
		}
		add(`INSERT INTO works VALUES (%d, %d, 0, 0, %d, 2, 2000, 10, 'Q%d')`, w.QID, kind, min(len(w.Tags), 1), w.QID)
		for provider, id := range w.IDs {
			value := id
			if provider != IMDb {
				value = strings.TrimLeft(id, "0") // numeric providers are stored as integers
			} else {
				value = "'" + id + "'"
			}
			add(`INSERT INTO work_ids VALUES (%d, %s, %d)`, provider, value, w.QID)
		}
		for tag, strength := range w.Tags {
			add(`INSERT INTO work_tags VALUES (%d, %d, %d)`, w.QID, tag, strength)
		}
		for rank, similar := range w.Similar {
			add(`INSERT INTO work_similar VALUES (%d, %d, %d, 1)`, w.QID, rank+1, similar)
		}
	}
	if _, err := conn.Exec(strings.Join(statements, ";\n")); err != nil {
		t.Fatal(err)
	}
}

// Release is one published release: the list, its signature and the gzip file it names.
type Release struct {
	Key      ed25519.PrivateKey
	Manifest map[string]any // as signed; change it and call Sign again
	List     []byte
	Sig      []byte
	Gzip     []byte
}

// NewKey makes a throwaway signing key.
func NewKey(t testing.TB) ed25519.PrivateKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

// Publish makes a release of f with the given release number, signed with key.
func Publish(t testing.TB, key ed25519.PrivateKey, sequence int64, f File) *Release {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen.sqlite")
	Write(t, path, f)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var packed bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	zw.Write(raw)
	zw.Close()
	version := f.Version
	if version == "" {
		version = "2026.45"
	}
	sum := sha256.Sum256(packed.Bytes())
	public := key.Public().(ed25519.PublicKey)
	keySum := sha256.Sum256(public)
	r := &Release{Key: key, Gzip: packed.Bytes(), Manifest: map[string]any{
		"schema_version": 3, "sequence": sequence, "issued_at": "2026-11-10T00:00:00Z", "key_id": hex.EncodeToString(keySum[:8]),
		"domains": map[string]any{"screen": map[string]any{"dataset_version": version, "vocabulary_version": 3,
			"full": map[string]any{"version": version, "url": "screen/" + version + ".sqlite.gz", "sha256": hex.EncodeToString(sum[:]),
				"bytes": packed.Len(), "uncompressed_bytes": len(raw)}, "deltas": []any{}}},
	}}
	r.Sign()
	return r
}

// Full is the release's entry for the full file, to change in a test.
func (r *Release) Full() map[string]any {
	return r.Manifest["domains"].(map[string]any)["screen"].(map[string]any)["full"].(map[string]any)
}

// Sign writes the list's bytes from Manifest and signs them.
func (r *Release) Sign() {
	r.List, _ = json.MarshalIndent(r.Manifest, "", " ")
	r.Sig = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(r.Key, r.List)) + "\n")
}

// Host serves releases from memory at https://data.example/v3/, and counts requests.
type Host struct {
	mu       sync.Mutex
	files    map[string][]byte
	Requests int
}

// Address is the list's address on a Host.
const Address = "https://data.example/v3/manifest.json"

// Serve makes r the release the host answers with.
func (h *Host) Serve(r *Release) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files = map[string][]byte{Address: r.List, Address + ".sig": r.Sig}
	url, _ := r.Full()["url"].(string)
	h.files["https://data.example/v3/"+url] = r.Gzip
}

func (h *Host) Open(_ context.Context, address string) (io.ReadCloser, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Requests++
	body, ok := h.files[address]
	if !ok {
		return nil, fmt.Errorf("%s: 404 Not Found", address)
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}
