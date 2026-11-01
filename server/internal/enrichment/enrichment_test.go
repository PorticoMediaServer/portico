package enrichment

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// publisher serves a signed manifest and gzip dataset files, as the
// dataset's release host will.
type publisher struct {
	t       *testing.T
	key     ed25519.PrivateKey
	public  ed25519.PublicKey
	files   map[string][]byte
	domains map[string]ManifestDomain
	tamper  bool
	// sequence and keyID go into the signed manifest (defaults: 1 and the
	// publisher's own key); signer, when set, signs instead of key.
	sequence int64
	keyID    string
	signer   ed25519.PrivateKey
	status   int            // when set, every request answers it
	requests map[string]int // by path
}

func newPublisher(t *testing.T) *publisher {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &publisher{t: t, key: private, public: public, files: map[string][]byte{}, domains: map[string]ManifestDomain{}, sequence: 1, requests: map[string]int{}}
}

func (p *publisher) manifest() []byte {
	keyID := p.keyID
	if keyID == "" {
		keyID = KeyID(p.public)
	}
	manifest, _ := json.Marshal(Manifest{SchemaVersion: SchemaVersion, Sequence: p.sequence, IssuedAt: "2026-11-01T00:00:00Z", KeyID: keyID, Domains: p.domains})
	return manifest
}

func (p *publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.requests[r.URL.Path]++
	if p.status != 0 {
		w.WriteHeader(p.status)
		return
	}
	manifest := p.manifest()
	switch r.URL.Path {
	case "/manifest.json":
		if p.tamper {
			manifest = append(manifest, ' ')
		}
		w.Write(manifest)
	case "/manifest.json.sig":
		signer := p.key
		if p.signer != nil {
			signer = p.signer
		}
		w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(signer, manifest)) + "\n"))
	default:
		body, ok := p.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}
}

// build writes a dataset file with the contract schema and publishes it.
func (p *publisher) build(name, kind, version string, fill func(t *testing.T, db *sql.DB)) ManifestFile {
	path := filepath.Join(p.t.TempDir(), name+".sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		p.t.Fatal(err)
	}
	schema, err := os.ReadFile("testdata/schema.sql")
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		p.t.Fatal(err)
	}
	mustExec(p.t, db, `INSERT INTO kinds VALUES(1,'movie'),(2,'show'),(3,'artist'),(4,'album'),(5,'book')`)
	mustExec(p.t, db, `INSERT INTO providers VALUES(1,'tmdb','movie'),(2,'tmdb','tv'),(3,'tvdb','series'),(4,'tvdb','movie'),(5,'anilist','anime'),(6,'imdb','title')`)
	mustExec(p.t, db, `INSERT INTO meta VALUES('kind',?),('dataset_version',?),('schema_version','2')`, kind, version)
	fill(p.t, db)
	db.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		p.t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw)
	zw.Close()
	sum := sha256.Sum256(gz.Bytes())
	p.files["/"+name+".sqlite.gz"] = gz.Bytes()
	return ManifestFile{URL: name + ".sqlite.gz", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(gz.Len()), UncompressedBytes: int64(len(raw))}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// vocabulary and three works: Heat (TMDB 949), Thief (TMDB 11524), Collateral (TMDB 1538).
func screenV1(t *testing.T, db *sql.DB) {
	mustExec(t, db, `INSERT INTO vocabulary VALUES(1,'tone:gritty','tone','Gritty','Hard-edged.','["movie","show"]',1,NULL),(2,'theme:heist','theme','Heists','A robbery drives the story.','["movie","show"]',1,NULL),(3,'pacing:slow-burn','pacing','Slow burn','Tension builds slowly.','["movie","show"]',1,NULL)`)
	mustExec(t, db, `INSERT INTO works VALUES(47703,1,0,0,1,2,1995,70,'Heat'),(1197379,1,0,0,1,2,1981,30,'Thief'),(223596,1,0,0,1,2,2004,60,'Collateral')`)
	mustExec(t, db, `INSERT INTO work_ids VALUES(1,949,47703),(6,'tt0113277',47703),(1,11524,1197379),(1,1538,223596)`)
	mustExec(t, db, `INSERT INTO work_tags VALUES(47703,1,3),(47703,2,3),(47703,3,1),(1197379,2,2),(223596,1,2)`)
	mustExec(t, db, `INSERT INTO work_similar VALUES(47703,1,223596,1),(47703,2,1197379,5)`)
	mustExec(t, db, `INSERT INTO work_sources VALUES(47703,1,'en',123456)`)
}

func TestDatasetImportMatchesByProviderIdAndFeedsRecommendations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	heat := c.Movie(films, "/m/heat.mkv", "Heat", 1995)
	thief := c.Movie(films, "/m/thief.mkv", "Thief", 1981)
	other := c.Movie(films, "/m/other.mkv", "Other", 2000)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','949'),(?,'tmdb','movie','11524'),(?,'tmdb','movie','5')`, heat.ID, thief.ID, other.ID)
	c.Drain()
	p := newPublisher(t)
	full := p.build("screen-1", "full", "2026.09.26", screenV1)
	p.domains["screen"] = ManifestDomain{DatasetVersion: "2026.09.26", VocabularyVersion: 1, Full: full}
	s := publisherService(c.DB, p, t.TempDir())
	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	var qid int64
	if err := c.DB.QueryRow(`SELECT qid FROM catalog_dataset_works WHERE entity_id=?`, heat.ID).Scan(&qid); err != nil || qid != 47703 {
		t.Fatalf("Heat matched %d %v", qid, err)
	}
	var unmatched int
	c.DB.QueryRow(`SELECT count(*) FROM catalog_dataset_works WHERE entity_id=?`, other.ID).Scan(&unmatched)
	if unmatched != 0 {
		t.Fatal("a film the dataset doesn't hold was matched")
	}
	// Strong tags are recommendation facets; the weak one isn't.
	facets := map[string]bool{}
	rows, _ := c.DB.Query(`SELECT facet FROM catalog_rec_facets WHERE entity_id=?`, heat.ID)
	for rows.Next() {
		var f string
		rows.Scan(&f)
		facets[f] = true
	}
	rows.Close()
	if !facets["x:tone:gritty"] || !facets["x:theme:heist"] || facets["x:pacing:slow-burn"] {
		t.Fatalf("Heat's facets: %v", facets)
	}
	// Similar titles by the target's provider id (Collateral isn't in the
	// library: the link is kept and resolves when it arrives).
	var similar []string
	rows, _ = c.DB.Query(`SELECT provider||':'||provider_kind||':'||provider_id||':'||relation FROM catalog_similar WHERE entity_id=? AND source='portico-dataset' ORDER BY rank`, heat.ID)
	for rows.Next() {
		var v string
		rows.Scan(&v)
		similar = append(similar, v)
	}
	rows.Close()
	if len(similar) != 2 || similar[0] != "tmdb:movie:1538:tone" || similar[1] != "tmdb:movie:11524:creator" {
		t.Fatalf("Heat's similar titles: %v", similar)
	}
	var label string
	c.DB.QueryRow(`SELECT label FROM catalog_dataset_vocabulary WHERE tag='theme:heist'`).Scan(&label)
	if label != "Heists" {
		t.Fatalf("vocabulary label %q", label)
	}
	if err := compactcatalog.CheckRecPostings(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	// A film catalogued later is matched as its ids arrive.
	late := c.Movie(films, "/m/collateral.mkv", "Collateral", 2004)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','1538')`, late.ID)
	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT qid FROM catalog_dataset_works WHERE entity_id=?`, late.ID).Scan(&qid); err != nil || qid != 223596 {
		t.Fatalf("Collateral matched %d %v", qid, err)
	}
	// A delta replaces a work's rows in place.
	delta := p.build("screen-2", "delta", "2026.10.03", func(t *testing.T, db *sql.DB) {
		mustExec(t, db, `INSERT INTO vocabulary VALUES(1,'tone:gritty','tone','Gritty','Hard-edged.','["movie","show"]',1,NULL),(2,'theme:heist','theme','Heists','A robbery drives the story.','["movie","show"]',1,NULL),(3,'pacing:slow-burn','pacing','Slow burn','Tension builds slowly.','["movie","show"]',1,NULL)`)
		mustExec(t, db, `INSERT INTO works VALUES(47703,1,0,0,1,2,1995,71,'Heat')`)
		mustExec(t, db, `INSERT INTO work_ids VALUES(1,949,47703)`)
		mustExec(t, db, `INSERT INTO work_tags VALUES(47703,3,3)`)
	})
	delta.Version, delta.BaseVersion = "2026.10.03", "2026.09.26"
	p.domains["screen"] = ManifestDomain{DatasetVersion: "2026.10.03", VocabularyVersion: 1, Full: full, Deltas: []ManifestFile{delta}}
	c.Exec(`UPDATE enrichment_domains SET checked_at='2000-01-01T00:00:00Z'`)
	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	var tags []string
	rows, _ = c.DB.Query(`SELECT tag FROM catalog_dataset_tags WHERE entity_id=? ORDER BY tag`, heat.ID)
	for rows.Next() {
		var v string
		rows.Scan(&v)
		tags = append(tags, v)
	}
	rows.Close()
	if len(tags) != 1 || tags[0] != "pacing:slow-burn" {
		t.Fatalf("after the delta Heat's tags are %v", tags)
	}
}

func TestDatasetRefusesATamperedManifest(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	p := newPublisher(t)
	p.domains["screen"] = ManifestDomain{DatasetVersion: "1", Full: p.build("screen-1", "full", "1", screenV1)}
	p.tamper = true
	dir := t.TempDir()
	s := publisherService(c.DB, p, dir)
	if _, err := s.Step(context.Background(), 100); err == nil {
		t.Fatal("a tampered manifest was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "portico-screen.sqlite")); err == nil {
		t.Fatal("a file was installed from a tampered manifest")
	}
}
