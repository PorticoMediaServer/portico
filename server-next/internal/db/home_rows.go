package db

import "context"

// HomeRowItems opens a Home source as an ordinary item list, so See all keeps the library's
// sorts, filters, exact totals and cursors. Continue sources read only this profile's activity,
// not its whole libraries; the query rechecks visibility before returning or counting anything.
func (d *DB) HomeRowItems(ctx context.Context, v Viewer, row string, q ItemQuery) (ItemPage, error) {
	var continuing []Item
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
		// Only a row currently offered to this profile is addressable. Its label and reason
		// were checked by the API; the query reads its full tag source rather than its preview.
		q.homeMood = row
		if q.Sort == "" {
			q.Sort, q.Desc = SortCommunityRating, true
		}
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
