package db

import (
	"context"
	"database/sql"
	"slices"
	"time"
)

// periodicalFacts are what a magazine's or newspaper's archive says under its name (phone-read
// 24: "Experimenter Publishing · Monthly · 1926 to 1929"): its publisher, from the series' own
// studio (an owner's edit) or else the one most of its issues' files name, and how often it
// comes out, from the dates of the issues the profile may see. Both are "" when unknown, and
// for a series of books.
func periodicalFacts(ctx context.Context, tx *sql.Tx, acc access, series int64, own []Tag) (publisher, frequency string, err error) {
	issues := `SELECT v.id, ifnull(v.release_date, '') FROM items v WHERE ` + acc.visible("v") + ` AND v.publication IN (?, ?)
		AND (v.parent_id = ? OR v.id IN (SELECT related_item_id FROM item_relations WHERE kind = ? AND item_id = ?))`
	args := []any{PublicationMagazine, PublicationNewspaper, series, RelationSeriesMember, series}
	rows, err := tx.QueryContext(ctx, issues, args...)
	if err != nil {
		return "", "", err
	}
	var dates []time.Time
	held := 0
	for rows.Next() {
		var id int64
		var date string
		if err := rows.Scan(&id, &date); err != nil {
			rows.Close()
			return "", "", err
		}
		held++
		for _, layout := range []string{"2006-01-02", "2006-01"} {
			if t, err := time.Parse(layout, date); err == nil {
				dates = append(dates, t)
				break
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || held == 0 {
		return "", "", err
	}
	for _, t := range own {
		if t.Kind == TagStudio {
			publisher = t.Name
			break
		}
	}
	if publisher == "" {
		err = tx.QueryRowContext(ctx, `SELECT t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id
			WHERE t.kind = 'studio' AND it.item_id IN (SELECT id FROM (`+issues+`))
			GROUP BY t.id ORDER BY count(*) DESC, t.name LIMIT 1`, args...).Scan(&publisher)
		if err == sql.ErrNoRows {
			err = nil
		}
	}
	return publisher, issueFrequency(dates), err
}

// issueFrequency names how often a run of issues comes out, only when the dates say so
// plainly: at least three dated issues, and at least two thirds of the gaps between them in one
// rhythm. A run with many issues missing, or several issues dated only by the same month (a
// weekly with month-only dates), says nothing rather than something wrong.
func issueFrequency(dates []time.Time) string {
	slices.SortFunc(dates, func(a, b time.Time) int { return a.Compare(b) })
	before := len(dates)
	dates = slices.CompactFunc(dates, func(a, b time.Time) bool { return a.Equal(b) })
	if len(dates) < 3 || len(dates) < before {
		return ""
	}
	for _, rhythm := range []struct {
		name     string
		from, to float64
	}{{"daily", 1, 1.5}, {"weekly", 6, 8}, {"fortnightly", 13, 16}, {"monthly", 27, 32}, {"bimonthly", 58, 63},
		{"quarterly", 88, 93}, {"annual", 360, 370}} {
		in := 0
		for n := 1; n < len(dates); n++ {
			if gap := dates[n].Sub(dates[n-1]).Hours() / 24; gap >= rhythm.from && gap <= rhythm.to {
				in++
			}
		}
		if in*3 >= (len(dates)-1)*2 {
			return rhythm.name
		}
	}
	return ""
}
