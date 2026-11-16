package db

import (
	"context"
	"slices"
	"strings"
)

// HomeRowItems opens a Home source as an ordinary item list, so See all keeps the library's
// sorts, filters, exact totals and cursors. Continue sources read only this profile's activity,
// not its whole libraries; the query rechecks visibility before returning or counting anything.
func (d *DB) HomeRowItems(ctx context.Context, v Viewer, row string, q ItemQuery) (ItemPage, error) {
	var continuing []Item
	var mood *MoodRow
	switch row {
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
			return ItemPage{}, err
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
			return d.Items(ctx, v, q)
		}
		choice, err := d.MoodRow(ctx, v, row)
		if err != nil {
			return ItemPage{}, err
		}
		mood = &choice
		q.homeIDs, q.homeUnplayed = choice.Source, true
		if q.Sort == "" {
			q.Sort, q.Desc = SortOrder, false
		}
	}

	if mood != nil && q.Sort == SortOrder {
		// Read short runs of the kept ids. A CASE over an entire large recommendation source
		// would compare every title with every id on each page.
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
			if at < 0 || after.Seed != mood.Version || q.At != 0 {
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
						Values: []any{int64(slices.Index(q.homeIDs, last.ID))}, Seed: mood.Version}).encode()
					return page, nil
				}
				page.Items = append(page.Items, it)
			}
		}
		return page, nil
	}
	page, err := d.Items(ctx, v, q)
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
