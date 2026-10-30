package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Source verification: the one walk a returning source owes.
//
// A source's health is a transport fact, not a catalogue fact. While a folder is
// offline nothing about its files is rewritten: browsing keeps the catalogue it
// had, and playback answers from source health when it opens the file. But files
// can go while the folder is away, so a reconnect owes one question per file:
// is it still there? This walk asks it, and it is deliberately the only per-file
// work a reconnect can cause.
//
// The shape is the product ruling: one coalesced pending walk per source,
// resumable at an inventory object id, bounded per quantum, behind scans, users
// and every other writer. Only a file whose absence is physically proven against
// the source's own root identity is marked missing, through the same single-file
// writer a scan uses. Everything inconclusive is left alone for the next walk or
// the next scan, which is always allowed to be more certain.
//
// The durable state is one catalog_source_verification row per source, written by
// the health trigger and by this walk. It holds no per-item row, graph or result,
// and a source transition costs O(1) rows. `started` records that the walk
// captured its high-water object id, which is held in `fence_object` for the
// whole walk: the walk's end cannot move while it runs, and a later reconnect
// starts a new window because the trigger clears both.

var (
	// sourceVerificationWindow is how many raw inventory object ids one quantum
	// reads. It is a starting quantum inside the ordinary write plan, not a
	// constant of the design.
	sourceVerificationWindow = 16
	// sourceVerificationBytes bounds the scalar bytes one quantum decodes: the
	// object id, relative path, revision and root incarnation of every row.
	sourceVerificationBytes = 16 << 10
	// sourceVerificationBurst is how long the loop waits between two advancing
	// quanta. It is short on purpose: the walk's low priority comes from
	// dbwork.PaceBackground, Yield and the maintenance write class, not from an
	// artificial delay that would leave a large source unverified for days.
	sourceVerificationBurst = time.Millisecond
	// sourceVerificationQuiet is how long the loop waits after something more
	// authoritative than the walk moved, so it does not re-ask while a scan is
	// starting or a source is being reconfigured.
	sourceVerificationQuiet = 15 * time.Second
)

// Control outcomes, not failures: the walk simply has nothing it may do yet.
var (
	errVerificationStale     = errors.New("source verification: the source fence moved")
	errVerificationPreempted = errors.New("source verification: a scan holds the source")
	// errVerificationBusy means the storage layer asked this walk to come back
	// later. It keeps the raw cursor exactly where it is: a busy provider is not
	// evidence about any file.
	errVerificationBusy = errors.New("source verification: storage is busy")
	// errVerificationUnprovable means this build cannot yet prove a file's
	// absence on this source. The walk stays pending for it; it is never closed
	// as if it had been done.
	errVerificationUnprovable = errors.New("source verification: this source has no per-file absence proof")
)

// remoteAbsenceSupported reports whether the configured storage adapter can prove
// and revalidate a file's absence on a provider source. A source is claimable only
// when it can: otherwise its pending row is reported and left alone, which is also
// why it never holds a local source behind it.
func (s *Service) remoteAbsenceSupported() bool {
	if s.storage == nil || s.storage.Remote == nil {
		return false
	}
	_, ok := s.storage.Remote.(storage.RemoteInventoryAbsence)
	return ok
}

// validateAbsenceTx is the same-writer revalidation the accepting transaction
// needs. A local proof already opened the catalogued parent and compared the
// root's own identity and this writer re-reads the same row, so the storage layer
// answers nil for it. A remote proof came from retained provider listing
// evidence, and the adapter fences that exact listing and the captured source
// generation before anything is written.
func (s *Service) validateAbsenceTx(ctx context.Context, tx *sql.Tx, walk verificationWalk, request storage.InventoryRequest) error {
	if s.storage == nil {
		// In-process local proof: the writer-visible request above is the proof.
		return nil
	}
	err := s.storage.ValidateInventoryAbsenceTx(ctx, tx, verificationJob(walk), request)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrBusy):
		return errVerificationBusy
	case errors.Is(err, storage.ErrRemoteCursor), errors.Is(err, storage.ErrRemoteConfig),
		errors.Is(err, storage.ErrRemoteChanged), errors.Is(err, storage.ErrRemoteOffline),
		errors.Is(err, storage.ErrRemoteCredentials), errors.Is(err, storage.ErrRemoteBinary):
		return errVerificationUnprovable
	default:
		return errVerificationStale
	}
}

// sourceVerifiable is the set of stored health codes under which a source is
// reachable and not being restructured. It is the same set the availability view
// treats as usable: a scan is already the authority for everything this walk
// could conclude, and the walk waits for one rather than racing it.
func sourceVerifiable(health string) bool {
	return health == "ready" || health == "healthy" || health == "degraded"
}

// verificationWalk is one claimed pending row plus the source facts it must still
// hold to write anything.
type verificationWalk struct {
	source      string
	incarnation string
	generation  int64
	healthEpoch int64
	started     bool
	after       string
	fence       string
	root        string
	rootID      string
	follow      bool
}

// verificationRow is one raw inventory object id read in the current window.
type verificationRow struct {
	id, path, revision string
	state, rootInc     string
	unsupported        string
	retired            int
}

// runSourceVerification is the loop every background walk in this server has:
// wait for something to do, do one bounded piece, wait again. Its only urgent
// wake is a commit that set a pending row, so an idle server does no work at all.
func (s *Service) runSourceVerification(ctx context.Context) {
	wake := workerloop.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "catalog_source_verification")
	defer unregister()
	workerloop.RunWith(ctx, "ingestion.verify", nil, wake, s.verifySourceStep)
}

// verifySourceStep runs one quantum and says when to look again. Zero means
// "nothing is pending that I know of", so the loop sleeps until a commit wakes it
// or the safety tick passes.
func (s *Service) verifySourceStep(ctx context.Context) time.Duration {
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	ctx = dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, s.db))
	advanced, err := s.verifyOneSource(ctx)
	switch {
	case err == nil && advanced:
		// More exists: go straight on. PaceBackground inside the quantum is what
		// keeps a busy server ahead of this walk.
		return sourceVerificationBurst
	case err == nil:
		return 0
	case errors.Is(err, errVerificationStale), errors.Is(err, errVerificationPreempted):
		// Something more authoritative than this walk moved: a scan, a flap or a
		// configuration change. The row is untouched, so the next pass resumes it.
		return sourceVerificationQuiet
	case errors.Is(err, errVerificationBusy), errors.Is(err, errVerificationUnprovable):
		// Storage asked for room or cannot answer here; the row keeps its cursor
		// and stays pending, and reportBlockedVerification names the blocker once.
		return sourceVerificationQuiet
	case ctx.Err() != nil:
		return 0
	default:
		log.Printf("source verification: %v", err)
		return time.Minute
	}
}

// verifyOneSource runs one bounded quantum of the first walk that may run now.
func (s *Service) verifyOneSource(ctx context.Context) (bool, error) {
	// The batch boundary: no transaction is open here, and an owner pause or a
	// busy foreground is answered before any read.
	if !dbwork.PaceBackground(ctx, time.Now()) {
		return false, ctx.Err()
	}
	if blocked, err := s.verificationPreempted(ctx); err != nil || blocked {
		if err != nil {
			return false, err
		}
		return false, errVerificationPreempted
	}
	walk, ok, err := s.verificationTarget(ctx)
	if err != nil || !ok {
		return false, err
	}
	if walk.rootID == "" || !filepath.IsAbs(walk.root) {
		// Without a catalogued root identity there is nothing a per-file check can
		// be compared against, and this build will not conclude an absence from
		// less. The walk stays pending for a scan to confirm the root.
		return false, errVerificationUnprovable
	}

	// The walk's end is captured once, in the writer, and never re-read: an
	// object inserted while the walk runs belongs to the next walk, so this one
	// has a stable end and cannot chase a moving EOF.
	if !walk.started {
		fence, present, err := s.captureVerificationWindow(ctx, walk)
		if err != nil {
			return false, err
		}
		if !present {
			return false, s.finishVerification(ctx, walk, walk.after)
		}
		walk.fence, walk.started = fence, true
	}

	window, err := s.verificationWindow(ctx, walk)
	if err != nil {
		return false, err
	}
	if len(window) == 0 {
		return false, s.finishVerification(ctx, walk, walk.after)
	}

	// Between quanta a scan or a person can arrive. The memory-only checks are
	// repeated before every folder read and every acceptance; the scan queue is
	// re-read no more than four times a second, which is far more often than a
	// scan can start.
	lastScan, scanBusy := time.Time{}, false
	ioBlocked := func() (bool, error) {
		if !dbwork.Yield(ctx) || dbwork.WriteGate().Admission().UserQueued > 0 {
			return true, nil
		}
		s.mu.Lock()
		working := len(s.active) > 0
		s.mu.Unlock()
		if working {
			return true, nil
		}
		if time.Since(lastScan) < 250*time.Millisecond {
			return scanBusy, nil
		}
		lastScan = time.Now()
		var scans int
		if err := s.db.QueryRowContext(ctx, verificationScanSQL).Scan(&scans); err != nil {
			return true, err
		}
		scanBusy = scans != 0
		return scanBusy, nil
	}

	// Every filesystem fact is gathered outside a writer. Nothing below holds a
	// transaction while it touches a folder.
	proven := make([]bool, len(window))
	for i, row := range window {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if blocked, err := ioBlocked(); err != nil || blocked {
			if err != nil {
				return false, err
			}
			return false, errVerificationPreempted
		}
		if !verificationCandidate(row, walk.incarnation) {
			continue
		}
		absent, err := s.verificationAbsent(ctx, walk, row)
		if err != nil {
			switch {
			case errors.Is(err, storage.ErrBusy), errors.Is(err, context.DeadlineExceeded):
				// The storage layer is at its budget: keep the raw cursor and come
				// back. This is not evidence about any file.
				return false, errVerificationBusy
			case errors.Is(err, storage.ErrInventoryChanged):
				// The folder under this root is not the one the catalogue was built
				// from. No file's absence may be concluded from it.
				return false, errVerificationStale
			case errors.Is(err, storage.ErrRemoteCursor), errors.Is(err, storage.ErrRemoteConfig),
				errors.Is(err, storage.ErrRemoteChanged), errors.Is(err, storage.ErrRemoteOffline),
				errors.Is(err, storage.ErrRemoteCredentials), errors.Is(err, storage.ErrRemoteBinary):
				// A provider could not answer about this source: the ordinary scan of
				// it remains the authority. Keep the cursor, keep it pending, and say
				// so once rather than pretending the walk finished.
				return false, errVerificationUnprovable
			}
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			// Inconclusive: retained for the next walk, never marked missing.
			continue
		}
		proven[i] = absent
	}

	// One proven-absent file per bounded maintenance transaction, so a window
	// that proves many missing files is many small writers, not one large one.
	// Every acceptance also advances the raw cursor, so a proof that has gone
	// stale cannot desynchronise the window from the row.
	cursor := walk.after
	remote := s.storage != nil && s.storage.IsRemote(walk.root)
	for i, row := range window {
		if !proven[i] {
			continue
		}
		if blocked, err := ioBlocked(); err != nil || blocked {
			if err != nil {
				return false, err
			}
			return false, errVerificationPreempted
		}
		if remote && !s.remoteAbsenceSupported() {
			// The proof came from a provider listing; without the same-writer
			// revalidation this build must not write from it.
			return false, errVerificationUnprovable
		}
		if err := s.acceptVerification(ctx, walk, cursor, row); err != nil {
			return false, err
		}
		cursor = verificationCursor(row.id)
	}
	if tail := verificationCursor(window[len(window)-1].id); cursor != tail {
		if err = s.checkpointVerification(ctx, walk, cursor, tail); err != nil {
			return false, err
		}
		cursor = tail
	}
	if window[len(window)-1].id >= walk.fence {
		// The captured end is reached, so this walk is done in the same pass.
		return false, s.finishVerification(ctx, walk, cursor)
	}
	return true, nil
}

// verificationScanSQL reports whether any scan job is queued, running or paused.
// It reads jobs by status first, so it also sees a scan whose claim row has not
// been written yet.
const verificationScanSQL = `SELECT EXISTS(
 SELECT 1 FROM jobs j INDEXED BY jobs_status_directory
 WHERE j.status IN('queued','running','paused')
  AND EXISTS(SELECT 1 FROM inventory_runs r WHERE r.job_id=j.id))`

// verificationPreempted reports whether anything more important than this walk is
// running or waiting. It fails closed: a scheduler state it cannot read means "do
// not verify now".
func (s *Service) verificationPreempted(ctx context.Context) (bool, error) {
	if !dbwork.Yield(ctx) || dbwork.WriteGate().Admission().UserQueued > 0 {
		// An owner pause or a suspended background window.
		return true, nil
	}
	s.mu.Lock()
	working := len(s.active) > 0
	s.mu.Unlock()
	if working {
		return true, nil
	}
	if s.Admission != nil {
		allowed, err := s.Admission(ctx, nil, "analysis")
		if err != nil || !allowed {
			return true, err
		}
	}
	var scans int
	if err := s.db.QueryRowContext(ctx, verificationScanSQL).Scan(&scans); err != nil {
		return true, err
	}
	if scans != 0 {
		return true, nil
	}
	var active int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_source_active)`).Scan(&active); err != nil {
		return true, err
	}
	return active != 0, nil
}

// verificationTarget claims one pending walk whose source may be checked now. The
// source facts are read with the row so a later write can be fenced against them
// without trusting anything it read earlier. A source this build cannot prove a
// file absent on is not claimable, so one such source never holds a healthy one
// behind it; it stays pending and is reported instead.
func (s *Service) verificationTarget(ctx context.Context) (verificationWalk, bool, error) {
	// Read raw pending source identities before rejecting health/config/provider
	// states. A busy remote source cannot hold every later local source behind it.
	s.mu.Lock()
	after, hasAfter := s.verificationAfter, s.verificationHasAfter
	s.mu.Unlock()
	observed := false
	for wrap := 0; wrap < 2; wrap++ {
		query := `SELECT v.source_id,v.incarnation,v.configuration_generation,v.health_epoch,v.started,v.after_object,v.fence_object,
 COALESCE(s.root,''),COALESCE(s.root_identity,''),COALESCE(s.follow_symlinks,0),COALESCE(s.enabled,0),COALESCE(s.identity_confirmed,0),COALESCE(s.health,''),COALESCE(s.incarnation,''),COALESCE(s.generation,0),COALESCE(s.kind,''),
 EXISTS(SELECT 1 FROM inventory_source_active a WHERE a.source_id=v.source_id) OR EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id)
 FROM catalog_source_verification v INDEXED BY catalog_source_verification_pending LEFT JOIN library_sources s ON s.id=v.source_id WHERE v.pending=1`
		args := []any{}
		if hasAfter {
			query += ` AND v.source_id>?`
			args = append(args, after)
		}
		query += ` ORDER BY v.source_id LIMIT 8`
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return verificationWalk{}, false, err
		}
		n, scalarBytes := 0, 0
		for rows.Next() {
			var w verificationWalk
			var enabled, confirmed, follow, started int
			var health, sourceIncarnation, kind string
			var sourceGeneration int64
			var blocked bool
			if err = rows.Scan(&w.source, &w.incarnation, &w.generation, &w.healthEpoch, &started, &w.after, &w.fence, &w.root, &w.rootID, &follow, &enabled, &confirmed, &health, &sourceIncarnation, &sourceGeneration, &kind, &blocked); err != nil {
				rows.Close()
				return w, false, err
			}
			n++
			scalarBytes += len(w.source) + len(w.incarnation) + len(w.after) + len(w.fence) + len(w.root) + len(w.rootID) + len(sourceIncarnation) + len(health) + len(kind)
			observed = true
			after, hasAfter = w.source, true
			s.mu.Lock()
			s.verificationAfter, s.verificationHasAfter = after, true
			s.mu.Unlock()
			w.started, w.follow = started != 0, follow != 0
			provider := kind == "local" || kind == "remote" && s.remoteAbsenceSupported()
			if provider && enabled == 1 && confirmed == 1 && w.rootID != "" && sourceVerifiable(health) && sourceIncarnation == w.incarnation && sourceGeneration == w.generation && !blocked {
				rows.Close()
				return w, true, nil
			}
			if scalarBytes >= sourceVerificationBytes {
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return verificationWalk{}, false, err
		}
		if n > 0 {
			break
		}
		if !hasAfter {
			return verificationWalk{}, false, nil
		}
		after, hasAfter = "", false
		s.mu.Lock()
		s.verificationAfter, s.verificationHasAfter = "", false
		s.mu.Unlock()
	}
	if observed {
		return verificationWalk{}, false, errVerificationPreempted
	}
	return verificationWalk{}, false, nil
}

// captureVerificationWindow records the source's current maximum object id as
// this walk's end, once. The compare-and-set on started means a second claimant
// cannot move the end; it re-reads the row instead. A walk that already captured
// its end never runs this again.
func (s *Service) captureVerificationWindow(ctx context.Context, walk verificationWalk) (string, bool, error) {
	var high sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM inventory_objects WHERE source_id=?`, walk.source).Scan(&high); err != nil {
		return "", false, err
	}
	if !high.Valid {
		return "", false, nil
	}
	err := verificationWrite(ctx, s.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE catalog_source_verification SET started=1,fence_object=?
 WHERE source_id=? AND pending=1 AND health_epoch=? AND started=0 AND after_object=''`, high.String, walk.source, walk.healthEpoch)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errVerificationStale
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return high.String, true, nil
}

// verificationWindow reads one bounded raw window of inventory object ids.
//
// Raw means every row in the range is read, retired or not, and its eligibility
// is decided in Go: the keyset cursor then always advances, and no single
// ineligible object can wedge the walk. Scalar bytes are bounded across the id,
// path, revision and root incarnation of every row, so a source of long names and
// long revisions cannot make one quantum unbounded.
func (s *Service) verificationWindow(ctx context.Context, walk verificationWalk) ([]verificationRow, error) {
	q := `SELECT id,relative_path,revision,state,root_incarnation,unsupported_reason,retired
 FROM inventory_objects WHERE source_id=? AND id<=?`
	args := []any{walk.source, walk.fence}
	if walk.after != "" {
		var after string
		if err := json.Unmarshal([]byte(walk.after), &after); err != nil {
			return nil, err
		}
		q += ` AND id>?`
		args = append(args, after)
	}
	q += ` ORDER BY id LIMIT ?`
	args = append(args, sourceVerificationWindow)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	window := make([]verificationRow, 0, sourceVerificationWindow)
	retained := 0
	for rows.Next() {
		var row verificationRow
		if err = rows.Scan(&row.id, &row.path, &row.revision, &row.state, &row.rootInc, &row.unsupported, &row.retired); err != nil {
			return nil, err
		}
		// At least one row per window, so an over-long row still advances; beyond
		// that the decoded scalar bytes are bounded.
		if len(window) > 0 && retained+verificationRowBytes(row) > sourceVerificationBytes {
			break
		}
		retained += verificationRowBytes(row)
		window = append(window, row)
	}
	return window, rows.Err()
}

func verificationRowBytes(row verificationRow) int {
	return len(row.id) + len(row.path) + len(row.revision) + len(row.rootInc) + len(row.state) + len(row.unsupported)
}

// verificationCandidate says whether this object's absence is worth proving: it
// must be catalogued evidence from the incarnation the source still has.
func verificationCandidate(row verificationRow, incarnation string) bool {
	return row.retired == 0 && row.state == "available" && row.rootInc == incarnation && row.unsupported == "" &&
		row.path != "" && row.path != "." && len(row.path) <= MaxPathBytes && filepath.IsLocal(row.path)
}

// verificationAbsent asks the storage layer whether the exact catalogued file is
// absent. The walk's own name is the key: a provider-side proof is retained and
// cached under it, and the accepting transaction revalidates that same key. A
// local root proves absence against the root's own identity and a readable
// parent, so an unreachable folder is an error and never an absence.
func (s *Service) verificationAbsent(ctx context.Context, walk verificationWalk, row verificationRow) (bool, error) {
	request := storage.InventoryRequest{Root: walk.root, RootIdentity: walk.rootID, RelativePath: row.path, FollowSymlinks: walk.follow}
	if s.storage != nil {
		return s.storage.InventoryAbsence(ctx, verificationJob(walk), request)
	}
	return storage.LocalInventoryAbsence(request)
}

// acceptVerification examines one proven-absent file in its own bounded
// maintenance transaction. Every fact the proof depended on is re-checked in the
// writer, so a proof taken before a flap, a rebind, a rescan or the file's return
// cannot mark anything. The raw cursor advances either way: a stale proof is a
// reason not to write, never a reason to stop moving.
func (s *Service) acceptVerification(ctx context.Context, walk verificationWalk, expectedAfter string, row verificationRow) error {
	ctx = dbwork.WithCatalogIngest(ctx)
	now := time.Now()
	request := storage.InventoryRequest{Root: walk.root, RootIdentity: walk.rootID, RelativePath: row.path, FollowSymlinks: walk.follow}
	return verificationWrite(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.verificationFenceTx(ctx, tx, walk, expectedAfter); err != nil {
			return err
		}
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE id=? AND source_id=? AND revision=? AND retired=0 AND state='available' AND root_incarnation=?)`,
			row.id, walk.source, row.revision, walk.incarnation).Scan(&current); err != nil {
			return err
		}
		if !current {
			// The file's evidence moved under the proof. Nothing is written; the
			// cursor still advances so the window and the row cannot drift apart.
			_, err := tx.ExecContext(ctx, `UPDATE catalog_source_verification SET started=1,after_object=?
 WHERE source_id=? AND health_epoch=? AND pending=1`, verificationCursor(row.id), walk.source, walk.healthEpoch)
			return err
		}
		if err := s.validateAbsenceTx(ctx, tx, walk, request); err != nil {
			return err
		}
		if err := catalog.MarkInventoryMissingTx(ctx, tx, walk.source, verificationJob(walk), row.id, now); err != nil {
			return err
		}
		// The durable evidence of what happened is the inventory_changes row the
		// single-file writer just made, named for this walk. A walk that found
		// thousands gone must not also become thousands of log lines.
		_, err := tx.ExecContext(ctx, `UPDATE catalog_source_verification SET started=1,after_object=?
 WHERE source_id=? AND health_epoch=? AND pending=1`, verificationCursor(row.id), walk.source, walk.healthEpoch)
		return err
	})
}

// checkpointVerification advances the cursor over a window that needed no writes,
// or over the tail of one that did.
func (s *Service) checkpointVerification(ctx context.Context, walk verificationWalk, expectedAfter, after string) error {
	return verificationWrite(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.verificationFenceTx(ctx, tx, walk, expectedAfter); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE catalog_source_verification SET started=1,after_object=?
 WHERE source_id=? AND health_epoch=? AND pending=1`, after, walk.source, walk.healthEpoch)
		return err
	})
}

// verificationFenceTx is the acceptance fence shared by a missing write and a
// bare checkpoint. It fails with a control outcome, not an error the walk would
// retry: the caller leaves the row exactly as it found it.
func (s *Service) verificationFenceTx(ctx context.Context, tx *sql.Tx, walk verificationWalk, expectedAfter string) error {
	var epoch int64
	var started int
	var fence string
	err := tx.QueryRowContext(ctx, `SELECT health_epoch,started,fence_object FROM catalog_source_verification
 WHERE source_id=? AND pending=1 AND incarnation=? AND configuration_generation=? AND after_object=?`,
		walk.source, walk.incarnation, walk.generation, expectedAfter).Scan(&epoch, &started, &fence)
	if errors.Is(err, sql.ErrNoRows) {
		return errVerificationStale
	}
	if err != nil {
		return err
	}
	if epoch != walk.healthEpoch || started != 1 || fence != walk.fence {
		return errVerificationStale
	}
	var enabled, confirmed, follow int
	var health, incarnation, root, rootID string
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT enabled,identity_confirmed,follow_symlinks,health,incarnation,generation,root,root_identity FROM library_sources WHERE id=?`, walk.source).
		Scan(&enabled, &confirmed, &follow, &health, &incarnation, &generation, &root, &rootID)
	if errors.Is(err, sql.ErrNoRows) {
		return errVerificationStale
	}
	if err != nil {
		return err
	}
	if enabled != 1 || confirmed != 1 || !sourceVerifiable(health) ||
		incarnation != walk.incarnation || generation != walk.generation ||
		root != walk.root || rootID != walk.rootID || (follow != 0) != walk.follow {
		return errVerificationStale
	}
	var scanning int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_source_active WHERE source_id=?)`, walk.source).Scan(&scanning); err != nil {
		return err
	}
	if scanning != 0 {
		return errVerificationPreempted
	}
	return nil
}

// finishVerification closes a walk: the pending flag and the window fields go, the
// health epoch stays. The row stays small and inert so a proof that arrives after
// this walk ended can never be mistaken for one taken under a later reconnect,
// whose epoch it does not have. expectedAfter must be the cursor the caller last
// wrote, so a walk that moved underneath it is not closed by mistake.
func (s *Service) finishVerification(ctx context.Context, walk verificationWalk, expectedAfter string) error {
	return verificationWrite(ctx, s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_source_verification SET pending=0,started=0,after_object='',fence_object=''
 WHERE source_id=? AND health_epoch=? AND pending=1 AND after_object=?`, walk.source, walk.healthEpoch, expectedAfter)
		return err
	})
}

// verificationJob names the walk in the evidence it writes. inventory_changes
// keeps the reason each file went missing, and a stable per-walk name stops one
// file being counted twice by the same walk.
func verificationJob(walk verificationWalk) string {
	return "source-verify:" + walk.source + ":" + strconv.FormatInt(walk.healthEpoch, 10)
}

// A JSON string keeps the initial empty cursor distinct from a consumed empty ID.
// It is one bounded continuation scalar, not an inventory result.
func verificationCursor(id string) string {
	value, _ := json.Marshal(id)
	return string(value)
}

// Verification has no latency-sensitive consumer. Spend at least six times a
// completed writer's elapsed time outside the gate before requesting it again:
// this caps sustained writer duty at 1/7 (about 14%), including missing marks.
// Queue wait is counted conservatively. No database/read lease survives the wait.
func verificationWrite(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	started := time.Now()
	err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, fn)
	if err != nil {
		return err
	}
	timer := time.NewTimer(6 * time.Since(started))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
