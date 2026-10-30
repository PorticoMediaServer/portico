package preparedmedia

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
)

// A whole-image track/total does not describe an extracted cue track. Resolve
// the exact captured range and its current numbered cohort in one snapshot;
// never attach a different item's metadata after a cue-sheet replacement.
func (s *Service) cueMusicTags(ctx context.Context, w work, tags audiotags.Tags) (audiotags.Tags, error) {
	source := w.Source
	if source.End == nil || source.Start < 0 || *source.End <= source.Start || math.IsNaN(source.Start) || math.IsInf(source.Start, 0) || math.IsNaN(*source.End) || math.IsInf(*source.End, 0) {
		return tags, ErrSourceChanged
	}
	var title string
	var ordinal, total, first, last int
	err := s.db.QueryRowContext(ctx, `SELECT entity.title,cue.ordinal,cohort.total,cohort.first,cohort.last
 FROM music_virtual_tracks cue JOIN catalog_entities entity ON entity.id=cue.entity_id AND entity.retired=0
 CROSS JOIN (SELECT COUNT(*) AS total,MIN(ordinal) AS first,MAX(ordinal) AS last FROM music_virtual_tracks WHERE asset_id=?) cohort
 WHERE cue.asset_id=? AND entity.public_id=pid_blob(?) AND cue.start_seconds=? AND cue.end_seconds=?`, source.Asset, source.Asset, w.ItemID, source.Start, *source.End).Scan(&title, &ordinal, &total, &first, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return tags, ErrSourceChanged
	}
	if err != nil {
		return tags, err
	}
	if first != 1 || last != total || total > 4096 || ordinal < 1 || ordinal > total {
		return tags, ErrSourceChanged
	}
	delete(tags.Values, "cuesheet")
	delete(tags.Values, "cue_sheet")
	tags.Values["title"] = []string{title}
	tags.Values["track"] = []string{strconv.Itoa(ordinal)}
	tags.Values["total_tracks"] = []string{strconv.Itoa(total)}
	return tags, nil
}
