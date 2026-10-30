package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Intro and credits detection is its own background work, not a scan stage:
// it needs a show's other episodes, it should not hold a library scan open,
// and the owner turns it on and off for the whole server. One loop does
// everything, one thing at a time, at the lowest priority the server has:
//
//  1. notice files that need measuring (new and changed files through the
//     inventory change log; a full pass when the modes change and daily);
//  2. measure one file: decode the audio where an intro and the credits can
//     be (and, in thorough mode, the picture near the end), keep the result
//     as an analysis result, and publish what the file shows on its own;
//  3. compare a show's episodes once none of its files is waiting, and
//     publish the intros and credits they share.
//
// Every step is resumable from the database. Comparison is incremental: a
// file is re-compared only when its own evidence or a neighbour's changed.

// SegmentStage is the analysis result stage that holds a file's evidence.
const SegmentStage = "segment_evidence"

const (
	segmentCatchupPage    = 64
	segmentOrphanPage     = 256
	segmentDropPage       = 512
	segmentChangesPage    = 128
	segmentFullPassEvery  = 24 * time.Hour
	segmentMatchBatch     = 8
	segmentAttempts       = 3
	segmentNeighbourCount = 8
)

// RunSegments drives the detector until ctx ends.
func (s *Service) RunSegments(ctx context.Context) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	wake := worker.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "inventory_changes", "server_settings", "console_settings_revision", "admin_documents", "remote_source_settings", "remote_source_read_profiles", "segment_*")
	defer unregister()
	worker.Run(ctx, "mediaanalysis.segments", wake, func(ctx context.Context) time.Duration {
		if !dbwork.Yield(ctx) || !dbwork.WaitBackground(ctx) {
			return 0
		}
		ctx = dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, s.db))
		started := time.Now()
		wait, err := s.SegmentStep(ctx)
		if err != nil && ctx.Err() == nil {
			return 30 * time.Second
		}
		if !dbwork.PaceBackground(ctx, started) {
			return 0
		}
		return wait
	})
}

// SegmentStep does one quantum of detection work and says when to run again
// (0: nothing is due; wait to be woken).
func (s *Service) SegmentStep(ctx context.Context) (time.Duration, error) {
	more, err := s.segmentCatchup(ctx)
	if err != nil {
		return 0, err
	}
	worked, wait, err := s.segmentFileStep(ctx)
	if err != nil || worked {
		return time.Millisecond, err
	}
	matched, err := s.segmentShowStep(ctx)
	if err != nil || matched || more {
		return time.Millisecond, err
	}
	return wait, nil
}

type segmentCatchupState struct {
	Cursor         string
	Settled        bool
	PassMS         int64
	ChangeSequence int64
	Modes          string
}

func readSegmentCatchup(ctx context.Context, q queryer) (segmentCatchupState, error) {
	var c segmentCatchupState
	err := q.QueryRowContext(ctx, `SELECT cursor,settled,pass_ms,change_sequence,modes FROM segment_catchup WHERE singleton=1`).Scan(&c.Cursor, &c.Settled, &c.PassMS, &c.ChangeSequence, &c.Modes)
	if errors.Is(err, sql.ErrNoRows) {
		return segmentCatchupState{}, nil
	}
	return c, err
}

// segmentCatchup finds files that need measuring. It reports whether a full
// pass is still under way.
func (s *Service) segmentCatchup(ctx context.Context) (bool, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	libraries, _, err := segmentLibrariesTx(ctx, tx)
	if err != nil {
		return false, err
	}
	planning, err := segmentPlanningTx(ctx, tx)
	if err != nil {
		return false, err
	}
	for i := range libraries {
		libraries[i].Planning = planning.digest()
	}
	state, err := readSegmentCatchup(ctx, tx)
	if err != nil {
		return false, err
	}
	previous := state
	enabled := map[string]segmentLibrary{}
	for _, l := range libraries {
		if l.Mode != SegmentModeOff {
			enabled[l.ID] = l
		}
	}
	sourceModes, err := segmentSourceModesTx(ctx, tx)
	if err != nil {
		return false, err
	}
	digest := token(segmentModesDigest(libraries) + "|" + sourceModes)
	if digest != state.Modes {
		// A changed mode is a new pass: a library turned on (or up to
		// thorough) has files to measure, and one turned off has queued
		// work to drop.
		state = segmentCatchupState{ChangeSequence: state.ChangeSequence, Modes: digest}
	}
	// Queued work in libraries that are now off is dropped a page at a time
	// (SEG2). Until then the file step drops any such file it reaches, so a
	// mode change never holds the writer for the size of the library.
	if err = dropDisabledSegmentWork(ctx, tx, enabled); err != nil {
		return false, err
	}
	if state.Settled && nowMS()-state.PassMS > segmentFullPassEvery.Milliseconds() {
		state.Settled, state.Cursor = false, ""
	}
	// New and changed files since the last look.
	var latest int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM inventory_changes`).Scan(&latest); err != nil {
		return false, err
	}
	if latest < state.ChangeSequence {
		state.ChangeSequence = 0 // the log was rebuilt; the full pass covers the gap
		state.Settled, state.Cursor = false, ""
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT object_id,sequence FROM inventory_changes WHERE sequence>? AND object_id<>'' ORDER BY sequence LIMIT `+fmt.Sprint(segmentChangesPage), state.ChangeSequence)
	if err != nil {
		return false, err
	}
	var changed []string
	for rows.Next() {
		var object string
		if err = rows.Scan(&object, &state.ChangeSequence); err != nil {
			rows.Close()
			return false, err
		}
		changed = append(changed, object)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return false, err
	}
	if len(changed) < segmentChangesPage {
		state.ChangeSequence = max(state.ChangeSequence, latest)
	}
	for _, object := range changed {
		if err = s.segmentConsider(ctx, tx, object, enabled); err != nil {
			return false, err
		}
	}
	more := false
	if !state.Settled && len(enabled) > 0 {
		rows, err := tx.QueryContext(ctx, `SELECT o.id FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id WHERE o.id>? AND src.library_id IN(SELECT value FROM json_each(?)) ORDER BY o.id LIMIT `+fmt.Sprint(segmentCatchupPage), state.Cursor, jsonString(keys(enabled)))
		if err != nil {
			return false, err
		}
		var page []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return false, err
			}
			page = append(page, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return false, err
		}
		for _, object := range page {
			if err = s.segmentConsider(ctx, tx, object, enabled); err != nil {
				return false, err
			}
		}
		if len(page) == segmentCatchupPage {
			state.Cursor, more = page[len(page)-1], true
		} else {
			state.Cursor, state.Settled, state.PassMS = "", true, nowMS()
		}
	} else if len(enabled) == 0 {
		state.Settled, state.Cursor = true, ""
	}
	// Forget files that left the inventory, and re-compare their shows. Each
	// look examines one page of segment rows after the orphan cursor, so a
	// settled corpus costs a page per look, not a walk of every row (SRV022).
	orphanCursor, orphanLast, orphanExamined := s.segmentOrphanCursor, "", 0
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(object_id),'') FROM (SELECT object_id FROM segment_files WHERE object_id>? ORDER BY object_id LIMIT ?)`, orphanCursor, segmentOrphanPage).Scan(&orphanExamined, &orphanLast); err != nil {
		return false, err
	}
	if orphanExamined > 0 {
		orphans := `SELECT f.object_id FROM segment_files f LEFT JOIN inventory_objects o ON o.id=f.object_id WHERE f.object_id>?1 AND f.object_id<=?2 AND (o.id IS NULL OR o.retired=1)`
		if _, err = tx.ExecContext(ctx, `UPDATE segment_shows SET dirty=1,updated_ms=?3 WHERE show_id IN(SELECT f.show_id FROM segment_files f WHERE f.show_id>0 AND f.object_id IN(`+orphans+`))`, orphanCursor, orphanLast, nowMS()); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM segment_files WHERE object_id IN(`+orphans+`)`, orphanCursor, orphanLast); err != nil {
			return false, err
		}
		// Failed and unsupported rows of a library that is off go as the window
		// passes them, so turning it back on measures them afresh.
		if _, err = tx.ExecContext(ctx, `DELETE FROM segment_files WHERE object_id>? AND object_id<=? AND state<>'complete' AND library_id NOT IN(SELECT value FROM json_each(?))`, orphanCursor, orphanLast, jsonString(keys(enabled))); err != nil {
			return false, err
		}
	}
	// An unchanged upsert still changes SQLite rows and wakes this worker again.
	// Keep real cursor/high-water progress durable, but leave a settled pass
	// silent. Other work above may have changed, so still commit the transaction.
	if state != previous {
		if _, err = tx.ExecContext(ctx, `INSERT INTO segment_catchup(singleton,cursor,settled,pass_ms,change_sequence,modes) VALUES(1,?,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET cursor=excluded.cursor,settled=excluded.settled,pass_ms=excluded.pass_ms,change_sequence=excluded.change_sequence,modes=excluded.modes`, state.Cursor, state.Settled, state.PassMS, state.ChangeSequence, state.Modes); err != nil {
			return false, err
		}
	}
	if err = gated.Commit(); err != nil {
		return false, err
	}
	if orphanExamined < segmentOrphanPage {
		s.segmentOrphanCursor = ""
	} else {
		s.segmentOrphanCursor = orphanLast
	}
	return more, nil
}

// dropDisabledSegmentWork deletes one page of pending work in libraries that
// are off, found through the per-library projection and its partial index, so a
// pass costs the page plus one row per library with pending work. Failed and
// unsupported rows of those libraries go with the catch-up's orphan window.
func dropDisabledSegmentWork(ctx context.Context, tx *sql.Tx, enabled map[string]segmentLibrary) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM segment_files WHERE object_id IN(
 SELECT f.object_id FROM segment_pending_activity a JOIN segment_files f INDEXED BY segment_files_pending_by_library ON f.library_id=a.library_id AND f.state='pending'
 WHERE a.library_id NOT IN(SELECT value FROM json_each(?)) LIMIT ?)`, jsonString(keys(enabled)), segmentDropPage)
	return err
}

func keys(m map[string]segmentLibrary) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// segmentConsider queues one file when it is in a measured library and its
// current revision has not been measured in its library's mode (a thorough
// measurement also serves standard).
func (s *Service) segmentConsider(ctx context.Context, tx *sql.Tx, object string, enabled map[string]segmentLibrary) error {
	var revision, library, relative, analyzed, source string
	err := tx.QueryRowContext(ctx, `SELECT o.revision,src.library_id,o.relative_path,o.analysis_revision,o.source_id FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id WHERE o.id=? AND o.retired=0 AND o.state='available' AND o.root_incarnation=src.incarnation AND src.enabled=1`, object).Scan(&revision, &library, &relative, &analyzed, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	l, ok := enabled[library]
	// Remote (.strm) targets are not read for detection, and a file is only
	// measured once its probe has given it a duration.
	if !ok || strings.HasSuffix(strings.ToLower(relative), ".strm") || analyzed != revision {
		return nil
	}
	consent, err := catalog.SourceAnalysisConsentTx(ctx, tx, source)
	if err != nil {
		return err
	}
	if !consent.Allowed {
		_, err = tx.ExecContext(ctx, `DELETE FROM segment_files WHERE object_id=? AND state='pending'`, object)
		return err
	}
	// The source's own page can switch intro and credit detection off for the
	// files found through it, or choose the listening or picture mode.
	var sourceMode string
	e := tx.QueryRowContext(ctx, `SELECT st.intros_mode FROM library_sources s JOIN remote_source_settings st ON st.source_id=s.remote_source_id WHERE s.id=?`, source).Scan(&sourceMode)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil {
		switch sourceMode {
		case "off":
			_, e = tx.ExecContext(ctx, `DELETE FROM segment_files WHERE object_id=? AND state='pending'`, object)
			return e
		case "sound":
			l.Mode = SegmentModeStandard
		case "picture":
			l.Mode = SegmentModeThorough
		}
	}
	var current, mode, state, planning string
	err = tx.QueryRowContext(ctx, `SELECT source_revision,mode,state,planning_policy FROM segment_files WHERE object_id=?`, object).Scan(&current, &mode, &state, &planning)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && current == revision && planning == l.Planning && (mode == l.Mode || mode == SegmentModeThorough) {
		return nil
	}
	// The show is known from the start, so a file still waiting or one that
	// could not be read is part of its show's review and re-run.
	var show int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT ep.show_id FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id JOIN catalog_asset_links ia ON ia.asset_id=a.id JOIN catalog_episodes ep ON ep.entity_id=ia.entity_id WHERE o.id=? ORDER BY ia.part_index,ia.entity_id LIMIT 1),0)`, object).Scan(&show)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO segment_files(object_id,source_revision,library_id,show_id,mode,planning_policy,state,updated_ms) VALUES(?,?,?,?,?,?,'pending',?) ON CONFLICT(object_id) DO UPDATE SET source_revision=excluded.source_revision,library_id=excluded.library_id,show_id=excluded.show_id,mode=excluded.mode,planning_policy=excluded.planning_policy,state='pending',result_id='',match_digest='',attempt=0,next_ms=0,error_code='',updated_ms=excluded.updated_ms`, object, revision, library, show, l.Mode, l.Planning, nowMS())
	return err
}

// segmentFile is everything one measurement needs to know about a file.
type segmentFile struct {
	ReadFacts                           ReadFacts
	Planning                            segmentPlanning
	ObjectID, Revision, LibraryID, Mode string
	AssetID, SourceID, Incarnation      string
	Generation                          int64
	Container, AudioCodec, VideoCodec   string
	DurationUS                          int64
	ItemID, ItemKind                    string
	ShowID                              int64
	Video                               assets.StreamDetail
}

func (f segmentFile) binding() string { return token(f.Incarnation, fmt.Sprint(f.Generation), "") }

// segmentFileTx reads a queued file's current facts; sql.ErrNoRows means the
// file is gone, moved or changed since it was queued.
func segmentFileTx(ctx context.Context, tx *sql.Tx, object, revision string) (segmentFile, error) {
	f := segmentFile{ObjectID: object}
	var duration float64
	var analyzed string
	err := tx.QueryRowContext(ctx, `SELECT o.revision,src.library_id,o.asset_id,src.id,src.incarnation,src.generation,a.container,a.audio_codec,a.video_codec,a.duration,o.analysis_revision FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=? AND o.retired=0 AND o.state='available' AND o.root_incarnation=src.incarnation AND src.enabled=1 AND a.available=1`, object).Scan(&f.Revision, &f.LibraryID, &f.AssetID, &f.SourceID, &f.Incarnation, &f.Generation, &f.Container, &f.AudioCodec, &f.VideoCodec, &duration, &analyzed)
	if err != nil {
		return f, err
	}
	if f.Revision != revision || analyzed != revision {
		return f, sql.ErrNoRows
	}
	f.ReadFacts, err = readFactsTx(ctx, tx, f.SourceID, object, revision, "", f.AssetID)
	if err != nil {
		return f, err
	}
	if f.DurationUS, err = exactUS(duration); err != nil {
		return f, ErrUnsupported
	}
	err = tx.QueryRowContext(ctx, `SELECT pid(i.public_id),k.name,COALESCE(ep.show_id,0) FROM catalog_assets a JOIN catalog_asset_links ia ON ia.asset_id=a.id JOIN catalog_entities i ON i.id=ia.entity_id JOIN catalog_libraries cl ON cl.id=i.library_id JOIN catalog_kinds k ON k.id=i.kind LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id WHERE a.token=? AND cl.library_id=? AND k.name IN('episode','movie') ORDER BY ia.part_index,ia.entity_id LIMIT 1`, f.AssetID, f.LibraryID).Scan(&f.ItemID, &f.ItemKind, &f.ShowID)
	if err != nil {
		return f, err
	}
	f.ReadFacts.ItemID = f.ItemID
	if f.VideoCodec != "" {
		var detail string
		e := tx.QueryRowContext(ctx, `SELECT st.detail_json FROM asset_streams st JOIN catalog_assets a ON a.id=st.asset_id WHERE a.token=? AND st.type='video' ORDER BY st.stream_index LIMIT 1`, f.AssetID).Scan(&detail)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return f, e
		}
		if e == nil {
			var stream assets.Stream
			stream.DecodeDetail(detail)
			f.Video = stream.Detail
		}
	}
	return f, nil
}

// segmentFileStep measures the next queued file. It reports whether it did
// anything and, when nothing was due, how long until a retry is.
func (s *Service) segmentFileStep(ctx context.Context) (bool, time.Duration, error) {
	var object, revision, mode string
	var attempt int
	err := dbwork.QueryRow(ctx, s.db, `SELECT object_id,source_revision,mode,attempt FROM segment_files WHERE state='pending' AND next_ms<=? ORDER BY show_id,object_id LIMIT 1`, nowMS()).Scan(&object, &revision, &mode, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		var next sql.NullInt64
		if err = dbwork.QueryRow(ctx, s.db, `SELECT MIN(next_ms) FROM segment_files WHERE state='pending'`).Scan(&next); err != nil || !next.Valid {
			return false, 0, err
		}
		return false, time.Duration(max(1000, next.Int64-nowMS())) * time.Millisecond, nil
	}
	if err != nil {
		return false, 0, err
	}
	// The analysis slot and producer lock are shared with scan analysis: one
	// decoder at a time, whoever asks first.
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	default:
		return false, 5 * time.Second, nil
	}
	physical, err := s.physical.Lock(livechannels.Allocation{ID: analysisLockID("analysis-producer"), Generation: 1})
	if errors.Is(err, livechannels.ErrPhysicalBusy) {
		return false, 5 * time.Second, nil
	}
	if err != nil {
		return false, 0, err
	}
	defer physical.Close()
	if err = s.cleanStaging(); err != nil {
		return false, 0, err
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return false, 0, err
	}
	f, err := segmentFileTx(ctx, snapshot.Tx(), object, revision)
	if err == nil {
		f.Planning, err = segmentPlanningTx(ctx, snapshot.Tx())
	}
	if err == nil && s.Admission != nil {
		var allowed bool
		if allowed, err = s.Admission(ctx, snapshot.Tx(), "analysis"); err == nil && !allowed {
			snapshot.Rollback()
			return false, time.Minute, nil
		}
	}
	snapshot.Rollback()
	if errors.Is(err, storage.ErrRemoteAnalysisDisabled) {
		_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM segment_files WHERE object_id=? AND source_revision=? AND state='pending'`, object, revision)
		return true, 0, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Gone or changed: the catch-up queues the current revision again.
		_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM segment_files WHERE object_id=? AND source_revision=?`, object, revision)
		return true, 0, err
	}
	if err != nil {
		return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
	}
	f.Mode = mode
	ctx, read := s.segmentReadContext(ctx, f, attempt)
	defer read.Close()
	// The source's own intro choice applies to work already queued: a file whose
	// source was switched off is dropped, and one whose mode changed is queued
	// again under the new mode instead of being measured with the old one.
	if effective, e := s.effectiveMode(ctx, f.ObjectID, f.LibraryID); e != nil {
		return true, 0, s.segmentFailure(ctx, object, revision, attempt, e)
	} else if effective == SegmentModeOff {
		_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM segment_files WHERE object_id=? AND source_revision=? AND state='pending'`, f.ObjectID, f.Revision)
		return true, 0, e
	} else if effective != "" && effective != f.Mode {
		_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE segment_files SET mode=?,state='pending',updated_ms=? WHERE object_id=? AND source_revision=?`, effective, nowMS(), f.ObjectID, f.Revision)
		return true, 0, e
	}
	chapters, err := s.chapters(ctx, f.AssetID)
	if err != nil {
		return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
	}
	plan := planSegmentsWithPolicy(f.ItemKind, mode, f.DurationUS, f.AudioCodec != "", f.VideoCodec != "", f.Planning)
	evidence := segmentdetect.Evidence{DurationUS: f.DurationUS}
	tool := "none"
	inputEvidence := ""
	if !plan.empty() {
		if f.Container == "strm" {
			return true, 0, s.segmentFailure(ctx, object, revision, attempt, ErrUnsupported)
		}
		input, err := s.options.Open(ctx, f.ItemID, f.AssetID)
		if err != nil {
			return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
		}
		defer input.Close()
		input = &authorityInput{Input: input, invocation: read}
		if err = input.Validate(ctx); err != nil {
			return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
		}
		if tool, err = toolHash(s.options.FFmpeg); err != nil {
			return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
		}
		runner := s.options.Decoder(&countedInput{Input: input, limit: s.options.MaxInputBytes * 4, progress: func(int64) {}})
		decode := func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
			spec.PhysicalLock = physical
			return runner(ctx, key, spec, consume)
		}
		video := decoder.PreviewVideoFacts{DynamicRange: f.Video.DynamicRange, DolbyVisionProfile: f.Video.DolbyVisionProfile, DolbyVisionCompatibility: f.Video.DolbyVisionCompatibility}
		evidence, err = extractSegmentEvidence(ctx, decode, token("segments", object, revision, mode), plan, f.DurationUS, video)
		if err != nil {
			return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
		}
		if err = input.Validate(ctx); err != nil {
			return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
		}
		if current, e := toolHash(s.options.FFmpeg); e != nil || current != tool {
			return true, 0, s.segmentFailure(ctx, object, revision, attempt, ErrConflict)
		}
		inputEvidence = input.Evidence()
	}
	err = s.publishSegmentEvidence(ctx, f, evidence, fileSegments(evidence, chapters), inputEvidence, tool)
	if err != nil {
		return true, 0, s.segmentFailure(ctx, object, revision, attempt, err)
	}
	return true, 0, nil
}

// segmentFailure records a failed measurement. Interruptions and contention
// are retried without counting; a file that cannot be decoded, or fails three
// times, waits for its next revision (or the owner's re-run).
func (s *Service) segmentFailure(ctx context.Context, object, revision string, attempt int, cause error) error {
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if errors.Is(cause, storage.ErrRemoteAnalysisDisabled) {
		_, err := dbwork.ExecWrite(cleanup, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM segment_files WHERE object_id=? AND source_revision=? AND state='pending'`, object, revision)
		return err
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(cause, context.Canceled) || errors.Is(cause, storage.ErrBusy) || errors.Is(cause, storage.ErrRemoteChanged) {
		return nil
	}
	state, code := "pending", "analysis_failed"
	switch {
	case errors.Is(cause, ErrUnsupported) || errors.Is(cause, decoder.ErrConfinementUnavailable) || errors.Is(cause, decoder.ErrInvalidConfiguration):
		state, code = "unsupported", "decoder_or_stream_unsupported"
	case errors.Is(cause, ErrBudget):
		state, code = "failed", "analysis_budget_exceeded"
	case errors.Is(cause, ErrConflict):
		code = "analysis_source_or_markers_changed"
	}
	if state == "pending" && attempt+1 >= segmentAttempts {
		state = "failed"
	}
	// The owner sees the code; the cause goes to the server log for support.
	log.Printf("intro and credits detection: file %s: %s (%s): %v", object, code, state, cause)
	next := nowMS() + min(int64(3600000), int64(60000)<<min(attempt, 6))
	_, err := dbwork.ExecWrite(cleanup, s.db, dbwork.ClassBackgroundMedia, `UPDATE segment_files SET state=?,attempt=attempt+1,next_ms=?,error_code=?,updated_ms=? WHERE object_id=? AND source_revision=?`, state, next, code, nowMS(), object, revision)
	return err
}
