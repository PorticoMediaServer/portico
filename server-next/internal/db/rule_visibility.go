package db

import (
	"context"
	"time"
)

// RuleRatingVisible uses current matching airings, including one already on now.
// An empty guide falls back to the creating programme's saved rating; an unknown
// fallback is not visible under a rating limit. Current unrated guide entries are allowed.
func (d *DB) RuleRatingVisible(ctx context.Context, rule RecordingRule, maxAge *int, now time.Time) (bool, error) {
	if maxAge == nil {
		return true, nil
	}
	type listing struct {
		source int64
		key    string
	}
	channels, err := d.read.QueryContext(ctx, `SELECT guide_source_id, guide_key FROM channels
		WHERE source_id = ? AND (? = 0 OR id = ?) AND guide_source_id IS NOT NULL AND guide_key IS NOT NULL`, rule.SourceID, rule.ChannelID, rule.ChannelID)
	if err != nil {
		return false, err
	}
	keys := map[listing]bool{}
	for channels.Next() {
		var key listing
		if err := channels.Scan(&key.source, &key.key); err != nil {
			channels.Close()
			return false, err
		}
		keys[key] = true
	}
	err = channels.Err()
	channels.Close()
	if err != nil {
		return false, err
	}
	column, value := "series_key", rule.SeriesKey
	if value == "" {
		column, value = "title", rule.Title
	}
	rows, err := d.guide.read.QueryContext(ctx, `SELECT source_id, guide_key, rating_age FROM programmes WHERE `+column+` = ? AND ends_at > ?`, value, now.UnixMilli())
	if err != nil {
		return false, err
	}
	defer rows.Close()
	known := false
	for rows.Next() {
		var key listing
		var age *int
		if err := rows.Scan(&key.source, &key.key, &age); err != nil {
			return false, err
		}
		if !keys[key] {
			continue
		}
		known = true
		if age == nil || *age <= *maxAge {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return !known && rule.RatingAge != nil && *rule.RatingAge <= *maxAge, nil
}
