package api

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/reading"
)

// readingArea is what reading (area 10) keeps on the server. Add this area's fields here and its start-up
// to startReading, in this file, so that building the area never edits server.go.
type readingArea struct {
	// reading opens books. See reading.go.
	reading *reading.Service
	// bookText is the index "search all books" reads (reading_search.go); nil while its file
	// cannot be opened, when search answers that it is unavailable and each pass tries again.
	bookText atomic.Pointer[reading.BookText]
}

// startReading builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startReading(ctx context.Context) error {
	// Book addresses are signed with a key the server keeps, so they work across a restart.
	bookKey, err := s.db.SettingOrCreate(ctx, "reading.address_key", auth.NewKey)
	if err != nil {
		return err
	}
	// A book on remote storage is read through its source, as playback reads a film there.
	s.reading = reading.New(s.db, bookKey, func() time.Time { return s.now() }, s.remote.Open)
	s.reading.FromHome = s.fromHome
	s.reading.Copies = filepath.Join(s.dataDir, "download-copies")
	s.readingRoutes()
	openBookText := func() *reading.BookText {
		// The file is derived and rebuilt at will, so one that cannot be opened (a full disk,
		// say) never stops the server: search waits for it.
		t, err := reading.OpenBookText(filepath.Join(s.dataDir, "booktext.db"), s.reading, s.log)
		if err != nil {
			s.log.Error("book text index cannot be opened; search all books waits for it", "err", err)
			return nil
		}
		s.bookText.Store(t)
		return t
	}
	openBookText()
	s.readingSearchRoutes()
	// Each pass reads new and changed books for under a minute, so a large library is read
	// over many passes and other housekeeping, which runs one piece at a time, waits little.
	s.jobs.Tidy("book text", 2*time.Minute, func(ctx context.Context) error {
		index := s.bookText.Load()
		if index == nil {
			if index = openBookText(); index == nil {
				return nil
			}
		}
		files, err := s.db.BookTextFiles(ctx)
		if err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if err := index.Update(bounded, files); err != nil && bounded.Err() == nil {
			return err
		}
		return nil
	})
	s.jobs.Tidy("deleted annotations", 24*time.Hour, func(ctx context.Context) error {
		return s.db.RemoveOldAnnotationMarkers(ctx, s.now())
	})
	return nil
}
