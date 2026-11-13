package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/segments"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"net/http"
	"sort"
	"strconv"
	"time"
)

func (s *Server) startSegments(ctx context.Context) {
	s.segments = &segments.Service{DB: s.db, Settings: s.settings, Tools: s.analysisTools, Remote: s.remote.Open, Now: s.now}
	s.jobs.Register(s.segments.Kind())
	s.jobs.Tidy("intro and credits detection", time.Minute, s.queuePendingSegments)
	settings.Segments.Watch(s.settings, func(settings.SegmentSettings) { s.goBackground(func() { _ = s.queuePendingSegments(ctx) }) })
	Handle(s, "GET /v1/admin/segments", Owner, s.segmentStatus)
	Handle(s, "GET /v1/admin/segments/titles", Owner, s.segmentTitles)
	Handle(s, "GET /v1/admin/segments/titles/{id}", Owner, s.segmentTitle)
	Handle(s, "POST /v1/admin/segments/titles/{id}/approve", Owner, s.approveSegments)
	Handle(s, "POST /v1/admin/segments/titles/{id}/redetect", Owner, s.redetectSegments)
	Handle(s, "POST /v1/admin/segments/redetect", Owner, s.redetectAllSegments)
	Handle(s, "POST /v1/admin/segments/retry", Owner, s.retryUnreadableSegments)
	Handle(s, "POST /v1/items/{id}/analysis/markers", Owner, s.editSegmentMarker)
}
func (s *Server) segmentLibrary(ctx context.Context, id string) (db.Library, error) {
	libs, err := s.db.AllLibraries(ctx)
	if err != nil {
		return db.Library{}, err
	}
	for _, l := range libs {
		if l.PublicID == id && (l.Kind == db.LibraryMovies || l.Kind == db.LibraryShows || l.Kind == db.LibraryAnime) {
			return l, nil
		}
	}
	return db.Library{}, db.ErrNotFound
}
func (s *Server) queueSegmentLibrary(ctx context.Context, id string) error {
	lib, err := s.segmentLibrary(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	mode := s.segments.Mode(lib)
	if mode == "off" {
		return nil
	}
	c, _, err := s.db.SegmentCounts(ctx, id, "", s.segments.MeasurementMode(lib))
	if err != nil || c.Pending == 0 {
		return err
	}
	_, err = s.jobs.Start(ctx, segments.JobKind, jobs.Request{Target: jobs.Target{Library: id}})
	return err
}
func (s *Server) queuePendingSegments(ctx context.Context) error {
	libs, err := s.db.AllLibraries(ctx)
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if err := s.queueSegmentLibrary(ctx, lib.PublicID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) segmentStatus(r *http.Request, _ None) (SegmentDetectionStatus, error) {
	out := SegmentDetectionStatus{Mode: settings.Segments.Get(s.settings).Mode, Libraries: []SegmentLibraryStatus{}, Failures: []SegmentReadFailure{}, CatchUpComplete: true}
	libs, err := s.db.AllLibraries(r.Context())
	if err != nil {
		return out, err
	}
	for _, lib := range libs {
		if lib.Kind != db.LibraryMovies && lib.Kind != db.LibraryShows && lib.Kind != db.LibraryAnime {
			continue
		}
		mode := s.segments.Mode(lib)
		measuredMode := s.segments.MeasurementMode(lib)
		c, _, err := s.db.SegmentCounts(r.Context(), lib.PublicID, "", measuredMode)
		if err != nil {
			return out, err
		}
		choice := lib.Options.SegmentDetection
		if choice == "" {
			choice = "server"
		}
		kind := "episode"
		if lib.Kind == db.LibraryMovies {
			kind = "movie"
		}
		out.Libraries = append(out.Libraries, SegmentLibraryStatus{LibraryID: lib.PublicID, Name: lib.Name, ItemKind: kind, Choice: choice, Mode: mode, Files: segmentFiles(c)})
		out.Files.Pending += c.Pending
		out.Files.Complete += c.Complete
		out.Files.Failed += c.Failed
		out.Files.Unsupported += c.Unsupported
		n, err := s.db.SegmentWaitingShows(r.Context(), lib.PublicID, measuredMode)
		if err != nil {
			return out, err
		}
		out.ShowsWaiting += n
		failed, err := s.db.SegmentFailures(r.Context(), lib.PublicID)
		if err != nil {
			return out, err
		}
		for _, e := range failed {
			if e.Mode != measuredMode || e.SourceKey != e.Key() {
				continue
			}
			out.Failures = append(out.Failures, SegmentReadFailure{ItemID: e.ItemID, Title: e.Title, State: e.State, ErrorCode: e.Problem, UpdatedMS: e.UpdatedAt})
		}
		if job, err := s.db.JobFor(r.Context(), segments.JobKind, lib.PublicID); err == nil && job.State == db.JobDone && job.FinishedAt > 0 {
			if out.LastFullPassMS == 0 || job.FinishedAt < out.LastFullPassMS {
				out.LastFullPassMS = job.FinishedAt
			}
		}
	}
	out.CatchUpComplete = out.Files.Pending == 0
	if !out.CatchUpComplete {
		out.LastFullPassMS = 0
	}
	sort.Slice(out.Failures, func(i, j int) bool { return out.Failures[i].UpdatedMS > out.Failures[j].UpdatedMS })
	if len(out.Failures) > 32 {
		out.Failures = out.Failures[:32]
	}
	return out, nil
}
func segmentCursor(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, ErrField("cursor", "Read the list again.")
	}
	return n, nil
}
func (s *Server) segmentTitles(r *http.Request, _ None) (SegmentTitlePage, error) {
	out := SegmentTitlePage{LibraryID: r.URL.Query().Get("libraryId"), Titles: []SegmentTitleSummary{}}
	lib, err := s.segmentLibrary(r.Context(), out.LibraryID)
	if err != nil {
		return out, err
	}
	after, err := segmentCursor(r)
	if err != nil {
		return out, err
	}
	titles, err := s.db.SegmentTitles(r.Context(), lib.PublicID, after, 51)
	if err != nil {
		return out, err
	}
	if len(titles) > 50 {
		titles = titles[:50]
		out.NextCursor = fmt.Sprint(titles[49].RowID)
	}
	for _, t := range titles {
		c, m, err := s.db.SegmentCounts(r.Context(), lib.PublicID, t.ID, s.segments.MeasurementMode(lib))
		if err != nil {
			return out, err
		}
		out.Titles = append(out.Titles, SegmentTitleSummary{t.ID, t.Title, t.Kind.String(), segmentFiles(c), segmentCounts(m)})
	}
	return out, nil
}
func (s *Server) segmentTitle(r *http.Request, _ None) (SegmentTitleReview, error) {
	title, err := s.db.SegmentTitle(r.Context(), r.PathValue("id"))
	if err != nil {
		return SegmentTitleReview{}, err
	}
	lib, err := s.segmentLibrary(r.Context(), title.LibraryID)
	if err != nil {
		return SegmentTitleReview{}, err
	}
	after, err := segmentCursor(r)
	if err != nil {
		return SegmentTitleReview{}, err
	}
	out := SegmentTitleReview{ID: title.ID, Title: title.Title, Kind: title.Kind.String(), LibraryID: lib.PublicID, Mode: s.segments.Mode(lib), Seasons: []SegmentReviewSeason{}}
	entries, err := s.db.SegmentEntries(r.Context(), lib.PublicID, title.ID, after, 101)
	if err != nil {
		return out, err
	}
	if len(entries) > 100 {
		entries = entries[:100]
		out.NextCursor = fmt.Sprint(entries[99].MediaID)
	}
	groups := map[int]int{}
	for _, e := range entries {
		index, ok := groups[e.Season]
		if !ok {
			index = len(out.Seasons)
			groups[e.Season] = index
			out.Seasons = append(out.Seasons, SegmentReviewSeason{Number: e.Season, Title: "", Entries: []SegmentReviewEntry{}})
		}
		state := e.State
		if e.SourceKey != e.Key() || e.Mode != s.segments.MeasurementMode(lib) {
			state = "pending"
		}
		if out.Mode == "off" {
			state = "off"
		}
		row := SegmentReviewEntry{ItemID: e.ItemID, Title: e.Title, Number: e.Number, DurationUS: fmt.Sprint(e.DurationMS * 1000), State: state, ErrorCode: e.Problem,
			SourceID: e.VersionID, SourceRevision: segmentRevision(e), MappingRevision: e.VersionID, MarkerRevision: e.Revision, Markers: []SegmentReviewMarker{}, VersionName: e.VersionName}
		markers, err := s.db.SegmentMarkers(r.Context(), e.MediaID)
		if err != nil {
			return out, err
		}
		for _, m := range markers {
			row.Markers = append(row.Markers, segmentMarker(m, settings.Segments.Get(s.settings).MarkerPolicy(), out.Mode != "off"))
		}
		out.Seasons[index].Entries = append(out.Seasons[index].Entries, row)
	}
	sort.Slice(out.Seasons, func(i, j int) bool { return out.Seasons[i].Number < out.Seasons[j].Number })
	return out, nil
}
func (s *Server) editSegmentMarker(r *http.Request, req SegmentMarkerEdit) (None, error) {
	if req.RequestID == "" || len(req.RequestID) > 128 {
		return None{}, ErrField("requestId", "Send an identifier for this change.")
	}
	e, err := s.db.SegmentVersion(r.Context(), r.PathValue("id"), req.SourceID)
	if err != nil {
		return None{}, err
	}
	if req.SourceRevision != segmentRevision(e) {
		return None{}, errStale()
	}
	marker := db.SegmentMarker{}
	marker.ID, _ = strconv.ParseInt(req.MarkerID, 10, 64)
	if req.Action == "create" || req.Action == "edit" {
		kind, ok := segmentKind(req.Kind)
		if !ok {
			return None{}, ErrField("kind", "Choose an intro, recap, credits, commercial or outro.")
		}
		start, err := strconv.ParseInt(req.StartUS, 10, 64)
		if err != nil || start < 0 {
			return None{}, ErrField("startUS", "Give the start in microseconds.")
		}
		end, err := strconv.ParseInt(req.EndUS, 10, 64)
		if err != nil || end <= start || end > e.DurationMS*1000 {
			return None{}, ErrField("endUS", "The end must follow the start and be inside the title.")
		}
		marker.Kind, marker.StartMS, marker.EndMS = kind, start/1000, (end+999)/1000
	}
	raw, _ := json.Marshal(req)
	err = s.db.ChangeSegmentMarker(r.Context(), db.SegmentChange{Entry: e, SourceKey: e.Key(), Revision: req.ExpectedRevision, UserID: CallerFrom(r.Context()).UserID,
		RequestID: "segment:" + req.RequestID, RequestHash: fmt.Sprintf("%x", sha256.Sum256(raw)), Action: req.Action, Marker: marker})
	if errors.Is(err, db.ErrConflict) {
		return None{}, errStale()
	}
	if errors.Is(err, db.ErrKeyReused) {
		return None{}, errKeyReused()
	}
	return None{}, err
}
func (s *Server) redetectSegments(r *http.Request, _ None) (SegmentChanged, error) {
	title, err := s.db.SegmentTitle(r.Context(), r.PathValue("id"))
	if err != nil {
		return SegmentChanged{}, err
	}
	lib, err := s.segmentLibrary(r.Context(), title.LibraryID)
	if err != nil {
		return SegmentChanged{}, err
	}
	if s.segments.Mode(lib) == "off" {
		return SegmentChanged{}, ErrInvalid("Turn on intro and credits detection first.")
	}
	n, err := s.db.RedetectSegments(r.Context(), title.LibraryID, title.ID)
	if err != nil {
		return SegmentChanged{}, err
	}
	if err = s.queueSegmentLibrary(r.Context(), title.LibraryID); err != nil {
		return SegmentChanged{}, err
	}
	return SegmentChanged{n}, nil
}
func (s *Server) approveSegments(r *http.Request, req SegmentApprove) (SegmentChanged, error) {
	title, err := s.db.SegmentTitle(r.Context(), r.PathValue("id"))
	if err != nil {
		return SegmentChanged{}, err
	}
	kinds := map[db.MarkerKind]bool{}
	for _, name := range req.Kinds {
		kind, ok := segmentKind(name)
		if !ok {
			return SegmentChanged{}, ErrField("kinds", "Choose the marker kinds to approve.")
		}
		kinds[kind] = true
	}
	if len(kinds) == 0 {
		return SegmentChanged{}, ErrField("kinds", "Choose the marker kinds to approve.")
	}
	var after int64
	out := SegmentChanged{}
	for {
		entries, err := s.db.SegmentEntries(r.Context(), title.LibraryID, title.ID, after, 100)
		if err != nil {
			return out, err
		}
		if len(entries) == 0 {
			return out, nil
		}
		for _, e := range entries {
			after = e.MediaID
			if req.Season != nil && e.Season != *req.Season {
				continue
			}
			markers, err := s.db.SegmentMarkers(r.Context(), e.MediaID)
			if err != nil {
				return out, err
			}
			for _, m := range markers {
				if !kinds[m.Kind] || m.Dismissed || m.Source == db.MarkerFromPerson {
					continue
				}
				err = s.db.ChangeSegmentMarker(r.Context(), db.SegmentChange{Entry: e, SourceKey: e.Key(), Revision: e.Revision, Action: "approve", Marker: m})
				if errors.Is(err, db.ErrConflict) {
					return out, errStale()
				}
				if err != nil {
					return out, err
				}
				out.Changed++
				e.Revision++
			}
		}
	}
}
