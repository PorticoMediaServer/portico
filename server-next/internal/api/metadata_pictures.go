package api

import (
	"context"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// jobPictures is a library's picture pass: it fetches the provider's pictures that its lookups
// and refreshes recorded, so a title's details never wait for its pictures. It is started by the
// server after every lookup and refresh, and joins one already waiting or running.
const jobPictures = "metadata_pictures"

func (s *Server) picturePass() {
	s.jobs.Register(jobs.Kind{Name: jobPictures, Access: jobs.Internal, Run: func(ctx context.Context, work jobs.Work) error {
		id, err := s.db.LibraryID(ctx, work.Target.Library)
		if errors.Is(err, db.ErrNotFound) {
			return nil // removed since
		}
		if err != nil {
			return err
		}
		_, err = s.metadata.FetchPictures(ctx, id, math.MaxInt)
		return err
	}})
}

// fetchPicturesLater starts a library's picture pass, for the pictures a lookup or a refresh
// just recorded.
func (s *Server) fetchPicturesLater(ctx context.Context, library string) {
	if _, err := s.jobs.Start(ctx, jobPictures, jobs.Request{Target: jobs.Target{Library: library}}); err != nil && ctx.Err() == nil {
		s.log.Error("a library's picture pass could not be started", "library", library, "error", err)
	}
}
