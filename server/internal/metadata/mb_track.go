package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type mbTrackProjection struct{ releaseRevision, releaseID, trackID, recordingRevision, recordingID, status string }

func readMBTrack(ctx context.Context, tx *sql.Tx, j mbJob, canonicalRecording string) (mbTrackProjection, error) {
	p := mbTrackProjection{releaseRevision: j.base.ReleaseRevision, releaseID: j.base.ReleaseID, status: "unmatched"}
	if p.releaseRevision == "" {
		return p, nil
	}
	if j.base.HintProblem != "" {
		p.status = j.base.HintProblem
		return p, nil
	}
	var title, artist string
	var disc, position int
	query := `SELECT t.track_id,t.recording_revision,r.provider_id,t.title,t.artist,t.medium_position,t.position FROM mb_release_tracks t JOIN mb_recording_evidence r ON r.revision_id=t.recording_revision AND r.sealed=1 JOIN mb_release_evidence re ON re.revision_id=t.release_revision AND re.sealed=1 WHERE t.release_revision=? AND `
	args := []any{p.releaseRevision}
	if hint := j.base.Hints["musicbrainz_releasetrackid"]; hint != "" {
		query += `t.track_id=?`
		args = append(args, hint)
	} else if j.base.Disc.Valid && j.base.Position.Valid {
		query += `t.medium_position=? AND t.position=?`
		args = append(args, j.base.Disc.Int64, j.base.Position.Int64)
	} else {
		return p, nil
	}
	e := tx.QueryRowContext(ctx, query, args...).Scan(&p.trackID, &p.recordingRevision, &p.recordingID, &title, &artist, &disc, &position)
	if errors.Is(e, sql.ErrNoRows) {
		p.status = "not_found"
		return p, nil
	}
	if e != nil {
		return p, e
	}
	p.status = "matched"
	if j.base.Hints["musicbrainz_releasetrackid"] == "" && (!strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(j.title)) || !strings.EqualFold(strings.TrimSpace(artist), strings.TrimSpace(j.artist))) {
		p.status = "title_artist_mismatch"
	}
	if (j.base.Disc.Valid && j.base.Disc.Int64 != int64(disc)) || (j.base.Position.Valid && j.base.Position.Int64 != int64(position)) {
		p.status = "position_conflict"
	}
	if canonicalRecording != "" && canonicalRecording != p.recordingID {
		p.status = "recording_conflict"
	}
	return p, nil
}
func nullableMB(v string) any {
	if v == "" {
		return nil
	}
	return v
}
