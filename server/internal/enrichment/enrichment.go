// Package enrichment keeps the Portico title dataset on this server: an open
// (CC BY-SA 4.0) SQLite file per domain of mood, theme, pacing and other tags
// and similar-title lists for popular titles, built offline and published
// with a signed manifest (Title Enrichment Dataset — Plan (Codex).md).
//
// The server downloads the whole file of each domain it has libraries for
// (so the host never learns what any library holds), verifies the manifest's
// Ed25519 signature and each file's SHA-256, and matches library works to
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
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
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
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// The published dataset: its manifest and the Ed25519 key it's signed with.
// Empty until the first release is signed (the dataset brief's gate G4);
// PORTICO_DATASET_MANIFEST and PORTICO_DATASET_PUBLIC_KEY (standard base64)
// override them, for testing a release before it ships.
const (
	PublishedManifest  = ""
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
const SchemaVersion = 1

// Config says where the dataset comes from. An empty ManifestURL or key
// leaves the feature off.
type Config struct {
	ManifestURL string
	PublicKey   ed25519.PublicKey
	Dir         string        // where domain files are kept
	Client      *http.Client  // optional timeout; egress policy always belongs to the service
	Interval    time.Duration // how often the manifest is checked (default a day)
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
		cfg.Interval = 24 * time.Hour
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
func (s *Service) Step(ctx context.Context, limit int) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	domains, err := s.neededDomains(ctx)
	if err != nil {
		return false, err
	}
	for _, domain := range domains {
		due, err := s.due(ctx, domain)
		if err != nil {
			return false, err
		}
		if due {
			if err = s.refresh(ctx, domain); err != nil {
				_ = s.noteChecked(ctx, domain, "", err)
				return false, err
			}
		}
	}
	return s.drainJobs(ctx, limit)
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

func (s *Service) due(ctx context.Context, domain string) (bool, error) {
	var checked string
	err := s.db.QueryRowContext(ctx, `SELECT checked_at FROM enrichment_domains WHERE domain=?`, domain).Scan(&checked)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	at, err := time.Parse(time.RFC3339, checked)
	if err != nil || s.now().Sub(at) >= s.cfg.Interval {
		return true, nil
	}
	_, statErr := os.Stat(s.path(domain))
	return statErr != nil, nil
}

func (s *Service) path(domain string) string {
	return filepath.Join(s.cfg.Dir, "portico-"+domain+".sqlite")
}

// Manifest is the signed list of published files.
type Manifest struct {
	SchemaVersion int                       `json:"schema_version"`
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
	if m.SchemaVersion != SchemaVersion {
		return m, datasetFailure("schema_unsupported", s.cfg.ManifestURL)
	}
	return m, nil
}

// refresh brings one domain's file to the manifest's version: its deltas when
// a chain leads from the installed version, else the full file; then every
// library work of the domain is matched again.
func (s *Service) refresh(ctx context.Context, domain string) error {
	m, err := s.FetchManifest(ctx)
	if err != nil {
		return err
	}
	d, ok := m.Domains[domain]
	if !ok {
		return s.noteChecked(ctx, domain, "", nil)
	}
	var installed string
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
	ref, err := url.Parse(f.URL)
	if err != nil {
		return "", datasetFailure("network_policy", s.cfg.ManifestURL)
	}
	target = target.ResolveReference(ref)
	if f.Bytes < 1 || f.Bytes > MaxFileBytes {
		return "", datasetFailure("asset_too_large", target.String())
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
	written, err := io.Copy(out, io.LimitReader(zr, MaxFileBytes+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && written > MaxFileBytes {
		err = datasetFailure("asset_too_large", target.String())
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// checkFile opens a dataset file and checks its schema and kind.
func checkFile(path, kind string) error {
	db, err := openDataset(path, true)
	if err != nil {
		return err
	}
	defer db.Close()
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
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	defer w.Rollback()
	for _, e := range all {
		if _, err = w.Tx().ExecContext(ctx, `INSERT INTO catalog_dataset_vocabulary(tag,family,label) VALUES(?,?,?) ON CONFLICT(tag) DO UPDATE SET family=excluded.family,label=excluded.label`, e.tag, e.family, e.label); err != nil {
			return err
		}
	}
	return w.Commit()
}

// drainJobs matches works queued since the last pass.
func (s *Service) drainJobs(ctx context.Context, limit int) (bool, error) {
	works, err := scanIDs(s.db.QueryContext(ctx, `SELECT entity_id FROM enrichment_jobs ORDER BY entity_id LIMIT ?`, limit))
	if err != nil || len(works) == 0 {
		return false, err
	}
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
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return false, err
	}
	defer w.Rollback()
	raw, _ := json.Marshal(works)
	if _, err = w.Tx().ExecContext(ctx, `DELETE FROM enrichment_jobs WHERE entity_id IN(SELECT value FROM json_each(?))`, string(raw)); err != nil {
		return false, err
	}
	return len(works) == limit, w.Commit()
}

type matched struct {
	qid     int64
	tags    [][2]any // tag, strength
	similar [][3]string
}

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
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	defer w.Rollback()
	tx := w.Tx()
	for _, work := range works {
		m := found[work]
		if m == nil {
			for _, q := range []string{`DELETE FROM catalog_dataset_works WHERE entity_id=?`, `DELETE FROM catalog_dataset_tags WHERE entity_id=?`, `DELETE FROM catalog_similar WHERE entity_id=? AND source='portico-dataset'`} {
				if _, err = tx.ExecContext(ctx, q, work); err != nil {
					return err
				}
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_dataset_works(entity_id,domain,qid) VALUES(?,?,?) ON CONFLICT(entity_id) DO UPDATE SET domain=excluded.domain,qid=excluded.qid`, work, domain, m.qid); err != nil {
			return err
		}
		if err = replaceTags(ctx, tx, work, m.tags); err != nil {
			return err
		}
		if err = replaceSimilar(ctx, tx, work, m.similar); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE rec_data_revision SET revision=revision+1 WHERE id=1`); err != nil {
		return err
	}
	return w.Commit()
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
	similar, err := ds.QueryContext(ctx, `SELECT similar FROM work_similar WHERE qid=? ORDER BY rank`, qid)
	if err != nil {
		return nil, err
	}
	targets, err := scanIDs(similar, nil)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if id, ok, err := bestTarget(ctx, ds, target); err != nil {
			return nil, err
		} else if ok {
			m.similar = append(m.similar, id)
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

func replaceTags(ctx context.Context, tx *sql.Tx, work int64, tags [][2]any) error {
	current := map[string]int{}
	rows, err := tx.QueryContext(ctx, `SELECT tag,strength FROM catalog_dataset_tags WHERE entity_id=?`, work)
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
			if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_dataset_tags WHERE entity_id=? AND tag=?`, work, tag); err != nil {
				return err
			}
		}
	}
	for tag, strength := range next {
		if current[tag] != strength {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_dataset_tags(entity_id,tag,strength) VALUES(?,?,?)`, work, tag, strength); err != nil {
				return err
			}
		}
	}
	return nil
}

func replaceSimilar(ctx context.Context, tx *sql.Tx, work int64, similar [][3]string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_similar WHERE entity_id=? AND source='portico-dataset'`, work); err != nil {
		return err
	}
	for i, t := range similar {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_similar(entity_id,source,rank,provider,provider_kind,provider_id) VALUES(?,'portico-dataset',?,?,?,?)`, work, i+1, t[0], t[1], t[2]); err != nil {
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
