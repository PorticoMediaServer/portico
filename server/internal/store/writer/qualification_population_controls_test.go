package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

func TestQualificationPopulationCopy(t *testing.T) {
	data := []byte("immutable population transport control")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "copy")
	if err := copyQualificationPopulation(source, destination, int64(len(data)), digest); err != nil {
		t.Fatal(err)
	}
	if err := copyQualificationPopulation(source, destination, int64(len(data)), digest); err == nil {
		t.Fatal("overwrote existing destination")
	}
	for _, tc := range []struct {
		name string
		size int64
		hash string
	}{
		{"digest", int64(len(data)), hex.EncodeToString(make([]byte, 32))},
		{"length", int64(len(data) - 1), digest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, tc.name)
			if err := copyQualificationPopulation(source, out, tc.size, tc.hash); err == nil {
				t.Fatal("accepted corrupted image")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("partial image retained: %v", err)
			}
		})
	}
	linked := filepath.Join(dir, "link")
	if err := os.Symlink(source, linked); err != nil {
		t.Fatal(err)
	}
	if err := copyQualificationPopulation(linked, filepath.Join(dir, "linked-copy"), int64(len(data)), digest); err == nil {
		t.Fatal("accepted symlink image")
	}
}

func TestQualificationPopulationClosedJSON(t *testing.T) {
	for _, raw := range []string{`{"output_dir":"/tmp/fixture","unexpected":1}`, `{} {}`, `{`} {
		var c qualificationPopulationExportConfig
		if err := decodeQualificationPopulationJSON([]byte(raw), &c); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var c qualificationPopulationExportConfig
	if err := decodeQualificationPopulationJSON([]byte(`{"output_dir":"/tmp/fixture","source_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","seed":0,"residents":1}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Seed != 0 || c.Residents != 1 {
		t.Fatal("lost observed zero seed")
	}
}

func TestQualificationPopulationRequiredZeroFields(t *testing.T) {
	if _, err := qualificationPopulationRequiredFields([]byte(`{"seed":0}`), "seed"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"seed":null}`} {
		if _, err := qualificationPopulationRequiredFields([]byte(raw), "seed"); err == nil {
			t.Fatalf("missing seed became observed zero: %s", raw)
		}
	}
}

// These are file-transport controls, not a native SQLite population or receipt.
func qualificationPopulationTransportControl(t *testing.T) (string, QualificationPopulationManifest, string) {
	t.Helper()
	dir := t.TempDir()
	data := []byte("synthetic transport image; not SQLite")
	digest := sha256.Sum256(data)
	m := QualificationPopulationManifest{
		Version: qualificationPopulationVersion, SourceCommit: strings.Repeat("a", 40), SchemaSHA256: schema.SHA256,
		Engine: "3.53.4", EngineSource: "synthetic protocol control", GoVersion: "go1.26.10",
		ActualPolicy:   sqliteconn.Settings{Engine: "3.53.4", JournalMode: "wal", ForeignKeys: 1, Synchronous: 2, CacheSize: -1024, TempStore: 1},
		DatabaseSHA256: hex.EncodeToString(digest[:]), DatabaseBytes: int64(len(data)),
		Facts: qualificationPopulationFacts{Version: "canonical-movies-v1", Residents: 1, LibraryID: 7, LibraryKey: "qualification-movies", FirstID: 1000000001, LastID: 1000000001, Counts: map[string]int64{"catalog_entities": 1}, LogicalSHA256: strings.Repeat("b", 64)},
	}
	for _, name := range []string{"catalog_subjects", "catalog_entities", "catalog_assets", "catalog_asset_links", "catalog_scalar_contributions", "catalog_asset_contributions", "current_movies", "libraries", "catalog_libraries", "catalog_kinds", "catalog_membership_clock", "catalog_age_item_counts"} {
		m.Facts.Counts[name] = 1
	}
	if err := os.WriteFile(filepath.Join(dir, qualificationPopulationImage), data, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, qualificationPopulationManifestFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(raw)
	return dir, m, hex.EncodeToString(pin[:])
}

func TestQualificationPopulationManifestAndIndependentCopies(t *testing.T) {
	dir, m, pin := qualificationPopulationTransportControl(t)
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			observed := UseQualificationPopulation(t, dir, pin)
			if observed.DatabaseSHA256 != m.DatabaseSHA256 {
				t.Fatal("changed image identity")
			}
			path := qualificationCanonicalFixturePath(t)
			if path == filepath.Join(dir, qualificationPopulationImage) {
				t.Fatal("opened immutable source in place")
			}
			if err := os.WriteFile(path, []byte("mutated private copy"), 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
	input, err := os.ReadFile(filepath.Join(dir, qualificationPopulationImage))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input)
	if hex.EncodeToString(digest[:]) != m.DatabaseSHA256 {
		t.Fatal("consumer mutated cached source")
	}
}

func TestQualificationPopulationRejectsMissingPolicyAndSidecars(t *testing.T) {
	t.Run("wrong_actual_policy", func(t *testing.T) {
		dir, m, _ := qualificationPopulationTransportControl(t)
		m.ActualPolicy.ForeignKeys = 0
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, qualificationPopulationManifestFile), raw, 0600); err != nil {
			t.Fatal(err)
		}
		pin := sha256.Sum256(raw)
		if _, err = readQualificationPopulation(dir, hex.EncodeToString(pin[:])); err == nil {
			t.Fatal("accepted disabled actual foreign keys")
		}
	})
	t.Run("unobserved_zero", func(t *testing.T) {
		dir, _, _ := qualificationPopulationTransportControl(t)
		p := filepath.Join(dir, qualificationPopulationManifestFile)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "dqs_ddl")
		raw, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
		pin := sha256.Sum256(raw)
		if _, err = readQualificationPopulation(dir, hex.EncodeToString(pin[:])); err == nil {
			t.Fatal("missing DQS became asserted zero")
		}
	})
	t.Run("sidecar", func(t *testing.T) {
		dir, _, pin := qualificationPopulationTransportControl(t)
		if err := os.WriteFile(filepath.Join(dir, qualificationPopulationImage)+"-wal", nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readQualificationPopulation(dir, pin); err == nil {
			t.Fatal("accepted unjoined WAL")
		}
	})
}

func TestQualificationPopulationNativeRoundTrip(t *testing.T) {
	// A tiny functional artifact, with deliberately synthetic source identity;
	// never a population-size, host-profile or qualification measurement.
	output := filepath.Join(t.TempDir(), "artifact")
	config := qualificationPopulationExportConfig{OutputDir: output, SourceCommit: strings.Repeat("c", 40), Seed: 0, Residents: 16}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORTICO_QUAL_FIXTURE", "1")
	t.Setenv("PORTICO_QUAL_FIXTURE_CONFIG", configPath)
	TestExportQualificationPopulation(t)
	raw, err = os.ReadFile(filepath.Join(output, qualificationPopulationManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(raw)
	manifest := UseQualificationPopulation(t, output, hex.EncodeToString(pin[:]))
	w, _ := testCanonicalExecutor(t)
	var count int
	if err = w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_entities WHERE library_id=7 AND kind=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 16 || manifest.Facts.Residents != 16 {
		t.Fatalf("copied population differs: count=%d facts=%+v", count, manifest.Facts)
	}
}
