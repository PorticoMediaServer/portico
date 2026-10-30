package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Removing a library, and removing one folder from a library.
//
// Removing a library hides it at once (nobody sees it, nothing scans it) and
// then takes it apart in the background in short transactions: every title
// goes, with each profile's state kept under the title's identities so the
// same title added again later gets it back; then the scan records; then the
// library itself. Files on disk are never touched.
//
// Removing a folder marks the files that were under it missing. Their titles
// follow the owner's rule for missing titles, exactly as if the files had been
// deleted, and a folder added back before then brings them back.

// ErrLibraryRemoving: the library is already being removed.
var ErrLibraryRemoving = errors.New("the library is being removed")
var errLibraryMutationStale = errors.New("library mutation custody changed; authorized resolution required")

const (
	libraryRemovalKind = "library-removal"
	folderRemovalKind  = "folder-removal"
	removalBatch       = 4
)

// RemoveLibrary starts removing a library. name must be the library's current
// name (the owner typed it back) and expected its configuration revision.
// enqueue queues the background work in the same transaction.
func (s *Service) RemoveLibrary(ctx context.Context, id, name string, expected int64, authorize func(*sql.Tx) error, enqueue func(context.Context, *sql.Tx, string, string) error) error {
	if enqueue == nil {
		return ErrAdminQuery
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		if err := configurationFence(ctx, tx, id, expected, authorize); err != nil {
			return err
		}
		var current string
		var retired bool
		if err := tx.QueryRowContext(ctx, `SELECT l.name,COALESCE((SELECT retired FROM catalog_libraries WHERE library_id=l.id),0) FROM libraries l WHERE l.id=?`, id).Scan(&current, &retired); err != nil {
			return err
		}
		if retired {
			return ErrLibraryRemoving
		}
		if strings.TrimSpace(name) != current {
			return ErrConfirmationMismatch
		}
		if _, err := compactcatalog.LibraryTx(ctx, tx, id); err != nil {
			return err
		}
		for _, q := range []string{
			`UPDATE catalog_libraries SET retired=1 WHERE library_id=?`,
			`UPDATE jobs SET status='cancelled' WHERE library_id=? AND status IN('queued','running')`,
			`DELETE FROM inventory_source_active WHERE source_id IN(SELECT id FROM library_sources WHERE library_id=?)`,
			`UPDATE library_sources SET enabled=0,health='removing',generation=generation+1,next_scan_at='' WHERE library_id=?`,
			`UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`,
			`UPDATE library_configuration SET revision=revision+1,metadata_revision=metadata_revision+1 WHERE library_id=?`,
			`DELETE FROM catalog_removal_progress WHERE kind='library' AND target=?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return enqueue(ctx, tx, libraryRemovalKind, id)
	})
}

// ErrConfirmationMismatch: what the owner typed is not the library's name.
var ErrConfirmationMismatch = requestinput.New("the confirmation does not match")

// LibraryRemovalAdapter is the background work behind RemoveLibrary.
func (s *Service) LibraryRemovalAdapter() operations.Adapter {
	return operations.Adapter{Kind: libraryRemovalKind, Lane: "background-media", Resource: operations.LaneWriteHeavy,
		ValidateTx: func(ctx context.Context, tx *sql.Tx, id string) error {
			var n int
			return tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_libraries WHERE library_id=? AND retired=1`, id).Scan(&n)
		},
		Step: func(ctx context.Context, id string) (operations.JobObservation, error) {
			return s.removalStep(ctx, func(tx *sql.Tx) (bool, error) { return removeLibraryBatchTx(ctx, tx, id) })
		}}
}

// FolderRemovalAdapter is the background work behind RemoveSource.
func (s *Service) FolderRemovalAdapter() operations.Adapter {
	return operations.Adapter{Kind: folderRemovalKind, Lane: "background-media", Resource: operations.LaneWriteHeavy,
		ValidateTx: func(ctx context.Context, tx *sql.Tx, id string) error {
			var n int
			return tx.QueryRowContext(ctx, `SELECT 1 FROM library_sources WHERE id=? AND enabled=0`, id).Scan(&n)
		},
		Step: func(ctx context.Context, id string) (operations.JobObservation, error) {
			return s.removalStep(ctx, func(tx *sql.Tx) (bool, error) { return removeFolderBatchTx(ctx, tx, id) })
		}}
}

func (s *Service) removalStep(ctx context.Context, batch func(*sql.Tx) (bool, error)) (operations.JobObservation, error) {
	out := operations.JobObservation{State: "running"}
	for ctx.Err() == nil {
		done := false
		err := dbwork.WithWriteTx(MutationWriteContext(ctx), s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			var e error
			done, e = batch(tx)
			return e
		})
		if err != nil {
			if errors.Is(err, errLibraryMutationStale) {
				return operations.JobObservation{State: "paused", Phase: "catalog custody changed; authorized resolution required"}, nil
			}
			if errors.Is(err, sql.ErrNoRows) {
				return operations.JobObservation{State: "succeeded"}, nil
			}
			if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return out, err
		}
		if done {
			out.State = "succeeded"
			break
		}
		dbwork.Yield(ctx)
	}
	return out, nil
}

// removalProgress is where one removal has got to (catalog_removal_progress).
type removalProgress struct {
	kind, target string
	phase        int
	entityKind   int
	entityCursor int64
	objectCursor string
	walkRemoved  bool
	walkKept     bool
}

func loadRemovalProgressTx(ctx context.Context, tx *sql.Tx, kind, target string) (removalProgress, error) {
	p := removalProgress{kind: kind, target: target}
	err := tx.QueryRowContext(ctx, `SELECT phase,entity_kind,entity_cursor,object_cursor,walk_removed,walk_kept FROM catalog_removal_progress WHERE kind=? AND target=?`, kind, target).Scan(&p.phase, &p.entityKind, &p.entityCursor, &p.objectCursor, &p.walkRemoved, &p.walkKept)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return p, err
}

func (p removalProgress) saveTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_removal_progress(kind,target,phase,entity_kind,entity_cursor,object_cursor,walk_removed,walk_kept) VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(kind,target) DO UPDATE SET phase=excluded.phase,entity_kind=excluded.entity_kind,entity_cursor=excluded.entity_cursor,object_cursor=excluded.object_cursor,walk_removed=excluded.walk_removed,walk_kept=excluded.walk_kept`,
		p.kind, p.target, p.phase, p.entityKind, p.entityCursor, p.objectCursor, p.walkRemoved, p.walkKept)
	return err
}

func clearRemovalProgressTx(ctx context.Context, tx *sql.Tx, kind, target string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_removal_progress WHERE kind=? AND target=?`, kind, target)
	return err
}

// removeFolderBatchTx marks a few more of a removed folder's files missing.
// A folder that was added back in the meantime is left alone. It walks the
// folder's files once, in id order from a saved place: a file already missing
// stays at the front of the index, so starting from the front each time
// would read every finished file again.
func removeFolderBatchTx(ctx context.Context, tx *sql.Tx, source string) (bool, error) {
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM library_sources WHERE id=?`, source).Scan(&enabled); err != nil {
		return false, err
	}
	if enabled {
		return true, clearRemovalProgressTx(ctx, tx, "folder", source)
	}
	progress, err := loadRemovalProgressTx(ctx, tx, "folder", source)
	if err != nil {
		return false, err
	}
	type file struct{ object, token, state string }
	rows, err := tx.QueryContext(ctx, `SELECT id,asset_id,state FROM inventory_objects INDEXED BY inventory_objects_source WHERE source_id=? AND retired=0 AND id>? ORDER BY id LIMIT ?`, source, progress.objectCursor, removalBatch*4)
	if err != nil {
		return false, err
	}
	var files []file
	for rows.Next() {
		var f file
		if err = rows.Scan(&f.object, &f.token, &f.state); err != nil {
			rows.Close()
			return false, err
		}
		files = append(files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, f := range files {
		if f.state == "missing" {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET state='missing',missing_since=CASE WHEN missing_since='' THEN ? ELSE missing_since END WHERE id=?`, now, f.object); err != nil {
			return false, err
		}
		id, e := compactcatalog.AssetByTokenTx(ctx, tx, f.token)
		if e != nil {
			return false, e
		}
		if id == 0 {
			continue
		}
		var available bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT available FROM inventory_asset_availability WHERE asset_id=?),0)`, f.token).Scan(&available); err != nil {
			return false, err
		}
		if err = compactcatalog.SetAssetAvailableTx(ctx, tx, id, available); err != nil {
			return false, err
		}
		if err = compactcatalog.SettleAssetMissingTx(ctx, tx, id); err != nil {
			return false, err
		}
	}
	if len(files) < removalBatch*4 {
		var job string
		e := tx.QueryRowContext(ctx, `SELECT job_id FROM inventory_reading_jobs WHERE source_id=? ORDER BY job_id LIMIT 1`, source).Scan(&job)
		if e == nil {
			_, e = DrainReadingClosuresTx(ctx, tx, job, 64)
			return false, e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return false, e
		}
		return true, clearRemovalProgressTx(ctx, tx, "folder", source)
	}
	progress.objectCursor = files[len(files)-1].object
	return false, progress.saveTx(ctx, tx)
}

// The phases of taking a library apart, in order. Each is saved as it
// finishes, so a batch never re-reads a phase that is done.
const (
	removalTitles = iota
	removalFiles
	removalBookkeeping
	removalContainers
	removalProjections
	removalLibrary
)

// containerKindOrder is the order containers are taken apart in: seasons,
// albums, books and collections before the shows, artists and authors that
// hold them; then every other kind. A container that cannot go yet is tried
// again on a later walk once something under it has gone.
var containerKindOrder = []int{3, 6, 8, 10, 2, 5}

// removeLibraryBatchTx does the next small piece of taking a library apart
// and reports whether nothing is left to do. Its place is saved with each
// piece, so a restart carries on where it stopped.
func removeLibraryBatchTx(ctx context.Context, tx *sql.Tx, library string) (bool, error) {
	progress, err := loadRemovalProgressTx(ctx, tx, "library", library)
	if err != nil {
		return false, err
	}
	var handle int64
	var retired bool
	err = tx.QueryRowContext(ctx, `SELECT id,retired FROM catalog_libraries WHERE library_id=?`, library).Scan(&handle, &retired)
	if errors.Is(err, sql.ErrNoRows) {
		// The catalogue row is gone. That used to be the end of the removal; a
		// Reading settle state still owes its bounded residue proof, which does
		// not need the public rows. Everything else finishes as before.
		return readingRemovalAfterRowTx(ctx, tx, library, &progress)
	}
	if err != nil {
		return false, err
	}
	if !retired {
		return true, nil
	}
	for {
		var more bool
		switch progress.phase {
		case removalTitles:
			more, err = removeLibraryTitlesTx(ctx, tx, handle)
		case removalFiles:
			more, err = removeLibraryFilesTx(ctx, tx, library)
		case removalBookkeeping:
			more, err = removeLibraryBookkeepingTx(ctx, tx, library)
		case removalContainers:
			more, err = removeLibraryContainersTx(ctx, tx, handle, &progress)
		case removalProjections:
			var clear bool
			clear, err = compactcatalog.ClearLibraryTx(ctx, tx, library, 64)
			more = !clear
		case removalLibrary:
			// Private Reading lifetime is reclaimed here, before and after the
			// final rows, because no dispatcher owns it for a retired library.
			// Every path persists its own phase-5 state in this transaction and
			// reports whether the removal is finished.
			return removeLibraryReadingTx(ctx, tx, library, handle, &progress)
		default:
			return true, removeLibraryRowTx(ctx, tx, library)
		}
		if err != nil {
			return false, err
		}
		if more {
			return false, progress.saveTx(ctx, tx)
		}
		// A finished phase costs one indexed probe; the next phase starts in
		// this same transaction only when the finished one did no work.
		progress = removalProgress{kind: progress.kind, target: progress.target, phase: progress.phase + 1}
	}
}

// 1. Everything that plays. Removing a title keeps each profile's state and
// takes its emptied show, season, album or book with it. Each playable kind
// is sought on its own, so the books and comics of a reading library, which
// do not play, are never read here.
func removeLibraryTitlesTx(ctx context.Context, tx *sql.Tx, handle int64) (bool, error) {
	var title int64
	err := tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_kinds k CROSS JOIN catalog_entities e INDEXED BY catalog_entities_library ON e.library_id=? AND e.kind=k.id WHERE k.playable=1 ORDER BY k.id,e.id LIMIT 1`, handle).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	step, err := removeOwnedStepTx(ctx, tx, title, "library", fmt.Sprint(handle), 1, "remove")
	if err != nil {
		return true, err
	}
	if step.Complete && step.Outcome == "stale" && !step.Progressed {
		return true, errLibraryMutationStale
	}
	if step.Complete && step.Outcome != "stale" {
		err = ackOwnedSubjectTx(ctx, tx, "library", fmt.Sprint(handle), title, "remove")
	}
	return true, err
}

// 2. Scan records for files that never became titles, one folder at a time.
// Each batch deletes what it read, so the front of the index is always new.
func removeLibraryFilesTx(ctx context.Context, tx *sql.Tx, library string) (bool, error) {
	sources, err := librarySourceIDsTx(ctx, tx, library)
	if err != nil {
		return false, err
	}
	type file struct{ object, token string }
	var files []file
	for _, source := range sources {
		rows, err := tx.QueryContext(ctx, `SELECT id,asset_id FROM inventory_objects INDEXED BY inventory_objects_source WHERE source_id=? ORDER BY retired,id LIMIT ?`, source, removalBatch*4-len(files))
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var f file
			if err = rows.Scan(&f.object, &f.token); err != nil {
				rows.Close()
				return false, err
			}
			files = append(files, f)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		if len(files) == removalBatch*4 {
			break
		}
	}
	for _, f := range files {
		ready, e := compactcatalog.PrepareInfoObjectDeleteTx(ctx, tx, f.object)
		if e != nil {
			return false, e
		}
		if !ready {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_analysis_queue WHERE object_id=?`, f.object); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_objects WHERE id=?`, f.object); err != nil {
			return false, err
		}
		var used bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE asset_id=?1) OR EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE a.token=?1)`, f.token).Scan(&used); err != nil {
			return false, err
		}
		if !used {
			if id, e := compactcatalog.AssetByTokenTx(ctx, tx, f.token); e != nil {
				return false, e
			} else if id != 0 {
				if e = forgetAssetTx(ctx, tx, id, f.token); e != nil {
					return false, e
				}
			}
		}
	}
	return len(files) > 0, nil
}

func librarySourceIDsTx(ctx context.Context, tx *sql.Tx, library string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM library_sources WHERE library_id=? ORDER BY id`, library)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// 3. Scan bookkeeping, and every other row that hangs off the library or
// its folders in numbers, a bounded slice at a time. What is left for the
// last statements to delete, or to cascade, is a handful of rows.
func removeLibraryBookkeepingTx(ctx context.Context, tx *sql.Tx, library string) (bool, error) {
	var closureJob string
	e := tx.QueryRowContext(ctx, `SELECT c.job_id FROM jobs j INDEXED BY jobs_library_directory CROSS JOIN inventory_reading_jobs c ON c.job_id=j.id WHERE j.library_id=? LIMIT 1`, library).Scan(&closureJob)
	if e == nil {
		return DrainReadingClosuresTx(ctx, tx, closureJob, 64)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	for _, q := range []string{
		`DELETE FROM inventory_directories WHERE rowid IN(SELECT d.rowid FROM jobs j INDEXED BY jobs_library_directory CROSS JOIN inventory_directories d ON d.job_id=j.id WHERE j.library_id=?1 LIMIT 2000)`,
		`DELETE FROM scan_queue WHERE rowid IN(SELECT q.rowid FROM jobs j INDEXED BY jobs_library_directory CROSS JOIN scan_queue q ON q.job_id=j.id WHERE j.library_id=?1 LIMIT 2000)`,
		`DELETE FROM inventory_analysis_queue WHERE rowid IN(SELECT q.rowid FROM jobs j INDEXED BY jobs_library_directory CROSS JOIN inventory_analysis_queue q ON q.job_id=j.id WHERE j.library_id=?1 LIMIT 2000)`,
	} {
		res, e := tx.ExecContext(ctx, q, library)
		if e != nil {
			return false, e
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return true, nil
		}
	}
	sources, err := librarySourceIDsTx(ctx, tx, library)
	if err != nil {
		return false, err
	}
	refs, err := bulkReferences(ctx, tx)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		ids := []any{library}
		if ref.parent == "library_sources" {
			ids = ids[:0]
			for _, source := range sources {
				ids = append(ids, source)
			}
		}
		for _, id := range ids {
			res, e := tx.ExecContext(ctx, `DELETE FROM "`+ref.table+`" WHERE `+ref.key+` IN(SELECT `+ref.keyColumns+` FROM "`+ref.table+`" WHERE "`+ref.column+`"=? LIMIT 2000)`, id)
			if e != nil {
				return false, e
			}
			if n, _ := res.RowsAffected(); n > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

// 4. What is left of the catalogue: containers nobody plays from any more,
// one kind at a time from a saved place. One that something still refers to
// stays behind, out of sight. A walk that removed something and also had to
// keep something is followed by another, since a show goes once its seasons
// have and an artist once its albums have.
func removeLibraryContainersTx(ctx context.Context, tx *sql.Tx, handle int64, progress *removalProgress) (bool, error) {
	kinds, err := containerKinds(ctx, tx)
	if err != nil {
		return false, err
	}
	const containerBatch = 64
	for progress.entityKind < len(kinds) {
		containers, err := scanInt64s(tx.QueryContext(ctx, `SELECT id FROM catalog_entities INDEXED BY catalog_entities_library WHERE library_id=? AND kind=? AND id>? ORDER BY id LIMIT ?`, handle, kinds[progress.entityKind], progress.entityCursor, containerBatch))
		if err != nil {
			return false, err
		}
		for _, id := range containers {
			step, e := removeOwnedStepTx(ctx, tx, id, "library", fmt.Sprint(handle), 1, "force")
			if e != nil {
				return false, e
			}
			if !step.Complete {
				return true, nil
			}
			if step.Outcome == "stale" {
				if step.Progressed {
					return true, nil
				}
				return true, errLibraryMutationStale
			}
			if e = ackOwnedSubjectTx(ctx, tx, "library", fmt.Sprint(handle), id, "force"); e != nil {
				return false, e
			}
			if step.Outcome == "removed" {
				progress.walkRemoved = true
			} else {
				progress.walkKept = true
			}
			progress.entityCursor = id
			return true, nil
		}
		if len(containers) == containerBatch {
			return true, nil
		}
		progress.entityKind++
		progress.entityCursor = 0
		if len(containers) > 0 {
			return true, nil
		}
	}
	if progress.walkRemoved && progress.walkKept {
		*progress = removalProgress{kind: progress.kind, target: progress.target, phase: progress.phase}
		return true, nil
	}
	return false, nil
}

func containerKinds(ctx context.Context, tx *sql.Tx) ([]int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM catalog_kinds ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kinds := append([]int(nil), containerKindOrder...)
	first := map[int]bool{}
	for _, kind := range containerKindOrder {
		first[kind] = true
	}
	for rows.Next() {
		var kind int
		if err = rows.Scan(&kind); err != nil {
			return nil, err
		}
		if !first[kind] {
			kinds = append(kinds, kind)
		}
	}
	return kinds, rows.Err()
}

// removeLibraryContainerTx removes one container and reports whether it went.
func removeLibraryContainerTx(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT library_container`); err != nil {
		return false, err
	}
	e := forceRemoveContainerTx(ctx, tx, id)
	if e != nil {
		// Something outside the catalogue still names it: those references
		// go with it, since nothing can reach the library any more.
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO library_container`); err != nil {
			return false, err
		}
		if e = clearReferencesTx(ctx, tx, "catalog_entities", id); e == nil {
			e = forceRemoveContainerTx(ctx, tx, id)
		}
		if e != nil {
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO library_container`); err != nil {
				return false, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `RELEASE library_container`); err != nil {
		return false, err
	}
	return e == nil, nil
}

// 5. The library itself. Its folders are freed for another library first,
// so the rows that remain can never get in the way of adding it again.
func removeLibraryRowTx(ctx context.Context, tx *sql.Tx, library string) error {
	for _, q := range []string{
		`DELETE FROM catalog_removal_progress WHERE kind='folder' AND target IN(SELECT id FROM library_sources WHERE library_id=?1)`,
		`DELETE FROM catalog_removal_progress WHERE kind='library' AND target=?1`,
		`DELETE FROM inventory_operation_jobs WHERE job_id IN(SELECT id FROM jobs WHERE library_id=?1)`,
		`DELETE FROM inventory_runs WHERE job_id IN(SELECT id FROM jobs WHERE library_id=?1)`,
		`DELETE FROM jobs WHERE library_id=?1`,
		`DELETE FROM audio_tag_evidence WHERE library_id=?1`,
		`DELETE FROM audio_source_metadata WHERE library_id=?1`,
		`DELETE FROM episodic_sources WHERE library_id=?1`,
		`DELETE FROM inventory_policy_pending WHERE library_id=?1`,
		`DELETE FROM catalog_missing_titles WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)`,
		`DELETE FROM admin_documents WHERE scope='library:'||?1`,
		`UPDATE library_sources SET root='removed:'||id,configured_root='removed:'||id WHERE library_id=?1 AND root NOT LIKE 'removed:%'`,
		`UPDATE libraries SET root='removed:'||id WHERE id=?1 AND root NOT LIKE 'removed:%'`,
		`UPDATE catalog_libraries SET root='removed:'||library_id WHERE library_id=?1 AND root NOT LIKE 'removed:%'`,
	} {
		if _, err := tx.ExecContext(ctx, q, library); err != nil {
			return err
		}
	}
	// Rows elsewhere that still name the library by either id, and that the
	// steps above do not know about, go too; a removed library leaves no row.
	if err := clearReferencesTx(ctx, tx, "libraries", library); err != nil {
		return err
	}
	var catalogHandle int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_libraries WHERE library_id=?`, library).Scan(&catalogHandle); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if catalogHandle != 0 {
		if err := clearReferencesTx(ctx, tx, "catalog_libraries", catalogHandle); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT library_row`); err != nil {
		return err
	}
	gone := true
	for _, q := range []string{
		`DELETE FROM library_scan_policies WHERE library_id=?1`,
		`DELETE FROM library_sources WHERE library_id=?1`,
		`DELETE FROM libraries WHERE id=?1`,
	} {
		if _, e := tx.ExecContext(ctx, q, library); e != nil {
			// Something derived still points at the library. It stays as a
			// hidden, empty row; nothing can reach it and its folders are free.
			gone = false
			break
		}
	}
	if !gone {
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO library_row`); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `RELEASE library_row`)
	return err
}

// referenceColumns lists, per parent table, every child (table, column) whose
// foreign key neither cascades nor clears on delete. The schema is fixed for
// the life of the process, so it is read once.
var referenceColumns = struct {
	sync.Mutex
	byParent map[string][][2]string
}{}

func restrictingReferences(ctx context.Context, tx *sql.Tx, parent string) ([][2]string, error) {
	referenceColumns.Lock()
	defer referenceColumns.Unlock()
	if referenceColumns.byParent == nil {
		rows, err := tx.QueryContext(ctx, `SELECT f."table",m.name,f."from" FROM sqlite_schema m JOIN pragma_foreign_key_list(m.name) f
 WHERE m.type='table' AND f."table" IN('libraries','catalog_libraries','catalog_entities') AND upper(f.on_delete) NOT IN('CASCADE','SET NULL') ORDER BY m.name,f."from"`)
		if err != nil {
			return nil, err
		}
		found := map[string][][2]string{}
		for rows.Next() {
			var p, table, column string
			if err = rows.Scan(&p, &table, &column); err != nil {
				rows.Close()
				return nil, err
			}
			found[p] = append(found[p], [2]string{table, column})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		referenceColumns.byParent = found
	}
	return referenceColumns.byParent[parent], nil
}

// bulkReference is a table whose rows name a library or one of its folders
// and can be many: the removal empties it in bounded slices before the last
// statements, which would otherwise delete or cascade over all of it at once.
type bulkReference struct {
	parent, table, column string
	// key selects one slice: rowid, or the primary key of a WITHOUT ROWID table.
	key, keyColumns string
}

var bulkReferenceList = struct {
	sync.Mutex
	refs []bulkReference
	read bool
}{}

// bulkReferences lists every table that refers to libraries or library_sources
// through a single-column foreign key that is not unique, that nothing else
// refers to (so deleting its rows can neither fail nor cascade further), and
// that is not one of the rows the removal takes apart in its own order. The
// schema is fixed for the life of the process, so it is read once.
func bulkReferences(ctx context.Context, tx *sql.Tx) ([]bulkReference, error) {
	bulkReferenceList.Lock()
	defer bulkReferenceList.Unlock()
	if bulkReferenceList.read {
		return bulkReferenceList.refs, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT f."table",m.name,f."from",instr(upper(m.sql),'WITHOUT ROWID')>0 FROM sqlite_schema m JOIN pragma_foreign_key_list(m.name) f
 WHERE m.type='table' AND f."table" IN('libraries','library_sources')
 AND m.name NOT IN('catalog_libraries','jobs','library_sources','library_scan_policies','inventory_runs','inventory_source_active','inventory_objects')
 AND NOT EXISTS(SELECT 1 FROM pragma_foreign_key_list(m.name) g WHERE g.id=f.id AND g.seq>0)
 AND NOT EXISTS(SELECT 1 FROM sqlite_schema o JOIN pragma_foreign_key_list(o.name) r WHERE o.type='table' AND r."table"=m.name)
 AND NOT EXISTS(SELECT 1 FROM pragma_table_info(m.name) c WHERE c.pk=1 AND c.name=f."from" AND (SELECT count(*) FROM pragma_table_info(m.name) WHERE pk>0)=1)
 AND NOT EXISTS(SELECT 1 FROM pragma_index_list(m.name) i WHERE i."unique"=1 AND (SELECT count(*) FROM pragma_index_info(i.name))=1 AND (SELECT name FROM pragma_index_info(i.name))=f."from")
 ORDER BY f."table",m.name,f."from"`)
	if err != nil {
		return nil, err
	}
	var refs []bulkReference
	var withoutRowid []bool
	for rows.Next() {
		var ref bulkReference
		var noRowid bool
		if err = rows.Scan(&ref.parent, &ref.table, &ref.column, &noRowid); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
		withoutRowid = append(withoutRowid, noRowid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range refs {
		refs[i].key, refs[i].keyColumns = "rowid", "rowid"
		if !withoutRowid[i] {
			continue
		}
		columns, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) WHERE pk>0 ORDER BY pk`, refs[i].table)
		if err != nil {
			return nil, err
		}
		var key []string
		for columns.Next() {
			var name string
			if err = columns.Scan(&name); err != nil {
				columns.Close()
				return nil, err
			}
			key = append(key, `"`+name+`"`)
		}
		err = columns.Err()
		columns.Close()
		if err != nil {
			return nil, err
		}
		refs[i].keyColumns = strings.Join(key, ",")
		refs[i].key = "(" + refs[i].keyColumns + ")"
	}
	bulkReferenceList.refs, bulkReferenceList.read = refs, true
	return refs, nil
}

// clearReferencesTx deletes the rows that still name one row of parent
// through a restricting foreign key: the last step of taking a removed library
// apart, after every known derived row has gone in bounded slices. A row of
// the catalogue's own hierarchy is never deleted here; the removal steps above
// take those apart in order.
func clearReferencesTx(ctx context.Context, tx *sql.Tx, parent string, id any) error {
	refs, err := restrictingReferences(ctx, tx, parent)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		table, column := ref[0], ref[1]
		switch table {
		case "catalog_entity_mutations", "catalog_entities", "catalog_libraries", "libraries", "library_sources", "library_scan_policies":
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM "`+table+`" WHERE "`+column+`"=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// forceRemoveContainerTx removes a show, season, album, artist, book, author
// or collection of a library that is being removed, keeping what people had
// marked on it.
func forceRemoveContainerTx(ctx context.Context, tx *sql.Tx, id int64) error {
	if err := retainStateTx(ctx, tx, id); err != nil {
		return err
	}
	if err := compactcatalog.ForgetIdentitiesTx(ctx, tx, id); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM container_personal_state WHERE container_id=?`,
		`DELETE FROM container_personal_undo WHERE container_id=?`,
		`DELETE FROM playback_sessions WHERE item_id=?`,
		`DELETE FROM book_resume WHERE book_id=?`,
		`DELETE FROM catalog_song_artists WHERE artist_id=?`,
		`DELETE FROM catalog_book_group_members WHERE book_id=?`,
		`DELETE FROM catalog_book_series_spellings WHERE book_id=?`,
		`DELETE FROM catalog_book_series_terms WHERE book_id=?`,
		`DELETE FROM catalog_book_position_terms WHERE book_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return compactcatalog.DeleteEntityTx(ctx, tx, id)
}
