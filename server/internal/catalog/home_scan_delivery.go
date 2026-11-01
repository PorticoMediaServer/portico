package catalog

import (
	"context"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// HomeRows coalesces composition while one of this scope's scans is running.
// Every caller still enters its own read/authority snapshot. Cached documents
// keep their original catalogue revision, rather than claiming a newer build.
func (s *Service) HomeRows(r HomeRequest) (HomeDocument, error) {
	return s.homeDocumentDuringScan(r, false)
}

// HomeManifest shares the same scan freshness for its spotlight. Its deferred
// rows still obtain current offers and permissions in the HTTP projection.
func (s *Service) HomeManifest(r HomeRequest) (HomeDocument, error) {
	return s.homeDocumentDuringScan(r, true)
}

func (s *Service) homeDocumentDuringScan(r HomeRequest, manifest bool) (HomeDocument, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out HomeDocument
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).homeDocumentDuringScan(r, manifest)
			return err
		})
		return out, err
	}
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return HomeDocument{}, err
	}
	r.Now = s.recommendationNow(r.Now)
	surface := "document"
	compose := s.homeRowsFresh
	if manifest {
		surface = "manifest"
		compose = s.homeManifestFresh
	}
	key, err := s.homeScanKey(r, surface, HomeRowPage{})
	if err != nil {
		return HomeDocument{}, err
	}
	if key == "" {
		return compose(r)
	}
	data, reused, err := s.state.homeScans.load(s.Context(), key, s.homeScanNow(), func() ([]byte, error) {
		out, err := compose(r)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	})
	if err != nil {
		return HomeDocument{}, err
	}
	var out HomeDocument
	if err = json.Unmarshal(data, &out); err != nil {
		return HomeDocument{}, err
	}
	if reused {
		if err = s.refreshCachedHomeRows(r, out.Rows); err != nil {
			return HomeDocument{}, err
		}
	}
	return out, nil
}

// Only first pages may reuse a composition. Continuations and ranged/anchored
// pages retain the ordinary live projection fences. Live TV always calls its
// current guide/authority owner, independently of catalogue scan freshness.
func (s *Service) HomeSingleRow(r HomeRequest, id string, page HomeRowPage) (HomeRow, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out HomeRow
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).HomeSingleRow(r, id, page)
			return err
		})
		return out, err
	}
	if id == "on_now" || page.Cursor != "" || page.Revision != "" || page.Start != 0 || page.AnchorID != "" {
		return s.homeSingleRowFresh(r, id, page)
	}
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return HomeRow{}, err
	}
	r.Now = s.recommendationNow(r.Now)
	key, err := s.homeScanKey(r, "row:"+id, page)
	if err != nil {
		return HomeRow{}, err
	}
	if key == "" {
		return s.homeSingleRowFresh(r, id, page)
	}
	data, reused, err := s.state.homeScans.load(s.Context(), key, s.homeScanNow(), func() ([]byte, error) {
		out, err := s.homeSingleRowFresh(r, id, page)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	})
	if err != nil {
		return HomeRow{}, err
	}
	var out HomeRow
	if err = json.Unmarshal(data, &out); err != nil {
		return HomeRow{}, err
	}
	if reused {
		rows := []HomeRow{out}
		if err = s.refreshCachedHomeRows(r, rows); err != nil {
			return HomeRow{}, err
		}
		out = rows[0]
	}
	return out, nil
}

// Reading personal projection is authorized afresh in this caller's snapshot,
// using only selected IDs. No stored callback can stand in for this proof.
// Live guide rows similarly retain their own current authority and expiry.
func (s *Service) refreshCachedHomeRows(r HomeRequest, rows []HomeRow) error {
	ids := []string{}
	entries := []*ContentEntry{}
	var collect func(*ContentEntry)
	collect = func(entry *ContentEntry) {
		if entry.Kind == "ebook" || entry.Kind == "comic" {
			ids = append(ids, entry.ID)
			entries = append(entries, entry)
		}
		if entry.Media != nil {
			collect(entry.Media)
		}
	}
	var visit func([]HomeRow) error
	visit = func(rows []HomeRow) error {
		for i := range rows {
			row := &rows[i]
			if row.ID == "on_now" && !row.Deferred {
				if r.OnNow == nil {
					return ErrHomeRowUnknown
				}
				live, err := r.OnNow(HomeRowPage{Limit: row.Limit})
				if err != nil {
					return err
				}
				row.Live, row.Total, row.NextCursor = live.Live, live.Total, live.NextCursor
				row.Start, row.HasMore, row.Limit, row.SubtitleText = live.Start, live.HasMore, live.Limit, live.SubtitleText
			}
			for j := range row.Entries {
				collect(&row.Entries[j])
			}
			for j := range row.Resumes {
				collect(&row.Resumes[j].Target)
			}
			if row.Collection != nil {
				for j := range row.Collection.Members {
					collect(&row.Collection.Members[j])
				}
			}
			if err := visit(row.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(rows); err != nil {
		return err
	}
	current, err := s.homeReadingPage(r.Viewer, homeUnique(ids), r.ReadingPersonalAuthorize)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fresh, ok := current[entry.ID]
		if !ok {
			return ErrReadingProjectionChanged
		}
		fresh.New, fresh.AddedAt = entry.New, entry.AddedAt
		*entry = fresh
	}
	return nil
}
