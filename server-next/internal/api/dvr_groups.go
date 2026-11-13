package api

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// DvrGroup is one recorded programme title with its counts and newest recording.
type DvrGroup struct {
	Key        string       `json:"key"`
	Title      string       `json:"title"`
	Recordings int          `json:"recordings"`
	Unwatched  int          `json:"unwatched"`
	Kept       int          `json:"kept"`
	Exceptions int          `json:"exceptions"`
	Latest     time.Time    `json:"latest"`
	Recording  DvrRecording `json:"recording"`
}

func (s *Server) dvrGroupRoutes() {
	Handle(s, "GET /v1/dvr/groups", WithProfile, s.listRecordingGroups)
}

func (s *Server) recordingReadScope(r *http.Request, lv db.LiveViewer) (db.RecordingReadScope, error) {
	scope := db.RecordingReadScope{Viewer: viewer(r), Live: lv, Everyone: r.URL.Query().Get("scope") == "everyone", SeeOthers: lv.Owner}
	scope.AllRules = scope.Everyone && CallerFrom(r.Context()).Role == "owner"
	names, err := s.db.ProfileNames(r.Context(), scope.Viewer.ProfileID)
	if err != nil {
		return scope, err
	}
	scope.ProfilePub = names.PublicID
	if scope.Everyone && !scope.SeeOthers {
		lib, err := s.recorder.Library(r.Context())
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return scope, err
		}
		if err == nil {
			_, err = s.db.Library(r.Context(), scope.Viewer, lib.PublicID)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return scope, err
			}
			scope.SeeOthers = err == nil
		}
	}
	return scope, nil
}

func (s *Server) listRecordingGroups(r *http.Request, _ None) (Page[DvrGroup], error) {
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return Page[DvrGroup]{}, err
	}
	scope, err := s.recordingReadScope(r, lv)
	if err != nil {
		return Page[DvrGroup]{}, err
	}
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "recent"
	}
	if !slices.Contains([]string{"recent", "title", "oldest"}, sort) {
		return Page[DvrGroup]{}, ErrField("sort", "Choose recent, title or oldest.")
	}
	q := db.RecordingGroupQuery{Sort: sort, Limit: 51}
	var cursor struct {
		Title  string `json:"t"`
		Latest int64  `json:"a"`
	}
	if text := r.URL.Query().Get("cursor"); text != "" {
		if decodeOpaque(text, &cursor) != nil {
			return Page[DvrGroup]{}, ErrField("cursor", "This cursor is not one this list gave.")
		}
		q.AfterTitle, q.AfterLatest = &cursor.Title, cursor.Latest
	}
	groups, err := s.db.RecordingGroups(r.Context(), scope, q)
	if err != nil {
		return Page[DvrGroup]{}, err
	}
	more := len(groups) > 50
	if more {
		groups = groups[:50]
	}
	rows := make([]db.Recording, len(groups))
	for i, g := range groups {
		rows[i] = g.Recording
	}
	items, err := s.itemIDs(r.Context(), viewer(r), rows)
	if err != nil {
		return Page[DvrGroup]{}, err
	}
	out := Page[DvrGroup]{Items: []DvrGroup{}}
	for _, g := range groups {
		recording := s.withMay(r, s.recordingOut(r.Context(), g.Recording, false, items), g.Recording, scope.ProfilePub)
		out.Items = append(out.Items, DvrGroup{Key: g.Title, Title: g.Title, Recordings: g.Recordings, Unwatched: g.Unwatched, Kept: g.Kept,
			Exceptions: g.Exceptions, Latest: g.Latest, Recording: recording})
	}
	if more {
		last := groups[len(groups)-1]
		out.NextCursor = encodeOpaque(map[string]any{"t": last.Title, "a": last.Latest.UnixMilli()})
	}
	return out, nil
}
