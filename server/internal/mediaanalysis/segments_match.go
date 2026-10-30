package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
)

// matchAlgorithm names how neighbours are chosen and compared; a change
// re-compares every show.
const matchAlgorithm = "season-neighbours-1"

// showFile is one measured episode file of a show.
type showFile struct {
	ObjectID, Revision, ResultID, Digest string
	Episode                              int64
	Season, Number                       int // Season -1: numbered across the show
}

// segmentNeighbours picks the episodes a file is compared with: the nearest
// episodes of its own season and, when its season is too small to confirm
// anything, the nearest of the seasons around it (an intro often outlives a
// season). Another file of the same episode (a second version) is never a
// neighbour: it would agree with everything.
func segmentNeighbours(files []showFile, i, limit int) []int {
	self := files[i]
	seasonDistance := func(season int) int {
		switch {
		case season == self.Season:
			return 0
		case season == 0 || self.Season == 0:
			return 50 // specials and regular seasons rarely share an intro
		case season > self.Season:
			return season - self.Season
		}
		return self.Season - season
	}
	type candidate struct{ index, season, number int }
	var same, other []candidate
	seen := map[int64]bool{self.Episode: true}
	for j, f := range files {
		if seen[f.Episode] {
			continue
		}
		seen[f.Episode] = true
		d := f.Number - self.Number
		if d < 0 {
			d = -d
		}
		c := candidate{j, seasonDistance(f.Season), d}
		if c.season == 0 {
			same = append(same, c)
		} else {
			other = append(other, c)
		}
	}
	byDistance := func(list []candidate) {
		sort.SliceStable(list, func(a, b int) bool {
			if list[a].season != list[b].season {
				return list[a].season < list[b].season
			}
			return list[a].number < list[b].number
		})
	}
	byDistance(same)
	byDistance(other)
	var out []int
	for _, c := range same {
		if len(out) == limit {
			break
		}
		out = append(out, c.index)
	}
	const enough = 4
	for _, c := range other {
		if len(out) >= enough || len(out) == limit {
			break
		}
		out = append(out, c.index)
	}
	return out
}

func showFilesTx(ctx context.Context, q queryer, show int64) ([]showFile, error) {
	planning, err := segmentPlanningTx(ctx, q)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT f.object_id,f.source_revision,f.result_id,f.match_digest,e.entity_id,COALESCE(se.number,-1),e.number
 FROM segment_files f JOIN inventory_objects o ON o.id=f.object_id AND o.revision=f.source_revision
 JOIN catalog_episodes e ON e.entity_id=(SELECT ia.entity_id FROM catalog_assets a JOIN catalog_asset_links ia ON ia.asset_id=a.id JOIN catalog_episodes x ON x.entity_id=ia.entity_id WHERE a.token=o.asset_id AND x.show_id=f.show_id ORDER BY ia.part_index,ia.entity_id LIMIT 1)
 LEFT JOIN catalog_seasons se ON se.entity_id=e.season_id
 WHERE f.show_id=? AND f.planning_policy=? AND f.state='complete' AND f.result_id<>''
  AND EXISTS(SELECT 1 FROM analysis_heads h JOIN analysis_results r ON r.id=h.result_id WHERE h.object_id=f.object_id AND h.source_revision=f.source_revision AND h.stage=? AND r.id=f.result_id AND r.retired_ms=0)
 ORDER BY 6,7,f.object_id LIMIT 4097`, show, planning.digest(), SegmentStage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []showFile
	for rows.Next() {
		var f showFile
		if err = rows.Scan(&f.ObjectID, &f.Revision, &f.ResultID, &f.Digest, &f.Episode, &f.Season, &f.Number); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if len(out) > 4096 {
		return nil, ErrBudget
	}
	return out, rows.Err()
}

// segmentShowStep compares the next dirty show whose files are all measured.
// A file whose own evidence and neighbours are unchanged since its last
// comparison is skipped, so a new episode re-compares only itself and the
// episodes around it.
func (s *Service) segmentShowStep(ctx context.Context) (bool, error) {
	var show int64
	err := dbwork.QueryRow(ctx, s.db, `SELECT show_id FROM segment_shows s WHERE dirty=1 AND NOT EXISTS(SELECT 1 FROM segment_files f WHERE f.show_id=s.show_id AND f.state='pending') ORDER BY updated_ms,show_id LIMIT 1`).Scan(&show)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return false, err
	}
	files, err := showFilesTx(ctx, snapshot.Tx(), show)
	snapshot.Rollback()
	if err != nil {
		return false, err
	}
	type job struct {
		index      int
		neighbours []int
		digest     string
	}
	var todo []job
	for i := range files {
		neighbours := segmentNeighbours(files, i, segmentNeighbourCount)
		ids := make([]string, 0, len(neighbours)+2)
		ids = append(ids, matchAlgorithm, files[i].ResultID)
		var rest []string
		for _, j := range neighbours {
			rest = append(rest, files[j].ResultID)
		}
		sort.Strings(rest)
		digest := token(append(ids, rest...)...)
		if digest != files[i].Digest {
			todo = append(todo, job{i, neighbours, digest})
		}
	}
	if len(todo) == 0 {
		_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE segment_shows SET dirty=0,matched_ms=?,updated_ms=? WHERE show_id=? AND dirty=1`, nowMS(), nowMS(), show)
		return true, err
	}
	cache := map[string]segmentdetect.Evidence{}
	load := func(f showFile) (segmentdetect.Evidence, bool, error) {
		if e, ok := cache[f.ResultID]; ok {
			return e, true, nil
		}
		e, err := s.segmentEvidence(ctx, f.ResultID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, segmentdetect.ErrInput) {
			// The stored measurement is gone or unreadable: measure again.
			_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE segment_files SET state='pending',attempt=0,next_ms=0,result_id='',updated_ms=? WHERE object_id=? AND result_id=?`, nowMS(), f.ObjectID, f.ResultID)
			return e, false, err
		}
		if err != nil {
			return e, false, err
		}
		cache[f.ResultID] = e
		return e, true, nil
	}
	for _, j := range todo[:min(len(todo), segmentMatchBatch)] {
		if err = ctx.Err(); err != nil {
			return true, err
		}
		self := files[j.index]
		evidence, ok, err := load(self)
		if err != nil || !ok {
			return true, err
		}
		var openings, closings []segmentdetect.Fingerprint
		for _, n := range j.neighbours {
			e, ok, err := load(files[n])
			if err != nil {
				return true, err
			}
			if !ok {
				continue
			}
			if len(e.Opening.Points) > 0 {
				openings = append(openings, e.Opening)
			}
			if len(e.Closing.Points) > 0 {
				closings = append(closings, e.Closing)
			}
		}
		var detections []segmentdetect.Detection
		if len(evidence.Opening.Points) > 0 && len(openings) > 0 {
			if d, ok := segmentdetect.Intro(evidence.Opening, segmentdetect.MatchRegion(evidence.Opening, openings), evidence.DurationUS); ok {
				detections = append(detections, d)
			}
		}
		if len(evidence.Closing.Points) > 0 && len(closings) > 0 {
			if d, ok := segmentdetect.Credits(evidence.Closing, segmentdetect.MatchRegion(evidence.Closing, closings), evidence.DurationUS); ok {
				detections = append(detections, d)
			}
		}
		if err = s.publishSeasonMarkers(ctx, self, j.digest, detections); err != nil {
			return true, err
		}
	}
	return true, nil
}

// publishSeasonMarkers replaces one file's season markers and records the
// evidence they were made from.
func (s *Service) publishSeasonMarkers(ctx context.Context, file showFile, digest string, detections []segmentdetect.Detection) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	f, err := segmentFileTx(ctx, tx, file.ObjectID, file.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return gated.Commit() // changed since; its next measurement re-compares
	}
	if err != nil {
		return err
	}
	planning, err := segmentPlanningTx(ctx, tx)
	if err != nil {
		return err
	}
	var current, policy string
	if err = tx.QueryRowContext(ctx, `SELECT result_id,planning_policy FROM segment_files WHERE object_id=?`, file.ObjectID).Scan(&current, &policy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return gated.Commit()
		}
		return err
	}
	if current != file.ResultID || policy != planning.digest() {
		return gated.Commit()
	}
	if _, err = replaceDetectorMarkers(ctx, tx, f, file.ResultID, seasonFamily, detections, true); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE segment_files SET match_digest=?,updated_ms=? WHERE object_id=? AND result_id=?`, digest, nowMS(), file.ObjectID, file.ResultID); err != nil {
		return err
	}
	return gated.Commit()
}

// segmentEvidence reads one stored measurement.
func (s *Service) segmentEvidence(ctx context.Context, result string) (segmentdetect.Evidence, error) {
	var obj mediaartifact.Object
	if err := dbwork.QueryRow(ctx, s.db, `SELECT digest,size FROM analysis_artifacts WHERE result_id=? AND kind=? ORDER BY ordinal LIMIT 1`, result, SegmentStage).Scan(&obj.Digest, &obj.Size); err != nil {
		return segmentdetect.Evidence{}, err
	}
	if obj.Size > 2<<20 {
		return segmentdetect.Evidence{}, ErrBudget
	}
	s.publication.Lock()
	release, err := s.custody.read(obj.Digest)
	if err != nil {
		s.publication.Unlock()
		return segmentdetect.Evidence{}, err
	}
	defer release()
	reader, err := s.artifacts.Open(ctx, obj)
	s.publication.Unlock()
	if errors.Is(err, fs.ErrNotExist) {
		return segmentdetect.Evidence{}, sql.ErrNoRows
	}
	if err != nil {
		return segmentdetect.Evidence{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, obj.Size+1))
	if err != nil {
		return segmentdetect.Evidence{}, err
	}
	if int64(len(data)) != obj.Size {
		return segmentdetect.Evidence{}, mediaartifact.ErrIdentity
	}
	return segmentdetect.UnmarshalEvidence(data)
}
