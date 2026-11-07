package writer

// Test-only population transport. SQL population facts are supplied by the
// single source-bound seeder; this file opens no SQLite connection or issuer.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

const qualificationPopulationVersion = "portico-qualification-population-v1"
const qualificationPopulationImage = "population.sqlite"
const qualificationPopulationManifestFile = "population.json"

// QualificationPopulationManifest describes a cached immutable population, not
// a command capability, recipe, physical profile or qualification verdict.
type QualificationPopulationManifest struct {
	Version        string                       `json:"version"`
	SourceCommit   string                       `json:"source_commit"`
	SchemaSHA256   string                       `json:"schema_sha256"`
	Engine         string                       `json:"engine"`
	EngineSource   string                       `json:"engine_source"`
	GoVersion      string                       `json:"go_version"`
	ActualPolicy   sqliteconn.Settings          `json:"actual_policy"`
	DQSDDL         int                          `json:"dqs_ddl"`
	DQSDML         int                          `json:"dqs_dml"`
	DatabaseSHA256 string                       `json:"database_sha256"`
	DatabaseBytes  int64                        `json:"database_bytes"`
	Facts          qualificationPopulationFacts `json:"facts"`
}

type qualificationPopulationExportConfig struct {
	OutputDir    string `json:"output_dir"`
	SourceCommit string `json:"source_commit"`
	Seed         uint32 `json:"seed"`
	Residents    int    `json:"residents"`
}

type qualificationPopulationAttachment struct {
	dir      string
	manifest QualificationPopulationManifest
	consumed bool
}

var qualificationPopulationAttachments = struct {
	sync.Mutex
	byTest map[*testing.T]*qualificationPopulationAttachment
}{byTest: map[*testing.T]*qualificationPopulationAttachment{}}
var qualificationPopulationSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)
var qualificationPopulationCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// UseQualificationPopulation binds the NEXT existing canonical fixture clone to
// one verified artifact. Call from the exact test owner before its constructor.
// ExpectedManifestSHA comes from QL's trusted artifact receipt, not the file
// being checked. The existing opener/policy remains unchanged. The source image
// is never opened in place, mutated, ATTACHed or treated as runtime authority.
func UseQualificationPopulation(t *testing.T, dir, expectedManifestSHA string) QualificationPopulationManifest {
	t.Helper()
	m, err := readQualificationPopulation(dir, expectedManifestSHA)
	if err != nil {
		t.Fatal(err)
	}
	qualificationPopulationAttachments.Lock()
	if _, exists := qualificationPopulationAttachments.byTest[t]; exists {
		qualificationPopulationAttachments.Unlock()
		t.Fatal("population already attached to this test")
	}
	a := &qualificationPopulationAttachment{dir: dir, manifest: m}
	qualificationPopulationAttachments.byTest[t] = a
	qualificationPopulationAttachments.Unlock()
	t.Cleanup(func() {
		qualificationPopulationAttachments.Lock()
		used := a.consumed
		delete(qualificationPopulationAttachments.byTest, t)
		qualificationPopulationAttachments.Unlock()
		if !used {
			t.Error("qualification population was not consumed by the same fixture owner")
		}
	})
	return m
}

func qualificationCanonicalFixturePath(t *testing.T) string {
	t.Helper()
	qualificationPopulationAttachments.Lock()
	a := qualificationPopulationAttachments.byTest[t]
	if a == nil {
		qualificationPopulationAttachments.Unlock()
		return schematest.Clone(t)
	}
	if a.consumed {
		qualificationPopulationAttachments.Unlock()
		t.Fatal("qualification population already consumed")
	}
	a.consumed = true
	qualificationPopulationAttachments.Unlock()
	destination := filepath.Join(t.TempDir(), "catalog.sqlite")
	if err := copyQualificationPopulation(filepath.Join(a.dir, qualificationPopulationImage), destination, a.manifest.DatabaseBytes, a.manifest.DatabaseSHA256); err != nil {
		t.Fatal(err)
	}
	return destination
}

func readQualificationPopulation(dir, expectedManifestSHA string) (QualificationPopulationManifest, error) {
	var m QualificationPopulationManifest
	if !filepath.IsAbs(dir) || !qualificationPopulationSHA.MatchString(expectedManifestSHA) {
		return m, errors.New("absolute artifact directory and pinned manifest SHA256 required")
	}
	p := filepath.Join(dir, qualificationPopulationManifestFile)
	info, err := os.Lstat(p)
	if err != nil {
		return m, err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return m, errors.New("invalid population manifest file")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return m, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expectedManifestSHA {
		return m, errors.New("population manifest digest mismatch")
	}
	if err := decodeQualificationPopulationJSON(data, &m); err != nil {
		return m, err
	}
	fields, err := qualificationPopulationRequiredFields(data, "version", "source_commit", "schema_sha256", "engine", "engine_source", "go_version", "actual_policy", "dqs_ddl", "dqs_dml", "database_sha256", "database_bytes", "facts")
	if err != nil {
		return m, err
	}
	if _, err = qualificationPopulationRequiredFields(fields["actual_policy"], "Engine", "JournalMode", "ForeignKeys", "Synchronous", "AutoCheckpoint", "CacheSize", "BusyTimeout", "MmapSize", "QueryOnly", "TempStore"); err != nil {
		return m, err
	}
	if _, err = qualificationPopulationRequiredFields(fields["facts"], "version", "seed", "residents", "library_id", "library_key", "first_id", "last_id", "counts", "logical_sha256"); err != nil {
		return m, err
	}

	if m.Version != qualificationPopulationVersion || m.SchemaSHA256 != schema.SHA256 || m.Engine != "3.53.4" || m.EngineSource == "" || m.GoVersion != "go1.26.10" || !qualificationPopulationCommit.MatchString(m.SourceCommit) || !qualificationPopulationSHA.MatchString(m.DatabaseSHA256) || m.DatabaseBytes <= 0 || m.DatabaseBytes > 8<<30 {
		return m, errors.New("population provenance/format mismatch")
	}
	policy := m.ActualPolicy
	if policy.Engine != m.Engine || policy.JournalMode != "wal" || policy.ForeignKeys != 1 || policy.Synchronous != 2 || policy.AutoCheckpoint != 0 || policy.CacheSize != -1024 || policy.BusyTimeout != 0 || policy.MmapSize != 0 || policy.QueryOnly != 0 || policy.TempStore != 1 || policy.DQSDDL != 0 || policy.DQSDML != 0 || m.DQSDDL != 0 || m.DQSDML != 0 {
		return m, errors.New("population actual writer policy mismatch")
	}
	if m.Facts.Version != "canonical-movies-v1" || m.Facts.Counts == nil || m.Facts.Residents < 1 || m.Facts.Residents > 1000000 || m.Facts.LibraryID != 7 || m.Facts.LibraryKey != "qualification-movies" || !qualificationPopulationSHA.MatchString(m.Facts.LogicalSHA256) {
		return m, errors.New("invalid population facts")
	}
	if m.Facts.FirstID != qualificationMovieIDBase+1 || m.Facts.LastID != qualificationMovieIDBase+int64(m.Facts.Residents) {
		return m, errors.New("population ID range mismatch")
	}
	for _, name := range []string{"catalog_subjects", "catalog_entities", "catalog_assets", "catalog_asset_links", "catalog_scalar_contributions", "catalog_asset_contributions", "current_movies"} {
		if count, observed := m.Facts.Counts[name]; !observed || count != int64(m.Facts.Residents) {
			return m, fmt.Errorf("population count mismatch: %s", name)
		}
	}
	for _, name := range []string{"libraries", "catalog_libraries", "catalog_kinds", "catalog_membership_clock", "catalog_age_item_counts"} {
		if count, observed := m.Facts.Counts[name]; !observed || count != 1 {
			return m, fmt.Errorf("population singleton count mismatch: %s", name)
		}
	}
	for name, count := range m.Facts.Counts {
		if count < 0 {
			return m, fmt.Errorf("negative population count: %s", name)
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(filepath.Join(dir, qualificationPopulationImage) + suffix); !errors.Is(err, os.ErrNotExist) {
			return m, fmt.Errorf("population sidecar present or unreadable: %s", suffix)
		}
	}
	image, err := os.Lstat(filepath.Join(dir, qualificationPopulationImage))
	if err != nil {
		return m, err
	}
	if !image.Mode().IsRegular() || image.Size() != m.DatabaseBytes {
		return m, errors.New("population image length/type mismatch")
	}
	return m, nil
}
func qualificationPopulationRequiredFields(data []byte, names ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, name := range names {
		value, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("missing population field %s", name)
		}
	}
	return fields, nil
}

func decodeQualificationPopulationJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("extra population JSON data")
	}
	return nil
}

func copyQualificationPopulation(source, destination string, size int64, want string) (err error) {
	if size <= 0 || size > 8<<30 || !qualificationPopulationSHA.MatchString(want) {
		return errors.New("invalid population copy bounds")
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.Mode().IsRegular() || sourceInfo.Size() != size {
		return errors.New("invalid population source type/length")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(sourceInfo, openedInfo) {
		return errors.New("population source changed before copy")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(destination)
		}
	}()
	h := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(out, h), io.LimitReader(input, size+1), make([]byte, 64<<10))
	syncErr := out.Sync()
	closeErr := out.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != want {
		return errors.New("population copy digest/length mismatch")
	}
	if err = input.Close(); err != nil {
		return err
	}
	success = true
	return nil
}

func TestExportQualificationPopulation(t *testing.T) {
	if os.Getenv("PORTICO_QUAL_FIXTURE") != "1" {
		t.Skip("explicit qualification population export only")
	}
	if runtime.Version() != "go1.26.10" {
		t.Fatal("population export requires pinned Go1.26.10", runtime.Version())
	}
	file, err := os.Open(os.Getenv("PORTICO_QUAL_FIXTURE_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 16385))
	if err = errors.Join(readErr, file.Close()); err != nil || len(data) > 16384 {
		t.Fatal("invalid export config", err)
	}
	var c qualificationPopulationExportConfig
	if err = decodeQualificationPopulationJSON(data, &c); err != nil {
		t.Fatal(err)
	}
	if _, err = qualificationPopulationRequiredFields(data, "output_dir", "source_commit", "seed", "residents"); err != nil {
		t.Fatal(err)
	}

	if !filepath.IsAbs(c.OutputDir) || !qualificationPopulationCommit.MatchString(c.SourceCommit) || c.Residents < 1 || c.Residents > 1000000 {
		t.Fatal("invalid export provenance/bounds")
	}
	if c.Residents >= 100000 && (os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("PORTICO_QUAL_FIXTURE_HOSTED") != "1") {
		t.Fatal("100k+ population export belongs on explicit GitHub-hosted job")
	}
	if err = os.MkdirAll(c.OutputDir, 0700); err != nil {
		t.Fatal(err)
	}
	w, path := testCanonicalExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := sqliteconn.Assert(t.Context(), conn, w.config)
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	var engineSource string
	if err = conn.QueryRowContext(t.Context(), "SELECT sqlite_source_id()").Scan(&engineSource); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	facts := seedQualificationPopulation(t, w, qualificationPopulationSpec{Seed: c.Seed, Residents: c.Residents})
	// Reassert the actual physical policy after all seeding, rather than relying
	// on a one-time assertion that could predate a replacement connection.
	conn, err = w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err = sqliteconn.Assert(t.Context(), conn, w.config)
	closeErr := conn.Close()
	if err = errors.Join(err, closeErr); err != nil {
		t.Fatal(err)
	}

	if err = w.close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err = os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("export has sidecar", suffix, err)
		}
	}
	image, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceFile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	_, err = io.Copy(h, sourceFile)
	err = errors.Join(err, sourceFile.Close())
	if err != nil {
		t.Fatal(err)
	}
	m := QualificationPopulationManifest{Version: qualificationPopulationVersion, SourceCommit: c.SourceCommit, SchemaSHA256: schema.SHA256, Engine: actual.Engine, EngineSource: engineSource, GoVersion: runtime.Version(), ActualPolicy: actual, DQSDDL: actual.DQSDDL, DQSDML: actual.DQSDML, DatabaseSHA256: hex.EncodeToString(h.Sum(nil)), DatabaseBytes: image.Size(), Facts: facts}
	if err = copyQualificationPopulation(path, filepath.Join(c.OutputDir, qualificationPopulationImage), m.DatabaseBytes, m.DatabaseSHA256); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifest = append(manifest, '\n')
	output, err := os.OpenFile(filepath.Join(c.OutputDir, qualificationPopulationManifestFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = output.Write(manifest)
	err = errors.Join(err, output.Sync(), output.Close())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("QUALIFICATION_POPULATION_V1 residents=%d seed=%d image_bytes=%d image_sha256=%s manifest_sha256=%x", facts.Residents, facts.Seed, m.DatabaseBytes, m.DatabaseSHA256, sha256.Sum256(manifest))
}
