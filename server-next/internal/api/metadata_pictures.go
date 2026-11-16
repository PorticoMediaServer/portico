package api

import (
	"cmp"
	"context"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// jobPictures is a library's picture pass: it fetches the provider's pictures that its lookups
// and refreshes recorded, so a title's details never wait for its pictures, then makes a
// picture for each episode that has none (makeEpisodePictures). It is started by the
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
		if errors.Is(err, metadata.ErrPictureHostDown) {
			err = jobs.DoneWith{Note: "The provider's picture server did not answer. The pictures not fetched are fetched by a later pass, after the next lookup or refresh."}
		}
		if ctx.Err() != nil || err != nil && !errors.As(err, new(jobs.DoneWith)) {
			return err
		}
		// Then a picture made from the video for each episode still without one. An episode
		// whose still is waiting for the provider's picture server waits for it.
		all, listErr := s.db.AllLibraries(ctx)
		for _, lib := range all {
			if lib.ID == id && listErr == nil {
				listErr = s.makeEpisodePictures(ctx, lib)
			}
		}
		return cmp.Or(listErr, err)
	}})
}

// fetchPicturesLater starts a library's picture pass, for the pictures a lookup or a refresh
// just recorded.
func (s *Server) fetchPicturesLater(ctx context.Context, library string) {
	if _, err := s.jobs.Start(ctx, jobPictures, jobs.Request{Target: jobs.Target{Library: library}}); err != nil && ctx.Err() == nil {
		s.log.Error("a library's picture pass could not be started", "library", library, "error", err)
	}
}
