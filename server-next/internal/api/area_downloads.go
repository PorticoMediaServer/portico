package api

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/downloads"
	"github.com/PorticoMediaServer/portico/server-next/internal/prepare"
	"github.com/PorticoMediaServer/portico/server-next/internal/reading"
)

// downloadsArea is what downloads and offline (area 9) keeps on the server. Add this area's fields here and its start-up
// to startDownloads, in this file, so that building the area never edits server.go.
type downloadsArea struct {
	// prepare makes the smaller copies people download. See package prepare.
	prepare *prepare.Service
	// downloads answers what profiles ask to have on their devices. See downloads.go.
	downloads *downloads.Service
}

// startDownloads builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startDownloads(ctx context.Context) error {
	// The copies made for downloads: made in the background, and swept when nobody wants them.
	var err error
	if s.prepare, err = prepare.New(prepare.Options{DB: s.db, Folder: filepath.Join(s.dataDir, "prepared"), Settings: s.settings,
		Remote: s.remote.Open, Log: s.log, Now: func() time.Time { return s.now() }}); err != nil {
		return err
	}
	s.goBackground(func() { s.prepare.Run(ctx) })
	s.jobs.Tidy("unused prepared copies", 6*time.Hour, s.prepare.Tidy)
	s.downloads = &downloads.Service{DB: s.db, Prepare: s.prepare, Settings: s.settings, Now: func() time.Time { return s.now() },
		SubtitleDir: filepath.Join(s.dataDir, "subtitles"),
		BookCopy: func(ctx context.Context, v db.Viewer, item string, fileID int64) (reading.Copy, bool, error) {
			version, ok, err := s.bookVersion(ctx, v, item, fileID)
			if !ok {
				return reading.Copy{}, false, err
			}
			return s.reading.DownloadCopy(ctx, version)
		},
		DropBookCopy: func(ctx context.Context, v db.Viewer, item string, fileID int64) {
			if version, ok, _ := s.bookVersion(ctx, v, item, fileID); ok {
				s.reading.DropCopy(version)
			}
		}}
	// A book's download copy lasts 48 hours at most (area 9 rule 24).
	s.jobs.Tidy("download copies of books", time.Hour, func(ctx context.Context) error { return s.reading.PruneCopies(ctx) })
	s.downloadRoutes()
	return nil
}

// bookVersion is the version of a book, comic or issue whose first file is fileID; ok is false
// for anything that is not one.
func (s *Server) bookVersion(ctx context.Context, v db.Viewer, item string, fileID int64) (db.BookVersion, bool, error) {
	book, err := s.db.Book(ctx, v, item)
	if errors.Is(err, db.ErrNotFound) {
		return db.BookVersion{}, false, nil
	}
	if err != nil {
		return db.BookVersion{}, false, err
	}
	for _, version := range book.Versions {
		if version.FileID == fileID {
			return version, true, nil
		}
	}
	return db.BookVersion{}, false, nil
}
