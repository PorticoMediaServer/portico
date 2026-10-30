package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// inventory_runs.reported is a durable report mode: 0 ordinary scan, 1
// ordinary path report, 2 strict path report. Keep scan_mode incremental, so
// existing inventory, reconciliation and analysis phases remain in use.
// No schema change: reported is an unconstrained INTEGER in the native schema.
const strictReportMode = 2

type strictQueueKey struct{}

// StrictRefusalReason is machine-readable. A refusal commits no changes from
// this request, including when another path in the same request was safe.
type StrictRefusalReason string

const (
	StrictInvalidPath         StrictRefusalReason = "invalid_path"
	StrictOutsideLibraries    StrictRefusalReason = "outside_libraries"
	StrictRootRefused         StrictRefusalReason = "source_root_refused"
	StrictAmbiguousSource     StrictRefusalReason = "ambiguous_source"
	StrictUnverifiedSource    StrictRefusalReason = "source_unverified"
	StrictUnsupportedSource   StrictRefusalReason = "unsupported_source"
	StrictSymlinksUnsupported StrictRefusalReason = "symlink_following_unsupported"
	StrictUnknownPath         StrictRefusalReason = "unknown_path"
	StrictProofUnavailable    StrictRefusalReason = "path_proof_unavailable"
	StrictSourceChanged       StrictRefusalReason = "source_changed"
	StrictSourceBusy          StrictRefusalReason = "source_busy"
	StrictPendingReports      StrictRefusalReason = "ordinary_reports_pending"
	StrictOverloaded          StrictRefusalReason = "path_report_overloaded"
)

type StrictPathRefusal struct {
	Reason StrictRefusalReason
	Path   string
}

func (e *StrictPathRefusal) Error() string { return "strict path report refused: " + string(e.Reason) }

// Retryable distinguishes admission/state boundaries from invalid path scope.
// No strict request is reported as accepted while it is waiting at a boundary.
func (e *StrictPathRefusal) Retryable() bool {
	switch e.Reason {
	case StrictSourceBusy, StrictPendingReports, StrictOverloaded, StrictProofUnavailable, StrictSourceChanged:
		return true
	default:
		return false
	}
}

func strictRefusal(reason StrictRefusalReason, path string) error {
	return &StrictPathRefusal{Reason: reason, Path: boundedPath(path)}
}

type strictSource struct {
	reportSource
	identity, kind    string
	generation        int64
	follow, confirmed bool
}

type strictTarget struct {
	path, folder string
	source       strictSource
}

// ReportPathsStrict admits only the named existing directory, a regular file's
// immediate directory, or the immediate readable parent of a known removed
// path. It never searches for a known ancestor. Root targets are refused.
//
// The batch is atomic. Busy sources retain bounded durable intent, separately
// from the ordinary widening notice queue. At the approved per-source cap an
// explicit source-wide incremental intent replaces the pending folders. An
// unstarted strict job may coalesce further reports. Authorization is checked
// before inspection (library="") and for every matched library in the
// transaction committing jobs. authorize must be reentrant and must not have
// external side effects. The native HTTP route remains owner-only.
func (s *Service) ReportPathsStrict(ctx context.Context, paths []string, authorize func(*sql.Tx, string) error) (PathReport, error) {
	empty := PathReport{Paths: []ReportedPath{}, Jobs: []Job{}}
	if len(paths) == 0 || len(paths) > MaxReportedPaths {
		return empty, ErrPathReport
	}
	sources, err := s.strictSources(ctx, authorize)
	if err != nil {
		return empty, err
	}
	// One shared inspection budget, independent of the number of paths. The
	// production storage client supervises blocking filesystem operations.
	proof, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	targets := make([]strictTarget, 0, len(paths))
	for _, raw := range paths {
		path, ok := cleanReportedPath(raw)
		if !ok {
			return empty, strictRefusal(StrictInvalidPath, raw)
		}
		var matched *strictSource
		var relative string
		for i := range sources {
			source := &sources[i]
			rel, inside := relativeTo(source.resolved, path)
			if !inside {
				rel, inside = relativeTo(source.configured, path)
			}
			if !inside {
				continue
			}
			// A root of any source is refused even if nested in another.
			if rel == "." {
				return empty, strictRefusal(StrictRootRefused, path)
			}
			if matched != nil {
				return empty, strictRefusal(StrictAmbiguousSource, path)
			}
			matched, relative = source, rel
		}
		if matched == nil {
			return empty, strictRefusal(StrictOutsideLibraries, path)
		}
		if len(strings.Split(relative, string(filepath.Separator))) > maxPathDepth {
			return empty, strictRefusal(StrictInvalidPath, path)
		}
		if matched.kind != "local" || s.storage.IsRemote(matched.resolved) {
			return empty, strictRefusal(StrictUnsupportedSource, path)
		}
		// Following a directory link can turn a named descendant into a walk
		// of the source root (or an unrelated sibling). Refuse such sources
		// until the native scanner has a per-run no-follow override.
		if matched.follow {
			return empty, strictRefusal(StrictSymlinksUnsupported, path)
		}
		if matched.identity == "" || !matched.confirmed {
			return empty, strictRefusal(StrictUnverifiedSource, path)
		}
		folder, e := s.strictFolder(proof, *matched, relative, path)
		if e != nil {
			if ctx.Err() != nil {
				return empty, ctx.Err()
			}
			return empty, e
		}
		targets = append(targets, strictTarget{path: path, folder: folder, source: *matched})
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return empty, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	bySource := map[string][]string{}
	for _, target := range targets {
		source := target.source
		if authorize != nil {
			if err = authorize(tx, source.library); err != nil {
				return empty, err
			}
		}
		var current bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_sources s WHERE id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND configured_root=? AND kind=? AND follow_symlinks=? AND identity_confirmed=1 AND enabled=1 AND health NOT IN('root_changed','removing') AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id))`, source.id, source.generation, source.incarnation, source.identity, source.resolved, source.configured, source.kind, source.follow).Scan(&current)
		if err != nil {
			return empty, err
		}
		if !current {
			return empty, strictRefusal(StrictSourceChanged, target.path)
		}
		bySource[source.id] = append(bySource[source.id], target.folder)
	}
	ids := make([]string, 0, len(bySource))
	for id := range bySource {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := PathReport{Paths: []ReportedPath{}, Jobs: []Job{}}
	deferred := map[string]bool{}
	whole := map[string]bool{}
	for _, id := range ids {
		var source strictSource
		for _, target := range targets {
			if target.source.id == id {
				source = target.source
				break
			}
		}
		var sourceWide bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_strict_intents WHERE source_id=? AND mode='incremental')`, id).Scan(&sourceWide); err != nil {
			return empty, err
		}
		var job Job
		var e error
		if !sourceWide {
			job, e = queueStrictTargetsTx(ctx, tx, id, bySource[id])
		}
		var refusal *StrictPathRefusal
		if sourceWide || (errors.As(e, &refusal) && refusal.Retryable()) {
			// Path/source proofs and caller authorization above still hold in this
			// transaction. Failure to obtain a source slot is not failure to accept work.
			widened, saveErr := maintainIntentsTx(ctx, tx, source, bySource[id], sourceWide)
			if saveErr != nil {
				return empty, saveErr
			}
			deferred[id] = true
			whole[id] = widened
			out.Deferred = true
		} else if e != nil {
			return empty, e
		} else {
			// A retry can now own folders that were deferred earlier. Retire
			// only intent actually covered by this real strict job, atomically.
			if _, e = tx.ExecContext(ctx, `DELETE FROM inventory_strict_intents AS i WHERE i.source_id=? AND i.mode='strict' AND i.library_id=? AND i.source_generation=? AND i.root_incarnation=? AND i.root_identity=? AND i.root=? AND i.configured_root=? AND i.source_kind=? AND i.follow_symlinks=? AND EXISTS(SELECT 1 FROM inventory_run_targets t WHERE t.job_id=? AND (i.relative_path=t.relative_path OR substr(i.relative_path,1,length(t.relative_path)+1)=t.relative_path||?))`, source.id, source.library, source.generation, source.incarnation, source.identity, source.resolved, source.configured, source.kind, source.follow, job.ID, string(filepath.Separator)); e != nil {
				return empty, e
			}
			out.Jobs = append(out.Jobs, job)
		}
	}
	for _, target := range targets {
		status, scope := "queued", ""
		if deferred[target.source.id] {
			status = "deferred"
		}
		if whole[target.source.id] {
			scope = "source"
		}
		out.Paths = append(out.Paths, ReportedPath{Path: target.path, Status: status, ScanScope: scope, LibraryID: target.source.library, SourceID: target.source.id, Folder: target.folder})
	}
	if err = gated.Commit(); err != nil {
		return empty, err
	}
	return out, nil
}

func (s *Service) strictSources(ctx context.Context, authorize func(*sql.Tx, string) error) ([]strictSource, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return nil, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx, ""); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,library_id,configured_root,root,incarnation,root_identity,kind,generation,follow_symlinks,identity_confirmed FROM library_sources s WHERE enabled=1 AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id) ORDER BY id LIMIT 4097`)
	if err != nil {
		return nil, err
	}
	out := []strictSource{}
	for rows.Next() {
		var source strictSource
		if err = rows.Scan(&source.id, &source.library, &source.configured, &source.resolved, &source.incarnation, &source.identity, &source.kind, &source.generation, &source.follow, &source.confirmed); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out) > 4096 {
		return nil, strictRefusal(StrictOverloaded, "")
	}
	if err = gated.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) strictFolder(ctx context.Context, source strictSource, relative, path string) (string, error) {
	req := storage.InventoryRequest{Root: source.resolved, RootIdentity: source.identity, RelativePath: relative, FollowSymlinks: source.follow}
	directory := func(r storage.InventoryRequest) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.storage != nil {
			_, err := s.storage.InventoryDirectory(ctx, source.id, r)
			return err
		}
		_, err := storage.LocalInventoryDirectory(r) // deterministic isolated fixtures
		return err
	}
	if err := directory(req); err == nil {
		return relative, nil
	}
	// A top-level non-directory or deletion would require scanning the root.
	// Refuse before even asking for absence evidence from that parent.
	if filepath.Dir(relative) == "." {
		return "", strictRefusal(StrictRootRefused, path)
	}
	var err error
	if s.storage != nil {
		_, err = s.storage.InventoryStat(ctx, source.id, req)
	} else {
		_, err = storage.LocalInventoryStat(req)
	}
	if err != nil {
		// An unknown nonexistent descendant supplies no evidence. Do not
		// climb from it or trust the caller's change label as proof.
		var known bool
		err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE source_id=? AND root_incarnation=? AND relative_path=? AND retired=0) OR EXISTS(SELECT 1 FROM inventory_directory_cache WHERE source_id=? AND incarnation=? AND relative_path=?)`, source.id, source.incarnation, relative, source.id, source.incarnation, relative).Scan(&known)
		if err != nil {
			if ctx.Err() != nil {
				return "", strictRefusal(StrictProofUnavailable, path)
			}
			return "", err
		}
		var absent bool
		if s.storage != nil {
			absent, err = s.storage.InventoryAbsence(ctx, source.id, req)
		} else {
			absent, err = storage.LocalInventoryAbsence(req)
		}
		if err != nil || !absent {
			return "", strictRefusal(StrictProofUnavailable, path)
		}
		if !known {
			return "", strictRefusal(StrictUnknownPath, path)
		}
	}
	parent := filepath.Dir(relative)
	if parent == "." {
		return "", strictRefusal(StrictRootRefused, path)
	}
	req.RelativePath = parent
	if err = directory(req); err != nil {
		return "", strictRefusal(StrictProofUnavailable, path)
	}
	return parent, nil
}

// queueStrictTargetsTx uses the same durable notices, job/source claim, worker
// and writer admission as ordinary reports. Notices exist only in this atomic
// transaction: none can later be consumed as an automatically widened report.
func queueStrictTargetsTx(ctx context.Context, tx *sql.Tx, source string, folders []string) (Job, error) {
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_path_notices WHERE source_id=?`, source).Scan(&pending); err != nil {
		return Job{}, err
	}
	if pending >= maxPendingNotices {
		return Job{}, strictRefusal(StrictOverloaded, "")
	}
	if pending != 0 {
		return Job{}, strictRefusal(StrictPendingReports, "")
	}
	var job Job
	var mode int
	var generation int64
	var incarnation, identity string
	err := tx.QueryRowContext(ctx, `SELECT j.id,j.status,r.phase,r.scan_mode,r.reported,r.source_generation,r.root_incarnation,r.root_identity FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id JOIN inventory_runs r ON r.job_id=j.id WHERE a.source_id=? AND j.status IN('queued','running','paused')`, source).Scan(&job.ID, &job.Status, &job.Phase, &job.ScanMode, &mode, &generation, &incarnation, &identity)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, err
	}
	if err == nil {
		var untouched bool
		if err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM inventory_directories WHERE job_id=? AND (state<>'pending' OR pages<>0 OR cursor<>'')) AND EXISTS(SELECT 1 FROM inventory_run_targets WHERE job_id=?) AND EXISTS(SELECT 1 FROM library_sources WHERE id=? AND generation=? AND incarnation=? AND root_identity=?)`, job.ID, job.ID, source, generation, incarnation, identity).Scan(&untouched); err != nil {
			return Job{}, err
		}
		if job.Status != "queued" || job.Phase != "inventory" || job.ScanMode != "incremental" || mode != strictReportMode || !untouched {
			return Job{}, strictRefusal(StrictSourceBusy, "")
		}
		rows, e := tx.QueryContext(ctx, `SELECT relative_path FROM inventory_run_targets WHERE job_id=? LIMIT ?`, job.ID, maxPendingNotices+1)
		if e != nil {
			return Job{}, e
		}
		for rows.Next() {
			var folder string
			if e = rows.Scan(&folder); e != nil {
				rows.Close()
				return Job{}, e
			}
			folders = append(folders, folder)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return Job{}, e
		}
	} else {
		job = Job{}
	}
	// Bound before ancestor coalescing: no overload-to-root transformation.
	unique := map[string]bool{}
	for _, folder := range folders {
		if folder == "." || !filepath.IsLocal(folder) {
			return Job{}, strictRefusal(StrictRootRefused, "")
		}
		unique[folder] = true
	}
	if len(unique) > maxPendingNotices {
		return Job{}, strictRefusal(StrictOverloaded, "")
	}
	targets := []string{}
	for folder := range unique {
		covered := false
		for parent := range unique {
			if parent != folder && within(parent, folder) {
				covered = true
				break
			}
		}
		if !covered {
			targets = append(targets, folder)
		}
	}
	sort.Strings(targets)
	for _, target := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_path_notices(source_id,relative_path,created_ms) VALUES(?,?,?)`, source, target, time.Now().UnixMilli()); err != nil {
			return Job{}, err
		}
	}
	if job.ID == "" {
		job, err = queueSourceTx(context.WithValue(ctx, strictQueueKey{}, true), tx, source, false)
		if err != nil {
			return Job{}, err
		}
	}
	job.SourceID = source
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET reported=? WHERE job_id=?`, strictReportMode, job.ID); err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_directories WHERE job_id=?`, job.ID); err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_run_targets WHERE job_id=?`, job.ID); err != nil {
		return Job{}, err
	}
	for _, target := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_run_targets(job_id,relative_path) VALUES(?,?)`, job.ID, target); err != nil {
			return Job{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,?)`, job.ID, target); err != nil {
			return Job{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_path_notices WHERE source_id=?`, source); err != nil {
		return Job{}, err
	}
	return job, nil
}

// Strict jobs retain the existing protected issues/warnings for unstable paths,
// but must never produce an ordinary notice that later escalates or collapses.
// The caller must explicitly report again when the path is stable.
func recordRunNoticeTx(ctx context.Context, tx *sql.Tx, job, source, folder string, now int64) error {
	var mode int
	if err := tx.QueryRowContext(ctx, `SELECT reported FROM inventory_runs WHERE job_id=?`, job).Scan(&mode); err != nil {
		return err
	}
	if mode == strictReportMode {
		return nil
	}
	return recordNoticeTx(ctx, tx, source, folder, now)
}
