package ingestion

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// One analysis-enqueue page holds the single SQLite writer for its whole
// duration. The page is a bounded raw window over the source's existing
// (source_id, retired, id) index, so a run of rows another phase ruled out
// cannot hide unbounded work behind a LIMIT. A policy that configures sonic
// keeps the smaller window: any row of such a page can run SonicCurrentTx,
// whose inner binding read is already bounded at 4097 track rows, and that
// inner work must not be multiplied by a larger outer page. The selection uses
// the policy alone: the runtime's availability is checked per row, because it
// can change while a page is being built.
const (
	analysisEnqueueRawPageSize   = 64
	analysisEnqueueSonicPageSize = 32
)

func (s *Service) enqueueAnalysisPage(ctx context.Context, job string, source catalog.LibrarySource) error {
	if more, e := s.enqueueSidecarChanges(ctx, job, source); more || e != nil {
		return e
	}
	answers, err := s.sonicPrepass(ctx, job, source)
	if err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, err = catalog.InventoryFenceTx(ctx, tx, job); err != nil {
		return err
	}
	policy, err := catalog.ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	if policy.Tier == "file_list_only" {
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_analysis_queue SET state='skipped' WHERE job_id=? AND state='pending'`, job); err != nil {
			return err
		}
		if err := finishScanTx(ctx, tx, job, source.ID); err != nil {
			return err
		}
		return gated.Commit()
	}
	w, err := s.analysisWindowTx(ctx, tx, job, source, policy)
	if err != nil {
		return err
	}
	usable := answers.usable(w.cursor, policy.Revision, s.DeepAnalysis.SonicRuntimeToken())
	for _, o := range w.batch {
		// Availability is re-read for every row: the runtime can be swapped by a
		// concurrent RetrySonic while this page holds its window open.
		if !o.needs && policy.Allows(catalog.SonicOperation) && s.DeepAnalysis.SonicCanAttempt() {
			if usable {
				// The pre-pass answered for this window under this runtime. A row
				// it did not answer for changed in between: queue it, which costs
				// at most one quantum that finds it complete.
				current, answered := answers.current[o.id+"\x00"+o.revision]
				o.needs = !answered || !current
			} else {
				current, e := s.DeepAnalysis.SonicCurrentTx(ctx, tx, mediaanalysis.Request{JobID: job, ObjectID: o.id, SourceRevision: o.revision, PolicyRevision: policy.Revision})
				if e != nil {
					return e
				}
				o.needs = !current
			}
		}
		if !o.needs {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) VALUES(?,?,?,?,'pending') ON CONFLICT(job_id,object_id) DO UPDATE SET revision=excluded.revision,policy_revision=excluded.policy_revision,state=excluded.state`, job, o.id, o.revision, policy.Revision); err != nil {
			return err
		}
	}
	// The cursor is the last raw id of the window, not the last row the predicate
	// selected: filtered rows must be consumed too. Queue rows, cursor and phase
	// move in this one commit, so a crash replays at most this window.
	if w.atEnd {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET phase='basic_pending',reconcile_cursor='',policy_revision=? WHERE job_id=?`, policy.Revision, job)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET reconcile_cursor=?,policy_revision=? WHERE job_id=?`, w.last, policy.Revision, job)
	}
	if err != nil {
		return err
	}
	return gated.Commit()
}

// analysisCandidate is one raw row of an enqueue window; needs is the
// predicate's answer before the sonic currentness check.
type analysisCandidate struct {
	id, revision string
	needs        bool
}

// analysisWindow is one enqueue page's plan: the raw window after cursor, the
// predicate's answer for each eligible row, and whether the source ends here.
type analysisWindow struct {
	cursor, last string
	batch        []analysisCandidate
	atEnd        bool
}

// analysisWindowTx plans one enqueue page. It only reads, so the sonic pre-pass
// can plan the same window in a read snapshot that the writer plans again.
func (s *Service) analysisWindowTx(ctx context.Context, tx *sql.Tx, job string, source catalog.LibrarySource, policy catalog.ScanPolicy) (analysisWindow, error) {
	var w analysisWindow
	var strmEnabled bool
	var strmRevision int64
	err := tx.QueryRowContext(ctx, `SELECT enabled,revision FROM source_strm_analysis WHERE library_id=?`, source.LibraryID).Scan(&strmEnabled, &strmRevision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return w, err
	}
	required := append([]string(nil), policy.Operations...)
	if !s.DeepAnalysis.SonicCanAttempt() {
		kept := required[:0]
		for _, op := range required {
			if op != catalog.SonicOperation {
				kept = append(kept, op)
			}
		}
		required = kept
	}
	if policy.Allows("probe") {
		required = append(required, assets.PlaybackAnalysisRevision)
	}
	// A remote source's own page can switch whole-file analysis stages off for
	// the files found through it. Local folders keep the library's policy.
	if switches, ok, e := remotesources.AnalysisSwitchesTx(ctx, tx, source.ID); e != nil {
		return w, e
	} else if ok {
		required = switches.Filter(required)
	}
	requiredJSON, _ := json.Marshal(required)
	// The configured policy decides the window, not the runtime's current
	// availability. RetrySonic commits its refresh and releases the writer
	// before it swaps the runtime (sonic_retry.go), so a page can start while
	// an unavailable runtime is still installed and see the new one mid-page;
	// caching availability here would skip SonicCurrentTx for the rest of the
	// page. The check below stays per row, exactly as the 32-row page had it.
	rawPageSize := analysisEnqueueRawPageSize
	if policy.Allows(catalog.SonicOperation) {
		rawPageSize = analysisEnqueueSonicPageSize
	}
	var cursor string
	if err = tx.QueryRowContext(ctx, `SELECT reconcile_cursor FROM inventory_runs WHERE job_id=?`, job).Scan(&cursor); err != nil {
		return w, err
	}
	// Read the raw keyset window first, bounded by the existing
	// (source_id, retired, id) index. The extra row is a probe: if it comes back
	// there is more source after this window, so the phase is not over and the
	// probe is re-read as the next window's first row. Rows the filters below
	// reject are still consumed here, so a rejected prefix cannot repeat forever.
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.revision FROM inventory_objects o INDEXED BY inventory_objects_source WHERE o.source_id=? AND o.retired=0 AND o.id>? ORDER BY o.id LIMIT ?`, source.ID, cursor, rawPageSize+1)
	if err != nil {
		return w, err
	}
	window := []analysisCandidate{}
	for rows.Next() {
		var o analysisCandidate
		if err = rows.Scan(&o.id, &o.revision); err != nil {
			rows.Close()
			return w, err
		}
		window = append(window, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return w, err
	}
	atEnd := len(window) <= rawPageSize
	if !atEnd {
		window = window[:rawPageSize]
	}
	// Bound evidence evaluation even when the entire source is already complete.
	// The unchanged predicate is evaluated in one statement over exactly this
	// window's ids, not one query per row. Unchanged candidates advance the
	// cursor without creating queue rows.
	batch := []analysisCandidate{}
	if len(window) > 0 {
		ids := make([]string, len(window))
		for i := range window {
			ids[i] = window[i].id
		}
		idsJSON, _ := json.Marshal(ids)
		rows, err = tx.QueryContext(ctx, `WITH batch AS MATERIALIZED (
 SELECT o.* FROM inventory_objects o JOIN inventory_runs r ON r.source_id=o.source_id
 WHERE r.job_id=? AND o.id IN (SELECT value FROM json_each(?)) AND o.retired=0 AND o.state='available' AND o.root_incarnation=r.root_incarnation AND o.unsupported_reason=''
 AND (r.reported<>? OR (EXISTS(SELECT 1 FROM inventory_run_targets WHERE job_id=r.job_id) AND `+targetScopeSQL+`))
 ORDER BY o.id)
 SELECT o.id,o.revision,(lower(substr(o.relative_path,-5)) NOT IN('.epub','.mobi','.azw3') AND lower(substr(o.relative_path,-4)) NOT IN('.pdf','.cbz','.cb7','.cbr') AND (lower(substr(o.relative_path,-5))<>'.strm' OR ?=1) AND (o.analysis_revision<>o.revision OR o.analysis_policy<>? OR o.analysis_state='pending' OR o.analysis_generation<>r.source_generation OR (o.analysis_algorithm<>? AND EXISTS(SELECT 1 FROM json_each((SELECT operations_json FROM library_scan_policies WHERE library_id=?)) WHERE value IN('checksum','chapter_images','trickplay','waveform','loudness','fingerprint'))) OR
 (lower(substr(o.relative_path,-5))<>'.strm' AND EXISTS(SELECT 1 FROM json_each(?) wanted WHERE NOT EXISTS(SELECT 1 FROM json_each(o.analysis_operations_json) done WHERE done.value=wanted.value))) OR
 EXISTS(SELECT 1 FROM json_each(?) wanted WHERE wanted.value IN('checksum','chapter_images','trickplay','waveform','loudness','fingerprint') AND NOT EXISTS(SELECT 1 FROM analysis_heads h JOIN analysis_results a ON a.id=h.result_id WHERE h.object_id=o.id AND h.source_revision=o.revision AND h.stage=wanted.value AND a.algorithm=? AND a.root_incarnation=r.root_incarnation AND a.configuration_generation=r.source_generation AND a.retired_ms=0)) OR
 (lower(substr(o.relative_path,-5))='.strm' AND NOT EXISTS(SELECT 1 FROM json_each(o.analysis_operations_json) done WHERE done.value=?))))
 FROM batch o JOIN inventory_runs r ON r.source_id=o.source_id WHERE r.job_id=? ORDER BY o.id`, job, string(idsJSON), strictReportMode, string(filepath.Separator), strmEnabled && policy.Allows("probe"), policy.Revision, mediaanalysis.Algorithm, source.LibraryID, string(requiredJSON), string(requiredJSON), mediaanalysis.Algorithm, "strm_target:"+strconv.FormatInt(strmRevision, 10), job)
		if err != nil {
			return w, err
		}
		for rows.Next() {
			var o analysisCandidate
			if err = rows.Scan(&o.id, &o.revision, &o.needs); err != nil {
				rows.Close()
				return w, err
			}
			batch = append(batch, o)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return w, err
		}
	}
	w.cursor, w.batch, w.atEnd = cursor, batch, atEnd
	if len(window) > 0 {
		w.last = window[len(window)-1].id
	}
	return w, nil
}

// sonicAnswers are the pre-pass's sonic currentness answers for one window,
// keyed by object id and revision, with what they were taken under.
type sonicAnswers struct {
	taken          bool
	cursor         string
	policyRevision int64
	runtime        string
	current        map[string]bool
}

// usable says whether the answers were taken for this window (the same cursor
// and policy revision) under the runtime installed now.
func (a sonicAnswers) usable(cursor string, policyRevision int64, runtime string) bool {
	return a.taken && a.cursor == cursor && a.policyRevision == policyRevision && a.runtime == runtime
}

// sonicPrepass answers SonicCurrentTx for a sonic page's settled rows in a
// read-only snapshot, before the page takes the writer. Each answer reads the
// object's binding, up to 4097 track rows, so 32 of them inside the writer
// held it for the whole binding read (ENQUEUE1 R2). The writer re-plans the
// window and uses an answer only for the window, policy and runtime it was
// taken under, so its decisions are those a page run at the snapshot would
// have made; a change after the snapshot is seen as one after the page would
// have been.
func (s *Service) sonicPrepass(ctx context.Context, job string, source catalog.LibrarySource) (sonicAnswers, error) {
	var a sonicAnswers
	if !s.DeepAnalysis.SonicCanAttempt() {
		return a, nil
	}
	runtime := s.DeepAnalysis.SonicRuntimeToken()
	tx, done, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return a, err
	}
	defer done()
	if _, err = catalog.InventoryFenceTx(ctx, tx, job); err != nil {
		// The writer re-proves the fence and answers for it.
		return a, nil
	}
	policy, err := catalog.ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return a, err
	}
	if policy.Tier == "file_list_only" || !policy.Allows(catalog.SonicOperation) {
		return a, nil
	}
	w, err := s.analysisWindowTx(ctx, tx, job, source, policy)
	if err != nil {
		return a, err
	}
	a = sonicAnswers{taken: true, cursor: w.cursor, policyRevision: policy.Revision, runtime: runtime, current: map[string]bool{}}
	for _, o := range w.batch {
		if o.needs {
			continue
		}
		current, err := s.DeepAnalysis.SonicCurrentTx(ctx, tx, mediaanalysis.Request{JobID: job, ObjectID: o.id, SourceRevision: o.revision, PolicyRevision: policy.Revision})
		if err != nil {
			return sonicAnswers{}, err
		}
		a.current[o.id+"\x00"+o.revision] = current
	}
	return a, nil
}
func (s *Service) playbackBusy(ctx context.Context, source string) (bool, error) {
	var busy bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions p JOIN inventory_objects o ON o.asset_id=p.asset_id WHERE o.source_id=? AND p.state IN('playing','buffering') AND p.expires_at>?)`, source, time.Now().UTC().Format(time.RFC3339)).Scan(&busy)
	return busy, err
}
func (s *Service) analysisAllowed(ctx context.Context, job, library string, revision int64, op string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, job).Scan(&status); err != nil {
		return err
	}
	if status != "running" {
		return context.Canceled
	}
	policy, err := s.catalog.ScanPolicy(ctx, library)
	if err != nil {
		return err
	}
	if policy.Revision != revision || !policy.Allows(op) {
		return context.Canceled
	}
	return nil
}
func (s *Service) analyzeOne(parent context.Context, job string, source catalog.LibrarySource) error {
	policy, err := s.catalog.ScanPolicy(parent, source.LibraryID)
	if err != nil {
		return err
	}
	var object, revision, relative, analyzed, operations, analysisState string
	var queuedPolicy, analysisPolicy int64
	// An object whose stage waits for a retry is passed over until it is due,
	// so one delayed file cannot hold every later one behind it.
	now := time.Now().UnixMilli()
	err = s.db.QueryRowContext(parent, `SELECT q.object_id,q.revision,q.policy_revision,o.relative_path,o.analysis_revision,o.analysis_operations_json,o.analysis_policy,o.analysis_state FROM inventory_analysis_queue q JOIN inventory_objects o ON o.id=q.object_id WHERE q.job_id=? AND q.state='pending' AND NOT EXISTS(SELECT 1 FROM catalog_assets ia JOIN catalog_info_changes ic ON ic.asset_id=ia.id AND ic.asset_token=ia.token WHERE ia.token=o.asset_id AND ic.phase BETWEEN 1 AND 3) AND NOT EXISTS(SELECT 1 FROM analysis_stage_runs w WHERE w.job_id=q.job_id AND w.object_id=q.object_id AND w.source_revision=q.revision AND w.policy_revision=q.policy_revision AND w.state NOT IN('failed','unsupported','complete') AND w.next_ms>?) ORDER BY q.object_id LIMIT 1`, job, now).Scan(&object, &revision, &queuedPolicy, &relative, &analyzed, &operations, &analysisPolicy, &analysisState)
	if errors.Is(err, sql.ErrNoRows) {
		// Everything left is waiting for a retry: no progress this turn, and the
		// run is not over.
		var waiting bool
		if err = s.db.QueryRowContext(parent, `SELECT EXISTS(SELECT 1 FROM inventory_analysis_queue WHERE job_id=? AND state='pending')`, job).Scan(&waiting); err != nil || waiting {
			return err
		}
		gated2, e := dbwork.Begin(parent, s.db, dbwork.ClassBackgroundMedia)
		if e != nil {
			return e
		}
		tx := gated2.Tx()
		defer gated2.Rollback()
		if _, e = catalog.InventoryFenceTx(parent, tx, job); e != nil {
			return e
		}
		if e := finishScanTx(parent, tx, job, source.ID); e != nil {
			return e
		}
		return gated2.Commit()
	}
	if err != nil {
		return err
	}
	if queuedPolicy != policy.Revision || policy.Tier == "file_list_only" {
		return s.phase(parent, job, "analysis_enqueue", true)
	}
	busy, err := s.playbackBusy(parent, source.ID)
	if err != nil {
		return err
	}
	if busy {
		if !waitScanPace(parent, 250*time.Millisecond) {
			return parent.Err()
		}
	}
	if _, err = dbwork.ExecWrite(parent, s.db, dbwork.ClassBackgroundMedia, `UPDATE inventory_runs SET pause_reason='',phase='basic_running' WHERE job_id=?`, job); err != nil {
		return err
	}
	// Completed basic stages are retained when Complete adds downstream stages.
	basicNeeded := analyzed != revision
	var done []string
	_ = json.Unmarshal([]byte(operations), &done)
	for _, op := range catalog.BasicScanOperations {
		if policy.Allows(op) {
			found := false
			for _, v := range done {
				found = found || v == op
			}
			basicNeeded = basicNeeded || !found
		}
	}
	descriptor := strings.EqualFold(filepath.Ext(relative), ".strm")
	if !descriptor && policy.Allows("probe") {
		found := false
		for _, op := range done {
			found = found || op == assets.PlaybackAnalysisRevision
		}
		basicNeeded = basicNeeded || !found
	}
	var strmRevision int64
	if descriptor {
		var enabled bool
		err = s.db.QueryRowContext(parent, `SELECT enabled,revision FROM source_strm_analysis WHERE library_id=?`, source.LibraryID).Scan(&enabled, &strmRevision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !enabled {
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, false, "")
		}
		basicNeeded = analyzed != revision
		found := false
		for _, op := range done {
			found = found || op == "strm_target:"+strconv.FormatInt(strmRevision, 10)
		}
		basicNeeded = basicNeeded || !found
	}
	var discRequested bool
	if err = s.db.QueryRowContext(parent, `SELECT EXISTS(SELECT 1 FROM inventory_disc_title_requests WHERE job_id=? AND object_id=? AND source_revision=?)`, job, object, revision).Scan(&discRequested); err != nil {
		return err
	}
	basicNeeded = basicNeeded || discRequested
	deep := false
	for _, op := range catalog.DeepScanOperations {
		deep = deep || policy.Allows(op)
	}
	// Root64: a remote disc image is never probed, deep-analysed or read for a
	// scan, whatever historical operation tokens the row carries and whether or
	// not its basic bookkeeping looks complete. Naming is the only analysis it
	// gets, and this revision's naming being recorded is what completes it.
	// The source's own kind is the authority: a stale or missing storage registry
	// must never let a remote image fall into the local disc probe.
	remoteDisc := source.Kind == "remote" && assets.DiscImage(relative)
	if remoteDisc && analyzed == revision && analysisPolicy == queuedPolicy && analysisState == "deferred" {
		// Naming is already recorded for this exact revision and policy. Settle
		// this queue invocation through the ordinary outcome path, which reproves
		// the fence and policy revision, and reads nothing from the source.
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, false, "")
	}
	if !remoteDisc && (!policy.Allows("probe") || !basicNeeded) {
		if deep {
			return s.analyzeDeep(parent, job, object, revision, policy, source)
		}
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, false, "")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// Policy, pause/cancel and foreground activity preempt a running bounded read;
	// it stays pending and does not consume a failure attempt.
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	supervise.Go("ingestion.probe-watch", func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-watcherDone:
				return
			case <-ticker.C:
				if remoteDisc {
					// Naming has no probe prerequisite. Its sidecar reads separately
					// reprove local_metadata; retain the original running job/policy.
					var status string
					var currentPolicy int64
					e := s.db.QueryRowContext(ctx, `SELECT j.status,p.revision FROM jobs j JOIN library_scan_policies p ON p.library_id=j.library_id WHERE j.id=? AND j.library_id=?`, job, source.LibraryID).Scan(&status, &currentPolicy)
					if e != nil || status != "running" || currentPolicy != policy.Revision {
						cancel()
						return
					}
				} else if e := s.analysisAllowed(ctx, job, source.LibraryID, policy.Revision, "probe"); e != nil {
					cancel()
					return
				}

			}
		}
	})
	path, err := relativeObjectPath(source, relative)
	if err != nil {
		return err
	}
	stat := func() (storage.Snapshot, error) {
		r := storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, RelativePath: relative, FollowSymlinks: source.FollowSymlinks}
		// A folder disc's object path is a directory, which the regular-file
		// observation refuses; the directory observation is its exact stat.
		if s.storage != nil {
			if v, err := s.storage.InventoryStat(ctx, source.ID, r); err == nil {
				return v, nil
			}
			return s.storage.InventoryDirectory(ctx, source.ID, r)
		}
		if v, err := storage.LocalInventoryStat(r); err == nil {
			return v, nil
		}
		return storage.LocalInventoryDirectory(r)
	}
	// A folder disc carries its title bytes below the observed folder, so the
	// queued revision includes the last committed structure witness. The same
	// token is applied to every stat of this object while it is analysed.
	discToken := ""
	matches := func(v storage.Snapshot) bool {
		if v.Directory {
			v.ChangeToken = discToken
		}
		r, _, e2 := catalog.ObservationRevision(source, v)
		return e2 == nil && r == revision
	}
	clearActivityFile := s.activityFile(ctx, job, relative)
	defer clearActivityFile()
	before, err := stat()
	if err == nil && before.Directory {
		var fingerprint string
		if e := s.db.QueryRowContext(ctx, `SELECT COALESCE(fingerprint,'') FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&fingerprint); e == nil {
			discToken = catalog.DiscStructureWitness(fingerprint)
		}
	}
	if err != nil || !matches(before) {
		if ctx.Err() != nil {
			return nil
		}
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "source_revision_changed")
	}
	probe := s.probe
	probe.ReadGuard = func(check context.Context, _ string) error {
		return s.analysisAllowed(check, job, source.LibraryID, policy.Revision, "probe")
	}
	// One directory recheck is shared by every projection for this file, so a
	// folder is observed once per analysis instead of once per consumer. The
	// projections stay separate: each still answers only its own question, and
	// none of them is an I/O grant.
	listed, artworkListed, musicListed := s.analysisListings(ctx, job, source)
	artworkObserved := true
	readContext := func(operation string) context.Context {
		// The original media target is the invocation, not the observation of a
		// separately inventoried NFO, cover or subtitle candidate read below.
		admission := storage.InventoryBlobAdmission{Purpose: storage.InventoryBlobAnalysisSidecar,
			JobID: job, LibraryID: source.LibraryID, LibrarySourceID: source.ID,
			RootIncarnation: source.Incarnation, RootIdentity: source.RootIdentity, SourceGeneration: source.Generation,
			PolicyRevision: policy.Revision, ObjectID: object, SourceRevision: revision, Operation: operation}
		rooted := storage.WithScanInput(ctx, s.storage, source.ID, storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, FollowSymlinks: source.FollowSymlinks}, admission)
		// What this scan saw when it listed the directory spares a helper
		// process for every sidecar that is not there.
		rooted = localmetadata.WithListing(rooted, listed)
		rooted = localmetadata.WithMusicTrackListing(rooted, musicListed)
		rooted = localmetadata.WithArtworkListing(rooted, artworkListed)
		rooted = localmetadata.WithArtworkReadCheck(rooted, func() { artworkObserved = false })
		// One observation answers for both the admission check and the snapshot
		// it already took. Storage derives the admission guard from this same
		// closure, so it cannot disagree with the observation; the observation
		// returns the request it saw so a caller can only use it for the
		// identical candidate.
		observe := func(check context.Context, candidate string) (storage.ScanReadWitness, error) {
			if err := s.analysisAllowed(check, job, source.LibraryID, policy.Revision, operation); err != nil {
				return storage.ScanReadWitness{}, err
			}
			rel, err := filepath.Rel(source.ResolvedPath, candidate)
			if err != nil || !filepath.IsLocal(rel) {
				return storage.ScanReadWitness{}, catalog.ErrAdminQuery
			}
			r := storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, RelativePath: rel, FollowSymlinks: source.FollowSymlinks}
			var current storage.Snapshot
			if s.storage != nil {
				current, err = s.storage.InventoryStat(check, source.ID, r)
			} else {
				current, err = storage.LocalInventoryStat(r)
			}
			if err != nil {
				return storage.ScanReadWitness{}, err
			}
			return storage.ScanReadWitness{SourceID: source.ID, Root: r.Root, RootIdentity: r.RootIdentity,
				RelativePath: rel, FollowSymlinks: r.FollowSymlinks, Snapshot: current}, nil
		}
		return storage.WithScanReadWitness(rooted, s.storage, observe)
	}
	if !remoteDisc && !descriptor && !discRequested && !before.Directory && basicNeeded {
		evidence, e := s.localRefreshEvidence(ctx, source, object, revision, relative, analyzed, done, policy)
		if e != nil {
			return e
		}
		if evidence != nil {
			return s.refreshLocalMetadata(parent, ctx, job, object, revision, queuedPolicy, policy, source, path, *evidence, readContext, &artworkObserved, stat, matches, before, deep)
		}
	}
	if remoteDisc {
		// Root64: a remote disc image is an ordinary title. A scan never reads
		// the image, so publish its name and local metadata only and leave
		// technical facts deferred; no probe, ranged read or disc read runs here.
		f := assets.Facts{}
		completed := []string{}
		if policy.Allows("local_metadata") && s.LocalMetadata != nil {
			if e := s.analysisAllowed(ctx, job, source.LibraryID, policy.Revision, "local_metadata"); e == nil {
				var kind string
				if e = s.db.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&kind); e == nil {
					f = s.LocalMetadata.VideoNFO(readContext("local_metadata"), source.LibraryID, source.ResolvedPath, path, kind, f)
					if kind == "movie" {
						f = s.LocalMetadata.MovieArtwork(readContext("local_metadata"), source.LibraryID, path, f)
					}
					season := -1
					if kind == "tv" || kind == "anime" {
						if rel, rerr := filepath.Rel(source.ResolvedPath, path); rerr == nil {
							if plan := catalog.ParseEpisode(rel, kind); plan.Issue == "" && plan.Season >= 0 && plan.Season <= 9999 {
								season = plan.Season
							}
						}
					}
					extra := s.LocalMetadata.ReadSidecars(readContext("local_metadata"), source.LibraryID, kind, source.ResolvedPath, path, season)
					if key := extra["album/cover"]; key != "" && f.ArtworkKey == "" {
						f.ArtworkKey = key
						delete(extra, "album/cover")
					}
					if len(extra) > 0 {
						f.ArtworkExtra = extra
					}
					f.ArtworkCandidates, f.ArtworkCandidatesObserved = s.LocalMetadata.ReadArtworkCandidates(readContext("local_metadata"), source.LibraryID, kind, source.ResolvedPath, path, season)
					f.ArtworkObserved = f.LocalMetadataIssue == ""
					completed = append(completed, "local_metadata")
				}
			}
		}
		after, e := stat()
		if e != nil || !matches(after) {
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "source_revision_changed")
		}
		f.ObservedSize, f.ObservedModifiedNS, f.ObservedRevision = before.Size, before.ModifiedNS, before.Revision
		f.AnalysisOperations = completed
		if err = s.catalog.CommitInventoryNaming(ctx, job, object, revision, policy.Revision, f); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "analysis_publication_failed")
		}
		return nil
	}
	var f assets.Facts
	if err = s.analysisAllowed(ctx, job, source.LibraryID, policy.Revision, "probe"); err != nil {
		return nil
	}
	if descriptor {
		if s.AnalyzeSTRM == nil {
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "strm_analysis_unavailable")
		}
		f, err = s.AnalyzeSTRM(ctx, source.LibraryID, path)
		s.recordDescriptorAnalysis(ctx, path, err)
	} else if s.storage.IsRemote(path) {
		f, err = s.inspectRemote(ctx, path, before)
	} else if before.Directory {
		if !storage.DirectoryDiscScanSupported() {
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "unsupported_disc_layout")
		}
		f, err = s.inspectDisc(ctx, job, object, revision, policy.Revision, source, relative, readContext("probe"), probe, path, true)
		if errors.Is(err, disc.ErrEncrypted) {
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "unsupported_disc_image")
		}
	} else if assets.DiscImage(path) {
		f, err = s.inspectDisc(ctx, job, object, revision, policy.Revision, source, relative, readContext("probe"), probe, path, false)
		if errors.Is(err, disc.ErrEncrypted) {
			// Portico ships no disc decryption (VIDEO §3 VS-4). The image is
			// catalogued as unsupported rather than probed as a broken file.
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "unsupported_disc_image")
		}
	} else {
		f, err = probe.InspectScan(readContext("probe"), path)
		if err == nil {
			probe.AnalyzePlayback(readContext("probe"), path, &f)
		}
	}
	if err == nil && f.DiscAlternate {
		after, e := stat()
		if e != nil || !matches(after) {
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "source_revision_changed")
		}
		receipt, e := s.catalog.AcceptDiscAlternate(ctx, job, object, revision, policy.Revision, f)
		if e != nil {
			if infoPublicationDeferred(e) {
				return nil
			}
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "probe_or_sidecar_failed")
		}
		if receipt.State != compactcatalog.InfoInputApplied {
			return nil
		}
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, false, "")
	}
	if err == nil && f.InventoryOnly {
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "remote_probe_unavailable")
	}
	completed := []string{"probe"}
	if !descriptor {
		completed = append(completed, assets.PlaybackAnalysisRevision)
	}
	if descriptor {
		completed = append(completed, "strm_target:"+strconv.FormatInt(strmRevision, 10))
	}
	if err == nil && !descriptor && policy.Allows("local_metadata") && s.LocalMetadata != nil {
		if err = s.analysisAllowed(ctx, job, source.LibraryID, policy.Revision, "local_metadata"); err == nil {
			var kind string
			err = s.db.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&kind)
			if err == nil {
				if kind == "music" || kind == "audiobook" {
					f = s.LocalMetadata.PrepareKind(readContext("local_metadata"), source.LibraryID, kind, source.ResolvedPath, path, f)
				} else if kind == "movie" || kind == "tv" || kind == "anime" {
					f = s.LocalMetadata.VideoNFO(readContext("local_metadata"), source.LibraryID, source.ResolvedPath, path, kind, f)
					if kind == "movie" {
						f = s.LocalMetadata.MovieArtwork(readContext("local_metadata"), source.LibraryID, path, f)
					}
				}
				// Sidecar images for every agent (Spec — Page Content §0.5):
				// show and season art, artist portraits, movie logos and
				// parent-folder album covers. Keys travel in Facts; the
				// catalog commit writes them as local_artwork rows.
				if kind == "movie" || kind == "tv" || kind == "anime" || kind == "music" {
					season := -1
					if kind == "tv" || kind == "anime" {
						if rel, rerr := filepath.Rel(source.ResolvedPath, path); rerr == nil {
							if plan := catalog.ParseEpisode(rel, kind); plan.Issue == "" && plan.Season >= 0 && plan.Season <= 9999 {
								season = plan.Season
							}
						}
					}
					extra := s.LocalMetadata.ReadSidecars(readContext("local_metadata"), source.LibraryID, kind, source.ResolvedPath, path, season)
					if key := extra["album/cover"]; key != "" && f.ArtworkKey == "" {
						f.ArtworkKey = key
						delete(extra, "album/cover")
					}
					if len(extra) > 0 {
						f.ArtworkExtra = extra
					}
					if kind == "movie" || kind == "tv" || kind == "anime" {
						f.ArtworkCandidates, f.ArtworkCandidatesObserved = s.LocalMetadata.ReadArtworkCandidates(readContext("local_metadata"), source.LibraryID, kind, source.ResolvedPath, path, season)
					}
				}
			}
		}
	}
	if err == nil && !descriptor && policy.Allows("local_metadata") && s.LocalMetadata != nil && f.LocalMetadataIssue == "" {
		completed = append(completed, "local_metadata")
	}
	if err == nil && !descriptor && policy.Allows("subtitles") && f.VideoCodec != "" {
		if err = s.analysisAllowed(ctx, job, source.LibraryID, policy.Revision, "subtitles"); err == nil {
			tracks := []subtitles.Track{}
			for _, st := range f.Streams {
				if st.Type != "subtitle" {
					continue
				}
				format, reason := subtitles.CodecFormat(st.Codec), ""
				if format == "" {
					format = st.Codec
					reason = "unsupported_format"
				}
				tracks = append(tracks, subtitles.Track{Origin: "embedded", StreamIndex: st.Index, Format: format, Language: st.Language, Title: st.Title, Default: st.Default, Forced: st.Forced, HearingImpaired: st.HearingImpaired, Reason: reason})
			}
			relative, _ := filepath.Rel(source.ResolvedPath, filepath.Dir(path))
			f.SubtitleInventory, err = subtitles.DiscoverManaged(readContext("subtitles"), s.db, s.storage, source.ID, source.Incarnation, job, relative, path, tracks, f.OriginUS, f.TimingKnown)
		}
	}
	if err == nil && !descriptor && policy.Allows("subtitles") && (f.VideoCodec == "" || f.SubtitleInventory != nil && f.SubtitleInventory.Status == "known") {
		completed = append(completed, "subtitles")
	}
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		if infoPublicationDeferred(err) {
			return nil
		}
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "probe_or_sidecar_failed")
	}
	after, err := stat()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil || !matches(after) {
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "source_revision_changed")
	}
	f.ObservedSize, f.ObservedModifiedNS, f.ObservedRevision = before.Size, before.ModifiedNS, before.Revision
	f.ArtworkObserved = policy.Allows("local_metadata") && s.LocalMetadata != nil && artworkObserved && f.LocalMetadataIssue == ""
	f.AnalysisOperations = completed
	f.AnalysisAlgorithm = mediaanalysis.Algorithm
	f.SubtitleTextPending = !descriptor && policy.Allows("subtitles") && s.Subtitles != nil && f.VideoCodec != ""
	receipt, err := s.catalog.AcceptInventoryAnalysis(ctx, job, object, revision, policy.Revision, f)
	if err != nil {
		if infoPublicationDeferred(err) {
			return nil
		}
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "analysis_publication_failed")
	}
	if receipt.State != compactcatalog.InfoInputApplied {
		return nil
	}
	return s.finishBasicAnalysis(parent, ctx, job, object, revision, queuedPolicy, policy, source, completed, descriptor, deep, f.SubtitleTextPending)
}

// localRefresh is what a sidecar-only refresh reuses from the file's last
// probe: which operations it must redo, and the embedded subtitle tracks and
// timing that probe recorded.
type localRefresh struct {
	audio          *assets.Facts
	kind           string
	done           []string
	metadata, subs bool
	embedded       []subtitles.Track
	originUS       int64
	timingKnown    bool
}

// localRefreshEvidence decides whether a queued media file needs only its
// local metadata and subtitle discovery redone (S6). That is the case when its
// analysis is of this exact revision with the probe and playback analysis
// recorded, and the only allowed basic operations missing are local_metadata
// and subtitles, which a sidecar change removes. Anything else, or a file
// whose stored evidence doesn't match it, takes the full probe as before.
func (s *Service) localRefreshEvidence(ctx context.Context, source catalog.LibrarySource, object, revision, relative, analyzed string, done []string, policy catalog.ScanPolicy) (*localRefresh, error) {
	if source.Kind != "local" || analyzed != revision || assets.DiscImage(relative) || !policy.Allows("probe") {
		return nil, nil
	}
	has := map[string]bool{}
	for _, op := range done {
		has[op] = true
	}
	if !has["probe"] || !has[assets.PlaybackAnalysisRevision] {
		return nil, nil
	}
	r := localRefresh{done: done}
	for _, op := range catalog.BasicScanOperations {
		if !policy.Allows(op) || has[op] {
			continue
		}
		switch op {
		case "local_metadata":
			r.metadata = true
		case "subtitles":
			r.subs = true
		default:
			return nil, nil
		}
	}
	if !r.metadata && !r.subs {
		return nil, nil
	}
	var asset, videoCodec string
	var size, modified int64
	err := s.db.QueryRowContext(ctx, `SELECT l.kind,a.token,a.video_codec,o.size,o.modified_ns FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id AND a.size=o.size AND a.modified_ns=o.modified_ns JOIN library_sources src ON src.id=o.source_id JOIN libraries l ON l.id=src.library_id WHERE o.id=? AND o.revision=? AND o.retired=0`, object, revision).Scan(&r.kind, &asset, &videoCodec, &size, &modified)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.kind == "music" || r.kind == "audiobook" {
		if !r.metadata {
			return nil, nil
		}
		r.audio, err = s.catalog.InventoryAudioFileMetadata(ctx, object, revision)
		if err != nil || r.audio == nil || r.audio.AudioCodec == "" {
			return nil, err
		}
		if r.audio.ArtworkKey != "" && s.LocalMetadata != nil {
			cached, _, cacheErr := s.LocalMetadata.Open(r.audio.ArtworkKey)
			if cacheErr != nil {
				return nil, nil
			}
			cached.Close()
		}
		return &r, nil
	}
	if r.kind != "movie" && r.kind != "tv" && r.kind != "anime" || videoCodec == "" {
		return nil, nil
	}
	if r.subs {
		var ok bool
		if r.embedded, r.originUS, r.timingKnown, ok, err = subtitles.StoredEmbedded(ctx, s.db, asset, size, modified); err != nil || !ok {
			return nil, err
		}
	}
	return &r, nil
}

// refreshLocalMetadata re-reads a file's local metadata and subtitle
// sidecars without probing it again, and publishes them with
// CommitInventoryLocalRefresh. The reads are the basic path's own, under the
// same scan read witness, and the file must still be this revision before and
// after them.
func (s *Service) refreshLocalMetadata(parent, ctx context.Context, job, object, revision string, queuedPolicy int64, policy catalog.ScanPolicy, source catalog.LibrarySource, path string, r localRefresh, readContext func(string) context.Context, artworkObserved *bool, stat func() (storage.Snapshot, error), matches func(storage.Snapshot) bool, before storage.Snapshot, deep bool) error {
	var f assets.Facts
	completed := append([]string(nil), r.done...)
	var err error
	if r.metadata && s.LocalMetadata != nil {
		if err = s.analysisAllowed(ctx, job, source.LibraryID, policy.Revision, "local_metadata"); err != nil {
			return nil
		}
		if r.audio != nil {
			f = s.LocalMetadata.PrepareAudioSidecars(readContext("local_metadata"), source.LibraryID, r.kind, source.ResolvedPath, path, *r.audio)
		} else {
			f = s.LocalMetadata.VideoNFO(readContext("local_metadata"), source.LibraryID, source.ResolvedPath, path, r.kind, f)
			if r.kind == "movie" {
				f = s.LocalMetadata.MovieArtwork(readContext("local_metadata"), source.LibraryID, path, f)
			}
		}
		season := -1
		if r.kind == "tv" || r.kind == "anime" {
			if rel, rerr := filepath.Rel(source.ResolvedPath, path); rerr == nil {
				if plan := catalog.ParseEpisode(rel, r.kind); plan.Issue == "" && plan.Season >= 0 && plan.Season <= 9999 {
					season = plan.Season
				}
			}
		}
		extra := s.LocalMetadata.ReadSidecars(readContext("local_metadata"), source.LibraryID, r.kind, source.ResolvedPath, path, season)
		if key := extra["album/cover"]; key != "" && f.ArtworkKey == "" {
			f.ArtworkKey = key
			delete(extra, "album/cover")
		}
		if len(extra) > 0 {
			f.ArtworkExtra = extra
		}
		if r.audio == nil {
			f.ArtworkCandidates, f.ArtworkCandidatesObserved = s.LocalMetadata.ReadArtworkCandidates(readContext("local_metadata"), source.LibraryID, r.kind, source.ResolvedPath, path, season)
		}
		if f.LocalMetadataIssue == "" {
			completed = append(completed, "local_metadata")
		}
	}
	if r.subs && r.audio == nil {
		if err = s.analysisAllowed(ctx, job, source.LibraryID, policy.Revision, "subtitles"); err != nil {
			return nil
		}
		relative, _ := filepath.Rel(source.ResolvedPath, filepath.Dir(path))
		f.SubtitleInventory, err = subtitles.DiscoverManaged(readContext("subtitles"), s.db, s.storage, source.ID, source.Incarnation, job, relative, path, r.embedded, r.originUS, r.timingKnown)
		if err == nil && f.SubtitleInventory != nil && f.SubtitleInventory.Status == "known" {
			completed = append(completed, "subtitles")
		}
	}
	if r.subs && r.audio != nil {
		completed = append(completed, "subtitles")
	}

	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "probe_or_sidecar_failed")
	}
	after, err := stat()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil || !matches(after) {
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "source_revision_changed")
	}
	f.ObservedSize, f.ObservedModifiedNS, f.ObservedRevision = before.Size, before.ModifiedNS, before.Revision
	f.ArtworkObserved = r.metadata && s.LocalMetadata != nil && *artworkObserved && f.LocalMetadataIssue == ""
	f.AnalysisOperations = completed
	f.SubtitleTextPending = f.SubtitleInventory != nil && s.Subtitles != nil
	receipt, err := s.catalog.AcceptInventoryLocalRefresh(ctx, job, object, revision, policy.Revision, f)
	if err != nil {
		if infoPublicationDeferred(err) {
			return nil
		}
		return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "analysis_publication_failed")
	}
	if receipt.State != compactcatalog.InfoInputApplied {
		return nil
	}
	return s.finishBasicAnalysis(parent, ctx, job, object, revision, queuedPolicy, policy, source, completed, false, deep, f.SubtitleTextPending)
}

// finishBasicAnalysis is what follows a basic-analysis commit: the scan's
// subtitle text import, the check that every allowed basic operation was
// recorded, then the deep stages. A full probe and a local-metadata refresh
// both end here.
func (s *Service) finishBasicAnalysis(parent, ctx context.Context, job, object, revision string, queuedPolicy int64, policy catalog.ScanPolicy, source catalog.LibrarySource, completed []string, descriptor, deep, textPending bool) error {
	var err error
	if textPending {
		var item, asset string
		if err = s.db.QueryRowContext(ctx, `SELECT pid(e.public_id),a.token FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE o.id=? AND o.revision=? AND cl.library_id=? ORDER BY e.id LIMIT 1`, object, revision, source.LibraryID).Scan(&item, &asset); errors.Is(err, sql.ErrNoRows) {
			// The file never became an item (for example a naming issue left
			// it unassigned): there is no subtitle text to import, so skip
			// without hiding the real issue behind subtitle_text_import_failed.
			err = nil
		} else if err == nil {
			err = s.Subtitles.ImportScanText(ctx, item, asset, func(check context.Context, tx *sql.Tx) error {
				live, e := catalog.InventoryFenceTx(check, tx, job)
				if e != nil {
					return e
				}
				if live.ID != source.ID || live.Incarnation != source.Incarnation {
					return catalog.ErrAdminQuery
				}
				current, e := catalog.ScanPolicyTx(check, tx, source.LibraryID)
				if e != nil {
					return e
				}
				if current.Revision != policy.Revision || !current.Allows("subtitles") {
					return catalog.ErrAdminQuery
				}
				var valid bool
				e = tx.QueryRowContext(check, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE id=? AND revision=? AND asset_id=? AND state='available' AND retired=0)`, object, revision, asset).Scan(&valid)
				if e != nil {
					return e
				}
				if !valid {
					return catalog.ErrAdminQuery
				}
				return nil
			})
		}
		if err == nil {
			err = s.catalog.CompleteInventoryTextImports(ctx, job, object, revision, policy.Revision)
		}
		if err != nil {
			return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "subtitle_text_import_failed")
		}
	}
	if !descriptor {
		for _, op := range catalog.BasicScanOperations {
			if !policy.Allows(op) {
				continue
			}
			found := false
			for _, done := range completed {
				found = found || done == op
			}
			if !found {
				return s.analysisOutcome(parent, job, object, revision, queuedPolicy, true, "sidecar_analysis_incomplete")
			}
		}
	}
	if deep {
		return s.analyzeDeep(parent, job, object, revision, policy, source)
	}
	return nil
}

// inspectDisc probes one disc image as a single title. The bounded helper
// observes the disc structure, the committed structure picks the main title
// (the longest of at least a minute, ties by disc order), and only that title's
// timed media is read through the closed disc input (VIDEO §3 VS-4a/VS-4b).
func (s *Service) inspectDisc(ctx context.Context, job, object, revision string, policyRevision int64, source catalog.LibrarySource, relative string, readCtx context.Context, probe assets.Probe, path string, directory bool) (assets.Facts, error) {
	tool := disc.Tool{}
	probeStructure := func() (disc.Probe, error) {
		if directory {
			return tool.ProbeDirectory(readCtx, path)
		}
		return tool.ProbeLocal(readCtx, path)
	}
	structure, err := probeStructure()
	if err != nil {
		return assets.Facts{}, err
	}
	if structure.Encryption != "none" {
		return assets.Facts{}, disc.ErrEncrypted
	}
	title, ok, err := s.catalog.InventoryDiscMainTitle(ctx, object, revision, structure)
	var requestedKey, requestedFingerprint string
	requestErr := s.db.QueryRowContext(ctx, `SELECT title_key,fingerprint FROM inventory_disc_title_requests WHERE job_id=? AND object_id=? AND source_revision=?`, job, object, revision).Scan(&requestedKey, &requestedFingerprint)
	if requestErr != nil && !errors.Is(requestErr, sql.ErrNoRows) {
		return assets.Facts{}, requestErr
	}
	if requestedKey != "" {
		raw, e := json.Marshal(structure)
		if e != nil {
			return assets.Facts{}, e
		}
		sum := sha256.Sum256(append([]byte("disc_structure_v1\x00"), raw...))
		if hex.EncodeToString(sum[:]) != requestedFingerprint {
			return assets.Facts{}, catalog.ErrDiscSelectionChanged
		}
		ok = false
		for _, candidate := range structure.Titles {
			if candidate.Key(structure.Kind) == requestedKey {
				title = candidate
				ok = true
				break
			}
		}
	}
	if err != nil {
		return assets.Facts{}, err
	}
	if !ok {
		return assets.Facts{}, errors.New("disc has no title of at least a minute")
	}
	manifest := func() ([]storage.DiscMember, error) { return nil, nil }
	if directory {
		consumed, e := disc.ConsumedMembers(structure)
		if e != nil {
			return assets.Facts{}, e
		}
		required, optional, directories := []string{}, []string{}, []string{}
		for _, member := range consumed {
			switch {
			case member.Directory:
				directories = append(directories, member.Path)
			case member.Required:
				required = append(required, member.Path)
			default:
				optional = append(optional, member.Path)
			}
		}
		req := storage.InventoryRequest{Root: source.ResolvedPath, RelativePath: relative, RootIdentity: source.RootIdentity, FollowSymlinks: source.FollowSymlinks}
		manifest = func() ([]storage.DiscMember, error) {
			if s.storage != nil {
				return s.storage.DiscMemberManifest(readCtx, source.ID, req, required, optional, directories)
			}
			return storage.LocalDiscMemberManifest(req, required, optional, directories)
		}
	}
	// The consumed files are witnessed before and after the reads, and the
	// structure is observed again between them, so a changed member can never
	// mix two source revisions into one publication.
	before, err := manifest()
	if err != nil {
		return assets.Facts{}, err
	}
	again, err := probeStructure()
	if err != nil {
		return assets.Facts{}, err
	}
	if !reflect.DeepEqual(structure, again) {
		return assets.Facts{}, errors.New("disc structure changed during analysis")
	}
	facts, err := probe.InspectDiscScan(readCtx, path, again, title)
	if err != nil {
		return assets.Facts{}, err
	}
	if directory {
		after, e := manifest()
		if e != nil {
			return assets.Facts{}, e
		}
		if len(before) == 0 || len(after) == 0 || !sameDiscMembers(before, after) {
			return assets.Facts{}, errors.New("disc members changed during analysis")
		}
	}
	receipt, err := s.catalog.AcceptInventoryDiscProbe(ctx, job, object, revision, policyRevision, again)
	if err != nil {
		return assets.Facts{}, err
	}
	if receipt.State != compactcatalog.InfoInputApplied {
		return assets.Facts{}, compactcatalog.ErrInfoPreparing
	}
	raw, err := json.Marshal(again)
	if err != nil {
		return assets.Facts{}, err
	}
	digest := sha256.Sum256(append([]byte("disc_structure_v1\x00"), raw...))
	facts.DiscAlternate = requestedKey != ""
	facts.DiscSelection = &assets.DiscMediaSelection{TitleKey: title.Key(again.Kind), StructureFingerprint: hex.EncodeToString(digest[:]), Members: before}
	return facts, nil
}

// sameDiscMembers compares the bounded identities and revisions of the files
// the title read actually consumed.
func sameDiscMembers(a, b []storage.DiscMember) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].ObjectIdentity != b[i].ObjectIdentity || a[i].Size != b[i].Size || a[i].ModifiedNS != b[i].ModifiedNS || a[i].Required != b[i].Required || a[i].Directory != b[i].Directory {
			return false
		}
	}
	return true
}

func (s *Service) analysisOutcome(ctx context.Context, job, object, revision string, policy int64, warning bool, code string) error {
	gated3, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	source, err := catalog.InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	current, err := catalog.ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	if current.Revision != policy {
		return nil
	}
	state := "done"
	if warning {
		state = "warning"
	} else {
		code = ""
	}
	result, err := tx.ExecContext(ctx, `UPDATE inventory_analysis_queue SET state=? WHERE job_id=? AND object_id=? AND revision=? AND policy_revision=? AND state!='warning'`, state, job, object, revision, policy)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 && warning {
		var relative string
		if err = tx.QueryRowContext(ctx, `SELECT relative_path FROM inventory_objects WHERE id=?`, object).Scan(&relative); err != nil {
			return err
		}
		if code == "source_revision_changed" {
			if err = recordRunNoticeTx(ctx, tx, job, source.ID, filepath.Dir(relative), time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		if err = catalog.InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: relative, Reason: code, Warning: false}); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_error=?,analysis_state='warning' WHERE id=? AND revision=?`, code, object, revision); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET warnings=warnings+1 WHERE job_id=?`, job); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_changes(source_id,job_id,object_id,asset_id,revision,kind,created_at) SELECT ?,?,id,asset_id,revision,?,? FROM inventory_objects WHERE id=? AND revision=?`, source.ID, job, code, time.Now().UTC().Format(time.RFC3339Nano), object, revision); err != nil {
			return err
		}
	}
	if !warning {
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_state='complete',analysis_error='',analysis_algorithm=?,analysis_generation=? WHERE id=? AND revision=?`, mediaanalysis.Algorithm, source.Generation, object, revision); err != nil {
			return err
		}
	}
	return gated3.Commit()
}

func finishScanTx(ctx context.Context, tx *sql.Tx, job, source string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// A file the library couldn't turn into an item (an episode name needing
	// assignment) keeps the scan from reading as clean, whichever scan last
	// looked at it: an incremental scan that skipped its folder still ends
	// "with warnings" and the source isn't "healthy" while any remain. One
	// seek on the issues-only index (0223).
	attention := `EXISTS(SELECT 1 FROM episodic_sources e JOIN inventory_objects o ON o.asset_id=e.asset_id JOIN library_sources s ON s.id=o.source_id WHERE s.id=? AND e.library_id=s.library_id AND e.issue<>'' AND o.retired=0 AND o.state='available' AND o.root_incarnation=s.incarnation)`
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status=CASE WHEN (SELECT warnings FROM inventory_runs WHERE job_id=?)>0 OR `+attention+` THEN 'complete_with_warnings' ELSE 'complete' END WHERE id=? AND status='running'`, job, source, job); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inventory_runs SET phase=(SELECT status FROM jobs WHERE id=?),finished_at=?,pause_reason='' WHERE job_id=?`, job, now, job); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_sources SET health=CASE WHEN (SELECT warnings FROM inventory_runs WHERE job_id=?)>0 OR `+attention+` THEN 'degraded' WHEN (SELECT reported FROM inventory_runs WHERE job_id=?)=? AND last_complete_at='' THEN 'ready' ELSE 'healthy' END WHERE id=?`, job, source, job, strictReportMode, source); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`, now, source); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM inventory_source_active WHERE job_id=?`, job); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_changes(source_id,job_id,kind,created_at) VALUES(?,?,'scan_completed',?)`, source, job, now); err != nil {
		return err
	}
	var reportMode int
	if err := tx.QueryRowContext(ctx, `SELECT reported FROM inventory_runs WHERE job_id=?`, job).Scan(&reportMode); err != nil {
		return err
	}
	if reportMode == strictReportMode {
		// The ordinary double-check successor has no target fence. A strict
		// report never initiates it or consumes another run's second-pass marker.
		return nil
	}
	// One bounded second folder pass per initiating run: the run that asked for
	// it is remembered, so the follow-up pass itself never asks again, and the
	// caller queues exactly one job even when the folder's own interval is zero.
	doubleCheck, err := remotesources.DoubleCheckFoldersTx(ctx, tx, source)
	if err != nil {
		return err
	}
	var secondPass string
	if err = tx.QueryRowContext(ctx, `SELECT double_check_job FROM library_sources WHERE id=?`, source).Scan(&secondPass); err != nil {
		return err
	}
	if secondPass != "" {
		_, err = tx.ExecContext(ctx, `UPDATE library_sources SET double_check_job='' WHERE id=?`, source)
		return err
	}
	if !doubleCheck {
		return nil
	}
	var changed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_changes WHERE job_id=? AND kind<>'scan_completed')`, job).Scan(&changed); err != nil || !changed {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_sources SET double_check_job=? WHERE id=?`, job, source); err != nil {
		return err
	}
	// The successor and the finishing run share one commit. Shutdown after
	// this transaction can no longer strand the second-pass marker.
	_, err = queueSourceTx(context.WithValue(ctx, scanModeKey{}, "incremental"), tx, source, false)
	return err
}

// A durable pending source frame and an exact disc structure awaiting apply
// remain queued. They are not probe failures and may not acknowledge analysis.
func infoPublicationDeferred(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, compactcatalog.ErrInfoInputBusy) || errors.Is(err, compactcatalog.ErrInfoInputDeferred) || errors.Is(err, compactcatalog.ErrInfoPreparing) || errors.Is(err, catalog.ErrDiscSelectionChanged)
}
