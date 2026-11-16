package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/segments"
)

// makeEpisodePictures is the second half of Episode thumbnails' default, "Provider, then made"
// (definition area 4, rule 46): after the picture pass has fetched the provider's stills, an
// episode with no picture at all gets one made from its video, a single frame a tenth of the
// way in. A provider's still or a picture beside the file that comes later is shown before it.
// A file no frame could be made from is not tried again until it is replaced. It runs for a
// show or anime library whose owner has not chosen Provider only or Off.
func (s *Server) makeEpisodePictures(ctx context.Context, lib db.Library) error {
	if lib.Kind != db.LibraryShows && lib.Kind != db.LibraryAnime || lib.Options.EpisodeThumbnails == db.EpisodeThumbnailsProvider ||
		lib.Options.EpisodeThumbnails == db.EpisodeThumbnailsOff {
		return nil
	}
	var after int64
	for {
		entries, err := s.db.EpisodesWithoutPicture(ctx, lib.PublicID, after)
		if err != nil || len(entries) == 0 {
			return err
		}
		for _, e := range entries {
			after = e.MediaID
			if err := s.settings.WaitForBackground(ctx, s.now); err != nil {
				return err
			}
			data, err := s.madeFrame(ctx, e)
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case errors.Is(err, errNoDecoder):
				return nil // no decoder on this server: nothing can be made
			case errors.Is(err, errOutOfReach):
				continue // changed or out of reach now: the next pass tries again
			}
			hash := ""
			if err == nil {
				sum := sha256.Sum256(data)
				hash = hex.EncodeToString(sum[:])
				if err := s.artwork.Put(hash, data); errors.Is(err, artwork.ErrNotAPicture) {
					hash = ""
				} else if err != nil {
					return err
				}
			}
			if err := s.db.AddMadePicture(ctx, e.ItemID, e.FileID, hash); err != nil {
				return err
			}
		}
	}
}

var (
	errNoDecoder  = errors.New("this server has no decoder to make a picture with")
	errOutOfReach = errors.New("the episode's file cannot be read now")
)

// madeFrame makes one picture from an episode's video, a frame a tenth of the way in: for the
// picture pass and for Make from the video. A file that changed or is out of reach is
// errOutOfReach; a server with no decoder, errNoDecoder; a version of several files or a part
// of one, mediaanalysis.ErrUnsupported.
func (s *Server) madeFrame(ctx context.Context, e db.SegmentEntry) ([]byte, error) {
	supervisor, ffmpeg, err := s.analysisTools(ctx)
	if err != nil || supervisor == nil || ffmpeg == "" {
		return nil, errors.Join(errNoDecoder, err)
	}
	if e.Parts != 1 || e.StartMS != 0 || e.EndMS != 0 || e.DurationMS <= 0 {
		return nil, mediaanalysis.ErrUnsupported
	}
	file, err := segments.OpenFile(ctx, s.remote.Open, e)
	if err != nil {
		return nil, errors.Join(errOutOfReach, err)
	}
	defer file.Close()
	made, err := mediaanalysis.MakeEpisodeThumbnail(ctx, mediaanalysis.FileDecode(supervisor, ffmpeg, nil, file, e.Size),
		mediaanalysis.EpisodeThumbnailRequest{Mode: mediaanalysis.EpisodeThumbnailDefaultMode, Manual: true, DurationUS: e.DurationMS * 1000,
			EndUS: e.DurationMS * 1000, SourceVideo: decoder.PreviewVideoFacts{DynamicRange: e.HDR}})
	return made.JPEG, err
}
