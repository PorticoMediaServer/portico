package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

func TestCanonicalEnrichComponentAttachedPopulation(t *testing.T) {
	w, path := testCanonicalExecutor(t)
	facts := seedQualificationPopulation(t, w, qualificationPopulationSpec{Seed: 17, Residents: 4})
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := sqliteconn.Assert(t.Context(), conn, w.config)
	if err != nil {
		t.Fatal(err)
	}
	var engineSource string
	if err := conn.QueryRowContext(t.Context(), "SELECT sqlite_source_id()").Scan(&engineSource); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	h := sha256.New()
	src, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(h, src); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyQualificationPopulation(path, filepath.Join(dir, qualificationPopulationImage), info.Size(), hex.EncodeToString(h.Sum(nil))); err != nil {
		t.Fatal(err)
	}
	m := QualificationPopulationManifest{Version: qualificationPopulationVersion, SourceCommit: strings.Repeat("a", 40), SchemaSHA256: schema.SHA256, Engine: actual.Engine, EngineSource: engineSource, GoVersion: "go1.26.10", ActualPolicy: actual, DQSDDL: actual.DQSDDL, DQSDML: actual.DQSDML, DatabaseSHA256: hex.EncodeToString(h.Sum(nil)), DatabaseBytes: info.Size(), Facts: facts}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifest = append(manifest, '\n')
	if err := os.WriteFile(filepath.Join(dir, qualificationPopulationManifestFile), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(manifest)
	UseQualificationPopulation(t, dir, hex.EncodeToString(sum[:]))
	streamPath := filepath.Join(t.TempDir(), "s.ndjson")
	f, err := os.Create(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	stream := &n1Stream{file: f, origin: time.Now(), base: map[string]any{}}
	c := newCanonicalEnrichComponent(t, 3, stream, true, false)
	for n := 0; n < 3; n++ {
		if err := c.action(t, t.Context(), n, "control"); err != nil {
			t.Fatalf("action %d: %v", n, err)
		}
	}
	if err := c.maintenance.close(); err != nil {
		t.Fatal(err)
	}
	if err := c.maintenance.drain(stream); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}
