package db

import (
	"context"
	"strings"
)

// HomeQueueSource is the row a queue started from. Query-backed rows keep their query;
// Continue and recommendation rows keep the chosen ids so playing does not reorder them.
// It is saved in the queue's existing selection, never accepted from an app.
type HomeQueueSource struct {
	Query    ItemQuery
	IDs      []int64 `json:",omitempty"`
	Recent   bool    `json:",omitempty"`
	Released bool    `json:",omitempty"`
	Popular  int64   `json:",omitempty"`
	Unplayed bool    `json:",omitempty"`
}

func (s HomeQueueSource) ItemQuery() ItemQuery {
	q := s.Query
	q.homeIDs, q.homeRecent, q.homeReleased, q.homeUnplayed = s.IDs, s.Recent, s.Released, s.Unplayed
	q.homePopular = s.Popular
	return q
}

func (d *DB) HomeQueueSource(ctx context.Context, v Viewer, row string, q ItemQuery) (HomeQueueSource, error) {
	if row == "" { // an already opened saved view, checked by the API
		if len(q.Kinds) == 0 {
			q.Kinds = []ItemKind{KindMovie, KindEpisode, KindTrack, KindAudiobook}
		}
		return HomeQueueSource{Query: q}, nil
	}
	var err error
	if row == "trending" || strings.HasPrefix(row, "because:") {
		// A ranked row is held as the titles See all shows now, in its order and up to the queue's
		// limit, so playing does not rank it again.
		page, ids := q, []int64{}
		page.Cursor, page.Limit = "", maxPageSize
		for len(ids) < QueueMax {
			got, err := d.HomeRowItems(ctx, v, row, page)
			if err != nil {
				return HomeQueueSource{}, err
			}
			for _, item := range got.Items {
				ids = append(ids, item.ID)
			}
			if got.NextCursor == "" || len(got.Items) == 0 {
				break
			}
			page.Cursor = got.NextCursor
		}
		q.homeIDs, q.Sort, q.Desc = ids, SortOrder, false
	} else {
		q, _, _, err = d.homeRowQuery(ctx, v, row, q)
	}
	if err != nil {
		return HomeQueueSource{}, err
	}
	if len(q.Kinds) == 0 {
		q.Kinds = []ItemKind{KindMovie, KindShow, KindSeason, KindArtist, KindAlbum, KindEpisode, KindTrack, KindAudiobook}
	}
	return HomeQueueSource{Query: q, IDs: q.homeIDs, Recent: q.homeRecent, Released: q.homeReleased, Popular: q.homePopular, Unplayed: q.homeUnplayed}, nil
}

// queueCard reads one ranked Home card using only a short run of the kept ids. A CASE over
// an entire recommendation ranking would make every refill sort the whole ranking again.
func (d *DB) queueCard(ctx context.Context, v Viewer, q ItemQuery) (ItemPage, error) {
	if q.homeIDs == nil || q.Sort != SortOrder {
		return d.Items(ctx, v, q)
	}
	after, err := decodeCursor(q.Cursor, SortOrder, q.Desc)
	if err != nil {
		return ItemPage{}, err
	}
	at, step := 0, 1
	if q.Desc {
		at, step = len(q.homeIDs)-1, -1
	}
	if after != nil {
		if after.Nulls || len(after.Values) != 1 {
			return ItemPage{}, ErrCursorExpired
		}
		index, ok := after.Values[0].(int64)
		if !ok || index < 0 || index >= int64(len(q.homeIDs)) || q.homeIDs[index] != after.ID {
			return ItemPage{}, ErrCursorExpired
		}
		at = int(index) + step
	}
	for at >= 0 && at < len(q.homeIDs) {
		batch := make([]int64, 0, maxPageSize)
		positions := map[int64]int{}
		for len(batch) < maxPageSize && at >= 0 && at < len(q.homeIDs) {
			id := q.homeIDs[at]
			positions[id] = at
			batch = append(batch, id)
			at += step
		}
		read := q
		read.homeIDs, read.Desc, read.Cursor = batch, false, ""
		page, err := d.Items(ctx, v, read)
		if err != nil {
			return page, err
		}
		if len(page.Items) > 0 {
			card := page.Items[0]
			place := positions[card.ID]
			page.NextCursor = ""
			if place+step >= 0 && place+step < len(q.homeIDs) {
				page.NextCursor = (listCursor{Sort: SortOrder, Desc: q.Desc, ID: card.ID, Values: []any{int64(place)}}).encode()
			}
			return page, nil
		}
	}
	return ItemPage{}, nil
}
