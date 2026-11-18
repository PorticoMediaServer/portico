package db

import (
	"context"
	"slices"
	"strings"
	"time"
)

// HomeRowFacets counts the same visible titles as a Home row's list. Only the
// ordinary catalogue rows offer facets; ranked rows and Continue keep their order.
func (d *DB) HomeRowFacets(ctx context.Context, v Viewer, row string, q ItemQuery, facet string) ([]FacetValue, error) {
	if row != "recently_added" && row != "recently_released" {
		return nil, invalid("This Home row does not have filter choices.")
	}
	if facet != FacetGenre && facet != FacetLetter {
		return nil, invalid("This Home row supports genre and letter counts.")
	}
	q, _, _, err := d.homeRowQuery(ctx, v, row, q)
	if err != nil {
		return nil, err
	}
	return d.Facets(ctx, v, q, facet)
}

// HomeRowItems opens a Home source as an ordinary item list, so See all keeps the library's
// sorts, filters, exact totals and cursors. Continue sources read only this profile's activity,
// not its whole libraries; the query rechecks visibility before returning or counting anything.
func (d *DB) HomeRowItems(ctx context.Context, v Viewer, row string, q ItemQuery) (ItemPage, error) {
	if row == "trending" {
		return d.TrendingPage(ctx, v, q)
	}
	if strings.HasPrefix(row, "because:") {
		return d.BecauseWatchedPage(ctx, v, row, q)
	}
	q, continuing, mood, err := d.homeRowQuery(ctx, v, row, q)
	if err != nil {
		return ItemPage{}, err
	}

	// A Continue or mood row's own order is its list of ids. Read it in short runs: one CASE
	// over a whole source of thousands of started songs or kept titles would compare every
	// title with every id on each page.
	var page ItemPage
	if q.homeIDs != nil && q.Sort == SortOrder {
		version := int64(0)
		if mood != nil {
			version = mood.Version
		}
		page, err = d.keptOrderPage(ctx, v, q, version)
	} else {
		page, err = d.Items(ctx, v, q)
	}
	if err != nil {
		return page, err
	}
	for n := range page.Items {
		for _, source := range continuing {
			if source.ID == page.Items[n].ID {
				page.Items[n].Chapter = source.Chapter
				break
			}
		}
	}
	return page, nil
}

// keptOrderPage is a page of a list in the order of q.homeIDs. version is the mood row's
// version its cursor carries (0 for a Continue row).
func (d *DB) keptOrderPage(ctx context.Context, v Viewer, q ItemQuery, version int64) (ItemPage, error) {
	after, err := decodeCursor(q.Cursor, SortOrder, q.Desc)
	if err != nil {
		return ItemPage{}, err
	}
	at, step := 0, 1
	if q.Desc {
		at, step = len(q.homeIDs)-1, -1
	}
	if after != nil {
		at = slices.Index(q.homeIDs, after.ID)
		if at < 0 || after.Seed != version || q.At != 0 {
			return ItemPage{}, ErrCursorExpired
		}
		at += step
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	page := ItemPage{Items: []Item{}}
	if q.WithTotal {
		count := q
		count.Sort, count.Cursor, count.At, count.Limit = SortAdded, "", 0, 1
		all, err := d.Items(ctx, v, count)
		if err != nil {
			return page, err
		}
		page.Total = all.Total
	}
	skip := q.At
	for at >= 0 && at < len(q.homeIDs) {
		batch := make([]int64, 0, maxPageSize)
		for len(batch) < maxPageSize && at >= 0 && at < len(q.homeIDs) {
			batch = append(batch, q.homeIDs[at])
			at += step
		}
		read := q
		read.homeIDs, read.Cursor, read.At, read.WithTotal, read.Desc, read.Limit = batch, "", 0, false, false, maxPageSize
		part, err := d.Items(ctx, v, read)
		if err != nil {
			return page, err
		}
		for _, it := range part.Items {
			if skip > 0 {
				skip--
				continue
			}
			if len(page.Items) == limit {
				last := page.Items[len(page.Items)-1]
				page.NextCursor = (listCursor{Sort: SortOrder, Desc: q.Desc, ID: last.ID,
					Values: []any{int64(slices.Index(q.homeIDs, last.ID))}, Seed: version}).encode()
				return page, nil
			}
			page.Items = append(page.Items, it)
		}
	}
	return page, nil
}

// homeRowQuery keeps See all and Play on the same source and default order.
func (d *DB) homeRowQuery(ctx context.Context, v Viewer, row string, q ItemQuery) (ItemQuery, []Item, *MoodRow, error) {
	var continuing []Item
	var mood *MoodRow
	switch row {
	case "popular":
		if q.Sort == "" {
			q.Sort, q.Desc = SortOrder, true
		}
		after, err := decodeCursor(q.Cursor, q.Sort, q.Desc)
		if err != nil {
			return q, nil, nil, err
		}
		now := q.HomeAt
		if now.IsZero() {
			now = time.Now()
		}
		q.homePopular = now.UnixMilli()
		if after != nil {
			if after.AsOf <= 0 || after.AsOf > q.homePopular {
				return q, nil, nil, ErrCursorExpired
			}
			q.homePopular = after.AsOf
		}
	case "recently_released":
		q.homeRecent, q.homeReleased = true, true
		if q.Sort == "" {
			q.Sort, q.Desc = SortRelease, true
		}
	case "recently_added":
		q.homeRecent = true
		if q.Sort == "" {
			q.Sort, q.Desc = SortAdded, true
		}
	case "continue", "continue_listening", "continue_books":
		kinds := watchingKinds
		if q.Library != "" {
			kinds = nil
		} else if row == "continue_listening" {
			kinds = listeningKinds
		} else if row == "continue_books" {
			kinds = bookKinds
		}
		var err error
		continuing, err = d.continueItems(ctx, v, kinds, q.Library, -1)
		if err != nil {
			return q, nil, nil, err
		}
		q.homeIDs = make([]int64, 0, len(continuing))
		for _, item := range continuing {
			q.homeIDs = append(q.homeIDs, item.ID)
		}
		if q.Sort == "" {
			q.Sort, q.Desc = SortOrder, false
		}
	default:
		if strings.HasPrefix(row, "collection:") {
			q.Collection = strings.TrimPrefix(row, "collection:")
			return q, nil, nil, nil
		}
		choice, err := d.MoodRow(ctx, v, row)
		if err != nil {
			return q, nil, nil, err
		}
		mood = &choice
		q.homeIDs, q.homeUnplayed = choice.Source, true
		if q.Sort == "" {
			q.Sort, q.Desc = SortOrder, false
		}
	}

	return q, continuing, mood, nil
}
