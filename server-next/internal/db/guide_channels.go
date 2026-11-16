package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// GuideForChannels reads each channel's own indexed guide range in one statement per short
// batch. Searching now/next across many channels must not make one SQL request per channel.
func (d *DB) GuideForChannels(ctx context.Context, channels []LiveChannel, from, to time.Time, limit int) ([][]GuideProgramme, error) {
	out := make([][]GuideProgramme, len(channels))
	for start := 0; start < len(channels); start += 100 {
		var wanted []string
		args := []any{sql.Named("from", from.Add(-48*time.Hour).UnixMilli()), sql.Named("to", to.UnixMilli()),
			sql.Named("now", from.UnixMilli()), sql.Named("limit", limit)}
		for n := start; n < min(start+100, len(channels)); n++ {
			c := channels[n]
			if c.Kind == ChannelFromSource && (c.GuideSource == 0 || c.GuideKey == "") {
				continue
			}
			wanted = append(wanted, fmt.Sprintf("(%d,@source%d,@key%d)", n, n, n))
			args = append(args, sql.Named(fmt.Sprint("source", n), c.GuideSource), sql.Named(fmt.Sprint("key", n), c.GuideKey))
		}
		if len(wanted) == 0 {
			continue
		}
		rows, err := d.guide.read.QueryContext(ctx, `WITH wanted(n, source, key) AS (VALUES `+strings.Join(wanted, ",")+`)
			SELECT w.n, p.starts_at, p.ends_at, p.programme_key, ifnull(p.series_key, ''), p.title, ifnull(p.episode_title, ''),
			p.season_number, p.episode_number, ifnull(p.summary, ''), ifnull(p.rating, ''), p.rating_age, ifnull(p.flags, 0),
			ifnull(p.image_url, ''), ifnull(p.item_id, 0)
			FROM wanted w CROSS JOIN programmes p ON p.source_id = w.source AND p.guide_key = w.key
			AND p.starts_at IN (SELECT starts_at FROM programmes pick WHERE pick.source_id = w.source AND pick.guide_key = w.key
				AND pick.starts_at > @from AND pick.starts_at < @to AND pick.ends_at > @now ORDER BY pick.starts_at LIMIT @limit)
			ORDER BY w.n, p.starts_at`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p GuideProgramme
			var n int
			var begins, ends int64
			if err := rows.Scan(&n, &begins, &ends, &p.ProgrammeKey, &p.SeriesKey, &p.Title, &p.EpisodeTitle, &p.Season, &p.Episode,
				&p.Summary, &p.Rating, &p.RatingAge, &p.Flags, &p.ImageURL, &p.ItemID); err != nil {
				rows.Close()
				return nil, err
			}
			p.SourceID, p.GuideKey = channels[n].GuideSource, channels[n].GuideKey
			p.StartsAt, p.EndsAt = time.UnixMilli(begins), time.UnixMilli(ends)
			out[n] = append(out[n], p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
