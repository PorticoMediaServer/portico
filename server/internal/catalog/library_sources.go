package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrSourceBusy = errors.New("source has active playback or scan work")
var ErrSourceChanged = errors.New("source root changed; explicitly accept the replacement root before scanning")

// Source settings are owner-only. Viewer projections never include these paths.
// remoteSourceIDExpr names the saved remote source a library folder belongs to,
// captured once when the folder is attached. Queries then use the indexed
// association instead of matching path prefixes on every read.
const remoteSourceIDExpr = `(SELECT r.id FROM remote_sources r WHERE r.removed=0 AND (r.root=? OR substr(?,1,length(r.root)+1)=r.root||'/') ORDER BY length(r.root) DESC LIMIT 1)`

type LibrarySource struct {
	IdentityConfirmed bool   `json:"identityConfirmed"`
	ID                string `json:"id"`
	LibraryID         string `json:"libraryId"`
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	ResolvedPath      string `json:"resolvedPath"`
	Classification    string `json:"classification"`
	Generation        int64  `json:"generation"`
	Health            string `json:"health"`
	Enabled           bool   `json:"enabled"`
	FollowSymlinks    bool   `json:"followSymlinks"`
	IntervalSeconds   int64  `json:"intervalSeconds"`
	NextScanAt        string `json:"nextScanAt"`
	LastCompleteAt    string `json:"lastCompleteAt"`
	LastProgressAt    string `json:"lastProgressAt"`
	RootIdentity      string `json:"-"`
	Incarnation       string `json:"-"`
}
type SourceSettings struct {
	// ScanAfter true queues the attached folder's first scan in the same
	// transaction; omitted or false attaches it without one (as before).
	ScanAfter            *bool                     `json:"scanAfter,omitempty"`
	ExpectedRemoteSource ExpectedRemoteSourceInput `json:"expectedRemoteSource,omitempty"`
	Name                 string                    `json:"name"`
	Path                 string                    `json:"path"`
	Classification       string                    `json:"classification"`
	FollowSymlinks       bool                      `json:"followSymlinks"`
	IntervalSeconds      int64                     `json:"intervalSeconds"`
	ExpectedRevision     int64                     `json:"expectedRevision"`
	AcceptReplacement    bool                      `json:"acceptReplacement"`
}
type ScanPolicy struct {
	Tier       string            `json:"tier"`
	Revision   int64             `json:"revision"`
	Operations []string          `json:"operations"`
	Trickplay  TrickplaySettings `json:"trickplay"`
}

// TrickplaySettings is owner-configured production geometry. Zero means the
// documented default; the stored values are clamped once here so no producer,
// delivery route or client re-derives a different bound.
type TrickplaySettings struct {
	IntervalSeconds int `json:"intervalSeconds"`
	TileWidth       int `json:"tileWidth"`
	MaxTiles        int `json:"maxTiles"`
}

const (
	DefaultTrickplayIntervalSeconds = 10
	DefaultTrickplayTileWidth       = 320
	DefaultTrickplayMaxTiles        = 1080
	// MaxTrickplayTiles is the most frames one library may ask for: the analysis
	// runner produces at most its own MaxPreviewFrames per set, which defaults to
	// and is capped at this value. A larger stored number would publish scrubbing
	// hours the runner never produces.
	MaxTrickplayTiles = 4096
)

// Normalized returns the effective production geometry. Callers never see a
// zero, a negative, or an unbounded frame budget.
func (t TrickplaySettings) Normalized() TrickplaySettings {
	if t.IntervalSeconds == 0 {
		t.IntervalSeconds = DefaultTrickplayIntervalSeconds
	}
	if t.TileWidth == 0 {
		t.TileWidth = DefaultTrickplayTileWidth
	}
	if t.MaxTiles == 0 {
		t.MaxTiles = DefaultTrickplayMaxTiles
	}
	t.IntervalSeconds = min(600, max(1, t.IntervalSeconds))
	t.TileWidth = min(640, max(64, t.TileWidth/2*2))
	t.MaxTiles = min(MaxTrickplayTiles, max(16, t.MaxTiles))
	return t
}

// A submitted value is accepted only inside the published bounds. Silently
// clamping an owner's choice would publish geometry they never selected.
func validTrickplay(t TrickplaySettings) bool {
	if t.IntervalSeconds != 0 && (t.IntervalSeconds < 1 || t.IntervalSeconds > 600) {
		return false
	}
	if t.TileWidth != 0 && (t.TileWidth < 64 || t.TileWidth > 640 || t.TileWidth%2 != 0) {
		return false
	}
	return t.MaxTiles == 0 || t.MaxTiles >= 16 && t.MaxTiles <= 20000
}

var BasicScanOperations = []string{"probe", "local_metadata", "subtitles"}

// Intro and credits detection is not a scan stage: mediaanalysis runs it as
// its own background work, compared across a show's episodes, under the
// server's and each library's detection mode.
var DeepScanOperations = []string{"checksum", "chapter_images", "trickplay", "waveform", "loudness", "fingerprint", SonicOperation, EpisodeThumbnailOperation}

// Complete prepares the navigation and audio features viewers can use. An owner
// can explicitly request integrity reads or diagnostic fingerprints in Custom.
var CompleteScanOperations = []string{"chapter_images", "trickplay", "waveform", "loudness", EpisodeThumbnailOperation}

func (p ScanPolicy) Allows(operation string) bool {
	if p.Tier == "file_list_only" {
		return false
	}
	for _, v := range p.Operations {
		if v == operation {
			return true
		}
	}
	return false
}
func ValidateScanPolicy(p ScanPolicy) (ScanPolicy, error) {
	if !validTrickplay(p.Trickplay) {
		return p, ErrAdminQuery
	}
	switch p.Tier {
	case "file_list_only":
		// Preserve independent picture choices while all content reads are off.
		// The runtime's Allows gate still refuses every operation for this tier.
		seen := map[string]bool{}
		for _, op := range p.Operations {
			if seen[op] || (op != "probe" && op != "trickplay" && op != "chapter_images") {
				return p, ErrAdminQuery
			}
			seen[op] = true
		}
		if (seen["trickplay"] || seen["chapter_images"]) && !seen["probe"] {
			return p, ErrAdminQuery
		}
		if p.Operations == nil {
			p.Operations = []string{}
		}
	case "basic":
		p.Operations = append([]string{}, BasicScanOperations...)
	case "complete":
		p.Operations = append(append([]string{}, BasicScanOperations...), CompleteScanOperations...)
	case "custom":
		seen := map[string]bool{}
		for _, v := range p.Operations {
			valid := false
			for _, allowed := range append(append([]string{}, BasicScanOperations...), DeepScanOperations...) {
				valid = valid || v == allowed
			}
			if !valid || seen[v] {
				return p, ErrAdminQuery
			}
			seen[v] = true
		}
		// Local extraction/subtitle timing requires a bounded probe. Do not silently
		// broaden an owner's Custom content-read permission to satisfy dependencies.
		if (seen["local_metadata"] || seen["subtitles"] || seen["chapter_images"] || seen["trickplay"] || seen["waveform"] || seen["loudness"] || seen["fingerprint"] || seen[SonicOperation] || seen[EpisodeThumbnailOperation]) && !seen["probe"] {
			return p, ErrAdminQuery
		}
		if p.Operations == nil {
			p.Operations = []string{}
		}
	default:
		return p, ErrAdminQuery
	}
	return p, nil
}

const scanPolicyColumns = `tier,revision,operations_json,trickplay_interval_seconds,trickplay_tile_width,trickplay_max_tiles`

func scanScanPolicy(row interface{ Scan(...any) error }) (ScanPolicy, error) {
	var tier, raw string
	var revision int64
	var trickplay TrickplaySettings
	if err := row.Scan(&tier, &revision, &raw, &trickplay.IntervalSeconds, &trickplay.TileWidth, &trickplay.MaxTiles); err != nil {
		return ScanPolicy{}, err
	}
	return ScanPolicyFromStored(tier, revision, raw, trickplay)
}

// ScanPolicyFromStored builds the effective policy from its stored columns, the
// way every reader does: Complete stands for the operation set it names, and
// Custom drops an operation this build no longer runs as a scan stage (the
// retired segment_detection) so it cannot stay wanted forever. It is exported
// for a reader that batches the columns for several libraries: such a reader has
// to answer exactly what one single read answers.
func ScanPolicyFromStored(tier string, revision int64, operationsJSON string, trickplay TrickplaySettings) (ScanPolicy, error) {
	p := ScanPolicy{Tier: tier, Revision: revision, Trickplay: trickplay, Operations: []string{}}
	if err := json.Unmarshal([]byte(operationsJSON), &p.Operations); err != nil {
		return p, err
	}
	if p.Tier == "complete" {
		return ValidateScanPolicy(p)
	}
	if p.Tier == "custom" {
		known := append(append([]string{}, BasicScanOperations...), DeepScanOperations...)
		kept := p.Operations[:0]
		for _, op := range p.Operations {
			for _, k := range known {
				if op == k {
					kept = append(kept, op)
					break
				}
			}
		}
		p.Operations = kept
	}
	return p, nil
}
func (s *Service) ScanPolicy(ctx context.Context, library string) (ScanPolicy, error) {
	p, e := scanScanPolicy(s.read().QueryRowContext(ctx, `SELECT `+scanPolicyColumns+` FROM library_scan_policies WHERE library_id=?`, library))
	if e != nil {
		return p, e
	}
	return musicLoudnessPolicy(ctx, s.read(), library, p)
}
func ScanPolicyTx(ctx context.Context, tx *sql.Tx, library string) (ScanPolicy, error) {
	p, e := scanScanPolicy(tx.QueryRowContext(ctx, `SELECT `+scanPolicyColumns+` FROM library_scan_policies WHERE library_id=?`, library))
	if e != nil {
		return p, e
	}
	return musicLoudnessPolicy(ctx, tx, library, p)
}

const sourceColumns = `id,library_id,kind,name,configured_root,root,classification,generation,health,enabled,follow_symlinks,interval_seconds,next_scan_at,last_complete_at,COALESCE((SELECT p.last_progress_at FROM library_source_progress AS p WHERE p.source_id=library_sources.id),'') AS last_progress_at,root_identity,incarnation,identity_confirmed`

func scanLibrarySource(row interface{ Scan(...any) error }) (LibrarySource, error) {
	var v LibrarySource
	err := row.Scan(&v.ID, &v.LibraryID, &v.Kind, &v.Name, &v.Path, &v.ResolvedPath, &v.Classification, &v.Generation, &v.Health, &v.Enabled, &v.FollowSymlinks, &v.IntervalSeconds, &v.NextScanAt, &v.LastCompleteAt, &v.LastProgressAt, &v.RootIdentity, &v.Incarnation, &v.IdentityConfirmed)
	return v, err
}
func (s *Service) LibrarySources(ctx context.Context, library string) ([]LibrarySource, error) {
	rows, err := s.read().QueryContext(ctx, `SELECT `+sourceColumns+` FROM library_sources WHERE library_id=? ORDER BY id LIMIT 65`, library)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibrarySource{}
	for rows.Next() {
		v, e := scanLibrarySource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LibrarySourcesTx is the same bounded source projection in a caller's read snapshot.
func LibrarySourcesTx(ctx context.Context, tx *sql.Tx, library string) ([]LibrarySource, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+sourceColumns+` FROM library_sources WHERE library_id=? ORDER BY id LIMIT 65`, library)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibrarySource{}
	for rows.Next() {
		v, e := scanLibrarySource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) LibrarySource(ctx context.Context, id string) (LibrarySource, error) {
	return scanLibrarySource(s.read().QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM library_sources WHERE id=?`, id))
}
func (s *Service) inspectSource(ctx context.Context, path string) (storage.Snapshot, error) {
	if !filepath.IsAbs(path) || !utf8.ValidString(path) || len(path) > 8192 || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return storage.Snapshot{}, ErrAdminQuery
	}
	if s.storage != nil {
		return s.storage.InspectRoot(ctx, path)
	}
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return storage.Snapshot{}, err
	}
	return storage.LocalInventoryDirectory(storage.InventoryRequest{Root: root, RelativePath: "."})
}
func configurationFence(ctx context.Context, tx *sql.Tx, library string, expected int64, authorize func(*sql.Tx) error) error {
	if authorize == nil {
		return ErrAdminQuery
	}
	if err := authorize(tx); err != nil {
		return err
	}
	var managed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_private_libraries WHERE library_id=?)`, library).Scan(&managed); err != nil {
		return err
	}
	if managed {
		return ErrAdminQuery
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM library_configuration WHERE library_id=?`, library).Scan(&revision); err != nil {
		return err
	}
	if revision != expected {
		return ErrLibraryConfigurationConflict
	}
	return nil
}
func (s *Service) SaveSource(ctx context.Context, library, id string, input SourceSettings, authorize func(*sql.Tx) error) (LibrarySource, error) {
	return s.SaveSourceThen(ctx, library, id, input, authorize, nil)
}

// SaveSourceThen commits any work that accompanies the source configuration,
// such as the first scan of a newly attached folder, in the same transaction.
func (s *Service) SaveSourceThen(ctx context.Context, library, id string, input SourceSettings, authorize func(*sql.Tx) error, after func(*sql.Tx, string) error) (LibrarySource, error) {
	var result LibrarySource
	expectedRemoteSource, err := input.ExpectedRemoteSource.Selection()
	if err != nil {
		return result, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = filepath.Base(filepath.Clean(input.Path))
	}
	if input.Name == "" || len(input.Name) > 100 || !utf8.ValidString(input.Name) || strings.IndexFunc(input.Name, unicode.IsControl) >= 0 || input.Classification != "local" && input.Classification != "network" || input.IntervalSeconds != 0 && (input.IntervalSeconds < 900 || input.IntervalSeconds > 365*86400) {
		return result, ErrAdminQuery
	}
	if s.storage.IsRemote(input.Path) {
		input.Classification = "network"
		if input.FollowSymlinks {
			return result, ErrAdminQuery
		}
	}
	if s.storage != nil && s.storage.SourceGuard != nil && !s.storage.IsRemote(input.Path) {
		var previous string
		if id != "" {
			e := s.db.QueryRowContext(ctx, `SELECT configured_root FROM library_sources WHERE id=? AND library_id=?`, id, library).Scan(&previous)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return result, e
			}
		}
		if previous != input.Path {
			if e := s.storage.SourceGuard(input.Path); e != nil {
				return result, e
			}
		}
	}
	observed, err := s.inspectSource(ctx, input.Path)
	if err != nil {
		return result, errors.New("source unavailable or storage operation timed out")
	}
	rootID := storage.RootIdentity(observed)
	if rootID == "" {
		return result, ErrSourceChanged
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return result, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = configurationFence(ctx, tx, library, input.ExpectedRevision, authorize); err != nil {
		return result, err
	}
	if err = checkExpectedRemoteSource(ctx, tx, input.Path, expectedRemoteSource); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,root FROM library_sources WHERE library_id=? AND enabled=1`, library)
	if err != nil {
		return result, err
	}
	count := 0
	for rows.Next() {
		var existing, path string
		if err = rows.Scan(&existing, &path); err != nil {
			rows.Close()
			return result, err
		}
		count++
		if existing != id {
			a, _ := filepath.Rel(path, observed.Path)
			b, _ := filepath.Rel(observed.Path, path)
			if filepath.IsLocal(a) || filepath.IsLocal(b) {
				rows.Close()
				return result, ErrSourceAlreadyConfigured
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	next := ""
	if input.IntervalSeconds > 0 {
		next = now.Add(time.Duration(input.IntervalSeconds) * time.Second).Format(time.RFC3339Nano)
	}
	if id == "" {
		// A folder that was removed from this library and is added again is the
		// same folder: it comes back with its files (still marked missing until
		// the next scan finds them) rather than being refused as a duplicate.
		var removed string
		err = tx.QueryRowContext(ctx, `SELECT id FROM library_sources WHERE library_id=? AND root=? AND enabled=0`, library, observed.Path).Scan(&removed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		id = removed
	}
	if id == "" {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM library_sources WHERE library_id=?`, library).Scan(&count); err != nil {
			return result, err
		}
		if count >= 64 {
			return result, ErrAdminQuery
		}
		id = identity.Token()
		_, err = tx.ExecContext(ctx, `INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,remote_source_id) VALUES(?,?,?,?,?,?,?,?,?,?,1,`+remoteSourceIDExpr+`)`, id, library, input.Name, input.Path, observed.Path, input.Classification, rootID, input.FollowSymlinks, input.IntervalSeconds, next, input.Path, input.Path)
	} else {
		old, e := scanLibrarySource(tx.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM library_sources WHERE id=? AND library_id=?`, id, library))
		if e != nil {
			return result, e
		}
		if old.Kind != "local" && old.Kind != "remote" {
			return result, ErrAdminQuery
		}
		var busy int
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_source_active WHERE source_id=?)`, id).Scan(&busy); e != nil {
			return result, e
		}
		if busy != 0 {
			return result, ErrSourceBusy
		}
		replacement := old.RootIdentity != "" && !storage.SameRoot(old.RootIdentity, rootID)
		if replacement && !input.AcceptReplacement {
			return result, ErrSourceChanged
		}
		if old.ResolvedPath != observed.Path || replacement || old.FollowSymlinks != input.FollowSymlinks {
			if e = sourcePlaybackBusy(ctx, tx, id); e != nil {
				return result, e
			}
		}
		incarnation := old.Incarnation
		if replacement {
			incarnation = identity.Token()

		}
		_, err = tx.ExecContext(ctx, `UPDATE library_sources SET name=?,configured_root=?,root=?,classification=?,generation=generation+1,last_complete_job='',last_complete_at='',incarnation=?,root_identity=?,identity_confirmed=1,health='ready',enabled=1,follow_symlinks=?,interval_seconds=?,next_scan_at=?,remote_source_id=`+remoteSourceIDExpr+` WHERE id=?`, input.Name, input.Path, observed.Path, input.Classification, incarnation, rootID, input.FollowSymlinks, input.IntervalSeconds, next, input.Path, input.Path, id)

		// Keep the original primary root as a compatibility projection. Preparation
		// selects the exact source below; it never widens to a common ancestor.
		if err == nil && id == library {
			_, err = tx.ExecContext(ctx, `UPDATE libraries SET root=? WHERE id=?`, observed.Path, library)
		}
	}
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_sources SET kind=? WHERE id=?`, s.sourceKind(observed.Path), id); err != nil {
		return result, err
	}
	if after != nil {
		if err = after(tx, id); err != nil {
			return result, err
		}
	}
	if err = gated.Commit(); err != nil {
		return result, err
	}
	return s.LibrarySource(ctx, id)
}
func sourcePlaybackBusy(ctx context.Context, tx *sql.Tx, source string) error {
	var busy int
	// Legacy established sessions plus contract-32 preparation/live occurrences.
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions p JOIN catalog_entities i ON i.id=p.item_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE (EXISTS(SELECT 1 FROM inventory_objects o WHERE o.asset_id=p.asset_id AND o.source_id=?) OR cl.library_id=? AND NOT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id WHERE o.asset_id=p.asset_id AND s.library_id=cl.library_id)) AND p.state NOT IN('ended','stopped','failed') AND p.expires_at>?)`, source, source, time.Now().UTC().Format(time.RFC3339)).Scan(&busy)
	if err != nil {
		return err
	}
	if busy != 0 {
		return ErrSourceBusy
	}
	return nil
}
func (s *Service) SetSourceHealth(ctx context.Context, id, health string) error {
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE library_sources SET health=? WHERE id=? AND health!=?`, health, id, health); err != nil {
		return err
	}

	return gated2.Commit()
}

// RemoveSource takes a folder out of a library. Scanning of it stops at once;
// enqueue (when given) queues the background work that marks its files
// missing, after which their titles follow the owner's rule for missing titles.
func (s *Service) RemoveSource(ctx context.Context, library, id string, expected int64, authorize func(*sql.Tx) error, enqueue func(context.Context, *sql.Tx, string, string) error) error {
	gated3, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if err = configurationFence(ctx, tx, library, expected, authorize); err != nil {
		return err
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM library_sources WHERE id=? AND library_id=?`, id, library).Scan(&exists); err != nil {
		return err
	}
	if err = sourcePlaybackBusy(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET status='cancelled' WHERE id IN(SELECT job_id FROM inventory_source_active WHERE source_id=?)`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_source_active WHERE source_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_sources SET enabled=0,health='removing',generation=generation+1,next_scan_at='' WHERE id=?`, id); err != nil {
		return err
	}
	if err = clearRemovalProgressTx(ctx, tx, "folder", id); err != nil {
		return err
	}
	if enqueue != nil {
		if err = enqueue(ctx, tx, folderRemovalKind, id); err != nil {
			return err
		}
	}
	return gated3.Commit()
}
func (s *Service) UpdateScanPolicy(ctx context.Context, library string, p ScanPolicy, expected int64, authorize func(*sql.Tx) error) (ScanPolicy, error) {
	p, err := ValidateScanPolicy(p)
	if err != nil {
		return p, err
	}
	raw, _ := json.Marshal(p.Operations)
	gated4, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return p, err
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if authorize == nil {
		return p, ErrAdminQuery
	}
	if err = authorize(tx); err != nil {
		return p, err
	}
	r, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil {
		return p, err
	}
	if r != nil {
		return ScanPolicyTx(ctx, tx, library)
	}
	if err = configurationFence(ctx, tx, library, expected, authorize); err != nil {
		return p, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_scan_policies SET tier=?,operations_json=?,trickplay_interval_seconds=?,trickplay_tile_width=?,trickplay_max_tiles=?,revision=revision+1 WHERE library_id=?`, p.Tier, string(raw), p.Trickplay.IntervalSeconds, p.Trickplay.TileWidth, p.Trickplay.MaxTiles, library); err != nil {
		return p, err
	}
	// Existing inventory remains valid; its pending analysis is read-fenced by
	// the new policy revision. The scanner schedules missing current stages.
	current, err := ScanPolicyTx(ctx, tx, library)
	if err != nil {
		return p, err
	}
	if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: library, Revision: current.Revision}, time.Now().UnixMilli()); err != nil {
		return p, err
	}
	if err = gated4.Commit(); err != nil {
		return p, err
	}
	return s.ScanPolicy(ctx, library)
}

// AdoptRootIdentity records the identity a source's root has on this boot when
// it is the same folder under a renumbered device (storage.SameRoot). Nothing
// about the source changes: it keeps its incarnation, its files and their
// analysis. The running scan is moved to the new identity with it.
func (s *Service) AdoptRootIdentity(ctx context.Context, job string, source LibrarySource, observed string) error {
	if !storage.SameRoot(source.RootIdentity, observed) {
		return ErrSourceChanged
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia), func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE library_sources SET root_identity=? WHERE id=? AND generation=? AND root_identity=?`, observed, source.ID, source.Generation, source.RootIdentity)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrSourceChanged
		}
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET root_identity=? WHERE job_id=? AND root_identity=?`, observed, job, source.RootIdentity)
		return err
	})
}

// CreateFoldersAuthorizedThen validates every source before creating anything,
// then commits all folders, metadata configuration and queued scans together.
// It never deletes, renames or modifies the selected media directories.
func (s *Service) CreateFoldersAuthorizedThen(ctx context.Context, name, kind string, folders []string, authorize func(*sql.Tx) error, created func(*sql.Tx, Library) error) (Library, error) {
	if len(folders) < 1 || len(folders) > 64 {
		return Library{}, ErrAdminQuery
	}
	return s.createFolders(ctx, name, kind, folders, authorize, created)
}
func (s *Service) createFolders(ctx context.Context, name, kind string, folders []string, authorize func(*sql.Tx) error, created func(*sql.Tx, Library) error) (Library, error) {
	type source struct{ configured, path, identity, classification, kind string }
	sources := make([]source, 0, len(folders))
	for _, folder := range folders {
		if !filepath.IsAbs(folder) || len(folder) > 8192 || strings.IndexFunc(folder, unicode.IsControl) >= 0 {
			return Library{}, ErrAdminQuery
		}
		if s.storage != nil && s.storage.SourceGuard != nil && !s.storage.IsRemote(folder) {
			if err := s.storage.SourceGuard(folder); err != nil {
				return Library{}, err
			}
		}
		observed, err := s.inspectSource(ctx, folder)
		if err != nil || storage.RootIdentity(observed) == "" {
			return Library{}, ErrAdminQuery
		}
		for _, prior := range sources {
			a, _ := filepath.Rel(prior.path, observed.Path)
			b, _ := filepath.Rel(observed.Path, prior.path)
			if filepath.IsLocal(a) || filepath.IsLocal(b) {
				return Library{}, ErrSourceAlreadyConfigured
			}
		}
		sources = append(sources, source{folder, observed.Path, storage.RootIdentity(observed), s.sourceClassification(observed.Path), s.sourceKind(observed.Path)})
	}
	return s.CreateAuthorizedThen(ctx, name, kind, folders[0], authorize, func(tx *sql.Tx, library Library) error {
		for _, source := range sources[1:] {
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,identity_confirmed,kind,remote_source_id) VALUES(?,?,?,?,?,?,?,1,?,`+remoteSourceIDExpr+`)`, identity.Token(), library.ID, filepath.Base(source.path), source.configured, source.path, source.classification, source.identity, source.kind, source.configured, source.configured); err != nil {
				return err
			}
		}
		if created != nil {
			return created(tx, library)
		}
		return nil
	})
}

// AppliesLoudness reports whether this library measures source loudness whether
// or not its stored stage list names it: every music library does, outside a
// file-list-only scan, so gain does not depend on the selected tier.
func AppliesLoudness(kind string, p ScanPolicy) bool {
	return kind == "music" && p.Tier != "file_list_only"
}

// A file-list-only scan never opens media; every other music scan measures
// source loudness so gain does not depend on the selected enrichment tier.
func musicLoudnessPolicy(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, library string, p ScanPolicy) (ScanPolicy, error) {
	var kind string
	if e := q.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, library).Scan(&kind); e != nil {
		return p, e
	}
	if AppliesLoudness(kind, p) && !p.Allows("loudness") {
		p.Operations = append(append([]string{}, p.Operations...), "loudness")
	}
	if kind == "music" && p.Tier != "file_list_only" {
		enabled := SonicDefaultEnabled()
		err := q.QueryRowContext(ctx, `SELECT enabled FROM library_sonic_settings WHERE library_id=?`, library).Scan(&enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return p, err
		}
		if enabled && !p.Allows(SonicOperation) {
			p.Operations = append(append([]string{}, p.Operations...), SonicOperation)
		}
		if !enabled && p.Allows(SonicOperation) {
			kept := make([]string, 0, len(p.Operations))
			for _, op := range p.Operations {
				if op != SonicOperation {
					kept = append(kept, op)
				}
			}
			p.Operations = kept
		}
	}
	return p, nil
}
