package enrichment

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// releaseFixture is a library with Heat (TMDB 949), a publisher holding a
// signed screen release, and a service on a controllable clock.
type releaseFixture struct {
	c   *catalogtest.Catalog
	p   *publisher
	s   *Service
	dir string
	now time.Time
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	heat := c.Movie(films, "/m/heat.mkv", "Heat", 1995)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','949')`, heat.ID)
	c.Drain()
	p := newPublisher(t)
	p.domains["screen"] = ManifestDomain{DatasetVersion: "1", VocabularyVersion: 2, Full: p.build("screen-1", "full", "1", screenV1)}
	f := &releaseFixture{c: c, p: p, dir: t.TempDir(), now: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	f.s = publisherService(c.DB, p, f.dir)
	f.s.now = func() time.Time { return f.now }
	return f
}

func (f *releaseFixture) step(t *testing.T) error {
	t.Helper()
	_, err := f.s.Step(context.Background(), 100)
	return err
}

func (f *releaseFixture) installed(t *testing.T) string {
	t.Helper()
	var version string
	if err := f.c.DB.QueryRow(`SELECT version FROM enrichment_domains WHERE domain='screen'`).Scan(&version); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "portico-screen.sqlite")); err != nil {
		return ""
	}
	return version
}

func (f *releaseFixture) manifestRequests() int { return f.p.requests["/manifest.json"] }

func refused(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), code) {
		t.Fatalf("want refusal %q, got %v", code, err)
	}
}

// The lead's ruling (1 Nov): a manifest naming a key this server doesn't
// hold is refused even when its signature is valid under the key it does.
func TestAManifestNamingAnUnknownKeyIsRefusedEvenWithAValidSignature(t *testing.T) {
	other, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, setup := range map[string]func(p *publisher){
		// Signed by the server's key, but naming another (known elsewhere) key.
		"names another real key": func(p *publisher) { p.keyID = KeyID(other) },
		"names a key nobody has": func(p *publisher) { p.keyID = "0011223344556677" },
		// Signed by another key and naming it: the signature itself fails.
		"signed by another key": func(p *publisher) { p.keyID, p.signer = KeyID(other), otherPrivate },
	} {
		t.Run(name, func(t *testing.T) {
			f := newReleaseFixture(t)
			setup(f.p)
			err := f.step(t)
			if name == "signed by another key" {
				refused(t, err, "manifest_signature")
			} else {
				refused(t, err, "manifest_key")
			}
			if f.installed(t) != "" || f.p.requests["/screen-1.sqlite.gz"] != 0 {
				t.Fatal("a file was fetched or installed under a refused manifest")
			}
		})
	}
}

func TestAnOlderSignedManifestCantRollTheDatasetBack(t *testing.T) {
	f := newReleaseFixture(t)
	f.p.sequence = 5
	if err := f.step(t); err != nil || f.installed(t) != "1" {
		t.Fatal("first release", err)
	}
	// A replay of an older, genuinely signed manifest (sequence 4, version 0).
	f.p.sequence = 4
	f.p.domains["screen"] = ManifestDomain{DatasetVersion: "0", VocabularyVersion: 2, Full: f.p.build("screen-0", "full", "0", screenV1)}
	f.c.Exec(`UPDATE enrichment_domains SET checked_at='2000-01-01T00:00:00Z'`)
	refused(t, f.step(t), "manifest_rollback")
	if f.installed(t) != "1" || f.p.requests["/screen-0.sqlite.gz"] != 0 {
		t.Fatal("an older manifest replaced the dataset")
	}
	// The same sequence again (an unchanged manifest re-read) is fine.
	f.p.sequence = 5
	f.now = f.now.Add(lastRetry)
	if err := f.step(t); err != nil {
		t.Fatal("the current manifest was refused after a replay", err)
	}
}

func TestFilesComeOnlyFromTheManifestsOrigin(t *testing.T) {
	for _, u := range []string{"https://dataset.example/screen-1.sqlite.gz", "https://cdn.example/screen-1.sqlite.gz", "//cdn.example/screen-1.sqlite.gz", "http://dataset.example/screen-1.sqlite.gz"} {
		t.Run(u, func(t *testing.T) {
			f := newReleaseFixture(t)
			d := f.p.domains["screen"]
			d.Full.URL = u
			f.p.domains["screen"] = d
			refused(t, f.step(t), "network_policy")
			if f.installed(t) != "" || f.p.requests["/screen-1.sqlite.gz"] != 0 {
				t.Fatal("a file outside the manifest's origin was fetched")
			}
		})
	}
	// Relative paths that stay on the origin are fine.
	f := newReleaseFixture(t)
	d := f.p.domains["screen"]
	d.Full.URL = "./releases/../screen-1.sqlite.gz"
	f.p.domains["screen"] = d
	if err := f.step(t); err != nil || f.installed(t) != "1" {
		t.Fatal("a relative file URL was refused", err)
	}
}

func TestADamagedFileWithAMatchingHashIsRefused(t *testing.T) {
	f := newReleaseFixture(t)
	// Rebuild the published file with a damaged b-tree page and re-sign its hash,
	// as a broken (not hostile) build would.
	full := f.p.build("screen-1", "full", "1", func(t *testing.T, db *sql.DB) {
		screenV1(t, db)
		for i := 0; i < 400; i++ {
			mustExec(t, db, `INSERT INTO works VALUES(?,1,0,0,0,NULL,2000,1,?)`, 5000000+i, strings.Repeat("x", 200))
		}
	})
	raw := gunzip(t, f.p.files["/"+full.URL])
	if len(raw) < 8*4096 {
		t.Fatal("fixture too small to damage", len(raw))
	}
	for i := 5 * 4096; i < 5*4096+64; i++ {
		raw[i] = 0xA5
	}
	full = f.p.publish("screen-1", raw)
	f.p.domains["screen"] = ManifestDomain{DatasetVersion: "1", VocabularyVersion: 2, Full: full}
	refused(t, f.step(t), "asset_integrity")
	if f.installed(t) != "" {
		t.Fatal("a damaged file was installed")
	}
}

func TestASchemaV1FileIsRefused(t *testing.T) {
	f := newReleaseFixture(t)
	f.p.domains["screen"] = ManifestDomain{DatasetVersion: "1", VocabularyVersion: 2, Full: f.p.build("screen-1", "full", "1", func(t *testing.T, db *sql.DB) {
		screenV1(t, db)
		mustExec(t, db, `PRAGMA user_version=1`)
	})}
	if err := f.step(t); err == nil || f.installed(t) != "" {
		t.Fatal("a schema 1 file was installed", err)
	}
}

func TestChecksAreWeeklyAtThisInstallsOwnOffset(t *testing.T) {
	f := newReleaseFixture(t)
	if err := f.step(t); err != nil || f.manifestRequests() != 1 {
		t.Fatal("first start must check at once", err, f.manifestRequests())
	}
	// Pin this install's offset to five hours for the arithmetic below.
	f.c.Exec(`UPDATE enrichment_state SET seed=?`, int64(5*time.Hour/time.Second)+int64(JitterWindow/time.Second)*1000)
	start := f.now
	for _, at := range []time.Duration{time.Hour, 6 * 24 * time.Hour, DefaultInterval + 5*time.Hour - time.Second} {
		f.now = start.Add(at)
		if err := f.step(t); err != nil || f.manifestRequests() != 1 {
			t.Fatalf("checked early at +%v (%d requests)", at, f.manifestRequests())
		}
	}
	f.now = start.Add(DefaultInterval + 5*time.Hour)
	if err := f.step(t); err != nil || f.manifestRequests() != 2 {
		t.Fatal("the weekly check didn't happen at its offset", err, f.manifestRequests())
	}
	// A library of a new domain is checked at once, without waiting.
	f.c.Library("b", "Books", "audiobook", "/b")
	f.now = f.now.Add(time.Minute)
	if err := f.step(t); err != nil || f.manifestRequests() != 3 {
		t.Fatal("a newly needed domain wasn't checked at once", err, f.manifestRequests())
	}
}

func TestTheOffsetSeedIsRandomPerInstallNotFromTheLibrary(t *testing.T) {
	seeds := map[int64]bool{}
	for range 3 {
		// Identical libraries: anything derived from the server or its
		// contents would give the same seed.
		f := newReleaseFixture(t)
		if err := f.step(t); err != nil {
			t.Fatal(err)
		}
		var seed int64
		if err := f.c.DB.QueryRow(`SELECT seed FROM enrichment_state`).Scan(&seed); err != nil {
			t.Fatal(err)
		}
		f.now = f.now.Add(DefaultInterval + JitterWindow)
		if err := f.step(t); err != nil {
			t.Fatal(err)
		}
		var again int64
		f.c.DB.QueryRow(`SELECT seed FROM enrichment_state`).Scan(&again)
		if again != seed || seed < 0 {
			t.Fatal("the seed changed between checks", seed, again)
		}
		seeds[seed] = true
	}
	if len(seeds) != 3 {
		t.Fatal("identical installs drew the same seed", seeds)
	}
	// The jitter is inside the window.
	for _, seed := range []int64{0, 1, int64(JitterWindow / time.Second), 1<<62 + 12345} {
		if j := (installState{seed: seed}).jitter(); j < 0 || j >= JitterWindow {
			t.Fatal("jitter outside the window", seed, j)
		}
	}
}

func TestAFailedCheckBacksOffFromAnHourToADay(t *testing.T) {
	f := newReleaseFixture(t)
	f.p.status = 503
	start := f.now
	wait := time.Hour
	elapsed := time.Duration(0)
	for attempt := 1; attempt <= 7; attempt++ {
		if err := f.step(t); err == nil || f.manifestRequests() != attempt {
			t.Fatalf("attempt %d: %v (%d requests)", attempt, err, f.manifestRequests())
		}
		var stored string
		f.c.DB.QueryRow(`SELECT error FROM enrichment_domains WHERE domain='screen'`).Scan(&stored)
		if !strings.Contains(stored, "response_status (503)") {
			t.Fatal("the failure wasn't recorded", stored)
		}
		// Not before the backoff…
		f.now = start.Add(elapsed + wait - time.Second)
		if err := f.step(t); err != nil || f.manifestRequests() != attempt {
			t.Fatalf("retried before %v", wait)
		}
		elapsed += wait
		f.now = start.Add(elapsed)
		wait = min(wait*2, 24*time.Hour)
	}
	if wait != 24*time.Hour {
		t.Fatal("the backoff never reached a day", wait)
	}
	// Recovery clears it.
	f.p.status = 0
	if err := f.step(t); err != nil || f.installed(t) != "1" {
		t.Fatal("recovery", err)
	}
	var failures int
	var retry string
	f.c.DB.QueryRow(`SELECT failures,retry_at FROM enrichment_state`).Scan(&failures, &retry)
	if failures != 0 || retry != "" {
		t.Fatal("backoff not cleared", failures, retry)
	}
}

func TestABrokenDeltaChainFallsBackToTheFullFile(t *testing.T) {
	f := newReleaseFixture(t)
	if err := f.step(t); err != nil || f.installed(t) != "1" {
		t.Fatal(err)
	}
	// Version 3 publishes only the 2→3 delta: a server on 1 takes the full file.
	full := f.p.build("screen-3", "full", "3", screenV1)
	delta := f.p.build("screen-2-3", "delta", "3", func(t *testing.T, db *sql.DB) {})
	delta.Version, delta.BaseVersion = "3", "2"
	f.p.domains["screen"] = ManifestDomain{DatasetVersion: "3", VocabularyVersion: 2, Full: full, Deltas: []ManifestFile{delta}}
	f.p.sequence = 2
	f.now = f.now.Add(DefaultInterval + JitterWindow)
	if err := f.step(t); err != nil || f.installed(t) != "3" {
		t.Fatal(err, f.installed(t))
	}
	if f.p.requests["/screen-3.sqlite.gz"] != 1 || f.p.requests["/screen-2-3.sqlite.gz"] != 0 {
		t.Fatal("expected the full file only", f.p.requests)
	}
}

// publish serves raw bytes (gzipped) under name, with their hash.
func (p *publisher) publish(name string, raw []byte) ManifestFile {
	gz := gzipBytes(p.t, raw)
	sum := sha256.Sum256(gz)
	p.files["/"+name+".sqlite.gz"] = gz
	return ManifestFile{URL: name + ".sqlite.gz", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(gz))}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func gunzip(t *testing.T, gz []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
