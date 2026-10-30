package reading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// Series pages are chosen before any group is evaluated. A series id is a hash
// of its library and key (compactcatalog.ReadingSeriesID), so within one library
// (series_key, series_id) order is series_key order and each key names exactly
// one series. Selection walks distinct keys in index order and asks one bounded
// EXISTS per candidate; only the selected groups are then labelled, counted and
// previewed. Page cost follows the series visited, never the whole library.

type seriesCandidate struct{ Key, ID string }

// seriesAnchorTx reads the key of a cursor's anchor member from the same ready
// generation. Members of a ready generation are immutable, so the anchor's key
// is exact even if the member has since become invisible.
func seriesAnchorTx(ctx context.Context, tx *sql.Tx, handle, generation, item int64) (seriesCandidate, error) {
	var out seriesCandidate
	err := tx.QueryRowContext(ctx, `SELECT series_key,series_id FROM reading_attribution_members WHERE library_id=? AND generation=? AND item_id=? AND series_id IS NOT NULL`, handle, generation, item).Scan(&out.Key, &out.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrChanged
	}
	return out, err
}

// selectSeries takes candidates from next in order and keeps those present
// reports, stopping after want of them.
func selectSeries(next func() (seriesCandidate, bool, error), present func(string) (bool, error), want int) ([]seriesCandidate, error) {
	out := []seriesCandidate{}
	for len(out) < want {
		candidate, ok, err := next()
		if err != nil || !ok {
			return out, err
		}
		if strings.ContainsRune(candidate.Key, 0) || len(candidate.ID) != 43 {
			return nil, ErrChanged
		}
		found, err := present(candidate.ID)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, candidate)
		}
	}
	return out, nil
}

// librarySeriesWalk visits every accepted series of one ready generation in key
// order, strictly after the given key when after is set, one index seek each.
func librarySeriesWalk(ctx context.Context, tx *sql.Tx, handle, generation int64, after *string) (func() (seriesCandidate, bool, error), func(), error) {
	first, err := tx.PrepareContext(ctx, `SELECT series_key,series_id FROM reading_attribution_members INDEXED BY reading_attribution_series_facet WHERE library_id=? AND generation=? AND series_key>=? ORDER BY series_key LIMIT 1`)
	if err != nil {
		return nil, nil, err
	}
	following, err := tx.PrepareContext(ctx, `SELECT series_key,series_id FROM reading_attribution_members INDEXED BY reading_attribution_series_facet WHERE library_id=? AND generation=? AND series_key>? ORDER BY series_key LIMIT 1`)
	if err != nil {
		first.Close()
		return nil, nil, err
	}
	var last *string
	if after != nil {
		key := *after
		last = &key
	}
	next := func() (seriesCandidate, bool, error) {
		var out seriesCandidate
		var err error
		if last == nil {
			err = first.QueryRowContext(ctx, handle, generation, "").Scan(&out.Key, &out.ID)
		} else {
			err = following.QueryRowContext(ctx, handle, generation, *last).Scan(&out.Key, &out.ID)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		key := out.Key
		last = &key
		return out, true, nil
	}
	return next, func() { first.Close(); following.Close() }, nil
}

// periodicalSeriesWalk lists the series that have members of one publication
// type by skip-scanning the issue-date index (one seek per series of that type,
// not per issue or per book series), then orders them by key. Periodical series
// are few beside a library's titles; the list is bounded by them.
func periodicalSeriesWalk(ctx context.Context, tx *sql.Tx, handle, generation int64, publicationType string, after *string) (func() (seriesCandidate, bool, error), error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE walk(id) AS(
 SELECT (SELECT min(series_id) FROM reading_attribution_members INDEXED BY reading_attribution_issue_date WHERE library_id=?1 AND generation=?2 AND publication_type=?3 AND series_id IS NOT NULL)
 UNION ALL
 SELECT (SELECT min(series_id) FROM reading_attribution_members INDEXED BY reading_attribution_issue_date WHERE library_id=?1 AND generation=?2 AND publication_type=?3 AND series_id>walk.id) FROM walk WHERE walk.id IS NOT NULL)
 SELECT walk.id,(SELECT k.series_key FROM reading_attribution_members k INDEXED BY reading_attribution_series_seek WHERE k.library_id=?1 AND k.generation=?2 AND k.series_id=walk.id LIMIT 1) FROM walk WHERE walk.id IS NOT NULL`, handle, generation, publicationType)
	if err != nil {
		return nil, err
	}
	var all []seriesCandidate
	for rows.Next() {
		var c seriesCandidate
		if err = rows.Scan(&c.ID, &c.Key); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Key != all[j].Key {
			return all[i].Key < all[j].Key
		}
		return all[i].ID < all[j].ID
	})
	at := 0
	if after != nil {
		at = sort.Search(len(all), func(i int) bool { return all[i].Key > *after })
	}
	return func() (seriesCandidate, bool, error) {
		if at >= len(all) {
			return seriesCandidate{}, false, nil
		}
		at++
		return all[at-1], true, nil
	}, nil
}

// presentSeries prepares one EXISTS over a series' eligible, visible members.
// from must end in a WHERE clause whose first three parameters are the library
// handle, generation and series id; args follow them.
func presentSeries(ctx context.Context, tx *sql.Tx, from string, handle, generation int64, args []any) (func(string) (bool, error), func(), error) {
	statement, err := tx.PrepareContext(ctx, `SELECT EXISTS(SELECT 1`+from+`)`)
	if err != nil {
		return nil, nil, err
	}
	return func(series string) (bool, error) {
		var found bool
		values := append([]any{handle, generation, series}, args...)
		err := statement.QueryRowContext(ctx, values...).Scan(&found)
		return found, err
	}, func() { statement.Close() }, nil
}

func seriesIDsJSON(selected []seriesCandidate) (string, error) {
	ids := make([]string, len(selected))
	for i := range selected {
		ids[i] = selected[i].ID
	}
	raw, err := json.Marshal(ids)
	return string(raw), err
}
