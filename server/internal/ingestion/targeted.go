package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Scans limited to paths an outside tool reported.
//
// A download manager knows exactly which folder it just wrote. Telling Portico
// lets the scan look there and nowhere else, instead of walking a library to
// find one new file.
//
// A reported path is mapped to the library folder it lies in and narrowed to a
// directory Portico already knows: the path itself when it is a known folder,
// the folder of a known file, otherwise the nearest known folder above it
// (which is where a new folder or file will be found). A removed, known file is marked missing immediately when its parent
// and verified library root are readable; other changes are queued.
//
// The scan that follows is an ordinary incremental run seeded with those
// directories instead of the library folder's root. Everything else about it
// is unchanged: the same listing, the same matching, the same rule that a file
// gone from a listed folder is marked missing rather than deleted. Folders
// outside the reported ones are not listed and nothing in them is judged.
//
// A report that arrives while a scan of the same library folder is running is
// kept and scanned when that run ends. Reports for one folder made twice are
// one report.

const (
	// MaxReportedPaths bounds one report; MaxPathBytes one path in it.
	MaxReportedPaths = 200
	MaxPathBytes     = 4096
	// maxPendingNotices bounds what waits for one library folder. Past it the
	// reports collapse into one scan of the whole folder, which is what that
	// many changes amount to.
	maxPendingNotices = 500
	// maxPathDepth bounds the walk from a reported path up to a known folder.
	maxPathDepth = 64
)

// ErrPathReport is a report that cannot be read at all.
var ErrPathReport = requestinput.New("invalid path report")

// ReportedPath is what became of one path in a report.
type ReportedPath struct {
	Path string `json:"path"`
	// Status is "queued", "deferred" for accepted maintained work, or why nothing will be scanned:
	// "outside_libraries" (no library folder contains it) or "invalid".
	Status    string `json:"status"`
	LibraryID string `json:"libraryId,omitempty"`
	SourceID  string `json:"sourceId,omitempty"`
	ScanScope string `json:"scanScope,omitempty"`
	// Folder is the folder that will be scanned, relative to the library
	// folder; "." is the library folder itself.
	Folder string `json:"folder,omitempty"`
}

// PathReport is the answer to a report.
type PathReport struct {
	Deferred bool           `json:"deferred,omitempty"`
	Paths    []ReportedPath `json:"paths"`
	// Jobs are the scans that were started or were already running for the
	// affected library folders.
	Jobs []Job `json:"jobs"`
}

type reportSource struct {
	id, library, configured, resolved, incarnation string
}

func cleanReportedPath(raw string) (string, bool) {
	if raw == "" || len(raw) > MaxPathBytes || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return "", false
	}
	if !filepath.IsAbs(raw) {
		return "", false
	}
	return filepath.Clean(raw), true
}

// relativeTo returns path relative to root when path is root or inside it.
func relativeTo(root, path string) (string, bool) {
	if root == "" || !filepath.IsAbs(root) {
		return "", false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	if relative == "." {
		return ".", true
	}
	if !filepath.IsLocal(relative) {
		return "", false
	}
	return relative, true
}

// ReportPaths records that the given absolute paths were added, changed or
// removed and starts scans limited to them. authorize runs in the transaction
// that records the report.
func (s *Service) ReportPaths(ctx context.Context, paths []string, authorize func(*sql.Tx) error) (PathReport, error) {
	out := PathReport{Paths: []ReportedPath{}, Jobs: []Job{}}
	if len(paths) == 0 || len(paths) > MaxReportedPaths {
		return out, ErrPathReport
	}
	evidence, err := s.removedPathEvidence(ctx, paths)
	if err != nil {
		return out, err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return out, err
		}
	}
	if err = applyReportedMissingTx(ctx, tx, evidence); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.library_id,s.configured_root,s.root,s.incarnation FROM library_sources s WHERE s.enabled=1 AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id) ORDER BY s.id LIMIT 4096`)
	if err != nil {
		return out, err
	}
	sources := []reportSource{}
	for rows.Next() {
		var source reportSource
		if err = rows.Scan(&source.id, &source.library, &source.configured, &source.resolved, &source.incarnation); err != nil {
			rows.Close()
			return out, err
		}
		sources = append(sources, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	now := time.Now().UnixMilli()
	touched := map[string]bool{}
	for _, raw := range paths {
		path, ok := cleanReportedPath(raw)
		if !ok {
			out.Paths = append(out.Paths, ReportedPath{Path: boundedPath(raw), Status: "invalid"})
			continue
		}
		matched := false
		for _, source := range sources {
			// The owner may have typed a path that resolves elsewhere (a link);
			// a tool may report either spelling.
			relative, inside := relativeTo(source.resolved, path)
			if !inside {
				relative, inside = relativeTo(source.configured, path)
			}
			if !inside {
				continue
			}
			folder, e := knownFolderTx(ctx, tx, source, relative)
			if e != nil {
				return out, e
			}
			if e = recordNoticeTx(ctx, tx, source.id, folder, now); e != nil {
				return out, e
			}
			matched, touched[source.id] = true, true
			out.Paths = append(out.Paths, ReportedPath{Path: path, Status: "queued", LibraryID: source.library, SourceID: source.id, Folder: folder})
		}
		if !matched {
			out.Paths = append(out.Paths, ReportedPath{Path: path, Status: "outside_libraries"})
		}
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		job, e := startTargetedTx(ctx, tx, id)
		if e != nil {
			return out, e
		}
		out.Jobs = append(out.Jobs, job)
	}
	if err = gated.Commit(); err != nil {
		return PathReport{Paths: []ReportedPath{}, Jobs: []Job{}}, err
	}
	return out, nil
}

func boundedPath(raw string) string {
	if len(raw) > 256 {
		raw = raw[:256]
	}
	return strings.ToValidUTF8(strings.ReplaceAll(raw, "\x00", ""), "")
}

// knownFolderTx narrows a path inside a library folder to a folder the last
// scan knew: the path when it is one, the folder of a known file, or the
// nearest known folder above. The library folder itself is always known.
func knownFolderTx(ctx context.Context, tx *sql.Tx, source reportSource, relative string) (string, error) {
	if relative == "." {
		return ".", nil
	}
	var known int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE source_id=? AND relative_path=? AND retired=0)`, source.id, relative).Scan(&known)
	if err != nil {
		return "", err
	}
	current := relative
	if known != 0 {
		current = filepath.Dir(relative)
	}
	for depth := 0; depth < maxPathDepth && current != "." && current != string(filepath.Separator); depth++ {
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_directory_cache WHERE source_id=? AND incarnation=? AND relative_path=?)`, source.id, source.incarnation, current).Scan(&known); err != nil {
			return "", err
		}
		if known != 0 {
			return current, nil
		}
		current = filepath.Dir(current)
	}
	return ".", nil
}

func recordNoticeTx(ctx context.Context, tx *sql.Tx, source, folder string, now int64) error {
	var waiting int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_path_notices WHERE source_id=?`, source).Scan(&waiting); err != nil {
		return err
	}
	if waiting >= maxPendingNotices {
		if _, err := tx.ExecContext(ctx, `DELETE FROM inventory_path_notices WHERE source_id=?`, source); err != nil {
			return err
		}
		folder = "."
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_path_notices(source_id,relative_path,created_ms) VALUES(?,?,?)`, source, folder, now)
	return err
}

// within reports whether folder is ancestor or inside it.
func within(ancestor, folder string) bool {
	return ancestor == "." || folder == ancestor || strings.HasPrefix(folder, ancestor+string(filepath.Separator))
}

// startTargetedTx turns a library folder's waiting reports into a scan. While
// another run holds the folder the reports stay waiting, and that run's job is
// returned. A run that is due to be a full one anyway (the first, or the weekly
// integrity run) covers every report by itself.
func startTargetedTx(ctx context.Context, tx *sql.Tx, source string) (Job, error) {
	var active string
	err := tx.QueryRowContext(ctx, `SELECT a.job_id FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id WHERE a.source_id=? AND j.status IN('queued','running','paused')`, source).Scan(&active)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, err
	}
	if err == nil {
		// queueSourceTx answers with the run in progress and starts nothing.
		return queueSourceTx(ctx, tx, source, false)
	}
	rows, err := tx.QueryContext(ctx, `SELECT relative_path FROM inventory_path_notices WHERE source_id=? ORDER BY relative_path LIMIT ?`, source, maxPendingNotices+1)
	if err != nil {
		return Job{}, err
	}
	folders := []string{}
	for rows.Next() {
		var folder string
		if err = rows.Scan(&folder); err != nil {
			rows.Close()
			return Job{}, err
		}
		folders = append(folders, folder)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Job{}, err
	}
	if len(folders) == 0 {
		return Job{}, sql.ErrNoRows
	}
	job, err := queueSourceTx(context.WithValue(ctx, scanModeKey{}, ""), tx, source, false)
	if err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET reported=1 WHERE job_id=?`, job.ID); err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_path_notices WHERE source_id=?`, source); err != nil {
		return Job{}, err
	}
	if job.ScanMode != "incremental" {
		return job, nil
	}
	// A folder inside another reported folder is scanned with it.
	targets := []string{}
	for _, folder := range folders {
		covered := false
		for _, other := range folders {
			covered = covered || other != folder && within(other, folder)
		}
		if !covered {
			targets = append(targets, folder)
		}
	}
	for _, target := range targets {
		if target == "." {
			// The whole library folder was reported: an ordinary run.
			return job, nil
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_directories WHERE job_id=? AND relative_path='.'`, job.ID); err != nil {
		return Job{}, err
	}
	for _, target := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_run_targets(job_id,relative_path) VALUES(?,?)`, job.ID, target); err != nil {
			return Job{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_directories(job_id,relative_path) VALUES(?,?)`, job.ID, target); err != nil {
			return Job{}, err
		}
	}
	return job, nil
}

// startWaitingReports begins the scans for reports that arrived while their
// library folder was busy. It is called from the scan loop, which a finished
// run wakes.
func (s *Service) startWaitingReports(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT n.source_id FROM inventory_path_notices n WHERE NOT EXISTS(SELECT 1 FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id WHERE a.source_id=n.source_id AND j.status IN('queued','running','paused')) LIMIT 8`)
	if err != nil {
		return
	}
	sources := []string{}
	for rows.Next() {
		var source string
		if rows.Scan(&source) == nil {
			sources = append(sources, source)
		}
	}
	rows.Close()
	for _, source := range sources {
		gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
		if err != nil {
			return
		}
		if _, err = startTargetedTx(ctx, gated.Tx(), source); err == nil {
			err = gated.Commit()
		}
		if err != nil {
			gated.Rollback()
			// A library folder that can no longer be scanned (disabled, removed)
			// must not hold its reports for ever.
			if errors.Is(err, sql.ErrNoRows) {
				_, _ = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM inventory_path_notices WHERE source_id=?`, source)
			}
		}
	}
}

// targetScopeSQL limits a targeted run's judgement of what is missing to the
// folders it was told about. It is true for every object of an ordinary run.
// The single argument is the path separator.
const targetScopeSQL = `(NOT EXISTS(SELECT 1 FROM inventory_run_targets t WHERE t.job_id=r.job_id) OR EXISTS(SELECT 1 FROM inventory_run_targets t WHERE t.job_id=r.job_id AND (o.directory_path=t.relative_path OR substr(o.directory_path,1,length(t.relative_path)+1)=t.relative_path||?)))`

// widenTarget replaces a reported folder that could not be listed with the
// folder above it. A tool that deletes a folder reports the folder it deleted;
// that folder is no longer there to list, and it is the listing of its parent
// that shows it is gone. Each call climbs one level, so the climb is bounded by
// the depth of the path, and it ends at the library folder itself, where the
// run becomes an ordinary one and an unreachable library is handled as it
// always is. It reports false for a folder that is not a reported one or has
// already been partly listed.
func (s *Service) widenTarget(ctx context.Context, job, relative string) (bool, error) {
	if relative == "." {
		return false, nil
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	var reportMode int
	if err = tx.QueryRowContext(ctx, `SELECT reported FROM inventory_runs WHERE job_id=?`, job).Scan(&reportMode); err != nil {
		return false, err
	}
	if reportMode == strictReportMode {
		// A target lost after admission is protected/excluded by the caller.
		// Never widen a strict job, including to a non-root ancestor.
		return false, nil
	}
	var untouched int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_run_targets t JOIN inventory_directories d ON d.job_id=t.job_id AND d.relative_path=t.relative_path WHERE t.job_id=? AND t.relative_path=? AND d.state='pending' AND d.cursor='' AND d.pages=0)`, job, relative).Scan(&untouched); err != nil || untouched == 0 {
		return false, err
	}
	parent := filepath.Dir(relative)
	if parent == string(filepath.Separator) || parent == "" {
		parent = "."
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_directories WHERE job_id=? AND relative_path=?`, job, relative); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_run_targets WHERE job_id=? AND relative_path=?`, job, relative); err != nil {
		return false, err
	}
	if parent == "." {
		// The whole library folder: an ordinary run, which skips what has not
		// changed and judges everything.
		if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_run_targets WHERE job_id=?`, job); err != nil {
			return false, err
		}
	} else if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_run_targets(job_id,relative_path) VALUES(?,?)`, job, parent); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_directories(job_id,relative_path) VALUES(?,?)`, job, parent); err != nil {
		return false, err
	}
	return true, gated.Commit()
}

// reportHandoverAfter is how long a run analyses before path reports waiting
// for its source take the source over. A run holds its source for its whole
// analysis backlog, which can take days after a large first import; without a
// handover a file added meanwhile would not even be listed until then. The
// floor keeps steady reports from starving the backlog: every run still gets
// this much analysis, and its finished results stay finished.
var reportHandoverAfter = 2 * time.Minute

// handOverToReports ends a run whose discovery is done when new path reports
// have waited for its source and it has analysed for reportHandoverAfter. It
// runs between analysis quanta, so no probe or deep stage is cut off. The run
// finishes exactly as it would at the end of its queue, and a targeted run for
// the waiting reports starts in the same commit. Nothing is dropped: that run's
// own analysis enqueue walks the whole source, so every object the old run had
// not analysed yet is queued again, under the new run's fences.
//
// Only the basic phases hand over. The enqueue walk is bounded and must finish
// for a run to analyse anything, so handing over during it would let a steady
// stream of reports keep any analysis from starting.
func (s *Service) handOverToReports(ctx context.Context, job string, source catalog.LibrarySource) (bool, error) {
	s.mu.Lock()
	if s.analysingSince == nil {
		s.analysingSince = map[string]time.Time{}
	}
	since, seen := s.analysingSince[job]
	if !seen {
		since = time.Now()
		s.analysingSince[job] = since
	}
	s.mu.Unlock()
	if time.Since(since) < reportHandoverAfter {
		return false, nil
	}
	var waiting bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_path_notices WHERE source_id=?)`, source.ID).Scan(&waiting); err != nil || !waiting {
		return false, err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if _, err = catalog.InventoryFenceTx(ctx, tx, job); err != nil {
		return false, err
	}
	if err = finishScanTx(ctx, tx, job, source.ID); err != nil {
		return false, err
	}
	// A second-pass successor queued by the finish holds the source instead;
	// the reports then wait for it, as they would for any other active run.
	next, err := startTargetedTx(ctx, tx, source.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err = gated.Commit(); err != nil {
		return false, err
	}
	s.forgetAnalysing(job)
	log.Printf("library scan %s handed its source to waiting folder reports (scan %s); its remaining analysis continues there", job, next.ID)
	return true, nil
}

func (s *Service) forgetAnalysing(job string) {
	s.mu.Lock()
	delete(s.analysingSince, job)
	s.mu.Unlock()
}
