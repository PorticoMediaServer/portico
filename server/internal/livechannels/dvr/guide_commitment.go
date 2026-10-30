package dvr

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"strings"
	"time"
)

// An explicit commitment survives a missing guide row. A unique nearby airing
// with matching programme facts can follow a provider's schedule correction.
// Ambiguous matches retain the accepted interval instead of choosing a rerun.
func (s *Store) committedProgrammeTx(ctx context.Context, tx *sql.Tx, r Recording, generation string) (livechannels.Programme, error) {
	p, e := s.live.ProgrammeTx(ctx, tx, r.Occurrence.SourceID, r.Occurrence.ChannelID, generation, r.Occurrence.ProgrammeID)
	if !errors.Is(e, livechannels.ErrConflict) {
		return p, e
	}
	start, e := time.Parse(time.RFC3339Nano, r.Programme.Start)
	if e != nil {
		return p, e
	}
	rows, e := tx.QueryContext(ctx, programmeQuery+` WHERE p.generation_id=? AND p.channel_id=? AND p.start_utc>=? AND p.start_utc<=? ORDER BY p.start_utc LIMIT 33`, generation, r.Occurrence.ChannelID, start.Add(-30*time.Minute).UTC().Format(time.RFC3339Nano), start.Add(30*time.Minute).UTC().Format(time.RFC3339Nano))
	if e != nil {
		return p, e
	}
	defer rows.Close()
	matches := []livechannels.Programme{}
	count := 0
	for rows.Next() {
		candidate, err := scanProgramme(rows)
		if err != nil {
			return p, err
		}
		count++
		if sameCommittedProgramme(r.Programme, candidate) {
			matches = append(matches, candidate)
		}
	}
	if e = rows.Err(); e != nil {
		return p, e
	}
	if count <= 32 && len(matches) == 1 {
		return matches[0], nil
	}
	return r.Programme, nil
}
func sameCommittedProgramme(a, b livechannels.Programme) bool {
	if a.EpisodeID != "" || b.EpisodeID != "" {
		return a.EpisodeID != "" && a.EpisodeID == b.EpisodeID
	}
	if a.Title == "" || !strings.EqualFold(strings.TrimSpace(a.Title), strings.TrimSpace(b.Title)) || a.SeriesID != b.SeriesID || a.Subtitle != b.Subtitle {
		return false
	}
	if a.Episode != nil || b.Episode != nil {
		return a.Episode != nil && b.Episode != nil && *a.Episode == *b.Episode
	}
	return true
}
