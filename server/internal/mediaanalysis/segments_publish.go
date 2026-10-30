package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
)

// Marker families. Each publisher replaces only its own family's markers, and
// only those the owner has not touched: an edited, approved, dismissed or
// owner-made marker is the owner's, and no detector writes over it or adds a
// marker of the same kind on top of it.
func fileFamily(provenance string) bool {
	switch provenance {
	case segmentdetect.ChapterProvenance, segmentdetect.VisualCreditsProvenance,
		// The scan-stage detector this replaced published these.
		"luminance_opening_boundary:v1", "sustained_end_title_luminance:v1", "paired_black_boundaries_semantic_ambiguous:v1":
		return true
	}
	return false
}

func seasonFamily(provenance string) bool {
	return strings.HasPrefix(provenance, segmentdetect.SeasonProvenance)
}

// publishSegmentEvidence keeps one file's measurement as its current
// segment_evidence result, publishes what the file shows on its own and asks
// for its show to be compared again.
func (s *Service) publishSegmentEvidence(ctx context.Context, f segmentFile, e segmentdetect.Evidence, detections []segmentdetect.Detection, inputEvidence, tool string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := token("segment-evidence", f.ObjectID, f.Revision, f.Mode, f.Planning.digest(), segmentdetect.EvidenceAlgorithm, fmt.Sprint(nowMS()))
	out := produced{Artifacts: []artifactOutput{}, Markers: []Marker{}}
	if err := s.adder(ctx, id, &out)(e.Marshal(), SegmentStage, "application/json", 0, 0, f.DurationUS, 0, 0); err != nil {
		return err
	}
	defer func() {
		for _, a := range out.Artifacts {
			_ = s.artifacts.RemoveRetained(a.RetainedKey)
		}
	}()
	s.publication.Lock()
	defer s.publication.Unlock()
	release, err := s.sealArtifacts(ctx, &out)
	if err != nil {
		return err
	}
	defer release()
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = checkReadPublicationTx(ctx, tx); err != nil {
		return err
	}
	current, err := segmentFileTx(ctx, tx, f.ObjectID, f.Revision)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (current.ReadFacts != f.ReadFacts || current.AssetID != f.AssetID || current.Incarnation != f.Incarnation || current.Generation != f.Generation || current.DurationUS != f.DurationUS) {
		// The file changed while it was measured; the catch-up queues the new one.
		if _, err = tx.ExecContext(ctx, `DELETE FROM segment_files WHERE object_id=? AND source_revision=?`, f.ObjectID, f.Revision); err != nil {
			return err
		}
		return gated.Commit()
	}
	if err != nil {
		return err
	}
	planning, err := segmentPlanningTx(ctx, tx)
	if err != nil {
		return err
	}
	var queuedPolicy, queuedMode string
	if err = tx.QueryRowContext(ctx, `SELECT planning_policy,mode FROM segment_files WHERE object_id=? AND source_revision=?`, f.ObjectID, f.Revision).Scan(&queuedPolicy, &queuedMode); errors.Is(err, sql.ErrNoRows) {
		return gated.Commit()
	} else if err != nil {
		return err
	}
	if planning != f.Planning || queuedPolicy != planning.digest() || queuedMode != f.Mode {
		return gated.Commit()
	}
	// The source may have changed its intro choice while this file was measured.
	// Publication follows the mode that applies now, not the one it started with.
	effective, err := effectiveSegmentModeTx(ctx, tx, f.ObjectID, f.LibraryID)
	if err != nil {
		return err
	}
	if effective == SegmentModeOff {
		if _, err = tx.ExecContext(ctx, `DELETE FROM segment_files WHERE object_id=? AND source_revision=?`, f.ObjectID, f.Revision); err != nil {
			return err
		}
		return gated.Commit()
	}
	if effective != "" && effective != f.Mode {
		if _, err = tx.ExecContext(ctx, `UPDATE segment_files SET mode=?,state='pending',updated_ms=? WHERE object_id=? AND source_revision=?`, effective, nowMS(), f.ObjectID, f.Revision); err != nil {
			return err
		}
		return gated.Commit()
	}
	summary := map[string]any{"algorithm": segmentdetect.EvidenceAlgorithm, "mode": f.Mode, "planningPolicy": f.Planning.digest(), "openingPoints": len(e.Opening.Points), "closingPoints": len(e.Closing.Points), "frames": len(e.Frames), "fileDetections": len(detections)}
	if _, err = tx.ExecContext(ctx, `INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) VALUES(?,?,?,?,?,?,0,?,?,?,?,?,?,?)`, id, f.ObjectID, f.AssetID, f.Revision, f.Incarnation, f.Generation, SegmentStage, segmentdetect.EvidenceAlgorithm+"/"+f.Mode, inputEvidence, tool, f.DurationUS, jsonString(summary), nowMS()); err != nil {
		return err
	}
	for _, a := range out.Artifacts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO analysis_artifacts(id,result_id,digest,size,kind,mime,ordinal,start_us,end_us,width,height) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, token(id, fmt.Sprint(a.Ordinal), a.Kind), id, a.Object.Digest, a.Object.Size, a.Kind, a.MIME, a.Ordinal, a.StartUS, a.EndUS, a.Width, a.Height); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE analysis_results SET retired_ms=? WHERE id IN(SELECT result_id FROM analysis_heads WHERE object_id=? AND source_revision=? AND stage=?) AND retired_ms=0`, nowMS(), f.ObjectID, f.Revision, SegmentStage); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO analysis_heads(object_id,source_revision,stage,result_id) VALUES(?,?,?,?) ON CONFLICT(object_id,source_revision,stage) DO UPDATE SET result_id=excluded.result_id`, f.ObjectID, f.Revision, SegmentStage, id); err != nil {
		return err
	}
	if _, err = replaceDetectorMarkers(ctx, tx, f, id, fileFamily, detections, false); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE segment_files SET state='complete',attempt=0,next_ms=0,error_code='',result_id=?,show_id=?,match_digest='',updated_ms=? WHERE object_id=? AND source_revision=?`, id, f.ShowID, nowMS(), f.ObjectID, f.Revision); err != nil {
		return err
	}
	if err = markShowDirty(ctx, tx, f.ShowID); err != nil {
		return err
	}
	return gated.Commit()
}

func markShowDirty(ctx context.Context, tx *sql.Tx, show int64) error {
	if show <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO segment_shows(show_id,dirty,updated_ms) VALUES(?,1,?) ON CONFLICT(show_id) DO UPDATE SET dirty=1,updated_ms=excluded.updated_ms`, show, nowMS())
	return err
}

type storedMarker struct {
	id, kind, provenance      string
	start, end                int64
	approved, edited, deleted bool
}

// owned is a marker the owner decided on: made, adjusted, approved or
// dismissed. No detector changes or removes one.
func (m storedMarker) owned() bool { return m.approved || m.edited || m.deleted }

func overlaps(aStart, aEnd, bStart, bEnd int64) bool { return aStart < bEnd && bStart < aEnd }

// replaceDetectorMarkers makes one family's untouched markers on a file equal
// to detections. With supersede, a measured detection also retires an
// untouched single-file suggestion of the same kind it overlaps (a chapter
// title or the picture's guess, now confirmed or corrected by other
// episodes). It reports whether the file's markers changed; only a change
// moves the marker revision, so clients' marker sets stay valid otherwise.
func replaceDetectorMarkers(ctx context.Context, tx *sql.Tx, f segmentFile, resultID string, family func(string) bool, detections []segmentdetect.Detection, supersede bool) (bool, error) {
	binding := f.binding()
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,start_us,end_us,provenance,approved,edited,deleted FROM analysis_markers WHERE object_id=? AND source_revision=? AND source_binding=? LIMIT 1025`, f.ObjectID, f.Revision, binding)
	if err != nil {
		return false, err
	}
	var stored []storedMarker
	for rows.Next() {
		var m storedMarker
		if err = rows.Scan(&m.id, &m.kind, &m.start, &m.end, &m.provenance, &m.approved, &m.edited, &m.deleted); err != nil {
			rows.Close()
			return false, err
		}
		stored = append(stored, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return false, err
	}
	if len(stored) > 1024 {
		return false, ErrBudget
	}
	type wanted struct {
		id string
		d  segmentdetect.Detection
	}
	var desired []wanted
	for _, d := range detections {
		if d.StartUS < 0 || d.EndUS <= d.StartUS || d.EndUS > f.DurationUS || !family(d.Provenance) || len(desired) >= 64 {
			continue
		}
		owned := false
		for _, m := range stored {
			owned = owned || (m.approved || m.edited) && m.kind == d.Kind && overlaps(m.start, m.end, d.StartUS, d.EndUS)
		}
		if !owned {
			desired = append(desired, wanted{token(f.ObjectID, f.Revision, binding, d.Kind, fmt.Sprint(d.StartUS), fmt.Sprint(d.EndUS), d.Provenance), d})
		}
	}
	keep := map[string]bool{}
	for _, w := range desired {
		keep[w.id] = true
	}
	var remove []string
	present := map[string]bool{}
	for _, m := range stored {
		if m.owned() {
			continue
		}
		switch {
		case family(m.provenance):
			present[m.id] = true
			if !keep[m.id] {
				remove = append(remove, m.id)
			}
		case supersede && fileFamily(m.provenance):
			for _, w := range desired {
				if w.d.Kind == m.kind && overlaps(m.start, m.end, w.d.StartUS, w.d.EndUS) {
					remove = append(remove, m.id)
					break
				}
			}
		}
	}
	added := 0
	for id := range keep {
		if !present[id] {
			added++
		}
	}
	if len(remove) == 0 && added == 0 {
		return false, nil
	}
	sort.Strings(remove)
	for _, id := range remove {
		if _, err = tx.ExecContext(ctx, `DELETE FROM analysis_markers WHERE id=? AND approved=0 AND edited=0 AND deleted=0`, id); err != nil {
			return false, err
		}
	}
	for _, w := range desired {
		// An identical candidate the owner once dismissed stays dismissed:
		// its row exists and is ignored here.
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,title) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, w.id, f.ObjectID, f.Revision, binding, w.d.Kind, w.d.StartUS, w.d.EndUS, w.d.Confidence, w.d.Provenance, resultID, w.d.Kind); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_marker_sets(object_id,source_revision) VALUES(?,?)`, f.ObjectID, f.Revision); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE analysis_marker_sets SET revision=revision+1 WHERE object_id=? AND source_revision=?`, f.ObjectID, f.Revision)
	return err == nil, err
}
