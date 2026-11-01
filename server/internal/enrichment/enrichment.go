// Package enrichment keeps the Portico title dataset on this server: an open
// (CC BY-SA 4.0) SQLite file per domain of mood, theme, pacing and other tags
// and similar-title lists for popular titles, built offline and published
// with a signed manifest (Title Enrichment Dataset — Plan (Codex).md).
//
// The server downloads the whole file of each domain it has libraries for
// (so the host never learns what any library holds): on first need, then a
// weekly check at a per-install random offset. It verifies the manifest's
// Ed25519 signature, its key id and that it is no older than the last one
// accepted, then each file's SHA-256, size and integrity, and matches library works to
// dataset works by exact provider id (TMDB, TVDB, AniList, IMDb), never by
// title. A matched work gets its tags (catalog_dataset_tags, read by the
// recommendation facets as 'x:<tag>') and similar titles (catalog_similar,
// source 'portico-dataset', stored by the target's provider id). Works that
// gain provider ids later are queued (enrichment_jobs) and matched as they
// arrive. A server without the file works exactly as before.
package enrichment

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite" // the dataset file is a plain SQLite file

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// The published dataset: its manifest and the Ed25519 key it's signed with.
// The key stays empty until Justin signs the first release, so nothing is
// downloaded before then. PORTICO_DATASET_MANIFEST and
// PORTICO_DATASET_PUBLIC_KEY (standard base64) override them, for testing a
// release before it ships.
const (
	PublishedManifest  = "https://data.getportico.tv/v2/manifest.json"
	PublishedPublicKey = ""
)

// ConfigFromEnvironment is the shipped configuration with the overrides
// applied; dir is where domain files are kept.
func ConfigFromEnvironment(dir string) Config {
	manifest, key := PublishedManifest, PublishedPublicKey
	if v := os.Getenv("PORTICO_DATASET_MANIFEST"); v != "" {
		manifest = v
	}
	if v := os.Getenv("PORTICO_DATASET_PUBLIC_KEY"); v != "" {
		key = v
	}
	cfg := Config{ManifestURL: manifest, Dir: dir}
	if raw, err := base64.StdEncoding.DecodeString(key); err == nil && len(raw) == ed25519.PublicKeySize {
		cfg.PublicKey = ed25519.PublicKey(raw)
	}
	return cfg
}

// MaxFileBytes caps a decompressed domain file (the dataset's budget is under
// 1 GB per file): a larger one is refused, whatever the manifest says.
const MaxFileBytes = 2 << 30

// SchemaVersion is the dataset schema this server reads (the file's
// PRAGMA user_version and the manifest's schema_version).
const SchemaVersion = 2

// The check cadence: weekly, plus a fixed offset of up to JitterWindow drawn
// once per install from a random seed (never from anything about the server or
// its library), so servers spread out over the window. A failed check is
// retried after an hour, doubling to at most a day.
const (
	DefaultInterval = 7 * 24 * time.Hour
	JitterWindow    = 48 * time.Hour
	firstRetry      = time.Hour
	lastRetry       = 24 * time.Hour
)

// Config says where the dataset comes from. An empty ManifestURL or key
// leaves the feature off.
type Config struct {
	ManifestURL string
	PublicKey   ed25519.PublicKey
	Dir         string        // where domain files are kept
	Client      *http.Client  // optional timeout; egress policy always belongs to the service
	Interval    time.Duration // how often the manifest is checked (default a week)
}

// KeyID names a public key in the manifest: the first 8 bytes of SHA-256 over
// the raw key, hex.
func KeyID(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// Service keeps the dataset: Step downloads and imports, bounded per call.
type Service struct {
	db       *sql.DB
	cfg      Config
	now      func() time.Time
	resolver remotemedia.Resolver
}

func New(db *sql.DB, cfg Config) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	s := &Service{db: db, cfg: cfg, now: time.Now}
	s.cfg.Client = s.datasetClient(cfg.Client)
	return s
}

// Enabled reports whether the dataset is configured.
func (s *Service) Enabled() bool {
	return s.cfg.ManifestURL != "" && len(s.cfg.PublicKey) == ed25519.PublicKeySize
}

// domainKinds are the library kinds whose works each domain describes, and
// the entity kinds those works are.
// (catalog_libraries.kind: 1 film, 2 TV, 3 anime, 4 music, 5 audiobook.)
var domainKinds = map[string]struct {
	libraries []int
	entities  []int
}{
	"screen": {[]int{1, 2, 3}, []int{1, 2}},
	"music":  {[]int{4}, []int{5, 6}},
	"books":  {[]int{5}, []int{8}},
}

// datasetProviders maps the dataset's provider codes to the server's
// provider/kind vocabulary (catalog_external_ids, catalog_similar).
var datasetProviders = map[int][2]string{
	1: {"tmdb", "movie"}, 2: {"tmdb", "tv"}, 3: {"tvdb", "series"}, 4: {"tvdb", "movie"},
	5: {"anilist", "anime"}, 6: {"imdb", "title"},
}

// similarTargetOrder is which of a similar work's ids names it, most useful
// first (the ids servers most often hold).
var similarTargetOrder = []int{1, 2, 3, 5, 4, 6}

// Step runs one bounded pass: a manifest check when one is due, then up to
// limit queued works matched. It returns whether there may be more to do.
// While the owner has the dataset switched off it makes no request and
// removes, in bounded batches, everything a download put on this server.
func (s *Service) Step(ctx context.Context, limit int) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	on, err := s.switchedOn(ctx)
	if err != nil {
		return false, err
	}
	if !on {
		return s.purge(ctx, limit)
	}
	domains, err := s.neededDomains(ctx)
	if err != nil {
		return false, err
	}
	due, err := s.dueDomains(ctx, domains)
	if err != nil {
		return false, err
	}
	if len(due) > 0 {
		if err = s.check(ctx, due); err != nil {
			return false, err
		}
	}
	return s.drainJobs(ctx, limit)
}

// check fetches the manifest once and brings every due domain to it. A failure
// is recorded on the domain and backs off the next check.
func (s *Service) check(ctx context.Context, due []string) error {
	m, err := s.FetchManifest(ctx)
	if err == nil {
		err = s.accept(ctx, m)
	}
	done := 0
	for ; err == nil && done < len(due); done++ {
		err = s.refresh(ctx, due[done], m)
	}
	if err != nil {
		if done > 0 {
			done-- // the domain that failed
		}
		for _, domain := range due[done:] {
			_ = s.noteFailure(ctx, domain, err)
		}
		_ = s.backOff(ctx)
		return err
	}
	return s.recovered(ctx)
}

// installState is this install's dataset state: the jitter seed, drawn once
// at random, and the newest manifest sequence accepted.
type installState struct {
	seed     int64
	sequence int64
	failures int
	retryAt  time.Time
}

func (s *Service) state(ctx context.Context) (installState, error) {
	var st installState
	var retry string
	err := s.db.QueryRowContext(ctx, `SELECT seed,sequence,failures,retry_at FROM enrichment_state WHERE id=1`).Scan(&st.seed, &st.sequence, &st.failures, &retry)
	if errors.Is(err, sql.ErrNoRows) {
		var raw [8]byte
		if _, err = rand.Read(raw[:]); err != nil {
			return st, err
		}
		seed := int64(binary.BigEndian.Uint64(raw[:]) >> 1)
		if err = s.writeState(ctx, `INSERT INTO enrichment_state(id,seed) VALUES(1,?) ON CONFLICT(id) DO NOTHING`, seed); err != nil {
			return st, err
		}
		return s.state(ctx)
	}
	if err != nil {
		return st, err
	}
	if retry != "" {
		st.retryAt, _ = time.Parse(time.RFC3339, retry)
	}
	return st, nil
}

// jitter is this install's fixed offset within the window.
func (st installState) jitter() time.Duration {
	return time.Duration(st.seed%int64(JitterWindow/time.Second)) * time.Second
}

func (s *Service) writeState(ctx context.Context, statement string, args ...any) error {
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	defer w.Rollback()
	if _, err = w.Tx().ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	return w.Commit()
}

// accept refuses a manifest older than the newest one accepted, then records it.
func (s *Service) accept(ctx context.Context, m Manifest) error {
	st, err := s.state(ctx)
	if err != nil {
		return err
	}
	if m.Sequence < st.sequence {
		return datasetFailure("manifest_rollback", s.cfg.ManifestURL)
	}
	return s.writeState(ctx, `UPDATE enrichment_state SET sequence=max(sequence,?) WHERE id=1`, m.Sequence)
}

func (s *Service) backOff(ctx context.Context) error {
	st, err := s.state(ctx)
	if err != nil {
		return err
	}
	wait := lastRetry
	if st.failures < 5 {
		wait = min(firstRetry<<st.failures, lastRetry)
	}
	return s.writeState(ctx, `UPDATE enrichment_state SET failures=failures+1,retry_at=? WHERE id=1`, s.now().Add(wait).UTC().Format(time.RFC3339))
}

func (s *Service) recovered(ctx context.Context) error {
	return s.writeState(ctx, `UPDATE enrichment_state SET failures=0,retry_at='' WHERE id=1`)
}

// switchedOn reads the owner's switch (Settings › Metadata › Recommendations,
// recommendations.titleDataset). A server that never saved it has it on.
func (s *Service) switchedOn(ctx context.Context) (bool, error) {
	var on sql.NullBool
	err := s.db.QueryRowContext(ctx, `SELECT "recommendations.titleDataset" FROM server_settings WHERE singleton=1`).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return !on.Valid || on.Bool, err
}

// purge removes the dataset from this server: the domain files, then the
// imported rows in batches of limit works, then the vocabulary, queue and
// per-domain state. The install's seed and newest accepted manifest sequence
// stay, so switching back on keeps the rollback guard. It returns whether
// there is more to remove.
func (s *Service) purge(ctx context.Context, limit int) (bool, error) {
	for _, domain := range []string{"screen", "music", "books"} {
		if err := os.Remove(s.path(domain)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	for _, pattern := range []string{"download-*.gz", "dataset-*.sqlite"} {
		scratch, _ := filepath.Glob(filepath.Join(s.cfg.Dir, pattern))
		for _, name := range scratch {
			_ = os.Remove(name)
		}
	}
	works, err := scanIDs(s.db.QueryContext(ctx, `SELECT entity_id FROM catalog_dataset_works ORDER BY entity_id LIMIT ?`, limit))
	if err != nil {
		return false, err
	}
	batch := s.boundedWrites(ctx)
	defer batch.rollback()
	for _, work := range works {
		if err = removeWork(ctx, batch, work); err != nil {
			return false, err
		}
	}
	if err = batch.commit(); err != nil {
		return false, err
	}
	if len(works) == limit {
		return true, nil
	}
	// Nothing imported is left: the vocabulary (each row also moves its
	// table bound), the per-domain state and the queue go, a plan at a time.
	// Works keep gaining provider ids while the switch is off; their queue
	// entries mean nothing until it is on again, when every work is matched.
	chunk := int(dbwork.DefaultPlan(dbwork.ClassBackgroundMedia).Rows) / 2
	for _, q := range []string{
		`DELETE FROM catalog_dataset_vocabulary WHERE tag IN(SELECT tag FROM catalog_dataset_vocabulary LIMIT ?)`,
		`DELETE FROM enrichment_jobs WHERE entity_id IN(SELECT entity_id FROM enrichment_jobs LIMIT ?)`,
		`DELETE FROM enrichment_domains WHERE domain IN(SELECT domain FROM enrichment_domains LIMIT ?)`,
	} {
		for removed := 0; removed < limit; {
			n, err := s.deleteChunk(ctx, q, chunk)
			if err != nil {
				return false, err
			}
			if n < int64(chunk) {
				break
			}
			if removed += int(n); removed >= limit {
				return true, nil
			}
		}
	}
	return false, nil
}

// deleteChunk runs one bounded DELETE … LIMIT ? in its own write.
func (s *Service) deleteChunk(ctx context.Context, statement string, chunk int) (int64, error) {
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return 0, err
	}
	defer w.Rollback()
	result, err := w.Tx().ExecContext(ctx, statement, chunk)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return n, w.Commit()
}

func (s *Service) neededDomains(ctx context.Context) ([]string, error) {
	var out []string
	for _, domain := range []string{"screen", "music", "books"} {
		kinds, _ := json.Marshal(domainKinds[domain].libraries)
		var any bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_libraries WHERE retired=0 AND kind IN(SELECT value FROM json_each(?)))`, string(kinds)).Scan(&any); err != nil {
			return nil, err
		}
		if any {
			out = append(out, domain)
		}
	}
	return out, nil
}

// dueDomains is the needed domains whose check is due: never checked, a
// published file missing on disk, or the weekly check (plus this install's
// offset) reached. Nothing is due while a failed check is backing off.
func (s *Service) dueDomains(ctx context.Context, domains []string) ([]string, error) {
	st, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if now.Before(st.retryAt) {
		return nil, nil
	}
	var due []string
	for _, domain := range domains {
		var version, checked string
		err := s.db.QueryRowContext(ctx, `SELECT version,checked_at FROM enrichment_domains WHERE domain=?`, domain).Scan(&version, &checked)
		if errors.Is(err, sql.ErrNoRows) {
			due = append(due, domain)
			continue
		}
		if err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339, checked); err != nil || !now.Before(at.Add(s.cfg.Interval+st.jitter())) {
			due = append(due, domain)
			continue
		}
		if _, statErr := os.Stat(s.path(domain)); version != "" && statErr != nil {
			due = append(due, domain)
		}
	}
	return due, nil
}

func (s *Service) path(domain string) string {
	return filepath.Join(s.cfg.Dir, "portico-"+domain+".sqlite")
}

// Manifest is the signed list of published files. Sequence grows by one per
// release: a server refuses a manifest older than the last it accepted, so an
// old signed manifest can't be replayed to roll it back.
type Manifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Sequence      int64                     `json:"sequence"`
	IssuedAt      string                    `json:"issued_at"`
	KeyID         string                    `json:"key_id"`
	Domains       map[string]ManifestDomain `json:"domains"`
}

type ManifestDomain struct {
	DatasetVersion    string         `json:"dataset_version"`
	VocabularyVersion int            `json:"vocabulary_version"`
	Full              ManifestFile   `json:"full"`
	Deltas            []ManifestFile `json:"deltas"`
}

type ManifestFile struct {
	Version     string `json:"version,omitempty"`
	BaseVersion string `json:"base_version,omitempty"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	// UncompressedBytes is the file's signed size once decompressed:
	// decompression stops there, so a damaged but signed file can't fill the
	// disk, and a file of any other size is refused.
	UncompressedBytes int64 `json:"uncompressed_bytes"`
}

// FetchManifest downloads the manifest and its detached signature and
// verifies the signature over the manifest's exact bytes before parsing it.
func (s *Service) FetchManifest(ctx context.Context) (Manifest, error) {
	var m Manifest
	body, err := s.get(ctx, s.cfg.ManifestURL, 1<<20)
	if err != nil {
		return m, err
	}
	signatureURL, err := datasetURL(s.cfg.ManifestURL)
	if err != nil {
		return m, err
	}
	signatureURL.Path += ".sig"
	if signatureURL.RawPath != "" {
		signatureURL.RawPath += ".sig"
	}
	sig, err := s.get(ctx, signatureURL.String(), 4096)
	if err != nil {
		return m, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(s.cfg.PublicKey, body, signature) {
		return m, datasetFailure("manifest_signature", s.cfg.ManifestURL)
	}
	if err = json.Unmarshal(body, &m); err != nil {
		return m, datasetFailure("manifest_format", s.cfg.ManifestURL)
	}
	// The key id is inside the signed bytes: a manifest that names any key
	// other than the one it verified under is refused, even with a valid
	// signature.
	if m.KeyID != KeyID(s.cfg.PublicKey) {
		return m, datasetFailure("manifest_key", s.cfg.ManifestURL)
	}
	if m.SchemaVersion != SchemaVersion {
		return m, datasetFailure("schema_unsupported", s.cfg.ManifestURL)
	}
	if m.Sequence < 1 {
		return m, datasetFailure("manifest_format", s.cfg.ManifestURL)
	}
	return m, nil
}

// refresh brings one domain's file to the manifest's version: its deltas when
// a chain leads from the installed version, else the full file; then every
// library work of the domain is matched again.
func (s *Service) refresh(ctx context.Context, domain string, m Manifest) error {
	d, ok := m.Domains[domain]
	if !ok {
		return s.noteChecked(ctx, domain, "", nil)
	}
	var installed string
	var err error
	if err = s.db.QueryRowContext(ctx, `SELECT version FROM enrichment_domains WHERE domain=?`, domain).Scan(&installed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, statErr := os.Stat(s.path(domain)); statErr != nil {
		installed = ""
	}
	if installed == d.DatasetVersion {
		return s.noteChecked(ctx, domain, installed, nil)
	}
	if err = os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
		return err
	}
	if chain := deltaChain(d.Deltas, installed, d.DatasetVersion); installed != "" && chain != nil {
		for _, delta := range chain {
			if err = s.applyDelta(ctx, domain, delta); err != nil {
				return err
			}
		}
	} else {
		tmp, err := s.download(ctx, d.Full)
		if err != nil {
			return err
		}
		if err = checkFile(tmp, "full"); err != nil {
			os.Remove(tmp)
			return err
		}
		if err = os.Rename(tmp, s.path(domain)); err != nil {
			return err
		}
	}
	if err = s.ImportAll(ctx, domain); err != nil {
		return err
	}
	return s.noteChecked(ctx, domain, d.DatasetVersion, nil)
}

// deltaChain is the deltas from installed to target, in order, or nil.
func deltaChain(deltas []ManifestFile, installed, target string) []ManifestFile {
	var chain []ManifestFile
	at := installed
	for at != target {
		found := false
		for _, d := range deltas {
			if d.BaseVersion == at {
				chain = append(chain, d)
				at, found = d.Version, true
				break
			}
		}
		if !found || len(chain) > len(deltas) {
			return nil
		}
	}
	return chain
}

// download fetches a published (gzip) file, checks its size and SHA-256, and
// writes the decompressed SQLite file next to the installed one.
func (s *Service) download(ctx context.Context, f ManifestFile) (string, error) {
	target, err := datasetURL(s.cfg.ManifestURL)
	if err != nil {
		return "", err
	}
	// Files are named relative to the manifest: one signed origin serves
	// everything, and a manifest can't send the server anywhere else.
	ref, err := url.Parse(f.URL)
	if err != nil || ref.IsAbs() || ref.Host != "" || ref.User != nil || strings.HasPrefix(f.URL, "//") || strings.ContainsAny(f.URL, "\\\x00\r\n") {
		return "", datasetFailure("network_policy", s.cfg.ManifestURL)
	}
	origin := datasetOrigin(target)
	target = target.ResolveReference(ref)
	if datasetOrigin(target) != origin {
		return "", datasetFailure("network_policy", s.cfg.ManifestURL)
	}
	if f.Bytes < 1 || f.Bytes > MaxFileBytes || f.UncompressedBytes < 1 || f.UncompressedBytes > MaxFileBytes {
		return "", datasetFailure("asset_too_large", target.String())
	}
	// Room for the download, the decompressed file and the producer floor
	// that protects the database's volume.
	if !diskspace.Room(s.cfg.Dir, f.Bytes+f.UncompressedBytes+diskspace.ProducerFloor) {
		return "", datasetFailure("disk_space", target.String())
	}
	res, err := s.openDatasetURL(ctx, target.String())
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	compressed, err := os.CreateTemp(s.cfg.Dir, "download-*.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(compressed.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(compressed, hash), io.LimitReader(res.Body, f.Bytes+1))
	if closeErr := compressed.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", datasetFailure("response_read", target.String())
	}
	if n != f.Bytes || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(f.SHA256) {
		return "", datasetFailure("asset_integrity", target.String())
	}
	in, err := os.Open(compressed.Name())
	if err != nil {
		return "", err
	}
	defer in.Close()
	zr, err := gzip.NewReader(bufio.NewReader(in))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	out, err := os.CreateTemp(s.cfg.Dir, "dataset-*.sqlite")
	if err != nil {
		return "", err
	}
	written, err := io.Copy(out, io.LimitReader(zr, f.UncompressedBytes+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && written != f.UncompressedBytes {
		err = datasetFailure("asset_integrity", target.String())
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// checkFile opens a dataset file and checks its integrity, schema and kind.
func checkFile(path, kind string) error {
	db, err := openDataset(path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err = db.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return datasetFailure("asset_integrity", "")
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	var got string
	if err = db.QueryRow(`SELECT value FROM meta WHERE key='kind'`).Scan(&got); err != nil {
		return err
	}
	if version != SchemaVersion || got != kind {
		return fmt.Errorf("title dataset file is schema %d %s; want %d %s", version, got, SchemaVersion, kind)
	}
	return nil
}

func openDataset(path string, readOnly bool) (*sql.DB, error) {
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode="+mode)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, db.Ping()
}

// applyDelta replaces the rows of every work a delta names in the installed
// file, then applies its removals and redirects.
func (s *Service) applyDelta(ctx context.Context, domain string, f ManifestFile) error {
	tmp, err := s.download(ctx, f)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err = checkFile(tmp, "delta"); err != nil {
		return err
	}
	db, err := openDataset(s.path(domain), false)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `ATTACH DATABASE ? AS delta`, "file:"+filepath.ToSlash(tmp)+"?mode=ro"); err != nil {
		return err
	}
	statements := []string{`BEGIN`}
	for _, table := range []string{"work_ids", "work_tags", "work_similar", "work_sources"} {
		statements = append(statements, `DELETE FROM `+table+` WHERE qid IN(SELECT qid FROM delta.works UNION SELECT qid FROM delta.removed)`)
	}
	statements = append(statements,
		`DELETE FROM works WHERE qid IN(SELECT qid FROM delta.works UNION SELECT qid FROM delta.removed)`,
		`INSERT OR REPLACE INTO vocabulary SELECT * FROM delta.vocabulary`,
		`INSERT OR REPLACE INTO fandom_wikis SELECT * FROM delta.fandom_wikis`,
		`INSERT INTO works SELECT * FROM delta.works`,
		`INSERT INTO work_ids SELECT * FROM delta.work_ids`,
		`INSERT INTO work_tags SELECT * FROM delta.work_tags`,
		`INSERT INTO work_similar SELECT * FROM delta.work_similar`,
		`INSERT INTO work_sources SELECT * FROM delta.work_sources`,
		`INSERT OR REPLACE INTO redirects SELECT * FROM delta.redirects`,
		`UPDATE meta SET value=(SELECT value FROM delta.meta WHERE key='dataset_version') WHERE key='dataset_version'`,
		`COMMIT`)
	for _, statement := range statements {
		if _, err = conn.ExecContext(ctx, statement); err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return datasetFailure("apply_failed", s.cfg.ManifestURL)
		}
	}
	_, err = conn.ExecContext(ctx, `DETACH DATABASE delta`)
	return err
}

// noteFailure records a failed check without moving the domain's last check,
// so the backoff (not the weekly cadence) decides the retry.
func (s *Service) noteFailure(ctx context.Context, domain string, problem error) error {
	return s.writeState(ctx, `INSERT INTO enrichment_domains(domain,version,checked_at,error) VALUES(?,'','',?)
	 ON CONFLICT(domain) DO UPDATE SET error=excluded.error`, domain, datasetDiagnostic(problem))
}

func (s *Service) noteChecked(ctx context.Context, domain, version string, problem error) error {
	message := datasetDiagnostic(problem)
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	defer w.Rollback()
	if _, err = w.Tx().ExecContext(ctx, `INSERT INTO enrichment_domains(domain,version,checked_at,error) VALUES(?,?,?,?)
	 ON CONFLICT(domain) DO UPDATE SET version=CASE WHEN excluded.version<>'' THEN excluded.version ELSE version END,checked_at=excluded.checked_at,error=excluded.error`,
		domain, version, s.now().UTC().Format(time.RFC3339), message); err != nil {
		return err
	}
	return w.Commit()
}

// ImportAll matches every library work of a domain against the installed file.
func (s *Service) ImportAll(ctx context.Context, domain string) error {
	kinds, _ := json.Marshal(domainKinds[domain].entities)
	var after int64
	for {
		// Every work with provider ids, and every work matched before (one
		// that lost its ids loses its dataset rows).
		works, err := scanIDs(s.db.QueryContext(ctx, `SELECT entity_id FROM (
		 SELECT x.entity_id FROM catalog_external_ids x CROSS JOIN catalog_entities e ON e.id=x.entity_id WHERE x.entity_id>?1 AND e.kind IN(SELECT value FROM json_each(?2))
		 UNION SELECT entity_id FROM catalog_dataset_works WHERE entity_id>?1 AND domain=?3) ORDER BY entity_id LIMIT 200`, after, string(kinds), domain))
		if err != nil {
			return err
		}
		if len(works) == 0 {
			break
		}
		if err = s.importWorks(ctx, domain, works); err != nil {
			return err
		}
		after = works[len(works)-1]
	}
	return s.importVocabulary(ctx, domain)
}

func (s *Service) importVocabulary(ctx context.Context, domain string) error {
	ds, err := openDataset(s.path(domain), true)
	if err != nil {
		return err
	}
	defer ds.Close()
	rows, err := ds.QueryContext(ctx, `SELECT tag,family,label FROM vocabulary WHERE retired_in IS NULL`)
	if err != nil {
		return err
	}
	type entry struct{ tag, family, label string }
	var all []entry
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.tag, &e.family, &e.label); err != nil {
			rows.Close()
			return err
		}
		all = append(all, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	// A release's vocabulary is a few hundred rows, each also moving the
	// table's bound: more than one transaction may hold.
	batch := s.boundedWrites(ctx)
	defer batch.rollback()
	for _, e := range all {
		tx, err := batch.next(rowCost)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_dataset_vocabulary(tag,family,label) VALUES(?,?,?) ON CONFLICT(tag) DO UPDATE SET family=excluded.family,label=excluded.label`, e.tag, e.family, e.label); err != nil {
			return err
		}
	}
	return batch.commit()
}

// drainJobs matches works queued since the last pass.
func (s *Service) drainJobs(ctx context.Context, limit int) (bool, error) {
	works, err := scanIDs(s.db.QueryContext(ctx, `SELECT entity_id FROM enrichment_jobs ORDER BY entity_id LIMIT ?`, limit))
	if err != nil || len(works) == 0 {
		return false, err
	}
	drained := len(works)
	byDomain := map[string][]int64{}
	for _, work := range works {
		var kind int
		if err = s.db.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, work).Scan(&kind); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		for domain, d := range domainKinds {
			for _, k := range d.entities {
				if k == kind {
					byDomain[domain] = append(byDomain[domain], work)
				}
			}
		}
	}
	for domain, list := range byDomain {
		if _, statErr := os.Stat(s.path(domain)); statErr != nil {
			continue // matched when the domain's file arrives (ImportAll)
		}
		if err = s.importWorks(ctx, domain, list); err != nil {
			return false, err
		}
	}
	// The queue entries go a plan at a time, like every write of this pass.
	chunk := int(dbwork.DefaultPlan(dbwork.ClassBackgroundMedia).Rows)
	for len(works) > 0 {
		part := works[:min(chunk, len(works))]
		works = works[len(part):]
		w, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
		if err != nil {
			return false, err
		}
		raw, _ := json.Marshal(part)
		if _, err = w.Tx().ExecContext(ctx, `DELETE FROM enrichment_jobs WHERE entity_id IN(SELECT value FROM json_each(?))`, string(raw)); err != nil {
			w.Rollback()
			return false, err
		}
		if err = w.Commit(); err != nil {
			return false, err
		}
	}
	return drained == limit, nil
}

type matched struct {
	qid     int64
	tags    [][2]any // tag, strength
	similar []similarLink
}

// similarLink is a similar title by its provider id, and why it is similar.
type similarLink struct {
	id       [3]string // provider, provider kind, provider id
	relation string
}

// similarRelations names the dataset's relation codes (catalog_similar.relation);
// 0, the source not saying, is the empty string.
var similarRelations = map[int]string{1: "tone", 2: "theme", 3: "premise", 4: "style", 5: "creator"}

// importWorks matches works by their provider ids and writes what the
// dataset says about them; a work that no longer matches loses its rows.
func (s *Service) importWorks(ctx context.Context, domain string, works []int64) error {
	ds, err := openDataset(s.path(domain), true)
	if err != nil {
		return err
	}
	defer ds.Close()
	found := map[int64]*matched{}
	for _, work := range works {
		m, err := s.match(ctx, ds, work)
		if err != nil {
			return err
		}
		if m != nil {
			found[work] = m
		}
	}
	batch := s.boundedWrites(ctx)
	defer batch.rollback()
	// A work's rows are written in parts that each fit the plan (its tags a
	// few at a time, then its similar titles): a release gives a work up to
	// eighty tags, more than one transaction may hold. The rows are derived
	// and rewritten whole on every pass, so a pass cut short leaves at worst a
	// work half updated until the next.
	for _, work := range works {
		m := found[work]
		if m == nil {
			if err = removeWork(ctx, batch, work); err != nil {
				return err
			}
			continue
		}
		tx, err := batch.next(rowCost)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_dataset_works(entity_id,domain,qid) VALUES(?,?,?) ON CONFLICT(entity_id) DO UPDATE SET domain=excluded.domain,qid=excluded.qid`, work, domain, m.qid); err != nil {
			return err
		}
		if err = replaceTags(ctx, batch, work, m.tags); err != nil {
			return err
		}
		if err = replaceSimilar(ctx, batch, work, m.similar); err != nil {
			return err
		}
	}
	return batch.commit()
}

// rowCost is what writing or removing one dataset row costs a transaction:
// the row, the trigger write it causes (a catalog_dirty mark or a
// rec_data_revision bump), and room for the index work the plan also counts.
const rowCost = 3

// removeWork removes a work's dataset rows, a plan at a time.
func removeWork(ctx context.Context, batch *boundedWriter, work int64) error {
	for _, q := range []string{
		`DELETE FROM catalog_dataset_tags WHERE entity_id=?1 AND tag IN(SELECT tag FROM catalog_dataset_tags WHERE entity_id=?1 LIMIT ?2)`,
		`DELETE FROM catalog_similar WHERE entity_id=?1 AND source='portico-dataset' AND rank IN(SELECT rank FROM catalog_similar WHERE entity_id=?1 AND source='portico-dataset' LIMIT ?2)`,
	} {
		for {
			chunk := batch.room() / rowCost
			tx, err := batch.next(chunk * rowCost)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, q, work, chunk)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n < int64(chunk) {
				break
			}
		}
	}
	tx, err := batch.next(rowCost)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog_dataset_works WHERE entity_id=?`, work)
	return err
}

// boundedWriter spreads one pass's writes over transactions that each stay
// within the background-media write plan (dbwork.DefaultPlan), so an import or
// a purge of a large library never holds the single writer for long. Every
// transaction it commits marks the recommendation data changed.
type boundedWriter struct {
	ctx    context.Context
	db     *sql.DB
	budget int
	used   int
	w      *dbwork.Write
}

func (s *Service) boundedWrites(ctx context.Context) *boundedWriter {
	// Three quarters of the plan: the costs below are estimates.
	return &boundedWriter{ctx: ctx, db: s.db, budget: int(dbwork.DefaultPlan(dbwork.ClassBackgroundMedia).Rows) * 3 / 4}
}

// room is what the current transaction can still take (all of a fresh one's),
// never less than one row's cost.
func (b *boundedWriter) room() int {
	if b.w == nil || b.budget-b.used < rowCost {
		return b.budget - 1
	}
	return b.budget - b.used
}

// next returns the transaction for a write of about cost rows, committing the
// current one first when the write would take it past the budget. A single
// write larger than the budget gets a transaction of its own.
func (b *boundedWriter) next(cost int) (*sql.Tx, error) {
	if b.w != nil && b.used+cost > b.budget {
		if err := b.commit(); err != nil {
			return nil, err
		}
	}
	if b.w == nil {
		w, err := dbwork.Begin(b.ctx, b.db, dbwork.ClassFrom(b.ctx, dbwork.ClassBackgroundMedia))
		if err != nil {
			return nil, err
		}
		b.w, b.used = w, 1 // the revision mark below
	}
	b.used += cost
	return b.w.Tx(), nil
}

func (b *boundedWriter) commit() error {
	if b.w == nil {
		return nil
	}
	w := b.w
	b.w = nil
	defer w.Rollback()
	if _, err := w.Tx().ExecContext(b.ctx, `UPDATE rec_data_revision SET revision=revision+1 WHERE id=1`); err != nil {
		return err
	}
	return w.Commit()
}

func (b *boundedWriter) rollback() {
	if b.w != nil {
		b.w.Rollback()
		b.w = nil
	}
}

// match finds the dataset work of a library work by its provider ids.
func (s *Service) match(ctx context.Context, ds *sql.DB, work int64) (*matched, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider,provider_kind,provider_id FROM catalog_external_ids WHERE entity_id=?`, work)
	if err != nil {
		return nil, err
	}
	var ids [][3]string
	for rows.Next() {
		var id [3]string
		if err = rows.Scan(&id[0], &id[1], &id[2]); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var qid int64
	for _, code := range similarTargetOrder {
		p := datasetProviders[code]
		for _, id := range ids {
			if id[0] != p[0] || id[1] != p[1] {
				continue
			}
			var value any = id[2]
			if n, e := strconv.ParseInt(id[2], 10, 64); e == nil && p[0] != "imdb" {
				value = n
			}
			err = ds.QueryRowContext(ctx, `SELECT COALESCE((SELECT qid FROM redirects WHERE old_qid=w.qid),w.qid) FROM work_ids w WHERE w.provider=? AND w.provider_id=? ORDER BY w.qid LIMIT 1`, code, value).Scan(&qid)
			if err == nil {
				break
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		if qid != 0 {
			break
		}
	}
	if qid == 0 {
		return nil, nil
	}
	m := &matched{qid: qid}
	tags, err := ds.QueryContext(ctx, `SELECT v.tag,t.strength FROM work_tags t CROSS JOIN vocabulary v ON v.id=t.tag WHERE t.qid=? AND v.retired_in IS NULL ORDER BY v.tag`, qid)
	if err != nil {
		return nil, err
	}
	for tags.Next() {
		var tag string
		var strength int
		if err = tags.Scan(&tag, &strength); err != nil {
			tags.Close()
			return nil, err
		}
		m.tags = append(m.tags, [2]any{tag, strength})
	}
	tags.Close()
	if err = tags.Err(); err != nil {
		return nil, err
	}
	rows, err = ds.QueryContext(ctx, `SELECT similar,relation FROM work_similar WHERE qid=? ORDER BY rank`, qid)
	if err != nil {
		return nil, err
	}
	type link struct {
		target   int64
		relation int
	}
	var links []link
	for rows.Next() {
		var l link
		if err = rows.Scan(&l.target, &l.relation); err != nil {
			rows.Close()
			return nil, err
		}
		links = append(links, l)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, l := range links {
		if id, ok, err := bestTarget(ctx, ds, l.target); err != nil {
			return nil, err
		} else if ok {
			m.similar = append(m.similar, similarLink{id: id, relation: similarRelations[l.relation]})
		}
	}
	return m, nil
}

// bestTarget names a similar work by the provider id servers most often hold.
func bestTarget(ctx context.Context, ds *sql.DB, qid int64) ([3]string, bool, error) {
	rows, err := ds.QueryContext(ctx, `SELECT provider,provider_id FROM work_ids WHERE qid=?`, qid)
	if err != nil {
		return [3]string{}, false, err
	}
	defer rows.Close()
	best, have := len(similarTargetOrder), map[int]string{}
	for rows.Next() {
		var code int
		var value any
		if err = rows.Scan(&code, &value); err != nil {
			return [3]string{}, false, err
		}
		have[code] = fmt.Sprint(value)
	}
	if err = rows.Err(); err != nil {
		return [3]string{}, false, err
	}
	for i, code := range similarTargetOrder {
		if _, ok := have[code]; ok && i < best {
			best = i
		}
	}
	if best == len(similarTargetOrder) {
		return [3]string{}, false, nil
	}
	code := similarTargetOrder[best]
	p := datasetProviders[code]
	return [3]string{p[0], p[1], have[code]}, true, nil
}

func replaceTags(ctx context.Context, batch *boundedWriter, work int64, tags [][2]any) error {
	current := map[string]int{}
	rows, err := batch.db.QueryContext(ctx, `SELECT tag,strength FROM catalog_dataset_tags WHERE entity_id=?`, work)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tag string
		var strength int
		if err = rows.Scan(&tag, &strength); err != nil {
			rows.Close()
			return err
		}
		current[tag] = strength
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	next := map[string]int{}
	for _, t := range tags {
		next[t[0].(string)] = t[1].(int)
	}
	// Unchanged rows aren't rewritten: every write re-derives the work's facets.
	for tag, strength := range current {
		if next[tag] != strength {
			tx, err := batch.next(rowCost)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_dataset_tags WHERE entity_id=? AND tag=?`, work, tag); err != nil {
				return err
			}
		}
	}
	for tag, strength := range next {
		if current[tag] != strength {
			tx, err := batch.next(rowCost)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_dataset_tags(entity_id,tag,strength) VALUES(?,?,?)`, work, tag, strength); err != nil {
				return err
			}
		}
	}
	return nil
}

// replaceSimilar rewrites a work's similar titles (at most fifteen) in one part.
func replaceSimilar(ctx context.Context, batch *boundedWriter, work int64, similar []similarLink) error {
	tx, err := batch.next(rowCost * (2*len(similar) + 1))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_similar WHERE entity_id=? AND source='portico-dataset'`, work); err != nil {
		return err
	}
	for i, t := range similar {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_similar(entity_id,source,rank,provider,provider_kind,provider_id,relation) VALUES(?,'portico-dataset',?,?,?,?,?)`, work, i+1, t.id[0], t.id[1], t.id[2], t.relation); err != nil {
			return err
		}
	}
	return nil
}

func scanIDs(rows *sql.Rows, err error) ([]int64, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
