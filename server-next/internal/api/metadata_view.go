package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// metadataView reads a whole-view selection. Added order stays put when a refresh changes a
// title's name, or an edit changes the fields used to filter it.
func metadataView(req jobs.Request) (db.ItemQuery, error) {
	values, err := url.ParseQuery(req.Target.Filter)
	if err != nil {
		return db.ItemQuery{}, ErrField("target.filter", "This is not a list query.")
	}
	q, err := itemQuery(values)
	q.Sort, q.Desc, q.Cursor, q.At, q.Limit, q.WithTotal = db.SortAdded, false, "", 0, 200, false
	return q, err
}

func (s *Server) metadataSelection(ctx context.Context, req jobs.Request) ([]db.ItemKind, error) {
	v, err := s.db.MainViewer(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if len(req.Target.Items) > 0 {
		kinds, err := s.db.SeesItems(ctx, v, req.Target.Items)
		slices.Sort(kinds)
		return slices.Compact(kinds), err
	}
	if req.Target.Filter == "" {
		return nil, fmt.Errorf("%w: send titles or a view in target.filter", db.ErrInvalid)
	}
	q, err := metadataView(req)
	if err != nil {
		return nil, err
	}
	q.Limit = 1
	_, err = s.db.Items(ctx, v, q)
	return q.Kinds, err
}

func (s *Server) metadataPage(ctx context.Context, req jobs.Request, cursor string) ([]string, string, int64, error) {
	v, err := s.db.MainViewer(ctx, req.UserID)
	if err != nil {
		return nil, "", 0, err
	}
	if len(req.Target.Items) > 0 {
		var ids []string
		for _, id := range req.Target.Items {
			err := s.db.SeesItem(ctx, v, id)
			if errors.Is(err, db.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, "", 0, err
			}
			ids = append(ids, id)
		}
		return ids, "", int64(len(ids)), nil
	}
	q, err := metadataView(req)
	if err != nil {
		return nil, "", 0, err
	}
	q.Cursor, q.WithTotal = cursor, cursor == ""
	page, err := s.db.Items(ctx, v, q)
	ids := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		ids = append(ids, it.PublicID)
	}
	return ids, page.NextCursor, int64(page.Total), err
}

func (s *Server) refreshItems(ctx context.Context, work jobs.Work) (result error) {
	defer func() { result = s.metadataJobError(ctx, work.Job.PublicID, result) }()
	v, err := s.db.MainViewer(ctx, work.UserID)
	if err != nil {
		return err
	}
	var done, total int64
	cursor := ""
	libraries := map[int64]bool{}
	defer func() {
		if all, err := s.db.AllLibraries(ctx); err == nil {
			for _, lib := range all {
				if libraries[lib.ID] {
					s.fetchPicturesLater(ctx, lib.PublicID)
				}
			}
		}
	}()
	for {
		ids, next, count, err := s.metadataPage(ctx, work.Request, cursor)
		if err != nil {
			return err
		}
		if cursor == "" {
			total = count
		}
		refreshed, err := s.metadata.RefreshJobTitles(ctx, v, work.Job.ID, ids, time.UnixMilli(work.Job.CreatedAt), func(n int64) { work.Progress(done+n, max(total, done+n)) })
		for id := range refreshed {
			libraries[id] = true
		}
		if err != nil {
			return err
		}
		done += int64(len(ids))
		if next == "" {
			return s.selectionResult(ctx, work.Job.ID)
		}
		cursor = next
	}
}
